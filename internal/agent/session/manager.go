package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrSessionAlreadyOpen = errors.New("agent transport session is already open")
	ErrSessionNotOpen     = errors.New("agent transport session is not open")
	ErrModuleSetSealed    = errors.New("agent module set is sealed for the session handshake")
	ErrStaleSession       = errors.New("stale agent transport session")
)

// ModuleDescriptor is transport-neutral capability metadata.
type ModuleDescriptor struct {
	Name             string
	Capabilities     []string
	MinSchemaVersion string
	MaxSchemaVersion string
	MessageSchemas   []string
}

// ReceiveAndRoute receives one Gateway message from the Manager-owned
// connection and routes it by the closed module schema advertisement.
func (manager *Manager) ReceiveAndRoute(ctx context.Context) error {
	manager.mu.Lock()
	connection := manager.connection
	manager.mu.Unlock()
	if connection == nil {
		return ErrSessionNotOpen
	}
	envelope, err := connection.Receive(ctx)
	if err != nil {
		return err
	}
	if err := manager.validateCurrent(envelope); err != nil {
		return err
	}
	manager.mu.Lock()
	var selected Module
	for _, module := range manager.modules {
		for _, schema := range module.Descriptor().MessageSchemas {
			if schema != envelope.SchemaVersion {
				continue
			}
			if selected != nil {
				manager.mu.Unlock()
				return fmt.Errorf("multiple Agent modules advertise schema %q", schema)
			}
			selected = module
		}
	}
	manager.mu.Unlock()
	if selected == nil {
		return fmt.Errorf("no Agent module advertises schema %q", envelope.SchemaVersion)
	}
	return selected.Handle(ctx, envelope)
}

// FlushOne sends one queued application message over the current connection.
// A send error requeues the stable envelope because it cannot prove delivery.
func (manager *Manager) FlushOne(ctx context.Context) (bool, error) {
	manager.mu.Lock()
	connection := manager.connection
	manager.mu.Unlock()
	if connection == nil {
		return false, ErrSessionNotOpen
	}
	envelope, found := manager.queue.Dequeue()
	if !found {
		return false, nil
	}
	if err := manager.validateCurrent(envelope); err != nil {
		_ = manager.queue.Enqueue(envelope)
		return false, err
	}
	if err := connection.Send(ctx, envelope); err != nil {
		if queueErr := manager.queue.Enqueue(envelope); queueErr != nil {
			return false, errors.Join(err, queueErr)
		}
		return false, err
	}
	return true, nil
}

// Module handles typed messages and never receives a transport connection.
type Module interface {
	Descriptor() ModuleDescriptor
	Handle(context.Context, Envelope) error
}

// Handshake binds one Host identity and proposed generation to a connection attempt.
type Handshake struct {
	HostIdentity              string
	SessionGeneration         uint64
	ProtocolVersion           string
	Capabilities              []string
	SessionAttemptID          string
	ConnectionInstanceID      string
	AgentArtifactDigest       string
	CredentialBindingRevision int64
}

// TransportConnection is owned exclusively by Manager.
type TransportConnection interface {
	Send(context.Context, Envelope) error
	Receive(context.Context) (Envelope, error)
	Close() error
}

// ReceiptConnection is implemented by multiplexed transports that expose
// durable Gateway message receipts separately from inbound module envelopes.
type ReceiptConnection interface {
	TransportConnection
	ReceiveReceipt(context.Context) (Receipt, error)
}

// TransportAdapter hides HTTP/2/gRPC implementation details from modules.
type TransportAdapter interface {
	Open(context.Context, Handshake) (TransportConnection, error)
}

// Manager owns exactly one current transport connection for one Host identity.
type Manager struct {
	mu         sync.Mutex
	handshake  Handshake
	adapter    TransportAdapter
	queue      *PriorityQueue
	authority  AuthorityView
	modules    map[string]Module
	connection TransportConnection
	opening    bool
	sealed     bool
}

// NewManager creates a transport-neutral Session Manager.
func NewManager(handshake Handshake, adapter TransportAdapter, queue *PriorityQueue) (*Manager, error) {
	authority, err := NewMemoryAuthorityView(handshake.HostIdentity, handshake.SessionGeneration)
	if err != nil {
		return nil, err
	}
	return NewManagerWithAuthority(handshake, adapter, queue, authority)
}

// NewManagerWithAuthority injects the authenticated current-generation view.
func NewManagerWithAuthority(handshake Handshake, adapter TransportAdapter, queue *PriorityQueue, authority AuthorityView) (*Manager, error) {
	if handshake.HostIdentity == "" || handshake.SessionGeneration == 0 || handshake.ProtocolVersion == "" {
		return nil, errors.New("complete Agent handshake identity is required")
	}
	if adapter == nil || queue == nil || authority == nil {
		return nil, errors.New("transport adapter, priority queue, and authority view are required")
	}
	return &Manager{handshake: handshake, adapter: adapter, queue: queue, authority: authority, modules: make(map[string]Module)}, nil
}

// RegisterModule changes capability routing but never opens another connection.
func (manager *Manager) RegisterModule(module Module) error {
	if module == nil {
		return errors.New("Agent module is required")
	}
	descriptor := module.Descriptor()
	if descriptor.Name == "" || len(descriptor.Capabilities) == 0 {
		return errors.New("Agent module descriptor name and capabilities are required")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.sealed {
		return ErrModuleSetSealed
	}
	if _, exists := manager.modules[descriptor.Name]; exists {
		return fmt.Errorf("Agent module %q is already registered", descriptor.Name)
	}
	for _, schema := range descriptor.MessageSchemas {
		if schema == "" {
			return errors.New("Agent module message schema must not be empty")
		}
		for _, existing := range manager.modules {
			for _, existingSchema := range existing.Descriptor().MessageSchemas {
				if schema == existingSchema {
					return fmt.Errorf("Agent message schema %q is already registered", schema)
				}
			}
		}
	}
	manager.modules[descriptor.Name] = module
	manager.handshake.Capabilities = append(manager.handshake.Capabilities, descriptor.Capabilities...)
	return nil
}

// Open establishes the Manager's single current transport connection.
func (manager *Manager) Open(ctx context.Context) error {
	manager.mu.Lock()
	if manager.connection != nil || manager.opening {
		manager.mu.Unlock()
		return ErrSessionAlreadyOpen
	}
	manager.opening = true
	manager.sealed = true
	handshake := manager.handshake
	handshake.Capabilities = append([]string(nil), manager.handshake.Capabilities...)
	manager.mu.Unlock()
	if err := manager.validateAuthority(handshake.HostIdentity, handshake.SessionGeneration); err != nil {
		manager.mu.Lock()
		manager.opening = false
		manager.mu.Unlock()
		return err
	}

	connection, err := manager.adapter.Open(ctx, handshake)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.opening = false
	if err != nil {
		return fmt.Errorf("open Agent transport session: %w", err)
	}
	manager.connection = connection
	return nil
}

// Publish validates and queues a typed module message. It exposes no socket,
// endpoint, certificate, or reconnect primitive to the module.
func (manager *Manager) Publish(envelope Envelope) error {
	if err := manager.validateCurrent(envelope); err != nil {
		return err
	}
	return manager.queue.Enqueue(envelope)
}

// AcceptInbound fences stale-session messages before module routing.
func (manager *Manager) AcceptInbound(ctx context.Context, moduleName string, envelope Envelope) error {
	if err := manager.validateCurrent(envelope); err != nil {
		return err
	}
	manager.mu.Lock()
	module := manager.modules[moduleName]
	manager.mu.Unlock()
	if module == nil {
		return fmt.Errorf("Agent module %q is not registered", moduleName)
	}
	return module.Handle(ctx, envelope)
}

// Close drains ownership of the live connection, not resource authority.
func (manager *Manager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.connection == nil {
		return ErrSessionNotOpen
	}
	err := manager.connection.Close()
	manager.connection = nil
	return err
}

func (manager *Manager) validateCurrent(envelope Envelope) error {
	if err := manager.validateAuthority(envelope.HostIdentity, envelope.SessionGeneration); err != nil {
		return err
	}
	if err := envelope.Validate(manager.queue.limits.MaxMessageBytes); err != nil {
		return err
	}
	return nil
}

func (manager *Manager) validateAuthority(host string, generation uint64) error {
	current := manager.authority.Snapshot()
	if current.State != AuthorityCurrent {
		return ErrSessionFenced
	}
	if host != current.HostIdentity || generation != current.SessionGeneration {
		return ErrStaleSession
	}
	return nil
}
