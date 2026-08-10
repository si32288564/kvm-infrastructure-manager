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

type itemLocalError struct {
	workID string
	err    error
}

func (failure *itemLocalError) Error() string {
	return fmt.Sprintf("OVN runtime work %s: %v", failure.workID, failure.err)
}
func (failure *itemLocalError) Unwrap() error { return failure.err }

type renewalAuthorityError struct{ err error }

func (failure *renewalAuthorityError) Error() string { return failure.err.Error() }
func (failure *renewalAuthorityError) Unwrap() error { return failure.err }

// RunOnceError preserves whether a claimed batch failed only at typed adapter
// item boundaries or also lost PostgreSQL claim/renewal authority.
type RunOnceError struct {
	ItemErrors error
	FatalError error
}

func (failure *RunOnceError) Error() string {
	return errors.Join(failure.ItemErrors, failure.FatalError).Error()
}

func (failure *RunOnceError) Unwrap() []error {
	var failures []error
	if failure.ItemErrors != nil {
		failures = append(failures, failure.ItemErrors)
	}
	if failure.FatalError != nil {
		failures = append(failures, failure.FatalError)
	}
	return failures
}

func (failure *RunOnceError) ItemLocalOnly() bool {
	return failure.ItemErrors != nil && failure.FatalError == nil
}

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
	ErrorHandler          func(error)
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
	// A claimed batch is also the local concurrency bound. Starting every item
	// immediately prevents a serial local queue from consuming claim lifetime
	// before an item can establish its own authorization and renewal loop.
	type itemOutcome struct {
		completed bool
		err       error
	}
	outcomes := make(chan itemOutcome, len(work))
	for _, item := range work {
		item := item
		go func() {
			completed, err := worker.processWork(ctx, item)
			outcomes <- itemOutcome{completed: completed, err: err}
		}()
	}
	completed := 0
	var itemErrors, fatalErrors error
	for range work {
		outcome := <-outcomes
		if outcome.completed {
			completed++
		}
		if outcome.err == nil {
			continue
		}
		var itemFailure *itemLocalError
		if errors.As(outcome.err, &itemFailure) {
			itemErrors = errors.Join(itemErrors, outcome.err)
		} else {
			fatalErrors = errors.Join(fatalErrors, outcome.err)
		}
	}
	if itemErrors == nil && fatalErrors == nil {
		return completed, nil
	}
	return completed, &RunOnceError{ItemErrors: itemErrors, FatalError: fatalErrors}
}

func (worker Worker) processWork(ctx context.Context, item postgres.OVNRuntimeWork) (bool, error) {
	claim := postgres.OVNRuntimeClaim{WorkID: item.WorkID, Owner: worker.Owner, ClaimGeneration: item.ClaimGeneration}
	var result ovnadapter.RuntimeResult
	var err error
	if item.ClaimMode == "READ_BACK_FIRST" {
		if err := worker.Store.RecordReadBack(ctx, claim); err != nil {
			return false, err
		}
		result, err = worker.runAdapterWithRenewal(ctx, claim, func(operationContext context.Context) (ovnadapter.RuntimeResult, error) {
			return worker.Adapter.ObservePort(operationContext, item.CanonicalObjectSet, item.ObjectSetDigest)
		})
		if err != nil {
			if errors.Is(err, ovnadapter.ErrForeignOVNObject) {
				if quarantineErr := worker.Store.Quarantine(ctx, claim, "foreign_ovn_object"); quarantineErr != nil {
					return false, errors.Join(err, quarantineErr)
				}
				return false, nil
			}
			var renewalFailure *renewalAuthorityError
			if ctx.Err() != nil || errors.As(err, &renewalFailure) {
				return false, err
			}
			return false, &itemLocalError{workID: item.WorkID, err: fmt.Errorf("read-back: %w", err)}
		}
		if result.Observation.NBState() == "MATCHED" && result.Observation.SBState() == "MATCHED" {
			if err := worker.Store.Complete(ctx, claim, runtimeObservation(item, result, worker.AdapterArtifactDigest)); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if err := worker.Store.AuthorizeApply(ctx, claim); err != nil {
		return false, err
	}
	result, err = worker.runAdapterWithRenewal(ctx, claim, func(operationContext context.Context) (ovnadapter.RuntimeResult, error) {
		return worker.Adapter.ReconcilePort(operationContext, item.CanonicalObjectSet, item.ObjectSetDigest)
	})
	if err != nil {
		if errors.Is(err, ovnadapter.ErrForeignOVNObject) {
			if quarantineErr := worker.Store.Quarantine(ctx, claim, "foreign_ovn_object"); quarantineErr != nil {
				return false, errors.Join(err, quarantineErr)
			}
			return false, nil
		}
		var renewalFailure *renewalAuthorityError
		if ctx.Err() != nil || errors.As(err, &renewalFailure) {
			return false, err
		}
		return false, &itemLocalError{workID: item.WorkID, err: fmt.Errorf("apply: %w", err)}
	}
	if err := worker.Store.Complete(ctx, claim, runtimeObservation(item, result, worker.AdapterArtifactDigest)); err != nil {
		return false, err
	}
	return true, nil
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
				return result.result, &renewalAuthorityError{err: errors.Join(err, result.err)}
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
	runOnce := func() error {
		if _, err := worker.RunOnce(ctx); err != nil && ctx.Err() == nil {
			var runFailure *RunOnceError
			if errors.As(err, &runFailure) && runFailure.ItemLocalOnly() {
				if worker.ErrorHandler != nil {
					worker.ErrorHandler(err)
				}
				return nil
			}
			return err
		}
		return nil
	}
	if err := runOnce(); err != nil {
		return err
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := runOnce(); err != nil {
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
