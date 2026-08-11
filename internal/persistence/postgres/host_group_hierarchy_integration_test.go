package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestHostGroupHierarchyAuthorityPostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch,authority_generation,mode)
		VALUES ('host-group-hierarchy-test',1,'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	dimension := "hierarchy-location-" + suffix
	hierarchyID := "hierarchy-" + suffix
	hostID := "hierarchy-host-" + suffix
	siteID := "site-" + suffix
	rackAID := "rack-a-" + suffix
	rackBID := "rack-b-" + suffix
	chassisID := "chassis-" + suffix
	if _, err := pool.Exec(ctx,
		`INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, hostID); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []HostGroupRevision{
		{HostGroupID: siteID, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "site", LifecycleState: "ACTIVE"},
		{HostGroupID: rackAID, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE"},
		{HostGroupID: rackBID, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE"},
		{HostGroupID: chassisID, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "chassis", LifecycleState: "ACTIVE"},
	} {
		if err := UpsertHostGroup(ctx, pool, revision); err != nil {
			t.Fatal(err)
		}
	}

	preHierarchySet, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "pre-hierarchy-set-" + suffix, HostGroupID: chassisID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 0,
		SourceType: "EXPLICIT", SourceRevision: "operator-1",
		Members: []HostGroupMembership{{
			HostGroupID: chassisID, HostID: hostID, Generation: 1, State: "ACTIVE",
			SourceType: "EXPLICIT", SourceRevision: "operator-1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if preHierarchySet.HierarchyGeneration != 0 {
		t.Fatalf("pre-hierarchy membership set unexpectedly bound to %#v", preHierarchySet)
	}

	request := HostGroupHierarchyRequest{
		PublishRequestID: "hierarchy-publish-1-" + suffix, HierarchyID: hierarchyID,
		GroupType: "FAILURE_DOMAIN", Dimension: dimension,
		ScopeType: "SYSTEM", ScopeID: "system", GraphMode: "TREE",
		ExpectedCurrentGeneration: 0,
		Levels:                    []string{"site", "rack", "chassis"},
		NodeGroupIDs:              []string{siteID, rackAID, rackBID, chassisID},
		Relations: []HostGroupHierarchyRelation{
			{ParentGroupID: siteID, ChildGroupID: rackAID},
			{ParentGroupID: siteID, ChildGroupID: rackBID},
			{ParentGroupID: rackAID, ChildGroupID: chassisID},
		},
	}
	hierarchy1, err := PublishHostGroupHierarchy(ctx, pool, request)
	if err != nil {
		t.Fatal(err)
	}
	if hierarchy1.HierarchyGeneration != 1 || hierarchy1.NodeCount != 4 || hierarchy1.RelationCount != 3 {
		t.Fatalf("hierarchy generation 1 = %#v", hierarchy1)
	}
	if replay, err := PublishHostGroupHierarchy(ctx, pool, request); err != nil || replay != hierarchy1 {
		t.Fatalf("stable hierarchy replay = %#v/%v", replay, err)
	}
	conflictingRequest := request
	conflictingRequest.Relations = []HostGroupHierarchyRelation{
		{ParentGroupID: siteID, ChildGroupID: rackAID},
		{ParentGroupID: siteID, ChildGroupID: rackBID},
		{ParentGroupID: rackBID, ChildGroupID: chassisID},
	}
	if _, err := PublishHostGroupHierarchy(ctx, pool, conflictingRequest); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("same request identity semantic conflict = %v", err)
	}
	if _, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "stale-hierarchy-snapshot-" + suffix, HostGroupID: chassisID, Purpose: "UPGRADE",
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("pre-hierarchy membership set snapshot = %v", err)
	}

	hierarchyGeneration := hierarchy1.HierarchyGeneration
	boundSet, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "hierarchy-bound-set-" + suffix, HostGroupID: chassisID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: preHierarchySet.MembershipSetGeneration,
		HierarchyGeneration: &hierarchyGeneration,
		SourceType:          "EXPLICIT", SourceRevision: "operator-2",
		Members: []HostGroupMembership{{
			HostGroupID: chassisID, HostID: hostID, Generation: 1, State: "ACTIVE",
			SourceType: "EXPLICIT", SourceRevision: "operator-1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if boundSet.HierarchyID != hierarchyID || boundSet.HierarchyGeneration != 1 {
		t.Fatalf("hierarchy-bound membership set = %#v", boundSet)
	}
	snapshot, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "hierarchy-snapshot-" + suffix, HostGroupID: chassisID, Purpose: "UPGRADE",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HierarchyID != hierarchyID || snapshot.HierarchyGeneration != 1 {
		t.Fatalf("hierarchy-bound snapshot = %#v", snapshot)
	}

	multiParent := request
	multiParent.PublishRequestID = "hierarchy-multi-parent-" + suffix
	multiParent.ExpectedCurrentGeneration = 1
	multiParent.Relations = append(multiParent.Relations,
		HostGroupHierarchyRelation{ParentGroupID: rackBID, ChildGroupID: chassisID})
	if _, err := PublishHostGroupHierarchy(ctx, pool, multiParent); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("multi-parent hierarchy publish = %v", err)
	}
	inverted := request
	inverted.PublishRequestID = "hierarchy-inverted-" + suffix
	inverted.ExpectedCurrentGeneration = 1
	inverted.Relations = []HostGroupHierarchyRelation{{ParentGroupID: chassisID, ChildGroupID: siteID}}
	if _, err := PublishHostGroupHierarchy(ctx, pool, inverted); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("inverted hierarchy publish = %v", err)
	}

	left := request
	left.PublishRequestID = "hierarchy-parallel-left-" + suffix
	left.HierarchyID = hierarchyID + "-left"
	left.ExpectedCurrentGeneration = 1
	right := conflictingRequest
	right.PublishRequestID = "hierarchy-parallel-right-" + suffix
	right.HierarchyID = hierarchyID + "-right"
	right.ExpectedCurrentGeneration = 1
	results := make(chan error, 2)
	go func() { _, err := PublishHostGroupHierarchy(ctx, pool, left); results <- err }()
	go func() { _, err := PublishHostGroupHierarchy(ctx, pool, right); results <- err }()
	succeeded, conflicted := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrHostGroupConflict):
			conflicted++
		default:
			t.Fatalf("parallel hierarchy publish = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("parallel hierarchy outcomes success=%d conflict=%d", succeeded, conflicted)
	}
	if _, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "generation-2-stale-set-" + suffix, HostGroupID: chassisID, Purpose: "UPGRADE",
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("generation-1 hierarchy-bound set under generation 2 = %v", err)
	}

	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{
		HostGroupID: rackAID, Generation: 2, GroupType: "FAILURE_DOMAIN",
		Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE",
	}); err != nil {
		t.Fatal(err)
	}
	if replay, err := PublishHostGroupHierarchy(ctx, pool, request); err != nil || replay != hierarchy1 {
		t.Fatalf("response-loss replay after node generation drift = %#v/%v", replay, err)
	}
	currentGeneration := uint64(2)
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "stale-graph-membership-" + suffix, HostGroupID: chassisID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: boundSet.MembershipSetGeneration,
		HierarchyGeneration: &currentGeneration, SourceType: "EXPLICIT", SourceRevision: "operator-3",
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("membership publish against stale hierarchy graph = %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE kim.host_group_hierarchy_set_evidence SET graph_mode='TREE' WHERE hierarchy_id=$1 AND hierarchy_generation=1`,
		hierarchyID); err == nil {
		t.Fatal("immutable hierarchy evidence UPDATE unexpectedly succeeded")
	}
}
