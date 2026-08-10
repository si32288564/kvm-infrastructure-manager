package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrUpgradeCampaignClaimUnavailable = errors.New("upgrade campaign coordinator claim is unavailable")
var ErrStaleUpgradeCampaignClaim = errors.New("upgrade campaign coordinator claim is stale")
var ErrUpgradeCampaignEvidenceConflict = errors.New("upgrade campaign evidence conflicts with current authority")

type UpgradeWavePlan struct {
	WaveID             string
	WaveOrdinal        int
	WaveType           string
	MaximumUnavailable int
	FailureThreshold   int
}

type UpgradeTargetPlan struct {
	TargetID, WaveID, ComponentType, ComponentID, TargetReleaseID, TargetArtifactDigest string
	TargetManifestRevision                                                              uint64
}

type UpgradeCampaignPlanRequest struct {
	CampaignID, SourceReleaseID, TargetReleaseID, Strategy, SBOMSnapshotDigest string
	PlanRevision, SourceManifestRevision, TargetManifestRevision               uint64
	ComponentGraph, ProvenanceSnapshot                                         json.RawMessage
	Waves                                                                      []UpgradeWavePlan
	Targets                                                                    []UpgradeTargetPlan
}

type UpgradeCampaignPlan struct {
	CampaignID, PlanDigest, ComponentGraphDigest, ProvenanceSnapshotDigest string
	PlanRevision                                                           uint64
}

type UpgradeCampaignClaimRequest struct {
	CampaignID, Owner      string
	Lease, MaximumLifetime time.Duration
}

type UpgradeCampaignClaim struct {
	CampaignID, Owner, ClaimMode, WaveID              string
	ClaimGeneration, PlanRevision, CampaignGeneration uint64
	ExpiresAt, MaximumExpiresAt                       time.Time
}

type UpgradeCampaignRenewRequest struct {
	CampaignID, Owner string
	ClaimGeneration   uint64
	Extension         time.Duration
}

type UpgradeCampaignRenewal struct {
	RenewalGeneration uint64
	ExpiresAt         time.Time
}

type UpgradeTargetOutcomeRequest struct {
	CampaignID, TargetID, Owner, Outcome, ResultDigest string
	ClaimGeneration                                    uint64
}

type UpgradeCanaryDecisionRequest struct {
	CampaignID, Owner, EvaluatorArtifactDigest string
	ClaimGeneration                            uint64
}

type UpgradeCanaryDecision struct {
	DecisionID, Decision                                  string
	Succeeded, Failed, Unknown, Pending, FailureThreshold int
}

func PublishUpgradeCampaignPlan(ctx context.Context, db TxBeginner, request UpgradeCampaignPlanRequest) (UpgradeCampaignPlan, error) {
	if request.CampaignID == "" || request.PlanRevision == 0 || request.SourceReleaseID == "" ||
		request.SourceManifestRevision == 0 || request.TargetReleaseID == "" || request.TargetManifestRevision == 0 ||
		request.Strategy != "CANARY_ROLLING" || !validDigest(request.SBOMSnapshotDigest) || len(request.Waves) == 0 || len(request.Targets) == 0 {
		return UpgradeCampaignPlan{}, errors.New("complete immutable upgrade campaign plan is required")
	}
	componentGraph, componentGraphDigest, err := canonicalJSONObject(request.ComponentGraph)
	if err != nil {
		return UpgradeCampaignPlan{}, fmt.Errorf("component graph: %w", err)
	}
	graphNodes, err := validateUpgradeComponentGraph(componentGraph)
	if err != nil {
		return UpgradeCampaignPlan{}, fmt.Errorf("component graph: %w", err)
	}
	provenance, provenanceDigest, err := canonicalJSONObject(request.ProvenanceSnapshot)
	if err != nil {
		return UpgradeCampaignPlan{}, fmt.Errorf("provenance snapshot: %w", err)
	}
	provenanceArtifacts, err := validateUpgradeProvenance(provenance)
	if err != nil {
		return UpgradeCampaignPlan{}, fmt.Errorf("provenance snapshot: %w", err)
	}
	waves := append([]UpgradeWavePlan(nil), request.Waves...)
	slices.SortFunc(waves, func(a, b UpgradeWavePlan) int { return a.WaveOrdinal - b.WaveOrdinal })
	targets := append([]UpgradeTargetPlan(nil), request.Targets...)
	slices.SortFunc(targets, func(a, b UpgradeTargetPlan) int {
		if a.TargetID < b.TargetID {
			return -1
		}
		if a.TargetID > b.TargetID {
			return 1
		}
		return 0
	})
	waveByID := make(map[string]UpgradeWavePlan, len(waves))
	for i, wave := range waves {
		if wave.WaveID == "" || wave.WaveOrdinal != i+1 || (wave.WaveType != "CANARY" && wave.WaveType != "BATCH") ||
			wave.MaximumUnavailable <= 0 || wave.FailureThreshold < 0 {
			return UpgradeCampaignPlan{}, errors.New("ordered bounded upgrade waves are required")
		}
		if _, duplicate := waveByID[wave.WaveID]; duplicate {
			return UpgradeCampaignPlan{}, errors.New("duplicate upgrade wave")
		}
		waveByID[wave.WaveID] = wave
	}
	if waves[0].WaveType != "CANARY" {
		return UpgradeCampaignPlan{}, errors.New("first upgrade wave must be CANARY")
	}
	targetDigests := make(map[string]string, len(targets))
	for _, target := range targets {
		if target.TargetID == "" || target.ComponentID == "" || target.TargetReleaseID == "" ||
			target.TargetManifestRevision == 0 || !validDigest(target.TargetArtifactDigest) {
			return UpgradeCampaignPlan{}, errors.New("complete immutable upgrade targets are required")
		}
		if _, ok := waveByID[target.WaveID]; !ok {
			return UpgradeCampaignPlan{}, errors.New("upgrade target references an unknown wave")
		}
		if _, ok := graphNodes[target.ComponentType]; !ok {
			return UpgradeCampaignPlan{}, errors.New("upgrade target component is absent from the component graph")
		}
		if provenanceArtifacts[target.ComponentType] != target.TargetArtifactDigest {
			return UpgradeCampaignPlan{}, errors.New("upgrade target artifact is not verified by the provenance snapshot")
		}
		targetRaw, _ := json.Marshal(target)
		targetDigests[target.TargetID] = digestReleaseBytes(targetRaw)
	}
	waveSnapshotDigests := make(map[string]string, len(waves))
	for _, wave := range waves {
		var members []string
		for _, target := range targets {
			if target.WaveID == wave.WaveID {
				members = append(members, targetDigests[target.TargetID])
			}
		}
		if len(members) == 0 {
			return UpgradeCampaignPlan{}, errors.New("every upgrade wave requires a target snapshot")
		}
		raw, _ := json.Marshal(members)
		waveSnapshotDigests[wave.WaveID] = digestReleaseBytes(raw)
	}
	planInput := map[string]any{"campaign_id": request.CampaignID, "plan_revision": request.PlanRevision,
		"source_release_id": request.SourceReleaseID, "source_manifest_revision": request.SourceManifestRevision,
		"target_release_id": request.TargetReleaseID, "target_manifest_revision": request.TargetManifestRevision,
		"strategy": request.Strategy, "component_graph_digest": componentGraphDigest,
		"provenance_snapshot_digest": provenanceDigest, "sbom_snapshot_digest": request.SBOMSnapshotDigest,
		"waves": waves, "targets": targets}
	planRaw, _ := json.Marshal(planInput)
	plan := UpgradeCampaignPlan{CampaignID: request.CampaignID, PlanRevision: request.PlanRevision,
		PlanDigest: digestReleaseBytes(planRaw), ComponentGraphDigest: componentGraphDigest,
		ProvenanceSnapshotDigest: provenanceDigest}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var releasesValid bool
		if err := tx.QueryRow(ctx, `SELECT count(*)=2 FROM kim.release_manifest_evidence manifest
			WHERE (release_id,manifest_revision) IN (($1,$2),($3,$4)) AND certification_state='VALIDATED'
			 AND NOT EXISTS(SELECT 1 FROM kim.release_distrust_evidence distrust
				WHERE distrust.distrust_scope='MANIFEST' AND distrust.source_release_id=manifest.release_id
				 AND distrust.source_manifest_revision=manifest.manifest_revision)`, request.SourceReleaseID,
			request.SourceManifestRevision, request.TargetReleaseID, request.TargetManifestRevision).Scan(&releasesValid); err != nil || !releasesValid {
			return ErrIncompatibleRelease
		}
		var existingPlanDigest string
		existingErr := tx.QueryRow(ctx, `SELECT plan_digest FROM kim.upgrade_plan_evidence
			WHERE campaign_id=$1 AND plan_revision=$2 FOR SHARE`, request.CampaignID, request.PlanRevision).Scan(&existingPlanDigest)
		if existingErr == nil {
			if existingPlanDigest == plan.PlanDigest {
				return nil
			}
			return ErrUpgradeCampaignEvidenceConflict
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_plan_evidence(
			campaign_id,plan_revision,source_release_id,source_manifest_revision,target_release_id,target_manifest_revision,
			strategy,component_graph,component_graph_digest,provenance_snapshot,provenance_snapshot_digest,
			sbom_snapshot_digest,plan_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			request.CampaignID, request.PlanRevision, request.SourceReleaseID, request.SourceManifestRevision,
			request.TargetReleaseID, request.TargetManifestRevision, request.Strategy, componentGraph, componentGraphDigest,
			provenance, provenanceDigest, request.SBOMSnapshotDigest, plan.PlanDigest); err != nil {
			return err
		}
		for _, wave := range waves {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_wave_evidence(
				campaign_id,plan_revision,wave_id,wave_ordinal,wave_type,maximum_unavailable,failure_threshold,target_snapshot_digest
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, request.CampaignID, request.PlanRevision, wave.WaveID,
				wave.WaveOrdinal, wave.WaveType, wave.MaximumUnavailable, wave.FailureThreshold,
				waveSnapshotDigests[wave.WaveID]); err != nil {
				return err
			}
		}
		for _, target := range targets {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_evidence(
				target_id,campaign_id,plan_revision,wave_id,component_type,component_id,target_release_id,
				target_manifest_revision,target_artifact_digest,target_digest
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, target.TargetID, request.CampaignID,
				request.PlanRevision, target.WaveID, target.ComponentType, target.ComponentID, target.TargetReleaseID,
				target.TargetManifestRevision, target.TargetArtifactDigest, targetDigests[target.TargetID]); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_targets_current(target_id,target_state) VALUES($1,'PENDING')`, target.TargetID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_target_executions_current(target_id,execution_state)
				VALUES($1,'PENDING')`, target.TargetID); err != nil {
				return err
			}
		}
		command, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_campaigns_current(
			campaign_id,plan_revision,campaign_generation,campaign_state,current_wave_id
		) VALUES($1,$2,1,'PREPARED',$3) ON CONFLICT(campaign_id) DO UPDATE SET
			plan_revision=EXCLUDED.plan_revision,campaign_generation=kim.upgrade_campaigns_current.campaign_generation+1,
			campaign_state='PREPARED',current_wave_id=EXCLUDED.current_wave_id,coordinator_owner=NULL,
			coordinator_claim_expires_at=NULL,coordinator_maximum_expires_at=NULL,latest_canary_decision_id=NULL,
			updated_at=statement_timestamp()
			WHERE kim.upgrade_campaigns_current.plan_revision<EXCLUDED.plan_revision
			 AND kim.upgrade_campaigns_current.campaign_state IN ('PREPARED','PAUSED','BLOCKED')
			 AND (kim.upgrade_campaigns_current.coordinator_owner IS NULL
				OR kim.upgrade_campaigns_current.coordinator_claim_expires_at<=statement_timestamp())`,
			request.CampaignID, request.PlanRevision, waves[0].WaveID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrUpgradeCampaignEvidenceConflict
		}
		return nil
	})
	return plan, err
}

func ClaimUpgradeCampaign(ctx context.Context, db TxBeginner, request UpgradeCampaignClaimRequest) (UpgradeCampaignClaim, error) {
	if request.CampaignID == "" || request.Owner == "" || request.Lease <= 0 || request.MaximumLifetime < request.Lease {
		return UpgradeCampaignClaim{}, ErrUpgradeCampaignClaimUnavailable
	}
	var claim UpgradeCampaignClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state, waveID string
		var planRevision, campaignGeneration, priorGeneration int64
		var priorOwner *string
		var priorExpiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT campaign_state,current_wave_id,plan_revision,campaign_generation,
			coordinator_owner,coordinator_claim_generation,coordinator_claim_expires_at
			FROM kim.upgrade_campaigns_current WHERE campaign_id=$1 FOR UPDATE`, request.CampaignID).Scan(
			&state, &waveID, &planRevision, &campaignGeneration, &priorOwner, &priorGeneration, &priorExpiry); err != nil {
			return err
		}
		if state != "PREPARED" && state != "CANARY" && state != "ROLLING" {
			return ErrUpgradeCampaignClaimUnavailable
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if priorOwner != nil && priorExpiry != nil && priorExpiry.After(now) {
			return ErrUpgradeCampaignClaimUnavailable
		}
		mode := "EXECUTE"
		generation := priorGeneration + 1
		if priorOwner != nil && priorGeneration > 0 {
			mode = "RECOVER_FROM_DB"
			eventID := fmt.Sprintf("upgrade:%s:coordinator-unknown:%d", request.CampaignID, priorGeneration)
			if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_campaign_event_evidence(
				event_id,campaign_id,claim_generation,event_type,evidence_digest
			) VALUES($1,$2,$3,'COORDINATOR_UNKNOWN',$4) ON CONFLICT DO NOTHING`, eventID, request.CampaignID,
				priorGeneration, digestReleaseBytes([]byte(eventID))); err != nil {
				return err
			}
		}
		expires := now.Add(request.Lease)
		maximum := now.Add(request.MaximumLifetime)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_coordinator_attempt_evidence(
			campaign_id,claim_generation,coordinator_owner,claim_mode,lease_expires_at,maximum_expires_at,
			plan_revision,campaign_generation) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, request.CampaignID,
			generation, request.Owner, mode, expires, maximum, planRevision, campaignGeneration); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade:%s:claim:%d", request.CampaignID, generation)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_campaign_event_evidence(
			event_id,campaign_id,claim_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'CLAIM_GRANTED',$4)`, eventID, request.CampaignID, generation,
			digestReleaseBytes([]byte(eventID))); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_campaigns_current SET campaign_state=CASE WHEN campaign_state='PREPARED' THEN 'CANARY' ELSE campaign_state END,
			coordinator_owner=$2,coordinator_claim_generation=$3,coordinator_claim_expires_at=$4,
			coordinator_maximum_expires_at=$5,coordinator_renewal_generation=0,updated_at=statement_timestamp() WHERE campaign_id=$1`,
			request.CampaignID, request.Owner, generation, expires, maximum); err != nil {
			return err
		}
		claim = UpgradeCampaignClaim{CampaignID: request.CampaignID, Owner: request.Owner, ClaimMode: mode,
			WaveID: waveID, ClaimGeneration: uint64(generation), PlanRevision: uint64(planRevision),
			CampaignGeneration: uint64(campaignGeneration), ExpiresAt: expires, MaximumExpiresAt: maximum}
		return nil
	})
	return claim, err
}

func RenewUpgradeCampaignClaim(ctx context.Context, db TxBeginner, request UpgradeCampaignRenewRequest) (UpgradeCampaignRenewal, error) {
	if request.CampaignID == "" || request.Owner == "" || request.ClaimGeneration == 0 || request.Extension <= 0 {
		return UpgradeCampaignRenewal{}, ErrStaleUpgradeCampaignClaim
	}
	var renewal UpgradeCampaignRenewal
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var owner *string
		var generation, renewalGeneration int64
		var priorExpiry, maximumExpiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT coordinator_owner,coordinator_claim_generation,
			coordinator_renewal_generation,coordinator_claim_expires_at,coordinator_maximum_expires_at
			FROM kim.upgrade_campaigns_current WHERE campaign_id=$1 FOR UPDATE`, request.CampaignID).Scan(
			&owner, &generation, &renewalGeneration, &priorExpiry, &maximumExpiry); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if owner == nil || *owner != request.Owner || uint64(generation) != request.ClaimGeneration ||
			priorExpiry == nil || maximumExpiry == nil || !priorExpiry.After(now) {
			return ErrStaleUpgradeCampaignClaim
		}
		renewedExpiry := now.Add(request.Extension)
		if renewedExpiry.After(*maximumExpiry) {
			renewedExpiry = *maximumExpiry
		}
		if !renewedExpiry.After(*priorExpiry) {
			renewal = UpgradeCampaignRenewal{RenewalGeneration: uint64(renewalGeneration), ExpiresAt: *priorExpiry}
			return nil
		}
		newGeneration := renewalGeneration + 1
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_coordinator_renewal_evidence(
			campaign_id,claim_generation,renewal_generation,coordinator_owner,prior_expires_at,
			renewed_expires_at,maximum_expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, request.CampaignID,
			request.ClaimGeneration, newGeneration, request.Owner, *priorExpiry, renewedExpiry, *maximumExpiry); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_campaigns_current SET coordinator_claim_expires_at=$2,
			coordinator_renewal_generation=$3,updated_at=statement_timestamp() WHERE campaign_id=$1`,
			request.CampaignID, renewedExpiry, newGeneration); err != nil {
			return err
		}
		renewal = UpgradeCampaignRenewal{RenewalGeneration: uint64(newGeneration), ExpiresAt: renewedExpiry}
		return nil
	})
	return renewal, err
}

func RecordUpgradeTargetOutcome(ctx context.Context, db TxBeginner, request UpgradeTargetOutcomeRequest) error {
	if request.CampaignID == "" || request.TargetID == "" || request.Owner == "" || request.ClaimGeneration == 0 ||
		(request.Outcome != "SUCCEEDED" && request.Outcome != "FAILED" && request.Outcome != "UNKNOWN") || !validDigest(request.ResultDigest) {
		return ErrUpgradeCampaignEvidenceConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		waveID, err := requireCurrentUpgradeClaim(ctx, tx, request.CampaignID, request.Owner, request.ClaimGeneration)
		if err != nil {
			return err
		}
		var currentState string
		var currentDigest *string
		if err := tx.QueryRow(ctx, `SELECT current.target_state,current.result_digest FROM kim.upgrade_targets_current current
			JOIN kim.upgrade_target_evidence evidence USING(target_id)
			WHERE current.target_id=$1 AND evidence.campaign_id=$2 AND evidence.wave_id=$3 FOR UPDATE OF current`,
			request.TargetID, request.CampaignID, waveID).Scan(&currentState, &currentDigest); err != nil {
			return err
		}
		if currentState == "SUCCEEDED" || currentState == "FAILED" || currentState == "UNKNOWN" {
			if currentState == request.Outcome && currentDigest != nil && *currentDigest == request.ResultDigest {
				return nil
			}
			return ErrUpgradeCampaignEvidenceConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_targets_current SET target_state=$2,
			attempt_generation=attempt_generation+1,result_digest=$3,updated_at=statement_timestamp() WHERE target_id=$1`,
			request.TargetID, request.Outcome, request.ResultDigest); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade:%s:target:%s:%s", request.CampaignID, request.TargetID, request.ResultDigest)
		_, err = tx.Exec(ctx, `INSERT INTO kim.upgrade_campaign_event_evidence(
			event_id,campaign_id,claim_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,'TARGET_RESULT_ACCEPTED',$4)`, eventID, request.CampaignID,
			request.ClaimGeneration, digestReleaseBytes([]byte(eventID)))
		return err
	})
}

func EvaluateUpgradeCanary(ctx context.Context, db TxBeginner, request UpgradeCanaryDecisionRequest) (UpgradeCanaryDecision, error) {
	if request.CampaignID == "" || request.Owner == "" || request.ClaimGeneration == 0 || !validDigest(request.EvaluatorArtifactDigest) {
		return UpgradeCanaryDecision{}, ErrUpgradeCampaignEvidenceConflict
	}
	var result UpgradeCanaryDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		waveID, err := requireCurrentUpgradeClaim(ctx, tx, request.CampaignID, request.Owner, request.ClaimGeneration)
		if err != nil {
			return err
		}
		var planRevision, campaignGeneration int64
		var threshold int
		if err := tx.QueryRow(ctx, `SELECT campaign.plan_revision,campaign.campaign_generation,wave.failure_threshold
			FROM kim.upgrade_campaigns_current campaign JOIN kim.upgrade_wave_evidence wave
			 ON wave.campaign_id=campaign.campaign_id AND wave.plan_revision=campaign.plan_revision AND wave.wave_id=campaign.current_wave_id
			WHERE campaign.campaign_id=$1 FOR UPDATE OF campaign`, request.CampaignID).Scan(&planRevision, &campaignGeneration, &threshold); err != nil {
			return err
		}
		var succeeded, failed, unknown, pending int
		if err := tx.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE current.target_state='SUCCEEDED'),count(*) FILTER (WHERE current.target_state='FAILED'),
			count(*) FILTER (WHERE current.target_state='UNKNOWN'),count(*) FILTER (WHERE current.target_state IN ('PENDING','IN_PROGRESS'))
			FROM kim.upgrade_targets_current current JOIN kim.upgrade_target_evidence evidence USING(target_id)
			WHERE evidence.campaign_id=$1 AND evidence.plan_revision=$2 AND evidence.wave_id=$3`,
			request.CampaignID, planRevision, waveID).Scan(&succeeded, &failed, &unknown, &pending); err != nil {
			return err
		}
		decision, eventType := "HOLD", "CANARY_HELD"
		newState := "CANARY"
		newWaveID := waveID
		if failed > threshold {
			decision, eventType, newState = "PAUSE", "CANARY_PAUSED", "PAUSED"
		} else if unknown == 0 && pending == 0 {
			decision, eventType, newState = "CONTINUE", "CANARY_CONTINUE", "ROLLING"
			var nextWave string
			nextErr := tx.QueryRow(ctx, `SELECT successor.wave_id FROM kim.upgrade_wave_evidence current
				JOIN kim.upgrade_wave_evidence successor ON successor.campaign_id=current.campaign_id
				 AND successor.plan_revision=current.plan_revision AND successor.wave_ordinal=current.wave_ordinal+1
				WHERE current.campaign_id=$1 AND current.plan_revision=$2 AND current.wave_id=$3`,
				request.CampaignID, planRevision, waveID).Scan(&nextWave)
			if nextErr == nil {
				newWaveID = nextWave
			} else if errors.Is(nextErr, pgx.ErrNoRows) {
				newState = "VERIFYING"
			} else {
				return nextErr
			}
		}
		var existingID, existingDecision string
		var existingSucceeded, existingFailed, existingUnknown, existingPending, existingThreshold int
		existingErr := tx.QueryRow(ctx, `SELECT decision.decision_id,decision.decision,decision.succeeded_targets,
			decision.failed_targets,decision.unknown_targets,decision.pending_targets,decision.failure_threshold
			FROM kim.upgrade_campaigns_current campaign JOIN kim.upgrade_canary_decision_evidence decision
			 ON decision.decision_id=campaign.latest_canary_decision_id
			WHERE campaign.campaign_id=$1 AND decision.wave_id=$2 AND decision.evaluator_artifact_digest=$3`,
			request.CampaignID, waveID, request.EvaluatorArtifactDigest).Scan(&existingID, &existingDecision,
			&existingSucceeded, &existingFailed, &existingUnknown, &existingPending, &existingThreshold)
		if existingErr == nil && existingDecision == decision && existingSucceeded == succeeded && existingFailed == failed &&
			existingUnknown == unknown && existingPending == pending && existingThreshold == threshold {
			result = UpgradeCanaryDecision{DecisionID: existingID, Decision: decision, Succeeded: succeeded,
				Failed: failed, Unknown: unknown, Pending: pending, FailureThreshold: threshold}
			return nil
		}
		if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		input := map[string]any{"campaign": request.CampaignID, "plan_revision": planRevision, "wave": waveID,
			"campaign_generation": campaignGeneration, "decision": decision, "succeeded": succeeded,
			"failed": failed, "unknown": unknown, "pending": pending, "threshold": threshold,
			"evaluator": request.EvaluatorArtifactDigest}
		raw, _ := json.Marshal(input)
		evidenceDigest := digestReleaseBytes(raw)
		decisionID := "upgrade-canary:" + evidenceDigest
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_canary_decision_evidence(
			decision_id,campaign_id,plan_revision,wave_id,campaign_generation,decision,succeeded_targets,
			failed_targets,unknown_targets,pending_targets,failure_threshold,evaluator_artifact_digest,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT DO NOTHING`, decisionID,
			request.CampaignID, planRevision, waveID, campaignGeneration, decision, succeeded, failed, unknown,
			pending, threshold, request.EvaluatorArtifactDigest, evidenceDigest); err != nil {
			return err
		}
		eventID := fmt.Sprintf("upgrade:%s:canary:%s", request.CampaignID, evidenceDigest)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_campaign_event_evidence(
			event_id,campaign_id,claim_generation,event_type,evidence_digest
		) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, eventID, request.CampaignID,
			request.ClaimGeneration, eventType, evidenceDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.upgrade_campaigns_current SET campaign_state=$2,current_wave_id=$3,
			campaign_generation=campaign_generation+1,latest_canary_decision_id=$4,updated_at=statement_timestamp()
			WHERE campaign_id=$1`, request.CampaignID, newState, newWaveID, decisionID); err != nil {
			return err
		}
		result = UpgradeCanaryDecision{DecisionID: decisionID, Decision: decision, Succeeded: succeeded,
			Failed: failed, Unknown: unknown, Pending: pending, FailureThreshold: threshold}
		return nil
	})
	return result, err
}

func requireCurrentUpgradeClaim(ctx context.Context, tx pgx.Tx, campaignID, owner string, generation uint64) (string, error) {
	var waveID string
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT current_wave_id,
		(coordinator_owner=$2 AND coordinator_claim_generation=$3 AND coordinator_claim_expires_at>statement_timestamp())
		FROM kim.upgrade_campaigns_current WHERE campaign_id=$1 FOR UPDATE`, campaignID, owner, generation).Scan(&waveID, &valid); err != nil {
		return "", err
	}
	if !valid {
		return "", ErrStaleUpgradeCampaignClaim
	}
	return waveID, nil
}

func canonicalJSONObject(raw json.RawMessage) ([]byte, string, error) {
	var value map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, "", errors.New("canonical JSON object is required")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	return canonical, digestReleaseBytes(canonical), nil
}

func validateUpgradeComponentGraph(raw []byte) (map[string]struct{}, error) {
	var graph struct {
		Nodes []string `json:"nodes"`
		Edges []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(raw, &graph); err != nil || len(graph.Nodes) == 0 {
		return nil, errors.New("typed component graph is required")
	}
	allowed := map[string]struct{}{"API": {}, "AGENT_GATEWAY": {}, "CONTROL_WORKER": {}, "OVN_RUNTIME_WORKER": {}, "HOST_AGENT": {}}
	nodes := make(map[string]struct{}, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if _, ok := allowed[node]; !ok {
			return nil, errors.New("component graph contains an unsupported component type")
		}
		if _, duplicate := nodes[node]; duplicate {
			return nil, errors.New("component graph contains a duplicate node")
		}
		nodes[node] = struct{}{}
	}
	adjacency := make(map[string][]string, len(nodes))
	for _, edge := range graph.Edges {
		if _, ok := nodes[edge.From]; !ok {
			return nil, errors.New("component graph edge has an unknown source")
		}
		if _, ok := nodes[edge.To]; !ok || edge.From == edge.To {
			return nil, errors.New("component graph edge has an invalid target")
		}
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
	}
	visiting := make(map[string]bool, len(nodes))
	visited := make(map[string]bool, len(nodes))
	var visit func(string) bool
	visit = func(node string) bool {
		if visiting[node] {
			return false
		}
		if visited[node] {
			return true
		}
		visiting[node] = true
		for _, successor := range adjacency[node] {
			if !visit(successor) {
				return false
			}
		}
		visiting[node] = false
		visited[node] = true
		return true
	}
	for node := range nodes {
		if !visit(node) {
			return nil, errors.New("component graph contains a cycle")
		}
	}
	return nodes, nil
}

func validateUpgradeProvenance(raw []byte) (map[string]string, error) {
	var provenance struct {
		Builder        string            `json:"builder"`
		BundleDigest   string            `json:"bundle_digest"`
		SignatureState string            `json:"signature_state"`
		Artifacts      map[string]string `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &provenance); err != nil || provenance.Builder == "" ||
		!validDigest(provenance.BundleDigest) || provenance.SignatureState != "VERIFIED" || len(provenance.Artifacts) == 0 {
		return nil, errors.New("verified builder, bundle, signature, and artifact evidence are required")
	}
	for component, artifact := range provenance.Artifacts {
		if component == "" || !validDigest(artifact) {
			return nil, errors.New("provenance artifact evidence is invalid")
		}
	}
	return provenance.Artifacts, nil
}
