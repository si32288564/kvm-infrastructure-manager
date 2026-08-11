package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestHostGroupAuthorityAndSnapshotPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch,authority_generation,mode)
		VALUES ('host-group-test',1,'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	hostA, hostB := "host-group-a-"+suffix, "host-group-b-"+suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED'),($2,'APPROVED')`, hostA, hostB); err != nil {
		t.Fatal(err)
	}
	poolID, rackID, cohortID := "pool-"+suffix, "rack-"+suffix, "cohort-"+suffix
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{
		PoolID: poolID, PoolGeneration: 1, LifecycleState: "ACTIVE",
		PolicyID: "placement-policy", PolicyGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []HostGroupRevision{
		{HostGroupID: rackID, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: "physical-location", Level: "rack", LifecycleState: "ACTIVE"},
		{HostGroupID: cohortID, Generation: 1, GroupType: "OPERATIONAL_COHORT", Dimension: "baseline-ring", Level: "ring", LifecycleState: "ACTIVE"},
	} {
		if err := UpsertHostGroup(ctx, pool, revision); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: rackID, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: "power-path", Level: "feed", LifecycleState: "ACTIVE"}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("same-generation HostGroup semantic conflict = %v", err)
	}

	if err := AssignHostPlacementPool(ctx, pool, HostPlacementMembership{HostID: hostA, PoolID: poolID, Generation: 1, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	for _, membership := range []HostGroupMembership{
		{HostGroupID: rackID, HostID: hostA, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: "fixture-1"},
		{HostGroupID: cohortID, HostID: hostA, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: "fixture-1"},
		{HostGroupID: rackID, HostID: hostB, Generation: 1, State: "ACTIVE", SourceType: "EXTERNAL_ASSERTION", SourceRevision: "cmdb-1"},
	} {
		if err := AssignHostGroupMembership(ctx, pool, membership); err != nil {
			t.Fatal(err)
		}
	}

	var memberships int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_group_memberships_current WHERE host_id=$1 AND membership_state='ACTIVE'`, hostA).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 3 {
		t.Fatalf("Host A active HostGroup memberships = %d, want 3", memberships)
	}
	var groupType string
	if err := pool.QueryRow(ctx, `SELECT group_type FROM kim.host_groups_current WHERE host_group_id=$1`, poolID).Scan(&groupType); err != nil {
		t.Fatal(err)
	}
	if groupType != "PLACEMENT_POOL" {
		t.Fatalf("Placement Pool compatibility HostGroup type = %q", groupType)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: cohortID, Generation: 2, GroupType: "OPERATIONAL_COHORT", Dimension: "baseline-ring", Level: "ring", LifecycleState: "DRAINING"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{SnapshotID: "draining-snapshot-" + suffix, HostGroupID: cohortID, Purpose: "MAINTENANCE"}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("DRAINING HostGroup new snapshot error = %v", err)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: cohortID, Generation: 3, GroupType: "OPERATIONAL_COHORT", Dimension: "baseline-ring", Level: "ring", LifecycleState: "DRAFT"}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("non-monotonic HostGroup lifecycle transition error = %v", err)
	}

	snapshotRequest := HostGroupSnapshotRequest{SnapshotID: "snapshot-" + suffix, HostGroupID: rackID, Purpose: "UPGRADE"}
	snapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, snapshotRequest)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.MemberCount != 2 || snapshot.MembershipDigest == "" {
		t.Fatalf("HostGroup snapshot = %#v", snapshot)
	}
	parallelSnapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "snapshot-parallel-" + suffix, HostGroupID: rackID, Purpose: "UPGRADE",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parallelSnapshot.MembershipDigest != snapshot.MembershipDigest {
		t.Fatalf("same member set produced different digest: first=%s parallel=%s", snapshot.MembershipDigest, parallelSnapshot.MembershipDigest)
	}
	if err := AssignHostGroupMembership(ctx, pool, HostGroupMembership{
		HostGroupID: rackID, HostID: hostB, Generation: 2, State: "REMOVED",
		SourceType: "EXTERNAL_ASSERTION", SourceRevision: "cmdb-2",
	}); err != nil {
		t.Fatal(err)
	}
	replayed, err := CreateHostGroupMembershipSnapshot(ctx, pool, snapshotRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != snapshot {
		t.Fatalf("snapshot replay changed immutable target: first=%#v replay=%#v", snapshot, replayed)
	}
	var snapshotMembers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_group_membership_snapshot_members WHERE snapshot_id=$1`, snapshot.SnapshotID).Scan(&snapshotMembers); err != nil {
		t.Fatal(err)
	}
	if snapshotMembers != 2 {
		t.Fatalf("immutable snapshot members = %d, want 2", snapshotMembers)
	}

	if _, err := pool.Exec(ctx, `UPDATE kim.host_group_membership_evidence SET membership_state='BLOCKED' WHERE host_group_id=$1 AND host_id=$2 AND membership_generation=1`, rackID, hostA); err == nil {
		t.Fatal("immutable HostGroup membership evidence UPDATE unexpectedly succeeded")
	}
	if err := AssignHostGroupMembership(ctx, pool, HostGroupMembership{
		HostGroupID: rackID, HostID: hostA, Generation: 1, State: "BLOCKED",
		SourceType: "EXPLICIT", SourceRevision: "fixture-1",
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("same-generation membership semantic conflict = %v", err)
	}
}

func TestHostGroupMembershipSetAuthorityPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.database_authority SET mode='ACTIVE' WHERE singleton`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	groupID := "set-group-" + suffix
	hosts := []string{"set-a-" + suffix, "set-b-" + suffix, "set-c-" + suffix, "set-d-" + suffix}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED'),($2,'APPROVED'),($3,'APPROVED'),($4,'APPROVED')`, hosts[0], hosts[1], hosts[2], hosts[3]); err != nil {
		t.Fatal(err)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: "physical-location", Level: "rack", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	emptyGroupID := "empty-set-group-" + suffix
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: emptyGroupID, Generation: 1, GroupType: "OPERATIONAL_COHORT", Dimension: "empty", Level: "cohort", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	emptySet, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "empty-set-request-" + suffix, HostGroupID: emptyGroupID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 0,
		SourceType: "EXPLICIT", SourceRevision: "empty-source-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if emptySet.MembershipSetGeneration != 1 || emptySet.MemberCount != 0 {
		t.Fatalf("empty accepted set = %#v", emptySet)
	}
	assertCurrentMembershipSet(t, ctx, pool, emptyGroupID, 1, 0)

	member := func(host string, generation uint64) HostGroupMembership {
		return HostGroupMembership{HostGroupID: groupID, HostID: host, Generation: generation, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: "operator-1"}
	}
	set1Request := HostGroupMembershipSetRequest{
		PublishRequestID: "set-request-1-" + suffix, HostGroupID: groupID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 0,
		SourceType: "EXPLICIT", SourceRevision: "set-source-1",
		Members: []HostGroupMembership{member(hosts[2], 1), member(hosts[0], 1), member(hosts[1], 1)},
	}
	set1, err := PublishHostGroupMembershipSet(ctx, pool, set1Request)
	if err != nil {
		t.Fatal(err)
	}
	if set1.MembershipSetGeneration != 1 || set1.MemberCount != 3 || set1.CanonicalMemberSetDigest == "" {
		t.Fatalf("set 1 = %#v", set1)
	}
	assertCurrentMembershipSet(t, ctx, pool, groupID, 1, 3)
	replayed, err := PublishHostGroupMembershipSet(ctx, pool, set1Request)
	if err != nil || replayed != set1 {
		t.Fatalf("set replay = %#v/%v, want %#v", replayed, err, set1)
	}
	identical := set1Request
	identical.PublishRequestID = "set-request-identical-" + suffix
	if got, err := PublishHostGroupMembershipSet(ctx, pool, identical); err != nil || got.MembershipSetGeneration != 1 {
		t.Fatalf("semantic replay amplified generation: %#v/%v", got, err)
	}
	conflicting := set1Request
	conflicting.Members = conflicting.Members[:2]
	if _, err := PublishHostGroupMembershipSet(ctx, pool, conflicting); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("same request identity conflict = %v", err)
	}

	failed := HostGroupMembershipSetRequest{
		PublishRequestID: "set-request-failed-" + suffix, HostGroupID: groupID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 1,
		SourceType: "EXPLICIT", SourceRevision: "set-source-failed",
		Members: []HostGroupMembership{{HostGroupID: groupID, HostID: "missing-" + suffix, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: "operator-1"}},
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, failed); err == nil {
		t.Fatal("invalid complete-set transaction unexpectedly committed")
	}
	assertCurrentMembershipSet(t, ctx, pool, groupID, 1, 3)
	var failedEvidence int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_group_membership_set_evidence WHERE publish_request_id=$1`, failed.PublishRequestID).Scan(&failedEvidence); err != nil || failedEvidence != 0 {
		t.Fatalf("failed transaction evidence = %d/%v", failedEvidence, err)
	}

	set2Request := HostGroupMembershipSetRequest{
		PublishRequestID: "set-request-2-" + suffix, HostGroupID: groupID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 1,
		SourceType: "EXPLICIT", SourceRevision: "set-source-2",
		Members: []HostGroupMembership{member(hosts[0], 1), member(hosts[2], 1), member(hosts[3], 1)},
	}
	set2, err := PublishHostGroupMembershipSet(ctx, pool, set2Request)
	if err != nil {
		t.Fatal(err)
	}
	if set2.MembershipSetGeneration != 2 || set2.MemberCount != 3 || set2.CanonicalMemberSetDigest == set1.CanonicalMemberSetDigest {
		t.Fatalf("set 2 = %#v", set2)
	}
	assertCurrentMembershipSet(t, ctx, pool, groupID, 2, 3)
	var removedState string
	if err := pool.QueryRow(ctx, `SELECT membership_state FROM kim.host_group_memberships_current WHERE host_group_id=$1 AND host_id=$2`, groupID, hosts[1]).Scan(&removedState); err != nil || removedState != "REMOVED" {
		t.Fatalf("removed member projection = %q/%v", removedState, err)
	}
	var historicalCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_group_membership_set_member_evidence WHERE host_group_id=$1 AND membership_set_generation=1`, groupID).Scan(&historicalCount); err != nil || historicalCount != 3 {
		t.Fatalf("immutable set 1 members = %d/%v", historicalCount, err)
	}

	snapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{SnapshotID: "set-snapshot-" + suffix, HostGroupID: groupID, Purpose: "UPGRADE"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.MembershipSetGeneration != 2 || snapshot.MembershipDigest != set2.CanonicalMemberSetDigest || snapshot.MemberCount != 3 {
		t.Fatalf("set-backed snapshot = %#v", snapshot)
	}

	left := set2Request
	left.PublishRequestID = "set-race-left-" + suffix
	left.ExpectedCurrentSetGeneration = 2
	left.SourceRevision = "race-left"
	left.Members[0] = member(hosts[0], 2)
	right := set2Request
	right.PublishRequestID = "set-race-right-" + suffix
	right.ExpectedCurrentSetGeneration = 2
	right.SourceRevision = "race-right"
	right.Members[0] = member(hosts[0], 2)
	results := make(chan error, 2)
	go func() { _, err := PublishHostGroupMembershipSet(ctx, pool, left); results <- err }()
	go func() { _, err := PublishHostGroupMembershipSet(ctx, pool, right); results <- err }()
	succeeded, conflicted := 0, 0
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		} else if errors.Is(err, ErrHostGroupConflict) {
			conflicted++
		} else {
			t.Fatalf("parallel publish error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		staleMember := set2Request
		staleMember.PublishRequestID = "set-stale-member-" + suffix
		staleMember.ExpectedCurrentSetGeneration = 3
		staleMember.SourceRevision = "stale-member"
		staleMember.Members[0] = member(hosts[0], 1)
		if _, err := PublishHostGroupMembershipSet(ctx, pool, staleMember); !errors.Is(err, ErrHostGroupConflict) {
			t.Fatalf("stale individual member generation publish = %v", err)
		}
		assertCurrentMembershipSet(t, ctx, pool, groupID, 3, 3)

		t.Fatalf("parallel publish outcomes success=%d conflict=%d", succeeded, conflicted)
	}
	assertCurrentMembershipSet(t, ctx, pool, groupID, 3, 3)
	if _, err := pool.Exec(ctx, `UPDATE kim.host_group_memberships_current SET membership_generation=1 WHERE host_group_id=$1 AND host_id=$2`, groupID, hosts[0]); err == nil {
		t.Fatal("current member projection escaped accepted set-member evidence")
	}

	replayedSnapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{SnapshotID: snapshot.SnapshotID, HostGroupID: groupID, Purpose: "UPGRADE"})
	if err != nil || replayedSnapshot != snapshot {
		t.Fatalf("snapshot changed after set switch: %#v/%v", replayedSnapshot, err)
	}

	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 2, GroupType: "FAILURE_DOMAIN", Dimension: "physical-location", Level: "rack", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if recovered, err := PublishHostGroupMembershipSet(ctx, pool, set1Request); err != nil || recovered != set1 {
		t.Fatalf("committed response-loss replay after Group generation change = %#v/%v", recovered, err)
	}

	stale := left
	stale.PublishRequestID = "set-stale-group-" + suffix
	stale.ExpectedCurrentSetGeneration = 3
	if _, err := PublishHostGroupMembershipSet(ctx, pool, stale); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("stale HostGroup generation publish = %v", err)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 3, GroupType: "FAILURE_DOMAIN", Dimension: "physical-location", Level: "rack", LifecycleState: "DRAINING"}); err != nil {
		t.Fatal(err)
	}
	draining := stale
	draining.PublishRequestID = "set-draining-" + suffix
	draining.BasedOnHostGroupGeneration = 3
	if _, err := PublishHostGroupMembershipSet(ctx, pool, draining); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("DRAINING HostGroup publish = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.host_group_membership_set_evidence SET member_count=99 WHERE host_group_id=$1 AND membership_set_generation=1`, groupID); err == nil {
		t.Fatal("immutable membership set evidence UPDATE unexpectedly succeeded")
	}
}

func assertCurrentMembershipSet(t *testing.T, ctx context.Context, pool QueryRower, groupID string, wantGeneration uint64, wantMembers int) {
	t.Helper()
	var generation uint64
	var memberCount, mismatchedProjection int
	if err := pool.QueryRow(ctx, `SELECT membership_set_generation,member_count FROM kim.host_group_membership_sets_current WHERE host_group_id=$1`, groupID).Scan(&generation, &memberCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_group_memberships_current WHERE host_group_id=$1 AND membership_set_generation<>$2`, groupID, wantGeneration).Scan(&mismatchedProjection); err != nil {
		t.Fatal(err)
	}
	if generation != wantGeneration || memberCount != wantMembers || mismatchedProjection != 0 {
		t.Fatalf("current set generation=%d members=%d mismatched=%d, want %d/%d/0", generation, memberCount, mismatchedProjection, wantGeneration, wantMembers)
	}
}
