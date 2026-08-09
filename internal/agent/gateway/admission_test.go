package gateway

import (
	"errors"
	"testing"
)

func TestAdmissionLimiterRejectsWithoutExceedingBound(t *testing.T) {
	limiter, err := NewAdmissionLimiter(2)
	if err != nil {
		t.Fatal(err)
	}
	releaseA, err := limiter.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	releaseB, err := limiter.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.TryAcquire(); !errors.Is(err, ErrAdmissionLimited) {
		t.Fatalf("third admission error = %v", err)
	}
	releaseA()
	releaseA()
	releaseB()
	if limiter.Peak() != 2 || limiter.Rejected() != 1 {
		t.Fatalf("peak/rejected = %d/%d", limiter.Peak(), limiter.Rejected())
	}
}
