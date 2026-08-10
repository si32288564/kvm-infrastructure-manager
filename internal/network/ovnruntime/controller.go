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
	Renew(context.Context, postgres.OVNRuntimeClaim, time.Duration) (postgres.OVNRuntimeRenewal, error)
	RecordReadBack(context.Context, postgres.OVNRuntimeClaim) error
	AuthorizeApply(context.Context, postgres.OVNRuntimeClaim) error
	Quarantine(context.Context, postgres.OVNRuntimeClaim, string) error
	Complete(context.Context, postgres.OVNRuntimeClaim, postgres.OVNPortObservation) error
}

type PostgresWorkStore struct{ DB postgres.TxBeginner }

func (store PostgresWorkStore) Claim(ctx context.Context, request postgres.OVNRuntimeClaimRequest) ([]postgres.OVNRuntimeWork, error) {
	return postgres.ClaimOVNRuntimeWork(ctx, store.DB, request)
}

func (store PostgresWorkStore) Renew(ctx context.Context, claim postgres.OVNRuntimeClaim, lease time.Duration) (postgres.OVNRuntimeRenewal, error) {
	return postgres.RenewOVNRuntimeClaim(ctx, store.DB, claim, lease)
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
	ClaimMaximumLifetime  time.Duration
	ClaimRenewInterval    time.Duration
	AdapterArtifactDigest string
}

func (worker Worker) RunOnce(ctx context.Context) (int, error) {
	_, digestErr := hex.DecodeString(worker.AdapterArtifactDigest)
	maximumLifetime := worker.ClaimMaximumLifetime
	if maximumLifetime == 0 {
		maximumLifetime = worker.ClaimLease
	}
	if worker.Store == nil || worker.Adapter == nil || worker.Owner == "" || worker.BatchLimit < 1 || worker.BatchLimit > 100 || worker.ClaimLease <= 0 || maximumLifetime < worker.ClaimLease || maximumLifetime > postgres.MaxOVNRuntimeClaimLifetime || worker.ClaimRenewInterval < 0 || (worker.ClaimRenewInterval > 0 && (worker.ClaimRenewInterval >= worker.ClaimLease || maximumLifetime <= worker.ClaimLease)) || len(worker.AdapterArtifactDigest) != 64 || digestErr != nil {
		return 0, errors.New("complete bounded OVN runtime worker configuration is required")
	}
	work, err := worker.Store.Claim(ctx, postgres.OVNRuntimeClaimRequest{Owner: worker.Owner, Limit: worker.BatchLimit, Lease: worker.ClaimLease, MaximumLifetime: maximumLifetime})
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
			result, err = worker.runAdapterWithRenewal(ctx, claim, func(operationContext context.Context) (ovnadapter.RuntimeResult, error) {
				return worker.Adapter.ObservePort(operationContext, item.CanonicalObjectSet, item.ObjectSetDigest)
			})
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
		result, err = worker.runAdapterWithRenewal(ctx, claim, func(operationContext context.Context) (ovnadapter.RuntimeResult, error) {
			return worker.Adapter.ReconcilePort(operationContext, item.CanonicalObjectSet, item.ObjectSetDigest)
		})
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

func (worker Worker) runAdapterWithRenewal(ctx context.Context, claim postgres.OVNRuntimeClaim, operation func(context.Context) (ovnadapter.RuntimeResult, error)) (ovnadapter.RuntimeResult, error) {
	if worker.ClaimRenewInterval == 0 {
		return operation(ctx)
	}
	operationContext, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		result ovnadapter.RuntimeResult
		err    error
	}
	completed := make(chan outcome, 1)
	go func() {
		result, err := operation(operationContext)
		completed <- outcome{result: result, err: err}
	}()
	ticker := time.NewTicker(worker.ClaimRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case result := <-completed:
			return result.result, result.err
		case <-ticker.C:
			if _, err := worker.Store.Renew(ctx, claim, worker.ClaimLease); err != nil {
				cancel()
				result := <-completed
				return result.result, errors.Join(err, result.err)
			}
		case <-ctx.Done():
			cancel()
			result := <-completed
			return result.result, errors.Join(ctx.Err(), result.err)
		}
	}
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
