package p1c03ovnwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

var errInjectedRenewalResponseLoss = errors.New("injected OVN claim renewal response loss after commit")

// renewalResponseLossStore commits the first renewal through PostgreSQL and
// discards only its client-visible response. It does not alter DB authority.
type renewalResponseLossStore struct {
	ovnruntime.PostgresWorkStore
	injected atomic.Bool
}

func (store *renewalResponseLossStore) Renew(ctx context.Context, claim postgres.OVNRuntimeClaim, lease time.Duration) (postgres.OVNRuntimeRenewal, error) {
	renewed, err := store.PostgresWorkStore.Renew(ctx, claim, lease)
	if err == nil && store.injected.CompareAndSwap(false, true) {
		return renewed, errInjectedRenewalResponseLoss
	}
	return renewed, err
}

type responseLossAdapter struct {
	applied        atomic.Bool
	applyCount     atomic.Int32
	observationCnt atomic.Int32
	matched        ovnadapter.RuntimeResult
}

func (adapter *responseLossAdapter) ReconcilePort(ctx context.Context, _ []byte, _ string) (ovnadapter.RuntimeResult, error) {
	adapter.applyCount.Add(1)
	adapter.applied.Store(true)
	<-ctx.Done()
	return ovnadapter.RuntimeResult{}, ctx.Err()
}

func (adapter *responseLossAdapter) ObservePort(context.Context, []byte, string) (ovnadapter.RuntimeResult, error) {
	adapter.observationCnt.Add(1)
	if !adapter.applied.Load() {
		return ovnadapter.RuntimeResult{}, errors.New("OVN side effect is not observable")
	}
	return adapter.matched, nil
}
func (adapter *responseLossAdapter) ObservePortBindingRetirement(ctx context.Context, raw []byte, digest string) (ovnadapter.RuntimeResult, error) {
	return adapter.ObservePort(ctx, raw, digest)
}
func (adapter *responseLossAdapter) RetirePortBinding(ctx context.Context, raw []byte, digest string) (ovnadapter.RuntimeResult, error) {
	return adapter.ReconcilePort(ctx, raw, digest)
}

func TestOVNRuntimeClaimRenewalResponseLossConvergence(t *testing.T) {
	if os.Getenv("KIM_RUN_DOCKER_POSTGRES_RENEWAL_RESPONSE_LOSS") != "1" {
		t.Skip("KIM_RUN_DOCKER_POSTGRES_RENEWAL_RESPONSE_LOSS is not enabled")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	container := startRenewalResponseLossPostgreSQL(t, ctx)
	databaseURL := postgresContainerURL(t, ctx, container)
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("renewal-loss-%d", time.Now().UnixNano())
	ids := seedOVNAuthority(t, ctx, pool, suffix)
	decision, err := postgres.CommitOVNPortIntent(ctx, pool, postgres.OVNPortIntentRequest{
		IntentID: ids.intentID, IntentGeneration: 1, PortID: ids.portID,
	})
	if err != nil {
		t.Fatal(err)
	}
	matched := ovnadapter.RuntimeResult{
		ApplyResponseState:  "UNKNOWN",
		NBObservationDigest: digest("renewal-loss-nb"), SBObservationDigest: digest("renewal-loss-sb"),
		ChassisIdentityDigest: digest("renewal-loss-chassis"),
		Observation: ovnadapter.Observation{
			OwnershipMarkerMatches: true, ObjectSetDigestMatches: true,
			LogicalSwitchPresent: true, LogicalSwitchPortPresent: true,
			PortBindingPresent: true, DatapathPresent: true, ExpectedChassisMatches: true,
		},
	}
	adapter := &responseLossAdapter{matched: matched}
	storeA := &renewalResponseLossStore{PostgresWorkStore: ovnruntime.PostgresWorkStore{DB: pool}}
	workerA := ovnruntime.Worker{
		Store: storeA, Adapter: adapter, Owner: "ovn-renewal-loss-worker-a", BatchLimit: 1,
		ClaimLease: 180 * time.Millisecond, ClaimMaximumLifetime: 700 * time.Millisecond,
		ClaimRenewInterval: 50 * time.Millisecond, AdapterArtifactDigest: digest("qualified-ovn-adapter"),
	}
	completed, runErr := workerA.RunOnce(ctx)
	if completed != 0 || !errors.Is(runErr, errInjectedRenewalResponseLoss) || !storeA.injected.Load() || adapter.applyCount.Load() != 1 {
		t.Fatalf("worker A completed=%d injected=%t applies=%d err=%v", completed, storeA.injected.Load(), adapter.applyCount.Load(), runErr)
	}

	workID := fmt.Sprintf("ovn-runtime:%s:1", ids.intentID)
	var initialExpiry, renewedExpiry, maximumExpiry time.Time
	var renewalGeneration int64
	if err := pool.QueryRow(ctx, `SELECT attempt.lease_expires_at,current.claim_expires_at,current.claim_maximum_expires_at,current.last_renewal_generation
		FROM kim.ovn_runtime_work_current current
		JOIN kim.ovn_runtime_work_attempt_evidence attempt ON attempt.work_id=current.work_id AND attempt.claim_generation=1
		WHERE current.work_id=$1`, workID).Scan(&initialExpiry, &renewedExpiry, &maximumExpiry, &renewalGeneration); err != nil {
		t.Fatal(err)
	}
	if renewalGeneration != 1 || !renewedExpiry.After(initialExpiry) || renewedExpiry.After(maximumExpiry) {
		t.Fatalf("renewal generation=%d initial=%s renewed=%s maximum=%s", renewalGeneration, initialExpiry, renewedExpiry, maximumExpiry)
	}
	var renewalEvidence int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_renewal_evidence
		WHERE work_id=$1 AND claim_generation=1 AND renewal_generation=1`, workID).Scan(&renewalEvidence); err != nil || renewalEvidence != 1 {
		t.Fatalf("committed renewal evidence=%d err=%v", renewalEvidence, err)
	}

	workerB := ovnruntime.Worker{
		Store: ovnruntime.PostgresWorkStore{DB: pool}, Adapter: adapter,
		Owner: "ovn-renewal-loss-worker-b", BatchLimit: 1,
		ClaimLease: 300 * time.Millisecond, ClaimMaximumLifetime: 300 * time.Millisecond,
		AdapterArtifactDigest: digest("qualified-ovn-adapter"),
	}
	if earlyCompleted, err := workerB.RunOnce(ctx); err != nil || earlyCompleted != 0 {
		t.Fatalf("renewed claim allowed early takeover completed=%d err=%v", earlyCompleted, err)
	}
	wait := time.Until(renewedExpiry) + 25*time.Millisecond
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	completed, err = workerB.RunOnce(ctx)
	if err != nil || completed != 1 {
		t.Fatalf("worker B read-back convergence completed=%d err=%v", completed, err)
	}
	if adapter.applyCount.Load() != 1 || adapter.observationCnt.Load() != 1 {
		t.Fatalf("physical applies=%d observations=%d", adapter.applyCount.Load(), adapter.observationCnt.Load())
	}

	stale := postgres.OVNRuntimeClaim{WorkID: workID, Owner: "ovn-renewal-loss-worker-a", ClaimGeneration: 1}
	if err := postgres.AuthorizeOVNRuntimeApply(ctx, pool, stale); !errors.Is(err, postgres.ErrStaleOVNRuntimeClaim) {
		t.Fatalf("old claim retained authority after recovery: %v", err)
	}
	var state, owner, mode string
	var generation, attempts, unknownEvents, readBackEvents, applyEvents int64
	if err := pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, workID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT claim_owner,claim_generation,claim_mode FROM kim.ovn_runtime_work_attempt_evidence
		WHERE work_id=$1 ORDER BY claim_generation DESC LIMIT 1`, workID).Scan(&owner, &generation, &mode); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.ovn_runtime_work_attempt_evidence WHERE work_id=$1`, workID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE event_type='DISPATCH_UNKNOWN'),
		count(*) FILTER (WHERE event_type='READ_BACK_STARTED'),
		count(*) FILTER (WHERE event_type='APPLY_AUTHORIZED')
		FROM kim.ovn_runtime_work_event_evidence WHERE work_id=$1`, workID).Scan(&unknownEvents, &readBackEvents, &applyEvents); err != nil {
		t.Fatal(err)
	}
	if state != "OBSERVED" || owner != "ovn-renewal-loss-worker-b" || generation != 2 || mode != "READ_BACK_FIRST" || attempts != 2 || unknownEvents != 1 || readBackEvents != 1 || applyEvents != 1 {
		t.Fatalf("state=%s recovery=%s/%d/%s attempts=%d unknown=%d readback=%d apply-events=%d plan=%s", state, owner, generation, mode, attempts, unknownEvents, readBackEvents, applyEvents, decision.ObjectSetDigest)
	}
}

func startRenewalResponseLossPostgreSQL(t *testing.T, ctx context.Context) string {
	t.Helper()
	name := fmt.Sprintf("kim-ovn-renewal-loss-%d", time.Now().UnixNano())
	dockerMust(t, ctx, "run", "-d", "--name", name,
		"-e", "POSTGRES_PASSWORD=kimtest", "-e", "POSTGRES_DB=kimtest",
		"-p", "127.0.0.1::5432", "postgres:17")
	t.Cleanup(func() { _, _ = dockerOutput(context.Background(), "rm", "-f", name) })
	waitDockerPostgreSQL(t, ctx, name)
	return name
}
