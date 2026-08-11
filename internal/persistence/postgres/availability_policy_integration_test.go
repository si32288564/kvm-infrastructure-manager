package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

	// Failure Epoch authority binds the exact Availability Binding present at
	// open time. The typed observation remains SUSPECTED and is not fencing or
	// recovery authority.
	failureCountsBefore := make([]int, 6)
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.compute_allocation_claims),
		(SELECT count(*) FROM kim.pci_vf_allocation_claims),
		(SELECT count(*) FROM kim.network_identity_claims),
		(SELECT count(*) FROM kim.volume_attachment_claims),
		(SELECT count(*) FROM kim.vm_power_observation_evidence),
		(SELECT count(*) FROM kim.execution_jobs)`).Scan(&failureCountsBefore[0], &failureCountsBefore[1], &failureCountsBefore[2], &failureCountsBefore[3], &failureCountsBefore[4], &failureCountsBefore[5]); err != nil {
		t.Fatal(err)
	}
	var failureSourceRevision uint64
	var failureSourceDigest string
	if err := pool.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&failureSourceRevision, &failureSourceDigest); err != nil {
		t.Fatal(err)
	}
	trigger := FailureObservation{EvidenceID: "failure-trigger-" + suffix, EvidenceType: "AGENT_CONNECTIVITY_LOSS",
		SourceType: "CONTROL_PLANE", SourceHostID: host, SourceSessionGeneration: 1,
		SourceCredentialBindingRevision: 1, ObservationGeneration: 1, ObservedState: "ABSENT",
		FreshnessState: "CURRENT", ObservedAt: time.Now().UTC(), PayloadDigest: digestBytes([]byte("agent-connectivity-loss"))}
	openRequest := OpenFailureEpochRequest{OpenRequestID: "failure-open-" + suffix, FailureEpochID: "failure-epoch-" + suffix,
		IncidentKey: "host-connectivity-incident-" + suffix, WorkloadID: r1.WorkloadID,
		FailureClass: "HOST_CONNECTIVITY_LOSS", RequestedBy: "failure-detector/v1",
		ExpectedBindingRevision: failureSourceRevision, ExpectedBindingDigest: failureSourceDigest, Trigger: trigger}
	epoch1, err := OpenFailureEpoch(ctx, pool, openRequest)
	if err != nil || epoch1.EpochState != "SUSPECTED" || epoch1.AvailabilityBindingRevision != failureSourceRevision || epoch1.AvailabilityBindingDigest != failureSourceDigest || epoch1.PolicyID == "" {
		t.Fatalf("epoch1=%+v err=%v", epoch1, err)
	}
	if replay, err := OpenFailureEpoch(ctx, pool, openRequest); err != nil || replay.EpochDigest != epoch1.EpochDigest {
		t.Fatalf("epoch replay=%+v err=%v", replay, err)
	}
	unknown := FailureObservation{EvidenceID: "failure-unknown-" + suffix, EvidenceType: "AGENT_CONNECTIVITY_LOSS",
		SourceType: "CONTROL_PLANE", SourceHostID: host, SourceSessionGeneration: 1,
		SourceCredentialBindingRevision: 1, ObservationGeneration: 2, ObservedState: "UNKNOWN",
		FreshnessState: "UNKNOWN", ObservedAt: time.Now().UTC().Add(time.Second), PayloadDigest: digestBytes([]byte("observation-unavailable"))}
	unknownRecorded, err := AppendFailureObservation(ctx, pool, epoch1.FailureEpochID, unknown)
	if err != nil || unknownRecorded.EvidenceGeneration != 2 {
		t.Fatalf("unknown evidence=%+v err=%v", unknownRecorded, err)
	}
	if replay, err := AppendFailureObservation(ctx, pool, epoch1.FailureEpochID, unknown); err != nil || replay.EvidenceGeneration != 2 || replay.EvidenceDigest != unknownRecorded.EvidenceDigest {
		t.Fatalf("evidence replay=%+v err=%v", replay, err)
	}
	conflictingEvidence := unknown
	conflictingEvidence.PayloadDigest = digestBytes([]byte("different-observation"))
	if _, err := AppendFailureObservation(ctx, pool, epoch1.FailureEpochID, conflictingEvidence); !errors.Is(err, ErrFailureEvidenceConflict) {
		t.Fatalf("evidence identity conflict=%v", err)
	}
	parallelEvidence := FailureObservation{EvidenceID: "failure-parallel-" + suffix, EvidenceType: "AGENT_CONNECTIVITY_LOSS",
		SourceType: "CONTROL_PLANE", SourceHostID: host, SourceSessionGeneration: 1,
		SourceCredentialBindingRevision: 1, ObservationGeneration: 3, ObservedState: "ABSENT",
		FreshnessState: "CURRENT", ObservedAt: time.Now().UTC().Add(2 * time.Second), PayloadDigest: digestBytes([]byte("parallel-observation"))}
	var parallelEvidenceResults [2]FailureObservation
	var parallelEvidenceErrors [2]error
	rebindWG.Add(2)
	for i := range parallelEvidenceResults {
		go func(i int) {
			defer rebindWG.Done()
			parallelEvidenceResults[i], parallelEvidenceErrors[i] = AppendFailureObservation(ctx, pool, epoch1.FailureEpochID, parallelEvidence)
		}(i)
	}
	rebindWG.Wait()
	if parallelEvidenceErrors[0] != nil || parallelEvidenceErrors[1] != nil || parallelEvidenceResults[0].EvidenceGeneration != 3 || parallelEvidenceResults[1].EvidenceGeneration != 3 || parallelEvidenceResults[0].EvidenceDigest != parallelEvidenceResults[1].EvidenceDigest {
		t.Fatalf("parallel evidence results=%+v errors=%v", parallelEvidenceResults, parallelEvidenceErrors)
	}
	late := FailureObservation{EvidenceID: "failure-late-" + suffix, EvidenceType: "AGENT_CONNECTIVITY_LOSS",
		SourceType: "CONTROL_PLANE", SourceHostID: host, SourceSessionGeneration: 1,
		SourceCredentialBindingRevision: 1, ObservationGeneration: 4, ObservedState: "CONFLICTING",
		FreshnessState: "STALE", ObservedAt: trigger.ObservedAt.Add(-time.Minute), PayloadDigest: digestBytes([]byte("late-observation"))}
	lateRecorded, err := AppendFailureObservation(ctx, pool, epoch1.FailureEpochID, late)
	if err != nil || lateRecorded.EvidenceGeneration != 4 {
		t.Fatalf("late evidence=%+v err=%v", lateRecorded, err)
	}
	var epochState string
	var transitionCount, evidenceCount int
	if err := pool.QueryRow(ctx, `SELECT c.epoch_state,
		(SELECT count(*) FROM kim.failure_epoch_transition_evidence WHERE failure_epoch_id=$1),
		(SELECT count(*) FROM kim.failure_observation_evidence WHERE failure_epoch_id=$1)
		FROM kim.failure_epochs_current c WHERE c.failure_epoch_id=$1`, epoch1.FailureEpochID).Scan(&epochState, &transitionCount, &evidenceCount); err != nil || epochState != "SUSPECTED" || transitionCount != 1 || evidenceCount != 4 {
		t.Fatalf("failure projection state=%s transitions=%d evidence=%d err=%v", epochState, transitionCount, evidenceCount, err)
	}

	// Rebind after epoch creation does not rewrite epoch responsibility. A new
	// incident can bind the new current revision.
	failureTarget := availabilityPolicyFixture("failure-rebind-target-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE")
	failureTargetDigest, err := PublishAvailabilityPolicy(ctx, pool, failureTarget)
	if err != nil {
		t.Fatal(err)
	}
	failureRebind := VMAvailabilityRebindRequest{RebindID: "failure-rebind-" + suffix, WorkloadID: r1.WorkloadID,
		ExpectedCurrentBindingRevision: failureSourceRevision, SourceBindingDigest: failureSourceDigest,
		TargetPolicyID: failureTarget.PolicyID, TargetPolicyRevision: 1, TargetPolicyDigest: failureTargetDigest,
		RequestedBy: "operator-failure", AuthorizedBy: "approver-failure",
		AuthorizationReference: "approval/failure/rebind", Reason: "failure epoch historical stability"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, failureRebind); err != nil {
		t.Fatal(err)
	}
	_, bindingAfterEpoch, err := DecideVMAvailabilityRebind(ctx, pool, failureRebind.RebindID, "decision-authority")
	if err != nil || bindingAfterEpoch.BindingRevision != failureSourceRevision+1 {
		t.Fatalf("post-epoch rebind=%+v err=%v", bindingAfterEpoch, err)
	}
	var historicalRevision, historicalPolicyRevision uint64
	if err := pool.QueryRow(ctx, `SELECT availability_binding_revision,availability_policy_revision FROM kim.failure_epoch_evidence WHERE failure_epoch_id=$1`, epoch1.FailureEpochID).Scan(&historicalRevision, &historicalPolicyRevision); err != nil || historicalRevision != failureSourceRevision || historicalPolicyRevision != epoch1.PolicyRevision {
		t.Fatalf("epoch history revision=%d policy=%d err=%v", historicalRevision, historicalPolicyRevision, err)
	}
	trigger2 := trigger
	trigger2.EvidenceID = "failure-trigger-2-" + suffix
	trigger2.ObservationGeneration = 4
	trigger2.PayloadDigest = digestBytes([]byte("second-incident"))
	secondEpochRequest := OpenFailureEpochRequest{OpenRequestID: "failure-open-2-" + suffix, FailureEpochID: "failure-epoch-2-" + suffix,
		IncidentKey: "host-connectivity-incident-2-" + suffix, WorkloadID: r1.WorkloadID,
		FailureClass: "HOST_CONNECTIVITY_LOSS", RequestedBy: "failure-detector/v1",
		ExpectedBindingRevision: bindingAfterEpoch.BindingRevision, ExpectedBindingDigest: bindingAfterEpoch.BindingDigest, Trigger: trigger2}
	epoch2, err := OpenFailureEpoch(ctx, pool, secondEpochRequest)
	if err != nil || epoch2.AvailabilityBindingRevision != bindingAfterEpoch.BindingRevision || epoch2.PolicyDigest != bindingAfterEpoch.PolicyDigest {
		t.Fatalf("epoch2=%+v err=%v", epoch2, err)
	}

	// Different open request identities for the same explicit incident key
	// converge to one epoch under the incident-scoped database lock.
	duplicateBase := secondEpochRequest
	duplicateBase.IncidentKey = "duplicate-incident-" + suffix
	duplicateBase.FailureEpochID = "duplicate-epoch-a-" + suffix
	duplicateBase.OpenRequestID = "duplicate-open-a-" + suffix
	duplicateBase.Trigger.EvidenceID = "duplicate-evidence-a-" + suffix
	duplicateOther := duplicateBase
	duplicateOther.FailureEpochID = "duplicate-epoch-b-" + suffix
	duplicateOther.OpenRequestID = "duplicate-open-b-" + suffix
	duplicateOther.Trigger.EvidenceID = "duplicate-evidence-b-" + suffix
	var duplicateEpochs [2]FailureEpoch
	var duplicateErrors [2]error
	rebindWG.Add(2)
	go func() {
		defer rebindWG.Done()
		duplicateEpochs[0], duplicateErrors[0] = OpenFailureEpoch(ctx, pool, duplicateBase)
	}()
	go func() {
		defer rebindWG.Done()
		duplicateEpochs[1], duplicateErrors[1] = OpenFailureEpoch(ctx, pool, duplicateOther)
	}()
	rebindWG.Wait()
	if duplicateErrors[0] != nil || duplicateErrors[1] != nil || duplicateEpochs[0].FailureEpochID != duplicateEpochs[1].FailureEpochID {
		t.Fatalf("duplicate epoch outcomes=%+v errors=%v", duplicateEpochs, duplicateErrors)
	}
	var duplicateCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.failure_epoch_evidence WHERE workload_id=$1 AND failure_class='HOST_CONNECTIVITY_LOSS' AND incident_key=$2`, r1.WorkloadID, duplicateBase.IncidentKey).Scan(&duplicateCount); err != nil || duplicateCount != 1 {
		t.Fatalf("duplicate incident count=%d err=%v", duplicateCount, err)
	}

	// Ten deterministic races between epoch open and Rebind admit complete old
	// provenance or stale open only; mixed Binding/Policy rows are forbidden.
	for i := 0; i < 10; i++ {
		var sourceRevision uint64
		var sourceBindingDigest string
		if err := pool.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&sourceRevision, &sourceBindingDigest); err != nil {
			t.Fatal(err)
		}
		target := availabilityPolicyFixture(fmt.Sprintf("failure-race-policy-%d-%s", i, suffix), 1, "MANUAL", "NO_AUTOMATIC_ACTION", "ACTIVE")
		targetDigest, err := PublishAvailabilityPolicy(ctx, pool, target)
		if err != nil {
			t.Fatal(err)
		}
		rb := VMAvailabilityRebindRequest{RebindID: fmt.Sprintf("failure-race-rebind-%d-%s", i, suffix), WorkloadID: r1.WorkloadID,
			ExpectedCurrentBindingRevision: sourceRevision, SourceBindingDigest: sourceBindingDigest,
			TargetPolicyID: target.PolicyID, TargetPolicyRevision: 1, TargetPolicyDigest: targetDigest,
			RequestedBy: "operator-race", AuthorizedBy: "approver-race", AuthorizationReference: "approval/failure/race", Reason: "epoch rebind race"}
		if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, rb); err != nil {
			t.Fatal(err)
		}
		racingTrigger := trigger
		racingTrigger.EvidenceID = fmt.Sprintf("failure-race-evidence-%d-%s", i, suffix)
		racingTrigger.ObservationGeneration = uint64(100 + i)
		racingTrigger.PayloadDigest = digestBytes([]byte(fmt.Sprintf("failure-race-%d", i)))
		epochRequest := OpenFailureEpochRequest{OpenRequestID: fmt.Sprintf("failure-race-open-%d-%s", i, suffix), FailureEpochID: fmt.Sprintf("failure-race-epoch-%d-%s", i, suffix),
			IncidentKey: fmt.Sprintf("failure-race-incident-%d-%s", i, suffix), WorkloadID: r1.WorkloadID,
			FailureClass: "HOST_CONNECTIVITY_LOSS", RequestedBy: "failure-detector/v1",
			ExpectedBindingRevision: sourceRevision, ExpectedBindingDigest: sourceBindingDigest, Trigger: racingTrigger}
		var opened FailureEpoch
		var openErr, rebindRaceErr error
		rebindWG.Add(2)
		go func() { defer rebindWG.Done(); opened, openErr = OpenFailureEpoch(ctx, pool, epochRequest) }()
		go func() {
			defer rebindWG.Done()
			_, _, rebindRaceErr = DecideVMAvailabilityRebind(ctx, pool, rb.RebindID, "decision-authority")
		}()
		rebindWG.Wait()
		if rebindRaceErr != nil {
			t.Fatalf("race %d rebind=%v", i, rebindRaceErr)
		}
		if openErr == nil {
			if opened.AvailabilityBindingRevision != sourceRevision || opened.AvailabilityBindingDigest != sourceBindingDigest {
				t.Fatalf("race %d mixed epoch=%+v source=%d/%s", i, opened, sourceRevision, sourceBindingDigest)
			}
			var exactPolicyID string
			if err := pool.QueryRow(ctx, `SELECT availability_policy_id FROM kim.vm_availability_binding_evidence WHERE workload_id=$1 AND binding_revision=$2 AND binding_digest=$3`, r1.WorkloadID, opened.AvailabilityBindingRevision, opened.AvailabilityBindingDigest).Scan(&exactPolicyID); err != nil || exactPolicyID != opened.PolicyID {
				t.Fatalf("race %d mixed policy=%s epoch=%s err=%v", i, exactPolicyID, opened.PolicyID, err)
			}
		} else if !errors.Is(openErr, ErrFailureEpochStale) {
			t.Fatalf("race %d open outcome=%v", i, openErr)
		}
	}
	failureCountsAfter := make([]int, 6)
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.compute_allocation_claims),
		(SELECT count(*) FROM kim.pci_vf_allocation_claims),
		(SELECT count(*) FROM kim.network_identity_claims),
		(SELECT count(*) FROM kim.volume_attachment_claims),
		(SELECT count(*) FROM kim.vm_power_observation_evidence),
		(SELECT count(*) FROM kim.execution_jobs)`).Scan(&failureCountsAfter[0], &failureCountsAfter[1], &failureCountsAfter[2], &failureCountsAfter[3], &failureCountsAfter[4], &failureCountsAfter[5]); err != nil {
		t.Fatal(err)
	}
	for i := range failureCountsBefore {
		if failureCountsBefore[i] != failureCountsAfter[i] {
			t.Fatalf("Failure Epoch caused runtime/resource mutation before=%v after=%v", failureCountsBefore, failureCountsAfter)
		}
	}

	// A pre-051 Availability Policy has no typed confirmation authority. Its
	// historical Epoch remains evaluable, but must fail closed without an
	// invented default Policy or a CONFIRMED transition.
	legacyEvaluation, err := EvaluateFailureConfirmation(ctx, pool, "legacy-confirmation-evaluation-"+suffix, epoch1.FailureEpochID, "confirmation-evaluator/v1", digestBytes([]byte("confirmation-evaluator/v1")))
	if err != nil || legacyEvaluation.ResultState != "NO_CONFIRMATION_POLICY" {
		t.Fatalf("legacy confirmation evaluation=%+v err=%v", legacyEvaluation, err)
	}
	if replay, err := EvaluateFailureConfirmation(ctx, pool, legacyEvaluation.EvaluationID, epoch1.FailureEpochID, legacyEvaluation.EvaluatorVersion, legacyEvaluation.EvaluatorDigest); err != nil || replay.EvaluationDigest != legacyEvaluation.EvaluationDigest {
		t.Fatalf("legacy evaluation replay=%+v err=%v", replay, err)
	}
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "legacy-confirmation-decision-"+suffix, legacyEvaluation.EvaluationID, "failure-authority/v1"); !errors.Is(err, ErrFailureConfirmationBlocked) {
		t.Fatalf("legacy Epoch confirmation=%v", err)
	}

	confirmationPolicy := FailureConfirmationPolicy{
		PolicyID: "host-connectivity-confirmation-" + suffix, PolicyRevision: 1,
		ApplicableFailureClass: "HOST_CONNECTIVITY_LOSS", ConfirmationMode: "ALL_REQUIRED_EVIDENCE",
		RequireDistinctSources: true, LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture",
		Requirements: []FailureConfirmationRequirement{
			{Ordinal: 1, EvidenceType: "AGENT_CONNECTIVITY_LOSS", ObservedState: "ABSENT", FreshnessState: "CURRENT", SourceType: "CONTROL_PLANE"},
			{Ordinal: 2, EvidenceType: "HOST_OPERATION_AUTHORITY_STATE", ObservedState: "PRESENT", FreshnessState: "CURRENT", SourceType: "CONTROL_PLANE"},
		},
	}
	confirmationPolicy, err = PublishFailureConfirmationPolicy(ctx, pool, confirmationPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := PublishFailureConfirmationPolicy(ctx, pool, confirmationPolicy); err != nil || replay.PolicyDigest != confirmationPolicy.PolicyDigest {
		t.Fatalf("confirmation Policy replay=%+v err=%v", replay, err)
	}
	legacyAttach := p1
	legacyAttach.FailureConfirmationPolicyID = confirmationPolicy.PolicyID
	legacyAttach.FailureConfirmationPolicyRevision = confirmationPolicy.PolicyRevision
	legacyAttach.FailureConfirmationPolicyDigest = confirmationPolicy.PolicyDigest
	if legacyDigest := availabilityPolicyDigestValue(legacyAttach); legacyDigest != d1 {
		t.Fatalf("pre-051 Availability Policy digest changed old=%s new=%s", d1, legacyDigest)
	}
	if _, err := PublishAvailabilityPolicy(ctx, pool, legacyAttach); err == nil {
		t.Fatalf("typed confirmation association backfilled into historical Availability Policy: %v", err)
	}

	// Move the test VM through the only explicit path to an Availability Policy
	// that references the exact typed confirmation Policy revision.
	var typedSourceRevision uint64
	var typedSourceDigest string
	if err := pool.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&typedSourceRevision, &typedSourceDigest); err != nil {
		t.Fatal(err)
	}
	typedAvailability := availabilityPolicyFixture("typed-confirmation-availability-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "EVACUATE", "ACTIVE")
	typedAvailability.FailureConfirmationPolicyID = confirmationPolicy.PolicyID
	typedAvailability.FailureConfirmationPolicyRevision = confirmationPolicy.PolicyRevision
	typedAvailability.FailureConfirmationPolicyDigest = confirmationPolicy.PolicyDigest
	typedAvailabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, typedAvailability)
	if err != nil {
		t.Fatal(err)
	}
	typedRebind := VMAvailabilityRebindRequest{RebindID: "typed-confirmation-rebind-" + suffix, WorkloadID: r1.WorkloadID,
		ExpectedCurrentBindingRevision: typedSourceRevision, SourceBindingDigest: typedSourceDigest,
		TargetPolicyID: typedAvailability.PolicyID, TargetPolicyRevision: typedAvailability.PolicyRevision, TargetPolicyDigest: typedAvailabilityDigest,
		RequestedBy: "operator", AuthorizedBy: "availability-authority", AuthorizationReference: "approval/typed-confirmation", Reason: "bind typed confirmation policy"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, typedRebind); err != nil {
		t.Fatal(err)
	}
	_, typedBinding, err := DecideVMAvailabilityRebind(ctx, pool, typedRebind.RebindID, "availability-authority")
	if err != nil {
		t.Fatal(err)
	}

	observationGeneration := uint64(1000)
	openConfirmationEpoch := func(label, agentState, freshness string) (FailureEpoch, FailureObservation) {
		var currentBindingRevision uint64
		var currentBindingDigest string
		if queryErr := pool.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&currentBindingRevision, &currentBindingDigest); queryErr != nil {
			t.Fatalf("load current Binding for %s: %v", label, queryErr)
		}
		observationGeneration++
		agent := FailureObservation{EvidenceID: label + "-agent-" + suffix, EvidenceType: "AGENT_CONNECTIVITY_LOSS",
			SourceType: "CONTROL_PLANE", SourceHostID: host, SourceSessionGeneration: 1,
			SourceCredentialBindingRevision: 1, ObservationGeneration: observationGeneration,
			ObservedState: agentState, FreshnessState: freshness, ObservedAt: time.Now().UTC(), PayloadDigest: digestBytes([]byte(label + "/agent"))}
		epoch, openErr := OpenFailureEpoch(ctx, pool, OpenFailureEpochRequest{OpenRequestID: label + "-open-" + suffix,
			FailureEpochID: label + "-epoch-" + suffix, IncidentKey: label + "-incident-" + suffix, WorkloadID: r1.WorkloadID,
			FailureClass: "HOST_CONNECTIVITY_LOSS", RequestedBy: "failure-detector/v1",
			ExpectedBindingRevision: currentBindingRevision, ExpectedBindingDigest: currentBindingDigest, Trigger: agent})
		if openErr != nil {
			t.Fatalf("open %s: %v", label, openErr)
		}
		return epoch, agent
	}
	appendAuthorityEvidence := func(epoch FailureEpoch, label, state, freshness string) FailureObservation {
		observationGeneration++
		o := FailureObservation{EvidenceID: label + "-authority-" + suffix, EvidenceType: "HOST_OPERATION_AUTHORITY_STATE",
			SourceType: "CONTROL_PLANE", SourceHostID: host, SourceHostAuthorityGeneration: epoch.SourceHostAuthorityGeneration,
			ObservationGeneration: observationGeneration, ObservedState: state, FreshnessState: freshness,
			ObservedAt: time.Now().UTC(), PayloadDigest: digestBytes([]byte(label + "/authority/" + state + "/" + freshness))}
		appended, appendErr := AppendFailureObservation(ctx, pool, epoch.FailureEpochID, o)
		if appendErr != nil {
			t.Fatalf("append %s: %v", label, appendErr)
		}
		return appended
	}
	evaluate := func(epoch FailureEpoch, label string) FailureConfirmationEvaluation {
		e, evaluateErr := EvaluateFailureConfirmation(ctx, pool, label+"-evaluation-"+suffix, epoch.FailureEpochID, "confirmation-evaluator/v1", digestBytes([]byte("confirmation-evaluator/v1")))
		if evaluateErr != nil {
			t.Fatalf("evaluate %s: %v", label, evaluateErr)
		}
		return e
	}

	// Basic positive authority: Evaluation is immutable and pure; only the
	// explicit Decision appends the second transition and switches current.
	basicEpoch, _ := openConfirmationEpoch("confirmation-basic", "ABSENT", "CURRENT")
	appendAuthorityEvidence(basicEpoch, "confirmation-basic", "PRESENT", "CURRENT")
	basicEvaluation := evaluate(basicEpoch, "confirmation-basic")
	if basicEvaluation.ResultState != "SATISFIED" || basicEvaluation.PolicyRevision != 1 {
		t.Fatalf("basic evaluation=%+v", basicEvaluation)
	}
	if replay, err := EvaluateFailureConfirmation(ctx, pool, basicEvaluation.EvaluationID, basicEpoch.FailureEpochID, basicEvaluation.EvaluatorVersion, basicEvaluation.EvaluatorDigest); err != nil || replay.EvaluationDigest != basicEvaluation.EvaluationDigest {
		t.Fatalf("basic evaluation replay=%+v err=%v", replay, err)
	}
	var basicState string
	var basicTransitions int
	if err := pool.QueryRow(ctx, `SELECT c.epoch_state,(SELECT count(*) FROM kim.failure_epoch_transition_evidence t WHERE t.failure_epoch_id=c.failure_epoch_id) FROM kim.failure_epochs_current c WHERE c.failure_epoch_id=$1`, basicEpoch.FailureEpochID).Scan(&basicState, &basicTransitions); err != nil || basicState != "SUSPECTED" || basicTransitions != 1 {
		t.Fatalf("pure evaluation state=%s transitions=%d err=%v", basicState, basicTransitions, err)
	}
	basicDecision, confirmedEpoch, err := ConfirmFailureEpoch(ctx, pool, "confirmation-basic-decision-"+suffix, basicEvaluation.EvaluationID, "failure-authority/v1")
	if err != nil || confirmedEpoch.EpochState != "CONFIRMED" || confirmedEpoch.TransitionGeneration != 2 {
		t.Fatalf("basic Decision=%+v epoch=%+v err=%v", basicDecision, confirmedEpoch, err)
	}
	if replayDecision, replayEpoch, err := ConfirmFailureEpoch(ctx, pool, basicDecision.DecisionID, basicEvaluation.EvaluationID, basicDecision.DecidedBy); err != nil || replayDecision.DecisionDigest != basicDecision.DecisionDigest || replayEpoch.TransitionGeneration != 2 {
		t.Fatalf("Decision replay=%+v epoch=%+v err=%v", replayDecision, replayEpoch, err)
	}

	// UNKNOWN, STALE, and contradictory CURRENT evidence have different
	// immutable outcomes and can never authorize a Decision.
	unknownEpoch, _ := openConfirmationEpoch("confirmation-unknown", "UNKNOWN", "CURRENT")
	appendAuthorityEvidence(unknownEpoch, "confirmation-unknown", "PRESENT", "CURRENT")
	unknownEvaluation := evaluate(unknownEpoch, "confirmation-unknown")
	if unknownEvaluation.ResultState != "UNKNOWN" {
		t.Fatalf("UNKNOWN evaluation=%+v", unknownEvaluation)
	}
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "confirmation-unknown-decision-"+suffix, unknownEvaluation.EvaluationID, "failure-authority/v1"); !errors.Is(err, ErrFailureConfirmationBlocked) {
		t.Fatalf("UNKNOWN Decision=%v", err)
	}
	staleEpoch, _ := openConfirmationEpoch("confirmation-stale", "ABSENT", "STALE")
	appendAuthorityEvidence(staleEpoch, "confirmation-stale", "PRESENT", "CURRENT")
	staleEvaluation := evaluate(staleEpoch, "confirmation-stale")
	if staleEvaluation.ResultState != "STALE_EVIDENCE" {
		t.Fatalf("STALE evaluation=%+v", staleEvaluation)
	}
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "confirmation-stale-decision-"+suffix, staleEvaluation.EvaluationID, "failure-authority/v1"); !errors.Is(err, ErrFailureConfirmationBlocked) {
		t.Fatalf("STALE Decision=%v", err)
	}
	conflictEpoch, _ := openConfirmationEpoch("confirmation-conflict", "ABSENT", "CURRENT")
	appendAuthorityEvidence(conflictEpoch, "confirmation-conflict", "PRESENT", "CURRENT")
	observationGeneration++
	conflicting := FailureObservation{EvidenceID: "confirmation-conflict-agent-present-" + suffix, EvidenceType: "AGENT_CONNECTIVITY_LOSS",
		SourceType: "CONTROL_PLANE", SourceHostID: host, SourceSessionGeneration: 1, SourceCredentialBindingRevision: 1,
		ObservationGeneration: observationGeneration, ObservedState: "PRESENT", FreshnessState: "CURRENT", ObservedAt: time.Now().UTC(), PayloadDigest: digestBytes([]byte("conflicting agent evidence"))}
	if _, err := AppendFailureObservation(ctx, pool, conflictEpoch.FailureEpochID, conflicting); err != nil {
		t.Fatal(err)
	}
	conflictEvaluation := evaluate(conflictEpoch, "confirmation-conflict")
	if conflictEvaluation.ResultState != "CONFLICTING_INPUT" {
		t.Fatalf("CONFLICTING evaluation=%+v", conflictEvaluation)
	}
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "confirmation-conflict-decision-"+suffix, conflictEvaluation.EvaluationID, "failure-authority/v1"); !errors.Is(err, ErrFailureConfirmationBlocked) {
		t.Fatalf("CONFLICTING Decision=%v", err)
	}

	// Late evidence never rewrites E1. It makes E1 stale at Decision and
	// requires a new immutable Evaluation.
	driftEpoch, _ := openConfirmationEpoch("confirmation-evidence-drift", "ABSENT", "CURRENT")
	appendAuthorityEvidence(driftEpoch, "confirmation-evidence-drift", "PRESENT", "CURRENT")
	driftEvaluation := evaluate(driftEpoch, "confirmation-evidence-drift")
	appendAuthorityEvidence(driftEpoch, "confirmation-evidence-drift-late", "PRESENT", "CURRENT")
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "confirmation-evidence-drift-decision-"+suffix, driftEvaluation.EvaluationID, "failure-authority/v1"); !errors.Is(err, ErrFailureConfirmationStale) {
		t.Fatalf("evidence drift Decision=%v", err)
	}

	// Two Decisions for one SUSPECTED Epoch serialize at the Epoch authority:
	// exactly one transition commits and the other becomes stale.
	parallelEpoch, _ := openConfirmationEpoch("confirmation-parallel", "ABSENT", "CURRENT")
	appendAuthorityEvidence(parallelEpoch, "confirmation-parallel", "PRESENT", "CURRENT")
	parallelEvaluation := evaluate(parallelEpoch, "confirmation-parallel")
	var parallelDecisionErr [2]error
	rebindWG.Add(2)
	for i := range parallelDecisionErr {
		i := i
		go func() {
			defer rebindWG.Done()
			_, _, parallelDecisionErr[i] = ConfirmFailureEpoch(ctx, pool, fmt.Sprintf("confirmation-parallel-decision-%d-%s", i, suffix), parallelEvaluation.EvaluationID, "failure-authority/v1")
		}()
	}
	rebindWG.Wait()
	parallelSuccess, parallelStale := 0, 0
	for _, decisionErr := range parallelDecisionErr {
		if decisionErr == nil {
			parallelSuccess++
		} else if errors.Is(decisionErr, ErrFailureConfirmationStale) {
			parallelStale++
		} else {
			t.Fatalf("parallel Decision error=%v", decisionErr)
		}
	}
	if parallelSuccess != 1 || parallelStale != 1 {
		t.Fatalf("parallel Decision outcomes success=%d stale=%d errors=%v", parallelSuccess, parallelStale, parallelDecisionErr)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.failure_epoch_transition_evidence WHERE failure_epoch_id=$1 AND to_state='CONFIRMED'`, parallelEpoch.FailureEpochID).Scan(&basicTransitions); err != nil || basicTransitions != 1 {
		t.Fatalf("parallel CONFIRMED transitions=%d err=%v", basicTransitions, err)
	}

	// A later explicit Availability Rebind may race with Confirmation, but the
	// historical Epoch and its Decision remain bound to the old Binding.
	historicalEpoch, _ := openConfirmationEpoch("confirmation-rebind-race", "ABSENT", "CURRENT")
	appendAuthorityEvidence(historicalEpoch, "confirmation-rebind-race", "PRESENT", "CURRENT")
	historicalEvaluation := evaluate(historicalEpoch, "confirmation-rebind-race")
	nextAvailability := availabilityPolicyFixture("post-epoch-availability-"+suffix, 1, "MANUAL", "NO_AUTOMATIC_ACTION", "ACTIVE")
	nextAvailability.FailureConfirmationPolicyID = confirmationPolicy.PolicyID
	nextAvailability.FailureConfirmationPolicyRevision = confirmationPolicy.PolicyRevision
	nextAvailability.FailureConfirmationPolicyDigest = confirmationPolicy.PolicyDigest
	nextAvailabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, nextAvailability)
	if err != nil {
		t.Fatal(err)
	}
	historicalRebind := VMAvailabilityRebindRequest{RebindID: "confirmation-history-rebind-" + suffix, WorkloadID: r1.WorkloadID,
		ExpectedCurrentBindingRevision: typedBinding.BindingRevision, SourceBindingDigest: typedBinding.BindingDigest,
		TargetPolicyID: nextAvailability.PolicyID, TargetPolicyRevision: 1, TargetPolicyDigest: nextAvailabilityDigest,
		RequestedBy: "operator", AuthorizedBy: "availability-authority", AuthorizationReference: "approval/history", Reason: "confirmation history race"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, historicalRebind); err != nil {
		t.Fatal(err)
	}
	var historyConfirmErr, historyRebindErr error
	rebindWG.Add(2)
	go func() {
		defer rebindWG.Done()
		_, _, historyConfirmErr = ConfirmFailureEpoch(ctx, pool, "confirmation-history-decision-"+suffix, historicalEvaluation.EvaluationID, "failure-authority/v1")
	}()
	go func() {
		defer rebindWG.Done()
		_, _, historyRebindErr = DecideVMAvailabilityRebind(ctx, pool, historicalRebind.RebindID, "availability-authority")
	}()
	rebindWG.Wait()
	if historyConfirmErr != nil || historyRebindErr != nil {
		t.Fatalf("Confirmation/Rebind race confirm=%v rebind=%v", historyConfirmErr, historyRebindErr)
	}
	var historicalConfirmationBinding uint64
	if err := pool.QueryRow(ctx, `SELECT e.availability_binding_revision FROM kim.failure_confirmation_decision_evidence d JOIN kim.failure_confirmation_evaluation_evidence e ON e.evaluation_id=d.evaluation_id WHERE d.decision_id=$1`, "confirmation-history-decision-"+suffix).Scan(&historicalConfirmationBinding); err != nil || historicalConfirmationBinding != typedBinding.BindingRevision {
		t.Fatalf("historical Confirmation binding=%d want=%d err=%v", historicalConfirmationBinding, typedBinding.BindingRevision, err)
	}

	// Policy current revision switching races with Decision. The only allowed
	// outcomes are a complete rev1 Decision or stale rejection; no rev2 uplift.
	policyDriftEpoch, _ := openConfirmationEpoch("confirmation-policy-drift", "ABSENT", "CURRENT")
	appendAuthorityEvidence(policyDriftEpoch, "confirmation-policy-drift", "PRESENT", "CURRENT")
	policyDriftEvaluation := evaluate(policyDriftEpoch, "confirmation-policy-drift")
	confirmationPolicyV2 := confirmationPolicy
	confirmationPolicyV2.PolicyRevision = 2
	confirmationPolicyV2.PolicyDigest = ""
	confirmationPolicyV2.RequirementsDigest = ""
	var policyDecisionErr, policyPublishErr error
	rebindWG.Add(2)
	go func() {
		defer rebindWG.Done()
		_, _, policyDecisionErr = ConfirmFailureEpoch(ctx, pool, "confirmation-policy-drift-decision-"+suffix, policyDriftEvaluation.EvaluationID, "failure-authority/v1")
	}()
	go func() {
		defer rebindWG.Done()
		_, policyPublishErr = PublishFailureConfirmationPolicy(ctx, pool, confirmationPolicyV2)
	}()
	rebindWG.Wait()
	if policyPublishErr != nil || (policyDecisionErr != nil && !errors.Is(policyDecisionErr, ErrFailureConfirmationStale)) {
		t.Fatalf("Policy/Decision race publish=%v decision=%v", policyPublishErr, policyDecisionErr)
	}
	var decisionPolicyRevision uint64
	err = pool.QueryRow(ctx, `SELECT confirmation_policy_revision FROM kim.failure_confirmation_decision_evidence WHERE decision_id=$1`, "confirmation-policy-drift-decision-"+suffix).Scan(&decisionPolicyRevision)
	if policyDecisionErr == nil {
		if err != nil || decisionPolicyRevision != 1 {
			t.Fatalf("Policy uplift revision=%d err=%v", decisionPolicyRevision, err)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale Decision unexpectedly persisted err=%v", err)
	}

	// Historical reconstruction uses only immutable evidence. Confirmation has
	// no Host authority, resource, power, Job, fencing, or Recovery side effect.
	var auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.failure_epoch_transition_evidence t
		JOIN kim.failure_confirmation_decision_evidence d ON d.decision_id=t.confirmation_decision_id
		JOIN kim.failure_confirmation_evaluation_evidence e ON e.evaluation_id=d.evaluation_id
		JOIN kim.failure_confirmation_policy_revision_evidence p ON p.policy_id=d.confirmation_policy_id AND p.policy_revision=d.confirmation_policy_revision AND p.policy_digest=d.confirmation_policy_digest
		JOIN kim.failure_confirmation_evaluation_input_evidence i ON i.evaluation_id=e.evaluation_id
		JOIN kim.failure_observation_evidence o ON o.evidence_id=i.evidence_id AND o.evidence_generation=i.evidence_generation AND o.evidence_digest=i.evidence_digest
		JOIN kim.failure_epoch_evidence f ON f.failure_epoch_id=d.failure_epoch_id
		JOIN kim.vm_availability_binding_evidence b ON b.workload_id=f.workload_id AND b.binding_revision=f.availability_binding_revision AND b.binding_digest=f.availability_binding_digest
		JOIN kim.availability_policy_revision_evidence a ON a.policy_id=f.availability_policy_id AND a.policy_revision=f.availability_policy_revision AND a.policy_digest=f.availability_policy_digest
		WHERE d.decision_id=$1 AND t.to_state='CONFIRMED'`, basicDecision.DecisionID).Scan(&auditRows); err != nil || auditRows != 2 {
		t.Fatalf("Confirmation audit chain rows=%d err=%v", auditRows, err)
	}
	failureCountsFinal := make([]int, 6)
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.compute_allocation_claims),
		(SELECT count(*) FROM kim.pci_vf_allocation_claims),
		(SELECT count(*) FROM kim.network_identity_claims),
		(SELECT count(*) FROM kim.volume_attachment_claims),
		(SELECT count(*) FROM kim.vm_power_observation_evidence),
		(SELECT count(*) FROM kim.execution_jobs)`).Scan(&failureCountsFinal[0], &failureCountsFinal[1], &failureCountsFinal[2], &failureCountsFinal[3], &failureCountsFinal[4], &failureCountsFinal[5]); err != nil {
		t.Fatal(err)
	}
	for i := range failureCountsAfter {
		if failureCountsAfter[i] != failureCountsFinal[i] {
			t.Fatalf("Failure Confirmation caused runtime/resource mutation before=%v after=%v", failureCountsAfter, failureCountsFinal)
		}
	}
	var hostAuthorityState string
	var hostAuthorityGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT authority_state,authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1`, host).Scan(&hostAuthorityState, &hostAuthorityGeneration); err != nil || hostAuthorityState != "ARMED" || hostAuthorityGeneration != 1 {
		t.Fatalf("Failure Confirmation changed Host authority state=%s generation=%d err=%v", hostAuthorityState, hostAuthorityGeneration, err)
	}
}
