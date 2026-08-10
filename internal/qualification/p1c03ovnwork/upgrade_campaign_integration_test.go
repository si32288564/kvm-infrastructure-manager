package p1c03ovnwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestProductUpgradeCampaignCoordinatorRecoveryAndCanaryDecision(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_UPGRADE_CAMPAIGN") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_UPGRADE_CAMPAIGN is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	container := startRenewalResponseLossPostgreSQL(t, ctx)
	databaseURL := postgresContainerURL(t, ctx, container)
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES($1,1,'ACTIVE')`, fmt.Sprintf("upgrade-campaign-%d", time.Now().UnixNano())); err != nil {
		t.Fatal(err)
	}

	sourceArtifact := digest("campaign-source-worker")
	targetArtifact := digest("campaign-target-worker")
	publishRollingManifest(t, ctx, pool, "kim-campaign-source", "1.0.0", sourceArtifact, []string{postgres.OVNRuntimeWorkSchemaV1})
	publishRollingManifest(t, ctx, pool, "kim-campaign-target", "1.1.0", targetArtifact, []string{postgres.OVNRuntimeWorkSchemaV1, ovnRuntimeWorkSchemaV2})
	if err := postgres.PublishReleaseCompatibilityEdge(ctx, pool, postgres.ReleaseCompatibilityEdge{
		SourceReleaseID: "kim-campaign-source", SourceManifestRevision: 1,
		TargetReleaseID: "kim-campaign-target", TargetManifestRevision: 1,
		AllowedWorkSchemas: []string{postgres.OVNRuntimeWorkSchemaV1}, EdgeDigest: digest("campaign-edge"),
		CertificationState: "VALIDATED",
	}); err != nil {
		t.Fatal(err)
	}

	passPlan := campaignPlan("upgrade-campaign-pass", targetArtifact, 0, []postgres.UpgradeTargetPlan{
		campaignTarget("pass-api", "canary", "API", "api-1", targetArtifact),
		campaignTarget("pass-gateway", "canary", "AGENT_GATEWAY", "gateway-1", targetArtifact),
		campaignTarget("pass-ovn", "canary", "OVN_RUNTIME_WORKER", "ovn-worker-1", targetArtifact),
		campaignTarget("pass-agent", "batch-1", "HOST_AGENT", "host-agent-1", targetArtifact),
	})
	published, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, passPlan)
	if err != nil {
		t.Fatal(err)
	}
	cyclicPlan := campaignPlan("upgrade-campaign-cycle", targetArtifact, 0, []postgres.UpgradeTargetPlan{
		campaignTarget("cycle-api", "canary", "API", "api-cycle", targetArtifact),
		campaignTarget("cycle-agent", "batch-1", "HOST_AGENT", "agent-cycle", targetArtifact),
	})
	cyclicPlan.ComponentGraph = json.RawMessage(`{"nodes":["API","HOST_AGENT"],"edges":[{"from":"API","to":"HOST_AGENT"},{"from":"HOST_AGENT","to":"API"}]}`)
	if _, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, cyclicPlan); err == nil {
		t.Fatal("cyclic upgrade component graph was accepted")
	}
	replayed, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, passPlan)
	if err != nil || replayed.PlanDigest != published.PlanDigest {
		t.Fatalf("plan replay=%+v err=%v", replayed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.upgrade_plan_evidence SET plan_digest=$1 WHERE campaign_id=$2`,
		digest("rewrite-plan"), passPlan.CampaignID); err == nil {
		t.Fatal("immutable upgrade plan accepted UPDATE")
	}

	claimA, err := postgres.ClaimUpgradeCampaign(ctx, pool, postgres.UpgradeCampaignClaimRequest{
		CampaignID: passPlan.CampaignID, Owner: "coordinator-a", Lease: 250 * time.Millisecond, MaximumLifetime: time.Second,
	})
	if err != nil || claimA.ClaimMode != "EXECUTE" || claimA.ClaimGeneration != 1 {
		t.Fatalf("claim A=%+v err=%v", claimA, err)
	}
	firstDigest := digest("pass-api-success")
	if err := postgres.RecordUpgradeTargetOutcome(ctx, pool, postgres.UpgradeTargetOutcomeRequest{
		CampaignID: passPlan.CampaignID, TargetID: "pass-api", Owner: claimA.Owner,
		ClaimGeneration: claimA.ClaimGeneration, Outcome: "SUCCEEDED", ResultDigest: firstDigest,
	}); err != nil {
		t.Fatal(err)
	}
	// A lost response is replayed with the same stable evidence and must not create a second target decision.
	if err := postgres.RecordUpgradeTargetOutcome(ctx, pool, postgres.UpgradeTargetOutcomeRequest{
		CampaignID: passPlan.CampaignID, TargetID: "pass-api", Owner: claimA.Owner,
		ClaimGeneration: claimA.ClaimGeneration, Outcome: "SUCCEEDED", ResultDigest: firstDigest,
	}); err != nil {
		t.Fatal(err)
	}
	hold, err := postgres.EvaluateUpgradeCanary(ctx, pool, postgres.UpgradeCanaryDecisionRequest{
		CampaignID: passPlan.CampaignID, Owner: claimA.Owner, ClaimGeneration: claimA.ClaimGeneration,
		EvaluatorArtifactDigest: digest("campaign-canary-evaluator"),
	})
	if err != nil || hold.Decision != "HOLD" || hold.Succeeded != 1 || hold.Pending != 2 {
		t.Fatalf("hold=%+v err=%v", hold, err)
	}

	var claimB postgres.UpgradeCampaignClaim
	eventually(t, 5*time.Second, func() bool {
		claimB, err = postgres.ClaimUpgradeCampaign(ctx, pool, postgres.UpgradeCampaignClaimRequest{
			CampaignID: passPlan.CampaignID, Owner: "coordinator-b", Lease: time.Second, MaximumLifetime: 2 * time.Second,
		})
		return err == nil
	}, "coordinator B did not recover expired campaign claim")
	if claimB.ClaimMode != "RECOVER_FROM_DB" || claimB.ClaimGeneration != 2 || claimB.PlanRevision != claimA.PlanRevision {
		t.Fatalf("claim B=%+v", claimB)
	}
	if err := postgres.RecordUpgradeTargetOutcome(ctx, pool, postgres.UpgradeTargetOutcomeRequest{
		CampaignID: passPlan.CampaignID, TargetID: "pass-gateway", Owner: claimA.Owner,
		ClaimGeneration: claimA.ClaimGeneration, Outcome: "SUCCEEDED", ResultDigest: digest("stale-result"),
	}); !errors.Is(err, postgres.ErrStaleUpgradeCampaignClaim) {
		t.Fatalf("stale coordinator result accepted: %v", err)
	}
	for _, targetID := range []string{"pass-gateway", "pass-ovn"} {
		if err := postgres.RecordUpgradeTargetOutcome(ctx, pool, postgres.UpgradeTargetOutcomeRequest{
			CampaignID: passPlan.CampaignID, TargetID: targetID, Owner: claimB.Owner,
			ClaimGeneration: claimB.ClaimGeneration, Outcome: "SUCCEEDED", ResultDigest: digest(targetID + "-success"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	continueDecision, err := postgres.EvaluateUpgradeCanary(ctx, pool, postgres.UpgradeCanaryDecisionRequest{
		CampaignID: passPlan.CampaignID, Owner: claimB.Owner, ClaimGeneration: claimB.ClaimGeneration,
		EvaluatorArtifactDigest: digest("campaign-canary-evaluator"),
	})
	if err != nil || continueDecision.Decision != "CONTINUE" || continueDecision.Succeeded != 3 {
		t.Fatalf("continue=%+v err=%v", continueDecision, err)
	}

	failPlan := campaignPlan("upgrade-campaign-pause", targetArtifact, 0, []postgres.UpgradeTargetPlan{
		campaignTarget("pause-api", "canary", "API", "api-2", targetArtifact),
		campaignTarget("pause-worker", "canary", "CONTROL_WORKER", "worker-2", targetArtifact),
		campaignTarget("pause-agent", "batch-1", "HOST_AGENT", "host-agent-2", targetArtifact),
	})
	if _, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, failPlan); err != nil {
		t.Fatal(err)
	}
	pauseClaim, err := postgres.ClaimUpgradeCampaign(ctx, pool, postgres.UpgradeCampaignClaimRequest{
		CampaignID: failPlan.CampaignID, Owner: "coordinator-pause", Lease: time.Second, MaximumLifetime: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for targetID, outcome := range map[string]string{"pause-api": "SUCCEEDED", "pause-worker": "FAILED"} {
		if err := postgres.RecordUpgradeTargetOutcome(ctx, pool, postgres.UpgradeTargetOutcomeRequest{
			CampaignID: failPlan.CampaignID, TargetID: targetID, Owner: pauseClaim.Owner,
			ClaimGeneration: pauseClaim.ClaimGeneration, Outcome: outcome, ResultDigest: digest(targetID + "-" + outcome),
		}); err != nil {
			t.Fatal(err)
		}
	}
	pauseDecision, err := postgres.EvaluateUpgradeCanary(ctx, pool, postgres.UpgradeCanaryDecisionRequest{
		CampaignID: failPlan.CampaignID, Owner: pauseClaim.Owner, ClaimGeneration: pauseClaim.ClaimGeneration,
		EvaluatorArtifactDigest: digest("campaign-canary-evaluator"),
	})
	if err != nil || pauseDecision.Decision != "PAUSE" || pauseDecision.Failed != 1 {
		t.Fatalf("pause=%+v err=%v", pauseDecision, err)
	}
	revisionPlan := campaignPlan(failPlan.CampaignID, targetArtifact, 0, []postgres.UpgradeTargetPlan{
		campaignTarget("pause-v2-api", "canary", "API", "api-2", targetArtifact),
		campaignTarget("pause-v2-agent", "batch-1", "HOST_AGENT", "host-agent-2", targetArtifact),
	})
	revisionPlan.PlanRevision = 2
	if _, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, revisionPlan); !errors.Is(err, postgres.ErrUpgradeCampaignEvidenceConflict) {
		t.Fatalf("active coordinator allowed Plan revision switch: %v", err)
	}

	var passState, passWave, pauseState string
	var coordinatorUnknown, passTargetEvents, planCount, waveCount, targetCount, decisionCount int
	if err := pool.QueryRow(ctx, `SELECT campaign_state,current_wave_id FROM kim.upgrade_campaigns_current WHERE campaign_id=$1`, passPlan.CampaignID).Scan(&passState, &passWave); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT campaign_state FROM kim.upgrade_campaigns_current WHERE campaign_id=$1`, failPlan.CampaignID).Scan(&pauseState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='COORDINATOR_UNKNOWN'),
		count(*) FILTER (WHERE event_type='TARGET_RESULT_ACCEPTED') FROM kim.upgrade_campaign_event_evidence
		WHERE campaign_id=$1`, passPlan.CampaignID).Scan(&coordinatorUnknown, &passTargetEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.upgrade_plan_evidence),
		(SELECT count(*) FROM kim.upgrade_wave_evidence),(SELECT count(*) FROM kim.upgrade_target_evidence),
		(SELECT count(*) FROM kim.upgrade_canary_decision_evidence)`).Scan(&planCount, &waveCount, &targetCount, &decisionCount); err != nil {
		t.Fatal(err)
	}
	if passState != "ROLLING" || passWave != "batch-1" || pauseState != "PAUSED" || coordinatorUnknown != 1 || passTargetEvents != 3 ||
		planCount != 2 || waveCount != 4 || targetCount != 7 || decisionCount != 3 {
		t.Fatalf("states=%s/%s/%s unknown=%d target_events=%d plans=%d waves=%d targets=%d decisions=%d",
			passState, passWave, pauseState, coordinatorUnknown, passTargetEvents, planCount, waveCount, targetCount, decisionCount)
	}
	t.Logf("campaign recovery converged: plan=%s claim=%s/%d canary=%s pause=%s evidence=%d/%d/%d/%d",
		published.PlanDigest, claimB.ClaimMode, claimB.ClaimGeneration, continueDecision.Decision,
		pauseDecision.Decision, planCount, waveCount, targetCount, decisionCount)
}

func campaignPlan(campaignID, targetArtifact string, threshold int, targets []postgres.UpgradeTargetPlan) postgres.UpgradeCampaignPlanRequest {
	graph, _ := json.Marshal(map[string]any{
		"nodes": []string{"API", "AGENT_GATEWAY", "CONTROL_WORKER", "OVN_RUNTIME_WORKER", "HOST_AGENT"},
		"edges": []map[string]string{{"from": "API", "to": "AGENT_GATEWAY"}, {"from": "AGENT_GATEWAY", "to": "HOST_AGENT"}},
	})
	provenance, _ := json.Marshal(map[string]any{
		"builder": "kim-qualified-builder", "bundle_digest": digest(campaignID + "-bundle"), "signature_state": "VERIFIED",
		"artifacts": map[string]string{
			"API": targetArtifact, "AGENT_GATEWAY": targetArtifact, "CONTROL_WORKER": targetArtifact,
			"OVN_RUNTIME_WORKER": targetArtifact, "HOST_AGENT": targetArtifact,
		},
	})
	return postgres.UpgradeCampaignPlanRequest{
		CampaignID: campaignID, PlanRevision: 1, SourceReleaseID: "kim-campaign-source", SourceManifestRevision: 1,
		TargetReleaseID: "kim-campaign-target", TargetManifestRevision: 1, Strategy: "CANARY_ROLLING",
		ComponentGraph: graph, ProvenanceSnapshot: provenance, SBOMSnapshotDigest: digest(campaignID + "-sbom"),
		Waves: []postgres.UpgradeWavePlan{
			{WaveID: "canary", WaveOrdinal: 1, WaveType: "CANARY", MaximumUnavailable: 1, FailureThreshold: threshold},
			{WaveID: "batch-1", WaveOrdinal: 2, WaveType: "BATCH", MaximumUnavailable: 2, FailureThreshold: 1},
		},
		Targets: targets,
	}
}

func campaignTarget(targetID, waveID, componentType, componentID, artifact string) postgres.UpgradeTargetPlan {
	return postgres.UpgradeTargetPlan{TargetID: targetID, WaveID: waveID, ComponentType: componentType,
		ComponentID: componentID, TargetReleaseID: "kim-campaign-target", TargetManifestRevision: 1,
		TargetArtifactDigest: artifact}
}
