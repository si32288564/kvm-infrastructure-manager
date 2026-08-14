// Package flavor defines the Northbound Flavor lifecycle. A Flavor revision is
// an immutable logical Placement shape; changing it never retrofits existing
// VM Admissions, which remain bound to their exact historical revision.
package flavor

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
	ProjectID        string    `json:"projectId"`
	Name             string    `json:"name"`
	VCPUs            uint64    `json:"vcpus"`
	MemoryMiB        uint64    `json:"memoryMiB"`
	RootDiskGiB      uint64    `json:"rootDiskGiB"`
	NUMAPolicy       string    `json:"numaPolicy"`
	NUMANodes        *uint32   `json:"numaNodes,omitempty"`
	HugePageSizeKiB  *uint64   `json:"hugePageSizeKiB,omitempty"`
	CPUAllocation    string    `json:"cpuAllocation"`
	CPUPinning       bool      `json:"cpuPinning"`
	DeleteProtection bool      `json:"deleteProtection"`
	Revision         uint64    `json:"revision"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type Desired struct {
	ProjectID        string  `json:"projectId"`
	Name             string  `json:"name"`
	VCPUs            uint64  `json:"vcpus"`
	MemoryMiB        uint64  `json:"memoryMiB"`
	RootDiskGiB      uint64  `json:"rootDiskGiB"`
	NUMAPolicy       string  `json:"numaPolicy"`
	NUMANodes        *uint32 `json:"numaNodes,omitempty"`
	HugePageSizeKiB  *uint64 `json:"hugePageSizeKiB,omitempty"`
	CPUAllocation    string  `json:"cpuAllocation"`
	CPUPinning       bool    `json:"cpuPinning"`
	DeleteProtection bool    `json:"deleteProtection"`
}

type CreateRequest struct {
	Desired
	IdempotencyKey, RequestID, CanonicalPath string
}

type Patch struct {
	Name, NUMAPolicy, CPUAllocation *string
	VCPUs, MemoryMiB, RootDiskGiB   *uint64
	NUMANodes                       **uint32
	HugePageSizeKiB                 **uint64
	CPUPinning, DeleteProtection    *bool
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
	Create(context.Context, resource.Principal, CreateRequest, string, string) (Resource, bool, error)
	Get(context.Context, resource.Principal, string, string) (Resource, error)
	List(context.Context, resource.Principal, ListRequest, string) (Page, error)
	Patch(context.Context, resource.Principal, string, uint64, Patch, string) (Resource, error)
	Delete(context.Context, resource.Principal, string, uint64, string) error
}

type Service struct{ Store Store }

func (s Service) Create(ctx context.Context, principal resource.Principal, request CreateRequest) (Resource, bool, error) {
	request.Name = strings.TrimSpace(request.Name)
	if s.Store == nil {
		return Resource{}, false, resource.ErrServiceUnavailable
	}
	if !principal.Valid() {
		return Resource{}, false, resource.ErrUnauthenticated
	}
	if request.CanonicalPath != "/api/v1/flavors" || request.RequestID == "" || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 || !validDesired(request.Desired) {
		return Resource{}, false, resource.ErrValidation
	}
	id, err := resource.NewID()
	if err != nil {
		return Resource{}, false, err
	}
	digest, err := DesiredDigest(request.Desired)
	if err != nil {
		return Resource{}, false, err
	}
	return s.Store.Create(ctx, principal, request, id, digest)
}

func (s Service) Get(ctx context.Context, principal resource.Principal, id, requestID string) (Resource, error) {
	if s.Store == nil {
		return Resource{}, resource.ErrServiceUnavailable
	}
	if !principal.Valid() {
		return Resource{}, resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || requestID == "" {
		return Resource{}, resource.ErrValidation
	}
	return s.Store.Get(ctx, principal, id, requestID)
}
func (s Service) List(ctx context.Context, principal resource.Principal, request ListRequest, requestID string) (Page, error) {
	if s.Store == nil {
		return Page{}, resource.ErrServiceUnavailable
	}
	if !principal.Valid() {
		return Page{}, resource.ErrUnauthenticated
	}
	if request.RequestIDInvalid(requestID) {
		return Page{}, resource.ErrValidation
	}
	return s.Store.List(ctx, principal, request, requestID)
}
func (r ListRequest) RequestIDInvalid(requestID string) bool {
	return requestID == "" || r.Limit < 1 || r.Limit > 100 || (r.AfterID != "" && !idPattern.MatchString(r.AfterID)) || (r.ProjectID != "" && !idPattern.MatchString(r.ProjectID))
}
func (s Service) Patch(ctx context.Context, principal resource.Principal, id string, revision uint64, patch Patch, requestID string) (Resource, error) {
	if s.Store == nil {
		return Resource{}, resource.ErrServiceUnavailable
	}
	if !principal.Valid() {
		return Resource{}, resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || revision == 0 || requestID == "" || patch.empty() {
		return Resource{}, resource.ErrValidation
	}
	if patch.Name != nil {
		v := strings.TrimSpace(*patch.Name)
		patch.Name = &v
	}
	return s.Store.Patch(ctx, principal, id, revision, patch, requestID)
}
func (s Service) Delete(ctx context.Context, principal resource.Principal, id string, revision uint64, requestID string) error {
	if s.Store == nil {
		return resource.ErrServiceUnavailable
	}
	if !principal.Valid() {
		return resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || revision == 0 || requestID == "" {
		return resource.ErrValidation
	}
	return s.Store.Delete(ctx, principal, id, revision, requestID)
}

func (p Patch) empty() bool {
	return p.Name == nil && p.NUMAPolicy == nil && p.CPUAllocation == nil && p.VCPUs == nil && p.MemoryMiB == nil && p.RootDiskGiB == nil && p.NUMANodes == nil && p.HugePageSizeKiB == nil && p.CPUPinning == nil && p.DeleteProtection == nil
}
func validDesired(d Desired) bool {
	return idPattern.MatchString(d.ProjectID) && d.Name != "" && len(d.Name) <= 255 && d.VCPUs > 0 && d.MemoryMiB > 0 && d.RootDiskGiB > 0 && (d.NUMAPolicy == "NONE" && d.NUMANodes == nil || d.NUMAPolicy == "REQUIRED" && d.NUMANodes != nil && *d.NUMANodes > 0) && (d.CPUAllocation == "SHARED" || d.CPUAllocation == "DEDICATED") && (!d.CPUPinning || d.CPUAllocation == "DEDICATED") && (d.HugePageSizeKiB == nil || *d.HugePageSizeKiB > 0)
}
func Apply(base Resource, p Patch) (Desired, error) {
	d := Desired{ProjectID: base.ProjectID, Name: base.Name, VCPUs: base.VCPUs, MemoryMiB: base.MemoryMiB, RootDiskGiB: base.RootDiskGiB, NUMAPolicy: base.NUMAPolicy, NUMANodes: base.NUMANodes, HugePageSizeKiB: base.HugePageSizeKiB, CPUAllocation: base.CPUAllocation, CPUPinning: base.CPUPinning, DeleteProtection: base.DeleteProtection}
	if p.Name != nil {
		d.Name = *p.Name
	}
	if p.VCPUs != nil {
		d.VCPUs = *p.VCPUs
	}
	if p.MemoryMiB != nil {
		d.MemoryMiB = *p.MemoryMiB
	}
	if p.RootDiskGiB != nil {
		d.RootDiskGiB = *p.RootDiskGiB
	}
	if p.NUMAPolicy != nil {
		d.NUMAPolicy = *p.NUMAPolicy
	}
	if p.NUMANodes != nil {
		d.NUMANodes = *p.NUMANodes
	}
	if p.HugePageSizeKiB != nil {
		d.HugePageSizeKiB = *p.HugePageSizeKiB
	}
	if p.CPUAllocation != nil {
		d.CPUAllocation = *p.CPUAllocation
	}
	if p.CPUPinning != nil {
		d.CPUPinning = *p.CPUPinning
	}
	if p.DeleteProtection != nil {
		d.DeleteProtection = *p.DeleteProtection
	}
	if !validDesired(d) {
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
