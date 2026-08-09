package session

import (
	"context"
	"errors"
	"testing"
)

type runtimeConnection struct {
	inbound Envelope
	sent    []Envelope
	sendErr error
}

func (connection *runtimeConnection) Send(_ context.Context, envelope Envelope) error {
	if connection.sendErr != nil {
		return connection.sendErr
	}
	connection.sent = append(connection.sent, envelope)
	return nil
}
func (connection *runtimeConnection) Receive(context.Context) (Envelope, error) {
	return connection.inbound, nil
}
func (*runtimeConnection) Close() error { return nil }

type runtimeAdapter struct{ connection *runtimeConnection }

func (adapter runtimeAdapter) Open(context.Context, Handshake) (TransportConnection, error) {
	return adapter.connection, nil
}

type runtimeModule struct{ handled int }

func (*runtimeModule) Descriptor() ModuleDescriptor {
	return ModuleDescriptor{Name: "execution", Capabilities: []string{"kim.agent.execution/v1"}, MinSchemaVersion: "command/v1", MaxSchemaVersion: "command/v1", MessageSchemas: []string{"command/v1"}}
}
func (module *runtimeModule) Handle(context.Context, Envelope) error { module.handled++; return nil }

func TestManagerRoutesInboundSchemaAndRequeuesUncertainSend(t *testing.T) {
	inbound := NewEnvelope("host-1", 1, StreamCommand, "command-1", "command/v1", "command", 1, []byte("command"))
	connection := &runtimeConnection{inbound: inbound}
	manager, err := NewManager(Handshake{HostIdentity: "host-1", SessionGeneration: 1, ProtocolVersion: "v1"}, runtimeAdapter{connection}, newTestQueue(t))
	if err != nil {
		t.Fatal(err)
	}
	module := new(runtimeModule)
	if err := manager.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	if err := manager.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReceiveAndRoute(context.Background()); err != nil || module.handled != 1 {
		t.Fatalf("route handled/error = %d/%v", module.handled, err)
	}
	result := NewEnvelope("host-1", 1, StreamResult, "result-1", "result/v1", "command", 1, []byte("result"))
	if err := manager.Publish(result); err != nil {
		t.Fatal(err)
	}
	connection.sendErr = errors.New("uncertain send")
	if sent, err := manager.FlushOne(context.Background()); err == nil || sent {
		t.Fatalf("uncertain FlushOne = %v/%v", sent, err)
	}
	if messages, _ := manager.queue.Stats(); messages != 1 {
		t.Fatalf("requeued messages = %d", messages)
	}
	connection.sendErr = nil
	if sent, err := manager.FlushOne(context.Background()); err != nil || !sent || len(connection.sent) != 1 {
		t.Fatalf("successful FlushOne = %v/%v sent=%d", sent, err, len(connection.sent))
	}
}
