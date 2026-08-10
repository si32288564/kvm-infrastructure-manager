package p1c03ovnwork

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestOVNRuntimeWorkerPostgreSQLFailoverConvergence(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_FAILOVER") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_FAILOVER is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	cluster := startPostgreSQLFailoverCluster(t, ctx)

	root, err := filepathAbs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	workerBinary := buildBinary(t, root, workspace+"/kim-network-worker", "./cmd/kim-network-worker")
	nbctl := buildBinary(t, root, workspace+"/ovn-nbctl", "./internal/qualification/p1c03ovnwork/ovnfixture")
	sbctl := buildBinary(t, root, workspace+"/ovn-sbctl", "./internal/qualification/p1c03ovnwork/ovnfixture")

	primaryPool, err := postgres.OpenWithMaxConnections(ctx, cluster.primaryURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.Migrate(ctx, primaryPool); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("failover-%d", time.Now().UnixNano())
	ids := seedOVNAuthority(t, ctx, primaryPool, suffix)
	publishCurrentTestWorkerRelease(t, ctx, primaryPool, digest("qualified-ovn-adapter"))
	decision, err := postgres.CommitOVNPortIntent(ctx, primaryPool, postgres.OVNPortIntentRequest{IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID})
	if err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	plan, err := ovnadapter.DecodePortPlan(decision.CanonicalObjectSet, decision.ObjectSetDigest)
	if err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	var restoreEpoch, commitLSN string
	var authorityGeneration int64
	if err := primaryPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&restoreEpoch, &authorityGeneration); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	if err := primaryPool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&commitLSN); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}

	statePath := workspace + "/ovn-state.json"
	writeJSON(t, statePath, map[string]any{
		"logical_switch_name": plan.LogicalSwitch.Name,
		"logical_port_name":   plan.LogicalPort.Name,
		"network_markers":     plan.NetworkExternalIDs,
		"port_markers":        withDigest(plan.PortExternalIDs, decision.ObjectSetDigest),
		"chassis_name":        plan.LogicalPort.OVNChassisName,
		"applied":             false,
		"apply_count":         0,
	})
	readBackSignal, readBackRelease := workspace+"/readback-started", workspace+"/readback-release"
	baseArgs := func(databaseURL string) []string {
		arguments := []string{
			"-database-url", databaseURL, "-adapter-artifact-digest", digest("qualified-ovn-adapter"),
			"-ovn-nb-db", "unix:/fixture/nb.sock", "-ovn-sb-db", "unix:/fixture/sb.sock",
			"-ovn-nbctl", nbctl, "-ovn-sbctl", sbctl, "-poll-interval", "20ms",
			"-batch-limit", "1", "-claim-lease", "500ms", "-claim-maximum-lifetime", "4s",
			"-claim-renew-interval", "100ms", "-command-timeout", "5s",
		}
		return append(arguments, testWorkerReleaseArguments()...)
	}
	workerA := startProcess(t, workerBinary, append(baseArgs(cluster.primaryURL), "-worker-id", "ovn-db-primary-worker")...)
	workerA.cmd.Env = append(os.Environ(),
		"KIM_OVN_FIXTURE_STATE="+statePath,
		"KIM_OVN_FIXTURE_BLOCK_READBACK=1",
		"KIM_OVN_FIXTURE_READBACK_SIGNAL="+readBackSignal,
		"KIM_OVN_FIXTURE_READBACK_RELEASE="+readBackRelease,
		"KIM_OVN_FIXTURE_APPLY_DELAY=800ms",
	)
	workerA.start(t)
	defer workerA.stop()
	waitForFileOrProcessFailure(t, workerA, readBackSignal, 30*time.Second, "worker A did not reach post-apply read-back")

	workID := fmt.Sprintf("ovn-runtime:%s:1", ids.intentID)
	var firstOwner string
	var firstGeneration int64
	if err := primaryPool.QueryRow(ctx, `SELECT claim_owner,claim_generation FROM kim.ovn_runtime_work_current WHERE work_id=$1`, workID).Scan(&firstOwner, &firstGeneration); err != nil || firstOwner != "ovn-db-primary-worker" || firstGeneration != 1 {
		primaryPool.Close()
		t.Fatalf("pre-failover claim=%s/%d err=%v", firstOwner, firstGeneration, err)
	}
	var initialExpiry, renewedExpiry, maximumExpiry time.Time
	if err := primaryPool.QueryRow(ctx, `SELECT lease_expires_at,maximum_expires_at FROM kim.ovn_runtime_work_attempt_evidence
		WHERE work_id=$1 AND claim_generation=1`, workID).Scan(&initialExpiry, &maximumExpiry); err != nil {
		primaryPool.Close()
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool {
		return primaryPool.QueryRow(ctx, `SELECT claim_expires_at FROM kim.ovn_runtime_work_current
			WHERE work_id=$1 AND last_renewal_generation>0`, workID).Scan(&renewedExpiry) == nil && renewedExpiry.After(initialExpiry) && !renewedExpiry.After(maximumExpiry)
	}, "worker A did not durably renew its long-running claim")

	cluster.failover(t, ctx)
	primaryPool.Close()
	if err := os.WriteFile(readBackRelease, []byte("promoted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForProcessFailure(t, workerA, 15*time.Second, "old-primary worker did not stop after database failover")

	newPrimaryPool := openPostgreSQLEventually(t, ctx, cluster.standbyURL)
	defer newPrimaryPool.Close()
	var promoted, replayed bool
	var promotedEpoch string
	var promotedGeneration int64
	if err := newPrimaryPool.QueryRow(ctx, `SELECT NOT pg_is_in_recovery(), pg_current_wal_lsn() >= $1::pg_lsn`, commitLSN).Scan(&promoted, &replayed); err != nil {
		t.Fatal(err)
	}
	if err := newPrimaryPool.QueryRow(ctx, `SELECT restore_epoch,authority_generation FROM kim.database_authority WHERE singleton`).Scan(&promotedEpoch, &promotedGeneration); err != nil {
		t.Fatal(err)
	}
	if !promoted || !replayed || promotedEpoch != restoreEpoch || promotedGeneration != authorityGeneration {
		t.Fatalf("promoted=%t replayed=%t authority=%s/%d want=%s/%d", promoted, replayed, promotedEpoch, promotedGeneration, restoreEpoch, authorityGeneration)
	}
	var replicatedRenewals int
	if err := newPrimaryPool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_renewal_evidence
		WHERE work_id=$1 AND claim_generation=1`, workID).Scan(&replicatedRenewals); err != nil || replicatedRenewals < 1 {
		t.Fatalf("replicated renewal evidence=%d err=%v", replicatedRenewals, err)
	}

	workerB := startProcess(t, workerBinary, append(baseArgs(cluster.standbyURL), "-worker-id", "ovn-db-promoted-worker")...)
	workerB.cmd.Env = append(os.Environ(), "KIM_OVN_FIXTURE_STATE="+statePath)
	workerB.start(t)
	defer workerB.stop()
	eventually(t, 30*time.Second, func() bool {
		var state string
		return newPrimaryPool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, workID).Scan(&state) == nil && state == "OBSERVED"
	}, "promoted-primary worker did not converge the uncertain OVN work")

	stale := postgres.OVNRuntimeClaim{WorkID: workID, Owner: "ovn-db-primary-worker", ClaimGeneration: 1}
	if err := postgres.AuthorizeOVNRuntimeApply(ctx, newPrimaryPool, stale); !errors.Is(err, postgres.ErrStaleOVNRuntimeClaim) {
		t.Fatalf("pre-failover claim accepted by promoted primary: %v", err)
	}
	var attempts, renewalEvents, unknownEvents, readBackEvents, applyEvents int
	if err := newPrimaryPool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, workID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := newPrimaryPool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE event_type='DISPATCH_UNKNOWN'),
		count(*) FILTER (WHERE event_type='READ_BACK_STARTED'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED')
		FROM kim.ovn_runtime_work_event_evidence WHERE work_id=$1`, workID).Scan(&unknownEvents, &readBackEvents, &applyEvents); err != nil {
		t.Fatal(err)
	}
	if err := newPrimaryPool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_renewal_evidence WHERE work_id=$1`, workID).Scan(&renewalEvents); err != nil {
		t.Fatal(err)
	}
	state := readFixtureState(t, statePath)
	if attempts != 2 || renewalEvents < 1 || unknownEvents != 1 || readBackEvents != 1 || applyEvents != 1 || state.ApplyCount != 1 {
		t.Fatalf("attempts=%d renewals=%d unknown=%d readback=%d apply-events=%d physical-applies=%d", attempts, renewalEvents, unknownEvents, readBackEvents, applyEvents, state.ApplyCount)
	}
	var acceptedOwner, claimMode string
	var acceptedGeneration int64
	if err := newPrimaryPool.QueryRow(ctx, `SELECT claim_owner,claim_generation,claim_mode FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1 ORDER BY claim_generation DESC LIMIT 1`, workID).Scan(&acceptedOwner, &acceptedGeneration, &claimMode); err != nil || acceptedOwner != "ovn-db-promoted-worker" || acceptedGeneration != 2 || claimMode != "READ_BACK_FIRST" {
		t.Fatalf("recovery claim=%s/%d/%s err=%v", acceptedOwner, acceptedGeneration, claimMode, err)
	}
}

type postgresFailoverCluster struct {
	primary, standby, network, primaryVolume, standbyVolume string
	primaryURL, standbyURL                                  string
}

func (cluster *postgresFailoverCluster) rejoinOriginalPrimaryAsSynchronousStandby(t *testing.T, ctx context.Context) {
	t.Helper()
	dockerMust(t, ctx, "rm", "-f", cluster.primary)
	dockerMust(t, ctx, "volume", "rm", "-f", cluster.primaryVolume)
	dockerMust(t, ctx, "volume", "create", cluster.primaryVolume)
	dockerMust(t, ctx, "exec", cluster.standby, "psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1",
		"-c", "ALTER SYSTEM SET synchronous_commit='remote_apply'", "-c", "SELECT pg_reload_conf()")
	dockerMust(t, ctx, "run", "--rm", "--network", cluster.network,
		"-v", cluster.primaryVolume+":/var/lib/postgresql/data", "postgres:17", "sh", "-ceu",
		"chown -R postgres:postgres /var/lib/postgresql/data; exec gosu postgres pg_basebackup -h "+cluster.standby+" -U postgres -D /var/lib/postgresql/data -Fp -Xs -P -R")
	dockerMust(t, ctx, "run", "-d", "--name", cluster.primary, "--network", cluster.network,
		"-p", "127.0.0.1::5432", "-v", cluster.primaryVolume+":/var/lib/postgresql/data",
		"postgres:17", "postgres", "-c", "hot_standby=on")
	waitDockerPostgreSQL(t, ctx, cluster.primary)
	dockerMust(t, ctx, "exec", cluster.standby, "psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1",
		"-c", "ALTER SYSTEM SET synchronous_standby_names='*'", "-c", "SELECT pg_reload_conf()")
	eventually(t, 30*time.Second, func() bool {
		output, err := dockerOutput(ctx, "exec", cluster.standby, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT count(*) FROM pg_stat_replication WHERE state='streaming' AND sync_state='sync'")
		return err == nil && strings.TrimSpace(output) == "1"
	}, "rejoined PostgreSQL primary did not become synchronous standby")
	cluster.primaryURL = postgresContainerURL(t, ctx, cluster.primary)
}

func (cluster *postgresFailoverCluster) failback(t *testing.T, ctx context.Context) {
	t.Helper()
	dockerMust(t, ctx, "kill", cluster.standby)
	dockerMust(t, ctx, "exec", "-u", "postgres", cluster.primary, "pg_ctl", "promote", "-D", "/var/lib/postgresql/data", "-w", "-t", "30")
	eventually(t, 30*time.Second, func() bool {
		output, err := dockerOutput(ctx, "exec", cluster.primary, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT NOT pg_is_in_recovery()")
		return err == nil && strings.TrimSpace(output) == "t"
	}, "rejoined PostgreSQL standby was not promoted")
}

func startPostgreSQLFailoverCluster(t *testing.T, ctx context.Context) *postgresFailoverCluster {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required for PostgreSQL failover qualification")
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	cluster := &postgresFailoverCluster{
		primary: "kim-pgfi-primary-" + suffix, standby: "kim-pgfi-standby-" + suffix,
		network: "kim-pgfi-net-" + suffix, primaryVolume: "kim-pgfi-primary-data-" + suffix,
		standbyVolume: "kim-pgfi-standby-data-" + suffix,
	}
	t.Cleanup(func() { cluster.cleanup() })
	dockerMust(t, ctx, "network", "create", cluster.network)
	dockerMust(t, ctx, "volume", "create", cluster.primaryVolume)
	dockerMust(t, ctx, "volume", "create", cluster.standbyVolume)
	dockerMust(t, ctx, "run", "-d", "--name", cluster.primary, "--network", cluster.network,
		"-e", "POSTGRES_PASSWORD=kimtest", "-e", "POSTGRES_DB=kimtest", "-p", "127.0.0.1::5432",
		"-v", cluster.primaryVolume+":/var/lib/postgresql/data", "postgres:17", "postgres",
		"-c", "wal_level=replica", "-c", "max_wal_senders=10", "-c", "max_replication_slots=10",
		"-c", "synchronous_commit=remote_apply")
	waitDockerPostgreSQL(t, ctx, cluster.primary)
	dockerMust(t, ctx, "exec", cluster.primary, "sh", "-ceu",
		"printf '%s\\n' 'host replication all 0.0.0.0/0 trust' >> \"$PGDATA/pg_hba.conf\"")
	dockerMust(t, ctx, "exec", cluster.primary, "psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", "SELECT pg_reload_conf()")
	dockerMust(t, ctx, "run", "--rm", "--network", cluster.network,
		"-v", cluster.standbyVolume+":/var/lib/postgresql/data", "postgres:17", "sh", "-ceu",
		"chown -R postgres:postgres /var/lib/postgresql/data; exec gosu postgres pg_basebackup -h "+cluster.primary+" -U postgres -D /var/lib/postgresql/data -Fp -Xs -P -R")
	dockerMust(t, ctx, "run", "-d", "--name", cluster.standby, "--network", cluster.network,
		"-p", "127.0.0.1::5432", "-v", cluster.standbyVolume+":/var/lib/postgresql/data",
		"postgres:17", "postgres", "-c", "hot_standby=on")
	waitDockerPostgreSQL(t, ctx, cluster.standby)
	dockerMust(t, ctx, "exec", cluster.primary, "psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1",
		"-c", "ALTER SYSTEM SET synchronous_standby_names='*'", "-c", "SELECT pg_reload_conf()")
	eventually(t, 30*time.Second, func() bool {
		output, err := dockerOutput(ctx, "exec", cluster.primary, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT count(*) FROM pg_stat_replication WHERE state='streaming' AND sync_state='sync'")
		return err == nil && strings.TrimSpace(output) == "1"
	}, "PostgreSQL standby did not become synchronous")
	cluster.primaryURL = postgresContainerURL(t, ctx, cluster.primary)
	cluster.standbyURL = postgresContainerURL(t, ctx, cluster.standby)
	return cluster
}

func (cluster *postgresFailoverCluster) failover(t *testing.T, ctx context.Context) {
	t.Helper()
	dockerMust(t, ctx, "kill", cluster.primary)
	dockerMust(t, ctx, "exec", "-u", "postgres", cluster.standby, "pg_ctl", "promote", "-D", "/var/lib/postgresql/data", "-w", "-t", "30")
	eventually(t, 30*time.Second, func() bool {
		output, err := dockerOutput(ctx, "exec", cluster.standby, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT NOT pg_is_in_recovery()")
		return err == nil && strings.TrimSpace(output) == "t"
	}, "PostgreSQL standby was not promoted")
}

func (cluster *postgresFailoverCluster) cleanup() {
	for _, name := range []string{cluster.primary, cluster.standby} {
		_, _ = dockerOutput(context.Background(), "rm", "-f", name)
	}
	for _, name := range []string{cluster.primaryVolume, cluster.standbyVolume} {
		_, _ = dockerOutput(context.Background(), "volume", "rm", "-f", name)
	}
	_, _ = dockerOutput(context.Background(), "network", "rm", cluster.network)
}

func postgresContainerURL(t *testing.T, ctx context.Context, container string) string {
	t.Helper()
	output := dockerMust(t, ctx, "port", container, "5432/tcp")
	address := strings.TrimSpace(strings.Split(output, "\n")[0])
	_, portText, found := strings.Cut(address, ":")
	if !found {
		t.Fatalf("unexpected Docker port output %q", output)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"sslmode": {"disable"}, "connect_timeout": {"2"}}
	return fmt.Sprintf("postgres://postgres:kimtest@127.0.0.1:%d/kimtest?%s", port, values.Encode())
}

func waitDockerPostgreSQL(t *testing.T, ctx context.Context, container string) {
	t.Helper()
	eventually(t, 30*time.Second, func() bool {
		process, processErr := dockerOutput(ctx, "exec", container, "cat", "/proc/1/comm")
		if processErr != nil || strings.TrimSpace(process) != "postgres" {
			return false
		}
		output, err := dockerOutput(ctx, "exec", container, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT 1")
		return err == nil && strings.TrimSpace(output) == "1"
	}, "PostgreSQL container did not become ready: "+container)
}

func dockerMust(t *testing.T, ctx context.Context, arguments ...string) string {
	t.Helper()
	output, err := dockerOutput(ctx, arguments...)
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return output
}

func dockerOutput(ctx context.Context, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "docker", arguments...)
	raw, err := command.CombinedOutput()
	return string(raw), err
}

func openPostgreSQLEventually(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	var pool *pgxpool.Pool
	eventually(t, 30*time.Second, func() bool {
		candidate, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 8)
		if err != nil {
			return false
		}
		if err := candidate.Ping(ctx); err != nil {
			candidate.Close()
			return false
		}
		pool = candidate
		return true
	}, "promoted PostgreSQL did not accept connections")
	return pool
}

func waitForProcessFailure(t *testing.T, process *process, timeout time.Duration, message string) {
	t.Helper()
	select {
	case err := <-process.done:
		process.done <- err
		if err == nil {
			t.Fatalf("%s: process exited successfully: %s", message, process.output.String())
		}
	case <-time.After(timeout):
		t.Fatalf("%s: %s", message, process.output.String())
	}
}

func filepathAbs(path string) (string, error) { return filepath.Abs(path) }
