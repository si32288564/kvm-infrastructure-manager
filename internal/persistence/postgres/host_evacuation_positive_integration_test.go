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
	var authorityGeneration int64
	if err := db.QueryRow(ctx, `SELECT authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1`, hostID).Scan(&authorityGeneration); err != nil {
		t.Fatal(err)
	}
	leaseRequest := CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authorityGeneration, Duration: time.Minute}
	var commandType, schemaVersion string
	if err := db.QueryRow(ctx, `SELECT command_type,schema_version FROM kim.execution_commands WHERE command_id=$1`, commandID).Scan(&commandType, &schemaVersion); err != nil {
		t.Fatal(err)
	}
	if isReadOnlyVerificationCommand(commandType, schemaVersion) {
		leaseRequest.AuthorityScope = CommandLeaseScopeReadOnlyVerification
	}
	grant, err := AcquireCommandLease(ctx, db, leaseRequest)
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

func recordCleanupLeaseLossVerification(t *testing.T, ctx context.Context, db recoveryQualificationDB, hostID, commandID, verificationID, state string, observationGeneration uint64, evidence map[string]any) int {
	t.Helper()
	var authorityGeneration int64
	if err := db.QueryRow(ctx, `SELECT authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1`, hostID).Scan(&authorityGeneration); err != nil {
		t.Fatal(err)
	}
	request := CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authorityGeneration, Duration: time.Minute}
	var commandType, schemaVersion string
	if err := db.QueryRow(ctx, `SELECT command_type,schema_version FROM kim.execution_commands WHERE command_id=$1`, commandID).Scan(&commandType, &schemaVersion); err != nil {
		t.Fatal(err)
	}
	if isReadOnlyVerificationCommand(commandType, schemaVersion) {
		request.AuthorityScope = CommandLeaseScopeReadOnlyVerification
	}
	grant, err := AcquireCommandLease(ctx, db, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE kim.command_leases_current SET expires_at=statement_timestamp()-interval '1 microsecond' WHERE command_id=$1 AND lease_generation=$2`, commandID, grant.LeaseGeneration); err != nil {
		t.Fatal(err)
	}
	if err := ExpireCommandLease(ctx, db, commandID); err != nil {
		t.Fatal(err)
	}
	if err := RecordCommandVerification(ctx, db, CommandVerification{VerificationID: verificationID, CommandID: commandID, AttemptIndex: grant.AttemptIndex, ObservationGeneration: int64(observationGeneration), ObservationDigest: digestBytes([]byte(commandID + "/observation")), State: state, VerifierArtifactDigest: digestBytes([]byte(commandID + "/verifier")), Evidence: evidence}); err != nil {
		t.Fatal(err)
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

func qualifyEvacuationLocalLVMCopy(t *testing.T, ctx context.Context, db recoveryQualificationDB, claim HostEvacuationClaim, suffix, destinationAdmission, safetyID, contentDigest string, responseLost bool) LocalLVMRelocationCopyVerification {
	t.Helper()
	copyID, commandID := "local-lvm-copy-"+suffix, "local-lvm-copy-command-"+suffix
	authority, err := PrepareLocalLVMRelocationCopy(ctx, db, claim, LocalLVMRelocationCopyRequest{CopyOperationID: copyID, DestinationAdmissionID: destinationAdmission, SourceSafetyEvidenceID: safetyID, JobID: "local-lvm-copy-job-" + suffix, CommandID: commandID})
	if err != nil {
		t.Fatalf("prepare Local LVM copy: %v", err)
	}
	transportSession, err := PrepareLocalLVMTransportSession(ctx, db, LocalLVMTransportSessionRequest{TransportSessionID: "local-lvm-transport-" + suffix, CopyOperationID: copyID, Duration: time.Minute, ChunkSizeBytes: 1 << 20, MaximumConcurrentPerHost: 1})
	if err != nil {
		t.Fatalf("prepare cross-Host Local LVM transport: %v", err)
	}
	if transportSession.AuthorityDigest != transportSession.AgentAuthority().Digest() || transportSession.SourceHostID != authority.SourceHostID || transportSession.DestinationHostID != authority.DestinationHostID {
		t.Fatalf("transport authority did not preserve exact copy identity: %+v", transportSession)
	}
	if _, err := PrepareLocalLVMTransportSession(ctx, db, LocalLVMTransportSessionRequest{TransportSessionID: "local-lvm-transport-conflict-" + suffix, CopyOperationID: copyID, Duration: time.Minute, ChunkSizeBytes: 1 << 20, MaximumConcurrentPerHost: 1}); err == nil {
		t.Fatal("concurrent session for the same destination LV was accepted")
	}
	if err := RecordLocalLVMTransportProgress(ctx, db, transportSession.TransportSessionID, 1, 0, 0, "STARTED", "PENDING"); err != nil {
		t.Fatal(err)
	}
	// The block side effect completed but the destination Agent response was
	// lost. Success is recovered only from the two independent read-backs.
	if err := RecordLocalLVMTransportProgress(ctx, db, transportSession.TransportSessionID, 1, authority.ExpectedSizeBytes, authority.ExpectedSizeBytes, "DISCONNECTED", "LOST"); err != nil {
		t.Fatal(err)
	}
	if err := RecordLocalLVMTransportProgress(ctx, db, transportSession.TransportSessionID, 1, authority.ExpectedSizeBytes, authority.ExpectedSizeBytes, "LEASE_EXPIRED", "LOST"); err != nil {
		t.Fatal(err)
	}
	if err := RecordLocalLVMTransportProgress(ctx, db, transportSession.TransportSessionID, 2, authority.ExpectedSizeBytes, authority.ExpectedSizeBytes, "READ_BACK", "LOST"); err != nil {
		t.Fatal(err)
	}
	peer := func(role string) LocalLVMTransportPeerObservation {
		if role == "SOURCE" {
			return LocalLVMTransportPeerObservation{EvidenceID: "local-lvm-transport-source-" + suffix, Role: role, HostID: transportSession.SourceHostID, CertificateFingerprint: transportSession.SourceCertificateFingerprint, CredentialBindingRevision: transportSession.SourceCredentialBindingRevision, SessionGeneration: transportSession.SourceSessionGeneration, VolumeID: transportSession.SourceVolumeID, BindingID: transportSession.SourceBindingID, BindingGeneration: transportSession.SourceBindingGeneration, LVUUID: transportSession.SourceLVUUID, SizeBytes: transportSession.ExactByteCount, ContentDigest: contentDigest, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte("source-transport-observation/" + suffix)), VerifierArtifactDigest: digestBytes([]byte("source-transport-verifier/" + suffix))}
		}
		return LocalLVMTransportPeerObservation{EvidenceID: "local-lvm-transport-destination-" + suffix, Role: role, HostID: transportSession.DestinationHostID, CertificateFingerprint: transportSession.DestinationCertificateFingerprint, CredentialBindingRevision: transportSession.DestinationCredentialBindingRevision, SessionGeneration: transportSession.DestinationSessionGeneration, VolumeID: transportSession.DestinationVolumeID, BindingID: transportSession.DestinationBindingID, BindingGeneration: transportSession.DestinationBindingGeneration, LVUUID: transportSession.DestinationLVUUID, SizeBytes: transportSession.ExactByteCount, ContentDigest: contentDigest, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte("destination-transport-observation/" + suffix)), VerifierArtifactDigest: digestBytes([]byte("destination-transport-verifier/" + suffix))}
	}
	transportTerminalID := "local-lvm-transport-terminal-" + suffix
	sourceObservation, destinationObservation := peer("SOURCE"), peer("DESTINATION")
	wrongPeer := sourceObservation
	wrongPeer.EvidenceID += "-wrong-peer"
	wrongPeer.HostID = transportSession.DestinationHostID
	if err := RecordLocalLVMTransportPeerObservation(ctx, db, transportSession.TransportSessionID, wrongPeer); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("wrong source peer accepted: %v", err)
	}
	mismatchTx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mismatchDestination := destinationObservation
	mismatchDestination.ContentDigest = digestBytes([]byte("different-content/" + suffix))
	if err := RecordLocalLVMTransportPeerObservation(ctx, scopeTxBeginner{mismatchTx}, transportSession.TransportSessionID, sourceObservation); err != nil {
		_ = mismatchTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := RecordLocalLVMTransportPeerObservation(ctx, scopeTxBeginner{mismatchTx}, transportSession.TransportSessionID, mismatchDestination); err != nil {
		_ = mismatchTx.Rollback(ctx)
		t.Fatal(err)
	}
	mismatchCompletion := LocalLVMTransportCompletion{TerminalEvidenceID: transportTerminalID + "-digest-mismatch", AttemptIndex: 2, BytesTransferred: transportSession.ExactByteCount, ResponseState: "LOST", SourceEvidenceID: sourceObservation.EvidenceID, DestinationEvidenceID: destinationObservation.EvidenceID}
	err = CompleteLocalLVMTransport(ctx, scopeTxBeginner{mismatchTx}, transportSession.TransportSessionID, mismatchCompletion)
	_ = mismatchTx.Rollback(ctx)
	if !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("mismatched peer digests accepted: %v", err)
	}
	if err := RecordLocalLVMTransportPeerObservation(ctx, db, transportSession.TransportSessionID, sourceObservation); err != nil {
		t.Fatalf("record source transport read-back: %v", err)
	}
	if err := RecordLocalLVMTransportPeerObservation(ctx, db, transportSession.TransportSessionID, destinationObservation); err != nil {
		t.Fatalf("record destination transport read-back: %v", err)
	}
	completion := LocalLVMTransportCompletion{TerminalEvidenceID: transportTerminalID, AttemptIndex: 2, BytesTransferred: transportSession.ExactByteCount, ResponseState: "LOST", SourceEvidenceID: sourceObservation.EvidenceID, DestinationEvidenceID: destinationObservation.EvidenceID}
	for label, drift := range map[string]func(pgx.Tx) error{
		"source authority": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE kim.host_operation_authorities_current SET authority_state='DISARMED' WHERE host_id=$1`, transportSession.SourceHostID)
			return err
		},
		"source credential": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE kim.agent_credential_bindings_current SET binding_state='REVOKED' WHERE host_id=$1`, transportSession.SourceHostID)
			return err
		},
		"source incarnation": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET host_id=$2 WHERE vm_id=(SELECT vm_id FROM kim.local_lvm_relocation_copy_operation_evidence WHERE copy_operation_id=$1)`, copyID, transportSession.DestinationHostID)
			return err
		},
		"destination binding": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1`, transportSession.DestinationBindingID)
			return err
		},
		"destination session": func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE kim.agent_transport_sessions_current SET state='FENCED' WHERE host_id=$1`, transportSession.DestinationHostID)
			return err
		},
	} {
		tx, err := db.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := drift(tx); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		candidate := completion
		candidate.TerminalEvidenceID += "-stale-" + label
		err = CompleteLocalLVMTransport(ctx, scopeTxBeginner{tx}, transportSession.TransportSessionID, candidate)
		_ = tx.Rollback(ctx)
		if !errors.Is(err, ErrHostEvacuationStale) {
			t.Fatalf("%s drift accepted: %v", label, err)
		}
	}
	if err := CompleteLocalLVMTransport(ctx, db, transportSession.TransportSessionID, completion); err != nil {
		t.Fatalf("complete cross-Host Local LVM transport read-back: %v", err)
	}
	if err := CompleteLocalLVMTransport(ctx, db, transportSession.TransportSessionID, completion); err != nil {
		t.Fatalf("transport terminal replay: %v", err)
	}
	conflictingReplay := completion
	conflictingReplay.SourceEvidenceID += "-different"
	conflictingReplay.DestinationEvidenceID += "-different"
	if err := CompleteLocalLVMTransport(ctx, db, transportSession.TransportSessionID, conflictingReplay); !errors.Is(err, ErrHostEvacuationConflict) {
		t.Fatalf("conflicting transport replay accepted: %v", err)
	}
	evidence := map[string]any{"copy_operation_id": copyID, "source_host_id": authority.SourceHostID, "source_volume_id": authority.SourceVolumeID, "source_binding_id": authority.SourceBindingID, "source_binding_generation": float64(1), "source_lv_uuid": authority.SourceLVUUID, "destination_host_id": authority.DestinationHostID, "destination_volume_id": authority.DestinationVolumeID, "destination_binding_id": authority.DestinationBindingID, "destination_binding_generation": float64(1), "destination_lv_uuid": authority.DestinationLVUUID, "source_size_bytes": float64(authority.ExpectedSizeBytes), "destination_size_bytes": float64(authority.ExpectedSizeBytes), "digest_algorithm": "SHA-256", "source_content_digest": contentDigest, "destination_content_digest": contentDigest, "copy_state": "COMPLETE"}
	commandVerificationID := "local-lvm-copy-command-verification-" + suffix
	if responseLost {
		acceptEvacuationLostReadBack(t, ctx, db, authority.DestinationHostID, commandID, commandVerificationID, 1, evidence)
	} else {
		acceptEvacuationCommand(t, ctx, db, authority.DestinationHostID, commandID, commandVerificationID, 1, evidence, "SUCCEEDED")
	}
	verified, err := VerifyLocalLVMRelocationCopy(ctx, db, claim, copyID, commandVerificationID, "local-lvm-source-content-"+suffix, "local-lvm-destination-content-"+suffix, "local-lvm-copy-verification-"+suffix, "local-lvm-copy-terminal-"+suffix)
	if err != nil {
		t.Fatalf("verify Local LVM copy: %v", err)
	}
	wantResponse := "RECEIVED"
	if responseLost {
		wantResponse = "LOST"
	}
	if verified.ContentDigest != contentDigest || verified.ResponseState != wantResponse {
		t.Fatalf("copy verification=%+v", verified)
	}
	var linkedTransportTerminal string
	if err := db.QueryRow(ctx, `SELECT transport_terminal_evidence_id FROM kim.local_lvm_relocation_copy_verification_evidence WHERE verification_id=$1`, verified.VerificationID).Scan(&linkedTransportTerminal); err != nil || linkedTransportTerminal != transportTerminalID {
		t.Fatalf("copy verification transport terminal=%q err=%v", linkedTransportTerminal, err)
	}
	replayed, err := VerifyLocalLVMRelocationCopy(ctx, db, claim, copyID, commandVerificationID, "local-lvm-source-content-"+suffix, "local-lvm-destination-content-"+suffix, "local-lvm-copy-verification-"+suffix, "local-lvm-copy-terminal-"+suffix)
	if err != nil || replayed.TerminalDigest != verified.TerminalDigest {
		t.Fatalf("copy replay=%+v err=%v", replayed, err)
	}
	return verified
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
	if relocationID != "" {
		if _, err := PrepareVMImageMaterialization(ctx, db, VMImageMaterializationRequest{VMID: vmID, PlanID: planID, JobID: "forbidden-image-job-" + suffix, CommandID: "forbidden-image-command-" + suffix}); !errors.Is(err, ErrVMMaterializationConflict) {
			t.Fatalf("base Image overwrite was not denied for preserved root: %v", err)
		}
		if err := AcceptVMPreservedRootReadiness(ctx, db, vmID, planID, "preserved-root-evidence-"+suffix); err != nil {
			t.Fatalf("accept preserved root: %v", err)
		}
		return decision
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
	guestContentDigest := digestBytes([]byte("base-image/unique-guest-mutation-marker/second-marker-near-end/" + f.Suffix))
	if err := AuthorizeHostEvacuationRelocation(ctx, pool, claim, "shape-only-relocation-"+f.Suffix, f.DestinationAdmission, safetyID, releaseID); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("shape-only Local LVM relocation authorized: %v", err)
	}
	if _, err := PrepareLocalLVMRelocationCopy(ctx, pool, claim, LocalLVMRelocationCopyRequest{CopyOperationID: "wrong-safety-copy-" + f.Suffix, DestinationAdmissionID: f.DestinationAdmission, SourceSafetyEvidenceID: "wrong-safety", JobID: "wrong-safety-copy-job-" + f.Suffix, CommandID: "wrong-safety-copy-command-" + f.Suffix}); !errors.Is(err, ErrHostEvacuationBlocked) {
		t.Fatalf("wrong source safety/binding provenance accepted: %v", err)
	}
	rollbackRunning := errors.New("rollback running source")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.vm_power_state_current SET observed_power_state='RUNNING' WHERE vm_id=$1`, f.VMID); err != nil {
			return err
		}
		if _, err := PrepareLocalLVMRelocationCopy(ctx, scopeTxBeginner{tx}, claim, LocalLVMRelocationCopyRequest{CopyOperationID: "running-source-copy-" + f.Suffix, DestinationAdmissionID: f.DestinationAdmission, SourceSafetyEvidenceID: safetyID, JobID: "running-source-copy-job-" + f.Suffix, CommandID: "running-source-copy-command-" + f.Suffix}); !errors.Is(err, ErrHostEvacuationBlocked) {
			t.Fatalf("RUNNING source authorized copy: %v", err)
		}
		return rollbackRunning
	})
	if !errors.Is(err, rollbackRunning) {
		t.Fatal(err)
	}
	rollbackHolder := errors.New("rollback source holder")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_observations_current SET holder_open=true WHERE attachment_id=$1`, f.SourceAttachment); err != nil {
			return err
		}
		if _, err := PrepareLocalLVMRelocationCopy(ctx, scopeTxBeginner{tx}, claim, LocalLVMRelocationCopyRequest{CopyOperationID: "held-source-copy-" + f.Suffix, DestinationAdmissionID: f.DestinationAdmission, SourceSafetyEvidenceID: safetyID, JobID: "held-source-copy-job-" + f.Suffix, CommandID: "held-source-copy-command-" + f.Suffix}); !errors.Is(err, ErrHostEvacuationBlocked) {
			t.Fatalf("held source authorized copy: %v", err)
		}
		return rollbackHolder
	})
	if !errors.Is(err, rollbackHolder) {
		t.Fatal(err)
	}
	rollbackCorruption := errors.New("rollback destination corruption")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		operationID, commandID := "corrupt-copy-"+f.Suffix, "corrupt-copy-command-"+f.Suffix
		authority, err := PrepareLocalLVMRelocationCopy(ctx, scopeTxBeginner{tx}, claim, LocalLVMRelocationCopyRequest{CopyOperationID: operationID, DestinationAdmissionID: f.DestinationAdmission, SourceSafetyEvidenceID: safetyID, JobID: "corrupt-copy-job-" + f.Suffix, CommandID: commandID})
		if err != nil {
			return err
		}
		evidence := map[string]any{"copy_operation_id": operationID, "source_host_id": authority.SourceHostID, "source_volume_id": authority.SourceVolumeID, "source_binding_id": authority.SourceBindingID, "source_binding_generation": float64(1), "source_lv_uuid": authority.SourceLVUUID, "destination_host_id": authority.DestinationHostID, "destination_volume_id": authority.DestinationVolumeID, "destination_binding_id": authority.DestinationBindingID, "destination_binding_generation": float64(1), "destination_lv_uuid": authority.DestinationLVUUID, "source_size_bytes": float64(authority.ExpectedSizeBytes), "destination_size_bytes": float64(authority.ExpectedSizeBytes), "digest_algorithm": "SHA-256", "source_content_digest": guestContentDigest, "destination_content_digest": digestBytes([]byte("one-block-corruption")), "copy_state": "COMPLETE"}
		acceptEvacuationCommand(t, ctx, scopeTxBeginner{tx}, authority.DestinationHostID, commandID, "corrupt-copy-command-verification-"+f.Suffix, 1, evidence, "SUCCEEDED")
		if _, err := VerifyLocalLVMRelocationCopy(ctx, scopeTxBeginner{tx}, claim, operationID, "corrupt-copy-command-verification-"+f.Suffix, "corrupt-source-content-"+f.Suffix, "corrupt-destination-content-"+f.Suffix, "corrupt-copy-verification-"+f.Suffix, "corrupt-copy-terminal-"+f.Suffix); !errors.Is(err, ErrHostEvacuationBlocked) {
			t.Fatalf("destination digest corruption verified: %v", err)
		}
		return rollbackCorruption
	})
	if !errors.Is(err, rollbackCorruption) {
		t.Fatal(err)
	}
	copyVerification := qualifyEvacuationLocalLVMCopy(t, ctx, pool, claim, "positive-"+f.Suffix, f.DestinationAdmission, safetyID, guestContentDigest, true)
	relocationID := "evacuation-relocation-authority-" + f.Suffix
	if err := AuthorizeHostEvacuationRelocation(ctx, pool, claim, relocationID, f.DestinationAdmission, safetyID, releaseID); err != nil {
		t.Fatal(err)
	}
	if copyVerification.ResponseState != "LOST" {
		t.Fatal("Local LVM response-loss read-back was not qualified")
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
	assertTerminalStale("destination-storage-binding", `UPDATE kim.volume_backend_bindings_current SET lv_uuid=$2 WHERE binding_id=$1`, f.DestinationBinding, "forged-lv-"+f.Suffix)
	assertTerminalStale("copy-current", `UPDATE kim.local_lvm_relocation_copy_operations_current SET operation_state='UNKNOWN' WHERE terminal_evidence_id=$1`, copyVerification.TerminalEvidenceID)
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
	var sourceBindingState, sourceCapacityState string
	if err := pool.QueryRow(ctx, `SELECT binding.binding_state,capacity.claim_state FROM kim.volume_backend_bindings_current binding JOIN kim.storage_capacity_claims capacity ON capacity.volume_id=binding.volume_id AND capacity.placement_admission_id=$2 WHERE binding.binding_id=$1`, f.SourceBinding, f.SourceAdmission).Scan(&sourceBindingState, &sourceCapacityState); err != nil || sourceBindingState != "BOUND" || sourceCapacityState == "RELEASED" {
		t.Fatalf("source LV/capacity was reclaimed by relocation: binding=%s capacity=%s err=%v", sourceBindingState, sourceCapacityState, err)
	}
	cleanupMetricsBefore, err := LoadLocalLVMCleanupMetrics(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}

	// Cleanup is a separate, post-terminal authority. A holder, stale terminal,
	// or missing content-preservation terminal cannot nominate the source LV.
	cleanupID := "local-lvm-source-cleanup-" + f.Suffix
	rollbackCleanupHolder := errors.New("rollback cleanup holder")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_observations_current SET holder_open=true WHERE attachment_id=$1`, f.SourceAttachment); err != nil {
			return err
		}
		if _, err := CommitHostEvacuationSourceLocalLVMCleanup(ctx, scopeTxBeginner{tx}, cleanupID+"-held", 1, terminalID); !errors.Is(err, ErrBackendCleanupStale) {
			t.Fatalf("held source LV nominated for cleanup: %v", err)
		}
		return rollbackCleanupHolder
	})
	if !errors.Is(err, rollbackCleanupHolder) {
		t.Fatal(err)
	}
	if _, err := CommitHostEvacuationSourceLocalLVMCleanup(ctx, pool, cleanupID+"-wrong-terminal", 1, "unknown-child-terminal"); !errors.Is(err, ErrBackendCleanupStale) {
		t.Fatalf("unknown terminal nominated source cleanup: %v", err)
	}
	authority, err := CommitHostEvacuationSourceLocalLVMCleanup(ctx, pool, cleanupID, 1, terminalID)
	if err != nil {
		t.Fatalf("commit source Local LVM cleanup authority: %v", err)
	}
	if authority.SourceVolumeID != f.SourceVolume || authority.SourceBindingID != f.SourceBinding || authority.SourceLVUUID != f.SourceLV || authority.ChildTerminalID != terminalID || authority.CopyTerminalID != copyVerification.TerminalEvidenceID {
		t.Fatalf("cleanup authority lost exact source/copy provenance: %+v", authority)
	}
	if _, err := ReclaimLocalLVMSourceCapacity(ctx, pool, cleanupID, 1, "premature-reclamation-"+f.Suffix); !errors.Is(err, ErrBackendCleanupStale) {
		t.Fatalf("capacity reclaimed before physical absence: %v", err)
	}

	cleanupEvidence := func(id, commandID, verificationID, resultState, response string, claimGeneration, generation uint64, attempt int, exactPresent, foreign bool, observedLV string) LocalLVMCleanupObservation {
		present, running := exactPresent, false
		return LocalLVMCleanupObservation{BackendCleanupObservation: BackendCleanupObservation{EvidenceID: id, OperationID: cleanupID, ResourceType: "LOCAL_LVM_VOLUME", ResourceID: authority.SourceVolumeID, SourceHostID: authority.SourceHostID, VMID: authority.VMID, BackendIdentityDigest: authority.BackendIdentityDigest, ApplyResponseState: response, CommandID: commandID, VerificationID: verificationID, VerifierDigest: digestBytes([]byte(commandID + "/verifier")), ObservationDigest: digestBytes([]byte(commandID + "/observation")), ResultState: resultState, ArtifactDigest: digestBytes([]byte("local-lvm-delete-adapter/v1")), EvidenceDigest: digestBytes([]byte(id + "/evidence")), OperationGeneration: 1, ClaimGeneration: claimGeneration, ResourceGeneration: authority.SourceBindingGeneration, VMGeneration: authority.VMGeneration, MaterializationGeneration: authority.MaterializationGeneration, ObservationGeneration: generation, AttemptIndex: attempt, BackendPresent: &present, BackendRunning: &running, IdentityMatches: true}, ObservedLVUUID: observedLV, ExactSourceLVPresent: exactPresent, ForeignReplacementPresent: foreign}
	}
	commandEvidence := func(exactPresent, foreign bool, observed string) map[string]any {
		return map[string]any{"backend_id": authority.SourceBackendID, "backend_generation": float64(authority.SourceBackendGeneration), "vg_uuid": authority.SourceVGUUID, "expected_lv_uuid": authority.SourceLVUUID, "backend_resource_key": authority.SourceBackendResourceKey, "binding_id": authority.SourceBindingID, "binding_generation": float64(authority.SourceBindingGeneration), "cleanup_operation_id": cleanupID, "cleanup_generation": float64(1), "desired_state": "ABSENT", "exact_source_lv_present": exactPresent, "foreign_replacement_present": foreign, "observed_lv_uuid": observed}
	}

	claim1, err := ClaimBackendCleanup(ctx, pool, cleanupID, 1, "cleanup-worker-1", time.Minute)
	if err != nil || claim1.Mode != "APPLY_ALLOWED" {
		t.Fatalf("cleanup claim1=%+v err=%v", claim1, err)
	}
	apply1 := "local-lvm-delete-command-1-" + f.Suffix
	if _, err := AuthorizeLocalLVMCleanupCommand(ctx, pool, claim1, "local-lvm-delete-job-1-"+f.Suffix, apply1); err != nil {
		t.Fatal(err)
	}
	attempt1 := recordCleanupLeaseLossVerification(t, ctx, pool, f.SourceHost, apply1, "local-lvm-delete-verification-1-"+f.Suffix, "UNKNOWN", 1, commandEvidence(true, false, f.SourceLV))
	if err := CompleteLocalLVMCleanup(ctx, pool, claim1, cleanupEvidence("local-lvm-delete-unknown-"+f.Suffix, apply1, "local-lvm-delete-verification-1-"+f.Suffix, "UNKNOWN", "LOST", claim1.ClaimGeneration, 1, attempt1, true, false, f.SourceLV)); err != nil {
		t.Fatal(err)
	}

	claim2, err := ClaimBackendCleanup(ctx, pool, cleanupID, 1, "cleanup-worker-2", time.Minute)
	if err != nil || claim2.Mode != "READ_BACK_FIRST" {
		t.Fatalf("cleanup successor=%+v err=%v", claim2, err)
	}
	if _, err := AuthorizeLocalLVMCleanupCommand(ctx, pool, claim2, "blind-delete-job-"+f.Suffix, "blind-delete-command-"+f.Suffix); !errors.Is(err, ErrBackendCleanupStale) {
		t.Fatalf("READ_BACK_FIRST successor authorized blind delete: %v", err)
	}
	readPresent := "local-lvm-delete-readback-present-" + f.Suffix
	if _, err := AuthorizeLocalLVMCleanupReadBackCommand(ctx, pool, claim2, "local-lvm-delete-readback-job-present-"+f.Suffix, readPresent); err != nil {
		t.Fatal(err)
	}
	presentAttempt := recordCleanupLeaseLossVerification(t, ctx, pool, f.SourceHost, readPresent, "local-lvm-delete-readback-verification-present-"+f.Suffix, "NOT_APPLIED", 2, commandEvidence(true, false, f.SourceLV))
	if err := CompleteLocalLVMCleanup(ctx, pool, claim2, cleanupEvidence("local-lvm-delete-present-"+f.Suffix, readPresent, "local-lvm-delete-readback-verification-present-"+f.Suffix, "PRESENT", "NOT_APPLICABLE", claim2.ClaimGeneration, 2, presentAttempt, true, false, f.SourceLV)); err != nil {
		t.Fatal(err)
	}
	apply2 := "local-lvm-delete-command-2-" + f.Suffix
	if _, err := AuthorizeLocalLVMCleanupCommand(ctx, pool, claim2, "local-lvm-delete-job-2-"+f.Suffix, apply2); err != nil {
		t.Fatal(err)
	}
	attempt2 := recordCleanupLeaseLossVerification(t, ctx, pool, f.SourceHost, apply2, "local-lvm-delete-verification-2-"+f.Suffix, "UNKNOWN", 3, commandEvidence(false, false, ""))
	if err := CompleteLocalLVMCleanup(ctx, pool, claim2, cleanupEvidence("local-lvm-delete-lost-after-side-effect-"+f.Suffix, apply2, "local-lvm-delete-verification-2-"+f.Suffix, "UNKNOWN", "LOST", claim2.ClaimGeneration, 3, attempt2, false, false, "")); err != nil {
		t.Fatal(err)
	}

	claim3, err := ClaimBackendCleanup(ctx, pool, cleanupID, 1, "cleanup-worker-3", time.Minute)
	if err != nil || claim3.Mode != "READ_BACK_FIRST" {
		t.Fatalf("cleanup read-back successor=%+v err=%v", claim3, err)
	}
	readAbsent := "local-lvm-delete-readback-absent-" + f.Suffix
	if _, err := AuthorizeLocalLVMCleanupReadBackCommand(ctx, pool, claim3, "local-lvm-delete-readback-job-absent-"+f.Suffix, readAbsent); err != nil {
		t.Fatal(err)
	}
	absentAttempt := recordCleanupLeaseLossVerification(t, ctx, pool, f.SourceHost, readAbsent, "local-lvm-delete-readback-verification-absent-"+f.Suffix, "MATCHED", 4, commandEvidence(false, false, ""))
	absenceID := "local-lvm-delete-absence-" + f.Suffix
	if err := CompleteLocalLVMCleanup(ctx, pool, claim3, cleanupEvidence(absenceID, readAbsent, "local-lvm-delete-readback-verification-absent-"+f.Suffix, "ABSENT", "NOT_APPLICABLE", claim3.ClaimGeneration, 4, absentAttempt, false, false, "")); err != nil {
		t.Fatal(err)
	}
	reclamationID := "local-lvm-capacity-reclamation-" + f.Suffix
	reclamation, err := ReclaimLocalLVMSourceCapacity(ctx, pool, cleanupID, 1, reclamationID)
	if err != nil || reclamation.ReleasedBytes != authority.ReservedBytes {
		t.Fatalf("capacity reclamation=%+v err=%v", reclamation, err)
	}
	if replay, err := ReclaimLocalLVMSourceCapacity(ctx, pool, cleanupID, 1, reclamationID); err != nil || replay.Digest != reclamation.Digest {
		t.Fatalf("reclamation replay=%+v err=%v", replay, err)
	}
	cleanupMetricsAfter, err := LoadLocalLVMCleanupMetrics(ctx, pool)
	if err != nil || cleanupMetricsAfter.Active != cleanupMetricsBefore.Active || cleanupMetricsAfter.Attempts-cleanupMetricsBefore.Attempts != 3 || cleanupMetricsAfter.Unknown-cleanupMetricsBefore.Unknown != 2 || cleanupMetricsAfter.Present-cleanupMetricsBefore.Present != 1 || cleanupMetricsAfter.Absent-cleanupMetricsBefore.Absent != 1 || cleanupMetricsAfter.CapacityReleasePending != cleanupMetricsBefore.CapacityReleasePending || cleanupMetricsAfter.ReleasedBytes-cleanupMetricsBefore.ReleasedBytes != int64(authority.ReservedBytes) || cleanupMetricsAfter.DurationNanoseconds <= cleanupMetricsBefore.DurationNanoseconds {
		t.Fatalf("cleanup metrics before=%+v after=%+v err=%v", cleanupMetricsBefore, cleanupMetricsAfter, err)
	}
	var cleanupState, capacityState, bindingState, intentState, volumeState, destinationHostAfter, destinationPowerAfter, parentAfter string
	if err := pool.QueryRow(ctx, `SELECT cleanup.operation_state,capacity.claim_state,binding.binding_state,intent.binding_state,volume.lifecycle_state,vm.host_id,power.observed_power_state,parent.lifecycle_state FROM kim.backend_cleanup_operations_current cleanup JOIN kim.local_lvm_source_cleanup_authority_evidence detail USING(cleanup_operation_id,cleanup_generation) JOIN kim.storage_capacity_claims capacity ON capacity.capacity_claim_id=detail.source_capacity_claim_id JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=detail.source_binding_id JOIN kim.volume_backend_binding_intents intent ON intent.binding_id=detail.source_binding_id JOIN kim.volumes_current volume ON volume.volume_id=detail.source_volume_id JOIN kim.virtual_machines_current vm ON vm.vm_id=(SELECT vm_id FROM kim.backend_cleanup_operation_evidence WHERE cleanup_operation_id=$1) JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id JOIN kim.host_evacuation_operations_current parent ON parent.evacuation_operation_id=$2 WHERE cleanup.cleanup_operation_id=$1`, cleanupID, operationID).Scan(&cleanupState, &capacityState, &bindingState, &intentState, &volumeState, &destinationHostAfter, &destinationPowerAfter, &parentAfter); err != nil {
		t.Fatal(err)
	}
	if cleanupState != "VERIFIED" || capacityState != "RELEASED" || bindingState != "REVOKED" || intentState != "RELEASED" || volumeState != "DELETED" || destinationHostAfter != f.DestinationHost || destinationPowerAfter != "RUNNING" || parentAfter != "VERIFIED" {
		t.Fatalf("cleanup/capacity=%s/%s binding=%s/%s volume=%s destination=%s/%s parent=%s", cleanupState, capacityState, bindingState, intentState, volumeState, destinationHostAfter, destinationPowerAfter, parentAfter)
	}
	for label, statement := range map[string]string{
		"cleanup authority": `UPDATE kim.local_lvm_source_cleanup_authority_evidence SET authority_digest=$2 WHERE cleanup_operation_id=$1`,
		"cleanup identity":  `UPDATE kim.local_lvm_source_cleanup_observation_identity_evidence SET identity_digest=$2 WHERE cleanup_evidence_id=$1`,
		"reclamation":       `UPDATE kim.local_lvm_capacity_reclamation_evidence SET reclamation_digest=$2 WHERE reclamation_evidence_id=$1`,
	} {
		id := map[string]string{"cleanup authority": cleanupID, "cleanup identity": absenceID, "reclamation": reclamationID}[label]
		if _, err := pool.Exec(ctx, statement, id, digestBytes([]byte("forged-"+label))); err == nil {
			t.Fatalf("immutable %s accepted UPDATE", label)
		}
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
	for label, target := range map[string]struct{ table, column, id string }{
		"copy operation":     {"local_lvm_relocation_copy_operation_evidence", "copy_operation_id", "local-lvm-copy-positive-" + f.Suffix},
		"copy attempt":       {"local_lvm_relocation_copy_attempt_evidence", "copy_operation_id", "local-lvm-copy-positive-" + f.Suffix},
		"source content":     {"local_lvm_relocation_content_observation_evidence", "content_evidence_id", copyVerification.SourceContentEvidenceID},
		"copy verification":  {"local_lvm_relocation_copy_verification_evidence", "verification_id", copyVerification.VerificationID},
		"copy terminal":      {"local_lvm_relocation_copy_terminal_evidence", "terminal_evidence_id", copyVerification.TerminalEvidenceID},
		"transport session":  {"local_lvm_relocation_transport_session_evidence", "transport_session_id", "local-lvm-transport-positive-" + f.Suffix},
		"transport event":    {"local_lvm_relocation_transport_event_evidence", "transport_session_id", "local-lvm-transport-positive-" + f.Suffix},
		"transport peer":     {"local_lvm_relocation_transport_peer_observation_evidence", "peer_evidence_id", "local-lvm-transport-source-positive-" + f.Suffix},
		"transport terminal": {"local_lvm_relocation_transport_terminal_evidence", "terminal_evidence_id", "local-lvm-transport-terminal-positive-" + f.Suffix},
	} {
		statement := fmt.Sprintf("UPDATE kim.%s SET recorded_at=recorded_at WHERE %s=$1", target.table, target.column)
		if _, err := pool.Exec(ctx, statement, target.id); err == nil {
			t.Fatalf("immutable %s accepted UPDATE", label)
		}
	}
}
