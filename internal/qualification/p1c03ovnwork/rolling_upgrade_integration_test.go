package p1c03ovnwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

const ovnRuntimeWorkSchemaV2 = "kim.network.ovn-runtime-work/v2"

func TestOVNRuntimeWorkerExplicitNMinusOneRollingUpgrade(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_ROLLING_UPGRADE") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_ROLLING_UPGRADE is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
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
		VALUES($1,1,'ACTIVE')`, fmt.Sprintf("ovn-rolling-upgrade-%d", time.Now().UnixNano())); err != nil {
		t.Fatal(err)
	}

	oldArtifact := digest("ovn-worker-release-n-minus-one")
	newArtifact := digest("ovn-worker-release-n")
	unsupportedArtifact := digest("ovn-worker-release-n-minus-two")
	publishRollingManifest(t, ctx, pool, "kim-n-minus-one", "1.0.0", oldArtifact, []string{postgres.OVNRuntimeWorkSchemaV1})
	publishRollingManifest(t, ctx, pool, "kim-n", "1.1.0", newArtifact, []string{postgres.OVNRuntimeWorkSchemaV1, ovnRuntimeWorkSchemaV2})
	publishRollingManifest(t, ctx, pool, "kim-n-minus-two", "0.9.0", unsupportedArtifact, []string{postgres.OVNRuntimeWorkSchemaV1})
	if err := postgres.PublishReleaseCompatibilityEdge(ctx, pool, postgres.ReleaseCompatibilityEdge{
		SourceReleaseID: "kim-n-minus-one", SourceManifestRevision: 1,
		TargetReleaseID: "kim-n", TargetManifestRevision: 1,
		AllowedWorkSchemas: []string{postgres.OVNRuntimeWorkSchemaV1},
		EdgeDigest:         digest("explicit-n-minus-one-edge"), CertificationState: "VALIDATED",
	}); err != nil {
		t.Fatal(err)
	}
	if err := postgres.SetReleaseAuthority(ctx, pool, "kim-n", 1, postgres.OVNRuntimeWorkSchemaV1, "ROLLING"); err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	workerBinary := buildBinary(t, root, filepath.Join(workspace, "kim-network-worker"), "./cmd/kim-network-worker")
	nbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-nbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")
	sbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-sbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")
	statePath := filepath.Join(workspace, "rolling-ovn-state.json")

	first := prepareRollingWork(t, ctx, pool, statePath, "n-minus-one")
	oldWorker := startProcess(t, workerBinary, rollingWorkerArguments(databaseURL, nbctl, sbctl,
		"ovn-worker-n-minus-one", "kim-n-minus-one", oldArtifact, postgres.OVNRuntimeWorkSchemaV1)...)
	oldWorker.cmd.Env = append(os.Environ(), "KIM_OVN_FIXTURE_STATE="+statePath, "KIM_OVN_FIXTURE_APPLY_DELAY=1200ms")
	oldWorker.start(t)
	waitForClaim(t, ctx, pool, first.workID, "ovn-worker-n-minus-one", 15*time.Second)

	newWorker := startProcess(t, workerBinary, rollingWorkerArguments(databaseURL, nbctl, sbctl,
		"ovn-worker-n", "kim-n", newArtifact, postgres.OVNRuntimeWorkSchemaV1+","+ovnRuntimeWorkSchemaV2)...)
	newWorker.cmd.Env = append(os.Environ(), "KIM_OVN_FIXTURE_STATE="+statePath)
	newWorker.start(t)
	defer newWorker.stop()
	eventually(t, 10*time.Second, func() bool {
		var state string
		return pool.QueryRow(ctx, `SELECT compatibility_state FROM kim.component_release_bindings_current WHERE component_id='ovn-worker-n'`).Scan(&state) == nil && state == "VALIDATED"
	}, "N worker did not establish a validated release binding")

	if err := oldWorker.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool {
		var state string
		return pool.QueryRow(ctx, `SELECT lifecycle_state FROM kim.component_release_bindings_current WHERE component_id='ovn-worker-n-minus-one'`).Scan(&state) == nil && state == "DRAINING"
	}, "N-1 worker did not publish DRAINING before finishing current work")
	if err := postgres.ActivateOVNWorkSchema(ctx, pool, ovnRuntimeWorkSchemaV2); !errors.Is(err, postgres.ErrReleaseFeatureGateBlocked) {
		t.Fatalf("v2 FeatureGate activated while N-1 was draining: %v", err)
	}
	if err := waitForProcess(t, oldWorker, 10*time.Second); err != nil {
		t.Fatalf("N-1 graceful drain failed: %v: %s", err, oldWorker.output.String())
	}
	assertDrainConvergence(t, ctx, pool, first, 1, 0, 0, 1)
	var oldCompatibility, oldLifecycle string
	if err := pool.QueryRow(ctx, `SELECT compatibility_state,lifecycle_state FROM kim.component_release_bindings_current
		WHERE component_id='ovn-worker-n-minus-one'`).Scan(&oldCompatibility, &oldLifecycle); err != nil || oldCompatibility != "COMPATIBLE" || oldLifecycle != "STOPPED" {
		t.Fatalf("N-1 binding compatibility=%s lifecycle=%s err=%v", oldCompatibility, oldLifecycle, err)
	}

	if err := postgres.ActivateOVNWorkSchema(ctx, pool, ovnRuntimeWorkSchemaV2); err != nil {
		t.Fatal(err)
	}
	second := prepareRollingWork(t, ctx, pool, statePath, "n-v2")
	eventually(t, 15*time.Second, func() bool {
		var state string
		return pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, second.workID).Scan(&state) == nil && state == "OBSERVED"
	}, "N worker did not claim and converge v2 work")
	assertDrainConvergence(t, ctx, pool, second, 1, 0, 0, 1)
	var requiredSchema string
	var attemptBindingGeneration, currentBindingGeneration int64
	if err := pool.QueryRow(ctx, `SELECT work.required_work_schema_version,attempt.release_binding_generation,binding.binding_generation
		FROM kim.ovn_runtime_work_current work
		JOIN kim.ovn_runtime_work_attempt_evidence attempt ON attempt.work_id=work.work_id AND attempt.claim_generation=1
		JOIN kim.component_release_bindings_current binding ON binding.component_id=attempt.claim_owner
		WHERE work.work_id=$1`, second.workID).Scan(&requiredSchema, &attemptBindingGeneration, &currentBindingGeneration); err != nil || requiredSchema != ovnRuntimeWorkSchemaV2 || attemptBindingGeneration != currentBindingGeneration {
		t.Fatalf("v2 work schema=%s attempt_binding=%d current_binding=%d err=%v", requiredSchema, attemptBindingGeneration, currentBindingGeneration, err)
	}

	unsupported, err := postgres.RegisterOVNWorkerRelease(ctx, pool, postgres.OVNWorkerReleaseRequest{
		WorkerID: "ovn-worker-n-minus-two", ReleaseID: "kim-n-minus-two", ManifestRevision: 1,
		ArtifactDigest: unsupportedArtifact, SupportedWorkSchemas: []string{postgres.OVNRuntimeWorkSchemaV1},
		EvaluatorArtifactDigest: digest("rolling-compatibility-evaluator"),
	})
	if err != nil || unsupported.CompatibilityState != "INCOMPATIBLE" || unsupported.LifecycleState != "FENCED" {
		t.Fatalf("N-2 binding=%+v err=%v", unsupported, err)
	}
	claims, err := postgres.ClaimOVNRuntimeWork(ctx, pool, postgres.OVNRuntimeClaimRequest{
		Owner: unsupported.ComponentID, Limit: 1, Lease: time.Second,
		MaximumLifetime: time.Second, ReleaseBindingGeneration: unsupported.BindingGeneration,
	})
	if !errors.Is(err, postgres.ErrIncompatibleRelease) || len(claims) != 0 {
		t.Fatalf("incompatible N-2 claimed work count=%d err=%v", len(claims), err)
	}

	newWorker.stop()
	var decisions, oldAttempts, newAttempts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.compatibility_decision_evidence`).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE claim_owner='ovn-worker-n-minus-one'`).Scan(&oldAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE claim_owner='ovn-worker-n'`).Scan(&newAttempts); err != nil {
		t.Fatal(err)
	}
	if decisions != 3 || oldAttempts != 1 || newAttempts != 1 {
		t.Fatalf("decisions=%d old_attempts=%d new_attempts=%d", decisions, oldAttempts, newAttempts)
	}
	t.Logf("rolling upgrade converged: decisions=%d N-1=%s/%s attempts=%d N=v2 attempts=%d N-2=%s/%s",
		decisions, oldCompatibility, oldLifecycle, oldAttempts, newAttempts, unsupported.CompatibilityState, unsupported.LifecycleState)
}

func publishRollingManifest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, releaseID, version, artifact string, schemas []string) {
	t.Helper()
	if err := postgres.PublishReleaseManifest(ctx, pool, postgres.ReleaseManifest{
		ReleaseID: releaseID, ManifestRevision: 1, ProductVersion: version, Channel: "DEVELOPER_PREVIEW",
		ManifestDigest: digest("manifest-" + releaseID), CertificationState: "VALIDATED",
		OVNWorkerArtifactDigest: artifact, OVNWorkerWorkSchemas: schemas,
		OVNWorkerComponentContractDigest: digest("component-contract-" + releaseID),
	}); err != nil {
		t.Fatal(err)
	}
}

func prepareRollingWork(t *testing.T, ctx context.Context, pool *pgxpool.Pool, statePath, label string) drainFixture {
	t.Helper()
	suffix := fmt.Sprintf("rolling-%s-%d", label, time.Now().UnixNano())
	ids := seedOVNAuthority(t, ctx, pool, suffix)
	decision, err := postgres.CommitOVNPortIntent(ctx, pool, postgres.OVNPortIntentRequest{IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ovnadapter.DecodePortPlan(decision.CanonicalObjectSet, decision.ObjectSetDigest)
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, statePath, map[string]any{
		"logical_switch_name": plan.LogicalSwitch.Name,
		"logical_port_name":   plan.LogicalPort.Name,
		"network_markers":     plan.NetworkExternalIDs,
		"port_markers":        withDigest(plan.PortExternalIDs, decision.ObjectSetDigest),
		"chassis_name":        plan.LogicalPort.OVNChassisName,
		"applied":             false,
		"apply_count":         0,
	})
	return drainFixture{ids: ids, workID: fmt.Sprintf("ovn-runtime:%s:1", ids.intentID), statePath: statePath}
}

func rollingWorkerArguments(databaseURL, nbctl, sbctl, workerID, releaseID, artifactDigest, schemas string) []string {
	return []string{
		"-database-url", databaseURL, "-worker-id", workerID,
		"-release-id", releaseID, "-release-manifest-revision", "1", "-work-schema-versions", schemas,
		"-compatibility-evaluator-artifact-digest", digest("rolling-compatibility-evaluator"),
		"-adapter-artifact-digest", artifactDigest,
		"-ovn-nb-db", "unix:/fixture/nb.sock", "-ovn-sb-db", "unix:/fixture/sb.sock",
		"-ovn-nbctl", nbctl, "-ovn-sbctl", sbctl, "-poll-interval", "20ms",
		"-batch-limit", "1", "-database-max-connections", "2", "-claim-lease", "500ms",
		"-claim-maximum-lifetime", "3s", "-claim-renew-interval", "100ms", "-command-timeout", "5s", "-drain-timeout", "3s",
	}
}
