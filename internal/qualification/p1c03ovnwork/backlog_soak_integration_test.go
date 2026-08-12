package p1c03ovnwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

var errInjectedOVNRetryStorm = errors.New("injected bounded OVN retry-storm failure")

type soakMode uint8

const (
	soakNormal soakMode = iota
	soakPreApplyFailure
	soakPostApplyResponseLoss
	soakLongRunning
)

type soakAdapterState struct {
	mode           soakMode
	reconcileCalls int
	physicalApply  int
	applied        bool
}

type soakAdapter struct {
	mu        sync.Mutex
	byDigest  map[string]*soakAdapterState
	longDelay time.Duration
}

func (adapter *soakAdapter) register(objectSetDigest string, mode soakMode) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.byDigest[objectSetDigest] = &soakAdapterState{mode: mode}
}

func (adapter *soakAdapter) ReconcilePort(ctx context.Context, _ []byte, objectSetDigest string) (ovnadapter.RuntimeResult, error) {
	adapter.mu.Lock()
	state := adapter.byDigest[objectSetDigest]
	if state == nil {
		adapter.mu.Unlock()
		return ovnadapter.RuntimeResult{}, errors.New("unregistered OVN soak object")
	}
	state.reconcileCalls++
	call, mode := state.reconcileCalls, state.mode
	if mode == soakPreApplyFailure && call == 1 {
		adapter.mu.Unlock()
		return ovnadapter.RuntimeResult{}, errInjectedOVNRetryStorm
	}
	adapter.mu.Unlock()

	if mode == soakLongRunning {
		timer := time.NewTimer(adapter.longDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ovnadapter.RuntimeResult{}, ctx.Err()
		}
	}

	adapter.mu.Lock()
	state = adapter.byDigest[objectSetDigest]
	state.physicalApply++
	state.applied = true
	adapter.mu.Unlock()
	if mode == soakPostApplyResponseLoss && call == 1 {
		return ovnadapter.RuntimeResult{}, errInjectedOVNRetryStorm
	}
	return soakRuntimeResult(objectSetDigest, "RECEIVED", true), nil
}

func (adapter *soakAdapter) ObservePort(_ context.Context, _ []byte, objectSetDigest string) (ovnadapter.RuntimeResult, error) {
	adapter.mu.Lock()
	state := adapter.byDigest[objectSetDigest]
	applied := state != nil && state.applied
	adapter.mu.Unlock()
	if state == nil {
		return ovnadapter.RuntimeResult{}, errors.New("unregistered OVN soak object")
	}
	return soakRuntimeResult(objectSetDigest, "UNKNOWN", applied), nil
}
func (adapter *soakAdapter) ObservePortBindingRetirement(ctx context.Context, raw []byte, digest string) (ovnadapter.RuntimeResult, error) {
	return adapter.ObservePort(ctx, raw, digest)
}
func (adapter *soakAdapter) RetirePortBinding(ctx context.Context, raw []byte, digest string) (ovnadapter.RuntimeResult, error) {
	return adapter.ReconcilePort(ctx, raw, digest)
}

func (adapter *soakAdapter) assertSinglePhysicalApply(t *testing.T) {
	t.Helper()
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	for objectSetDigest, state := range adapter.byDigest {
		if state.physicalApply != 1 || !state.applied {
			t.Fatalf("object %s physical applies=%d applied=%t reconcile calls=%d mode=%d", objectSetDigest, state.physicalApply, state.applied, state.reconcileCalls, state.mode)
		}
	}
}

func soakRuntimeResult(objectSetDigest, responseState string, matched bool) ovnadapter.RuntimeResult {
	return ovnadapter.RuntimeResult{
		ApplyResponseState:    responseState,
		NBObservationDigest:   digest("soak-nb:" + objectSetDigest),
		SBObservationDigest:   digest("soak-sb:" + objectSetDigest),
		ChassisIdentityDigest: digest("soak-chassis:" + objectSetDigest),
		Observation: ovnadapter.Observation{
			OwnershipMarkerMatches: matched, ObjectSetDigestMatches: matched,
			LogicalSwitchPresent: matched, LogicalSwitchPortPresent: matched,
			PortBindingPresent: matched, DatapathPresent: matched, ExpectedChassisMatches: matched,
		},
	}
}

type soakMetrics struct {
	mu           sync.Mutex
	runLatency   []time.Duration
	runs, errors atomic.Int64
}

func (metrics *soakMetrics) record(latency time.Duration, err error) {
	metrics.runs.Add(1)
	if err != nil {
		metrics.errors.Add(1)
	}
	metrics.mu.Lock()
	metrics.runLatency = append(metrics.runLatency, latency)
	metrics.mu.Unlock()
}

func (metrics *soakMetrics) percentiles() (time.Duration, time.Duration, time.Duration) {
	metrics.mu.Lock()
	values := append([]time.Duration(nil), metrics.runLatency...)
	metrics.mu.Unlock()
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return 0, 0, 0
	}
	value := func(percent int) time.Duration {
		index := (len(values)*percent + 99) / 100
		if index < 1 {
			index = 1
		}
		if index > len(values) {
			index = len(values)
		}
		return values[index-1]
	}
	return value(50), value(95), value(99)
}

func TestOVNRuntimeBacklogRetryStormMultiWorkerSoak(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_OVN_SOAK") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_OVN_SOAK is not enabled")
	}
	const (
		workItems     = 512
		workerCount   = 16
		batchLimit    = 2
		poolCapacity  = 64
		claimLease    = time.Second
		maximumLease  = 5 * time.Second
		renewInterval = 200 * time.Millisecond
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

	adapter := &soakAdapter{byDigest: make(map[string]*soakAdapterState, workItems), longDelay: 1500 * time.Millisecond}
	failureItems, longRunningItems := 0, 0
	for index := 0; index < workItems; index++ {
		suffix := fmt.Sprintf("soak-%d-%d", time.Now().UnixNano(), index)
		ids := seedOVNAuthority(t, ctx, pool, suffix)
		decision, err := postgres.CommitOVNPortIntent(ctx, pool, postgres.OVNPortIntentRequest{
			IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID,
		})
		if err != nil {
			pool.Close()
			t.Fatal(err)
		}
		mode := soakNormal
		switch index % 10 {
		case 0:
			mode = soakPreApplyFailure
			failureItems++
		case 1:
			mode = soakPostApplyResponseLoss
			failureItems++
		case 2:
			mode = soakLongRunning
			longRunningItems++
		}
		adapter.register(decision.ObjectSetDigest, mode)
	}

	workerContext, stopWorkers := context.WithCancel(ctx)
	start := make(chan struct{})
	metrics := &soakMetrics{}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	startedAt := time.Now()
	for index := 0; index < workerCount; index++ {
		owner := fmt.Sprintf("ovn-soak-worker-%02d", index)
		go func() {
			defer workers.Done()
			<-start
			worker := ovnruntime.Worker{
				Store: ovnruntime.PostgresWorkStore{DB: pool}, Adapter: adapter,
				Owner: owner, BatchLimit: batchLimit, ClaimLease: claimLease,
				ClaimMaximumLifetime: maximumLease, ClaimRenewInterval: renewInterval,
				AdapterArtifactDigest: digest("qualified-ovn-soak-adapter"),
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
	}, "OVN backlog did not fully converge")
	stopWorkers()
	workers.Wait()
	elapsed := time.Since(startedAt)

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
	if total != workItems || observed != workItems || attempts != workItems+failureItems || renewals < longRunningItems || unknownEvents != failureItems || readBackEvents != failureItems || maxAttempts != 2 || distinctOwners != workerCount || p99 >= maximumLease || elapsed >= 30*time.Second {
		t.Fatalf("total=%d observed=%d attempts=%d renewals=%d unknown=%d readback=%d max-attempts=%d owners=%d", total, observed, attempts, renewals, unknownEvents, readBackEvents, maxAttempts, distinctOwners)
	}
	poolStats := pool.Stat()
	t.Logf("items=%d workers=%d batch=%d pool=%d elapsed=%s runs=%d errors=%d attempts=%d renewals=%d unknown=%d owners=%d max_attempts=%d p50=%s p95=%s p99=%s empty_acquires=%d acquire_wait=%s",
		workItems, workerCount, batchLimit, poolCapacity, elapsed, metrics.runs.Load(), metrics.errors.Load(), attempts, renewals, unknownEvents,
		distinctOwners, maxAttempts, p50, p95, p99, poolStats.EmptyAcquireCount(), poolStats.AcquireDuration())

	pool.Close()
	runtime.GC()
	eventually(t, 3*time.Second, func() bool { return runtime.NumGoroutine() <= baselineGoroutines+12 }, "worker/database goroutines did not return to a bounded baseline")
}
