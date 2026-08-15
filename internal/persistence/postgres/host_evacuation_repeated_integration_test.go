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

type repeatedEvacuationIncarnation struct {
	Host, Admission, Plan, Volume, Attachment, Binding, LV, Backend, VG string
	Materialization, PortGeneration, BindingGeneration                  uint64
}

type repeatedEvacuationMove struct {
	Operation, ShutdownAuthority, ShutdownCommand, PlannedQuiescence string
	StorageSafety, NetworkRetirementAuthority, RetirementEvidence    string
	NetworkQuiescence, Handoff, Relocation                           string
	DestinationRealization, DestinationDataplane                     string
	ChildVerification, ChildTerminal, ParentTerminal                 string
	Source, Destination                                              repeatedEvacuationIncarnation
}

type repeatedEvacuationPort struct {
	PortID, NetworkID, SegmentID, MAC                       string
	SourcePortGeneration, SourceBindingGeneration           uint64
	RetirementIntentGeneration, DestinationIntentGeneration uint64
}

func qualifyDelayedRepeatedLocalLVMCleanup(t *testing.T, ctx context.Context, db recoveryQualificationDB, terminalID, suffix string) LocalLVMSourceCleanupAuthority {
	t.Helper()
	operationID := "repeated-local-lvm-cleanup-" + suffix
	authority, err := CommitHostEvacuationSourceLocalLVMCleanup(ctx, db, operationID, 1, terminalID)
	if err != nil {
		t.Fatalf("delayed cleanup producer %s: %v", suffix, err)
	}
	claim, err := ClaimBackendCleanup(ctx, db, operationID, 1, "repeated-cleanup-worker-"+suffix, time.Minute)
	if err != nil || claim.Mode != "APPLY_ALLOWED" {
		t.Fatalf("delayed cleanup claim=%+v err=%v", claim, err)
	}
	commandID := "repeated-local-lvm-delete-command-" + suffix
	if _, err := AuthorizeLocalLVMCleanupCommand(ctx, db, claim, "repeated-local-lvm-delete-job-"+suffix, commandID); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"backend_id": authority.SourceBackendID, "backend_generation": float64(authority.SourceBackendGeneration), "vg_uuid": authority.SourceVGUUID, "expected_lv_uuid": authority.SourceLVUUID, "backend_resource_key": authority.SourceBackendResourceKey, "binding_id": authority.SourceBindingID, "binding_generation": float64(authority.SourceBindingGeneration), "cleanup_operation_id": operationID, "cleanup_generation": float64(1), "desired_state": "ABSENT", "exact_source_lv_present": false, "foreign_replacement_present": false, "observed_lv_uuid": ""}
	verificationID := "repeated-local-lvm-delete-verification-" + suffix
	attempt := recordCleanupLeaseLossVerification(t, ctx, db, authority.SourceHostID, commandID, verificationID, "MATCHED", 1, evidence)
	present, running := false, false
	observationID := "repeated-local-lvm-delete-absence-" + suffix
	observation := LocalLVMCleanupObservation{BackendCleanupObservation: BackendCleanupObservation{EvidenceID: observationID, OperationID: operationID, ResourceType: "LOCAL_LVM_VOLUME", ResourceID: authority.SourceVolumeID, SourceHostID: authority.SourceHostID, VMID: authority.VMID, BackendIdentityDigest: authority.BackendIdentityDigest, ApplyResponseState: "LOST", CommandID: commandID, VerificationID: verificationID, VerifierDigest: digestBytes([]byte(commandID + "/verifier")), ObservationDigest: digestBytes([]byte(commandID + "/observation")), ResultState: "ABSENT", ArtifactDigest: digestBytes([]byte("local-lvm-delete-adapter/v1")), EvidenceDigest: digestBytes([]byte(observationID + "/evidence")), OperationGeneration: 1, ClaimGeneration: claim.ClaimGeneration, ResourceGeneration: authority.SourceBindingGeneration, VMGeneration: authority.VMGeneration, MaterializationGeneration: authority.MaterializationGeneration, ObservationGeneration: 1, AttemptIndex: attempt, BackendPresent: &present, BackendRunning: &running, IdentityMatches: true}}
	if err := CompleteLocalLVMCleanup(ctx, db, claim, observation); err != nil {
		t.Fatalf("delayed exact absence %s: %v", suffix, err)
	}
	if _, err := ReclaimLocalLVMSourceCapacity(ctx, db, operationID, 1, "repeated-local-lvm-reclamation-"+suffix); err != nil {
		t.Fatalf("delayed reclamation %s: %v", suffix, err)
	}
	return authority
}

func assertRepeatedEvacuationCurrent(t *testing.T, ctx context.Context, db recoveryQualificationDB, vmID, portID, networkID, subnetID, mac, ip, host string, materialization, portGeneration, bindingGeneration uint64) {
	t.Helper()
	var currentHost, currentPort, currentNetwork, currentSubnet, currentMAC, currentIP, dataplaneHost string
	var currentMaterialization, currentPortGeneration, currentBindingGeneration uint64
	var activeDataplanes int
	if err := db.QueryRow(ctx, `SELECT vm.host_id,p.port_id,p.network_id,p.subnet_id,mac.mac_address::text,host(ip.ip_address),(plan.plan_payload->>'materialization_generation')::bigint,p.port_generation,b.binding_generation,(SELECT count(*) FROM kim.vm_port_dataplane_state_current d WHERE d.vm_id=vm.vm_id AND d.convergence_state='CONVERGED'),(SELECT e.host_id FROM kim.vm_port_dataplane_state_current d JOIN kim.vm_port_dataplane_observation_evidence e ON e.evidence_id=d.evidence_id WHERE d.vm_id=vm.vm_id AND d.convergence_state='CONVERGED') FROM kim.virtual_machines_current vm JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id JOIN kim.network_ports_current p ON p.placement_admission_id=vm.placement_admission_id JOIN kim.port_bindings_current b ON b.port_id=p.port_id JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' WHERE vm.vm_id=$1`, vmID).Scan(&currentHost, &currentPort, &currentNetwork, &currentSubnet, &currentMAC, &currentIP, &currentMaterialization, &currentPortGeneration, &currentBindingGeneration, &activeDataplanes, &dataplaneHost); err != nil || currentHost != host || currentPort != portID || currentNetwork != networkID || currentSubnet != subnetID || currentMAC != mac || currentIP != ip || currentMaterialization != materialization || currentPortGeneration != portGeneration || currentBindingGeneration != bindingGeneration || activeDataplanes != 1 || dataplaneHost != host {
		t.Fatalf("current host=%s dataplane=%s/%d VM mat=%d Port=%s %d/%d identity=%s/%s/%s/%s want host=%s mat=%d Port=%s %d/%d identity=%s/%s/%s/%s err=%v", currentHost, dataplaneHost, activeDataplanes, currentMaterialization, currentPort, currentPortGeneration, currentBindingGeneration, currentNetwork, currentSubnet, currentMAC, currentIP, host, materialization, portID, portGeneration, bindingGeneration, networkID, subnetID, mac, ip, err)
	}
}

func registerRepeatedEvacuationStorage(t *testing.T, ctx context.Context, db recoveryQualificationDB, host, storageClass, suffix string, index int) (string, string) {
	t.Helper()
	backend, vg := fmt.Sprintf("evacuation-repeated-backend-%d-%s", index, suffix), fmt.Sprintf("evacuation-repeated-vg-%d-%s", index, suffix)
	if err := RegisterLocalLVMFoundation(ctx, db, LocalLVMFoundation{BackendID: backend, HostID: host, VGUUID: vg, BackendState: "ACTIVE", BackendGeneration: 1, CapabilityState: "CURRENT", HostCapabilityGeneration: 1, SupportTier: "VALIDATED", StorageClassID: storageClass, StorageClassRevision: 1, ClassState: "ACTIVE", FencingPolicyRevision: 1, CapacityObservationID: fmt.Sprintf("evacuation-repeated-capacity-%d-%s", index, suffix), CapacityGeneration: 1, CapacityState: "CURRENT", HealthState: "HEALTHY", TotalBytes: 1 << 30, ObservedFreeBytes: 1 << 30, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return backend, vg
}

func executeRepeatedEvacuationMove(t *testing.T, ctx context.Context, db recoveryQualificationDB, suffix, label, vmID, imageID, imageChecksum, networkID, segmentID, portID, mac string, source repeatedEvacuationIncarnation, destinationHost, destinationBackend, destinationVG string, sourcePortGeneration, sourceBindingGeneration, sourcePowerObservation, destinationPowerObservation, retirementIntentGeneration, destinationIntentGeneration uint64, old *repeatedEvacuationMove, foreign map[string]string, additionalPorts ...repeatedEvacuationPort) repeatedEvacuationMove {
	t.Helper()
	ports := append([]repeatedEvacuationPort{{PortID: portID, NetworkID: networkID, SegmentID: segmentID, MAC: mac, SourcePortGeneration: sourcePortGeneration, SourceBindingGeneration: sourceBindingGeneration, RetirementIntentGeneration: retirementIntentGeneration, DestinationIntentGeneration: destinationIntentGeneration}}, additionalPorts...)
	move := repeatedEvacuationMove{Operation: "evacuation-repeated-" + label + "-" + suffix, Source: source}
	operation, children, err := StartHostEvacuation(ctx, db, HostEvacuationRequest{OperationID: move.Operation, SourceHostID: source.Host, EvacuationGeneration: 1, SourceHostAuthorityGeneration: 1, DrainPolicyID: "planned", DrainPolicyRevision: 1, EvacuationPolicyRevision: 1, MaximumConcurrentWorkloads: 1, Reason: "repeated incarnation " + label, RequestedBy: "integration"})
	if err != nil || operation.WorkloadCount != 1 || len(children) != 1 || operation.SourceHostID != source.Host {
		t.Fatalf("%s start=%+v children=%+v err=%v", label, operation, children, err)
	}
	var snapshotHost, snapshotAdmission, snapshotPlan string
	var snapshotMaterialization uint64
	var snapshotNetworkCount int
	if err := db.QueryRow(ctx, `SELECT e.source_host_id,e.source_admission_id,e.source_plan_id,e.source_materialization_generation,jsonb_array_length(e.network_requirements) FROM kim.host_evacuation_workload_evidence e WHERE e.child_operation_id=$1`, children[0].ChildOperationID).Scan(&snapshotHost, &snapshotAdmission, &snapshotPlan, &snapshotMaterialization, &snapshotNetworkCount); err != nil || snapshotHost != source.Host || snapshotAdmission != source.Admission || snapshotPlan != source.Plan || snapshotMaterialization != source.Materialization || snapshotNetworkCount != len(ports) {
		t.Fatalf("%s snapshot=%s/%s/%s mat=%d Ports=%d err=%v", label, snapshotHost, snapshotAdmission, snapshotPlan, snapshotMaterialization, snapshotNetworkCount, err)
	}
	for _, p := range ports {
		var snapshotPort, snapshotBinding uint64
		var snapshotIdentity bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_evacuation_workload_evidence e CROSS JOIN LATERAL jsonb_array_elements(e.network_requirements) requirement WHERE e.child_operation_id=$1 AND requirement->>'PortID'=$2),(SELECT port_generation FROM kim.network_ports_current WHERE port_id=$2),(SELECT binding_generation FROM kim.port_bindings_current WHERE port_id=$2)`, children[0].ChildOperationID, p.PortID).Scan(&snapshotIdentity, &snapshotPort, &snapshotBinding); err != nil || !snapshotIdentity || snapshotPort != p.SourcePortGeneration || snapshotBinding != p.SourceBindingGeneration {
			t.Fatalf("%s snapshot Port=%s %d/%d err=%v", label, p.PortID, snapshotPort, snapshotBinding, err)
		}
	}
	if err := EvaluateHostEvacuationEligibility(ctx, db, move.Operation); err != nil {
		t.Fatalf("%s eligibility: %v", label, err)
	}
	claim, err := ClaimHostEvacuationWorkload(ctx, db, move.Operation, "repeated-worker-"+label, time.Minute)
	if err != nil || claim.ClaimGeneration != 1 {
		t.Fatalf("%s claim=%+v err=%v", label, claim, err)
	}
	move.ShutdownAuthority, move.ShutdownCommand = "evacuation-repeated-shutdown-authority-"+label+"-"+suffix, "evacuation-repeated-shutdown-command-"+label+"-"+suffix
	if old != nil {
		if err := AuthorizeHostEvacuationSourceShutdown(ctx, db, claim, old.ShutdownAuthority, "old-shutdown-job-"+label+"-"+suffix, old.ShutdownCommand); err == nil {
			t.Fatal("old E1 shutdown authority uplifted to E2 source")
		}
		if err := ReleaseHostEvacuationSourcePlacement(ctx, db, claim, "old-safety-release-"+label+"-"+suffix, old.StorageSafety); !errors.Is(err, ErrHostEvacuationBlocked) {
			t.Fatalf("old E1 Storage safety uplift error=%v", err)
		}
		if _, err := AuthorizeHostEvacuationNetworkPortRetirement(ctx, db, claim, HostEvacuationNetworkRetirementRequest{AuthorityID: old.NetworkRetirementAuthority, PortID: portID, OperationID: "old-retirement-uplift-" + label + "-" + suffix, OperationGeneration: 1, IntentID: "old-retirement-intent-uplift-" + label + "-" + suffix, IntentGeneration: retirementIntentGeneration}); err == nil {
			t.Fatal("old E1 Network retirement authority uplifted to E2 2/2")
		}
	}
	if foreign != nil {
		if err := ReleaseHostEvacuationSourcePlacement(ctx, db, claim, "foreign-safety-release-"+label+"-"+suffix, foreign["storage_safety"]); !errors.Is(err, ErrHostEvacuationBlocked) {
			t.Fatalf("foreign-origin Storage safety authorized planned release: %v", err)
		}
	}
	if err := AuthorizeHostEvacuationSourceShutdown(ctx, db, claim, move.ShutdownAuthority, "evacuation-repeated-shutdown-job-"+label+"-"+suffix, move.ShutdownCommand); err != nil {
		t.Fatalf("%s shutdown authorization: %v", label, err)
	}
	acceptEvacuationLostReadBack(t, ctx, db, source.Host, move.ShutdownCommand, "evacuation-repeated-shutdown-verification-"+label+"-"+suffix, sourcePowerObservation, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"})
	move.PlannedQuiescence = "evacuation-repeated-planned-quiescence-" + label + "-" + suffix
	if _, err := RecordPlannedSourceQuiescence(ctx, db, claim, move.PlannedQuiescence); err != nil {
		t.Fatalf("%s planned quiescence: %v", label, err)
	}
	rootCommand, rootVerification := "evacuation-repeated-root-command-"+label+"-"+suffix, "evacuation-repeated-root-verification-"+label+"-"+suffix
	if err := CreateExecutionCommand(ctx, db, ExecutionCommandRequest{JobID: "evacuation-repeated-root-job-" + label + "-" + suffix, CommandID: rootCommand, HostID: source.Host, ResourceType: "VOLUME_ATTACHMENT", ResourceID: source.Attachment, DesiredRevision: int64(source.Materialization), CommandType: SourceRootSafetyReadBackCommandType, SchemaVersion: SourceRootSafetyReadBackSchema, TargetResourceID: "attachment:" + source.Attachment, Payload: map[string]any{"desired_state": "OBSERVE"}}); err != nil {
		t.Fatalf("%s root safety command: %v", label, err)
	}
	rootEvidence := map[string]any{"attachment_id": source.Attachment, "volume_id": source.Volume, "binding_id": source.Binding, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": source.LV, "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": false}
	rootObservationGeneration := source.Materialization
	var previousRootObservation uint64
	if err := db.QueryRow(ctx, `SELECT coalesce(max(observation_generation),0) FROM kim.volume_attachment_observation_evidence WHERE attachment_id=$1`, source.Attachment).Scan(&previousRootObservation); err != nil {
		t.Fatal(err)
	}
	if rootObservationGeneration <= previousRootObservation {
		rootObservationGeneration = previousRootObservation + 1
	}
	rootAttempt := acceptEvacuationCommand(t, ctx, db, source.Host, rootCommand, rootVerification, rootObservationGeneration, rootEvidence, "SUCCEEDED")
	if err := AcceptSourceRootSafetyObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: "evacuation-repeated-root-evidence-" + label + "-" + suffix, AttachmentID: source.Attachment, VolumeID: source.Volume, AttachmentGeneration: 1, BindingID: source.Binding, BindingGeneration: 1, HostID: source.Host, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: source.LV, CommandID: rootCommand, VerificationID: rootVerification, AttemptIndex: uint32(rootAttempt), ObservationGeneration: rootObservationGeneration, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true}); err != nil {
		t.Fatalf("%s source root safety observation: %v", label, err)
	}
	move.StorageSafety = "evacuation-repeated-storage-safety-" + label + "-" + suffix
	if err := EvaluateHostEvacuationSourceStorageSafety(ctx, db, claim, move.StorageSafety); err != nil {
		t.Fatalf("%s source storage safety: %v", label, err)
	}
	for i, p := range ports {
		portLabel := fmt.Sprintf("%s-p%d", label, i)
		authorityID := "evacuation-repeated-retirement-authority-" + portLabel + "-" + suffix
		retirementOperation, retirementIntent := "evacuation-repeated-retirement-"+portLabel+"-"+suffix, "evacuation-repeated-retirement-intent-"+portLabel+"-"+suffix
		retirement, err := AuthorizeHostEvacuationNetworkPortRetirement(ctx, db, claim, HostEvacuationNetworkRetirementRequest{AuthorityID: authorityID, PortID: p.PortID, OperationID: retirementOperation, OperationGeneration: 1, IntentID: retirementIntent, IntentGeneration: p.RetirementIntentGeneration})
		if err != nil || retirement.PortGeneration != p.SourcePortGeneration || retirement.BindingGeneration != p.SourceBindingGeneration {
			t.Fatalf("%s retirement Port=%s decision=%+v err=%v", label, p.PortID, retirement, err)
		}
		owner := "repeated-retirement-worker-" + portLabel + "-" + suffix
		work, err := ClaimOVNRuntimeWork(ctx, db, OVNRuntimeClaimRequest{Owner: owner, Limit: 1, Lease: time.Minute})
		if err != nil || len(work) != 1 || work[0].WorkID != retirement.WorkID {
			t.Fatalf("%s retirement work Port=%s work=%+v err=%v", label, p.PortID, work, err)
		}
		retirementClaim := OVNRuntimeClaim{WorkID: retirement.WorkID, Owner: owner, ClaimGeneration: work[0].ClaimGeneration}
		if err := AuthorizeOVNRuntimeApply(ctx, db, retirementClaim); err != nil {
			t.Fatalf("%s retirement apply authorization Port=%s: %v", label, p.PortID, err)
		}
		retirementEvidence := "evacuation-repeated-retirement-evidence-" + portLabel + "-" + suffix
		if err := CompleteOVNPortBindingRetirement(ctx, db, retirementClaim, OVNPortBindingRetirementObservation{EvidenceID: retirementEvidence, IntentID: retirementIntent, IntentGeneration: p.RetirementIntentGeneration, PortID: p.PortID, PortGeneration: p.SourcePortGeneration, BindingGeneration: p.SourceBindingGeneration, SourceHostID: source.Host, OperationGeneration: 1, NBObservationGeneration: p.RetirementIntentGeneration, SBObservationGeneration: p.RetirementIntentGeneration, OVSObservationGeneration: p.RetirementIntentGeneration, NBObservationDigest: digestBytes([]byte("retirement-nb-" + portLabel + "-" + suffix)), SBObservationDigest: digestBytes([]byte("retirement-sb-" + portLabel + "-" + suffix)), OVSObservationDigest: digestBytes([]byte("retirement-ovs-" + portLabel + "-" + suffix)), AdapterArtifactDigest: digestBytes([]byte("retirement-adapter-" + portLabel + "-" + suffix)), ApplyResponseState: "RECEIVED", Observation: ovnadapter.PortBindingRetirementObservation{OwnershipMarkerMatches: true, ObjectSetDigestMatches: true, LogicalSwitchPresent: true, LogicalSwitchPortPresent: true, RequestedChassisAbsent: true, SourceChassisInactive: true, SourceOVSInterfaceAbsent: true}}); err != nil {
			t.Fatalf("%s retirement completion Port=%s: %v", label, p.PortID, err)
		}
		networkQuiescence, err := PrepareHostEvacuationNetworkPortSourceQuiescence(ctx, db, claim, HostEvacuationNetworkQuiescenceRequest{PortID: p.PortID, JobID: "evacuation-repeated-network-quiescence-job-" + portLabel + "-" + suffix, CommandID: "evacuation-repeated-network-quiescence-command-" + portLabel + "-" + suffix})
		if err != nil {
			t.Fatalf("%s source network quiescence preparation Port=%s: %v", label, p.PortID, err)
		}
		networkVerification := "evacuation-repeated-network-quiescence-verification-" + portLabel + "-" + suffix
		networkAttempt := acceptEvacuationCommand(t, ctx, db, source.Host, networkQuiescence.CommandID, networkVerification, p.SourcePortGeneration, map[string]any{"domain_uuid": vmID, "vm_generation": float64(1), "port_id": p.PortID, "port_generation": float64(p.SourcePortGeneration), "binding_generation": float64(p.SourceBindingGeneration), "domain_running": false, "interface_present": false}, "SUCCEEDED")
		networkEvidence := "evacuation-repeated-network-quiescence-evidence-" + portLabel + "-" + suffix
		if err := AcceptHostEvacuationNetworkPortSourceQuiescence(ctx, db, claim, HostEvacuationNetworkQuiescenceObservation{EvidenceID: networkEvidence, PortID: p.PortID, SourceHostID: source.Host, VMID: vmID, CommandID: networkQuiescence.CommandID, VerificationID: networkVerification, PortGeneration: p.SourcePortGeneration, BindingGeneration: p.SourceBindingGeneration, VMGeneration: 1, ObservationGeneration: p.SourcePortGeneration, AttemptIndex: uint32(networkAttempt), ObservationDigest: digestBytes([]byte(networkQuiescence.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(networkQuiescence.CommandID + "/verifier"))}); err != nil {
			t.Fatalf("%s source network quiescence observation Port=%s: %v", label, p.PortID, err)
		}
		if i == 0 {
			move.NetworkRetirementAuthority, move.RetirementEvidence, move.NetworkQuiescence = authorityID, retirementEvidence, networkEvidence
		}
	}
	releaseID := "evacuation-repeated-release-" + label + "-" + suffix
	if err := ReleaseHostEvacuationSourcePlacement(ctx, db, claim, releaseID, move.StorageSafety); err != nil {
		t.Fatalf("%s source placement release: %v", label, err)
	}
	destinationRequest, err := BuildHostEvacuationDestinationPlacementRequest(ctx, db, claim, "evacuation-repeated-destination-"+label+"-"+suffix, destinationHost)
	if err != nil || len(destinationRequest.Network) != len(ports) {
		t.Fatalf("%s destination request=%+v err=%v", label, destinationRequest, err)
	}
	for _, p := range ports {
		matched := false
		for _, required := range destinationRequest.Network {
			if required.PortID == p.PortID && required.SourcePortGeneration == p.SourcePortGeneration && required.SourceBindingGeneration == p.SourceBindingGeneration {
				matched = true
				if p.PortID == portID {
					move.Handoff = required.HandoffID
				}
			}
		}
		if !matched {
			t.Fatalf("%s destination request missing exact Port=%s: %+v", label, p.PortID, destinationRequest.Network)
		}
	}
	if old != nil {
		stale := destinationRequest
		stale.RequestID += "-old-handoff"
		stale.Network = append([]placement.NetworkRequirement(nil), destinationRequest.Network...)
		stale.Network[0].HandoffID = "old-handoff-uplift-" + label + "-" + suffix
		stale.Network[0].SourceQuiescenceEvidenceID = old.NetworkQuiescence
		stale.Network[0].SourcePortGeneration = old.Source.PortGeneration
		stale.Network[0].SourceBindingGeneration = old.Source.BindingGeneration
		stale.Network[0].DestinationPortGeneration = old.Destination.PortGeneration
		stale.Network[0].DestinationBindingGeneration = old.Destination.BindingGeneration
		if dry, err := DryEvaluatePlacement(ctx, db, stale, destinationHost); err != nil || dry.Eligible {
			t.Fatalf("old A->B Handoff/quiescence accepted for C: dry=%+v err=%v", dry, err)
		}
	}
	if foreign != nil {
		stale := destinationRequest
		stale.RequestID += "-foreign-handoff"
		stale.Network = append([]placement.NetworkRequirement(nil), destinationRequest.Network...)
		stale.Network[0].HandoffID = foreign["handoff"]
		stale.Network[0].SourceQuiescenceEvidenceID = foreign["network_quiescence"]
		stale.Network[0].SourcePortGeneration = sourcePortGeneration - 1
		stale.Network[0].SourceBindingGeneration = sourceBindingGeneration - 1
		stale.Network[0].DestinationPortGeneration = sourcePortGeneration
		stale.Network[0].DestinationBindingGeneration = sourceBindingGeneration
		if dry, err := DryEvaluatePlacement(ctx, db, stale, destinationHost); err != nil || dry.Eligible {
			t.Fatalf("Recovery A->B Handoff/quiescence accepted for planned C: dry=%+v err=%v", dry, err)
		}
	}
	dry, err := DryEvaluatePlacement(ctx, db, destinationRequest, destinationHost)
	if err != nil || !dry.Eligible {
		t.Fatalf("%s destination dry=%+v err=%v", label, dry, err)
	}
	destinationAdmission, err := FinalAdmitPlacement(ctx, db, destinationRequest, dry)
	if err != nil {
		t.Fatalf("%s destination final request=%+v dry=%+v: %v", label, destinationRequest, dry, err)
	}
	for i, p := range ports {
		var portAuthoritySource string
		if err := db.QueryRow(ctx, `SELECT authority_source FROM kim.network_ports_current WHERE port_id=$1`, p.PortID).Scan(&portAuthoritySource); err != nil {
			t.Fatalf("%s Port authority source Port=%s: %v", label, p.PortID, err)
		}
		if portAuthoritySource != "PORT_RESOURCE" {
			continue
		}
		resource, err := GetPortResource(ctx, db, p.PortID)
		if err != nil || resource.RealizationState != "PENDING" {
			t.Fatalf("%s destination Port realization Port=%s resource=%+v err=%v", label, p.PortID, resource, err)
		}
		resourceClaim, err := ClaimPortRealization(ctx, db, resource.OperationID, fmt.Sprintf("repeated-port-resource-worker-%s-p%d-%s", label, i, suffix), time.Minute)
		if err != nil {
			t.Fatalf("%s destination Port claim Port=%s: %v", label, p.PortID, err)
		}
		_, resourcePlan, err := ovnadapter.RestoreStoredPortResourcePlan(resourceClaim.CanonicalPlan, resourceClaim.PlanDigest)
		if err != nil {
			t.Fatalf("%s destination Port plan Port=%s: %v", label, p.PortID, err)
		}
		if err := AuthorizePortRealizationApply(ctx, db, resourceClaim); err != nil {
			t.Fatalf("%s destination Port apply authorization Port=%s: %v", label, p.PortID, err)
		}
		if _, err := AcceptPortRealizationObservation(ctx, db, resourceClaim, matchedPortObservation(resourceClaim, resourcePlan, fmt.Sprintf("repeated-port-resource-lsp-%s-p%d-%s", label, i, suffix), "RECEIVED")); err != nil {
			t.Fatalf("%s destination Port observation Port=%s: %v", label, p.PortID, err)
		}
	}
	if foreign != nil {
		if _, err := PrepareVMMaterialization(ctx, db, VMMaterializationRequest{VMID: vmID, AdmissionID: destinationAdmission.AdmissionID, PlanID: "foreign-relocation-plan-" + label + "-" + suffix, JobID: "foreign-relocation-job-" + label + "-" + suffix, CommandID: "foreign-relocation-command-" + label + "-" + suffix, RelocationAuthorityID: foreign["operation"], MaterializationGeneration: source.Materialization + 1}); err == nil {
			t.Fatal("Recovery Operation authorized planned destination materialization")
		}
	}
	destinationBinding, destinationLV := realizeEvacuationBinding(t, ctx, db, destinationHost, destinationAdmission.AdmissionID, destinationRequest.Storage[0].VolumeID, destinationBackend, destinationVG, destinationRequest.RequestID)
	qualifyEvacuationLocalLVMCopy(t, ctx, db, claim, "repeated-"+label+"-"+suffix, destinationAdmission.AdmissionID, move.StorageSafety, digestBytes([]byte("repeated-guest-state/"+label+"/"+suffix)), false)
	move.Relocation = "evacuation-repeated-relocation-" + label + "-" + suffix
	if old != nil {
		if err := AuthorizeHostEvacuationRelocation(ctx, db, claim, "old-storage-relocation-"+label+"-"+suffix, destinationAdmission.AdmissionID, old.StorageSafety, releaseID); !errors.Is(err, ErrHostEvacuationBlocked) {
			t.Fatalf("old E1 safety authorized E2 relocation: %v", err)
		}
	}
	if err := AuthorizeHostEvacuationRelocation(ctx, db, claim, move.Relocation, destinationAdmission.AdmissionID, move.StorageSafety, releaseID); err != nil {
		t.Fatalf("%s relocation authorization: %v", label, err)
	}
	destinationPlan := "evacuation-repeated-destination-plan-" + label + "-" + suffix
	if old != nil {
		if _, err := PrepareVMMaterialization(ctx, db, VMMaterializationRequest{VMID: vmID, AdmissionID: destinationAdmission.AdmissionID, PlanID: "old-relocation-plan-" + label + "-" + suffix, JobID: "old-relocation-job-" + label + "-" + suffix, CommandID: "old-relocation-command-" + label + "-" + suffix, RelocationAuthorityID: old.Relocation, MaterializationGeneration: source.Materialization + 1}); err == nil {
			t.Fatal("old E1 relocation authority materialized E2 destination")
		}
	}
	if foreign != nil {
		if _, err := FinalizeHostEvacuation(ctx, db, move.Operation, foreign["terminal"]); !errors.Is(err, ErrHostEvacuationConflict) {
			t.Fatalf("Recovery terminal identity mutated EVACUATE parent: %v", err)
		}
	}
	materializeEvacuationVM(t, ctx, db, destinationHost, vmID, destinationAdmission.AdmissionID, destinationPlan, imageID, imageChecksum, destinationRequest.Storage[0].VolumeID, destinationBinding, destinationVG, destinationLV, "repeated-destination-"+label+"-"+suffix, move.Relocation, source.Materialization+1)
	for i, p := range ports {
		portLabel := fmt.Sprintf("%s-p%d", label, i)
		completeEvacuationOVNIntent(t, ctx, db, "evacuation-repeated-destination-intent-"+portLabel+"-"+suffix, p.PortID, "repeated-destination-worker-"+portLabel+"-"+suffix, "repeated-destination-"+portLabel+"-"+suffix, p.DestinationIntentGeneration)
		realization := realizeEvacuationOVSPort(t, ctx, db, destinationHost, vmID, destinationPlan, p.PortID, p.NetworkID, p.SegmentID, p.MAC, "repeated-destination-"+portLabel+"-"+suffix, "evacuation-repeated-power-job-"+label+"-"+suffix, "evacuation-repeated-power-command-"+label+"-"+suffix, p.SourcePortGeneration+1, p.SourceBindingGeneration+1, source.Materialization+1)
		if i == 0 {
			move.DestinationRealization = realization
		}
	}
	acceptEvacuationCommand(t, ctx, db, destinationHost, "evacuation-repeated-power-command-"+label+"-"+suffix, "evacuation-repeated-power-verification-"+label+"-"+suffix, destinationPowerObservation, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")
	for i, p := range ports {
		portLabel := fmt.Sprintf("%s-p%d", label, i)
		dataplane := convergeEvacuationOVSDataplane(t, ctx, db, destinationHost, vmID, destinationPlan, p.PortID, p.NetworkID, p.SegmentID, p.MAC, "repeated-destination-"+portLabel+"-"+suffix, p.SourcePortGeneration+1, p.SourceBindingGeneration+1, source.Materialization+1)
		if i == 0 {
			move.DestinationDataplane = dataplane
		}
	}
	move.ChildVerification = "evacuation-repeated-child-verification-" + label + "-" + suffix
	if foreign != nil {
		rollback := errors.New("rollback foreign destination evidence")
		err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE kim.vm_power_state_current SET evidence_id=$2 WHERE vm_id=$1`, vmID, foreign["power_evidence"]); err != nil {
				return err
			}
			if _, err := EvaluateHostEvacuationChildEvidence(ctx, scopeTxBeginner{tx}, claim, move.ChildVerification+"-foreign-power", "evacuation-repeated-destination-binding-"+label+"-foreign-power-"+suffix, destinationAdmission.AdmissionID); !errors.Is(err, ErrHostEvacuationBlocked) {
				t.Fatalf("Recovery destination power evidence verified EVACUATE destination: %v", err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		if err := CompleteHostEvacuationChild(ctx, db, claim, foreign["verification"], "foreign-verification-terminal-"+label+"-"+suffix); err == nil {
			t.Fatal("Recovery verification completed EVACUATE child")
		}
	}
	if old != nil {
		rollback := errors.New("rollback old destination evidence")
		err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			oldPowerID := "vm-power/evacuation-repeated-power-command-e1-" + suffix + "/1"
			if _, err := tx.Exec(ctx, `UPDATE kim.vm_power_state_current SET evidence_id=$2 WHERE vm_id=$1`, vmID, oldPowerID); err != nil {
				return err
			}
			if _, err := EvaluateHostEvacuationChildEvidence(ctx, scopeTxBeginner{tx}, claim, move.ChildVerification+"-old-power", "evacuation-repeated-destination-binding-"+label+"-old-power-"+suffix, destinationAdmission.AdmissionID); !errors.Is(err, ErrHostEvacuationBlocked) {
				t.Fatalf("old E1 destination power evidence verified E2: %v", err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		if err := CompleteHostEvacuationChild(ctx, db, claim, old.ChildVerification, "old-child-verification-terminal-"+label+"-"+suffix); err == nil {
			t.Fatal("old E1 child verification completed E2 child")
		}
	}
	if _, err := EvaluateHostEvacuationChildEvidence(ctx, db, claim, move.ChildVerification, "evacuation-repeated-destination-binding-"+label+"-"+suffix, destinationAdmission.AdmissionID); err != nil {
		t.Fatal(err)
	}
	move.ChildTerminal = "evacuation-repeated-child-terminal-" + label + "-" + suffix
	if label == "e2" || label == "mixed-e2" {
		rollback := errors.New("rollback terminal 4/4 drift")
		err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET port_generation=4 WHERE port_id=$1`, portID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_generation=4 WHERE port_id=$1`, portID); err != nil {
				return err
			}
			if err := CompleteHostEvacuationChild(ctx, scopeTxBeginner{tx}, claim, move.ChildVerification, move.ChildTerminal+"-drift"); !errors.Is(err, ErrHostEvacuationStale) {
				t.Fatalf("E2 old verification accepted 4/4 drift: %v", err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
	if err := CompleteHostEvacuationChild(ctx, db, claim, move.ChildVerification, move.ChildTerminal); err != nil {
		t.Fatal(err)
	}
	move.ParentTerminal = "evacuation-repeated-parent-terminal-" + label + "-" + suffix
	if old != nil {
		if _, err := FinalizeHostEvacuation(ctx, db, move.Operation, old.ParentTerminal); err == nil {
			t.Fatal("old E1 parent terminal identity mutated E2 parent")
		}
	}
	parent, err := FinalizeHostEvacuation(ctx, db, move.Operation, move.ParentTerminal)
	if err != nil || parent.LifecycleState != "VERIFIED" || parent.WorkloadCount != 1 {
		t.Fatalf("%s parent=%+v err=%v", label, parent, err)
	}
	if replay, err := FinalizeHostEvacuation(ctx, db, move.Operation, move.ParentTerminal); err != nil || replay.LifecycleState != "VERIFIED" || replay.WorkloadCount != 1 {
		t.Fatalf("%s parent replay=%+v err=%v", label, replay, err)
	}
	move.Destination = repeatedEvacuationIncarnation{Host: destinationHost, Admission: destinationAdmission.AdmissionID, Plan: destinationPlan, Volume: destinationRequest.Storage[0].VolumeID, Attachment: destinationRequest.Storage[0].AttachmentID, Binding: destinationBinding, LV: destinationLV, Backend: destinationBackend, VG: destinationVG, Materialization: source.Materialization + 1, PortGeneration: sourcePortGeneration + 1, BindingGeneration: sourceBindingGeneration + 1}
	return move
}

func TestHostEvacuationRepeatedIncarnationPostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('evacuation-repeated',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	vmID := fmt.Sprintf("70000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	hostA, hostB, hostC := "evacuation-repeated-a-"+suffix, "evacuation-repeated-b-"+suffix, "evacuation-repeated-c-"+suffix
	poolID, workloadID, imageID, flavorID, storageClass := "evacuation-repeated-pool-"+suffix, "evacuation-repeated-workload-"+suffix, "evacuation-repeated-image-"+suffix, "evacuation-repeated-flavor-"+suffix, "evacuation-repeated-storage-"+suffix
	networkID, subnetID, segmentID, portID := "evacuation-repeated-network-"+suffix, "evacuation-repeated-subnet-"+suffix, "evacuation-repeated-segment-"+suffix, "evacuation-repeated-port-"+suffix
	mac, ip := "02:00:00:70:00:01", "192.0.2.70"
	for _, host := range []string{hostA, hostB, hostC} {
		prepareEvacuationHost(t, ctx, pool, host)
	}
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: poolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "evacuation-repeated", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	members := []HostGroupMembership{{HostGroupID: poolID, HostID: hostA, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}, {HostGroupID: poolID, HostID: hostB, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}, {HostGroupID: poolID, HostID: hostC, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "evacuation-repeated-members-" + suffix, HostGroupID: poolID, SourceType: "EXPLICIT", SourceRevision: suffix, BasedOnHostGroupGeneration: 1, Members: members}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_placement_pool_memberships_current(host_id,pool_id,membership_generation,membership_state) VALUES($1,$4,1,'ACTIVE'),($2,$4,1,'ACTIVE'),($3,$4,1,'ACTIVE')`, hostA, hostB, hostC, poolID); err != nil {
		t.Fatal(err)
	}
	if err := UpsertNetworkFoundation(ctx, pool, NetworkFoundation{NetworkID: networkID, ProjectID: "project", NetworkGeneration: 1, NetworkState: "ACTIVE", MTU: 1500, SubnetID: subnetID, SubnetGeneration: 1, SubnetState: "ACTIVE", CIDR: "192.0.2.0/24", AllocationStart: "192.0.2.10", AllocationEnd: "192.0.2.200", SegmentClaimID: segmentID, SegmentType: "VLAN", ScopeID: "repeated-" + suffix, SegmentID: 700, SegmentGeneration: 1, ProviderMappingRevision: 1, SegmentState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{hostA, hostB, hostC} {
		if err := UpsertHostNetworkMapping(ctx, pool, HostNetworkMapping{HostID: host, SegmentClaimID: segmentID, Generation: 1, State: "CURRENT", MaximumMTU: 9000, SupportedBindingTypes: []string{"OVS"}, OVNChassisName: "chassis-" + host}); err != nil {
			t.Fatal(err)
		}
	}
	backendA, vgA := registerRepeatedEvacuationStorage(t, ctx, pool, hostA, storageClass, suffix, 1)
	backendB, vgB := registerRepeatedEvacuationStorage(t, ctx, pool, hostB, storageClass, suffix, 2)
	checksum := digestBytes([]byte("evacuation-repeated-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "qualification://evacuation-repeated", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "evacuation-repeated", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	sourceRequest := PlacementAdmissionRequest{RequestID: "evacuation-repeated-source-" + suffix, ProjectID: "project", WorkloadID: workloadID, ImageID: imageID, FlavorID: flavorID, PoolID: poolID, Network: []placement.NetworkRequirement{{PortID: portID, NetworkID: networkID, NetworkGeneration: 1, SubnetID: subnetID, SubnetGeneration: 1, SegmentClaimID: segmentID, SegmentGeneration: 1, HostMappingGeneration: 1, IPAddress: ip, MACAddress: mac, BindingType: "OVS", RequiredMTU: 1500}}, Storage: []placement.StorageRequirement{{VolumeID: "evacuation-repeated-root-a-" + suffix, AttachmentID: "evacuation-repeated-attachment-a-" + suffix, BackendID: backendA, BackendGeneration: 1, VGUUID: vgA, StorageClassID: storageClass, StorageClassRevision: 1, CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1, SizeBytes: 16 << 20, AccessMode: "SINGLE_WRITER", Bootable: true}}}
	dry, err := DryEvaluatePlacement(ctx, pool, sourceRequest, hostA)
	if err != nil || !dry.Eligible {
		t.Fatalf("initial dry=%+v err=%v", dry, err)
	}
	sourceAdmission, err := FinalAdmitPlacement(ctx, pool, sourceRequest, dry)
	if err != nil {
		t.Fatal(err)
	}
	bindingA, lvA := realizeEvacuationBinding(t, ctx, pool, hostA, sourceAdmission.AdmissionID, sourceRequest.Storage[0].VolumeID, backendA, vgA, sourceRequest.RequestID)
	planA := "evacuation-repeated-plan-a-" + suffix
	materializeEvacuationVM(t, ctx, pool, hostA, vmID, sourceAdmission.AdmissionID, planA, imageID, checksum, sourceRequest.Storage[0].VolumeID, bindingA, vgA, lvA, "repeated-a-"+suffix, "", 1)
	completeEvacuationOVNIntent(t, ctx, pool, "evacuation-repeated-intent-a-"+suffix, portID, "repeated-worker-a-"+suffix, "repeated-a-"+suffix, 1)
	realizeEvacuationOVSPort(t, ctx, pool, hostA, vmID, planA, portID, networkID, segmentID, mac, "repeated-a-"+suffix, "evacuation-repeated-power-job-a-"+suffix, "evacuation-repeated-power-command-a-"+suffix, 1, 1, 1)
	acceptEvacuationCommand(t, ctx, pool, hostA, "evacuation-repeated-power-command-a-"+suffix, "evacuation-repeated-power-verification-a-"+suffix, 1, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")
	convergeEvacuationOVSDataplane(t, ctx, pool, hostA, vmID, planA, portID, networkID, segmentID, mac, "repeated-a-"+suffix, 1, 1, 1)
	initial := repeatedEvacuationIncarnation{Host: hostA, Admission: sourceAdmission.AdmissionID, Plan: planA, Volume: sourceRequest.Storage[0].VolumeID, Attachment: sourceRequest.Storage[0].AttachmentID, Binding: bindingA, LV: lvA, Backend: backendA, VG: vgA, Materialization: 1, PortGeneration: 1, BindingGeneration: 1}
	assertRepeatedEvacuationCurrent(t, ctx, pool, vmID, portID, networkID, subnetID, mac, ip, hostA, 1, 1, 1)
	beforeEpochs, beforeFencing := 0, 0
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&beforeEpochs, &beforeFencing); err != nil {
		t.Fatal(err)
	}
	e1 := executeRepeatedEvacuationMove(t, ctx, pool, suffix, "e1", vmID, imageID, checksum, networkID, segmentID, portID, mac, initial, hostB, backendB, vgB, 1, 1, 2, 3, 2, 3, nil, nil)
	var afterE1Host, afterE1Admission, afterE1Plan, afterE1MAC, afterE1IP string
	var afterE1Materialization, afterE1Port, afterE1Binding uint64
	if err := pool.QueryRow(ctx, `SELECT vm.host_id,vm.placement_admission_id,vm.current_plan_id,(plan.plan_payload->>'materialization_generation')::bigint,p.port_generation,b.binding_generation,mac.mac_address::text,host(ip.ip_address) FROM kim.virtual_machines_current vm JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id JOIN kim.network_ports_current p ON p.placement_admission_id=vm.placement_admission_id JOIN kim.port_bindings_current b ON b.port_id=p.port_id JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' WHERE vm.vm_id=$1`, vmID).Scan(&afterE1Host, &afterE1Admission, &afterE1Plan, &afterE1Materialization, &afterE1Port, &afterE1Binding, &afterE1MAC, &afterE1IP); err != nil || afterE1Host != hostB || afterE1Admission != e1.Destination.Admission || afterE1Plan != e1.Destination.Plan || afterE1Materialization != 2 || afterE1Port != 2 || afterE1Binding != 2 || afterE1MAC != mac || afterE1IP != ip {
		t.Fatalf("E1 current=%s/%s/%s mat=%d Port=%d/%d identity=%s/%s err=%v", afterE1Host, afterE1Admission, afterE1Plan, afterE1Materialization, afterE1Port, afterE1Binding, afterE1MAC, afterE1IP, err)
	}
	assertRepeatedEvacuationCurrent(t, ctx, pool, vmID, portID, networkID, subnetID, mac, ip, hostB, 2, 2, 2)
	// A rollback-only partial E2 proves an E2 blocker cannot roll E1 back or
	// move the current VM away from B.
	rollback := errors.New("rollback partial E2")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		partialID := "evacuation-repeated-e2-partial-" + suffix
		_, children, err := StartHostEvacuation(ctx, scopeTxBeginner{tx}, HostEvacuationRequest{OperationID: partialID, SourceHostID: hostB, EvacuationGeneration: 1, SourceHostAuthorityGeneration: 1, DrainPolicyID: "planned", DrainPolicyRevision: 1, EvacuationPolicyRevision: 1, MaximumConcurrentWorkloads: 1, Reason: "partial rollback branch", RequestedBy: "integration"})
		if err != nil || len(children) != 1 {
			t.Fatalf("partial E2 start=%+v err=%v", children, err)
		}
		var e1State, vmHost string
		if err := tx.QueryRow(ctx, `SELECT (SELECT lifecycle_state FROM kim.host_evacuation_operations_current WHERE evacuation_operation_id=$1),(SELECT host_id FROM kim.virtual_machines_current WHERE vm_id=$2)`, e1.Operation, vmID).Scan(&e1State, &vmHost); err != nil || e1State != "VERIFIED" || vmHost != hostB {
			t.Fatalf("partial E2 changed E1/current=%s/%s err=%v", e1State, vmHost, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	backendC, vgC := registerRepeatedEvacuationStorage(t, ctx, pool, hostC, storageClass, suffix, 3)
	e2 := executeRepeatedEvacuationMove(t, ctx, pool, suffix, "e2", vmID, imageID, checksum, networkID, segmentID, portID, mac, e1.Destination, hostC, backendC, vgC, 2, 2, 4, 5, 4, 5, &e1, nil)
	var finalHost, finalMAC, finalIP, drainA, drainB, latestHandoff string
	var finalMaterialization, finalPort, finalBinding, activeDataplanes int
	if err := pool.QueryRow(ctx, `SELECT vm.host_id,(plan.plan_payload->>'materialization_generation')::int,p.port_generation,b.binding_generation,mac.mac_address::text,host(ip.ip_address),da.drain_state,db.drain_state,h.handoff_id,(SELECT count(*) FROM kim.vm_port_dataplane_state_current d JOIN kim.vm_port_dataplane_observation_evidence e ON e.evidence_id=d.evidence_id WHERE d.vm_id=vm.vm_id AND d.convergence_state='CONVERGED' AND e.host_id=vm.host_id) FROM kim.virtual_machines_current vm JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id JOIN kim.network_ports_current p ON p.placement_admission_id=vm.placement_admission_id JOIN kim.port_bindings_current b ON b.port_id=p.port_id JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' JOIN kim.host_placement_drains_current da ON da.source_host_id=$2 JOIN kim.host_placement_drains_current db ON db.source_host_id=$3 JOIN kim.port_binding_handoffs_current h ON h.port_id=p.port_id WHERE vm.vm_id=$1`, vmID, hostA, hostB).Scan(&finalHost, &finalMaterialization, &finalPort, &finalBinding, &finalMAC, &finalIP, &drainA, &drainB, &latestHandoff, &activeDataplanes); err != nil || finalHost != hostC || finalMaterialization != 3 || finalPort != 3 || finalBinding != 3 || finalMAC != mac || finalIP != ip || drainA != "DRAINED" || drainB != "DRAINED" || latestHandoff != e2.Handoff || activeDataplanes != 1 {
		t.Fatalf("final=%s mat=%d Port=%d/%d identity=%s/%s drains=%s/%s latest=%s active=%d err=%v", finalHost, finalMaterialization, finalPort, finalBinding, finalMAC, finalIP, drainA, drainB, latestHandoff, activeDataplanes, err)
	}
	assertRepeatedEvacuationCurrent(t, ctx, pool, vmID, portID, networkID, subnetID, mac, ip, hostC, 3, 3, 3)
	cleanupA := qualifyDelayedRepeatedLocalLVMCleanup(t, ctx, pool, e1.ChildTerminal, "a-after-c-"+suffix)
	cleanupB := qualifyDelayedRepeatedLocalLVMCleanup(t, ctx, pool, e2.ChildTerminal, "b-after-c-"+suffix)
	if cleanupA.SourceHostID != hostA || cleanupA.SourceVolumeID != initial.Volume || cleanupB.SourceHostID != hostB || cleanupB.SourceVolumeID != e1.Destination.Volume || cleanupA.SourceLVUUID == cleanupB.SourceLVUUID {
		t.Fatalf("delayed incarnation cleanup identities A=%+v B=%+v", cleanupA, cleanupB)
	}
	var historicalHandoffs, historicalRetirements, historicalNetworkBindings, historicalRealizations, historicalDataplanes, parentTerminals, childTerminals int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.port_binding_handoff_evidence WHERE handoff_id=ANY($1)),(SELECT count(*) FROM kim.network_port_binding_retirement_evidence WHERE evidence_id=ANY($2)),(SELECT count(*) FROM kim.host_evacuation_child_network_evidence_binding WHERE verification_id=ANY($3)),(SELECT count(*) FROM kim.host_evacuation_terminal_evidence WHERE terminal_evidence_id=ANY($4)),(SELECT count(*) FROM kim.host_evacuation_child_terminal_evidence WHERE terminal_evidence_id=ANY($5)),(SELECT count(*) FROM kim.vm_network_port_realization_evidence WHERE evidence_id=ANY($6)),(SELECT count(*) FROM kim.vm_port_dataplane_observation_evidence WHERE evidence_id=ANY($7))`, []string{e1.Handoff, e2.Handoff}, []string{e1.RetirementEvidence, e2.RetirementEvidence}, []string{e1.ChildVerification, e2.ChildVerification}, []string{e1.ParentTerminal, e2.ParentTerminal}, []string{e1.ChildTerminal, e2.ChildTerminal}, []string{e1.DestinationRealization, e2.DestinationRealization}, []string{e1.DestinationDataplane, e2.DestinationDataplane}).Scan(&historicalHandoffs, &historicalRetirements, &historicalNetworkBindings, &parentTerminals, &childTerminals, &historicalRealizations, &historicalDataplanes); err != nil || historicalHandoffs != 2 || historicalRetirements != 2 || historicalNetworkBindings != 2 || historicalRealizations != 2 || historicalDataplanes != 2 || parentTerminals != 2 || childTerminals != 2 {
		t.Fatalf("history handoffs=%d retirements=%d networkBindings=%d realizations=%d dataplanes=%d parents=%d children=%d err=%v", historicalHandoffs, historicalRetirements, historicalNetworkBindings, historicalRealizations, historicalDataplanes, parentTerminals, childTerminals, err)
	}
	if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		for label, candidate := range map[string]struct{ admission, source string }{"A": {e1.Destination.Admission, hostA}, "B": {e2.Destination.Admission, hostB}} {
			required, matched, evidenceSet, err := recoverySourceNetworkCleanupEvidenceTx(ctx, tx, candidate.admission, candidate.source)
			if err != nil || required != 1 || matched != 1 || evidenceSet == "" {
				t.Fatalf("delayed %s cleanup resolver=%d/%d/%q err=%v", label, required, matched, evidenceSet, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var afterEpochs, afterFencing int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&afterEpochs, &afterFencing); err != nil || beforeEpochs != afterEpochs || beforeFencing != afterFencing {
		t.Fatalf("Failure/Fencing generated %d/%d -> %d/%d err=%v", beforeEpochs, beforeFencing, afterEpochs, afterFencing, err)
	}
	for _, item := range []struct{ label, table, idColumn, id string }{
		{"E1 quiescence", "planned_source_quiescence_evidence", "quiescence_evidence_id", e1.PlannedQuiescence},
		{"E1 retirement", "network_port_binding_retirement_evidence", "evidence_id", e1.RetirementEvidence},
		{"E1 Handoff", "port_binding_handoff_evidence", "handoff_id", e1.Handoff},
		{"E1 verification", "host_evacuation_child_verification_evidence", "verification_id", e1.ChildVerification},
		{"E1 terminal", "host_evacuation_child_terminal_evidence", "terminal_evidence_id", e1.ChildTerminal},
		{"E1 parent", "host_evacuation_terminal_evidence", "terminal_evidence_id", e1.ParentTerminal},
		{"E2 retirement", "network_port_binding_retirement_evidence", "evidence_id", e2.RetirementEvidence},
		{"E2 Handoff", "port_binding_handoff_evidence", "handoff_id", e2.Handoff},
	} {
		statement := fmt.Sprintf("UPDATE kim.%s SET recorded_at=recorded_at WHERE %s=$1", item.table, item.idColumn)
		if _, err := pool.Exec(ctx, statement, item.id); err == nil {
			t.Fatalf("immutable %s accepted UPDATE", item.label)
		}
	}
}
