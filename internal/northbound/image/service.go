// Package image defines logical Image and asynchronous artifact ingestion contracts.
package image

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

var (
	idPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

type Resource struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"projectId"`
	Name              string    `json:"name"`
	Architecture      string    `json:"architecture"`
	Format            string    `json:"format"`
	ExpectedDigest    string    `json:"expectedDigest"`
	SourceID          string    `json:"sourceId"`
	Visibility        string    `json:"visibility"`
	VerifiedDigest    *string   `json:"verifiedDigest"`
	VerifiedSizeBytes *uint64   `json:"verifiedSizeBytes"`
	VerificationState string    `json:"verificationState"`
	LifecycleState    string    `json:"lifecycleState"`
	DeleteProtection  bool      `json:"deleteProtection"`
	Revision          uint64    `json:"revision"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type Desired struct {
	ProjectID        string `json:"projectId"`
	Name             string `json:"name"`
	Architecture     string `json:"architecture"`
	Format           string `json:"format"`
	ExpectedDigest   string `json:"expectedDigest"`
	SourceID         string `json:"sourceId"`
	Visibility       string `json:"visibility"`
	DeleteProtection bool   `json:"deleteProtection"`
}
type CreateRequest struct {
	Desired
	IdempotencyKey, RequestID, CanonicalPath string
}
type Patch struct {
	Name             *string
	Architecture     *string
	Format           *string
	ExpectedDigest   *string
	SourceID         *string
	Visibility       *string
	LifecycleState   *string
	DeleteProtection *bool
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
	TerminalState      *string    `json:"terminalState"`
	ErrorCode          *string    `json:"errorCode"`
	Retryable          bool       `json:"retryable"`
	Cancellable        bool       `json:"cancellable"`
	CompletedAt        *time.Time `json:"completedAt"`
}
type IngestionRequest struct{ IdempotencyKey, RequestID string }

type Store interface {
	Create(context.Context, resource.Principal, CreateRequest, string, string) (Resource, bool, error)
	Get(context.Context, resource.Principal, string, string) (Resource, error)
	List(context.Context, resource.Principal, ListRequest, string) (Page, error)
	Patch(context.Context, resource.Principal, string, uint64, Patch, string) (Resource, error)
	Delete(context.Context, resource.Principal, string, uint64, string) error
	CreateIngestion(context.Context, resource.Principal, string, uint64, IngestionRequest, string, string) (Operation, bool, error)
	GetOperation(context.Context, resource.Principal, string, string) (Operation, error)
}
type Service struct{ Store Store }

func (s Service) Create(ctx context.Context, p resource.Principal, r CreateRequest) (Resource, bool, error) {
	r.Name, r.ExpectedDigest, r.SourceID = strings.TrimSpace(r.Name), strings.ToLower(r.ExpectedDigest), strings.TrimSpace(r.SourceID)
	if s.Store == nil {
		return Resource{}, false, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Resource{}, false, resource.ErrUnauthenticated
	}
	if r.CanonicalPath != "/api/v1/images" || r.RequestID == "" || r.IdempotencyKey == "" || len(r.IdempotencyKey) > 255 || !validDesired(r.Desired) {
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
	if patch.Name != nil {
		v := strings.TrimSpace(*patch.Name)
		patch.Name = &v
	}
	if patch.ExpectedDigest != nil {
		v := strings.ToLower(strings.TrimSpace(*patch.ExpectedDigest))
		patch.ExpectedDigest = &v
	}
	if patch.SourceID != nil {
		v := strings.TrimSpace(*patch.SourceID)
		patch.SourceID = &v
	}
	if !idPattern.MatchString(id) || revision == 0 || requestID == "" || patch.empty() || !patch.valid() {
		return Resource{}, resource.ErrValidation
	}
	return s.Store.Patch(ctx, p, id, revision, patch, requestID)
}

func (p Patch) empty() bool {
	return p.Name == nil && p.Architecture == nil && p.Format == nil && p.ExpectedDigest == nil && p.SourceID == nil && p.Visibility == nil && p.LifecycleState == nil && p.DeleteProtection == nil
}

func (p Patch) valid() bool {
	return (p.Name == nil || *p.Name != "" && len(*p.Name) <= 255) &&
		(p.Architecture == nil || *p.Architecture == "X86_64" || *p.Architecture == "AARCH64") &&
		(p.Format == nil || *p.Format == "RAW" || *p.Format == "QCOW2") &&
		(p.ExpectedDigest == nil || digestPattern.MatchString(*p.ExpectedDigest)) &&
		(p.SourceID == nil || sourcePattern.MatchString(*p.SourceID)) &&
		(p.Visibility == nil || *p.Visibility == "PRIVATE" || *p.Visibility == "SHARED" || *p.Visibility == "PUBLIC") &&
		(p.LifecycleState == nil || *p.LifecycleState == "ACTIVE" || *p.LifecycleState == "DEPRECATED")
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
func (s Service) Ingest(ctx context.Context, p resource.Principal, id string, revision uint64, r IngestionRequest) (Operation, bool, error) {
	if s.Store == nil {
		return Operation{}, false, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Operation{}, false, resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || revision == 0 || r.RequestID == "" || r.IdempotencyKey == "" || len(r.IdempotencyKey) > 255 {
		return Operation{}, false, resource.ErrValidation
	}
	opID, err := resource.NewID()
	if err != nil {
		return Operation{}, false, err
	}
	sum := sha256.Sum256([]byte(id + "\x00" + stringRevision(revision)))
	return s.Store.CreateIngestion(ctx, p, id, revision, r, opID, hex.EncodeToString(sum[:]))
}
func (s Service) GetOperation(ctx context.Context, p resource.Principal, id, requestID string) (Operation, error) {
	if s.Store == nil {
		return Operation{}, resource.ErrServiceUnavailable
	}
	if !p.Valid() {
		return Operation{}, resource.ErrUnauthenticated
	}
	if !idPattern.MatchString(id) || requestID == "" {
		return Operation{}, resource.ErrValidation
	}
	return s.Store.GetOperation(ctx, p, id, requestID)
}

func validDesired(d Desired) bool {
	return idPattern.MatchString(d.ProjectID) && d.Name != "" && len(d.Name) <= 255 && (d.Architecture == "X86_64" || d.Architecture == "AARCH64") && (d.Format == "RAW" || d.Format == "QCOW2") && digestPattern.MatchString(d.ExpectedDigest) && sourcePattern.MatchString(d.SourceID) && (d.Visibility == "PRIVATE" || d.Visibility == "SHARED" || d.Visibility == "PUBLIC")
}
func DesiredDigest(d Desired) (string, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func stringRevision(v uint64) string { b, _ := json.Marshal(v); return string(b) }
