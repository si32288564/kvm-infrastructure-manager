package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrUpgradeTargetClaimUnavailable = errors.New("upgrade target execution claim is unavailable")
var ErrUpgradeTargetAlreadyCompleted = errors.New("upgrade target execution is already completed")
var ErrStaleUpgradeTargetClaim = errors.New("upgrade target execution claim is stale")
var ErrUpgradeTargetObservationConflict = errors.New("upgrade target observation conflicts with current authority")

type UpgradeTargetClaimRequest struct {
	CampaignID, TargetID, Owner string
	Lease, MaximumLifetime      time.Duration
}

type UpgradeTargetClaim struct {
	CampaignID, TargetID, Owner, AttemptMode, WaveID               string
	ComponentType, ComponentID, TargetArtifactDigest, TargetDigest string
	AttemptGeneration, CoordinatorClaimGeneration, PlanRevision    uint64
	ExpiresAt, MaximumExpiresAt                                    time.Time
}

type UpgradeTargetRenewRequest struct {
	TargetID, Owner   string
	AttemptGeneration uint64
	Extension         time.Duration
}

type UpgradeTargetRenewal struct {
	RenewalGeneration uint64
	ExpiresAt         time.Time
}

type UpgradeTargetObservationRequest struct {
	TargetID, Owner, ObservationState, ObservedDigest string
	AttemptGeneration                                 uint64
}

type UpgradeTargetObservationDecision struct {
	Action string
}

type UpgradeTargetCompletionRequest struct {
	TargetID, Owner, Outcome, ResultDigest, ObservedDigest string
	AttemptGeneration                                      uint64
}

func ClaimUpgradeTarget(ctx context.Context, db TxBeginner, request UpgradeTargetClaimRequest) (UpgradeTargetClaim, error) {
	if request.CampaignID == "" || request.TargetID == "" || request.Owner == "" || request.Lease <= 0 || request.MaximumLifetime < request.Lease {
		return UpgradeTargetClaim{}, ErrUpgradeTargetClaimUnavailable
	}
	var claim UpgradeTargetClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var campaignState, waveID, targetWaveID, componentType, componentID, artifactDigest, targetDigest, targetState, executionState string
		var planRevision, coordinatorGeneration, priorAttempt int64
		var coordinatorOwner, priorOwner *string
		var coordinatorExpiry, priorExpiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT campaign.campaign_state,campaign.current_wave_id,campaign.plan_revision,
			campaign.coordinator_owner,campaign.coordinator_claim_generation,campaign.coordinator_claim_expires_at,
			evidence.wave_id,evidence.component_type,evidence.component_id,evidence.target_artifact_digest,evidence.target_digest,
			target.target_state,execution.execution_state,execution.executor_owner,execution.attempt_generation,
			execution.claim_expires_at
			FROM kim.upgrade_campaigns_current campaign
			JOIN kim.upgrade_target_evidence evidence ON evidence.campaign_id=campaign.campaign_id
			 AND evidence.plan_revision=campaign.plan_revision
			JOIN kim.upgrade_targets_current target ON target.target_id=evidence.target_id
			JOIN kim.upgrade_target_executions_current execution ON execution.target_id=evidence.target_id
			WHERE campaign.campaign_id=$1 AND evidence.target_id=$2
			FOR UPDATE OF campaign,target,execution`, request.CampaignID, request.TargetID).Scan(
			&campaignState, &waveID, &planRevision, &coordinatorOwner, &coordinatorGeneration, &coordinatorExpiry,
			&targetWaveID, &componentType, &componentID, &artifactDigest, &targetDigest, &targetState, &executionState,
			&priorOwner, &priorAttempt, &priorExpiry); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if targetState == "SUCCEEDED" || executionState == "SUCCEEDED" {
			return ErrUpgradeTargetAlreadyCompleted
		}
		if (campaignState != "CANARY" && campaignState != "ROLLING") || targetWaveID != waveID || coordinatorOwner == nil ||
			coordinatorExpiry == nil || !coordinatorExpiry.After(now) || coordinatorGeneration <= 0 ||
			targetState == "FAILED" || targetState == "FENCED" || executionState == "FAILED" || executionState == "FENCED" {
			return ErrUpgradeTargetClaimUnavailable
		}
		if priorOwner != nil && priorExpiry != nil && priorExpiry.After(now) {
			return ErrUpgradeTargetClaimUnavailable
		}
		generation := priorAttempt + 1
		mode := "APPLY_ALLOWED"
		if priorAttempt > 0 {
			mode = "READ_BACK_FIRST"
			eventID := fmt.Sprintf("upgrade-target:%s:unknown:%d", request.TargetID, priorAttempt)
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_execution_event_evidence(
				event_id,target_id,attempt_generation,event_type,evidence_digest
			) VALUES($1,$2,$3,'TARGET_UNKNOWN',$4) ON CONFLICT DO NOTHING`, eventID, request.TargetID,
				priorAttempt, digestReleaseBytes([]byte(eventID))); err != nil {
				return err
			}
		}
		expires := now.Add(request.Lease)
		maximum := now.Add(request.MaximumLifetime)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_attempt_evidence(
			target_id,attempt_generation,campaign_id,plan_revision,wave_id,executor_owner,attempt_mode,
			coordinator_claim_generation,lease_expires_at,maximum_expires_at,target_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, request.TargetID, generation, request.CampaignID,
			planRevision, waveID, request.Owner, mode, coordinatorGeneration, expires, maximum, targetDigest); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-target:%s:claim:%d", request.TargetID, generation)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_execution_event_evidence(
			event_id,target_id,attempt_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'CLAIM_GRANTED',$4)`, eventID, request.TargetID, generation,
			digestReleaseBytes([]byte(eventID))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_executions_current SET execution_state='CLAIMED',
			executor_owner=$2,attempt_generation=$3,attempt_mode=$4,coordinator_claim_generation=$5,
			renewal_generation=0,claim_expires_at=$6,maximum_expires_at=$7,updated_at=statement_timestamp()
			WHERE target_id=$1`, request.TargetID, request.Owner, generation, mode, coordinatorGeneration, expires, maximum); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_targets_current SET target_state='IN_PROGRESS',
			attempt_generation=$2,result_digest=NULL,updated_at=statement_timestamp() WHERE target_id=$1`,
			request.TargetID, generation); err != nil {
			return err
		}
		claim = UpgradeTargetClaim{CampaignID: request.CampaignID, TargetID: request.TargetID, Owner: request.Owner,
			AttemptMode: mode, WaveID: waveID, ComponentType: componentType, ComponentID: componentID,
			TargetArtifactDigest: artifactDigest, TargetDigest: targetDigest, AttemptGeneration: uint64(generation),
			CoordinatorClaimGeneration: uint64(coordinatorGeneration), PlanRevision: uint64(planRevision),
			ExpiresAt: expires, MaximumExpiresAt: maximum}
		return nil
	})
	return claim, err
}

func RenewUpgradeTargetClaim(ctx context.Context, db TxBeginner, request UpgradeTargetRenewRequest) (UpgradeTargetRenewal, error) {
	if request.TargetID == "" || request.Owner == "" || request.AttemptGeneration == 0 || request.Extension <= 0 {
		return UpgradeTargetRenewal{}, ErrStaleUpgradeTargetClaim
	}
	var renewal UpgradeTargetRenewal
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var owner *string
		var attempt, renewalGeneration, boundCoordinatorGeneration, currentCoordinatorGeneration int64
		var priorExpiry, maximumExpiry, coordinatorExpiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT execution.executor_owner,execution.attempt_generation,
			execution.renewal_generation,execution.claim_expires_at,execution.maximum_expires_at,
			execution.coordinator_claim_generation,campaign.coordinator_claim_generation,campaign.coordinator_claim_expires_at
			FROM kim.upgrade_target_executions_current execution
			JOIN kim.upgrade_target_evidence target ON target.target_id=execution.target_id
			JOIN kim.upgrade_campaigns_current campaign ON campaign.campaign_id=target.campaign_id
			WHERE execution.target_id=$1 FOR UPDATE OF execution,campaign`, request.TargetID).Scan(&owner, &attempt,
			&renewalGeneration, &priorExpiry, &maximumExpiry, &boundCoordinatorGeneration,
			&currentCoordinatorGeneration, &coordinatorExpiry); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if owner == nil || *owner != request.Owner || uint64(attempt) != request.AttemptGeneration ||
			priorExpiry == nil || maximumExpiry == nil || coordinatorExpiry == nil || !priorExpiry.After(now) ||
			!coordinatorExpiry.After(now) || boundCoordinatorGeneration != currentCoordinatorGeneration || currentCoordinatorGeneration <= 0 {
			return ErrStaleUpgradeTargetClaim
		}
		renewed := now.Add(request.Extension)
		if renewed.After(*maximumExpiry) {
			renewed = *maximumExpiry
		}
		if !renewed.After(*priorExpiry) {
			renewal = UpgradeTargetRenewal{RenewalGeneration: uint64(renewalGeneration), ExpiresAt: *priorExpiry}
			return nil
		}
		generation := renewalGeneration + 1
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_renewal_evidence(
			target_id,attempt_generation,renewal_generation,executor_owner,prior_expires_at,renewed_expires_at,maximum_expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7)`, request.TargetID, request.AttemptGeneration, generation,
			request.Owner, *priorExpiry, renewed, *maximumExpiry); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_executions_current SET claim_expires_at=$2,
			renewal_generation=$3,updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID, renewed, generation); err != nil {
			return err
		}
		renewal = UpgradeTargetRenewal{RenewalGeneration: uint64(generation), ExpiresAt: renewed}
		return nil
	})
	return renewal, err
}

func ObserveUpgradeTarget(ctx context.Context, db TxBeginner, request UpgradeTargetObservationRequest) (UpgradeTargetObservationDecision, error) {
	if request.TargetID == "" || request.Owner == "" || request.AttemptGeneration == 0 ||
		(request.ObservationState != "MATCHED" && request.ObservationState != "ABSENT" &&
			request.ObservationState != "CONFLICTING" && request.ObservationState != "UNKNOWN") || !validDigest(request.ObservedDigest) {
		return UpgradeTargetObservationDecision{}, ErrUpgradeTargetObservationConflict
	}
	var decision UpgradeTargetObservationDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		mode, err := requireCurrentUpgradeTargetClaim(ctx, tx, request.TargetID, request.Owner, request.AttemptGeneration)
		if err != nil {
			return err
		}
		identity := fmt.Sprintf("%s:%d:%s:%s", request.TargetID, request.AttemptGeneration, request.ObservationState, request.ObservedDigest)
		observationID := "upgrade-target-observation:" + digestReleaseBytes([]byte(identity))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_observation_evidence(
			observation_id,target_id,attempt_generation,observation_state,observed_digest,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, observationID, request.TargetID,
			request.AttemptGeneration, request.ObservationState, request.ObservedDigest, digestReleaseBytes([]byte(identity))); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-target:%s:read-back:%s", request.TargetID, digestReleaseBytes([]byte(identity)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_execution_event_evidence(
			event_id,target_id,attempt_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'READ_BACK_STARTED',$4) ON CONFLICT DO NOTHING`, eventID, request.TargetID,
			request.AttemptGeneration, digestReleaseBytes([]byte(eventID))); err != nil {
			return err
		}
		switch request.ObservationState {
		case "MATCHED":
			decision.Action = "COMPLETE_MATCHED"
		case "ABSENT":
			if mode == "READ_BACK_FIRST" || mode == "APPLY_ALLOWED" {
				if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_executions_current SET attempt_mode='APPLY_ALLOWED',
					updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
					return err
				}
				authorizeID := fmt.Sprintf("upgrade-target:%s:apply:%d", request.TargetID, request.AttemptGeneration)
				if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_execution_event_evidence(
					event_id,target_id,attempt_generation,event_type,evidence_digest
				) VALUES($1,$2,$3,'APPLY_AUTHORIZED',$4) ON CONFLICT DO NOTHING`, authorizeID, request.TargetID,
					request.AttemptGeneration, digestReleaseBytes([]byte(authorizeID))); err != nil {
					return err
				}
				decision.Action = "APPLY_AUTHORIZED"
			}
		case "CONFLICTING":
			quarantineID := fmt.Sprintf("upgrade-target:%s:conflict:%d:%s", request.TargetID,
				request.AttemptGeneration, request.ObservedDigest)
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_execution_event_evidence(
				event_id,target_id,attempt_generation,event_type,evidence_digest
			) VALUES($1,$2,$3,'CONFLICT_QUARANTINED',$4) ON CONFLICT DO NOTHING`, quarantineID,
				request.TargetID, request.AttemptGeneration, digestReleaseBytes([]byte(quarantineID))); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_executions_current SET execution_state='FENCED',
				executor_owner=NULL,attempt_mode=NULL,claim_expires_at=NULL,maximum_expires_at=NULL,
				updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_targets_current SET target_state='FENCED',
				result_digest=NULL,updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
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

func CompleteUpgradeTarget(ctx context.Context, db TxBeginner, request UpgradeTargetCompletionRequest) error {
	if request.TargetID == "" || request.Owner == "" || request.AttemptGeneration == 0 ||
		(request.Outcome != "SUCCEEDED" && request.Outcome != "FAILED") || !validDigest(request.ResultDigest) || !validDigest(request.ObservedDigest) {
		return ErrUpgradeTargetObservationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var existingOutcome, existingResult, existingObserved string
		existingErr := tx.QueryRow(ctx, `SELECT outcome,result_digest,observed_digest FROM kim.upgrade_target_result_evidence
			WHERE target_id=$1 AND attempt_generation=$2`, request.TargetID, request.AttemptGeneration).Scan(
			&existingOutcome, &existingResult, &existingObserved)
		if existingErr == nil {
			if existingOutcome == request.Outcome && existingResult == request.ResultDigest && existingObserved == request.ObservedDigest {
				return nil
			}
			return ErrUpgradeTargetObservationConflict
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		if _, err := requireCurrentUpgradeTargetClaim(ctx, tx, request.TargetID, request.Owner, request.AttemptGeneration); err != nil {
			return err
		}
		if request.Outcome == "SUCCEEDED" {
			var matched bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.upgrade_target_observation_evidence
				WHERE target_id=$1 AND attempt_generation=$2 AND observation_state='MATCHED' AND observed_digest=$3)`,
				request.TargetID, request.AttemptGeneration, request.ObservedDigest).Scan(&matched); err != nil || !matched {
				return ErrUpgradeTargetObservationConflict
			}
		}
		resultID := fmt.Sprintf("upgrade-target-result:%s:%d:%s", request.TargetID, request.AttemptGeneration, request.ResultDigest)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_result_evidence(
			result_id,target_id,attempt_generation,outcome,result_digest,observed_digest
		) VALUES($1,$2,$3,$4,$5,$6)`, resultID, request.TargetID, request.AttemptGeneration,
			request.Outcome, request.ResultDigest, request.ObservedDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_targets_current SET target_state=$2,result_digest=$3,
			updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID, request.Outcome, request.ResultDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_target_executions_current SET execution_state=$2,
			executor_owner=NULL,attempt_mode=NULL,claim_expires_at=NULL,maximum_expires_at=NULL,
			updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID, request.Outcome); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade-target:%s:result:%d", request.TargetID, request.AttemptGeneration)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_execution_event_evidence(
			event_id,target_id,attempt_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'RESULT_ACCEPTED',$4)`, eventID, request.TargetID, request.AttemptGeneration,
			digestReleaseBytes([]byte(eventID))); err != nil {
			return err
		}
		var campaignID string
		var coordinatorGeneration int64
		if err := tx.QueryRow(ctx, `SELECT target.campaign_id,attempt.coordinator_claim_generation
			FROM kim.upgrade_target_evidence target JOIN kim.upgrade_target_attempt_evidence attempt
			 ON attempt.target_id=target.target_id AND attempt.attempt_generation=$2 WHERE target.target_id=$1`,
			request.TargetID, request.AttemptGeneration).Scan(&campaignID, &coordinatorGeneration); err != nil {
			return err
		}
		campaignEventID := fmt.Sprintf("upgrade:%s:target:%s:%s", campaignID, request.TargetID, request.ResultDigest)
		_, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_campaign_event_evidence(
			event_id,campaign_id,claim_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'TARGET_RESULT_ACCEPTED',$4) ON CONFLICT DO NOTHING`, campaignEventID, campaignID,
			coordinatorGeneration, digestReleaseBytes([]byte(campaignEventID)))
		return err
	})
}

func requireCurrentUpgradeTargetClaim(ctx context.Context, tx pgx.Tx, targetID, owner string, attemptGeneration uint64) (string, error) {
	if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
		return "", err
	}
	var currentOwner *string
	var currentAttempt, boundCoordinator, currentCoordinator int64
	var mode *string
	var campaignState, currentWave, targetWave string
	var claimExpiry, coordinatorExpiry *time.Time
	if err := tx.QueryRow(ctx, `SELECT execution.executor_owner,execution.attempt_generation,execution.attempt_mode,
		execution.coordinator_claim_generation,execution.claim_expires_at,campaign.coordinator_claim_generation,
		campaign.coordinator_claim_expires_at,campaign.campaign_state,campaign.current_wave_id,target.wave_id
		FROM kim.upgrade_target_executions_current execution
		JOIN kim.upgrade_target_evidence target ON target.target_id=execution.target_id
		JOIN kim.upgrade_campaigns_current campaign ON campaign.campaign_id=target.campaign_id
		WHERE execution.target_id=$1 FOR UPDATE OF execution,campaign`, targetID).Scan(&currentOwner, &currentAttempt,
		&mode, &boundCoordinator, &claimExpiry, &currentCoordinator, &coordinatorExpiry, &campaignState, &currentWave, &targetWave); err != nil {
		return "", err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		return "", err
	}
	if currentOwner == nil || mode == nil || *currentOwner != owner || uint64(currentAttempt) != attemptGeneration ||
		claimExpiry == nil || !claimExpiry.After(now) || coordinatorExpiry == nil || !coordinatorExpiry.After(now) ||
		boundCoordinator != currentCoordinator || currentWave != targetWave ||
		(campaignState != "CANARY" && campaignState != "ROLLING") {
		return "", ErrStaleUpgradeTargetClaim
	}
	return *mode, nil
}
