package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

type runnerConnection struct {
	inbound  chan Envelope
	receipts chan Receipt
	sent     chan Envelope
}

func (connection *runnerConnection) Send(ctx context.Context, envelope Envelope) error {
	select {
	case connection.sent <- envelope:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (connection *runnerConnection) Receive(ctx context.Context) (Envelope, error) {
	select {
	case envelope := <-connection.inbound:
		return envelope, nil
	case <-ctx.Done():
		return Envelope{}, context.Cause(ctx)
	}
}
func (connection *runnerConnection) ReceiveReceipt(ctx context.Context) (Receipt, error) {
	select {
	case receipt := <-connection.receipts:
		return receipt, nil
	case <-ctx.Done():
		return Receipt{}, context.Cause(ctx)
	}
}
func (*runnerConnection) Close() error { return nil }

type runnerAdapter struct{ connection *runnerConnection }

func (adapter runnerAdapter) Open(context.Context, Handshake) (TransportConnection, error) {
	return adapter.connection, nil
}

type receiptRecorder struct {
	mu    sync.Mutex
	count int
}

func (recorder *receiptRecorder) HandleReceipt(context.Context, Receipt) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.count++
	return nil
}

type runnerModule struct{ handled chan struct{} }

func (*runnerModule) Descriptor() ModuleDescriptor {
	return ModuleDescriptor{Name: "execution", Capabilities: []string{"kim.agent.execution/v1"}, MinSchemaVersion: "command/v1", MaxSchemaVersion: "command/v1", MessageSchemas: []string{"command/v1"}}
}
func (module *runnerModule) Handle(context.Context, Envelope) error {
	select {
	case module.handled <- struct{}{}:
	default:
	}
	return nil
}

func TestRunnerMultiplexesInboundOutboundAndReceipts(t *testing.T) {
	connection := &runnerConnection{inbound: make(chan Envelope, 1), receipts: make(chan Receipt, 1), sent: make(chan Envelope, 1)}
	manager, err := NewManager(Handshake{HostIdentity: "host-1", SessionGeneration: 1, ProtocolVersion: "v1"}, runnerAdapter{connection}, newTestQueue(t))
	if err != nil {
		t.Fatal(err)
	}
	module := &runnerModule{handled: make(chan struct{}, 1)}
	if err := manager.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	if err := manager.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := NewEnvelope("host-1", 1, StreamResult, "result-1", "result/v1", "result", 1, []byte("result"))
	if err := manager.Publish(result); err != nil {
		t.Fatal(err)
	}
	command := NewEnvelope("host-1", 1, StreamCommand, "command-1", "command/v1", "command", 1, []byte("command"))
	connection.inbound <- command
	connection.receipts <- Receipt{HostIdentity: "host-1", AcceptedSessionGeneration: 1, Stream: StreamResult, MessageID: "result-1", SequenceScope: "result", Sequence: 1, PayloadDigest: result.PayloadDigest, Disposition: "ACCEPTED"}
	ctx, cancel := context.WithCancel(t.Context())
	recorder := new(receiptRecorder)
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Manager: manager, ReceiptHandler: recorder, FlushInterval: time.Millisecond}).Run(ctx)
	}()
	select {
	case <-connection.sent:
	case <-time.After(time.Second):
		t.Fatal("outbound envelope was not flushed")
	}
	select {
	case <-module.handled:
	case <-time.After(time.Second):
		t.Fatal("inbound envelope was not routed")
	}
	deadline := time.Now().Add(time.Second)
	for {
		recorder.mu.Lock()
		count := recorder.count
		recorder.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("receipts = %d", count)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Runner did not stop")
	}
}

func TestRunnerRequiresReceiptHandlerBeforeStartingLoops(t *testing.T) {
	connection := &runnerConnection{inbound: make(chan Envelope), receipts: make(chan Receipt), sent: make(chan Envelope)}
	manager, err := NewManager(Handshake{HostIdentity: "host-1", SessionGeneration: 1, ProtocolVersion: "v1"}, runnerAdapter{connection}, newTestQueue(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := (Runner{Manager: manager, FlushInterval: time.Millisecond}).Run(t.Context()); err == nil {
		t.Fatal("receipt-capable transport was accepted without durable receipt handler")
	}
}
