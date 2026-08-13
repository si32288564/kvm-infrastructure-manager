package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

type mixedRecoveryAuthority struct {
	Epoch, Confirmation, FencingProof, StorageProof, RootProof  string
	Operation, Verification, Terminal, Budget                   string
	Retirement, Quiescence, Handoff                             string
	DestinationAdmission, DestinationPlan                       string
	DestinationRealization, DestinationDataplane, PowerEvidence string
	Source, Destination                                         repeatedEvacuationIncarnation
}

func completeMixedRecoveryDestination(t *testing.T, ctx context.Context, db recoveryQualificationDB, suffix, vmID, imageID, checksum, networkID, segmentID, portID, mac, destinationBackend, destinationVG string, plan RecoveryPlan, start RecoveryOperationStart, fencingProof, storageProof string) mixedRecoveryAuthority {
	t.Helper()
	out := mixedRecoveryAuthority{Operation: start.RecoveryOperationID, Budget: start.BudgetClaimID, FencingProof: fencingProof, StorageProof: storageProof, DestinationAdmission: start.DestinationAdmissionID}
	acceptEvacuationCommand(t, ctx, db, start.DestinationHostID, start.ExecutionCommandID, "mixed-recovery-preparation-verification-"+suffix, 1, map[string]any{"state": "APPLIED"}, "SUCCEEDED")
	if _, err := RefreshRecoveryOperationExecution(ctx, db, start.RecoveryOperationID, "mixed-recovery-preparation-verification-"+suffix); err != nil {
		t.Fatal(err)
	}
	required := plan.DestinationRequest.Storage[0]
	bindingID, lvUUID := realizeEvacuationBinding(t, ctx, db, start.DestinationHostID, start.DestinationAdmissionID, required.VolumeID, destinationBackend, destinationVG, plan.DestinationRequest.RequestID)
	request := RecoveryMaterializationRequest{RecoveryOperationID: start.RecoveryOperationID, MaterializationID: "mixed-recovery-materialization-" + suffix, VMID: vmID, VMPlanID: "mixed-recovery-plan-b-" + suffix, DefineJobID: "mixed-recovery-define-job-" + suffix, DefineCommandID: "mixed-recovery-define-command-" + suffix}
	materialization, err := PrepareRecoveryMaterialization(ctx, db, request)
	if err != nil || materialization.MaterializationGeneration != 2 {
		t.Fatalf("Recovery materialization=%+v err=%v", materialization, err)
	}
	out.DestinationPlan = materialization.VMPlanID
	defineVerification := "mixed-recovery-define-verification-" + suffix
	defineAttempt := acceptEvacuationCommand(t, ctx, db, start.DestinationHostID, request.DefineCommandID, defineVerification, 2, map[string]any{"domain_uuid": vmID, "materialization_generation": float64(2), "plan_digest": materialization.VMPlanDigest, "domain_present": true, "domain_identity_matches": true, "plan_identity_matches": true, "compute_shape_matches": true, "root_volume_identity_matches": true, "image_materialization_state": "PENDING", "network_realization_state": "PENDING"}, "SUCCEEDED")
	if err := AcceptVMDefinitionObservation(ctx, db, VMDefinitionObservation{EvidenceID: "mixed-recovery-define-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: materialization.VMPlanID, PlanDigest: materialization.VMPlanDigest, HostID: start.DestinationHostID, CommandID: request.DefineCommandID, AttemptIndex: uint32(defineAttempt), VerificationID: defineVerification, ObservationGeneration: 2, ObservationDigest: digestBytes([]byte(request.DefineCommandID + "/observation")), VerifierDigest: digestBytes([]byte(request.DefineCommandID + "/verifier")), EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	imageRequest := VMImageMaterializationRequest{VMID: vmID, PlanID: materialization.VMPlanID, JobID: "mixed-recovery-image-job-" + suffix, CommandID: "mixed-recovery-image-command-" + suffix}
	if _, err := PrepareVMImageMaterialization(ctx, db, imageRequest); err != nil {
		t.Fatal(err)
	}
	var resourceKey string
	if err := db.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, bindingID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	imageVerification := "mixed-recovery-image-verification-" + suffix
	imageAttempt := acceptEvacuationCommand(t, ctx, db, start.DestinationHostID, imageRequest.CommandID, imageVerification, 2, map[string]any{"domain_uuid": vmID, "materialization_generation": float64(2), "image_id": imageID, "image_revision": float64(1), "expected_content_digest": checksum, "observed_content_digest": checksum, "image_size_bytes": float64(4096), "volume_id": required.VolumeID, "observed_vg_uuid": destinationVG, "observed_lv_uuid": lvUUID, "backend_resource_key": resourceKey, "holder_open": false, "content_identity_matches": true}, "SUCCEEDED")
	if err := AcceptVMImageRealizationObservation(ctx, db, VMImageRealizationObservation{EvidenceID: "mixed-recovery-image-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: materialization.VMPlanID, PlanDigest: materialization.VMPlanDigest, HostID: start.DestinationHostID, ImageID: imageID, ImageRevision: 1, ExpectedDigest: checksum, ObservedDigest: checksum, ImageSizeBytes: 4096, VolumeID: required.VolumeID, BindingID: bindingID, BindingGeneration: 1, VGUUID: destinationVG, LVUUID: lvUUID, BackendResourceKey: resourceKey, CommandID: imageRequest.CommandID, AttemptIndex: uint32(imageAttempt), VerificationID: imageVerification, ObservationGeneration: 2, ObservationDigest: digestBytes([]byte(imageRequest.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(imageRequest.CommandID + "/verifier")), EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	completeEvacuationOVNIntent(t, ctx, db, "mixed-recovery-destination-intent-"+suffix, portID, "mixed-recovery-ovn-worker-"+suffix, "mixed-recovery-b-"+suffix, 3)
	realizeRequest := OVSPortRealizationRequest{VMID: vmID, PlanID: materialization.VMPlanID, PortID: portID, JobID: "mixed-recovery-ovs-job-" + suffix, CommandID: "mixed-recovery-ovs-command-" + suffix}
	if _, err := PrepareOVSPortRealization(ctx, db, realizeRequest); err != nil {
		t.Fatal(err)
	}
	realizeVerification := "mixed-recovery-ovs-verification-" + suffix
	realizeAttempt := acceptEvacuationCommand(t, ctx, db, start.DestinationHostID, realizeRequest.CommandID, realizeVerification, 2, map[string]any{"domain_uuid": vmID, "vm_generation": float64(1), "port_id": portID, "port_generation": float64(2), "network_id": networkID, "network_generation": float64(1), "segment_claim_id": segmentID, "segment_generation": float64(1), "host_mapping_generation": float64(1), "binding_generation": float64(2), "binding_type": "OVS", "mac_address": mac, "interface_id": portID, "domain_nic_identity_matches": true, "bridge_observed": true}, "SUCCEEDED")
	out.DestinationRealization = "mixed-recovery-ovs-evidence-" + suffix
	if err := AcceptOVSPortRealizationAndMaybeArmPower(ctx, db, OVSPortRealizationObservation{EvidenceID: out.DestinationRealization, VMID: vmID, VMGeneration: 1, PlanID: materialization.VMPlanID, HostID: start.DestinationHostID, PortID: portID, PortGeneration: 2, NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 2, CommandID: realizeRequest.CommandID, AttemptIndex: uint32(realizeAttempt), VerificationID: realizeVerification, ObservationGeneration: 2, ObservationDigest: digestBytes([]byte(realizeRequest.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(realizeRequest.CommandID + "/verifier")), DeferPowerAuthorization: true}); err != nil {
		t.Fatal(err)
	}
	dangerous, err := EvaluateRecoveryDangerousStep(ctx, db, "mixed-recovery-dangerous-"+suffix, start.RecoveryOperationID, digestBytes([]byte("mixed-recovery-dangerous/v1")))
	if err != nil || dangerous.ResultState != "AUTHORIZED" || dangerous.FencingProofID != fencingProof || dangerous.StorageSafetyProofID != storageProof {
		t.Fatalf("dangerous=%+v err=%v", dangerous, err)
	}
	power, err := AuthorizeRecoveryPowerOn(ctx, db, "mixed-recovery-power-authority-"+suffix, start.RecoveryOperationID, dangerous.EvaluationID, "mixed-recovery-power-job-"+suffix, "mixed-recovery-power-command-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, db, start.DestinationHostID, power.PowerCommandID, "mixed-recovery-power-verification-"+suffix, 3, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")
	if _, err := RefreshRecoveryPowerExecution(ctx, db, start.RecoveryOperationID, "mixed-recovery-power-verification-"+suffix); err != nil {
		t.Fatal(err)
	}
	out.PowerEvidence = "vm-power/" + power.PowerCommandID + "/1"
	out.DestinationDataplane = convergeEvacuationOVSDataplane(t, ctx, db, start.DestinationHostID, vmID, materialization.VMPlanID, portID, networkID, segmentID, mac, "mixed-recovery-b-"+suffix, 2, 2, 2)
	if _, _, err := RefreshRecoveryNetworkVerificationReadiness(ctx, db, start.RecoveryOperationID); err != nil {
		t.Fatal(err)
	}
	attachmentCommand := "mixed-recovery-root-attachment-command-" + suffix
	if err := CreateExecutionCommand(ctx, db, ExecutionCommandRequest{JobID: "mixed-recovery-root-attachment-job-" + suffix, CommandID: attachmentCommand, HostID: start.DestinationHostID, ResourceType: "VOLUME_ATTACHMENT", ResourceID: required.AttachmentID, DesiredRevision: 1, CommandType: libvirtvolume.CommandType, SchemaVersion: libvirtvolume.SchemaVersion, TargetResourceID: "attachment:" + required.AttachmentID, Payload: map[string]any{"domain_uuid": vmID, "volume_id": required.VolumeID, "vg_uuid": destinationVG, "lv_uuid": lvUUID, "backend_resource_key": resourceKey, "disk_slot": 0, "desired_state": "ATTACHED", "access_mode": "SINGLE_WRITER"}}); err != nil {
		t.Fatal(err)
	}
	attachmentVerification := "mixed-recovery-root-attachment-verification-" + suffix
	attachmentAttempt := acceptEvacuationCommand(t, ctx, db, start.DestinationHostID, attachmentCommand, attachmentVerification, 2, map[string]any{"attachment_id": required.AttachmentID, "volume_id": required.VolumeID, "binding_id": bindingID, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": lvUUID, "desired_state": "ATTACHED", "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": true, "read_only": false}, "SUCCEEDED")
	if err := AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: "mixed-recovery-root-attachment-evidence-" + suffix, AttachmentID: required.AttachmentID, VolumeID: required.VolumeID, AttachmentGeneration: materialization.RootAttachmentGeneration, BindingID: bindingID, BindingGeneration: materialization.RootBindingGeneration, HostID: start.DestinationHostID, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvUUID, DesiredState: "ATTACHED", CommandID: attachmentCommand, VerificationID: attachmentVerification, ObservationGeneration: 2, AttemptIndex: uint32(attachmentAttempt), ObservationDigest: digestBytes([]byte(attachmentCommand + "/observation")), VerifierDigest: digestBytes([]byte(attachmentCommand + "/verifier")), EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true, HolderOpen: true}); err != nil {
		t.Fatalf("accept Recovery root attachment: %v", err)
	}
	out.Verification = "mixed-recovery-verification-" + suffix
	verification, err := EvaluateRecoveryVerification(ctx, db, out.Verification, start.RecoveryOperationID, "mixed-recovery-verifier/v1", digestBytes([]byte("mixed-recovery-verifier/v1")))
	if err != nil || verification.ResultState != "VERIFIED" {
		t.Fatalf("Recovery verification=%+v err=%v", verification, err)
	}
	out.Terminal = "mixed-recovery-terminal-" + suffix
	if _, err := CommitRecoveryTerminalDecision(ctx, db, out.Terminal, out.Verification, "mixed-recovery-authority/v1"); err != nil {
		t.Fatal(err)
	}
	out.Destination = repeatedEvacuationIncarnation{Host: start.DestinationHostID, Admission: start.DestinationAdmissionID, Plan: materialization.VMPlanID, Volume: required.VolumeID, Attachment: required.AttachmentID, Binding: bindingID, LV: lvUUID, Backend: destinationBackend, VG: destinationVG, Materialization: 2, PortGeneration: 2, BindingGeneration: 2}
	return out
}

func TestMixedRecoveryThenHostEvacuationPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('mixed-recovery-evacuation',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	vmID := fmt.Sprintf("71000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	hostA, hostB, hostC := "mixed-origin-a-"+suffix, "mixed-origin-b-"+suffix, "mixed-origin-c-"+suffix
	poolID, scopeID, workloadID := "mixed-origin-pool-"+suffix, "mixed-origin-scope-"+suffix, "mixed-origin-workload-"+suffix
	imageID, flavorID, storageClass := "mixed-origin-image-"+suffix, "mixed-origin-flavor-"+suffix, "mixed-origin-storage-"+suffix
	networkID, subnetID, segmentID, portID := "mixed-origin-network-"+suffix, "mixed-origin-subnet-"+suffix, "mixed-origin-segment-"+suffix, "mixed-origin-port-"+suffix
	mac, ip := "02:00:00:71:00:01", "192.0.2.71"
	for _, host := range []string{hostA, hostB, hostC} {
		prepareEvacuationHost(t, ctx, pool, host)
	}
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: poolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "mixed-origin", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	members := []HostGroupMembership{{HostGroupID: poolID, HostID: hostA, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}, {HostGroupID: poolID, HostID: hostB, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}, {HostGroupID: poolID, HostID: hostC, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "mixed-origin-members-" + suffix, HostGroupID: poolID, SourceType: "EXPLICIT", SourceRevision: suffix, BasedOnHostGroupGeneration: 1, Members: members}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_placement_pool_memberships_current(host_id,pool_id,membership_generation,membership_state) VALUES($1,$4,1,'ACTIVE'),($2,$4,1,'ACTIVE'),($3,$4,1,'ACTIVE')`, hostA, hostB, hostC, poolID); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "mixed-origin-scope-" + suffix, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: poolID, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertNetworkFoundation(ctx, pool, NetworkFoundation{NetworkID: networkID, ProjectID: "project", NetworkGeneration: 1, NetworkState: "ACTIVE", MTU: 1500, SubnetID: subnetID, SubnetGeneration: 1, SubnetState: "ACTIVE", CIDR: "192.0.2.0/24", AllocationStart: "192.0.2.71", AllocationEnd: "192.0.2.71", SegmentClaimID: segmentID, SegmentType: "VLAN", ScopeID: "mixed-origin-" + suffix, SegmentID: 710, SegmentGeneration: 1, ProviderMappingRevision: 1, SegmentState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{hostA, hostB, hostC} {
		if err := UpsertHostNetworkMapping(ctx, pool, HostNetworkMapping{HostID: host, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "chassis-" + host}); err != nil {
			t.Fatal(err)
		}
	}
	backendA, vgA := registerRepeatedEvacuationStorage(t, ctx, pool, hostA, storageClass, suffix, 1)
	backendB, vgB := registerRepeatedEvacuationStorage(t, ctx, pool, hostB, storageClass, suffix, 2)
	confirmation, err := PublishFailureConfirmationPolicy(ctx, pool, FailureConfirmationPolicy{PolicyID: "mixed-origin-confirmation-" + suffix, PolicyRevision: 1, ApplicableFailureClass: "VM_RUNTIME_UNAVAILABLE", ConfirmationMode: "ALL_REQUIRED_EVIDENCE", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification", Requirements: []FailureConfirmationRequirement{{Ordinal: 1, EvidenceType: "VM_RUNTIME_OBSERVATION", ObservedState: "PRESENT", FreshnessState: "CURRENT", SourceType: "LIBVIRT_READ_BACK"}}})
	if err != nil {
		t.Fatal(err)
	}
	fencing, err := PublishFailureFencingPolicy(ctx, pool, FailureFencingPolicy{PolicyID: "mixed-origin-fencing-" + suffix, PolicyRevision: 1, FencingMode: "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	storagePolicy, err := PublishStorageSafetyPolicy(ctx, pool, StorageSafetyPolicy{PolicyID: "mixed-origin-storage-" + suffix, PolicyRevision: 1, StorageClass: "LOCAL_LVM", SafetyMode: "SOURCE_ROOT_QUIESCED_DATA_DETACHED", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	budget, err := PublishRecoveryBudgetPolicy(ctx, pool, RecoveryBudgetPolicy{PolicyID: "mixed-origin-budget-" + suffix, PolicyRevision: 1, ScopeType: "GLOBAL", Phase: "PLANNING", MaxActiveRecoveries: 1, LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	availability := availabilityPolicyFixture("mixed-origin-availability-"+suffix, 1, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE")
	availability.FailureConfirmationPolicyID, availability.FailureConfirmationPolicyRevision, availability.FailureConfirmationPolicyDigest = confirmation.PolicyID, 1, confirmation.PolicyDigest
	availability.FencingPolicyID, availability.FencingPolicyRevision, availability.FencingPolicyDigest = fencing.PolicyID, 1, fencing.PolicyDigest
	availability.StorageSafetyPolicyID, availability.StorageSafetyPolicyRevision, availability.StorageSafetyPolicyDigest = storagePolicy.PolicyID, 1, storagePolicy.PolicyDigest
	availability.RecoveryBudgetPolicyID, availability.RecoveryBudgetPolicyRevision, availability.RecoveryBudgetPolicyDigest = budget.PolicyID, 1, budget.PolicyDigest
	availabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, availability)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "mixed-origin-policy-binding-" + suffix, BindingID: "mixed-origin-policy-binding-" + suffix, HostGroupID: poolID, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: availability.PolicyID, PolicyRevision: 1, PolicyDigest: availabilityDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	checksum := digestBytes([]byte("mixed-origin-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "qualification://mixed-origin", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "mixed-origin", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	sourceRequest := PlacementAdmissionRequest{RequestID: "mixed-origin-source-" + suffix, ProjectID: "project", WorkloadID: workloadID, ImageID: imageID, FlavorID: flavorID, PlacementScopeID: scopeID, Network: []placement.NetworkRequirement{{PortID: portID, NetworkID: networkID, NetworkGeneration: 1, SubnetID: subnetID, SubnetGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, IPAddress: ip, MACAddress: mac, AllocationSource: "EXPLICIT", BindingType: "OVS", RequiredMTU: 1500}}, Storage: []placement.StorageRequirement{{VolumeID: "mixed-origin-root-a-" + suffix, AttachmentID: "mixed-origin-attachment-a-" + suffix, BackendID: backendA, BackendGeneration: 1, VGUUID: vgA, StorageClassID: storageClass, StorageClassRevision: 1, CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1, SizeBytes: 16 << 20, AccessMode: "SINGLE_WRITER", Bootable: true}}}
	sourceDry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, sourceRequest)
	if err != nil || sourceDry.Status != "READY" {
		t.Fatalf("source dry=%+v err=%v", sourceDry, err)
	}
	var sourceCandidate AvailabilityPlacementCandidate
	for _, candidate := range sourceDry.Candidates {
		if candidate.Placement.HostID == hostA && candidate.Placement.Eligible {
			sourceCandidate = candidate
		}
	}
	sourceAdmission, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, sourceDry, sourceRequest, sourceCandidate)
	if err != nil || sourceAdmission.AvailabilityBinding == nil {
		t.Fatalf("source admission=%+v err=%v", sourceAdmission, err)
	}
	bindingA, lvA := realizeEvacuationBinding(t, ctx, pool, hostA, sourceAdmission.AdmissionID, sourceRequest.Storage[0].VolumeID, backendA, vgA, sourceRequest.RequestID)
	planA := "mixed-origin-plan-a-" + suffix
	materializeEvacuationVM(t, ctx, pool, hostA, vmID, sourceAdmission.AdmissionID, planA, imageID, checksum, sourceRequest.Storage[0].VolumeID, bindingA, vgA, lvA, "mixed-origin-a-"+suffix, "", 1)
	completeEvacuationOVNIntent(t, ctx, pool, "mixed-origin-source-intent-"+suffix, portID, "mixed-origin-source-worker-"+suffix, "mixed-origin-a-"+suffix, 1)
	realizeEvacuationOVSPort(t, ctx, pool, hostA, vmID, planA, portID, networkID, segmentID, mac, "mixed-origin-a-"+suffix, "mixed-origin-source-power-job-"+suffix, "mixed-origin-source-power-command-"+suffix, 1, 1, 1)
	acceptEvacuationCommand(t, ctx, pool, hostA, "mixed-origin-source-power-command-"+suffix, "mixed-origin-source-power-verification-"+suffix, 1, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")
	convergeEvacuationOVSDataplane(t, ctx, pool, hostA, vmID, planA, portID, networkID, segmentID, mac, "mixed-origin-a-"+suffix, 1, 1, 1)
	assertRepeatedEvacuationCurrent(t, ctx, pool, vmID, portID, networkID, subnetID, mac, ip, hostA, 1, 1, 1)
	if err := AuthorizeVMPowerOff(ctx, pool, vmID, 1, hostA, "mixed-origin-source-shutoff-job-"+suffix, "mixed-origin-source-shutoff-command-"+suffix); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, pool, hostA, "mixed-origin-source-shutoff-command-"+suffix, "mixed-origin-source-shutoff-verification-"+suffix, 2, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"}, "SUCCEEDED")
	epoch, err := OpenFailureEpoch(ctx, pool, OpenFailureEpochRequest{OpenRequestID: "mixed-origin-failure-open-" + suffix, FailureEpochID: "mixed-origin-failure-epoch-" + suffix, IncidentKey: "mixed-origin-" + suffix, WorkloadID: workloadID, FailureClass: "VM_RUNTIME_UNAVAILABLE", RequestedBy: "qualification", ExpectedBindingRevision: sourceAdmission.AvailabilityBinding.BindingRevision, ExpectedBindingDigest: sourceAdmission.AvailabilityBinding.BindingDigest, Trigger: FailureObservation{EvidenceID: "mixed-origin-failure-observation-" + suffix, EvidenceType: "VM_RUNTIME_OBSERVATION", SourceType: "LIBVIRT_READ_BACK", SourceHostID: hostA, ObservedState: "PRESENT", FreshnessState: "CURRENT", PayloadDigest: digestBytes([]byte("mixed-origin-source-shutoff-command-" + suffix + "/observation")), ObservationGeneration: 2, ObservedAt: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	confirmationEvaluation, err := EvaluateFailureConfirmation(ctx, pool, "mixed-origin-confirmation-evaluation-"+suffix, epoch.FailureEpochID, "mixed-origin-confirmation/v1", digestBytes([]byte("mixed-origin-confirmation/v1")))
	if err != nil || confirmationEvaluation.ResultState != "SATISFIED" {
		t.Fatalf("confirmation=%+v err=%v", confirmationEvaluation, err)
	}
	confirmationDecision := "mixed-origin-confirmation-decision-" + suffix
	if _, _, err := ConfirmFailureEpoch(ctx, pool, confirmationDecision, confirmationEvaluation.EvaluationID, "mixed-origin-failure-authority/v1"); err != nil {
		t.Fatal(err)
	}
	rootCommand := "mixed-origin-source-root-command-" + suffix
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "mixed-origin-source-root-job-" + suffix, CommandID: rootCommand, HostID: hostA, ResourceType: "SOURCE_ROOT_SAFETY", ResourceID: sourceRequest.Storage[0].AttachmentID, DesiredRevision: 1, CommandType: SourceRootSafetyReadBackCommandType, SchemaVersion: SourceRootSafetyReadBackSchema, TargetResourceID: "attachment:" + sourceRequest.Storage[0].AttachmentID, Payload: map[string]any{"desired_state": "OBSERVE"}}); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return fenceHostOperationAuthorityTx(ctx, tx, hostA, "mixed_origin_recovery") }); err != nil {
		t.Fatal(err)
	}
	fencingObservation, err := RecordSourceExecutionFencingObservation(ctx, pool, "mixed-origin-fencing-observation-"+suffix, epoch.FailureEpochID)
	if err != nil || fencingObservation.ObservationState != "PROVEN" {
		t.Fatalf("fencing observation=%+v err=%v", fencingObservation, err)
	}
	fencingEvaluation, err := EvaluateFailureFencing(ctx, pool, "mixed-origin-fencing-evaluation-"+suffix, epoch.FailureEpochID, "mixed-origin-fencing/v1", digestBytes([]byte("mixed-origin-fencing/v1")))
	if err != nil {
		t.Fatal(err)
	}
	rootVerification := "mixed-origin-source-root-verification-" + suffix
	rootAttempt := acceptEvacuationCommand(t, ctx, pool, hostA, rootCommand, rootVerification, 2, map[string]any{"attachment_id": sourceRequest.Storage[0].AttachmentID, "volume_id": sourceRequest.Storage[0].VolumeID, "binding_id": bindingA, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": lvA, "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": false}, "SUCCEEDED")
	if err := AcceptSourceRootSafetyObservation(ctx, pool, LocalLVMAttachmentObservation{EvidenceID: "mixed-origin-source-root-evidence-" + suffix, AttachmentID: sourceRequest.Storage[0].AttachmentID, VolumeID: sourceRequest.Storage[0].VolumeID, AttachmentGeneration: 1, BindingID: bindingA, BindingGeneration: 1, HostID: hostA, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvA, CommandID: rootCommand, VerificationID: rootVerification, AttemptIndex: uint32(rootAttempt), ObservationGeneration: 2, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	rootEvaluation, err := EvaluateSourceRootSafety(ctx, pool, "mixed-origin-root-evaluation-"+suffix, epoch.FailureEpochID, "mixed-origin-root/v1", digestBytes([]byte("mixed-origin-root/v1")))
	if err != nil || rootEvaluation.ResultState != "SAFE" {
		t.Fatalf("root evaluation=%+v err=%v", rootEvaluation, err)
	}
	rootProof, err := MaterializeSourceRootSafetyProof(ctx, pool, "mixed-origin-root-proof-"+suffix, rootEvaluation.EvaluationID, "mixed-origin-root-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	storageEvaluation, err := EvaluateStorageSafety(ctx, pool, "mixed-origin-storage-evaluation-"+suffix, epoch.FailureEpochID, "mixed-origin-storage/v1", digestBytes([]byte("mixed-origin-storage/v1")))
	if err != nil || storageEvaluation.ResultState != "SAFE" {
		t.Fatalf("storage evaluation=%+v err=%v", storageEvaluation, err)
	}
	storageProof, err := MaterializeStorageSafetyProof(ctx, pool, "mixed-origin-storage-proof-"+suffix, storageEvaluation.EvaluationID, "mixed-origin-storage-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	fencingProof, _, err := MaterializeFailureFencingProof(ctx, pool, "mixed-origin-fencing-proof-"+suffix, fencingEvaluation.EvaluationID, "mixed-origin-fencing-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	retirement, err := CommitOVNPortBindingRetirement(ctx, pool, OVNPortBindingRetirementRequest{OperationID: "mixed-origin-recovery-retirement-" + suffix, OperationGeneration: 1, IntentID: "mixed-origin-recovery-retirement-intent-" + suffix, IntentGeneration: 2, PortID: portID, PortGeneration: 1, BindingGeneration: 1, SourceHostID: hostA})
	if err != nil {
		t.Fatal(err)
	}
	work, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "mixed-origin-retirement-worker-" + suffix, Limit: 1, Lease: time.Minute})
	if err != nil || len(work) != 1 {
		t.Fatalf("retirement work=%+v err=%v", work, err)
	}
	retirementClaim := OVNRuntimeClaim{WorkID: retirement.WorkID, Owner: "mixed-origin-retirement-worker-" + suffix, ClaimGeneration: work[0].ClaimGeneration}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, retirementClaim); err != nil {
		t.Fatal(err)
	}
	recoveryRetirementEvidence := "mixed-origin-recovery-retirement-evidence-" + suffix
	if err := CompleteOVNPortBindingRetirement(ctx, pool, retirementClaim, OVNPortBindingRetirementObservation{EvidenceID: recoveryRetirementEvidence, IntentID: retirement.IntentID, IntentGeneration: retirement.IntentGeneration, PortID: portID, PortGeneration: 1, BindingGeneration: 1, SourceHostID: hostA, OperationGeneration: 1, NBObservationGeneration: 2, SBObservationGeneration: 2, OVSObservationGeneration: 2, NBObservationDigest: digestBytes([]byte("mixed-origin-retirement-nb-" + suffix)), SBObservationDigest: digestBytes([]byte("mixed-origin-retirement-sb-" + suffix)), OVSObservationDigest: digestBytes([]byte("mixed-origin-retirement-ovs-" + suffix)), AdapterArtifactDigest: digestBytes([]byte("mixed-origin-retirement-adapter-" + suffix)), ApplyResponseState: "RECEIVED", Observation: verifiedOVNRetirementObservation()}); err != nil {
		t.Fatal(err)
	}
	quiescence, err := PrepareNetworkPortSourceQuiescence(ctx, pool, NetworkPortSourceQuiescenceRequest{FailureEpochID: epoch.FailureEpochID, PortID: portID, JobID: "mixed-origin-recovery-quiescence-job-" + suffix, CommandID: "mixed-origin-recovery-quiescence-command-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	quiescenceVerification := "mixed-origin-recovery-quiescence-verification-" + suffix
	quiescenceAttempt := acceptEvacuationCommand(t, ctx, pool, hostA, quiescence.CommandID, quiescenceVerification, 1, map[string]any{"domain_uuid": vmID, "vm_generation": float64(1), "port_id": portID, "port_generation": float64(1), "binding_generation": float64(1), "domain_running": false, "interface_present": false}, "SUCCEEDED")
	recoveryQuiescence := "mixed-origin-recovery-quiescence-evidence-" + suffix
	if err := AcceptNetworkPortSourceQuiescence(ctx, pool, NetworkPortSourceQuiescenceObservation{EvidenceID: recoveryQuiescence, FailureEpochID: epoch.FailureEpochID, PortID: portID, SourceHostID: hostA, VMID: vmID, CommandID: quiescence.CommandID, VerificationID: quiescenceVerification, ObservationDigest: digestBytes([]byte(quiescence.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(quiescence.CommandID + "/verifier")), PortGeneration: 1, BindingGeneration: 1, VMGeneration: 1, ObservationGeneration: 1, AttemptIndex: uint32(quiescenceAttempt)}); err != nil {
		t.Fatal(err)
	}
	if _, err := RetireSourceMaterialization(ctx, pool, "mixed-origin-source-retirement-"+suffix, epoch.FailureEpochID, rootProof.ProofID, fencingProof.ProofID, "mixed-origin-retirement-authority/v1"); err != nil {
		t.Fatal(err)
	}
	eligibility, err := EvaluateRecoveryEligibility(ctx, pool, "mixed-origin-eligibility-"+suffix, epoch.FailureEpochID, scopeID, "mixed-origin-eligibility/v1", digestBytes([]byte("mixed-origin-eligibility/v1")))
	if err != nil || eligibility.ResultState != "ELIGIBLE" {
		t.Fatalf("eligibility=%+v err=%v", eligibility, err)
	}
	decision, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "mixed-origin-eligibility-decision-"+suffix, eligibility.EvaluationID, "mixed-origin-recovery-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	recoveryOperation := "mixed-origin-recovery-operation-" + suffix
	if _, err := RecordRecoveryOperationRequest(ctx, pool, recoveryOperation, decision.DecisionID, decision.BudgetClaimID, "RESTART_ON_OTHER_HOST", "qualification"); err != nil {
		t.Fatal(err)
	}
	_, recoveryPlan, err := PlanRecoveryOperation(ctx, pool, recoveryOperation, "mixed-origin-recovery-plan-"+suffix, hostB)
	if err != nil {
		t.Fatal(err)
	}
	recoveryStart, err := StartRecoveryOperation(ctx, pool, recoveryOperation, "mixed-origin-recovery-preparation-job-"+suffix, "mixed-origin-recovery-preparation-command-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	recovery := completeMixedRecoveryDestination(t, ctx, pool, suffix, vmID, imageID, checksum, networkID, segmentID, portID, mac, backendB, vgB, recoveryPlan, recoveryStart, fencingProof.ProofID, storageProof.ProofID)
	recovery.Epoch, recovery.Confirmation, recovery.RootProof, recovery.Retirement, recovery.Quiescence = epoch.FailureEpochID, confirmationDecision, rootProof.ProofID, recoveryRetirementEvidence, recoveryQuiescence
	recovery.Source = repeatedEvacuationIncarnation{Host: hostA, Admission: sourceAdmission.AdmissionID, Plan: planA, Volume: sourceRequest.Storage[0].VolumeID, Attachment: sourceRequest.Storage[0].AttachmentID, Binding: bindingA, LV: lvA, Backend: backendA, VG: vgA, Materialization: 1, PortGeneration: 1, BindingGeneration: 1}
	if err := pool.QueryRow(ctx, `SELECT handoff_id FROM kim.port_binding_handoffs_current WHERE port_id=$1`, portID).Scan(&recovery.Handoff); err != nil {
		t.Fatal(err)
	}
	assertRepeatedEvacuationCurrent(t, ctx, pool, vmID, portID, networkID, subnetID, mac, ip, hostB, 2, 2, 2)
	var recoveryState, epochState, budgetState string
	if err := pool.QueryRow(ctx, `SELECT o.lifecycle_state,e.epoch_state,b.claim_state FROM kim.recovery_operations_current o JOIN kim.recovery_operation_evidence oe USING(recovery_operation_id) JOIN kim.failure_epochs_current e ON e.failure_epoch_id=oe.failure_epoch_id JOIN kim.recovery_budget_claims_current b ON b.claim_id=oe.recovery_budget_claim_id WHERE o.recovery_operation_id=$1`, recoveryOperation).Scan(&recoveryState, &epochState, &budgetState); err != nil || recoveryState != "VERIFIED" || epochState != "RECOVERED" || budgetState != "RELEASED" {
		t.Fatalf("Recovery terminal=%s/%s/%s err=%v", recoveryState, epochState, budgetState, err)
	}
	// A rollback-only B authority-loss branch proves the prior Recovery remains
	// terminal while the new planned origin moves to SOURCE_UNREACHABLE.
	rollback := errors.New("rollback mixed-origin authority loss")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		operation, children, err := StartHostEvacuation(ctx, scopeTxBeginner{tx}, HostEvacuationRequest{OperationID: "mixed-origin-evacuation-loss-" + suffix, SourceHostID: hostB, EvacuationGeneration: 1, SourceHostAuthorityGeneration: 1, DrainPolicyID: "planned", DrainPolicyRevision: 1, EvacuationPolicyRevision: 1, MaximumConcurrentWorkloads: 1, Reason: "mixed origin authority loss", RequestedBy: "integration"})
		if err != nil || len(children) != 1 {
			t.Fatalf("loss start=%+v err=%v", operation, err)
		}
		if err := EvaluateHostEvacuationEligibility(ctx, scopeTxBeginner{tx}, operation.OperationID); err != nil {
			t.Fatal(err)
		}
		claim, err := ClaimHostEvacuationWorkload(ctx, scopeTxBeginner{tx}, operation.OperationID, "mixed-loss-worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_operation_authorities_current SET authority_state='DISARMED' WHERE host_id=$1`, hostB); err != nil {
			t.Fatal(err)
		}
		if err := ReconcileHostEvacuationSourceAuthority(ctx, scopeTxBeginner{tx}, operation.OperationID); err != nil {
			t.Fatal(err)
		}
		var parent, child, prior string
		if err := tx.QueryRow(ctx, `SELECT (SELECT lifecycle_state FROM kim.host_evacuation_operations_current WHERE evacuation_operation_id=$1),(SELECT phase FROM kim.host_evacuation_workloads_current WHERE child_operation_id=$2),(SELECT lifecycle_state FROM kim.recovery_operations_current WHERE recovery_operation_id=$3)`, operation.OperationID, claim.ChildOperationID, recoveryOperation).Scan(&parent, &child, &prior); err != nil || parent != "SOURCE_UNREACHABLE" || child != "RECOVERY_REQUIRED" || prior != "VERIFIED" {
			t.Fatalf("loss branch=%s/%s prior=%s err=%v", parent, child, prior, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	backendC, vgC := registerRepeatedEvacuationStorage(t, ctx, pool, hostC, storageClass, suffix, 3)
	var recoveryRetirementOperation string
	if err := pool.QueryRow(ctx, `SELECT operation_id FROM kim.network_port_binding_retirement_evidence WHERE evidence_id=$1`, recovery.Retirement).Scan(&recoveryRetirementOperation); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitOVNPortBindingRetirement(ctx, pool, OVNPortBindingRetirementRequest{OperationID: recoveryRetirementOperation, OperationGeneration: 1, IntentID: "mixed-origin-old-retirement-uplift-intent-" + suffix, IntentGeneration: 4, PortID: portID, PortGeneration: 2, BindingGeneration: 2, SourceHostID: hostB}); err == nil {
		t.Fatal("Recovery A 1/1 retirement operation uplifted to planned B 2/2")
	}
	foreign := map[string]string{"storage_safety": recovery.StorageProof, "operation": recovery.Operation, "power_evidence": recovery.PowerEvidence, "verification": recovery.Verification, "terminal": recovery.Terminal, "handoff": recovery.Handoff, "network_quiescence": recovery.Quiescence}
	evacuation := executeRepeatedEvacuationMove(t, ctx, pool, suffix, "mixed-e2", vmID, imageID, checksum, networkID, segmentID, portID, mac, recovery.Destination, hostC, backendC, vgC, 2, 2, 4, 5, 4, 5, nil, foreign)
	if _, err := FinalizeHostEvacuation(ctx, pool, evacuation.Operation, recovery.Terminal); !errors.Is(err, ErrHostEvacuationConflict) {
		t.Fatalf("Recovery terminal reused by EVACUATE finalize: %v", err)
	}
	if _, err := CommitRecoveryTerminalDecision(ctx, pool, evacuation.ParentTerminal, recovery.Verification, "mixed-recovery-authority/v1"); !errors.Is(err, ErrRecoveryOperationConflict) {
		t.Fatalf("EVACUATE terminal reused by Recovery consumer: %v", err)
	}
	if err := CompleteHostEvacuationChild(ctx, pool, HostEvacuationClaim{OperationID: evacuation.Operation, ChildOperationID: evacuation.Operation + ":workload:" + vmID, Owner: "repeated-worker-mixed-e2", ClaimGeneration: 1}, recovery.Verification, "mixed-origin-cross-verification-"+suffix); err == nil {
		t.Fatal("Recovery verification reused by EVACUATE child terminal")
	}
	assertRepeatedEvacuationCurrent(t, ctx, pool, vmID, portID, networkID, subnetID, mac, ip, hostC, 3, 3, 3)
	mixedCleanup := qualifyDelayedRepeatedLocalLVMCleanup(t, ctx, pool, evacuation.ChildTerminal, "mixed-b-after-c-"+suffix)
	if mixedCleanup.SourceHostID != hostB || mixedCleanup.SourceVolumeID != recovery.Destination.Volume || mixedCleanup.ChildTerminalID != evacuation.ChildTerminal {
		t.Fatalf("mixed-origin planned cleanup consumed wrong origin: %+v", mixedCleanup)
	}
	var history, retirements int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.port_binding_handoff_evidence WHERE handoff_id=ANY($1)),(SELECT count(*) FROM kim.network_port_binding_retirement_evidence WHERE evidence_id=ANY($2))`, []string{recovery.Handoff, evacuation.Handoff}, []string{recovery.Retirement, evacuation.RetirementEvidence}).Scan(&history, &retirements); err != nil || history != 2 || retirements != 2 {
		t.Fatalf("mixed history=%d retirements=%d err=%v", history, retirements, err)
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		for label, candidate := range map[string]struct{ admission, source string }{"A": {recovery.Destination.Admission, hostA}, "B": {evacuation.Destination.Admission, hostB}} {
			required, matched, evidence, err := recoverySourceNetworkCleanupEvidenceTx(ctx, tx, candidate.admission, candidate.source)
			if err != nil || required != 1 || matched != 1 || evidence == "" {
				t.Fatalf("delayed %s cleanup=%d/%d/%q err=%v", label, required, matched, evidence, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT o.lifecycle_state,e.epoch_state,b.claim_state FROM kim.recovery_operations_current o JOIN kim.recovery_operation_evidence oe USING(recovery_operation_id) JOIN kim.failure_epochs_current e ON e.failure_epoch_id=oe.failure_epoch_id JOIN kim.recovery_budget_claims_current b ON b.claim_id=oe.recovery_budget_claim_id WHERE o.recovery_operation_id=$1`, recoveryOperation).Scan(&recoveryState, &epochState, &budgetState); err != nil || recoveryState != "VERIFIED" || epochState != "RECOVERED" || budgetState != "RELEASED" {
		t.Fatalf("EVACUATE rewrote Recovery=%s/%s/%s err=%v", recoveryState, epochState, budgetState, err)
	}
	for _, item := range []struct{ table, column, id string }{{"failure_epoch_evidence", "failure_epoch_id", recovery.Epoch}, {"failure_fencing_proof_evidence", "proof_id", recovery.FencingProof}, {"recovery_verification_evidence", "verification_id", recovery.Verification}, {"recovery_terminal_decision_evidence", "terminal_decision_id", recovery.Terminal}, {"network_port_binding_retirement_evidence", "evidence_id", recovery.Retirement}, {"port_binding_handoff_evidence", "handoff_id", recovery.Handoff}} {
		if _, err := pool.Exec(ctx, fmt.Sprintf("UPDATE kim.%s SET recorded_at=recorded_at WHERE %s=$1", item.table, item.column), item.id); err == nil {
			t.Fatalf("immutable mixed-origin history UPDATE accepted: %s", item.table)
		}
	}
}

func verifiedOVNRetirementObservation() ovnadapter.PortBindingRetirementObservation {
	return ovnadapter.PortBindingRetirementObservation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, RequestedChassisAbsent: true, SourceChassisInactive: true, SourceOVSInterfaceAbsent: true}
}
