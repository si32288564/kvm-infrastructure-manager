package p1c03ovnwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestOVNRuntimeRollingUpgradeHardDrainPostgreSQLFailover(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_OVN_UPGRADE_FAILOVER") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_OVN_UPGRADE_FAILOVER is not enabled")
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
		VALUES($1,1,'ACTIVE')`, "ovn-upgrade-failover"); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}

	oldArtifact := digest("upgrade-failover-worker-n-minus-one")
	newArtifact := digest("upgrade-failover-worker-n")
	publishRollingManifest(t, ctx, primaryPool, "kim-upgrade-n-minus-one", "1.0.0", oldArtifact, []string{postgres.OVNRuntimeWorkSchemaV1})
	publishRollingManifest(t, ctx, primaryPool, "kim-upgrade-n", "1.1.0", newArtifact, []string{postgres.OVNRuntimeWorkSchemaV1, ovnRuntimeWorkSchemaV2})
	if err := postgres.PublishReleaseCompatibilityEdge(ctx, primaryPool, postgres.ReleaseCompatibilityEdge{
		SourceReleaseID: "kim-upgrade-n-minus-one", SourceManifestRevision: 1,
		TargetReleaseID: "kim-upgrade-n", TargetManifestRevision: 1,
		AllowedWorkSchemas: []string{postgres.OVNRuntimeWorkSchemaV1},
		EdgeDigest:         digest("upgrade-failover-edge"), CertificationState: "VALIDATED",
	}); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	if err := postgres.SetReleaseAuthority(ctx, primaryPool, "kim-upgrade-n", 1, postgres.OVNRuntimeWorkSchemaV1, "ROLLING"); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}

	root, err := filepath.Abs("../../..")
	if err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	workspace := t.TempDir()
	workerBinary := buildBinary(t, root, filepath.Join(workspace, "kim-network-worker"), "./cmd/kim-network-worker")
	nbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-nbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")
	sbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-sbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")
	statePath := filepath.Join(workspace, "upgrade-failover-state.json")
	fixture := prepareRollingWork(t, ctx, primaryPool, statePath, "upgrade-failover")
	readBackSignal := filepath.Join(workspace, "readback-started")

	oldWorker := startProcess(t, workerBinary, rollingWorkerArguments(cluster.primaryURL, nbctl, sbctl,
		"ovn-upgrade-worker-n-minus-one", "kim-upgrade-n-minus-one", oldArtifact, postgres.OVNRuntimeWorkSchemaV1)...)
	oldWorker.cmd.Env = append(os.Environ(),
		"KIM_OVN_FIXTURE_STATE="+statePath,
		"KIM_OVN_FIXTURE_BLOCK_READBACK=1",
		"KIM_OVN_FIXTURE_READBACK_SIGNAL="+readBackSignal,
	)
	oldWorker.start(t)
	waitForFileOrProcessFailure(t, oldWorker, readBackSignal, 30*time.Second, "N-1 worker did not reach post-apply read-back")
	waitForClaim(t, ctx, primaryPool, fixture.workID, "ovn-upgrade-worker-n-minus-one", 10*time.Second)
	if err := oldWorker.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool {
		var state string
		return primaryPool.QueryRow(ctx, `SELECT lifecycle_state FROM kim.component_release_bindings_current
			WHERE component_id='ovn-upgrade-worker-n-minus-one'`).Scan(&state) == nil && state == "DRAINING"
	}, "N-1 worker did not publish DRAINING")
	if err := postgres.ActivateOVNWorkSchema(ctx, primaryPool, ovnRuntimeWorkSchemaV2); !errors.Is(err, postgres.ErrReleaseFeatureGateBlocked) {
		t.Fatalf("v2 schema activated while N-1 was draining: %v", err)
	}
	var restoreEpoch, replicatedLSN string
	var databaseGeneration, oldBindingGeneration int64
	if err := primaryPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&restoreEpoch, &databaseGeneration); err != nil {
		t.Fatal(err)
	}
	if err := primaryPool.QueryRow(ctx, `SELECT binding_generation FROM kim.component_release_bindings_current
		WHERE component_id='ovn-upgrade-worker-n-minus-one'`).Scan(&oldBindingGeneration); err != nil {
		t.Fatal(err)
	}
	if err := primaryPool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&replicatedLSN); err != nil {
		t.Fatal(err)
	}
	if err := oldWorker.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := waitForProcess(t, oldWorker, 8*time.Second); err == nil {
		t.Fatalf("hard-drained N-1 worker exited successfully: %s", oldWorker.output.String())
	}
	cluster.failover(t, ctx)
	primaryPool.Close()

	promotedPool := openPostgreSQLEventually(t, ctx, cluster.standbyURL)
	defer promotedPool.Close()
	var promoted, replayed bool
	var promotedEpoch, oldLifecycle string
	var promotedGeneration int64
	if err := promotedPool.QueryRow(ctx, `SELECT NOT pg_is_in_recovery(),pg_current_wal_lsn() >= $1::pg_lsn`, replicatedLSN).Scan(&promoted, &replayed); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&promotedEpoch, &promotedGeneration); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT lifecycle_state FROM kim.component_release_bindings_current
		WHERE component_id='ovn-upgrade-worker-n-minus-one'`).Scan(&oldLifecycle); err != nil {
		t.Fatal(err)
	}
	if !promoted || !replayed || promotedEpoch != restoreEpoch || promotedGeneration != databaseGeneration || oldLifecycle != "DRAINING" {
		t.Fatalf("promoted=%t replayed=%t authority=%s/%d want=%s/%d old_lifecycle=%s",
			promoted, replayed, promotedEpoch, promotedGeneration, restoreEpoch, databaseGeneration, oldLifecycle)
	}

	newWorker := startProcess(t, workerBinary, rollingWorkerArguments(cluster.standbyURL, nbctl, sbctl,
		"ovn-upgrade-worker-n", "kim-upgrade-n", newArtifact, postgres.OVNRuntimeWorkSchemaV1+","+ovnRuntimeWorkSchemaV2)...)
	newWorker.cmd.Env = append(os.Environ(), "KIM_OVN_FIXTURE_STATE="+statePath)
	newWorker.start(t)
	defer newWorker.stop()
	eventually(t, 30*time.Second, func() bool {
		var state string
		return promotedPool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, fixture.workID).Scan(&state) == nil && state == "OBSERVED"
	}, "N worker did not recover N-1 hard-drained work after PostgreSQL promotion")
	assertDrainConvergence(t, ctx, promotedPool, fixture, 2, 1, 1, 1)

	if err := postgres.RevokeReleaseTrust(ctx, promotedPool, postgres.ReleaseDistrustRequest{
		DistrustID: "upgrade-failover-edge-revocation", Scope: "COMPATIBILITY_EDGE",
		SourceReleaseID: "kim-upgrade-n-minus-one", SourceManifestRevision: 1,
		TargetReleaseID: "kim-upgrade-n", TargetManifestRevision: 1, Reason: "upgrade_edge_revoked_after_failover",
		EvaluatorArtifactDigest: digest("rolling-compatibility-evaluator"), EvidenceDigest: digest("upgrade-failover-edge-revocation-evidence"),
	}); err != nil {
		t.Fatal(err)
	}
	claims, err := postgres.ClaimOVNRuntimeWork(ctx, promotedPool, postgres.OVNRuntimeClaimRequest{
		Owner: "ovn-upgrade-worker-n-minus-one", Limit: 1, Lease: time.Second,
		MaximumLifetime: time.Second, ReleaseBindingGeneration: uint64(oldBindingGeneration),
	})
	if !errors.Is(err, postgres.ErrIncompatibleRelease) || len(claims) != 0 {
		t.Fatalf("stale N-1 binding claimed after edge revocation count=%d err=%v", len(claims), err)
	}
	var compatibility, lifecycle string
	var currentBindingGeneration int64
	if err := promotedPool.QueryRow(ctx, `SELECT compatibility_state,lifecycle_state,binding_generation
		FROM kim.component_release_bindings_current WHERE component_id='ovn-upgrade-worker-n-minus-one'`).Scan(
		&compatibility, &lifecycle, &currentBindingGeneration); err != nil {
		t.Fatal(err)
	}
	if compatibility != "INCOMPATIBLE" || lifecycle != "FENCED" || currentBindingGeneration <= oldBindingGeneration {
		t.Fatalf("revoked N-1 binding=%s/%s generation=%d prior=%d", compatibility, lifecycle, currentBindingGeneration, oldBindingGeneration)
	}

	if err := postgres.ActivateOVNWorkSchema(ctx, promotedPool, ovnRuntimeWorkSchemaV2); err != nil {
		t.Fatal(err)
	}
	if err := postgres.RollbackOVNWorkSchema(ctx, promotedPool, postgres.OVNRuntimeWorkSchemaV1); err != nil {
		t.Fatal(err)
	}
	var transitions, unknown, readBack, attempts int
	if err := promotedPool.QueryRow(ctx, `SELECT count(*) FROM kim.release_work_schema_transition_evidence`).Scan(&transitions); err != nil || transitions != 2 {
		t.Fatalf("schema transition evidence=%d err=%v", transitions, err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, fixture.workID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := promotedPool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='DISPATCH_UNKNOWN'),
		count(*) FILTER (WHERE event_type='READ_BACK_STARTED') FROM kim.ovn_runtime_work_event_evidence WHERE work_id=$1`,
		fixture.workID).Scan(&unknown, &readBack); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || unknown != 1 || readBack != 1 || readFixtureState(t, statePath).ApplyCount != 1 {
		t.Fatalf("attempts=%d unknown=%d readback=%d physical_apply=%d", attempts, unknown, readBack, readFixtureState(t, statePath).ApplyCount)
	}
	t.Logf("upgrade failover converged: authority=%s/%d attempts=%d transitions=%d N-1=%s/%s physical_apply=1",
		promotedEpoch, promotedGeneration, attempts, transitions, compatibility, lifecycle)
}
