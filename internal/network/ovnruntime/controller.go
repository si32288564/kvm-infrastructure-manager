package ovnruntime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type Store interface {
	CommitPortIntent(context.Context, postgres.OVNPortIntentRequest) (postgres.OVNPortIntentDecision, error)
	AcceptPortObservation(context.Context, postgres.OVNPortObservation) error
}

type PostgresStore struct{ DB postgres.TxBeginner }

func (store PostgresStore) CommitPortIntent(ctx context.Context, request postgres.OVNPortIntentRequest) (postgres.OVNPortIntentDecision, error) {
	return postgres.CommitOVNPortIntent(ctx, store.DB, request)
}

func (store PostgresStore) AcceptPortObservation(ctx context.Context, observation postgres.OVNPortObservation) error {
	return postgres.AcceptOVNPortObservation(ctx, store.DB, observation)
}

type PortAdapter interface {
	ReconcilePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
}

type Request struct {
	IntentID, PortID      string
	IntentGeneration      uint64
	ObservationGeneration uint64
	AdapterArtifactDigest string
}

type Controller struct {
	Store   Store
	Adapter PortAdapter
}

func (controller Controller) ReconcilePort(ctx context.Context, request Request) (postgres.OVNPortIntentDecision, error) {
	_, digestErr := hex.DecodeString(request.AdapterArtifactDigest)
	if controller.Store == nil || controller.Adapter == nil || request.IntentID == "" || request.PortID == "" || request.IntentGeneration == 0 || request.ObservationGeneration == 0 || len(request.AdapterArtifactDigest) != 64 || digestErr != nil {
		return postgres.OVNPortIntentDecision{}, errors.New("complete OVN runtime reconciliation request is required")
	}
	decision, err := controller.Store.CommitPortIntent(ctx, postgres.OVNPortIntentRequest{IntentID: request.IntentID, IntentGeneration: request.IntentGeneration, PortID: request.PortID})
	if err != nil {
		return postgres.OVNPortIntentDecision{}, err
	}
	if decision.IntentID != request.IntentID || decision.IntentGeneration != request.IntentGeneration || decision.PortID != request.PortID || decision.PortGeneration == 0 || decision.BindingGeneration == 0 || len(decision.ObjectSetDigest) != 64 || len(decision.CanonicalObjectSet) == 0 {
		return postgres.OVNPortIntentDecision{}, errors.New("OVN intent decision does not match the runtime request")
	}
	result, err := controller.Adapter.ReconcilePort(ctx, decision.CanonicalObjectSet, decision.ObjectSetDigest)
	if err != nil {
		return decision, err
	}
	prefix := fmt.Sprintf("ovn-runtime:%s:%d:%d", request.IntentID, request.IntentGeneration, request.ObservationGeneration)
	observation := postgres.OVNPortObservation{
		NBObservationID: prefix + ":nb", SBObservationID: prefix + ":sb",
		IntentID: request.IntentID, IntentGeneration: request.IntentGeneration, PortID: request.PortID,
		PortGeneration: decision.PortGeneration, BindingGeneration: decision.BindingGeneration,
		NBObservationGeneration: request.ObservationGeneration, SBObservationGeneration: request.ObservationGeneration,
		NBObservationDigest: result.NBObservationDigest, SBObservationDigest: result.SBObservationDigest,
		AdapterArtifactDigest: request.AdapterArtifactDigest, ChassisIdentityDigest: result.ChassisIdentityDigest,
		ApplyResponseState: result.ApplyResponseState, Observation: result.Observation,
	}
	if err := controller.Store.AcceptPortObservation(ctx, observation); err != nil {
		return decision, err
	}
	return decision, nil
}
