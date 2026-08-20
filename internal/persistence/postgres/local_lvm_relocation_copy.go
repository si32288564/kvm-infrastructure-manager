package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const (
	LocalLVMRelocationCopyCommandType = "VIRTUAL_MACHINE_ROOT_VOLUME_COPY"
	LocalLVMRelocationCopySchema      = "kim.command.virtual-machine-root-volume-copy/v1"
)

type LocalLVMRelocationCopyRequest struct {
	CopyOperationID, DestinationAdmissionID, SourceSafetyEvidenceID string
	JobID, CommandID                                                string
	VolumeOrdinal                                                   uint32
}

type LocalLVMRelocationCopyAuthority struct {
	CopyOperationID, CommandID, SourceHostID, DestinationHostID  string
	SourceVolumeID, SourceBindingID, SourceLVUUID                string
	DestinationVolumeID, DestinationBindingID, DestinationLVUUID string
	ExpectedSizeBytes, CopyGeneration                            uint64
	AuthorityDigest                                              string
}

type LocalLVMRelocationCopyVerification struct {
	VerificationID, TerminalEvidenceID, SourceContentEvidenceID, DestinationContentEvidenceID string
	ContentDigest, VerificationDigest, TerminalDigest                                         string
	AttemptIndex                                                                              int
	ResponseState                                                                             string
}

// PrepareLocalLVMRelocationCopy creates one closed typed copy Command. All
// backend identities and the exact byte range are derived from current
// Volume/Binding authority; callers can provide neither paths nor LV UUIDs.
func PrepareLocalLVMRelocationCopy(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, request LocalLVMRelocationCopyRequest) (LocalLVMRelocationCopyAuthority, error) {
	var out LocalLVMRelocationCopyAuthority
	if request.CopyOperationID == "" || request.DestinationAdmissionID == "" || request.SourceSafetyEvidenceID == "" || request.JobID == "" || request.CommandID == "" {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var childGeneration, vmGeneration, sourceMaterialization, sourceBindingGeneration, destinationBindingGeneration uint64
		var vmID, sourceHost, sourceVolume, sourceBinding, sourceVG, sourceLV, safetyDigest string
		var role, sourceMemberID, sourceMemberDigest string
		var destinationHost, destinationVolume, destinationBinding, destinationVG, destinationLV string
		var sourceSize, destinationSize uint64
		err := tx.QueryRow(ctx, `SELECT c.child_generation,e.vm_id::text,e.vm_generation,e.source_host_id,e.source_materialization_generation,
			COALESCE(member.volume_id,s.root_volume_id),COALESCE(member.binding_id,s.root_binding_id),COALESCE(member.binding_generation,s.root_binding_generation),sb.vg_uuid,sb.lv_uuid,sv.size_bytes,s.safety_digest,
			CASE WHEN $4=0 THEN 'ROOT' ELSE 'DATA' END,COALESCE(member.safety_member_evidence_id,''),COALESCE(member.safety_member_digest,''),
			d.host_id,dv.volume_id,db.binding_id,db.binding_generation,db.vg_uuid,db.lv_uuid,dv.size_bytes
			FROM kim.host_evacuation_workloads_current c
			JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.host_evacuation_source_storage_safety_evidence s ON s.safety_evidence_id=$3 AND s.child_operation_id=c.child_operation_id AND s.child_generation=c.child_generation AND s.safety_state='SAFE'
			LEFT JOIN kim.host_evacuation_source_storage_safety_set_evidence safety_set ON safety_set.root_storage_safety_evidence_id=s.safety_evidence_id AND safety_set.child_operation_id=c.child_operation_id AND safety_set.safety_state='SAFE'
			LEFT JOIN kim.host_evacuation_source_storage_volume_safety_evidence member ON member.root_storage_safety_evidence_id=s.safety_evidence_id AND member.child_operation_id=c.child_operation_id AND member.volume_ordinal=$4 AND member.safety_state='SAFE'
			JOIN kim.volumes_current sv ON sv.volume_id=COALESCE(member.volume_id,s.root_volume_id) AND sv.placement_admission_id=e.source_admission_id AND sv.bootable=($4=0)
			JOIN kim.volume_backend_bindings_current sb ON sb.binding_id=COALESCE(member.binding_id,s.root_binding_id) AND sb.binding_generation=COALESCE(member.binding_generation,s.root_binding_generation) AND sb.volume_id=sv.volume_id AND sb.host_id=e.source_host_id AND sb.lv_uuid IS NOT NULL AND sb.binding_state='BOUND'
			JOIN kim.placement_admission_decisions d ON d.admission_id=$2 AND d.workload_id=e.workload_id AND d.host_id<>e.source_host_id AND d.decision_state='ACCEPTED'
			CROSS JOIN LATERAL jsonb_array_elements(d.storage_requirements) destination_requirement(value)
			JOIN kim.volumes_current dv ON dv.volume_id=destination_requirement.value->>'VolumeID' AND dv.placement_admission_id=d.admission_id AND dv.bootable=($4=0) AND dv.storage_class_id=sv.storage_class_id AND dv.storage_class_revision=sv.storage_class_revision AND dv.access_mode=sv.access_mode
			JOIN kim.volume_backend_bindings_current db ON db.volume_id=dv.volume_id AND db.host_id=d.host_id AND db.binding_state='BOUND' AND db.lv_uuid IS NOT NULL
			JOIN kim.vm_power_state_current power ON power.vm_id=e.vm_id AND power.vm_generation=e.vm_generation AND power.observed_power_state='SHUTOFF' AND power.convergence_state='MATCHED'
			JOIN kim.vm_power_observation_evidence power_evidence ON power_evidence.evidence_id=power.evidence_id AND power_evidence.host_id=e.source_host_id AND power_evidence.observation_generation=power.observation_generation AND power_evidence.observed_power_state='SHUTOFF'
			JOIN kim.volume_attachment_observations_current holder ON holder.attachment_id=COALESCE(member.attachment_id,s.root_attachment_id) AND holder.attachment_generation=COALESCE(member.attachment_generation,s.root_attachment_generation) AND NOT holder.holder_open
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' AND ($4=0 OR (safety_set.volume_count>$4 AND member.safety_member_evidence_id IS NOT NULL)) FOR UPDATE OF c,sb,db,power,holder`, claim.ChildOperationID, request.DestinationAdmissionID, request.SourceSafetyEvidenceID, request.VolumeOrdinal).Scan(
			&childGeneration, &vmID, &vmGeneration, &sourceHost, &sourceMaterialization, &sourceVolume, &sourceBinding, &sourceBindingGeneration, &sourceVG, &sourceLV, &sourceSize, &safetyDigest,
			&role, &sourceMemberID, &sourceMemberDigest,
			&destinationHost, &destinationVolume, &destinationBinding, &destinationBindingGeneration, &destinationVG, &destinationLV, &destinationSize)
		if err != nil || request.VolumeOrdinal > 1 || sourceSize == 0 || sourceSize != destinationSize || sourceLV == destinationLV {
			return ErrHostEvacuationBlocked
		}
		payload := map[string]any{"copy_operation_id": request.CopyOperationID, "copy_generation": uint64(1), "source_host_id": sourceHost, "source_volume_id": sourceVolume, "source_binding_id": sourceBinding, "source_binding_generation": sourceBindingGeneration, "source_vg_uuid": sourceVG, "source_lv_uuid": sourceLV, "destination_host_id": destinationHost, "destination_volume_id": destinationVolume, "destination_binding_id": destinationBinding, "destination_binding_generation": destinationBindingGeneration, "destination_vg_uuid": destinationVG, "destination_lv_uuid": destinationLV, "exact_byte_count": sourceSize, "digest_algorithm": "SHA-256", "copy_policy_revision": uint64(1), "desired_state": "CONTENT_IDENTICAL"}
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: request.JobID, CommandID: request.CommandID, HostID: destinationHost, ResourceType: "LOCAL_LVM_RELOCATION_COPY", ResourceID: request.CopyOperationID, DesiredRevision: 1, CommandType: LocalLVMRelocationCopyCommandType, SchemaVersion: LocalLVMRelocationCopySchema, TargetResourceID: "local-lvm-relocation:" + request.CopyOperationID, Payload: payload}); err != nil {
			return err
		}
		authorityDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%s/%d/%d/%s/%s/%d/%s/%d/%d/%s/%s", claim.ChildOperationID, vmID, vmGeneration, sourceHost, sourceMaterialization, request.VolumeOrdinal, sourceVolume, sourceBinding, sourceBindingGeneration, destinationVolume, destinationBindingGeneration, sourceSize, safetyDigest, sourceMemberDigest)))
		var memberID, memberDigest any
		if sourceMemberID != "" {
			memberID, memberDigest = sourceMemberID, sourceMemberDigest
		}
		tag, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,source_materialization_generation,source_volume_id,source_binding_id,source_binding_generation,source_vg_uuid,source_lv_uuid,source_storage_safety_evidence_id,source_storage_safety_digest,destination_host_id,destination_admission_id,destination_volume_id,destination_binding_id,destination_binding_generation,destination_vg_uuid,destination_lv_uuid,expected_size_bytes,digest_algorithm,block_profile,copy_policy_revision,command_id,authority_digest,volume_ordinal,device_role,source_volume_safety_evidence_id,source_volume_safety_digest) VALUES($1,1,$2,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,'SHA-256','EXACT_BYTE_RANGE_V1',1,$23,$24,$25,$26,$27,$28) ON CONFLICT(copy_operation_id) DO NOTHING`, request.CopyOperationID, claim.ChildOperationID, childGeneration, vmID, vmGeneration, sourceHost, sourceMaterialization, sourceVolume, sourceBinding, sourceBindingGeneration, sourceVG, sourceLV, request.SourceSafetyEvidenceID, safetyDigest, destinationHost, request.DestinationAdmissionID, destinationVolume, destinationBinding, destinationBindingGeneration, destinationVG, destinationLV, sourceSize, request.CommandID, authorityDigest, request.VolumeOrdinal, role, memberID, memberDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if _, err = tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_copy_operations_current(copy_operation_id,copy_generation,operation_state,response_state) VALUES($1,1,'PENDING','PENDING')`, request.CopyOperationID); err != nil {
				return err
			}
		}
		var acceptedDigest string
		if err := tx.QueryRow(ctx, `SELECT authority_digest FROM kim.local_lvm_relocation_copy_operation_evidence WHERE copy_operation_id=$1 AND child_operation_id=$2 AND destination_admission_id=$3 AND command_id=$4`, request.CopyOperationID, claim.ChildOperationID, request.DestinationAdmissionID, request.CommandID).Scan(&acceptedDigest); err != nil || acceptedDigest != authorityDigest {
			return ErrHostEvacuationConflict
		}
		out = LocalLVMRelocationCopyAuthority{CopyOperationID: request.CopyOperationID, CommandID: request.CommandID, SourceHostID: sourceHost, DestinationHostID: destinationHost, SourceVolumeID: sourceVolume, SourceBindingID: sourceBinding, SourceLVUUID: sourceLV, DestinationVolumeID: destinationVolume, DestinationBindingID: destinationBinding, DestinationLVUUID: destinationLV, ExpectedSizeBytes: sourceSize, CopyGeneration: 1, AuthorityDigest: authorityDigest}
		return nil
	})
	return out, err
}

// VerifyLocalLVMRelocationCopy consumes a MATCHED typed read-back. Positive
// content identity is derived only from equal exact sizes and equal SHA-256
// digests; no caller boolean is accepted.
type localLVMCopyConsumer struct {
	kind, id string
	claim    HostEvacuationClaim
}

func VerifyLocalLVMRelocationCopy(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, operationID, commandVerificationID, sourceEvidenceID, destinationEvidenceID, verificationID, terminalID string) (LocalLVMRelocationCopyVerification, error) {
	return verifyLocalLVMRelocationCopy(ctx, db, localLVMCopyConsumer{kind: "HOST_EVACUATION", id: claim.ChildOperationID, claim: claim}, operationID, commandVerificationID, sourceEvidenceID, destinationEvidenceID, verificationID, terminalID)
}

func VerifyRecoveryLocalLVMDataCopy(ctx context.Context, db TxBeginner, recoveryOperationID, operationID, commandVerificationID, sourceEvidenceID, destinationEvidenceID, verificationID, terminalID string) (LocalLVMRelocationCopyVerification, error) {
	return verifyLocalLVMRelocationCopy(ctx, db, localLVMCopyConsumer{kind: "RECOVERY", id: recoveryOperationID}, operationID, commandVerificationID, sourceEvidenceID, destinationEvidenceID, verificationID, terminalID)
}

func verifyLocalLVMRelocationCopy(ctx context.Context, db TxBeginner, consumer localLVMCopyConsumer, operationID, commandVerificationID, sourceEvidenceID, destinationEvidenceID, verificationID, terminalID string) (LocalLVMRelocationCopyVerification, error) {
	var out LocalLVMRelocationCopyVerification
	if consumer.id == "" || operationID == "" || commandVerificationID == "" || sourceEvidenceID == "" || destinationEvidenceID == "" || verificationID == "" || terminalID == "" {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if consumer.kind == "HOST_EVACUATION" {
			if err := validateEvacuationClaimTx(ctx, tx, consumer.claim); err != nil {
				return err
			}
		} else if consumer.kind == "RECOVERY" {
			var state string
			if err := tx.QueryRow(ctx, `SELECT lifecycle_state FROM kim.recovery_operations_current WHERE recovery_operation_id=$1 FOR SHARE`, consumer.id).Scan(&state); err != nil || (state != "RUNNING" && state != "VERIFYING") {
				return ErrRecoveryOperationBlocked
			}
		} else {
			return ErrHostEvacuationConflict
		}
		var existingOperation string
		if err := tx.QueryRow(ctx, `SELECT copy_operation_id FROM kim.local_lvm_relocation_copy_terminal_evidence WHERE terminal_evidence_id=$1`, terminalID).Scan(&existingOperation); err == nil {
			if existingOperation != operationID {
				return ErrHostEvacuationConflict
			}
			return loadLocalLVMCopyVerificationTx(ctx, tx, operationID, &out)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var generation, sourceBindingGeneration, destinationBindingGeneration, expectedSize, sourceMaterialization uint64
		var commandID, sourceHost, sourceVolume, sourceBinding, sourceLV, destinationHost, destinationAdmission, destinationVolume, destinationBinding, destinationLV, safetyID, safetyDigest string
		consumerColumn := "child_operation_id"
		if consumer.kind == "RECOVERY" {
			consumerColumn = "recovery_operation_id"
		}
		if err := tx.QueryRow(ctx, `SELECT copy_generation,command_id,source_host_id,source_materialization_generation,source_volume_id,source_binding_id,source_binding_generation,source_lv_uuid,coalesce(source_storage_safety_evidence_id,recovery_storage_safety_proof_id),coalesce(source_storage_safety_digest,recovery_storage_safety_proof_digest),destination_host_id,destination_admission_id,destination_volume_id,destination_binding_id,destination_binding_generation,destination_lv_uuid,expected_size_bytes FROM kim.local_lvm_relocation_copy_operation_evidence WHERE copy_operation_id=$1 AND `+consumerColumn+`=$2`, operationID, consumer.id).Scan(&generation, &commandID, &sourceHost, &sourceMaterialization, &sourceVolume, &sourceBinding, &sourceBindingGeneration, &sourceLV, &safetyID, &safetyDigest, &destinationHost, &destinationAdmission, &destinationVolume, &destinationBinding, &destinationBindingGeneration, &destinationLV, &expectedSize); err != nil {
			return ErrHostEvacuationBlocked
		}
		var attempt int
		var observationGeneration uint64
		var observationDigest, verifierDigest string
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT attempt_index,observation_generation,observation_digest,verifier_artifact_digest,evidence_payload FROM kim.command_verification_evidence WHERE verification_id=$1 AND command_id=$2 AND verification_state='MATCHED'`, commandVerificationID, commandID).Scan(&attempt, &observationGeneration, &observationDigest, &verifierDigest, &raw); err != nil {
			return ErrHostEvacuationBlocked
		}
		var evidence map[string]any
		if json.Unmarshal(raw, &evidence) != nil {
			return ErrHostEvacuationBlocked
		}
		if nested, ok := evidence["read_back"].(map[string]any); ok {
			evidence = nested
		}
		stringValue := func(key string) string { value, _ := evidence[key].(string); return value }
		uintValue := func(key string) uint64 { value, _ := evidence[key].(float64); return uint64(value) }
		sourceDigest, destinationDigest := stringValue("source_content_digest"), stringValue("destination_content_digest")
		if stringValue("copy_operation_id") != operationID || stringValue("source_host_id") != sourceHost || stringValue("source_volume_id") != sourceVolume || stringValue("source_binding_id") != sourceBinding || uintValue("source_binding_generation") != sourceBindingGeneration || stringValue("source_lv_uuid") != sourceLV || stringValue("destination_host_id") != destinationHost || stringValue("destination_volume_id") != destinationVolume || stringValue("destination_binding_id") != destinationBinding || uintValue("destination_binding_generation") != destinationBindingGeneration || stringValue("destination_lv_uuid") != destinationLV || uintValue("source_size_bytes") != expectedSize || uintValue("destination_size_bytes") != expectedSize || stringValue("digest_algorithm") != "SHA-256" || len(sourceDigest) != 64 || sourceDigest != destinationDigest || stringValue("copy_state") != "COMPLETE" {
			return ErrHostEvacuationBlocked
		}
		var transportTerminalID, transportTerminalDigest string
		var transportCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.local_lvm_relocation_transport_session_evidence WHERE copy_operation_id=$1 AND copy_generation=$2`, operationID, generation).Scan(&transportCount); err != nil {
			return err
		}
		if transportCount > 0 {
			if err := tx.QueryRow(ctx, `SELECT terminal.terminal_evidence_id,terminal.terminal_digest
				FROM kim.local_lvm_relocation_transport_terminal_evidence terminal
				JOIN kim.local_lvm_relocation_transport_sessions_current current USING(transport_session_id,transport_generation)
				WHERE terminal.copy_operation_id=$1 AND terminal.copy_generation=$2 AND terminal.terminal_state='VERIFIED'
				AND terminal.bytes_transferred=$3 AND terminal.source_content_digest=$4 AND terminal.destination_content_digest=$5
				AND current.session_state='VERIFIED' AND current.terminal_evidence_id=terminal.terminal_evidence_id`, operationID, generation, expectedSize, sourceDigest, destinationDigest).Scan(&transportTerminalID, &transportTerminalDigest); err != nil {
				return ErrHostEvacuationBlocked
			}
		}
		// Re-read all source safety and both binding incarnations immediately before accepting identity.
		var stillCurrent bool
		var currentErr error
		if consumer.kind == "HOST_EVACUATION" {
			currentErr = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_evacuation_source_storage_safety_evidence s JOIN kim.volume_backend_bindings_current sb ON sb.binding_id=$3 AND sb.binding_generation=$4 AND sb.volume_id=$5 AND sb.lv_uuid=$6 AND sb.host_id=$7 AND sb.binding_state='BOUND' JOIN kim.volume_backend_bindings_current db ON db.binding_id=$8 AND db.binding_generation=$9 AND db.volume_id=$10 AND db.lv_uuid=$11 AND db.host_id=$12 AND db.binding_state='BOUND' JOIN kim.virtual_machines_current vm ON vm.vm_id=s.vm_id AND vm.current_plan_id=s.source_plan_id AND vm.host_id=s.source_host_id WHERE s.safety_evidence_id=$1 AND s.safety_digest=$2 AND s.source_materialization_generation=$13 AND s.safety_state='SAFE')`, safetyID, safetyDigest, sourceBinding, sourceBindingGeneration, sourceVolume, sourceLV, sourceHost, destinationBinding, destinationBindingGeneration, destinationVolume, destinationLV, destinationHost, sourceMaterialization).Scan(&stillCurrent)
		} else {
			currentErr = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.local_lvm_relocation_copy_operation_evidence copy JOIN kim.storage_safety_proof_evidence proof ON proof.proof_id=copy.recovery_storage_safety_proof_id AND proof.proof_digest=copy.recovery_storage_safety_proof_digest AND proof.proof_state='SAFE' JOIN kim.storage_safety_evaluation_input_evidence input ON input.evaluation_id=proof.evaluation_id AND input.attachment_evidence_id=copy.recovery_source_attachment_evidence_id AND input.observation_digest=copy.recovery_source_attachment_observation_digest JOIN kim.volume_attachment_observations_current source_current ON source_current.evidence_id=input.attachment_evidence_id AND source_current.observation_generation=input.observation_generation AND source_current.attachment_state='DETACHED' AND NOT source_current.device_present AND NOT source_current.holder_open JOIN kim.volume_attachment_claims source_claim ON source_claim.attachment_claim_id=input.attachment_claim_id AND source_claim.claim_state='RELEASED' AND source_claim.claim_state_generation=input.claim_state_generation JOIN kim.volume_backend_bindings_current sb ON sb.binding_id=$3 AND sb.binding_generation=$4 AND sb.volume_id=$5 AND sb.lv_uuid=$6 AND sb.host_id=$7 AND sb.binding_state='BOUND' JOIN kim.volume_backend_bindings_current db ON db.binding_id=$8 AND db.binding_generation=$9 AND db.volume_id=$10 AND db.lv_uuid=$11 AND db.host_id=$12 AND db.binding_state='BOUND' WHERE copy.copy_operation_id=$1 AND copy.recovery_operation_id=$2 AND copy.source_materialization_generation=$13)`, operationID, consumer.id, sourceBinding, sourceBindingGeneration, sourceVolume, sourceLV, sourceHost, destinationBinding, destinationBindingGeneration, destinationVolume, destinationLV, destinationHost, sourceMaterialization).Scan(&stillCurrent)
		}
		if currentErr != nil || !stillCurrent {
			return ErrHostEvacuationStale
		}
		var leaseGeneration, hostAuthority, sessionGeneration uint64
		var resultCount int
		if err := tx.QueryRow(ctx, `SELECT lease_generation,host_authority_generation,session_generation,(SELECT count(*) FROM kim.command_results r WHERE r.command_id=a.command_id AND r.attempt_index=a.attempt_index) FROM kim.command_attempts a WHERE command_id=$1 AND attempt_index=$2`, commandID, attempt).Scan(&leaseGeneration, &hostAuthority, &sessionGeneration, &resultCount); err != nil {
			return ErrHostEvacuationBlocked
		}
		responseState := "LOST"
		if resultCount > 0 {
			responseState = "RECEIVED"
		}
		attemptDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%d/%s", operationID, attempt, leaseGeneration, responseState)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_copy_attempt_evidence(copy_operation_id,copy_generation,attempt_index,command_id,lease_generation,host_authority_generation,session_generation,response_state,attempt_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, operationID, generation, attempt, commandID, leaseGeneration, hostAuthority, sessionGeneration, responseState, attemptDigest); err != nil {
			return err
		}
		sourceEvidenceDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/SOURCE/%s/%s/%d/%s/%d/%s", operationID, sourceVolume, sourceBinding, sourceBindingGeneration, sourceLV, expectedSize, sourceDigest)))
		destinationEvidenceDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/DESTINATION/%s/%s/%d/%s/%d/%s", operationID, destinationVolume, destinationBinding, destinationBindingGeneration, destinationLV, expectedSize, destinationDigest)))
		for _, row := range []struct {
			id, role, host, volume, binding, lv, digest, evidenceDigest string
			bindingGeneration                                           uint64
		}{{sourceEvidenceID, "SOURCE_POINT", sourceHost, sourceVolume, sourceBinding, sourceLV, sourceDigest, sourceEvidenceDigest, sourceBindingGeneration}, {destinationEvidenceID, "DESTINATION", destinationHost, destinationVolume, destinationBinding, destinationLV, destinationDigest, destinationEvidenceDigest, destinationBindingGeneration}} {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_content_observation_evidence(content_evidence_id,copy_operation_id,copy_generation,content_role,host_id,volume_id,binding_id,binding_generation,lv_uuid,size_bytes,digest_algorithm,content_digest,observation_generation,command_id,attempt_index,command_verification_id,observation_digest,verifier_artifact_digest,content_evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'SHA-256',$11,$12,$13,$14,$15,$16,$17,$18)`, row.id, operationID, generation, row.role, row.host, row.volume, row.binding, row.bindingGeneration, row.lv, expectedSize, row.digest, observationGeneration, commandID, attempt, commandVerificationID, observationDigest, verifierDigest, row.evidenceDigest); err != nil {
				return err
			}
		}
		verificationDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%s/%s/%s/%s", operationID, generation, sourceEvidenceDigest, destinationEvidenceDigest, sourceDigest, destinationDigest, transportTerminalDigest)))
		var transportTerminal any
		if transportTerminalID != "" {
			transportTerminal = transportTerminalID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_copy_verification_evidence(verification_id,copy_operation_id,copy_generation,source_content_evidence_id,destination_content_evidence_id,expected_size_bytes,digest_algorithm,source_content_digest,destination_content_digest,content_identity_state,verification_digest,transport_terminal_evidence_id) VALUES($1,$2,$3,$4,$5,$6,'SHA-256',$7,$8,'VERIFIED',$9,$10)`, verificationID, operationID, generation, sourceEvidenceID, destinationEvidenceID, expectedSize, sourceDigest, destinationDigest, verificationDigest, transportTerminal); err != nil {
			return err
		}
		terminalDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%s/%d/%s", operationID, generation, verificationDigest, destinationBinding, destinationBindingGeneration, destinationLV)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_copy_terminal_evidence(terminal_evidence_id,copy_operation_id,copy_generation,verification_id,destination_binding_id,destination_binding_generation,destination_lv_uuid,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,$6,$7,'VERIFIED',$8)`, terminalID, operationID, generation, verificationID, destinationBinding, destinationBindingGeneration, destinationLV, terminalDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.local_lvm_relocation_copy_operations_current SET operation_state='VERIFIED',latest_attempt_index=$2,response_state=$3,verification_id=$4,terminal_evidence_id=$5,updated_at=statement_timestamp() WHERE copy_operation_id=$1 AND copy_generation=$6`, operationID, attempt, responseState, verificationID, terminalID, generation); err != nil {
			return err
		}
		out = LocalLVMRelocationCopyVerification{VerificationID: verificationID, TerminalEvidenceID: terminalID, SourceContentEvidenceID: sourceEvidenceID, DestinationContentEvidenceID: destinationEvidenceID, ContentDigest: sourceDigest, VerificationDigest: verificationDigest, TerminalDigest: terminalDigest, AttemptIndex: attempt, ResponseState: responseState}
		return nil
	})
	return out, err
}

func loadLocalLVMCopyVerificationTx(ctx context.Context, tx pgx.Tx, operationID string, out *LocalLVMRelocationCopyVerification) error {
	return tx.QueryRow(ctx, `SELECT v.verification_id,t.terminal_evidence_id,v.source_content_evidence_id,v.destination_content_evidence_id,v.source_content_digest,v.verification_digest,t.terminal_digest,c.latest_attempt_index,c.response_state FROM kim.local_lvm_relocation_copy_verification_evidence v JOIN kim.local_lvm_relocation_copy_terminal_evidence t ON t.copy_operation_id=v.copy_operation_id AND t.copy_generation=v.copy_generation AND t.verification_id=v.verification_id JOIN kim.local_lvm_relocation_copy_operations_current c ON c.copy_operation_id=v.copy_operation_id AND c.copy_generation=v.copy_generation WHERE v.copy_operation_id=$1`, operationID).Scan(&out.VerificationID, &out.TerminalEvidenceID, &out.SourceContentEvidenceID, &out.DestinationContentEvidenceID, &out.ContentDigest, &out.VerificationDigest, &out.TerminalDigest, &out.AttemptIndex, &out.ResponseState)
}
