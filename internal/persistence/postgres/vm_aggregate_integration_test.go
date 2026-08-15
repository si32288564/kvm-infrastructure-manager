package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

func TestVMAggregateAuthorityPostgreSQLIntegration(t *testing.T) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, url, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('vm-aggregate',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	vmID := fmt.Sprintf("82000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	host, group := "vm-aggregate-host-"+suffix, "vm-aggregate-pool-"+suffix
	prepareEvacuationHost(t, ctx, pool, host)
	if err = UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: group, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "vm-aggregate-placement", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "vm-aggregate-members-" + suffix, HostGroupID: group, SourceType: "EXPLICIT", SourceRevision: suffix, BasedOnHostGroupGeneration: 1, Members: []HostGroupMembership{{HostGroupID: group, HostID: host, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}}}); err != nil {
		t.Fatal(err)
	}

	policyID := "vm-aggregate-policy-" + suffix
	policy := availabilityPolicyFixture(policyID, 1, "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION", "ACTIVE")
	policyDigest, err := PublishAvailabilityPolicy(ctx, pool, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "vm-aggregate-policy-binding-" + suffix, BindingID: "vm-aggregate-policy-binding-" + suffix, HostGroupID: group, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: policyID, PolicyRevision: 1, PolicyDigest: policyDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	scopeID := "vm-aggregate-scope-" + suffix
	if _, err = PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "vm-aggregate-scope-publish-" + suffix, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: group, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}

	backendID, vgUUID, classID := "vm-aggregate-backend-"+suffix, "vm-aggregate-vg-"+suffix, "vm-aggregate-class-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, pool, LocalLVMFoundation{BackendID: backendID, HostID: host, VGUUID: vgUUID, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: classID, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "vm-aggregate-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	imageID, flavorID := "vm-aggregate-image-"+suffix, "vm-aggregate-flavor-"+suffix
	imageDigest := digestBytes([]byte("vm-aggregate-image"))
	if _, err = RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: imageDigest, ObservedChecksum: imageDigest, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "qualification://vm-aggregate", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "vm-aggregate.small", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}

	volumeRequest := VolumeResourceRequest{VolumeID: "vm-aggregate-root-" + suffix, ProjectID: "project", Name: "root", StorageClassID: classID, StorageClassRevision: 1, SizeBytes: 16 << 20, Bootable: true, SourceType: "BLANK"}
	volume, err := CreateVolumeResource(ctx, pool, volumeRequest)
	if err != nil {
		t.Fatal(err)
	}
	volume, err = AllocateVolumeCapacity(ctx, pool, VolumeCapacityAllocationRequest{VolumeID: volume.VolumeID, BackendID: backendID, ExpectedVolumeRevision: 1, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := &volumeResourceLVMClient{vgUUID: vgUUID}
	mutation := locallvm.Backend{Client: client, VolumeGroups: map[string]string{vgUUID: "kim_test_vg"}}
	readOnly := locallvm.ReadBackBackend{Backend: mutation}
	first, err := ClaimVolumeMaterialization(ctx, pool, volume.OperationID, "vm-aggregate-volume-worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := AuthorizeVolumeMaterializationCommand(ctx, pool, first, "vm-aggregate-volume-apply-job-"+suffix, "vm-aggregate-volume-apply-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, pool, apply.CommandID, 1, CommandLeaseScopeMutation, mutation)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, pool, first); err != nil {
		t.Fatal(err)
	}
	successor, err := ClaimVolumeMaterialization(ctx, pool, volume.OperationID, "vm-aggregate-volume-worker-b", time.Minute)
	if err != nil || successor.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("successor=%+v err=%v", successor, err)
	}
	read, err := AuthorizeVolumeMaterializationCommand(ctx, pool, successor, "vm-aggregate-volume-read-job-"+suffix, "vm-aggregate-volume-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	volumeVerification := observeVolumeBackendAfterLostResponse(t, ctx, pool, read.CommandID, "vm-aggregate-volume-verification-"+suffix, 1, readOnly)
	if _, err = CompleteVolumeMaterialization(ctx, pool, successor, CompleteVolumeMaterializationRequest{OperationID: successor.OperationID, OperationGeneration: successor.OperationGeneration, ClaimGeneration: successor.ClaimGeneration, ObservationID: "vm-aggregate-volume-observation-" + suffix, VerificationID: volumeVerification}); err != nil {
		t.Fatal(err)
	}
	volume, err = GetVolumeResource(ctx, pool, volume.VolumeID)
	if err != nil || volume.Lifecycle != "AVAILABLE" || volume.MaterializationState != "VERIFIED" {
		t.Fatalf("volume=%+v err=%v", volume, err)
	}

	create := VMAggregateCreateRequest{RequestID: "vm-aggregate-create-request-" + suffix, OperationID: "vm-aggregate-create-operation-" + suffix, VMID: vmID, ProjectID: "project", Name: "qualified-vm-" + suffix, FlavorID: flavorID, FlavorRevision: 1, ImageID: imageID, ImageRevision: 1, AvailabilityPolicyID: policyID, AvailabilityPolicyRevision: 1, PlacementScopeID: scopeID, PlacementScopeGeneration: 1, RootVolumeID: volume.VolumeID, RootVolumeRevision: 1, DesiredPowerState: "RUNNING"}
	wrong := create
	wrong.RequestID, wrong.OperationID, wrong.VMID, wrong.RootVolumeRevision = wrong.RequestID+"-wrong", wrong.OperationID+"-wrong", "82000000-0000-4000-8000-000000000002", 2
	if _, err = CreateVMAggregate(ctx, pool, wrong); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("stale Volume revision accepted: %v", err)
	}
	aggregate, err := CreateVMAggregate(ctx, pool, create)
	if err != nil || aggregate.VMRevision != 1 || aggregate.RuntimeIntentGeneration != 1 || aggregate.OperationState != "PENDING" {
		t.Fatalf("aggregate=%+v err=%v", aggregate, err)
	}
	if replay, err := CreateVMAggregate(ctx, pool, create); err != nil || replay.DependencyDigest != aggregate.DependencyDigest {
		t.Fatalf("create replay=%+v err=%v", replay, err)
	}

	claim, err := ClaimVMAggregateLifecycle(ctx, pool, aggregate.OperationID, "vm-aggregate-worker-placement", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	placementRequest, err := CompileVMAggregatePlacement(ctx, pool, claim)
	if err != nil || placementRequest.WorkloadID != vmID || len(placementRequest.Network) != 0 || len(placementRequest.PCI) != 0 || len(placementRequest.Storage) != 1 || placementRequest.Storage[0].VolumeID != volume.VolumeID {
		t.Fatalf("compiled placement=%+v err=%v", placementRequest, err)
	}
	dry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, placementRequest)
	if err != nil || dry.Status != "READY" || len(dry.Candidates) != 1 {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	admission, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, dry, placementRequest, dry.Candidates[0])
	if err != nil || admission.HostID != host {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	if _, err = BindVMAggregateAdmission(ctx, pool, claim, admission.AdmissionID); err != nil {
		t.Fatal(err)
	}

	claim, err = ClaimVMAggregateLifecycle(ctx, pool, aggregate.OperationID, "vm-aggregate-worker-materialization", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := PrepareVMAggregateMaterialization(ctx, pool, claim)
	if err != nil || decision.HostID != host {
		t.Fatalf("materialization=%+v err=%v", decision, err)
	}
	defineCommand := "vm-define-command:" + aggregate.OperationID + ":1"
	defineVerification := "vm-aggregate-define-verification-" + suffix
	defineEvidence := map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "plan_digest": decision.PlanDigest, "domain_present": true, "domain_identity_matches": true, "plan_identity_matches": true, "compute_shape_matches": true, "root_volume_identity_matches": true, "image_materialization_state": "PENDING", "network_realization_state": "PENDING"}
	defineAttempt := acceptEvacuationCommand(t, ctx, pool, host, defineCommand, defineVerification, 1, defineEvidence, "SUCCEEDED")
	if err = AcceptVMDefinitionObservation(ctx, pool, VMDefinitionObservation{EvidenceID: "vm-aggregate-define-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: decision.PlanID, PlanDigest: decision.PlanDigest, HostID: host, CommandID: defineCommand, AttemptIndex: uint32(defineAttempt), VerificationID: defineVerification, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(defineCommand + "/observation")), VerifierDigest: digestBytes([]byte(defineCommand + "/verifier")), EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	imageRequest := VMImageMaterializationRequest{VMID: vmID, PlanID: decision.PlanID, JobID: "vm-aggregate-image-job-" + suffix, CommandID: "vm-aggregate-image-command-" + suffix}
	if _, err = PrepareVMImageMaterialization(ctx, pool, imageRequest); err != nil {
		t.Fatal(err)
	}
	var resourceKey string
	if err = pool.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, volume.BindingID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	imageVerification := "vm-aggregate-image-verification-" + suffix
	imageEvidence := map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "image_id": imageID, "image_revision": float64(1), "expected_content_digest": imageDigest, "observed_content_digest": imageDigest, "image_size_bytes": float64(4096), "volume_id": volume.VolumeID, "observed_vg_uuid": vgUUID, "observed_lv_uuid": volume.LVUUID, "backend_resource_key": resourceKey, "holder_open": false, "content_identity_matches": true}
	imageAttempt := acceptEvacuationCommand(t, ctx, pool, host, imageRequest.CommandID, imageVerification, 1, imageEvidence, "SUCCEEDED")
	if err = AcceptVMImageRealizationObservation(ctx, pool, VMImageRealizationObservation{EvidenceID: "vm-aggregate-image-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: decision.PlanID, PlanDigest: decision.PlanDigest, HostID: host, ImageID: imageID, ImageRevision: 1, ExpectedDigest: imageDigest, ObservedDigest: imageDigest, ImageSizeBytes: 4096, VolumeID: volume.VolumeID, BindingID: volume.BindingID, BindingGeneration: volume.BindingGeneration, VGUUID: vgUUID, LVUUID: volume.LVUUID, BackendResourceKey: resourceKey, CommandID: imageRequest.CommandID, AttemptIndex: uint32(imageAttempt), VerificationID: imageVerification, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(imageRequest.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(imageRequest.CommandID + "/verifier")), EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}

	claim, err = ClaimVMAggregateLifecycle(ctx, pool, aggregate.OperationID, "vm-aggregate-worker-verification", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EvaluateVMAggregateEvidence(ctx, pool, claim, "vm-aggregate-pre-power-verification-"+suffix); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("missing RUNNING read-back accepted: %v", err)
	}
	powerCommand := "vm-aggregate-power-command-" + suffix
	if err = AuthorizeZeroPortVMPowerOn(ctx, pool, vmID, 1, host, "vm-aggregate-power-job-"+suffix, powerCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationLostReadBack(t, ctx, pool, host, powerCommand, "vm-aggregate-power-verification-"+suffix, 1, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"})
	verificationID := "vm-aggregate-verification-" + suffix
	verification, err := EvaluateVMAggregateEvidence(ctx, pool, claim, verificationID)
	if err != nil || verification.VerificationState != "VERIFIED" {
		t.Fatalf("verification=%+v err=%v", verification, err)
	}
	if replay, err := EvaluateVMAggregateEvidence(ctx, pool, claim, verificationID); err != nil || replay.VerificationDigest != verification.VerificationDigest {
		t.Fatalf("verification replay=%+v err=%v", replay, err)
	}
	terminalID := "vm-aggregate-terminal-" + suffix
	driftRollback := errors.New("rollback terminal drift branch")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET observation_generation=observation_generation+1 WHERE vm_id=$1`, vmID); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateLifecycle(ctx, scopeTxBeginner{tx}, claim, verificationID, terminalID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("terminal readiness drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	if terminal, err := CompleteVMAggregateLifecycle(ctx, pool, claim, verificationID, terminalID); err != nil || terminal != terminalID {
		t.Fatalf("terminal=%s err=%v", terminal, err)
	}
	if terminal, err := CompleteVMAggregateLifecycle(ctx, pool, claim, verificationID, terminalID); err != nil || terminal != terminalID {
		t.Fatalf("terminal replay=%s err=%v", terminal, err)
	}
	aggregate, err = GetVMAggregate(ctx, pool, vmID)
	if err != nil || aggregate.LifecycleState != "ACTIVE" || aggregate.ConvergenceState != "CONVERGED" || aggregate.OperationState != "VERIFIED" {
		t.Fatalf("terminal aggregate=%+v err=%v", aggregate, err)
	}
	var runtimeHost, runtimeAdmission, runtimePlan string
	if err = pool.QueryRow(ctx, `SELECT host_id,admission_id,plan_id FROM kim.vm_resource_runtime_bindings_current WHERE vm_id=$1`, vmID).Scan(&runtimeHost, &runtimeAdmission, &runtimePlan); err != nil || runtimeHost != host || runtimeAdmission != admission.AdmissionID || runtimePlan != decision.PlanID {
		t.Fatalf("runtime=%s/%s/%s err=%v", runtimeHost, runtimeAdmission, runtimePlan, err)
	}
	for _, table := range []string{"vm_dependency_snapshot_evidence", "vm_runtime_intent_evidence", "vm_aggregate_verification_evidence", "vm_aggregate_terminal_evidence"} {
		if _, err = pool.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
}
