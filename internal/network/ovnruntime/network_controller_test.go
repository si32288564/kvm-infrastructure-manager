package ovnruntime

import (
	"context"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type fakeNetworkStore struct {
	claim     postgres.NetworkRealizationClaim
	completed postgres.NetworkRealizationObservation
	unknown   bool
	readBack  bool
	apply     bool
}

func (store *fakeNetworkStore) ClaimNetwork(context.Context, string, time.Duration) (postgres.NetworkRealizationClaim, error) {
	return store.claim, nil
}

func (store *fakeNetworkStore) MarkNetworkUnknown(context.Context, postgres.NetworkRealizationClaim) error {
	store.unknown = true
	return nil
}

func (store *fakeNetworkStore) RecordNetworkReadBack(context.Context, postgres.NetworkRealizationClaim) error {
	store.readBack = true
	return nil
}

func (store *fakeNetworkStore) AuthorizeNetworkApply(context.Context, postgres.NetworkRealizationClaim) error {
	store.apply = true
	return nil
}

func (store *fakeNetworkStore) CompleteNetwork(_ context.Context, _ postgres.NetworkRealizationClaim, observation postgres.NetworkRealizationObservation) (string, error) {
	store.completed = observation
	return "VERIFIED", nil
}

type networkCapableAdapter struct {
	*fakeWorkAdapter
	networkObserve, networkReconcile ovnadapter.RuntimeResult
	networkObserveCalls              int
	networkReconcileCalls            int
}

func (adapter *networkCapableAdapter) ObserveNetwork(context.Context, []byte, string) (ovnadapter.RuntimeResult, error) {
	adapter.networkObserveCalls++
	return adapter.networkObserve, nil
}

func (adapter *networkCapableAdapter) ReconcileNetwork(context.Context, []byte, string) (ovnadapter.RuntimeResult, error) {
	adapter.networkReconcileCalls++
	return adapter.networkReconcile, nil
}

func TestWorkerNetworkReadBackFirstCompletesWithoutApply(t *testing.T) {
	raw, planDigest, err := ovnadapter.PlanNetwork(ovnadapter.NetworkIntentInput{OperationID: "network-operation", OperationGeneration: 1, ProjectID: "project", NetworkID: "network", AuthorityRevision: 1, BackendRevision: 1, AllocationID: "allocation", AllocationGeneration: 1, RealizationGeneration: 1, SegmentType: "VNI", SegmentID: 10000, DesiredState: "PRESENT"})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeNetworkStore{claim: postgres.NetworkRealizationClaim{OperationID: "network-operation", OperationGeneration: 1, ClaimGeneration: 2, ClaimMode: "READ_BACK_FIRST", CanonicalPlan: raw, PlanDigest: planDigest}}
	result := ovnadapter.RuntimeResult{ApplyResponseState: "UNKNOWN", NBObservationDigest: digest("network-observation"), NetworkObservation: ovnadapter.NetworkObservation{LogicalSwitchPresent: true, OwnershipMarkerMatches: true, PlanDigestMatches: true, LogicalSwitchName: "kim-ls-network", BackendUUID: "backend"}}
	adapter := &networkCapableAdapter{fakeWorkAdapter: &fakeWorkAdapter{}, networkObserve: result, networkReconcile: result}
	worker := Worker{Store: &fakeWorkStore{}, NetworkStore: store, Adapter: adapter, Owner: "worker", BatchLimit: 1, ClaimLease: time.Minute, ClaimMaximumLifetime: time.Minute, AdapterArtifactDigest: digest("adapter"), Metrics: NewMetrics()}
	completed, err := worker.RunOnce(context.Background())
	if err != nil || completed != 1 || adapter.networkObserveCalls != 1 || adapter.networkReconcileCalls != 0 || !store.readBack || store.apply || store.completed.ObservationID == "" {
		t.Fatalf("completed=%d observe=%d reconcile=%d observation=%+v err=%v", completed, adapter.networkObserveCalls, adapter.networkReconcileCalls, store.completed, err)
	}
}
