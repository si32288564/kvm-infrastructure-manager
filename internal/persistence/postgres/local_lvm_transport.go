package postgres

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	transportauthority "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvmtransport"
)

type LocalLVMTransportSessionRequest struct {
	TransportSessionID, CopyOperationID      string
	Duration                                 time.Duration
	ChunkSizeBytes, MaximumConcurrentPerHost int
	BandwidthLimitBytesPerSecond             uint64
}

type LocalLVMTransportSession struct {
	TransportSessionID, CopyOperationID                                             string
	TransportGeneration, CopyGeneration                                             uint64
	SourceHostID, DestinationHostID                                                 string
	SourceHostAuthorityGeneration, DestinationHostAuthorityGeneration               uint64
	SourceCredentialBindingRevision, DestinationCredentialBindingRevision           uint64
	SourceSessionGeneration, DestinationSessionGeneration                           uint64
	SourceCertificateFingerprint, DestinationCertificateFingerprint                 string
	SourceVolumeID, SourceBindingID, SourceVGUUID, SourceLVUUID                     string
	SourceBindingGeneration                                                         uint64
	DestinationVolumeID, DestinationBindingID, DestinationVGUUID, DestinationLVUUID string
	DestinationBindingGeneration                                                    uint64
	ExactByteCount                                                                  uint64
	ChunkSizeBytes                                                                  int
	ExpiresAt                                                                       time.Time
	AuthorityDigest                                                                 string
}

type LocalLVMTransportPeerObservation struct {
	EvidenceID, Role, HostID, CertificateFingerprint         string
	CredentialBindingRevision, SessionGeneration             uint64
	VolumeID, BindingID, LVUUID                              string
	BindingGeneration, SizeBytes, ObservationGeneration      uint64
	ContentDigest, ObservationDigest, VerifierArtifactDigest string
	HolderOpen                                               bool
}

type LocalLVMTransportCompletion struct {
	TerminalEvidenceID                      string
	AttemptIndex                            int
	BytesTransferred                        uint64
	ResponseState                           string
	SourceEvidenceID, DestinationEvidenceID string
}

// PrepareLocalLVMTransportSession derives one shared cross-Host session from
// the exact Migration 070 copy authority and both current Agent trust/session
// generations. No endpoint, path, token, shell, or argv is accepted.
func PrepareLocalLVMTransportSession(ctx context.Context, db TxBeginner, request LocalLVMTransportSessionRequest) (LocalLVMTransportSession, error) {
	var out LocalLVMTransportSession
	if request.TransportSessionID == "" || request.CopyOperationID == "" || request.Duration <= 0 || request.Duration > 24*time.Hour || request.ChunkSizeBytes < 4096 || request.ChunkSizeBytes > 4<<20 || request.MaximumConcurrentPerHost < 1 || request.MaximumConcurrentPerHost > 64 {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var sourceAuthority, destinationAuthority, sourceCredential, destinationCredential, sourceSession, destinationSession uint64
		var sourceFingerprint, destinationFingerprint string
		var bandwidth *uint64
		if request.BandwidthLimitBytesPerSecond > 0 {
			bandwidth = &request.BandwidthLimitBytesPerSecond
		}
		if err := tx.QueryRow(ctx, `SELECT copy.copy_generation,copy.source_host_id,copy.source_volume_id,copy.source_binding_id,copy.source_binding_generation,copy.source_vg_uuid,copy.source_lv_uuid,
			copy.destination_host_id,copy.destination_volume_id,copy.destination_binding_id,copy.destination_binding_generation,copy.destination_vg_uuid,copy.destination_lv_uuid,copy.expected_size_bytes,
			source_auth.authority_generation,source_auth.credential_binding_revision,source_auth.session_generation,source_credential.certificate_fingerprint_sha256,
			destination_auth.authority_generation,destination_auth.credential_binding_revision,destination_auth.session_generation,destination_credential.certificate_fingerprint_sha256
			FROM kim.local_lvm_relocation_copy_operation_evidence copy
			JOIN kim.local_lvm_relocation_copy_operations_current copy_current ON copy_current.copy_operation_id=copy.copy_operation_id AND copy_current.copy_generation=copy.copy_generation AND copy_current.operation_state IN ('PENDING','VERIFYING','UNKNOWN')
			JOIN kim.host_evacuation_source_storage_safety_evidence safety ON safety.safety_evidence_id=copy.source_storage_safety_evidence_id AND safety.safety_digest=copy.source_storage_safety_digest AND safety.safety_state='SAFE'
			JOIN kim.planned_source_quiescence_evidence quiescence ON quiescence.quiescence_evidence_id=safety.quiescence_evidence_id AND quiescence.child_operation_id=copy.child_operation_id AND quiescence.child_generation=copy.child_generation AND quiescence.vm_id=copy.vm_id AND quiescence.vm_generation=copy.vm_generation AND quiescence.source_host_id=copy.source_host_id AND quiescence.source_materialization_generation=copy.source_materialization_generation
			JOIN kim.virtual_machines_current vm ON vm.vm_id=copy.vm_id AND vm.vm_generation=copy.vm_generation AND vm.host_id=copy.source_host_id AND vm.current_plan_id=safety.source_plan_id
			JOIN kim.vm_materialization_plan_evidence source_plan ON source_plan.plan_id=safety.source_plan_id AND source_plan.vm_id=copy.vm_id AND source_plan.vm_generation=copy.vm_generation AND source_plan.host_id=copy.source_host_id
			JOIN kim.vm_power_state_current source_power ON source_power.vm_id=copy.vm_id AND source_power.vm_generation=copy.vm_generation AND source_power.observed_power_state='SHUTOFF' AND source_power.convergence_state='MATCHED' AND source_power.evidence_id=safety.power_observation_evidence_id AND source_power.observation_generation=safety.power_observation_generation
			JOIN kim.volume_attachment_observations_current source_holder ON source_holder.attachment_id=safety.root_attachment_id AND source_holder.attachment_generation=safety.root_attachment_generation AND source_holder.binding_id=copy.source_binding_id AND source_holder.binding_generation=copy.source_binding_generation AND source_holder.host_id=copy.source_host_id AND source_holder.observed_lv_uuid=copy.source_lv_uuid AND NOT source_holder.holder_open
			JOIN kim.host_operation_authorities_current source_auth ON source_auth.host_id=copy.source_host_id AND source_auth.authority_state='ARMED'
			JOIN kim.agent_transport_sessions_current source_session ON source_session.host_id=copy.source_host_id AND source_session.session_generation=source_auth.session_generation AND source_session.credential_binding_revision=source_auth.credential_binding_revision AND source_session.state='CURRENT'
			JOIN kim.agent_credential_binding_evidence source_credential ON source_credential.host_id=copy.source_host_id AND source_credential.binding_revision=source_auth.credential_binding_revision AND source_credential.binding_state='ACTIVE' AND statement_timestamp() BETWEEN source_credential.valid_not_before AND source_credential.valid_not_after
			JOIN kim.host_operation_authorities_current destination_auth ON destination_auth.host_id=copy.destination_host_id AND destination_auth.authority_state='ARMED'
			JOIN kim.agent_transport_sessions_current destination_session ON destination_session.host_id=copy.destination_host_id AND destination_session.session_generation=destination_auth.session_generation AND destination_session.credential_binding_revision=destination_auth.credential_binding_revision AND destination_session.state='CURRENT'
			JOIN kim.agent_credential_binding_evidence destination_credential ON destination_credential.host_id=copy.destination_host_id AND destination_credential.binding_revision=destination_auth.credential_binding_revision AND destination_credential.binding_state='ACTIVE' AND statement_timestamp() BETWEEN destination_credential.valid_not_before AND destination_credential.valid_not_after
			JOIN kim.volume_backend_bindings_current source_binding ON source_binding.binding_id=copy.source_binding_id AND source_binding.binding_generation=copy.source_binding_generation AND source_binding.volume_id=copy.source_volume_id AND source_binding.host_id=copy.source_host_id AND source_binding.lv_uuid=copy.source_lv_uuid AND source_binding.binding_state='BOUND'
			JOIN kim.volume_backend_bindings_current destination_binding ON destination_binding.binding_id=copy.destination_binding_id AND destination_binding.binding_generation=copy.destination_binding_generation AND destination_binding.volume_id=copy.destination_volume_id AND destination_binding.host_id=copy.destination_host_id AND destination_binding.lv_uuid=copy.destination_lv_uuid AND destination_binding.binding_state='BOUND'
			WHERE copy.copy_operation_id=$1 FOR UPDATE OF copy_current,source_auth,destination_auth,source_binding,destination_binding`, request.CopyOperationID).Scan(&out.CopyGeneration, &out.SourceHostID, &out.SourceVolumeID, &out.SourceBindingID, &out.SourceBindingGeneration, &out.SourceVGUUID, &out.SourceLVUUID, &out.DestinationHostID, &out.DestinationVolumeID, &out.DestinationBindingID, &out.DestinationBindingGeneration, &out.DestinationVGUUID, &out.DestinationLVUUID, &out.ExactByteCount, &sourceAuthority, &sourceCredential, &sourceSession, &sourceFingerprint, &destinationAuthority, &destinationCredential, &destinationSession, &destinationFingerprint); err != nil {
			return ErrHostEvacuationBlocked
		}
		hosts := []string{out.SourceHostID, out.DestinationHostID}
		sort.Strings(hosts)
		for _, host := range hosts {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,71))`, "local-lvm-transport/"+host); err != nil {
				return err
			}
		}
		for _, host := range []string{out.SourceHostID, out.DestinationHostID} {
			var active int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.local_lvm_relocation_transport_session_evidence evidence JOIN kim.local_lvm_relocation_transport_sessions_current current USING(transport_session_id,transport_generation) WHERE (evidence.source_host_id=$1 OR evidence.destination_host_id=$1) AND evidence.expires_at>statement_timestamp() AND current.session_state IN ('AUTHORIZED','STREAMING','PARTIAL','UNKNOWN')`, host).Scan(&active); err != nil || active >= request.MaximumConcurrentPerHost {
				return ErrHostEvacuationBlocked
			}
		}
		out.TransportSessionID = request.TransportSessionID
		out.TransportGeneration = 1
		out.CopyOperationID = request.CopyOperationID
		out.SourceHostAuthorityGeneration = sourceAuthority
		out.DestinationHostAuthorityGeneration = destinationAuthority
		out.SourceCredentialBindingRevision = sourceCredential
		out.DestinationCredentialBindingRevision = destinationCredential
		out.SourceSessionGeneration = sourceSession
		out.DestinationSessionGeneration = destinationSession
		out.SourceCertificateFingerprint = sourceFingerprint
		out.DestinationCertificateFingerprint = destinationFingerprint
		out.ChunkSizeBytes = request.ChunkSizeBytes
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()+$1::interval`, request.Duration.String()).Scan(&out.ExpiresAt); err != nil {
			return err
		}
		authorityDigest := out.AgentAuthority().Digest()
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_transport_session_evidence(transport_session_id,transport_generation,copy_operation_id,copy_generation,source_host_id,source_host_authority_generation,source_credential_binding_revision,source_session_generation,source_volume_id,source_binding_id,source_binding_generation,source_vg_uuid,source_lv_uuid,destination_host_id,destination_host_authority_generation,destination_credential_binding_revision,destination_session_generation,destination_volume_id,destination_binding_id,destination_binding_generation,destination_vg_uuid,destination_lv_uuid,exact_byte_count,chunk_size_bytes,chunk_profile,digest_algorithm,transport_policy_revision,maximum_concurrent_per_host,bandwidth_limit_bytes_per_second,expires_at,authority_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,'EXACT_OFFSET_SHA256_V1','SHA-256',1,$24,$25,$26,$27)`, out.TransportSessionID, out.CopyOperationID, out.CopyGeneration, out.SourceHostID, sourceAuthority, sourceCredential, sourceSession, out.SourceVolumeID, out.SourceBindingID, out.SourceBindingGeneration, out.SourceVGUUID, out.SourceLVUUID, out.DestinationHostID, destinationAuthority, destinationCredential, destinationSession, out.DestinationVolumeID, out.DestinationBindingID, out.DestinationBindingGeneration, out.DestinationVGUUID, out.DestinationLVUUID, out.ExactByteCount, request.ChunkSizeBytes, request.MaximumConcurrentPerHost, bandwidth, out.ExpiresAt, authorityDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_transport_sessions_current(transport_session_id,transport_generation,session_state) VALUES($1,1,'AUTHORIZED')`, out.TransportSessionID); err != nil {
			return err
		}
		out.AuthorityDigest = authorityDigest
		return nil
	})
	return out, err
}

// AgentAuthority is the only projection delivered to either Agent. Its digest
// implementation is shared with the data plane so CP and both peers authorize
// exactly the same immutable field set.
func (s LocalLVMTransportSession) AgentAuthority() transportauthority.Authority {
	return transportauthority.Authority{
		TransportSessionID: s.TransportSessionID, TransportGeneration: s.TransportGeneration,
		CopyOperationID: s.CopyOperationID, CopyGeneration: s.CopyGeneration,
		Source:                        transportauthority.VolumeIdentity{HostID: s.SourceHostID, VolumeID: s.SourceVolumeID, BindingID: s.SourceBindingID, BindingGeneration: s.SourceBindingGeneration, VGUUID: s.SourceVGUUID, LVUUID: s.SourceLVUUID},
		Destination:                   transportauthority.VolumeIdentity{HostID: s.DestinationHostID, VolumeID: s.DestinationVolumeID, BindingID: s.DestinationBindingID, BindingGeneration: s.DestinationBindingGeneration, VGUUID: s.DestinationVGUUID, LVUUID: s.DestinationLVUUID},
		SourceHostAuthorityGeneration: s.SourceHostAuthorityGeneration, DestinationHostAuthorityGeneration: s.DestinationHostAuthorityGeneration,
		SourceCredentialBindingRevision: s.SourceCredentialBindingRevision, DestinationCredentialBindingRevision: s.DestinationCredentialBindingRevision,
		SourceSessionGeneration: s.SourceSessionGeneration, DestinationSessionGeneration: s.DestinationSessionGeneration,
		ExactByteCount: s.ExactByteCount, ChunkSize: s.ChunkSizeBytes, DigestAlgorithm: "SHA-256", TransportPolicyRevision: 1, ExpiresAt: s.ExpiresAt,
		SourceCertificateFingerprint: s.SourceCertificateFingerprint, DestinationCertificateFingerprint: s.DestinationCertificateFingerprint,
	}
}

func RecordLocalLVMTransportProgress(ctx context.Context, db TxBeginner, sessionID string, attempt int, bytes, lastOffset uint64, eventType, responseState string) error {
	if sessionID == "" || attempt < 1 || lastOffset > bytes {
		return ErrHostEvacuationConflict
	}
	valid := map[string]bool{"STARTED": true, "PROGRESS": true, "DISCONNECTED": true, "LEASE_EXPIRED": true, "READ_BACK": true}
	if !valid[eventType] {
		return ErrHostEvacuationConflict
	}
	state := "STREAMING"
	if eventType == "DISCONNECTED" {
		state = "UNKNOWN"
	}
	if eventType == "LEASE_EXPIRED" {
		state = "PARTIAL"
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var generation, exact uint64
		var currentAttempt int
		if err := tx.QueryRow(ctx, `SELECT evidence.transport_generation,evidence.exact_byte_count,current.attempt_index FROM kim.local_lvm_relocation_transport_session_evidence evidence JOIN kim.local_lvm_relocation_transport_sessions_current current USING(transport_session_id,transport_generation) WHERE evidence.transport_session_id=$1 AND evidence.expires_at>statement_timestamp() FOR UPDATE OF current`, sessionID).Scan(&generation, &exact, &currentAttempt); err != nil || bytes > exact || attempt < currentAttempt {
			return ErrHostEvacuationStale
		}
		var sequence uint64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(event_sequence),0)+1 FROM kim.local_lvm_relocation_transport_event_evidence WHERE transport_session_id=$1 AND transport_generation=$2`, sessionID, generation).Scan(&sequence); err != nil {
			return err
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%d/%s/%d/%d/%s", sessionID, generation, attempt, eventType, bytes, lastOffset, responseState)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_transport_event_evidence(transport_session_id,transport_generation,event_sequence,attempt_index,event_type,bytes_transferred,last_verified_offset,response_state,event_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, sessionID, generation, sequence, attempt, eventType, bytes, lastOffset, responseState, digest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.local_lvm_relocation_transport_sessions_current SET session_state=$2,attempt_index=$3,bytes_transferred=$4,last_verified_offset=$5,response_state=$6,updated_at=statement_timestamp() WHERE transport_session_id=$1`, sessionID, state, attempt, bytes, lastOffset, responseState)
		return err
	})
}

// RecordLocalLVMTransportPeerObservation ingests one Agent's independent
// whole-volume read-back. It is bound to that Host's current credential and
// Agent session before becoming immutable evidence. It does not decide
// cross-Host content identity.
func RecordLocalLVMTransportPeerObservation(ctx context.Context, db TxBeginner, sessionID string, observation LocalLVMTransportPeerObservation) error {
	validDigest := func(value string) bool {
		decoded, err := hex.DecodeString(value)
		return err == nil && len(decoded) == 32
	}
	if sessionID == "" || observation.EvidenceID == "" || (observation.Role != "SOURCE" && observation.Role != "DESTINATION") || observation.HostID == "" || observation.VolumeID == "" || observation.BindingID == "" || observation.BindingGeneration == 0 || observation.LVUUID == "" || observation.CredentialBindingRevision == 0 || observation.SessionGeneration == 0 || observation.SizeBytes == 0 || observation.ObservationGeneration == 0 || observation.HolderOpen || !validDigest(observation.CertificateFingerprint) || !validDigest(observation.ContentDigest) || !validDigest(observation.ObservationDigest) || !validDigest(observation.VerifierArtifactDigest) {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var generation, sourceBindingGeneration, destinationBindingGeneration, sourceAuthority, destinationAuthority, sourceCredential, destinationCredential, sourceSession, destinationSession, exact uint64
		var sourceHost, sourceVolume, sourceBinding, sourceLV, destinationHost, destinationVolume, destinationBinding, destinationLV string
		if err := tx.QueryRow(ctx, `SELECT evidence.transport_generation,evidence.exact_byte_count,evidence.source_host_id,evidence.source_host_authority_generation,evidence.source_credential_binding_revision,evidence.source_session_generation,evidence.source_volume_id,evidence.source_binding_id,evidence.source_binding_generation,evidence.source_lv_uuid,evidence.destination_host_id,evidence.destination_host_authority_generation,evidence.destination_credential_binding_revision,evidence.destination_session_generation,evidence.destination_volume_id,evidence.destination_binding_id,evidence.destination_binding_generation,evidence.destination_lv_uuid
			FROM kim.local_lvm_relocation_transport_session_evidence evidence
			JOIN kim.local_lvm_relocation_transport_sessions_current current USING(transport_session_id,transport_generation)
			WHERE evidence.transport_session_id=$1 AND current.session_state IN ('AUTHORIZED','STREAMING','PARTIAL','UNKNOWN','COMPLETED') FOR UPDATE OF current`, sessionID).Scan(&generation, &exact, &sourceHost, &sourceAuthority, &sourceCredential, &sourceSession, &sourceVolume, &sourceBinding, &sourceBindingGeneration, &sourceLV, &destinationHost, &destinationAuthority, &destinationCredential, &destinationSession, &destinationVolume, &destinationBinding, &destinationBindingGeneration, &destinationLV); err != nil {
			return ErrHostEvacuationStale
		}
		expectedHost, expectedVolume, expectedBinding, expectedLV := sourceHost, sourceVolume, sourceBinding, sourceLV
		expectedAuthority, expectedCredential, expectedSession, expectedBindingGeneration := sourceAuthority, sourceCredential, sourceSession, sourceBindingGeneration
		if observation.Role == "DESTINATION" {
			expectedHost, expectedVolume, expectedBinding, expectedLV = destinationHost, destinationVolume, destinationBinding, destinationLV
			expectedAuthority, expectedCredential, expectedSession, expectedBindingGeneration = destinationAuthority, destinationCredential, destinationSession, destinationBindingGeneration
		}
		if observation.HostID != expectedHost || observation.VolumeID != expectedVolume || observation.BindingID != expectedBinding || observation.BindingGeneration != expectedBindingGeneration || observation.LVUUID != expectedLV || observation.CredentialBindingRevision != expectedCredential || observation.SessionGeneration != expectedSession || observation.SizeBytes != exact {
			return ErrHostEvacuationBlocked
		}
		var currentFingerprint string
		if err := tx.QueryRow(ctx, `SELECT evidence.certificate_fingerprint_sha256 FROM kim.agent_credential_binding_evidence evidence
			JOIN kim.agent_credential_bindings_current credential ON credential.host_id=evidence.host_id AND credential.binding_revision=evidence.binding_revision AND credential.binding_state='CURRENT'
			JOIN kim.agent_transport_sessions_current session ON session.host_id=evidence.host_id AND session.credential_binding_revision=evidence.binding_revision AND session.session_generation=$3 AND session.state='CURRENT'
			JOIN kim.host_operation_authorities_current authority ON authority.host_id=evidence.host_id AND authority.credential_binding_revision=evidence.binding_revision AND authority.session_generation=$3 AND authority.authority_generation=$4 AND authority.authority_state='ARMED'
			WHERE evidence.host_id=$1 AND evidence.binding_revision=$2 AND evidence.binding_state='ACTIVE' AND statement_timestamp() BETWEEN evidence.valid_not_before AND evidence.valid_not_after`, observation.HostID, observation.CredentialBindingRevision, observation.SessionGeneration, expectedAuthority).Scan(&currentFingerprint); err != nil || currentFingerprint != observation.CertificateFingerprint {
			return ErrHostEvacuationStale
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_transport_peer_observation_evidence(peer_evidence_id,transport_session_id,transport_generation,peer_role,host_id,credential_binding_revision,session_generation,peer_certificate_fingerprint,volume_id,binding_id,binding_generation,lv_uuid,size_bytes,content_digest,holder_open,observation_generation,observation_digest,verifier_artifact_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,false,$15,$16,$17)`, observation.EvidenceID, sessionID, generation, observation.Role, observation.HostID, observation.CredentialBindingRevision, observation.SessionGeneration, observation.CertificateFingerprint, observation.VolumeID, observation.BindingID, observation.BindingGeneration, observation.LVUUID, observation.SizeBytes, observation.ContentDigest, observation.ObservationGeneration, observation.ObservationDigest, observation.VerifierArtifactDigest); err != nil {
			return err
		}
		return nil
	})
}

// CompleteLocalLVMTransport requires two independent Agent observations. TLS
// stream completion is not accepted as proof: exact size and whole-volume
// digests must agree and every current Host/credential/session/Binding identity
// is rejoined at terminal time.
func CompleteLocalLVMTransport(ctx context.Context, db TxBeginner, sessionID string, completion LocalLVMTransportCompletion) error {
	if sessionID == "" || completion.TerminalEvidenceID == "" || completion.SourceEvidenceID == "" || completion.DestinationEvidenceID == "" || completion.SourceEvidenceID == completion.DestinationEvidenceID || completion.AttemptIndex < 1 || completion.BytesTransferred == 0 || (completion.ResponseState != "RECEIVED" && completion.ResponseState != "LOST") {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var replaySession, replayResponse, replaySourceEvidence, replayDestinationEvidence, replaySourceDigest, replayDestinationDigest string
		var replayAttempt int
		var replayBytes uint64
		if err := tx.QueryRow(ctx, `SELECT transport_session_id,attempt_index,bytes_transferred,response_state,source_peer_evidence_id,destination_peer_evidence_id,source_content_digest,destination_content_digest FROM kim.local_lvm_relocation_transport_terminal_evidence WHERE terminal_evidence_id=$1`, completion.TerminalEvidenceID).Scan(&replaySession, &replayAttempt, &replayBytes, &replayResponse, &replaySourceEvidence, &replayDestinationEvidence, &replaySourceDigest, &replayDestinationDigest); err == nil {
			if replaySession != sessionID || replayAttempt != completion.AttemptIndex || replayBytes != completion.BytesTransferred || replayResponse != completion.ResponseState || replaySourceEvidence != completion.SourceEvidenceID || replayDestinationEvidence != completion.DestinationEvidenceID {
				return ErrHostEvacuationConflict
			}
			return nil
		} else if err != pgx.ErrNoRows {
			return err
		}
		var generation, copyGeneration, exact uint64
		var copyID, sourceHost, destinationHost, sourceVolume, sourceBinding, sourceLV, destinationVolume, destinationBinding, destinationLV string
		var sourceBindingGeneration, destinationBindingGeneration, sourceAuthority, destinationAuthority, sourceCredential, destinationCredential, sourceSession, destinationSession uint64
		if err := tx.QueryRow(ctx, `SELECT evidence.transport_generation,evidence.copy_operation_id,evidence.copy_generation,evidence.exact_byte_count,evidence.source_host_id,evidence.source_host_authority_generation,evidence.source_credential_binding_revision,evidence.source_session_generation,evidence.source_volume_id,evidence.source_binding_id,evidence.source_binding_generation,evidence.source_lv_uuid,evidence.destination_host_id,evidence.destination_host_authority_generation,evidence.destination_credential_binding_revision,evidence.destination_session_generation,evidence.destination_volume_id,evidence.destination_binding_id,evidence.destination_binding_generation,evidence.destination_lv_uuid
			FROM kim.local_lvm_relocation_transport_session_evidence evidence JOIN kim.local_lvm_relocation_transport_sessions_current current USING(transport_session_id,transport_generation)
			JOIN kim.local_lvm_relocation_copy_operation_evidence copy ON copy.copy_operation_id=evidence.copy_operation_id AND copy.copy_generation=evidence.copy_generation AND copy.source_host_id=evidence.source_host_id AND copy.source_volume_id=evidence.source_volume_id AND copy.source_binding_id=evidence.source_binding_id AND copy.source_binding_generation=evidence.source_binding_generation AND copy.source_lv_uuid=evidence.source_lv_uuid AND copy.destination_host_id=evidence.destination_host_id AND copy.destination_volume_id=evidence.destination_volume_id AND copy.destination_binding_id=evidence.destination_binding_id AND copy.destination_binding_generation=evidence.destination_binding_generation AND copy.destination_lv_uuid=evidence.destination_lv_uuid
			JOIN kim.host_evacuation_source_storage_safety_evidence safety ON safety.safety_evidence_id=copy.source_storage_safety_evidence_id AND safety.safety_digest=copy.source_storage_safety_digest AND safety.safety_state='SAFE'
			JOIN kim.planned_source_quiescence_evidence quiescence ON quiescence.quiescence_evidence_id=safety.quiescence_evidence_id AND quiescence.child_operation_id=copy.child_operation_id AND quiescence.child_generation=copy.child_generation AND quiescence.vm_id=copy.vm_id AND quiescence.vm_generation=copy.vm_generation AND quiescence.source_host_id=copy.source_host_id AND quiescence.source_materialization_generation=copy.source_materialization_generation
			JOIN kim.virtual_machines_current vm ON vm.vm_id=copy.vm_id AND vm.vm_generation=copy.vm_generation AND vm.host_id=copy.source_host_id AND vm.current_plan_id=safety.source_plan_id
			JOIN kim.vm_power_state_current source_power ON source_power.vm_id=copy.vm_id AND source_power.vm_generation=copy.vm_generation AND source_power.observed_power_state='SHUTOFF' AND source_power.convergence_state='MATCHED' AND source_power.evidence_id=safety.power_observation_evidence_id AND source_power.observation_generation=safety.power_observation_generation
			JOIN kim.volume_attachment_observations_current source_holder ON source_holder.attachment_id=safety.root_attachment_id AND source_holder.attachment_generation=safety.root_attachment_generation AND source_holder.binding_id=copy.source_binding_id AND source_holder.binding_generation=copy.source_binding_generation AND source_holder.host_id=copy.source_host_id AND source_holder.observed_lv_uuid=copy.source_lv_uuid AND NOT source_holder.holder_open
			JOIN kim.host_operation_authorities_current source_auth ON source_auth.host_id=evidence.source_host_id AND source_auth.authority_generation=evidence.source_host_authority_generation AND source_auth.credential_binding_revision=evidence.source_credential_binding_revision AND source_auth.session_generation=evidence.source_session_generation AND source_auth.authority_state='ARMED'
			JOIN kim.agent_credential_bindings_current source_credential ON source_credential.host_id=evidence.source_host_id AND source_credential.binding_revision=evidence.source_credential_binding_revision AND source_credential.binding_state='CURRENT'
			JOIN kim.agent_transport_sessions_current source_session ON source_session.host_id=evidence.source_host_id AND source_session.session_generation=evidence.source_session_generation AND source_session.credential_binding_revision=evidence.source_credential_binding_revision AND source_session.state='CURRENT'
			JOIN kim.host_operation_authorities_current destination_auth ON destination_auth.host_id=evidence.destination_host_id AND destination_auth.authority_generation=evidence.destination_host_authority_generation AND destination_auth.credential_binding_revision=evidence.destination_credential_binding_revision AND destination_auth.session_generation=evidence.destination_session_generation AND destination_auth.authority_state='ARMED'
			JOIN kim.agent_credential_bindings_current destination_credential ON destination_credential.host_id=evidence.destination_host_id AND destination_credential.binding_revision=evidence.destination_credential_binding_revision AND destination_credential.binding_state='CURRENT'
			JOIN kim.agent_transport_sessions_current destination_session ON destination_session.host_id=evidence.destination_host_id AND destination_session.session_generation=evidence.destination_session_generation AND destination_session.credential_binding_revision=evidence.destination_credential_binding_revision AND destination_session.state='CURRENT'
			JOIN kim.volume_backend_bindings_current source_binding ON source_binding.binding_id=evidence.source_binding_id AND source_binding.binding_generation=evidence.source_binding_generation AND source_binding.volume_id=evidence.source_volume_id AND source_binding.host_id=evidence.source_host_id AND source_binding.lv_uuid=evidence.source_lv_uuid AND source_binding.binding_state='BOUND'
			JOIN kim.volume_backend_bindings_current destination_binding ON destination_binding.binding_id=evidence.destination_binding_id AND destination_binding.binding_generation=evidence.destination_binding_generation AND destination_binding.volume_id=evidence.destination_volume_id AND destination_binding.host_id=evidence.destination_host_id AND destination_binding.lv_uuid=evidence.destination_lv_uuid AND destination_binding.binding_state='BOUND'
			WHERE evidence.transport_session_id=$1 AND current.session_state IN ('AUTHORIZED','STREAMING','PARTIAL','UNKNOWN','COMPLETED') FOR UPDATE OF current,source_auth,destination_auth,source_binding,destination_binding`, sessionID).Scan(&generation, &copyID, &copyGeneration, &exact, &sourceHost, &sourceAuthority, &sourceCredential, &sourceSession, &sourceVolume, &sourceBinding, &sourceBindingGeneration, &sourceLV, &destinationHost, &destinationAuthority, &destinationCredential, &destinationSession, &destinationVolume, &destinationBinding, &destinationBindingGeneration, &destinationLV); err != nil {
			return ErrHostEvacuationStale
		}
		if completion.BytesTransferred != exact {
			return ErrHostEvacuationBlocked
		}
		var sourceDigest, destinationDigest string
		if err := tx.QueryRow(ctx, `SELECT source.content_digest,destination.content_digest
			FROM kim.local_lvm_relocation_transport_peer_observation_evidence source
			JOIN kim.local_lvm_relocation_transport_peer_observation_evidence destination ON destination.transport_session_id=source.transport_session_id AND destination.transport_generation=source.transport_generation
			WHERE source.peer_evidence_id=$1 AND destination.peer_evidence_id=$2
			AND source.transport_session_id=$3 AND source.transport_generation=$4
			AND source.peer_role='SOURCE' AND source.host_id=$5 AND source.credential_binding_revision=$6 AND source.session_generation=$7 AND source.volume_id=$8 AND source.binding_id=$9 AND source.binding_generation=$10 AND source.lv_uuid=$11 AND source.size_bytes=$12 AND NOT source.holder_open
			AND destination.peer_role='DESTINATION' AND destination.host_id=$13 AND destination.credential_binding_revision=$14 AND destination.session_generation=$15 AND destination.volume_id=$16 AND destination.binding_id=$17 AND destination.binding_generation=$18 AND destination.lv_uuid=$19 AND destination.size_bytes=$12 AND NOT destination.holder_open`, completion.SourceEvidenceID, completion.DestinationEvidenceID, sessionID, generation, sourceHost, sourceCredential, sourceSession, sourceVolume, sourceBinding, sourceBindingGeneration, sourceLV, exact, destinationHost, destinationCredential, destinationSession, destinationVolume, destinationBinding, destinationBindingGeneration, destinationLV).Scan(&sourceDigest, &destinationDigest); err != nil || sourceDigest != destinationDigest {
			return ErrHostEvacuationBlocked
		}
		terminalDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%d/%d/%d/%s/%s", sessionID, generation, copyID, copyGeneration, completion.AttemptIndex, exact, sourceDigest, destinationDigest)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_transport_terminal_evidence(terminal_evidence_id,transport_session_id,transport_generation,copy_operation_id,copy_generation,attempt_index,bytes_transferred,response_state,source_peer_evidence_id,destination_peer_evidence_id,source_content_digest,destination_content_digest,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'VERIFIED',$13)`, completion.TerminalEvidenceID, sessionID, generation, copyID, copyGeneration, completion.AttemptIndex, exact, completion.ResponseState, completion.SourceEvidenceID, completion.DestinationEvidenceID, sourceDigest, destinationDigest, terminalDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.local_lvm_relocation_transport_sessions_current SET session_state='VERIFIED',attempt_index=$2,bytes_transferred=$3,last_verified_offset=$3,response_state=$4,terminal_evidence_id=$5,updated_at=statement_timestamp() WHERE transport_session_id=$1`, sessionID, completion.AttemptIndex, exact, completion.ResponseState, completion.TerminalEvidenceID); err != nil {
			return err
		}
		return nil
	})
}
