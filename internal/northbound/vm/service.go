// Package vm defines the public logical VM aggregate contract. Physical
// Placement, Host, Port binding, Volume backend and mobility incarnations are
// deliberately absent from this package.
package vm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Reference struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

type Desired struct {
	ProjectID                  string      `json:"projectId"`
	Name                       string      `json:"name"`
	FlavorID                   string      `json:"flavorId"`
	FlavorRevision             uint64      `json:"flavorRevision"`
	ImageID                    string      `json:"imageId"`
	ImageRevision              uint64      `json:"imageRevision"`
	AvailabilityPolicyID       string      `json:"availabilityPolicyId"`
	AvailabilityPolicyRevision uint64      `json:"availabilityPolicyRevision"`
	PlacementScopeID           string      `json:"placementScopeId"`
	PlacementScopeGeneration   uint64      `json:"placementScopeGeneration"`
	RootVolume                 Reference   `json:"rootVolume"`
	Ports                      []Reference `json:"ports"`
	DataVolumes                []Reference `json:"dataVolumes"`
	DesiredPowerState          string      `json:"desiredPowerState"`
	DeleteProtection           bool        `json:"deleteProtection"`
}

type Resource struct {
	ID string `json:"id"`
	Desired
	Revision                uint64    `json:"revision"`
	RuntimeIntentGeneration uint64    `json:"runtimeIntentGeneration"`
	LifecycleState          string    `json:"lifecycleState"`
	ConvergenceState        string    `json:"convergenceState"`
	OperationID             string    `json:"operationId,omitempty"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

type Operation struct {
	ID                 string     `json:"id"`
	Type               string     `json:"type"`
	TargetResourceType string     `json:"targetResourceType"`
	TargetResourceID   string     `json:"targetResourceId"`
	TargetRevision     uint64     `json:"targetRevision"`
	AcceptedAt         time.Time  `json:"acceptedAt"`
	Phase              string     `json:"phase"`
	TerminalState      *string    `json:"terminalState,omitempty"`
	ErrorCode          *string    `json:"errorCode,omitempty"`
	Retryable          bool       `json:"retryable"`
	Cancellable        bool       `json:"cancellable"`
	CompletedAt        *time.Time `json:"completedAt,omitempty"`
}

type CreateRequest struct {
	Desired                                  Desired
	IdempotencyKey, RequestID, CanonicalPath string
}
type Patch struct {
	Name              *string
	DeleteProtection  *bool
	DesiredPowerState *string
}
type ListRequest struct {
	ProjectID, AfterID string
	Limit              int
}
type Page struct {
	Items     []Resource
	NextAfter string
}

type Store interface {
	Create(context.Context, resource.Principal, CreateRequest, string, string, string) (Resource, bool, error)
	Get(context.Context, resource.Principal, string, string) (Resource, error)
	List(context.Context, resource.Principal, ListRequest, string) (Page, error)
	Patch(context.Context, resource.Principal, string, uint64, Patch, string, string, string) (Resource, error)
	Delete(context.Context, resource.Principal, string, uint64, string, string) (Operation, error)
	GetOperation(context.Context, resource.Principal, string, string) (Operation, error)
}

type Service struct{ Store Store }

func (s Service) Create(ctx context.Context, p resource.Principal, r CreateRequest) (Resource, bool, error) {
	if s.Store == nil {
		return Resource{}, false, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, false, resource.ErrUnauthenticated
	}
	r.Desired.Name = strings.TrimSpace(r.Desired.Name)
	canonicalize(&r.Desired)
	if r.CanonicalPath != "/api/v1/vms" || r.RequestID == "" || r.IdempotencyKey == "" || len(r.IdempotencyKey) > 255 || !validDesired(r.Desired) {
		return Resource{}, false, resource.ErrValidation
	}
	id, err := resource.NewID()
	if err != nil {
		return Resource{}, false, err
	}
	op, err := resource.NewID()
	if err != nil {
		return Resource{}, false, err
	}
	digest, err := DesiredDigest(r.Desired)
	if err != nil {
		return Resource{}, false, err
	}
	return s.Store.Create(ctx, p, r, id, op, digest)
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
	if requestID == "" || r.Limit < 1 || r.Limit > 100 || (r.AfterID != "" && !idPattern.MatchString(r.AfterID)) || (r.ProjectID != "" && !idPattern.MatchString(r.ProjectID)) {
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
	if !idPattern.MatchString(id) || revision == 0 || requestID == "" {
		return Resource{}, resource.ErrValidation
	}
	metadata := patch.Name != nil || patch.DeleteProtection != nil
	power := patch.DesiredPowerState != nil
	if metadata == power {
		return Resource{}, resource.ErrValidation
	}
	if patch.Name != nil {
		v := strings.TrimSpace(*patch.Name)
		if v == "" || len(v) > 255 {
			return Resource{}, resource.ErrValidation
		}
		patch.Name = &v
	}
	if power && *patch.DesiredPowerState != "RUNNING" && *patch.DesiredPowerState != "SHUTOFF" {
		return Resource{}, resource.ErrValidation
	}
	evidence, err := resource.NewID()
	if err != nil {
		return Resource{}, err
	}
	op := ""
	if power {
		op, err = resource.NewID()
		if err != nil {
			return Resource{}, err
		}
	}
	return s.Store.Patch(ctx, p, id, revision, patch, requestID, evidence, op)
}

func (s Service) Delete(ctx context.Context, p resource.Principal, id string, revision uint64, requestID string) (Operation, error) {
	if s.Store == nil {
		return Operation{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Operation{}, resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || revision == 0 || requestID == "" {
		return Operation{}, resource.ErrValidation
	}
	op, err := resource.NewID()
	if err != nil {
		return Operation{}, err
	}
	return s.Store.Delete(ctx, p, id, revision, requestID, op)
}

func (s Service) GetOperation(ctx context.Context, p resource.Principal, id, requestID string) (Operation, error) {
	if s.Store == nil {
		return Operation{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Operation{}, resource.ErrUnauthenticated
	}
	if id == "" || requestID == "" {
		return Operation{}, resource.ErrValidation
	}
	return s.Store.GetOperation(ctx, p, id, requestID)
}

func DesiredDigest(d Desired) (string, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func canonicalize(d *Desired) {
	sort.Slice(d.Ports, func(i, j int) bool { return d.Ports[i].ID < d.Ports[j].ID })
	sort.Slice(d.DataVolumes, func(i, j int) bool { return d.DataVolumes[i].ID < d.DataVolumes[j].ID })
}
func validDesired(d Desired) bool {
	if !idPattern.MatchString(d.ProjectID) || d.Name == "" || len(d.Name) > 255 || !idPattern.MatchString(d.FlavorID) || d.FlavorRevision == 0 || !idPattern.MatchString(d.ImageID) || d.ImageRevision == 0 || !idPattern.MatchString(d.AvailabilityPolicyID) || d.AvailabilityPolicyRevision == 0 || d.PlacementScopeID == "" || d.PlacementScopeGeneration == 0 || !idPattern.MatchString(d.RootVolume.ID) || d.RootVolume.Revision == 0 || d.DesiredPowerState != "RUNNING" || len(d.Ports) > 2 || len(d.DataVolumes) > 1 {
		return false
	}
	seen := map[string]bool{d.RootVolume.ID: true}
	for _, r := range append(append([]Reference{}, d.Ports...), d.DataVolumes...) {
		if !idPattern.MatchString(r.ID) || r.Revision == 0 || seen[r.ID] {
			return false
		}
		seen[r.ID] = true
	}
	return true
}
