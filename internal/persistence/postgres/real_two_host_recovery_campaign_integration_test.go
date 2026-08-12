package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/ovsnetwork"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

const (
	realCampaignSource      = "kvm-base-g01-n001-p.core.s01.si1230.com"
	realCampaignDestination = "kvm-base-g02-n001-p.core.s01.si1230.com"
	realCampaignVMUUID      = "f1c06a00-0000-4000-8000-202608120058"
)

type realCampaignHost struct {
	Host, HelperPath, NetworkWorkerPath, VGName, VGUUID, CacheRoot, StateRoot, OVSBridge string
}

type realCampaignExecution struct {
	CommandID, ResultID, VerificationID string
	LeaseGeneration                     int64
	AttemptIndex                        int
	Observation                         contract.Observation
	VerifierDigest                      string
}

func executeRealCampaignCommand(t *testing.T, ctx context.Context, pool QueryRower, databaseURL, runner string, host realCampaignHost, commandID, label string) realCampaignExecution {
	t.Helper()
	request := map[string]any{"command_id": commandID, "message_id": "real-recovery-campaign/" + label + "/message", "verification_id": "real-recovery-campaign/" + label + "/verification", "host": host.Host, "helper_path": host.HelperPath, "vg_name": host.VGName, "vg_uuid": host.VGUUID, "cache_root": host.CacheRoot, "state_root": host.StateRoot}
	var commandType string
	if err := pool.QueryRow(ctx, `SELECT command_type FROM kim.execution_commands WHERE command_id=$1`, commandID).Scan(&commandType); err != nil {
		t.Fatalf("load command type for %s: %v", label, err)
	}
	if commandType == ovsnetwork.CommandType || commandType == ovsnetwork.DataplaneCommandType {
		request["ovs_bridge"] = host.OVSBridge
	}
	encoded, _ := json.Marshal(request)
	command := exec.CommandContext(ctx, runner)
	command.Stdin = bytes.NewReader(encoded)
	command.Env = append(os.Environ(), "KIM_POSTGRES_TEST_URL="+databaseURL, "KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("real campaign command %s: %v stderr=%s", label, err, stderr.String())
	}
	var out realCampaignExecution
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode real campaign command %s: %v output=%s", label, err, stdout.String())
	}
	if out.CommandID != commandID || out.AttemptIndex < 1 || out.LeaseGeneration < 1 || out.Observation.State != "MATCHED" {
		t.Fatalf("real campaign command %s identity=%+v", label, out)
	}
	if err := pool.QueryRow(ctx, `SELECT verifier_artifact_digest FROM kim.command_verification_evidence WHERE verification_id=$1 AND command_id=$2 AND attempt_index=$3`, out.VerificationID, out.CommandID, out.AttemptIndex).Scan(&out.VerifierDigest); err != nil {
		t.Fatalf("load verifier for %s: %v", label, err)
	}
	return out
}

func executeRealOVNWork(t *testing.T, ctx context.Context, pool QueryRower, databaseURL string, host realCampaignHost, label, expectedWorkID string, remotePort int, requireSB bool) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil || host.NetworkWorkerPath == "" || host.OVSBridge != "br-int" {
		t.Fatal("complete real OVN worker configuration is required")
	}
	localHost, localPort, err := net.SplitHostPort(parsed.Host)
	if err != nil || (localHost != "127.0.0.1" && localHost != "localhost") {
		t.Fatalf("real OVN worker requires a loopback disposable PostgreSQL URL: %v", err)
	}
	remoteURL := *parsed
	remoteURL.Host = net.JoinHostPort("127.0.0.1", fmt.Sprint(remotePort))
	forward := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%s", remotePort, localPort)
	for attempt := 1; attempt <= 8; attempt++ {
		var state string
		if err := pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, expectedWorkID).Scan(&state); err == nil && state == "OBSERVED" {
			return
		}
		if !requireSB {
			var nbState string
			if err := pool.QueryRow(ctx, `SELECT nb_state FROM kim.network_ovn_state_current s JOIN kim.ovn_runtime_work_current w ON w.port_id=s.port_id AND w.port_generation=s.port_generation AND w.binding_generation=s.binding_generation WHERE w.work_id=$1`, expectedWorkID).Scan(&nbState); err == nil && nbState == "MATCHED" {
				return
			}
		}
		command := exec.CommandContext(ctx, "ssh", "-o", "ExitOnForwardFailure=yes", "-R", forward, host.Host,
			"sudo", "-n", "env", "KIM_REAL_OVN_WORK_ONCE=1", host.NetworkWorkerPath,
			"--database-url", remoteURL.String(), "--worker-id", fmt.Sprintf("real-ovn-%s-%d", label, attempt))
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("real OVN work %s attempt %d: %v output=%s", label, attempt, err, output)
		}
	}
	var state string
	_ = pool.QueryRow(ctx, `SELECT work_state FROM kim.ovn_runtime_work_current WHERE work_id=$1`, expectedWorkID).Scan(&state)
	t.Fatalf("real OVN work %s did not converge: work=%s state=%s require_sb=%t", label, expectedWorkID, state, requireSB)
}

func realCampaignString(t *testing.T, evidence map[string]any, key string) string {
	t.Helper()
	value, ok := evidence[key].(string)
	if !ok || value == "" {
		t.Fatalf("missing string evidence %s in %+v", key, evidence)
	}
	return value
}

func realCampaignUint64(t *testing.T, evidence map[string]any, key string) uint64 {
	t.Helper()
	value, ok := evidence[key].(float64)
	if !ok || value <= 0 {
		t.Fatalf("missing numeric evidence %s in %+v", key, evidence)
	}
	return uint64(value)
}

func injectRealCampaignSourceFailure(t *testing.T, ctx context.Context, source realCampaignHost, vmID, resourceKey string) {
	t.Helper()
	if source.Host != realCampaignSource || vmID != realCampaignVMUUID || source.VGName != "kimrr_campaign058_g01" || resourceKey == "" {
		t.Fatal("source failure injection is outside the exact campaign allow-list")
	}
	inspect := exec.CommandContext(ctx, "ssh", source.Host, "sudo", "-n", "virsh", "domblklist", "--details", vmID)
	output, err := inspect.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte(" vda ")) || !bytes.Contains(output, []byte(source.VGName)) || !bytes.Contains(output, []byte(resourceKey)) {
		t.Fatalf("source failure guard rejected Domain/root identity: err=%v output=%s", err, output)
	}
	uuidCommand := exec.CommandContext(ctx, "ssh", source.Host, "sudo", "-n", "virsh", "dumpxml", vmID)
	uuidOutput, err := uuidCommand.CombinedOutput()
	if err != nil || !bytes.Contains(uuidOutput, []byte("<uuid>"+vmID+"</uuid>")) {
		t.Fatalf("source failure guard rejected UUID XML: err=%v output=%s", err, uuidOutput)
	}
	destroy := exec.CommandContext(ctx, "ssh", source.Host, "sudo", "-n", "virsh", "destroy", vmID)
	if output, err := destroy.CombinedOutput(); err != nil {
		t.Fatalf("source failure injection: %v output=%s", err, output)
	}
}

func prepareRealCampaignHost(t *testing.T, ctx context.Context, pool TxBeginner, host string) {
	t.Helper()
	fingerprint := digestBytes([]byte("real-two-host-recovery-campaign/" + host))
	prepareSessionIdentityFixture(t, ctx, pool, host, 1, fingerprint)
	if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: "real-campaign/session/" + host, HostID: host, ConnectionInstanceID: "real-campaign", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("real-campaign-helper")), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, pool, host)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: host, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: host, PolicyID: "real-campaign-host-policy", PolicyGeneration: 1, ActorID: "qualification", ReasonCode: "explicit_real_two_host_recovery_campaign"}); err != nil {
		t.Fatal(err)
	}
}

// TestRealTwoHostRecoveryAuthorityCampaignPostgreSQLIntegration is the only
// PASS gate for the real two-Host authority campaign. Every backend Result is
// produced by the exact CP Lease-bound helper and every proof/decision shares
// this one PostgreSQL history. It is never run by ordinary CI.
func TestRealTwoHostRecoveryAuthorityCampaignPostgreSQLIntegration(t *testing.T) {
	if os.Getenv("KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E") != "1" || os.Getenv("KIM_REAL_KVM_RECOVERY_CAMPAIGN") != "1" {
		t.Skip("explicit real two-Host Recovery campaign opt-in is not set")
	}
	databaseURL, runner := os.Getenv("KIM_POSTGRES_TEST_URL"), os.Getenv("KIM_REAL_KVM_RECOVERY_RUNNER")
	networkCampaign := os.Getenv("KIM_REAL_KVM_RECOVERY_OVN") == "1"
	source := realCampaignHost{Host: realCampaignSource, HelperPath: os.Getenv("KIM_REAL_KVM_RECOVERY_SOURCE_HELPER"), NetworkWorkerPath: os.Getenv("KIM_REAL_OVN_SOURCE_WORKER"), VGName: "kimrr_campaign058_g01", VGUUID: os.Getenv("KIM_REAL_KVM_RECOVERY_SOURCE_VG_UUID"), CacheRoot: "/var/tmp/kim-real-recovery-campaign058/cache", StateRoot: "/var/tmp/kim-real-recovery-campaign058/state"}
	destination := realCampaignHost{Host: realCampaignDestination, HelperPath: os.Getenv("KIM_REAL_KVM_RECOVERY_DESTINATION_HELPER"), NetworkWorkerPath: os.Getenv("KIM_REAL_OVN_DESTINATION_WORKER"), VGName: "kimrr_campaign058_g02", VGUUID: os.Getenv("KIM_REAL_KVM_RECOVERY_DESTINATION_VG_UUID"), CacheRoot: "/var/tmp/kim-real-recovery-campaign058/cache", StateRoot: "/var/tmp/kim-real-recovery-campaign058/state"}
	if networkCampaign {
		source.OVSBridge, destination.OVSBridge = "br-int", "br-int"
	}
	if databaseURL == "" || runner == "" || source.HelperPath == "" || destination.HelperPath == "" || source.VGUUID == "" || destination.VGUUID == "" || source.Host == destination.Host {
		t.Fatal("complete exact two-Host campaign configuration is required")
	}
	if networkCampaign && (source.NetworkWorkerPath == "" || destination.NetworkWorkerPath == "") {
		t.Fatal("exact remote Network worker paths are required for OVN campaign")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('real-two-host-recovery-campaign058',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	prepareRealCampaignHost(t, ctx, pool, source.Host)
	prepareRealCampaignHost(t, ctx, pool, destination.Host)

	const groupID = "real-recovery-campaign-pool"
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: groupID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "real-recovery-placement", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "real-recovery-campaign-members", HostGroupID: groupID, SourceType: "EXPLICIT", SourceRevision: "campaign058", BasedOnHostGroupGeneration: 1, Members: upgradeSnapshotMembers(groupID, []string{source.Host, destination.Host}, 1, "campaign058")}); err != nil {
		t.Fatal(err)
	}
	const storageClassID = "real-recovery-campaign-local-lvm"
	for index, host := range []realCampaignHost{source, destination} {
		if err := RegisterLocalLVMFoundation(ctx, pool, LocalLVMFoundation{BackendID: fmt.Sprintf("real-recovery-campaign-backend-%d", index+1), HostID: host.Host, VGUUID: host.VGUUID, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: storageClassID, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: fmt.Sprintf("real-recovery-campaign-capacity-%d", index+1), CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 256 << 20, ObservedFreeBytes: 256 << 20, ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	const networkID, subnetID, segmentID, portID = "real-recovery-network060", "real-recovery-subnet060", "real-recovery-segment060", "f1c06a00-0000-4000-8000-202608120060"
	const portMAC, portIP = "02:00:00:00:60:01", "192.0.2.60"
	if networkCampaign {
		if err := UpsertNetworkFoundation(ctx, pool, NetworkFoundation{NetworkID: networkID, ProjectID: "project", NetworkGeneration: 1, NetworkState: "ACTIVE", MTU: 1500, SubnetID: subnetID, SubnetGeneration: 1, SubnetState: "ACTIVE", CIDR: "192.0.2.0/24", AllocationStart: "192.0.2.60", AllocationEnd: "192.0.2.60", SegmentClaimID: segmentID, SegmentType: "VNI", ScopeID: "real-recovery-overlay060", SegmentID: 6060, SegmentGeneration: 1, ProviderMappingRevision: 1, SegmentState: "ACTIVE"}); err != nil {
			t.Fatal(err)
		}
		for _, mapping := range []HostNetworkMapping{
			{HostID: source.Host, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 1500, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "dba0d9ce-859d-4422-a486-17161ebc1b31"},
			{HostID: destination.Host, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 1500, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "aefea4f0-27ed-4a2a-ae77-8e9ce09303ff"},
		} {
			if err := UpsertHostNetworkMapping(ctx, pool, mapping); err != nil {
				t.Fatal(err)
			}
		}
	}

	confirmationPolicy, err := PublishFailureConfirmationPolicy(ctx, pool, FailureConfirmationPolicy{PolicyID: "real-recovery-confirmation", PolicyRevision: 1, ApplicableFailureClass: "VM_RUNTIME_UNAVAILABLE", ConfirmationMode: "ALL_REQUIRED_EVIDENCE", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification", Requirements: []FailureConfirmationRequirement{{Ordinal: 1, EvidenceType: "VM_RUNTIME_OBSERVATION", ObservedState: "PRESENT", FreshnessState: "CURRENT", SourceType: "LIBVIRT_READ_BACK"}}})
	if err != nil {
		t.Fatal(err)
	}
	fencingPolicy, err := PublishFailureFencingPolicy(ctx, pool, FailureFencingPolicy{PolicyID: "real-recovery-fencing", PolicyRevision: 1, FencingMode: "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	storagePolicy, err := PublishStorageSafetyPolicy(ctx, pool, StorageSafetyPolicy{PolicyID: "real-recovery-storage", PolicyRevision: 1, StorageClass: "LOCAL_LVM", SafetyMode: "SOURCE_ROOT_QUIESCED_DATA_DETACHED", LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	budgetPolicy, err := PublishRecoveryBudgetPolicy(ctx, pool, RecoveryBudgetPolicy{PolicyID: "real-recovery-budget", PolicyRevision: 1, ScopeType: "GLOBAL", Phase: "PLANNING", MaxActiveRecoveries: 1, LifecycleState: "ACTIVE", CreatedBy: "qualification", ApprovedBy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	availability := availabilityPolicyFixture("real-recovery-availability", 1, "INFRASTRUCTURE_MANAGED", "RESTART_ON_OTHER_HOST", "ACTIVE")
	availability.FailureConfirmationPolicyID, availability.FailureConfirmationPolicyRevision, availability.FailureConfirmationPolicyDigest = confirmationPolicy.PolicyID, 1, confirmationPolicy.PolicyDigest
	availability.FencingPolicyID, availability.FencingPolicyRevision, availability.FencingPolicyDigest = fencingPolicy.PolicyID, 1, fencingPolicy.PolicyDigest
	availability.StorageSafetyPolicyID, availability.StorageSafetyPolicyRevision, availability.StorageSafetyPolicyDigest = storagePolicy.PolicyID, 1, storagePolicy.PolicyDigest
	availability.RecoveryBudgetPolicyID, availability.RecoveryBudgetPolicyRevision, availability.RecoveryBudgetPolicyDigest = budgetPolicy.PolicyID, 1, budgetPolicy.PolicyDigest
	availabilityDigest, err := PublishAvailabilityPolicy(ctx, pool, availability)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "real-recovery-campaign-policy-binding", BindingID: "real-recovery-campaign-policy-binding", HostGroupID: groupID, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: availability.PolicyID, PolicyRevision: 1, PolicyDigest: availabilityDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}

	const imageID, flavorID, scopeID = "real-recovery-campaign-image", "real-recovery-campaign-flavor", "real-recovery-campaign-scope"
	imageDigest := os.Getenv("KIM_REAL_KVM_RECOVERY_IMAGE_DIGEST")
	if len(imageDigest) != 64 {
		t.Fatal("exact campaign RAW image digest is required")
	}
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 1 << 20, DeclaredChecksum: imageDigest, ObservedChecksum: imageDigest, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("real-recovery-image-signature")), SourceURI: "qualification://real-recovery-campaign058", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "real-recovery.tiny", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "real-recovery-campaign-scope", PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: groupID, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}

	const workloadID = "real-recovery-campaign-workload"
	sourceVolumeID, sourceAttachmentID, sourceBackendID := "real-recovery-source-root", "real-recovery-source-root-attachment", "real-recovery-campaign-backend-1"
	sourceRequest := PlacementAdmissionRequest{RequestID: "real-recovery-source-admission", ProjectID: "project", WorkloadID: workloadID, ImageID: imageID, FlavorID: flavorID, PlacementScopeID: scopeID, Storage: []placement.StorageRequirement{{VolumeID: sourceVolumeID, AttachmentID: sourceAttachmentID, BackendID: sourceBackendID, BackendGeneration: 1, VGUUID: source.VGUUID, StorageClassID: storageClassID, StorageClassRevision: 1, CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1, SizeBytes: 16 << 20, AccessMode: "SINGLE_WRITER", Bootable: true}}}
	if networkCampaign {
		sourceRequest.Network = []placement.NetworkRequirement{{PortID: portID, NetworkID: networkID, NetworkGeneration: 1, SubnetID: subnetID, SubnetGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, IPAddress: portIP, MACAddress: portMAC, AllocationSource: "EXPLICIT", BindingType: "OVS", RequiredMTU: 1500}}
	}
	sourceDry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, sourceRequest)
	if err != nil || sourceDry.Status != "READY" {
		t.Fatalf("source dry=%+v err=%v", sourceDry, err)
	}
	var sourceCandidate AvailabilityPlacementCandidate
	for _, candidate := range sourceDry.Candidates {
		if candidate.Placement.HostID == source.Host && candidate.Placement.Eligible {
			sourceCandidate = candidate
		}
	}
	if sourceCandidate.Placement.HostID == "" {
		t.Fatal("exact source Host is not eligible")
	}
	sourceAdmission, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, sourceDry, sourceRequest, sourceCandidate)
	if err != nil || sourceAdmission.AvailabilityBinding == nil {
		t.Fatalf("source admission=%+v err=%v", sourceAdmission, err)
	}
	sourceBindingID := "storage-binding:" + sourceRequest.RequestID + ":" + sourceVolumeID
	var sourceResourceKey string
	if err := pool.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, sourceBindingID).Scan(&sourceResourceKey); err != nil {
		t.Fatal(err)
	}
	const sourceLVMCommand = "real-recovery-source-lvm-command"
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "real-recovery-source-lvm-job", CommandID: sourceLVMCommand, HostID: source.Host, ResourceType: "VOLUME", ResourceID: sourceVolumeID, DesiredRevision: 1, CommandType: locallvm.CommandType, SchemaVersion: locallvm.SchemaVersion, TargetResourceID: "volume:" + sourceVolumeID, Payload: map[string]any{"vg_uuid": source.VGUUID, "size_mib": 16, "desired_state": "PRESENT"}}); err != nil {
		t.Fatal(err)
	}
	sourceLVM := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, sourceLVMCommand, "source-lvm")
	sourceLVUUID := realCampaignString(t, sourceLVM.Observation.Evidence, "observed_lv_uuid")
	if err := AcceptLocalLVMBindingObservation(ctx, pool, LocalLVMBindingObservation{EvidenceID: "real-recovery-source-binding-evidence", BindingID: sourceBindingID, VolumeID: sourceVolumeID, BackendID: sourceBackendID, HostID: source.Host, VGUUID: source.VGUUID, LVUUID: sourceLVUUID, BackendResourceKey: sourceResourceKey, BindingGeneration: 1, CommandID: sourceLVM.CommandID, VerificationID: sourceLVM.VerificationID, AttemptIndex: uint32(sourceLVM.AttemptIndex), ObservationGeneration: uint64(sourceLVM.Observation.Generation), ObservationDigest: sourceLVM.Observation.Digest, VerifierDigest: sourceLVM.VerifierDigest, EvidenceState: "MATCHED", ObservedSizeBytes: realCampaignUint64(t, sourceLVM.Observation.Evidence, "observed_size_bytes")}); err != nil {
		t.Fatal(err)
	}

	sourceMaterialization, err := PrepareVMMaterialization(ctx, pool, VMMaterializationRequest{VMID: realCampaignVMUUID, AdmissionID: sourceAdmission.AdmissionID, PlanID: "real-recovery-source-plan", JobID: "real-recovery-source-define-job", CommandID: "real-recovery-source-define-command"})
	if err != nil {
		t.Fatal(err)
	}
	sourceDefine := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, sourceMaterialization.CommandID, "source-define")
	if err := AcceptVMDefinitionObservation(ctx, pool, VMDefinitionObservation{EvidenceID: "real-recovery-source-define-evidence", VMID: realCampaignVMUUID, VMGeneration: 1, PlanID: sourceMaterialization.PlanID, PlanDigest: sourceMaterialization.PlanDigest, HostID: source.Host, CommandID: sourceDefine.CommandID, AttemptIndex: uint32(sourceDefine.AttemptIndex), VerificationID: sourceDefine.VerificationID, ObservationGeneration: uint64(sourceDefine.Observation.Generation), ObservationDigest: sourceDefine.Observation.Digest, VerifierDigest: sourceDefine.VerifierDigest, EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	sourceImageRequest := VMImageMaterializationRequest{VMID: realCampaignVMUUID, PlanID: sourceMaterialization.PlanID, JobID: "real-recovery-source-image-job", CommandID: "real-recovery-source-image-command"}
	if _, err := PrepareVMImageMaterialization(ctx, pool, sourceImageRequest); err != nil {
		t.Fatal(err)
	}
	sourceImage := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, sourceImageRequest.CommandID, "source-image")
	if err := AcceptVMImageRealizationObservation(ctx, pool, VMImageRealizationObservation{EvidenceID: "real-recovery-source-image-evidence", VMID: realCampaignVMUUID, VMGeneration: 1, PlanID: sourceMaterialization.PlanID, PlanDigest: sourceMaterialization.PlanDigest, HostID: source.Host, ImageID: imageID, ImageRevision: 1, ExpectedDigest: imageDigest, ObservedDigest: imageDigest, ImageSizeBytes: 1 << 20, VolumeID: sourceVolumeID, BindingID: sourceBindingID, BindingGeneration: 1, VGUUID: source.VGUUID, LVUUID: sourceLVUUID, BackendResourceKey: sourceResourceKey, CommandID: sourceImage.CommandID, AttemptIndex: uint32(sourceImage.AttemptIndex), VerificationID: sourceImage.VerificationID, ObservationGeneration: uint64(sourceImage.Observation.Generation), ObservationDigest: sourceImage.Observation.Digest, VerifierDigest: sourceImage.VerifierDigest, EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	if networkCampaign {
		if _, err := CommitOVNPortIntent(ctx, pool, OVNPortIntentRequest{IntentID: "real-recovery-source-ovn-intent", IntentGeneration: 1, PortID: portID}); err != nil {
			t.Fatal(err)
		}
		executeRealOVNWork(t, ctx, pool, databaseURL, source, "source-bind-preboot", "ovn-runtime:real-recovery-source-ovn-intent:1", 55461, false)
		realizeRequest := OVSPortRealizationRequest{VMID: realCampaignVMUUID, PlanID: sourceMaterialization.PlanID, PortID: portID, JobID: "real-recovery-source-port-job", CommandID: "real-recovery-source-port-command"}
		if _, err := PrepareOVSPortRealization(ctx, pool, realizeRequest); err != nil {
			t.Fatal(err)
		}
		realized := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, realizeRequest.CommandID, "source-port")
		if err := AcceptOVSPortRealizationAndMaybeArmPower(ctx, pool, OVSPortRealizationObservation{EvidenceID: "real-recovery-source-port-evidence", VMID: realCampaignVMUUID, VMGeneration: 1, PlanID: sourceMaterialization.PlanID, HostID: source.Host, PortID: portID, PortGeneration: 1, NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1, CommandID: realized.CommandID, AttemptIndex: uint32(realized.AttemptIndex), VerificationID: realized.VerificationID, ObservationGeneration: uint64(realized.Observation.Generation), ObservationDigest: realized.Observation.Digest, VerifierDigest: realized.VerifierDigest, PowerJobID: "real-recovery-source-power-on-job", PowerCommandID: "real-recovery-source-power-on-command"}); err != nil {
			t.Fatal(err)
		}
	} else if err := AuthorizeZeroPortVMPowerOn(ctx, pool, realCampaignVMUUID, 1, source.Host, "real-recovery-source-power-on-job", "real-recovery-source-power-on-command"); err != nil {
		t.Fatal(err)
	}
	sourceRunning := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, "real-recovery-source-power-on-command", "source-running")
	if networkCampaign {
		executeRealOVNWork(t, ctx, pool, databaseURL, source, "source-bind-postpower", "ovn-runtime:real-recovery-source-ovn-intent:1", 55461, true)
		sourceDataplaneRequest := OVSDataplaneRequest{VMID: realCampaignVMUUID, PlanID: sourceMaterialization.PlanID, PortID: portID, JobID: "real-recovery-source-dataplane-job", CommandID: "real-recovery-source-dataplane-command"}
		if _, err := PrepareOVSDataplaneObservation(ctx, pool, sourceDataplaneRequest); err != nil {
			t.Fatal(err)
		}
		sourceDataplane := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, sourceDataplaneRequest.CommandID, "source-dataplane")
		if err := AcceptOVSDataplaneObservation(ctx, pool, OVSDataplaneObservation{
			EvidenceID: "real-recovery-source-dataplane-evidence", VMID: realCampaignVMUUID, VMGeneration: 1,
			PlanID: sourceMaterialization.PlanID, HostID: source.Host, PortID: portID,
			PortGeneration: 1, NetworkID: networkID, NetworkGeneration: 1, SegmentClaimID: segmentID,
			SegmentGeneration: 1, HostMappingGeneration: 1, BindingGeneration: 1,
			CommandID: sourceDataplane.CommandID, AttemptIndex: uint32(sourceDataplane.AttemptIndex),
			VerificationID: sourceDataplane.VerificationID, ObservationGeneration: uint64(sourceDataplane.Observation.Generation),
			ObservationDigest: sourceDataplane.Observation.Digest, VerifierDigest: sourceDataplane.VerifierDigest,
		}); err != nil {
			t.Fatal(err)
		}
	}
	injectRealCampaignSourceFailure(t, ctx, source, realCampaignVMUUID, sourceResourceKey)
	if err := AuthorizeVMPowerOff(ctx, pool, realCampaignVMUUID, 1, source.Host, "real-recovery-source-power-off-job", "real-recovery-source-power-off-command"); err != nil {
		t.Fatal(err)
	}
	sourceShutoff := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, "real-recovery-source-power-off-command", "source-shutoff")
	_ = sourceRunning

	epoch, err := OpenFailureEpoch(ctx, pool, OpenFailureEpochRequest{OpenRequestID: "real-recovery-failure-open", FailureEpochID: "real-recovery-failure-epoch", IncidentKey: "real-recovery-campaign058", WorkloadID: workloadID, FailureClass: "VM_RUNTIME_UNAVAILABLE", RequestedBy: "qualification", ExpectedBindingRevision: sourceAdmission.AvailabilityBinding.BindingRevision, ExpectedBindingDigest: sourceAdmission.AvailabilityBinding.BindingDigest, Trigger: FailureObservation{EvidenceID: "real-recovery-failure-observation", EvidenceType: "VM_RUNTIME_OBSERVATION", SourceType: "LIBVIRT_READ_BACK", SourceHostID: source.Host, ObservedState: "PRESENT", FreshnessState: "CURRENT", PayloadDigest: sourceShutoff.Observation.Digest, ObservationGeneration: uint64(sourceShutoff.Observation.Generation), ObservedAt: time.Now().UTC()}})
	if err != nil || epoch.EpochState != "SUSPECTED" {
		t.Fatalf("epoch=%+v err=%v", epoch, err)
	}
	confirmation, err := EvaluateFailureConfirmation(ctx, pool, "real-recovery-confirmation-evaluation", epoch.FailureEpochID, "real-recovery-confirmation-evaluator/v1", digestBytes([]byte("real-recovery-confirmation-evaluator/v1")))
	if err != nil || confirmation.ResultState != "SATISFIED" {
		t.Fatalf("confirmation=%+v err=%v", confirmation, err)
	}
	if _, _, err := ConfirmFailureEpoch(ctx, pool, "real-recovery-confirmation-decision", confirmation.EvaluationID, "real-recovery-failure-authority/v1"); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fenceHostOperationAuthorityTx(ctx, tx, source.Host, "real_recovery_campaign")
	}); err != nil {
		t.Fatal(err)
	}
	fencingObservation, err := RecordSourceExecutionFencingObservation(ctx, pool, "real-recovery-fencing-observation", epoch.FailureEpochID)
	if err != nil || fencingObservation.ObservationState != "PROVEN" {
		t.Fatalf("fencing observation=%+v err=%v", fencingObservation, err)
	}
	fencingEvaluation, err := EvaluateFailureFencing(ctx, pool, "real-recovery-fencing-evaluation", epoch.FailureEpochID, "real-recovery-fencing-evaluator/v1", digestBytes([]byte("real-recovery-fencing-evaluator/v1")))
	if err != nil || fencingEvaluation.ResultState != "PROVEN" {
		t.Fatalf("fencing evaluation=%+v err=%v", fencingEvaluation, err)
	}
	const rootCommandID = "real-recovery-source-root-read-back-command"
	rootPayload := map[string]any{"domain_uuid": realCampaignVMUUID, "volume_id": sourceVolumeID, "binding_id": sourceBindingID, "vg_uuid": source.VGUUID, "lv_uuid": sourceLVUUID, "backend_resource_key": sourceResourceKey}
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "real-recovery-source-root-read-back-job", CommandID: rootCommandID, HostID: source.Host, ResourceType: "SOURCE_ROOT_SAFETY", ResourceID: sourceAttachmentID, DesiredRevision: 1, CommandType: libvirtvolume.SourceRootSafetyReadBackCommandType, SchemaVersion: libvirtvolume.SourceRootSafetyReadBackSchema, TargetResourceID: "attachment:" + sourceAttachmentID, Payload: rootPayload}); err != nil {
		t.Fatal(err)
	}
	rootReadBack := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, rootCommandID, "source-root-read-back")
	if err := AcceptSourceRootSafetyObservation(ctx, pool, LocalLVMAttachmentObservation{EvidenceID: "real-recovery-source-root-evidence", AttachmentID: sourceAttachmentID, VolumeID: sourceVolumeID, BindingID: sourceBindingID, HostID: source.Host, DomainUUID: realCampaignVMUUID, TargetDevice: "vda", ObservedLVUUID: sourceLVUUID, DesiredState: "ATTACHED", CommandID: rootReadBack.CommandID, VerificationID: rootReadBack.VerificationID, ObservationDigest: rootReadBack.Observation.Digest, VerifierDigest: rootReadBack.VerifierDigest, EvidenceState: "MATCHED", AttachmentGeneration: 1, BindingGeneration: 1, ObservationGeneration: uint64(rootReadBack.Observation.Generation), AttemptIndex: uint32(rootReadBack.AttemptIndex), DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true, HolderOpen: false}); err != nil {
		t.Fatal(err)
	}
	rootEvaluation, err := EvaluateSourceRootSafety(ctx, pool, "real-recovery-source-root-evaluation", epoch.FailureEpochID, "real-recovery-root-evaluator/v1", digestBytes([]byte("real-recovery-root-evaluator/v1")))
	if err != nil || rootEvaluation.ResultState != "SAFE" {
		t.Fatalf("root evaluation=%+v err=%v", rootEvaluation, err)
	}
	rootProof, err := MaterializeSourceRootSafetyProof(ctx, pool, "real-recovery-source-root-proof", rootEvaluation.EvaluationID, "real-recovery-root-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	storageEvaluation, err := EvaluateStorageSafety(ctx, pool, "real-recovery-storage-evaluation", epoch.FailureEpochID, "real-recovery-storage-evaluator/v1", digestBytes([]byte("real-recovery-storage-evaluator/v1")))
	if err != nil || storageEvaluation.ResultState != "SAFE" {
		t.Fatalf("storage evaluation=%+v err=%v", storageEvaluation, err)
	}
	storageProof, err := MaterializeStorageSafetyProof(ctx, pool, "real-recovery-storage-proof", storageEvaluation.EvaluationID, "real-recovery-storage-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	fencingProof, _, err := MaterializeFailureFencingProof(ctx, pool, "real-recovery-fencing-proof", fencingEvaluation.EvaluationID, "real-recovery-fencing-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	if networkCampaign {
		retirement, err := CommitOVNPortBindingRetirement(ctx, pool, OVNPortBindingRetirementRequest{
			OperationID: "real-recovery-source-port-retirement", OperationGeneration: 1,
			IntentID: "real-recovery-source-port-retirement-intent", IntentGeneration: 2,
			PortID: portID, PortGeneration: 1, BindingGeneration: 1, SourceHostID: source.Host,
		})
		if err != nil {
			t.Fatal(err)
		}
		executeRealOVNWork(t, ctx, pool, databaseURL, source, "source-unbind", "ovn-runtime:real-recovery-source-port-retirement-intent:2", 55462, true)
		var retirementState string
		if err := pool.QueryRow(ctx, `SELECT retirement_state FROM kim.network_port_binding_retirements_current WHERE port_id=$1 AND operation_id=$2 AND operation_generation=$3`, portID, retirement.OperationID, retirement.OperationGeneration).Scan(&retirementState); err != nil || retirementState != "VERIFIED" {
			t.Fatalf("source retirement state=%s err=%v", retirementState, err)
		}
		quiescence, err := PrepareNetworkPortSourceQuiescence(ctx, pool, NetworkPortSourceQuiescenceRequest{FailureEpochID: epoch.FailureEpochID, PortID: portID, JobID: "real-recovery-source-port-quiescence-job", CommandID: "real-recovery-source-port-quiescence-command"})
		if err != nil {
			t.Fatal(err)
		}
		observed := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, quiescence.CommandID, "source-port-quiescence")
		if err := AcceptNetworkPortSourceQuiescence(ctx, pool, NetworkPortSourceQuiescenceObservation{
			EvidenceID: "real-recovery-source-port-quiescence-evidence", FailureEpochID: epoch.FailureEpochID,
			PortID: portID, SourceHostID: source.Host, VMID: realCampaignVMUUID,
			CommandID: observed.CommandID, VerificationID: observed.VerificationID,
			ObservationDigest: observed.Observation.Digest, VerifierDigest: observed.VerifierDigest,
			PortGeneration: quiescence.PortGeneration, BindingGeneration: quiescence.BindingGeneration,
			VMGeneration: quiescence.VMGeneration, ObservationGeneration: uint64(observed.Observation.Generation),
			AttemptIndex: uint32(observed.AttemptIndex),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RetireSourceMaterialization(ctx, pool, "real-recovery-source-retirement", epoch.FailureEpochID, rootProof.ProofID, fencingProof.ProofID, "real-recovery-retirement-authority/v1"); err != nil {
		t.Fatal(err)
	}
	eligibility, err := EvaluateRecoveryEligibility(ctx, pool, "real-recovery-eligibility-evaluation", epoch.FailureEpochID, scopeID, "real-recovery-eligibility-evaluator/v1", digestBytes([]byte("real-recovery-eligibility-evaluator/v1")))
	if err != nil || eligibility.ResultState != "ELIGIBLE" {
		t.Fatalf("eligibility=%+v err=%v", eligibility, err)
	}
	decision, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "real-recovery-eligibility-decision", eligibility.EvaluationID, "real-recovery-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	const operationID = "real-recovery-operation"
	if _, err := RecordRecoveryOperationRequest(ctx, pool, operationID, decision.DecisionID, decision.BudgetClaimID, "RESTART_ON_OTHER_HOST", "qualification"); err != nil {
		t.Fatal(err)
	}
	operation, plan, err := PlanRecoveryOperation(ctx, pool, operationID, "real-recovery-plan", destination.Host)
	if err != nil || plan.DestinationHostID != destination.Host {
		t.Fatalf("operation=%+v plan=%+v err=%v", operation, plan, err)
	}
	start, err := StartRecoveryOperation(ctx, pool, operationID, "real-recovery-preparation-job", "real-recovery-preparation-command")
	if err != nil || start.LifecycleState != "RUNNING" {
		t.Fatalf("start=%+v err=%v", start, err)
	}
	preparation := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, start.ExecutionCommandID, "destination-preparation")
	if _, err := RefreshRecoveryOperationExecution(ctx, pool, operationID, preparation.VerificationID); err != nil {
		t.Fatal(err)
	}

	qualifyRealRecoveryDestinationAndTerminal(t, ctx, pool, databaseURL, runner, source, destination, operationID, realCampaignVMUUID, imageID, imageDigest, "real-recovery-campaign-backend-2", destination.VGUUID, plan, start, decision.BudgetClaimID, fencingProof.ProofID, storageProof.ProofID)
}

func qualifyRealRecoveryDestinationAndTerminal(t *testing.T, ctx context.Context, pool recoveryQualificationDB, databaseURL, runner string, source, destination realCampaignHost, operationID, vmID, imageID, imageDigest, destinationBackendID, destinationVGUUID string, plan RecoveryPlan, start RecoveryOperationStart, budgetClaimID, fencingProofID, storageProofID string) {
	t.Helper()
	if len(plan.DestinationRequest.Storage) != 1 || plan.DestinationHostID != destination.Host || plan.SourceHostID != source.Host {
		t.Fatalf("fixed destination plan=%+v", plan)
	}
	required := plan.DestinationRequest.Storage[0]
	bindingID := "storage-binding:" + plan.DestinationRequest.RequestID + ":" + required.VolumeID
	var resourceKey string
	if err := pool.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1 AND placement_admission_id=$2`, bindingID, start.DestinationAdmissionID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	const lvmCommandID = "real-recovery-destination-lvm-command"
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "real-recovery-destination-lvm-job", CommandID: lvmCommandID, HostID: destination.Host, ResourceType: "VOLUME", ResourceID: required.VolumeID, DesiredRevision: 1, CommandType: locallvm.CommandType, SchemaVersion: locallvm.SchemaVersion, TargetResourceID: "volume:" + required.VolumeID, Payload: map[string]any{"vg_uuid": destinationVGUUID, "size_mib": required.SizeBytes / (1 << 20), "desired_state": "PRESENT"}}); err != nil {
		t.Fatal(err)
	}
	lvmExecution := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, lvmCommandID, "destination-lvm")
	lvUUID := realCampaignString(t, lvmExecution.Observation.Evidence, "observed_lv_uuid")
	if err := AcceptLocalLVMBindingObservation(ctx, pool, LocalLVMBindingObservation{EvidenceID: "real-recovery-destination-binding-evidence", BindingID: bindingID, VolumeID: required.VolumeID, BackendID: destinationBackendID, HostID: destination.Host, VGUUID: destinationVGUUID, LVUUID: lvUUID, BackendResourceKey: resourceKey, BindingGeneration: 1, CommandID: lvmExecution.CommandID, VerificationID: lvmExecution.VerificationID, AttemptIndex: uint32(lvmExecution.AttemptIndex), ObservationGeneration: uint64(lvmExecution.Observation.Generation), ObservationDigest: lvmExecution.Observation.Digest, VerifierDigest: lvmExecution.VerifierDigest, EvidenceState: "MATCHED", ObservedSizeBytes: realCampaignUint64(t, lvmExecution.Observation.Evidence, "observed_size_bytes")}); err != nil {
		t.Fatal(err)
	}

	materializationRequest := RecoveryMaterializationRequest{RecoveryOperationID: operationID, MaterializationID: "real-recovery-destination-materialization", VMID: vmID, VMPlanID: "real-recovery-destination-vm-plan", DefineJobID: "real-recovery-destination-define-job", DefineCommandID: "real-recovery-destination-define-command"}
	materialization, err := PrepareRecoveryMaterialization(ctx, pool, materializationRequest)
	if err != nil || materialization.MaterializationGeneration != 2 {
		t.Fatalf("destination materialization=%+v err=%v", materialization, err)
	}
	defineExecution := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, materializationRequest.DefineCommandID, "destination-define")
	if err := AcceptVMDefinitionObservation(ctx, pool, VMDefinitionObservation{EvidenceID: "real-recovery-destination-define-evidence", VMID: vmID, VMGeneration: materialization.VMGeneration, PlanID: materialization.VMPlanID, PlanDigest: materialization.VMPlanDigest, HostID: destination.Host, CommandID: defineExecution.CommandID, AttemptIndex: uint32(defineExecution.AttemptIndex), VerificationID: defineExecution.VerificationID, ObservationGeneration: uint64(defineExecution.Observation.Generation), ObservationDigest: defineExecution.Observation.Digest, VerifierDigest: defineExecution.VerifierDigest, EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	imageRequest := VMImageMaterializationRequest{VMID: vmID, PlanID: materialization.VMPlanID, JobID: "real-recovery-destination-image-job", CommandID: "real-recovery-destination-image-command"}
	if _, err := PrepareVMImageMaterialization(ctx, pool, imageRequest); err != nil {
		t.Fatal(err)
	}
	imageExecution := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, imageRequest.CommandID, "destination-image")
	if err := AcceptVMImageRealizationObservation(ctx, pool, VMImageRealizationObservation{EvidenceID: "real-recovery-destination-image-evidence", VMID: vmID, VMGeneration: materialization.VMGeneration, PlanID: materialization.VMPlanID, PlanDigest: materialization.VMPlanDigest, HostID: destination.Host, ImageID: imageID, ImageRevision: 1, ExpectedDigest: imageDigest, ObservedDigest: imageDigest, ImageSizeBytes: 1 << 20, VolumeID: required.VolumeID, BindingID: bindingID, BindingGeneration: 1, VGUUID: destinationVGUUID, LVUUID: lvUUID, BackendResourceKey: resourceKey, CommandID: imageExecution.CommandID, AttemptIndex: uint32(imageExecution.AttemptIndex), VerificationID: imageExecution.VerificationID, ObservationGeneration: uint64(imageExecution.Observation.Generation), ObservationDigest: imageExecution.Observation.Digest, VerifierDigest: imageExecution.VerifierDigest, EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	var destinationNetwork *placement.NetworkRequirement
	if len(plan.DestinationRequest.Network) == 0 {
		if err := MarkRecoveryNoNetworkReady(ctx, pool, operationID); err != nil {
			t.Fatal(err)
		}
	} else if len(plan.DestinationRequest.Network) == 1 {
		requiredNetwork := plan.DestinationRequest.Network[0]
		destinationNetwork = &requiredNetwork
		if _, err := CommitOVNPortIntent(ctx, pool, OVNPortIntentRequest{IntentID: "real-recovery-destination-ovn-intent", IntentGeneration: 3, PortID: requiredNetwork.PortID}); err != nil {
			t.Fatal(err)
		}
		executeRealOVNWork(t, ctx, pool, databaseURL, destination, "destination-bind-preboot", "ovn-runtime:real-recovery-destination-ovn-intent:3", 55463, false)
		realizeRequest := OVSPortRealizationRequest{VMID: vmID, PlanID: materialization.VMPlanID, PortID: requiredNetwork.PortID, JobID: "real-recovery-destination-port-job", CommandID: "real-recovery-destination-port-command"}
		if _, err := PrepareOVSPortRealization(ctx, pool, realizeRequest); err != nil {
			t.Fatal(err)
		}
		realized := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, realizeRequest.CommandID, "destination-port")
		if err := AcceptOVSPortRealizationAndMaybeArmPower(ctx, pool, OVSPortRealizationObservation{
			EvidenceID: "real-recovery-destination-port-evidence", VMID: vmID, VMGeneration: materialization.VMGeneration,
			PlanID: materialization.VMPlanID, HostID: destination.Host, PortID: requiredNetwork.PortID,
			PortGeneration: requiredNetwork.DestinationPortGeneration, NetworkID: requiredNetwork.NetworkID,
			NetworkGeneration: requiredNetwork.NetworkGeneration, SegmentClaimID: requiredNetwork.SegmentClaimID,
			SegmentGeneration: requiredNetwork.SegmentGeneration, HostMappingGeneration: requiredNetwork.HostMappingGeneration,
			BindingGeneration: requiredNetwork.DestinationBindingGeneration, CommandID: realized.CommandID,
			AttemptIndex: uint32(realized.AttemptIndex), VerificationID: realized.VerificationID,
			ObservationGeneration: uint64(realized.Observation.Generation), ObservationDigest: realized.Observation.Digest,
			VerifierDigest: realized.VerifierDigest, DeferPowerAuthorization: true,
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatalf("real Recovery campaign supports exactly one Network Port, got %d", len(plan.DestinationRequest.Network))
	}
	dangerous, err := EvaluateRecoveryDangerousStep(ctx, pool, "real-recovery-dangerous-step", operationID, digestBytes([]byte("real-recovery-dangerous-step/v1")))
	if err != nil || dangerous.ResultState != "AUTHORIZED" || dangerous.FencingProofID != fencingProofID || dangerous.StorageSafetyProofID != storageProofID {
		t.Fatalf("dangerous=%+v err=%v", dangerous, err)
	}
	powerAuthority, err := AuthorizeRecoveryPowerOn(ctx, pool, "real-recovery-power-authority", operationID, dangerous.EvaluationID, "real-recovery-destination-power-job", "real-recovery-destination-power-command")
	if err != nil {
		t.Fatal(err)
	}
	powerExecution := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, powerAuthority.PowerCommandID, "destination-power")
	if _, err := RefreshRecoveryPowerExecution(ctx, pool, operationID, powerExecution.VerificationID); err != nil {
		t.Fatal(err)
	}
	if destinationNetwork != nil {
		executeRealOVNWork(t, ctx, pool, databaseURL, destination, "destination-bind-postpower", "ovn-runtime:real-recovery-destination-ovn-intent:3", 55463, true)
		dataplaneRequest := OVSDataplaneRequest{VMID: vmID, PlanID: materialization.VMPlanID, PortID: destinationNetwork.PortID, JobID: "real-recovery-destination-dataplane-job", CommandID: "real-recovery-destination-dataplane-command"}
		if _, err := PrepareOVSDataplaneObservation(ctx, pool, dataplaneRequest); err != nil {
			t.Fatal(err)
		}
		observed := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, dataplaneRequest.CommandID, "destination-dataplane")
		if err := AcceptOVSDataplaneObservation(ctx, pool, OVSDataplaneObservation{
			EvidenceID: "real-recovery-destination-dataplane-evidence", VMID: vmID, VMGeneration: materialization.VMGeneration,
			PlanID: materialization.VMPlanID, HostID: destination.Host, PortID: destinationNetwork.PortID,
			PortGeneration: destinationNetwork.DestinationPortGeneration, NetworkID: destinationNetwork.NetworkID,
			NetworkGeneration: destinationNetwork.NetworkGeneration, SegmentClaimID: destinationNetwork.SegmentClaimID,
			SegmentGeneration: destinationNetwork.SegmentGeneration, HostMappingGeneration: destinationNetwork.HostMappingGeneration,
			BindingGeneration: destinationNetwork.DestinationBindingGeneration, CommandID: observed.CommandID,
			AttemptIndex: uint32(observed.AttemptIndex), VerificationID: observed.VerificationID,
			ObservationGeneration: uint64(observed.Observation.Generation), ObservationDigest: observed.Observation.Digest,
			VerifierDigest: observed.VerifierDigest,
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := RefreshRecoveryNetworkVerificationReadiness(ctx, pool, operationID); err != nil {
			t.Fatal(err)
		}
	}

	const attachmentCommandID = "real-recovery-destination-root-read-back-command"
	attachmentPayload := map[string]any{"domain_uuid": vmID, "volume_id": required.VolumeID, "vg_uuid": destinationVGUUID, "lv_uuid": lvUUID, "backend_resource_key": resourceKey, "disk_slot": 0, "desired_state": "ATTACHED", "access_mode": "SINGLE_WRITER"}
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "real-recovery-destination-root-read-back-job", CommandID: attachmentCommandID, HostID: destination.Host, ResourceType: "VOLUME_ATTACHMENT", ResourceID: required.AttachmentID, DesiredRevision: int64(materialization.VMGeneration), CommandType: libvirtvolume.CommandType, SchemaVersion: libvirtvolume.SchemaVersion, TargetResourceID: "attachment:" + required.AttachmentID, Payload: attachmentPayload}); err != nil {
		t.Fatal(err)
	}
	attachmentExecution := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, destination, attachmentCommandID, "destination-root-read-back")
	if holder, _ := attachmentExecution.Observation.Evidence["holder_open"].(bool); !holder {
		t.Fatalf("destination root holder is not open: %+v", attachmentExecution.Observation.Evidence)
	}
	if err := AcceptLocalLVMAttachmentObservation(ctx, pool, LocalLVMAttachmentObservation{EvidenceID: "real-recovery-destination-root-evidence", AttachmentID: required.AttachmentID, VolumeID: required.VolumeID, AttachmentGeneration: materialization.RootAttachmentGeneration, BindingID: bindingID, BindingGeneration: materialization.RootBindingGeneration, HostID: destination.Host, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvUUID, DesiredState: "ATTACHED", CommandID: attachmentExecution.CommandID, VerificationID: attachmentExecution.VerificationID, ObservationGeneration: uint64(attachmentExecution.Observation.Generation), AttemptIndex: uint32(attachmentExecution.AttemptIndex), ObservationDigest: attachmentExecution.Observation.Digest, VerifierDigest: attachmentExecution.VerifierDigest, EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true, HolderOpen: true}); err != nil {
		t.Fatal(err)
	}

	// Terminal source re-read is independently Lease-bound and persisted, but
	// does not replace the exact root observation generation used by the proof.
	const finalSourceCommand = "real-recovery-source-final-read-back-command"
	var sourceVolumeID, sourceBindingID, sourceAttachmentID, sourceVGUUID, sourceLVUUID, sourceResourceKey string
	if err := pool.QueryRow(ctx, `SELECT p.root_volume_id,p.root_binding_id,p.root_attachment_id,b.vg_uuid,b.lv_uuid,b.backend_resource_key FROM kim.vm_materialization_plan_evidence p JOIN kim.volume_backend_bindings_current b ON b.binding_id=p.root_binding_id AND b.binding_generation=p.root_binding_generation WHERE p.vm_id=$1 AND p.host_id=$2 ORDER BY p.vm_generation LIMIT 1`, vmID, source.Host).Scan(&sourceVolumeID, &sourceBindingID, &sourceAttachmentID, &sourceVGUUID, &sourceLVUUID, &sourceResourceKey); err != nil {
		t.Fatal(err)
	}
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "real-recovery-source-final-read-back-job", CommandID: finalSourceCommand, HostID: source.Host, ResourceType: "SOURCE_ROOT_SAFETY", ResourceID: sourceAttachmentID, DesiredRevision: 1, CommandType: libvirtvolume.SourceRootSafetyReadBackCommandType, SchemaVersion: libvirtvolume.SourceRootSafetyReadBackSchema, TargetResourceID: "attachment:" + sourceAttachmentID, Payload: map[string]any{"domain_uuid": vmID, "volume_id": sourceVolumeID, "binding_id": sourceBindingID, "vg_uuid": sourceVGUUID, "lv_uuid": sourceLVUUID, "backend_resource_key": sourceResourceKey}}); err != nil {
		t.Fatal(err)
	}
	finalSource := executeRealCampaignCommand(t, ctx, pool, databaseURL, runner, source, finalSourceCommand, "source-final-read-back")
	if holder, _ := finalSource.Observation.Evidence["holder_open"].(bool); holder {
		t.Fatalf("source root holder reopened: %+v", finalSource.Observation.Evidence)
	}

	verification, err := EvaluateRecoveryVerification(ctx, pool, "real-recovery-terminal-verification", operationID, "real-recovery-terminal-verifier/v1", digestBytes([]byte("real-recovery-terminal-verifier/v1")))
	if err != nil || verification.ResultState != "VERIFIED" {
		t.Fatalf("terminal verification=%+v err=%v", verification, err)
	}
	decision, err := CommitRecoveryTerminalDecision(ctx, pool, "real-recovery-terminal-decision", verification.VerificationID, "real-recovery-terminal-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := CommitRecoveryTerminalDecision(ctx, pool, decision.TerminalDecisionID, verification.VerificationID, "real-recovery-terminal-authority/v1"); err != nil || replay.DecisionDigest != decision.DecisionDigest {
		t.Fatalf("terminal replay=%+v err=%v", replay, err)
	}
	var operationState, epochState, budgetState string
	var terminalCount, verifiedCount, recoveredCount, releasedCount int
	if err := pool.QueryRow(ctx, `SELECT c.lifecycle_state,e.epoch_state,b.claim_state,(SELECT count(*) FROM kim.recovery_terminal_decision_evidence WHERE recovery_operation_id=$1),(SELECT count(*) FROM kim.recovery_operation_transition_evidence WHERE recovery_operation_id=$1 AND to_state='VERIFIED'),(SELECT count(*) FROM kim.failure_epoch_transition_evidence WHERE failure_epoch_id=o.failure_epoch_id AND to_state='RECOVERED'),(SELECT count(*) FROM kim.recovery_budget_claim_transition_evidence WHERE claim_id=$2 AND to_state='RELEASED') FROM kim.recovery_operations_current c JOIN kim.recovery_operation_evidence o USING(recovery_operation_id) JOIN kim.failure_epochs_current e ON e.failure_epoch_id=o.failure_epoch_id JOIN kim.recovery_budget_claims_current b ON b.claim_id=o.recovery_budget_claim_id WHERE c.recovery_operation_id=$1`, operationID, budgetClaimID).Scan(&operationState, &epochState, &budgetState, &terminalCount, &verifiedCount, &recoveredCount, &releasedCount); err != nil {
		t.Fatal(err)
	}
	if operationState != "VERIFIED" || epochState != "RECOVERED" || budgetState != "RELEASED" || terminalCount != 1 || verifiedCount != 1 || recoveredCount != 1 || releasedCount != 1 {
		t.Fatalf("terminal=%s/%s/%s counts=%d/%d/%d/%d", operationState, epochState, budgetState, terminalCount, verifiedCount, recoveredCount, releasedCount)
	}
	var sourceState, destinationState string
	if err := pool.QueryRow(ctx, `SELECT (SELECT observed_power_state FROM kim.vm_power_observation_evidence WHERE vm_id=$1 AND host_id=$2 ORDER BY observation_generation DESC LIMIT 1),(SELECT observed_power_state FROM kim.vm_power_observation_evidence WHERE vm_id=$1 AND host_id=$3 ORDER BY observation_generation DESC LIMIT 1)`, vmID, source.Host, destination.Host).Scan(&sourceState, &destinationState); err != nil || sourceState != "SHUTOFF" || destinationState != "RUNNING" {
		t.Fatalf("split brain assertion source=%s destination=%s err=%v", sourceState, destinationState, err)
	}
}
