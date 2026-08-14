package ovnruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type SubnetWorkAdapter interface {
	ObserveSubnet(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
	ReconcileSubnet(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
}

type SubnetWorkStore interface {
	ClaimSubnet(context.Context, string, time.Duration) (postgres.SubnetRealizationClaim, error)
	RecordSubnetReadBack(context.Context, postgres.SubnetRealizationClaim) error
	AuthorizeSubnetApply(context.Context, postgres.SubnetRealizationClaim) error
	MarkSubnetUnknown(context.Context, postgres.SubnetRealizationClaim) error
	CompleteSubnet(context.Context, postgres.SubnetRealizationClaim, postgres.SubnetRealizationObservation) (string, error)
}

type PostgresSubnetWorkStore struct{ DB postgres.TxBeginner }

func (s PostgresSubnetWorkStore) ClaimSubnet(c context.Context, o string, l time.Duration) (postgres.SubnetRealizationClaim, error) {
	return postgres.ClaimSubnetRealization(c, s.DB, "", o, l)
}
func (s PostgresSubnetWorkStore) RecordSubnetReadBack(c context.Context, x postgres.SubnetRealizationClaim) error {
	return postgres.RecordSubnetRealizationReadBackStarted(c, s.DB, x)
}
func (s PostgresSubnetWorkStore) AuthorizeSubnetApply(c context.Context, x postgres.SubnetRealizationClaim) error {
	return postgres.AuthorizeSubnetRealizationApply(c, s.DB, x)
}
func (s PostgresSubnetWorkStore) MarkSubnetUnknown(c context.Context, x postgres.SubnetRealizationClaim) error {
	return postgres.MarkSubnetRealizationDispatchUnknown(c, s.DB, x)
}
func (s PostgresSubnetWorkStore) CompleteSubnet(c context.Context, x postgres.SubnetRealizationClaim, o postgres.SubnetRealizationObservation) (string, error) {
	return postgres.AcceptSubnetRealizationObservation(c, s.DB, x, o)
}

func (worker Worker) runSubnetOnce(ctx context.Context) (bool, error) {
	if worker.SubnetStore == nil {
		return false, nil
	}
	adapter, ok := worker.Adapter.(SubnetWorkAdapter)
	if !ok {
		return false, errors.New("OVN adapter does not implement typed Subnet realization")
	}
	claim, err := worker.SubnetStore.ClaimSubnet(ctx, worker.Owner, worker.ClaimLease)
	if errors.Is(err, postgres.ErrNoSubnetRealizationWork) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var result ovnadapter.RuntimeResult
	if claim.ClaimMode == "READ_BACK_FIRST" {
		if err = worker.SubnetStore.RecordSubnetReadBack(ctx, claim); err == nil {
			result, err = adapter.ObserveSubnet(ctx, claim.CanonicalPlan, claim.PlanDigest)
		}
		if err == nil {
			_, plan, e := ovnadapter.RestoreStoredSubnetPlan(claim.CanonicalPlan, claim.PlanDigest)
			if e != nil {
				err = e
			} else if state := result.SubnetObservation.State(plan); state != "VERIFIED" && state != "ABSENT" {
				if err = worker.SubnetStore.AuthorizeSubnetApply(ctx, claim); err == nil {
					result, err = adapter.ReconcileSubnet(ctx, claim.CanonicalPlan, claim.PlanDigest)
				}
			}
		}
	} else {
		if err = worker.SubnetStore.AuthorizeSubnetApply(ctx, claim); err == nil {
			result, err = adapter.ReconcileSubnet(ctx, claim.CanonicalPlan, claim.PlanDigest)
		}
	}
	if err != nil {
		if e := worker.SubnetStore.MarkSubnetUnknown(ctx, claim); e != nil {
			return false, errors.Join(err, e)
		}
		return false, &itemLocalError{workID: claim.OperationID, err: fmt.Errorf("Subnet realization: %w", err)}
	}
	observation := postgres.SubnetRealizationObservation{ObservationID: fmt.Sprintf("subnet-observation:%s:%d", claim.OperationID, claim.ClaimGeneration), OperationID: claim.OperationID, OperationGeneration: claim.OperationGeneration, ObservationGeneration: claim.ClaimGeneration, ApplyResponseState: result.ApplyResponseState, DHCPObjectName: result.SubnetObservation.DHCPObjectName, BackendUUID: result.SubnetObservation.BackendUUID, ObservationDigest: result.NBObservationDigest, AdapterArtifactDigest: worker.AdapterArtifactDigest, Observation: result.SubnetObservation}
	if _, err = worker.SubnetStore.CompleteSubnet(ctx, claim, observation); err != nil {
		return false, err
	}
	return true, nil
}
