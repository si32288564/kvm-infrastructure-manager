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
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

func TestVMAggregateOneStandardPortPostgreSQLIntegration(t *testing.T) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	db, err := OpenWithMaxConnections(ctx, url, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('vm-aggregate-standard-port',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%012x", uint64(time.Now().UnixNano())&0xffffffffffff)
	projectID := "83000000-0000-4000-8000-" + suffix
	vmID := "83000001-0000-4000-8000-" + suffix
	host, group := "vm-port-host-"+suffix, "vm-port-pool-"+suffix
	projectDigest := digestBytes([]byte("vm-port-project-" + suffix))
	if _, err = db.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,actor_issuer,actor_subject,request_id) VALUES($1,1,$2,false,'ACTIVE',$3,'integration','vm-port',$4)`, projectID, "vm-port-project-"+suffix, projectDigest, "vm-port-project-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at) VALUES($1,1,$2,false,'ACTIVE',$3,statement_timestamp())`, projectID, "vm-port-project-"+suffix, projectDigest); err != nil {
		t.Fatal(err)
	}
	prepareEvacuationHost(t, ctx, db, host)
	if err = UpsertPlacementPool(ctx, db, PlacementPoolBinding{PoolID: group, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "vm-port-placement", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = PublishHostGroupMembershipSet(ctx, db, HostGroupMembershipSetRequest{PublishRequestID: "vm-port-members-" + suffix, HostGroupID: group, SourceType: "EXPLICIT", SourceRevision: suffix, BasedOnHostGroupGeneration: 1, Members: []HostGroupMembership{{HostGroupID: group, HostID: host, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}}}); err != nil {
		t.Fatal(err)
	}
	policyID := "vm-port-policy-" + suffix
	policyDigest, err := PublishAvailabilityPolicy(ctx, db, availabilityPolicyFixture(policyID, 1, "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION", "ACTIVE"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PublishGroupPolicyBinding(ctx, db, GroupPolicyBindingRequest{PublishRequestID: "vm-port-policy-binding-" + suffix, BindingID: "vm-port-policy-binding-" + suffix, HostGroupID: group, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: policyID, PolicyRevision: 1, PolicyDigest: policyDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	scopeID := "vm-port-scope-" + suffix
	if _, err = PublishPlacementScope(ctx, db, PlacementScopePublishRequest{PublishRequestID: "vm-port-scope-publish-" + suffix, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: projectID, LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: group, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}

	network, err := CreateNetworkResource(ctx, db, NetworkResourceRequest{NetworkID: "vm-port-network-" + suffix, ProjectID: projectID, Name: "standard", Profile: "STANDARD_OVERLAY", MTU: 1450, SegmentPolicy: "AUTO"})
	if err != nil {
		t.Fatal(err)
	}
	nc, err := ClaimNetworkRealization(ctx, db, network.OperationID, "vm-port-network-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, networkPlan, _ := ovnadapter.RestoreStoredNetworkPlan(nc.CanonicalPlan, nc.PlanDigest)
	if err = AuthorizeNetworkRealizationApply(ctx, db, nc); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptNetworkRealizationObservation(ctx, db, nc, matchedNetworkObservation(nc, networkPlan, "vm-port-network-backend", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	subnet, err := CreateSubnetResource(ctx, db, SubnetResourceRequest{SubnetID: "vm-port-subnet-" + suffix, ProjectID: projectID, NetworkID: network.NetworkID, Name: "standard-v4", IPFamily: "IPV4", CIDR: "10.83.0.0/24", GatewayPolicy: "AUTO", AllocationPolicy: "RANGE", AllocationStart: "10.83.0.10", AllocationEnd: "10.83.0.30", DHCPEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := ClaimSubnetRealization(ctx, db, subnet.OperationID, "vm-port-subnet-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, subnetPlan, _ := ovnadapter.RestoreStoredSubnetPlan(sc.CanonicalPlan, sc.PlanDigest)
	if err = AuthorizeSubnetRealizationApply(ctx, db, sc); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptSubnetRealizationObservation(ctx, db, sc, matchedSubnetObservation(sc, subnetPlan, "vm-port-dhcp-backend", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	segmentID := "network-segment:" + network.NetworkID
	if err = UpsertHostNetworkMapping(ctx, db, HostNetworkMapping{HostID: host, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "chassis-" + host}); err != nil {
		t.Fatal(err)
	}
	port, err := CreatePortResource(ctx, db, PortResourceRequest{PortID: "vm-port-resource-" + suffix, ProjectID: projectID, NetworkID: network.NetworkID, Name: "eth0", MACPolicy: "AUTO", SubnetID: subnet.SubnetID, IPAllocationMode: "AUTO", AttachmentPolicy: "ON_DEMAND", DatapathProfile: "STANDARD"})
	if err != nil {
		t.Fatal(err)
	}
	pc, err := ClaimPortRealization(ctx, db, port.OperationID, "vm-port-resource-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, portPlan, _ := ovnadapter.RestoreStoredPortResourcePlan(pc.CanonicalPlan, pc.PlanDigest)
	if err = AuthorizePortRealizationApply(ctx, db, pc); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptPortRealizationObservation(ctx, db, pc, matchedPortObservation(pc, portPlan, "vm-port-lsp", "RECEIVED")); err != nil {
		t.Fatal(err)
	}

	backendID, vgUUID, classID := "vm-port-backend-"+suffix, "vm-port-vg-"+suffix, "vm-port-class-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, db, LocalLVMFoundation{BackendID: backendID, HostID: host, VGUUID: vgUUID, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: classID, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "vm-port-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	imageID, flavorID := "vm-port-image-"+suffix, "vm-port-flavor-"+suffix
	imageDigest := digestBytes([]byte("vm-port-image"))
	if _, err = RegisterImageRevision(ctx, db, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: projectID, Format: "RAW", SizeBytes: 4096, DeclaredChecksum: imageDigest, ObservedChecksum: imageDigest, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "qualification://vm-port", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterFlavorRevision(ctx, db, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: projectID, Name: "vm-port.small", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	volume, err := CreateVolumeResource(ctx, db, VolumeResourceRequest{VolumeID: "vm-port-root-" + suffix, ProjectID: projectID, Name: "root", StorageClassID: classID, StorageClassRevision: 1, SizeBytes: 16 << 20, Bootable: true, SourceType: "BLANK"})
	if err != nil {
		t.Fatal(err)
	}
	volume, err = AllocateVolumeCapacity(ctx, db, VolumeCapacityAllocationRequest{VolumeID: volume.VolumeID, BackendID: backendID, ExpectedVolumeRevision: 1, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := &volumeResourceLVMClient{vgUUID: vgUUID}
	mutation := locallvm.Backend{Client: client, VolumeGroups: map[string]string{vgUUID: "kim_test_vg"}}
	readOnly := locallvm.ReadBackBackend{Backend: mutation}
	vc, err := ClaimVolumeMaterialization(ctx, db, volume.OperationID, "vm-port-volume-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := AuthorizeVolumeMaterializationCommand(ctx, db, vc, "vm-port-volume-job-"+suffix, "vm-port-volume-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, db, apply.CommandID, 1, CommandLeaseScopeMutation, mutation)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, db, vc); err != nil {
		t.Fatal(err)
	}
	vc, err = ClaimVolumeMaterialization(ctx, db, volume.OperationID, "vm-port-volume-successor", time.Minute)
	if err != nil || vc.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("volume successor=%+v err=%v", vc, err)
	}
	read, err := AuthorizeVolumeMaterializationCommand(ctx, db, vc, "vm-port-volume-read-job-"+suffix, "vm-port-volume-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	volumeVerification := observeVolumeBackendAfterLostResponse(t, ctx, db, read.CommandID, "vm-port-volume-verification-"+suffix, 1, readOnly)
	if _, err = CompleteVolumeMaterialization(ctx, db, vc, CompleteVolumeMaterializationRequest{OperationID: vc.OperationID, OperationGeneration: vc.OperationGeneration, ClaimGeneration: vc.ClaimGeneration, ObservationID: "vm-port-volume-observation-" + suffix, VerificationID: volumeVerification}); err != nil {
		t.Fatal(err)
	}
	volume, err = GetVolumeResource(ctx, db, volume.VolumeID)
	if err != nil {
		t.Fatal(err)
	}

	create := VMAggregateCreateRequest{RequestID: "vm-port-create-request-" + suffix, OperationID: "vm-port-create-operation-" + suffix, VMID: vmID, ProjectID: projectID, Name: "standard-port-vm", FlavorID: flavorID, FlavorRevision: 1, ImageID: imageID, ImageRevision: 1, AvailabilityPolicyID: policyID, AvailabilityPolicyRevision: 1, PlacementScopeID: scopeID, PlacementScopeGeneration: 1, RootVolumeID: volume.VolumeID, RootVolumeRevision: 1, PortID: port.PortID, PortRevision: 1, DesiredPowerState: "RUNNING"}
	stale := create
	stale.RequestID += "-stale"
	stale.OperationID += "-stale"
	stale.VMID = "83000002-0000-4000-8000-" + suffix
	stale.PortRevision = 2
	if _, err = CreateVMAggregate(ctx, db, stale); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("stale Port revision accepted: %v", err)
	}
	aggregate, err := CreateVMAggregate(ctx, db, create)
	if err != nil || aggregate.PortID != port.PortID || aggregate.PortRevision != 1 {
		t.Fatalf("aggregate=%+v err=%v", aggregate, err)
	}
	var desiredHasPhysical bool
	if err = db.QueryRow(ctx, `SELECT dependency_payload::text ~ '"(host_id|binding_generation|backend_uuid|ovn_chassis_name|ovs_uuid)"' FROM kim.vm_dependency_snapshot_evidence WHERE dependency_snapshot_id=$1`, aggregate.DependencySnapshotID).Scan(&desiredHasPhysical); err != nil || desiredHasPhysical {
		t.Fatalf("physical desired leakage=%v err=%v", desiredHasPhysical, err)
	}
	claim, err := ClaimVMAggregateLifecycle(ctx, db, aggregate.OperationID, "vm-port-placement-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	placementRequest, err := CompileVMAggregatePlacement(ctx, db, claim)
	if err != nil || len(placementRequest.Network) != 1 || placementRequest.Network[0].PortID != port.PortID || placementRequest.Network[0].AttachmentIntentID == "" || placementRequest.Network[0].IPAddress != port.IPAddress || placementRequest.Network[0].MACAddress != port.MACAddress {
		t.Fatalf("placement=%+v err=%v", placementRequest, err)
	}
	dry, err := DryEvaluateAvailabilityPlacementScope(ctx, db, placementRequest)
	if err != nil || dry.Status != "READY" || len(dry.Candidates) != 1 {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	admission, err := FinalAdmitAvailabilityPlacementScope(ctx, db, dry, placementRequest, dry.Candidates[0])
	if err != nil || admission.HostID != host {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	bound, err := GetPortResource(ctx, db, port.PortID)
	if err != nil || bound.RealizationState != "PENDING" || bound.RealizationGeneration != 2 {
		t.Fatalf("bound Port=%+v err=%v", bound, err)
	}
	boundClaim, err := ClaimPortRealization(ctx, db, bound.OperationID, "vm-port-bound-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, boundPlan, _ := ovnadapter.RestoreStoredPortResourcePlan(boundClaim.CanonicalPlan, boundClaim.PlanDigest)
	if err = AuthorizePortRealizationApply(ctx, db, boundClaim); err != nil {
		t.Fatal(err)
	}
	if _, err = AcceptPortRealizationObservation(ctx, db, boundClaim, matchedPortObservation(boundClaim, boundPlan, "vm-port-bound-lsp", "RECEIVED")); err != nil {
		t.Fatal(err)
	}
	if _, err = BindVMAggregateAdmission(ctx, db, claim, admission.AdmissionID); err != nil {
		t.Fatal(err)
	}

	claim, err = ClaimVMAggregateLifecycle(ctx, db, aggregate.OperationID, "vm-port-materialization-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := PrepareVMAggregateMaterialization(ctx, db, claim)
	if err != nil {
		t.Fatal(err)
	}
	defineCommand := "vm-define-command:" + aggregate.OperationID + ":1"
	defineVerification := "vm-port-define-verification-" + suffix
	defineAttempt := acceptEvacuationCommand(t, ctx, db, host, defineCommand, defineVerification, 1, map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "plan_digest": decision.PlanDigest, "domain_present": true, "domain_identity_matches": true, "plan_identity_matches": true, "compute_shape_matches": true, "root_volume_identity_matches": true, "image_materialization_state": "PENDING", "network_realization_state": "PENDING"}, "SUCCEEDED")
	if err = AcceptVMDefinitionObservation(ctx, db, VMDefinitionObservation{EvidenceID: "vm-port-define-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: decision.PlanID, PlanDigest: decision.PlanDigest, HostID: host, CommandID: defineCommand, AttemptIndex: uint32(defineAttempt), VerificationID: defineVerification, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(defineCommand + "/observation")), VerifierDigest: digestBytes([]byte(defineCommand + "/verifier")), EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	imageRequest := VMImageMaterializationRequest{VMID: vmID, PlanID: decision.PlanID, JobID: "vm-port-image-job-" + suffix, CommandID: "vm-port-image-command-" + suffix}
	if _, err = PrepareVMImageMaterialization(ctx, db, imageRequest); err != nil {
		t.Fatal(err)
	}
	var resourceKey string
	if err = db.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, volume.BindingID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	imageVerification := "vm-port-image-verification-" + suffix
	imageAttempt := acceptEvacuationCommand(t, ctx, db, host, imageRequest.CommandID, imageVerification, 1, map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "image_id": imageID, "image_revision": float64(1), "expected_content_digest": imageDigest, "observed_content_digest": imageDigest, "image_size_bytes": float64(4096), "volume_id": volume.VolumeID, "observed_vg_uuid": vgUUID, "observed_lv_uuid": volume.LVUUID, "backend_resource_key": resourceKey, "holder_open": false, "content_identity_matches": true}, "SUCCEEDED")
	if err = AcceptVMImageRealizationObservation(ctx, db, VMImageRealizationObservation{EvidenceID: "vm-port-image-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: decision.PlanID, PlanDigest: decision.PlanDigest, HostID: host, ImageID: imageID, ImageRevision: 1, ExpectedDigest: imageDigest, ObservedDigest: imageDigest, ImageSizeBytes: 4096, VolumeID: volume.VolumeID, BindingID: volume.BindingID, BindingGeneration: volume.BindingGeneration, VGUUID: vgUUID, LVUUID: volume.LVUUID, BackendResourceKey: resourceKey, CommandID: imageRequest.CommandID, AttemptIndex: uint32(imageAttempt), VerificationID: imageVerification, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(imageRequest.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(imageRequest.CommandID + "/verifier")), EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}

	claim, err = ClaimVMAggregateLifecycle(ctx, db, aggregate.OperationID, "vm-port-verification-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EvaluateVMAggregateEvidence(ctx, db, claim, "vm-port-forged-network-verification-"+suffix); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("missing OVS observation accepted: %v", err)
	}
	powerCommand := "vm-port-power-command-" + suffix
	realizeEvacuationOVSPort(t, ctx, db, host, vmID, decision.PlanID, port.PortID, network.NetworkID, segmentID, port.MACAddress, "aggregate-"+suffix, "vm-port-power-job-"+suffix, powerCommand, 1, 1, 1)
	acceptEvacuationLostReadBack(t, ctx, db, host, powerCommand, "vm-port-power-verification-"+suffix, 1, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"})
	verificationID := "vm-port-verification-" + suffix
	verification, err := EvaluateVMAggregateEvidence(ctx, db, claim, verificationID)
	if err != nil || verification.VerificationState != "VERIFIED" {
		t.Fatalf("verification=%+v err=%v", verification, err)
	}
	terminalID := "vm-port-terminal-" + suffix
	driftRollback := errors.New("rollback Port binding drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_generation=binding_generation+1 WHERE port_id=$1`, port.PortID); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateLifecycle(ctx, scopeTxBeginner{tx}, claim, verificationID, terminalID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("terminal Port drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	if _, err = CompleteVMAggregateLifecycle(ctx, db, claim, verificationID, terminalID); err != nil {
		t.Fatal(err)
	}
	aggregate, err = GetVMAggregate(ctx, db, vmID)
	if err != nil || aggregate.LifecycleState != "ACTIVE" || aggregate.ConvergenceState != "CONVERGED" {
		t.Fatalf("terminal aggregate=%+v err=%v", aggregate, err)
	}
	for _, table := range []string{"vm_aggregate_port_binding_evidence", "vm_aggregate_network_port_verification_evidence"} {
		if _, err = db.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
}
