package workerruntime

import (
	"context"
	"sync"
	"testing"
	"time"
)

type countingMaintainer struct {
	mu    sync.Mutex
	calls int
}

type countingPublisher struct {
	mu    sync.Mutex
	calls int
}

func (publisher *countingPublisher) PublishOnce(context.Context) (int, error) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.calls++
	return 1, nil
}

func (maintainer *countingMaintainer) ExpireDue(context.Context, int) (int, error) {
	maintainer.mu.Lock()
	defer maintainer.mu.Unlock()
	maintainer.calls++
	return 1, nil
}

func TestWorkerRunsDeliveryPublisherOnIndependentBoundedCadence(t *testing.T) {
	maintainer := new(countingMaintainer)
	publisher := new(countingPublisher)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- RunWithDelivery(ctx, Config{SweepInterval: 10 * time.Millisecond, BatchLimit: 16, PublishInterval: time.Millisecond}, maintainer, publisher)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		publisher.mu.Lock()
		calls := publisher.calls
		publisher.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Worker delivery publisher loop did not run")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRunsImmediatelyAndStopsGracefully(t *testing.T) {
	maintainer := new(countingMaintainer)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Config{SweepInterval: time.Millisecond, BatchLimit: 16}, maintainer) }()
	deadline := time.Now().Add(time.Second)
	for {
		maintainer.mu.Lock()
		calls := maintainer.calls
		maintainer.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Worker maintenance loop did not run")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
