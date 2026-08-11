package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func acquireHostDisruptiveOperationClaimTx(ctx context.Context, tx pgx.Tx, hostID, domain, authorityID, targetID, operation string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "host-disruptive-operation/"+hostID); err != nil {
		return err
	}
	var currentDomain, currentAuthority, currentTarget, state string
	var generation int64
	err := tx.QueryRow(ctx, `SELECT operation_domain,authority_id,target_id,claim_state,claim_generation
		FROM kim.host_disruptive_operation_claims_current WHERE host_id=$1 FOR UPDATE`, hostID).Scan(
		&currentDomain, &currentAuthority, &currentTarget, &state, &generation)
	if err == nil && state == "ACTIVE" && (currentDomain != domain || currentAuthority != authorityID || currentTarget != targetID) {
		return ErrHostDisruptiveOperationConflict
	}
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	generation++
	digest := digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%s\n%s\n%s\n%s", hostID, generation, domain, authorityID, targetID, operation)))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.host_disruptive_operation_claim_evidence(
		host_id,claim_generation,operation_domain,authority_id,target_id,operation_type,evidence_digest
	) VALUES($1,$2,$3,$4,$5,$6,$7)`, hostID, generation, domain, authorityID, targetID, operation, digest); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.host_disruptive_operation_claims_current(
		host_id,claim_generation,operation_domain,authority_id,target_id,operation_type,claim_state,evidence_digest
	) VALUES($1,$2,$3,$4,$5,$6,'ACTIVE',$7) ON CONFLICT(host_id) DO UPDATE SET
		claim_generation=EXCLUDED.claim_generation,operation_domain=EXCLUDED.operation_domain,
		authority_id=EXCLUDED.authority_id,target_id=EXCLUDED.target_id,operation_type=EXCLUDED.operation_type,
		claim_state='ACTIVE',evidence_digest=EXCLUDED.evidence_digest,updated_at=statement_timestamp()`,
		hostID, generation, domain, authorityID, targetID, operation, digest)
	return err
}

func releaseHostDisruptiveOperationClaimTx(ctx context.Context, tx pgx.Tx, hostID, domain, authorityID, targetID string) error {
	command, err := tx.Exec(ctx, `UPDATE kim.host_disruptive_operation_claims_current SET claim_state='RELEASED',
		updated_at=statement_timestamp() WHERE host_id=$1 AND operation_domain=$2 AND authority_id=$3
		AND target_id=$4 AND claim_state='ACTIVE'`, hostID, domain, authorityID, targetID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrHostDisruptiveOperationConflict
	}
	return nil
}
