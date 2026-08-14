// Package project defines the Northbound Project resource contract. It is
// deliberately independent of HTTP and PostgreSQL representations.
package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

var (
	ErrValidation          = resource.ErrValidation
	ErrUnauthenticated     = resource.ErrUnauthenticated
	ErrForbidden           = resource.ErrForbidden
	ErrNotFound            = resource.ErrNotFound
	ErrConflict            = resource.ErrConflict
	ErrStaleRevision       = resource.ErrStaleRevision
	ErrIdempotencyConflict = resource.ErrIdempotencyConflict
	ErrDependencyConflict  = resource.ErrDependencyConflict
	ErrDeleteProtected     = resource.ErrDeleteProtected
	ErrServiceUnavailable  = resource.ErrServiceUnavailable
)

var projectIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Principal = resource.Principal

type Resource struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	DeleteProtection bool      `json:"deleteProtection"`
	Revision         uint64    `json:"revision"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type CreateRequest struct {
	Name             string `json:"name"`
	DeleteProtection bool   `json:"deleteProtection"`
	IdempotencyKey   string `json:"-"`
	RequestID        string `json:"-"`
	CanonicalPath    string `json:"-"`
}

type Patch struct {
	NamePresent             bool
	Name                    string
	DeleteProtectionPresent bool
	DeleteProtection        bool
}

type ListRequest struct {
	AfterID string
	Limit   int
}
type Page struct {
	Items     []Resource
	NextAfter string
}

type AuditEvent = resource.AuditEvent

type Store interface {
	Create(context.Context, Principal, CreateRequest, string, string) (Resource, bool, error)
	Get(context.Context, Principal, string, string) (Resource, error)
	List(context.Context, Principal, ListRequest, string) (Page, error)
	Patch(context.Context, Principal, string, uint64, Patch, string) (Resource, error)
	Delete(context.Context, Principal, string, uint64, string) error
	RecordAudit(context.Context, AuditEvent) error
	Ready(context.Context) error
}

type Service struct{ Store Store }

func (s Service) Create(ctx context.Context, principal Principal, request CreateRequest) (Resource, bool, error) {
	if s.Store == nil {
		return Resource{}, false, ErrServiceUnavailable
	}
	request.Name = strings.TrimSpace(request.Name)
	if !principal.Valid() {
		return Resource{}, false, ErrUnauthenticated
	}
	if request.Name == "" || len(request.Name) > 255 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 || request.RequestID == "" || request.CanonicalPath != "/api/v1/projects" {
		return Resource{}, false, ErrValidation
	}
	id, err := resource.NewID()
	if err != nil {
		return Resource{}, false, fmt.Errorf("generate Project ID: %w", err)
	}
	digest, err := DesiredDigest(request.Name, request.DeleteProtection)
	if err != nil {
		return Resource{}, false, err
	}
	return s.Store.Create(ctx, principal, request, id, digest)
}

func (s Service) Get(ctx context.Context, principal Principal, id, requestID string) (Resource, error) {
	if !principal.Valid() {
		return Resource{}, ErrUnauthenticated
	}
	if s.Store == nil {
		return Resource{}, ErrServiceUnavailable
	}
	if !projectIDPattern.MatchString(id) || requestID == "" {
		return Resource{}, ErrValidation
	}
	return s.Store.Get(ctx, principal, id, requestID)
}

func (s Service) List(ctx context.Context, principal Principal, request ListRequest, requestID string) (Page, error) {
	if !principal.Valid() {
		return Page{}, ErrUnauthenticated
	}
	if s.Store == nil {
		return Page{}, ErrServiceUnavailable
	}
	if requestID == "" || request.Limit < 1 || request.Limit > 100 || (request.AfterID != "" && !projectIDPattern.MatchString(request.AfterID)) {
		return Page{}, ErrValidation
	}
	return s.Store.List(ctx, principal, request, requestID)
}

func (s Service) Patch(ctx context.Context, principal Principal, id string, revision uint64, patch Patch, requestID string) (Resource, error) {
	if !principal.Valid() {
		return Resource{}, ErrUnauthenticated
	}
	if s.Store == nil {
		return Resource{}, ErrServiceUnavailable
	}
	if !projectIDPattern.MatchString(id) || revision == 0 || requestID == "" || (!patch.NamePresent && !patch.DeleteProtectionPresent) {
		return Resource{}, ErrValidation
	}
	if patch.NamePresent {
		patch.Name = strings.TrimSpace(patch.Name)
		if patch.Name == "" || len(patch.Name) > 255 {
			return Resource{}, ErrValidation
		}
	}
	return s.Store.Patch(ctx, principal, id, revision, patch, requestID)
}

func (s Service) Delete(ctx context.Context, principal Principal, id string, revision uint64, requestID string) error {
	if !principal.Valid() {
		return ErrUnauthenticated
	}
	if s.Store == nil {
		return ErrServiceUnavailable
	}
	if !projectIDPattern.MatchString(id) || revision == 0 || requestID == "" {
		return ErrValidation
	}
	return s.Store.Delete(ctx, principal, id, revision, requestID)
}

func DesiredDigest(name string, protection bool) (string, error) {
	raw, err := json.Marshal(struct {
		Name             string `json:"name"`
		DeleteProtection bool   `json:"deleteProtection"`
	}{name, protection})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

var NewID = resource.NewID
