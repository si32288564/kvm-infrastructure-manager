package p1c03ovnwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

var errInjectedOVNEndpointTimeout = errors.New("injected bounded OVN endpoint partial timeout")

type latencyMode uint8

const (
	latencyNormal latencyMode = iota
	latencyPreApplyTimeout
	latencyPostApplyTimeout
)

type latencyAdapterState struct {
	mode           latencyMode
	reconcileCalls int
	physicalApply  int
	applied        bool
}

type latencyAdapter struct {
	mu       sync.Mutex
	byDigest map[string]*latencyAdapterState
	delay    time.Duration
}

func (adapter *latencyAdapter) register(objectSetDigest string, mode latencyMode) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.byDigest[objectSetDigest] = &latencyAdapterState{mode: mode}
}

func (adapter *latencyAdapter) wait(ctx context.Context) error {
	timer := time.NewTimer(adapter.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (adapter *latencyAdapter) ReconcilePort(ctx context.Context, _ []byte, objectSetDigest string) (ovnadapter.RuntimeResult, error) {
	adapter.mu.Lock()
	state := adapter.byDigest[objectSetDigest]
	if state == nil {
		adapter.mu.Unlock()
		return ovnadapter.RuntimeResult{}, errors.New("unregistered OVN latency object")
	}
	state.reconcileCalls++
	call, mode := state.reconcileCalls, state.mode
	adapter.mu.Unlock()
	if err := adapter.wait(ctx); err != nil {
		return ovnadapter.RuntimeResult{}, err
	}
	if mode == latencyPreApplyTimeout && call == 1 {
		return ovnadapter.RuntimeResult{}, errInjectedOVNEndpointTimeout
	}

	adapter.mu.Lock()
	state = adapter.byDigest[objectSetDigest]
	state.physicalApply++
	state.applied = true
	adapter.mu.Unlock()
	if mode == latencyPostApplyTimeout && call == 1 {
		return ovnadapter.RuntimeResult{}, errInjectedOVNEndpointTimeout
	}
	return soakRuntimeResult(objectSetDigest, "RECEIVED", true), nil
}

func (adapter *latencyAdapter) ObservePort(ctx context.Context, _ []byte, objectSetDigest string) (ovnadapter.RuntimeResult, error) {
	if err := adapter.wait(ctx); err != nil {
		return ovnadapter.RuntimeResult{}, err
	}
	adapter.mu.Lock()
	state := adapter.byDigest[objectSetDigest]
	applied := state != nil && state.applied
	adapter.mu.Unlock()
	if state == nil {
		return ovnadapter.RuntimeResult{}, errors.New("unregistered OVN latency object")
	}
	return soakRuntimeResult(objectSetDigest, "UNKNOWN", applied), nil
}
func (adapter *latencyAdapter) ObservePortBindingRetirement(ctx context.Context, raw []byte, digest string) (ovnadapter.RuntimeResult, error) {
	return adapter.ObservePort(ctx, raw, digest)
}
func (adapter *latencyAdapter) RetirePortBinding(ctx context.Context, raw []byte, digest string) (ovnadapter.RuntimeResult, error) {
	return adapter.ReconcilePort(ctx, raw, digest)
}

func (adapter *latencyAdapter) assertSinglePhysicalApply(t *testing.T) {
	t.Helper()
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	for objectSetDigest, state := range adapter.byDigest {
		if state.physicalApply != 1 || !state.applied {
			t.Fatalf("object %s physical applies=%d applied=%t reconcile calls=%d mode=%d", objectSetDigest, state.physicalApply, state.applied, state.reconcileCalls, state.mode)
		}
	}
}

func TestOVNRuntimeSustainedLatencyPoolSaturation(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_OVN_LATENCY_SATURATION") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_OVN_LATENCY_SATURATION is not enabled")
	}
	const (
		workItems           = 96
		workerCount         = 8
		batchLimit          = 2
		poolCapacity        = 16
		reservedConnections = 8
		claimLease          = 1500 * time.Millisecond
		maximumLease        = 8 * time.Second
		renewInterval       = 300 * time.Millisecond
		endpointDelay       = 2 * time.Second
	)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	baselineGoroutines := runtime.NumGoroutine()
	container := startRenewalResponseLossPostgreSQL(t, ctx)
	databaseURL := postgresContainerURL(t, ctx, container)
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, poolCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}

	adapter := &latencyAdapter{byDigest: make(map[string]*latencyAdapterState, workItems), delay: endpointDelay}
	failureItems := 0
	for index := 0; index < workItems; index++ {
		suffix := fmt.Sprintf("latency-saturation-%d-%d", time.Now().UnixNano(), index)
		ids := seedOVNAuthority(t, ctx, pool, suffix)
		decision, err := postgres.CommitOVNPortIntent(ctx, pool, postgres.OVNPortIntentRequest{
			IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID,
		})
		if err != nil {
			pool.Close()
			t.Fatal(err)
		}
		mode := latencyNormal
		switch index % 8 {
		case 0:
			mode = latencyPreApplyTimeout
			failureItems++
		case 1:
			mode = latencyPostApplyTimeout
			failureItems++
		}
		adapter.register(decision.ObjectSetDigest, mode)
	}

	reserved := reservePoolConnections(t, ctx, pool, reservedConnections)
	workerContext, stopWorkers := context.WithCancel(ctx)
	start := make(chan struct{})
	metrics := &soakMetrics{}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	startedAt := time.Now()
	for index := 0; index < workerCount; index++ {
		owner := fmt.Sprintf("ovn-latency-worker-%02d", index)
		go func() {
			defer workers.Done()
			<-start
			worker := ovnruntime.Worker{
				Store: ovnruntime.PostgresWorkStore{DB: pool}, Adapter: adapter,
				Owner: owner, BatchLimit: batchLimit, ClaimLease: claimLease,
				ClaimMaximumLifetime: maximumLease, ClaimRenewInterval: renewInterval,
				AdapterArtifactDigest: digest("qualified-ovn-latency-adapter"),
			}
			for workerContext.Err() == nil {
				began := time.Now()
				completed, runErr := worker.RunOnce(workerContext)
				metrics.record(time.Since(began), runErr)
				if workerContext.Err() != nil {
					return
				}
				if runErr != nil || completed == 0 {
					timer := time.NewTimer(3 * time.Millisecond)
					select {
					case <-timer.C:
					case <-workerContext.Done():
						timer.Stop()
						return
					}
				}
			}
		}()
	}
	close(start)

	eventually(t, 90*time.Second, func() bool {
		var observed int
		return pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_current WHERE work_state='OBSERVED'`).Scan(&observed) == nil && observed == workItems
	}, "OVN latency/saturation work did not fully converge")
	stopWorkers()
	workers.Wait()
	elapsed := time.Since(startedAt)
	for _, connection := range reserved {
		connection.Release()
	}

	var total, observed, attempts, renewals, unknownEvents, readBackEvents, maxAttempts, distinctOwners int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE work_state='OBSERVED'),max(attempt_count)
		FROM kim.ovn_runtime_work_current`).Scan(&total, &observed, &maxAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT claim_owner) FROM kim.ovn_runtime_work_attempt_evidence`).Scan(&attempts, &distinctOwners); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_renewal_evidence`).Scan(&renewals); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE event_type='DISPATCH_UNKNOWN'),
		count(*) FILTER (WHERE event_type='READ_BACK_STARTED')
		FROM kim.ovn_runtime_work_event_evidence`).Scan(&unknownEvents, &readBackEvents); err != nil {
		t.Fatal(err)
	}
	adapter.assertSinglePhysicalApply(t)
	p50, p95, p99 := metrics.percentiles()
	poolStats := pool.Stat()
	if total != workItems || observed != workItems || attempts != workItems+failureItems || renewals < workItems || unknownEvents != failureItems || readBackEvents != failureItems || maxAttempts != 2 || distinctOwners != workerCount || poolStats.EmptyAcquireCount() == 0 || poolStats.AcquireDuration() == 0 || p99 >= maximumLease || elapsed >= 60*time.Second {
		t.Fatalf("total=%d observed=%d attempts=%d renewals=%d unknown=%d readback=%d max-attempts=%d owners=%d empty-acquires=%d acquire-wait=%s",
			total, observed, attempts, renewals, unknownEvents, readBackEvents, maxAttempts, distinctOwners, poolStats.EmptyAcquireCount(), poolStats.AcquireDuration())
	}
	t.Logf("items=%d workers=%d batch=%d pool=%d reserved=%d delay=%s elapsed=%s runs=%d errors=%d attempts=%d renewals=%d unknown=%d owners=%d max_attempts=%d p50=%s p95=%s p99=%s empty_acquires=%d acquire_wait=%s",
		workItems, workerCount, batchLimit, poolCapacity, reservedConnections, endpointDelay, elapsed, metrics.runs.Load(), metrics.errors.Load(), attempts, renewals, unknownEvents,
		distinctOwners, maxAttempts, p50, p95, p99, poolStats.EmptyAcquireCount(), poolStats.AcquireDuration())

	pool.Close()
	runtime.GC()
	eventually(t, 3*time.Second, func() bool { return runtime.NumGoroutine() <= baselineGoroutines+12 }, "latency/saturation worker/database goroutines did not return to a bounded baseline")
}

func reservePoolConnections(t *testing.T, ctx context.Context, pool *pgxpool.Pool, count int) []*pgxpool.Conn {
	t.Helper()
	connections := make([]*pgxpool.Conn, 0, count)
	for range count {
		connection, err := pool.Acquire(ctx)
		if err != nil {
			for _, acquired := range connections {
				acquired.Release()
			}
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	return connections
}
