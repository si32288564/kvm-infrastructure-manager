package session

import (
	"errors"
	"sync"
)

var ErrSessionFenced = errors.New("agent transport session is fenced")

// AuthorityState is the locally enforced view of Gateway/Registry authority.
type AuthorityState string

const (
	AuthorityCurrent AuthorityState = "CURRENT"
	AuthorityFenced  AuthorityState = "FENCED"
	AuthorityUnknown AuthorityState = "UNKNOWN"
)

// AuthoritySnapshot is an immutable read of current session authority.
type AuthoritySnapshot struct {
	HostIdentity      string
	SessionGeneration uint64
	State             AuthorityState
}

// AuthorityView is revalidated by authenticated Control messages or a
// Gateway Session Registry reader. Transport liveness is not authority.
type AuthorityView interface {
	Snapshot() AuthoritySnapshot
}

// MemoryAuthorityView is the Agent-local enforcement projection. Updates must
// originate from the authenticated control/revalidation path, never a module.
type MemoryAuthorityView struct {
	mu       sync.RWMutex
	snapshot AuthoritySnapshot
}

// NewMemoryAuthorityView creates a current local projection.
func NewMemoryAuthorityView(host string, generation uint64) (*MemoryAuthorityView, error) {
	if host == "" || generation == 0 {
		return nil, errors.New("complete session authority identity is required")
	}
	return &MemoryAuthorityView{snapshot: AuthoritySnapshot{
		HostIdentity:      host,
		SessionGeneration: generation,
		State:             AuthorityCurrent,
	}}, nil
}

// Snapshot returns the current local enforcement projection.
func (view *MemoryAuthorityView) Snapshot() AuthoritySnapshot {
	view.mu.RLock()
	defer view.mu.RUnlock()
	return view.snapshot
}

// Apply replaces the projection monotonically. Same-generation fencing is
// allowed; same-generation rearming and generation rollback are rejected.
func (view *MemoryAuthorityView) Apply(next AuthoritySnapshot) error {
	if next.HostIdentity == "" || next.SessionGeneration == 0 {
		return errors.New("complete session authority snapshot is required")
	}
	if next.State != AuthorityCurrent && next.State != AuthorityFenced && next.State != AuthorityUnknown {
		return errors.New("unknown session authority state")
	}
	view.mu.Lock()
	defer view.mu.Unlock()
	current := view.snapshot
	if next.HostIdentity != current.HostIdentity {
		return errors.New("session authority Host identity cannot change")
	}
	if next.SessionGeneration < current.SessionGeneration {
		return errors.New("session authority generation cannot decrease")
	}
	if next.SessionGeneration == current.SessionGeneration && current.State != AuthorityCurrent && next.State == AuthorityCurrent {
		return errors.New("same-generation session authority cannot rearm")
	}
	view.snapshot = next
	return nil
}
