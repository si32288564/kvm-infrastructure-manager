package session

import (
	"fmt"
	"time"
)

// AdmissionRejectedError is a typed, non-authoritative transport control. It
// never consumes a session generation or proves that a prior session is gone.
type AdmissionRejectedError struct {
	Code       string
	RetryAfter time.Duration
	Retryable  bool
}

func (rejection *AdmissionRejectedError) Error() string {
	return fmt.Sprintf("Agent session rejected: code=%s retryable=%t retry_after=%s", rejection.Code, rejection.Retryable, rejection.RetryAfter)
}
