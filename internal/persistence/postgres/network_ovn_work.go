package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

var ErrStaleOVNRuntimeClaim = errors.New("stale OVN runtime work claim")
var ErrOVNRuntimeClaimMaximumLifetime = errors.New("OVN runtime work claim maximum lifetime reached")

const MaxOVNRuntimeClaimLifetime = 24 * time.Hour

type OVNRuntimeClaimRequest struct {
	Owner           string
	Limit           int
	Lease           time.Duration
	MaximumLifetime time.Duration
}

type OVNRuntimeWork struct {
	WorkID, IntentID, PortID, ObjectSetDigest, ClaimMode string
	IntentGeneration, PortGeneration, BindingGeneration  uint64
	ClaimGeneration                                      uint64
	ClaimExpiresAt, ClaimMaximumExpiresAt                time.Time
	CanonicalObjectSet                                   []byte
}

type OVNRuntimeClaim struct {
	WorkID, Owner   string
	ClaimGeneration uint64
}

type OVNRuntimeRenewal struct {
	WorkID, Owner                               string
	ClaimGeneration, RenewalGeneration          uint64
	PriorExpiresAt, RenewedExpiresAt, MaximumAt time.Time
}

func ClaimOVNRuntimeWork(ctx context.Context, db TxBeginner, request OVNRuntimeClaimRequest) ([]OVNRuntimeWork, error) {
	maximumLifetime := request.MaximumLifetime
	if maximumLifetime == 0 {
		maximumLifetime = request.Lease
	}
	if request.Owner == "" || request.Limit < 1 || request.Limit > 100 || request.Lease <= 0 || maximumLifetime < request.Lease || maximumLifetime > MaxOVNRuntimeClaimLifetime {
		return nil, errors.New("bounded OVN runtime claim parameters are required")
	}
	var claimed []OVNRuntimeWork
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT work.work_id,work.intent_id,work.intent_generation,work.port_id,work.port_generation,
			 work.binding_generation,work.object_set_digest,work.work_state,work.claim_owner,work.claim_generation,
			 intent.canonical_object_set
			FROM kim.ovn_runtime_work_current work
			JOIN kim.network_ovn_state_current current ON current.port_id=work.port_id
			 AND current.intent_id=work.intent_id AND current.intent_generation=work.intent_generation
			 AND current.port_generation=work.port_generation AND current.binding_generation=work.binding_generation
			JOIN kim.network_intent_revision_evidence intent ON intent.intent_id=work.intent_id
			 AND intent.intent_generation=work.intent_generation AND intent.object_set_digest=work.object_set_digest
			WHERE work.work_state IN ('PENDING','DISPATCH_UNKNOWN')
			 OR (work.work_state='CLAIMED' AND work.claim_expires_at<=statement_timestamp())
			ORDER BY work.created_at,work.work_id
			FOR UPDATE OF work SKIP LOCKED LIMIT $1
		`, request.Limit)
		if err != nil {
			return err
		}
		type candidate struct {
			work            OVNRuntimeWork
			state           string
			priorOwner      *string
			priorGeneration *int64
		}
		var candidates []candidate
		for rows.Next() {
			var item candidate
			var intentGeneration, portGeneration, bindingGeneration int64
			if err := rows.Scan(&item.work.WorkID, &item.work.IntentID, &intentGeneration, &item.work.PortID,
				&portGeneration, &bindingGeneration, &item.work.ObjectSetDigest, &item.state,
				&item.priorOwner, &item.priorGeneration, &item.work.CanonicalObjectSet); err != nil {
				rows.Close()
				return err
			}
			item.work.IntentGeneration, item.work.PortGeneration, item.work.BindingGeneration = uint64(intentGeneration), uint64(portGeneration), uint64(bindingGeneration)
			candidates = append(candidates, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range candidates {
			canonical, _, err := ovnadapter.RestoreStoredPortPlan(item.work.CanonicalObjectSet, item.work.ObjectSetDigest)
			if err != nil {
				return fmt.Errorf("restore canonical OVN runtime plan: %w", err)
			}
			item.work.CanonicalObjectSet = canonical
			mode := "APPLY_ALLOWED"
			if item.state != "PENDING" {
				mode = "READ_BACK_FIRST"
			}
			if item.state == "CLAIMED" && item.priorOwner != nil && item.priorGeneration != nil {
				prior := OVNRuntimeClaim{WorkID: item.work.WorkID, Owner: *item.priorOwner, ClaimGeneration: uint64(*item.priorGeneration)}
				if err := appendOVNRuntimeEventTx(ctx, tx, prior, "DISPATCH_UNKNOWN", map[string]any{"reason": "claim_expired"}); err != nil {
					return err
				}
			}
			var generation int64
			var expires, maximumExpires time.Time
			if err := tx.QueryRow(ctx, `UPDATE kim.ovn_runtime_work_current SET
				work_state='CLAIMED',claim_owner=$2,claim_generation=last_claim_generation+1,
				last_claim_generation=last_claim_generation+1,
				claim_expires_at=statement_timestamp()+($3*interval '1 microsecond'),
				claim_maximum_expires_at=statement_timestamp()+($4*interval '1 microsecond'),last_renewal_generation=0,
				attempt_count=attempt_count+1,updated_at=statement_timestamp()
				WHERE work_id=$1 RETURNING claim_generation,claim_expires_at,claim_maximum_expires_at`, item.work.WorkID, request.Owner, request.Lease.Microseconds(), maximumLifetime.Microseconds()).Scan(&generation, &expires, &maximumExpires); err != nil {
				return err
			}
			item.work.ClaimMode, item.work.ClaimGeneration, item.work.ClaimExpiresAt, item.work.ClaimMaximumExpiresAt = mode, uint64(generation), expires, maximumExpires
			if _, err := tx.Exec(ctx, `INSERT INTO kim.ovn_runtime_work_attempt_evidence(
				work_id,claim_generation,claim_owner,claim_mode,lease_expires_at,maximum_expires_at
			) VALUES($1,$2,$3,$4,$5,$6)`, item.work.WorkID, generation, request.Owner, mode, expires, maximumExpires); err != nil {
				return err
			}
			claim := OVNRuntimeClaim{WorkID: item.work.WorkID, Owner: request.Owner, ClaimGeneration: uint64(generation)}
			if err := appendOVNRuntimeEventTx(ctx, tx, claim, "CLAIM_GRANTED", map[string]any{"claim_mode": mode, "lease_expires_at": expires.UTC().Format(time.RFC3339Nano)}); err != nil {
				return err
			}
			claimed = append(claimed, item.work)
		}
		return nil
	})
	return claimed, err
}

func RenewOVNRuntimeClaim(ctx context.Context, db TxBeginner, claim OVNRuntimeClaim, lease time.Duration) (OVNRuntimeRenewal, error) {
	if lease <= 0 || lease > MaxOVNRuntimeClaimLifetime {
		return OVNRuntimeRenewal{}, errors.New("bounded OVN runtime renewal lease is required")
	}
	var renewed OVNRuntimeRenewal
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if claim.WorkID == "" || claim.Owner == "" || claim.ClaimGeneration == 0 {
			return ErrStaleOVNRuntimeClaim
		}
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var prior, maximum time.Time
		var lastRenewal int64
		if err := tx.QueryRow(ctx, `SELECT claim_expires_at,claim_maximum_expires_at,last_renewal_generation
			FROM kim.ovn_runtime_work_current
			WHERE work_id=$1 AND work_state='CLAIMED' AND claim_owner=$2 AND claim_generation=$3
			 AND claim_expires_at>statement_timestamp() FOR UPDATE`, claim.WorkID, claim.Owner, claim.ClaimGeneration).Scan(&prior, &maximum, &lastRenewal); err != nil {
			return ErrStaleOVNRuntimeClaim
		}
		var next time.Time
		if err := tx.QueryRow(ctx, `SELECT LEAST($1::timestamptz,statement_timestamp()+($2*interval '1 microsecond'))`, maximum, lease.Microseconds()).Scan(&next); err != nil {
			return err
		}
		if !next.After(prior) {
			return ErrOVNRuntimeClaimMaximumLifetime
		}
		renewalGeneration := lastRenewal + 1
		if _, err := tx.Exec(ctx, `UPDATE kim.ovn_runtime_work_current SET
			claim_expires_at=$4,last_renewal_generation=$5,updated_at=statement_timestamp()
			WHERE work_id=$1 AND claim_owner=$2 AND claim_generation=$3`, claim.WorkID, claim.Owner, claim.ClaimGeneration, next, renewalGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.ovn_runtime_work_renewal_evidence(
			work_id,claim_generation,renewal_generation,claim_owner,prior_expires_at,renewed_expires_at,maximum_expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7)`, claim.WorkID, claim.ClaimGeneration, renewalGeneration, claim.Owner, prior, next, maximum); err != nil {
			return err
		}
		renewed = OVNRuntimeRenewal{WorkID: claim.WorkID, Owner: claim.Owner, ClaimGeneration: claim.ClaimGeneration,
			RenewalGeneration: uint64(renewalGeneration), PriorExpiresAt: prior, RenewedExpiresAt: next, MaximumAt: maximum}
		return nil
	})
	return renewed, err
}

func RecordOVNRuntimeReadBackStarted(ctx context.Context, db TxBeginner, claim OVNRuntimeClaim) error {
	return withCurrentOVNRuntimeClaim(ctx, db, claim, func(tx pgx.Tx) error {
		return appendOVNRuntimeEventTx(ctx, tx, claim, "READ_BACK_STARTED", map[string]any{"reason": "uncertain_prior_dispatch"})
	})
}

func AuthorizeOVNRuntimeApply(ctx context.Context, db TxBeginner, claim OVNRuntimeClaim) error {
	return withCurrentOVNRuntimeClaim(ctx, db, claim, func(tx pgx.Tx) error {
		var mode string
		if err := tx.QueryRow(ctx, `SELECT claim_mode FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1 AND claim_generation=$2`, claim.WorkID, claim.ClaimGeneration).Scan(&mode); err != nil {
			return ErrStaleOVNRuntimeClaim
		}
		if mode == "READ_BACK_FIRST" {
			var readBackStarted bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.ovn_runtime_work_event_evidence
				WHERE work_id=$1 AND claim_generation=$2 AND event_type='READ_BACK_STARTED')`, claim.WorkID, claim.ClaimGeneration).Scan(&readBackStarted); err != nil || !readBackStarted {
				return ErrStaleOVNRuntimeClaim
			}
		}
		return appendOVNRuntimeEventTx(ctx, tx, claim, "APPLY_AUTHORIZED", map[string]any{"claim_mode": mode})
	})
}

func QuarantineOVNRuntimeWork(ctx context.Context, db TxBeginner, claim OVNRuntimeClaim, reason string) error {
	if reason == "" {
		return errors.New("OVN runtime quarantine reason is required")
	}
	return withCurrentOVNRuntimeClaim(ctx, db, claim, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.ovn_runtime_work_current SET work_state='CONFLICTING',
			claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,claim_maximum_expires_at=NULL,updated_at=statement_timestamp()
			WHERE work_id=$1 AND claim_owner=$2 AND claim_generation=$3`, claim.WorkID, claim.Owner, claim.ClaimGeneration); err != nil {
			return err
		}
		return appendOVNRuntimeEventTx(ctx, tx, claim, "CONFLICT_QUARANTINED", map[string]any{"reason": reason})
	})
}

func CompleteOVNRuntimeWork(ctx context.Context, db TxBeginner, claim OVNRuntimeClaim, observed OVNPortObservation) error {
	if err := validateOVNPortObservation(observed); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockCurrentOVNRuntimeClaim(ctx, tx, claim); err != nil {
			return err
		}
		var intentID, portID string
		var intentGeneration, portGeneration, bindingGeneration int64
		if err := tx.QueryRow(ctx, `SELECT intent_id,intent_generation,port_id,port_generation,binding_generation
			FROM kim.ovn_runtime_work_current WHERE work_id=$1`, claim.WorkID).Scan(&intentID, &intentGeneration, &portID, &portGeneration, &bindingGeneration); err != nil {
			return err
		}
		if observed.IntentID != intentID || observed.IntentGeneration != uint64(intentGeneration) || observed.PortID != portID || observed.PortGeneration != uint64(portGeneration) || observed.BindingGeneration != uint64(bindingGeneration) {
			return ErrStaleOVNRuntimeClaim
		}
		if err := acceptOVNPortObservationTx(ctx, tx, observed); err != nil {
			return err
		}
		state, eventType := "DISPATCH_UNKNOWN", "OBSERVATION_ACCEPTED"
		if observed.Observation.NBState() == "CONFLICTING" || observed.Observation.SBState() == "CONFLICTING" {
			state, eventType = "CONFLICTING", "CONFLICT_QUARANTINED"
		} else if observed.Observation.NBState() == "MATCHED" && observed.Observation.SBState() == "MATCHED" {
			state = "OBSERVED"
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.ovn_runtime_work_current SET work_state=$4,
			claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,claim_maximum_expires_at=NULL,terminal_observation_id=$5,updated_at=statement_timestamp()
			WHERE work_id=$1 AND claim_owner=$2 AND claim_generation=$3`, claim.WorkID, claim.Owner, claim.ClaimGeneration, state, observed.SBObservationID); err != nil {
			return err
		}
		return appendOVNRuntimeEventTx(ctx, tx, claim, eventType, map[string]any{"nb_state": observed.Observation.NBState(), "sb_state": observed.Observation.SBState(), "work_state": state})
	})
}

func withCurrentOVNRuntimeClaim(ctx context.Context, db TxBeginner, claim OVNRuntimeClaim, action func(pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockCurrentOVNRuntimeClaim(ctx, tx, claim); err != nil {
			return err
		}
		return action(tx)
	})
}

func lockCurrentOVNRuntimeClaim(ctx context.Context, tx pgx.Tx, claim OVNRuntimeClaim) error {
	if claim.WorkID == "" || claim.Owner == "" || claim.ClaimGeneration == 0 {
		return ErrStaleOVNRuntimeClaim
	}
	if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
		return err
	}
	var workID string
	if err := tx.QueryRow(ctx, `SELECT work_id FROM kim.ovn_runtime_work_current
		WHERE work_id=$1 AND work_state='CLAIMED' AND claim_owner=$2 AND claim_generation=$3
		 AND claim_expires_at>statement_timestamp() FOR UPDATE`, claim.WorkID, claim.Owner, claim.ClaimGeneration).Scan(&workID); err != nil || workID != claim.WorkID {
		return ErrStaleOVNRuntimeClaim
	}
	return nil
}

func appendOVNRuntimeEventTx(ctx context.Context, tx pgx.Tx, claim OVNRuntimeClaim, eventType string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	digestBytes := sha256.Sum256(encoded)
	digest := hex.EncodeToString(digestBytes[:])
	tag, err := tx.Exec(ctx, `INSERT INTO kim.ovn_runtime_work_event_evidence(
		work_id,claim_generation,event_type,event_payload,event_payload_digest
	) VALUES($1,$2,$3,$4,$5) ON CONFLICT(work_id,claim_generation,event_type) DO NOTHING`, claim.WorkID, claim.ClaimGeneration, eventType, encoded, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var accepted string
	if err := tx.QueryRow(ctx, `SELECT event_payload_digest FROM kim.ovn_runtime_work_event_evidence
		WHERE work_id=$1 AND claim_generation=$2 AND event_type=$3`, claim.WorkID, claim.ClaimGeneration, eventType).Scan(&accepted); err != nil {
		return err
	}
	if accepted != digest {
		return fmt.Errorf("OVN runtime evidence conflict: %w", ErrPlacementConflict)
	}
	return nil
}
