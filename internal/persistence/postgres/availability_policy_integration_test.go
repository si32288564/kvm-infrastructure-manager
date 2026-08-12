package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
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

	// Typed fencing and Local LVM storage safety are independent authorities.
	fencingPolicy, err := PublishFailureFencingPolicy(ctx, pool, FailureFencingPolicy{PolicyID: "fencing-policy-" + suffix, PolicyRevision: 1, FencingMode: "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF", LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	storageSafetyPolicy, err := PublishStorageSafetyPolicy(ctx, pool, StorageSafetyPolicy{PolicyID: "storage-safety-policy-" + suffix, PolicyRevision: 1, StorageClass: "LOCAL_LVM", SafetyMode: "SOURCE_DETACHED_NO_HOLDER", LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	recoveryBudgetPolicy, err := PublishRecoveryBudgetPolicy(ctx, pool, RecoveryBudgetPolicy{PolicyID: "recovery-budget-policy-" + suffix, PolicyRevision: 1, ScopeType: "GLOBAL", Phase: "PLANNING", MaxActiveRecoveries: 1, LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := PublishRecoveryBudgetPolicy(ctx, pool, recoveryBudgetPolicy); err != nil || replay.PolicyDigest != recoveryBudgetPolicy.PolicyDigest {
		t.Fatalf("Recovery Budget Policy replay=%+v err=%v", replay, err)
	}
	if replay, err := PublishFailureFencingPolicy(ctx, pool, fencingPolicy); err != nil || replay.PolicyDigest != fencingPolicy.PolicyDigest {
		t.Fatalf("Fencing Policy replay=%+v err=%v", replay, err)
	}
	if replay, err := PublishStorageSafetyPolicy(ctx, pool, storageSafetyPolicy); err != nil || replay.PolicyDigest != storageSafetyPolicy.PolicyDigest {
		t.Fatalf("Storage Safety Policy replay=%+v err=%v", replay, err)
	}
	var safetySourceRevision uint64
	var safetySourceDigest string
	if err := pool.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&safetySourceRevision, &safetySourceDigest); err != nil {
		t.Fatal(err)
	}
	safetyAvailability := availabilityPolicyFixture("safety-availability-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE")
	safetyAvailability.FailureConfirmationPolicyID, safetyAvailability.FailureConfirmationPolicyRevision, safetyAvailability.FailureConfirmationPolicyDigest = confirmationPolicy.PolicyID, 1, confirmationPolicy.PolicyDigest
	safetyAvailability.FencingPolicyID, safetyAvailability.FencingPolicyRevision, safetyAvailability.FencingPolicyDigest = fencingPolicy.PolicyID, 1, fencingPolicy.PolicyDigest
	safetyAvailability.StorageSafetyPolicyID, safetyAvailability.StorageSafetyPolicyRevision, safetyAvailability.StorageSafetyPolicyDigest = storageSafetyPolicy.PolicyID, 1, storageSafetyPolicy.PolicyDigest
	safetyAvailability.RecoveryBudgetPolicyID, safetyAvailability.RecoveryBudgetPolicyRevision, safetyAvailability.RecoveryBudgetPolicyDigest = recoveryBudgetPolicy.PolicyID, 1, recoveryBudgetPolicy.PolicyDigest
	safetyAvailabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, safetyAvailability)
	if err != nil {
		t.Fatal(err)
	}
	safetyRebind := VMAvailabilityRebindRequest{RebindID: "safety-rebind-" + suffix, WorkloadID: r1.WorkloadID, ExpectedCurrentBindingRevision: safetySourceRevision, SourceBindingDigest: safetySourceDigest, TargetPolicyID: safetyAvailability.PolicyID, TargetPolicyRevision: 1, TargetPolicyDigest: safetyAvailabilityDigest, RequestedBy: "operator", AuthorizedBy: "availability-authority", AuthorizationReference: "approval/safety", Reason: "bind exact safety policies"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, safetyRebind); err != nil {
		t.Fatal(err)
	}
	_, safetyBinding, err := DecideVMAvailabilityRebind(ctx, pool, safetyRebind.RebindID, "availability-authority")
	if err != nil {
		t.Fatal(err)
	}

	// Seed exact standard libvirt power evidence and Local LVM attachment
	// evidence. These are qualification fixtures, not safety authority by
	// themselves; the Evaluations snapshot and classify them later.
	vmID := fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1000000000000)
	if _, err := pool.Exec(ctx, `INSERT INTO kim.virtual_machines_current(vm_id,placement_admission_id,project_id,workload_id,host_id,vm_generation,desired_power_state,lifecycle_state) VALUES($1,$2,'project',$3,$4,1,'SHUTOFF','DEFINED')`, vmID, admission.AdmissionID, r1.WorkloadID, host); err != nil {
		t.Fatal(err)
	}
	seedTypedVerification := func(label, resourceType, resourceID, commandType, schemaVersion, targetResourceID string, observationGeneration int64, payload map[string]any) (string, string) {
		jobID, commandID, verificationID := label+"-job-"+suffix, label+"-command-"+suffix, label+"-verification-"+suffix
		observationDigest := digestBytes([]byte(label + "/observation"))
		verifierDigest := digestBytes([]byte(label + "/verifier"))
		if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: jobID, CommandID: commandID, HostID: host, ResourceType: resourceType, ResourceID: resourceID, DesiredRevision: 1, CommandType: commandType, SchemaVersion: schemaVersion, TargetResourceID: targetResourceID, Payload: payload}); err != nil {
			t.Fatalf("create verification command %s: %v", label, err)
		}
		var authorityGeneration int64
		if err := pool.QueryRow(ctx, `SELECT authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1 AND authority_state='ARMED'`, host).Scan(&authorityGeneration); err != nil {
			t.Fatal(err)
		}
		grant, err := AcquireCommandLease(ctx, pool, CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authorityGeneration, Duration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		result := contract.CommandResult{SchemaVersion: contract.CommandResultSchema, CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, JournalDigest: digestBytes([]byte(label + "/journal")), ResultID: label + "-result-" + suffix, Outcome: "SUCCEEDED", Result: map[string]any{"state": "OBSERVED"}, Observation: contract.Observation{State: "MATCHED", Digest: observationDigest, Generation: observationGeneration, Evidence: payload}, VerifierDigest: verifierDigest}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		envelope := session.NewEnvelope(host, uint64(grant.SessionGeneration), session.StreamResult, label+"-message-"+suffix, contract.CommandResultSchema, commandID, uint64(grant.AttemptIndex), encoded)
		envelope.CorrelationKey = commandID
		if receipt, err := AcceptAgentCommandResult(ctx, pool, envelope, 1<<20, AgentCommandResultDecision{Start: CommandAttemptStart{CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, JournalEvidenceDigest: result.JournalDigest}, Result: CommandResultSubmission{CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, ResultID: result.ResultID, Outcome: result.Outcome, Payload: result.Result}, Verification: &CommandVerification{VerificationID: verificationID, CommandID: commandID, AttemptIndex: grant.AttemptIndex, ObservationGeneration: observationGeneration, ObservationDigest: observationDigest, State: "MATCHED", VerifierArtifactDigest: verifierDigest, Evidence: payload}}); err != nil || receipt.Disposition != "ACCEPTED" {
			t.Fatalf("accept verification %s receipt=%+v err=%v", label, receipt, err)
		}
		return commandID, verificationID
	}
	seedVerification := func(label, resourceType, resourceID string, payload map[string]any) (string, string) {
		return seedTypedVerification(label, resourceType, resourceID, "QUALIFICATION_READ_BACK", "kim.qualification.read-back/v1", resourceID, 1, payload)
	}
	powerCommand, powerVerification := seedVerification("safety-power", "QUALIFICATION_OBSERVATION", vmID, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF"})
	powerEvidenceID := "safety-power-evidence-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.vm_power_observation_evidence(evidence_id,vm_id,vm_generation,host_id,command_id,attempt_index,verification_id,desired_power_state,observed_power_state,observation_generation,observation_digest,verifier_digest) VALUES($1,$2,1,$3,$4,1,$5,'SHUTOFF','SHUTOFF',1,$6,$7); INSERT INTO kim.vm_power_state_current(vm_id,vm_generation,desired_power_state,observed_power_state,convergence_state,observation_generation,evidence_id) VALUES($2,1,'SHUTOFF','SHUTOFF','MATCHED',1,$1)`, pgx.QueryExecModeSimpleProtocol, powerEvidenceID, vmID, host, powerCommand, powerVerification, digestBytes([]byte("safety power observation")), digestBytes([]byte("safety power verifier"))); err != nil {
		t.Fatal(err)
	}

	backendID, vgUUID := "safety-backend-"+suffix, "safety-vg-"+suffix
	classID, volumeID := "safety-class-"+suffix, "safety-volume-"+suffix
	bindingID, attachmentID := "safety-binding-"+suffix, "safety-attachment-"+suffix
	resourceKey := "kim-" + digestBytes([]byte("safety-resource-" + suffix))[:32]
	lvUUID := "safety-lv-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.storage_backends_current(backend_id,backend_type,backend_generation,lifecycle_state,host_id,vg_uuid,capability_generation,capability_state,support_tier) VALUES($1,'LOCAL_LVM',1,'ACTIVE',$2,$3,1,'CURRENT','VALIDATED'); INSERT INTO kim.storage_class_revision_evidence(storage_class_id,class_revision,allowed_backend_type,locality,access_modes,thin_provisioning,encryption_required,fencing_policy_revision,class_digest) VALUES($4,1,'LOCAL_LVM','HOST_LOCAL',ARRAY['SINGLE_WRITER'],false,false,1,$5); INSERT INTO kim.storage_classes_current(storage_class_id,class_revision,lifecycle_state) VALUES($4,1,'ACTIVE'); INSERT INTO kim.volumes_current(volume_id,placement_admission_id,project_id,storage_class_id,storage_class_revision,desired_generation,size_bytes,access_mode,bootable,lifecycle_state) VALUES($6,$7,'project',$4,1,1,4096,'SINGLE_WRITER',true,'AVAILABLE'); INSERT INTO kim.volume_backend_binding_intents(binding_id,placement_admission_id,volume_id,binding_generation,backend_id,host_id,vg_uuid,backend_resource_key,binding_state,observed_lv_uuid) VALUES($8,$7,$6,1,$1,$2,$3,$9,'BOUND',$10)`, pgx.QueryExecModeSimpleProtocol, backendID, host, vgUUID, classID, digestBytes([]byte("safety class")), volumeID, admission.AdmissionID, bindingID, resourceKey, lvUUID); err != nil {
		t.Fatal(err)
	}
	bindingCommand, bindingVerification := seedVerification("safety-binding", "VOLUME", volumeID, map[string]any{"binding_id": bindingID, "lv_uuid": lvUUID})
	if _, err := pool.Exec(ctx, `INSERT INTO kim.volume_backend_binding_evidence(evidence_id,binding_id,volume_id,binding_generation,backend_id,host_id,vg_uuid,lv_uuid,backend_resource_key,observed_size_bytes,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,evidence_state) VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,4096,$9,1,$10,1,$11,$12,'MATCHED'); INSERT INTO kim.volume_backend_bindings_current(binding_id,volume_id,binding_generation,observation_generation,evidence_id,binding_state,host_id,vg_uuid,lv_uuid,backend_resource_key) VALUES($2,$3,1,1,$1,'BOUND',$5,$6,$7,$8); INSERT INTO kim.volume_attachments_current(attachment_id,placement_admission_id,volume_id,workload_id,desired_host_id,attachment_generation,access_mode,desired_state) VALUES($13,$14,$3,$15,$5,1,'SINGLE_WRITER','DETACHED'); INSERT INTO kim.volume_attachment_claims(attachment_claim_id,placement_admission_id,attachment_id,volume_id,workload_id,host_id,attachment_generation,access_mode,fencing_policy_revision,claim_state) VALUES($16,$14,$13,$3,$15,$5,1,'SINGLE_WRITER',1,'RELEASED')`, pgx.QueryExecModeSimpleProtocol, "safety-binding-evidence-"+suffix, bindingID, volumeID, backendID, host, vgUUID, lvUUID, resourceKey, bindingCommand, bindingVerification, digestBytes([]byte("safety binding observation")), digestBytes([]byte("safety binding verifier")), attachmentID, admission.AdmissionID, r1.WorkloadID, "safety-attachment-claim-"+suffix); err != nil {
		t.Fatal(err)
	}
	attachmentCommand, attachmentVerification := seedVerification("safety-attachment", "VOLUME_ATTACHMENT", attachmentID, map[string]any{"attachment_id": attachmentID, "desired_state": "DETACHED"})
	unknownAttachmentEvidence := "safety-attachment-unknown-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.volume_attachment_observation_evidence(evidence_id,attachment_id,volume_id,attachment_generation,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,desired_state,device_present,device_identity_matches,source_identity_matches,holder_open,read_only,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,evidence_state) VALUES($1,$2,$3,1,$4,1,$5,$6,'vdb',$7,'DETACHED',false,false,false,false,false,$8,1,$9,1,$10,$11,'UNKNOWN'); INSERT INTO kim.volume_attachment_observations_current(attachment_id,volume_id,attachment_generation,observation_generation,evidence_id,attachment_state,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,device_present,holder_open) VALUES($2,$3,1,1,$1,'UNKNOWN',$4,1,$5,$6,'vdb',$7,false,false)`, pgx.QueryExecModeSimpleProtocol, unknownAttachmentEvidence, attachmentID, volumeID, bindingID, host, vmID, lvUUID, attachmentCommand, attachmentVerification, digestBytes([]byte("unknown attachment observation")), digestBytes([]byte("attachment verifier"))); err != nil {
		t.Fatal(err)
	}
	sourcePlanID, sourcePlanDigest := "source-root-plan-"+suffix, digestBytes([]byte("source root materialization plan "+suffix))
	if _, err := pool.Exec(ctx, `INSERT INTO kim.vm_materialization_plan_evidence(plan_id,vm_id,vm_generation,placement_admission_id,host_id,image_id,image_revision,flavor_id,flavor_revision,flavor_shape_digest,compute_allocation_id,root_volume_id,root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation,plan_payload,plan_digest) SELECT $1,$2::uuid,1,a.admission_id,a.host_id,a.image_id,a.image_revision,a.flavor_id,a.flavor_revision,a.flavor_shape_digest,c.allocation_id,$3,$4,1,$5,1,'{}'::jsonb,$6 FROM kim.placement_admission_decisions a JOIN kim.compute_allocation_claims c ON c.admission_id=a.admission_id WHERE a.admission_id=$7; UPDATE kim.virtual_machines_current SET current_plan_id=$1 WHERE vm_id=$2::uuid`, pgx.QueryExecModeSimpleProtocol, sourcePlanID, vmID, volumeID, bindingID, attachmentID, sourcePlanDigest, admission.AdmissionID); err != nil {
		t.Fatal(err)
	}
	rootObservationPayload := map[string]any{"attachment_id": attachmentID, "volume_id": volumeID, "binding_id": bindingID, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": lvUUID, "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": false}
	rootCommand, rootVerification := seedTypedVerification("source-root-safe", "SOURCE_ROOT_SAFETY", attachmentID, SourceRootSafetyReadBackCommandType, SourceRootSafetyReadBackSchema, "attachment:"+attachmentID, 3, rootObservationPayload)

	safetyEpoch, _ := openConfirmationEpoch("failure-safety", "ABSENT", "CURRENT")
	if safetyEpoch.AvailabilityBindingRevision != safetyBinding.BindingRevision {
		t.Fatalf("Safety Epoch binding=%d want=%d", safetyEpoch.AvailabilityBindingRevision, safetyBinding.BindingRevision)
	}
	appendAuthorityEvidence(safetyEpoch, "failure-safety", "PRESENT", "CURRENT")
	safetyConfirmation := evaluate(safetyEpoch, "failure-safety")
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "failure-safety-confirmation-"+suffix, safetyConfirmation.EvaluationID, "failure-authority/v1"); err != nil {
		t.Fatal(err)
	}
	budgetRaceEpoch, _ := openConfirmationEpoch("failure-budget-race", "ABSENT", "CURRENT")
	if budgetRaceEpoch.AvailabilityBindingRevision != safetyBinding.BindingRevision || budgetRaceEpoch.PolicyID != safetyAvailability.PolicyID {
		t.Fatalf("Budget-race Epoch historical binding=%d/%s want=%d/%s", budgetRaceEpoch.AvailabilityBindingRevision, budgetRaceEpoch.PolicyID, safetyBinding.BindingRevision, safetyAvailability.PolicyID)
	}
	appendAuthorityEvidence(budgetRaceEpoch, "failure-budget-race", "PRESENT", "CURRENT")
	budgetRaceConfirmation := evaluate(budgetRaceEpoch, "failure-budget-race")
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "failure-budget-race-confirmation-"+suffix, budgetRaceConfirmation.EvaluationID, "failure-authority/v1"); err != nil {
		t.Fatal(err)
	}
	postEpochAvailability := availabilityPolicyFixture("post-epoch-safety-availability-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "EVACUATE", "ACTIVE")
	postEpochAvailability.FailureConfirmationPolicyID, postEpochAvailability.FailureConfirmationPolicyRevision, postEpochAvailability.FailureConfirmationPolicyDigest = confirmationPolicy.PolicyID, 1, confirmationPolicy.PolicyDigest
	postEpochAvailability.FencingPolicyID, postEpochAvailability.FencingPolicyRevision, postEpochAvailability.FencingPolicyDigest = fencingPolicy.PolicyID, 1, fencingPolicy.PolicyDigest
	postEpochAvailability.StorageSafetyPolicyID, postEpochAvailability.StorageSafetyPolicyRevision, postEpochAvailability.StorageSafetyPolicyDigest = storageSafetyPolicy.PolicyID, 1, storageSafetyPolicy.PolicyDigest
	postEpochAvailabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, postEpochAvailability)
	if err != nil {
		t.Fatal(err)
	}
	postEpochRebind := VMAvailabilityRebindRequest{RebindID: "post-epoch-safety-rebind-" + suffix, WorkloadID: r1.WorkloadID, ExpectedCurrentBindingRevision: safetyBinding.BindingRevision, SourceBindingDigest: safetyBinding.BindingDigest, TargetPolicyID: postEpochAvailability.PolicyID, TargetPolicyRevision: 1, TargetPolicyDigest: postEpochAvailabilityDigest, RequestedBy: "operator", AuthorizedBy: "availability-authority", AuthorizationReference: "approval/post-epoch", Reason: "prove historical Failure Epoch safety provenance"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, postEpochRebind); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecideVMAvailabilityRebind(ctx, pool, postEpochRebind.RebindID, "availability-authority"); err != nil {
		t.Fatal(err)
	}
	missingFencingEligibility, err := EvaluateRecoveryEligibility(ctx, pool, "recovery-eligibility-missing-fencing-"+suffix, safetyEpoch.FailureEpochID, scopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
	if err != nil || missingFencingEligibility.ResultState != "FENCING_PROOF_MISSING" || missingFencingEligibility.FencingUsability != "MISSING" {
		t.Fatalf("missing Fencing Recovery Eligibility=%+v err=%v", missingFencingEligibility, err)
	}
	if _, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "recovery-eligibility-missing-fencing-decision-"+suffix, missingFencingEligibility.EvaluationID, "recovery-authority/v1"); !errors.Is(err, ErrRecoveryEligibilityBlocked) {
		t.Fatalf("missing Fencing positive Decision=%v", err)
	}

	unknownStorage, err := EvaluateStorageSafety(ctx, pool, "storage-safety-unknown-evaluation-"+suffix, safetyEpoch.FailureEpochID, "storage-safety-evaluator/v1", digestBytes([]byte("storage-safety-evaluator/v1")))
	if err != nil || unknownStorage.ResultState != "UNKNOWN" || unknownStorage.AvailabilityBindingRevision != safetyBinding.BindingRevision {
		t.Fatalf("unknown Storage evaluation=%+v err=%v", unknownStorage, err)
	}
	if _, err := MaterializeStorageSafetyProof(ctx, pool, "storage-safety-unknown-proof-"+suffix, unknownStorage.EvaluationID, "storage-safety-authority/v1"); !errors.Is(err, ErrFailureSafetyBlocked) {
		t.Fatalf("UNKNOWN Storage proof=%v", err)
	}
	safeAttachmentEvidence := "safety-attachment-safe-" + suffix
	safeAttachmentCommand, safeAttachmentVerification := seedVerification("safety-attachment-safe", "VOLUME_ATTACHMENT", attachmentID, map[string]any{"attachment_id": attachmentID, "desired_state": "DETACHED", "observed_state": "DETACHED"})
	if _, err := pool.Exec(ctx, `INSERT INTO kim.volume_attachment_observation_evidence(evidence_id,attachment_id,volume_id,attachment_generation,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,desired_state,device_present,device_identity_matches,source_identity_matches,holder_open,read_only,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,evidence_state) SELECT $1,attachment_id,volume_id,attachment_generation,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,'DETACHED',false,false,false,false,false,$5,1,$6,2,$2,verifier_digest,'MATCHED' FROM kim.volume_attachment_observation_evidence WHERE evidence_id=$3; UPDATE kim.volume_attachment_observations_current SET observation_generation=2,evidence_id=$1,attachment_state='DETACHED',device_present=false,holder_open=false,updated_at=statement_timestamp() WHERE attachment_id=$4`, pgx.QueryExecModeSimpleProtocol, safeAttachmentEvidence, digestBytes([]byte("safe attachment observation")), unknownAttachmentEvidence, attachmentID, safeAttachmentCommand, safeAttachmentVerification); err != nil {
		t.Fatal(err)
	}
	storageEvaluation, err := EvaluateStorageSafety(ctx, pool, "storage-safety-evaluation-"+suffix, safetyEpoch.FailureEpochID, "storage-safety-evaluator/v1", digestBytes([]byte("storage-safety-evaluator/v1")))
	if err != nil || storageEvaluation.ResultState != "SAFE" {
		t.Fatalf("SAFE Storage evaluation=%+v err=%v", storageEvaluation, err)
	}
	if replay, err := EvaluateStorageSafety(ctx, pool, storageEvaluation.EvaluationID, safetyEpoch.FailureEpochID, storageEvaluation.EvaluatorVersion, storageEvaluation.EvaluatorDigest); err != nil || replay.EvaluationDigest != storageEvaluation.EvaluationDigest {
		t.Fatalf("Storage evaluation replay=%+v err=%v", replay, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.volume_attachment_claims SET claim_state='ACTIVE' WHERE attachment_id=$1`, attachmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeStorageSafetyProof(ctx, pool, "storage-safety-stale-proof-"+suffix, storageEvaluation.EvaluationID, "storage-safety-authority/v1"); !errors.Is(err, ErrFailureSafetyStale) {
		t.Fatalf("Storage evidence drift proof=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.volume_attachment_claims SET claim_state='RELEASED' WHERE attachment_id=$1`, attachmentID); err != nil {
		t.Fatal(err)
	}
	storageEvaluation, err = EvaluateStorageSafety(ctx, pool, "storage-safety-current-evaluation-"+suffix, safetyEpoch.FailureEpochID, "storage-safety-evaluator/v1", digestBytes([]byte("storage-safety-evaluator/v1")))
	if err != nil || storageEvaluation.ResultState != "SAFE" {
		t.Fatalf("current SAFE Storage evaluation=%+v err=%v", storageEvaluation, err)
	}
	var storageProofResults [2]StorageSafetyProof
	var storageProofErrors [2]error
	var safetyWG sync.WaitGroup
	for i := range storageProofResults {
		safetyWG.Add(1)
		go func(index int) {
			defer safetyWG.Done()
			storageProofResults[index], storageProofErrors[index] = MaterializeStorageSafetyProof(ctx, pool, "storage-safety-proof-"+suffix, storageEvaluation.EvaluationID, "storage-safety-authority/v1")
		}(i)
	}
	safetyWG.Wait()
	if storageProofErrors[0] != nil || storageProofErrors[1] != nil || storageProofResults[0].ProofDigest != storageProofResults[1].ProofDigest {
		t.Fatalf("parallel Storage proofs=%+v errors=%v", storageProofResults, storageProofErrors)
	}
	storageProof := storageProofResults[0]
	if replay, err := MaterializeStorageSafetyProof(ctx, pool, storageProof.ProofID, storageEvaluation.EvaluationID, storageProof.DecidedBy); err != nil || replay.ProofDigest != storageProof.ProofDigest {
		t.Fatalf("Storage proof replay=%+v err=%v", replay, err)
	}
	budgetRaceStorageEvaluation, err := EvaluateStorageSafety(ctx, pool, "storage-safety-budget-race-evaluation-"+suffix, budgetRaceEpoch.FailureEpochID, "storage-safety-evaluator/v1", digestBytes([]byte("storage-safety-evaluator/v1")))
	if err != nil || budgetRaceStorageEvaluation.ResultState != "SAFE" {
		t.Fatalf("budget-race Storage evaluation=%+v err=%v", budgetRaceStorageEvaluation, err)
	}
	budgetRaceStorageProof, err := MaterializeStorageSafetyProof(ctx, pool, "storage-safety-budget-race-proof-"+suffix, budgetRaceStorageEvaluation.EvaluationID, "storage-safety-authority/v1")
	if err != nil || budgetRaceStorageProof.ProofState != "SAFE" {
		t.Fatalf("budget-race Storage proof=%+v err=%v", budgetRaceStorageProof, err)
	}

	// Connectivity loss plus a confirmed Epoch is still not fencing proof.
	unknownFenceObservation, err := RecordSourceExecutionFencingObservation(ctx, pool, "source-fence-unknown-"+suffix, safetyEpoch.FailureEpochID)
	if err != nil || unknownFenceObservation.ObservationState != "UNKNOWN" {
		t.Fatalf("unfenced observation=%+v err=%v", unknownFenceObservation, err)
	}
	unknownFenceEvaluation, err := EvaluateFailureFencing(ctx, pool, "source-fence-unknown-evaluation-"+suffix, safetyEpoch.FailureEpochID, "fencing-evaluator/v1", digestBytes([]byte("fencing-evaluator/v1")))
	if err != nil || unknownFenceEvaluation.ResultState != "UNKNOWN" || unknownFenceEvaluation.AvailabilityBindingRevision != safetyBinding.BindingRevision {
		t.Fatalf("unknown Fencing evaluation=%+v err=%v", unknownFenceEvaluation, err)
	}
	if _, _, err := MaterializeFailureFencingProof(ctx, pool, "source-fence-unknown-proof-"+suffix, unknownFenceEvaluation.EvaluationID, "fencing-authority/v1"); !errors.Is(err, ErrFailureSafetyBlocked) {
		t.Fatalf("UNKNOWN Fencing proof=%v", err)
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return fenceHostOperationAuthorityTx(ctx, tx, host, "failure_safety_fixture") }); err != nil {
		t.Fatal(err)
	}
	provenFenceObservation, err := RecordSourceExecutionFencingObservation(ctx, pool, "source-fence-proven-"+suffix, safetyEpoch.FailureEpochID)
	if err != nil || provenFenceObservation.ObservationState != "PROVEN" {
		t.Fatalf("proven Fencing observation=%+v err=%v", provenFenceObservation, err)
	}
	fencingEvaluation, err := EvaluateFailureFencing(ctx, pool, "source-fence-evaluation-"+suffix, safetyEpoch.FailureEpochID, "fencing-evaluator/v1", digestBytes([]byte("fencing-evaluator/v1")))
	if err != nil || fencingEvaluation.ResultState != "PROVEN" {
		t.Fatalf("Fencing evaluation=%+v err=%v", fencingEvaluation, err)
	}
	if replay, err := EvaluateFailureFencing(ctx, pool, fencingEvaluation.EvaluationID, safetyEpoch.FailureEpochID, fencingEvaluation.EvaluatorVersion, fencingEvaluation.EvaluatorDigest); err != nil || replay.EvaluationDigest != fencingEvaluation.EvaluationDigest {
		t.Fatalf("Fencing evaluation replay=%+v err=%v", replay, err)
	}
	var preProofEpochState string
	if err := pool.QueryRow(ctx, `SELECT epoch_state FROM kim.failure_epochs_current WHERE failure_epoch_id=$1`, safetyEpoch.FailureEpochID).Scan(&preProofEpochState); err != nil || preProofEpochState != "CONFIRMED" {
		t.Fatalf("Fencing Evaluation changed Epoch state=%s err=%v", preProofEpochState, err)
	}
	var preProofHostState string
	var preProofHostGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT authority_state,authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1`, host).Scan(&preProofHostState, &preProofHostGeneration); err != nil || preProofHostState != "FENCED" {
		t.Fatalf("pre-proof Host authority state=%s generation=%d err=%v", preProofHostState, preProofHostGeneration, err)
	}
	if _, err := RecordSourceExecutionFencingObservation(ctx, pool, "source-fence-drift-"+suffix, safetyEpoch.FailureEpochID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MaterializeFailureFencingProof(ctx, pool, "source-fence-stale-proof-"+suffix, fencingEvaluation.EvaluationID, "fencing-authority/v1"); !errors.Is(err, ErrFailureSafetyStale) {
		t.Fatalf("Fencing evidence drift proof=%v", err)
	}
	fencingEvaluation, err = EvaluateFailureFencing(ctx, pool, "source-fence-current-evaluation-"+suffix, safetyEpoch.FailureEpochID, "fencing-evaluator/v1", digestBytes([]byte("fencing-evaluator/v1")))
	if err != nil || fencingEvaluation.ResultState != "PROVEN" {
		t.Fatalf("current Fencing evaluation=%+v err=%v", fencingEvaluation, err)
	}
	var fencingProofResults [2]FailureFencingProof
	var fencedEpochResults [2]FailureEpoch
	var fencingProofErrors [2]error
	for i := range fencingProofResults {
		safetyWG.Add(1)
		go func(index int) {
			defer safetyWG.Done()
			fencingProofResults[index], fencedEpochResults[index], fencingProofErrors[index] = MaterializeFailureFencingProof(ctx, pool, "source-fence-proof-"+suffix, fencingEvaluation.EvaluationID, "fencing-authority/v1")
		}(i)
	}
	safetyWG.Wait()
	if fencingProofErrors[0] != nil || fencingProofErrors[1] != nil || fencingProofResults[0].ProofDigest != fencingProofResults[1].ProofDigest || fencedEpochResults[0].TransitionGeneration != fencedEpochResults[1].TransitionGeneration {
		t.Fatalf("parallel Fencing proofs=%+v epochs=%+v errors=%v", fencingProofResults, fencedEpochResults, fencingProofErrors)
	}
	fencingProof, fencedEpoch := fencingProofResults[0], fencedEpochResults[0]
	if replay, replayEpoch, err := MaterializeFailureFencingProof(ctx, pool, fencingProof.ProofID, fencingEvaluation.EvaluationID, fencingProof.DecidedBy); err != nil || replay.ProofDigest != fencingProof.ProofDigest || replayEpoch.TransitionGeneration != fencedEpoch.TransitionGeneration {
		t.Fatalf("Fencing proof replay=%+v epoch=%+v err=%v", replay, replayEpoch, err)
	}
	budgetRaceFenceObservation, err := RecordSourceExecutionFencingObservation(ctx, pool, "source-fence-budget-race-"+suffix, budgetRaceEpoch.FailureEpochID)
	if err != nil || budgetRaceFenceObservation.ObservationState != "PROVEN" {
		t.Fatalf("budget-race Fencing observation=%+v err=%v", budgetRaceFenceObservation, err)
	}
	budgetRaceFenceEvaluation, err := EvaluateFailureFencing(ctx, pool, "source-fence-budget-race-evaluation-"+suffix, budgetRaceEpoch.FailureEpochID, "fencing-evaluator/v1", digestBytes([]byte("fencing-evaluator/v1")))
	if err != nil || budgetRaceFenceEvaluation.ResultState != "PROVEN" {
		t.Fatalf("budget-race Fencing evaluation=%+v err=%v", budgetRaceFenceEvaluation, err)
	}
	budgetRaceFencingProof, budgetRaceFencedEpoch, err := MaterializeFailureFencingProof(ctx, pool, "source-fence-budget-race-proof-"+suffix, budgetRaceFenceEvaluation.EvaluationID, "fencing-authority/v1")
	if err != nil || budgetRaceFencingProof.ProofState != "PROVEN" || budgetRaceFencedEpoch.EpochState != "FENCED" {
		t.Fatalf("budget-race Fencing proof=%+v epoch=%+v err=%v", budgetRaceFencingProof, budgetRaceFencedEpoch, err)
	}
	noDestinationEvaluation, err := EvaluateRecoveryEligibility(ctx, pool, "recovery-eligibility-no-destination-"+suffix, safetyEpoch.FailureEpochID, scopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
	if err != nil || noDestinationEvaluation.ResultState != "NO_DESTINATION" || noDestinationEvaluation.EligibleDestinationCount != 0 {
		t.Fatalf("source-only Recovery Eligibility=%+v err=%v", noDestinationEvaluation, err)
	}

	// Recovery Eligibility binds the historical Epoch responsibility and both
	// current-usable safety proofs, but remains read-only until an explicit
	// Decision atomically reserves the bounded planning budget.
	destinationHost := "availability-recovery-destination-" + suffix
	destinationFingerprint := digestBytes([]byte("availability-recovery-destination-cert-" + suffix))
	prepareSessionIdentityFixture(t, ctx, pool, destinationHost, 1, destinationFingerprint)
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: destinationHost + "-attempt", HostID: destinationHost,
		ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1",
		AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1,
		PeerCertificateFingerprint: destinationFingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, pool, destinationHost)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: destinationHost, CapabilityGeneration: 1,
		BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED",
		ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: destinationHost, PolicyID: "availability-recovery-destination-policy",
		PolicyGeneration: 1, ActorID: "fixture", ReasonCode: "recovery_destination_fixture"}); err != nil {
		t.Fatal(err)
	}
	// Recovery planning must reserve an ordinary destination Local LVM boot
	// volume through Final Admission; it may not invent storage afterwards.
	destinationBackendID, destinationVGUUID := "recovery-destination-backend-"+suffix, "recovery-destination-vg-"+suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.storage_backends_current(backend_id,backend_type,backend_generation,lifecycle_state,host_id,vg_uuid,capability_generation,capability_state,support_tier) VALUES($1,'LOCAL_LVM',1,'ACTIVE',$2,$3,1,'CURRENT','VALIDATED'); INSERT INTO kim.storage_capacity_observation_evidence(observation_id,backend_id,capacity_generation,host_capability_generation,total_bytes,observed_free_bytes,external_or_unknown_bytes,hard_reserve_bytes,health_state,observation_digest,observed_at) VALUES($4,$1,1,1,10737418240,10737418240,0,0,'HEALTHY',$5,statement_timestamp()); INSERT INTO kim.storage_capacity_projections_current(backend_id,capacity_generation,observation_id,projection_state) VALUES($1,1,$4,'CURRENT')`, pgx.QueryExecModeSimpleProtocol, destinationBackendID, destinationHost, destinationVGUUID, "recovery-destination-capacity-"+suffix, digestBytes([]byte("recovery destination capacity"))); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "availability-recovery-set-2-" + suffix,
		HostGroupID: groupID, SourceType: "EXPLICIT", SourceRevision: "recovery-fixture", BasedOnHostGroupGeneration: 1,
		ExpectedCurrentSetGeneration: 1, Members: upgradeSnapshotMembers(groupID, []string{host, destinationHost}, 2, "recovery-fixture")}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "availability-recovery-binding-3-" + suffix,
		BindingID: binding.BindingID, ExpectedCurrentGeneration: 2, HostGroupID: groupID, HostGroupGeneration: 1,
		PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: safetyAvailability.PolicyID,
		PolicyRevision: 1, PolicyDigest: safetyAvailabilityDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	destinationDriftEvaluation, err := EvaluateRecoveryEligibility(ctx, pool, "recovery-eligibility-destination-drift-"+suffix, safetyEpoch.FailureEpochID, scopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
	if err != nil || destinationDriftEvaluation.ResultState != "ELIGIBLE" {
		t.Fatalf("pre-drift Recovery Eligibility=%+v err=%v", destinationDriftEvaluation, err)
	}
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: destinationHost, CapabilityGeneration: 1,
		BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED",
		ComplianceGeneration: 2, ComplianceState: "NON_COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "recovery-eligibility-destination-drift-decision-"+suffix, destinationDriftEvaluation.EvaluationID, "recovery-authority/v1"); !errors.Is(err, ErrRecoveryEligibilityStale) {
		t.Fatalf("destination drift Decision=%v", err)
	}
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: destinationHost, CapabilityGeneration: 1,
		BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED",
		ComplianceGeneration: 3, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: destinationHost, PolicyID: "availability-recovery-destination-policy",
		PolicyGeneration: 1, ActorID: "fixture", ReasonCode: "recovery_destination_rearm_after_drift"}); err != nil {
		t.Fatal(err)
	}

	eligibilityIDs := []string{"recovery-eligibility-a-" + suffix, "recovery-eligibility-b-" + suffix}
	eligibilityEpochs := []FailureEpoch{fencedEpoch, budgetRaceFencedEpoch}
	eligibility := make([]RecoveryEligibilityEvaluation, 2)
	for index := range eligibility {
		eligibility[index], err = EvaluateRecoveryEligibility(ctx, pool, eligibilityIDs[index], eligibilityEpochs[index].FailureEpochID, scopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
		if err != nil || eligibility[index].ResultState != "ELIGIBLE" || eligibility[index].FencingUsability != "USABLE" || eligibility[index].StorageUsability != "USABLE" || eligibility[index].EligibleDestinationCount != 1 || len(eligibility[index].Candidates) != 2 {
			t.Fatalf("Recovery Eligibility[%d]=%+v err=%v", index, eligibility[index], err)
		}
		if eligibility[index].AvailabilityBindingRevision != safetyBinding.BindingRevision || eligibility[index].AvailabilityPolicyID != safetyAvailability.PolicyID {
			t.Fatalf("Recovery Eligibility silently substituted current Binding/Policy: %+v", eligibility[index])
		}
		sourceExcluded, destinationEligible := false, false
		for _, candidate := range eligibility[index].Candidates {
			sourceExcluded = sourceExcluded || (candidate.HostID == host && candidate.CandidateState == "SOURCE_EXCLUDED")
			destinationEligible = destinationEligible || (candidate.HostID == destinationHost && candidate.CandidateState == "ELIGIBLE")
		}
		if !sourceExcluded || !destinationEligible {
			t.Fatalf("Recovery candidates do not preserve source exclusion/destination eligibility: %+v", eligibility[index].Candidates)
		}
	}
	if replay, err := EvaluateRecoveryEligibility(ctx, pool, eligibility[0].EvaluationID, eligibility[0].FailureEpochID, scopeID, eligibility[0].EvaluatorVersion, eligibility[0].EvaluatorDigest); err != nil || replay.EvaluationDigest != eligibility[0].EvaluationDigest || len(replay.Candidates) != 2 {
		t.Fatalf("Recovery Eligibility replay=%+v err=%v", replay, err)
	} else {
		for index := range replay.Candidates {
			if len(replay.Candidates[index].VisibilityProvenance) != len(eligibility[0].Candidates[index].VisibilityProvenance) || replay.Candidates[index].VisibilityProvenanceDigest != eligibility[0].Candidates[index].VisibilityProvenanceDigest {
				t.Fatalf("Recovery Eligibility replay lost visibility provenance: original=%+v replay=%+v", eligibility[0].Candidates[index], replay.Candidates[index])
			}
		}
	}
	var preDecisionCount, preBudgetClaimCount int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.recovery_eligibility_decision_evidence WHERE failure_epoch_id=ANY($1)),
		(SELECT count(*) FROM kim.recovery_budget_claim_evidence WHERE failure_epoch_id=ANY($1))`, []string{eligibilityEpochs[0].FailureEpochID, eligibilityEpochs[1].FailureEpochID}).Scan(&preDecisionCount, &preBudgetClaimCount); err != nil || preDecisionCount != 0 || preBudgetClaimCount != 0 {
		t.Fatalf("pure Eligibility Evaluation created permission decision=%d budget_claim=%d err=%v", preDecisionCount, preBudgetClaimCount, err)
	}

	decisionResults := make([]RecoveryEligibilityDecision, 2)
	decisionErrors := make([]error, 2)
	var decisionWG sync.WaitGroup
	decisionWG.Add(2)
	for index := range decisionResults {
		go func(index int) {
			defer decisionWG.Done()
			decisionResults[index], decisionErrors[index] = MaterializeRecoveryEligibilityDecision(ctx, pool, "recovery-eligibility-decision-"+fmt.Sprint(index)+"-"+suffix, eligibility[index].EvaluationID, "recovery-authority/v1")
		}(index)
	}
	decisionWG.Wait()
	winnerIndex, budgetRejected := -1, 0
	for index, decisionErr := range decisionErrors {
		if decisionErr == nil {
			winnerIndex = index
		} else if errors.Is(decisionErr, ErrRecoveryEligibilityBudgetExhausted) {
			budgetRejected++
		} else {
			t.Fatalf("unexpected Recovery Budget race result[%d]=%v", index, decisionErr)
		}
	}
	if winnerIndex < 0 || budgetRejected != 1 {
		t.Fatalf("Recovery Budget max=1 race decisions=%+v errors=%v", decisionResults, decisionErrors)
	}
	winnerDecision := decisionResults[winnerIndex]
	if winnerDecision.DecisionState != "ACCEPTED" || winnerDecision.BudgetClaimID == "" || winnerDecision.EvaluationDigest != eligibility[winnerIndex].EvaluationDigest {
		t.Fatalf("accepted Recovery permission=%+v", winnerDecision)
	}
	parallelReplay := make([]RecoveryEligibilityDecision, 2)
	parallelReplayErrors := make([]error, 2)
	decisionWG.Add(2)
	for index := range parallelReplay {
		go func(index int) {
			defer decisionWG.Done()
			parallelReplay[index], parallelReplayErrors[index] = MaterializeRecoveryEligibilityDecision(ctx, pool, winnerDecision.DecisionID, winnerDecision.EvaluationID, winnerDecision.DecidedBy)
		}(index)
	}
	decisionWG.Wait()
	if parallelReplayErrors[0] != nil || parallelReplayErrors[1] != nil || parallelReplay[0].DecisionDigest != winnerDecision.DecisionDigest || parallelReplay[1].BudgetClaimDigest != winnerDecision.BudgetClaimDigest {
		t.Fatalf("parallel Decision replay=%+v errors=%v", parallelReplay, parallelReplayErrors)
	}
	if _, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "recovery-eligibility-competing-"+suffix, winnerDecision.EvaluationID, winnerDecision.DecidedBy); !errors.Is(err, ErrRecoveryEligibilityStale) {
		t.Fatalf("distinct Decision identity for one Epoch=%v", err)
	}
	var decisions, budgetClaims, activeBudgetClaims, recoverySideEffects int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.recovery_eligibility_decision_evidence WHERE failure_epoch_id=ANY($1)),
		(SELECT count(*) FROM kim.recovery_budget_claim_evidence WHERE failure_epoch_id=ANY($1)),
		(SELECT count(*) FROM kim.recovery_budget_claims_current WHERE recovery_budget_policy_id=$2 AND claim_state='RESERVED'),
		(SELECT count(*) FROM kim.execution_jobs WHERE created_at>(SELECT recorded_at FROM kim.recovery_eligibility_decision_evidence WHERE failure_epoch_id=ANY($1) LIMIT 1)) +
		(SELECT count(*) FROM kim.compute_allocation_claims WHERE created_at>(SELECT recorded_at FROM kim.recovery_eligibility_decision_evidence WHERE failure_epoch_id=ANY($1) LIMIT 1))`, []string{eligibilityEpochs[0].FailureEpochID, eligibilityEpochs[1].FailureEpochID}, recoveryBudgetPolicy.PolicyID).Scan(&decisions, &budgetClaims, &activeBudgetClaims, &recoverySideEffects); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 || budgetClaims != 1 || activeBudgetClaims != 1 || recoverySideEffects != 0 {
		t.Fatalf("Recovery permission atomicity decisions=%d claims=%d active=%d runtime/resource side effects=%d", decisions, budgetClaims, activeBudgetClaims, recoverySideEffects)
	}

	// Recovery Request and immutable Plan remain separate from start authority.
	// Planning fixes one exact destination but creates no Admission, Job, Command,
	// or Budget consumption. The selected Host is never silently substituted.
	recoveryOperationID := "recovery-operation-" + suffix
	recoveryPlanID := "recovery-plan-" + suffix
	recoveryRequest, err := RecordRecoveryOperationRequest(ctx, pool, recoveryOperationID, winnerDecision.DecisionID, winnerDecision.BudgetClaimID, "RESTART_ON_OTHER_HOST", "recovery-operator")
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := RecordRecoveryOperationRequest(ctx, pool, recoveryOperationID, winnerDecision.DecisionID, winnerDecision.BudgetClaimID, "RESTART_ON_OTHER_HOST", "recovery-operator"); err != nil || replay.RequestDigest != recoveryRequest.RequestDigest {
		t.Fatalf("Recovery Operation Request replay=%+v err=%v", replay, err)
	}
	recoveryOperation, recoveryPlan, err := PlanRecoveryOperation(ctx, pool, recoveryOperationID, recoveryPlanID, destinationHost)
	if err != nil || recoveryOperation.LifecycleState != "PLANNED" || recoveryPlan.DestinationHostID != destinationHost || recoveryPlan.RecoveryAction != "RESTART_ON_OTHER_HOST" {
		t.Fatalf("Recovery Plan operation=%+v plan=%+v err=%v", recoveryOperation, recoveryPlan, err)
	}
	if replayOperation, replayPlan, err := PlanRecoveryOperation(ctx, pool, recoveryOperationID, recoveryPlanID, destinationHost); err != nil || replayOperation.OperationDigest != recoveryOperation.OperationDigest || replayPlan.PlanDigest != recoveryPlan.PlanDigest {
		t.Fatalf("Recovery Plan replay operation=%+v plan=%+v err=%v", replayOperation, replayPlan, err)
	}
	var beforeStartAdmissions, beforeStartJobs, beforeStartCommands int
	var beforeStartBudgetState string
	var beforeStartBudgetGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.recovery_destination_admission_evidence),(SELECT count(*) FROM kim.execution_jobs WHERE resource_type='RECOVERY_OPERATION'),(SELECT count(*) FROM kim.execution_commands WHERE target_resource_id=$1),(SELECT claim_state FROM kim.recovery_budget_claims_current WHERE claim_id=$2),(SELECT state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$2)`, "recovery-operation:"+recoveryOperationID, winnerDecision.BudgetClaimID).Scan(&beforeStartAdmissions, &beforeStartJobs, &beforeStartCommands, &beforeStartBudgetState, &beforeStartBudgetGeneration); err != nil || beforeStartAdmissions != 0 || beforeStartJobs != 0 || beforeStartCommands != 0 || beforeStartBudgetState != "RESERVED" || beforeStartBudgetGeneration != 1 {
		t.Fatalf("Request/Plan created start authority admissions=%d jobs=%d commands=%d budget=%s/%d err=%v", beforeStartAdmissions, beforeStartJobs, beforeStartCommands, beforeStartBudgetState, beforeStartBudgetGeneration, err)
	}
	rollbackStartFI := errors.New("rollback Recovery start FI")
	assertBlockedStart := func(label, mutation string, args ...any) {
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, mutation, args...); err != nil {
				return err
			}
			_, startErr := StartRecoveryOperation(ctx, scopeTxBeginner{tx}, recoveryOperationID, "blocked-"+label+"-job-"+suffix, "blocked-"+label+"-command-"+suffix)
			if !errors.Is(startErr, ErrRecoveryOperationStale) {
				return fmt.Errorf("%s start error=%v", label, startErr)
			}
			var admissions, jobs int
			var state string
			var generation uint64
			if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.recovery_destination_admission_evidence WHERE recovery_operation_id=$1),(SELECT count(*) FROM kim.execution_jobs WHERE resource_type='RECOVERY_OPERATION'),claim_state,state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$2`, recoveryOperationID, winnerDecision.BudgetClaimID).Scan(&admissions, &jobs, &state, &generation); err != nil || admissions != 0 || jobs != 0 {
				return fmt.Errorf("%s leaked authority admissions=%d jobs=%d budget=%s/%d err=%v", label, admissions, jobs, state, generation, err)
			}
			return rollbackStartFI
		})
		if !errors.Is(err, rollbackStartFI) {
			t.Fatalf("%s deterministic start FI=%v", label, err)
		}
	}
	assertBlockedStart("fencing-stale", `UPDATE kim.host_operation_authorities_current SET authority_state='ARMED' WHERE host_id=$1`, host)
	assertBlockedStart("storage-stale", `UPDATE kim.volume_attachment_claims SET claim_state='ACTIVE' WHERE attachment_id=$1`, attachmentID)
	assertBlockedStart("budget-stale", `UPDATE kim.recovery_budget_claims_current SET claim_state='FENCED',state_generation=2 WHERE claim_id=$1`, winnerDecision.BudgetClaimID)
	assertBlockedStart("destination-stale", `UPDATE kim.host_readiness_gates_current SET compliance_state='NON_COMPLIANT' WHERE host_id=$1`, destinationHost)

	// Start atomically revalidates current safety, releases only the fenced
	// source compute accounting authority, performs the ordinary destination
	// Final Admission, consumes the Budget, and dispatches one closed typed
	// destination-preparation Command. It does not claim recovery success.
	recoveryJobID, recoveryCommandID := "recovery-job-"+suffix, "recovery-command-"+suffix
	ordinaryRequest := recoveryPlan.DestinationRequest
	ordinaryRequest.RequestID = "ordinary-placement-race-" + suffix
	ordinaryDry, err := DryEvaluatePlacementScope(ctx, pool, ordinaryRequest)
	if err != nil || ordinaryDry.Status != "READY" {
		t.Fatalf("ordinary Placement race dry=%+v err=%v", ordinaryDry, err)
	}
	var ordinaryCandidate PlacementScopeCandidate
	for _, candidate := range ordinaryDry.Candidates {
		if candidate.HostID == destinationHost && candidate.Eligible {
			ordinaryCandidate = candidate
		}
	}
	if ordinaryCandidate.HostID == "" {
		t.Fatalf("ordinary Placement race has no destination candidate: %+v", ordinaryDry.Candidates)
	}
	// Recovery Start wins the exact workload/resource authority first; a
	// subsequent ordinary Admission for the same workload must fail atomically.
	// The lower-level Final Admission suite separately qualifies concurrent
	// resource races and lock ordering.
	recoveryStart, err := StartRecoveryOperation(ctx, pool, recoveryOperationID, recoveryJobID, recoveryCommandID)
	_, ordinaryPlacementErr := FinalAdmitPlacementScope(ctx, pool, ordinaryDry, ordinaryRequest, ordinaryCandidate)
	if err != nil || recoveryStart.LifecycleState != "RUNNING" || recoveryStart.DestinationHostID != destinationHost || recoveryStart.BudgetStateGeneration != 2 || recoveryStart.OperationStateGeneration != 2 {
		t.Fatalf("Recovery Operation start=%+v err=%v", recoveryStart, err)
	}
	if ordinaryPlacementErr == nil {
		t.Fatal("ordinary Placement raced Recovery and committed a duplicate active workload claim")
	}
	var activeWorkloadClaims int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.compute_allocation_claims WHERE project_id=$1 AND workload_id=$2 AND claim_state IN ('RESERVED','ALLOCATED')`, ordinaryRequest.ProjectID, ordinaryRequest.WorkloadID).Scan(&activeWorkloadClaims); err != nil || activeWorkloadClaims != 1 {
		t.Fatalf("Recovery/ordinary Placement race active claims=%d ordinaryErr=%v queryErr=%v", activeWorkloadClaims, ordinaryPlacementErr, err)
	}
	if replay, err := StartRecoveryOperation(ctx, pool, recoveryOperationID, recoveryJobID, recoveryCommandID); err != nil || replay.DestinationAdmissionID != recoveryStart.DestinationAdmissionID || replay.ExecutionCommandID != recoveryCommandID || replay.BudgetStateGeneration != 2 {
		t.Fatalf("Recovery Operation start replay=%+v err=%v", replay, err)
	}
	var sourceComputeState, destinationComputeState, budgetConsumed, operationState, commandType, commandState string
	var operationCount, planCount, destinationAdmissionCount, recoveryJobCount int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT claim_state FROM kim.compute_allocation_claims WHERE admission_id=$1),
		(SELECT claim_state FROM kim.compute_allocation_claims WHERE admission_id=$2),
		(SELECT claim_state FROM kim.recovery_budget_claims_current WHERE claim_id=$3),
		(SELECT lifecycle_state FROM kim.recovery_operations_current WHERE recovery_operation_id=$4),
		(SELECT command_type FROM kim.execution_commands WHERE command_id=$5),
		(SELECT command_state FROM kim.execution_commands_current WHERE command_id=$5),
		(SELECT count(*) FROM kim.recovery_operation_evidence WHERE recovery_operation_id=$4),
		(SELECT count(*) FROM kim.recovery_plan_evidence WHERE recovery_operation_id=$4),
		(SELECT count(*) FROM kim.recovery_destination_admission_evidence WHERE recovery_operation_id=$4),
		(SELECT count(*) FROM kim.execution_jobs WHERE job_id=$6)`, admission.AdmissionID, recoveryStart.DestinationAdmissionID, winnerDecision.BudgetClaimID, recoveryOperationID, recoveryCommandID, recoveryJobID).Scan(&sourceComputeState, &destinationComputeState, &budgetConsumed, &operationState, &commandType, &commandState, &operationCount, &planCount, &destinationAdmissionCount, &recoveryJobCount); err != nil || sourceComputeState != "RELEASED" || destinationComputeState != "RESERVED" || budgetConsumed != "CONSUMED" || operationState != "RUNNING" || commandType != "HOST_AGENT_STATE_MARKER_ENSURE" || commandState != "PENDING" || operationCount != 1 || planCount != 1 || destinationAdmissionCount != 1 || recoveryJobCount != 1 {
		t.Fatalf("Recovery start atomic state source=%s destination=%s budget=%s operation=%s command=%s/%s counts=%d/%d/%d/%d err=%v", sourceComputeState, destinationComputeState, budgetConsumed, operationState, commandType, commandState, operationCount, planCount, destinationAdmissionCount, recoveryJobCount, err)
	}
	if _, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "recovery-eligibility-after-active-operation-"+suffix, eligibility[1-winnerIndex].EvaluationID, "recovery-authority/v1"); !errors.Is(err, ErrRecoveryEligibilityBudgetExhausted) {
		t.Fatalf("CONSUMED active Recovery disappeared from Budget count: %v", err)
	}
	dangerous, err := EvaluateRecoveryDangerousStep(ctx, pool, "recovery-dangerous-step-current-"+suffix, recoveryOperationID, digestBytes([]byte("recovery-dangerous-step/v1")))
	if err != nil || dangerous.ResultState != "AUTHORIZED" || dangerous.BudgetStateGeneration != 2 {
		t.Fatalf("Recovery dangerous-step current safety=%+v err=%v", dangerous, err)
	}

	// Transport/Result ambiguity is UNKNOWN, not FAILED or proof of no side
	// effect. A read-back MATCH moves only to VERIFYING; marker success never
	// claims that the VM recovered.
	if _, err := pool.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state='UNKNOWN',current_attempt_index=1 WHERE command_id=$1`, recoveryCommandID); err != nil {
		t.Fatal(err)
	}
	unknownOperation, err := RefreshRecoveryOperationExecution(ctx, pool, recoveryOperationID, "")
	if err != nil || unknownOperation.LifecycleState != "UNKNOWN" {
		t.Fatalf("Recovery UNKNOWN projection=%+v err=%v", unknownOperation, err)
	}
	recoveryVerificationID := "recovery-verification-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '2 minutes',statement_timestamp()-interval '1 minute'); INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1); INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($4,$1,1,1,$5,'MATCHED',$6,jsonb_build_object('target_resource_id',$7,'state','MATCHED')); UPDATE kim.execution_commands_current SET command_state='SUCCEEDED',current_attempt_index=1 WHERE command_id=$1; UPDATE kim.execution_jobs SET job_state='SUCCEEDED' WHERE job_id=$8`, pgx.QueryExecModeSimpleProtocol, recoveryCommandID, destinationHost, digestBytes([]byte("recovery-token")), recoveryVerificationID, digestBytes([]byte("recovery-observation")), digestBytes([]byte("recovery-verifier")), "recovery-operation:"+recoveryOperationID, recoveryJobID); err != nil {
		t.Fatal(err)
	}
	verifyingOperation, err := RefreshRecoveryOperationExecution(ctx, pool, recoveryOperationID, recoveryVerificationID)
	if err != nil || verifyingOperation.LifecycleState != "VERIFYING" {
		t.Fatalf("Recovery read-back projection=%+v err=%v", verifyingOperation, err)
	}
	if err := pool.QueryRow(ctx, `SELECT lifecycle_state FROM kim.recovery_operations_current WHERE recovery_operation_id=$1`, recoveryOperationID).Scan(&operationState); err != nil || operationState == "VERIFIED" {
		t.Fatalf("preparation Command incorrectly claimed Recovery VERIFIED state=%s err=%v", operationState, err)
	}

	// Migration 055: use the exact destination Admission and the ordinary
	// Local LVM, VM define, image, attachment, power, and read-back authorities;
	// only the Recovery orchestration/verification/terminal evidence is new.
	recoveryTerminalRollback := errors.New("rollback Recovery terminal qualification fixture")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		qualifyRecoveryMaterializationTerminal(t, ctx, scopeTxBeginner{tx}, suffix,
			recoveryOperationID, vmID, imageID, checksum, destinationBackendID,
			destinationVGUUID, eligibility[1-winnerIndex].EvaluationID, recoveryPlan,
			recoveryStart, winnerDecision.BudgetClaimID)
		return recoveryTerminalRollback
	})
	if !errors.Is(err, recoveryTerminalRollback) {
		t.Fatalf("Recovery terminal qualification rollback=%v", err)
	}

	var postProofHostState string
	var postProofHostGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT authority_state,authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1`, host).Scan(&postProofHostState, &postProofHostGeneration); err != nil || postProofHostState != preProofHostState || postProofHostGeneration != preProofHostGeneration {
		t.Fatalf("Fencing proof mutated Host authority before=%s/%d after=%s/%d err=%v", preProofHostState, preProofHostGeneration, postProofHostState, postProofHostGeneration, err)
	}

	// Storage RELEASED -> ACTIVE -> RELEASED is an ABA transition. The
	// immutable old Proof remains, but claim_state_generation makes it stale.
	if _, err := pool.Exec(ctx, `UPDATE kim.volume_attachment_claims SET claim_state='ACTIVE' WHERE attachment_id=$1; UPDATE kim.volume_attachment_claims SET claim_state='RELEASED' WHERE attachment_id=$1`, pgx.QueryExecModeSimpleProtocol, attachmentID); err != nil {
		t.Fatal(err)
	}
	storageABAEvaluation, err := EvaluateRecoveryEligibility(ctx, pool, "recovery-eligibility-storage-aba-"+suffix, eligibilityEpochs[1-winnerIndex].FailureEpochID, scopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
	if err != nil || storageABAEvaluation.ResultState != "STORAGE_PROOF_STALE" || storageABAEvaluation.StorageUsability != "STALE" || storageABAEvaluation.FencingUsability != "USABLE" {
		t.Fatalf("Storage ABA Eligibility=%+v err=%v", storageABAEvaluation, err)
	}
	storageABADangerous, err := EvaluateRecoveryDangerousStep(ctx, pool, "recovery-dangerous-step-storage-aba-"+suffix, recoveryOperationID, digestBytes([]byte("recovery-dangerous-step/v1")))
	if err != nil || storageABADangerous.ResultState != "BLOCKED_STORAGE" || storageABADangerous.StorageUsability != "STALE" {
		t.Fatalf("Storage ABA dangerous-step gate=%+v err=%v", storageABADangerous, err)
	}

	// The qualification fixtures above intentionally created VM, execution, and
	// storage rows. Reset the no-mutation baseline so subsequent confirmation
	// races still prove that confirmation itself creates no runtime authority.
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.compute_allocation_claims),
		(SELECT count(*) FROM kim.pci_vf_allocation_claims),
		(SELECT count(*) FROM kim.network_identity_claims),
		(SELECT count(*) FROM kim.volume_attachment_claims),
		(SELECT count(*) FROM kim.vm_power_observation_evidence),
		(SELECT count(*) FROM kim.execution_jobs)`).Scan(&failureCountsAfter[0], &failureCountsAfter[1], &failureCountsAfter[2], &failureCountsAfter[3], &failureCountsAfter[4], &failureCountsAfter[5]); err != nil {
		t.Fatal(err)
	}

	// Exact rev1 safety Evaluations never uplift to a newly-current rev2.
	fencingDriftPolicy, err := PublishFailureFencingPolicy(ctx, pool, FailureFencingPolicy{PolicyID: "fencing-drift-policy-" + suffix, PolicyRevision: 1, FencingMode: "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF", LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	storageDriftPolicy, err := PublishStorageSafetyPolicy(ctx, pool, StorageSafetyPolicy{PolicyID: "storage-drift-policy-" + suffix, PolicyRevision: 1, StorageClass: "LOCAL_LVM", SafetyMode: "SOURCE_DETACHED_NO_HOLDER", LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var driftSourceRevision uint64
	var driftSourceDigest string
	if err := pool.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&driftSourceRevision, &driftSourceDigest); err != nil {
		t.Fatal(err)
	}
	driftAvailability := availabilityPolicyFixture("safety-policy-drift-availability-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "EVACUATE", "ACTIVE")
	driftAvailability.FailureConfirmationPolicyID, driftAvailability.FailureConfirmationPolicyRevision, driftAvailability.FailureConfirmationPolicyDigest = confirmationPolicy.PolicyID, 1, confirmationPolicy.PolicyDigest
	driftAvailability.FencingPolicyID, driftAvailability.FencingPolicyRevision, driftAvailability.FencingPolicyDigest = fencingDriftPolicy.PolicyID, 1, fencingDriftPolicy.PolicyDigest
	driftAvailability.StorageSafetyPolicyID, driftAvailability.StorageSafetyPolicyRevision, driftAvailability.StorageSafetyPolicyDigest = storageDriftPolicy.PolicyID, 1, storageDriftPolicy.PolicyDigest
	driftAvailabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, driftAvailability)
	if err != nil {
		t.Fatal(err)
	}
	driftRebind := VMAvailabilityRebindRequest{RebindID: "safety-policy-drift-rebind-" + suffix, WorkloadID: r1.WorkloadID, ExpectedCurrentBindingRevision: driftSourceRevision, SourceBindingDigest: driftSourceDigest, TargetPolicyID: driftAvailability.PolicyID, TargetPolicyRevision: 1, TargetPolicyDigest: driftAvailabilityDigest, RequestedBy: "operator", AuthorizedBy: "availability-authority", AuthorizationReference: "approval/safety-drift", Reason: "qualify exact safety policy drift"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, driftRebind); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecideVMAvailabilityRebind(ctx, pool, driftRebind.RebindID, "availability-authority"); err != nil {
		t.Fatal(err)
	}
	safetyPolicyDriftEpoch, _ := openConfirmationEpoch("failure-safety-policy-drift", "ABSENT", "CURRENT")
	appendAuthorityEvidence(safetyPolicyDriftEpoch, "failure-safety-policy-drift", "PRESENT", "CURRENT")
	driftConfirmation := evaluate(safetyPolicyDriftEpoch, "failure-safety-policy-drift")
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "failure-safety-policy-drift-confirmation-"+suffix, driftConfirmation.EvaluationID, "failure-authority/v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordSourceExecutionFencingObservation(ctx, pool, "failure-safety-policy-drift-fence-"+suffix, safetyPolicyDriftEpoch.FailureEpochID); err != nil {
		t.Fatal(err)
	}
	driftFencingEvaluation, err := EvaluateFailureFencing(ctx, pool, "failure-safety-policy-drift-fencing-evaluation-"+suffix, safetyPolicyDriftEpoch.FailureEpochID, "fencing-evaluator/v1", digestBytes([]byte("fencing-evaluator/v1")))
	if err != nil || driftFencingEvaluation.ResultState != "PROVEN" {
		t.Fatalf("drift Fencing Evaluation=%+v err=%v", driftFencingEvaluation, err)
	}
	driftStorageEvaluation, err := EvaluateStorageSafety(ctx, pool, "failure-safety-policy-drift-storage-evaluation-"+suffix, safetyPolicyDriftEpoch.FailureEpochID, "storage-safety-evaluator/v1", digestBytes([]byte("storage-safety-evaluator/v1")))
	if err != nil || driftStorageEvaluation.ResultState != "SAFE" {
		t.Fatalf("drift Storage Evaluation=%+v err=%v", driftStorageEvaluation, err)
	}
	fencingDriftPolicy.PolicyRevision, fencingDriftPolicy.PolicyDigest = 2, ""
	if _, err := PublishFailureFencingPolicy(ctx, pool, fencingDriftPolicy); err != nil {
		t.Fatal(err)
	}
	storageDriftPolicy.PolicyRevision, storageDriftPolicy.PolicyDigest = 2, ""
	if _, err := PublishStorageSafetyPolicy(ctx, pool, storageDriftPolicy); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MaterializeFailureFencingProof(ctx, pool, "failure-safety-policy-drift-fencing-proof-"+suffix, driftFencingEvaluation.EvaluationID, "fencing-authority/v1"); !errors.Is(err, ErrFailureSafetyStale) {
		t.Fatalf("Fencing Policy drift proof=%v", err)
	}
	if _, err := MaterializeStorageSafetyProof(ctx, pool, "failure-safety-policy-drift-storage-proof-"+suffix, driftStorageEvaluation.EvaluationID, "storage-safety-authority/v1"); !errors.Is(err, ErrFailureSafetyStale) {
		t.Fatalf("Storage Policy drift proof=%v", err)
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
	if err := pool.QueryRow(ctx, `SELECT authority_state,authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1`, host).Scan(&hostAuthorityState, &hostAuthorityGeneration); err != nil || hostAuthorityState != "FENCED" || hostAuthorityGeneration != 1 {
		t.Fatalf("Safety proof/Confirmation changed explicit Host fence state=%s generation=%d err=%v", hostAuthorityState, hostAuthorityGeneration, err)
	}

	// Migration 057: a configured inactive root vda is observation-only safety
	// evidence, never generic attach/detach authority. The exact source plan
	// derives its identity; wrong vdb is conflicting, UNKNOWN is never safe.
	compositeStoragePolicy, err := PublishStorageSafetyPolicy(ctx, pool, StorageSafetyPolicy{PolicyID: "root-composite-storage-policy-" + suffix, PolicyRevision: 1, StorageClass: "LOCAL_LVM", SafetyMode: "SOURCE_ROOT_QUIESCED_DATA_DETACHED", LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	rootConfirmationPolicy := confirmationPolicy
	rootConfirmationPolicy.PolicyID, rootConfirmationPolicy.PolicyRevision = "root-confirmation-policy-"+suffix, 1
	rootConfirmationPolicy.RequirementsDigest, rootConfirmationPolicy.PolicyDigest = "", ""
	rootConfirmationPolicy, err = PublishFailureConfirmationPolicy(ctx, pool, rootConfirmationPolicy)
	if err != nil {
		t.Fatal(err)
	}
	rootFencingPolicy, err := PublishFailureFencingPolicy(ctx, pool, FailureFencingPolicy{PolicyID: "root-fencing-policy-" + suffix, PolicyRevision: 1, FencingMode: "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF", LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	rootBudgetPolicy, err := PublishRecoveryBudgetPolicy(ctx, pool, RecoveryBudgetPolicy{PolicyID: "root-budget-policy-" + suffix, PolicyRevision: 1, ScopeType: "GLOBAL", Phase: "PLANNING", MaxActiveRecoveries: 2, LifecycleState: "ACTIVE", CreatedBy: "fixture", ApprovedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var rootSourceRevision uint64
	var rootSourceDigest string
	if err := pool.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1`, r1.WorkloadID).Scan(&rootSourceRevision, &rootSourceDigest); err != nil {
		t.Fatal(err)
	}
	rootAvailability := availabilityPolicyFixture("root-safety-availability-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE")
	rootAvailability.FailureConfirmationPolicyID, rootAvailability.FailureConfirmationPolicyRevision, rootAvailability.FailureConfirmationPolicyDigest = rootConfirmationPolicy.PolicyID, 1, rootConfirmationPolicy.PolicyDigest
	rootAvailability.FencingPolicyID, rootAvailability.FencingPolicyRevision, rootAvailability.FencingPolicyDigest = rootFencingPolicy.PolicyID, 1, rootFencingPolicy.PolicyDigest
	rootAvailability.StorageSafetyPolicyID, rootAvailability.StorageSafetyPolicyRevision, rootAvailability.StorageSafetyPolicyDigest = compositeStoragePolicy.PolicyID, 1, compositeStoragePolicy.PolicyDigest
	rootAvailability.RecoveryBudgetPolicyID, rootAvailability.RecoveryBudgetPolicyRevision, rootAvailability.RecoveryBudgetPolicyDigest = rootBudgetPolicy.PolicyID, 1, rootBudgetPolicy.PolicyDigest
	rootAvailabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, rootAvailability)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "root-safety-group-binding-" + suffix, BindingID: binding.BindingID, ExpectedCurrentGeneration: 3, HostGroupID: groupID, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: rootAvailability.PolicyID, PolicyRevision: 1, PolicyDigest: rootAvailabilityDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	rootRebind := VMAvailabilityRebindRequest{RebindID: "root-safety-rebind-" + suffix, WorkloadID: r1.WorkloadID, ExpectedCurrentBindingRevision: rootSourceRevision, SourceBindingDigest: rootSourceDigest, TargetPolicyID: rootAvailability.PolicyID, TargetPolicyRevision: 1, TargetPolicyDigest: rootAvailabilityDigest, RequestedBy: "operator", AuthorizedBy: "availability-authority", AuthorizationReference: "approval/root-safety", Reason: "qualify exact source root retirement"}
	if _, err := RecordVMAvailabilityRebindRequest(ctx, pool, rootRebind); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecideVMAvailabilityRebind(ctx, pool, rootRebind.RebindID, "availability-authority"); err != nil {
		t.Fatal(err)
	}
	rootEpoch, _ := openConfirmationEpoch("source-root-safety", "ABSENT", "CURRENT")
	appendAuthorityEvidence(rootEpoch, "source-root-safety", "PRESENT", "CURRENT")
	rootConfirmation := evaluate(rootEpoch, "source-root-safety")
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "source-root-confirmation-"+suffix, rootConfirmation.EvaluationID, "failure-authority/v1"); err != nil {
		t.Fatal(err)
	}
	wrongRoot, err := EvaluateSourceRootSafety(ctx, pool, "source-root-wrong-vdb-"+suffix, rootEpoch.FailureEpochID, "source-root-evaluator/v1", digestBytes([]byte("source-root-evaluator/v1")))
	if err != nil || wrongRoot.ResultState != "CONFLICTING_INPUT" || wrongRoot.TargetDevice != "vdb" {
		t.Fatalf("wrong-volume root evaluation=%+v err=%v", wrongRoot, err)
	}
	if _, err := MaterializeSourceRootSafetyProof(ctx, pool, "source-root-wrong-proof-"+suffix, wrongRoot.EvaluationID, "source-root-authority/v1"); !errors.Is(err, ErrFailureSafetyBlocked) {
		t.Fatalf("wrong-volume root proof=%v", err)
	}
	rootObservationDigest, rootVerifierDigest := digestBytes([]byte("source-root-safe/observation")), digestBytes([]byte("source-root-safe/verifier"))
	if err := AcceptSourceRootSafetyObservation(ctx, pool, LocalLVMAttachmentObservation{EvidenceID: "source-root-safe-evidence-" + suffix, AttachmentID: attachmentID, VolumeID: volumeID, BindingID: bindingID, HostID: host, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvUUID, DesiredState: "ATTACHED", CommandID: rootCommand, VerificationID: rootVerification, ObservationDigest: rootObservationDigest, VerifierDigest: rootVerifierDigest, EvidenceState: "MATCHED", AttachmentGeneration: 1, BindingGeneration: 1, ObservationGeneration: 3, AttemptIndex: 1, DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true, HolderOpen: false}); err != nil {
		t.Fatal(err)
	}
	rootEvaluation, err := EvaluateSourceRootSafety(ctx, pool, "source-root-safe-evaluation-"+suffix, rootEpoch.FailureEpochID, "source-root-evaluator/v1", digestBytes([]byte("source-root-evaluator/v1")))
	if err != nil || rootEvaluation.ResultState != "SAFE" || rootEvaluation.TargetDevice != "vda" || rootEvaluation.HolderOpen {
		t.Fatalf("safe root evaluation=%+v err=%v", rootEvaluation, err)
	}
	rootProof, err := MaterializeSourceRootSafetyProof(ctx, pool, "source-root-safe-proof-"+suffix, rootEvaluation.EvaluationID, "source-root-authority/v1")
	if err != nil || rootProof.ProofState != "SAFE" {
		t.Fatalf("root proof=%+v err=%v", rootProof, err)
	}
	compositeStorageEvaluation, err := EvaluateStorageSafety(ctx, pool, "root-composite-storage-evaluation-"+suffix, rootEpoch.FailureEpochID, "storage-safety-evaluator/v1", digestBytes([]byte("storage-safety-evaluator/v1")))
	if err != nil || compositeStorageEvaluation.ResultState != "SAFE" || compositeStorageEvaluation.RootSafetyProofID != rootProof.ProofID {
		t.Fatalf("composite Storage evaluation=%+v err=%v", compositeStorageEvaluation, err)
	}
	compositeStorageProof, err := MaterializeStorageSafetyProof(ctx, pool, "root-composite-storage-proof-"+suffix, compositeStorageEvaluation.EvaluationID, "storage-safety-authority/v1")
	if err != nil || compositeStorageProof.ProofType != "LOCAL_LVM_SOURCE_ROOT_QUIESCED_DATA_DETACHED" {
		t.Fatalf("composite Storage proof=%+v err=%v", compositeStorageProof, err)
	}
	if _, err := RecordSourceExecutionFencingObservation(ctx, pool, "source-root-fencing-observation-"+suffix, rootEpoch.FailureEpochID); err != nil {
		t.Fatal(err)
	}
	rootFencingEvaluation, err := EvaluateFailureFencing(ctx, pool, "source-root-fencing-evaluation-"+suffix, rootEpoch.FailureEpochID, "fencing-evaluator/v1", digestBytes([]byte("fencing-evaluator/v1")))
	if err != nil || rootFencingEvaluation.ResultState != "PROVEN" {
		t.Fatalf("root fencing evaluation=%+v err=%v", rootFencingEvaluation, err)
	}
	rootFencingProof, _, err := MaterializeFailureFencingProof(ctx, pool, "source-root-fencing-proof-"+suffix, rootFencingEvaluation.EvaluationID, "fencing-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	retirement, err := RetireSourceMaterialization(ctx, pool, "source-root-retirement-"+suffix, rootEpoch.FailureEpochID, rootProof.ProofID, rootFencingProof.ProofID, "source-retirement-authority/v1")
	if err != nil || retirement.DecisionState != "RETIRED" {
		t.Fatalf("source retirement=%+v err=%v", retirement, err)
	}
	rootEligibility, err := EvaluateRecoveryEligibility(ctx, pool, "source-root-recovery-eligibility-"+suffix, rootEpoch.FailureEpochID, scopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
	if err != nil || rootEligibility.ResultState != "ELIGIBLE" || rootEligibility.StorageSafetyProofID != compositeStorageProof.ProofID {
		t.Fatalf("root-safe Recovery Eligibility=%+v err=%v", rootEligibility, err)
	}
	// ABA: holder/observation generation may return to the same boolean state,
	// but the old root/composite proof must remain stale by exact generation.
	rootABARollback := errors.New("rollback root holder ABA")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_observations_current SET observation_generation=observation_generation+2 WHERE attachment_id=$1`, attachmentID); err != nil {
			return err
		}
		_, _, usability, err := loadSourceRootSafetyProofUsabilityTx(ctx, tx, rootEpoch)
		if err != nil || usability != "STALE" {
			t.Fatalf("root observation ABA usability=%s err=%v", usability, err)
		}
		return rootABARollback
	})
	if !errors.Is(err, rootABARollback) {
		t.Fatalf("root ABA rollback=%v", err)
	}
	for _, drift := range []struct {
		label, mutation string
		args            []any
	}{
		{label: "power-generation", mutation: `UPDATE kim.vm_power_state_current SET observation_generation=observation_generation+2 WHERE vm_id=$1::uuid`, args: []any{vmID}},
		{label: "binding-generation", mutation: `UPDATE kim.volume_backend_bindings_current SET observation_generation=observation_generation+2 WHERE binding_id=$1`, args: []any{bindingID}},
		{label: "materialization-identity", mutation: `UPDATE kim.virtual_machines_current SET current_plan_id=NULL WHERE vm_id=$1::uuid`, args: []any{vmID}},
	} {
		rollback := errors.New("rollback root " + drift.label + " ABA")
		err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, drift.mutation, drift.args...); err != nil {
				return err
			}
			_, _, usability, err := loadSourceRootSafetyProofUsabilityTx(ctx, tx, rootEpoch)
			if err != nil || usability != "STALE" {
				t.Fatalf("root %s ABA usability=%s err=%v", drift.label, usability, err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("root %s rollback=%v", drift.label, err)
		}
	}

	rootDecision, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "source-root-recovery-decision-"+suffix, rootEligibility.EvaluationID, "recovery-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	rootOperationID, rootPlanID := "source-root-recovery-operation-"+suffix, "source-root-recovery-plan-"+suffix
	if _, err := RecordRecoveryOperationRequest(ctx, pool, rootOperationID, rootDecision.DecisionID, rootDecision.BudgetClaimID, "RESTART_ON_OTHER_HOST", "recovery-operator"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PlanRecoveryOperation(ctx, pool, rootOperationID, rootPlanID, destinationHost); err != nil {
		t.Fatal(err)
	}
	startDriftRollback := errors.New("rollback root stale start")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_observations_current SET observation_generation=observation_generation+2 WHERE attachment_id=$1`, attachmentID); err != nil {
			return err
		}
		if _, err := StartRecoveryOperation(ctx, scopeTxBeginner{tx}, rootOperationID, "source-root-stale-start-job-"+suffix, "source-root-stale-start-command-"+suffix); !errors.Is(err, ErrRecoveryOperationStale) {
			t.Fatalf("root drift StartRecoveryOperation=%v", err)
		}
		return startDriftRollback
	})
	if !errors.Is(err, startDriftRollback) {
		t.Fatalf("root stale start rollback=%v", err)
	}
	// The ordinary Recovery Operation has already crossed Start authority by
	// the time this block runs. Its formerly accepted secondary-disk Storage
	// proof is now stale because the exact current root observation is vda.
	gate, err := EvaluateRecoveryDangerousStep(ctx, pool, "source-root-dangerous-drift-"+suffix, recoveryOperationID, digestBytes([]byte("source-root-dangerous-step/v1")))
	if err != nil || gate.ResultState != "BLOCKED_STORAGE" || gate.StorageUsability != "STALE" {
		t.Fatalf("root dangerous-step gate=%+v err=%v", gate, err)
	}

	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "root-safety-group-binding-restore-" + suffix, BindingID: binding.BindingID, ExpectedCurrentGeneration: 4, HostGroupID: groupID, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: safetyAvailability.PolicyID, PolicyRevision: 1, PolicyDigest: safetyAvailabilityDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}

	// FENCED generation N -> ARMED N+1 -> FENCED N+1 never revives a proof
	// tied to the old exact authority generation/event identity.
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: host, PolicyID: "availability-host-policy",
		PolicyGeneration: 1, ActorID: "fixture", ReasonCode: "fencing_aba_rearm"}); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fenceHostOperationAuthorityTx(ctx, tx, host, "fencing_aba_refence")
	}); err != nil {
		t.Fatal(err)
	}
	fencingABAEvaluation, err := EvaluateRecoveryEligibility(ctx, pool, "recovery-eligibility-fencing-aba-"+suffix, eligibilityEpochs[1-winnerIndex].FailureEpochID, scopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
	if err != nil || fencingABAEvaluation.ResultState != "FENCING_PROOF_STALE" || fencingABAEvaluation.FencingUsability != "STALE" {
		t.Fatalf("Fencing ABA Eligibility=%+v err=%v", fencingABAEvaluation, err)
	}
	fencingABADangerous, err := EvaluateRecoveryDangerousStep(ctx, pool, "recovery-dangerous-step-fencing-aba-"+suffix, recoveryOperationID, digestBytes([]byte("recovery-dangerous-step/v1")))
	if err != nil || fencingABADangerous.ResultState != "BLOCKED_FENCING" || fencingABADangerous.FencingUsability != "STALE" {
		t.Fatalf("Fencing ABA dangerous-step gate=%+v err=%v", fencingABADangerous, err)
	}
}
