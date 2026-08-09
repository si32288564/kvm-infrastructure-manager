package reconnect

import (
	"testing"
	"time"
)

func TestBackoffIsBoundedAndDeterministic(t *testing.T) {
	backoff := Backoff{Base: 10 * time.Millisecond, Max: 80 * time.Millisecond}
	for attempt := 1; attempt <= 10; attempt++ {
		first, err := backoff.Delay(attempt, 42+uint64(attempt))
		if err != nil {
			t.Fatal(err)
		}
		second, err := backoff.Delay(attempt, 42+uint64(attempt))
		if err != nil {
			t.Fatal(err)
		}
		if first != second || first < 0 || first > backoff.Max {
			t.Fatalf("attempt %d delays = %v/%v", attempt, first, second)
		}
	}
}
