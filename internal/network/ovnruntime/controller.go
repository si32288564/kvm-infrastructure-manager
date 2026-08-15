package ovnruntime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type WorkAdapter interface {
	ObservePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
	ReconcilePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
	ObservePortBindingRetirement(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
	RetirePortBinding(context.Context, []byte, string) (ovnadapter.RuntimeResult, error)
}

type WorkStore interface {
	Claim(context.Context, postgres.OVNRuntimeClaimRequest) ([]postgres.OVNRuntimeWork, error)
	Renew(context.Context, postgres.OVNRuntimeClaim, time.Duration) (postgres.OVNRuntimeRenewal, error)
	RecordReadBack(context.Context, postgres.OVNRuntimeClaim) error
	AuthorizeApply(context.Context, postgres.OVNRuntimeClaim) error
	Quarantine(context.Context, postgres.OVNRuntimeClaim, string) error
	Complete(context.Context, postgres.OVNRuntimeClaim, postgres.OVNPortObservation) error
	CompleteRetirement(context.Context, postgres.OVNRuntimeClaim, postgres.OVNPortBindingRetirementObservation) error
}

type PostgresWorkStore struct {
	DB                       postgres.TxBeginner
	ReleaseBindingGeneration uint64
}

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
	request.ReleaseBindingGeneration = store.ReleaseBindingGeneration
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

func (store PostgresWorkStore) CompleteRetirement(ctx context.Context, claim postgres.OVNRuntimeClaim, observation postgres.OVNPortBindingRetirementObservation) error {
	return postgres.CompleteOVNPortBindingRetirement(ctx, store.DB, claim, observation)
}

// Worker is a bounded multi-worker OVN runtime executor. PostgreSQL owns work
// assignment; the process and its transport are not authority.
type Worker struct {
	Store                 WorkStore
	NetworkStore          NetworkWorkStore
	SubnetStore           SubnetWorkStore
	PortResourceStore     PortResourceWorkStore
	Adapter               WorkAdapter
	Owner                 string
	BatchLimit            int
	ClaimLease            time.Duration
	ClaimMaximumLifetime  time.Duration
	ClaimRenewInterval    time.Duration
	AdapterArtifactDigest string
	ErrorHandler          func(error)
	Metrics               *Metrics
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
	networkCompleted, networkErr := worker.runNetworkOnce(ctx)
	if networkErr != nil {
		var itemFailure *itemLocalError
		if !errors.As(networkErr, &itemFailure) {
			return boolToInt(networkCompleted), networkErr
		}
	}
	subnetCompleted, subnetErr := worker.runSubnetOnce(ctx)
	if subnetErr != nil {
		var itemFailure *itemLocalError
		if !errors.As(subnetErr, &itemFailure) {
			return boolToInt(networkCompleted), subnetErr
		}
	}
	portCompleted, portErr := worker.runPortResourceOnce(ctx)
	if portErr != nil {
		var itemFailure *itemLocalError
		if !errors.As(portErr, &itemFailure) {
			return boolToInt(networkCompleted) + boolToInt(subnetCompleted), portErr
		}
	}
	claimStarted := time.Now()
	work, err := worker.Store.Claim(ctx, postgres.OVNRuntimeClaimRequest{Owner: worker.Owner, Limit: worker.BatchLimit, Lease: worker.ClaimLease, MaximumLifetime: maximumLifetime})
	worker.Metrics.recordClaim(work, time.Since(claimStarted), err)
	if err != nil {
		return boolToInt(networkCompleted) + boolToInt(subnetCompleted) + boolToInt(portCompleted), errors.Join(networkErr, subnetErr, portErr, err)
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
			worker.Metrics.recordWork(item.WorkID, completed, err)
			outcomes <- itemOutcome{completed: completed, err: err}
		}()
	}
	completed := boolToInt(networkCompleted) + boolToInt(subnetCompleted) + boolToInt(portCompleted)
	var itemErrors, fatalErrors error
	if networkErr != nil {
		var itemFailure *itemLocalError
		if errors.As(networkErr, &itemFailure) {
			itemErrors = errors.Join(itemErrors, networkErr)
		} else {
			fatalErrors = errors.Join(fatalErrors, networkErr)
		}
	}
	if subnetErr != nil {
		var itemFailure *itemLocalError
		if errors.As(subnetErr, &itemFailure) {
			itemErrors = errors.Join(itemErrors, subnetErr)
		} else {
			fatalErrors = errors.Join(fatalErrors, subnetErr)
		}
	}
	if portErr != nil {
		var itemFailure *itemLocalError
		if errors.As(portErr, &itemFailure) {
			itemErrors = errors.Join(itemErrors, portErr)
		} else {
			fatalErrors = errors.Join(fatalErrors, portErr)
		}
	}
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

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
			if item.OperationKind == "UNBIND" {
				return worker.Adapter.ObservePortBindingRetirement(operationContext, item.CanonicalObjectSet, item.ObjectSetDigest)
			}
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
		if runtimeResultTerminal(item, result) {
			if err := worker.complete(ctx, claim, item, result); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if err := worker.Store.AuthorizeApply(ctx, claim); err != nil {
		return false, err
	}
	result, err = worker.runAdapterWithRenewal(ctx, claim, func(operationContext context.Context) (ovnadapter.RuntimeResult, error) {
		if item.OperationKind == "UNBIND" {
			return worker.Adapter.RetirePortBinding(operationContext, item.CanonicalObjectSet, item.ObjectSetDigest)
		}
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
	if err := worker.complete(ctx, claim, item, result); err != nil {
		return false, err
	}
	return true, nil
}

func runtimeResultTerminal(item postgres.OVNRuntimeWork, result ovnadapter.RuntimeResult) bool {
	if item.OperationKind == "UNBIND" {
		return result.RetirementObservation.State() == "VERIFIED"
	}
	return result.Observation.NBState() == "MATCHED" && result.Observation.SBState() == "MATCHED"
}

func (worker Worker) complete(ctx context.Context, claim postgres.OVNRuntimeClaim, item postgres.OVNRuntimeWork, result ovnadapter.RuntimeResult) error {
	if item.OperationKind == "UNBIND" {
		prefix := fmt.Sprintf("ovn-runtime:%s:%d:%d", item.IntentID, item.IntentGeneration, item.ClaimGeneration)
		_, plan, err := ovnadapter.RestoreStoredPortBindingRetirementPlan(item.CanonicalObjectSet, item.ObjectSetDigest)
		if err != nil {
			return err
		}
		return worker.Store.CompleteRetirement(ctx, claim, postgres.OVNPortBindingRetirementObservation{
			EvidenceID: prefix + ":retirement", IntentID: item.IntentID, IntentGeneration: item.IntentGeneration,
			PortID: item.PortID, PortGeneration: item.PortGeneration, BindingGeneration: item.BindingGeneration,
			SourceHostID: plan.SourceHostID, OperationGeneration: plan.OperationGeneration,
			NBObservationGeneration: item.ClaimGeneration, SBObservationGeneration: item.ClaimGeneration, OVSObservationGeneration: item.ClaimGeneration,
			NBObservationDigest: result.NBObservationDigest, SBObservationDigest: result.SBObservationDigest, OVSObservationDigest: result.OVSObservationDigest,
			AdapterArtifactDigest: worker.AdapterArtifactDigest, ApplyResponseState: result.ApplyResponseState, Observation: result.RetirementObservation,
		})
	}
	return worker.Store.Complete(ctx, claim, runtimeObservation(item, result, worker.AdapterArtifactDigest))
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
			renewalStarted := time.Now()
			renewal, err := worker.Store.Renew(ctx, claim, worker.ClaimLease)
			worker.Metrics.recordRenewal(renewal, time.Since(renewalStarted), err)
			if err != nil {
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
	return worker.RunWithDrain(ctx, nil, pollInterval)
}

// RunWithDrain stops taking new claims after drain is closed while allowing
// the current claimed batch and its renewals to finish. ctx is reserved for a
// hard stop when the bounded drain deadline is exceeded.
func (worker Worker) RunWithDrain(ctx context.Context, drain <-chan struct{}, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		return errors.New("OVN runtime worker poll interval must be positive")
	}
	worker.Metrics.setState(workerStateActive)
	defer worker.Metrics.stop()
	var drainRequested atomic.Bool
	drainWatcherDone := make(chan struct{})
	drainWatcherExited := make(chan struct{})
	if drain != nil {
		go func() {
			defer close(drainWatcherExited)
			select {
			case <-drain:
				drainRequested.Store(true)
				worker.Metrics.startDrain()
			case <-ctx.Done():
			case <-drainWatcherDone:
			}
		}()
		defer func() {
			close(drainWatcherDone)
			<-drainWatcherExited
		}()
	}
	isDraining := func() bool {
		if drainRequested.Load() {
			return true
		}
		if drain == nil {
			return false
		}
		select {
		case <-drain:
			drainRequested.Store(true)
			worker.Metrics.startDrain()
			return true
		default:
			return false
		}
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
	if isDraining() {
		return nil
	}
	if err := runOnce(); err != nil {
		return err
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if isDraining() {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-drain:
			worker.Metrics.startDrain()
			return nil
		case <-ticker.C:
			if isDraining() {
				return nil
			}
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
