package p1c03ovnwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestUpgradeCoordinatorProcessKillPostgreSQLFailover(t *testing.T) {
	if testing.Short() || os.Getenv("KIM_RUN_DOCKER_POSTGRES_UPGRADE_COORDINATOR_FAILOVER") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_UPGRADE_COORDINATOR_FAILOVER is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	cluster := startPostgreSQLFailoverCluster(t, ctx)
	primaryPool, err := postgres.OpenWithMaxConnections(ctx, cluster.primaryURL, 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.Migrate(ctx, primaryPool); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	if _, err := primaryPool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES('upgrade-coordinator-failover',1,'ACTIVE')`); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	targetArtifact := digest("coordinator-failover-target")
	publishRollingManifest(t, ctx, primaryPool, "kim-coordinator-source", "1.0.0", digest("coordinator-source"), []string{postgres.OVNRuntimeWorkSchemaV1})
	publishRollingManifest(t, ctx, primaryPool, "kim-coordinator-target", "1.1.0", targetArtifact, []string{postgres.OVNRuntimeWorkSchemaV1, ovnRuntimeWorkSchemaV2})
	plan := campaignPlan("upgrade-coordinator-failover", targetArtifact, 0, []postgres.UpgradeTargetPlan{
		coordinatorTarget("coordinator-api", "canary", "API", "api-fi", targetArtifact),
		coordinatorTarget("coordinator-gateway", "canary", "AGENT_GATEWAY", "gateway-fi", targetArtifact),
		coordinatorTarget("coordinator-worker", "canary", "CONTROL_WORKER", "worker-fi", targetArtifact),
		coordinatorTarget("coordinator-agent", "batch-1", "HOST_AGENT", "agent-fi", targetArtifact),
	})
	plan.SourceReleaseID = "kim-coordinator-source"
	plan.TargetReleaseID = "kim-coordinator-target"
	if _, err := postgres.PublishUpgradeCampaignPlan(ctx, primaryPool, plan); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}

	root, err := filepath.Abs("../../..")
	if err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	coordinatorBinary := buildBinary(t, root, filepath.Join(t.TempDir(), "kim-upgrade-coordinator"), "./cmd/kim-upgrade-coordinator")
	evaluator := digest("process-canary-evaluator")
	coordinatorA := startProcess(t, coordinatorBinary, coordinatorArguments(cluster.primaryURL, plan.CampaignID, "upgrade-coordinator-a", evaluator, "500ms", "3s", "100ms")...)
	coordinatorA.start(t)
	coordinatorAKilled := false
	defer func() {
		if !coordinatorAKilled {
			coordinatorA.stop()
		}
	}()

	var ownerA string
	var generationA int64
	eventually(t, 15*time.Second, func() bool {
		return primaryPool.QueryRow(ctx, `SELECT coordinator_owner,coordinator_claim_generation
			FROM kim.upgrade_campaigns_current WHERE campaign_id=$1`, plan.CampaignID).Scan(&ownerA, &generationA) == nil &&
			ownerA == "upgrade-coordinator-a" && generationA == 1
	}, "Coordinator A did not claim Campaign")
	eventually(t, 10*time.Second, func() bool {
		var renewals int
		return primaryPool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_coordinator_renewal_evidence
			WHERE campaign_id=$1 AND claim_generation=1`, plan.CampaignID).Scan(&renewals) == nil && renewals > 0
	}, "Coordinator A did not renew its DB claim")
	if err := postgres.RecordUpgradeTargetOutcome(ctx, primaryPool, postgres.UpgradeTargetOutcomeRequest{
		CampaignID: plan.CampaignID, TargetID: "coordinator-api", Owner: ownerA, ClaimGeneration: uint64(generationA),
		Outcome: "SUCCEEDED", ResultDigest: digest("coordinator-api-success"),
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() bool {
		var succeeded, pending int
		return primaryPool.QueryRow(ctx, `SELECT succeeded_targets,pending_targets FROM kim.upgrade_canary_decision_evidence
			WHERE campaign_id=$1 AND decision='HOLD' ORDER BY decided_at DESC LIMIT 1`, plan.CampaignID).Scan(&succeeded, &pending) == nil && succeeded == 1 && pending == 2
	}, "Coordinator A did not persist canary HOLD after accepted Target evidence")
	var restoreEpoch, committedLSN string
	var databaseGeneration int64
	if err := primaryPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&restoreEpoch, &databaseGeneration); err != nil {
		t.Fatal(err)
	}
	if err := primaryPool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&committedLSN); err != nil {
		t.Fatal(err)
	}
	coordinatorA.kill(t)
	coordinatorAKilled = true
	cluster.failover(t, ctx)
	primaryPool.Close()

	promotedPool := openPostgreSQLEventually(t, ctx, cluster.standbyURL)
	defer promotedPool.Close()
	var promoted, replayed bool
	var promotedEpoch string
	var promotedGeneration int64
	if err := promotedPool.QueryRow(ctx, `SELECT NOT pg_is_in_recovery(),pg_current_wal_lsn()>=$1::pg_lsn`, committedLSN).Scan(&promoted, &replayed); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&promotedEpoch, &promotedGeneration); err != nil {
		t.Fatal(err)
	}
	if !promoted || !replayed || promotedEpoch != restoreEpoch || promotedGeneration != databaseGeneration {
		t.Fatalf("promoted=%t replayed=%t authority=%s/%d want=%s/%d", promoted, replayed, promotedEpoch, promotedGeneration, restoreEpoch, databaseGeneration)
	}

	coordinatorB := startProcess(t, coordinatorBinary, coordinatorArguments(cluster.standbyURL, plan.CampaignID, "upgrade-coordinator-b", evaluator, "1s", "3s", "200ms")...)
	coordinatorB.start(t)
	coordinatorBFinished := false
	defer func() {
		if !coordinatorBFinished {
			coordinatorB.stop()
		}
	}()
	var ownerB, claimMode string
	var generationB int64
	eventually(t, 20*time.Second, func() bool {
		return promotedPool.QueryRow(ctx, `SELECT current.coordinator_owner,current.coordinator_claim_generation,attempt.claim_mode
			FROM kim.upgrade_campaigns_current current JOIN kim.upgrade_coordinator_attempt_evidence attempt
			 ON attempt.campaign_id=current.campaign_id AND attempt.claim_generation=current.coordinator_claim_generation
			WHERE current.campaign_id=$1`, plan.CampaignID).Scan(&ownerB, &generationB, &claimMode) == nil &&
			ownerB == "upgrade-coordinator-b" && generationB == 2
	}, "Coordinator B did not recover Campaign from promoted PostgreSQL")
	if claimMode != "RECOVER_FROM_DB" {
		t.Fatalf("Coordinator B claim mode=%s", claimMode)
	}
	if err := postgres.RecordUpgradeTargetOutcome(ctx, promotedPool, postgres.UpgradeTargetOutcomeRequest{
		CampaignID: plan.CampaignID, TargetID: "coordinator-gateway", Owner: ownerA, ClaimGeneration: uint64(generationA),
		Outcome: "SUCCEEDED", ResultDigest: digest("stale-coordinator-result"),
	}); !errors.Is(err, postgres.ErrStaleUpgradeCampaignClaim) {
		t.Fatalf("stale Coordinator A Result accepted: %v", err)
	}
	for _, targetID := range []string{"coordinator-gateway", "coordinator-worker"} {
		if err := postgres.RecordUpgradeTargetOutcome(ctx, promotedPool, postgres.UpgradeTargetOutcomeRequest{
			CampaignID: plan.CampaignID, TargetID: targetID, Owner: ownerB, ClaimGeneration: uint64(generationB),
			Outcome: "SUCCEEDED", ResultDigest: digest(targetID + "-success"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := waitForProcess(t, coordinatorB, 15*time.Second); err != nil {
		t.Fatalf("Coordinator B did not finish canary: %v: %s", err, coordinatorB.output.String())
	}
	coordinatorBFinished = true
	if !strings.Contains(coordinatorB.output.String(), "mode=RECOVER_FROM_DB") || !strings.Contains(coordinatorB.output.String(), "canary decision=CONTINUE") {
		t.Fatalf("Coordinator B output lost recovery/decision evidence: %s", coordinatorB.output.String())
	}
	var campaignState, waveID string
	var attempts, unknownEvents, targetEvents, duplicateDecisions, renewals int
	if err := promotedPool.QueryRow(ctx, `SELECT campaign_state,current_wave_id FROM kim.upgrade_campaigns_current WHERE campaign_id=$1`, plan.CampaignID).Scan(&campaignState, &waveID); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_coordinator_attempt_evidence WHERE campaign_id=$1`, plan.CampaignID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='COORDINATOR_UNKNOWN'),
		count(*) FILTER (WHERE event_type='TARGET_RESULT_ACCEPTED') FROM kim.upgrade_campaign_event_evidence
		WHERE campaign_id=$1`, plan.CampaignID).Scan(&unknownEvents, &targetEvents); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT decision,succeeded_targets,failed_targets,unknown_targets,pending_targets,count(*)
		FROM kim.upgrade_canary_decision_evidence WHERE campaign_id=$1
		GROUP BY decision,succeeded_targets,failed_targets,unknown_targets,pending_targets HAVING count(*)>1
	) duplicate`, plan.CampaignID).Scan(&duplicateDecisions); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_coordinator_renewal_evidence WHERE campaign_id=$1`, plan.CampaignID).Scan(&renewals); err != nil {
		t.Fatal(err)
	}
	if campaignState != "ROLLING" || waveID != "batch-1" || attempts != 2 || unknownEvents != 1 || targetEvents != 3 || duplicateDecisions != 0 || renewals < 1 {
		t.Fatalf("state=%s/%s attempts=%d unknown=%d target_events=%d duplicate_decisions=%d renewals=%d",
			campaignState, waveID, attempts, unknownEvents, targetEvents, duplicateDecisions, renewals)
	}
	t.Logf("Coordinator failover converged: authority=%s/%d attempts=%d mode=%s renewals=%d state=%s/%s",
		promotedEpoch, promotedGeneration, attempts, claimMode, renewals, campaignState, waveID)
}

func coordinatorArguments(databaseURL, campaignID, owner, evaluator, lease, maximum, renewal string) []string {
	return []string{"-database-url", databaseURL, "-campaign-id", campaignID, "-coordinator-id", owner,
		"-canary-evaluator-artifact-digest", evaluator, "-claim-lease", lease,
		"-claim-maximum-lifetime", maximum, "-claim-renew-interval", renewal,
		"-poll-interval", "50ms", "-database-max-connections", "4"}
}

func coordinatorTarget(targetID, waveID, componentType, componentID, artifact string) postgres.UpgradeTargetPlan {
	return postgres.UpgradeTargetPlan{TargetID: targetID, WaveID: waveID, ComponentType: componentType,
		ComponentID: componentID, TargetReleaseID: "kim-coordinator-target", TargetManifestRevision: 1,
		TargetArtifactDigest: artifact}
}
