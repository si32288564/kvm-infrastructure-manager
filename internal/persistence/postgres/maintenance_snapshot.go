package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrMaintenanceEvidenceConflict = errors.New("maintenance evidence conflicts with current authority")
var ErrMaintenanceClaimUnavailable = errors.New("maintenance coordinator claim is unavailable")
var ErrMaintenanceTargetClaimUnavailable = errors.New("maintenance target execution claim is unavailable")
var ErrHostDisruptiveOperationConflict = errors.New("Host disruptive operation conflicts with current authority")

type MaintenancePlanRequest struct {
	MaintenanceID, SnapshotID, SnapshotDigest, WaveID               string
	OperationType, OperationSchemaVersion, ProfileID, ProfileDigest string
	PlanRevision, ProfileRevision                                   uint64
	MaximumConcurrent, FailureDomainMaximumUnavailable              int
}

type MaintenancePlan struct {
	MaintenanceID, PlanDigest, SnapshotID, SnapshotDigest string
	PlanRevision                                          uint64
}

type MaintenanceCoordinatorClaimRequest struct {
	MaintenanceID, Owner   string
	Lease, MaximumLifetime time.Duration
}

type MaintenanceCoordinatorClaim struct {
	MaintenanceID, Owner, ClaimMode, WaveID              string
	ClaimGeneration, PlanRevision, MaintenanceGeneration uint64
	ExpiresAt, MaximumExpiresAt                          time.Time
}

type MaintenanceResumeRequest struct {
	MaintenanceID, ResumeID, Actor string
}

type MaintenanceTargetClaimRequest struct {
	MaintenanceID, TargetID, Owner string
	Lease, MaximumLifetime         time.Duration
}

type MaintenanceTargetClaim struct {
	MaintenanceID, TargetID, HostID, Owner, AttemptMode string
	OperationType, OperationSchemaVersion, TargetDigest string
	AttemptGeneration, CoordinatorClaimGeneration       uint64
	ExpiresAt, MaximumExpiresAt                         time.Time
}

type MaintenanceTargetCompletionRequest struct {
	MaintenanceID, TargetID, Owner, Outcome string
	AttemptGeneration                       uint64
}

type maintenanceSnapshotMember struct {
	HostID, EvidenceDigest string
	Generation             uint64
}

func PublishMaintenancePlan(ctx context.Context, db TxBeginner, request MaintenancePlanRequest) (MaintenancePlan, error) {
	if request.MaintenanceID == "" || request.PlanRevision == 0 || request.SnapshotID == "" ||
		!validDigest(request.SnapshotDigest) || request.WaveID == "" || request.OperationType != "HOST_DRAIN" ||
		request.OperationSchemaVersion != "v1" || request.ProfileID == "" || request.ProfileRevision == 0 ||
		!validDigest(request.ProfileDigest) || request.MaximumConcurrent <= 0 ||
		request.FailureDomainMaximumUnavailable <= 0 {
		return MaintenancePlan{}, ErrMaintenanceEvidenceConflict
	}
	var result MaintenancePlan
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
			"maintenance-plan/"+request.MaintenanceID); err != nil {
			return err
		}
		var purpose, groupID string
		var setGeneration uint64
		var memberCount int
		if err := tx.QueryRow(ctx, `SELECT purpose,host_group_id,membership_set_generation,member_count
			FROM kim.host_group_membership_snapshot_evidence WHERE snapshot_id=$1 AND snapshot_digest=$2
			FOR SHARE`, request.SnapshotID, request.SnapshotDigest).Scan(&purpose, &groupID, &setGeneration, &memberCount); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrMaintenanceEvidenceConflict
			}
			return err
		}
		if purpose != "MAINTENANCE" || memberCount == 0 {
			return ErrMaintenanceEvidenceConflict
		}
		rows, err := tx.Query(ctx, `SELECT host_id,membership_generation,membership_evidence_digest
			FROM kim.host_group_membership_snapshot_members WHERE snapshot_id=$1 AND host_group_id=$2 ORDER BY host_id`,
			request.SnapshotID, groupID)
		if err != nil {
			return err
		}
		members := make([]maintenanceSnapshotMember, 0, memberCount)
		for rows.Next() {
			var member maintenanceSnapshotMember
			if err := rows.Scan(&member.HostID, &member.Generation, &member.EvidenceDigest); err != nil {
				rows.Close()
				return err
			}
			members = append(members, member)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(members) != memberCount {
			return ErrMaintenanceEvidenceConflict
		}
		targetDigests := make([]string, 0, len(members))
		for _, member := range members {
			targetDigests = append(targetDigests, maintenanceTargetDigest(request, member.HostID))
		}
		targetRaw, _ := json.Marshal(targetDigests)
		waveDigest := digestReleaseBytes(targetRaw)
		planRaw, _ := json.Marshal(map[string]any{
			"maintenance_id": request.MaintenanceID, "plan_revision": request.PlanRevision,
			"snapshot_id": request.SnapshotID, "snapshot_digest": request.SnapshotDigest,
			"operation_type": request.OperationType, "operation_schema_version": request.OperationSchemaVersion,
			"profile_id": request.ProfileID, "profile_revision": request.ProfileRevision,
			"profile_digest": request.ProfileDigest, "wave_id": request.WaveID,
			"maximum_concurrent":                 request.MaximumConcurrent,
			"failure_domain_maximum_unavailable": request.FailureDomainMaximumUnavailable,
			"target_snapshot_digest":             waveDigest,
		})
		result = MaintenancePlan{MaintenanceID: request.MaintenanceID, PlanRevision: request.PlanRevision,
			PlanDigest: digestReleaseBytes(planRaw), SnapshotID: request.SnapshotID, SnapshotDigest: request.SnapshotDigest}
		var existing string
		err = tx.QueryRow(ctx, `SELECT plan_digest FROM kim.maintenance_plan_evidence
			WHERE maintenance_id=$1 AND plan_revision=$2 FOR SHARE`, request.MaintenanceID, request.PlanRevision).Scan(&existing)
		if err == nil {
			if existing == result.PlanDigest {
				return nil
			}
			return ErrMaintenanceEvidenceConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_plan_evidence(
			maintenance_id,plan_revision,source_snapshot_id,source_snapshot_digest,source_host_group_id,
			source_membership_set_generation,operation_type,operation_schema_version,profile_id,profile_revision,
			profile_digest,maximum_concurrent,failure_domain_maximum_unavailable,plan_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, request.MaintenanceID,
			request.PlanRevision, request.SnapshotID, request.SnapshotDigest, groupID, setGeneration,
			request.OperationType, request.OperationSchemaVersion, request.ProfileID, request.ProfileRevision,
			request.ProfileDigest, request.MaximumConcurrent, request.FailureDomainMaximumUnavailable, result.PlanDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_wave_evidence(
			maintenance_id,plan_revision,wave_id,wave_ordinal,maximum_concurrent,
			failure_domain_maximum_unavailable,target_snapshot_digest
		) VALUES($1,$2,$3,1,$4,$5,$6)`, request.MaintenanceID, request.PlanRevision, request.WaveID,
			request.MaximumConcurrent, request.FailureDomainMaximumUnavailable, waveDigest); err != nil {
			return err
		}
		for _, member := range members {
			targetID := maintenanceTargetID(request, member.HostID)
			targetDigest := maintenanceTargetDigest(request, member.HostID)
			if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_target_evidence(
				target_id,maintenance_id,plan_revision,wave_id,host_id,operation_type,operation_schema_version,target_digest
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, targetID, request.MaintenanceID, request.PlanRevision,
				request.WaveID, member.HostID, request.OperationType, request.OperationSchemaVersion, targetDigest); err != nil {
				return err
			}
			provenanceDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s\n%s\n%s\n%d\n%s\n%d\n%s",
				targetID, request.SnapshotID, request.SnapshotDigest, setGeneration, member.HostID,
				member.Generation, member.EvidenceDigest)))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_target_host_group_member_evidence(
				target_id,maintenance_id,plan_revision,wave_id,source_snapshot_id,source_snapshot_digest,
				source_host_group_id,source_membership_set_generation,host_id,membership_generation,
				membership_evidence_digest,provenance_digest
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, targetID, request.MaintenanceID,
				request.PlanRevision, request.WaveID, request.SnapshotID, request.SnapshotDigest, groupID,
				setGeneration, member.HostID, member.Generation, member.EvidenceDigest, provenanceDigest); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_targets_current(target_id,target_state)
				VALUES($1,'PENDING')`, targetID); err != nil {
				return err
			}
		}
		command, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_plans_current(
			maintenance_id,plan_revision,maintenance_generation,maintenance_state,current_wave_id
		) VALUES($1,$2,1,'PREPARED',$3) ON CONFLICT(maintenance_id) DO UPDATE SET
			plan_revision=EXCLUDED.plan_revision,maintenance_generation=kim.maintenance_plans_current.maintenance_generation+1,
			maintenance_state='PREPARED',current_wave_id=EXCLUDED.current_wave_id,coordinator_owner=NULL,
			coordinator_claim_expires_at=NULL,coordinator_maximum_expires_at=NULL,updated_at=statement_timestamp()
			WHERE kim.maintenance_plans_current.plan_revision<EXCLUDED.plan_revision
			AND kim.maintenance_plans_current.maintenance_state IN ('PREPARED','PAUSED','BLOCKED')
			AND (kim.maintenance_plans_current.coordinator_owner IS NULL OR
			kim.maintenance_plans_current.coordinator_claim_expires_at<=statement_timestamp())`,
			request.MaintenanceID, request.PlanRevision, request.WaveID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrMaintenanceEvidenceConflict
		}
		return nil
	})
	return result, err
}

func maintenanceTargetID(request MaintenancePlanRequest, hostID string) string {
	return "maintenance-host:" + digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%s\n%s\n%s", request.MaintenanceID,
		request.PlanRevision, request.WaveID, request.SnapshotID, hostID)))
}

func maintenanceTargetDigest(request MaintenancePlanRequest, hostID string) string {
	return digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%s\n%s\n%s\n%s\n%s", request.MaintenanceID,
		request.PlanRevision, request.WaveID, hostID, request.OperationType, request.OperationSchemaVersion, request.ProfileDigest)))
}

func ClaimMaintenanceCoordinator(ctx context.Context, db TxBeginner, request MaintenanceCoordinatorClaimRequest) (MaintenanceCoordinatorClaim, error) {
	if request.MaintenanceID == "" || request.Owner == "" || request.Lease <= 0 || request.MaximumLifetime < request.Lease {
		return MaintenanceCoordinatorClaim{}, ErrMaintenanceClaimUnavailable
	}
	var claim MaintenanceCoordinatorClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state, wave string
		var planRevision, maintenanceGeneration, priorGeneration int64
		var priorOwner *string
		var priorExpiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT maintenance_state,current_wave_id,plan_revision,maintenance_generation,
			coordinator_owner,coordinator_claim_generation,coordinator_claim_expires_at
			FROM kim.maintenance_plans_current WHERE maintenance_id=$1 FOR UPDATE`, request.MaintenanceID).Scan(
			&state, &wave, &planRevision, &maintenanceGeneration, &priorOwner, &priorGeneration, &priorExpiry); err != nil {
			return err
		}
		if state != "PREPARED" && state != "ACTIVE" {
			return ErrMaintenanceClaimUnavailable
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if priorOwner != nil && priorExpiry != nil && priorExpiry.After(now) {
			return ErrMaintenanceClaimUnavailable
		}
		generation := priorGeneration + 1
		mode := "EXECUTE"
		if priorOwner != nil && priorGeneration > 0 {
			mode = "RECOVER_FROM_DB"
		}
		expires, maximum := now.Add(request.Lease), now.Add(request.MaximumLifetime)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_coordinator_attempt_evidence(
			maintenance_id,claim_generation,coordinator_owner,claim_mode,lease_expires_at,maximum_expires_at,
			plan_revision,maintenance_generation) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, request.MaintenanceID,
			generation, request.Owner, mode, expires, maximum, planRevision, maintenanceGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.maintenance_plans_current SET maintenance_state='ACTIVE',
			coordinator_owner=$2,coordinator_claim_generation=$3,coordinator_claim_expires_at=$4,
			coordinator_maximum_expires_at=$5,updated_at=statement_timestamp() WHERE maintenance_id=$1`,
			request.MaintenanceID, request.Owner, generation, expires, maximum); err != nil {
			return err
		}
		claim = MaintenanceCoordinatorClaim{MaintenanceID: request.MaintenanceID, Owner: request.Owner,
			ClaimMode: mode, WaveID: wave, ClaimGeneration: uint64(generation), PlanRevision: uint64(planRevision),
			MaintenanceGeneration: uint64(maintenanceGeneration), ExpiresAt: expires, MaximumExpiresAt: maximum}
		return nil
	})
	return claim, err
}

func PauseMaintenancePlan(ctx context.Context, db TxBeginner, maintenanceID, owner string, claimGeneration uint64) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var currentOwner *string
		var generation int64
		var expiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT coordinator_owner,coordinator_claim_generation,coordinator_claim_expires_at
			FROM kim.maintenance_plans_current WHERE maintenance_id=$1 FOR UPDATE`, maintenanceID).Scan(
			&currentOwner, &generation, &expiry); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if currentOwner == nil || *currentOwner != owner || uint64(generation) != claimGeneration || expiry == nil || !expiry.After(now) {
			return ErrMaintenanceClaimUnavailable
		}
		_, err := tx.Exec(ctx, `UPDATE kim.maintenance_plans_current SET maintenance_state='PAUSED',
			updated_at=statement_timestamp() WHERE maintenance_id=$1`, maintenanceID)
		return err
	})
}

func ResumeMaintenancePlan(ctx context.Context, db TxBeginner, request MaintenanceResumeRequest) error {
	if request.MaintenanceID == "" || request.ResumeID == "" || request.Actor == "" {
		return ErrMaintenanceEvidenceConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var state, wave string
		var planRevision, generation int64
		var expiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT maintenance_state,current_wave_id,plan_revision,maintenance_generation,
			coordinator_claim_expires_at FROM kim.maintenance_plans_current WHERE maintenance_id=$1 FOR UPDATE`,
			request.MaintenanceID).Scan(&state, &wave, &planRevision, &generation, &expiry); err != nil {
			return err
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%d\n%s\n%s", request.MaintenanceID,
			planRevision, generation, wave, request.Actor)))
		var existing string
		err := tx.QueryRow(ctx, `SELECT evidence_digest FROM kim.maintenance_resume_evidence WHERE resume_id=$1`, request.ResumeID).Scan(&existing)
		if err == nil {
			if existing == digest {
				return nil
			}
			return ErrMaintenanceEvidenceConflict
		}
		var now time.Time
		if nowErr := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); nowErr != nil {
			return nowErr
		}
		if !errors.Is(err, pgx.ErrNoRows) || state != "PAUSED" || (expiry != nil && expiry.After(now)) {
			return ErrMaintenanceEvidenceConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_resume_evidence(
			resume_id,maintenance_id,plan_revision,maintenance_generation,wave_id,actor,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7)`, request.ResumeID, request.MaintenanceID, planRevision,
			generation, wave, request.Actor, digest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.maintenance_plans_current SET maintenance_state='ACTIVE',
			coordinator_owner=NULL,coordinator_claim_expires_at=NULL,coordinator_maximum_expires_at=NULL,
			updated_at=statement_timestamp() WHERE maintenance_id=$1`, request.MaintenanceID)
		return err
	})
}

func ClaimMaintenanceTarget(ctx context.Context, db TxBeginner, request MaintenanceTargetClaimRequest) (MaintenanceTargetClaim, error) {
	if request.MaintenanceID == "" || request.TargetID == "" || request.Owner == "" || request.Lease <= 0 ||
		request.MaximumLifetime < request.Lease {
		return MaintenanceTargetClaim{}, ErrMaintenanceTargetClaimUnavailable
	}
	var claim MaintenanceTargetClaim
	fenced := false
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state, wave, targetWave, hostID, operation, schema, targetDigest, targetState string
		var coordinatorGeneration, priorAttempt int64
		var coordinatorOwner, priorOwner *string
		var coordinatorExpiry, priorExpiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT plan.maintenance_state,plan.current_wave_id,plan.coordinator_owner,
			plan.coordinator_claim_generation,plan.coordinator_claim_expires_at,target.wave_id,target.host_id,
			target.operation_type,target.operation_schema_version,target.target_digest,current.target_state,
			current.executor_owner,current.attempt_generation,current.claim_expires_at
			FROM kim.maintenance_plans_current plan JOIN kim.maintenance_target_evidence target
			 ON target.maintenance_id=plan.maintenance_id AND target.plan_revision=plan.plan_revision
			JOIN kim.maintenance_targets_current current USING(target_id)
			WHERE plan.maintenance_id=$1 AND target.target_id=$2 FOR UPDATE OF plan,current`, request.MaintenanceID,
			request.TargetID).Scan(&state, &wave, &coordinatorOwner, &coordinatorGeneration, &coordinatorExpiry,
			&targetWave, &hostID, &operation, &schema, &targetDigest, &targetState, &priorOwner, &priorAttempt, &priorExpiry); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if state != "ACTIVE" || wave != targetWave || coordinatorOwner == nil || coordinatorExpiry == nil ||
			!coordinatorExpiry.After(now) || targetState == "FENCED" || targetState == "FAILED" ||
			(priorOwner != nil && priorExpiry != nil && priorExpiry.After(now)) {
			return ErrMaintenanceTargetClaimUnavailable
		}
		var eligible bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_operation_authorities_current
			WHERE host_id=$1 AND authority_state='ARMED')`, hostID).Scan(&eligible); err != nil {
			return err
		}
		if !eligible {
			if _, err := tx.Exec(ctx, `UPDATE kim.maintenance_targets_current SET target_state='FENCED',
				updated_at=statement_timestamp() WHERE target_id=$1`, request.TargetID); err != nil {
				return err
			}
			fenced = true
			return nil
		}
		var concurrencyAvailable bool
		if err := tx.QueryRow(ctx, `SELECT count(current.target_id) FILTER (
			WHERE current.target_state='CLAIMED' AND current.target_id<>$3) < wave.maximum_concurrent
			FROM kim.maintenance_wave_evidence wave
			LEFT JOIN kim.maintenance_target_evidence target ON target.maintenance_id=wave.maintenance_id
			 AND target.plan_revision=wave.plan_revision AND target.wave_id=wave.wave_id
			LEFT JOIN kim.maintenance_targets_current current USING(target_id)
			WHERE wave.maintenance_id=$1 AND wave.plan_revision=(SELECT plan_revision FROM kim.maintenance_plans_current WHERE maintenance_id=$1)
			 AND wave.wave_id=$2 GROUP BY wave.maximum_concurrent`, request.MaintenanceID, wave,
			request.TargetID).Scan(&concurrencyAvailable); err != nil || !concurrencyAvailable {
			return ErrMaintenanceTargetClaimUnavailable
		}

		if err := acquireHostDisruptiveOperationClaimTx(ctx, tx, hostID, "MAINTENANCE", request.MaintenanceID,
			request.TargetID, operation); err != nil {
			return err
		}
		attempt := priorAttempt + 1
		mode := "APPLY_ALLOWED"
		if priorAttempt > 0 {
			mode = "READ_BACK_FIRST"
		}
		expires, maximum := now.Add(request.Lease), now.Add(request.MaximumLifetime)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_target_attempt_evidence(
			target_id,attempt_generation,maintenance_id,plan_revision,wave_id,executor_owner,attempt_mode,
			coordinator_claim_generation,lease_expires_at,maximum_expires_at,target_digest
		) SELECT $1,$2,target.maintenance_id,target.plan_revision,target.wave_id,$3,$4,$5,$6,$7,target.target_digest
		FROM kim.maintenance_target_evidence target WHERE target.target_id=$1`, request.TargetID, attempt,
			request.Owner, mode, coordinatorGeneration, expires, maximum); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.maintenance_targets_current SET target_state='CLAIMED',
			executor_owner=$2,attempt_generation=$3,attempt_mode=$4,coordinator_claim_generation=$5,
			claim_expires_at=$6,maximum_expires_at=$7,updated_at=statement_timestamp() WHERE target_id=$1`,
			request.TargetID, request.Owner, attempt, mode, coordinatorGeneration, expires, maximum); err != nil {
			return err
		}
		claim = MaintenanceTargetClaim{MaintenanceID: request.MaintenanceID, TargetID: request.TargetID,
			HostID: hostID, Owner: request.Owner, AttemptMode: mode, OperationType: operation,
			OperationSchemaVersion: schema, TargetDigest: targetDigest, AttemptGeneration: uint64(attempt),
			CoordinatorClaimGeneration: uint64(coordinatorGeneration), ExpiresAt: expires, MaximumExpiresAt: maximum}
		return nil
	})
	if err == nil && fenced {
		return MaintenanceTargetClaim{}, ErrMaintenanceTargetClaimUnavailable
	}
	return claim, err
}

func CompleteMaintenanceTarget(ctx context.Context, db TxBeginner, request MaintenanceTargetCompletionRequest) error {
	if request.MaintenanceID == "" || request.TargetID == "" || request.Owner == "" || request.AttemptGeneration == 0 ||
		(request.Outcome != "SUCCEEDED" && request.Outcome != "FAILED") {
		return ErrMaintenanceEvidenceConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var hostID string
		var owner *string
		var attempt int64
		var expiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT target.host_id,current.executor_owner,current.attempt_generation,current.claim_expires_at
			FROM kim.maintenance_targets_current current JOIN kim.maintenance_target_evidence target USING(target_id)
			WHERE current.target_id=$1 AND target.maintenance_id=$2 FOR UPDATE OF current`, request.TargetID,
			request.MaintenanceID).Scan(&hostID, &owner, &attempt, &expiry); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if owner == nil || *owner != request.Owner || uint64(attempt) != request.AttemptGeneration || expiry == nil || !expiry.After(now) {
			return ErrMaintenanceTargetClaimUnavailable
		}
		if err := releaseHostDisruptiveOperationClaimTx(ctx, tx, hostID, "MAINTENANCE", request.MaintenanceID, request.TargetID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.maintenance_targets_current SET target_state=$2,executor_owner=NULL,
			attempt_mode=NULL,claim_expires_at=NULL,maximum_expires_at=NULL,updated_at=statement_timestamp()
			WHERE target_id=$1`, request.TargetID, request.Outcome)
		return err
	})
}
