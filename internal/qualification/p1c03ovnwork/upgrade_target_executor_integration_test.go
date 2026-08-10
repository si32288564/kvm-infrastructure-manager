package p1c03ovnwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/upgrade/targetexecutor"
)

func TestUpgradeTargetExecutorProcessKillMultipleUnknownReadBackRecovery(t *testing.T) {
	if testing.Short() || os.Getenv("KIM_RUN_DOCKER_POSTGRES_UPGRADE_TARGET_EXECUTOR") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_UPGRADE_TARGET_EXECUTOR is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	container := startRenewalResponseLossPostgreSQL(t, ctx)
	databaseURL := postgresContainerURL(t, ctx, container)
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES($1,1,'ACTIVE')`, fmt.Sprintf("upgrade-target-executor-%d", time.Now().UnixNano())); err != nil {
		t.Fatal(err)
	}
	targetArtifact := digest("target-executor-artifact")
	publishRollingManifest(t, ctx, pool, "kim-target-executor-source", "1.0.0", digest("target-executor-source"), []string{postgres.OVNRuntimeWorkSchemaV1})
	publishRollingManifest(t, ctx, pool, "kim-target-executor-target", "1.1.0", targetArtifact, []string{postgres.OVNRuntimeWorkSchemaV1})
	targets := []postgres.UpgradeTargetPlan{
		coordinatorTarget("executor-api", "canary", "API", "api-process", targetArtifact),
		coordinatorTarget("executor-gateway", "canary", "AGENT_GATEWAY", "gateway-process", targetArtifact),
		coordinatorTarget("executor-worker", "canary", "CONTROL_WORKER", "worker-process", targetArtifact),
		coordinatorTarget("executor-agent", "batch-1", "HOST_AGENT", "agent-process", targetArtifact),
	}
	for index := range targets {
		targets[index].TargetReleaseID = "kim-target-executor-target"
	}
	plan := campaignPlan("upgrade-target-executor", targetArtifact, 0, targets)
	plan.SourceReleaseID = "kim-target-executor-source"
	plan.TargetReleaseID = "kim-target-executor-target"
	if _, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, plan); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	binaryDirectory := t.TempDir()
	coordinatorBinary := buildBinary(t, root, filepath.Join(binaryDirectory, "kim-upgrade-coordinator"), "./cmd/kim-upgrade-coordinator")
	executorBinary := buildBinary(t, root, filepath.Join(binaryDirectory, "kim-upgrade-target-executor"), "./cmd/kim-upgrade-target-executor")
	stateDirectory := filepath.Join(t.TempDir(), "target-state")
	coordinator := startProcess(t, coordinatorBinary, coordinatorArguments(databaseURL, plan.CampaignID,
		"target-fi-coordinator", digest("target-fi-evaluator"), "3s", "30s", "500ms")...)
	coordinator.start(t)
	coordinatorFinished := false
	defer func() {
		if !coordinatorFinished {
			coordinator.stop()
		}
	}()
	var coordinatorGeneration int64
	eventually(t, 15*time.Second, func() bool {
		return pool.QueryRow(ctx, `SELECT coordinator_claim_generation FROM kim.upgrade_campaigns_current
			WHERE campaign_id=$1 AND coordinator_owner='target-fi-coordinator'`, plan.CampaignID).Scan(&coordinatorGeneration) == nil && coordinatorGeneration == 1
	}, "Coordinator did not claim Target fault Campaign")

	canaryIDs := []string{"executor-api", "executor-gateway", "executor-worker"}
	firstExecutors := make([]*process, 0, len(canaryIDs))
	for index, targetID := range canaryIDs {
		executor := startProcess(t, executorBinary, targetExecutorArguments(databaseURL, plan.CampaignID, targetID,
			fmt.Sprintf("target-executor-a-%d", index), stateDirectory, "500ms", "10s", "100ms", "5s")...)
		executor.start(t)
		firstExecutors = append(firstExecutors, executor)
	}
	for index, targetID := range canaryIDs {
		waitForFileOrProcessFailure(t, firstExecutors[index], targetexecutor.MarkerPath(stateDirectory, targetID),
			15*time.Second, "Target side effect was not materialized before process kill")
	}
	renewalsBeforeKill := 0
	eventually(t, 10*time.Second, func() bool {
		return pool.QueryRow(ctx, `SELECT count(DISTINCT target_id) FROM kim.upgrade_target_renewal_evidence
			WHERE target_id=ANY($1)`, canaryIDs).Scan(&renewalsBeforeKill) == nil && renewalsBeforeKill == len(canaryIDs)
	}, "Target executors did not persist DB-time renewal evidence before process kill")
	for _, executor := range firstExecutors {
		executor.kill(t)
	}

	secondExecutors := make([]*process, 0, len(canaryIDs))
	for index, targetID := range canaryIDs {
		executor := startProcess(t, executorBinary, targetExecutorArguments(databaseURL, plan.CampaignID, targetID,
			fmt.Sprintf("target-executor-b-%d", index), stateDirectory, "2s", "10s", "500ms", "0s")...)
		executor.start(t)
		secondExecutors = append(secondExecutors, executor)
	}
	for index, executor := range secondExecutors {
		if err := waitForProcess(t, executor, 20*time.Second); err != nil {
			t.Fatalf("successor executor %s failed: %v: %s", canaryIDs[index], err, executor.output.String())
		}
	}
	terminalReplay := startProcess(t, executorBinary, targetExecutorArguments(databaseURL, plan.CampaignID, canaryIDs[0],
		"target-executor-terminal-replay", stateDirectory, "2s", "10s", "500ms", "0s")...)
	terminalReplay.start(t)
	if err := waitForProcess(t, terminalReplay, 10*time.Second); err != nil {
		t.Fatalf("terminal Target replay did not converge: %v: %s", err, terminalReplay.output.String())
	}
	if err := postgres.CompleteUpgradeTarget(ctx, pool, postgres.UpgradeTargetCompletionRequest{
		TargetID: canaryIDs[0], Owner: "target-executor-a-0", AttemptGeneration: 1, Outcome: "SUCCEEDED",
		ResultDigest: digest("stale-target-result"), ObservedDigest: digest("stale-target-observation"),
	}); !errors.Is(err, postgres.ErrStaleUpgradeTargetClaim) {
		t.Fatalf("stale Target executor completion accepted: %v", err)
	}
	if err := waitForProcess(t, coordinator, 20*time.Second); err != nil {
		t.Fatalf("Coordinator did not advance after Target recovery: %v: %s", err, coordinator.output.String())
	}
	coordinatorFinished = true
	concurrentClaims := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			_, claimErr := postgres.ClaimUpgradeTarget(ctx, pool, postgres.UpgradeTargetClaimRequest{
				CampaignID: plan.CampaignID, TargetID: "executor-agent", Owner: fmt.Sprintf("batch-target-%d", index),
				Lease: 2 * time.Second, MaximumLifetime: 5 * time.Second,
			})
			concurrentClaims <- claimErr
		}(index)
	}
	claimSuccesses, claimConflicts := 0, 0
	for index := 0; index < 2; index++ {
		switch claimErr := <-concurrentClaims; {
		case claimErr == nil:
			claimSuccesses++
		case errors.Is(claimErr, postgres.ErrUpgradeTargetClaimUnavailable):
			claimConflicts++
		default:
			t.Fatalf("unexpected concurrent Target claim error: %v", claimErr)
		}
	}
	if claimSuccesses != 1 || claimConflicts != 1 {
		t.Fatalf("concurrent Target claim successes=%d conflicts=%d", claimSuccesses, claimConflicts)
	}

	var campaignState, waveID string
	var attempts, readBackAttempts, unknownEvents, applyEvents, results, succeeded, renewals int
	if err := pool.QueryRow(ctx, `SELECT campaign_state,current_wave_id FROM kim.upgrade_campaigns_current WHERE campaign_id=$1`,
		plan.CampaignID).Scan(&campaignState, &waveID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE attempt_mode='READ_BACK_FIRST')
		FROM kim.upgrade_target_attempt_evidence WHERE campaign_id=$1 AND wave_id='canary'`, plan.CampaignID).Scan(&attempts, &readBackAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='TARGET_UNKNOWN'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED') FROM kim.upgrade_target_execution_event_evidence
		WHERE target_id=ANY($1)`, canaryIDs).Scan(&unknownEvents, &applyEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_result_evidence WHERE target_id=ANY($1)`, canaryIDs).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_targets_current WHERE target_id=ANY($1) AND target_state='SUCCEEDED'`, canaryIDs).Scan(&succeeded); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_renewal_evidence WHERE target_id=ANY($1)`, canaryIDs).Scan(&renewals); err != nil {
		t.Fatal(err)
	}
	markerMatches := 0
	for _, targetID := range canaryIDs {
		if _, err := os.Stat(targetexecutor.MarkerPath(stateDirectory, targetID)); err == nil {
			markerMatches++
		}
	}
	if campaignState != "ROLLING" || waveID != "batch-1" || attempts != 6 || readBackAttempts != 3 ||
		unknownEvents != 3 || applyEvents != 3 || results != 3 || succeeded != 3 || markerMatches != 3 || renewals < 3 {
		t.Fatalf("campaign=%s/%s attempts=%d readback=%d unknown=%d apply=%d results=%d succeeded=%d markers=%d renewals=%d",
			campaignState, waveID, attempts, readBackAttempts, unknownEvents, applyEvents, results, succeeded, markerMatches, renewals)
	}
	t.Logf("Target executor recovery converged: targets=%d attempts=%d unknown=%d readback=%d physical_markers=%d state=%s/%s",
		succeeded, attempts, unknownEvents, readBackAttempts, markerMatches, campaignState, waveID)
}

func targetExecutorArguments(databaseURL, campaignID, targetID, owner, stateDirectory, lease, maximum, renewal, settle string) []string {
	return []string{"-database-url", databaseURL, "-campaign-id", campaignID, "-target-id", targetID,
		"-executor-id", owner, "-state-directory", stateDirectory, "-claim-lease", lease,
		"-claim-maximum-lifetime", maximum, "-claim-renew-interval", renewal, "-claim-poll-interval", "20ms",
		"-observation-settle-window", settle, "-database-max-connections", "4"}
}
