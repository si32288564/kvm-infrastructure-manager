package ovnruntime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type WorkAdapter interface {
	ObservePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
	ReconcilePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
}

type WorkStore interface {
	Claim(context.Context, postgres.OVNRuntimeClaimRequest) ([]postgres.OVNRuntimeWork, error)
	RecordReadBack(context.Context, postgres.OVNRuntimeClaim) error
	AuthorizeApply(context.Context, postgres.OVNRuntimeClaim) error
	Quarantine(context.Context, postgres.OVNRuntimeClaim, string) error
	Complete(context.Context, postgres.OVNRuntimeClaim, postgres.OVNPortObservation) error
}

type PostgresWorkStore struct{ DB postgres.TxBeginner }

func (store PostgresWorkStore) Claim(ctx context.Context, request postgres.OVNRuntimeClaimRequest) ([]postgres.OVNRuntimeWork, error) {
	return postgres.ClaimOVNRuntimeWork(ctx, store.DB, request)
}

func (store PostgresWorkStore) RecordReadBack(ctx context.Context, claim postgres.OVNRuntimeClaim) error {
	return postgres.RecordOVNRuntimeReadBackStarted(ctx, store.DB, claim)
}

func (store PostgresWorkStore) AuthorizeApply(ctx context.Context, claim postgres.OVNRuntimeClaim) error {
	return postgres.AuthorizeOVNRuntimeApply(ctx, store.DB, claim)
}

func (store PostgresWorkStore) Quarantine(ctx context.Context, claim postgres.OVNRuntimeClaim, reason string) error {
	return postgres.QuarantineOVNRuntimeWork(ctx, store.DB, claim, reason)
}

func (store PostgresWorkStore) Complete(ctx context.Context, claim postgres.OVNRuntimeClaim, observation postgres.OVNPortObservation) error {
	return postgres.CompleteOVNRuntimeWork(ctx, store.DB, claim, observation)
}

// Worker is a bounded multi-worker OVN runtime executor. PostgreSQL owns work
// assignment; the process and its transport are not authority.
type Worker struct {
	Store                 WorkStore
	Adapter               WorkAdapter
	Owner                 string
	BatchLimit            int
	ClaimLease            time.Duration
	AdapterArtifactDigest string
}

func (worker Worker) RunOnce(ctx context.Context) (int, error) {
	_, digestErr := hex.DecodeString(worker.AdapterArtifactDigest)
	if worker.Store == nil || worker.Adapter == nil || worker.Owner == "" || worker.BatchLimit < 1 || worker.BatchLimit > 100 || worker.ClaimLease <= 0 || len(worker.AdapterArtifactDigest) != 64 || digestErr != nil {
		return 0, errors.New("complete bounded OVN runtime worker configuration is required")
	}
	work, err := worker.Store.Claim(ctx, postgres.OVNRuntimeClaimRequest{Owner: worker.Owner, Limit: worker.BatchLimit, Lease: worker.ClaimLease})
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, item := range work {
		claim := postgres.OVNRuntimeClaim{WorkID: item.WorkID, Owner: worker.Owner, ClaimGeneration: item.ClaimGeneration}
		var result ovnadapter.RuntimeResult
		if item.ClaimMode == "READ_BACK_FIRST" {
			if err := worker.Store.RecordReadBack(ctx, claim); err != nil {
				return completed, err
			}
			result, err = worker.Adapter.ObservePort(ctx, item.CanonicalObjectSet, item.ObjectSetDigest)
			if err != nil {
				if errors.Is(err, ovnadapter.ErrForeignOVNObject) {
					if quarantineErr := worker.Store.Quarantine(ctx, claim, "foreign_ovn_object"); quarantineErr != nil {
						return completed, errors.Join(err, quarantineErr)
					}
					continue
				}
				return completed, err
			}
			if result.Observation.NBState() == "MATCHED" && result.Observation.SBState() == "MATCHED" {
				if err := worker.Store.Complete(ctx, claim, runtimeObservation(item, result, worker.AdapterArtifactDigest)); err != nil {
					return completed, err
				}
				completed++
				continue
			}
		}
		if err := worker.Store.AuthorizeApply(ctx, claim); err != nil {
			return completed, err
		}
		result, err = worker.Adapter.ReconcilePort(ctx, item.CanonicalObjectSet, item.ObjectSetDigest)
		if err != nil {
			if errors.Is(err, ovnadapter.ErrForeignOVNObject) {
				if quarantineErr := worker.Store.Quarantine(ctx, claim, "foreign_ovn_object"); quarantineErr != nil {
					return completed, errors.Join(err, quarantineErr)
				}
				continue
			}
			return completed, err
		}
		if err := worker.Store.Complete(ctx, claim, runtimeObservation(item, result, worker.AdapterArtifactDigest)); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

func (worker Worker) Run(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		return errors.New("OVN runtime worker poll interval must be positive")
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := worker.RunOnce(ctx); err != nil {
				return err
			}
		}
	}
}

func runtimeObservation(item postgres.OVNRuntimeWork, result ovnadapter.RuntimeResult, adapterDigest string) postgres.OVNPortObservation {
	prefix := fmt.Sprintf("ovn-runtime:%s:%d:%d", item.IntentID, item.IntentGeneration, item.ClaimGeneration)
	return postgres.OVNPortObservation{
		NBObservationID: prefix + ":nb", SBObservationID: prefix + ":sb",
		IntentID: item.IntentID, IntentGeneration: item.IntentGeneration, PortID: item.PortID,
		PortGeneration: item.PortGeneration, BindingGeneration: item.BindingGeneration,
		NBObservationGeneration: item.ClaimGeneration, SBObservationGeneration: item.ClaimGeneration,
		NBObservationDigest: result.NBObservationDigest, SBObservationDigest: result.SBObservationDigest,
		AdapterArtifactDigest: adapterDigest, ChassisIdentityDigest: result.ChassisIdentityDigest,
		ApplyResponseState: result.ApplyResponseState, Observation: result.Observation,
	}
}
