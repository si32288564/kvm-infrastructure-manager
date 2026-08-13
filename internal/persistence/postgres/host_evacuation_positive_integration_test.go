package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

type evacuationPositiveFixture struct {
	Suffix, SourceHost, DestinationHost, PoolID, VMID, WorkloadID, ImageID, FlavorID, StorageClassID string
	SourceAdmission, SourcePlan, SourceBinding, SourceVolume, SourceAttachment, SourceLV             string
	DestinationAdmission, DestinationPlan, DestinationBinding, DestinationVolume, DestinationLV      string
}

func acceptEvacuationCommand(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID, commandID, verificationID string, observationGeneration uint64, evidence map[string]any, outcome string) int {
	t.Helper()
	grant, err := AcquireCommandLease(ctx, db, CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: 1, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	result := contract.CommandResult{SchemaVersion: contract.CommandResultSchema, CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, JournalDigest: digestBytes([]byte(commandID + "/journal")), ResultID: commandID + "/result", Outcome: outcome, Result: map[string]any{"state": "APPLIED"}, Observation: contract.Observation{State: "MATCHED", Digest: digestBytes([]byte(commandID + "/observation")), Generation: int64(observationGeneration), Evidence: evidence}, VerifierDigest: digestBytes([]byte(commandID + "/verifier"))}
	payload, _ := json.Marshal(result)
	envelope := session.NewEnvelope(hostID, uint64(grant.SessionGeneration), session.StreamResult, commandID+"/result-message", contract.CommandResultSchema, commandID, uint64(grant.AttemptIndex), payload)
	envelope.CorrelationKey = commandID
	receipt, err := AcceptAgentCommandResult(ctx, db, envelope, 1<<20, AgentCommandResultDecision{Start: CommandAttemptStart{CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, JournalEvidenceDigest: result.JournalDigest}, Result: CommandResultSubmission{CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, ResultID: result.ResultID, Outcome: outcome, Payload: result.Result}, Verification: &CommandVerification{VerificationID: verificationID, CommandID: commandID, AttemptIndex: grant.AttemptIndex, ObservationGeneration: int64(observationGeneration), ObservationDigest: result.Observation.Digest, State: "MATCHED", VerifierArtifactDigest: result.VerifierDigest, Evidence: evidence}})
	if err != nil || receipt.Disposition != "ACCEPTED" {
		t.Fatalf("command %s receipt=%+v err=%v", commandID, receipt, err)
	}
	return grant.AttemptIndex
}

func acceptEvacuationLostReadBack(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID, commandID, verificationID string, observationGeneration uint64, evidence map[string]any) int {
	t.Helper()
	grant, err := AcquireCommandLease(ctx, db, CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: 1, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE kim.command_leases_current SET expires_at=statement_timestamp()-interval '1 microsecond' WHERE command_id=$1 AND lease_generation=$2`, commandID, grant.LeaseGeneration); err != nil {
		t.Fatal(err)
	}
	if err := ExpireCommandLease(ctx, db, commandID); err != nil {
		t.Fatal(err)
	}
	candidate, err := LoadCommandVerificationCandidate(ctx, db, commandID)
	if err != nil {
		t.Fatal(err)
	}
	observationDigest, verifierDigest := digestBytes([]byte(commandID+"/read-back")), digestBytes([]byte(commandID+"/verifier"))
	observation := contract.VerificationObservation{SchemaVersion: contract.VerificationObservationSchema, CommandID: commandID, AttemptIndex: grant.AttemptIndex, TargetResourceID: candidate.TargetResourceID, CommandPayloadDigest: candidate.PayloadDigest, Observation: contract.Observation{State: "MATCHED", Digest: observationDigest, Generation: int64(observationGeneration), Evidence: evidence}, VerifierDigest: verifierDigest, JournalDigest: digestBytes([]byte(commandID + "/journal"))}
	payload, _ := json.Marshal(observation)
	envelope := session.NewEnvelope(hostID, uint64(candidate.SessionGeneration), session.StreamResync, commandID+"/read-back-message", contract.VerificationObservationSchema, commandID, uint64(grant.AttemptIndex), payload)
	envelope.CorrelationKey = commandID
	receipt, err := AcceptAgentVerificationObservation(ctx, db, envelope, 1<<20, AgentVerificationObservationDecision{TargetResourceID: candidate.TargetResourceID, CommandPayloadDigest: candidate.PayloadDigest, Verification: CommandVerification{VerificationID: verificationID, CommandID: commandID, AttemptIndex: grant.AttemptIndex, ObservationGeneration: int64(observationGeneration), ObservationDigest: observationDigest, State: "MATCHED", VerifierArtifactDigest: verifierDigest, Evidence: map[string]any{"journal_digest": observation.JournalDigest, "read_back": evidence}}})
	if err != nil || receipt.Disposition != "ACCEPTED" {
		t.Fatalf("lost read-back %s receipt=%+v err=%v", commandID, receipt, err)
	}
	return grant.AttemptIndex
}

func prepareEvacuationHost(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID string) {
	t.Helper()
	fingerprint := digestBytes([]byte("evacuation-positive/" + hostID))
	prepareSessionIdentityFixture(t, ctx, db, hostID, 1, fingerprint)
	if _, err := AdmitAgentSession(ctx, db, AgentSessionAdmission{SessionAttemptID: hostID + "/session", HostID: hostID, ConnectionInstanceID: "evacuation-positive", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("evacuation-agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, db, hostID)
	if err := UpdateHostReadinessGate(ctx, db, HostReadinessGate{HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArmHostOperationAuthority(ctx, db, HostAuthorityArmRequest{HostID: hostID, PolicyID: "evacuation-positive", PolicyGeneration: 1, ActorID: "integration", ReasonCode: "positive_e2e"}); err != nil {
		t.Fatal(err)
	}
}

func realizeEvacuationBinding(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID, admissionID, volumeID, backendID, vgUUID, suffix string) (string, string) {
	t.Helper()
	bindingID := "storage-binding:" + suffix + ":" + volumeID
	var resourceKey string
	if err := db.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1 AND placement_admission_id=$2`, bindingID, admissionID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	lvUUID := "lv-" + suffix
	commandID, verificationID := "lvm-command-"+suffix, "lvm-verification-"+suffix
	if err := CreateExecutionCommand(ctx, db, ExecutionCommandRequest{JobID: "lvm-job-" + suffix, CommandID: commandID, HostID: hostID, ResourceType: "VOLUME", ResourceID: volumeID, DesiredRevision: 1, CommandType: locallvm.CommandType, SchemaVersion: locallvm.SchemaVersion, TargetResourceID: "volume:" + volumeID, Payload: map[string]any{"vg_uuid": vgUUID, "size_mib": 16, "desired_state": "PRESENT"}}); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"vg_uuid": vgUUID, "observed_vg_uuid": vgUUID, "observed_lv_uuid": lvUUID, "backend_resource_key": resourceKey, "observed_size_bytes": float64(16 << 20)}
	attempt := acceptEvacuationCommand(t, ctx, db, hostID, commandID, verificationID, 1, evidence, "SUCCEEDED")
	if err := AcceptLocalLVMBindingObservation(ctx, db, LocalLVMBindingObservation{EvidenceID: "binding-evidence-" + suffix, BindingID: bindingID, VolumeID: volumeID, BackendID: backendID, HostID: hostID, VGUUID: vgUUID, LVUUID: lvUUID, BackendResourceKey: resourceKey, BindingGeneration: 1, CommandID: commandID, VerificationID: verificationID, AttemptIndex: uint32(attempt), ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(commandID + "/observation")), VerifierDigest: digestBytes([]byte(commandID + "/verifier")), EvidenceState: "MATCHED", ObservedSizeBytes: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	return bindingID, lvUUID
}

func materializeEvacuationVM(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID, vmID, admissionID, planID, imageID, checksum, volumeID, bindingID, vgUUID, lvUUID, suffix, relocationID string, generation uint64) VMMaterializationDecision {
	t.Helper()
	request := VMMaterializationRequest{VMID: vmID, AdmissionID: admissionID, PlanID: planID, JobID: "define-job-" + suffix, CommandID: "define-command-" + suffix, RelocationAuthorityID: relocationID, MaterializationGeneration: generation}
	if !vmUUIDPattern.MatchString(request.VMID) || request.AdmissionID == "" || request.PlanID == "" || request.JobID == "" || request.CommandID == "" {
		t.Fatalf("invalid materialization request: %+v", request)
	}
	decision, err := PrepareVMMaterialization(ctx, db, request)
	if err != nil {
		t.Fatalf("prepare materialization: %v", err)
	}
	defineVerification := "define-verification-" + suffix
	defineEvidence := map[string]any{"domain_uuid": vmID, "materialization_generation": float64(generation), "plan_digest": decision.PlanDigest, "domain_present": true, "domain_identity_matches": true, "plan_identity_matches": true, "compute_shape_matches": true, "root_volume_identity_matches": true, "image_materialization_state": "PENDING", "network_realization_state": "PENDING"}
	defineAttempt := acceptEvacuationCommand(t, ctx, db, hostID, request.CommandID, defineVerification, generation, defineEvidence, "SUCCEEDED")
	if err := AcceptVMDefinitionObservation(ctx, db, VMDefinitionObservation{EvidenceID: "define-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: planID, PlanDigest: decision.PlanDigest, HostID: hostID, CommandID: request.CommandID, AttemptIndex: uint32(defineAttempt), VerificationID: defineVerification, ObservationGeneration: generation, ObservationDigest: digestBytes([]byte(request.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(request.CommandID + "/verifier")), EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatalf("accept definition: %v", err)
	}
	imageRequest := VMImageMaterializationRequest{VMID: vmID, PlanID: planID, JobID: "image-job-" + suffix, CommandID: "image-command-" + suffix}
	if _, err := PrepareVMImageMaterialization(ctx, db, imageRequest); err != nil {
		t.Fatalf("prepare image: %v", err)
	}
	var resourceKey string
	if err := db.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, bindingID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	imageVerification := "image-verification-" + suffix
	imageEvidence := map[string]any{"domain_uuid": vmID, "materialization_generation": float64(generation), "image_id": imageID, "image_revision": float64(1), "expected_content_digest": checksum, "observed_content_digest": checksum, "image_size_bytes": float64(4096), "volume_id": volumeID, "observed_vg_uuid": vgUUID, "observed_lv_uuid": lvUUID, "backend_resource_key": resourceKey, "holder_open": false, "content_identity_matches": true}
	imageAttempt := acceptEvacuationCommand(t, ctx, db, hostID, imageRequest.CommandID, imageVerification, generation, imageEvidence, "SUCCEEDED")
	if err := AcceptVMImageRealizationObservation(ctx, db, VMImageRealizationObservation{EvidenceID: "image-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: planID, PlanDigest: decision.PlanDigest, HostID: hostID, ImageID: imageID, ImageRevision: 1, ExpectedDigest: checksum, ObservedDigest: checksum, ImageSizeBytes: 4096, VolumeID: volumeID, BindingID: bindingID, BindingGeneration: 1, VGUUID: vgUUID, LVUUID: lvUUID, BackendResourceKey: resourceKey, CommandID: imageRequest.CommandID, AttemptIndex: uint32(imageAttempt), VerificationID: imageVerification, ObservationGeneration: generation, ObservationDigest: digestBytes([]byte(imageRequest.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(imageRequest.CommandID + "/verifier")), EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatalf("accept image: %v", err)
	}
	return decision
}

func TestHostEvacuationNonEmptyZeroPortPositivePostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('evacuation-positive',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	f := evacuationPositiveFixture{Suffix: fmt.Sprint(now), VMID: fmt.Sprintf("68000000-0000-4000-8000-%012d", now%1_000_000_000_000)}
	f.SourceHost, f.DestinationHost, f.PoolID = "evacuation-positive-source-"+f.Suffix, "evacuation-positive-destination-"+f.Suffix, "evacuation-positive-pool-"+f.Suffix
	f.WorkloadID, f.ImageID, f.FlavorID, f.StorageClassID = "evacuation-positive-workload-"+f.Suffix, "evacuation-positive-image-"+f.Suffix, "evacuation-positive-flavor-"+f.Suffix, "evacuation-positive-storage-"+f.Suffix
	prepareEvacuationHost(t, ctx, pool, f.SourceHost)
	prepareEvacuationHost(t, ctx, pool, f.DestinationHost)
	if err := UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: f.PoolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "evacuation-positive", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "evacuation-positive-members-" + f.Suffix, HostGroupID: f.PoolID, SourceType: "EXPLICIT", SourceRevision: f.Suffix, BasedOnHostGroupGeneration: 1, Members: []HostGroupMembership{{HostGroupID: f.PoolID, HostID: f.SourceHost, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: f.Suffix}, {HostGroupID: f.PoolID, HostID: f.DestinationHost, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: f.Suffix}}}); err != nil {
		t.Fatal(err)
	}
	// The legacy Placement Pool projection remains a StartHostEvacuation input;
	// seed it from the already-published exact two-member HostGroup set.
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_placement_pool_memberships_current(host_id,pool_id,membership_generation,membership_state) VALUES($1,$3,1,'ACTIVE'),($2,$3,1,'ACTIVE')`, f.SourceHost, f.DestinationHost, f.PoolID); err != nil {
		t.Fatal(err)
	}
	for index, host := range []string{f.SourceHost, f.DestinationHost} {
		if err := RegisterLocalLVMFoundation(ctx, pool, LocalLVMFoundation{BackendID: fmt.Sprintf("evacuation-positive-backend-%d-%s", index+1, f.Suffix), HostID: host, VGUUID: fmt.Sprintf("evacuation-positive-vg-%d-%s", index+1, f.Suffix), BackendState: "ACTIVE", BackendGeneration: 1, CapabilityState: "CURRENT", HostCapabilityGeneration: 1, SupportTier: "VALIDATED", StorageClassID: f.StorageClassID, StorageClassRevision: 1, ClassState: "ACTIVE", FencingPolicyRevision: 1, CapacityObservationID: fmt.Sprintf("evacuation-positive-capacity-%d-%s", index+1, f.Suffix), CapacityGeneration: 1, CapacityState: "CURRENT", HealthState: "HEALTHY", TotalBytes: 1 << 30, ObservedFreeBytes: 1 << 30, ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	checksum := digestBytes([]byte("evacuation-positive-image"))
	if _, err := RegisterImageRevision(ctx, pool, ImageRevision{ImageID: f.ImageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: checksum, ObservedChecksum: checksum, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "qualification://evacuation-positive", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: f.FlavorID, Revision: 1, OwnerProjectID: "project", Name: "evacuation-positive", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	sourceRequest := PlacementAdmissionRequest{RequestID: "evacuation-source-" + f.Suffix, ProjectID: "project", WorkloadID: f.WorkloadID, ImageID: f.ImageID, FlavorID: f.FlavorID, PoolID: f.PoolID, Storage: []placement.StorageRequirement{{VolumeID: "evacuation-source-root-" + f.Suffix, AttachmentID: "evacuation-source-attachment-" + f.Suffix, BackendID: "evacuation-positive-backend-1-" + f.Suffix, BackendGeneration: 1, VGUUID: "evacuation-positive-vg-1-" + f.Suffix, StorageClassID: f.StorageClassID, StorageClassRevision: 1, CapacityGeneration: 1, AttachmentGeneration: 1, FencingPolicyRevision: 1, SizeBytes: 16 << 20, AccessMode: "SINGLE_WRITER", Bootable: true}}}
	sourceDry, err := DryEvaluatePlacement(ctx, pool, sourceRequest, f.SourceHost)
	if err != nil || !sourceDry.Eligible {
		t.Fatalf("source dry=%+v err=%v", sourceDry, err)
	}
	sourceAdmission, err := FinalAdmitPlacement(ctx, pool, sourceRequest, sourceDry)
	if err != nil {
		t.Fatal(err)
	}
	f.SourceAdmission, f.SourceVolume, f.SourceAttachment = sourceAdmission.AdmissionID, sourceRequest.Storage[0].VolumeID, sourceRequest.Storage[0].AttachmentID
	f.SourceBinding, f.SourceLV = realizeEvacuationBinding(t, ctx, pool, f.SourceHost, f.SourceAdmission, f.SourceVolume, sourceRequest.Storage[0].BackendID, sourceRequest.Storage[0].VGUUID, sourceRequest.RequestID)
	f.SourcePlan = "evacuation-source-plan-" + f.Suffix
	materializeEvacuationVM(t, ctx, pool, f.SourceHost, f.VMID, f.SourceAdmission, f.SourcePlan, f.ImageID, checksum, f.SourceVolume, f.SourceBinding, sourceRequest.Storage[0].VGUUID, f.SourceLV, "source-"+f.Suffix, "", 1)
	if err := AuthorizeZeroPortVMPowerOn(ctx, pool, f.VMID, 1, f.SourceHost, "source-power-job-"+f.Suffix, "source-power-command-"+f.Suffix); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, pool, f.SourceHost, "source-power-command-"+f.Suffix, "source-power-verification-"+f.Suffix, 1, map[string]any{"domain_uuid": f.VMID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")

	raceRequest := PlacementAdmissionRequest{RequestID: "post-drain-race-" + f.Suffix, ProjectID: "project", WorkloadID: "post-drain-race-" + f.Suffix, ImageID: f.ImageID, FlavorID: f.FlavorID, PoolID: f.PoolID}
	raceDry, err := DryEvaluatePlacement(ctx, pool, raceRequest, f.SourceHost)
	if err != nil || !raceDry.Eligible {
		t.Fatal("pre-drain source dry must be eligible")
	}
	beforeEpochs, beforeFencing := 0, 0
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)`).Scan(&beforeEpochs, &beforeFencing); err != nil {
		t.Fatal(err)
	}
	operationID := "evacuation-positive-" + f.Suffix
	operation, children, err := StartHostEvacuation(ctx, pool, HostEvacuationRequest{OperationID: operationID, SourceHostID: f.SourceHost, EvacuationGeneration: 1, SourceHostAuthorityGeneration: 1, DrainPolicyID: "planned", DrainPolicyRevision: 1, EvacuationPolicyRevision: 1, MaximumConcurrentWorkloads: 1, Reason: "positive qualification", RequestedBy: "integration"})
	if err != nil || operation.LifecycleState != "RUNNING" || len(children) != 1 {
		t.Fatalf("start operation=%+v children=%d err=%v", operation, len(children), err)
	}
	if _, err := FinalAdmitPlacement(ctx, pool, raceRequest, raceDry); !errors.Is(err, ErrPlacementIneligible) {
		t.Fatalf("post-drain stale dry admitted source: %v", err)
	}
	if err := EvaluateHostEvacuationEligibility(ctx, pool, operationID); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimHostEvacuationWorkload(ctx, pool, operationID, "positive-worker", time.Minute)
	if err != nil || claim.ClaimGeneration != 1 {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	shutdownCommand := "evacuation-shutdown-command-" + f.Suffix
	if err := AuthorizeHostEvacuationSourceShutdown(ctx, pool, claim, "evacuation-shutdown-authority-"+f.Suffix, "evacuation-shutdown-job-"+f.Suffix, shutdownCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationLostReadBack(t, ctx, pool, f.SourceHost, shutdownCommand, "evacuation-shutdown-verification-"+f.Suffix, 2, map[string]any{"domain_uuid": f.VMID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"})
	quiescenceID := "evacuation-quiescence-" + f.Suffix
	quiescence, err := RecordPlannedSourceQuiescence(ctx, pool, claim, quiescenceID)
	if err != nil || quiescence.ShutdownResponseState != "LOST" {
		t.Fatalf("quiescence=%+v err=%v", quiescence, err)
	}

	rootCommand := "evacuation-root-readback-command-" + f.Suffix
	rootVerification := "evacuation-root-readback-verification-" + f.Suffix
	if err := CreateExecutionCommand(ctx, pool, ExecutionCommandRequest{JobID: "evacuation-root-readback-job-" + f.Suffix, CommandID: rootCommand, HostID: f.SourceHost, ResourceType: "VOLUME_ATTACHMENT", ResourceID: f.SourceAttachment, DesiredRevision: 1, CommandType: SourceRootSafetyReadBackCommandType, SchemaVersion: SourceRootSafetyReadBackSchema, TargetResourceID: "attachment:" + f.SourceAttachment, Payload: map[string]any{"desired_state": "OBSERVE"}}); err != nil {
		t.Fatal(err)
	}
	rootEvidence := map[string]any{"attachment_id": f.SourceAttachment, "volume_id": f.SourceVolume, "binding_id": f.SourceBinding, "domain_uuid": f.VMID, "target_device": "vda", "observed_lv_uuid": f.SourceLV, "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": false}
	rootAttempt := acceptEvacuationCommand(t, ctx, pool, f.SourceHost, rootCommand, rootVerification, 1, rootEvidence, "SUCCEEDED")
	if err := AcceptSourceRootSafetyObservation(ctx, pool, LocalLVMAttachmentObservation{EvidenceID: "evacuation-root-observation-" + f.Suffix, AttachmentID: f.SourceAttachment, VolumeID: f.SourceVolume, AttachmentGeneration: 1, BindingID: f.SourceBinding, BindingGeneration: 1, HostID: f.SourceHost, DomainUUID: f.VMID, TargetDevice: "vda", ObservedLVUUID: f.SourceLV, CommandID: rootCommand, VerificationID: rootVerification, AttemptIndex: uint32(rootAttempt), ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	safetyID, releaseID := "evacuation-storage-safety-"+f.Suffix, "evacuation-source-release-"+f.Suffix
	if err := EvaluateHostEvacuationSourceStorageSafety(ctx, pool, claim, safetyID); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseHostEvacuationSourcePlacement(ctx, pool, claim, releaseID, safetyID); err != nil {
		t.Fatal(err)
	}
	destinationRequest, err := BuildHostEvacuationDestinationPlacementRequest(ctx, pool, claim, "evacuation-destination-"+f.Suffix, f.DestinationHost)
	if err != nil {
		t.Fatal(err)
	}
	destinationDry, err := DryEvaluatePlacement(ctx, pool, destinationRequest, f.DestinationHost)
	if err != nil || !destinationDry.Eligible {
		t.Fatalf("destination dry=%+v err=%v", destinationDry, err)
	}
	destinationAdmission, err := FinalAdmitPlacement(ctx, pool, destinationRequest, destinationDry)
	if err != nil {
		t.Fatal(err)
	}
	f.DestinationAdmission, f.DestinationVolume = destinationAdmission.AdmissionID, destinationRequest.Storage[0].VolumeID
	var exactRequirements bool
	if err := pool.QueryRow(ctx, `SELECT s.workload_id=d.workload_id AND s.host_id<>d.host_id AND s.network_requirements='[]'::jsonb AND d.network_requirements='[]'::jsonb AND s.pci_requirements='[]'::jsonb AND d.pci_requirements='[]'::jsonb AND s.network_requirements_digest=d.network_requirements_digest AND s.pci_requirements_digest=d.pci_requirements_digest AND (s.storage_requirements->0->>'StorageClassID')=(d.storage_requirements->0->>'StorageClassID') AND (s.storage_requirements->0->>'StorageClassRevision')=(d.storage_requirements->0->>'StorageClassRevision') AND (s.storage_requirements->0->>'SizeBytes')=(d.storage_requirements->0->>'SizeBytes') AND (s.storage_requirements->0->>'AccessMode')=(d.storage_requirements->0->>'AccessMode') AND (s.storage_requirements->0->>'Bootable')=(d.storage_requirements->0->>'Bootable') FROM kim.placement_admission_decisions s JOIN kim.placement_admission_decisions d ON d.admission_id=$2 WHERE s.admission_id=$1`, f.SourceAdmission, f.DestinationAdmission).Scan(&exactRequirements); err != nil || !exactRequirements {
		t.Fatalf("destination Admission requirements are not exact: %v/%v", exactRequirements, err)
	}
	f.DestinationBinding, f.DestinationLV = realizeEvacuationBinding(t, ctx, pool, f.DestinationHost, f.DestinationAdmission, f.DestinationVolume, destinationRequest.Storage[0].BackendID, destinationRequest.Storage[0].VGUUID, destinationRequest.RequestID)
	relocationID := "evacuation-relocation-authority-" + f.Suffix
	if err := AuthorizeHostEvacuationRelocation(ctx, pool, claim, relocationID, f.DestinationAdmission, safetyID, releaseID); err != nil {
		t.Fatal(err)
	}
	var relocationCurrent bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_materialization_relocation_authority_evidence r JOIN kim.virtual_machines_current vm ON vm.vm_id=r.vm_id AND vm.vm_generation=r.vm_generation WHERE r.relocation_authority_id=$1 AND r.source_admission_id=vm.placement_admission_id AND r.source_host_id=vm.host_id AND r.destination_admission_id=$2 AND r.destination_host_id=$3 AND r.destination_materialization_generation=2)`, relocationID, f.DestinationAdmission, f.DestinationHost).Scan(&relocationCurrent); err != nil || !relocationCurrent {
		t.Fatalf("relocation authority is not current: %v/%v", relocationCurrent, err)
	}
	f.DestinationPlan = "evacuation-destination-plan-" + f.Suffix
	materializeEvacuationVM(t, ctx, pool, f.DestinationHost, f.VMID, f.DestinationAdmission, f.DestinationPlan, f.ImageID, checksum, f.DestinationVolume, f.DestinationBinding, destinationRequest.Storage[0].VGUUID, f.DestinationLV, "destination-"+f.Suffix, relocationID, 2)
	if err := AuthorizeZeroPortVMPowerOn(ctx, pool, f.VMID, 1, f.DestinationHost, "destination-power-job-"+f.Suffix, "destination-power-command-"+f.Suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateHostEvacuationChildEvidence(ctx, pool, claim, "pre-running-verification-"+f.Suffix, "pre-running-binding-"+f.Suffix, f.DestinationAdmission); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("power Command without RUNNING observation verified child: %v", err)
	}
	acceptEvacuationCommand(t, ctx, pool, f.DestinationHost, "destination-power-command-"+f.Suffix, "destination-power-verification-"+f.Suffix, 3, map[string]any{"domain_uuid": f.VMID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}, "SUCCEEDED")

	verificationID, bindingID := "evacuation-child-verification-"+f.Suffix, "evacuation-destination-binding-"+f.Suffix
	verification, err := EvaluateHostEvacuationChildEvidence(ctx, pool, claim, verificationID, bindingID, f.DestinationAdmission)
	if err != nil {
		t.Fatal(err)
	}
	var verificationState, sourceStorageState, sourceNetworkState, sourcePCIState, destinationPowerState, destinationStorageState, destinationNetworkState, destinationPCIState string
	if err := pool.QueryRow(ctx, `SELECT verification_state,source_storage_state,source_network_state,source_pci_state,destination_power_state,destination_storage_state,destination_network_state,destination_pci_state FROM kim.host_evacuation_child_verification_evidence WHERE verification_id=$1`, verificationID).Scan(&verificationState, &sourceStorageState, &sourceNetworkState, &sourcePCIState, &destinationPowerState, &destinationStorageState, &destinationNetworkState, &destinationPCIState); err != nil {
		t.Fatal(err)
	}
	if verificationState != "VERIFIED" || sourceStorageState != "SAFE" || sourceNetworkState != "NOT_REQUIRED" || sourcePCIState != "NOT_REQUIRED" || destinationPowerState != "RUNNING" || destinationStorageState != "CURRENT" || destinationNetworkState != "NOT_REQUIRED" || destinationPCIState != "NOT_REQUIRED" {
		t.Fatalf("derived child verification=%s storage=%s/%s network=%s/%s pci=%s/%s power=%s", verificationState, sourceStorageState, destinationStorageState, sourceNetworkState, destinationNetworkState, sourcePCIState, destinationPCIState, destinationPowerState)
	}
	if replay, err := EvaluateHostEvacuationChildEvidence(ctx, pool, claim, verificationID, bindingID, f.DestinationAdmission); err != nil || replay.VerificationDigest != verification.VerificationDigest {
		t.Fatalf("verification replay=%+v err=%v", replay, err)
	}
	if _, err := EvaluateHostEvacuationChildEvidence(ctx, pool, claim, verificationID, bindingID+"-wrong", f.DestinationAdmission); !errors.Is(err, ErrHostEvacuationConflict) {
		t.Fatalf("verification identity reuse=%v", err)
	}
	assertTerminalStale := func(label, mutation string, args ...any) {
		t.Helper()
		rollback := errors.New("rollback " + label)
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, mutation, args...); err != nil {
				t.Fatal(err)
			}
			if err := CompleteHostEvacuationChild(ctx, scopeTxBeginner{tx}, claim, verificationID, "stale-terminal-"+label+"-"+f.Suffix); !errors.Is(err, ErrHostEvacuationStale) {
				t.Fatalf("%s terminal drift=%v", label, err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
	assertTerminalStale("current-plan", `UPDATE kim.virtual_machines_current SET current_plan_id=$2 WHERE vm_id=$1`, f.VMID, f.SourcePlan)
	assertTerminalStale("readiness-generation", `UPDATE kim.vm_materialization_readiness_current SET observation_generation=observation_generation+1 WHERE vm_id=$1`, f.VMID)
	assertTerminalStale("readiness-definition", `UPDATE kim.vm_materialization_readiness_current SET definition_evidence_id=$2 WHERE vm_id=$1`, f.VMID, "define-evidence-source-"+f.Suffix)
	assertTerminalStale("power-evidence", `UPDATE kim.vm_power_state_current SET evidence_id=$2 WHERE vm_id=$1`, f.VMID, "vm-power/"+shutdownCommand+"/1")
	assertTerminalStale("power-generation", `UPDATE kim.vm_power_state_current SET observation_generation=observation_generation+1 WHERE vm_id=$1`, f.VMID)
	assertTerminalStale("source-authority", `UPDATE kim.host_operation_authorities_current SET authority_generation=authority_generation+1 WHERE host_id=$1`, f.SourceHost)
	assertTerminalStale("drain-removed", `DELETE FROM kim.host_placement_drains_current WHERE source_host_id=$1`, f.SourceHost)
	terminalID := "evacuation-child-terminal-" + f.Suffix
	if err := CompleteHostEvacuationChild(ctx, pool, claim, verificationID, terminalID); err != nil {
		t.Fatal(err)
	}
	if err := CompleteHostEvacuationChild(ctx, pool, claim, verificationID, terminalID); err != nil {
		t.Fatalf("terminal replay: %v", err)
	}
	if err := CompleteHostEvacuationChild(ctx, pool, claim, "different-verification", terminalID); !errors.Is(err, ErrHostEvacuationConflict) {
		t.Fatalf("terminal identity reuse=%v", err)
	}
	parentTerminalID := "evacuation-parent-terminal-" + f.Suffix
	parent, err := FinalizeHostEvacuation(ctx, pool, operationID, parentTerminalID)
	if err != nil || parent.LifecycleState != "VERIFIED" || parent.WorkloadCount != 1 {
		t.Fatalf("parent=%+v err=%v", parent, err)
	}
	var workloadCount, parentVerifiedCount, parentActiveSource, parentPostDrain int
	if err := pool.QueryRow(ctx, `SELECT workload_count,verified_count,active_source_workload_count,post_drain_admission_count FROM kim.host_evacuation_terminal_evidence WHERE terminal_evidence_id=$1`, parentTerminalID).Scan(&workloadCount, &parentVerifiedCount, &parentActiveSource, &parentPostDrain); err != nil || workloadCount != 1 || parentVerifiedCount != 1 || parentActiveSource != 0 || parentPostDrain != 0 {
		t.Fatalf("parent terminal counts=%d/%d/%d/%d err=%v", workloadCount, parentVerifiedCount, parentActiveSource, parentPostDrain, err)
	}
	var drain, childPhase, childResult, vmHost, vmAdmission, power string
	var verifiedCount, activeSource, postDrain, cleanupCount, afterEpochs, afterFencing int
	if err := pool.QueryRow(ctx, `SELECT d.drain_state,c.phase,c.result_state,vm.host_id,vm.placement_admission_id,p.observed_power_state,
		(SELECT count(*) FROM kim.host_evacuation_workloads_current WHERE evacuation_operation_id=$1 AND phase='VERIFIED'),
		(SELECT count(*) FROM kim.virtual_machines_current WHERE host_id=$2 AND lifecycle_state<>'DELETED'),
		(SELECT count(*) FROM kim.placement_admission_decisions a JOIN kim.host_evacuation_operation_evidence o ON o.evacuation_operation_id=$1 WHERE a.host_id=$2 AND a.decided_at>o.recorded_at),
		(SELECT count(*) FROM kim.backend_cleanup_operation_evidence WHERE origin_authority_id=$1),
		(SELECT count(*) FROM kim.failure_epoch_evidence),(SELECT count(*) FROM kim.failure_fencing_proof_evidence)
		FROM kim.host_placement_drains_current d JOIN kim.host_evacuation_workloads_current c ON c.evacuation_operation_id=$1 JOIN kim.virtual_machines_current vm ON vm.vm_id=c.vm_id JOIN kim.vm_power_state_current p ON p.vm_id=vm.vm_id WHERE d.source_host_id=$2`, operationID, f.SourceHost).Scan(&drain, &childPhase, &childResult, &vmHost, &vmAdmission, &power, &verifiedCount, &activeSource, &postDrain, &cleanupCount, &afterEpochs, &afterFencing); err != nil {
		t.Fatal(err)
	}
	if drain != "DRAINED" || childPhase != "VERIFIED" || childResult != "VERIFIED" || vmHost != f.DestinationHost || vmAdmission != f.DestinationAdmission || power != "RUNNING" || verifiedCount != 1 || activeSource != 0 || postDrain != 0 || cleanupCount != 0 || beforeEpochs != afterEpochs || beforeFencing != afterFencing {
		t.Fatalf("terminal drain=%s child=%s/%s vm=%s/%s power=%s counts=%d/%d/%d cleanup=%d failure=%d/%d->%d/%d", drain, childPhase, childResult, vmHost, vmAdmission, power, verifiedCount, activeSource, postDrain, cleanupCount, beforeEpochs, beforeFencing, afterEpochs, afterFencing)
	}
	for label, statement := range map[string]string{
		"quiescence execution": `UPDATE kim.planned_source_quiescence_execution_evidence SET evidence_digest=$2 WHERE quiescence_evidence_id=$1`,
		"destination binding":  `UPDATE kim.host_evacuation_destination_evidence_binding SET binding_digest=$2 WHERE destination_binding_id=$1`,
		"child verification":   `UPDATE kim.host_evacuation_child_verification_evidence SET verification_digest=$2 WHERE verification_id=$1`,
		"child terminal":       `UPDATE kim.host_evacuation_child_terminal_evidence SET terminal_digest=$2 WHERE terminal_evidence_id=$1`,
		"parent terminal":      `UPDATE kim.host_evacuation_terminal_evidence SET decision_digest=$2 WHERE terminal_evidence_id=$1`,
	} {
		id := map[string]string{"quiescence execution": quiescenceID, "destination binding": bindingID, "child verification": verificationID, "child terminal": terminalID, "parent terminal": parentTerminalID}[label]
		if _, err := pool.Exec(ctx, statement, id, digestBytes([]byte("forged-"+label))); err == nil {
			t.Fatalf("immutable %s accepted UPDATE", label)
		}
	}
}
