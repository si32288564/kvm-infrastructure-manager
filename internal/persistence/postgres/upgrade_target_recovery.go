package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrUpgradeTargetRecoveryUnavailable = errors.New("upgrade target recovery authority is unavailable")
var ErrStaleUpgradeTargetRecoveryClaim = errors.New("upgrade target recovery claim is stale")
var ErrUpgradeTargetRecoveryConflict = errors.New("upgrade target recovery evidence conflicts with current authority")

const UpgradeRecoveryConfigureExisting = "CONFIGURE_EXISTING"

type UpgradeTargetRecoveryPlanRequest struct {
	RecoveryPlanID, TargetID, Strategy, AuthorizationID, AuthorizationDigest string
	RecoveryProfileRevision                                                  uint64
}

type UpgradeTargetRecoveryPlan struct {
	RecoveryPlanID, TargetID, Strategy, PlanDigest string
	RecoveryGeneration, SourceAttemptGeneration    uint64
}

type UpgradeTargetRecoveryClaimRequest struct {
	TargetID, Owner        string
	Lease, MaximumLifetime time.Duration
}

type UpgradeTargetRecoveryClaim struct {
	RecoveryPlanID, TargetID, Owner, Strategy, AttemptMode         string
	ComponentType, ComponentID, TargetArtifactDigest, TargetDigest string
	PlanDigest, AuthorizationDigest                                string
	RecoveryGeneration, AttemptGeneration, RecoveryProfileRevision uint64
	ExpiresAt, MaximumExpiresAt                                    time.Time
}

type UpgradeTargetRecoveryRenewRequest struct {
	TargetID, Owner                       string
	RecoveryGeneration, AttemptGeneration uint64
	Extension                             time.Duration
}

type UpgradeTargetRecoveryRenewal struct {
	RenewalGeneration uint64
	ExpiresAt         time.Time
}

type UpgradeTargetRecoveryObservationRequest struct {
	TargetID, Owner, ObservationState, ObservedCondition, ObservedDigest string
	RecoveryGeneration, AttemptGeneration                                uint64
}

type UpgradeTargetRecoveryObservationDecision struct{ Action string }

type UpgradeTargetRecoveryCompletionRequest struct {
	TargetID, Owner, Outcome, ResultDigest, ObservedDigest string
	RecoveryGeneration, AttemptGeneration                  uint64
}

type UpgradeTargetRecoveryRearmRequest struct {
	TargetID, AuthorizationID, AuthorizationDigest string
	RecoveryGeneration                             uint64
}

func ApproveUpgradeTargetRecoveryPlan(ctx context.Context, db TxBeginner, request UpgradeTargetRecoveryPlanRequest) (UpgradeTargetRecoveryPlan, error) {
	if request.RecoveryPlanID == "" || request.TargetID == "" || request.Strategy != UpgradeRecoveryConfigureExisting ||
		request.AuthorizationID == "" || !validDigest(request.AuthorizationDigest) || request.RecoveryProfileRevision == 0 {
		return UpgradeTargetRecoveryPlan{}, ErrUpgradeTargetRecoveryUnavailable
	}
	var approved UpgradeTargetRecoveryPlan
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var existingTarget, existingStrategy, existingAuthorizationID, existingAuthorizationDigest, existingPlanDigest string
		var existingGeneration, existingSourceAttempt, existingProfileRevision int64
		existingErr := tx.QueryRow(ctx, `SELECT target_id,recovery_generation,source_attempt_generation,strategy,
			authorization_id,authorization_digest,recovery_profile_revision,plan_digest
			FROM kim.upgrade_target_recovery_plan_evidence WHERE recovery_plan_id=$1`, request.RecoveryPlanID).Scan(
			&existingTarget, &existingGeneration, &existingSourceAttempt, &existingStrategy, &existingAuthorizationID,
			&existingAuthorizationDigest, &existingProfileRevision, &existingPlanDigest)
		if existingErr == nil {
			if existingTarget == request.TargetID && existingStrategy == request.Strategy &&
				existingAuthorizationID == request.AuthorizationID && existingAuthorizationDigest == request.AuthorizationDigest &&
				uint64(existingProfileRevision) == request.RecoveryProfileRevision {
				approved = UpgradeTargetRecoveryPlan{RecoveryPlanID: request.RecoveryPlanID, TargetID: request.TargetID,
					Strategy: request.Strategy, PlanDigest: existingPlanDigest, RecoveryGeneration: uint64(existingGeneration),
					SourceAttemptGeneration: uint64(existingSourceAttempt)}
				return nil
			}
			return ErrUpgradeTargetRecoveryConflict
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		var targetState, executionState, targetDigest, artifactDigest string
		var sourceAttempt int64
		if err := tx.QueryRow(ctx, `SELECT target.target_state,execution.execution_state,execution.attempt_generation,
			evidence.target_digest,evidence.target_artifact_digest
			FROM kim.upgrade_targets_current target
			JOIN kim.upgrade_target_executions_current execution USING(target_id)
			JOIN kim.upgrade_target_evidence evidence USING(target_id)
			WHERE target.target_id=$1 FOR UPDATE OF target,execution`, request.TargetID).Scan(
			&targetState, &executionState, &sourceAttempt, &targetDigest, &artifactDigest); err != nil {
			return err
		}
		if targetState != "FENCED" || executionState != "FENCED" || sourceAttempt <= 0 {
			return ErrUpgradeTargetRecoveryUnavailable
		}
		var observationID, observationDigest string
		if err := tx.QueryRow(ctx, `SELECT observation.observation_id,observation.observed_digest
			FROM kim.upgrade_target_observation_evidence observation
			JOIN kim.upgrade_target_execution_event_evidence event ON event.target_id=observation.target_id
			 AND event.attempt_generation=observation.attempt_generation AND event.event_type='CONFLICT_QUARANTINED'
			WHERE observation.target_id=$1 AND observation.attempt_generation=$2
			 AND observation.observation_state='CONFLICTING'
			ORDER BY observation.observed_at DESC LIMIT 1`, request.TargetID, sourceAttempt).Scan(&observationID, &observationDigest); err != nil {
			return ErrUpgradeTargetRecoveryUnavailable
		}
		var priorGeneration int64
		var priorState string
		err := tx.QueryRow(ctx, `SELECT recovery_generation,recovery_state FROM kim.upgrade_target_recoveries_current
			WHERE target_id=$1 FOR UPDATE`, request.TargetID).Scan(&priorGeneration, &priorState)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && priorState != "FAILED" && priorState != "FENCED" && priorState != "REARMED" {
			return ErrUpgradeTargetRecoveryUnavailable
		}
		generation := priorGeneration + 1
		identity := fmt.Sprintf("%s:%s:%d:%d:%s:%s:%s:%s", request.RecoveryPlanID, request.TargetID, generation,
			request.RecoveryProfileRevision, request.Strategy, request.AuthorizationDigest, targetDigest, observationDigest)
		planDigest := digestReleaseBytes([]byte(identity))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_plan_evidence(
			recovery_plan_id,target_id,recovery_generation,source_attempt_generation,source_observation_id,
			strategy,authorization_id,authorization_digest,recovery_profile_revision,target_digest,
			target_artifact_digest,plan_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, request.RecoveryPlanID, request.TargetID,
			generation, sourceAttempt, observationID, request.Strategy, request.AuthorizationID,
			request.AuthorizationDigest, request.RecoveryProfileRevision, targetDigest, artifactDigest, planDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recoveries_current(
			target_id,recovery_plan_id,recovery_generation,recovery_state
		) VALUES($1,$2,$3,'APPROVED') ON CONFLICT (target_id) DO UPDATE SET
			recovery_plan_id=EXCLUDED.recovery_plan_id,recovery_generation=EXCLUDED.recovery_generation,
			recovery_state='APPROVED',executor_owner=NULL,attempt_generation=0,attempt_mode=NULL,
			renewal_generation=0,claim_expires_at=NULL,maximum_expires_at=NULL,updated_at=statement_timestamp()
			WHERE kim.upgrade_target_recoveries_current.recovery_state IN ('FAILED','FENCED','REARMED')`,
			request.TargetID, request.RecoveryPlanID, generation); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-recovery:%s:plan:%d", request.TargetID, generation)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_event_evidence(
			event_id,target_id,recovery_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'PLAN_APPROVED',$4)`, eventID, request.TargetID, generation,
			digestReleaseBytes([]byte(eventID))); err != nil {
			return err
		}
		approved = UpgradeTargetRecoveryPlan{RecoveryPlanID: request.RecoveryPlanID, TargetID: request.TargetID,
			Strategy: request.Strategy, PlanDigest: planDigest, RecoveryGeneration: uint64(generation),
			SourceAttemptGeneration: uint64(sourceAttempt)}
		return nil
	})
	return approved, err
}

func ClaimUpgradeTargetRecovery(ctx context.Context, db TxBeginner, request UpgradeTargetRecoveryClaimRequest) (UpgradeTargetRecoveryClaim, error) {
	if request.TargetID == "" || request.Owner == "" || request.Lease <= 0 || request.MaximumLifetime < request.Lease {
		return UpgradeTargetRecoveryClaim{}, ErrUpgradeTargetRecoveryUnavailable
	}
	var claim UpgradeTargetRecoveryClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var recoveryPlanID, recoveryState, strategy, planDigest, authorizationDigest string
		var targetState, executionState, componentType, componentID, artifactDigest, targetDigest string
		var owner, mode *string
		var recoveryGeneration, priorAttempt, profileRevision int64
		var expiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT current.recovery_plan_id,current.recovery_generation,current.recovery_state,
			current.executor_owner,current.attempt_generation,current.attempt_mode,current.claim_expires_at,
			plan.strategy,plan.plan_digest,plan.authorization_digest,plan.recovery_profile_revision,
			target.target_state,execution.execution_state,evidence.component_type,evidence.component_id,
			evidence.target_artifact_digest,evidence.target_digest
			FROM kim.upgrade_target_recoveries_current current
			JOIN kim.upgrade_target_recovery_plan_evidence plan ON plan.recovery_plan_id=current.recovery_plan_id
			JOIN kim.upgrade_targets_current target ON target.target_id=current.target_id
			JOIN kim.upgrade_target_executions_current execution ON execution.target_id=current.target_id
			JOIN kim.upgrade_target_evidence evidence ON evidence.target_id=current.target_id
			WHERE current.target_id=$1 FOR UPDATE OF current,target,execution`, request.TargetID).Scan(
			&recoveryPlanID, &recoveryGeneration, &recoveryState, &owner, &priorAttempt, &mode, &expiry,
			&strategy, &planDigest, &authorizationDigest, &profileRevision, &targetState, &executionState,
			&componentType, &componentID, &artifactDigest, &targetDigest); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if targetState != "FENCED" || executionState != "FENCED" ||
			(recoveryState != "APPROVED" && recoveryState != "UNKNOWN" && recoveryState != "CLAIMED") ||
			(owner != nil && expiry != nil && expiry.After(now)) {
			return ErrUpgradeTargetRecoveryUnavailable
		}
		generation := priorAttempt + 1
		if priorAttempt > 0 {
			eventID := fmt.Sprintf("upgrade-recovery:%s:unknown:%d:%d", request.TargetID, recoveryGeneration, priorAttempt)
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_event_evidence(
				event_id,target_id,recovery_generation,attempt_generation,event_type,evidence_digest
			) VALUES($1,$2,$3,$4,'RECOVERY_UNKNOWN',$5) ON CONFLICT DO NOTHING`, eventID, request.TargetID,
				recoveryGeneration, priorAttempt, digestReleaseBytes([]byte(eventID))); err != nil {
				return err
			}
		}
		expires, maximum := now.Add(request.Lease), now.Add(request.MaximumLifetime)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_attempt_evidence(
			target_id,recovery_generation,attempt_generation,recovery_plan_id,executor_owner,attempt_mode,
			lease_expires_at,maximum_expires_at,plan_digest,authorization_digest
		) VALUES($1,$2,$3,$4,$5,'READ_BACK_FIRST',$6,$7,$8,$9)`, request.TargetID, recoveryGeneration,
			generation, recoveryPlanID, request.Owner, expires, maximum, planDigest, authorizationDigest); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-recovery:%s:claim:%d:%d", request.TargetID, recoveryGeneration, generation)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_event_evidence(
			event_id,target_id,recovery_generation,attempt_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,$4,'CLAIM_GRANTED',$5)`, eventID, request.TargetID, recoveryGeneration, generation,
			digestReleaseBytes([]byte(eventID))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_recoveries_current SET recovery_state='CLAIMED',
			executor_owner=$2,attempt_generation=$3,attempt_mode='READ_BACK_FIRST',renewal_generation=0,
			claim_expires_at=$4,maximum_expires_at=$5,updated_at=statement_timestamp() WHERE target_id=$1`,
			request.TargetID, request.Owner, generation, expires, maximum); err != nil {
			return err
		}
		claim = UpgradeTargetRecoveryClaim{RecoveryPlanID: recoveryPlanID, TargetID: request.TargetID, Owner: request.Owner,
			Strategy: strategy, AttemptMode: "READ_BACK_FIRST", ComponentType: componentType, ComponentID: componentID,
			TargetArtifactDigest: artifactDigest, TargetDigest: targetDigest, PlanDigest: planDigest,
			AuthorizationDigest: authorizationDigest, RecoveryGeneration: uint64(recoveryGeneration),
			AttemptGeneration: uint64(generation), RecoveryProfileRevision: uint64(profileRevision),
			ExpiresAt: expires, MaximumExpiresAt: maximum}
		return nil
	})
	return claim, err
}

func RenewUpgradeTargetRecoveryClaim(ctx context.Context, db TxBeginner, request UpgradeTargetRecoveryRenewRequest) (UpgradeTargetRecoveryRenewal, error) {
	if request.TargetID == "" || request.Owner == "" || request.RecoveryGeneration == 0 ||
		request.AttemptGeneration == 0 || request.Extension <= 0 {
		return UpgradeTargetRecoveryRenewal{}, ErrStaleUpgradeTargetRecoveryClaim
	}
	var renewal UpgradeTargetRecoveryRenewal
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var owner *string
		var recoveryGeneration, attempt, renewalGeneration int64
		var prior, maximum *time.Time
		if err := tx.QueryRow(ctx, `SELECT executor_owner,recovery_generation,attempt_generation,renewal_generation,
			claim_expires_at,maximum_expires_at FROM kim.upgrade_target_recoveries_current
			WHERE target_id=$1 FOR UPDATE`, request.TargetID).Scan(&owner, &recoveryGeneration, &attempt,
			&renewalGeneration, &prior, &maximum); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if owner == nil || *owner != request.Owner || uint64(recoveryGeneration) != request.RecoveryGeneration ||
			uint64(attempt) != request.AttemptGeneration || prior == nil || maximum == nil || !prior.After(now) {
			return ErrStaleUpgradeTargetRecoveryClaim
		}
		renewed := now.Add(request.Extension)
		if renewed.After(*maximum) {
			renewed = *maximum
		}
		if !renewed.After(*prior) {
			renewal = UpgradeTargetRecoveryRenewal{RenewalGeneration: uint64(renewalGeneration), ExpiresAt: *prior}
			return nil
		}
		generation := renewalGeneration + 1
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_renewal_evidence(
			target_id,recovery_generation,attempt_generation,renewal_generation,executor_owner,
			prior_expires_at,renewed_expires_at,maximum_expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, request.TargetID, recoveryGeneration, attempt, generation,
			request.Owner, *prior, renewed, *maximum); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_recoveries_current SET claim_expires_at=$2,
			renewal_generation=$3,updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID,
			renewed, generation); err != nil {
			return err
		}
		renewal = UpgradeTargetRecoveryRenewal{RenewalGeneration: uint64(generation), ExpiresAt: renewed}
		return nil
	})
	return renewal, err
}

func ObserveUpgradeTargetRecovery(ctx context.Context, db TxBeginner, request UpgradeTargetRecoveryObservationRequest) (UpgradeTargetRecoveryObservationDecision, error) {
	validObservation := (request.ObservationState == "MATCHED" && request.ObservedCondition == "DESIRED_RELEASE_MATCHED") ||
		(request.ObservationState == "CONFLICTING" && (request.ObservedCondition == "PACKAGE_HALF_CONFIGURED" ||
			request.ObservedCondition == "PACKAGE_STATE_CONFLICT")) ||
		(request.ObservationState == "UNKNOWN" && request.ObservedCondition == "OBSERVATION_UNKNOWN")
	if request.TargetID == "" || request.Owner == "" || request.RecoveryGeneration == 0 ||
		request.AttemptGeneration == 0 || !validObservation || !validDigest(request.ObservedDigest) {
		return UpgradeTargetRecoveryObservationDecision{}, ErrUpgradeTargetRecoveryConflict
	}
	var decision UpgradeTargetRecoveryObservationDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		strategy, _, err := requireCurrentUpgradeTargetRecoveryClaim(ctx, tx, request.TargetID, request.Owner,
			request.RecoveryGeneration, request.AttemptGeneration)
		if err != nil {
			return err
		}
		identity := fmt.Sprintf("%s:%d:%d:%s:%s:%s", request.TargetID, request.RecoveryGeneration,
			request.AttemptGeneration, request.ObservationState, request.ObservedCondition, request.ObservedDigest)
		observationID := "upgrade-recovery-observation:" + digestReleaseBytes([]byte(identity))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_observation_evidence(
			observation_id,target_id,recovery_generation,attempt_generation,observation_state,observed_condition,
			observed_digest,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, observationID, request.TargetID,
			request.RecoveryGeneration, request.AttemptGeneration, request.ObservationState, request.ObservedCondition,
			request.ObservedDigest, digestReleaseBytes([]byte(identity))); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-recovery:%s:read-back:%s", request.TargetID, digestReleaseBytes([]byte(identity)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_event_evidence(
			event_id,target_id,recovery_generation,attempt_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,$4,'READ_BACK_STARTED',$5) ON CONFLICT DO NOTHING`, eventID, request.TargetID,
			request.RecoveryGeneration, request.AttemptGeneration, digestReleaseBytes([]byte(eventID))); err != nil {
			return err
		}
		switch {
		case request.ObservationState == "MATCHED":
			decision.Action = "COMPLETE_VERIFIED"
		case request.ObservationState == "CONFLICTING" && request.ObservedCondition == "PACKAGE_HALF_CONFIGURED" &&
			strategy == UpgradeRecoveryConfigureExisting:
			if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_recoveries_current SET
				attempt_mode='RECOVERY_APPLY_ALLOWED',updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
				return err
			}
			authorizeID := fmt.Sprintf("upgrade-recovery:%s:apply:%d:%d", request.TargetID,
				request.RecoveryGeneration, request.AttemptGeneration)
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_event_evidence(
				event_id,target_id,recovery_generation,attempt_generation,event_type,evidence_digest
			) VALUES($1,$2,$3,$4,'RECOVERY_APPLY_AUTHORIZED',$5) ON CONFLICT DO NOTHING`, authorizeID,
				request.TargetID, request.RecoveryGeneration, request.AttemptGeneration,
				digestReleaseBytes([]byte(authorizeID))); err != nil {
				return err
			}
			decision.Action = "RECOVERY_APPLY_AUTHORIZED"
		case request.ObservationState == "CONFLICTING":
			if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_recoveries_current SET recovery_state='FENCED',
				executor_owner=NULL,attempt_mode=NULL,claim_expires_at=NULL,maximum_expires_at=NULL,
				updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
				return err
			}
			decision.Action = "FENCE_CONFLICTING"
		default:
			decision.Action = "BLOCKED"
		}
		return nil
	})
	return decision, err
}

func CompleteUpgradeTargetRecovery(ctx context.Context, db TxBeginner, request UpgradeTargetRecoveryCompletionRequest) error {
	if request.TargetID == "" || request.Owner == "" || request.Outcome != "VERIFIED" ||
		request.RecoveryGeneration == 0 || request.AttemptGeneration == 0 ||
		!validDigest(request.ResultDigest) || !validDigest(request.ObservedDigest) {
		return ErrUpgradeTargetRecoveryConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var existingOutcome, existingResult, existingObserved string
		existingErr := tx.QueryRow(ctx, `SELECT outcome,result_digest,observed_digest
			FROM kim.upgrade_target_recovery_result_evidence
			WHERE target_id=$1 AND recovery_generation=$2 AND attempt_generation=$3`, request.TargetID,
			request.RecoveryGeneration, request.AttemptGeneration).Scan(&existingOutcome, &existingResult, &existingObserved)
		if existingErr == nil {
			if existingOutcome == request.Outcome && existingResult == request.ResultDigest && existingObserved == request.ObservedDigest {
				return nil
			}
			return ErrUpgradeTargetRecoveryConflict
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		if _, _, err := requireCurrentUpgradeTargetRecoveryClaim(ctx, tx, request.TargetID, request.Owner,
			request.RecoveryGeneration, request.AttemptGeneration); err != nil {
			return err
		}
		var matched bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.upgrade_target_recovery_observation_evidence
			WHERE target_id=$1 AND recovery_generation=$2 AND attempt_generation=$3
			 AND observation_state='MATCHED' AND observed_condition='DESIRED_RELEASE_MATCHED' AND observed_digest=$4)`,
			request.TargetID, request.RecoveryGeneration, request.AttemptGeneration, request.ObservedDigest).Scan(&matched); err != nil || !matched {
			return ErrUpgradeTargetRecoveryConflict
		}
		resultID := fmt.Sprintf("upgrade-recovery-result:%s:%d:%d:%s", request.TargetID,
			request.RecoveryGeneration, request.AttemptGeneration, request.ResultDigest)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_result_evidence(
			result_id,target_id,recovery_generation,attempt_generation,outcome,result_digest,observed_digest
		) VALUES($1,$2,$3,$4,'VERIFIED',$5,$6)`, resultID, request.TargetID, request.RecoveryGeneration,
			request.AttemptGeneration, request.ResultDigest, request.ObservedDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_recoveries_current SET recovery_state='VERIFIED',
			executor_owner=NULL,attempt_mode=NULL,claim_expires_at=NULL,maximum_expires_at=NULL,
			updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-recovery:%s:result:%d:%d", request.TargetID,
			request.RecoveryGeneration, request.AttemptGeneration)
		_, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_event_evidence(
			event_id,target_id,recovery_generation,attempt_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,$4,'RESULT_ACCEPTED',$5)`, eventID, request.TargetID, request.RecoveryGeneration,
			request.AttemptGeneration, digestReleaseBytes([]byte(eventID)))
		return err
	})
}

func RearmUpgradeTargetAfterRecovery(ctx context.Context, db TxBeginner, request UpgradeTargetRecoveryRearmRequest) error {
	if request.TargetID == "" || request.AuthorizationID == "" || request.RecoveryGeneration == 0 ||
		!validDigest(request.AuthorizationDigest) {
		return ErrUpgradeTargetRecoveryUnavailable
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var existingAuthorizationID, existingAuthorizationDigest string
		existingErr := tx.QueryRow(ctx, `SELECT authorization_id,authorization_digest
			FROM kim.upgrade_target_recovery_rearm_evidence WHERE target_id=$1 AND recovery_generation=$2`,
			request.TargetID, request.RecoveryGeneration).Scan(&existingAuthorizationID, &existingAuthorizationDigest)
		if existingErr == nil {
			if existingAuthorizationID == request.AuthorizationID && existingAuthorizationDigest == request.AuthorizationDigest {
				return nil
			}
			return ErrUpgradeTargetRecoveryConflict
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		var recoveryState, targetState, executionState, resultID string
		var generation int64
		if err := tx.QueryRow(ctx, `SELECT recovery.recovery_state,recovery.recovery_generation,
			target.target_state,execution.execution_state,result.result_id
			FROM kim.upgrade_target_recoveries_current recovery
			JOIN kim.upgrade_targets_current target USING(target_id)
			JOIN kim.upgrade_target_executions_current execution USING(target_id)
			JOIN kim.upgrade_target_recovery_result_evidence result ON result.target_id=recovery.target_id
			 AND result.recovery_generation=recovery.recovery_generation AND result.outcome='VERIFIED'
			WHERE recovery.target_id=$1 FOR UPDATE OF recovery,target,execution`, request.TargetID).Scan(
			&recoveryState, &generation, &targetState, &executionState, &resultID); err != nil {
			return err
		}
		if recoveryState != "VERIFIED" || uint64(generation) != request.RecoveryGeneration ||
			targetState != "FENCED" || executionState != "FENCED" {
			return ErrUpgradeTargetRecoveryUnavailable
		}
		identity := fmt.Sprintf("%s:%d:%s:%s:%s", request.TargetID, request.RecoveryGeneration,
			resultID, request.AuthorizationID, request.AuthorizationDigest)
		rearmID := "upgrade-recovery-rearm:" + digestReleaseBytes([]byte(identity))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_rearm_evidence(
			rearm_id,target_id,recovery_generation,recovery_result_id,authorization_id,authorization_digest,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7)`, rearmID, request.TargetID, request.RecoveryGeneration,
			resultID, request.AuthorizationID, request.AuthorizationDigest, digestReleaseBytes([]byte(identity))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_recoveries_current SET recovery_state='REARMED',
			updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_targets_current SET target_state='PENDING',result_digest=NULL,
			updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_executions_current SET execution_state='PENDING',
			executor_owner=NULL,attempt_mode=NULL,claim_expires_at=NULL,maximum_expires_at=NULL,
			updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-recovery:%s:rearm:%d", request.TargetID, request.RecoveryGeneration)
		_, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_recovery_event_evidence(
			event_id,target_id,recovery_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'REARM_AUTHORIZED',$4)`, eventID, request.TargetID, request.RecoveryGeneration,
			digestReleaseBytes([]byte(eventID)))
		return err
	})
}

func requireCurrentUpgradeTargetRecoveryClaim(ctx context.Context, tx pgx.Tx, targetID, owner string,
	recoveryGeneration, attemptGeneration uint64) (string, string, error) {
	if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
		return "", "", err
	}
	var currentOwner, mode *string
	var state, strategy string
	var currentRecovery, currentAttempt int64
	var expiry *time.Time
	if err := tx.QueryRow(ctx, `SELECT current.executor_owner,current.recovery_generation,current.attempt_generation,
		current.attempt_mode,current.claim_expires_at,current.recovery_state,plan.strategy
		FROM kim.upgrade_target_recoveries_current current
		JOIN kim.upgrade_target_recovery_plan_evidence plan ON plan.recovery_plan_id=current.recovery_plan_id
		WHERE current.target_id=$1 FOR UPDATE OF current`, targetID).Scan(&currentOwner, &currentRecovery,
		&currentAttempt, &mode, &expiry, &state, &strategy); err != nil {
		return "", "", err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		return "", "", err
	}
	if currentOwner == nil || mode == nil || *currentOwner != owner || uint64(currentRecovery) != recoveryGeneration ||
		uint64(currentAttempt) != attemptGeneration || expiry == nil || !expiry.After(now) || state != "CLAIMED" {
		return "", "", ErrStaleUpgradeTargetRecoveryClaim
	}
	return strategy, *mode, nil
}
