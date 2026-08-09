package gateway

import (
	"context"
	"errors"
	"sync"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

var (
	ErrNoCurrentAgentSession = errors.New("no current Agent transport session")
	ErrStaleOutboundSession  = errors.New("outbound Agent session generation is stale")
)

type OutboundSink interface {
	Send(context.Context, session.Envelope) error
}

type registeredSink struct {
	generation   uint64
	connectionID string
	sink         OutboundSink
}

// OutboundRegistry is a liveness routing projection, not session authority.
// The PostgreSQL Grant remains authoritative; registration only exposes the
// matching live stream to a dispatcher.
type OutboundRegistry struct {
	mu       sync.RWMutex
	sessions map[string]registeredSink
}

func NewOutboundRegistry() *OutboundRegistry {
	return &OutboundRegistry{sessions: make(map[string]registeredSink)}
}

func (registry *OutboundRegistry) Register(hostID string, generation uint64, connectionID string, sink OutboundSink) (func(), error) {
	if hostID == "" || generation == 0 || connectionID == "" || sink == nil {
		return nil, errors.New("complete outbound Agent session identity is required")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if current, found := registry.sessions[hostID]; found && current.generation >= generation {
		return nil, ErrStaleOutboundSession
	}
	registry.sessions[hostID] = registeredSink{generation: generation, connectionID: connectionID, sink: sink}
	return func() {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		current, found := registry.sessions[hostID]
		if found && current.generation == generation && current.connectionID == connectionID {
			delete(registry.sessions, hostID)
		}
	}, nil
}

func (registry *OutboundRegistry) Send(ctx context.Context, hostID string, generation uint64, envelope session.Envelope) error {
	registry.mu.RLock()
	current, found := registry.sessions[hostID]
	registry.mu.RUnlock()
	if !found {
		return ErrNoCurrentAgentSession
	}
	if current.generation != generation || envelope.HostIdentity != hostID || envelope.SessionGeneration != generation {
		return ErrStaleOutboundSession
	}
	return current.sink.Send(ctx, envelope)
}
