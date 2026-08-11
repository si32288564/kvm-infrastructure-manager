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

func TestPlacementScopePostgreSQLIntegration(t *testing.T) {
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
		VALUES('placement-scope',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	hostA, hostB, hostD := "scope-a-"+suffix, "scope-b-"+suffix, "scope-d-"+suffix
	certificateFingerprint := digestBytes([]byte("scope-certificate-" + suffix))
	prepareSessionIdentityFixture(t, ctx, pool, hostA, 1, certificateFingerprint)
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: hostA + "-attempt", HostID: hostA, ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: certificateFingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, pool, hostA)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: hostA, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostA, PolicyID: "scope-policy", PolicyGeneration: 1, ActorID: "fixture", ReasonCode: "scope_fixture"}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{hostB, hostD} {
		if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, host); err != nil {
			t.Fatal(err)
		}
	}
	g1, g2 := "scope-pool-a-"+suffix, "scope-pool-b-"+suffix
	for _, group := range []string{g1, g2} {
		if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: group, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "default", PolicyGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
	set1, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "scope-set-g1-1-" + suffix, HostGroupID: g1, SourceType: "EXPLICIT", SourceRevision: "fixture-1", BasedOnHostGroupGeneration: 1, Members: upgradeSnapshotMembers(g1, []string{hostA, hostB}, 1, "fixture-1")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "scope-set-g2-1-" + suffix, HostGroupID: g2, SourceType: "EXPLICIT", SourceRevision: "fixture-1", BasedOnHostGroupGeneration: 1, Members: upgradeSnapshotMembers(g2, []string{hostA, hostD}, 1, "fixture-1")}); err != nil {
		t.Fatal(err)
	}
	imageID, flavorID := "scope-image-"+suffix, "scope-flavor-"+suffix
	checksum := digestBytes([]byte("scope-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "https://images.invalid/scope.raw", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "scope.full", VCPUs: 24, MemoryMiB: 32768, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	scopeID := "placement-scope-" + suffix
	publish := func(generation uint64, requestID, lifecycle string, groups ...string) PlacementScope {
		exposures := make([]PlacementScopeExposure, 0, len(groups))
		for _, group := range groups {
			exposures = append(exposures, PlacementScopeExposure{HostGroupID: group, HostGroupGeneration: 1})
		}
		s, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: requestID, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: lifecycle, ExpectedCurrentGeneration: generation - 1, Exposures: exposures})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	scope1 := publish(1, "scope-publish-1-"+suffix, "ACTIVE", g1)
	baseRequest := PlacementAdmissionRequest{ProjectID: "project", ImageID: imageID, FlavorID: flavorID, PlacementScopeID: scopeID}
	request := func(id string) PlacementAdmissionRequest {
		r := baseRequest
		r.RequestID = id
		r.WorkloadID = "vm-" + id
		return r
	}
	dry1, err := DryEvaluatePlacementScope(ctx, pool, request("basic-"+suffix))
	if err != nil || dry1.Status != "READY" {
		t.Fatalf("basic dry=%+v err=%v", dry1, err)
	}
	if len(dry1.Candidates) != 2 || findScopeCandidate(dry1, hostA) == nil || findScopeCandidate(dry1, hostD) != nil {
		t.Fatalf("visible population=%+v", dry1.Candidates)
	}
	a := findScopeCandidate(dry1, hostA)
	b := findScopeCandidate(dry1, hostB)
	if a == nil || !a.Eligible || b == nil || b.Eligible {
		t.Fatalf("visibility/eligibility A=%+v B=%+v", a, b)
	}
	bypassRequest := request("scope-bypass-" + suffix)
	bypassRequest.PoolID = g1
	if _, err := DryEvaluatePlacement(ctx, pool, bypassRequest, hostA); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("legacy Dry Scope bypass=%v", err)
	}
	if _, err := FinalAdmitPlacement(ctx, pool, bypassRequest, a.Evaluation); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("legacy Final Scope bypass=%v", err)
	}
	if blocked, err := DryEvaluatePlacementScope(ctx, pool, PlacementAdmissionRequest{RequestID: "wrong-project-" + suffix, ProjectID: "other", PlacementScopeID: scopeID, WorkloadID: "vm-wrong", ImageID: imageID, FlavorID: flavorID}); err != nil || blocked.Status != "SCOPE_BLOCKED" {
		t.Fatalf("project boundary=%+v err=%v", blocked, err)
	}

	// Multiple exposed Pools deduplicate Host identity while preserving both paths.
	publish(2, "scope-publish-2-"+suffix, "ACTIVE", g1, g2)
	multi, err := DryEvaluatePlacementScope(ctx, pool, request("multi-"+suffix))
	if err != nil || len(multi.Candidates) != 3 {
		t.Fatalf("multi=%+v err=%v", multi, err)
	}
	if overlap := findScopeCandidate(multi, hostA); overlap == nil || len(overlap.Provenance) != 2 {
		t.Fatalf("overlap provenance=%+v", overlap)
	}

	// Removing the selected Group after Dry fences Final Admission atomically.
	driftRequest := request("scope-drift-" + suffix)
	driftDry, err := DryEvaluatePlacementScope(ctx, pool, driftRequest)
	if err != nil {
		t.Fatal(err)
	}
	driftCandidate := *findScopeCandidate(driftDry, hostA)
	publish(3, "scope-publish-3-"+suffix, "ACTIVE", g2)
	if _, err := FinalAdmitPlacementScope(ctx, pool, driftDry, driftRequest, driftCandidate); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("scope drift final=%v", err)
	}
	assertNoPlacementScopeClaims(t, ctx, pool, driftRequest.RequestID)

	// Widening never causes Final Admission to re-run selection.
	publish(4, "scope-publish-4-"+suffix, "ACTIVE", g1)
	widenRequest := request("scope-widen-" + suffix)
	widenDry, err := DryEvaluatePlacementScope(ctx, pool, widenRequest)
	if err != nil {
		t.Fatal(err)
	}
	widenCandidate := *findScopeCandidate(widenDry, hostA)
	publish(5, "scope-publish-5-"+suffix, "ACTIVE", g1, g2)
	if _, err := FinalAdmitPlacementScope(ctx, pool, widenDry, widenRequest, widenCandidate); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("scope widening final=%v", err)
	}
	assertNoPlacementScopeClaims(t, ctx, pool, widenRequest.RequestID)

	// Any captured visibility path drifting out of its accepted Set fences Final.
	membershipRequest := request("membership-drift-" + suffix)
	membershipDry, err := DryEvaluatePlacementScope(ctx, pool, membershipRequest)
	if err != nil {
		t.Fatal(err)
	}
	membershipCandidate := *findScopeCandidate(membershipDry, hostA)
	set2, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "scope-set-g1-2-" + suffix, HostGroupID: g1, SourceType: "EXPLICIT", SourceRevision: "fixture-remove", BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: set1.MembershipSetGeneration, Members: upgradeSnapshotMembers(g1, []string{hostB}, 2, "fixture-remove")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FinalAdmitPlacementScope(ctx, pool, membershipDry, membershipRequest, membershipCandidate); !errors.Is(err, ErrPlacementStale) {
		t.Fatalf("membership drift final=%v", err)
	}
	assertNoPlacementScopeClaims(t, ctx, pool, membershipRequest.RequestID)
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "scope-set-g1-3-" + suffix, HostGroupID: g1, SourceType: "EXPLICIT", SourceRevision: "fixture-restore", BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: set2.MembershipSetGeneration, Members: upgradeSnapshotMembers(g1, []string{hostA, hostB}, 3, "fixture-restore")}); err != nil {
		t.Fatal(err)
	}

	// DRAINING and RETIRED preserve history but block new exposure/admission.
	publish(6, "scope-publish-6-"+suffix, "DRAINING", g1, g2)
	if draining, err := DryEvaluatePlacementScope(ctx, pool, request("draining-"+suffix)); err != nil || draining.Status != "SCOPE_BLOCKED" {
		t.Fatalf("draining=%+v err=%v", draining, err)
	}
	publish(7, "scope-publish-7-"+suffix, "RETIRED", g1, g2)
	if retired, err := DryEvaluatePlacementScope(ctx, pool, request("retired-"+suffix)); err != nil || retired.Status != "SCOPE_BLOCKED" {
		t.Fatalf("retired=%+v err=%v", retired, err)
	}
	scope8 := publish(8, "scope-publish-8-"+suffix, "ACTIVE", g1, g2)
	if replay, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "scope-publish-8-" + suffix, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", ExpectedCurrentGeneration: 7, Exposures: []PlacementScopeExposure{{HostGroupID: g1, HostGroupGeneration: 1}, {HostGroupID: g2, HostGroupGeneration: 1}}}); err != nil || replay.ScopeGeneration != scope8.ScopeGeneration {
		t.Fatalf("response-loss replay=%+v err=%v", replay, err)
	}
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "scope-publish-8-" + suffix, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", ExpectedCurrentGeneration: 7, Exposures: []PlacementScopeExposure{{HostGroupID: g1, HostGroupGeneration: 1}}}); !errors.Is(err, ErrPlacementScopeConflict) {
		t.Fatalf("same request identity with different Scope semantics=%v", err)
	}

	// Two complete publishers for the same expected generation leave one winner.
	raceID := "scope-race-" + suffix
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, groups := range [][]string{{g1}, {g1, g2}} {
		wg.Add(1)
		go func(i int, groups []string) {
			defer wg.Done()
			exposures := []PlacementScopeExposure{}
			for _, g := range groups {
				exposures = append(exposures, PlacementScopeExposure{HostGroupID: g, HostGroupGeneration: 1})
			}
			_, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: fmt.Sprintf("scope-race-%d-%s", i, suffix), PlacementScopeID: raceID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: exposures})
			results <- err
		}(i, groups)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrPlacementScopeConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("scope race success=%d conflict=%d", successes, conflicts)
	}

	// Two dry decisions are eligible, but only the first may consume full Host
	// capacity. Eligibility is not admission and the loser leaves no claim.
	firstRequest, secondRequest := request("scope-first-"+suffix), request("scope-second-"+suffix)
	firstDry, err := DryEvaluatePlacementScope(ctx, pool, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondDry, err := DryEvaluatePlacementScope(ctx, pool, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	firstCandidate, secondCandidate := *findScopeCandidate(firstDry, hostA), *findScopeCandidate(secondDry, hostA)
	admission, err := FinalAdmitPlacementScope(ctx, pool, firstDry, firstRequest, firstCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FinalAdmitPlacementScope(ctx, pool, secondDry, secondRequest, secondCandidate); err == nil {
		t.Fatal("competing capacity admission unexpectedly succeeded")
	}
	assertNoPlacementScopeClaims(t, ctx, pool, secondRequest.RequestID)
	publish(9, "scope-publish-9-"+suffix, "ACTIVE", g2)
	var recordedScopeGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT placement_scope_generation FROM kim.placement_admission_decisions WHERE admission_id=$1`, admission.AdmissionID).Scan(&recordedScopeGeneration); err != nil || recordedScopeGeneration != 8 {
		t.Fatalf("historical Scope generation=%d err=%v", recordedScopeGeneration, err)
	}
	_ = scope1

	// FAILURE_DOMAIN cannot be used as a visibility source.
	failureGroup := "scope-failure-domain-" + suffix
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: failureGroup, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: "physical-location", Level: "rack", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "unsupported-scope-" + suffix, PlacementScopeID: "unsupported-" + suffix, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: failureGroup, HostGroupGeneration: 1}}}); !errors.Is(err, ErrPlacementScopeConflict) {
		t.Fatalf("unsupported HostGroup exposure=%v", err)
	}
}

func findScopeCandidate(result PlacementScopeDryResult, hostID string) *PlacementScopeCandidate {
	for i := range result.Candidates {
		if result.Candidates[i].HostID == hostID {
			return &result.Candidates[i]
		}
	}
	return nil
}

func assertNoPlacementScopeClaims(t *testing.T, ctx context.Context, db QueryRower, requestID string) {
	t.Helper()
	var decisions, compute int
	if err := db.QueryRow(ctx, `SELECT
	(SELECT count(*) FROM kim.placement_admission_decisions WHERE request_id=$1),
	(SELECT count(*) FROM kim.compute_allocation_claims WHERE request_id=$1)`, requestID).Scan(&decisions, &compute); err != nil || decisions != 0 || compute != 0 {
		t.Fatalf("partial Scope claims decisions=%d compute=%d err=%v", decisions, compute, err)
	}
}
