package p1c03ovnwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type repeatedFailoverCycle struct {
	name                string
	activeURL           string
	recoveryURL         string
	activeWorker        string
	recoveryWorker      string
	failover            func(*testing.T, context.Context)
	workerBinary        string
	nbctl               string
	sbctl               string
	workspace           string
	restoreEpoch        string
	authorityGeneration int64
}

type repeatedFailoverCycleResult struct {
	commitLSN string
	renewals  int
	attempts  int
}

func TestOVNRuntimeWorkerRepeatedPostgreSQLFailoverDuringRenewal(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_REPEATED_FAILOVER") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_REPEATED_FAILOVER is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Minute)
	defer cancel()
	cluster := startPostgreSQLFailoverCluster(t, ctx)

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	workerBinary := buildBinary(t, root, workspace+"/kim-network-worker", "./cmd/kim-network-worker")
	nbctl := buildBinary(t, root, workspace+"/ovn-nbctl", "./internal/qualification/p1c03ovnwork/ovnfixture")
	sbctl := buildBinary(t, root, workspace+"/ovn-sbctl", "./internal/qualification/p1c03ovnwork/ovnfixture")

	initialPool, err := postgres.OpenWithMaxConnections(ctx, cluster.primaryURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.Migrate(ctx, initialPool); err != nil {
		initialPool.Close()
		t.Fatal(err)
	}
	if _, err := initialPool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES($1,1,'ACTIVE')`, fmt.Sprintf("ovn-repeated-failover-%d", time.Now().UnixNano())); err != nil {
		initialPool.Close()
		t.Fatal(err)
	}
	publishCurrentTestWorkerRelease(t, ctx, initialPool, digest("qualified-ovn-adapter"))
	var restoreEpoch string
	var authorityGeneration int64
	if err := initialPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&restoreEpoch, &authorityGeneration); err != nil {
		initialPool.Close()
		t.Fatal(err)
	}
	initialPool.Close()

	first := runRepeatedFailoverCycle(t, ctx, repeatedFailoverCycle{
		name: "primary-to-standby", activeURL: cluster.primaryURL, recoveryURL: cluster.standbyURL,
		activeWorker: "ovn-repeat-primary-a", recoveryWorker: "ovn-repeat-primary-b", failover: cluster.failover,
		workerBinary: workerBinary, nbctl: nbctl, sbctl: sbctl, workspace: workspace,
		restoreEpoch: restoreEpoch, authorityGeneration: authorityGeneration,
	})

	cluster.rejoinOriginalPrimaryAsSynchronousStandby(t, ctx)
	second := runRepeatedFailoverCycle(t, ctx, repeatedFailoverCycle{
		name: "standby-to-rejoined-primary", activeURL: cluster.standbyURL, recoveryURL: cluster.primaryURL,
		activeWorker: "ovn-repeat-standby-a", recoveryWorker: "ovn-repeat-standby-b", failover: cluster.failback,
		workerBinary: workerBinary, nbctl: nbctl, sbctl: sbctl, workspace: workspace,
		restoreEpoch: restoreEpoch, authorityGeneration: authorityGeneration,
	})

	if first.commitLSN == second.commitLSN {
		t.Fatalf("failover cycles unexpectedly recorded the same commit LSN %s", first.commitLSN)
	}
	if first.renewals < 1 || second.renewals < 1 || first.attempts != 2 || second.attempts != 2 {
		t.Fatalf("cycle evidence first=%+v second=%+v", first, second)
	}
	t.Logf("repeated failover converged: first_lsn=%s second_lsn=%s renewals=%d/%d attempts=%d/%d authority=%s/%d",
		first.commitLSN, second.commitLSN, first.renewals, second.renewals, first.attempts, second.attempts, restoreEpoch, authorityGeneration)
}

func runRepeatedFailoverCycle(t *testing.T, ctx context.Context, cycle repeatedFailoverCycle) repeatedFailoverCycleResult {
	t.Helper()
	activePool, err := postgres.OpenWithMaxConnections(ctx, cycle.activeURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("repeat-%s-%d", cycle.name, time.Now().UnixNano())
	ids := seedOVNAuthority(t, ctx, activePool, suffix)
	decision, err := postgres.CommitOVNPortIntent(ctx, activePool, postgres.OVNPortIntentRequest{IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID})
	if err != nil {
		activePool.Close()
		t.Fatal(err)
	}
	plan, err := ovnadapter.DecodePortPlan(decision.CanonicalObjectSet, decision.ObjectSetDigest)
	if err != nil {
		activePool.Close()
		t.Fatal(err)
	}
	var commitLSN string
	if err := activePool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&commitLSN); err != nil {
		activePool.Close()
		t.Fatal(err)
	}

	statePath := filepath.Join(cycle.workspace, cycle.name+"-state.json")
	writeJSON(t, statePath, map[string]any{
		"logical_switch_name": plan.LogicalSwitch.Name,
		"logical_port_name":   plan.LogicalPort.Name,
		"network_markers":     plan.NetworkExternalIDs,
		"port_markers":        withDigest(plan.PortExternalIDs, decision.ObjectSetDigest),
		"chassis_name":        plan.LogicalPort.OVNChassisName,
		"applied":             false,
		"apply_count":         0,
	})
	readBackSignal := filepath.Join(cycle.workspace, cycle.name+"-readback-started")
	readBackRelease := filepath.Join(cycle.workspace, cycle.name+"-readback-release")
	baseArgs := func(databaseURL, workerID string) []string {
		arguments := []string{
			"-database-url", databaseURL, "-adapter-artifact-digest", digest("qualified-ovn-adapter"),
			"-ovn-nb-db", "unix:/fixture/nb.sock", "-ovn-sb-db", "unix:/fixture/sb.sock",
			"-ovn-nbctl", cycle.nbctl, "-ovn-sbctl", cycle.sbctl, "-poll-interval", "20ms",
			"-batch-limit", "1", "-claim-lease", "500ms", "-claim-maximum-lifetime", "4s",
			"-claim-renew-interval", "100ms", "-command-timeout", "5s", "-worker-id", workerID,
		}
		return append(arguments, testWorkerReleaseArguments()...)
	}
	workerA := startProcess(t, cycle.workerBinary, baseArgs(cycle.activeURL, cycle.activeWorker)...)
	workerA.cmd.Env = append(os.Environ(),
		"KIM_OVN_FIXTURE_STATE="+statePath,
		"KIM_OVN_FIXTURE_BLOCK_READBACK=1",
		"KIM_OVN_FIXTURE_READBACK_SIGNAL="+readBackSignal,
		"KIM_OVN_FIXTURE_READBACK_RELEASE="+readBackRelease,
		"KIM_OVN_FIXTURE_APPLY_DELAY=800ms",
	)
	workerA.start(t)
	defer workerA.stop()
	waitForFileOrProcessFailure(t, workerA, readBackSignal, 30*time.Second, cycle.name+": active worker did not reach post-apply read-back")

	workID := fmt.Sprintf("ovn-runtime:%s:1", ids.intentID)
	var initialExpiry, renewedExpiry, maximumExpiry time.Time
	if err := activePool.QueryRow(ctx, `SELECT lease_expires_at,maximum_expires_at FROM kim.ovn_runtime_work_attempt_evidence
		WHERE work_id=$1 AND claim_generation=1`, workID).Scan(&initialExpiry, &maximumExpiry); err != nil {
		activePool.Close()
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool {
		return activePool.QueryRow(ctx, `SELECT claim_expires_at FROM kim.ovn_runtime_work_current
			WHERE work_id=$1 AND last_renewal_generation>0`, workID).Scan(&renewedExpiry) == nil && renewedExpiry.After(initialExpiry) && !renewedExpiry.After(maximumExpiry)
	}, cycle.name+": active worker did not durably renew its claim")

	cycle.failover(t, ctx)
	activePool.Close()
	if err := os.WriteFile(readBackRelease, []byte("promoted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForProcessFailure(t, workerA, 15*time.Second, cycle.name+": old-primary worker did not stop")

	recoveryPool := openPostgreSQLEventually(t, ctx, cycle.recoveryURL)
	defer recoveryPool.Close()
	var promoted, replayed bool
	var promotedEpoch string
	var promotedGeneration int64
	if err := recoveryPool.QueryRow(ctx, `SELECT NOT pg_is_in_recovery(), pg_current_wal_lsn() >= $1::pg_lsn`, commitLSN).Scan(&promoted, &replayed); err != nil {
		t.Fatal(err)
	}
	if err := recoveryPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&promotedEpoch, &promotedGeneration); err != nil {
		t.Fatal(err)
	}
	if !promoted || !replayed || promotedEpoch != cycle.restoreEpoch || promotedGeneration != cycle.authorityGeneration {
		t.Fatalf("%s: promoted=%t replayed=%t authority=%s/%d want=%s/%d", cycle.name, promoted, replayed, promotedEpoch, promotedGeneration, cycle.restoreEpoch, cycle.authorityGeneration)
	}

	workerB := startProcess(t, cycle.workerBinary, baseArgs(cycle.recoveryURL, cycle.recoveryWorker)...)
	workerB.cmd.Env = append(os.Environ(), "KIM_OVN_FIXTURE_STATE="+statePath)
	workerB.start(t)
	defer workerB.stop()
	eventually(t, 30*time.Second, func() bool {
		var state string
		return recoveryPool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, workID).Scan(&state) == nil && state == "OBSERVED"
	}, cycle.name+": recovery worker did not converge uncertain work")
	workerB.stop()

	stale := postgres.OVNRuntimeClaim{WorkID: workID, Owner: cycle.activeWorker, ClaimGeneration: 1}
	if err := postgres.AuthorizeOVNRuntimeApply(ctx, recoveryPool, stale); !errors.Is(err, postgres.ErrStaleOVNRuntimeClaim) {
		t.Fatalf("%s: stale claim accepted: %v", cycle.name, err)
	}
	var attempts, renewals, unknownEvents, readBackEvents, applyEvents int
	if err := recoveryPool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, workID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := recoveryPool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_renewal_evidence WHERE work_id=$1`, workID).Scan(&renewals); err != nil {
		t.Fatal(err)
	}
	if err := recoveryPool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE event_type='DISPATCH_UNKNOWN'),
		count(*) FILTER (WHERE event_type='READ_BACK_STARTED'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED')
		FROM kim.ovn_runtime_work_event_evidence WHERE work_id=$1`, workID).Scan(&unknownEvents, &readBackEvents, &applyEvents); err != nil {
		t.Fatal(err)
	}
	var acceptedOwner, claimMode string
	var acceptedGeneration int64
	if err := recoveryPool.QueryRow(ctx, `SELECT claim_owner,claim_generation,claim_mode FROM kim.ovn_runtime_work_attempt_evidence
		WHERE work_id=$1 ORDER BY claim_generation DESC LIMIT 1`, workID).Scan(&acceptedOwner, &acceptedGeneration, &claimMode); err != nil {
		t.Fatal(err)
	}
	state := readFixtureState(t, statePath)
	if attempts != 2 || renewals < 1 || unknownEvents != 1 || readBackEvents != 1 || applyEvents != 1 || state.ApplyCount != 1 || acceptedOwner != cycle.recoveryWorker || acceptedGeneration != 2 || claimMode != "READ_BACK_FIRST" {
		t.Fatalf("%s: attempts=%d renewals=%d unknown=%d readback=%d apply-events=%d physical-applies=%d recovery=%s/%d/%s",
			cycle.name, attempts, renewals, unknownEvents, readBackEvents, applyEvents, state.ApplyCount, acceptedOwner, acceptedGeneration, claimMode)
	}
	return repeatedFailoverCycleResult{commitLSN: commitLSN, renewals: renewals, attempts: attempts}
}
