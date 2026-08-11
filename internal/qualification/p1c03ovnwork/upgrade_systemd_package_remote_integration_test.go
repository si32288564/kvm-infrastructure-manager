package p1c03ovnwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/upgrade/targetexecutor"
)

const defaultSystemdQualificationHost = "kvm-base-g01-n001-p.core.s01.si1230.com"

func TestUpgradeTargetSystemdDebianPackageFaultRecovery(t *testing.T) {
	if testing.Short() || os.Getenv("KIM_RUN_REMOTE_SYSTEMD_PACKAGE_UPGRADE") != "1" {
		t.Skip("KIM_RUN_REMOTE_SYSTEMD_PACKAGE_UPGRADE is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	host := os.Getenv("KIM_P1D03_SYSTEMD_HOST")
	if host == "" {
		host = defaultSystemdQualificationHost
	}
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	packageName := "kim-upgrade-fixture-" + nonce
	serviceName := packageName + ".service"
	remoteRoot := "/tmp/kim-upgrade-systemd-" + nonce
	binaryPath := "/usr/lib/" + packageName + "/component"
	healthPath := "/run/" + packageName + "/health.json"
	installLog := "/var/lib/" + packageName + "/install.log"
	executorUnitA := packageName + "-executor-a.service"
	executorUnitB := packageName + "-executor-b.service"
	executorUnitC := packageName + "-executor-c.service"
	executorUnitD := packageName + "-executor-d.service"
	executorUnitE := packageName + "-executor-e.service"
	recoveryUnit := packageName + "-recovery.service"
	lockUnit := packageName + "-dpkg-lock.service"
	remoteCleanup := fmt.Sprintf("sudo -n systemctl stop %s %s %s %s %s %s %s %s >/dev/null 2>&1 || true; "+
		"sudo -n dpkg --purge %s >/dev/null 2>&1 || true; sudo -n systemctl daemon-reload; "+
		"sudo -n systemctl reset-failed %s %s %s %s %s %s %s %s >/dev/null 2>&1 || true; "+
		"sudo -n rm -rf %s %s %s", shellSafe(serviceName), shellSafe(executorUnitA), shellSafe(executorUnitB),
		shellSafe(executorUnitC), shellSafe(executorUnitD), shellSafe(executorUnitE), shellSafe(recoveryUnit), shellSafe(lockUnit),
		shellSafe(packageName), shellSafe(serviceName), shellSafe(executorUnitA), shellSafe(executorUnitB), shellSafe(executorUnitC),
		shellSafe(executorUnitD), shellSafe(executorUnitE), shellSafe(recoveryUnit), shellSafe(lockUnit), shellSafe(remoteRoot),
		shellSafe(filepath.Dir(healthPath)), shellSafe(filepath.Dir(installLog)))
	t.Cleanup(func() { _, _ = remoteCommand(context.Background(), host, remoteCleanup) })
	remoteMust(t, ctx, host, "mkdir -p "+shellSafe(remoteRoot))

	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	buildDirectory := t.TempDir()
	componentV1 := buildLinuxBinary(t, repositoryRoot, filepath.Join(buildDirectory, "component-v1"),
		"./cmd/kim-upgrade-fixture-component", "-X main.version=1.0.0")
	componentV2 := buildLinuxBinary(t, repositoryRoot, filepath.Join(buildDirectory, "component-v2"),
		"./cmd/kim-upgrade-fixture-component", "-X main.version=2.0.0")
	componentV3 := buildLinuxBinary(t, repositoryRoot, filepath.Join(buildDirectory, "component-v3"),
		"./cmd/kim-upgrade-fixture-component", "-X main.version=3.0.0")
	executorBinary := buildLinuxBinary(t, repositoryRoot, filepath.Join(buildDirectory, "kim-upgrade-target-executor"),
		"./cmd/kim-upgrade-target-executor", "")
	recoveryBinary := buildLinuxBinary(t, repositoryRoot, filepath.Join(buildDirectory, "kim-upgrade-recovery-executor"),
		"./cmd/kim-upgrade-recovery-executor", "")
	binaryDigestV2 := localFileDigest(t, componentV2)
	binaryDigestV3 := localFileDigest(t, componentV3)
	interruptMarker := remoteRoot + "/postinst-v3-started"
	interruptRelease := remoteRoot + "/postinst-v3-release"
	stageV1 := createDebianFixtureStage(t, packageName, serviceName, binaryPath, healthPath, installLog, "1.0.0", componentV1, "", "")
	stageV2 := createDebianFixtureStage(t, packageName, serviceName, binaryPath, healthPath, installLog, "2.0.0", componentV2, "", "")
	stageV3 := createDebianFixtureStage(t, packageName, serviceName, binaryPath, healthPath, installLog, "3.0.0", componentV3,
		interruptMarker, interruptRelease)
	remoteCopy(t, ctx, host, stageV1, remoteRoot+"/v1")
	remoteCopy(t, ctx, host, stageV2, remoteRoot+"/v2")
	remoteCopy(t, ctx, host, stageV3, remoteRoot+"/v3")
	remoteCopy(t, ctx, host, executorBinary, remoteRoot+"/kim-upgrade-target-executor")
	remoteCopy(t, ctx, host, recoveryBinary, remoteRoot+"/kim-upgrade-recovery-executor")
	remoteMust(t, ctx, host, fmt.Sprintf("chmod 0755 %s/kim-upgrade-target-executor %s/kim-upgrade-recovery-executor; dpkg-deb --build %s/v1 %s/v1.deb >/dev/null; dpkg-deb --build %s/v2 %s/v2.deb >/dev/null; dpkg-deb --build %s/v3 %s/v3.deb >/dev/null",
		shellSafe(remoteRoot),
		shellSafe(remoteRoot), shellSafe(remoteRoot), shellSafe(remoteRoot), shellSafe(remoteRoot), shellSafe(remoteRoot),
		shellSafe(remoteRoot), shellSafe(remoteRoot)))
	packageDigestV1 := strings.Fields(remoteMust(t, ctx, host, "sha256sum "+shellSafe(remoteRoot+"/v1.deb")))[0]
	packageDigestV2 := strings.Fields(remoteMust(t, ctx, host, "sha256sum "+shellSafe(remoteRoot+"/v2.deb")))[0]
	packageDigestV3 := strings.Fields(remoteMust(t, ctx, host, "sha256sum "+shellSafe(remoteRoot+"/v3.deb")))[0]

	profile := targetexecutor.SystemdPackageProfile{SchemaVersion: "kim.upgrade.systemd-package-profile/v1",
		ComponentType: "CONTROL_WORKER", ComponentID: packageName,
		PackageName: packageName, ServiceName: serviceName, BinaryPath: binaryPath, HealthPath: healthPath,
		HealthSchema: "kim.upgrade.fixture-health/v1",
		Artifacts: map[string]targetexecutor.SystemdPackageArtifact{packageDigestV2: {
			PackagePath: remoteRoot + "/v2.deb", PackageVersion: "2.0.0", BinaryDigest: binaryDigestV2,
		}, packageDigestV3: {
			PackagePath: remoteRoot + "/v3.deb", PackageVersion: "3.0.0", BinaryDigest: binaryDigestV3,
		}},
	}
	profileRaw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(buildDirectory, "systemd-package-profile.json")
	if err := os.WriteFile(profilePath, profileRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	remoteCopy(t, ctx, host, profilePath, remoteRoot+"/profile.json")
	remoteMust(t, ctx, host, fmt.Sprintf("chmod 0600 %s/profile.json; sudo -n chown root:root %s/profile.json; sudo -n dpkg --install %s/v1.deb >/dev/null; sudo -n systemctl daemon-reload; sudo -n systemctl restart %s",
		shellSafe(remoteRoot), shellSafe(remoteRoot), shellSafe(remoteRoot), shellSafe(serviceName)))
	eventuallyRemote(t, ctx, host, 20*time.Second, func(output string) bool {
		return strings.Contains(output, "1.0.0") && strings.Contains(output, `"ready":true`)
	}, fmt.Sprintf("sudo -n cat %s", shellSafe(healthPath)), "source package service did not become healthy")

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
		VALUES($1,1,'ACTIVE')`, "systemd-package-"+nonce); err != nil {
		t.Fatal(err)
	}
	publishRollingManifest(t, ctx, pool, "kim-systemd-source", "1.0.0", packageDigestV1, []string{postgres.OVNRuntimeWorkSchemaV1})
	publishRollingManifest(t, ctx, pool, "kim-systemd-target", "2.0.0", packageDigestV2, []string{postgres.OVNRuntimeWorkSchemaV1})
	target := postgres.UpgradeTargetPlan{TargetID: "systemd-package-target-" + nonce, WaveID: "canary",
		ComponentType: "CONTROL_WORKER", ComponentID: packageName, TargetReleaseID: "kim-systemd-target",
		TargetManifestRevision: 1, TargetArtifactDigest: packageDigestV2}
	batchTarget := postgres.UpgradeTargetPlan{TargetID: "systemd-package-batch-" + nonce, WaveID: "batch-1",
		ComponentType: "HOST_AGENT", ComponentID: "deferred-agent-" + nonce, TargetReleaseID: "kim-systemd-target",
		TargetManifestRevision: 1, TargetArtifactDigest: packageDigestV2}
	plan := campaignPlan("systemd-package-campaign-"+nonce, packageDigestV2, 0, []postgres.UpgradeTargetPlan{target, batchTarget})
	plan.SourceReleaseID = "kim-systemd-source"
	plan.TargetReleaseID = "kim-systemd-target"
	if _, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, plan); err != nil {
		t.Fatal(err)
	}
	coordinatorBinary := buildBinary(t, repositoryRoot, filepath.Join(buildDirectory, "kim-upgrade-coordinator"), "./cmd/kim-upgrade-coordinator")
	coordinator := startProcess(t, coordinatorBinary, coordinatorArguments(databaseURL, plan.CampaignID,
		"systemd-package-coordinator", digest("systemd-package-evaluator"), "5s", "1m", "1s")...)
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
			WHERE campaign_id=$1 AND coordinator_owner='systemd-package-coordinator'`, plan.CampaignID).Scan(&coordinatorGeneration) == nil && coordinatorGeneration == 1
	}, "Coordinator did not claim systemd package Campaign")

	parsedDatabaseURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	localPort := parsedDatabaseURL.Port()
	remotePort := 43000 + int(time.Now().UnixNano()%1000)
	tunnel := startReverseTunnel(t, ctx, host, remotePort, localPort)
	defer stopCommand(tunnel)
	remoteDatabaseURL := fmt.Sprintf("postgres://postgres:kimtest@127.0.0.1:%d/kimtest?sslmode=disable", remotePort)
	lockMarker := remoteRoot + "/dpkg-lock-held"
	lockScript := fmt.Sprintf("import fcntl,time,pathlib; f=open('/var/lib/dpkg/lock','r+'); fcntl.lockf(f,fcntl.LOCK_EX); pathlib.Path(%q).write_text('locked\\n'); time.sleep(30)", lockMarker)
	remoteMust(t, ctx, host, fmt.Sprintf("sudo -n systemd-run --quiet --collect --unit=%s --property=Type=exec -- /usr/bin/python3 -c %s",
		shellSafe(lockUnit), shellSafe(lockScript)))
	eventuallyRemote(t, ctx, host, 10*time.Second, func(output string) bool { return strings.Contains(output, "locked") },
		"cat "+shellSafe(lockMarker), "dpkg lock holder did not acquire the package database lock")
	startRemoteTargetExecutor(t, ctx, host, executorUnitA, remoteRoot+"/kim-upgrade-target-executor",
		remoteDatabaseURL, plan.CampaignID, target.TargetID, "systemd-target-a", remoteRoot+"/profile.json", "0s")
	eventuallyRemote(t, ctx, host, 10*time.Second, func(output string) bool {
		return strings.Contains(output, "failed") || strings.Contains(output, "inactive")
	}, fmt.Sprintf("sudo -n systemctl show %s --property=ActiveState --value 2>/dev/null || true", shellSafe(executorUnitA)),
		"Target executor did not stop after dpkg lock contention")
	lockFailureLog := remoteMust(t, ctx, host, fmt.Sprintf("sudo -n journalctl --unit=%s --no-pager -n 100", shellSafe(executorUnitA)))
	if !strings.Contains(lockFailureLog, "dpkg database lock") {
		t.Fatalf("Target executor did not record the injected dpkg lock contention: %s", lockFailureLog)
	}
	if output := remoteMust(t, ctx, host, fmt.Sprintf("dpkg-query -W -f='${Version}' %s; sudo -n awk '$1==\"2.0.0\"{count++} END{print \" installs=\" count+0}' %s",
		shellSafe(packageName), shellSafe(installLog))); !strings.Contains(output, "1.0.0") || !strings.Contains(output, "installs=0") {
		t.Fatalf("lock-contended Attempt changed package state: %s", output)
	}
	remoteMust(t, ctx, host, "sudo -n systemctl stop "+shellSafe(lockUnit))

	startRemoteTargetExecutor(t, ctx, host, executorUnitB, remoteRoot+"/kim-upgrade-target-executor",
		remoteDatabaseURL, plan.CampaignID, target.TargetID, "systemd-target-b", remoteRoot+"/profile.json", "10s")
	eventuallyRemote(t, ctx, host, 30*time.Second, func(output string) bool {
		return strings.Contains(output, "2.0.0") && strings.Contains(output, `"ready":true`)
	}, fmt.Sprintf("sudo -n cat %s", shellSafe(healthPath)), "Target package/service side effect did not become healthy")
	eventually(t, 10*time.Second, func() bool {
		var state string
		var attempt, renewals int
		return pool.QueryRow(ctx, `SELECT execution_state,attempt_generation FROM kim.upgrade_target_executions_current
			WHERE target_id=$1`, target.TargetID).Scan(&state, &attempt) == nil && state == "CLAIMED" && attempt == 2 &&
			pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_renewal_evidence WHERE target_id=$1`, target.TargetID).Scan(&renewals) == nil && renewals > 0
	}, "Target executor did not retain Attempt 2 after package restart")
	remoteMust(t, ctx, host, "sudo -n systemctl kill --kill-whom=all --signal=SIGKILL "+shellSafe(executorUnitB))

	startRemoteTargetExecutor(t, ctx, host, executorUnitC, remoteRoot+"/kim-upgrade-target-executor",
		remoteDatabaseURL, plan.CampaignID, target.TargetID, "systemd-target-c", remoteRoot+"/profile.json", "0s")
	eventually(t, 30*time.Second, func() bool {
		var state string
		var attempt int
		return pool.QueryRow(ctx, `SELECT target_state,attempt_generation FROM kim.upgrade_targets_current WHERE target_id=$1`,
			target.TargetID).Scan(&state, &attempt) == nil && state == "SUCCEEDED" && attempt == 3
	}, "successor Target executor did not recover installed package from read-back")
	if err := waitForProcess(t, coordinator, 20*time.Second); err != nil {
		t.Fatalf("Coordinator did not advance after systemd Target recovery: %v: %s", err, coordinator.output.String())
	}
	coordinatorFinished = true

	var attempts, unknownEvents, readBackAttempts, applyEvents, results int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE attempt_mode='READ_BACK_FIRST')
		FROM kim.upgrade_target_attempt_evidence WHERE target_id=$1`, target.TargetID).Scan(&attempts, &readBackAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='TARGET_UNKNOWN'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED') FROM kim.upgrade_target_execution_event_evidence
		WHERE target_id=$1`, target.TargetID).Scan(&unknownEvents, &applyEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_result_evidence WHERE target_id=$1`, target.TargetID).Scan(&results); err != nil {
		t.Fatal(err)
	}
	installEntries, err := strconv.Atoi(strings.TrimSpace(remoteMust(t, ctx, host,
		fmt.Sprintf("sudo -n awk '$1==\"2.0.0\"{count++} END{print count+0}' %s", shellSafe(installLog)))))
	if err != nil {
		t.Fatal(err)
	}
	processDigest := strings.Fields(remoteMust(t, ctx, host, fmt.Sprintf("pid=$(systemctl show %s -p MainPID --value); sudo -n sha256sum /proc/$pid/exe", shellSafe(serviceName))))[0]
	var campaignState, waveID string
	if err := pool.QueryRow(ctx, `SELECT campaign_state,current_wave_id FROM kim.upgrade_campaigns_current WHERE campaign_id=$1`,
		plan.CampaignID).Scan(&campaignState, &waveID); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || readBackAttempts != 2 || unknownEvents != 2 || applyEvents != 2 || results != 1 ||
		installEntries != 1 || processDigest != binaryDigestV2 || campaignState != "ROLLING" || waveID != "batch-1" {
		t.Fatalf("attempts=%d readback=%d unknown=%d apply=%d results=%d installs=%d process=%s campaign=%s/%s",
			attempts, readBackAttempts, unknownEvents, applyEvents, results, installEntries, processDigest, campaignState, waveID)
	}

	publishRollingManifest(t, ctx, pool, "kim-systemd-interrupted", "3.0.0", packageDigestV3, []string{postgres.OVNRuntimeWorkSchemaV1})
	interruptedTarget := postgres.UpgradeTargetPlan{TargetID: "systemd-package-interrupted-" + nonce, WaveID: "canary",
		ComponentType: "CONTROL_WORKER", ComponentID: packageName, TargetReleaseID: "kim-systemd-interrupted",
		TargetManifestRevision: 1, TargetArtifactDigest: packageDigestV3}
	interruptedBatch := postgres.UpgradeTargetPlan{TargetID: "systemd-package-interrupted-batch-" + nonce, WaveID: "batch-1",
		ComponentType: "HOST_AGENT", ComponentID: "deferred-interrupted-agent-" + nonce,
		TargetReleaseID: "kim-systemd-interrupted", TargetManifestRevision: 1, TargetArtifactDigest: packageDigestV3}
	interruptedPlan := campaignPlan("systemd-package-interrupted-campaign-"+nonce, packageDigestV3, 0,
		[]postgres.UpgradeTargetPlan{interruptedTarget, interruptedBatch})
	interruptedPlan.SourceReleaseID = "kim-systemd-target"
	interruptedPlan.TargetReleaseID = "kim-systemd-interrupted"
	if _, err := postgres.PublishUpgradeCampaignPlan(ctx, pool, interruptedPlan); err != nil {
		t.Fatal(err)
	}
	interruptedCoordinator := startProcess(t, coordinatorBinary, coordinatorArguments(databaseURL, interruptedPlan.CampaignID,
		"systemd-interrupted-coordinator", digest("systemd-interrupted-evaluator"), "5s", "1m", "1s")...)
	interruptedCoordinator.start(t)
	defer interruptedCoordinator.stop()
	eventually(t, 15*time.Second, func() bool {
		var generation int
		return pool.QueryRow(ctx, `SELECT coordinator_claim_generation FROM kim.upgrade_campaigns_current
			WHERE campaign_id=$1 AND coordinator_owner='systemd-interrupted-coordinator'`, interruptedPlan.CampaignID).Scan(&generation) == nil && generation == 1
	}, "Coordinator did not claim interrupted package Campaign")

	startRemoteTargetExecutor(t, ctx, host, executorUnitD, remoteRoot+"/kim-upgrade-target-executor",
		remoteDatabaseURL, interruptedPlan.CampaignID, interruptedTarget.TargetID, "systemd-target-d",
		remoteRoot+"/profile.json", "0s")
	eventuallyRemote(t, ctx, host, 20*time.Second, func(output string) bool { return strings.Contains(output, "postinst-started") },
		"cat "+shellSafe(interruptMarker), "v3 postinst did not enter the interruption gate")
	eventually(t, 10*time.Second, func() bool {
		var attempt, renewals int
		return pool.QueryRow(ctx, `SELECT attempt_generation FROM kim.upgrade_target_executions_current WHERE target_id=$1`,
			interruptedTarget.TargetID).Scan(&attempt) == nil && attempt == 1 &&
			pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_renewal_evidence WHERE target_id=$1`,
				interruptedTarget.TargetID).Scan(&renewals) == nil && renewals > 0
	}, "interrupted package Target did not retain Attempt 1 authority")
	remoteMust(t, ctx, host, "sudo -n systemctl kill --kill-whom=all --signal=SIGKILL "+shellSafe(executorUnitD))
	var interruptedPackageStatus string
	eventuallyRemote(t, ctx, host, 10*time.Second, func(output string) bool {
		interruptedPackageStatus = strings.TrimSpace(output)
		return strings.Contains(interruptedPackageStatus, "half-configured") && strings.Contains(interruptedPackageStatus, "3.0.0")
	}, fmt.Sprintf("dpkg-query -W -f='${Status} ${Version}' %s", shellSafe(packageName)),
		"interrupted package did not enter half-configured state")

	startRemoteTargetExecutor(t, ctx, host, executorUnitE, remoteRoot+"/kim-upgrade-target-executor",
		remoteDatabaseURL, interruptedPlan.CampaignID, interruptedTarget.TargetID, "systemd-target-e",
		remoteRoot+"/profile.json", "0s")
	eventually(t, 20*time.Second, func() bool {
		var targetState, executionState string
		return pool.QueryRow(ctx, `SELECT target.target_state,execution.execution_state
			FROM kim.upgrade_targets_current target JOIN kim.upgrade_target_executions_current execution USING(target_id)
			WHERE target.target_id=$1`, interruptedTarget.TargetID).Scan(&targetState, &executionState) == nil &&
			targetState == "FENCED" && executionState == "FENCED"
	}, "conflicting package observation did not fence Target authority")
	eventually(t, 20*time.Second, func() bool {
		var state string
		return pool.QueryRow(ctx, `SELECT campaign_state FROM kim.upgrade_campaigns_current WHERE campaign_id=$1`,
			interruptedPlan.CampaignID).Scan(&state) == nil && state == "PAUSED"
	}, "FENCED package Target did not pause the Campaign")
	if _, err := postgres.ClaimUpgradeTarget(ctx, pool, postgres.UpgradeTargetClaimRequest{
		CampaignID: interruptedPlan.CampaignID, TargetID: interruptedTarget.TargetID, Owner: "forbidden-retry",
		Lease: 2 * time.Second, MaximumLifetime: 5 * time.Second,
	}); !errors.Is(err, postgres.ErrUpgradeTargetClaimUnavailable) {
		t.Fatalf("FENCED package Target was reclaimable: %v", err)
	}
	var interruptedAttempts, interruptedReadBack, interruptedUnknown, interruptedApply, interruptedQuarantine, interruptedResults int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE attempt_mode='READ_BACK_FIRST')
		FROM kim.upgrade_target_attempt_evidence WHERE target_id=$1`, interruptedTarget.TargetID).Scan(
		&interruptedAttempts, &interruptedReadBack); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='TARGET_UNKNOWN'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED'),count(*) FILTER (WHERE event_type='CONFLICT_QUARANTINED')
		FROM kim.upgrade_target_execution_event_evidence WHERE target_id=$1`, interruptedTarget.TargetID).Scan(
		&interruptedUnknown, &interruptedApply, &interruptedQuarantine); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_result_evidence WHERE target_id=$1`,
		interruptedTarget.TargetID).Scan(&interruptedResults); err != nil {
		t.Fatal(err)
	}
	v3InstallEntries, err := strconv.Atoi(strings.TrimSpace(remoteMust(t, ctx, host,
		fmt.Sprintf("sudo -n awk '$1==\"3.0.0\"{count++} END{print count+0}' %s", shellSafe(installLog)))))
	if err != nil {
		t.Fatal(err)
	}
	if interruptedAttempts != 2 || interruptedReadBack != 1 || interruptedUnknown != 1 || interruptedApply != 1 ||
		interruptedQuarantine != 1 || interruptedResults != 0 || v3InstallEntries != 0 {
		t.Fatalf("interrupted attempts=%d readback=%d unknown=%d apply=%d quarantine=%d results=%d installs=%d status=%s",
			interruptedAttempts, interruptedReadBack, interruptedUnknown, interruptedApply, interruptedQuarantine,
			interruptedResults, v3InstallEntries, interruptedPackageStatus)
	}

	recoveryAuthorizationDigest := digest("package-recovery-authorization:" + nonce)
	recoveryPlanRequest := postgres.UpgradeTargetRecoveryPlanRequest{
		RecoveryPlanID: "package-recovery-plan-" + nonce, TargetID: interruptedTarget.TargetID,
		Strategy: postgres.UpgradeRecoveryConfigureExisting, AuthorizationID: "operator-recovery-approval-" + nonce,
		AuthorizationDigest: recoveryAuthorizationDigest, RecoveryProfileRevision: 1,
	}
	recoveryPlan, err := postgres.ApproveUpgradeTargetRecoveryPlan(ctx, pool, recoveryPlanRequest)
	if err != nil {
		t.Fatal(err)
	}
	replayedPlan, err := postgres.ApproveUpgradeTargetRecoveryPlan(ctx, pool, recoveryPlanRequest)
	if err != nil || replayedPlan.PlanDigest != recoveryPlan.PlanDigest ||
		replayedPlan.RecoveryGeneration != recoveryPlan.RecoveryGeneration {
		t.Fatalf("stable Recovery Plan replay diverged: original=%+v replay=%+v err=%v", recoveryPlan, replayedPlan, err)
	}
	// The explicit Plan includes an operator-controlled precondition that allows the previously blocked
	// maintainer script to finish. The recovery executor still owns only the closed dpkg --configure operation.
	remoteMust(t, ctx, host, "touch "+shellSafe(interruptRelease))
	startRemoteRecoveryExecutor(t, ctx, host, recoveryUnit, remoteRoot+"/kim-upgrade-recovery-executor",
		remoteDatabaseURL, interruptedTarget.TargetID, "systemd-recovery-a", remoteRoot+"/profile.json", "0s")
	eventually(t, 30*time.Second, func() bool {
		var recoveryState, targetState, executionState string
		return pool.QueryRow(ctx, `SELECT recovery.recovery_state,target.target_state,execution.execution_state
			FROM kim.upgrade_target_recoveries_current recovery
			JOIN kim.upgrade_targets_current target USING(target_id)
			JOIN kim.upgrade_target_executions_current execution USING(target_id)
			WHERE recovery.target_id=$1`, interruptedTarget.TargetID).Scan(&recoveryState, &targetState, &executionState) == nil &&
			recoveryState == "VERIFIED" && targetState == "FENCED" && executionState == "FENCED"
	}, "verified package recovery did not remain fenced before explicit rearm")
	eventuallyRemote(t, ctx, host, 20*time.Second, func(output string) bool {
		return strings.Contains(output, "3.0.0") && strings.Contains(output, `"ready":true`)
	}, fmt.Sprintf("sudo -n cat %s", shellSafe(healthPath)), "configured package service did not become healthy")
	var recoveryAttempts, recoveryApply, recoveryResults, rearmEvidence int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_recovery_attempt_evidence
		WHERE target_id=$1 AND recovery_generation=$2`, interruptedTarget.TargetID, recoveryPlan.RecoveryGeneration).Scan(&recoveryAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='RECOVERY_APPLY_AUTHORIZED')
		FROM kim.upgrade_target_recovery_event_evidence WHERE target_id=$1 AND recovery_generation=$2`,
		interruptedTarget.TargetID, recoveryPlan.RecoveryGeneration).Scan(&recoveryApply); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_recovery_result_evidence
		WHERE target_id=$1 AND recovery_generation=$2 AND outcome='VERIFIED'`, interruptedTarget.TargetID,
		recoveryPlan.RecoveryGeneration).Scan(&recoveryResults); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_recovery_rearm_evidence
		WHERE target_id=$1`, interruptedTarget.TargetID).Scan(&rearmEvidence); err != nil {
		t.Fatal(err)
	}
	if recoveryAttempts != 1 || recoveryApply != 1 || recoveryResults != 1 || rearmEvidence != 0 {
		t.Fatalf("recovery attempts=%d apply=%d results=%d premature-rearm=%d", recoveryAttempts, recoveryApply,
			recoveryResults, rearmEvidence)
	}
	var recoveryAttempt int
	var recoveryResultDigest, recoveryObservedDigest string
	if err := pool.QueryRow(ctx, `SELECT attempt_generation,result_digest,observed_digest
		FROM kim.upgrade_target_recovery_result_evidence WHERE target_id=$1 AND recovery_generation=$2`,
		interruptedTarget.TargetID, recoveryPlan.RecoveryGeneration).Scan(&recoveryAttempt, &recoveryResultDigest,
		&recoveryObservedDigest); err != nil {
		t.Fatal(err)
	}
	if err := postgres.CompleteUpgradeTargetRecovery(ctx, pool, postgres.UpgradeTargetRecoveryCompletionRequest{
		TargetID: interruptedTarget.TargetID, Owner: "response-loss-replay",
		RecoveryGeneration: recoveryPlan.RecoveryGeneration, AttemptGeneration: uint64(recoveryAttempt),
		Outcome: "VERIFIED", ResultDigest: recoveryResultDigest, ObservedDigest: recoveryObservedDigest,
	}); err != nil {
		t.Fatalf("stable Recovery Result replay did not recover the committed result: %v", err)
	}
	rearmAuthorizationDigest := digest("package-rearm-authorization:" + nonce)
	if err := postgres.RearmUpgradeTargetAfterRecovery(ctx, pool, postgres.UpgradeTargetRecoveryRearmRequest{
		TargetID: interruptedTarget.TargetID, RecoveryGeneration: recoveryPlan.RecoveryGeneration,
		AuthorizationID: "operator-rearm-approval-" + nonce, AuthorizationDigest: rearmAuthorizationDigest,
	}); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RearmUpgradeTargetAfterRecovery(ctx, pool, postgres.UpgradeTargetRecoveryRearmRequest{
		TargetID: interruptedTarget.TargetID, RecoveryGeneration: recoveryPlan.RecoveryGeneration,
		AuthorizationID: "operator-rearm-approval-" + nonce, AuthorizationDigest: rearmAuthorizationDigest,
	}); err != nil {
		t.Fatalf("stable rearm authorization replay diverged: %v", err)
	}
	var recoveryState, rearmedTargetState, rearmedExecutionState, pausedCampaignState string
	if err := pool.QueryRow(ctx, `SELECT recovery.recovery_state,target.target_state,execution.execution_state,campaign.campaign_state
		FROM kim.upgrade_target_recoveries_current recovery
		JOIN kim.upgrade_targets_current target USING(target_id)
		JOIN kim.upgrade_target_executions_current execution USING(target_id)
		JOIN kim.upgrade_target_evidence evidence USING(target_id)
		JOIN kim.upgrade_campaigns_current campaign USING(campaign_id)
		WHERE recovery.target_id=$1`, interruptedTarget.TargetID).Scan(&recoveryState, &rearmedTargetState,
		&rearmedExecutionState, &pausedCampaignState); err != nil {
		t.Fatal(err)
	}
	if recoveryState != "REARMED" || rearmedTargetState != "PENDING" || rearmedExecutionState != "PENDING" ||
		pausedCampaignState != "PAUSED" {
		t.Fatalf("recovery=%s target=%s execution=%s campaign=%s", recoveryState, rearmedTargetState,
			rearmedExecutionState, pausedCampaignState)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.upgrade_target_recovery_rearm_evidence
		WHERE target_id=$1 AND recovery_generation=$2`, interruptedTarget.TargetID,
		recoveryPlan.RecoveryGeneration).Scan(&rearmEvidence); err != nil || rearmEvidence != 1 {
		t.Fatalf("rearm evidence was not idempotent: count=%d err=%v", rearmEvidence, err)
	}
	if _, err := postgres.ClaimUpgradeTarget(ctx, pool, postgres.UpgradeTargetClaimRequest{
		CampaignID: interruptedPlan.CampaignID, TargetID: interruptedTarget.TargetID, Owner: "forbidden-paused-retry",
		Lease: 2 * time.Second, MaximumLifetime: 5 * time.Second,
	}); !errors.Is(err, postgres.ErrUpgradeTargetClaimUnavailable) {
		t.Fatalf("explicit Target rearm implicitly resumed the paused Campaign: %v", err)
	}
	v3InstallEntries, err = strconv.Atoi(strings.TrimSpace(remoteMust(t, ctx, host,
		fmt.Sprintf("sudo -n awk '$1==\"3.0.0\"{count++} END{print count+0}' %s", shellSafe(installLog)))))
	if err != nil || v3InstallEntries != 1 {
		t.Fatalf("closed CONFIGURE_EXISTING did not execute the maintainer script exactly once: entries=%d err=%v",
			v3InstallEntries, err)
	}
	t.Logf("systemd package recovery converged: package=%s digest=%s attempts=%d unknown=%d installs=%d process=%s campaign=%s/%s",
		packageName, packageDigestV2, attempts, unknownEvents, installEntries, processDigest, campaignState, waveID)
	t.Logf("interrupted package fenced: digest=%s status=%s attempts=%d quarantine=%d campaign=PAUSED",
		packageDigestV3, interruptedPackageStatus, interruptedAttempts, interruptedQuarantine)
	t.Logf("explicit package recovery verified and rearmed: plan=%s generation=%d strategy=%s recovery_attempts=%d configure_count=%d campaign=%s",
		recoveryPlan.RecoveryPlanID, recoveryPlan.RecoveryGeneration, recoveryPlan.Strategy, recoveryAttempts,
		v3InstallEntries, pausedCampaignState)
}

func buildLinuxBinary(t *testing.T, root, output, target, ldflags string) string {
	t.Helper()
	arguments := []string{"build", "-o", output}
	if ldflags != "" {
		arguments = append(arguments, "-ldflags", ldflags)
	}
	arguments = append(arguments, target)
	command := exec.Command("go", arguments...)
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Linux %s: %v: %s", target, err, raw)
	}
	return output
}

func createDebianFixtureStage(t *testing.T, packageName, serviceName, binaryPath, healthPath, installLog, version, binary,
	postinstGate, postinstRelease string) string {
	t.Helper()
	stage := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(stage, "DEBIAN"), 0o755); err != nil {
		t.Fatal(err)
	}
	stagedBinaryPath := filepath.Join(stage, strings.TrimPrefix(binaryPath, "/"))
	if err := os.MkdirAll(filepath.Dir(stagedBinaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stage, "lib/systemd/system"), 0o755); err != nil {
		t.Fatal(err)
	}
	control := fmt.Sprintf("Package: %s\nVersion: %s\nSection: admin\nPriority: optional\nArchitecture: amd64\nMaintainer: KIM Qualification\nDescription: isolated KIM upgrade qualification fixture\n", packageName, version)
	unit := fmt.Sprintf(`[Unit]
Description=KIM isolated upgrade qualification fixture
[Service]
Type=simple
ExecStart=%s -health-file %s
Restart=no
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
RuntimeDirectory=%s
RuntimeDirectoryMode=0755
ReadWritePaths=/run/%s
RestrictAddressFamilies=AF_UNIX
CapabilityBoundingSet=
LockPersonality=true
[Install]
WantedBy=multi-user.target
`, binaryPath, healthPath, packageName, packageName)
	postinst := fmt.Sprintf("#!/bin/sh\nset -eu\ninstall -d -m 0755 %s\n", filepath.Dir(installLog))
	if postinstGate != "" {
		postinst += fmt.Sprintf("printf 'postinst-started\\n' > %s\nwhile [ ! -e %s ]; do sleep 0.1; done\n",
			postinstGate, postinstRelease)
	}
	postinst += fmt.Sprintf("printf '%%s\\n' '%s' >> %s\n", version, installLog)
	if err := os.WriteFile(filepath.Join(stage, "DEBIAN/control"), []byte(control), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "DEBIAN/postinst"), []byte(postinst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFixtureFile(binary, stagedBinaryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "lib/systemd/system", serviceName), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	return stage
}

func copyFixtureFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func localFileDigest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func remoteCopy(t *testing.T, ctx context.Context, host, source, destination string) {
	t.Helper()
	command := exec.CommandContext(ctx, "scp", "-r", source, host+":"+destination)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("scp %s: %v: %s", source, err, raw)
	}
}

func remoteMust(t *testing.T, ctx context.Context, host, command string) string {
	t.Helper()
	output, err := remoteCommand(ctx, host, command)
	if err != nil {
		t.Fatalf("remote command: %v: %s", err, output)
	}
	return output
}

func remoteCommand(ctx context.Context, host, command string) (string, error) {
	process := exec.CommandContext(ctx, "ssh", host, command)
	raw, err := process.CombinedOutput()
	return string(raw), err
}

func eventuallyRemote(t *testing.T, ctx context.Context, host string, timeout time.Duration, condition func(string) bool, command, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last, _ = remoteCommand(ctx, host, command)
		if condition(last) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: %s", message, last)
}

func startReverseTunnel(t *testing.T, ctx context.Context, host string, remotePort int, localPort string) *exec.Cmd {
	t.Helper()
	command := exec.CommandContext(ctx, "ssh", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=5",
		"-N", "-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%s", remotePort, localPort), host)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := command.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("reverse tunnel failed: %s", output.String())
	}
	return command
}

func stopCommand(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Kill()
	_, _ = command.Process.Wait()
}

func startRemoteTargetExecutor(t *testing.T, ctx context.Context, host, unit, binary, databaseURL, campaignID, targetID, owner, profile, settle string) {
	t.Helper()
	command := fmt.Sprintf("sudo -n systemd-run --quiet --collect --unit=%s --property=Type=exec -- %s "+
		"-database-url %s -campaign-id %s -target-id %s -executor-id %s -backend systemd-package -backend-profile %s "+
		"-claim-lease 2s -claim-maximum-lifetime 30s -claim-renew-interval 500ms -claim-poll-interval 100ms "+
		"-observation-settle-window %s -database-max-connections 4", shellSafe(unit), shellSafe(binary),
		shellSafe(databaseURL), shellSafe(campaignID), shellSafe(targetID), shellSafe(owner), shellSafe(profile), shellSafe(settle))
	remoteMust(t, ctx, host, command)
}

func startRemoteRecoveryExecutor(t *testing.T, ctx context.Context, host, unit, binary, databaseURL, targetID, owner, profile, settle string) {
	t.Helper()
	command := fmt.Sprintf("sudo -n systemd-run --quiet --collect --unit=%s --property=Type=exec -- %s "+
		"-database-url %s -target-id %s -executor-id %s -backend-profile %s "+
		"-claim-lease 2s -claim-maximum-lifetime 30s -claim-renew-interval 500ms -claim-poll-interval 100ms "+
		"-observation-settle-window %s -database-max-connections 4", shellSafe(unit), shellSafe(binary),
		shellSafe(databaseURL), shellSafe(targetID), shellSafe(owner), shellSafe(profile), shellSafe(settle))
	remoteMust(t, ctx, host, command)
}

func shellSafe(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
