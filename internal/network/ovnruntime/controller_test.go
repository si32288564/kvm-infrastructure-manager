package ovnruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type fakeWorkStore struct {
	work      []postgres.OVNRuntimeWork
	sequence  []string
	completed postgres.OVNPortObservation
	claim     postgres.OVNRuntimeClaim
	renewals  atomic.Int32
}

func (store *fakeWorkStore) Claim(context.Context, postgres.OVNRuntimeClaimRequest) ([]postgres.OVNRuntimeWork, error) {
	store.sequence = append(store.sequence, "claim")
	return store.work, nil
}

func (store *fakeWorkStore) Renew(_ context.Context, claim postgres.OVNRuntimeClaim, _ time.Duration) (postgres.OVNRuntimeRenewal, error) {
	store.renewals.Add(1)
	return postgres.OVNRuntimeRenewal{WorkID: claim.WorkID, Owner: claim.Owner, ClaimGeneration: claim.ClaimGeneration}, nil
}

func (store *fakeWorkStore) RecordReadBack(_ context.Context, claim postgres.OVNRuntimeClaim) error {
	store.sequence = append(store.sequence, "read-back-evidence")
	store.claim = claim
	return nil
}

func (store *fakeWorkStore) AuthorizeApply(_ context.Context, claim postgres.OVNRuntimeClaim) error {
	store.sequence = append(store.sequence, "apply-authorized")
	store.claim = claim
	return nil
}

func (store *fakeWorkStore) Quarantine(_ context.Context, claim postgres.OVNRuntimeClaim, _ string) error {
	store.sequence = append(store.sequence, "quarantined")
	store.claim = claim
	return nil
}

func (store *fakeWorkStore) Complete(_ context.Context, claim postgres.OVNRuntimeClaim, observed postgres.OVNPortObservation) error {
	store.sequence = append(store.sequence, "completed")
	store.claim, store.completed = claim, observed
	return nil
}

type fakeWorkAdapter struct {
	observeResult, reconcileResult ovnadapter.RuntimeResult
	observeErr, reconcileErr       error
	observeCount, reconcileCount   int
	reconcileDelay                 time.Duration
	sequence                       *[]string
}

func (adapter *fakeWorkAdapter) ObservePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error) {
	adapter.observeCount++
	*adapter.sequence = append(*adapter.sequence, "observe")
	return adapter.observeResult, adapter.observeErr
}

func (adapter *fakeWorkAdapter) ReconcilePort(ctx context.Context, _ []byte, _ string) (ovnadapter.RuntimeResult, error) {
	adapter.reconcileCount++
	*adapter.sequence = append(*adapter.sequence, "apply")
	if adapter.reconcileDelay > 0 {
		select {
		case <-time.After(adapter.reconcileDelay):
		case <-ctx.Done():
			return ovnadapter.RuntimeResult{}, ctx.Err()
		}
	}
	return adapter.reconcileResult, adapter.reconcileErr
}

func TestWorkerRenewsClaimDuringLongRunningAdapterOperation(t *testing.T) {
	store := &fakeWorkStore{work: []postgres.OVNRuntimeWork{{
		WorkID: "work-renew", IntentID: "intent-renew", IntentGeneration: 1, PortID: "port-renew",
		PortGeneration: 1, BindingGeneration: 1, ObjectSetDigest: digest("plan"),
		ClaimMode: "APPLY_ALLOWED", ClaimGeneration: 1, CanonicalObjectSet: []byte(`{}`),
	}}}
	matched := ovnadapter.RuntimeResult{ApplyResponseState: "RECEIVED", NBObservationDigest: digest("nb"), SBObservationDigest: digest("sb"), ChassisIdentityDigest: digest("chassis"), Observation: ovnadapter.Observation{
		OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true,
		LogicalSwitchPortPresent: true, PortBindingPresent: true, DatapathPresent: true, ExpectedChassisMatches: true,
	}}
	adapter := &fakeWorkAdapter{reconcileResult: matched, reconcileDelay: 120 * time.Millisecond, sequence: &store.sequence}
	worker := Worker{Store: store, Adapter: adapter, Owner: "worker-renew", BatchLimit: 1,
		ClaimLease: 60 * time.Millisecond, ClaimMaximumLifetime: time.Second, ClaimRenewInterval: 20 * time.Millisecond,
		AdapterArtifactDigest: digest("adapter")}
	completed, err := worker.RunOnce(context.Background())
	if err != nil || completed != 1 || store.renewals.Load() < 2 || adapter.reconcileCount != 1 {
		t.Fatalf("completed=%d renewals=%d reconciles=%d err=%v sequence=%v", completed, store.renewals.Load(), adapter.reconcileCount, err, store.sequence)
	}
}

func TestWorkerReadsBackUncertainClaimBeforeApply(t *testing.T) {
	store := &fakeWorkStore{work: []postgres.OVNRuntimeWork{{
		WorkID: "work-1", IntentID: "intent-1", IntentGeneration: 2, PortID: "port-1",
		PortGeneration: 3, BindingGeneration: 4, ObjectSetDigest: digest("plan"),
		ClaimMode: "READ_BACK_FIRST", ClaimGeneration: 5, CanonicalObjectSet: []byte(`{}`),
	}}}
	unknown := ovnadapter.RuntimeResult{ApplyResponseState: "UNKNOWN", NBObservationDigest: digest("observe-nb"), SBObservationDigest: digest("observe-sb"), ChassisIdentityDigest: digest("chassis")}
	matched := ovnadapter.RuntimeResult{ApplyResponseState: "RECEIVED", NBObservationDigest: digest("apply-nb"), SBObservationDigest: digest("apply-sb"), ChassisIdentityDigest: digest("chassis"), Observation: ovnadapter.Observation{
		OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true,
		LogicalSwitchPortPresent: true, PortBindingPresent: true, DatapathPresent: true, ExpectedChassisMatches: true,
	}}
	adapter := &fakeWorkAdapter{observeResult: unknown, reconcileResult: matched, sequence: &store.sequence}
	worker := Worker{Store: store, Adapter: adapter, Owner: "worker-a", BatchLimit: 1, ClaimLease: time.Minute, AdapterArtifactDigest: digest("adapter")}
	completed, err := worker.RunOnce(context.Background())
	if err != nil || completed != 1 {
		t.Fatalf("RunOnce completed=%d err=%v", completed, err)
	}
	want := []string{"claim", "read-back-evidence", "observe", "apply-authorized", "apply", "completed"}
	if fmt.Sprint(store.sequence) != fmt.Sprint(want) || adapter.observeCount != 1 || adapter.reconcileCount != 1 {
		t.Fatalf("sequence=%v observe=%d apply=%d", store.sequence, adapter.observeCount, adapter.reconcileCount)
	}
	if store.completed.NBObservationGeneration != 5 || store.completed.ApplyResponseState != "RECEIVED" || store.claim.ClaimGeneration != 5 {
		t.Fatalf("completion=%#v claim=%#v", store.completed, store.claim)
	}
}

func TestWorkerConvergesFromReadBackWithoutDuplicateApply(t *testing.T) {
	store := &fakeWorkStore{work: []postgres.OVNRuntimeWork{{
		WorkID: "work-1", IntentID: "intent-1", IntentGeneration: 1, PortID: "port-1",
		PortGeneration: 1, BindingGeneration: 1, ObjectSetDigest: digest("plan"),
		ClaimMode: "READ_BACK_FIRST", ClaimGeneration: 2, CanonicalObjectSet: []byte(`{}`),
	}}}
	matched := ovnadapter.RuntimeResult{ApplyResponseState: "UNKNOWN", NBObservationDigest: digest("nb"), SBObservationDigest: digest("sb"), ChassisIdentityDigest: digest("chassis"), Observation: ovnadapter.Observation{
		OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true,
		LogicalSwitchPortPresent: true, PortBindingPresent: true, DatapathPresent: true, ExpectedChassisMatches: true,
	}}
	adapter := &fakeWorkAdapter{observeResult: matched, sequence: &store.sequence}
	worker := Worker{Store: store, Adapter: adapter, Owner: "worker-b", BatchLimit: 1, ClaimLease: time.Minute, AdapterArtifactDigest: digest("adapter")}
	completed, err := worker.RunOnce(context.Background())
	if err != nil || completed != 1 || adapter.reconcileCount != 0 {
		t.Fatalf("RunOnce completed=%d reconcile=%d err=%v", completed, adapter.reconcileCount, err)
	}
	want := []string{"claim", "read-back-evidence", "observe", "completed"}
	if fmt.Sprint(store.sequence) != fmt.Sprint(want) {
		t.Fatalf("sequence=%v", store.sequence)
	}
}

func TestWorkerQuarantinesForeignOVNObject(t *testing.T) {
	store := &fakeWorkStore{work: []postgres.OVNRuntimeWork{{
		WorkID: "work-1", IntentID: "intent-1", IntentGeneration: 1, PortID: "port-1",
		PortGeneration: 1, BindingGeneration: 1, ObjectSetDigest: digest("plan"),
		ClaimMode: "APPLY_ALLOWED", ClaimGeneration: 1, CanonicalObjectSet: []byte(`{}`),
	}}}
	adapter := &fakeWorkAdapter{reconcileErr: ovnadapter.ErrForeignOVNObject, sequence: &store.sequence}
	worker := Worker{Store: store, Adapter: adapter, Owner: "worker-a", BatchLimit: 1, ClaimLease: time.Minute, AdapterArtifactDigest: digest("adapter")}
	completed, err := worker.RunOnce(context.Background())
	if err != nil || completed != 0 {
		t.Fatalf("RunOnce completed=%d err=%v", completed, err)
	}
	want := []string{"claim", "apply-authorized", "apply", "quarantined"}
	if fmt.Sprint(store.sequence) != fmt.Sprint(want) {
		t.Fatalf("sequence=%v", store.sequence)
	}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
