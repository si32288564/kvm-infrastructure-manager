package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

type recordingSink struct{ messages []session.Envelope }

func (sink *recordingSink) Send(_ context.Context, envelope session.Envelope) error {
	sink.messages = append(sink.messages, envelope)
	return nil
}

func TestOutboundRegistryFencesStaleGenerationAndConditionalRelease(t *testing.T) {
	registry := NewOutboundRegistry()
	oldSink, newSink := new(recordingSink), new(recordingSink)
	releaseOld, err := registry.Register("host-1", 1, "connection-1", oldSink)
	if err != nil {
		t.Fatal(err)
	}
	releaseNew, err := registry.Register("host-1", 2, "connection-2", newSink)
	if err != nil {
		t.Fatal(err)
	}
	releaseOld()
	envelope := session.NewEnvelope("host-1", 2, session.StreamCommand, "message-1", "schema/v1", "command", 1, []byte("payload"))
	if err := registry.Send(context.Background(), "host-1", 2, envelope); err != nil {
		t.Fatal(err)
	}
	if len(oldSink.messages) != 0 || len(newSink.messages) != 1 {
		t.Fatalf("sink messages old/new = %d/%d", len(oldSink.messages), len(newSink.messages))
	}
	if err := registry.Send(context.Background(), "host-1", 1, envelope.BindSession(1)); !errors.Is(err, ErrStaleOutboundSession) {
		t.Fatalf("stale Send error = %v", err)
	}
	releaseNew()
	if err := registry.Send(context.Background(), "host-1", 2, envelope); !errors.Is(err, ErrNoCurrentAgentSession) {
		t.Fatalf("released Send error = %v", err)
	}
}
