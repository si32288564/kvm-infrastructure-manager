package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

// authorizeNorthboundTx applies the shared SYSTEM/PROJECT role lattice. The
// caller remains responsible for resolving the resource-specific Project ID.
func authorizeNorthboundTx(ctx context.Context, tx pgx.Tx, principal resource.Principal, action, projectID string) (bool, error) {
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

func auditTx(ctx context.Context, tx pgx.Tx, principal resource.Principal, requestID, action, resourceType, resourceID string, revision uint64, scopeType, scopeID, result, reason, digest string) error {
	id, err := resource.NewID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.northbound_audit_evidence(audit_id,request_id,principal_issuer,principal_subject,principal_type,action,resource_type,resource_id,scope_type,scope_id,resource_revision,result,reason_code,idempotency_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,0),$12,$13,NULLIF($14,''))`, id, requestID, principal.Issuer, principal.Subject, principal.Type, action, resourceType, resourceID, scopeType, scopeID, revision, result, reason, digest)
	return err
}
