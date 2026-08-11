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

func availabilityPolicyFixture(id string, revision uint64, responsibility, action, lifecycle string) AvailabilityPolicyRevision {
	return AvailabilityPolicyRevision{
		PolicyID: id, PolicyRevision: revision, Responsibility: responsibility, HostFailureAction: action,
		FailureConfirmationPolicy: "host-failure-confirmation/v1", FencingRequirements: "source-fenced/v1",
		StorageRequirements: "single-writer/v1", NetworkDeviceRequirements: "device-safe/v1",
		RecoveryEligibilityPolicy: "placement-recheck/v1", FailureDomainConstraints: "different-host/v1",
		RecoveryBudgetPolicyReference: "recovery-budget/default/v1", MaxAttempts: 3,
		EscalationPolicy: "operator/v1", NotificationPolicy: "durable-fault-event/v1",
		SupportTier: "DEVELOPER_PREVIEW", LifecycleState: lifecycle, CreatedBy: "fixture", ApprovedBy: "fixture",
	}
}

func TestAvailabilityPolicyPlacementConsumerPostgreSQLIntegration(t *testing.T) {
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
		VALUES('availability-policy',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	host := "availability-host-" + suffix
	fingerprint := digestBytes([]byte("availability-cert-" + suffix))
	prepareSessionIdentityFixture(t, ctx, pool, host, 1, fingerprint)
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: host + "-attempt", HostID: host,
		ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1",
		AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1,
		PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, pool, host)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: host, CapabilityGeneration: 1,
		BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED",
		ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: host, PolicyID: "availability-host-policy",
		PolicyGeneration: 1, ActorID: "fixture", ReasonCode: "availability_fixture"}); err != nil {
		t.Fatal(err)
	}

	groupID := "availability-pool-" + suffix
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: groupID, PoolGeneration: 1,
		LifecycleState: "ACTIVE", PolicyID: "placement-default", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "availability-set-1-" + suffix,
		HostGroupID: groupID, SourceType: "EXPLICIT", SourceRevision: "fixture", BasedOnHostGroupGeneration: 1,
		Members: upgradeSnapshotMembers(groupID, []string{host}, 1, "fixture")}); err != nil {
		t.Fatal(err)
	}

	policyID := "availability-policy-" + suffix
	p1 := availabilityPolicyFixture(policyID, 1, "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION", "ACTIVE")
	d1, err := PublishAvailabilityPolicy(ctx, pool, p1)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := PublishAvailabilityPolicy(ctx, pool, p1); err != nil || replay != d1 {
		t.Fatalf("policy replay digest=%s err=%v", replay, err)
	}
	if _, err := PublishAvailabilityPolicy(ctx, pool, availabilityPolicyFixture("invalid-"+suffix, 1, "WORKLOAD_MANAGED", "EVACUATE", "ACTIVE")); !errors.Is(err, ErrAvailabilityPolicyConflict) {
		t.Fatalf("invalid responsibility/action accepted: %v", err)
	}
	binding, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "availability-binding-1-" + suffix,
		BindingID: "availability-binding-" + suffix, HostGroupID: groupID, HostGroupGeneration: 1,
		PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: policyID,
		PolicyRevision: 1, PolicyDigest: d1, Priority: 100, LifecycleState: "ACTIVE"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "availability-binding-1-" + suffix,
		BindingID: binding.BindingID, HostGroupID: groupID, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY",
		ConsumerType: "VM_PLACEMENT", PolicyID: policyID, PolicyRevision: 1, PolicyDigest: d1,
		Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatalf("binding replay: %v", err)
	}

	imageID, flavorID := "availability-image-"+suffix, "availability-flavor-"+suffix
	checksum := digestBytes([]byte("availability-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project",
		Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum,
		SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")),
		SourceURI: "https://images.invalid/availability.raw", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project",
		Name: "availability.small", VCPUs: 1, MemoryMiB: 1024, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	scopeID := "availability-scope-" + suffix
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "availability-scope-1-" + suffix,
		PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE",
		Exposures: []PlacementScopeExposure{{HostGroupID: groupID, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}

	request := func(id string) PlacementAdmissionRequest {
		return PlacementAdmissionRequest{RequestID: id, ProjectID: "project",
			WorkloadID: "vm-" + id, ImageID: imageID, FlavorID: flavorID, PlacementScopeID: scopeID}
	}
	r1 := request("availability-admission-1-" + suffix)
	dry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, r1)
	if err != nil || dry.Status != "READY" || len(dry.Candidates) != 1 || dry.Candidates[0].AvailabilityStatus != "RESOLVED" || dry.Candidates[0].AvailabilityResolution.EffectivePolicyRevision != 1 {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	var resolutions, claims, availabilityBindings int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.host_group_policy_resolution_evidence WHERE resolution_id=$1),
		(SELECT count(*) FROM kim.compute_allocation_claims WHERE request_id=$2),
		(SELECT count(*) FROM kim.vm_availability_binding_evidence WHERE workload_id=$3)`,
		dry.Candidates[0].AvailabilityResolution.ResolutionID, r1.RequestID, r1.WorkloadID).Scan(&resolutions, &claims, &availabilityBindings); err != nil {
		t.Fatal(err)
	}
	if resolutions != 0 || claims != 0 || availabilityBindings != 0 {
		t.Fatalf("Dry wrote authority resolution=%d claims=%d bindings=%d", resolutions, claims, availabilityBindings)
	}
	admission, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, dry, r1, dry.Candidates[0])
	if err != nil || admission.AvailabilityBinding == nil || admission.AvailabilityBinding.PolicyRevision != 1 || admission.AvailabilityBinding.Responsibility != "WORKLOAD_MANAGED" {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	if replay, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, dry, r1, dry.Candidates[0]); err != nil || replay.AdmissionID != admission.AdmissionID {
		t.Fatalf("admission replay=%+v err=%v", replay, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.vm_availability_binding_evidence WHERE workload_id=$1`, r1.WorkloadID).Scan(&availabilityBindings); err != nil || availabilityBindings != 1 {
		t.Fatalf("binding count=%d err=%v", availabilityBindings, err)
	}

	// Advancing current Policy without rebinding makes the existing assignment stale.
	p2 := availabilityPolicyFixture(policyID, 2, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE")
	d2, err := PublishAvailabilityPolicy(ctx, pool, p2)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, dry, r1, dry.Candidates[0])
	if err != nil || recovered.AvailabilityBinding == nil || recovered.AvailabilityBinding.PolicyRevision != 1 {
		t.Fatalf("post-drift response replay=%+v err=%v", recovered, err)
	}
	staleRequest := request("availability-stale-" + suffix)
	staleDry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, staleRequest)
	if err != nil || staleDry.Status != "VISIBLE_BUT_NO_ELIGIBLE_HOST" || staleDry.Candidates[0].AvailabilityStatus != "STALE_ASSIGNMENT" {
		t.Fatalf("stale dry=%+v err=%v", staleDry, err)
	}
	if _, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, staleDry, staleRequest, staleDry.Candidates[0]); !errors.Is(err, ErrPlacementIneligible) {
		t.Fatalf("stale Final=%v", err)
	}
	if _, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, dry, request("availability-drift-"+suffix), dry.Candidates[0]); err == nil {
		t.Fatal("old Dry survived Policy drift")
	}

	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "availability-binding-2-" + suffix,
		BindingID: binding.BindingID, ExpectedCurrentGeneration: 1, HostGroupID: groupID, HostGroupGeneration: 1,
		PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: policyID,
		PolicyRevision: 2, PolicyDigest: d2, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	r2 := request("availability-admission-2-" + suffix)
	dry2, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, r2)
	if err != nil || dry2.Status != "READY" || dry2.Candidates[0].AvailabilityResolution.EffectivePolicyRevision != 2 {
		t.Fatalf("dry2=%+v err=%v", dry2, err)
	}
	admission2, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, dry2, r2, dry2.Candidates[0])
	if err != nil || admission2.AvailabilityBinding.PolicyRevision != 2 || admission2.AvailabilityBinding.Responsibility != "INFRASTRUCTURE_MANAGED" {
		t.Fatalf("admission2=%+v err=%v", admission2, err)
	}
	var oldRevision uint64
	if err := pool.QueryRow(ctx, `SELECT availability_policy_revision FROM kim.vm_availability_binding_evidence WHERE workload_id=$1`, r1.WorkloadID).Scan(&oldRevision); err != nil || oldRevision != 1 {
		t.Fatalf("historical binding revision=%d err=%v", oldRevision, err)
	}

	// Final Admission and a Policy current switch serialize to one complete authority.
	raceRequest := request("availability-admission-race-" + suffix)
	raceDry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, raceRequest)
	if err != nil || raceDry.Status != "READY" {
		t.Fatalf("race dry=%+v err=%v", raceDry, err)
	}
	p3 := availabilityPolicyFixture(policyID, 3, "INFRASTRUCTURE_MANAGED", "EVACUATE", "ACTIVE")
	var raceWG sync.WaitGroup
	var raceAdmission PlacementAdmission
	var finalErr, publishErr error
	raceWG.Add(2)
	go func() {
		defer raceWG.Done()
		raceAdmission, finalErr = FinalAdmitAvailabilityPlacementScope(ctx, pool, raceDry, raceRequest, raceDry.Candidates[0])
	}()
	go func() { defer raceWG.Done(); _, publishErr = PublishAvailabilityPolicy(ctx, pool, p3) }()
	raceWG.Wait()
	if publishErr != nil {
		t.Fatalf("concurrent policy publish: %v", publishErr)
	}
	if finalErr == nil {
		if raceAdmission.AvailabilityBinding == nil || raceAdmission.AvailabilityBinding.PolicyRevision != 2 {
			t.Fatalf("mixed concurrent binding=%+v", raceAdmission)
		}
	} else if !errors.Is(finalErr, ErrPlacementStale) {
		t.Fatalf("concurrent Final outcome=%v", finalErr)
	}

	// Retirement blocks new resolution but preserves historical bindings.
	p4 := availabilityPolicyFixture(policyID, 4, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "RETIRED")
	if _, err := PublishAvailabilityPolicy(ctx, pool, p4); err != nil {
		t.Fatal(err)
	}
	retiredDry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, request("availability-retired-"+suffix))
	if err != nil || retiredDry.Candidates[0].AvailabilityStatus != "STALE_ASSIGNMENT" {
		t.Fatalf("retired dry=%+v err=%v", retiredDry, err)
	}
	if err := pool.QueryRow(ctx, `SELECT availability_policy_revision FROM kim.vm_availability_binding_evidence WHERE workload_id=$1`, r1.WorkloadID).Scan(&oldRevision); err != nil || oldRevision != 1 {
		t.Fatalf("retirement rewrote history revision=%d err=%v", oldRevision, err)
	}

	// Explicit Rebind is the only path that advances an existing VM Binding.
	policy5 := availabilityPolicyFixture("availability-rebind-target-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "EVACUATE", "ACTIVE")
	d5, err := PublishAvailabilityPolicy(ctx, pool, policy5)
	if err != nil {
		t.Fatal(err)
	}
	var sourceDigest string
	if err := pool.QueryRow(ctx, `SELECT binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&sourceDigest); err != nil {
		t.Fatal(err)
	}
	beforeCounts := make([]int, 5)
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.compute_allocation_claims),
		(SELECT count(*) FROM kim.pci_vf_allocation_claims),
		(SELECT count(*) FROM kim.network_identity_claims),
		(SELECT count(*) FROM kim.volume_attachment_claims),
		(SELECT count(*) FROM kim.vm_power_observation_evidence)`).Scan(&beforeCounts[0], &beforeCounts[1], &beforeCounts[2], &beforeCounts[3], &beforeCounts[4]); err != nil {
		t.Fatal(err)
	}
	rebindRequest := VMAvailabilityRebindRequest{RebindID: "availability-rebind-1-" + suffix, WorkloadID: r1.WorkloadID,
		ExpectedCurrentBindingRevision: 1, SourceBindingDigest: sourceDigest, TargetPolicyID: policy5.PolicyID,
		TargetPolicyRevision: 1, TargetPolicyDigest: d5, RequestedBy: "operator-a", AuthorizedBy: "approver-a",
		AuthorizationReference: "approval/availability/1", Reason: "move responsibility to infrastructure"}
	recorded, err := RecordVMAvailabilityRebindRequest(ctx, pool, rebindRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := RecordVMAvailabilityRebindRequest(ctx, pool, rebindRequest); err != nil || replay.RequestDigest != recorded.RequestDigest {
		t.Fatalf("request replay=%+v err=%v", replay, err)
	}
	conflictRequest := rebindRequest
	conflictRequest.TargetPolicyID = policyID
	conflictRequest.TargetPolicyRevision = 4
	conflictRequest.TargetPolicyDigest = digestBytes([]byte("different"))
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, conflictRequest); !errors.Is(err, ErrAvailabilityRebindConflict) {
		t.Fatalf("request identity conflict=%v", err)
	}
	decision1, binding2, err := DecideVMAvailabilityRebind(ctx, pool, rebindRequest.RebindID, "decision-authority")
	if err != nil || decision1.ResultCode != "ACCEPTED" || binding2.BindingRevision != 2 || binding2.SourceBindingRevision != 1 || binding2.PolicyRevision != 1 {
		t.Fatalf("decision=%+v binding=%+v err=%v", decision1, binding2, err)
	}
	// A lost response replays the same decision and revision; it never creates rev3.
	replayedDecision, replayedBinding, err := DecideVMAvailabilityRebind(ctx, pool, rebindRequest.RebindID, "decision-authority")
	if err != nil || replayedDecision.DecisionDigest != decision1.DecisionDigest || replayedBinding.BindingDigest != binding2.BindingDigest {
		t.Fatalf("decision replay=%+v binding=%+v err=%v", replayedDecision, replayedBinding, err)
	}
	var currentRevision uint64
	var bindingCount, decisionCount int
	if err := pool.QueryRow(ctx, `SELECT c.binding_revision,
		(SELECT count(*) FROM kim.vm_availability_binding_evidence WHERE workload_id=$1),
		(SELECT count(*) FROM kim.vm_availability_rebind_decision_evidence WHERE rebind_id=$2)
		FROM kim.vm_availability_bindings_current c WHERE c.workload_id=$1`, r1.WorkloadID, rebindRequest.RebindID).Scan(&currentRevision, &bindingCount, &decisionCount); err != nil || currentRevision != 2 || bindingCount != 2 || decisionCount != 1 {
		t.Fatalf("current=%d bindings=%d decisions=%d err=%v", currentRevision, bindingCount, decisionCount, err)
	}
	afterCounts := make([]int, 5)
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.compute_allocation_claims),
		(SELECT count(*) FROM kim.pci_vf_allocation_claims),
		(SELECT count(*) FROM kim.network_identity_claims),
		(SELECT count(*) FROM kim.volume_attachment_claims),
		(SELECT count(*) FROM kim.vm_power_observation_evidence)`).Scan(&afterCounts[0], &afterCounts[1], &afterCounts[2], &afterCounts[3], &afterCounts[4]); err != nil {
		t.Fatal(err)
	}
	for i := range beforeCounts {
		if beforeCounts[i] != afterCounts[i] {
			t.Fatalf("Rebind caused side effect counts before=%v after=%v", beforeCounts, afterCounts)
		}
	}

	// A request tied to rev2 becomes stale after another explicit transition.
	policy6 := availabilityPolicyFixture("availability-rebind-target-6-"+suffix, 1, "MANUAL", "NO_AUTOMATIC_ACTION", "ACTIVE")
	d6, err := PublishAvailabilityPolicy(ctx, pool, policy6)
	if err != nil {
		t.Fatal(err)
	}
	staleRebind := VMAvailabilityRebindRequest{RebindID: "availability-rebind-stale-" + suffix, WorkloadID: r1.WorkloadID,
		ExpectedCurrentBindingRevision: 2, SourceBindingDigest: binding2.BindingDigest, TargetPolicyID: policy6.PolicyID,
		TargetPolicyRevision: 1, TargetPolicyDigest: d6, RequestedBy: "operator-b", AuthorizedBy: "approver-b",
		AuthorizationReference: "approval/availability/2", Reason: "stale request qualification"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, staleRebind); err != nil {
		t.Fatal(err)
	}
	policy7 := availabilityPolicyFixture("availability-rebind-target-7-"+suffix, 1, "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION", "ACTIVE")
	d7, err := PublishAvailabilityPolicy(ctx, pool, policy7)
	if err != nil {
		t.Fatal(err)
	}
	winner := staleRebind
	winner.RebindID = "availability-rebind-winner-" + suffix
	winner.TargetPolicyID = policy7.PolicyID
	winner.TargetPolicyDigest = d7
	winner.Reason = "advance binding once"
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, winner); err != nil {
		t.Fatal(err)
	}
	if _, binding3, err := DecideVMAvailabilityRebind(ctx, pool, winner.RebindID, "decision-authority"); err != nil || binding3.BindingRevision != 3 {
		t.Fatalf("winner binding=%+v err=%v", binding3, err)
	}
	if _, _, err := DecideVMAvailabilityRebind(ctx, pool, staleRebind.RebindID, "decision-authority"); !errors.Is(err, ErrAvailabilityRebindStaleSource) {
		t.Fatalf("stale source result=%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT binding_revision FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&currentRevision); err != nil || currentRevision != 3 {
		t.Fatalf("stale request amplified revision=%d err=%v", currentRevision, err)
	}

	// Distinct concurrent intents against the same source produce one transition.
	var source2Digest string
	if err := pool.QueryRow(ctx, `SELECT binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r2.WorkloadID).Scan(&source2Digest); err != nil {
		t.Fatal(err)
	}
	concurrentPolicies := []AvailabilityPolicyRevision{
		availabilityPolicyFixture("availability-concurrent-a-"+suffix, 1, "MANUAL", "NO_AUTOMATIC_ACTION", "ACTIVE"),
		availabilityPolicyFixture("availability-concurrent-b-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE"),
	}
	concurrentRequests := make([]VMAvailabilityRebindRequest, 2)
	for i, p := range concurrentPolicies {
		d, err := PublishAvailabilityPolicy(ctx, pool, p)
		if err != nil {
			t.Fatal(err)
		}
		concurrentRequests[i] = VMAvailabilityRebindRequest{RebindID: fmt.Sprintf("availability-concurrent-%d-%s", i, suffix), WorkloadID: r2.WorkloadID,
			ExpectedCurrentBindingRevision: 1, SourceBindingDigest: source2Digest, TargetPolicyID: p.PolicyID, TargetPolicyRevision: 1,
			TargetPolicyDigest: d, RequestedBy: "operator-concurrent", AuthorizedBy: "approver-concurrent",
			AuthorizationReference: "approval/availability/concurrent", Reason: "concurrent qualification"}
		if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, concurrentRequests[i]); err != nil {
			t.Fatal(err)
		}
	}
	var rebindWG sync.WaitGroup
	rebindErrs := make([]error, 2)
	rebindWG.Add(2)
	for i := range concurrentRequests {
		go func(i int) {
			defer rebindWG.Done()
			_, _, rebindErrs[i] = DecideVMAvailabilityRebind(ctx, pool, concurrentRequests[i].RebindID, "decision-authority")
		}(i)
	}
	rebindWG.Wait()
	accepted, stale := 0, 0
	for _, e := range rebindErrs {
		if e == nil {
			accepted++
		} else if errors.Is(e, ErrAvailabilityRebindStaleSource) {
			stale++
		} else {
			t.Fatalf("unexpected concurrent result=%v", e)
		}
	}
	if accepted != 1 || stale != 1 {
		t.Fatalf("concurrent accepted=%d stale=%d errors=%v", accepted, stale, rebindErrs)
	}
	if err := pool.QueryRow(ctx, `SELECT binding_revision FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r2.WorkloadID).Scan(&currentRevision); err != nil || currentRevision != 2 {
		t.Fatalf("concurrent current=%d err=%v", currentRevision, err)
	}

	// Exact target current switch never uplifts the request to a newer revision.
	racePolicy := availabilityPolicyFixture("availability-race-target-"+suffix, 1, "MANUAL", "NO_AUTOMATIC_ACTION", "ACTIVE")
	raceDigest, err := PublishAvailabilityPolicy(ctx, pool, racePolicy)
	if err != nil {
		t.Fatal(err)
	}
	var source3Digest string
	if err := pool.QueryRow(ctx, `SELECT binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, raceRequest.WorkloadID).Scan(&source3Digest); err == nil {
		raceRebind := VMAvailabilityRebindRequest{RebindID: "availability-policy-race-rebind-" + suffix, WorkloadID: raceRequest.WorkloadID,
			ExpectedCurrentBindingRevision: 1, SourceBindingDigest: source3Digest, TargetPolicyID: racePolicy.PolicyID,
			TargetPolicyRevision: 1, TargetPolicyDigest: raceDigest, RequestedBy: "operator-race", AuthorizedBy: "approver-race",
			AuthorizationReference: "approval/availability/race", Reason: "policy current race"}
		if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, raceRebind); err != nil {
			t.Fatal(err)
		}
		racePolicy2 := availabilityPolicyFixture(racePolicy.PolicyID, 2, "INFRASTRUCTURE_MANAGED", "EVACUATE", "ACTIVE")
		var decideRaceErr, publishRaceErr error
		rebindWG.Add(2)
		go func() {
			defer rebindWG.Done()
			_, _, decideRaceErr = DecideVMAvailabilityRebind(ctx, pool, raceRebind.RebindID, "decision-authority")
		}()
		go func() { defer rebindWG.Done(); _, publishRaceErr = PublishAvailabilityPolicy(ctx, pool, racePolicy2) }()
		rebindWG.Wait()
		if publishRaceErr != nil {
			t.Fatal(publishRaceErr)
		}
		if decideRaceErr != nil && !errors.Is(decideRaceErr, ErrAvailabilityRebindStaleTarget) {
			t.Fatalf("policy race result=%v", decideRaceErr)
		}
		var uplifted int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.vm_availability_binding_evidence WHERE workload_id=$1 AND availability_policy_id=$2 AND availability_policy_revision=2`, raceRequest.WorkloadID, racePolicy.PolicyID).Scan(&uplifted); err != nil || uplifted != 0 {
			t.Fatalf("silent uplift count=%d err=%v", uplifted, err)
		}
	}
}
