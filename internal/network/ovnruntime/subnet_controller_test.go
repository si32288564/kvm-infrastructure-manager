package ovnruntime

import (
	"context"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type fakeSubnetStore struct {
	claim           postgres.SubnetRealizationClaim
	completed       postgres.SubnetRealizationObservation
	readBack, apply bool
}

func (s *fakeSubnetStore) ClaimSubnet(context.Context, string, time.Duration) (postgres.SubnetRealizationClaim, error) {
	return s.claim, nil
}
func (s *fakeSubnetStore) RecordSubnetReadBack(context.Context, postgres.SubnetRealizationClaim) error {
	s.readBack = true
	return nil
}
func (s *fakeSubnetStore) AuthorizeSubnetApply(context.Context, postgres.SubnetRealizationClaim) error {
	s.apply = true
	return nil
}
func (s *fakeSubnetStore) MarkSubnetUnknown(context.Context, postgres.SubnetRealizationClaim) error {
	return nil
}
func (s *fakeSubnetStore) CompleteSubnet(_ context.Context, _ postgres.SubnetRealizationClaim, o postgres.SubnetRealizationObservation) (string, error) {
	s.completed = o
	return "VERIFIED", nil
}

type subnetCapableAdapter struct {
	*fakeWorkAdapter
	observe, reconcile           ovnadapter.RuntimeResult
	observeCalls, reconcileCalls int
}

func (a *subnetCapableAdapter) ObserveSubnet(context.Context, []byte, string) (ovnadapter.RuntimeResult, error) {
	a.observeCalls++
	return a.observe, nil
}
func (a *subnetCapableAdapter) ReconcileSubnet(context.Context, []byte, string) (ovnadapter.RuntimeResult, error) {
	a.reconcileCalls++
	return a.reconcile, nil
}

func TestWorkerSubnetReadBackFirstCompletesWithoutApply(t *testing.T) {
	raw, planDigest, err := ovnadapter.PlanSubnet(ovnadapter.SubnetIntentInput{OperationID: "subnet-operation", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", NetworkRevision: 1, SubnetID: "subnet", SubnetRevision: 1, RealizationGeneration: 1, CIDR: "192.0.2.0/24", GatewayAddress: "192.0.2.1", DHCPEnabled: true, DesiredState: "PRESENT"})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeSubnetStore{claim: postgres.SubnetRealizationClaim{OperationID: "subnet-operation", OperationGeneration: 1, ClaimGeneration: 2, ClaimMode: "READ_BACK_FIRST", CanonicalPlan: raw, PlanDigest: planDigest}}
	result := ovnadapter.RuntimeResult{ApplyResponseState: "UNKNOWN", NBObservationDigest: digest("subnet-observation"), SubnetObservation: ovnadapter.SubnetObservation{ObjectPresent: true, OwnershipMarkerMatches: true, PlanDigestMatches: true, CIDRMatches: true, OptionsMatch: true, NetworkAssociationMatches: true, DHCPObjectName: "kim-dhcp-18507519c552a7b9", BackendUUID: "backend"}}
	adapter := &subnetCapableAdapter{fakeWorkAdapter: &fakeWorkAdapter{}, observe: result, reconcile: result}
	worker := Worker{Store: &fakeWorkStore{}, SubnetStore: store, Adapter: adapter, Owner: "worker", BatchLimit: 1, ClaimLease: time.Minute, ClaimMaximumLifetime: time.Minute, AdapterArtifactDigest: digest("adapter"), Metrics: NewMetrics()}
	completed, err := worker.RunOnce(context.Background())
	if err != nil || completed != 1 || adapter.observeCalls != 1 || adapter.reconcileCalls != 0 || !store.readBack || store.apply || store.completed.ObservationID == "" {
		t.Fatalf("completed=%d observe=%d reconcile=%d observation=%+v err=%v", completed, adapter.observeCalls, adapter.reconcileCalls, store.completed, err)
	}
}
