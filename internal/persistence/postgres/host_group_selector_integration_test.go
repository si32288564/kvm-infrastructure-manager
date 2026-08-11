package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestHostGroupSelectorMaterializationPostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch,authority_generation,mode)
		VALUES ('host-group-selector-test',1,'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	dimension := "selector-location-" + suffix
	hostA, hostB, hostUnknown := "selector-a-"+suffix, "selector-b-"+suffix, "selector-unknown-"+suffix
	siteA, siteB := "selector-site-a-"+suffix, "selector-site-b-"+suffix
	rackA, rackB := "selector-rack-a-"+suffix, "selector-rack-b-"+suffix
	hierarchyID, selectorID := "selector-hierarchy-"+suffix, "selector-"+suffix
	for _, hostID := range []string{hostA, hostB, hostUnknown} {
		fingerprint := digestHostGroupFields(hostID, "certificate")
		prepareSessionIdentityFixture(t, ctx, pool, hostID, 1, fingerprint)
		if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{
			SessionAttemptID: hostID + "-attempt", HostID: hostID,
			ConnectionInstanceID: "selector-connection", TransportProfile: "integration",
			ProtocolVersion: "v1", AgentArtifactDigest: digestHostGroupFields("selector-agent"),
			CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint,
			ExpectedSessionGeneration: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	acceptSelectorInventory(t, ctx, pool, hostA, 1, "x86_64", agentinventory.AvailabilityAvailable)
	acceptSelectorInventory(t, ctx, pool, hostB, 1, "aarch64", agentinventory.AvailabilityAvailable)
	acceptSelectorInventory(t, ctx, pool, hostUnknown, 1, "", agentinventory.AvailabilityUnknown)
	for _, revision := range []HostGroupRevision{
		{HostGroupID: siteA, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "site", LifecycleState: "ACTIVE"},
		{HostGroupID: siteB, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "site", LifecycleState: "ACTIVE"},
		{HostGroupID: rackA, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE"},
		{HostGroupID: rackB, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE"},
	} {
		if err := UpsertHostGroup(ctx, pool, revision); err != nil {
			t.Fatal(err)
		}
	}
	var rackPolicyID string
	if err := pool.QueryRow(ctx, `
		SELECT cardinality_policy_id FROM kim.host_group_cardinality_policies_current
		WHERE group_type='FAILURE_DOMAIN' AND dimension=$1 AND level='rack'
		  AND scope_type='SYSTEM' AND scope_id='system'
	`, dimension).Scan(&rackPolicyID); err != nil {
		t.Fatal(err)
	}
	if err := UpsertHostGroupCardinalityPolicy(ctx, pool, HostGroupCardinalityPolicy{
		PolicyID: rackPolicyID, Generation: 2, GroupType: "FAILURE_DOMAIN", Dimension: dimension,
		Level: "rack", ScopeType: "SYSTEM", ScopeID: "system", Cardinality: "ZERO_OR_ONE", State: "ACTIVE",
	}); err != nil {
		t.Fatal(err)
	}
	hierarchyRequest := HostGroupHierarchyRequest{
		PublishRequestID: "selector-hierarchy-1-" + suffix, HierarchyID: hierarchyID,
		GroupType: "FAILURE_DOMAIN", Dimension: dimension, ScopeType: "SYSTEM", ScopeID: "system",
		GraphMode: "TREE", ExpectedCurrentGeneration: 0, Levels: []string{"site", "rack"},
		NodeGroupIDs: []string{siteA, siteB, rackA, rackB},
		Relations:    []HostGroupHierarchyRelation{{ParentGroupID: siteA, ChildGroupID: rackA}, {ParentGroupID: siteA, ChildGroupID: rackB}},
	}
	hierarchy1, err := PublishHostGroupHierarchy(ctx, pool, hierarchyRequest)
	if err != nil {
		t.Fatal(err)
	}
	evaluatorDigest := digestHostGroupFields("selector-evaluator-v1")
	upsertSelector := func(generation uint64, architecture string) {
		t.Helper()
		if err := UpsertHostGroupSelector(ctx, pool, HostGroupSelectorRevision{
			SelectorID: selectorID, HostGroupID: rackA, Generation: generation,
			BasedOnHostGroupGeneration: 1, SchemaVersion: HostGroupSelectorSchemaV1,
			EvaluatorArtifactDigest: evaluatorDigest, LifecycleState: "ACTIVE",
			Expression: selectorExpression(HostGroupSelectorPredicate{Field: "COMPUTE_ARCHITECTURE", Operator: "EQUALS", Value: architecture}),
		}); err != nil {
			t.Fatal(err)
		}
	}
	evaluate := func(id string, selectorGeneration, expected uint64, hosts ...string) HostGroupSelectorEvaluation {
		t.Helper()
		evaluation, err := EvaluateHostGroupSelector(ctx, pool, HostGroupSelectorEvaluationRequest{
			EvaluationID: id, SelectorID: selectorID, SelectorGeneration: selectorGeneration,
			ExpectedCurrentGeneration: expected, HostIDs: hosts,
		})
		if err != nil {
			t.Fatal(err)
		}
		return evaluation
	}

	upsertSelector(1, "x86_64")
	evaluation1 := evaluate("selector-eval-1-"+suffix, 1, 0, hostA, hostB)
	if evaluation1.ResultState != "MATCHED" || evaluation1.CandidateHostCount != 1 || evaluation1.Hosts[0].HostID != hostA {
		t.Fatalf("selector generation 1 evaluation = %#v", evaluation1)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.host_group_selector_evaluation_evidence SET result_state='NOT_MATCHED' WHERE evaluation_id=$1`, evaluation1.EvaluationID); err == nil {
		t.Fatal("immutable selector evaluation accepted UPDATE")
	}
	unknownEvaluation := evaluate("selector-eval-unknown-"+suffix, 1, 1, hostA, hostUnknown)
	if unknownEvaluation.ResultState != "UNKNOWN" {
		t.Fatalf("UNKNOWN collapsed to %s", unknownEvaluation.ResultState)
	}
	if _, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-unknown-set-" + suffix, EvaluationID: unknownEvaluation.EvaluationID,
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("UNKNOWN materialization = %v", err)
	}
	// Re-evaluate the known population after the UNKNOWN proposal became current.
	evaluation1 = evaluate("selector-eval-1b-"+suffix, 1, 2, hostA, hostB)
	set1, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-set-1-" + suffix, EvaluationID: evaluation1.EvaluationID,
		ExpectedCurrentSetGeneration: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if set1.MembershipSetGeneration != 1 || set1.SelectorGeneration != 1 || set1.SelectorEvaluationID != evaluation1.EvaluationID {
		t.Fatalf("selector-bound set 1 = %#v", set1)
	}
	if replay, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-set-1-" + suffix, EvaluationID: evaluation1.EvaluationID,
		ExpectedCurrentSetGeneration: 0,
	}); err != nil || replay.MembershipSetGeneration != 1 {
		t.Fatalf("materialization replay = %#v/%v", replay, err)
	}

	upsertSelector(2, "aarch64")
	if _, err := CreateHostGroupMembershipSnapshot(ctx, pool, HostGroupSnapshotRequest{
		SnapshotID: "selector-stale-snapshot-" + suffix, HostGroupID: rackA, Purpose: "UPGRADE",
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("old selector-bound Set remained current-compatible: %v", err)
	}
	evaluation2 := evaluate("selector-eval-2-"+suffix, 2, 3, hostA, hostB)
	acceptSelectorInventory(t, ctx, pool, hostB, 2, "aarch64", agentinventory.AvailabilityAvailable)
	if _, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-stale-inventory-" + suffix, EvaluationID: evaluation2.EvaluationID,
		ExpectedCurrentSetGeneration: 1,
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("inventory drift materialization = %v", err)
	}
	evaluation2 = evaluate("selector-eval-2b-"+suffix, 2, 4, hostA, hostB)
	set2, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-set-2-" + suffix, EvaluationID: evaluation2.EvaluationID,
		ExpectedCurrentSetGeneration: 1,
	})
	if err != nil || set2.MembershipSetGeneration != 2 {
		t.Fatalf("selector generation 2 materialization = %#v/%v", set2, err)
	}

	// First publish host A so host B can be owned exclusively by the sibling rack.
	upsertSelector(3, "x86_64")
	evaluation3 := evaluate("selector-eval-3-"+suffix, 3, 5, hostA, hostB)
	set3, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-set-3-" + suffix, EvaluationID: evaluation3.EvaluationID,
		ExpectedCurrentSetGeneration: 2,
	})
	if err != nil || set3.MembershipSetGeneration != 3 {
		t.Fatalf("selector generation 3 materialization = %#v/%v", set3, err)
	}
	// An exclusive sibling now owns host B; selector matching does not bypass cardinality authority.
	hierarchyGeneration := hierarchy1.HierarchyGeneration
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "selector-rack-b-set-" + suffix, HostGroupID: rackB,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 0,
		HierarchyGeneration: &hierarchyGeneration, SourceType: "EXPLICIT", SourceRevision: "operator",
		Members: []HostGroupMembership{{HostGroupID: rackB, HostID: hostB, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: "operator"}},
	}); err != nil {
		t.Fatal(err)
	}
	upsertSelector(4, "aarch64")
	evaluation4 := evaluate("selector-eval-4-"+suffix, 4, 6, hostA, hostB)
	if _, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-cardinality-conflict-" + suffix, EvaluationID: evaluation4.EvaluationID,
		ExpectedCurrentSetGeneration: 3,
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("selector cardinality conflict = %v", err)
	}

	// A hierarchy change fences the proposal until a new evaluation binds the new generation.
	upsertSelector(5, "x86_64")
	evaluation5 := evaluate("selector-eval-5-"+suffix, 5, 7, hostA, hostB)
	hierarchyRequest.PublishRequestID = "selector-hierarchy-2-" + suffix
	hierarchyRequest.ExpectedCurrentGeneration = 1
	hierarchyRequest.Relations = []HostGroupHierarchyRelation{{ParentGroupID: siteB, ChildGroupID: rackA}, {ParentGroupID: siteA, ChildGroupID: rackB}}
	hierarchy2, err := PublishHostGroupHierarchy(ctx, pool, hierarchyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-stale-hierarchy-" + suffix, EvaluationID: evaluation5.EvaluationID,
		ExpectedCurrentSetGeneration: 3,
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("hierarchy drift materialization = %v", err)
	}
	evaluation5 = evaluate("selector-eval-5b-"+suffix, 5, 8, hostA, hostB)
	set4, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
		PublishRequestID: "selector-set-4-" + suffix, EvaluationID: evaluation5.EvaluationID,
		ExpectedCurrentSetGeneration: 3,
	})
	if err != nil || set4.HierarchyGeneration != hierarchy2.HierarchyGeneration {
		t.Fatalf("hierarchy generation 2 materialization = %#v/%v", set4, err)
	}

	// Parallel identical evaluations converge to one generation in PostgreSQL authority.
	var wait sync.WaitGroup
	evaluations := make(chan HostGroupSelectorEvaluation, 2)
	errorsOut := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			value, err := EvaluateHostGroupSelector(ctx, pool, HostGroupSelectorEvaluationRequest{
				EvaluationID: fmt.Sprintf("selector-parallel-same-%d-%s", index, suffix), SelectorID: selectorID,
				SelectorGeneration: 5, ExpectedCurrentGeneration: 9, HostIDs: []string{hostA, hostB},
			})
			if err != nil {
				errorsOut <- err
				return
			}
			evaluations <- value
		}(index)
	}
	wait.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Fatal(err)
	}
	first, second := <-evaluations, <-evaluations
	if first.EvaluationGeneration != 10 || second.EvaluationGeneration != 10 || first.EvaluationID != second.EvaluationID {
		t.Fatalf("parallel semantic convergence = %#v / %#v", first, second)
	}

	// Different semantic populations at one expected generation permit only one current decision.
	results := make(chan error, 2)
	for index, hosts := range [][]string{{hostA, hostB}, {hostB}} {
		wait.Add(1)
		go func(index int, hosts []string) {
			defer wait.Done()
			_, err := EvaluateHostGroupSelector(ctx, pool, HostGroupSelectorEvaluationRequest{
				EvaluationID: fmt.Sprintf("selector-parallel-different-%d-%s", index, suffix), SelectorID: selectorID,
				SelectorGeneration: 5, ExpectedCurrentGeneration: 10, HostIDs: hosts,
			})
			results <- err
		}(index, hosts)
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrHostGroupConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("different semantic concurrency = %d success / %d conflict", successes, conflicts)
	}

	// Two publishers of the same evaluation converge to one Membership Set generation.
	evaluationForSamePublish := evaluate("selector-publish-same-eval-"+suffix, 5, 11, hostA, hostB)
	publishResults := make(chan HostGroupMembershipSet, 2)
	publishErrors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			set, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
				PublishRequestID: fmt.Sprintf("selector-parallel-publish-same-%d-%s", index, suffix),
				EvaluationID:     evaluationForSamePublish.EvaluationID, ExpectedCurrentSetGeneration: 4,
			})
			if err != nil {
				publishErrors <- err
				return
			}
			publishResults <- set
		}(index)
	}
	wait.Wait()
	close(publishErrors)
	for err := range publishErrors {
		t.Fatal(err)
	}
	firstSet, secondSet := <-publishResults, <-publishResults
	if firstSet.MembershipSetGeneration != 5 || secondSet.MembershipSetGeneration != 5 ||
		firstSet.CanonicalMemberSetDigest != secondSet.CanonicalMemberSetDigest {
		t.Fatalf("parallel identical publish = %#v / %#v", firstSet, secondSet)
	}

	// Different immutable candidate sets at one expected Set generation permit one publish only.
	emptyEvaluation := evaluate("selector-publish-empty-eval-"+suffix, 5, 12, hostB)
	matchedEvaluation := evaluate("selector-publish-matched-eval-"+suffix, 5, 13, hostA, hostB)
	publisherOutcomes := make(chan error, 2)
	for index, evaluation := range []HostGroupSelectorEvaluation{emptyEvaluation, matchedEvaluation} {
		wait.Add(1)
		go func(index int, evaluation HostGroupSelectorEvaluation) {
			defer wait.Done()
			_, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
				PublishRequestID: fmt.Sprintf("selector-parallel-publish-different-%d-%s", index, suffix),
				EvaluationID:     evaluation.EvaluationID, ExpectedCurrentSetGeneration: 5,
			})
			publisherOutcomes <- err
		}(index, evaluation)
	}
	wait.Wait()
	close(publisherOutcomes)
	successes, conflicts = 0, 0
	for err := range publisherOutcomes {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrHostGroupConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("different semantic publisher concurrency = %d success / %d conflict", successes, conflicts)
	}
}

func acceptSelectorInventory(t *testing.T, ctx context.Context, db TxBeginner, hostID string, generation uint64, architecture string, topologyState agentinventory.Availability) {
	t.Helper()
	reason := ""
	threads := []agentinventory.CPUThread{{LinuxID: 0, CoreID: 0, SocketID: 0, NUMANodeID: -1, Online: true}}
	if topologyState != agentinventory.AvailabilityAvailable {
		reason = "topology_observation_failed"
		threads = nil
	}
	snapshot := agentinventory.Snapshot{
		SchemaVersion: agentinventory.SnapshotSchemaV3, HostIdentity: hostID,
		ObservationGeneration: generation, CollectionStatus: "COMPLETE",
		Fragments: []agentinventory.Fragment{{
			Domain:       agentinventory.DomainCompute,
			Source:       agentinventory.Source{ModuleName: "selector-compute", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digestHostGroupFields("selector-compute")},
			Capabilities: []agentinventory.Capability{{Name: "kim.host.cpu-topology.v1", Version: "v1", State: topologyState, ReasonCode: reason}},
			Compute:      &agentinventory.Compute{Architecture: architecture, CPUModel: "selector-fixture", Threads: threads},
		}},
	}
	envelope, err := agentinventory.NewEnvelope(snapshot, 1, fmt.Sprintf("%s-inventory-%d", hostID, generation))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, db, envelope, 1<<20); err != nil {
		t.Fatal(err)
	}
}
