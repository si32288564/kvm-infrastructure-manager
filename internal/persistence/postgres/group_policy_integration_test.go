package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestGroupPolicyBindingPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES('group-policy-binding',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	host := "group-policy-host-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, host); err != nil {
		t.Fatal(err)
	}
	g1, g2 := "policy-group-a-"+suffix, "policy-group-b-"+suffix
	for _, g := range []string{g1, g2} {
		if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: g, Generation: 1, GroupType: "OPERATIONAL_COHORT", Dimension: "maintenance-ring", Level: "ring", LifecycleState: "ACTIVE"}); err != nil {
			t.Fatal(err)
		}
		if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "set-" + g, HostGroupID: g, SourceType: "EXPLICIT", SourceRevision: "operator", BasedOnHostGroupGeneration: 1, Members: upgradeSnapshotMembers(g, []string{host}, 1, "operator")}); err != nil {
			t.Fatal(err)
		}
	}
	p1 := MaintenancePolicyRevision{PolicyID: "policy-a-" + suffix, PolicyRevision: 1, OperationType: "HOST_DRAIN", OperationSchemaVersion: "v1", ProfileID: "safe-drain", ProfileRevision: 1, ProfileDigest: digestReleaseBytes([]byte("safe-drain/v1")), MaximumConcurrent: 1, FailureDomainMaximumUnavailable: 1, LifecycleState: "ACTIVE"}
	p2 := p1
	p2.PolicyID = "policy-b-" + suffix
	p2.ProfileID = "strict-drain"
	p2.ProfileDigest = digestReleaseBytes([]byte("strict-drain/v1"))
	d1, err := PublishMaintenancePolicy(ctx, pool, p1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := PublishMaintenancePolicy(ctx, pool, p2)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "bind-a-1-" + suffix, BindingID: "bind-a-" + suffix, HostGroupID: g1, HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p1.PolicyID, PolicyRevision: 1, PolicyDigest: d1, Priority: 100, LifecycleState: "ACTIVE"})
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "bind-a-1-" + suffix, BindingID: "bind-a-" + suffix, HostGroupID: g1, HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p1.PolicyID, PolicyRevision: 1, PolicyDigest: d1, Priority: 100, LifecycleState: "ACTIVE"}); err != nil || replay.BindingGeneration != b1.BindingGeneration {
		t.Fatalf("binding replay=%+v err=%v", replay, err)
	}
	r1, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "resolution-basic-" + suffix, HostID: host, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
	if err != nil || r1.Result != "RESOLVED" || r1.EffectivePolicyID != p1.PolicyID {
		t.Fatalf("basic resolution=%+v err=%v", r1, err)
	}
	if unsupported, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "resolution-unsupported-" + suffix, HostID: host, PolicyType: "AVAILABILITY", ConsumerType: "MAINTENANCE_PLAN"}); err != nil || unsupported.Result != "UNSUPPORTED" {
		t.Fatalf("unsupported=%+v err=%v", unsupported, err)
	}

	// Higher numeric priority wins across many-to-many membership.
	b2, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "bind-b-1-" + suffix, BindingID: "bind-b-" + suffix, HostGroupID: g2, HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p2.PolicyID, PolicyRevision: 1, PolicyDigest: d2, Priority: 200, LifecycleState: "ACTIVE"})
	if err != nil {
		t.Fatal(err)
	}
	high, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "resolution-high-" + suffix, HostID: host, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
	if err != nil || high.EffectivePolicyID != p2.PolicyID {
		t.Fatalf("priority=%+v err=%v", high, err)
	}
	// Equal priority and incompatible exact identities conflict.
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "bind-b-2-" + suffix, BindingID: b2.BindingID, ExpectedCurrentGeneration: 1, HostGroupID: g2, HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p2.PolicyID, PolicyRevision: 1, PolicyDigest: d2, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	conflict, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "resolution-conflict-" + suffix, HostID: host, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
	if err != nil || conflict.Result != "ASSIGNMENT_CONFLICT" {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	maintenanceSnapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "policy-maintenance-snapshot-" + suffix, HostGroupID: g1, Purpose: "MAINTENANCE"})
	if err != nil {
		t.Fatal(err)
	}
	maintenanceRequest := maintenancePlanFixture("policy-conflict-maintenance-"+suffix, maintenanceSnapshot)
	if _, err := PublishMaintenancePlan(ctx, pool, maintenanceRequest); !errors.Is(err, ErrMaintenanceEvidenceConflict) {
		t.Fatalf("conflicting policy advanced Maintenance authority: %v", err)
	}

	// Exact same policy identity at equal priority is equivalent.
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "bind-b-3-" + suffix, BindingID: b2.BindingID, ExpectedCurrentGeneration: 2, HostGroupID: g2, HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p1.PolicyID, PolicyRevision: 1, PolicyDigest: d1, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	equivalent, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "resolution-equivalent-" + suffix, HostID: host, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
	if err != nil || equivalent.Result != "RESOLVED" || equivalent.EffectivePolicyID != p1.PolicyID {
		t.Fatalf("equivalent=%+v err=%v", equivalent, err)
	}
	acceptedPlan, err := PublishMaintenancePlan(ctx, pool, maintenanceRequest)
	if err != nil {
		t.Fatal(err)
	}
	var planPolicyRevision uint64
	if err := pool.QueryRow(ctx, `SELECT effective_policy_revision
		FROM kim.maintenance_plan_policy_resolution_evidence
		WHERE maintenance_id=$1 AND plan_revision=$2 AND host_id=$3`, maintenanceRequest.MaintenanceID,
		maintenanceRequest.PlanRevision, host).Scan(&planPolicyRevision); err != nil || planPolicyRevision != 1 {
		t.Fatalf("plan policy revision=%d err=%v", planPolicyRevision, err)
	}
	_ = acceptedPlan

	// Advancing the Policy pointer does not rewrite B1 or historical R1; it
	// makes the highest intended assignment stale until an explicit rebind.
	p1v2 := p1
	p1v2.PolicyRevision = 2
	p1v2.ProfileRevision = 2
	p1v2.ProfileDigest = digestReleaseBytes([]byte("safe-drain/v2"))
	d1v2, err := PublishMaintenancePolicy(ctx, pool, p1v2)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "resolution-stale-" + suffix, HostID: host, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
	if err != nil || stale.Result != "STALE_ASSIGNMENT" {
		t.Fatalf("stale=%+v err=%v", stale, err)
	}
	var historicalPolicy string
	if err := pool.QueryRow(ctx, `SELECT effective_policy_id FROM kim.host_group_policy_resolution_evidence WHERE resolution_id=$1`, r1.ResolutionID).Scan(&historicalPolicy); err != nil || historicalPolicy != p1.PolicyID {
		t.Fatalf("historical policy=%s err=%v", historicalPolicy, err)
	}
	if err := pool.QueryRow(ctx, `SELECT effective_policy_revision
		FROM kim.maintenance_plan_policy_resolution_evidence
		WHERE maintenance_id=$1 AND plan_revision=$2 AND host_id=$3`, maintenanceRequest.MaintenanceID,
		maintenanceRequest.PlanRevision, host).Scan(&planPolicyRevision); err != nil || planPolicyRevision != 1 {
		t.Fatalf("live policy drift rewrote plan revision=%d err=%v", planPolicyRevision, err)
	}
	if replay, err := PublishMaintenancePlan(ctx, pool, maintenanceRequest); err != nil || replay.PlanDigest != acceptedPlan.PlanDigest {
		t.Fatalf("post-drift response-loss replay=%+v err=%v", replay, err)
	}
	for i, b := range []GroupPolicyBinding{b1, {BindingID: b2.BindingID, BindingGeneration: 3}} {
		if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: fmt.Sprintf("rebind-v2-%d-%s", i, suffix), BindingID: b.BindingID, ExpectedCurrentGeneration: b.BindingGeneration, HostGroupID: []string{g1, g2}[i], HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p1.PolicyID, PolicyRevision: 2, PolicyDigest: d1v2, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
			t.Fatal(err)
		}
	}
	resolvedV2, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "resolution-v2-" + suffix, HostID: host, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
	if err != nil || resolvedV2.EffectivePolicyRevision != 2 {
		t.Fatalf("v2=%+v err=%v", resolvedV2, err)
	}

	// Concurrent publishers for one expected generation leave one winner.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, policy := range []MaintenancePolicyRevision{p1v2, p2} {
		wg.Add(1)
		go func(i int, p MaintenancePolicyRevision) {
			defer wg.Done()
			digest := d1v2
			if p.PolicyID == p2.PolicyID {
				digest = d2
			}
			_, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: fmt.Sprintf("race-%d-%s", i, suffix), BindingID: "race-binding-" + suffix, HostGroupID: g1, HostGroupGeneration: 1, PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p.PolicyID, PolicyRevision: p.PolicyRevision, PolicyDigest: digest, Priority: 50, LifecycleState: "ACTIVE"})
			results <- err
		}(i, policy)
	}
	wg.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrGroupPolicyConflict) {
			failures++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("binding race success=%d conflict=%d", successes, failures)
	}

	// Resolver and binding update serialize on current rows. The accepted
	// resolution contains one complete old or new binding generation.
	resolveDone := make(chan GroupPolicyResolution, 1)
	resolveErr := make(chan error, 1)
	go func() {
		r, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{
			ResolutionID: "resolution-update-race-" + suffix, HostID: host,
			PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
		resolveDone <- r
		resolveErr <- err
	}()
	_, updateErr := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{
		PublishRequestID: "bind-a-update-race-" + suffix, BindingID: b1.BindingID,
		ExpectedCurrentGeneration: 2, HostGroupID: g1, HostGroupGeneration: 1,
		PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN", PolicyID: p1.PolicyID,
		PolicyRevision: 2, PolicyDigest: d1v2, Priority: 150, LifecycleState: "ACTIVE"})
	traced := <-resolveDone
	if err := <-resolveErr; err != nil || updateErr != nil || traced.Result != "RESOLVED" {
		t.Fatalf("resolver/update race resolution=%+v resolveErr=%v updateErr=%v", traced, err, updateErr)
	}
	var racedGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT binding_generation
		FROM kim.host_group_policy_resolution_input_evidence
		WHERE resolution_id=$1 AND binding_id=$2`, traced.ResolutionID, b1.BindingID).Scan(&racedGeneration); err != nil ||
		(racedGeneration != 2 && racedGeneration != 3) {
		t.Fatalf("mixed binding generation=%d err=%v", racedGeneration, err)
	}

	// Membership drift changes only new resolutions. Historical input evidence
	// retains both Group assignments.
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "remove-from-g2-" + suffix, HostGroupID: g2, SourceType: "EXPLICIT",
		SourceRevision: "operator-remove", BasedOnHostGroupGeneration: 1,
		ExpectedCurrentSetGeneration: 1, Members: nil}); err != nil {
		t.Fatal(err)
	}
	afterDrift, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{
		ResolutionID: "resolution-after-membership-drift-" + suffix, HostID: host,
		PolicyType: "MAINTENANCE", ConsumerType: "MAINTENANCE_PLAN"})
	if err != nil || afterDrift.Result != "RESOLVED" {
		t.Fatalf("membership drift=%+v err=%v", afterDrift, err)
	}
	var historicalInputs, currentInputs int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(DISTINCT host_group_id) FROM kim.host_group_policy_resolution_input_evidence WHERE resolution_id=$1),
		(SELECT count(DISTINCT host_group_id) FROM kim.host_group_policy_resolution_input_evidence WHERE resolution_id=$2)`,
		equivalent.ResolutionID, afterDrift.ResolutionID).Scan(&historicalInputs, &currentInputs); err != nil || historicalInputs != 2 || currentInputs != 1 {
		t.Fatalf("membership evidence historical=%d current=%d err=%v", historicalInputs, currentInputs, err)
	}
}
