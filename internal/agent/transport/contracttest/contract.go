package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

// ExerciseEcho runs the common typed-envelope contract against one candidate.
func ExerciseEcho(t testing.TB, adapter session.TransportAdapter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := adapter.Open(ctx, session.Handshake{
		HostIdentity:      "host-1",
		SessionGeneration: 7,
		ProtocolVersion:   "v1",
		Capabilities:      []string{"kim.agent.libvirt.v1", "kim.agent.storage.v1", "kim.agent.network.v1", "kim.agent.clock.v1", "kim.agent.compliance.v1"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	envelope := session.NewEnvelope("host-1", 7, session.StreamResult, "result-1", "v1", "attempt-1", 1, []byte("result"))
	if err := connection.Send(ctx, envelope); err != nil {
		t.Fatalf("Send: %v", err)
	}
	received, err := connection.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if received.MessageID != envelope.MessageID || received.SessionGeneration != envelope.SessionGeneration || received.PayloadDigest != envelope.PayloadDigest {
		t.Fatalf("received = %#v, want %#v", received, envelope)
	}
}

// ExerciseReceiveCancellation verifies that a bounded receive cancels and tears
// down the candidate stream rather than leaking a blocked receive goroutine.
func ExerciseReceiveCancellation(t testing.TB, adapter session.TransportAdapter) {
	t.Helper()
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOpen()
	connection, err := adapter.Open(openContext, session.Handshake{
		HostIdentity:      "host-cancel",
		SessionGeneration: 1,
		ProtocolVersion:   "v1",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	receiveContext, cancelReceive := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelReceive()
	if _, err := connection.Receive(receiveContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive error = %v, want deadline exceeded", err)
	}
	if err := connection.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// ExerciseDisconnect verifies that connection loss surfaces as transport
// uncertainty instead of a successful receive or an indefinitely blocked call.
func ExerciseDisconnect(t testing.TB, adapter session.TransportAdapter, disconnect func()) {
	t.Helper()
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOpen()
	connection, err := adapter.Open(openContext, session.Handshake{
		HostIdentity:      "host-disconnect",
		SessionGeneration: 2,
		ProtocolVersion:   "v1",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	disconnect()
	receiveContext, cancelReceive := context.WithTimeout(context.Background(), time.Second)
	defer cancelReceive()
	if _, err := connection.Receive(receiveContext); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive after disconnect error = %v", err)
	}
	_ = connection.Close()
}

// BenchmarkRoundTrip measures one already-open candidate session.
func BenchmarkRoundTrip(benchmark *testing.B, adapter session.TransportAdapter, payloadBytes int) {
	benchmark.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, err := adapter.Open(ctx, session.Handshake{
		HostIdentity:      "host-benchmark",
		SessionGeneration: 1,
		ProtocolVersion:   "v1",
	})
	if err != nil {
		benchmark.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	payload := make([]byte, payloadBytes)
	benchmark.SetBytes(int64(payloadBytes))
	benchmark.ResetTimer()
	for index := 0; index < benchmark.N; index++ {
		envelope := session.NewEnvelope("host-benchmark", 1, session.StreamResult, "result", "v1", "attempt", uint64(index+1), payload)
		if err := connection.Send(ctx, envelope); err != nil {
			benchmark.Fatal(err)
		}
		if _, err := connection.Receive(ctx); err != nil {
			benchmark.Fatal(err)
		}
	}
}
