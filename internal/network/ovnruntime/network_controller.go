package ovnruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type NetworkWorkAdapter interface {
	ObserveNetwork(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
	ReconcileNetwork(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
}

type NetworkWorkStore interface {
	ClaimNetwork(context.Context, string, time.Duration) (postgres.NetworkRealizationClaim, error)
	RecordNetworkReadBack(context.Context, postgres.NetworkRealizationClaim) error
	AuthorizeNetworkApply(context.Context, postgres.NetworkRealizationClaim) error
	MarkNetworkUnknown(context.Context, postgres.NetworkRealizationClaim) error
	CompleteNetwork(context.Context, postgres.NetworkRealizationClaim, postgres.NetworkRealizationObservation) (string, error)
}

type PostgresNetworkWorkStore struct {
	DB postgres.TxBeginner
}

func (store PostgresNetworkWorkStore) ClaimNetwork(ctx context.Context, owner string, lease time.Duration) (postgres.NetworkRealizationClaim, error) {
	return postgres.ClaimNetworkRealization(ctx, store.DB, "", owner, lease)
}

func (store PostgresNetworkWorkStore) MarkNetworkUnknown(ctx context.Context, claim postgres.NetworkRealizationClaim) error {
	return postgres.MarkNetworkRealizationDispatchUnknown(ctx, store.DB, claim)
}

func (store PostgresNetworkWorkStore) RecordNetworkReadBack(ctx context.Context, claim postgres.NetworkRealizationClaim) error {
	return postgres.RecordNetworkRealizationReadBackStarted(ctx, store.DB, claim)
}

func (store PostgresNetworkWorkStore) AuthorizeNetworkApply(ctx context.Context, claim postgres.NetworkRealizationClaim) error {
	return postgres.AuthorizeNetworkRealizationApply(ctx, store.DB, claim)
}

func (store PostgresNetworkWorkStore) CompleteNetwork(ctx context.Context, claim postgres.NetworkRealizationClaim, observation postgres.NetworkRealizationObservation) (string, error) {
	return postgres.AcceptNetworkRealizationObservation(ctx, store.DB, claim, observation)
}

func (worker Worker) runNetworkOnce(ctx context.Context) (bool, error) {
	if worker.NetworkStore == nil {
		return false, nil
	}
	adapter, ok := worker.Adapter.(NetworkWorkAdapter)
	if !ok {
		return false, errors.New("OVN adapter does not implement typed Network realization")
	}
	claim, err := worker.NetworkStore.ClaimNetwork(ctx, worker.Owner, worker.ClaimLease)
	if errors.Is(err, postgres.ErrNoNetworkRealizationWork) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	apply := claim.ClaimMode != "READ_BACK_FIRST"
	var result ovnadapter.RuntimeResult
	if apply {
		if err := worker.NetworkStore.AuthorizeNetworkApply(ctx, claim); err != nil {
			return false, err
		}
		result, err = adapter.ReconcileNetwork(ctx, claim.CanonicalPlan, claim.PlanDigest)
	} else {
		if err := worker.NetworkStore.RecordNetworkReadBack(ctx, claim); err != nil {
			return false, err
		}
		result, err = adapter.ObserveNetwork(ctx, claim.CanonicalPlan, claim.PlanDigest)
		if err == nil {
			_, plan, restoreErr := ovnadapter.RestoreStoredNetworkPlan(claim.CanonicalPlan, claim.PlanDigest)
			if restoreErr != nil {
				err = restoreErr
			} else if result.NetworkObservation.State(plan.DesiredState) != "VERIFIED" && result.NetworkObservation.State(plan.DesiredState) != "ABSENT" {
				if err := worker.NetworkStore.AuthorizeNetworkApply(ctx, claim); err != nil {
					return false, err
				}
				result, err = adapter.ReconcileNetwork(ctx, claim.CanonicalPlan, claim.PlanDigest)
			}
		}
	}
	if err != nil {
		if unknownErr := worker.NetworkStore.MarkNetworkUnknown(ctx, claim); unknownErr != nil {
			return false, errors.Join(err, unknownErr)
		}
		return false, &itemLocalError{workID: claim.OperationID, err: fmt.Errorf("Network realization: %w", err)}
	}
	observation := postgres.NetworkRealizationObservation{
		ObservationID:         fmt.Sprintf("network-observation:%s:%d", claim.OperationID, claim.ClaimGeneration),
		OperationID:           claim.OperationID,
		OperationGeneration:   claim.OperationGeneration,
		ObservationGeneration: claim.ClaimGeneration,
		ApplyResponseState:    result.ApplyResponseState,
		LogicalSwitchName:     result.NetworkObservation.LogicalSwitchName,
		BackendUUID:           result.NetworkObservation.BackendUUID,
		ObservationDigest:     result.NBObservationDigest,
		AdapterArtifactDigest: worker.AdapterArtifactDigest,
		Observation:           result.NetworkObservation,
	}
	if _, err := worker.NetworkStore.CompleteNetwork(ctx, claim, observation); err != nil {
		return false, err
	}
	return true, nil
}
