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
