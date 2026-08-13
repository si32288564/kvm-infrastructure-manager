package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
)

type NorthboundProjectStore struct{ DB TxBeginner }

func (s NorthboundProjectStore) Ready(ctx context.Context) error {
	if s.DB == nil {
		return project.ErrServiceUnavailable
	}
	var active bool
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.database_authority WHERE singleton AND mode='ACTIVE') AND to_regclass('kim.projects_current') IS NOT NULL`).Scan(&active)
	})
	if err != nil || !active {
		return project.ErrServiceUnavailable
	}
	return nil
}

func (s NorthboundProjectStore) Create(ctx context.Context, principal project.Principal, request project.CreateRequest, projectID, desiredDigest string) (resource project.Resource, replay bool, returnedErr error) {
	if s.DB == nil {
		return resource, false, project.ErrServiceUnavailable
	}
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return project.ErrServiceUnavailable
		}
		lockKey := fmt.Sprintf("northbound-idempotency/%s/%s/SYSTEM/POST%s/%s", principal.Issuer, principal.Subject, request.CanonicalPath, request.IdempotencyKey)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
			return err
		}
		allowed, err := authorizeProjectTx(ctx, tx, principal, "CREATE", "")
		if err != nil {
			return err
		}
		if !allowed {
			if err := insertProjectAuditTx(ctx, tx, principal, request.RequestID, "PROJECT_CREATE", "", 0, "SYSTEM", "", "DENIED", "FORBIDDEN", desiredDigest); err != nil {
				return err
			}
			returnedErr = project.ErrForbidden
			return nil
		}
		var existingDigest, existingID string
		var existingRevision uint64
		err = tx.QueryRow(ctx, `SELECT request_digest,resource_id,resource_revision FROM kim.northbound_idempotency_evidence
			WHERE principal_issuer=$1 AND principal_subject=$2 AND parent_scope='SYSTEM' AND http_method='POST' AND canonical_path=$3 AND idempotency_key=$4`, principal.Issuer, principal.Subject, request.CanonicalPath, request.IdempotencyKey).Scan(&existingDigest, &existingID, &existingRevision)
		if err == nil {
			if existingDigest != desiredDigest {
				if err := insertProjectAuditTx(ctx, tx, principal, request.RequestID, "PROJECT_CREATE", existingID, existingRevision, "SYSTEM", "", "DENIED", "IDEMPOTENCY_CONFLICT", desiredDigest); err != nil {
					return err
				}
				returnedErr = project.ErrIdempotencyConflict
				return nil
			}
			resource, err = loadProjectTx(ctx, tx, existingID, false)
			if err != nil || resource.Revision != existingRevision {
				return project.ErrConflict
			}
			if err := insertProjectAuditTx(ctx, tx, principal, request.RequestID, "PROJECT_CREATE", existingID, existingRevision, "PROJECT", existingID, "SUCCEEDED", "IDEMPOTENT_REPLAY", desiredDigest); err != nil {
				return err
			}
			replay = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,previous_revision,desired_digest,actor_issuer,actor_subject,request_id)
			VALUES($1,1,$2,$3,'ACTIVE',NULL,$4,$5,$6,$7)`, projectID, request.Name, request.DeleteProtection, desiredDigest, principal.Issuer, principal.Subject, request.RequestID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at,updated_at)
			VALUES($1,1,$2,$3,'ACTIVE',$4,statement_timestamp(),statement_timestamp())`, projectID, request.Name, request.DeleteProtection, desiredDigest); err != nil {
			return err
		}
		bindingID, err := project.NewID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision)
			VALUES($1,$2,$3,$4,'PROJECT',$5,'ADMIN','ACTIVE',1)`, bindingID, principal.Issuer, principal.Subject, principal.Type, projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.northbound_idempotency_evidence(principal_issuer,principal_subject,parent_scope,http_method,canonical_path,idempotency_key,request_digest,resource_type,resource_id,resource_revision,response_status,request_id)
			VALUES($1,$2,'SYSTEM','POST',$3,$4,$5,'PROJECT',$6,1,201,$7)`, principal.Issuer, principal.Subject, request.CanonicalPath, request.IdempotencyKey, desiredDigest, projectID, request.RequestID); err != nil {
			return err
		}
		if err := insertProjectAuditTx(ctx, tx, principal, request.RequestID, "PROJECT_CREATE", projectID, 1, "PROJECT", projectID, "SUCCEEDED", "CREATED", desiredDigest); err != nil {
			return err
		}
		resource, err = loadProjectTx(ctx, tx, projectID, false)
		return err
	})
	if err != nil {
		return project.Resource{}, false, fmt.Errorf("create Project authority: %w", err)
	}
	return resource, replay, returnedErr
}

func (s NorthboundProjectStore) Get(ctx context.Context, principal project.Principal, projectID, requestID string) (resource project.Resource, returnedErr error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		allowed, err := authorizeProjectTx(ctx, tx, principal, "READ", projectID)
		if err != nil {
			return err
		}
		if !allowed {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_READ", projectID, 0, "PROJECT", projectID, "DENIED", "FORBIDDEN", ""); err != nil {
				return err
			}
			returnedErr = project.ErrForbidden
			return nil
		}
		resource, err = loadProjectTx(ctx, tx, projectID, false)
		if errors.Is(err, pgx.ErrNoRows) {
			returnedErr = project.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		return insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_READ", projectID, resource.Revision, "PROJECT", projectID, "SUCCEEDED", "READ", "")
	})
	if err != nil {
		return resource, fmt.Errorf("read Project authority: %w", err)
	}
	return resource, returnedErr
}

func (s NorthboundProjectStore) List(ctx context.Context, principal project.Principal, request project.ListRequest, requestID string) (page project.Page, returnedErr error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT p.project_id,p.project_name,p.delete_protection,p.project_revision,p.created_at,p.updated_at
			FROM kim.projects_current p WHERE p.lifecycle_state='ACTIVE' AND p.project_id>$3 AND (
			EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='SYSTEM' AND b.role IN ('READER','WRITER','ADMIN')) OR
			EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='PROJECT' AND b.scope_id=p.project_id AND b.role IN ('READER','WRITER','ADMIN')))
			ORDER BY p.project_id LIMIT $4`, principal.Issuer, principal.Subject, request.AfterID, request.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item project.Resource
			if err := rows.Scan(&item.ID, &item.Name, &item.DeleteProtection, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(page.Items) > request.Limit {
			page.NextAfter = page.Items[request.Limit-1].ID
			page.Items = page.Items[:request.Limit]
		}
		return insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_LIST", "", 0, "SYSTEM", "", "SUCCEEDED", "LISTED", "")
	})
	if err != nil {
		return page, fmt.Errorf("list Project authority: %w", err)
	}
	return page, returnedErr
}

func (s NorthboundProjectStore) Patch(ctx context.Context, principal project.Principal, projectID string, expected uint64, patch project.Patch, requestID string) (resource project.Resource, returnedErr error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "project/"+projectID); err != nil {
			return err
		}
		allowed, err := authorizeProjectTx(ctx, tx, principal, "UPDATE", projectID)
		if err != nil {
			return err
		}
		if !allowed {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_UPDATE", projectID, expected, "PROJECT", projectID, "DENIED", "FORBIDDEN", ""); err != nil {
				return err
			}
			returnedErr = project.ErrForbidden
			return nil
		}
		resource, err = loadProjectTx(ctx, tx, projectID, true)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_UPDATE", projectID, expected, "PROJECT", projectID, "DENIED", "RESOURCE_NOT_FOUND", ""); err != nil {
				return err
			}
			returnedErr = project.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		if resource.Revision != expected {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_UPDATE", projectID, resource.Revision, "PROJECT", projectID, "DENIED", "STALE_RESOURCE_REVISION", ""); err != nil {
				return err
			}
			returnedErr = project.ErrStaleRevision
			return nil
		}
		name, protection := resource.Name, resource.DeleteProtection
		if patch.NamePresent {
			name = patch.Name
		}
		if patch.DeleteProtectionPresent {
			protection = patch.DeleteProtection
		}
		if name == resource.Name && protection == resource.DeleteProtection {
			return insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_UPDATE", projectID, resource.Revision, "PROJECT", projectID, "SUCCEEDED", "UNCHANGED", "")
		}
		digest, err := project.DesiredDigest(name, protection)
		if err != nil {
			return err
		}
		newRevision := resource.Revision + 1
		if _, err := tx.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,previous_revision,desired_digest,actor_issuer,actor_subject,request_id)
			VALUES($1,$2,$3,$4,'ACTIVE',$5,$6,$7,$8,$9)`, projectID, newRevision, name, protection, resource.Revision, digest, principal.Issuer, principal.Subject, requestID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.projects_current SET project_revision=$2,project_name=$3,delete_protection=$4,desired_digest=$5,updated_at=statement_timestamp() WHERE project_id=$1`, projectID, newRevision, name, protection, digest); err != nil {
			return err
		}
		if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_UPDATE", projectID, newRevision, "PROJECT", projectID, "SUCCEEDED", "UPDATED", ""); err != nil {
			return err
		}
		resource, err = loadProjectTx(ctx, tx, projectID, false)
		return err
	})
	if err != nil {
		return resource, fmt.Errorf("update Project authority: %w", err)
	}
	return resource, returnedErr
}

func (s NorthboundProjectStore) Delete(ctx context.Context, principal project.Principal, projectID string, expected uint64, requestID string) (returnedErr error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "project/"+projectID); err != nil {
			return err
		}
		allowed, err := authorizeProjectTx(ctx, tx, principal, "DELETE", projectID)
		if err != nil {
			return err
		}
		if !allowed {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, expected, "PROJECT", projectID, "DENIED", "FORBIDDEN", ""); err != nil {
				return err
			}
			returnedErr = project.ErrForbidden
			return nil
		}
		var currentRevision uint64
		var name, state string
		var protection bool
		var deletedFrom *uint64
		err = tx.QueryRow(ctx, `SELECT project_revision,project_name,delete_protection,lifecycle_state,deleted_from_revision FROM kim.projects_current WHERE project_id=$1 FOR UPDATE`, projectID).Scan(&currentRevision, &name, &protection, &state, &deletedFrom)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, expected, "PROJECT", projectID, "DENIED", "RESOURCE_NOT_FOUND", ""); err != nil {
				return err
			}
			returnedErr = project.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		if state == "DELETED" && deletedFrom != nil && *deletedFrom == expected {
			return insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, currentRevision, "PROJECT", projectID, "SUCCEEDED", "IDEMPOTENT_REPLAY", "")
		}
		if state != "ACTIVE" {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, currentRevision, "PROJECT", projectID, "DENIED", "RESOURCE_NOT_FOUND", ""); err != nil {
				return err
			}
			returnedErr = project.ErrNotFound
			return nil
		}
		if currentRevision != expected {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, currentRevision, "PROJECT", projectID, "DENIED", "STALE_RESOURCE_REVISION", ""); err != nil {
				return err
			}
			returnedErr = project.ErrStaleRevision
			return nil
		}
		if protection {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, currentRevision, "PROJECT", projectID, "DENIED", "DELETE_PROTECTED", ""); err != nil {
				return err
			}
			returnedErr = project.ErrDeleteProtected
			return nil
		}
		dependent, err := projectDependenciesExistTx(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if dependent {
			if err := insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, currentRevision, "PROJECT", projectID, "DENIED", "DEPENDENCY_CONFLICT", ""); err != nil {
				return err
			}
			returnedErr = project.ErrDependencyConflict
			return nil
		}
		digest, err := project.DesiredDigest(name, protection)
		if err != nil {
			return err
		}
		newRevision := currentRevision + 1
		if _, err := tx.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,previous_revision,desired_digest,actor_issuer,actor_subject,request_id)
			VALUES($1,$2,$3,$4,'DELETED',$5,$6,$7,$8,$9)`, projectID, newRevision, name, protection, currentRevision, digest, principal.Issuer, principal.Subject, requestID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.projects_current SET project_revision=$2,lifecycle_state='DELETED',desired_digest=$3,deleted_from_revision=$4,updated_at=statement_timestamp() WHERE project_id=$1`, projectID, newRevision, digest, currentRevision); err != nil {
			return err
		}
		return insertProjectAuditTx(ctx, tx, principal, requestID, "PROJECT_DELETE", projectID, newRevision, "PROJECT", projectID, "SUCCEEDED", "DELETED", "")
	})
	if err != nil {
		return fmt.Errorf("delete Project authority: %w", err)
	}
	return returnedErr
}

func (s NorthboundProjectStore) RecordAudit(ctx context.Context, event project.AuditEvent) error {
	if s.DB == nil {
		return project.ErrServiceUnavailable
	}
	return pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO kim.northbound_audit_evidence(audit_id,request_id,principal_issuer,principal_subject,principal_type,action,resource_type,resource_id,scope_type,scope_id,resource_revision,result,reason_code,idempotency_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,0),$12,$13,NULLIF($14,''))`, event.AuditID, event.RequestID, event.Principal.Issuer, event.Principal.Subject, event.Principal.Type, event.Action, event.ResourceType, event.ResourceID, event.ScopeType, event.ScopeID, event.ResourceRevision, event.Result, event.ReasonCode, event.IdempotencyDigest)
		return err
	})
}

func authorizeProjectTx(ctx context.Context, tx pgx.Tx, principal project.Principal, action, projectID string) (bool, error) {
	roles := []string{"ADMIN"}
	if action == "READ" || action == "LIST" {
		roles = []string{"READER", "WRITER", "ADMIN"}
	} else if action == "UPDATE" {
		roles = []string{"WRITER", "ADMIN"}
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current WHERE principal_issuer=$1 AND principal_subject=$2 AND principal_type=$3 AND lifecycle_state='ACTIVE' AND role=ANY($4) AND ((scope_type='SYSTEM' AND scope_id='') OR (scope_type='PROJECT' AND scope_id=$5)))`, principal.Issuer, principal.Subject, principal.Type, roles, projectID).Scan(&allowed)
	return allowed, err
}

func loadProjectTx(ctx context.Context, tx pgx.Tx, projectID string, lock bool) (resource project.Resource, err error) {
	query := `SELECT project_id,project_name,delete_protection,project_revision,created_at,updated_at FROM kim.projects_current WHERE project_id=$1 AND lifecycle_state='ACTIVE'`
	if lock {
		query += ` FOR UPDATE`
	}
	err = tx.QueryRow(ctx, query, projectID).Scan(&resource.ID, &resource.Name, &resource.DeleteProtection, &resource.Revision, &resource.CreatedAt, &resource.UpdatedAt)
	return resource, err
}

func insertProjectAuditTx(ctx context.Context, tx pgx.Tx, principal project.Principal, requestID, action, resourceID string, revision uint64, scopeType, scopeID, result, reason, idempotencyDigest string) error {
	auditID, err := project.NewID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.northbound_audit_evidence(audit_id,request_id,principal_issuer,principal_subject,principal_type,action,resource_type,resource_id,scope_type,scope_id,resource_revision,result,reason_code,idempotency_digest)
		VALUES($1,$2,$3,$4,$5,$6,'PROJECT',$7,$8,$9,NULLIF($10,0),$11,$12,NULLIF($13,''))`, auditID, requestID, principal.Issuer, principal.Subject, principal.Type, action, resourceID, scopeType, scopeID, revision, result, reason, idempotencyDigest)
	return err
}

func projectDependenciesExistTx(ctx context.Context, tx pgx.Tx, projectID string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM kim.images_current WHERE owner_project_id=$1 AND lifecycle_state<>'DELETED') OR
		EXISTS(SELECT 1 FROM kim.flavors_current WHERE owner_project_id=$1 AND lifecycle_state<>'DELETED') OR
		EXISTS(SELECT 1 FROM kim.placement_admission_decisions WHERE project_id=$1) OR
		EXISTS(SELECT 1 FROM kim.networks_current WHERE project_id=$1 AND lifecycle_state<>'DISABLED') OR
		EXISTS(SELECT 1 FROM kim.volumes_current WHERE project_id=$1 AND lifecycle_state<>'DELETED') OR
		EXISTS(SELECT 1 FROM kim.virtual_machines_current WHERE project_id=$1 AND lifecycle_state<>'DELETED') OR
		EXISTS(SELECT 1 FROM kim.placement_scopes_current WHERE project_id=$1 AND lifecycle_state<>'RETIRED') OR
		EXISTS(SELECT 1 FROM kim.pci_vf_allocation_claims WHERE project_id=$1 AND claim_state<>'RELEASED')`, projectID).Scan(&exists)
	return exists, err
}
