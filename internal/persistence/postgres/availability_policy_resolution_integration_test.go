package postgres

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestAvailabilityPolicyResolutionConflictAndConcurrencyPostgreSQLIntegration(t *testing.T) {
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
		VALUES('availability-resolution',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	host := "availability-resolution-host-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, host); err != nil {
		t.Fatal(err)
	}
	groups := []string{"availability-resolution-a-" + suffix, "availability-resolution-b-" + suffix}
	for _, group := range groups {
		if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: group, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "placement", PolicyGeneration: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "set-" + group,
			HostGroupID: group, SourceType: "EXPLICIT", SourceRevision: "fixture", BasedOnHostGroupGeneration: 1,
			Members: upgradeSnapshotMembers(group, []string{host}, 1, "fixture")}); err != nil {
			t.Fatal(err)
		}
	}
	p1 := availabilityPolicyFixture("availability-resolution-policy-a-"+suffix, 1, "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION", "ACTIVE")
	p2 := availabilityPolicyFixture("availability-resolution-policy-b-"+suffix, 1, "MANUAL", "NO_AUTOMATIC_ACTION", "ACTIVE")
	d1, err := PublishAvailabilityPolicy(ctx, pool, p1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := PublishAvailabilityPolicy(ctx, pool, p2)
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]GroupPolicyBinding, 2)
	for i, policy := range []struct {
		p AvailabilityPolicyRevision
		d string
	}{{p1, d1}, {p2, d2}} {
		bindings[i], err = PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: fmt.Sprintf("availability-resolution-bind-%d-%s", i, suffix),
			BindingID: fmt.Sprintf("availability-resolution-binding-%d-%s", i, suffix), HostGroupID: groups[i], HostGroupGeneration: 1,
			PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: policy.p.PolicyID,
			PolicyRevision: 1, PolicyDigest: policy.d, Priority: 100, LifecycleState: "ACTIVE"})
		if err != nil {
			t.Fatal(err)
		}
	}
	conflict, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "availability-conflict-" + suffix,
		HostID: host, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT"})
	if err != nil || conflict.Result != "ASSIGNMENT_CONFLICT" {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "availability-resolution-equivalent-" + suffix,
		BindingID: bindings[1].BindingID, ExpectedCurrentGeneration: 1, HostGroupID: groups[1], HostGroupGeneration: 1,
		PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: p1.PolicyID,
		PolicyRevision: 1, PolicyDigest: d1, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	equivalent, err := ResolveGroupPolicy(ctx, pool, GroupPolicyResolutionRequest{ResolutionID: "availability-equivalent-" + suffix,
		HostID: host, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT"})
	if err != nil || equivalent.Result != "RESOLVED" || equivalent.EffectivePolicyID != p1.PolicyID {
		t.Fatalf("equivalent=%+v err=%v", equivalent, err)
	}

	// Competing publishers for one exact revision serialize; no mixed revision is accepted.
	concurrentID := "availability-concurrent-" + suffix
	policies := []AvailabilityPolicyRevision{
		availabilityPolicyFixture(concurrentID, 1, "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION", "ACTIVE"),
		availabilityPolicyFixture(concurrentID, 1, "INFRASTRUCTURE_MANAGED", "EVACUATE", "ACTIVE"),
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range policies {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = PublishAvailabilityPolicy(ctx, pool, policies[i]) }(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("competing publisher successes=%d errors=%v", successes, errs)
	}
	var evidenceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.availability_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=1`, concurrentID).Scan(&evidenceCount); err != nil || evidenceCount != 1 {
		t.Fatalf("concurrent evidence=%d err=%v", evidenceCount, err)
	}
}
