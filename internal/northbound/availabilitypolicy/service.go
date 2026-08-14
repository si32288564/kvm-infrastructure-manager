// Package availabilitypolicy defines the SYSTEM-scoped Northbound lifecycle
// for closed non-automatic Availability Policy profiles. Runtime Failure,
// Fencing, Recovery, budget claims, and destination state are never fields.
package availabilitypolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Resource struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	AvailabilityMode string    `json:"availabilityMode"`
	MaxAttempts      int       `json:"maxAttempts"`
	DeleteProtection bool      `json:"deleteProtection"`
	Revision         uint64    `json:"revision"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}
type Desired struct {
	Name             string `json:"name"`
	AvailabilityMode string `json:"availabilityMode"`
	MaxAttempts      int    `json:"maxAttempts"`
	DeleteProtection bool   `json:"deleteProtection"`
}
type CreateRequest struct {
	Desired
	IdempotencyKey, RequestID, CanonicalPath string
}
type Patch struct {
	Name, AvailabilityMode *string
	MaxAttempts            *int
	DeleteProtection       *bool
}
type ListRequest struct {
	AfterID string
	Limit   int
}
type Page struct {
	Items     []Resource
	NextAfter string
}

type Store interface {
	Create(context.Context, resource.Principal, CreateRequest, string, string) (Resource, bool, error)
	Get(context.Context, resource.Principal, string, string) (Resource, error)
	List(context.Context, resource.Principal, ListRequest, string) (Page, error)
	Patch(context.Context, resource.Principal, string, uint64, Patch, string) (Resource, error)
	Delete(context.Context, resource.Principal, string, uint64, string) error
}
type Service struct{ Store Store }

func (s Service) Create(ctx context.Context, p resource.Principal, r CreateRequest) (Resource, bool, error) {
	r.Name = strings.TrimSpace(r.Name)
	if s.Store == nil {
		return Resource{}, false, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, false, resource.ErrUnauthenticated
	}
	if r.CanonicalPath != "/api/v1/availability-policies" || r.RequestID == "" || r.IdempotencyKey == "" || len(r.IdempotencyKey) > 255 || !valid(r.Desired) {
		return Resource{}, false, resource.ErrValidation
	}
	id, err := resource.NewID()
	if err != nil {
		return Resource{}, false, err
	}
	digest, err := DesiredDigest(r.Desired)
	if err != nil {
		return Resource{}, false, err
	}
	return s.Store.Create(ctx, p, r, id, digest)
}
func (s Service) Get(ctx context.Context, p resource.Principal, id, requestID string) (Resource, error) {
	if s.Store == nil {
		return Resource{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || requestID == "" {
		return Resource{}, resource.ErrValidation
	}
	return s.Store.Get(ctx, p, id, requestID)
}
func (s Service) List(ctx context.Context, p resource.Principal, r ListRequest, requestID string) (Page, error) {
	if s.Store == nil {
		return Page{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Page{}, resource.ErrUnauthenticated
	}
	if requestID == "" || r.Limit < 1 || r.Limit > 100 || (r.AfterID != "" && !idPattern.MatchString(r.AfterID)) {
		return Page{}, resource.ErrValidation
	}
	return s.Store.List(ctx, p, r, requestID)
}
func (s Service) Patch(ctx context.Context, p resource.Principal, id string, revision uint64, patch Patch, requestID string) (Resource, error) {
	if s.Store == nil {
		return Resource{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || revision == 0 || requestID == "" || patch.empty() {
		return Resource{}, resource.ErrValidation
	}
	if patch.Name != nil {
		v := strings.TrimSpace(*patch.Name)
		patch.Name = &v
	}
	return s.Store.Patch(ctx, p, id, revision, patch, requestID)
}
func (s Service) Delete(ctx context.Context, p resource.Principal, id string, revision uint64, requestID string) error {
	if s.Store == nil {
		return resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || revision == 0 || requestID == "" {
		return resource.ErrValidation
	}
	return s.Store.Delete(ctx, p, id, revision, requestID)
}
func (p Patch) empty() bool {
	return p.Name == nil && p.AvailabilityMode == nil && p.MaxAttempts == nil && p.DeleteProtection == nil
}
func valid(d Desired) bool {
	return d.Name != "" && len(d.Name) <= 255 && (d.AvailabilityMode == "MANUAL" || d.AvailabilityMode == "WORKLOAD_MANAGED") && d.MaxAttempts > 0 && d.MaxAttempts <= 100
}
func Apply(base Resource, p Patch) (Desired, error) {
	d := Desired{Name: base.Name, AvailabilityMode: base.AvailabilityMode, MaxAttempts: base.MaxAttempts, DeleteProtection: base.DeleteProtection}
	if p.Name != nil {
		d.Name = *p.Name
	}
	if p.AvailabilityMode != nil {
		d.AvailabilityMode = *p.AvailabilityMode
	}
	if p.MaxAttempts != nil {
		d.MaxAttempts = *p.MaxAttempts
	}
	if p.DeleteProtection != nil {
		d.DeleteProtection = *p.DeleteProtection
	}
	if !valid(d) {
		return Desired{}, resource.ErrValidation
	}
	return d, nil
}
func DesiredDigest(d Desired) (string, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
