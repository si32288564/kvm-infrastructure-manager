package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/flavor"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

type NorthboundFlavorStore struct{ DB TxBeginner }

func (s NorthboundFlavorStore) Create(ctx context.Context, principal resource.Principal, request flavor.CreateRequest, id, digest string) (out flavor.Resource, replay bool, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return resource.ErrServiceUnavailable
		}
		lock := fmt.Sprintf("northbound-flavor-idempotency/%s/%s/%s/%s", principal.Issuer, principal.Subject, request.ProjectID, request.IdempotencyKey)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, principal, "CREATE", request.ProjectID)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, principal, request.RequestID, "FLAVOR_CREATE", "FLAVOR", "", 0, "PROJECT", request.ProjectID, "DENIED", "FORBIDDEN", digest)
		}
		var projectActive bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.projects_current WHERE project_id=$1 AND lifecycle_state='ACTIVE')`, request.ProjectID).Scan(&projectActive); err != nil {
			return err
		}
		if !projectActive {
			returned = resource.ErrNotFound
			return auditTx(ctx, tx, principal, request.RequestID, "FLAVOR_CREATE", "FLAVOR", "", 0, "PROJECT", request.ProjectID, "DENIED", "RESOURCE_NOT_FOUND", digest)
		}
		var existingDigest, existingID string
		var existingRevision uint64
		err = tx.QueryRow(ctx, `SELECT request_digest,flavor_id,flavor_revision FROM kim.northbound_flavor_idempotency_evidence WHERE principal_issuer=$1 AND principal_subject=$2 AND parent_project_id=$3 AND http_method='POST' AND canonical_path=$4 AND idempotency_key=$5`, principal.Issuer, principal.Subject, request.ProjectID, request.CanonicalPath, request.IdempotencyKey).Scan(&existingDigest, &existingID, &existingRevision)
		if err == nil {
			if existingDigest != digest {
				returned = resource.ErrIdempotencyConflict
				return auditTx(ctx, tx, principal, request.RequestID, "FLAVOR_CREATE", "FLAVOR", existingID, existingRevision, "PROJECT", request.ProjectID, "DENIED", "IDEMPOTENCY_CONFLICT", digest)
			}
			out, err = loadFlavorTx(ctx, tx, existingID, false)
			if err != nil || out.Revision != existingRevision {
				return resource.ErrConflict
			}
			replay = true
			return auditTx(ctx, tx, principal, request.RequestID, "FLAVOR_CREATE", "FLAVOR", existingID, existingRevision, "PROJECT", request.ProjectID, "SUCCEEDED", "IDEMPOTENT_REPLAY", digest)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := insertFlavorRevisionTx(ctx, tx, id, 1, request.Desired); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.flavors_current(flavor_id,flavor_revision,owner_project_id,lifecycle_state,authority_generation,delete_protection,created_at,updated_at) VALUES($1,1,$2,'ACTIVE',1,$3,statement_timestamp(),statement_timestamp())`, id, request.ProjectID, request.DeleteProtection); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.northbound_flavor_idempotency_evidence(principal_issuer,principal_subject,parent_project_id,http_method,canonical_path,idempotency_key,request_digest,flavor_id,flavor_revision,response_status,request_id) VALUES($1,$2,$3,'POST',$4,$5,$6,$7,1,201,$8)`, principal.Issuer, principal.Subject, request.ProjectID, request.CanonicalPath, request.IdempotencyKey, digest, id, request.RequestID); err != nil {
			return err
		}
		if err := auditTx(ctx, tx, principal, request.RequestID, "FLAVOR_CREATE", "FLAVOR", id, 1, "PROJECT", request.ProjectID, "SUCCEEDED", "CREATED", digest); err != nil {
			return err
		}
		out, err = loadFlavorTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return out, false, fmt.Errorf("create Flavor authority: %w", err)
	}
	return out, replay, returned
}

func (s NorthboundFlavorStore) Get(ctx context.Context, p resource.Principal, id, requestID string) (out flavor.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var owner string
		if err := tx.QueryRow(ctx, `SELECT owner_project_id FROM kim.flavors_current WHERE flavor_id=$1`, id).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		} else if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "READ", owner)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, "FLAVOR_READ", "FLAVOR", id, 0, "PROJECT", owner, "DENIED", "FORBIDDEN", "")
		}
		out, err = loadFlavorTx(ctx, tx, id, false)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		return auditTx(ctx, tx, p, requestID, "FLAVOR_READ", "FLAVOR", id, out.Revision, "PROJECT", owner, "SUCCEEDED", "READ", "")
	})
	if err != nil {
		return out, fmt.Errorf("read Flavor authority: %w", err)
	}
	return out, returned
}

func (s NorthboundFlavorStore) List(ctx context.Context, p resource.Principal, r flavor.ListRequest, requestID string) (page flavor.Page, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.flavor_id,c.owner_project_id,e.name,e.vcpus,e.memory_mib,e.root_disk_gib,e.numa_policy,e.numa_nodes,e.hugepage_size_kib,e.cpu_allocation,e.cpu_pinning,c.delete_protection,c.flavor_revision,c.created_at,c.updated_at FROM kim.flavors_current c JOIN kim.flavor_revision_evidence e ON e.flavor_id=c.flavor_id AND e.flavor_revision=c.flavor_revision WHERE c.lifecycle_state='ACTIVE' AND c.flavor_id>$3 AND ($4='' OR c.owner_project_id=$4) AND (EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='SYSTEM' AND b.role IN('READER','WRITER','ADMIN')) OR EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='PROJECT' AND b.scope_id=c.owner_project_id AND b.role IN('READER','WRITER','ADMIN'))) ORDER BY c.flavor_id LIMIT $5`, p.Issuer, p.Subject, r.AfterID, r.ProjectID, r.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x flavor.Resource
			if err := rows.Scan(&x.ID, &x.ProjectID, &x.Name, &x.VCPUs, &x.MemoryMiB, &x.RootDiskGiB, &x.NUMAPolicy, &x.NUMANodes, &x.HugePageSizeKiB, &x.CPUAllocation, &x.CPUPinning, &x.DeleteProtection, &x.Revision, &x.CreatedAt, &x.UpdatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, x)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Flavors: %w", err)
		}
		if len(page.Items) > r.Limit {
			page.NextAfter = page.Items[r.Limit-1].ID
			page.Items = page.Items[:r.Limit]
		}
		scopeType := "SYSTEM"
		if r.ProjectID != "" {
			scopeType = "PROJECT"
		}
		return auditTx(ctx, tx, p, requestID, "FLAVOR_LIST", "FLAVOR", "", 0, scopeType, r.ProjectID, "SUCCEEDED", "LISTED", "")
	})
	if err != nil {
		return page, fmt.Errorf("list Flavor authority: %w", err)
	}
	return page, returned
}

func (s NorthboundFlavorStore) Patch(ctx context.Context, p resource.Principal, id string, expected uint64, patch flavor.Patch, requestID string) (out flavor.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "flavor/"+id); err != nil {
			return err
		}
		current, err := loadFlavorTx(ctx, tx, id, true)
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
			return auditTx(ctx, tx, p, requestID, "FLAVOR_UPDATE", "FLAVOR", id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "FORBIDDEN", "")
		}
		if current.Revision != expected {
			returned = resource.ErrStaleRevision
			return auditTx(ctx, tx, p, requestID, "FLAVOR_UPDATE", "FLAVOR", id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "STALE_RESOURCE_REVISION", "")
		}
		desired, err := flavor.Apply(current, patch)
		if err != nil {
			returned = err
			return auditTx(ctx, tx, p, requestID, "FLAVOR_UPDATE", "FLAVOR", id, current.Revision, "PROJECT", current.ProjectID, "DENIED", "VALIDATION_FAILED", "")
		}
		digest, _ := flavor.DesiredDigest(desired)
		currentDigest, _ := flavor.DesiredDigest(flavor.Desired{ProjectID: current.ProjectID, Name: current.Name, VCPUs: current.VCPUs, MemoryMiB: current.MemoryMiB, RootDiskGiB: current.RootDiskGiB, NUMAPolicy: current.NUMAPolicy, NUMANodes: current.NUMANodes, HugePageSizeKiB: current.HugePageSizeKiB, CPUAllocation: current.CPUAllocation, CPUPinning: current.CPUPinning, DeleteProtection: current.DeleteProtection})
		if digest == currentDigest {
			out = current
			return auditTx(ctx, tx, p, requestID, "FLAVOR_UPDATE", "FLAVOR", id, current.Revision, "PROJECT", current.ProjectID, "SUCCEEDED", "UNCHANGED", "")
		}
		next := current.Revision + 1
		if err := insertFlavorRevisionTx(ctx, tx, id, next, desired); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.flavors_current SET flavor_revision=$2,authority_generation=authority_generation+1,delete_protection=$3,updated_at=statement_timestamp() WHERE flavor_id=$1`, id, next, desired.DeleteProtection); err != nil {
			return err
		}
		if err := auditTx(ctx, tx, p, requestID, "FLAVOR_UPDATE", "FLAVOR", id, next, "PROJECT", current.ProjectID, "SUCCEEDED", "UPDATED", ""); err != nil {
			return err
		}
		out, err = loadFlavorTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return out, fmt.Errorf("update Flavor authority: %w", err)
	}
	return out, returned
}

func (s NorthboundFlavorStore) Delete(ctx context.Context, p resource.Principal, id string, expected uint64, requestID string) (returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "flavor/"+id); err != nil {
			return err
		}
		var rev uint64
		var owner, state string
		var protected bool
		var deletedFrom *uint64
		err := tx.QueryRow(ctx, `SELECT flavor_revision,owner_project_id,lifecycle_state,delete_protection,deleted_from_revision FROM kim.flavors_current WHERE flavor_id=$1 FOR UPDATE`, id).Scan(&rev, &owner, &state, &protected, &deletedFrom)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "DELETE", owner)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, "FLAVOR_DELETE", "FLAVOR", id, rev, "PROJECT", owner, "DENIED", "FORBIDDEN", "")
		}
		if state == "DELETED" && deletedFrom != nil && *deletedFrom == expected {
			return auditTx(ctx, tx, p, requestID, "FLAVOR_DELETE", "FLAVOR", id, rev, "PROJECT", owner, "SUCCEEDED", "IDEMPOTENT_REPLAY", "")
		}
		if state != "ACTIVE" {
			returned = resource.ErrNotFound
			return nil
		}
		if rev != expected {
			returned = resource.ErrStaleRevision
			return auditTx(ctx, tx, p, requestID, "FLAVOR_DELETE", "FLAVOR", id, rev, "PROJECT", owner, "DENIED", "STALE_RESOURCE_REVISION", "")
		}
		if protected {
			returned = resource.ErrDeleteProtected
			return auditTx(ctx, tx, p, requestID, "FLAVOR_DELETE", "FLAVOR", id, rev, "PROJECT", owner, "DENIED", "DELETE_PROTECTED", "")
		}
		dependent, err := flavorDependenciesExist(ctx, tx, id)
		if err != nil {
			return err
		}
		if dependent {
			returned = resource.ErrDependencyConflict
			return auditTx(ctx, tx, p, requestID, "FLAVOR_DELETE", "FLAVOR", id, rev, "PROJECT", owner, "DENIED", "DEPENDENCY_CONFLICT", "")
		}
		current, err := loadFlavorTx(ctx, tx, id, false)
		if err != nil {
			return err
		}
		desired := flavor.Desired{ProjectID: owner, Name: current.Name, VCPUs: current.VCPUs, MemoryMiB: current.MemoryMiB, RootDiskGiB: current.RootDiskGiB, NUMAPolicy: current.NUMAPolicy, NUMANodes: current.NUMANodes, HugePageSizeKiB: current.HugePageSizeKiB, CPUAllocation: current.CPUAllocation, CPUPinning: current.CPUPinning, DeleteProtection: false}
		next := rev + 1
		if err := insertFlavorRevisionTx(ctx, tx, id, next, desired); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.flavors_current SET flavor_revision=$2,lifecycle_state='DELETED',authority_generation=authority_generation+1,delete_protection=false,deleted_from_revision=$3,updated_at=statement_timestamp() WHERE flavor_id=$1`, id, next, rev); err != nil {
			return err
		}
		return auditTx(ctx, tx, p, requestID, "FLAVOR_DELETE", "FLAVOR", id, next, "PROJECT", owner, "SUCCEEDED", "DELETED", "")
	})
	if err != nil {
		return fmt.Errorf("delete Flavor authority: %w", err)
	}
	return returned
}

func flavorDependenciesExist(ctx context.Context, db QueryRower, id string) (bool, error) {
	var dependent bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.placement_admission_decisions WHERE flavor_id=$1) OR EXISTS(SELECT 1 FROM kim.vm_materialization_plan_evidence WHERE flavor_id=$1)`, id).Scan(&dependent)
	return dependent, err
}

func insertFlavorRevisionTx(ctx context.Context, tx pgx.Tx, id string, revision uint64, d flavor.Desired) error {
	extra, _ := json.Marshal(map[string]string{})
	shape := FlavorRevision{FlavorID: id, OwnerProjectID: d.ProjectID, Name: d.Name, Revision: revision, VCPUs: d.VCPUs, MemoryMiB: d.MemoryMiB, RootDiskGiB: d.RootDiskGiB, NUMAPolicy: d.NUMAPolicy, NUMANodes: d.NUMANodes, HugePageSizeKiB: d.HugePageSizeKiB, CPUAllocation: d.CPUAllocation, CPUPinning: d.CPUPinning, ExtraSpecs: map[string]string{}}
	normalized, _, err := normalizeFlavor(shape)
	if err != nil {
		return resource.ErrValidation
	}
	revisionDigest, err := flavorRevisionDigest(shape, normalized.ShapeDigest)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.flavor_revision_evidence(flavor_id,flavor_revision,owner_project_id,name,vcpus,memory_mib,root_disk_gib,numa_policy,numa_nodes,hugepage_size_kib,cpu_allocation,cpu_pinning,extra_specs,shape_digest,revision_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, id, revision, d.ProjectID, d.Name, d.VCPUs, d.MemoryMiB, d.RootDiskGiB, d.NUMAPolicy, d.NUMANodes, d.HugePageSizeKiB, d.CPUAllocation, d.CPUPinning, extra, normalized.ShapeDigest, revisionDigest)
	return err
}
func loadFlavorTx(ctx context.Context, tx pgx.Tx, id string, lock bool) (x flavor.Resource, err error) {
	q := `SELECT c.flavor_id,c.owner_project_id,e.name,e.vcpus,e.memory_mib,e.root_disk_gib,e.numa_policy,e.numa_nodes,e.hugepage_size_kib,e.cpu_allocation,e.cpu_pinning,c.delete_protection,c.flavor_revision,c.created_at,c.updated_at FROM kim.flavors_current c JOIN kim.flavor_revision_evidence e ON e.flavor_id=c.flavor_id AND e.flavor_revision=c.flavor_revision WHERE c.flavor_id=$1 AND c.lifecycle_state='ACTIVE'`
	if lock {
		q += ` FOR UPDATE OF c`
	}
	err = tx.QueryRow(ctx, q, id).Scan(&x.ID, &x.ProjectID, &x.Name, &x.VCPUs, &x.MemoryMiB, &x.RootDiskGiB, &x.NUMAPolicy, &x.NUMANodes, &x.HugePageSizeKiB, &x.CPUAllocation, &x.CPUPinning, &x.DeleteProtection, &x.Revision, &x.CreatedAt, &x.UpdatedAt)
	return
}
