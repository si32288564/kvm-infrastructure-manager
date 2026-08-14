// Package resource defines transport-independent contracts shared by
// Northbound persistent resources. Lifecycle logic remains resource-specific.
package resource

import (
	"crypto/rand"
	"errors"
	"fmt"
)

var (
	ErrValidation          = errors.New("resource validation failed")
	ErrUnauthenticated     = errors.New("Northbound principal is unauthenticated")
	ErrForbidden           = errors.New("resource action is forbidden")
	ErrNotFound            = errors.New("resource was not found")
	ErrConflict            = errors.New("resource authority conflict")
	ErrStaleRevision       = errors.New("resource revision is stale")
	ErrIdempotencyConflict = errors.New("resource idempotency conflict")
	ErrDependencyConflict  = errors.New("resource has dependent resources")
	ErrDeleteProtected     = errors.New("resource deletion is protected")
	ErrServiceUnavailable  = errors.New("resource authority is unavailable")
)

type Principal struct{ Issuer, Subject, Type string }

func (p Principal) Valid() bool {
	return p.Issuer != "" && p.Subject != "" && (p.Type == "HUMAN" || p.Type == "AUTOMATION")
}

type AuditEvent struct {
	AuditID, RequestID, Action, ResourceType, ResourceID, ScopeType, ScopeID string
	Principal                                                                Principal
	ResourceRevision                                                         uint64
	Result, ReasonCode, IdempotencyDigest                                    string
}

func NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
