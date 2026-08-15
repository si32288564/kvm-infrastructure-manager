package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

type captureStore struct{ create CreateRequest }

func (s *captureStore) Create(_ context.Context, _ resource.Principal, r CreateRequest, id, op, digest string) (Resource, bool, error) {
	s.create = r
	return Resource{ID: id, Desired: r.Desired, Revision: 1, OperationID: op}, false, nil
}
func (*captureStore) Get(context.Context, resource.Principal, string, string) (Resource, error) {
	return Resource{}, nil
}
func (*captureStore) List(context.Context, resource.Principal, ListRequest, string) (Page, error) {
	return Page{}, nil
}
func (*captureStore) Patch(context.Context, resource.Principal, string, uint64, Patch, string, string, string) (Resource, error) {
	return Resource{}, nil
}
func (*captureStore) Delete(context.Context, resource.Principal, string, uint64, string, string) (Operation, error) {
	return Operation{}, nil
}
func (*captureStore) GetOperation(context.Context, resource.Principal, string, string) (Operation, error) {
	return Operation{}, nil
}

func validTestDesired() Desired {
	return Desired{ProjectID: "10000000-0000-4000-8000-000000000001", Name: "vm", FlavorID: "20000000-0000-4000-8000-000000000001", FlavorRevision: 1, ImageID: "30000000-0000-4000-8000-000000000001", ImageRevision: 1, AvailabilityPolicyID: "40000000-0000-4000-8000-000000000001", AvailabilityPolicyRevision: 1, PlacementScopeID: "scope", PlacementScopeGeneration: 1, RootVolume: Reference{ID: "50000000-0000-4000-8000-000000000001", Revision: 1}, Ports: []Reference{{ID: "70000000-0000-4000-8000-000000000002", Revision: 1}, {ID: "70000000-0000-4000-8000-000000000001", Revision: 1}}, DataVolumes: []Reference{}, DesiredPowerState: "RUNNING"}
}
func TestCreateCanonicalizesLogicalSetsAndRejectsPhysicalOrUnqualifiedShape(t *testing.T) {
	store := &captureStore{}
	service := Service{Store: store}
	principal := resource.Principal{Issuer: "issuer", Subject: "subject", Type: "AUTOMATION"}
	out, _, err := service.Create(context.Background(), principal, CreateRequest{Desired: validTestDesired(), IdempotencyKey: "key", RequestID: "request", CanonicalPath: "/api/v1/vms"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == "" || out.OperationID == "" || store.create.Desired.Ports[0].ID != "70000000-0000-4000-8000-000000000001" {
		t.Fatalf("out=%+v desired=%+v", out, store.create.Desired)
	}
	bad := validTestDesired()
	bad.Ports = append(bad.Ports, Reference{ID: "70000000-0000-4000-8000-000000000003", Revision: 1})
	if _, _, err = service.Create(context.Background(), principal, CreateRequest{Desired: bad, IdempotencyKey: "key-2", RequestID: "request-2", CanonicalPath: "/api/v1/vms"}); !errors.Is(err, resource.ErrValidation) {
		t.Fatalf("three-Port create err=%v", err)
	}
	bad = validTestDesired()
	bad.DesiredPowerState = "SHUTOFF"
	if _, _, err = service.Create(context.Background(), principal, CreateRequest{Desired: bad, IdempotencyKey: "key-3", RequestID: "request-3", CanonicalPath: "/api/v1/vms"}); !errors.Is(err, resource.ErrValidation) {
		t.Fatalf("initial SHUTOFF err=%v", err)
	}
}
func TestPatchSeparatesMetadataAndPowerAuthority(t *testing.T) {
	service := Service{Store: &captureStore{}}
	principal := resource.Principal{Issuer: "issuer", Subject: "subject", Type: "AUTOMATION"}
	id := "10000000-0000-4000-8000-000000000001"
	name, power := "renamed", "SHUTOFF"
	if _, err := service.Patch(context.Background(), principal, id, 1, Patch{Name: &name, DesiredPowerState: &power}, "request"); !errors.Is(err, resource.ErrValidation) {
		t.Fatalf("mixed patch err=%v", err)
	}
	if _, err := service.Patch(context.Background(), principal, id, 1, Patch{DesiredPowerState: &power}, "request"); err != nil {
		t.Fatal(err)
	}
}
