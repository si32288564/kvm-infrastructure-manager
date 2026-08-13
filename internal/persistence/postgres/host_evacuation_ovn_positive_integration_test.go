package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

func completeEvacuationOVNIntent(t *testing.T, ctx context.Context, db recoveryQualificationDB, intentID, portID, owner, suffix string, generation uint64) {
	t.Helper()
	decision, err := CommitOVNPortIntent(ctx, db, OVNPortIntentRequest{IntentID: intentID, IntentGeneration: generation, PortID: portID})
	if err != nil {
		t.Fatal(err)
	}
	work, err := ClaimOVNRuntimeWork(ctx, db, OVNRuntimeClaimRequest{Owner: owner, Limit: 1, Lease: time.Minute, MaximumLifetime: 2 * time.Minute})
	if err != nil || len(work) != 1 || work[0].WorkID != fmt.Sprintf("ovn-runtime:%s:%d", intentID, generation) {
		t.Fatalf("OVN claim=%+v err=%v", work, err)
	}
	claim := OVNRuntimeClaim{WorkID: work[0].WorkID, Owner: owner, ClaimGeneration: work[0].ClaimGeneration}
	if err := AuthorizeOVNRuntimeApply(ctx, db, claim); err != nil {
		t.Fatal(err)
	}
	observation := OVNPortObservation{
		NBObservationID: "ovn-nb-" + suffix, SBObservationID: "ovn-sb-" + suffix,
		IntentID: intentID, IntentGeneration: generation, PortID: portID,
		PortGeneration: decision.PortGeneration, BindingGeneration: decision.BindingGeneration,
		NBObservationGeneration: generation, SBObservationGeneration: generation,
		NBObservationDigest: digestBytes([]byte("ovn-nb-" + suffix)), SBObservationDigest: digestBytes([]byte("ovn-sb-" + suffix)),
		AdapterArtifactDigest: digestBytes([]byte("ovn-adapter-" + suffix)), ChassisIdentityDigest: digestBytes([]byte("ovn-chassis-" + suffix)),
		ApplyResponseState: "RECEIVED",
		Observation:        ovnadapter.Observation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, PortBindingPresent: true, DatapathPresent: true, ExpectedChassisMatches: true},
	}
	if err := CompleteOVNRuntimeWork(ctx, db, claim, observation); err != nil {
		t.Fatal(err)
	}
}

func realizeEvacuationOVSPort(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID, vmID, planID, portID, networkID, segmentID, mac, suffix, powerJobID, powerCommandID string, portGeneration, bindingGeneration, observationGeneration uint64) string {
	t.Helper()
	request := OVSPortRealizationRequest{VMID: vmID, PlanID: planID, PortID: portID, JobID: "ovs-realize-job-" + suffix, CommandID: "ovs-realize-command-" + suffix}
	if _, err := PrepareOVSPortRealization(ctx, db, request); err != nil {
		t.Fatal(err)
	}
	verificationID := "ovs-realize-verification-" + suffix
	evidence := map[string]any{"domain_uuid": vmID, "vm_generation": float64(1), "port_id": portID, "port_generation": float64(portGeneration), "network_id": networkID, "network_generation": float64(1), "segment_claim_id": segmentID, "segment_generation": float64(1), "host_mapping_generation": float64(1), "binding_generation": float64(bindingGeneration), "binding_type": "OVS", "mac_address": mac, "interface_id": portID, "domain_nic_identity_matches": true, "bridge_observed": true}
	attempt := acceptEvacuationCommand(t, ctx, db, hostID, request.CommandID, verificationID, observationGeneration, evidence, "SUCCEEDED")
	evidenceID := "ovs-realize-evidence-" + suffix
	if err := AcceptOVSPortRealizationAndMaybeArmPower(ctx, db, OVSPortRealizationObservation{EvidenceID: evidenceID, VMID: vmID, VMGeneration: 1, PlanID: planID, HostID: hostID, PortID: portID, PortGeneration: portGeneration, NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: bindingGeneration, CommandID: request.CommandID, AttemptIndex: uint32(attempt), VerificationID: verificationID, ObservationGeneration: observationGeneration, ObservationDigest: digestBytes([]byte(request.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(request.CommandID + "/verifier")), PowerJobID: powerJobID, PowerCommandID: powerCommandID}); err != nil {
		t.Fatal(err)
	}
	return evidenceID
}

func convergeEvacuationOVSDataplane(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID, vmID, planID, portID, networkID, segmentID, mac, suffix string, portGeneration, bindingGeneration, observationGeneration uint64) string {
	t.Helper()
	request := OVSDataplaneRequest{VMID: vmID, PlanID: planID, PortID: portID, JobID: "ovs-dataplane-job-" + suffix, CommandID: "ovs-dataplane-command-" + suffix}
	if _, err := PrepareOVSDataplaneObservation(ctx, db, request); err != nil {
		t.Fatal(err)
	}
	verificationID := "ovs-dataplane-verification-" + suffix
	evidence := map[string]any{"domain_uuid": vmID, "vm_generation": float64(1), "port_id": portID, "port_generation": float64(portGeneration), "network_id": networkID, "network_generation": float64(1), "segment_claim_id": segmentID, "segment_generation": float64(1), "host_mapping_generation": float64(1), "binding_generation": float64(bindingGeneration), "binding_type": "OVS", "mac_address": mac, "domain_running": true, "interface_present": true, "target_device": "vnet-" + suffix, "bridge_observed": "br-int", "bridge_matches": true, "link_state": "up", "interface_id": portID}
	attempt := acceptEvacuationCommand(t, ctx, db, hostID, request.CommandID, verificationID, observationGeneration, evidence, "SUCCEEDED")
	evidenceID := "ovs-dataplane-evidence-" + suffix
	if err := AcceptOVSDataplaneObservation(ctx, db, OVSDataplaneObservation{EvidenceID: evidenceID, VMID: vmID, VMGeneration: 1, PlanID: planID, HostID: hostID, PortID: portID, PortGeneration: portGeneration, NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: bindingGeneration, CommandID: request.CommandID, AttemptIndex: uint32(attempt), VerificationID: verificationID, ObservationGeneration: observationGeneration, ObservationDigest: digestBytes([]byte(request.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(request.CommandID + "/verifier"))}); err != nil {
		t.Fatal(err)
	}
	return evidenceID
}

func TestHostEvacuationNonEmptyOneOVNPortPositivePostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('evacuation-ovn-positive',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	vmID := fmt.Sprintf("69000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	sourceHost, destinationHost, poolID := "evacuation-ovn-source-"+suffix, "evacuation-ovn-destination-"+suffix, "evacuation-ovn-pool-"+suffix
	workloadID, imageID, flavorID, storageClassID := "evacuation-ovn-workload-"+suffix, "evacuation-ovn-image-"+suffix, "evacuation-ovn-flavor-"+suffix, "evacuation-ovn-storage-"+suffix
	networkID, subnetID, segmentID, portID := "evacuation-ovn-network-"+suffix, "evacuation-ovn-subnet-"+suffix, "evacuation-ovn-segment-"+suffix, "evacuation-ovn-port-"+suffix
	mac, ip := "02:00:00:69:00:01", "192.0.2.69"
	prepareEvacuationHost(t, ctx, pool, sourceHost)
	prepareEvacuationHost(t, ctx, pool, destinationHost)
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: poolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "evacuation-ovn", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "evacuation-ovn-members-" + suffix, HostGroupID: poolID, SourceType: "EXPLICIT", SourceRevision: suffix, BasedOnHostGroupGeneration: 1, Members: []HostGroupMembership{{HostGroupID: poolID, HostID: sourceHost, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}, {HostGroupID: poolID, HostID: destinationHost, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_placement_pool_memberships_current(host_id,pool_id,membership_generation,membership_state) VALUES($1,$3,1,'ACTIVE'),($2,$3,1,'ACTIVE')`, sourceHost, destinationHost, poolID); err != nil {
		t.Fatal(err)
	}
	if err := UpsertNetworkFoundation(ctx, pool, NetworkFoundation{NetworkID: networkID, ProjectID: "project", NetworkGeneration: 1, NetworkState: "ACTIVE", MTU: 1500, SubnetID: subnetID, SubnetGeneration: 1, SubnetState: "ACTIVE", CIDR: "192.0.2.0/24", AllocationStart: "192.0.2.10", AllocationEnd: "192.0.2.200", SegmentClaimID: segmentID, SegmentType: "VLAN", ScopeID: "ovn-" + suffix, SegmentID: 690, SegmentGeneration: 1, ProviderMappingRevision: 1, SegmentState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{sourceHost, destinationHost} {
		if err := UpsertHostNetworkMapping(ctx, pool, HostNetworkMapping{HostID: host, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "chassis-" + host}); err != nil {
			t.Fatal(err)
		}
	}
	for index, host := range []string{sourceHost, destinationHost} {
		if err := RegisterLocalLVMFoundation(ctx, pool, LocalLVMFoundation{BackendID: fmt.Sprintf("evacuation-ovn-backend-%d-%s", index+1, suffix), HostID: host, VGUUID: fmt.Sprintf("evacuation-ovn-vg-%d-%s", index+1, suffix), BackendState: "ACTIVE", BackendGeneration: 1, CapabilityState: "CURRENT", HostCapabilityGeneration: 1, SupportTier: "VALIDATED", StorageClassID: storageClassID, StorageClassRevision: 1, ClassState: "ACTIVE", FencingPolicyRevision: 1, CapacityObservationID: fmt.Sprintf("evacuation-ovn-capacity-%d-%s", index+1, suffix), CapacityGeneration: 1, CapacityState: "CURRENT", HealthState: "HEALTHY", TotalBytes: 1 << 30, ObservedFreeBytes: 1 << 30, ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	checksum := digestBytes([]byte("evacuation-ovn-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "qualification://evacuation-ovn", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "evacuation-ovn", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	sourceRequest := PlacementAdmissionRequest{RequestID: "evacuation-ovn-source-" + suffix, ProjectID: "project", WorkloadID: workloadID, ImageID: imageID, FlavorID: flavorID, PoolID: poolID, Network: []placement.NetworkRequirement{{PortID: portID, NetworkID: networkID, NetworkGeneration: 1, SubnetID: subnetID, SubnetGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, IPAddress: ip, MACAddress: mac, BindingType: "OVS", RequiredMTU: 1500}}, Storage: []placement.StorageRequirement{{VolumeID: "evacuation-ovn-source-root-" + suffix, AttachmentID: "evacuation-ovn-source-attachment-" + suffix, BackendID: "evacuation-ovn-backend-1-" + suffix, BackendGeneration: 1, VGUUID: "evacuation-ovn-vg-1-" + suffix, StorageClassID: storageClassID, StorageClassRevision: 1, CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1, SizeBytes: 16 << 20, AccessMode: "SINGLE_WRITER", Bootable: true}}}
	sourceDry, err := DryEvaluatePlacement(ctx, pool, sourceRequest, sourceHost)
	if err != nil || !sourceDry.Eligible {
		t.Fatalf("source dry=%+v err=%v", sourceDry, err)
	}
	sourceAdmission, err := FinalAdmitPlacement(ctx, pool, sourceRequest, sourceDry)
	if err != nil {
		t.Fatal(err)
	}
	sourceBinding, sourceLV := realizeEvacuationBinding(t, ctx, pool, sourceHost, sourceAdmission.AdmissionID, sourceRequest.Storage[0].VolumeID, sourceRequest.Storage[0].BackendID, sourceRequest.Storage[0].VGUUID, sourceRequest.RequestID)
	sourcePlan := "evacuation-ovn-source-plan-" + suffix
	materializeEvacuationVM(t, ctx, pool, sourceHost, vmID, sourceAdmission.AdmissionID, sourcePlan, imageID, checksum, sourceRequest.Storage[0].VolumeID, sourceBinding, sourceRequest.Storage[0].VGUUID, sourceLV, "ovn-source-"+suffix, "", 1)
	completeEvacuationOVNIntent(t, ctx, pool, "evacuation-ovn-source-intent-"+suffix, portID, "ovn-source-worker-"+suffix, "source-"+suffix, 1)
	realizeEvacuationOVSPort(t, ctx, pool, sourceHost, vmID, sourcePlan, portID, networkID, segmentID, mac, "source-"+suffix, "source-power-job-"+suffix, "source-power-command-"+suffix, 1, 1, 1)
	acceptEvacuationCommand(t, ctx, pool, sourceHost, "source-power-command-"+suffix, "source-power-verification-"+suffix, 1, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")
	convergeEvacuationOVSDataplane(t, ctx, pool, sourceHost, vmID, sourcePlan, portID, networkID, segmentID, mac, "source-"+suffix, 1, 1, 1)

	beforeEpochs, beforeFencing := 0, 0
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&beforeEpochs, &beforeFencing); err != nil {
		t.Fatal(err)
	}
	operationID := "evacuation-ovn-positive-" + suffix
	operation, children, err := StartHostEvacuation(ctx, pool, HostEvacuationRequest{OperationID: operationID, SourceHostID: sourceHost, EvacuationGeneration: 1, SourceHostAuthorityGeneration: 1, DrainPolicyID: "planned", DrainPolicyRevision: 1, EvacuationPolicyRevision: 1, MaximumConcurrentWorkloads: 1, Reason: "one OVN Port qualification", RequestedBy: "integration"})
	if err != nil || operation.WorkloadCount != 1 || len(children) != 1 {
		t.Fatalf("operation=%+v children=%+v err=%v", operation, children, err)
	}
	var snapshotPorts int
	var snapshotIdentity bool
	if err := pool.QueryRow(ctx, `SELECT jsonb_array_length(network_requirements),(network_requirements->0->>'PortID')=$2 AND (network_requirements->0->>'NetworkID')=$3 AND (network_requirements->0->>'SubnetID')=$4 AND (network_requirements->0->>'MACAddress')=$5 AND (network_requirements->0->>'IPAddress')=$6 FROM kim.host_evacuation_workload_evidence WHERE child_operation_id=$1`, children[0].ChildOperationID, portID, networkID, subnetID, mac, ip).Scan(&snapshotPorts, &snapshotIdentity); err != nil || snapshotPorts != 1 || !snapshotIdentity {
		t.Fatalf("snapshot Ports=%d identity=%v err=%v", snapshotPorts, snapshotIdentity, err)
	}
	if err := EvaluateHostEvacuationEligibility(ctx, pool, operationID); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimHostEvacuationWorkload(ctx, pool, operationID, "ovn-positive-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	shutdownCommand := "evacuation-ovn-shutdown-command-" + suffix
	if err := AuthorizeHostEvacuationSourceShutdown(ctx, pool, claim, "evacuation-ovn-shutdown-authority-"+suffix, "evacuation-ovn-shutdown-job-"+suffix, shutdownCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationLostReadBack(t, ctx, pool, sourceHost, shutdownCommand, "evacuation-ovn-shutdown-verification-"+suffix, 2, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"})
	plannedQuiescenceID := "evacuation-ovn-planned-quiescence-" + suffix
	if _, err := RecordPlannedSourceQuiescence(ctx, pool, claim, plannedQuiescenceID); err != nil {
		t.Fatal(err)
	}
	rootCommand, rootVerification := "evacuation-ovn-root-readback-command-"+suffix, "evacuation-ovn-root-readback-verification-"+suffix
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "evacuation-ovn-root-readback-job-" + suffix, CommandID: rootCommand, HostID: sourceHost, ResourceType: "VOLUME_ATTACHMENT", ResourceID: sourceRequest.Storage[0].AttachmentID, DesiredRevision: 1, CommandType: SourceRootSafetyReadBackCommandType, SchemaVersion: SourceRootSafetyReadBackSchema, TargetResourceID: "attachment:" + sourceRequest.Storage[0].AttachmentID, Payload: map[string]any{"desired_state": "OBSERVE"}}); err != nil {
		t.Fatal(err)
	}
	rootEvidence := map[string]any{"attachment_id": sourceRequest.Storage[0].AttachmentID, "volume_id": sourceRequest.Storage[0].VolumeID, "binding_id": sourceBinding, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": sourceLV, "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": false}
	rootAttempt := acceptEvacuationCommand(t, ctx, pool, sourceHost, rootCommand, rootVerification, 1, rootEvidence, "SUCCEEDED")
	if err := AcceptSourceRootSafetyObservation(ctx, pool, LocalLVMAttachmentObservation{EvidenceID: "evacuation-ovn-root-observation-" + suffix, AttachmentID: sourceRequest.Storage[0].AttachmentID, VolumeID: sourceRequest.Storage[0].VolumeID, AttachmentGeneration: 1, BindingID: sourceBinding, BindingGeneration: 1, HostID: sourceHost, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: sourceLV, CommandID: rootCommand, VerificationID: rootVerification, AttemptIndex: uint32(rootAttempt), ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	safetyID := "evacuation-ovn-storage-safety-" + suffix
	if err := EvaluateHostEvacuationSourceStorageSafety(ctx, pool, claim, safetyID); err != nil {
		t.Fatal(err)
	}
	retirementOperationID, retirementIntentID := "evacuation-ovn-retirement-"+suffix, "evacuation-ovn-retirement-intent-"+suffix
	retirement, err := AuthorizeHostEvacuationNetworkPortRetirement(ctx, pool, claim, HostEvacuationNetworkRetirementRequest{AuthorityID: "evacuation-ovn-retirement-authority-" + suffix, PortID: portID, OperationID: retirementOperationID, OperationGeneration: 1, IntentID: retirementIntentID, IntentGeneration: 2})
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := AuthorizeHostEvacuationNetworkPortRetirement(ctx, pool, claim, HostEvacuationNetworkRetirementRequest{AuthorityID: "evacuation-ovn-retirement-authority-" + suffix, PortID: portID, OperationID: retirementOperationID, OperationGeneration: 1, IntentID: retirementIntentID, IntentGeneration: 2}); err != nil || replay.WorkID != retirement.WorkID {
		t.Fatalf("retirement authorization replay=%+v err=%v", replay, err)
	}
	retirementWork, err := ClaimOVNRuntimeWork(ctx, pool, OVNRuntimeClaimRequest{Owner: "ovn-retirement-worker-" + suffix, Limit: 1, Lease: time.Minute})
	if err != nil || len(retirementWork) != 1 || retirementWork[0].WorkID != retirement.WorkID {
		t.Fatalf("retirement work=%+v err=%v", retirementWork, err)
	}
	retirementClaim := OVNRuntimeClaim{WorkID: retirement.WorkID, Owner: "ovn-retirement-worker-" + suffix, ClaimGeneration: retirementWork[0].ClaimGeneration}
	if err := AuthorizeOVNRuntimeApply(ctx, pool, retirementClaim); err != nil {
		t.Fatal(err)
	}
	retirementEvidenceID := "evacuation-ovn-retirement-evidence-" + suffix
	if err := CompleteOVNPortBindingRetirement(ctx, pool, retirementClaim, OVNPortBindingRetirementObservation{EvidenceID: retirementEvidenceID, IntentID: retirementIntentID, IntentGeneration: 2, PortID: portID, PortGeneration: 1, BindingGeneration: 1, SourceHostID: sourceHost, OperationGeneration: 1, NBObservationGeneration: 2, SBObservationGeneration: 2, OVSObservationGeneration: 2, NBObservationDigest: digestBytes([]byte("retirement-nb-" + suffix)), SBObservationDigest: digestBytes([]byte("retirement-sb-" + suffix)), OVSObservationDigest: digestBytes([]byte("retirement-ovs-" + suffix)), AdapterArtifactDigest: digestBytes([]byte("retirement-adapter-" + suffix)), ApplyResponseState: "RECEIVED", Observation: ovnadapter.PortBindingRetirementObservation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, RequestedChassisAbsent: true, SourceChassisInactive: true, SourceOVSInterfaceAbsent: true}}); err != nil {
		t.Fatal(err)
	}
	networkQuiescence, err := PrepareHostEvacuationNetworkPortSourceQuiescence(ctx, pool, claim, HostEvacuationNetworkQuiescenceRequest{PortID: portID, JobID: "evacuation-ovn-port-quiescence-job-" + suffix, CommandID: "evacuation-ovn-port-quiescence-command-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	networkQuiescenceVerification := "evacuation-ovn-port-quiescence-verification-" + suffix
	networkQuiescenceAttempt := acceptEvacuationCommand(t, ctx, pool, sourceHost, networkQuiescence.CommandID, networkQuiescenceVerification, 2, map[string]any{"domain_uuid": vmID, "vm_generation": float64(1), "port_id": portID, "port_generation": float64(1), "binding_generation": float64(1), "domain_running": false, "interface_present": false}, "SUCCEEDED")
	networkQuiescenceID := "evacuation-ovn-port-quiescence-evidence-" + suffix
	if err := AcceptHostEvacuationNetworkPortSourceQuiescence(ctx, pool, claim, HostEvacuationNetworkQuiescenceObservation{EvidenceID: networkQuiescenceID, PortID: portID, SourceHostID: sourceHost, VMID: vmID, CommandID: networkQuiescence.CommandID, VerificationID: networkQuiescenceVerification, PortGeneration: 1, BindingGeneration: 1, VMGeneration: 1, ObservationGeneration: 2, AttemptIndex: uint32(networkQuiescenceAttempt), ObservationDigest: digestBytes([]byte(networkQuiescence.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(networkQuiescence.CommandID + "/verifier"))}); err != nil {
		t.Fatal(err)
	}
	if err := AcceptHostEvacuationNetworkPortSourceQuiescence(ctx, pool, claim, HostEvacuationNetworkQuiescenceObservation{EvidenceID: networkQuiescenceID, PortID: portID, SourceHostID: sourceHost, VMID: vmID, CommandID: networkQuiescence.CommandID, VerificationID: networkQuiescenceVerification, PortGeneration: 1, BindingGeneration: 1, VMGeneration: 1, ObservationGeneration: 2, AttemptIndex: uint32(networkQuiescenceAttempt), ObservationDigest: digestBytes([]byte(networkQuiescence.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(networkQuiescence.CommandID + "/verifier"))}); err != nil {
		t.Fatalf("Network source quiescence replay: %v", err)
	}
	var sourceRetiredWithoutDestination bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_port_binding_retirements_current r JOIN kim.network_port_binding_retirement_evidence e ON e.evidence_id=r.terminal_evidence_id WHERE r.port_id=$1 AND r.retirement_state='VERIFIED' AND e.source_chassis_inactive AND e.source_ovs_interface_absent) AND NOT EXISTS(SELECT 1 FROM kim.port_binding_handoff_evidence WHERE port_id=$1)`, portID).Scan(&sourceRetiredWithoutDestination); err != nil || !sourceRetiredWithoutDestination {
		t.Fatalf("source/destination dataplane overlap fence=%v err=%v", sourceRetiredWithoutDestination, err)
	}
	releaseID := "evacuation-ovn-source-release-" + suffix
	if err := ReleaseHostEvacuationSourcePlacement(ctx, pool, claim, releaseID, safetyID); err != nil {
		t.Fatal(err)
	}
	destinationRequest, err := BuildHostEvacuationDestinationPlacementRequest(ctx, pool, claim, "evacuation-ovn-destination-"+suffix, destinationHost)
	if err != nil || len(destinationRequest.Network) != 1 {
		t.Fatalf("destination request=%+v err=%v", destinationRequest, err)
	}
	destinationDry, err := DryEvaluatePlacement(ctx, pool, destinationRequest, destinationHost)
	if err != nil || !destinationDry.Eligible {
		t.Fatalf("destination dry=%+v err=%v", destinationDry, err)
	}
	destinationAdmission, err := FinalAdmitPlacement(ctx, pool, destinationRequest, destinationDry)
	if err != nil {
		t.Fatal(err)
	}
	var finalPortID, finalMAC, finalIP, portHost string
	var portGeneration, bindingGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT p.port_id,mac.mac_address::text,host(ip.ip_address),b.host_id,p.port_generation,b.binding_generation FROM kim.network_ports_current p JOIN kim.port_bindings_current b ON b.port_id=p.port_id JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' WHERE p.port_id=$1`, portID).Scan(&finalPortID, &finalMAC, &finalIP, &portHost, &portGeneration, &bindingGeneration); err != nil || finalPortID != portID || finalMAC != mac || finalIP != ip || portHost != destinationHost || portGeneration != 2 || bindingGeneration != 2 {
		t.Fatalf("handoff identity=%s/%s/%s host=%s generations=%d/%d err=%v", finalPortID, finalMAC, finalIP, portHost, portGeneration, bindingGeneration, err)
	}
	destinationBinding, destinationLV := realizeEvacuationBinding(t, ctx, pool, destinationHost, destinationAdmission.AdmissionID, destinationRequest.Storage[0].VolumeID, destinationRequest.Storage[0].BackendID, destinationRequest.Storage[0].VGUUID, destinationRequest.RequestID)
	qualifyEvacuationLocalLVMCopy(t, ctx, pool, claim, "ovn-"+suffix, destinationAdmission.AdmissionID, safetyID, digestBytes([]byte("ovn-guest-state/"+suffix)), false)
	relocationID := "evacuation-ovn-relocation-authority-" + suffix
	if err := AuthorizeHostEvacuationRelocation(ctx, pool, claim, relocationID, destinationAdmission.AdmissionID, safetyID, releaseID); err != nil {
		t.Fatal(err)
	}
	destinationPlan := "evacuation-ovn-destination-plan-" + suffix
	materializeEvacuationVM(t, ctx, pool, destinationHost, vmID, destinationAdmission.AdmissionID, destinationPlan, imageID, checksum, destinationRequest.Storage[0].VolumeID, destinationBinding, destinationRequest.Storage[0].VGUUID, destinationLV, "ovn-destination-"+suffix, relocationID, 2)
	if err := AuthorizeZeroPortVMPowerOn(ctx, pool, vmID, 1, destinationHost, "forged-early-power-job-"+suffix, "forged-early-power-command-"+suffix); !errors.Is(err, ErrVMMaterializationConflict) {
		t.Fatalf("power before Network convergence=%v", err)
	}
	completeEvacuationOVNIntent(t, ctx, pool, "evacuation-ovn-destination-intent-"+suffix, portID, "ovn-destination-worker-"+suffix, "destination-"+suffix, 3)
	destinationRealizationID := realizeEvacuationOVSPort(t, ctx, pool, destinationHost, vmID, destinationPlan, portID, networkID, segmentID, mac, "destination-"+suffix, "destination-power-job-"+suffix, "destination-power-command-"+suffix, 2, 2, 2)
	acceptEvacuationCommand(t, ctx, pool, destinationHost, "destination-power-command-"+suffix, "destination-power-verification-"+suffix, 3, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")
	if _, err := EvaluateHostEvacuationChildEvidence(ctx, pool, claim, "missing-dataplane-verification-"+suffix, "missing-dataplane-binding-"+suffix, destinationAdmission.AdmissionID); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("RUNNING without OVS dataplane verified child: %v", err)
	}
	destinationDataplaneID := convergeEvacuationOVSDataplane(t, ctx, pool, destinationHost, vmID, destinationPlan, portID, networkID, segmentID, mac, "destination-"+suffix, 2, 2, 2)
	verificationID, destinationEvidenceBindingID := "evacuation-ovn-child-verification-"+suffix, "evacuation-ovn-destination-binding-"+suffix
	assertVerificationBlocked := func(label, mutation string, args ...any) {
		t.Helper()
		rollback := errors.New("rollback " + label)
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, mutation, args...); err != nil {
				t.Fatal(err)
			}
			if _, err := EvaluateHostEvacuationChildEvidence(ctx, scopeTxBeginner{tx}, claim, verificationID+"-"+label, destinationEvidenceBindingID+"-"+label, destinationAdmission.AdmissionID); !errors.Is(err, ErrHostEvacuationBlocked) {
				t.Fatalf("%s Network drift verified child: %v", label, err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
	assertVerificationBlocked("sb-missing", `UPDATE kim.network_ovn_state_current SET sb_state='UNKNOWN',layer_status='NB_APPLIED' WHERE port_id=$1`, portID)
	assertVerificationBlocked("mac-drift", `UPDATE kim.network_identity_claims SET mac_address='02:00:00:69:00:ff' WHERE port_id=$1 AND claim_type='MAC'`, portID)
	assertVerificationBlocked("source-revival", `UPDATE kim.network_port_binding_retirements_current SET retirement_state='STALE' WHERE port_id=$1 AND port_generation=1 AND binding_generation=1`, portID)
	verification, err := EvaluateHostEvacuationChildEvidence(ctx, pool, claim, verificationID, destinationEvidenceBindingID, destinationAdmission.AdmissionID)
	if err != nil {
		t.Fatal(err)
	}
	var sourceNetworkState, destinationNetworkState string
	var networkBindingCount int
	if err := pool.QueryRow(ctx, `SELECT source_network_state,destination_network_state,network_binding_count FROM kim.host_evacuation_child_verification_evidence WHERE verification_id=$1`, verificationID).Scan(&sourceNetworkState, &destinationNetworkState, &networkBindingCount); err != nil || sourceNetworkState != "RETIRED" || destinationNetworkState != "CURRENT" || networkBindingCount != 1 {
		t.Fatalf("Network verification=%s/%s count=%d err=%v", sourceNetworkState, destinationNetworkState, networkBindingCount, err)
	}
	if replay, err := EvaluateHostEvacuationChildEvidence(ctx, pool, claim, verificationID, destinationEvidenceBindingID, destinationAdmission.AdmissionID); err != nil || replay.VerificationDigest != verification.VerificationDigest {
		t.Fatalf("verification replay=%+v err=%v", replay, err)
	}
	terminalID := "evacuation-ovn-child-terminal-" + suffix
	assertTerminalNetworkStale := func(label, mutation string, args ...any) {
		t.Helper()
		rollback := errors.New("rollback terminal " + label)
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, mutation, args...); err != nil {
				t.Fatal(err)
			}
			if err := CompleteHostEvacuationChild(ctx, scopeTxBeginner{tx}, claim, verificationID, terminalID+"-"+label); !errors.Is(err, ErrHostEvacuationStale) {
				t.Fatalf("terminal %s drift=%v", label, err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
	assertTerminalNetworkStale("port-generation", `UPDATE kim.network_ports_current SET port_generation=port_generation+1 WHERE port_id=$1`, portID)
	assertTerminalNetworkStale("binding-generation", `UPDATE kim.port_bindings_current SET binding_generation=binding_generation+1 WHERE port_id=$1`, portID)
	assertTerminalNetworkStale("handoff-current", `UPDATE kim.port_binding_handoffs_current SET handoff_state='STALE' WHERE port_id=$1`, portID)
	assertTerminalNetworkStale("realization-current", `UPDATE kim.vm_network_port_realizations_current SET observation_generation=observation_generation+1 WHERE port_id=$1`, portID)
	assertTerminalNetworkStale("realization-evidence", `UPDATE kim.vm_network_port_realizations_current SET evidence_id=$2 WHERE port_id=$1`, portID, "ovs-realize-evidence-source-"+suffix)
	assertTerminalNetworkStale("ovn-sb-evidence", `UPDATE kim.network_ovn_state_current SET sb_observation_id=$2 WHERE port_id=$1`, portID, "ovn-sb-source-"+suffix)
	assertTerminalNetworkStale("dataplane-current", `UPDATE kim.vm_port_dataplane_state_current SET observation_generation=observation_generation+1 WHERE port_id=$1`, portID)
	assertTerminalNetworkStale("dataplane-evidence", `UPDATE kim.vm_port_dataplane_state_current SET evidence_id=$2 WHERE port_id=$1`, portID, "ovs-dataplane-evidence-source-"+suffix)
	assertTerminalNetworkStale("destination-plan", `UPDATE kim.virtual_machines_current SET current_plan_id=$2 WHERE vm_id=$1`, vmID, sourcePlan)
	assertTerminalNetworkStale("power-generation", `UPDATE kim.vm_power_state_current SET observation_generation=observation_generation+1 WHERE vm_id=$1`, vmID)
	if err := CompleteHostEvacuationChild(ctx, pool, claim, verificationID, terminalID); err != nil {
		t.Fatal(err)
	}
	parentTerminalID := "evacuation-ovn-parent-terminal-" + suffix
	parent, err := FinalizeHostEvacuation(ctx, pool, operationID, parentTerminalID)
	if err != nil || parent.LifecycleState != "VERIFIED" || parent.WorkloadCount != 1 {
		t.Fatalf("parent=%+v err=%v", parent, err)
	}
	var drain, vmHost, power, currentPortHost, currentMAC, currentIP string
	var cleanupCount, afterEpochs, afterFencing int
	if err := pool.QueryRow(ctx, `SELECT d.drain_state,vm.host_id,power.observed_power_state,b.host_id,mac.mac_address::text,host(ip.ip_address),(SELECT count(*) FROM kim.backend_cleanup_operation_evidence WHERE origin_authority_id=$1),(SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence) FROM kim.host_placement_drains_current d JOIN kim.virtual_machines_current vm ON vm.vm_id=$3::uuid JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id JOIN kim.port_bindings_current b ON b.port_id=$4 JOIN kim.network_identity_claims mac ON mac.port_id=b.port_id AND mac.claim_type='MAC' JOIN kim.network_identity_claims ip ON ip.port_id=b.port_id AND ip.claim_type='IP' WHERE d.source_host_id=$2`, operationID, sourceHost, vmID, portID).Scan(&drain, &vmHost, &power, &currentPortHost, &currentMAC, &currentIP, &cleanupCount, &afterEpochs, &afterFencing); err != nil {
		t.Fatal(err)
	}
	if drain != "DRAINED" || vmHost != destinationHost || power != "RUNNING" || currentPortHost != destinationHost || currentMAC != mac || currentIP != ip || cleanupCount != 0 || beforeEpochs != afterEpochs || beforeFencing != afterFencing {
		t.Fatalf("final drain=%s vm=%s power=%s Port=%s %s/%s cleanup=%d failure=%d/%d->%d/%d", drain, vmHost, power, currentPortHost, currentMAC, currentIP, cleanupCount, beforeEpochs, beforeFencing, afterEpochs, afterFencing)
	}
	for label, statement := range map[string]string{
		"retirement":      `UPDATE kim.network_port_binding_retirement_evidence SET evidence_digest=$2 WHERE evidence_id=$1`,
		"quiescence":      `UPDATE kim.network_port_source_quiescence_evidence SET evidence_digest=$2 WHERE evidence_id=$1`,
		"handoff":         `UPDATE kim.port_binding_handoff_evidence SET handoff_digest=$2 WHERE handoff_id=$1`,
		"realization":     `UPDATE kim.vm_network_port_realization_evidence SET observation_digest=$2 WHERE evidence_id=$1`,
		"dataplane":       `UPDATE kim.vm_port_dataplane_observation_evidence SET observation_digest=$2 WHERE evidence_id=$1`,
		"network binding": `UPDATE kim.host_evacuation_child_network_evidence_binding SET evidence_set_digest=$2 WHERE verification_id=$1`,
	} {
		id := map[string]string{"retirement": retirementEvidenceID, "quiescence": networkQuiescenceID, "handoff": destinationRequest.Network[0].HandoffID, "realization": destinationRealizationID, "dataplane": destinationDataplaneID, "network binding": verificationID}[label]
		if _, err := pool.Exec(ctx, statement, id, digestBytes([]byte("forged-"+label))); err == nil {
			t.Fatalf("immutable %s accepted UPDATE", label)
		}
	}
}
