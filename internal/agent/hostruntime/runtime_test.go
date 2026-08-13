package hostruntime

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/reconnect"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/sessiongeneration"
)

type blockingConnection struct{}

func (blockingConnection) Send(context.Context, session.Envelope) error { return nil }
func (blockingConnection) Receive(ctx context.Context) (session.Envelope, error) {
	<-ctx.Done()
	return session.Envelope{}, context.Cause(ctx)
}

type scriptedConnection struct{ fail bool }

func (connection scriptedConnection) Send(context.Context, session.Envelope) error { return nil }
func (connection scriptedConnection) Receive(ctx context.Context) (session.Envelope, error) {
	if connection.fail {
		return session.Envelope{}, io.EOF
	}
	<-ctx.Done()
	return session.Envelope{}, context.Cause(ctx)
}
func (connection scriptedConnection) ReceiveReceipt(ctx context.Context) (session.Receipt, error) {
	if connection.fail {
		return session.Receipt{}, io.EOF
	}
	<-ctx.Done()
	return session.Receipt{}, context.Cause(ctx)
}
func (scriptedConnection) Close() error { return nil }

type scriptedAdapter struct {
	mu         sync.Mutex
	handshakes []session.Handshake
	thirdOpen  chan struct{}
}

func (adapter *scriptedAdapter) Open(_ context.Context, handshake session.Handshake) (session.TransportConnection, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.handshakes = append(adapter.handshakes, handshake)
	switch len(adapter.handshakes) {
	case 1:
		return nil, errors.New("rejected before SessionAccepted")
	case 2:
		return scriptedConnection{fail: true}, nil
	default:
		select {
		case <-adapter.thirdOpen:
		default:
			close(adapter.thirdOpen)
		}
		return scriptedConnection{}, nil
	}
}
func (blockingConnection) ReceiveReceipt(ctx context.Context) (session.Receipt, error) {
	<-ctx.Done()
	return session.Receipt{}, context.Cause(ctx)
}
func (blockingConnection) Close() error { return nil }

type acceptedAdapter struct{}

func (acceptedAdapter) Open(context.Context, session.Handshake) (session.TransportConnection, error) {
	return blockingConnection{}, nil
}

type lifecycleComponent struct {
	mu                                      sync.Mutex
	started, activated, deactivated, closed bool
	generation                              uint64
}

func (component *lifecycleComponent) Start(context.Context) error {
	component.mu.Lock()
	defer component.mu.Unlock()
	component.started = true
	return nil
}
func (component *lifecycleComponent) Activate(generation uint64, credential int64) error {
	component.mu.Lock()
	defer component.mu.Unlock()
	if !component.started || generation == 0 || credential != 1 {
		return errors.New("runtime activated before readiness or with wrong identity")
	}
	component.activated, component.generation = true, generation
	return nil
}
func (component *lifecycleComponent) Deactivate(generation uint64) {
	component.mu.Lock()
	defer component.mu.Unlock()
	if generation == component.generation {
		component.deactivated = true
	}
}
func (component *lifecycleComponent) Close(context.Context) error {
	component.mu.Lock()
	defer component.mu.Unlock()
	component.closed = true
	return nil
}

func TestRuntimeOwnsSessionUntilGracefulCancellation(t *testing.T) {
	root := t.TempDir()
	limits := session.QueueLimits{MaxMessageBytes: 64 << 10, MaxTotalMessages: 32, MaxTotalBytes: 1 << 20, ReservedPriorityMsgs: 4, ReservedPriorityBytes: 64 << 10, MaxConsecutivePriority: 8, PerStreamMessages: map[session.Stream]int{session.StreamControl: 8, session.StreamCommand: 8, session.StreamResult: 8, session.StreamHeartbeat: 8, session.StreamCredential: 8, session.StreamInventory: 8, session.StreamResync: 8}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{HostID: "host-1", ProtocolVersion: "v1", AgentArtifactDigest: strings.Repeat("a", 64), CredentialBindingRevision: 1, VerifierDigest: strings.Repeat("b", 64), StateDirectory: filepath.Join(root, "state"), SpoolDirectory: filepath.Join(root, "spool"), JournalDirectory: filepath.Join(root, "journal"), GenerationDirectory: filepath.Join(root, "generation"), Adapter: acceptedAdapter{}, QueueLimits: limits, SpoolMaxEntries: 32, SpoolMaxBytes: 1 << 20, FlushInterval: time.Millisecond, ReconnectBackoff: reconnect.Backoff{Base: time.Millisecond, Max: 10 * time.Millisecond}})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Host Agent runtime did not stop")
	}
}

func TestRuntimeComponentIsReadyBeforeSessionAndRevokedOnShutdown(t *testing.T) {
	root := t.TempDir()
	component := &lifecycleComponent{}
	limits := session.QueueLimits{MaxMessageBytes: 64 << 10, MaxTotalMessages: 32, MaxTotalBytes: 1 << 20, ReservedPriorityMsgs: 4, ReservedPriorityBytes: 64 << 10, MaxConsecutivePriority: 8, PerStreamMessages: map[session.Stream]int{session.StreamControl: 8, session.StreamCommand: 8, session.StreamResult: 8, session.StreamHeartbeat: 8, session.StreamCredential: 8, session.StreamInventory: 8, session.StreamResync: 8}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{HostID: "host-runtime-component", ProtocolVersion: "v1", AgentArtifactDigest: strings.Repeat("a", 64), CredentialBindingRevision: 1, VerifierDigest: strings.Repeat("b", 64), StateDirectory: filepath.Join(root, "state"), SpoolDirectory: filepath.Join(root, "spool"), JournalDirectory: filepath.Join(root, "journal"), GenerationDirectory: filepath.Join(root, "generation"), Adapter: acceptedAdapter{}, QueueLimits: limits, SpoolMaxEntries: 32, SpoolMaxBytes: 1 << 20, FlushInterval: time.Millisecond, ReconnectBackoff: reconnect.Backoff{Base: time.Millisecond, Max: 10 * time.Millisecond}, RuntimeComponents: []RuntimeComponent{component}})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		component.mu.Lock()
		active := component.activated
		component.mu.Unlock()
		if active || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	component.mu.Lock()
	defer component.mu.Unlock()
	if !component.started || !component.activated || !component.deactivated || !component.closed || component.generation != 1 {
		t.Fatalf("component lifecycle=%+v", component)
	}
}

func TestRuntimeConsumesGenerationOnlyAfterAcceptedSession(t *testing.T) {
	root := t.TempDir()
	generationDirectory := filepath.Join(root, "generation")
	adapter := &scriptedAdapter{thirdOpen: make(chan struct{})}
	limits := session.QueueLimits{MaxMessageBytes: 64 << 10, MaxTotalMessages: 32, MaxTotalBytes: 1 << 20, ReservedPriorityMsgs: 4, ReservedPriorityBytes: 64 << 10, MaxConsecutivePriority: 8, PerStreamMessages: map[session.Stream]int{session.StreamControl: 8, session.StreamCommand: 8, session.StreamResult: 8, session.StreamHeartbeat: 8, session.StreamCredential: 8, session.StreamInventory: 8, session.StreamResync: 8}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{HostID: "host-1", ProtocolVersion: "v1", AgentArtifactDigest: strings.Repeat("a", 64), CredentialBindingRevision: 1, VerifierDigest: strings.Repeat("b", 64), StateDirectory: filepath.Join(root, "state"), SpoolDirectory: filepath.Join(root, "spool"), JournalDirectory: filepath.Join(root, "journal"), GenerationDirectory: generationDirectory, Adapter: adapter, QueueLimits: limits, SpoolMaxEntries: 32, SpoolMaxBytes: 1 << 20, FlushInterval: time.Millisecond, ReconnectBackoff: reconnect.Backoff{Base: time.Millisecond, Max: 10 * time.Millisecond}})
	}()
	select {
	case <-adapter.thirdOpen:
	case <-time.After(time.Second):
		t.Fatal("Host Agent did not reach second accepted session")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	var generations []uint64
	for _, handshake := range adapter.handshakes {
		generations = append(generations, handshake.SessionGeneration)
	}
	adapter.mu.Unlock()
	if len(generations) < 3 || generations[0] != 1 || generations[1] != 1 || generations[2] != 2 {
		t.Fatalf("proposed session generations = %v", generations)
	}
	ledger, err := sessiongeneration.Open(generationDirectory, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if next, err := ledger.Next(); err != nil || next != 3 {
		t.Fatalf("next restart generation = %d, error = %v", next, err)
	}
}
