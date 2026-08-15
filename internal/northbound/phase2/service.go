// Package phase2 defines the public logical Network, Subnet, Port, and Volume
// contract. Physical realization identities deliberately remain internal.
package phase2

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

type Kind string

const (
	Network Kind = "NETWORK"
	Subnet  Kind = "SUBNET"
	Port    Kind = "PORT"
	Volume  Kind = "VOLUME"
)

func (k Kind) Plural() string {
	switch k {
	case Network:
		return "networks"
	case Subnet:
		return "subnets"
	case Port:
		return "ports"
	case Volume:
		return "volumes"
	}
	return ""
}

type Desired struct {
	ProjectID        string `json:"projectId"`
	Name             string `json:"name"`
	DeleteProtection bool   `json:"deleteProtection"`
	// Network
	Profile            string `json:"profile,omitempty"`
	MTU                uint32 `json:"mtu,omitempty"`
	SegmentPolicy      string `json:"segmentPolicy,omitempty"`
	RequestedSegmentID uint32 `json:"requestedSegmentId,omitempty"`
	// Subnet
	NetworkID         string   `json:"networkId,omitempty"`
	IPFamily          string   `json:"ipFamily,omitempty"`
	CIDR              string   `json:"cidr,omitempty"`
	GatewayPolicy     string   `json:"gatewayPolicy,omitempty"`
	GatewayAddress    string   `json:"gatewayAddress,omitempty"`
	AllocationPolicy  string   `json:"allocationPolicy,omitempty"`
	AllocationStart   string   `json:"allocationStart,omitempty"`
	AllocationEnd     string   `json:"allocationEnd,omitempty"`
	ReservedAddresses []string `json:"reservedAddresses,omitempty"`
	DHCPEnabled       bool     `json:"dhcpEnabled,omitempty"`
	DNSServers        []string `json:"dnsServers,omitempty"`
	// Port
	MACPolicy        string `json:"macPolicy,omitempty"`
	RequestedMAC     string `json:"requestedMac,omitempty"`
	SubnetID         string `json:"subnetId,omitempty"`
	IPAllocationMode string `json:"ipAllocationMode,omitempty"`
	RequestedIP      string `json:"requestedIp,omitempty"`
	AttachmentPolicy string `json:"attachmentPolicy,omitempty"`
	DatapathProfile  string `json:"datapathProfile,omitempty"`
	// Volume
	SizeBytes            uint64 `json:"sizeBytes,omitempty"`
	StorageClassID       string `json:"storageClassId,omitempty"`
	StorageClassRevision uint64 `json:"storageClassRevision,omitempty"`
	Bootable             bool   `json:"bootable,omitempty"`
	SourceType           string `json:"sourceType,omitempty"`
	SourceImageID        string `json:"sourceImageId,omitempty"`
	SourceImageRevision  uint64 `json:"sourceImageRevision,omitempty"`
	SourceArtifactDigest string `json:"sourceArtifactDigest,omitempty"`
}

type Resource struct {
	ID string `json:"id"`
	Desired
	Revision         uint64    `json:"revision"`
	LifecycleState   string    `json:"lifecycleState"`
	RealizationState string    `json:"realizationState"`
	OperationID      string    `json:"operationId,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type CreateRequest struct {
	Desired                                  Desired
	IdempotencyKey, RequestID, CanonicalPath string
}
type Patch struct {
	Name             *string
	DeleteProtection *bool
	MTU              *uint32
}
type ListRequest struct {
	ProjectID, AfterID string
	Limit              int
}
type Page struct {
	Items     []Resource
	NextAfter string
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

type Store interface {
	Create(context.Context, resource.Principal, Kind, CreateRequest, string, string) (Resource, bool, error)
	Get(context.Context, resource.Principal, Kind, string, string) (Resource, error)
	List(context.Context, resource.Principal, Kind, ListRequest, string) (Page, error)
	Patch(context.Context, resource.Principal, Kind, string, uint64, Patch, string) (Resource, error)
	Delete(context.Context, resource.Principal, Kind, string, uint64, string) (Operation, error)
	GetOperation(context.Context, resource.Principal, string, string) (Operation, error)
}

type Service struct{ Store Store }

func (s Service) Create(ctx context.Context, p resource.Principal, k Kind, r CreateRequest) (Resource, bool, error) {
	r.Desired.Name = strings.TrimSpace(r.Desired.Name)
	if s.Store == nil {
		return Resource{}, false, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, false, resource.ErrUnauthenticated
	}
	if !validKind(k) || r.CanonicalPath != "/api/v1/"+k.Plural() || r.RequestID == "" || r.IdempotencyKey == "" || len(r.IdempotencyKey) > 255 || !validDesired(k, r.Desired) {
		return Resource{}, false, resource.ErrValidation
	}
	id, err := resource.NewID()
	if err != nil {
		return Resource{}, false, err
	}
	digest, err := DesiredDigest(k, r.Desired)
	if err != nil {
		return Resource{}, false, err
	}
	return s.Store.Create(ctx, p, k, r, id, digest)
}

func (s Service) Get(ctx context.Context, p resource.Principal, k Kind, id, requestID string) (Resource, error) {
	if s.Store == nil {
		return Resource{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, resource.ErrUnauthenticated
	}
	if !validKind(k) || !idPattern.MatchString(id) || requestID == "" {
		return Resource{}, resource.ErrValidation
	}
	return s.Store.Get(ctx, p, k, id, requestID)
}
func (s Service) List(ctx context.Context, p resource.Principal, k Kind, r ListRequest, requestID string) (Page, error) {
	if s.Store == nil {
		return Page{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Page{}, resource.ErrUnauthenticated
	}
	if !validKind(k) || requestID == "" || r.Limit < 1 || r.Limit > 100 || (r.AfterID != "" && !idPattern.MatchString(r.AfterID)) || (r.ProjectID != "" && !idPattern.MatchString(r.ProjectID)) {
		return Page{}, resource.ErrValidation
	}
	return s.Store.List(ctx, p, k, r, requestID)
}
func (s Service) Patch(ctx context.Context, p resource.Principal, k Kind, id string, revision uint64, patch Patch, requestID string) (Resource, error) {
	if s.Store == nil {
		return Resource{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, resource.ErrUnauthenticated
	}
	if !validKind(k) || !idPattern.MatchString(id) || revision == 0 || requestID == "" || (patch.Name == nil && patch.DeleteProtection == nil && patch.MTU == nil) || (k != Network && patch.MTU != nil) {
		return Resource{}, resource.ErrValidation
	}
	if patch.Name != nil {
		v := strings.TrimSpace(*patch.Name)
		patch.Name = &v
		if v == "" || len(v) > 255 {
			return Resource{}, resource.ErrValidation
		}
	}
	return s.Store.Patch(ctx, p, k, id, revision, patch, requestID)
}
func (s Service) Delete(ctx context.Context, p resource.Principal, k Kind, id string, revision uint64, requestID string) (Operation, error) {
	if s.Store == nil {
		return Operation{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Operation{}, resource.ErrUnauthenticated
	}
	if !validKind(k) || !idPattern.MatchString(id) || revision == 0 || requestID == "" {
		return Operation{}, resource.ErrValidation
	}
	return s.Store.Delete(ctx, p, k, id, revision, requestID)
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
func DesiredDigest(k Kind, d Desired) (string, error) {
	raw, err := json.Marshal(struct {
		Kind    Kind
		Desired Desired
	}{k, d})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func validKind(k Kind) bool { return k == Network || k == Subnet || k == Port || k == Volume }
func validDesired(k Kind, d Desired) bool {
	if !idPattern.MatchString(d.ProjectID) || d.Name == "" || len(d.Name) > 255 {
		return false
	}
	switch k {
	case Network:
		return d.Profile != "" && d.MTU > 0 && d.SegmentPolicy != ""
	case Subnet:
		return idPattern.MatchString(d.NetworkID) && d.IPFamily != "" && d.CIDR != "" && d.GatewayPolicy != "" && d.AllocationPolicy != ""
	case Port:
		return idPattern.MatchString(d.NetworkID) && d.MACPolicy != "" && d.IPAllocationMode != "" && d.AttachmentPolicy != "" && d.DatapathProfile != ""
	case Volume:
		return d.SizeBytes > 0 && d.StorageClassID != "" && d.StorageClassRevision > 0 && d.SourceType != ""
	}
	return false
}
