package ovnruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type fakeStore struct {
	decision    postgres.OVNPortIntentDecision
	observation postgres.OVNPortObservation
	acceptErr   error
}

func (store *fakeStore) CommitPortIntent(context.Context, postgres.OVNPortIntentRequest) (postgres.OVNPortIntentDecision, error) {
	return store.decision, nil
}

func (store *fakeStore) AcceptPortObservation(_ context.Context, observation postgres.OVNPortObservation) error {
	store.observation = observation
	return store.acceptErr
}

type fakeAdapter struct {
	result ovnadapter.RuntimeResult
	err    error
}

func (adapter fakeAdapter) ReconcilePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error) {
	return adapter.result, adapter.err
}

func TestControllerBindsRuntimeObservationToCommittedIntent(t *testing.T) {
	store := &fakeStore{decision: postgres.OVNPortIntentDecision{IntentID: "intent-1", IntentGeneration: 2, PortID: "port-1", PortGeneration: 3, BindingGeneration: 4, ObjectSetDigest: digest("plan"), CanonicalObjectSet: []byte(`{"schema_version":"test"}`)}}
	adapter := fakeAdapter{result: ovnadapter.RuntimeResult{ApplyResponseState: "LOST", NBObservationDigest: digest("nb"), SBObservationDigest: digest("sb"), ChassisIdentityDigest: digest("chassis"), Observation: ovnadapter.Observation{OwnershipMarkerMatches: true}}}
	controller := Controller{Store: store, Adapter: adapter}
	request := Request{IntentID: "intent-1", IntentGeneration: 2, PortID: "port-1", ObservationGeneration: 5, AdapterArtifactDigest: digest("adapter")}
	decision, err := controller.ReconcilePort(context.Background(), request)
	if err != nil || decision.IntentID != "intent-1" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	observation := store.observation
	if observation.NBObservationID != "ovn-runtime:intent-1:2:5:nb" || observation.SBObservationID != "ovn-runtime:intent-1:2:5:sb" || observation.PortGeneration != 3 || observation.BindingGeneration != 4 || observation.ApplyResponseState != "LOST" || observation.NBObservationDigest != digest("nb") {
		t.Fatalf("bound observation=%#v", observation)
	}
}

func TestControllerDoesNotAcceptObservationAfterAdapterConflict(t *testing.T) {
	store := &fakeStore{decision: postgres.OVNPortIntentDecision{IntentID: "intent-1", IntentGeneration: 1, PortID: "port-1", PortGeneration: 1, BindingGeneration: 1, ObjectSetDigest: digest("plan"), CanonicalObjectSet: []byte(`{}`)}}
	controller := Controller{Store: store, Adapter: fakeAdapter{err: ovnadapter.ErrForeignOVNObject}}
	_, err := controller.ReconcilePort(context.Background(), Request{IntentID: "intent-1", IntentGeneration: 1, PortID: "port-1", ObservationGeneration: 1, AdapterArtifactDigest: digest("adapter")})
	if !errors.Is(err, ovnadapter.ErrForeignOVNObject) || store.observation.NBObservationID != "" {
		t.Fatalf("adapter conflict result=%v observation=%#v", err, store.observation)
	}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
