package ovnruntime

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

const (
	workerStateStarting int32 = iota
	workerStateActive
	workerStateDraining
	workerStateStopped
)

// Metrics contains bounded, identity-free worker lifecycle and execution
// measurements suitable for a Prometheus projection.
type Metrics struct {
	state atomic.Int32

	claimRuns, claimErrors, claimsTotal       atomic.Int64
	inFlight, completedTotal                  atomic.Int64
	itemErrors, fatalErrors                   atomic.Int64
	renewals, renewalErrors                   atomic.Int64
	claimLatencyNanos, renewalLatencyNanos    atomic.Int64
	drainStartedUnixNanos, drainDurationNanos atomic.Int64
	renewalHeadroomNanos                      atomic.Int64
	inFlightMu                                sync.Mutex
	inFlightStarted                           map[string]time.Time
}

type MetricsSnapshot struct {
	State                               string
	ClaimRuns, ClaimErrors, ClaimsTotal int64
	InFlight, CompletedTotal            int64
	ItemErrors, FatalErrors             int64
	Renewals, RenewalErrors             int64
	ClaimLatency, RenewalLatency        time.Duration
	DrainStartedAt                      time.Time
	DrainDuration                       time.Duration
	OldestInFlightAge, RenewalHeadroom  time.Duration
}

func NewMetrics() *Metrics {
	metrics := &Metrics{}
	metrics.state.Store(workerStateStarting)
	metrics.inFlightStarted = make(map[string]time.Time)
	return metrics
}

func (metrics *Metrics) setState(state int32) {
	if metrics != nil {
		metrics.state.Store(state)
	}
}

func (metrics *Metrics) startDrain() {
	if metrics == nil {
		return
	}
	if metrics.state.Swap(workerStateDraining) != workerStateDraining {
		metrics.drainStartedUnixNanos.CompareAndSwap(0, time.Now().UnixNano())
	}
}

func (metrics *Metrics) stop() {
	if metrics == nil {
		return
	}
	metrics.state.Store(workerStateStopped)
	started := metrics.drainStartedUnixNanos.Load()
	if started > 0 {
		metrics.drainDurationNanos.Store(time.Now().UnixNano() - started)
	}
}

func (metrics *Metrics) recordClaim(work []postgres.OVNRuntimeWork, latency time.Duration, err error) {
	if metrics == nil {
		return
	}
	metrics.claimRuns.Add(1)
	metrics.claimLatencyNanos.Store(latency.Nanoseconds())
	if err != nil {
		metrics.claimErrors.Add(1)
		return
	}
	metrics.claimsTotal.Add(int64(len(work)))
	metrics.inFlight.Add(int64(len(work)))
	now := time.Now()
	metrics.inFlightMu.Lock()
	for _, item := range work {
		metrics.inFlightStarted[item.WorkID] = now
	}
	metrics.inFlightMu.Unlock()
}

func (metrics *Metrics) recordWork(workID string, completed bool, err error) {
	if metrics == nil {
		return
	}
	metrics.inFlight.Add(-1)
	metrics.inFlightMu.Lock()
	delete(metrics.inFlightStarted, workID)
	metrics.inFlightMu.Unlock()
	if completed {
		metrics.completedTotal.Add(1)
	}
	if err == nil {
		return
	}
	var itemFailure *itemLocalError
	if errors.As(err, &itemFailure) {
		metrics.itemErrors.Add(1)
	} else {
		metrics.fatalErrors.Add(1)
	}
}

func (metrics *Metrics) recordRenewal(renewal postgres.OVNRuntimeRenewal, latency time.Duration, err error) {
	if metrics == nil {
		return
	}
	metrics.renewalLatencyNanos.Store(latency.Nanoseconds())
	if err != nil {
		metrics.renewalErrors.Add(1)
		return
	}
	metrics.renewals.Add(1)
	if !renewal.RenewedExpiresAt.IsZero() {
		metrics.renewalHeadroomNanos.Store(time.Until(renewal.RenewedExpiresAt).Nanoseconds())
	}
}

func (metrics *Metrics) Snapshot() MetricsSnapshot {
	if metrics == nil {
		return MetricsSnapshot{State: "UNINSTRUMENTED"}
	}
	state := map[int32]string{
		workerStateStarting: "STARTING", workerStateActive: "ACTIVE",
		workerStateDraining: "DRAINING", workerStateStopped: "STOPPED",
	}[metrics.state.Load()]
	started := metrics.drainStartedUnixNanos.Load()
	snapshot := MetricsSnapshot{
		State:     state,
		ClaimRuns: metrics.claimRuns.Load(), ClaimErrors: metrics.claimErrors.Load(), ClaimsTotal: metrics.claimsTotal.Load(),
		InFlight: metrics.inFlight.Load(), CompletedTotal: metrics.completedTotal.Load(),
		ItemErrors: metrics.itemErrors.Load(), FatalErrors: metrics.fatalErrors.Load(),
		Renewals: metrics.renewals.Load(), RenewalErrors: metrics.renewalErrors.Load(),
		ClaimLatency: time.Duration(metrics.claimLatencyNanos.Load()), RenewalLatency: time.Duration(metrics.renewalLatencyNanos.Load()),
		DrainDuration: time.Duration(metrics.drainDurationNanos.Load()), RenewalHeadroom: time.Duration(metrics.renewalHeadroomNanos.Load()),
	}
	metrics.inFlightMu.Lock()
	for _, startedAt := range metrics.inFlightStarted {
		age := time.Since(startedAt)
		if age > snapshot.OldestInFlightAge {
			snapshot.OldestInFlightAge = age
		}
	}
	metrics.inFlightMu.Unlock()
	if started > 0 {
		snapshot.DrainStartedAt = time.Unix(0, started)
	}
	return snapshot
}
