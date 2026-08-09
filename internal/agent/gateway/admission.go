// Package gateway provides transport-neutral Agent Gateway admission controls.
package gateway

import (
	"errors"
	"sync/atomic"
)

var ErrAdmissionLimited = errors.New("Agent Gateway admission concurrency is exhausted")

// AdmissionLimiter bounds concurrent handshake/authority transactions. It is
// availability protection, not session authority.
type AdmissionLimiter struct {
	permits  chan struct{}
	active   atomic.Int64
	peak     atomic.Int64
	rejected atomic.Int64
}

func NewAdmissionLimiter(maxConcurrent int) (*AdmissionLimiter, error) {
	if maxConcurrent < 1 {
		return nil, errors.New("Agent Gateway admission concurrency must be positive")
	}
	return &AdmissionLimiter{permits: make(chan struct{}, maxConcurrent)}, nil
}

// TryAcquire never waits inside the Gateway. A rejected Agent must apply its
// bounded reconnect policy before retrying.
func (limiter *AdmissionLimiter) TryAcquire() (func(), error) {
	select {
	case limiter.permits <- struct{}{}:
		active := limiter.active.Add(1)
		for {
			peak := limiter.peak.Load()
			if active <= peak || limiter.peak.CompareAndSwap(peak, active) {
				break
			}
		}
		var released atomic.Bool
		return func() {
			if released.CompareAndSwap(false, true) {
				<-limiter.permits
				limiter.active.Add(-1)
			}
		}, nil
	default:
		limiter.rejected.Add(1)
		return nil, ErrAdmissionLimited
	}
}

func (limiter *AdmissionLimiter) Peak() int64     { return limiter.peak.Load() }
func (limiter *AdmissionLimiter) Rejected() int64 { return limiter.rejected.Load() }
