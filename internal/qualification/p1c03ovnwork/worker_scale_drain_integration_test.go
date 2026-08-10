package p1c03ovnwork

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type drainWorkerInstance struct {
	owner   string
	drain   chan struct{}
	done    chan error
	metrics *ovnruntime.Metrics
}

func TestOVNRuntimeWorkerScaleUpDrainDown(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_DRAIN") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_OVN_WORKER_DRAIN is not enabled")
	}
	const (
		workItems      = 64
		initialWorkers = 2
		scaleWorkers   = 4
		batchLimit     = 2
		claimLease     = 500 * time.Millisecond
		maximumLease   = 4 * time.Second
		renewInterval  = 100 * time.Millisecond
		adapterDelay   = 700 * time.Millisecond
	)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	container := startRenewalResponseLossPostgreSQL(t, ctx)
	databaseURL := postgresContainerURL(t, ctx, container)
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	adapter := &latencyAdapter{byDigest: make(map[string]*latencyAdapterState, workItems), delay: adapterDelay}
	for index := 0; index < workItems; index++ {
		suffix := fmt.Sprintf("worker-drain-%d-%d", time.Now().UnixNano(), index)
		ids := seedOVNAuthority(t, ctx, pool, suffix)
		decision, err := postgres.CommitOVNPortIntent(ctx, pool, postgres.OVNPortIntentRequest{
			IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID,
		})
		if err != nil {
			t.Fatal(err)
		}
		adapter.register(decision.ObjectSetDigest, latencyNormal)
	}

	startWorker := func(index int) *drainWorkerInstance {
		instance := &drainWorkerInstance{
			owner: fmt.Sprintf("ovn-drain-worker-%02d", index), drain: make(chan struct{}),
			done: make(chan error, 1), metrics: ovnruntime.NewMetrics(),
		}
		worker := ovnruntime.Worker{
			Store: ovnruntime.PostgresWorkStore{DB: pool}, Adapter: adapter,
			Owner: instance.owner, BatchLimit: batchLimit, ClaimLease: claimLease,
			ClaimMaximumLifetime: maximumLease, ClaimRenewInterval: renewInterval,
			AdapterArtifactDigest: digest("qualified-ovn-drain-adapter"), Metrics: instance.metrics,
		}
		go func() { instance.done <- worker.RunWithDrain(ctx, instance.drain, 5*time.Millisecond) }()
		return instance
	}

	workers := make([]*drainWorkerInstance, 0, initialWorkers+scaleWorkers)
	for index := 0; index < initialWorkers; index++ {
		workers = append(workers, startWorker(index))
	}
	eventually(t, 10*time.Second, func() bool {
		return workers[0].metrics.Snapshot().InFlight > 0 && workers[1].metrics.Snapshot().InFlight > 0
	}, "initial workers did not acquire work")
	for index := initialWorkers; index < initialWorkers+scaleWorkers; index++ {
		workers = append(workers, startWorker(index))
	}
	eventually(t, 10*time.Second, func() bool {
		for _, worker := range workers[initialWorkers:] {
			if worker.metrics.Snapshot().ClaimsTotal == 0 {
				return false
			}
		}
		return true
	}, "scale-up workers did not participate")

	drained := workers[0]
	close(drained.drain)
	eventually(t, 2*time.Second, func() bool { return drained.metrics.Snapshot().State == "DRAINING" }, "worker did not enter DRAINING")
	var attemptsAtDrain int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE claim_owner=$1`, drained.owner).Scan(&attemptsAtDrain); err != nil {
		t.Fatal(err)
	}
	if err := <-drained.done; err != nil {
		t.Fatal(err)
	}
	var attemptsAfterStop int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE claim_owner=$1`, drained.owner).Scan(&attemptsAfterStop); err != nil {
		t.Fatal(err)
	}
	if attemptsAtDrain == 0 || attemptsAfterStop != attemptsAtDrain {
		t.Fatalf("drained worker claims changed from %d to %d", attemptsAtDrain, attemptsAfterStop)
	}

	eventually(t, 60*time.Second, func() bool {
		var observed int
		return pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_current WHERE work_state='OBSERVED'`).Scan(&observed) == nil && observed == workItems
	}, "scaled workers did not converge all work")
	for _, worker := range workers[1:] {
		close(worker.drain)
		if err := <-worker.done; err != nil {
			t.Fatal(err)
		}
	}

	var attempts, unknownEvents, owners int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT claim_owner) FROM kim.ovn_runtime_work_attempt_evidence`).Scan(&attempts, &owners); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_event_evidence WHERE event_type='DISPATCH_UNKNOWN'`).Scan(&unknownEvents); err != nil {
		t.Fatal(err)
	}
	adapter.assertSinglePhysicalApply(t)
	totalClaims, totalCompleted, totalRenewals := int64(0), int64(0), int64(0)
	for _, worker := range workers {
		snapshot := worker.metrics.Snapshot()
		if snapshot.State != "STOPPED" || snapshot.InFlight != 0 || snapshot.FatalErrors != 0 {
			t.Fatalf("worker %s snapshot=%+v", worker.owner, snapshot)
		}
		totalClaims += snapshot.ClaimsTotal
		totalCompleted += snapshot.CompletedTotal
		totalRenewals += snapshot.Renewals
	}
	if attempts != workItems || unknownEvents != 0 || owners != initialWorkers+scaleWorkers || totalClaims != workItems || totalCompleted != workItems || totalRenewals < workItems || drained.metrics.Snapshot().DrainDuration <= 0 {
		t.Fatalf("attempts=%d unknown=%d owners=%d claims=%d completed=%d renewals=%d drained=%+v",
			attempts, unknownEvents, owners, totalClaims, totalCompleted, totalRenewals, drained.metrics.Snapshot())
	}
	t.Logf("items=%d initial_workers=%d scaled_workers=%d owners=%d attempts=%d renewals=%d drained_owner_claims=%d drain_duration=%s",
		workItems, initialWorkers, scaleWorkers, owners, attempts, totalRenewals, attemptsAfterStop, drained.metrics.Snapshot().DrainDuration)
}
