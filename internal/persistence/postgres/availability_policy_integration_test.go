package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
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

	// Retirement blocks new resolution but preserves both historical bindings.
	p3 := availabilityPolicyFixture(policyID, 3, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "RETIRED")
	if _, err := PublishAvailabilityPolicy(ctx, pool, p3); err != nil {
		t.Fatal(err)
	}
	retiredDry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, request("availability-retired-"+suffix))
	if err != nil || retiredDry.Candidates[0].AvailabilityStatus != "STALE_ASSIGNMENT" {
		t.Fatalf("retired dry=%+v err=%v", retiredDry, err)
	}
	if err := pool.QueryRow(ctx, `SELECT availability_policy_revision FROM kim.vm_availability_binding_evidence WHERE workload_id=$1`, r1.WorkloadID).Scan(&oldRevision); err != nil || oldRevision != 1 {
		t.Fatalf("retirement rewrote history revision=%d err=%v", oldRevision, err)
	}
}
