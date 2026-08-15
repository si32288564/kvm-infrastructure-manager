package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
	vmapi "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/vm"
)

type NorthboundVMStore struct{ DB TxBeginner }

func (s NorthboundVMStore) Create(ctx context.Context, p resource.Principal, r vmapi.CreateRequest, vmID, operationID, digest string) (out vmapi.Resource, replay bool, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return resource.ErrServiceUnavailable
		}
		lock := fmt.Sprintf("northbound-vm/%s/%s/%s/%s", p.Issuer, p.Subject, r.Desired.ProjectID, r.IdempotencyKey)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "CREATE", r.Desired.ProjectID)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, r.RequestID, "VM_CREATE", "VM", "", 0, "PROJECT", r.Desired.ProjectID, "DENIED", "FORBIDDEN", digest)
		}
		var priorDigest, priorVM, priorOperation string
		var priorRevision uint64
		err = tx.QueryRow(ctx, `SELECT request_digest,vm_id::text,vm_revision,operation_id FROM kim.northbound_vm_idempotency_evidence WHERE principal_issuer=$1 AND principal_subject=$2 AND parent_project_id=$3 AND canonical_path=$4 AND idempotency_key=$5`, p.Issuer, p.Subject, r.Desired.ProjectID, r.CanonicalPath, r.IdempotencyKey).Scan(&priorDigest, &priorVM, &priorRevision, &priorOperation)
		if err == nil {
			if priorDigest != digest {
				returned = resource.ErrIdempotencyConflict
				return auditTx(ctx, tx, p, r.RequestID, "VM_CREATE", "VM", priorVM, priorRevision, "PROJECT", r.Desired.ProjectID, "DENIED", "IDEMPOTENCY_CONFLICT", digest)
			}
			out, err = loadNorthboundVM(ctx, scopeTxBeginner{tx}, priorVM)
			if err != nil {
				return err
			}
			replay = true
			return auditTx(ctx, tx, p, r.RequestID, "VM_CREATE", "VM", priorVM, priorRevision, "PROJECT", r.Desired.ProjectID, "SUCCEEDED", "IDEMPOTENT_REPLAY", digest)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		ports := make([]VMAggregatePortRequest, len(r.Desired.Ports))
		for i, v := range r.Desired.Ports {
			ports[i] = VMAggregatePortRequest{PortID: v.ID, PortRevision: v.Revision}
		}
		volumes := make([]VMAggregateVolumeRequest, len(r.Desired.DataVolumes))
		for i, v := range r.Desired.DataVolumes {
			volumes[i] = VMAggregateVolumeRequest{VolumeID: v.ID, VolumeRevision: v.Revision}
		}
		_, err = CreateVMAggregate(ctx, scopeTxBeginner{tx}, VMAggregateCreateRequest{RequestID: r.RequestID, OperationID: operationID, VMID: vmID, ProjectID: r.Desired.ProjectID, Name: r.Desired.Name, FlavorID: r.Desired.FlavorID, FlavorRevision: r.Desired.FlavorRevision, ImageID: r.Desired.ImageID, ImageRevision: r.Desired.ImageRevision, AvailabilityPolicyID: r.Desired.AvailabilityPolicyID, AvailabilityPolicyRevision: r.Desired.AvailabilityPolicyRevision, PlacementScopeID: r.Desired.PlacementScopeID, PlacementScopeGeneration: r.Desired.PlacementScopeGeneration, RootVolumeID: r.Desired.RootVolume.ID, RootVolumeRevision: r.Desired.RootVolume.Revision, Ports: ports, DataVolumes: volumes, DesiredPowerState: r.Desired.DesiredPowerState, DeleteProtection: r.Desired.DeleteProtection})
		if err != nil {
			return mapVMError(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO kim.northbound_vm_idempotency_evidence(principal_issuer,principal_subject,parent_project_id,http_method,canonical_path,idempotency_key,request_digest,vm_id,vm_revision,operation_id,response_status,request_id) VALUES($1,$2,$3,'POST',$4,$5,$6,$7,1,$8,201,$9)`, p.Issuer, p.Subject, r.Desired.ProjectID, r.CanonicalPath, r.IdempotencyKey, digest, vmID, operationID, r.RequestID); err != nil {
			return err
		}
		out, err = loadNorthboundVM(ctx, scopeTxBeginner{tx}, vmID)
		if err != nil {
			return err
		}
		return auditTx(ctx, tx, p, r.RequestID, "VM_CREATE", "VM", vmID, 1, "PROJECT", r.Desired.ProjectID, "SUCCEEDED", "CREATED", digest)
	})
	if err != nil {
		return out, false, fmt.Errorf("create VM authority: %w", err)
	}
	return out, replay, returned
}

func (s NorthboundVMStore) Get(ctx context.Context, p resource.Principal, id, requestID string) (out vmapi.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		owner, err := vmOwner(ctx, tx, id)
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
			return auditTx(ctx, tx, p, requestID, "VM_READ", "VM", id, 0, "PROJECT", owner, "DENIED", "FORBIDDEN", "")
		}
		out, err = loadNorthboundVM(ctx, scopeTxBeginner{tx}, id)
		if err != nil {
			return err
		}
		if out.LifecycleState == "DELETED" {
			returned = resource.ErrNotFound
			return nil
		}
		return auditTx(ctx, tx, p, requestID, "VM_READ", "VM", id, out.Revision, "PROJECT", owner, "SUCCEEDED", "READ", "")
	})
	if err != nil {
		return out, fmt.Errorf("read VM authority: %w", err)
	}
	return out, returned
}

func (s NorthboundVMStore) List(ctx context.Context, p resource.Principal, r vmapi.ListRequest, requestID string) (page vmapi.Page, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.vm_id::text FROM kim.vm_resources_current c WHERE c.lifecycle_state<>'DELETED' AND c.vm_id::text>$3 AND ($4='' OR c.project_id=$4) AND (EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='SYSTEM' AND b.role IN('READER','WRITER','ADMIN')) OR EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='PROJECT' AND b.scope_id=c.project_id AND b.role IN('READER','WRITER','ADMIN'))) ORDER BY c.vm_id::text LIMIT $5`, p.Issuer, p.Subject, r.AfterID, r.ProjectID, r.Limit+1)
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
			v, err := loadNorthboundVM(ctx, scopeTxBeginner{tx}, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, v)
		}
		if len(page.Items) > r.Limit {
			page.NextAfter = page.Items[r.Limit-1].ID
			page.Items = page.Items[:r.Limit]
		}
		return auditTx(ctx, tx, p, requestID, "VM_LIST", "VM", "", 0, "SYSTEM", "", "SUCCEEDED", "LISTED", "")
	})
	if err != nil {
		return page, fmt.Errorf("list VM authority: %w", err)
	}
	return page, returned
}

func (s NorthboundVMStore) Patch(ctx context.Context, p resource.Principal, id string, expected uint64, patch vmapi.Patch, requestID, evidenceID, operationID string) (out vmapi.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		current, err := loadNorthboundVM(ctx, scopeTxBeginner{tx}, id)
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
			return auditTx(ctx, tx, p, requestID, "VM_UPDATE", "VM", id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "FORBIDDEN", "")
		}
		if current.Revision != expected {
			returned = resource.ErrStaleRevision
			return auditTx(ctx, tx, p, requestID, "VM_UPDATE", "VM", id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "STALE_RESOURCE_REVISION", "")
		}
		if patch.DesiredPowerState != nil {
			if *patch.DesiredPowerState == current.DesiredPowerState {
				out = current
				return nil
			}
			_, err = StartVMAggregatePowerUpdate(ctx, scopeTxBeginner{tx}, VMAggregatePowerUpdateRequest{RequestID: requestID, OperationID: operationID, VMID: id, DesiredPowerState: *patch.DesiredPowerState, ExpectedRevision: expected})
		} else {
			name, protect := current.Name, current.DeleteProtection
			if patch.Name != nil {
				name = *patch.Name
			}
			if patch.DeleteProtection != nil {
				protect = *patch.DeleteProtection
			}
			if name == current.Name && protect == current.DeleteProtection {
				out = current
				return nil
			}
			_, err = UpdateVMAggregateMetadata(ctx, scopeTxBeginner{tx}, VMAggregateMetadataUpdateRequest{RequestID: requestID, UpdateEvidenceID: evidenceID, VMID: id, Name: name, ExpectedRevision: expected, DeleteProtection: protect})
		}
		if err != nil {
			return mapVMError(err)
		}
		out, err = loadNorthboundVM(ctx, scopeTxBeginner{tx}, id)
		if err != nil {
			return err
		}
		return auditTx(ctx, tx, p, requestID, "VM_UPDATE", "VM", id, out.Revision, "PROJECT", current.ProjectID, "SUCCEEDED", "UPDATED", "")
	})
	if err != nil {
		return out, fmt.Errorf("update VM authority: %w", err)
	}
	return out, returned
}

func (s NorthboundVMStore) Delete(ctx context.Context, p resource.Principal, id string, expected uint64, requestID, operationID string) (out vmapi.Operation, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		current, err := loadNorthboundVM(ctx, scopeTxBeginner{tx}, id)
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
			return auditTx(ctx, tx, p, requestID, "VM_DELETE", "VM", id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "FORBIDDEN", "")
		}
		if current.Revision != expected {
			returned = resource.ErrStaleRevision
			return nil
		}
		if current.DeleteProtection {
			returned = resource.ErrDeleteProtected
			return nil
		}
		_, err = StartVMAggregateDelete(ctx, scopeTxBeginner{tx}, VMAggregateDeleteRequest{RequestID: requestID, OperationID: operationID, VMID: id, ExpectedRevision: expected})
		if err != nil {
			return mapVMError(err)
		}
		out, err = loadNorthboundVMOperation(ctx, tx, operationID)
		if err != nil {
			return err
		}
		return auditTx(ctx, tx, p, requestID, "VM_DELETE", "VM", id, out.TargetRevision, "PROJECT", current.ProjectID, "SUCCEEDED", "DELETION_ACCEPTED", "")
	})
	if err != nil {
		return out, fmt.Errorf("delete VM authority: %w", err)
	}
	return out, returned
}

func (s NorthboundVMStore) GetOperation(ctx context.Context, p resource.Principal, id, requestID string) (out vmapi.Operation, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		out, err = loadNorthboundVMOperation(ctx, tx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		owner, err := vmOwner(ctx, tx, out.TargetResourceID)
		if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "READ", owner)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return nil
		}
		return auditTx(ctx, tx, p, requestID, "OPERATION_READ", "OPERATION", id, out.TargetRevision, "PROJECT", owner, "SUCCEEDED", "READ", "")
	})
	if err != nil {
		return out, fmt.Errorf("read VM Operation: %w", err)
	}
	return out, returned
}

func loadNorthboundVM(ctx context.Context, db TxBeginner, id string) (vmapi.Resource, error) {
	var out vmapi.Resource
	var snapshot string
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT c.vm_id::text,c.project_id,e.vm_name,e.flavor_id,e.flavor_revision,e.image_id,e.image_revision,e.availability_policy_id,e.availability_policy_revision,e.placement_scope_id,e.placement_scope_generation,e.desired_power_state,e.delete_protection,c.vm_revision,c.runtime_intent_generation,c.lifecycle_state,c.convergence_state,c.current_operation_id,s.dependency_snapshot_id,(SELECT min(recorded_at) FROM kim.vm_resource_revision_evidence WHERE vm_id=c.vm_id),c.updated_at FROM kim.vm_resources_current c JOIN kim.vm_resource_revision_evidence e ON(e.vm_id,e.vm_revision)=(c.vm_id,c.vm_revision) JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.runtime_intent_generation)=(c.vm_id,c.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id WHERE c.vm_id=$1`, id).Scan(&out.ID, &out.ProjectID, &out.Name, &out.FlavorID, &out.FlavorRevision, &out.ImageID, &out.ImageRevision, &out.AvailabilityPolicyID, &out.AvailabilityPolicyRevision, &out.PlacementScopeID, &out.PlacementScopeGeneration, &out.DesiredPowerState, &out.DeleteProtection, &out.Revision, &out.RuntimeIntentGeneration, &out.LifecycleState, &out.ConvergenceState, &out.OperationID, &snapshot, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT port_id,port_revision FROM kim.vm_dependency_port_evidence WHERE dependency_snapshot_id=$1 ORDER BY port_ordinal`, snapshot)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r vmapi.Reference
			if err := rows.Scan(&r.ID, &r.Revision); err != nil {
				return err
			}
			out.Ports = append(out.Ports, r)
		}
		vrows, err := tx.Query(ctx, `SELECT volume_id,volume_revision,device_role FROM kim.vm_dependency_volume_evidence WHERE dependency_snapshot_id=$1 ORDER BY volume_ordinal`, snapshot)
		if err != nil {
			return err
		}
		defer vrows.Close()
		for vrows.Next() {
			var r vmapi.Reference
			var role string
			if err := vrows.Scan(&r.ID, &r.Revision, &role); err != nil {
				return err
			}
			if role == "ROOT" {
				out.RootVolume = r
			} else {
				out.DataVolumes = append(out.DataVolumes, r)
			}
		}
		return vrows.Err()
	})
	return out, err
}

func loadNorthboundVMOperation(ctx context.Context, tx pgx.Tx, id string) (vmapi.Operation, error) {
	var out vmapi.Operation
	var internal string
	var terminal *string
	if err := tx.QueryRow(ctx, `SELECT o.operation_id,e.operation_kind,o.vm_id::text,o.vm_revision,e.accepted_at,o.operation_state,o.terminal_evidence_id FROM kim.vm_lifecycle_operations_current o JOIN kim.vm_lifecycle_operation_evidence e ON(e.operation_id,e.operation_generation)=(o.operation_id,o.operation_generation) WHERE o.operation_id=$1`, id).Scan(&out.ID, &out.Type, &out.TargetResourceID, &out.TargetRevision, &out.AcceptedAt, &internal, &terminal); err != nil {
		return out, err
	}
	out.TargetResourceType = "VM"
	out.Phase = publicVMOperationPhase(internal)
	if terminal != nil {
		state := "VERIFIED"
		out.TerminalState = &state
		var at time.Time
		table := map[string]string{"CREATE": "vm_aggregate_terminal_evidence", "POWER": "vm_power_update_terminal_evidence", "DELETE": "vm_delete_terminal_evidence"}[out.Type]
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT recorded_at FROM kim.%s WHERE terminal_evidence_id=$1`, table), *terminal).Scan(&at); err != nil {
			return out, err
		}
		out.CompletedAt = &at
	}
	return out, nil
}
func publicVMOperationPhase(v string) string {
	switch v {
	case "PENDING":
		return "PENDING"
	case "PLACING", "MATERIALIZING":
		return "RUNNING"
	case "VERIFYING":
		return "VERIFYING"
	case "VERIFIED":
		return "SUCCEEDED"
	case "FAILED":
		return "FAILED"
	case "UNKNOWN":
		return "UNKNOWN"
	}
	return "UNKNOWN"
}
func vmOwner(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var owner string
	err := tx.QueryRow(ctx, `SELECT project_id FROM kim.vm_resources_current WHERE vm_id=$1`, id).Scan(&owner)
	return owner, err
}
func mapVMError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return resource.ErrNotFound
	}
	if errors.Is(err, ErrVMAggregateConflict) {
		return resource.ErrConflict
	}
	return err
}
