package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestModulesShareOneTransportConnection(t *testing.T) {
	adapter := &countingAdapter{}
	manager, err := NewManager(Handshake{
		HostIdentity:      "host-1",
		SessionGeneration: 7,
		ProtocolVersion:   "v1",
	}, adapter, newTestQueue(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"libvirt", "storage", "network", "clock", "compliance"} {
		if err := manager.RegisterModule(&testModule{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if adapter.opens != 0 {
		t.Fatalf("module registration opened %d connections", adapter.opens)
	}
	if err := manager.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if adapter.opens != 1 {
		t.Fatalf("Manager opened %d connections, want 1", adapter.opens)
	}
	if err := manager.Open(context.Background()); !errors.Is(err, ErrSessionAlreadyOpen) {
		t.Fatalf("second Open error = %v", err)
	}
	if adapter.opens != 1 {
		t.Fatalf("second Open changed connection count to %d", adapter.opens)
	}
	if err := manager.RegisterModule(&testModule{name: "late-module"}); !errors.Is(err, ErrModuleSetSealed) {
		t.Fatalf("late RegisterModule error = %v", err)
	}
}

func TestStaleSessionIsFencedBeforeModule(t *testing.T) {
	adapter := &countingAdapter{}
	manager, err := NewManager(Handshake{HostIdentity: "host-1", SessionGeneration: 9, ProtocolVersion: "v1"}, adapter, newTestQueue(t))
	if err != nil {
		t.Fatal(err)
	}
	module := &testModule{name: "libvirt"}
	if err := manager.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	stale := NewEnvelope("host-1", 8, StreamResult, "result-1", "v1", "attempt-1", 1, []byte("result"))
	if err := manager.AcceptInbound(context.Background(), "libvirt", stale); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("AcceptInbound error = %v", err)
	}
	if module.handled != 0 {
		t.Fatalf("stale message reached module %d times", module.handled)
	}
}

type countingAdapter struct {
	opens int
}

func (adapter *countingAdapter) Open(context.Context, Handshake) (TransportConnection, error) {
	adapter.opens++
	return &testConnection{}, nil
}

type testConnection struct{}

func (*testConnection) Send(context.Context, Envelope) error { return nil }
func (*testConnection) Receive(context.Context) (Envelope, error) {
	return Envelope{}, errors.New("no message")
}
func (*testConnection) Close() error { return nil }

type testModule struct {
	name    string
	handled int
}

func (module *testModule) Descriptor() ModuleDescriptor {
	return ModuleDescriptor{
		Name:             module.name,
		Capabilities:     []string{fmt.Sprintf("kim.agent.%s.v1", module.name)},
		MinSchemaVersion: "v1",
		MaxSchemaVersion: "v1",
	}
}

func (module *testModule) Handle(context.Context, Envelope) error {
	module.handled++
	return nil
}
