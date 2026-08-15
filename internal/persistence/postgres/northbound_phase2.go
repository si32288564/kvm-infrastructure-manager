package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/phase2"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

type NorthboundPhase2Store struct{ DB TxBeginner }

func (s NorthboundPhase2Store) Create(ctx context.Context, p resource.Principal, k phase2.Kind, r phase2.CreateRequest, id, digest string) (out phase2.Resource, replay bool, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return resource.ErrServiceUnavailable
		}
		lock := fmt.Sprintf("northbound-phase2/%s/%s/%s/%s/%s", p.Issuer, p.Subject, r.Desired.ProjectID, k, r.IdempotencyKey)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "CREATE", r.Desired.ProjectID)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, r.RequestID, string(k)+"_CREATE", string(k), "", 0, "PROJECT", r.Desired.ProjectID, "DENIED", "FORBIDDEN", digest)
		}
		var priorDigest, priorID string
		var priorRevision uint64
		err = tx.QueryRow(ctx, `SELECT request_digest,resource_id,resource_revision FROM kim.northbound_phase2_idempotency_evidence WHERE principal_issuer=$1 AND principal_subject=$2 AND parent_project_id=$3 AND resource_type=$4 AND canonical_path=$5 AND idempotency_key=$6`, p.Issuer, p.Subject, r.Desired.ProjectID, k, r.CanonicalPath, r.IdempotencyKey).Scan(&priorDigest, &priorID, &priorRevision)
		if err == nil {
			if priorDigest != digest {
				returned = resource.ErrIdempotencyConflict
				return auditTx(ctx, tx, p, r.RequestID, string(k)+"_CREATE", string(k), priorID, priorRevision, "PROJECT", r.Desired.ProjectID, "DENIED", "IDEMPOTENCY_CONFLICT", digest)
			}
			out, err = loadPhase2(ctx, scopeTxBeginner{tx}, k, priorID)
			if err != nil || out.Revision != priorRevision {
				return resource.ErrConflict
			}
			replay = true
			return auditTx(ctx, tx, p, r.RequestID, string(k)+"_CREATE", string(k), priorID, priorRevision, "PROJECT", r.Desired.ProjectID, "SUCCEEDED", "IDEMPOTENT_REPLAY", digest)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		out, err = createPhase2(ctx, scopeTxBeginner{tx}, k, id, r.Desired)
		if err != nil {
			return mapPhase2Error(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO kim.northbound_phase2_idempotency_evidence(principal_issuer,principal_subject,parent_project_id,resource_type,http_method,canonical_path,idempotency_key,request_digest,resource_id,resource_revision,operation_id,response_status,request_id) VALUES($1,$2,$3,$4,'POST',$5,$6,$7,$8,$9,$10,201,$11)`, p.Issuer, p.Subject, r.Desired.ProjectID, k, r.CanonicalPath, r.IdempotencyKey, digest, id, out.Revision, out.OperationID, r.RequestID); err != nil {
			return err
		}
		return auditTx(ctx, tx, p, r.RequestID, string(k)+"_CREATE", string(k), id, out.Revision, "PROJECT", r.Desired.ProjectID, "SUCCEEDED", "CREATED", digest)
	})
	if err != nil {
		return out, false, fmt.Errorf("create %s authority: %w", k, err)
	}
	return out, replay, returned
}

func (s NorthboundPhase2Store) Get(ctx context.Context, p resource.Principal, k phase2.Kind, id, requestID string) (out phase2.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		owner, err := phase2Owner(ctx, tx, k, id)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "READ", owner)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, string(k)+"_READ", string(k), id, 0, "PROJECT", owner, "DENIED", "FORBIDDEN", "")
		}
		out, err = loadPhase2(ctx, scopeTxBeginner{tx}, k, id)
		if err != nil {
			return err
		}
		if out.LifecycleState == "DELETED" || out.LifecycleState == "RELEASED" || out.LifecycleState == "RETIRED" {
			returned = resource.ErrNotFound
			return nil
		}
		return auditTx(ctx, tx, p, requestID, string(k)+"_READ", string(k), id, out.Revision, "PROJECT", owner, "SUCCEEDED", "READ", "")
	})
	if err != nil {
		return out, fmt.Errorf("read %s authority: %w", k, err)
	}
	return out, returned
}

func (s NorthboundPhase2Store) List(ctx context.Context, p resource.Principal, k phase2.Kind, r phase2.ListRequest, requestID string) (page phase2.Page, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		table, idcol, projectcol, statecol, sourcecol := phase2Table(k)
		q := fmt.Sprintf(`SELECT x.%s FROM kim.%s x WHERE x.%s IN ('ACTIVE','CREATING') AND x.%s>$3 AND ($4='' OR x.%s=$4) AND x.%s=$5 AND (EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='SYSTEM' AND b.role IN('READER','WRITER','ADMIN')) OR EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='PROJECT' AND b.scope_id=x.%s AND b.role IN('READER','WRITER','ADMIN'))) ORDER BY x.%s LIMIT $6`, idcol, table, statecol, idcol, projectcol, sourcecol, projectcol, idcol)
		rows, err := tx.Query(ctx, q, p.Issuer, p.Subject, r.AfterID, r.ProjectID, phase2Source(k), r.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			v, err := loadPhase2(ctx, scopeTxBeginner{tx}, k, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, v)
		}
		if len(page.Items) > r.Limit {
			page.NextAfter = page.Items[r.Limit-1].ID
			page.Items = page.Items[:r.Limit]
		}
		scope := "SYSTEM"
		if r.ProjectID != "" {
			scope = "PROJECT"
		}
		return auditTx(ctx, tx, p, requestID, string(k)+"_LIST", string(k), "", 0, scope, r.ProjectID, "SUCCEEDED", "LISTED", "")
	})
	if err != nil {
		return page, fmt.Errorf("list %s authority: %w", k, err)
	}
	return page, returned
}

func (s NorthboundPhase2Store) Patch(ctx context.Context, p resource.Principal, k phase2.Kind, id string, expected uint64, patch phase2.Patch, requestID string) (out phase2.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		current, err := loadPhase2(ctx, scopeTxBeginner{tx}, k, id)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "UPDATE", current.ProjectID)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, string(k)+"_UPDATE", string(k), id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "FORBIDDEN", "")
		}
		if current.Revision != expected {
			returned = resource.ErrStaleRevision
			return auditTx(ctx, tx, p, requestID, string(k)+"_UPDATE", string(k), id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "STALE_RESOURCE_REVISION", "")
		}
		desired := current.Desired
		if patch.Name != nil {
			desired.Name = *patch.Name
		}
		if patch.DeleteProtection != nil {
			desired.DeleteProtection = *patch.DeleteProtection
		}
		if patch.MTU != nil {
			desired.MTU = *patch.MTU
		}
		if desired.Name == current.Name && desired.DeleteProtection == current.DeleteProtection && desired.MTU == current.MTU {
			out = current
			return auditTx(ctx, tx, p, requestID, string(k)+"_UPDATE", string(k), id, current.Revision, "PROJECT", current.ProjectID, "SUCCEEDED", "UNCHANGED", "")
		}
		out, err = updatePhase2(ctx, scopeTxBeginner{tx}, k, id, desired, expected)
		if err != nil {
			return mapPhase2Error(err)
		}
		return auditTx(ctx, tx, p, requestID, string(k)+"_UPDATE", string(k), id, out.Revision, "PROJECT", current.ProjectID, "SUCCEEDED", "UPDATED", "")
	})
	if err != nil {
		return out, fmt.Errorf("update %s authority: %w", k, err)
	}
	return out, returned
}

func (s NorthboundPhase2Store) Delete(ctx context.Context, p resource.Principal, k phase2.Kind, id string, expected uint64, requestID string) (out phase2.Operation, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		current, err := loadPhase2(ctx, scopeTxBeginner{tx}, k, id)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "DELETE", current.ProjectID)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, string(k)+"_DELETE", string(k), id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "FORBIDDEN", "")
		}
		if current.Revision != expected {
			returned = resource.ErrStaleRevision
			return auditTx(ctx, tx, p, requestID, string(k)+"_DELETE", string(k), id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "STALE_RESOURCE_REVISION", "")
		}
		if current.DeleteProtection {
			returned = resource.ErrDeleteProtected
			return auditTx(ctx, tx, p, requestID, string(k)+"_DELETE", string(k), id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "DELETE_PROTECTED", "")
		}
		retired, err := retirePhase2(ctx, scopeTxBeginner{tx}, k, id, expected)
		if err != nil {
			if errors.Is(err, ErrPlacementConflict) {
				returned = resource.ErrDependencyConflict
				return auditTx(ctx, tx, p, requestID, string(k)+"_DELETE", string(k), id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "DEPENDENCY_CONFLICT", "")
			}
			return mapPhase2Error(err)
		}
		out, err = loadPhase2Operation(ctx, tx, retired.OperationID)
		if err != nil {
			return err
		}
		return auditTx(ctx, tx, p, requestID, string(k)+"_DELETE", string(k), id, retired.Revision, "PROJECT", current.ProjectID, "SUCCEEDED", "RETIREMENT_ACCEPTED", "")
	})
	if err != nil {
		return out, fmt.Errorf("delete %s authority: %w", k, err)
	}
	return out, returned
}

func (s NorthboundPhase2Store) GetOperation(ctx context.Context, p resource.Principal, id, requestID string) (out phase2.Operation, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		out, err = loadPhase2Operation(ctx, tx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		owner, err := phase2Owner(ctx, tx, phase2.Kind(out.TargetResourceType), out.TargetResourceID)
		if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "READ", owner)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, "OPERATION_READ", "OPERATION", id, 0, "PROJECT", owner, "DENIED", "FORBIDDEN", "")
		}
		return auditTx(ctx, tx, p, requestID, "OPERATION_READ", "OPERATION", id, out.TargetRevision, "PROJECT", owner, "SUCCEEDED", "READ", "")
	})
	if err != nil {
		return out, fmt.Errorf("read Phase 2 Operation: %w", err)
	}
	return out, returned
}

func createPhase2(ctx context.Context, db TxBeginner, k phase2.Kind, id string, d phase2.Desired) (phase2.Resource, error) {
	switch k {
	case phase2.Network:
		v, e := CreateNetworkResource(ctx, db, NetworkResourceRequest{NetworkID: id, ProjectID: d.ProjectID, Name: d.Name, Profile: d.Profile, MTU: d.MTU, SegmentPolicy: d.SegmentPolicy, RequestedSegmentID: d.RequestedSegmentID, DeleteProtection: d.DeleteProtection})
		return networkPublic(v), e
	case phase2.Subnet:
		v, e := CreateSubnetResource(ctx, db, SubnetResourceRequest{SubnetID: id, ProjectID: d.ProjectID, NetworkID: d.NetworkID, Name: d.Name, IPFamily: d.IPFamily, CIDR: d.CIDR, GatewayPolicy: d.GatewayPolicy, GatewayAddress: d.GatewayAddress, AllocationPolicy: d.AllocationPolicy, AllocationStart: d.AllocationStart, AllocationEnd: d.AllocationEnd, ReservedAddresses: d.ReservedAddresses, DHCPEnabled: d.DHCPEnabled, DNSServers: d.DNSServers, DeleteProtection: d.DeleteProtection})
		return subnetPublic(v), e
	case phase2.Port:
		v, e := CreatePortResource(ctx, db, PortResourceRequest{PortID: id, ProjectID: d.ProjectID, NetworkID: d.NetworkID, Name: d.Name, MACPolicy: d.MACPolicy, RequestedMAC: d.RequestedMAC, SubnetID: d.SubnetID, IPAllocationMode: d.IPAllocationMode, RequestedIP: d.RequestedIP, AttachmentPolicy: d.AttachmentPolicy, DatapathProfile: d.DatapathProfile, DeleteProtection: d.DeleteProtection})
		return portPublic(v), e
	case phase2.Volume:
		v, e := CreateVolumeResource(ctx, db, VolumeResourceRequest{VolumeID: id, ProjectID: d.ProjectID, Name: d.Name, SizeBytes: d.SizeBytes, StorageClassID: d.StorageClassID, StorageClassRevision: d.StorageClassRevision, Bootable: d.Bootable, SourceType: d.SourceType, SourceImageID: d.SourceImageID, SourceImageRevision: d.SourceImageRevision, SourceArtifactDigest: d.SourceArtifactDigest, DeleteProtection: d.DeleteProtection})
		if e == nil {
			v, e = AllocateVolumeCapacityAutomatically(ctx, db, id, v.Revision)
		}
		return volumePublic(v), e
	}
	return phase2.Resource{}, resource.ErrValidation
}

func loadPhase2(ctx context.Context, db TxBeginner, k phase2.Kind, id string) (phase2.Resource, error) {
	switch k {
	case phase2.Network:
		v, e := GetNetworkResource(ctx, db, id)
		return networkPublic(v), e
	case phase2.Subnet:
		v, e := GetSubnetResource(ctx, db, id)
		return subnetPublic(v), e
	case phase2.Port:
		v, e := GetPortResource(ctx, db, id)
		return portPublic(v), e
	case phase2.Volume:
		v, e := GetVolumeResource(ctx, db, id)
		return volumePublic(v), e
	}
	return phase2.Resource{}, pgx.ErrNoRows
}

func updatePhase2(ctx context.Context, db TxBeginner, k phase2.Kind, id string, d phase2.Desired, expected uint64) (phase2.Resource, error) {
	switch k {
	case phase2.Network:
		v, e := UpdateNetworkResource(ctx, db, id, NetworkResourcePatch{ExpectedRevision: expected, Name: d.Name, MTU: d.MTU, DeleteProtection: d.DeleteProtection})
		return networkPublic(v), e
	case phase2.Subnet:
		v, e := UpdateSubnetResource(ctx, db, SubnetResourceRequest{SubnetID: id, ProjectID: d.ProjectID, NetworkID: d.NetworkID, Name: d.Name, IPFamily: d.IPFamily, CIDR: d.CIDR, GatewayPolicy: d.GatewayPolicy, GatewayAddress: d.GatewayAddress, AllocationPolicy: d.AllocationPolicy, AllocationStart: d.AllocationStart, AllocationEnd: d.AllocationEnd, ReservedAddresses: d.ReservedAddresses, DHCPEnabled: d.DHCPEnabled, DNSServers: d.DNSServers, DeleteProtection: d.DeleteProtection}, expected)
		return subnetPublic(v), e
	case phase2.Port:
		v, e := UpdatePortResource(ctx, db, PortResourceRequest{PortID: id, ProjectID: d.ProjectID, NetworkID: d.NetworkID, Name: d.Name, MACPolicy: d.MACPolicy, RequestedMAC: d.RequestedMAC, SubnetID: d.SubnetID, IPAllocationMode: d.IPAllocationMode, RequestedIP: d.RequestedIP, AttachmentPolicy: d.AttachmentPolicy, DatapathProfile: d.DatapathProfile, DeleteProtection: d.DeleteProtection}, expected)
		return portPublic(v), e
	case phase2.Volume:
		v, e := UpdateVolumeResource(ctx, db, VolumeResourceRequest{VolumeID: id, ProjectID: d.ProjectID, Name: d.Name, SizeBytes: d.SizeBytes, StorageClassID: d.StorageClassID, StorageClassRevision: d.StorageClassRevision, Bootable: d.Bootable, SourceType: d.SourceType, SourceImageID: d.SourceImageID, SourceImageRevision: d.SourceImageRevision, SourceArtifactDigest: d.SourceArtifactDigest, DeleteProtection: d.DeleteProtection}, expected)
		return volumePublic(v), e
	}
	return phase2.Resource{}, resource.ErrValidation
}

func retirePhase2(ctx context.Context, db TxBeginner, k phase2.Kind, id string, expected uint64) (phase2.Resource, error) {
	switch k {
	case phase2.Network:
		v, e := RequestNetworkRetirement(ctx, db, id, expected)
		return networkPublic(v), e
	case phase2.Subnet:
		v, e := RequestSubnetRetirement(ctx, db, id, expected)
		return subnetPublic(v), e
	case phase2.Port:
		v, e := RetirePortResource(ctx, db, id, expected)
		return portPublic(v), e
	case phase2.Volume:
		v, e := RequestVolumeRetirement(ctx, db, id, expected)
		return volumePublic(v), e
	}
	return phase2.Resource{}, resource.ErrValidation
}

func networkPublic(v NetworkResource) phase2.Resource {
	return phase2.Resource{ID: v.NetworkID, Desired: phase2.Desired{ProjectID: v.ProjectID, Name: v.Name, Profile: v.Profile, MTU: v.MTU, SegmentPolicy: v.SegmentPolicy, RequestedSegmentID: func() uint32 {
		if v.SegmentPolicy == "EXPLICIT" {
			return v.SegmentID
		}
		return 0
	}(), DeleteProtection: v.DeleteProtection}, Revision: v.Revision, LifecycleState: v.Lifecycle, RealizationState: v.RealizationState, OperationID: v.OperationID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func subnetPublic(v SubnetResource) phase2.Resource {
	reserved := make([]string, 0, len(v.ReservedAddresses))
	for _, address := range v.ReservedAddresses {
		if address != v.GatewayAddress {
			reserved = append(reserved, address)
		}
	}
	return phase2.Resource{ID: v.SubnetID, Desired: phase2.Desired{ProjectID: v.ProjectID, NetworkID: v.NetworkID, Name: v.Name, IPFamily: v.IPFamily, CIDR: v.CIDR, GatewayPolicy: v.GatewayPolicy, GatewayAddress: v.GatewayAddress, AllocationPolicy: v.AllocationPolicy, AllocationStart: v.AllocationStart, AllocationEnd: v.AllocationEnd, ReservedAddresses: reserved, DHCPEnabled: v.DHCPEnabled, DNSServers: v.DNSServers, DeleteProtection: v.DeleteProtection}, Revision: v.Revision, LifecycleState: v.Lifecycle, RealizationState: v.RealizationState, OperationID: v.OperationID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func portPublic(v PortResource) phase2.Resource {
	return phase2.Resource{ID: v.PortID, Desired: phase2.Desired{ProjectID: v.ProjectID, NetworkID: v.NetworkID, Name: v.Name, MACPolicy: v.MACPolicy, RequestedMAC: v.RequestedMAC, SubnetID: v.SubnetID, IPAllocationMode: v.IPAllocationMode, RequestedIP: v.RequestedIP, AttachmentPolicy: v.AttachmentPolicy, DatapathProfile: v.DatapathProfile, DeleteProtection: v.DeleteProtection}, Revision: v.Revision, LifecycleState: v.Lifecycle, RealizationState: v.RealizationState, OperationID: v.OperationID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func volumePublic(v VolumeResource) phase2.Resource {
	return phase2.Resource{ID: v.VolumeID, Desired: phase2.Desired{ProjectID: v.ProjectID, Name: v.Name, SizeBytes: v.SizeBytes, StorageClassID: v.StorageClassID, StorageClassRevision: v.StorageClassRevision, Bootable: v.Bootable, SourceType: v.SourceType, SourceImageID: v.SourceImageID, SourceImageRevision: v.SourceImageRevision, SourceArtifactDigest: v.SourceArtifactDigest, DeleteProtection: v.DeleteProtection}, Revision: v.Revision, LifecycleState: v.Lifecycle, RealizationState: v.MaterializationState, OperationID: v.OperationID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func phase2Table(k phase2.Kind) (string, string, string, string, string) {
	switch k {
	case phase2.Network:
		return "networks_current", "network_id", "project_id", "lifecycle_state", "authority_source"
	case phase2.Subnet:
		return "network_subnets_current", "subnet_id", "project_id", "lifecycle_state", "authority_source"
	case phase2.Port:
		return "network_ports_current", "port_id", "project_id", "desired_state", "authority_source"
	case phase2.Volume:
		return "volumes_current", "volume_id", "project_id", "lifecycle_state", "authority_source"
	}
	return "", "", "", "", ""
}
func phase2Source(k phase2.Kind) string {
	switch k {
	case phase2.Network:
		return "NETWORK_RESOURCE"
	case phase2.Subnet:
		return "SUBNET_RESOURCE"
	case phase2.Port:
		return "PORT_RESOURCE"
	case phase2.Volume:
		return "VOLUME_RESOURCE"
	}
	return ""
}
func phase2Owner(ctx context.Context, tx pgx.Tx, k phase2.Kind, id string) (string, error) {
	table, idcol, projectcol, _, sourcecol := phase2Table(k)
	q := fmt.Sprintf(`SELECT %s FROM kim.%s WHERE %s=$1 AND %s=$2`, projectcol, table, idcol, sourcecol)
	var owner string
	err := tx.QueryRow(ctx, q, id, phase2Source(k)).Scan(&owner)
	return owner, err
}
func mapPhase2Error(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return resource.ErrNotFound
	}
	if errors.Is(err, ErrPlacementConflict) {
		return resource.ErrConflict
	}
	return err
}

func loadPhase2Operation(ctx context.Context, tx pgx.Tx, id string) (phase2.Operation, error) {
	type shape struct {
		table, evidence, target, revision string
		kind                              phase2.Kind
	}
	for _, s := range []shape{
		{"network_realization_operations_current", "network_realization_operation_evidence", "network_id", "network_revision", phase2.Network},
		{"subnet_realization_operations_current", "subnet_realization_operation_evidence", "subnet_id", "subnet_revision", phase2.Subnet},
		{"port_realization_operations_current", "port_realization_operation_evidence", "port_id", "port_revision", phase2.Port},
		{"volume_materialization_operations_current", "volume_materialization_operation_evidence", "volume_id", "volume_revision", phase2.Volume},
	} {
		q := fmt.Sprintf(`SELECT o.operation_id,o.operation_kind,o.%s,o.%s,o.phase,e.accepted_at,o.terminal_evidence_id
			FROM kim.%s o
			JOIN kim.%s e ON e.operation_id=o.operation_id AND e.operation_generation=o.operation_generation
			WHERE o.operation_id=$1`, s.target, s.revision, s.table, s.evidence)
		var op phase2.Operation
		var internalPhase string
		var terminalID *string
		err := tx.QueryRow(ctx, q, id).Scan(&op.ID, &op.Type, &op.TargetResourceID, &op.TargetRevision, &internalPhase, &op.AcceptedAt, &terminalID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return op, err
		}
		op.TargetResourceType = string(s.kind)
		op.Phase = publicOperationPhase(internalPhase)
		if terminalID != nil {
			var state string
			var recorded time.Time
			terminalTable := map[phase2.Kind]string{phase2.Network: "network_realization_terminal_evidence", phase2.Subnet: "subnet_realization_terminal_evidence", phase2.Port: "port_realization_terminal_evidence", phase2.Volume: "volume_materialization_terminal_evidence"}[s.kind]
			if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT terminal_state,recorded_at FROM kim.%s WHERE terminal_evidence_id=$1`, terminalTable), *terminalID).Scan(&state, &recorded); err != nil {
				return op, err
			}
			op.TerminalState = &state
			op.CompletedAt = &recorded
		}
		return op, nil
	}
	return phase2.Operation{}, pgx.ErrNoRows
}
func publicOperationPhase(v string) string {
	switch v {
	case "PENDING":
		return "PENDING"
	case "CLAIMED":
		return "RUNNING"
	case "DISPATCH_UNKNOWN":
		return "UNKNOWN"
	case "VERIFYING":
		return "VERIFYING"
	case "SUCCEEDED":
		return "SUCCEEDED"
	case "FAILED":
		return "FAILED"
	}
	return "UNKNOWN"
}
