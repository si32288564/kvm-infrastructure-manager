package p1c03ovnwork

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestOVNRuntimeWorkerProcessDrainBoundaries(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_HARD_DRAIN") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_HARD_DRAIN is not enabled")
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

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	workerBinary := buildBinary(t, root, filepath.Join(workspace, "kim-network-worker"), "./cmd/kim-network-worker")
	nbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-nbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")
	sbctl := buildBinary(t, root, filepath.Join(workspace, "ovn-sbctl"), "./internal/qualification/p1c03ovnwork/ovnfixture")

	t.Run("graceful drain completes current authority", func(t *testing.T) {
		fixture := prepareDrainFixture(t, ctx, pool, workspace, "graceful")
		metricsAddress := availableListenAddress(t)
		worker := startProcess(t, workerBinary, workerArguments(databaseURL, nbctl, sbctl, "ovn-drain-graceful", metricsAddress,
			"400ms", "2s", "100ms", "2s")...)
		worker.cmd.Env = append(os.Environ(),
			"KIM_OVN_FIXTURE_STATE="+fixture.statePath,
			"KIM_OVN_FIXTURE_APPLY_DELAY=700ms",
		)
		worker.start(t)
		waitForClaim(t, ctx, pool, fixture.workID, "ovn-drain-graceful", 10*time.Second)
		if err := worker.cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		metrics := waitForMetrics(t, metricsAddress, `kim_ovn_worker_state{state="DRAINING"} 1`, 5*time.Second)
		if strings.Contains(metrics, fixture.ids.portID) || strings.Contains(metrics, fixture.workID) || !strings.Contains(metrics, `kim_ovn_worker_in_flight 1`) {
			t.Fatalf("metrics leaked identity or lost current work:\n%s", metrics)
		}
		if err := waitForProcess(t, worker, 10*time.Second); err != nil {
			t.Fatalf("graceful drain failed: %v: %s", err, worker.output.String())
		}
		assertDrainConvergence(t, ctx, pool, fixture, 1, 0, 0, 1)
	})

	for _, fault := range []struct {
		name         string
		secondSignal bool
		lease        string
		maximum      string
		drainTimeout string
	}{
		{name: "second signal", secondSignal: true, lease: "600ms", maximum: "3s", drainTimeout: "3s"},
		{name: "drain deadline", lease: "300ms", maximum: "700ms", drainTimeout: "700ms"},
	} {
		fault := fault
		t.Run(fault.name+" preserves unknown outcome", func(t *testing.T) {
			fixture := prepareDrainFixture(t, ctx, pool, workspace, strings.ReplaceAll(fault.name, " ", "-"))
			metricsAddress := availableListenAddress(t)
			readBackSignal := filepath.Join(workspace, strings.ReplaceAll(fault.name, " ", "-")+"-readback")
			worker := startProcess(t, workerBinary, workerArguments(databaseURL, nbctl, sbctl, "ovn-drain-fault-"+strings.ReplaceAll(fault.name, " ", "-"), metricsAddress,
				fault.lease, fault.maximum, "0", fault.drainTimeout)...)
			worker.cmd.Env = append(os.Environ(),
				"KIM_OVN_FIXTURE_STATE="+fixture.statePath,
				"KIM_OVN_FIXTURE_BLOCK_READBACK=1",
				"KIM_OVN_FIXTURE_READBACK_SIGNAL="+readBackSignal,
			)
			worker.start(t)
			waitForFileOrProcessFailure(t, worker, readBackSignal, 15*time.Second, "worker did not reach post-apply read-back")
			if err := worker.cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			metrics := waitForMetrics(t, metricsAddress, `kim_ovn_worker_state{state="DRAINING"} 1`, 5*time.Second)
			if !strings.Contains(metrics, `kim_ovn_worker_work_backlog{state="CLAIMED"} 1`) || !strings.Contains(metrics, `kim_ovn_worker_in_flight 1`) {
				t.Fatalf("hard-drain metrics did not retain current authority:\n%s", metrics)
			}
			if fault.secondSignal {
				if err := worker.cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}
			if err := waitForProcess(t, worker, 8*time.Second); err == nil {
				t.Fatalf("hard drain returned success: %s", worker.output.String())
			}
			if !strings.Contains(worker.output.String(), "outcome remains unknown until read-back") {
				t.Fatalf("hard drain did not report ambiguous outcome: %s", worker.output.String())
			}

			waitForClaimExpiry(t, ctx, pool, fixture.workID, 5*time.Second)
			recovery := startProcess(t, workerBinary, workerArguments(databaseURL, nbctl, sbctl, "ovn-drain-recovery-"+strings.ReplaceAll(fault.name, " ", "-"), "",
				"500ms", "500ms", "0", "500ms")...)
			recovery.cmd.Env = append(os.Environ(), "KIM_OVN_FIXTURE_STATE="+fixture.statePath)
			recovery.start(t)
			eventually(t, 15*time.Second, func() bool {
				var state string
				return pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, fixture.workID).Scan(&state) == nil && state == "OBSERVED"
			}, "successor did not converge hard-drained work from read-back")
			recovery.stop()
			assertDrainConvergence(t, ctx, pool, fixture, 2, 1, 1, 1)
		})
	}
}

type drainFixture struct {
	ids       authorityIDs
	workID    string
	statePath string
}

func prepareDrainFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspace, label string) drainFixture {
	t.Helper()
	suffix := fmt.Sprintf("hard-drain-%s-%d", label, time.Now().UnixNano())
	ids := seedOVNAuthority(t, ctx, pool, suffix)
	decision, err := postgres.CommitOVNPortIntent(ctx, pool, postgres.OVNPortIntentRequest{IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ovnadapter.DecodePortPlan(decision.CanonicalObjectSet, decision.ObjectSetDigest)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(workspace, label+"-state.json")
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

func workerArguments(databaseURL, nbctl, sbctl, owner, metricsAddress, claimLease, maximumLifetime, renewInterval, drainTimeout string) []string {
	arguments := []string{
		"-database-url", databaseURL, "-worker-id", owner,
		"-adapter-artifact-digest", digest("qualified-ovn-hard-drain-adapter"),
		"-ovn-nb-db", "unix:/fixture/nb.sock", "-ovn-sb-db", "unix:/fixture/sb.sock",
		"-ovn-nbctl", nbctl, "-ovn-sbctl", sbctl,
		"-poll-interval", "10ms", "-batch-limit", "1", "-database-max-connections", "2",
		"-claim-lease", claimLease, "-claim-maximum-lifetime", maximumLifetime,
		"-claim-renew-interval", renewInterval, "-command-timeout", "5s", "-drain-timeout", drainTimeout,
	}
	if metricsAddress != "" {
		arguments = append(arguments, "-metrics-listen-address", metricsAddress)
	}
	return arguments
}

func availableListenAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func waitForMetrics(t *testing.T, address, expected string, timeout time.Duration) string {
	t.Helper()
	var body string
	eventually(t, timeout, func() bool {
		response, err := http.Get("http://" + address + "/metrics") // #nosec G107 -- loopback qualification endpoint.
		if err != nil {
			return false
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			return false
		}
		body = string(raw)
		return response.StatusCode == http.StatusOK && strings.Contains(body, expected)
	}, "metrics did not expose "+expected)
	return body
}

func waitForClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workID, owner string, timeout time.Duration) {
	t.Helper()
	eventually(t, timeout, func() bool {
		var state, currentOwner string
		return pool.QueryRow(ctx, `SELECT work_state,claim_owner FROM kim.ovn_runtime_work_current WHERE work_id=$1`, workID).Scan(&state, &currentOwner) == nil && state == "CLAIMED" && currentOwner == owner
	}, "worker did not claim current work")
}

func waitForClaimExpiry(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workID string, timeout time.Duration) {
	t.Helper()
	eventually(t, timeout, func() bool {
		var expired bool
		return pool.QueryRow(ctx, `SELECT claim_expires_at <= clock_timestamp() FROM kim.ovn_runtime_work_current WHERE work_id=$1`, workID).Scan(&expired) == nil && expired
	}, "hard-drained claim did not expire")
}

func waitForProcess(t *testing.T, process *process, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-process.done:
		return err
	case <-time.After(timeout):
		_ = process.cmd.Process.Kill()
		<-process.done
		t.Fatalf("process did not stop: %s", process.output.String())
		return nil
	}
}

func assertDrainConvergence(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture drainFixture, attempts, unknown, readBack, physicalApply int) {
	t.Helper()
	var observedState string
	var actualAttempts, actualUnknown, actualReadBack, applyEvents int
	if err := pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, fixture.workID).Scan(&observedState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, fixture.workID).Scan(&actualAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE event_type='DISPATCH_UNKNOWN'),
		count(*) FILTER (WHERE event_type='READ_BACK_STARTED'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED')
		FROM kim.ovn_runtime_work_event_evidence WHERE work_id=$1`, fixture.workID).Scan(&actualUnknown, &actualReadBack, &applyEvents); err != nil {
		t.Fatal(err)
	}
	state := readFixtureState(t, fixture.statePath)
	if observedState != "OBSERVED" || actualAttempts != attempts || actualUnknown != unknown || actualReadBack != readBack || applyEvents != 1 || state.ApplyCount != physicalApply {
		t.Fatalf("state=%s attempts=%d unknown=%d readback=%d apply_events=%d physical_apply=%d", observedState, actualAttempts, actualUnknown, actualReadBack, applyEvents, state.ApplyCount)
	}
}
