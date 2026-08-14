package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/availabilitypolicy"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

type NorthboundAvailabilityPolicyStore struct{ DB TxBeginner }

func (s NorthboundAvailabilityPolicyStore) Create(ctx context.Context, p resource.Principal, r availabilitypolicy.CreateRequest, id, digest string) (out availabilitypolicy.Resource, replay bool, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return resource.ErrServiceUnavailable
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "northbound-availability-idempotency/"+p.Issuer+"/"+p.Subject+"/"+r.IdempotencyKey); err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "CREATE", "")
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, r.RequestID, "AVAILABILITY_POLICY_CREATE", "AVAILABILITY_POLICY", "", 0, "SYSTEM", "", "DENIED", "FORBIDDEN", digest)
		}
		var priorDigest, priorID string
		var priorRevision uint64
		err = tx.QueryRow(ctx, `SELECT request_digest,policy_id,policy_revision FROM kim.northbound_availability_policy_idempotency_evidence WHERE principal_issuer=$1 AND principal_subject=$2 AND parent_scope='SYSTEM' AND http_method='POST' AND canonical_path=$3 AND idempotency_key=$4`, p.Issuer, p.Subject, r.CanonicalPath, r.IdempotencyKey).Scan(&priorDigest, &priorID, &priorRevision)
		if err == nil {
			if priorDigest != digest {
				returned = resource.ErrIdempotencyConflict
				return auditTx(ctx, tx, p, r.RequestID, "AVAILABILITY_POLICY_CREATE", "AVAILABILITY_POLICY", priorID, priorRevision, "SYSTEM", "", "DENIED", "IDEMPOTENCY_CONFLICT", digest)
			}
			out, err = loadNorthboundAvailabilityPolicyTx(ctx, tx, priorID, false)
			if err != nil || out.Revision != priorRevision {
				return resource.ErrConflict
			}
			replay = true
			return auditTx(ctx, tx, p, r.RequestID, "AVAILABILITY_POLICY_CREATE", "AVAILABILITY_POLICY", priorID, priorRevision, "SYSTEM", "", "SUCCEEDED", "IDEMPOTENT_REPLAY", digest)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := insertNorthboundAvailabilityPolicyRevisionTx(ctx, tx, id, 1, r.Desired, "ACTIVE", p, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.availability_policies_current(policy_id,policy_revision,lifecycle_state,policy_digest,delete_protection,created_at,updated_at) SELECT $1,1,'ACTIVE',policy_digest,$2,statement_timestamp(),statement_timestamp() FROM kim.availability_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=1`, id, r.DeleteProtection); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.northbound_availability_policy_idempotency_evidence(principal_issuer,principal_subject,parent_scope,http_method,canonical_path,idempotency_key,request_digest,policy_id,policy_revision,response_status,request_id) VALUES($1,$2,'SYSTEM','POST',$3,$4,$5,$6,1,201,$7)`, p.Issuer, p.Subject, r.CanonicalPath, r.IdempotencyKey, digest, id, r.RequestID); err != nil {
			return err
		}
		if err := auditTx(ctx, tx, p, r.RequestID, "AVAILABILITY_POLICY_CREATE", "AVAILABILITY_POLICY", id, 1, "SYSTEM", "", "SUCCEEDED", "CREATED", digest); err != nil {
			return err
		}
		out, err = loadNorthboundAvailabilityPolicyTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return out, false, fmt.Errorf("create Availability Policy authority: %w", err)
	}
	return out, replay, returned
}

func (s NorthboundAvailabilityPolicyStore) Get(ctx context.Context, p resource.Principal, id, requestID string) (out availabilitypolicy.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "READ", "")
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_READ", "AVAILABILITY_POLICY", id, 0, "SYSTEM", "", "DENIED", "FORBIDDEN", "")
		}
		out, err = loadNorthboundAvailabilityPolicyTx(ctx, tx, id, false)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_READ", "AVAILABILITY_POLICY", id, out.Revision, "SYSTEM", "", "SUCCEEDED", "READ", "")
	})
	if err != nil {
		return out, fmt.Errorf("read Availability Policy authority: %w", err)
	}
	return out, returned
}

func (s NorthboundAvailabilityPolicyStore) List(ctx context.Context, p resource.Principal, r availabilitypolicy.ListRequest, requestID string) (page availabilitypolicy.Page, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "LIST", "")
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_LIST", "AVAILABILITY_POLICY", "", 0, "SYSTEM", "", "DENIED", "FORBIDDEN", "")
		}
		rows, err := tx.Query(ctx, `SELECT c.policy_id,m.policy_name,m.availability_mode,e.max_attempts,c.delete_protection,c.policy_revision,c.created_at,c.updated_at FROM kim.availability_policies_current c JOIN kim.northbound_availability_policy_revision_metadata m USING(policy_id,policy_revision) JOIN kim.availability_policy_revision_evidence e USING(policy_id,policy_revision) WHERE c.lifecycle_state<>'RETIRED' AND c.policy_id>$1 ORDER BY c.policy_id LIMIT $2`, r.AfterID, r.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x availabilitypolicy.Resource
			if err := rows.Scan(&x.ID, &x.Name, &x.AvailabilityMode, &x.MaxAttempts, &x.DeleteProtection, &x.Revision, &x.CreatedAt, &x.UpdatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, x)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(page.Items) > r.Limit {
			page.NextAfter = page.Items[r.Limit-1].ID
			page.Items = page.Items[:r.Limit]
		}
		return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_LIST", "AVAILABILITY_POLICY", "", 0, "SYSTEM", "", "SUCCEEDED", "LISTED", "")
	})
	if err != nil {
		return page, fmt.Errorf("list Availability Policies: %w", err)
	}
	return page, returned
}

func (s NorthboundAvailabilityPolicyStore) Patch(ctx context.Context, p resource.Principal, id string, expected uint64, patch availabilitypolicy.Patch, requestID string) (out availabilitypolicy.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-policy/"+id); err != nil {
			return err
		}
		current, err := loadNorthboundAvailabilityPolicyTx(ctx, tx, id, true)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "UPDATE", "")
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_UPDATE", "AVAILABILITY_POLICY", id, current.Revision, "SYSTEM", "", "DENIED", "FORBIDDEN", "")
		}
		if current.Revision != expected {
			returned = resource.ErrStaleRevision
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_UPDATE", "AVAILABILITY_POLICY", id, current.Revision, "SYSTEM", "", "DENIED", "STALE_RESOURCE_REVISION", "")
		}
		desired, err := availabilitypolicy.Apply(current, patch)
		if err != nil {
			returned = err
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_UPDATE", "AVAILABILITY_POLICY", id, current.Revision, "SYSTEM", "", "DENIED", "VALIDATION_FAILED", "")
		}
		digest, _ := availabilitypolicy.DesiredDigest(desired)
		prior, _ := availabilitypolicy.DesiredDigest(availabilitypolicy.Desired{Name: current.Name, AvailabilityMode: current.AvailabilityMode, MaxAttempts: current.MaxAttempts, DeleteProtection: current.DeleteProtection})
		if digest == prior {
			out = current
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_UPDATE", "AVAILABILITY_POLICY", id, current.Revision, "SYSTEM", "", "SUCCEEDED", "UNCHANGED", "")
		}
		next := current.Revision + 1
		if err := insertNorthboundAvailabilityPolicyRevisionTx(ctx, tx, id, next, desired, "ACTIVE", p, digest); err != nil {
			return err
		}
		var policyDigest string
		if err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.availability_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, id, next).Scan(&policyDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.availability_policies_current SET policy_revision=$2,lifecycle_state='ACTIVE',policy_digest=$3,delete_protection=$4,updated_at=statement_timestamp() WHERE policy_id=$1`, id, next, policyDigest, desired.DeleteProtection); err != nil {
			return err
		}
		if err := auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_UPDATE", "AVAILABILITY_POLICY", id, next, "SYSTEM", "", "SUCCEEDED", "UPDATED", digest); err != nil {
			return err
		}
		out, err = loadNorthboundAvailabilityPolicyTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return out, fmt.Errorf("update Availability Policy authority: %w", err)
	}
	return out, returned
}

func (s NorthboundAvailabilityPolicyStore) Delete(ctx context.Context, p resource.Principal, id string, expected uint64, requestID string) (returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-policy/"+id); err != nil {
			return err
		}
		var rev uint64
		var state string
		var protected bool
		var deletedFrom *uint64
		if err := tx.QueryRow(ctx, `SELECT policy_revision,lifecycle_state,delete_protection,deleted_from_revision FROM kim.availability_policies_current WHERE policy_id=$1 FOR UPDATE`, id).Scan(&rev, &state, &protected, &deletedFrom); errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		} else if err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "DELETE", "")
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_DELETE", "AVAILABILITY_POLICY", id, rev, "SYSTEM", "", "DENIED", "FORBIDDEN", "")
		}
		if state == "RETIRED" && deletedFrom != nil && *deletedFrom == expected {
			return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_DELETE", "AVAILABILITY_POLICY", id, rev, "SYSTEM", "", "SUCCEEDED", "IDEMPOTENT_REPLAY", "")
		}
		if state == "RETIRED" {
			returned = resource.ErrNotFound
			return nil
		}
		if rev != expected {
			returned = resource.ErrStaleRevision
			return nil
		}
		if protected {
			returned = resource.ErrDeleteProtected
			return nil
		}
		dependent, err := availabilityPolicyDependenciesExist(ctx, tx, id)
		if err != nil {
			return err
		}
		if dependent {
			returned = resource.ErrDependencyConflict
			return nil
		}
		current, err := loadNorthboundAvailabilityPolicyTx(ctx, tx, id, false)
		if err != nil {
			return err
		}
		desired := availabilitypolicy.Desired{Name: current.Name, AvailabilityMode: current.AvailabilityMode, MaxAttempts: current.MaxAttempts, DeleteProtection: false}
		digest, _ := availabilitypolicy.DesiredDigest(desired)
		next := rev + 1
		if err := insertNorthboundAvailabilityPolicyRevisionTx(ctx, tx, id, next, desired, "RETIRED", p, digest); err != nil {
			return err
		}
		var policyDigest string
		if err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.availability_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, id, next).Scan(&policyDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.availability_policies_current SET policy_revision=$2,lifecycle_state='RETIRED',policy_digest=$3,delete_protection=false,deleted_from_revision=$4,updated_at=statement_timestamp() WHERE policy_id=$1`, id, next, policyDigest, rev); err != nil {
			return err
		}
		return auditTx(ctx, tx, p, requestID, "AVAILABILITY_POLICY_DELETE", "AVAILABILITY_POLICY", id, next, "SYSTEM", "", "SUCCEEDED", "RETIRED", digest)
	})
	if err != nil {
		return fmt.Errorf("delete Availability Policy authority: %w", err)
	}
	return returned
}

func insertNorthboundAvailabilityPolicyRevisionTx(ctx context.Context, tx pgx.Tx, id string, revision uint64, d availabilitypolicy.Desired, lifecycle string, p resource.Principal, desiredDigest string) error {
	policy := AvailabilityPolicyRevision{PolicyID: id, PolicyRevision: revision, Responsibility: d.AvailabilityMode, HostFailureAction: "NO_AUTOMATIC_ACTION", FailureConfirmationPolicy: "NORTHBOUND_NOT_REQUIRED", FencingRequirements: "NORTHBOUND_NOT_REQUIRED", StorageRequirements: "NORTHBOUND_NOT_REQUIRED", NetworkDeviceRequirements: "NORTHBOUND_NOT_REQUIRED", RecoveryEligibilityPolicy: "NO_AUTOMATIC_RECOVERY", FailureDomainConstraints: "NONE", RecoveryBudgetPolicyReference: "NORTHBOUND_NOT_REQUIRED", MaxAttempts: d.MaxAttempts, EscalationPolicy: "MANUAL", NotificationPolicy: "NONE", SupportTier: "STANDARD", LifecycleState: lifecycle, CreatedBy: p.Issuer + "/" + p.Subject, ApprovedBy: p.Issuer + "/" + p.Subject}
	policyDigest := availabilityPolicyDigestValue(policy)
	if _, err := tx.Exec(ctx, `INSERT INTO kim.group_policy_revision_catalog(policy_type,policy_id,policy_revision,policy_digest,lifecycle_state) VALUES('AVAILABILITY_POLICY',$1,$2,$3,$4)`, id, revision, policyDigest, lifecycle); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.availability_policy_revision_evidence(policy_id,policy_revision,responsibility,host_failure_action,failure_confirmation_policy,fencing_requirements,storage_requirements,network_device_requirements,recovery_eligibility_policy,failure_domain_constraints,recovery_budget_policy_reference,max_attempts,escalation_policy,notification_policy,support_tier,lifecycle_state,created_by,approved_by,policy_digest) VALUES($1,$2,$3,'NO_AUTOMATIC_ACTION','NORTHBOUND_NOT_REQUIRED','NORTHBOUND_NOT_REQUIRED','NORTHBOUND_NOT_REQUIRED','NORTHBOUND_NOT_REQUIRED','NO_AUTOMATIC_RECOVERY','NONE','NORTHBOUND_NOT_REQUIRED',$4,'MANUAL','NONE','STANDARD',$5,$6,$6,$7)`, id, revision, d.AvailabilityMode, d.MaxAttempts, lifecycle, p.Issuer+"/"+p.Subject, policyDigest); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.group_policies_current(policy_type,policy_id,policy_revision,policy_digest,lifecycle_state) VALUES('AVAILABILITY_POLICY',$1,$2,$3,$4) ON CONFLICT(policy_type,policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,policy_digest=EXCLUDED.policy_digest,lifecycle_state=EXCLUDED.lifecycle_state,updated_at=statement_timestamp()`, id, revision, policyDigest, lifecycle); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO kim.northbound_availability_policy_revision_metadata(policy_id,policy_revision,policy_name,availability_mode,delete_protection,desired_digest) VALUES($1,$2,$3,$4,$5,$6)`, id, revision, d.Name, d.AvailabilityMode, d.DeleteProtection, desiredDigest)
	return err
}

func loadNorthboundAvailabilityPolicyTx(ctx context.Context, tx pgx.Tx, id string, lock bool) (x availabilitypolicy.Resource, err error) {
	q := `SELECT c.policy_id,m.policy_name,m.availability_mode,e.max_attempts,c.delete_protection,c.policy_revision,c.created_at,c.updated_at FROM kim.availability_policies_current c JOIN kim.northbound_availability_policy_revision_metadata m USING(policy_id,policy_revision) JOIN kim.availability_policy_revision_evidence e USING(policy_id,policy_revision) WHERE c.policy_id=$1 AND c.lifecycle_state<>'RETIRED'`
	if lock {
		q += ` FOR UPDATE OF c`
	}
	err = tx.QueryRow(ctx, q, id).Scan(&x.ID, &x.Name, &x.AvailabilityMode, &x.MaxAttempts, &x.DeleteProtection, &x.Revision, &x.CreatedAt, &x.UpdatedAt)
	return
}
func availabilityPolicyDependenciesExist(ctx context.Context, db QueryRower, id string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_group_policy_bindings_current WHERE policy_type='AVAILABILITY_POLICY' AND policy_id=$1 AND lifecycle_state<>'RETIRED') OR EXISTS(SELECT 1 FROM kim.vm_availability_binding_evidence WHERE availability_policy_id=$1)`, id).Scan(&exists)
	return exists, err
}
