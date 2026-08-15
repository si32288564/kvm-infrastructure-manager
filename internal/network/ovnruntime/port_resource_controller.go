package ovnruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type PortResourceWorkAdapter interface {
	ObservePortResource(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
	ReconcilePortResource(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
}
type PortResourceWorkStore interface {
	ClaimPort(context.Context, string, time.Duration) (postgres.PortRealizationClaim, error)
	RecordPortReadBack(context.Context, postgres.PortRealizationClaim) error
	AuthorizePortApply(context.Context, postgres.PortRealizationClaim) error
	MarkPortUnknown(context.Context, postgres.PortRealizationClaim) error
	CompletePort(context.Context, postgres.PortRealizationClaim, postgres.PortRealizationObservation) (string, error)
}
type PostgresPortResourceWorkStore struct{ DB postgres.TxBeginner }

func (s PostgresPortResourceWorkStore) ClaimPort(ctx context.Context, owner string, lease time.Duration) (postgres.PortRealizationClaim, error) {
	return postgres.ClaimPortRealization(ctx, s.DB, "", owner, lease)
}
func (s PostgresPortResourceWorkStore) RecordPortReadBack(ctx context.Context, c postgres.PortRealizationClaim) error {
	return postgres.RecordPortRealizationReadBackStarted(ctx, s.DB, c)
}
func (s PostgresPortResourceWorkStore) AuthorizePortApply(ctx context.Context, c postgres.PortRealizationClaim) error {
	return postgres.AuthorizePortRealizationApply(ctx, s.DB, c)
}
func (s PostgresPortResourceWorkStore) MarkPortUnknown(ctx context.Context, c postgres.PortRealizationClaim) error {
	return postgres.MarkPortRealizationDispatchUnknown(ctx, s.DB, c)
}
func (s PostgresPortResourceWorkStore) CompletePort(ctx context.Context, c postgres.PortRealizationClaim, o postgres.PortRealizationObservation) (string, error) {
	return postgres.AcceptPortRealizationObservation(ctx, s.DB, c, o)
}

func (w Worker) runPortResourceOnce(ctx context.Context) (bool, error) {
	if w.PortResourceStore == nil {
		return false, nil
	}
	a, ok := w.Adapter.(PortResourceWorkAdapter)
	if !ok {
		return false, errors.New("OVN adapter does not implement typed Port resource realization")
	}
	c, err := w.PortResourceStore.ClaimPort(ctx, w.Owner, w.ClaimLease)
	if errors.Is(err, postgres.ErrNoPortRealizationWork) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var result ovnadapter.RuntimeResult
	if c.ClaimMode == "READ_BACK_FIRST" {
		if err = w.PortResourceStore.RecordPortReadBack(ctx, c); err != nil {
			return false, err
		}
		result, err = a.ObservePortResource(ctx, c.CanonicalPlan, c.PlanDigest)
		if err == nil {
			_, p, e := ovnadapter.RestoreStoredPortResourcePlan(c.CanonicalPlan, c.PlanDigest)
			if e != nil {
				err = e
			} else if result.PortResourceObservation.State(p.DesiredState) == "UNKNOWN" {
				if err = w.PortResourceStore.AuthorizePortApply(ctx, c); err != nil {
					return false, err
				}
				result, err = a.ReconcilePortResource(ctx, c.CanonicalPlan, c.PlanDigest)
			}
		}
	} else {
		if err = w.PortResourceStore.AuthorizePortApply(ctx, c); err != nil {
			return false, err
		}
		result, err = a.ReconcilePortResource(ctx, c.CanonicalPlan, c.PlanDigest)
	}
	if err != nil {
		if e := w.PortResourceStore.MarkPortUnknown(ctx, c); e != nil {
			return false, errors.Join(err, e)
		}
		return false, &itemLocalError{workID: c.OperationID, err: fmt.Errorf("Port resource realization: %w", err)}
	}
	o := postgres.PortRealizationObservation{ObservationID: fmt.Sprintf("port-resource-observation:%s:%d", c.OperationID, c.ClaimGeneration), OperationID: c.OperationID, OperationGeneration: c.OperationGeneration, ObservationGeneration: c.ClaimGeneration, ApplyResponseState: result.ApplyResponseState, LogicalPortName: result.PortResourceObservation.LogicalPortName, BackendUUID: result.PortResourceObservation.BackendUUID, ObservationDigest: result.NBObservationDigest, AdapterArtifactDigest: w.AdapterArtifactDigest, Observation: result.PortResourceObservation}
	_, err = w.PortResourceStore.CompletePort(ctx, c, o)
	return err == nil, err
}
