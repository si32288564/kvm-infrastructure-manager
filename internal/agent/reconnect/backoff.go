// Package reconnect defines bounded Agent reconnect timing independent of the
// selected transport adapter.
package reconnect

import (
	"errors"
	"time"
)

// Backoff applies bounded exponential delay with deterministic full jitter.
// Entropy is supplied per Host/attempt so reconnect behavior needs no shared RNG.
type Backoff struct {
	Base time.Duration
	Max  time.Duration
}

func (backoff Backoff) Delay(attempt int, entropy uint64) (time.Duration, error) {
	if backoff.Base <= 0 || backoff.Max < backoff.Base || attempt < 1 {
		return 0, errors.New("valid reconnect base, maximum, and positive attempt are required")
	}
	ceiling := backoff.Base
	for step := 1; step < attempt && ceiling < backoff.Max; step++ {
		if ceiling > backoff.Max/2 {
			ceiling = backoff.Max
			break
		}
		ceiling *= 2
	}
	if ceiling > backoff.Max {
		ceiling = backoff.Max
	}
	// xorshift64* yields stable per-attempt fixture jitter in [0, ceiling].
	entropy ^= entropy >> 12
	entropy ^= entropy << 25
	entropy ^= entropy >> 27
	entropy *= 2685821657736338717
	return time.Duration(entropy % (uint64(ceiling) + 1)), nil
}
