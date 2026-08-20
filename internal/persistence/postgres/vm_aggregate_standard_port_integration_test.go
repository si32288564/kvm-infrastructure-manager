package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

func TestVMAggregateOneStandardPortPostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 1, false, false)
}

func TestVMAggregateOneStandardPortDeletePostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 1, false, true)
}

func TestVMAggregateOneStandardPortDataVolumeDeletePostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 1, true, true)
}

func TestVMAggregateOneStandardPortDataVolumeEvacuationPostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 1, true, false)
}

func TestVMAggregateMultiStandardPortPostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 2, false, false)
}

func TestVMAggregateMultiStandardPortDeletePostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 2, false, true)
}

func TestVMAggregateMultiStandardPortDataVolumeDeletePostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 2, true, true)
}

func TestVMAggregateMaximumProfileEvacuationPostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 2, true, false)
}

func TestVMAggregateMaximumProfileRecoveryPostgreSQLIntegration(t *testing.T) {
	testVMAggregateStandardPortsPostgreSQLIntegration(t, 2, true, false, "RECOVERY")
}

func testVMAggregateStandardPortsPostgreSQLIntegration(t *testing.T, portCount int, withData, deleteAfterCreate bool, profiles ...string) {
	maximumRecovery := len(profiles) > 0 && profiles[0] == "RECOVERY"
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
	host, destinationHost, evacuationHost, group := "vm-port-host-"+suffix, "vm-port-recovery-"+suffix, "vm-port-evacuation-"+suffix, "vm-port-pool-"+suffix
	projectDigest := digestBytes([]byte("vm-port-project-" + suffix))
	if _, err = db.Exec(ctx, `INSERT INTO kim.project_revision_evidence(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,actor_issuer,actor_subject,request_id) VALUES($1,1,$2,false,'ACTIVE',$3,'integration','vm-port',$4)`, projectID, "vm-port-project-"+suffix, projectDigest, "vm-port-project-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.projects_current(project_id,project_revision,project_name,delete_protection,lifecycle_state,desired_digest,created_at) VALUES($1,1,$2,false,'ACTIVE',$3,statement_timestamp())`, projectID, "vm-port-project-"+suffix, projectDigest); err != nil {
		t.Fatal(err)
	}
	prepareEvacuationHost(t, ctx, db, host)
	prepareEvacuationHost(t, ctx, db, destinationHost)
	prepareEvacuationHost(t, ctx, db, evacuationHost)
	if err = UpsertPlacementPool(ctx, db, PlacementPoolBinding{PoolID: group, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "vm-port-placement", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = PublishHostGroupMembershipSet(ctx, db, HostGroupMembershipSetRequest{PublishRequestID: "vm-port-members-" + suffix, HostGroupID: group, SourceType: "EXPLICIT", SourceRevision: suffix, BasedOnHostGroupGeneration: 1, Members: []HostGroupMembership{{HostGroupID: group, HostID: host, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}, {HostGroupID: group, HostID: destinationHost, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}, {HostGroupID: group, HostID: evacuationHost, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO kim.host_placement_pool_memberships_current(host_id,pool_id,membership_generation,membership_state) VALUES($1,$4,1,'ACTIVE'),($2,$4,1,'ACTIVE'),($3,$4,1,'ACTIVE')`, host, destinationHost, evacuationHost, group); err != nil {
		t.Fatal(err)
	}
	confirmation, err := PublishFailureConfirmationPolicy(ctx, db, FailureConfirmationPolicy{PolicyID: "vm-port-confirmation-" + suffix, PolicyRevision: 1, ApplicableFailureClass: "VM_RUNTIME_UNAVAILABLE", ConfirmationMode: "ALL_REQUIRED_EVIDENCE", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification", Requirements: []FailureConfirmationRequirement{{Ordinal: 1, EvidenceType: "VM_RUNTIME_OBSERVATION", ObservedState: "PRESENT", FreshnessState: "CURRENT", SourceType: "LIBVIRT_READ_BACK"}}})
	if err != nil {
		t.Fatal(err)
	}
	fencing, err := PublishFailureFencingPolicy(ctx, db, FailureFencingPolicy{PolicyID: "vm-port-fencing-" + suffix, PolicyRevision: 1, FencingMode: "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	storagePolicy, err := PublishStorageSafetyPolicy(ctx, db, StorageSafetyPolicy{PolicyID: "vm-port-storage-policy-" + suffix, PolicyRevision: 1, StorageClass: "LOCAL_LVM", SafetyMode: "SOURCE_ROOT_QUIESCED_DATA_DETACHED", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	budget, err := PublishRecoveryBudgetPolicy(ctx, db, RecoveryBudgetPolicy{PolicyID: "vm-port-budget-" + suffix, PolicyRevision: 1, ScopeType: "GLOBAL", Phase: "PLANNING", MaxActiveRecoveries: 1, LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	policyID := "vm-port-policy-" + suffix
	policy := availabilityPolicyFixture(policyID, 1, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE")
	policy.FailureConfirmationPolicyID, policy.FailureConfirmationPolicyRevision, policy.FailureConfirmationPolicyDigest = confirmation.PolicyID, 1, confirmation.PolicyDigest
	policy.FencingPolicyID, policy.FencingPolicyRevision, policy.FencingPolicyDigest = fencing.PolicyID, 1, fencing.PolicyDigest
	policy.StorageSafetyPolicyID, policy.StorageSafetyPolicyRevision, policy.StorageSafetyPolicyDigest = storagePolicy.PolicyID, 1, storagePolicy.PolicyDigest
	policy.RecoveryBudgetPolicyID, policy.RecoveryBudgetPolicyRevision, policy.RecoveryBudgetPolicyDigest = budget.PolicyID, 1, budget.PolicyDigest
	policyDigest, err := PublishAvailabilityPolicy(ctx, db, policy)
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
	if err = UpsertHostNetworkMapping(ctx, db, HostNetworkMapping{HostID: destinationHost, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "chassis-" + destinationHost}); err != nil {
		t.Fatal(err)
	}
	if err = UpsertHostNetworkMapping(ctx, db, HostNetworkMapping{HostID: evacuationHost, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "chassis-" + evacuationHost}); err != nil {
		t.Fatal(err)
	}
	ports := make([]PortResource, 0, portCount)
	for i := 0; i < portCount; i++ {
		port, err := CreatePortResource(ctx, db, PortResourceRequest{PortID: fmt.Sprintf("vm-port-resource-%d-%s", i, suffix), ProjectID: projectID, NetworkID: network.NetworkID, Name: fmt.Sprintf("eth%d", i), MACPolicy: "AUTO", SubnetID: subnet.SubnetID, IPAllocationMode: "AUTO", AttachmentPolicy: "ON_DEMAND", DatapathProfile: "STANDARD"})
		if err != nil {
			t.Fatal(err)
		}
		pc, err := ClaimPortRealization(ctx, db, port.OperationID, fmt.Sprintf("vm-port-resource-worker-%d", i), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, portPlan, _ := ovnadapter.RestoreStoredPortResourcePlan(pc.CanonicalPlan, pc.PlanDigest)
		if err = AuthorizePortRealizationApply(ctx, db, pc); err != nil {
			t.Fatal(err)
		}
		if _, err = AcceptPortRealizationObservation(ctx, db, pc, matchedPortObservation(pc, portPlan, fmt.Sprintf("vm-port-lsp-%d", i), "RECEIVED")); err != nil {
			t.Fatal(err)
		}
		ports = append(ports, port)
	}
	port := ports[0]

	backendID, vgUUID, classID := "vm-port-backend-"+suffix, "vm-port-vg-"+suffix, "vm-port-class-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, db, LocalLVMFoundation{BackendID: backendID, HostID: host, VGUUID: vgUUID, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: classID, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "vm-port-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	destinationBackend, destinationVG := "vm-port-destination-backend-"+suffix, "vm-port-destination-vg-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, db, LocalLVMFoundation{BackendID: destinationBackend, HostID: destinationHost, VGUUID: destinationVG, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: classID, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "vm-port-destination-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	evacuationBackend, evacuationVG := "vm-port-evacuation-backend-"+suffix, "vm-port-evacuation-vg-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, db, LocalLVMFoundation{BackendID: evacuationBackend, HostID: evacuationHost, VGUUID: evacuationVG, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: classID, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "vm-port-evacuation-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
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
	var dataVolume VolumeResource
	if withData {
		dataVolume = materializeVMAggregateStandardPortDataVolume(t, ctx, db, suffix, projectID, classID, backendID, vgUUID)
	}

	create := VMAggregateCreateRequest{RequestID: "vm-port-create-request-" + suffix, OperationID: "vm-port-create-operation-" + suffix, VMID: vmID, ProjectID: projectID, Name: "standard-port-vm", FlavorID: flavorID, FlavorRevision: 1, ImageID: imageID, ImageRevision: 1, AvailabilityPolicyID: policyID, AvailabilityPolicyRevision: 1, PlacementScopeID: scopeID, PlacementScopeGeneration: 1, RootVolumeID: volume.VolumeID, RootVolumeRevision: 1, DesiredPowerState: "RUNNING"}
	if withData {
		create.DataVolumes = []VMAggregateVolumeRequest{{VolumeID: dataVolume.VolumeID, VolumeRevision: 1}}
	}
	if portCount == 1 {
		create.PortID, create.PortRevision = port.PortID, 1 // legacy one-Port compatibility
	} else {
		// Reverse caller order; the producer must canonicalize by logical Port ID.
		for i := len(ports) - 1; i >= 0; i-- {
			create.Ports = append(create.Ports, VMAggregatePortRequest{PortID: ports[i].PortID, PortRevision: 1})
		}
	}
	stale := create
	stale.Ports = append([]VMAggregatePortRequest(nil), create.Ports...)
	stale.RequestID += "-stale"
	stale.OperationID += "-stale"
	stale.VMID = "83000002-0000-4000-8000-" + suffix
	if portCount == 1 {
		stale.PortRevision = 2
	} else {
		stale.Ports[0].PortRevision = 2
	}
	if _, err = CreateVMAggregate(ctx, db, stale); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("stale Port revision accepted: %v", err)
	}
	if portCount > 1 {
		oversized := create
		oversized.RequestID += "-oversized"
		oversized.OperationID += "-oversized"
		oversized.VMID = "83000004-0000-4000-8000-" + suffix
		oversized.Ports = append(append([]VMAggregatePortRequest(nil), create.Ports...), create.Ports[0])
		if _, err = CreateVMAggregate(ctx, db, oversized); !errors.Is(err, ErrVMAggregateConflict) {
			t.Fatalf("unqualified Port cardinality accepted: %v", err)
		}

		duplicate := create
		duplicate.Ports = append([]VMAggregatePortRequest(nil), create.Ports...)
		duplicate.RequestID += "-duplicate"
		duplicate.OperationID += "-duplicate"
		duplicate.VMID = "83000003-0000-4000-8000-" + suffix
		duplicate.Ports[1] = duplicate.Ports[0]
		if _, err = CreateVMAggregate(ctx, db, duplicate); !errors.Is(err, ErrVMAggregateConflict) {
			t.Fatalf("duplicate Port accepted: %v", err)
		}
	}
	aggregate, err := CreateVMAggregate(ctx, db, create)
	if err != nil || len(aggregate.Ports) != portCount || aggregate.PortID != ports[0].PortID || aggregate.PortRevision != 1 {
		t.Fatalf("aggregate=%+v err=%v", aggregate, err)
	}
	for i := range aggregate.Ports {
		if aggregate.Ports[i].PortID != ports[i].PortID {
			t.Fatalf("non-canonical Ports=%+v", aggregate.Ports)
		}
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
	expectedStorage := 1
	if withData {
		expectedStorage = 2
	}
	if err != nil || len(placementRequest.Network) != portCount || len(placementRequest.Storage) != expectedStorage {
		t.Fatalf("placement=%+v err=%v", placementRequest, err)
	}
	for i := range ports {
		if placementRequest.Network[i].PortID != ports[i].PortID || placementRequest.Network[i].AttachmentIntentID == "" || placementRequest.Network[i].IPAddress != ports[i].IPAddress || placementRequest.Network[i].MACAddress != ports[i].MACAddress {
			t.Fatalf("placement[%d]=%+v", i, placementRequest.Network[i])
		}
	}
	dry, err := DryEvaluateAvailabilityPlacementScope(ctx, db, placementRequest)
	if err != nil || dry.Status != "READY" || len(dry.Candidates) != 3 {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	eligible := -1
	for i := range dry.Candidates {
		if dry.Candidates[i].Placement.Eligible {
			if eligible != -1 {
				t.Fatal("multiple initial aggregate candidates unexpectedly eligible")
			}
			eligible = i
		}
	}
	if eligible == -1 {
		t.Fatal("no initial aggregate candidate eligible")
	}
	admission, err := FinalAdmitAvailabilityPlacementScope(ctx, db, dry, placementRequest, dry.Candidates[eligible])
	if err != nil || admission.HostID != host {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	for i := range ports {
		bound, err := GetPortResource(ctx, db, ports[i].PortID)
		if err != nil || bound.RealizationState != "PENDING" || bound.RealizationGeneration != 2 {
			t.Fatalf("bound Port[%d]=%+v err=%v", i, bound, err)
		}
		boundClaim, err := ClaimPortRealization(ctx, db, bound.OperationID, fmt.Sprintf("vm-port-bound-worker-%d", i), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, boundPlan, _ := ovnadapter.RestoreStoredPortResourcePlan(boundClaim.CanonicalPlan, boundClaim.PlanDigest)
		if err = AuthorizePortRealizationApply(ctx, db, boundClaim); err != nil {
			t.Fatal(err)
		}
		if _, err = AcceptPortRealizationObservation(ctx, db, boundClaim, matchedPortObservation(boundClaim, boundPlan, fmt.Sprintf("vm-port-bound-lsp-%d", i), "RECEIVED")); err != nil {
			t.Fatal(err)
		}
		// Mobility and delete qualification need the ordinary OVN runtime
		// incarnation so every Port can traverse exact retirement authority.
		completeEvacuationOVNIntent(t, ctx, db, fmt.Sprintf("vm-port-runtime-intent-%d-%s", i, suffix), ports[i].PortID, fmt.Sprintf("vm-port-runtime-worker-%d-%s", i, suffix), fmt.Sprintf("aggregate-%d-%s", i, suffix), 1)
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
	for i := range ports {
		realizeEvacuationOVSPort(t, ctx, db, host, vmID, decision.PlanID, ports[i].PortID, network.NetworkID, segmentID, ports[i].MACAddress, fmt.Sprintf("aggregate-%d-%s", i, suffix), "vm-port-power-job-"+suffix, powerCommand, 1, 1, 1)
	}
	acceptEvacuationLostReadBack(t, ctx, db, host, powerCommand, "vm-port-power-verification-"+suffix, 1, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"})
	if portCount > 1 {
		partialRollback := errors.New("rollback partial Port evidence")
		err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM kim.vm_network_port_realizations_current WHERE vm_id=$1 AND port_id=$2`, vmID, ports[1].PortID); err != nil {
				return err
			}
			if _, err := EvaluateVMAggregateEvidence(ctx, scopeTxBeginner{tx}, claim, "vm-port-partial-network-verification-"+suffix); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("partial Port evidence accepted: %v", err)
			}
			return partialRollback
		})
		if !errors.Is(err, partialRollback) {
			t.Fatal(err)
		}
	}
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
	var bindingCount, verificationCount int
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.vm_aggregate_port_binding_evidence WHERE operation_id=$1),(SELECT count(*) FROM kim.vm_aggregate_network_port_verification_evidence WHERE verification_id=$2)`, aggregate.OperationID, verificationID).Scan(&bindingCount, &verificationCount); err != nil || bindingCount != portCount || verificationCount != portCount {
		t.Fatalf("Port evidence cardinality binding=%d verification=%d err=%v", bindingCount, verificationCount, err)
	}
	if portCount > 1 {
		updates := map[string]string{"vm_dependency_port_evidence": "desired_digest=desired_digest", "vm_aggregate_port_binding_evidence": "binding_digest=binding_digest", "vm_aggregate_network_port_verification_evidence": "verification_digest=verification_digest"}
		for table, assignment := range updates {
			if _, err = db.Exec(ctx, fmt.Sprintf(`UPDATE kim.%s SET %s WHERE true`, table, assignment)); err == nil {
				t.Fatalf("immutable %s accepted UPDATE", table)
			}
		}
	}
	if deleteAfterCreate {
		if portCount == 2 {
			digests := make([]string, 2)
			for i := range ports {
				if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.network_ports_current WHERE port_id=$1`, ports[i].PortID).Scan(&digests[i]); err != nil {
					t.Fatal(err)
				}
			}
			var qualifiedData *VolumeResource
			if withData {
				qualifiedData = &dataVolume
			}
			qualifyVMAggregateTwoStandardPortDelete(t, ctx, db, suffix, vmID, ports, digests, qualifiedData)
		} else if withData {
			var deletePortDigest string
			if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.network_ports_current WHERE port_id=$1`, port.PortID).Scan(&deletePortDigest); err != nil {
				t.Fatal(err)
			}
			qualifyVMAggregateOneStandardPortDataVolumeDelete(t, ctx, db, suffix, vmID, port.PortID, deletePortDigest, volume, dataVolume)
		} else {
			var deletePortDigest string
			if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.network_ports_current WHERE port_id=$1`, port.PortID).Scan(&deletePortDigest); err != nil {
				t.Fatal(err)
			}
			qualifyVMAggregateOneStandardPortDelete(t, ctx, db, suffix, vmID, port.PortID, deletePortDigest)
		}
		return
	}
	if withData && !maximumRecovery {
		logicalRevision, logicalRuntime, logicalDependency, logicalDesired := aggregate.VMRevision, aggregate.RuntimeIntentGeneration, aggregate.DependencyDigest, aggregate.DesiredDigest
		var failureEpochsBefore, fencingProofsBefore int
		if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&failureEpochsBefore, &fencingProofsBefore); err != nil {
			t.Fatal(err)
		}
		var logicalRootDigest, logicalDataDigest string
		if err = db.QueryRow(ctx, `SELECT (SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$1),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$2)`, volume.VolumeID, dataVolume.VolumeID).Scan(&logicalRootDigest, &logicalDataDigest); err != nil {
			t.Fatal(err)
		}
		logicalPortDigests := make([]string, len(ports))
		for i := range ports {
			if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.network_ports_current WHERE port_id=$1`, ports[i].PortID).Scan(&logicalPortDigests[i]); err != nil {
				t.Fatal(err)
			}
			convergeEvacuationOVSDataplane(t, ctx, db, host, vmID, decision.PlanID, ports[i].PortID, network.NetworkID, segmentID, ports[i].MACAddress, fmt.Sprintf("aggregate-%d-%s", i, suffix), 1, 1, 1)
		}
		source := repeatedEvacuationIncarnation{Host: host, Admission: admission.AdmissionID, Plan: decision.PlanID, Volume: volume.VolumeID, Attachment: placementRequest.Storage[0].AttachmentID, Binding: volume.BindingID, LV: volume.LVUUID, Backend: backendID, VG: vgUUID, Materialization: 1, PortGeneration: 1, BindingGeneration: 1, Data: &repeatedEvacuationVolume{Volume: dataVolume.VolumeID, Attachment: placementRequest.Storage[1].AttachmentID, Binding: dataVolume.BindingID, LV: dataVolume.LVUUID, Backend: backendID, VG: vgUUID, AttachmentGeneration: 1, BindingGeneration: dataVolume.BindingGeneration}}
		label := "aggregate-root-data"
		var additionalPorts []repeatedEvacuationPort
		if portCount == 2 {
			label = "aggregate-maximum-profile"
			additionalPorts = append(additionalPorts, repeatedEvacuationPort{PortID: ports[1].PortID, NetworkID: network.NetworkID, SegmentID: segmentID, MAC: ports[1].MACAddress, SourcePortGeneration: 1, SourceBindingGeneration: 1, RetirementIntentGeneration: 2, DestinationIntentGeneration: 3})
		}
		move := executeRepeatedEvacuationMove(t, ctx, db, suffix, label, vmID, imageID, imageDigest, network.NetworkID, segmentID, ports[0].PortID, ports[0].MACAddress, source, destinationHost, destinationBackend, destinationVG, 1, 1, 2, 3, 2, 3, nil, nil, additionalPorts...)
		associationRequest := VMAggregateMobilityAssociationRequest{AssociationID: "vm-" + label + "-evacuation-association-" + suffix, VMID: vmID, MobilityKind: "HOST_EVACUATION", MobilityTerminalEvidenceID: move.ParentTerminal}
		if portCount == 2 {
			partialRollback := errors.New("rollback maximum-profile partial destination Port set")
			err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `DELETE FROM kim.vm_network_port_realizations_current WHERE vm_id=$1 AND port_id=$2`, vmID, ports[1].PortID); err != nil {
					return err
				}
				if _, err := AssociateVMAggregateMobility(ctx, scopeTxBeginner{tx}, VMAggregateMobilityAssociationRequest{AssociationID: associationRequest.AssociationID + "-partial-port", VMID: vmID, MobilityKind: associationRequest.MobilityKind, MobilityTerminalEvidenceID: associationRequest.MobilityTerminalEvidenceID}); !errors.Is(err, ErrVMAggregateConflict) {
					return fmt.Errorf("maximum-profile partial destination Port set accepted: %v", err)
				}
				return partialRollback
			})
			if !errors.Is(err, partialRollback) {
				t.Fatal(err)
			}
		}
		driftRollback := errors.New("rollback destination DATA binding drift")
		err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1`, move.Destination.Data.Binding); err != nil {
				return err
			}
			if _, err := AssociateVMAggregateMobility(ctx, scopeTxBeginner{tx}, VMAggregateMobilityAssociationRequest{AssociationID: associationRequest.AssociationID + "-drift", VMID: vmID, MobilityKind: associationRequest.MobilityKind, MobilityTerminalEvidenceID: associationRequest.MobilityTerminalEvidenceID}); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("destination DATA binding drift accepted: %v", err)
			}
			return driftRollback
		})
		if !errors.Is(err, driftRollback) {
			t.Fatal(err)
		}
		association, err := AssociateVMAggregateMobility(ctx, db, associationRequest)
		if err != nil || association.AssociationGeneration != 1 || association.SourceHostID != host || association.DestinationHostID != destinationHost || association.SourcePlanID != decision.PlanID || association.DestinationPlanID != move.Destination.Plan || association.DependencyDigest != logicalDependency || association.DesiredDigest != logicalDesired {
			t.Fatalf("ROOT+DATA EVACUATE association=%+v err=%v", association, err)
		}
		if replay, err := AssociateVMAggregateMobility(ctx, db, associationRequest); err != nil || replay.AssociationDigest != association.AssociationDigest {
			t.Fatalf("ROOT+DATA EVACUATE replay=%+v err=%v", replay, err)
		}
		var currentRevision, currentRuntime, currentAssociationGeneration uint64
		var currentDependency, currentDesired, currentHost, currentAdmission, currentPlan, currentAssociation string
		if err = db.QueryRow(ctx, `SELECT r.vm_revision,r.runtime_intent_generation,s.dependency_digest,r.desired_digest,b.host_id,b.admission_id,b.plan_id,b.mobility_association_generation,b.mobility_association_id FROM kim.vm_resources_current r JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.runtime_intent_generation)=(r.vm_id,r.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=r.vm_id WHERE r.vm_id=$1`, vmID).Scan(&currentRevision, &currentRuntime, &currentDependency, &currentDesired, &currentHost, &currentAdmission, &currentPlan, &currentAssociationGeneration, &currentAssociation); err != nil || currentRevision != logicalRevision || currentRuntime != logicalRuntime || currentDependency != logicalDependency || currentDesired != logicalDesired || currentHost != destinationHost || currentAdmission != move.Destination.Admission || currentPlan != move.Destination.Plan || currentAssociationGeneration != 1 || currentAssociation != association.AssociationID {
			t.Fatalf("ROOT+DATA post-mobility logical/runtime=%d/%d %s/%s physical=%s/%s/%s association=%d/%s err=%v", currentRevision, currentRuntime, currentDependency, currentDesired, currentHost, currentAdmission, currentPlan, currentAssociationGeneration, currentAssociation, err)
		}
		var currentRootDigest, currentDataDigest string
		var associatedPorts, associatedVolumes, relocationVolumes int
		if err = db.QueryRow(ctx, `SELECT (SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$2),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$3),(SELECT port_count FROM kim.vm_aggregate_mobility_association_evidence WHERE association_id=$1),(SELECT volume_count FROM kim.vm_aggregate_mobility_association_evidence WHERE association_id=$1),(SELECT volume_count FROM kim.vm_materialization_relocation_authority_evidence WHERE relocation_authority_id=$4)`, association.AssociationID, volume.VolumeID, dataVolume.VolumeID, move.Relocation).Scan(&currentRootDigest, &currentDataDigest, &associatedPorts, &associatedVolumes, &relocationVolumes); err != nil || currentRootDigest != logicalRootDigest || currentDataDigest != logicalDataDigest || associatedPorts != portCount || associatedVolumes != 2 || relocationVolumes != 2 {
			t.Fatalf("ROOT+DATA logical digests=%s/%s cardinality Ports=%d Volumes=%d/%d err=%v", currentRootDigest, currentDataDigest, associatedPorts, associatedVolumes, relocationVolumes, err)
		}
		for i := range ports {
			var currentDigest string
			var portGeneration, bindingGeneration uint64
			if err = db.QueryRow(ctx, `SELECT p.desired_digest,p.port_generation,b.binding_generation FROM kim.network_ports_current p JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=p.placement_admission_id WHERE p.port_id=$1`, ports[i].PortID).Scan(&currentDigest, &portGeneration, &bindingGeneration); err != nil || currentDigest != logicalPortDigests[i] || portGeneration != 2 || bindingGeneration != 2 {
				t.Fatalf("ROOT+DATA current Port[%d] digest=%s generation=%d/%d err=%v", i, currentDigest, portGeneration, bindingGeneration, err)
			}
		}
		var releasedSourceCapacity int
		if err = db.QueryRow(ctx, `SELECT count(*) FROM kim.storage_capacity_claims WHERE volume_id=ANY($1) AND claim_state IN ('RELEASE_PENDING','RELEASED')`, []string{volume.VolumeID, dataVolume.VolumeID}).Scan(&releasedSourceCapacity); err != nil || releasedSourceCapacity != 0 {
			t.Fatalf("source capacity reclaimed during relocation count=%d err=%v", releasedSourceCapacity, err)
		}
		var failureEpochsAfter, fencingProofsAfter int
		if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&failureEpochsAfter, &fencingProofsAfter); err != nil || failureEpochsAfter != failureEpochsBefore || fencingProofsAfter != fencingProofsBefore {
			t.Fatalf("planned EVACUATE generated Recovery authority epochs=%d→%d fencing=%d→%d err=%v", failureEpochsBefore, failureEpochsAfter, fencingProofsBefore, fencingProofsAfter, err)
		}
		for table, assignment := range map[string]string{
			"host_evacuation_source_storage_volume_safety_evidence": "safety_member_digest=safety_member_digest",
			"host_evacuation_source_storage_safety_set_evidence":    "member_set_digest=member_set_digest",
			"vm_materialization_relocation_volume_evidence":         "member_digest=member_digest",
		} {
			if _, err = db.Exec(ctx, fmt.Sprintf(`UPDATE kim.%s SET %s WHERE true`, table, assignment)); err == nil {
				t.Fatalf("immutable %s accepted UPDATE", table)
			}
		}
		return
	}
	if portCount == 2 && !maximumRecovery {
		logicalRevision, logicalRuntime, logicalDependency, logicalDesired := aggregate.VMRevision, aggregate.RuntimeIntentGeneration, aggregate.DependencyDigest, aggregate.DesiredDigest
		logicalPortDigests := make([]string, len(ports))
		for i := range ports {
			if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.network_ports_current WHERE port_id=$1`, ports[i].PortID).Scan(&logicalPortDigests[i]); err != nil {
				t.Fatal(err)
			}
			convergeEvacuationOVSDataplane(t, ctx, db, host, vmID, decision.PlanID, ports[i].PortID, network.NetworkID, segmentID, ports[i].MACAddress, fmt.Sprintf("aggregate-%d-%s", i, suffix), 1, 1, 1)
		}
		source := repeatedEvacuationIncarnation{Host: host, Admission: admission.AdmissionID, Plan: decision.PlanID, Volume: volume.VolumeID, Attachment: placementRequest.Storage[0].AttachmentID, Binding: volume.BindingID, LV: volume.LVUUID, Backend: backendID, VG: vgUUID, Materialization: 1, PortGeneration: 1, BindingGeneration: 1}
		move := executeRepeatedEvacuationMove(t, ctx, db, suffix, "aggregate-two-port", vmID, imageID, imageDigest, network.NetworkID, segmentID, ports[0].PortID, ports[0].MACAddress, source, destinationHost, destinationBackend, destinationVG, 1, 1, 2, 3, 2, 3, nil, nil, repeatedEvacuationPort{PortID: ports[1].PortID, NetworkID: network.NetworkID, SegmentID: segmentID, MAC: ports[1].MACAddress, SourcePortGeneration: 1, SourceBindingGeneration: 1, RetirementIntentGeneration: 2, DestinationIntentGeneration: 3})
		associationRequest := VMAggregateMobilityAssociationRequest{AssociationID: "vm-two-port-evacuation-association-" + suffix, VMID: vmID, MobilityKind: "HOST_EVACUATION", MobilityTerminalEvidenceID: move.ParentTerminal}
		partialRollback := errors.New("rollback partial destination Port set")
		err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM kim.vm_network_port_realizations_current WHERE vm_id=$1 AND port_id=$2`, vmID, ports[1].PortID); err != nil {
				return err
			}
			if _, err := AssociateVMAggregateMobility(ctx, scopeTxBeginner{tx}, VMAggregateMobilityAssociationRequest{AssociationID: associationRequest.AssociationID + "-partial", VMID: vmID, MobilityKind: associationRequest.MobilityKind, MobilityTerminalEvidenceID: associationRequest.MobilityTerminalEvidenceID}); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("partial destination Port set accepted: %v", err)
			}
			return partialRollback
		})
		if !errors.Is(err, partialRollback) {
			t.Fatal(err)
		}
		bindingDriftRollback := errors.New("rollback destination Port binding drift")
		err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_generation=binding_generation+1 WHERE port_id=$1`, ports[1].PortID); err != nil {
				return err
			}
			if _, err := AssociateVMAggregateMobility(ctx, scopeTxBeginner{tx}, VMAggregateMobilityAssociationRequest{AssociationID: associationRequest.AssociationID + "-drift", VMID: vmID, MobilityKind: associationRequest.MobilityKind, MobilityTerminalEvidenceID: associationRequest.MobilityTerminalEvidenceID}); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("destination Port binding drift accepted: %v", err)
			}
			return bindingDriftRollback
		})
		if !errors.Is(err, bindingDriftRollback) {
			t.Fatal(err)
		}
		association, err := AssociateVMAggregateMobility(ctx, db, associationRequest)
		if err != nil || association.AssociationGeneration != 1 || association.SourceHostID != host || association.DestinationHostID != destinationHost || association.SourcePlanID != decision.PlanID || association.DestinationPlanID != move.Destination.Plan || association.DependencyDigest != logicalDependency || association.DesiredDigest != logicalDesired {
			t.Fatalf("two-Port EVACUATE association=%+v err=%v", association, err)
		}
		replayed, err := AssociateVMAggregateMobility(ctx, db, associationRequest)
		if err != nil || replayed.AssociationDigest != association.AssociationDigest {
			t.Fatalf("two-Port EVACUATE replay=%+v err=%v", replayed, err)
		}
		var currentRevision, currentRuntime, currentAssociationGeneration uint64
		var currentDependency, currentDesired, currentHost, currentAdmission, currentPlan, currentAssociation string
		if err = db.QueryRow(ctx, `SELECT r.vm_revision,r.runtime_intent_generation,s.dependency_digest,r.desired_digest,b.host_id,b.admission_id,b.plan_id,b.mobility_association_generation,b.mobility_association_id FROM kim.vm_resources_current r JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.runtime_intent_generation)=(r.vm_id,r.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=r.vm_id WHERE r.vm_id=$1`, vmID).Scan(&currentRevision, &currentRuntime, &currentDependency, &currentDesired, &currentHost, &currentAdmission, &currentPlan, &currentAssociationGeneration, &currentAssociation); err != nil || currentRevision != logicalRevision || currentRuntime != logicalRuntime || currentDependency != logicalDependency || currentDesired != logicalDesired || currentHost != destinationHost || currentAdmission != move.Destination.Admission || currentPlan != move.Destination.Plan || currentAssociationGeneration != 1 || currentAssociation != association.AssociationID {
			t.Fatalf("two-Port post-mobility logical/runtime=%d/%d %s/%s physical=%s/%s/%s association=%d/%s err=%v", currentRevision, currentRuntime, currentDependency, currentDesired, currentHost, currentAdmission, currentPlan, currentAssociationGeneration, currentAssociation, err)
		}
		for i := range ports {
			var currentDigest string
			var portGeneration, bindingGeneration uint64
			if err = db.QueryRow(ctx, `SELECT p.desired_digest,p.port_generation,b.binding_generation FROM kim.network_ports_current p JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=p.placement_admission_id WHERE p.port_id=$1`, ports[i].PortID).Scan(&currentDigest, &portGeneration, &bindingGeneration); err != nil || currentDigest != logicalPortDigests[i] || portGeneration != 2 || bindingGeneration != 2 {
				t.Fatalf("two-Port current[%d] digest=%s generation=%d/%d err=%v", i, currentDigest, portGeneration, bindingGeneration, err)
			}
		}
		var associatedPorts int
		if err = db.QueryRow(ctx, `SELECT port_count FROM kim.vm_aggregate_mobility_association_evidence WHERE association_id=$1`, association.AssociationID).Scan(&associatedPorts); err != nil || associatedPorts != 2 {
			t.Fatalf("two-Port association cardinality=%d err=%v", associatedPorts, err)
		}
		return
	}
	logicalRevision, logicalRuntime, logicalDependency, logicalDesired := aggregate.VMRevision, aggregate.RuntimeIntentGeneration, aggregate.DependencyDigest, aggregate.DesiredDigest
	logicalPortDigests := make([]string, len(ports))
	for i := range ports {
		if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.network_ports_current WHERE port_id=$1`, ports[i].PortID).Scan(&logicalPortDigests[i]); err != nil {
			t.Fatal(err)
		}
		convergeEvacuationOVSDataplane(t, ctx, db, host, vmID, decision.PlanID, ports[i].PortID, network.NetworkID, segmentID, ports[i].MACAddress, fmt.Sprintf("aggregate-recovery-%d-%s", i, suffix), 1, 1, 1)
	}
	source := repeatedEvacuationIncarnation{Host: host, Admission: admission.AdmissionID, Plan: decision.PlanID, Volume: volume.VolumeID, Attachment: placementRequest.Storage[0].AttachmentID, Binding: volume.BindingID, LV: volume.LVUUID, Backend: backendID, VG: vgUUID, Materialization: 1, PortGeneration: 1, BindingGeneration: 1}
	if err = AuthorizeVMPowerOff(ctx, db, vmID, 1, host, "vm-port-recovery-shutoff-job-"+suffix, "vm-port-recovery-shutoff-command-"+suffix); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, db, host, "vm-port-recovery-shutoff-command-"+suffix, "vm-port-recovery-shutoff-verification-"+suffix, 2, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"}, "SUCCEEDED")
	if withData {
		dataCommand := "vm-port-recovery-source-data-detach-command-" + suffix
		dataAttachment := placementRequest.Storage[1].AttachmentID
		var dataResourceKey string
		if err = db.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, dataVolume.BindingID).Scan(&dataResourceKey); err != nil {
			t.Fatal(err)
		}
		if err = CreateExecutionCommand(ctx, db, ExecutionCommandRequest{JobID: "vm-port-recovery-source-data-detach-job-" + suffix, CommandID: dataCommand, HostID: host, ResourceType: "VOLUME_ATTACHMENT", ResourceID: dataAttachment, DesiredRevision: 1, CommandType: libvirtvolume.CommandType, SchemaVersion: libvirtvolume.SchemaVersion, TargetResourceID: "attachment:" + dataAttachment, Payload: map[string]any{"domain_uuid": vmID, "volume_id": dataVolume.VolumeID, "vg_uuid": vgUUID, "lv_uuid": dataVolume.LVUUID, "backend_resource_key": dataResourceKey, "disk_slot": 1, "desired_state": "DETACHED", "access_mode": "SINGLE_WRITER"}}); err != nil {
			t.Fatal(err)
		}
		dataVerification := "vm-port-recovery-source-data-detach-verification-" + suffix
		dataAttempt := acceptEvacuationCommand(t, ctx, db, host, dataCommand, dataVerification, 2, map[string]any{"attachment_id": dataAttachment, "volume_id": dataVolume.VolumeID, "binding_id": dataVolume.BindingID, "domain_uuid": vmID, "target_device": "vdb", "observed_lv_uuid": dataVolume.LVUUID, "desired_state": "DETACHED", "device_present": false, "device_identity_matches": false, "source_identity_matches": false, "holder_open": false, "read_only": false}, "SUCCEEDED")
		if err = AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: "vm-port-recovery-source-data-detach-evidence-" + suffix, AttachmentID: dataAttachment, VolumeID: dataVolume.VolumeID, AttachmentGeneration: 1, BindingID: dataVolume.BindingID, BindingGeneration: dataVolume.BindingGeneration, HostID: host, DomainUUID: vmID, TargetDevice: "vdb", ObservedLVUUID: dataVolume.LVUUID, DesiredState: "DETACHED", CommandID: dataCommand, VerificationID: dataVerification, ObservationGeneration: 2, AttemptIndex: uint32(dataAttempt), ObservationDigest: digestBytes([]byte(dataCommand + "/observation")), VerifierDigest: digestBytes([]byte(dataCommand + "/verifier")), EvidenceState: "MATCHED"}); err != nil {
			t.Fatal(err)
		}
	}
	epoch, err := OpenFailureEpoch(ctx, db, OpenFailureEpochRequest{OpenRequestID: "vm-port-failure-open-" + suffix, FailureEpochID: "vm-port-failure-epoch-" + suffix, IncidentKey: "vm-port-recovery-" + suffix, WorkloadID: placementRequest.WorkloadID, FailureClass: "VM_RUNTIME_UNAVAILABLE", RequestedBy: "qualification", ExpectedBindingRevision: admission.AvailabilityBinding.BindingRevision, ExpectedBindingDigest: admission.AvailabilityBinding.BindingDigest, Trigger: FailureObservation{EvidenceID: "vm-port-failure-observation-" + suffix, EvidenceType: "VM_RUNTIME_OBSERVATION", SourceType: "LIBVIRT_READ_BACK", SourceHostID: host, ObservedState: "PRESENT", FreshnessState: "CURRENT", PayloadDigest: digestBytes([]byte("vm-port-recovery-shutoff-command-" + suffix + "/observation")), ObservationGeneration: 2, ObservedAt: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	confirmationEvaluation, err := EvaluateFailureConfirmation(ctx, db, "vm-port-confirmation-evaluation-"+suffix, epoch.FailureEpochID, "vm-port-confirmation/v1", digestBytes([]byte("vm-port-confirmation/v1")))
	if err != nil || confirmationEvaluation.ResultState != "SATISFIED" {
		t.Fatalf("confirmation=%+v err=%v", confirmationEvaluation, err)
	}
	if _, _, err = ConfirmFailureEpoch(ctx, db, "vm-port-confirmation-decision-"+suffix, confirmationEvaluation.EvaluationID, "vm-port-failure-authority/v1"); err != nil {
		t.Fatal(err)
	}
	rootCommand := "vm-port-source-root-command-" + suffix
	if err = CreateExecutionCommand(ctx, db, ExecutionCommandRequest{JobID: "vm-port-source-root-job-" + suffix, CommandID: rootCommand, HostID: host, ResourceType: "SOURCE_ROOT_SAFETY", ResourceID: source.Attachment, DesiredRevision: 1, CommandType: SourceRootSafetyReadBackCommandType, SchemaVersion: SourceRootSafetyReadBackSchema, TargetResourceID: "attachment:" + source.Attachment, Payload: map[string]any{"desired_state": "OBSERVE"}}); err != nil {
		t.Fatal(err)
	}
	if err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error { return fenceHostOperationAuthorityTx(ctx, tx, host, "vm_aggregate_recovery") }); err != nil {
		t.Fatal(err)
	}
	if _, err = RecordSourceExecutionFencingObservation(ctx, db, "vm-port-fencing-observation-"+suffix, epoch.FailureEpochID); err != nil {
		t.Fatal(err)
	}
	fencingEvaluation, err := EvaluateFailureFencing(ctx, db, "vm-port-fencing-evaluation-"+suffix, epoch.FailureEpochID, "vm-port-fencing/v1", digestBytes([]byte("vm-port-fencing/v1")))
	if err != nil {
		t.Fatal(err)
	}
	rootVerification := "vm-port-source-root-verification-" + suffix
	rootAttempt := acceptEvacuationCommand(t, ctx, db, host, rootCommand, rootVerification, 2, map[string]any{"attachment_id": source.Attachment, "volume_id": source.Volume, "binding_id": source.Binding, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": source.LV, "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": false}, "SUCCEEDED")
	if err = AcceptSourceRootSafetyObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: "vm-port-source-root-evidence-" + suffix, AttachmentID: source.Attachment, VolumeID: source.Volume, AttachmentGeneration: 1, BindingID: source.Binding, BindingGeneration: 1, HostID: host, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: source.LV, CommandID: rootCommand, VerificationID: rootVerification, AttemptIndex: uint32(rootAttempt), ObservationGeneration: 2, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	rootEvaluation, err := EvaluateSourceRootSafety(ctx, db, "vm-port-root-evaluation-"+suffix, epoch.FailureEpochID, "vm-port-root/v1", digestBytes([]byte("vm-port-root/v1")))
	if err != nil || rootEvaluation.ResultState != "SAFE" {
		t.Fatalf("root evaluation=%+v err=%v", rootEvaluation, err)
	}
	rootProof, err := MaterializeSourceRootSafetyProof(ctx, db, "vm-port-root-proof-"+suffix, rootEvaluation.EvaluationID, "vm-port-root-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	storageEvaluation, err := EvaluateStorageSafety(ctx, db, "vm-port-storage-evaluation-"+suffix, epoch.FailureEpochID, "vm-port-storage/v1", digestBytes([]byte("vm-port-storage/v1")))
	if err != nil || storageEvaluation.ResultState != "SAFE" {
		t.Fatalf("storage evaluation=%+v err=%v", storageEvaluation, err)
	}
	storageProof, err := MaterializeStorageSafetyProof(ctx, db, "vm-port-storage-proof-"+suffix, storageEvaluation.EvaluationID, "vm-port-storage-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	fencingProof, _, err := MaterializeFailureFencingProof(ctx, db, "vm-port-fencing-proof-"+suffix, fencingEvaluation.EvaluationID, "vm-port-fencing-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	for i, recoveryPort := range ports {
		portSuffix := fmt.Sprintf("%d-%s", i, suffix)
		retirement, err := CommitOVNPortBindingRetirement(ctx, db, OVNPortBindingRetirementRequest{OperationID: "vm-port-recovery-retirement-" + portSuffix, OperationGeneration: 1, IntentID: "vm-port-recovery-retirement-intent-" + portSuffix, IntentGeneration: 2, PortID: recoveryPort.PortID, PortGeneration: 1, BindingGeneration: 1, SourceHostID: host})
		if err != nil {
			t.Fatal(err)
		}
		owner := "vm-port-retirement-worker-" + portSuffix
		retirementWork, err := ClaimOVNRuntimeWork(ctx, db, OVNRuntimeClaimRequest{Owner: owner, Limit: 1, Lease: time.Minute})
		if err != nil || len(retirementWork) != 1 {
			t.Fatalf("retirement work[%d]=%+v err=%v", i, retirementWork, err)
		}
		retirementClaim := OVNRuntimeClaim{WorkID: retirement.WorkID, Owner: owner, ClaimGeneration: retirementWork[0].ClaimGeneration}
		if err = AuthorizeOVNRuntimeApply(ctx, db, retirementClaim); err != nil {
			t.Fatal(err)
		}
		retirementEvidence := "vm-port-recovery-retirement-evidence-" + portSuffix
		if err = CompleteOVNPortBindingRetirement(ctx, db, retirementClaim, OVNPortBindingRetirementObservation{EvidenceID: retirementEvidence, IntentID: retirement.IntentID, IntentGeneration: retirement.IntentGeneration, PortID: recoveryPort.PortID, PortGeneration: 1, BindingGeneration: 1, SourceHostID: host, OperationGeneration: 1, NBObservationGeneration: 2, SBObservationGeneration: 2, OVSObservationGeneration: 2, NBObservationDigest: digestBytes([]byte("vm-port-retirement-nb-" + portSuffix)), SBObservationDigest: digestBytes([]byte("vm-port-retirement-sb-" + portSuffix)), OVSObservationDigest: digestBytes([]byte("vm-port-retirement-ovs-" + portSuffix)), AdapterArtifactDigest: digestBytes([]byte("vm-port-retirement-adapter-" + portSuffix)), ApplyResponseState: "RECEIVED", Observation: verifiedOVNRetirementObservation()}); err != nil {
			t.Fatal(err)
		}
		quiescence, err := PrepareNetworkPortSourceQuiescence(ctx, db, NetworkPortSourceQuiescenceRequest{FailureEpochID: epoch.FailureEpochID, PortID: recoveryPort.PortID, JobID: "vm-port-recovery-quiescence-job-" + portSuffix, CommandID: "vm-port-recovery-quiescence-command-" + portSuffix})
		if err != nil {
			t.Fatal(err)
		}
		quiescenceVerification := "vm-port-recovery-quiescence-verification-" + portSuffix
		quiescenceAttempt := acceptEvacuationCommand(t, ctx, db, host, quiescence.CommandID, quiescenceVerification, 1, map[string]any{"domain_uuid": vmID, "vm_generation": float64(1), "port_id": recoveryPort.PortID, "port_generation": float64(1), "binding_generation": float64(1), "domain_running": false, "interface_present": false}, "SUCCEEDED")
		if err = AcceptNetworkPortSourceQuiescence(ctx, db, NetworkPortSourceQuiescenceObservation{EvidenceID: "vm-port-recovery-quiescence-evidence-" + portSuffix, FailureEpochID: epoch.FailureEpochID, PortID: recoveryPort.PortID, SourceHostID: host, VMID: vmID, CommandID: quiescence.CommandID, VerificationID: quiescenceVerification, ObservationDigest: digestBytes([]byte(quiescence.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(quiescence.CommandID + "/verifier")), PortGeneration: 1, BindingGeneration: 1, VMGeneration: 1, ObservationGeneration: 1, AttemptIndex: uint32(quiescenceAttempt)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = RetireSourceMaterialization(ctx, db, "vm-port-source-retirement-"+suffix, epoch.FailureEpochID, rootProof.ProofID, fencingProof.ProofID, "vm-port-retirement-authority/v1"); err != nil {
		t.Fatal(err)
	}
	eligibility, err := EvaluateRecoveryEligibility(ctx, db, "vm-port-eligibility-"+suffix, epoch.FailureEpochID, scopeID, "vm-port-eligibility/v1", digestBytes([]byte("vm-port-eligibility/v1")))
	if err != nil || eligibility.ResultState != "ELIGIBLE" {
		t.Fatalf("eligibility=%+v err=%v", eligibility, err)
	}
	eligibilityDecision, err := MaterializeRecoveryEligibilityDecision(ctx, db, "vm-port-eligibility-decision-"+suffix, eligibility.EvaluationID, "vm-port-recovery-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	recoveryOperation := "vm-port-recovery-operation-" + suffix
	if _, err = RecordRecoveryOperationRequest(ctx, db, recoveryOperation, eligibilityDecision.DecisionID, eligibilityDecision.BudgetClaimID, "RESTART_ON_OTHER_HOST", "qualification"); err != nil {
		t.Fatal(err)
	}
	_, recoveryPlan, err := PlanRecoveryOperation(ctx, db, recoveryOperation, "vm-port-recovery-plan-"+suffix, destinationHost)
	if err != nil {
		t.Fatal(err)
	}
	recoveryStart, err := StartRecoveryOperation(ctx, db, recoveryOperation, "vm-port-recovery-preparation-job-"+suffix, "vm-port-recovery-preparation-command-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	recoveryOptions := mixedRecoveryDestinationOptions{}
	for i := 1; i < len(ports); i++ {
		recoveryOptions.AdditionalPorts = append(recoveryOptions.AdditionalPorts, repeatedEvacuationPort{PortID: ports[i].PortID, NetworkID: network.NetworkID, SegmentID: segmentID, MAC: ports[i].MACAddress})
	}
	if withData {
		recoveryOptions.DataContentDigest = digestBytes([]byte("vm-aggregate-recovery-data-marker/" + suffix))
	}
	recovery := completeMixedRecoveryDestination(t, ctx, db, "aggregate-"+suffix, vmID, imageID, imageDigest, network.NetworkID, segmentID, port.PortID, port.MACAddress, destinationBackend, destinationVG, recoveryPlan, recoveryStart, fencingProof.ProofID, storageProof.ProofID, recoveryOptions)
	recovery.Source = source
	recoveryAssociationRequest := VMAggregateMobilityAssociationRequest{AssociationID: "vm-port-recovery-association-" + suffix, VMID: vmID, MobilityKind: "RECOVERY", MobilityTerminalEvidenceID: recovery.Terminal}
	mobilityDriftRollback := errors.New("rollback post-terminal destination drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET current_plan_id=$2 WHERE vm_id=$1`, vmID, decision.PlanID); err != nil {
			return err
		}
		if _, err := AssociateVMAggregateMobility(ctx, scopeTxBeginner{tx}, VMAggregateMobilityAssociationRequest{AssociationID: recoveryAssociationRequest.AssociationID + "-drift", VMID: vmID, MobilityKind: "RECOVERY", MobilityTerminalEvidenceID: recovery.Terminal}); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("post-terminal destination plan drift accepted: %v", err)
		}
		return mobilityDriftRollback
	})
	if !errors.Is(err, mobilityDriftRollback) {
		t.Fatal(err)
	}
	if portCount == 2 {
		partialRollback := errors.New("rollback Recovery partial destination Port set")
		err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM kim.vm_network_port_realizations_current WHERE vm_id=$1 AND port_id=$2`, vmID, ports[1].PortID); err != nil {
				return err
			}
			if _, err := AssociateVMAggregateMobility(ctx, scopeTxBeginner{tx}, VMAggregateMobilityAssociationRequest{AssociationID: recoveryAssociationRequest.AssociationID + "-partial-port", VMID: vmID, MobilityKind: "RECOVERY", MobilityTerminalEvidenceID: recovery.Terminal}); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("Recovery partial destination Port set accepted: %v", err)
			}
			return partialRollback
		})
		if !errors.Is(err, partialRollback) {
			t.Fatal(err)
		}
	}
	if withData {
		dataDriftRollback := errors.New("rollback Recovery destination DATA binding drift")
		err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1`, recovery.Destination.Data.Binding); err != nil {
				return err
			}
			if _, err := AssociateVMAggregateMobility(ctx, scopeTxBeginner{tx}, VMAggregateMobilityAssociationRequest{AssociationID: recoveryAssociationRequest.AssociationID + "-data-drift", VMID: vmID, MobilityKind: "RECOVERY", MobilityTerminalEvidenceID: recovery.Terminal}); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("Recovery destination DATA binding drift accepted: %v", err)
			}
			return dataDriftRollback
		})
		if !errors.Is(err, dataDriftRollback) {
			t.Fatal(err)
		}
	}
	recoveryAssociation, err := AssociateVMAggregateMobility(ctx, db, recoveryAssociationRequest)
	if err != nil || recoveryAssociation.AssociationGeneration != 1 || recoveryAssociation.SourceHostID != host || recoveryAssociation.DestinationHostID != destinationHost || recoveryAssociation.SourcePlanID != decision.PlanID || recoveryAssociation.DestinationPlanID != recovery.Destination.Plan || recoveryAssociation.VMRevision != logicalRevision || recoveryAssociation.RuntimeIntentGeneration != logicalRuntime || recoveryAssociation.DependencyDigest != logicalDependency || recoveryAssociation.DesiredDigest != logicalDesired {
		t.Fatalf("Recovery association=%+v err=%v", recoveryAssociation, err)
	}
	replayed, err := AssociateVMAggregateMobility(ctx, db, recoveryAssociationRequest)
	if err != nil || replayed.AssociationDigest != recoveryAssociation.AssociationDigest {
		t.Fatalf("Recovery replay=%+v err=%v", replayed, err)
	}
	if _, err = AssociateVMAggregateMobility(ctx, db, VMAggregateMobilityAssociationRequest{AssociationID: recoveryAssociationRequest.AssociationID, VMID: vmID, MobilityKind: "HOST_EVACUATION", MobilityTerminalEvidenceID: recovery.Terminal}); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("association identifier rebound: %v", err)
	}
	if maximumRecovery {
		var associationPorts, associationVolumes, materializationVolumes, verificationVolumes int
		var epochState, operationState, copyResponse string
		if err = db.QueryRow(ctx, `SELECT association.port_count,association.volume_count,materialization.volume_count,verification.volume_count,epoch.epoch_state,operation.lifecycle_state,copy.response_state
			FROM kim.vm_aggregate_mobility_association_evidence association
			JOIN kim.recovery_terminal_decision_evidence terminal ON terminal.terminal_decision_id=association.mobility_terminal_evidence_id
			JOIN kim.recovery_operation_evidence recovery_operation ON recovery_operation.recovery_operation_id=terminal.recovery_operation_id
			JOIN kim.recovery_operations_current operation ON operation.recovery_operation_id=recovery_operation.recovery_operation_id
			JOIN kim.failure_epochs_current epoch ON epoch.failure_epoch_id=recovery_operation.failure_epoch_id
			JOIN kim.recovery_materialization_evidence materialization ON materialization.recovery_operation_id=recovery_operation.recovery_operation_id
			JOIN kim.recovery_verification_evidence verification ON verification.verification_id=terminal.verification_id
			JOIN kim.local_lvm_relocation_copy_operation_evidence copy_operation ON copy_operation.recovery_operation_id=recovery_operation.recovery_operation_id AND copy_operation.volume_ordinal=1
			JOIN kim.local_lvm_relocation_copy_operations_current copy ON copy.copy_operation_id=copy_operation.copy_operation_id AND copy.copy_generation=copy_operation.copy_generation
			WHERE association.association_id=$1`, recoveryAssociation.AssociationID).Scan(&associationPorts, &associationVolumes, &materializationVolumes, &verificationVolumes, &epochState, &operationState, &copyResponse); err != nil || associationPorts != 2 || associationVolumes != 2 || materializationVolumes != 2 || verificationVolumes != 2 || epochState != "RECOVERED" || operationState != "VERIFIED" || copyResponse != "LOST" {
			t.Fatalf("maximum Recovery cardinality Ports=%d Volumes=%d/%d/%d epoch=%s operation=%s copy=%s err=%v", associationPorts, associationVolumes, materializationVolumes, verificationVolumes, epochState, operationState, copyResponse, err)
		}
		for i := range ports {
			var digest string
			if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.network_ports_current WHERE port_id=$1`, ports[i].PortID).Scan(&digest); err != nil || digest != logicalPortDigests[i] {
				t.Fatalf("maximum Recovery logical Port[%d] digest=%s err=%v", i, digest, err)
			}
		}
		for _, table := range []string{"recovery_materialization_volume_evidence", "recovery_storage_volume_verification_evidence"} {
			if _, err = db.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
				t.Fatalf("immutable UPDATE succeeded: %s", table)
			}
		}
		return
	}
	move := executeRepeatedEvacuationMove(t, ctx, db, suffix, "aggregate-after-recovery", vmID, imageID, imageDigest, network.NetworkID, segmentID, port.PortID, port.MACAddress, recovery.Destination, evacuationHost, evacuationBackend, evacuationVG, 2, 2, 4, 5, 4, 5, nil, nil)
	evacuationAssociationRequest := VMAggregateMobilityAssociationRequest{AssociationID: "vm-port-evacuation-association-" + suffix, VMID: vmID, MobilityKind: "HOST_EVACUATION", MobilityTerminalEvidenceID: move.ParentTerminal}
	evacuationAssociation, err := AssociateVMAggregateMobility(ctx, db, evacuationAssociationRequest)
	if err != nil || evacuationAssociation.AssociationGeneration != 2 || evacuationAssociation.SourceHostID != destinationHost || evacuationAssociation.DestinationHostID != evacuationHost || evacuationAssociation.SourcePlanID != recovery.Destination.Plan || evacuationAssociation.DestinationPlanID != move.Destination.Plan || evacuationAssociation.DependencyDigest != logicalDependency || evacuationAssociation.DesiredDigest != logicalDesired {
		t.Fatalf("EVACUATE association=%+v err=%v", evacuationAssociation, err)
	}
	if _, err = AssociateVMAggregateMobility(ctx, db, VMAggregateMobilityAssociationRequest{AssociationID: recoveryAssociationRequest.AssociationID + "-stale-after-evacuation", VMID: vmID, MobilityKind: "RECOVERY", MobilityTerminalEvidenceID: recovery.Terminal}); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("old Recovery terminal advanced post-EVACUATE current: %v", err)
	}
	var currentVMRevision, currentRuntime, currentAssociationGeneration uint64
	var currentDependency, currentDesired, currentHost, currentAdmission, currentPlan, currentAssociation, currentPortDigest string
	if err = db.QueryRow(ctx, `SELECT r.vm_revision,r.runtime_intent_generation,s.dependency_digest,r.desired_digest,b.host_id,b.admission_id,b.plan_id,b.mobility_association_generation,b.mobility_association_id,p.desired_digest FROM kim.vm_resources_current r JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.runtime_intent_generation)=(r.vm_id,r.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=r.vm_id JOIN kim.network_ports_current p ON p.port_id=$2 WHERE r.vm_id=$1`, vmID, port.PortID).Scan(&currentVMRevision, &currentRuntime, &currentDependency, &currentDesired, &currentHost, &currentAdmission, &currentPlan, &currentAssociationGeneration, &currentAssociation, &currentPortDigest); err != nil || currentVMRevision != logicalRevision || currentRuntime != logicalRuntime || currentDependency != logicalDependency || currentDesired != logicalDesired || currentPortDigest != logicalPortDigests[0] || currentHost != evacuationHost || currentAdmission != move.Destination.Admission || currentPlan != move.Destination.Plan || currentAssociationGeneration != 2 || currentAssociation != evacuationAssociation.AssociationID {
		t.Fatalf("post-mobility logical/runtime=%d/%d %s/%s Port=%s physical=%s/%s/%s association=%d/%s err=%v", currentVMRevision, currentRuntime, currentDependency, currentDesired, currentPortDigest, currentHost, currentAdmission, currentPlan, currentAssociationGeneration, currentAssociation, err)
	}
	if _, err = db.Exec(ctx, `UPDATE kim.vm_aggregate_mobility_association_evidence SET recorded_at=recorded_at WHERE association_id=$1`, recoveryAssociation.AssociationID); err == nil {
		t.Fatal("immutable mobility association UPDATE succeeded")
	}
	for _, table := range []string{"vm_aggregate_port_binding_evidence", "vm_aggregate_network_port_verification_evidence"} {
		if _, err = db.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
}

func materializeVMAggregateStandardPortDataVolume(t *testing.T, ctx context.Context, db *pgxpool.Pool, suffix, projectID, classID, backendID, vgUUID string) VolumeResource {
	t.Helper()
	volume, err := CreateVolumeResource(ctx, db, VolumeResourceRequest{VolumeID: "vm-port-data-" + suffix, ProjectID: projectID, Name: "data", StorageClassID: classID, StorageClassRevision: 1, SizeBytes: 8 << 20, Bootable: false, SourceType: "BLANK"})
	if err != nil {
		t.Fatal(err)
	}
	volume, err = AllocateVolumeCapacity(ctx, db, VolumeCapacityAllocationRequest{VolumeID: volume.VolumeID, BackendID: backendID, ExpectedVolumeRevision: 1, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := &volumeResourceLVMClient{vgUUID: vgUUID, lvUUID: "lv-vm-port-data-" + suffix}
	mutation := locallvm.Backend{Client: client, VolumeGroups: map[string]string{vgUUID: "kim_test_vg"}}
	readOnly := locallvm.ReadBackBackend{Backend: mutation}
	claim, err := ClaimVolumeMaterialization(ctx, db, volume.OperationID, "vm-port-data-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := AuthorizeVolumeMaterializationCommand(ctx, db, claim, "vm-port-data-job-"+suffix, "vm-port-data-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, db, apply.CommandID, 1, CommandLeaseScopeMutation, mutation)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, db, claim); err != nil {
		t.Fatal(err)
	}
	claim, err = ClaimVolumeMaterialization(ctx, db, volume.OperationID, "vm-port-data-successor", time.Minute)
	if err != nil || claim.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("DATA successor=%+v err=%v", claim, err)
	}
	read, err := AuthorizeVolumeMaterializationCommand(ctx, db, claim, "vm-port-data-read-job-"+suffix, "vm-port-data-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	verification := observeVolumeBackendAfterLostResponse(t, ctx, db, read.CommandID, "vm-port-data-verification-"+suffix, 1, readOnly)
	if _, err = CompleteVolumeMaterialization(ctx, db, claim, CompleteVolumeMaterializationRequest{OperationID: claim.OperationID, OperationGeneration: claim.OperationGeneration, ClaimGeneration: claim.ClaimGeneration, ObservationID: "vm-port-data-observation-" + suffix, VerificationID: verification}); err != nil {
		t.Fatal(err)
	}
	volume, err = GetVolumeResource(ctx, db, volume.VolumeID)
	if err != nil || volume.Lifecycle != "AVAILABLE" || volume.MaterializationState != "VERIFIED" || volume.Bootable {
		t.Fatalf("DATA volume=%+v err=%v", volume, err)
	}
	return volume
}

func qualifyVMAggregateOneStandardPortDelete(t *testing.T, ctx context.Context, db *pgxpool.Pool, suffix, vmID, portID, logicalPortDigest string) {
	var host string
	var vmRevision, vmGeneration, powerObservationGeneration uint64
	if err := db.QueryRow(ctx, `SELECT r.vm_revision,b.host_id,b.vm_generation,p.observation_generation FROM kim.vm_resources_current r JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=r.vm_id JOIN kim.vm_power_state_current p ON p.vm_id=b.vm_id AND p.vm_generation=b.vm_generation WHERE r.vm_id=$1`, vmID).Scan(&vmRevision, &host, &vmGeneration, &powerObservationGeneration); err != nil {
		t.Fatal(err)
	}
	shutdownCommand := "vm-port-delete-shutoff-command-" + suffix
	if err := AuthorizeVMPowerOff(ctx, db, vmID, vmGeneration, host, "vm-port-delete-shutoff-job-"+suffix, shutdownCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, db, host, shutdownCommand, "vm-port-delete-shutoff-verification-"+suffix, powerObservationGeneration+1, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"}, "SUCCEEDED")

	deleting, err := StartVMAggregateDelete(ctx, db, VMAggregateDeleteRequest{RequestID: "vm-port-delete-request-" + suffix, OperationID: "vm-port-delete-operation-" + suffix, VMID: vmID, ExpectedRevision: vmRevision})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimVMAggregateLifecycle(ctx, db, deleting.OperationID, "vm-port-delete-worker-"+suffix, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	domainCommand := "vm-port-delete-domain-command-" + suffix
	if _, err = AuthorizeVMAggregateDeleteDomainCommand(ctx, db, claim, "vm-port-delete-domain-job-"+suffix, domainCommand); err != nil {
		t.Fatal(err)
	}
	domainVerification := "vm-port-delete-domain-verification-" + suffix
	var planDigest, backendDigest string
	var materializationGeneration uint64
	if err = db.QueryRow(ctx, `SELECT payload->>'source_plan_digest',(payload->>'source_materialization_generation')::bigint,payload->>'backend_identity_digest' FROM kim.execution_commands WHERE command_id=$1`, domainCommand).Scan(&planDigest, &materializationGeneration, &backendDigest); err != nil {
		t.Fatal(err)
	}
	domainAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, domainCommand, domainVerification, "MATCHED", 1, map[string]any{"cleanup_operation_id": deleting.OperationID, "cleanup_generation": 1, "domain_uuid": vmID, "vm_generation": vmGeneration, "source_host_id": host, "source_plan_digest": planDigest, "source_materialization_generation": materializationGeneration, "backend_identity_digest": backendDigest, "domain_present": false, "domain_running": false, "identity_matches": true})
	domainAbsenceID := "vm-port-delete-domain-absence-" + suffix
	if err = RecordVMAggregateDeleteDomainAbsence(ctx, db, claim, domainAbsenceID, domainCommand, domainVerification, uint32(domainAttempt), 1, digestBytes([]byte(domainCommand+"/observation")), digestBytes([]byte(domainCommand+"/verifier"))); err != nil {
		t.Fatal(err)
	}

	retirement, err := AuthorizeVMAggregateDeletePortRetirement(ctx, db, claim, domainAbsenceID)
	if err != nil {
		t.Fatal(err)
	}
	retirementOwner := "vm-port-delete-network-worker-" + suffix
	retirementClaims, err := ClaimOVNRuntimeWork(ctx, db, OVNRuntimeClaimRequest{Owner: retirementOwner, Limit: 1, Lease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var retirementClaim OVNRuntimeClaim
	for _, candidate := range retirementClaims {
		if candidate.WorkID == retirement.WorkID {
			retirementClaim = OVNRuntimeClaim{WorkID: candidate.WorkID, Owner: retirementOwner, ClaimGeneration: candidate.ClaimGeneration}
		}
	}
	if retirementClaim.WorkID == "" {
		t.Fatalf("delete retirement work not claimed: %+v", retirementClaims)
	}
	retirementEvidenceID := "vm-port-delete-retirement-evidence-" + suffix
	if err = CompleteOVNPortBindingRetirement(ctx, db, retirementClaim, OVNPortBindingRetirementObservation{EvidenceID: retirementEvidenceID, IntentID: retirement.IntentID, IntentGeneration: retirement.IntentGeneration, PortID: retirement.PortID, PortGeneration: retirement.PortGeneration, BindingGeneration: retirement.BindingGeneration, SourceHostID: retirement.SourceHostID, OperationGeneration: retirement.OperationGeneration, NBObservationGeneration: 2, SBObservationGeneration: 2, OVSObservationGeneration: 2, NBObservationDigest: digestBytes([]byte(retirementEvidenceID + "/nb")), SBObservationDigest: digestBytes([]byte(retirementEvidenceID + "/sb")), OVSObservationDigest: digestBytes([]byte(retirementEvidenceID + "/ovs")), AdapterArtifactDigest: digestBytes([]byte(retirementEvidenceID + "/adapter")), ApplyResponseState: "RECEIVED", Observation: verifiedOVNRetirementObservation()}); err != nil {
		t.Fatal(err)
	}
	networkAbsenceID := "vm-port-delete-network-absence-" + suffix
	if err = RecordVMAggregateDeleteNetworkAbsence(ctx, db, claim, networkAbsenceID, retirementEvidenceID); err != nil {
		t.Fatal(err)
	}
	if err = RecordVMAggregateDeleteNetworkAbsence(ctx, db, claim, networkAbsenceID, retirementEvidenceID); err != nil {
		t.Fatalf("network absence replay: %v", err)
	}
	if err = RecordVMAggregateDeleteNetworkAbsence(ctx, db, claim, networkAbsenceID, retirementEvidenceID+"-different"); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("network absence identifier rebind accepted: %v", err)
	}

	rootCommand := "vm-port-delete-root-command-" + suffix
	if _, err = AuthorizeVMAggregateDeleteRootAbsenceReadBack(ctx, db, claim, domainAbsenceID, "vm-port-delete-root-job-"+suffix, rootCommand); err != nil {
		t.Fatal(err)
	}
	var volumeID, attachmentID, bindingID, lvUUID string
	var attachmentGeneration, bindingGeneration uint64
	if err = db.QueryRow(ctx, `SELECT root_volume_id,root_attachment_id,root_attachment_generation,root_binding_id,root_binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=root_binding_id AND binding_generation=root_binding_generation) FROM kim.vm_delete_operation_evidence WHERE delete_operation_id=$1`, deleting.OperationID).Scan(&volumeID, &attachmentID, &attachmentGeneration, &bindingID, &bindingGeneration, &lvUUID); err != nil {
		t.Fatal(err)
	}
	rootVerification := "vm-port-delete-root-verification-" + suffix
	rootAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, rootCommand, rootVerification, "MATCHED", 1, map[string]any{"attachment_id": attachmentID, "volume_id": volumeID, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": lvUUID, "desired_state": libvirtvolume.StateDetached, "device_present": false, "device_identity_matches": false, "source_identity_matches": false, "holder_open": false, "read_only": false})
	rootObservation := "vm-port-delete-root-observation-" + suffix
	if err = AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: rootObservation, AttachmentID: attachmentID, VolumeID: volumeID, BindingID: bindingID, HostID: host, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvUUID, DesiredState: libvirtvolume.StateDetached, CommandID: rootCommand, VerificationID: rootVerification, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", AttachmentGeneration: attachmentGeneration, BindingGeneration: bindingGeneration, ObservationGeneration: 1, AttemptIndex: uint32(rootAttempt)}); err != nil {
		t.Fatal(err)
	}
	storageAbsenceID, releaseID := "vm-port-delete-storage-absence-"+suffix, "vm-port-delete-compute-release-"+suffix
	terminalID, tombstoneID := "vm-port-delete-terminal-"+suffix, "vm-port-delete-tombstone-"+suffix
	if _, err = CompleteVMAggregateDelete(ctx, db, claim, domainAbsenceID, rootObservation, storageAbsenceID+"-missing-network", releaseID+"-missing-network", terminalID+"-missing-network", tombstoneID+"-missing-network"); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("one-Port delete accepted without network evidence: %v", err)
	}
	driftRollback := errors.New("rollback Port binding drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_state='RELEASE_PENDING' WHERE port_id=$1`, portID); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateDeleteWithNetwork(ctx, scopeTxBeginner{tx}, claim, domainAbsenceID, rootObservation, networkAbsenceID, storageAbsenceID+"-drift", releaseID+"-drift", terminalID+"-drift", tombstoneID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("Port binding drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	if terminal, err := CompleteVMAggregateDeleteWithNetwork(ctx, db, claim, domainAbsenceID, rootObservation, networkAbsenceID, storageAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("one-Port delete terminal=%s err=%v", terminal, err)
	}
	if terminal, err := CompleteVMAggregateDeleteWithNetwork(ctx, db, claim, domainAbsenceID, rootObservation, networkAbsenceID, storageAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("one-Port delete terminal replay=%s err=%v", terminal, err)
	}
	if _, err := CompleteVMAggregateDeleteWithNetwork(ctx, db, claim, domainAbsenceID, rootObservation, networkAbsenceID+"-different", storageAbsenceID, releaseID, terminalID, tombstoneID); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("one-Port delete terminal identifier rebind accepted: %v", err)
	}
	var attachmentState, bindingState, desiredDigest string
	var workloadID, admissionID *string
	if err = db.QueryRow(ctx, `SELECT p.attachment_state,b.binding_state,p.desired_digest,p.workload_id,p.placement_admission_id FROM kim.network_ports_current p JOIN kim.port_bindings_current b USING(port_id) WHERE p.port_id=$1`, portID).Scan(&attachmentState, &bindingState, &desiredDigest, &workloadID, &admissionID); err != nil || attachmentState != "UNATTACHED" || bindingState != "RELEASED" || desiredDigest != logicalPortDigest || workloadID != nil || admissionID != nil {
		t.Fatalf("logical Port after VM delete attachment=%s binding=%s digest=%s workload=%v admission=%v err=%v", attachmentState, bindingState, desiredDigest, workloadID, admissionID, err)
	}
	for _, table := range []string{"vm_delete_network_operation_evidence", "vm_delete_network_absence_evidence"} {
		if _, err = db.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
}

func qualifyVMAggregateOneStandardPortDataVolumeDelete(t *testing.T, ctx context.Context, db *pgxpool.Pool, suffix, vmID, portID, logicalPortDigest string, rootVolume, dataVolume VolumeResource) {
	t.Helper()
	var host string
	var vmRevision, vmGeneration, powerObservationGeneration uint64
	if err := db.QueryRow(ctx, `SELECT r.vm_revision,b.host_id,b.vm_generation,p.observation_generation FROM kim.vm_resources_current r JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=r.vm_id JOIN kim.vm_power_state_current p ON p.vm_id=b.vm_id AND p.vm_generation=b.vm_generation WHERE r.vm_id=$1`, vmID).Scan(&vmRevision, &host, &vmGeneration, &powerObservationGeneration); err != nil {
		t.Fatal(err)
	}
	var rootDigest, dataDigest string
	if err := db.QueryRow(ctx, `SELECT (SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$1),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$2)`, rootVolume.VolumeID, dataVolume.VolumeID).Scan(&rootDigest, &dataDigest); err != nil {
		t.Fatal(err)
	}
	shutdownCommand := "vm-port-data-delete-shutoff-command-" + suffix
	if err := AuthorizeVMPowerOff(ctx, db, vmID, vmGeneration, host, "vm-port-data-delete-shutoff-job-"+suffix, shutdownCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, db, host, shutdownCommand, "vm-port-data-delete-shutoff-verification-"+suffix, powerObservationGeneration+1, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"}, "SUCCEEDED")

	deleting, err := StartVMAggregateDelete(ctx, db, VMAggregateDeleteRequest{RequestID: "vm-port-data-delete-request-" + suffix, OperationID: "vm-port-data-delete-operation-" + suffix, VMID: vmID, ExpectedRevision: vmRevision})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimVMAggregateLifecycle(ctx, db, deleting.OperationID, "vm-port-data-delete-worker-"+suffix, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	domainCommand := "vm-port-data-delete-domain-command-" + suffix
	if _, err = AuthorizeVMAggregateDeleteDomainCommand(ctx, db, claim, "vm-port-data-delete-domain-job-"+suffix, domainCommand); err != nil {
		t.Fatal(err)
	}
	var planDigest, backendDigest string
	var materializationGeneration uint64
	if err = db.QueryRow(ctx, `SELECT payload->>'source_plan_digest',(payload->>'source_materialization_generation')::bigint,payload->>'backend_identity_digest' FROM kim.execution_commands WHERE command_id=$1`, domainCommand).Scan(&planDigest, &materializationGeneration, &backendDigest); err != nil {
		t.Fatal(err)
	}
	domainVerification := "vm-port-data-delete-domain-verification-" + suffix
	domainAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, domainCommand, domainVerification, "MATCHED", 1, map[string]any{"cleanup_operation_id": deleting.OperationID, "cleanup_generation": 1, "domain_uuid": vmID, "vm_generation": vmGeneration, "source_host_id": host, "source_plan_digest": planDigest, "source_materialization_generation": materializationGeneration, "backend_identity_digest": backendDigest, "domain_present": false, "domain_running": false, "identity_matches": true})
	domainAbsenceID := "vm-port-data-delete-domain-absence-" + suffix
	if err = RecordVMAggregateDeleteDomainAbsence(ctx, db, claim, domainAbsenceID, domainCommand, domainVerification, uint32(domainAttempt), 1, digestBytes([]byte(domainCommand+"/observation")), digestBytes([]byte(domainCommand+"/verifier"))); err != nil {
		t.Fatal(err)
	}

	retirement, err := AuthorizeVMAggregateDeletePortRetirement(ctx, db, claim, domainAbsenceID)
	if err != nil {
		t.Fatal(err)
	}
	retirementOwner := "vm-port-data-delete-network-worker-" + suffix
	claims, err := ClaimOVNRuntimeWork(ctx, db, OVNRuntimeClaimRequest{Owner: retirementOwner, Limit: 1, Lease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var retirementClaim OVNRuntimeClaim
	for _, candidate := range claims {
		if candidate.WorkID == retirement.WorkID {
			retirementClaim = OVNRuntimeClaim{WorkID: candidate.WorkID, Owner: retirementOwner, ClaimGeneration: candidate.ClaimGeneration}
		}
	}
	if retirementClaim.WorkID == "" {
		t.Fatalf("combined delete retirement work not claimed: %+v", claims)
	}
	retirementEvidenceID := "vm-port-data-delete-retirement-evidence-" + suffix
	if err = CompleteOVNPortBindingRetirement(ctx, db, retirementClaim, OVNPortBindingRetirementObservation{EvidenceID: retirementEvidenceID, IntentID: retirement.IntentID, IntentGeneration: retirement.IntentGeneration, PortID: retirement.PortID, PortGeneration: retirement.PortGeneration, BindingGeneration: retirement.BindingGeneration, SourceHostID: retirement.SourceHostID, OperationGeneration: retirement.OperationGeneration, NBObservationGeneration: 2, SBObservationGeneration: 2, OVSObservationGeneration: 2, NBObservationDigest: digestBytes([]byte(retirementEvidenceID + "/nb")), SBObservationDigest: digestBytes([]byte(retirementEvidenceID + "/sb")), OVSObservationDigest: digestBytes([]byte(retirementEvidenceID + "/ovs")), AdapterArtifactDigest: digestBytes([]byte(retirementEvidenceID + "/adapter")), ApplyResponseState: "RECEIVED", Observation: verifiedOVNRetirementObservation()}); err != nil {
		t.Fatal(err)
	}
	networkAbsenceID := "vm-port-data-delete-network-absence-" + suffix
	if err = RecordVMAggregateDeleteNetworkAbsence(ctx, db, claim, networkAbsenceID, retirementEvidenceID); err != nil {
		t.Fatal(err)
	}

	type detachAuthority struct {
		commandID, verificationID, evidenceID, volumeID, attachmentID, bindingID, lvUUID string
		attachmentGeneration, bindingGeneration                                          uint64
	}
	readDetach := func(label string, authorize func(string, string) (VMAggregateDeleteCommand, error)) detachAuthority {
		t.Helper()
		a := detachAuthority{commandID: "vm-port-data-delete-" + label + "-command-" + suffix, verificationID: "vm-port-data-delete-" + label + "-verification-" + suffix, evidenceID: "vm-port-data-delete-" + label + "-observation-" + suffix}
		if _, err := authorize("vm-port-data-delete-"+label+"-job-"+suffix, a.commandID); err != nil {
			t.Fatal(err)
		}
		query := `SELECT root_volume_id,root_attachment_id,root_attachment_generation,root_binding_id,root_binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=root_binding_id AND binding_generation=root_binding_generation) FROM kim.vm_delete_operation_evidence WHERE delete_operation_id=$1`
		target := "vda"
		if label == "data" {
			query = `SELECT volume_id,physical_attachment_id,physical_attachment_generation,binding_id,binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=kim.vm_delete_data_volume_operation_evidence.binding_id AND binding_generation=kim.vm_delete_data_volume_operation_evidence.binding_generation) FROM kim.vm_delete_data_volume_operation_evidence WHERE delete_operation_id=$1`
			target = "vdb"
		}
		if err := db.QueryRow(ctx, query, deleting.OperationID).Scan(&a.volumeID, &a.attachmentID, &a.attachmentGeneration, &a.bindingID, &a.bindingGeneration, &a.lvUUID); err != nil {
			t.Fatal(err)
		}
		attempt := recordCleanupLeaseLossVerification(t, ctx, db, host, a.commandID, a.verificationID, "MATCHED", 1, map[string]any{"attachment_id": a.attachmentID, "volume_id": a.volumeID, "domain_uuid": vmID, "target_device": target, "observed_lv_uuid": a.lvUUID, "desired_state": libvirtvolume.StateDetached, "device_present": false, "device_identity_matches": false, "source_identity_matches": false, "holder_open": false, "read_only": false})
		if err := AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: a.evidenceID, AttachmentID: a.attachmentID, VolumeID: a.volumeID, BindingID: a.bindingID, HostID: host, DomainUUID: vmID, TargetDevice: target, ObservedLVUUID: a.lvUUID, DesiredState: libvirtvolume.StateDetached, CommandID: a.commandID, VerificationID: a.verificationID, ObservationDigest: digestBytes([]byte(a.commandID + "/observation")), VerifierDigest: digestBytes([]byte(a.commandID + "/verifier")), EvidenceState: "MATCHED", AttachmentGeneration: a.attachmentGeneration, BindingGeneration: a.bindingGeneration, ObservationGeneration: 1, AttemptIndex: uint32(attempt)}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	root := readDetach("root", func(jobID, commandID string) (VMAggregateDeleteCommand, error) {
		return AuthorizeVMAggregateDeleteRootAbsenceReadBack(ctx, db, claim, domainAbsenceID, jobID, commandID)
	})
	data := readDetach("data", func(jobID, commandID string) (VMAggregateDeleteCommand, error) {
		return AuthorizeVMAggregateDeleteDataAbsenceReadBack(ctx, db, claim, domainAbsenceID, jobID, commandID)
	})
	rootAbsenceID, dataAbsenceID := "vm-port-data-delete-root-absence-"+suffix, "vm-port-data-delete-data-absence-"+suffix
	releaseID := "vm-port-data-delete-compute-release-" + suffix
	terminalID, tombstoneID := "vm-port-data-delete-terminal-"+suffix, "vm-port-data-delete-tombstone-"+suffix
	if _, err = CompleteVMAggregateDeleteWithData(ctx, db, claim, domainAbsenceID, root.evidenceID, data.evidenceID, rootAbsenceID+"-missing-network", dataAbsenceID+"-missing-network", releaseID+"-missing-network", terminalID+"-missing-network", tombstoneID+"-missing-network"); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("combined delete accepted without network absence: %v", err)
	}
	if _, err = CompleteVMAggregateDeleteWithNetwork(ctx, db, claim, domainAbsenceID, root.evidenceID, networkAbsenceID, rootAbsenceID+"-missing-data", releaseID+"-missing-data", terminalID+"-missing-data", tombstoneID+"-missing-data"); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("combined delete accepted without DATA absence: %v", err)
	}
	driftRollback := errors.New("rollback combined Port and DATA drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_state='RELEASE_PENDING' WHERE port_id=$1`, portID); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateDeleteWithNetworkAndData(ctx, scopeTxBeginner{tx}, claim, domainAbsenceID, root.evidenceID, networkAbsenceID, data.evidenceID, rootAbsenceID+"-drift", dataAbsenceID+"-drift", releaseID+"-drift", terminalID+"-drift", tombstoneID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("combined terminal Port drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	dataDriftRollback := errors.New("rollback combined DATA binding drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1 AND binding_generation=$2`, data.bindingID, data.bindingGeneration); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateDeleteWithNetworkAndData(ctx, scopeTxBeginner{tx}, claim, domainAbsenceID, root.evidenceID, networkAbsenceID, data.evidenceID, rootAbsenceID+"-data-drift", dataAbsenceID+"-data-drift", releaseID+"-data-drift", terminalID+"-data-drift", tombstoneID+"-data-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("combined terminal DATA drift accepted: %v", err)
		}
		return dataDriftRollback
	})
	if !errors.Is(err, dataDriftRollback) {
		t.Fatal(err)
	}
	if terminal, err := CompleteVMAggregateDeleteWithNetworkAndData(ctx, db, claim, domainAbsenceID, root.evidenceID, networkAbsenceID, data.evidenceID, rootAbsenceID, dataAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("combined delete terminal=%s err=%v", terminal, err)
	}
	if terminal, err := CompleteVMAggregateDeleteWithNetworkAndData(ctx, db, claim, domainAbsenceID, root.evidenceID, networkAbsenceID, data.evidenceID, rootAbsenceID, dataAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("combined delete replay=%s err=%v", terminal, err)
	}

	var portAttachment, portBinding, resultingPortDigest string
	var workloadID, admissionID *string
	if err = db.QueryRow(ctx, `SELECT p.attachment_state,b.binding_state,p.desired_digest,p.workload_id,p.placement_admission_id FROM kim.network_ports_current p JOIN kim.port_bindings_current b USING(port_id) WHERE p.port_id=$1`, portID).Scan(&portAttachment, &portBinding, &resultingPortDigest, &workloadID, &admissionID); err != nil || portAttachment != "UNATTACHED" || portBinding != "RELEASED" || resultingPortDigest != logicalPortDigest || workloadID != nil || admissionID != nil {
		t.Fatalf("combined logical Port attachment=%s binding=%s digest=%s workload=%v admission=%v err=%v", portAttachment, portBinding, resultingPortDigest, workloadID, admissionID, err)
	}
	var retired, available, verified, capacityHeld int
	var resultingRootDigest, resultingDataDigest, computeState string
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.volume_attachment_intents_current WHERE workload_id=$1 AND intent_state='RETIRED'),(SELECT count(*) FROM kim.volumes_current WHERE volume_id IN($2,$3) AND lifecycle_state='AVAILABLE'),(SELECT count(*) FROM kim.volume_materializations_current WHERE volume_id IN($2,$3) AND materialization_state='VERIFIED'),(SELECT count(*) FROM kim.storage_capacity_claims WHERE volume_id IN($2,$3) AND claim_state IN('RESERVED','ALLOCATED')),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$2),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$3),(SELECT claim_state FROM kim.compute_allocation_claims WHERE workload_id=$1)`, vmID, root.volumeID, data.volumeID).Scan(&retired, &available, &verified, &capacityHeld, &resultingRootDigest, &resultingDataDigest, &computeState); err != nil || retired != 2 || available != 2 || verified != 2 || capacityHeld != 2 || resultingRootDigest != rootDigest || resultingDataDigest != dataDigest || computeState != "RELEASED" {
		t.Fatalf("combined storage retired=%d available=%d verified=%d capacity=%d compute=%s err=%v", retired, available, verified, capacityHeld, computeState, err)
	}
	deleted, err := GetVMAggregate(ctx, db, vmID)
	if err != nil || deleted.LifecycleState != "DELETED" || deleted.ConvergenceState != "CONVERGED" || deleted.OperationState != "VERIFIED" {
		t.Fatalf("combined deleted aggregate=%+v err=%v", deleted, err)
	}
}

func qualifyVMAggregateTwoStandardPortDelete(t *testing.T, ctx context.Context, db *pgxpool.Pool, suffix, vmID string, ports []PortResource, logicalPortDigests []string, dataVolume *VolumeResource) {
	t.Helper()
	var host string
	var vmRevision, vmGeneration, powerObservationGeneration uint64
	if err := db.QueryRow(ctx, `SELECT r.vm_revision,b.host_id,b.vm_generation,p.observation_generation FROM kim.vm_resources_current r JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=r.vm_id JOIN kim.vm_power_state_current p ON p.vm_id=b.vm_id AND p.vm_generation=b.vm_generation WHERE r.vm_id=$1`, vmID).Scan(&vmRevision, &host, &vmGeneration, &powerObservationGeneration); err != nil {
		t.Fatal(err)
	}
	shutdownCommand := "vm-two-port-delete-shutoff-command-" + suffix
	if err := AuthorizeVMPowerOff(ctx, db, vmID, vmGeneration, host, "vm-two-port-delete-shutoff-job-"+suffix, shutdownCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, db, host, shutdownCommand, "vm-two-port-delete-shutoff-verification-"+suffix, powerObservationGeneration+1, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"}, "SUCCEEDED")
	deleting, err := StartVMAggregateDelete(ctx, db, VMAggregateDeleteRequest{RequestID: "vm-two-port-delete-request-" + suffix, OperationID: "vm-two-port-delete-operation-" + suffix, VMID: vmID, ExpectedRevision: vmRevision})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimVMAggregateLifecycle(ctx, db, deleting.OperationID, "vm-two-port-delete-worker-"+suffix, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	domainCommand := "vm-two-port-delete-domain-command-" + suffix
	if _, err = AuthorizeVMAggregateDeleteDomainCommand(ctx, db, claim, "vm-two-port-delete-domain-job-"+suffix, domainCommand); err != nil {
		t.Fatal(err)
	}
	var planDigest, backendDigest string
	var materializationGeneration uint64
	if err = db.QueryRow(ctx, `SELECT payload->>'source_plan_digest',(payload->>'source_materialization_generation')::bigint,payload->>'backend_identity_digest' FROM kim.execution_commands WHERE command_id=$1`, domainCommand).Scan(&planDigest, &materializationGeneration, &backendDigest); err != nil {
		t.Fatal(err)
	}
	domainVerification := "vm-two-port-delete-domain-verification-" + suffix
	domainAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, domainCommand, domainVerification, "MATCHED", 1, map[string]any{"cleanup_operation_id": deleting.OperationID, "cleanup_generation": 1, "domain_uuid": vmID, "vm_generation": vmGeneration, "source_host_id": host, "source_plan_digest": planDigest, "source_materialization_generation": materializationGeneration, "backend_identity_digest": backendDigest, "domain_present": false, "domain_running": false, "identity_matches": true})
	domainAbsenceID := "vm-two-port-delete-domain-absence-" + suffix
	if err = RecordVMAggregateDeleteDomainAbsence(ctx, db, claim, domainAbsenceID, domainCommand, domainVerification, uint32(domainAttempt), 1, digestBytes([]byte(domainCommand+"/observation")), digestBytes([]byte(domainCommand+"/verifier"))); err != nil {
		t.Fatal(err)
	}
	if _, err = AuthorizeVMAggregateDeletePortRetirementAt(ctx, db, claim, domainAbsenceID, 2); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("unqualified Port ordinal accepted: %v", err)
	}

	absenceIDs := make([]string, 2)
	for ordinal := 0; ordinal < 2; ordinal++ {
		retirement, err := AuthorizeVMAggregateDeletePortRetirementAt(ctx, db, claim, domainAbsenceID, ordinal)
		if err != nil {
			t.Fatal(err)
		}
		owner := fmt.Sprintf("vm-two-port-delete-network-worker-%d-%s", ordinal, suffix)
		claims, err := ClaimOVNRuntimeWork(ctx, db, OVNRuntimeClaimRequest{Owner: owner, Limit: 2, Lease: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		var retirementClaim OVNRuntimeClaim
		for _, candidate := range claims {
			if candidate.WorkID == retirement.WorkID {
				retirementClaim = OVNRuntimeClaim{WorkID: candidate.WorkID, Owner: owner, ClaimGeneration: candidate.ClaimGeneration}
			}
		}
		if retirementClaim.WorkID == "" {
			t.Fatalf("Port %d retirement work not claimed: %+v", ordinal, claims)
		}
		retirementEvidenceID := fmt.Sprintf("vm-two-port-delete-retirement-evidence-%d-%s", ordinal, suffix)
		if err = CompleteOVNPortBindingRetirement(ctx, db, retirementClaim, OVNPortBindingRetirementObservation{EvidenceID: retirementEvidenceID, IntentID: retirement.IntentID, IntentGeneration: retirement.IntentGeneration, PortID: retirement.PortID, PortGeneration: retirement.PortGeneration, BindingGeneration: retirement.BindingGeneration, SourceHostID: retirement.SourceHostID, OperationGeneration: retirement.OperationGeneration, NBObservationGeneration: 2, SBObservationGeneration: 2, OVSObservationGeneration: 2, NBObservationDigest: digestBytes([]byte(retirementEvidenceID + "/nb")), SBObservationDigest: digestBytes([]byte(retirementEvidenceID + "/sb")), OVSObservationDigest: digestBytes([]byte(retirementEvidenceID + "/ovs")), AdapterArtifactDigest: digestBytes([]byte(retirementEvidenceID + "/adapter")), ApplyResponseState: "RECEIVED", Observation: verifiedOVNRetirementObservation()}); err != nil {
			t.Fatal(err)
		}
		absenceIDs[ordinal] = fmt.Sprintf("vm-two-port-delete-network-absence-%d-%s", ordinal, suffix)
		if err = RecordVMAggregateDeleteNetworkPortAbsence(ctx, db, claim, ordinal, absenceIDs[ordinal], retirementEvidenceID); err != nil {
			t.Fatal(err)
		}
		if ordinal == 0 {
			if err = FinalizeVMAggregateDeleteNetworkAbsenceSet(ctx, db, claim, "vm-two-port-delete-network-set-incomplete-"+suffix); !errors.Is(err, ErrVMAggregateConflict) {
				t.Fatalf("partial network absence set accepted: %v", err)
			}
		}
	}
	networkSetID := "vm-two-port-delete-network-set-" + suffix
	if err = FinalizeVMAggregateDeleteNetworkAbsenceSet(ctx, db, claim, networkSetID); err != nil {
		t.Fatal(err)
	}
	if err = FinalizeVMAggregateDeleteNetworkAbsenceSet(ctx, db, claim, networkSetID); err != nil {
		t.Fatalf("network set replay: %v", err)
	}

	rootCommand := "vm-two-port-delete-root-command-" + suffix
	if _, err = AuthorizeVMAggregateDeleteRootAbsenceReadBack(ctx, db, claim, domainAbsenceID, "vm-two-port-delete-root-job-"+suffix, rootCommand); err != nil {
		t.Fatal(err)
	}
	var volumeID, attachmentID, bindingID, lvUUID string
	var attachmentGeneration, bindingGeneration uint64
	if err = db.QueryRow(ctx, `SELECT root_volume_id,root_attachment_id,root_attachment_generation,root_binding_id,root_binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=root_binding_id AND binding_generation=root_binding_generation) FROM kim.vm_delete_operation_evidence WHERE delete_operation_id=$1`, deleting.OperationID).Scan(&volumeID, &attachmentID, &attachmentGeneration, &bindingID, &bindingGeneration, &lvUUID); err != nil {
		t.Fatal(err)
	}
	rootVerification := "vm-two-port-delete-root-verification-" + suffix
	rootAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, rootCommand, rootVerification, "MATCHED", 1, map[string]any{"attachment_id": attachmentID, "volume_id": volumeID, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": lvUUID, "desired_state": libvirtvolume.StateDetached, "device_present": false, "device_identity_matches": false, "source_identity_matches": false, "holder_open": false, "read_only": false})
	rootObservation := "vm-two-port-delete-root-observation-" + suffix
	if err = AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: rootObservation, AttachmentID: attachmentID, VolumeID: volumeID, BindingID: bindingID, HostID: host, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvUUID, DesiredState: libvirtvolume.StateDetached, CommandID: rootCommand, VerificationID: rootVerification, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", AttachmentGeneration: attachmentGeneration, BindingGeneration: bindingGeneration, ObservationGeneration: 1, AttemptIndex: uint32(rootAttempt)}); err != nil {
		t.Fatal(err)
	}
	var rootDesiredDigest string
	if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$1`, volumeID).Scan(&rootDesiredDigest); err != nil {
		t.Fatal(err)
	}
	var dataObservation, dataAbsenceID, dataBindingID, dataDesiredDigest string
	var dataBindingGeneration uint64
	if dataVolume != nil {
		dataCommand := "vm-two-port-delete-data-command-" + suffix
		if _, err = AuthorizeVMAggregateDeleteDataAbsenceReadBack(ctx, db, claim, domainAbsenceID, "vm-two-port-delete-data-job-"+suffix, dataCommand); err != nil {
			t.Fatal(err)
		}
		var dataVolumeID, dataAttachmentID, dataLVUUID string
		var dataAttachmentGeneration uint64
		if err = db.QueryRow(ctx, `SELECT volume_id,physical_attachment_id,physical_attachment_generation,binding_id,binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=kim.vm_delete_data_volume_operation_evidence.binding_id AND binding_generation=kim.vm_delete_data_volume_operation_evidence.binding_generation) FROM kim.vm_delete_data_volume_operation_evidence WHERE delete_operation_id=$1`, deleting.OperationID).Scan(&dataVolumeID, &dataAttachmentID, &dataAttachmentGeneration, &dataBindingID, &dataBindingGeneration, &dataLVUUID); err != nil {
			t.Fatal(err)
		}
		dataVerification := "vm-two-port-delete-data-verification-" + suffix
		dataAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, dataCommand, dataVerification, "MATCHED", 1, map[string]any{"attachment_id": dataAttachmentID, "volume_id": dataVolumeID, "domain_uuid": vmID, "target_device": "vdb", "observed_lv_uuid": dataLVUUID, "desired_state": libvirtvolume.StateDetached, "device_present": false, "device_identity_matches": false, "source_identity_matches": false, "holder_open": false, "read_only": false})
		dataObservation = "vm-two-port-delete-data-observation-" + suffix
		if err = AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: dataObservation, AttachmentID: dataAttachmentID, VolumeID: dataVolumeID, BindingID: dataBindingID, HostID: host, DomainUUID: vmID, TargetDevice: "vdb", ObservedLVUUID: dataLVUUID, DesiredState: libvirtvolume.StateDetached, CommandID: dataCommand, VerificationID: dataVerification, ObservationDigest: digestBytes([]byte(dataCommand + "/observation")), VerifierDigest: digestBytes([]byte(dataCommand + "/verifier")), EvidenceState: "MATCHED", AttachmentGeneration: dataAttachmentGeneration, BindingGeneration: dataBindingGeneration, ObservationGeneration: 1, AttemptIndex: uint32(dataAttempt)}); err != nil {
			t.Fatal(err)
		}
		dataAbsenceID = "vm-two-port-delete-data-absence-" + suffix
		if err = db.QueryRow(ctx, `SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$1`, dataVolumeID).Scan(&dataDesiredDigest); err != nil {
			t.Fatal(err)
		}
	}
	storageAbsenceID := "vm-two-port-delete-storage-absence-" + suffix
	releaseID := "vm-two-port-delete-compute-release-" + suffix
	terminalID, tombstoneID := "vm-two-port-delete-terminal-"+suffix, "vm-two-port-delete-tombstone-"+suffix
	complete := func(target TxBeginner, networkID, rootAbsence, dataAbsence, release, terminal, tombstone string) (string, error) {
		if dataVolume != nil {
			return CompleteVMAggregateDeleteWithNetworkAndData(ctx, target, claim, domainAbsenceID, rootObservation, networkID, dataObservation, rootAbsence, dataAbsence, release, terminal, tombstone)
		}
		return CompleteVMAggregateDeleteWithNetwork(ctx, target, claim, domainAbsenceID, rootObservation, networkID, rootAbsence, release, terminal, tombstone)
	}
	if _, err = complete(db, absenceIDs[0], storageAbsenceID+"-member", dataAbsenceID+"-member", releaseID+"-member", terminalID+"-member", tombstoneID+"-member"); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("single Port member accepted as network set: %v", err)
	}
	if dataVolume != nil {
		if _, err = CompleteVMAggregateDeleteWithNetwork(ctx, db, claim, domainAbsenceID, rootObservation, networkSetID, storageAbsenceID+"-missing-data", releaseID+"-missing-data", terminalID+"-missing-data", tombstoneID+"-missing-data"); !errors.Is(err, ErrVMAggregateConflict) {
			t.Fatalf("maximum profile accepted without DATA absence: %v", err)
		}
	}
	driftRollback := errors.New("rollback second Port binding drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_state='RELEASE_PENDING' WHERE port_id=$1`, ports[1].PortID); err != nil {
			return err
		}
		if _, err := complete(scopeTxBeginner{tx}, networkSetID, storageAbsenceID+"-drift", dataAbsenceID+"-drift", releaseID+"-drift", terminalID+"-drift", tombstoneID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("second Port binding drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	if dataVolume != nil {
		dataDriftRollback := errors.New("rollback maximum profile DATA binding drift")
		err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1 AND binding_generation=$2`, dataBindingID, dataBindingGeneration); err != nil {
				return err
			}
			if _, err := complete(scopeTxBeginner{tx}, networkSetID, storageAbsenceID+"-data-drift", dataAbsenceID+"-data-drift", releaseID+"-data-drift", terminalID+"-data-drift", tombstoneID+"-data-drift"); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("maximum profile DATA binding drift accepted: %v", err)
			}
			return dataDriftRollback
		})
		if !errors.Is(err, dataDriftRollback) {
			t.Fatal(err)
		}
	}
	if terminal, err := complete(db, networkSetID, storageAbsenceID, dataAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("two-Port delete terminal=%s err=%v", terminal, err)
	}
	if terminal, err := complete(db, networkSetID, storageAbsenceID, dataAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("two-Port delete replay=%s err=%v", terminal, err)
	}
	for i := range ports {
		var attachmentState, bindingState, desiredDigest string
		var workloadID, admissionID *string
		if err = db.QueryRow(ctx, `SELECT p.attachment_state,b.binding_state,p.desired_digest,p.workload_id,p.placement_admission_id FROM kim.network_ports_current p JOIN kim.port_bindings_current b USING(port_id) WHERE p.port_id=$1`, ports[i].PortID).Scan(&attachmentState, &bindingState, &desiredDigest, &workloadID, &admissionID); err != nil || attachmentState != "UNATTACHED" || bindingState != "RELEASED" || desiredDigest != logicalPortDigests[i] || workloadID != nil || admissionID != nil {
			t.Fatalf("logical Port %d attachment=%s binding=%s digest=%s workload=%v admission=%v err=%v", i, attachmentState, bindingState, desiredDigest, workloadID, admissionID, err)
		}
	}
	if dataVolume != nil {
		var retired, available, verified, capacityHeld int
		var resultingRootDigest, resultingDataDigest string
		if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.volume_attachment_intents_current WHERE workload_id=$1 AND intent_state='RETIRED'),(SELECT count(*) FROM kim.volumes_current WHERE volume_id IN($2,$3) AND lifecycle_state='AVAILABLE'),(SELECT count(*) FROM kim.volume_materializations_current WHERE volume_id IN($2,$3) AND materialization_state='VERIFIED'),(SELECT count(*) FROM kim.storage_capacity_claims WHERE volume_id IN($2,$3) AND claim_state IN('RESERVED','ALLOCATED')),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$2),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$3)`, vmID, volumeID, dataVolume.VolumeID).Scan(&retired, &available, &verified, &capacityHeld, &resultingRootDigest, &resultingDataDigest); err != nil || retired != 2 || available != 2 || verified != 2 || capacityHeld != 2 || resultingRootDigest != rootDesiredDigest || resultingDataDigest != dataDesiredDigest {
			t.Fatalf("maximum profile storage retired=%d available=%d verified=%d capacity=%d err=%v", retired, available, verified, capacityHeld, err)
		}
	}
	for _, table := range []string{"vm_delete_network_port_operation_evidence", "vm_delete_network_port_absence_evidence", "vm_delete_network_absence_set_evidence"} {
		if _, err = db.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
}
