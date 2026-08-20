package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type RecoveryLocalLVMDataCopyRequest struct {
	RecoveryOperationID, CopyOperationID, StorageSafetyProofID string
	JobID, CommandID                                           string
}

// PrepareRecoveryLocalLVMDataCopy is the Recovery consumer of the closed
// cross-Host copy primitive.  It accepts no backend path or LV identity: both
// endpoints are derived from the exact source safety input and destination
// Final Admission.  Recovery ROOT remains the separately qualified verified-
// image rebuild path; this consumer is deliberately DATA-only.
func PrepareRecoveryLocalLVMDataCopy(ctx context.Context, db TxBeginner, request RecoveryLocalLVMDataCopyRequest) (LocalLVMRelocationCopyAuthority, error) {
	var out LocalLVMRelocationCopyAuthority
	if request.RecoveryOperationID == "" || request.CopyOperationID == "" || request.StorageSafetyProofID == "" || request.JobID == "" || request.CommandID == "" {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+request.RecoveryOperationID); err != nil {
			return err
		}
		var vmID, sourceHost, sourceAdmission, destinationHost, destinationAdmission string
		var workloadID, proofDigest, evaluationID, attachmentEvidenceID, observationDigest string
		var sourceVolume, sourceBinding, sourceVG, sourceLV, destinationVolume, destinationBinding, destinationVG, destinationLV string
		var vmGeneration, sourceMaterialization, sourceBindingGeneration, destinationBindingGeneration, sourceSize, destinationSize uint64
		err := tx.QueryRow(ctx, `SELECT vm.vm_id::text,vm.vm_generation,runtime.materialization_generation,epoch.workload_id,epoch.source_host_id,epoch.admission_id,
			destination.destination_host_id,destination.admission_id,proof.proof_digest,proof.evaluation_id,input.attachment_evidence_id,input.observation_digest,
			observation.volume_id,observation.binding_id,observation.binding_generation,source_binding.vg_uuid,source_binding.lv_uuid,source_volume.size_bytes,
			destination_volume.volume_id,destination_binding.binding_id,destination_binding.binding_generation,destination_binding.vg_uuid,destination_binding.lv_uuid,destination_volume.size_bytes
			FROM kim.recovery_operation_evidence operation
			JOIN kim.recovery_operations_current current USING(recovery_operation_id)
			JOIN kim.failure_epoch_evidence epoch ON epoch.failure_epoch_id=operation.failure_epoch_id
			JOIN kim.virtual_machines_current vm ON vm.workload_id=epoch.workload_id AND vm.host_id=epoch.source_host_id AND vm.placement_admission_id=epoch.admission_id
			JOIN kim.vm_resource_runtime_bindings_current runtime ON runtime.vm_id=vm.vm_id AND runtime.admission_id=epoch.admission_id AND runtime.host_id=epoch.source_host_id AND runtime.vm_generation=vm.vm_generation
			JOIN kim.vm_resources_current resource ON resource.vm_id=runtime.vm_id AND resource.vm_revision=runtime.vm_revision AND resource.runtime_intent_generation=runtime.runtime_intent_generation AND resource.lifecycle_state='ACTIVE' AND resource.convergence_state='CONVERGED'
			JOIN kim.vm_runtime_intent_evidence intent ON intent.vm_id=resource.vm_id AND intent.runtime_intent_generation=resource.runtime_intent_generation
			JOIN kim.recovery_destination_admission_evidence destination ON destination.recovery_operation_id=operation.recovery_operation_id AND destination.destination_host_id=operation.selected_destination_host_id
			JOIN kim.storage_safety_proof_evidence proof ON proof.proof_id=$2 AND proof.failure_epoch_id=epoch.failure_epoch_id AND proof.proof_state='SAFE' AND proof.proof_type='LOCAL_LVM_SOURCE_ROOT_QUIESCED_DATA_DETACHED'
			JOIN kim.storage_safety_evaluation_input_evidence input ON input.evaluation_id=proof.evaluation_id AND input.claim_state='RELEASED' AND input.binding_state='BOUND'
			JOIN kim.volume_attachment_observation_evidence observation ON observation.evidence_id=input.attachment_evidence_id AND observation.attachment_id=input.attachment_id AND observation.attachment_generation=input.attachment_generation AND observation.observation_generation=input.observation_generation AND observation.desired_state='DETACHED' AND observation.evidence_state='MATCHED' AND NOT observation.device_present AND NOT observation.holder_open
			JOIN kim.vm_dependency_snapshot_evidence snapshot ON snapshot.dependency_snapshot_id=intent.dependency_snapshot_id AND snapshot.vm_id=vm.vm_id AND snapshot.volume_count=2
			JOIN kim.vm_dependency_volume_evidence dependency ON dependency.dependency_snapshot_id=snapshot.dependency_snapshot_id AND dependency.volume_ordinal=1 AND dependency.device_role='DATA' AND dependency.volume_id=observation.volume_id
			JOIN kim.volumes_current source_volume ON source_volume.volume_id=dependency.volume_id AND source_volume.volume_revision=dependency.volume_revision AND source_volume.desired_digest=dependency.desired_digest AND source_volume.placement_admission_id=epoch.admission_id AND NOT source_volume.bootable
			JOIN kim.volume_backend_bindings_current source_binding ON source_binding.binding_id=observation.binding_id AND source_binding.binding_generation=observation.binding_generation AND source_binding.volume_id=source_volume.volume_id AND source_binding.host_id=epoch.source_host_id AND source_binding.binding_state='BOUND'
			CROSS JOIN LATERAL jsonb_array_elements((SELECT storage_requirements FROM kim.placement_admission_decisions WHERE admission_id=destination.admission_id)) requirement(value)
			JOIN kim.volumes_current destination_volume ON destination_volume.volume_id=requirement.value->>'VolumeID' AND destination_volume.placement_admission_id=destination.admission_id AND NOT destination_volume.bootable AND destination_volume.storage_class_id=source_volume.storage_class_id AND destination_volume.storage_class_revision=source_volume.storage_class_revision AND destination_volume.access_mode=source_volume.access_mode AND destination_volume.size_bytes=source_volume.size_bytes
			JOIN kim.volume_backend_bindings_current destination_binding ON destination_binding.volume_id=destination_volume.volume_id AND destination_binding.host_id=destination.destination_host_id AND destination_binding.binding_state='BOUND'
			WHERE operation.recovery_operation_id=$1 AND current.lifecycle_state IN('RUNNING','VERIFYING')
			FOR UPDATE OF current,source_binding,destination_binding`, request.RecoveryOperationID, request.StorageSafetyProofID).Scan(
			&vmID, &vmGeneration, &sourceMaterialization, &workloadID, &sourceHost, &sourceAdmission,
			&destinationHost, &destinationAdmission, &proofDigest, &evaluationID, &attachmentEvidenceID, &observationDigest,
			&sourceVolume, &sourceBinding, &sourceBindingGeneration, &sourceVG, &sourceLV, &sourceSize,
			&destinationVolume, &destinationBinding, &destinationBindingGeneration, &destinationVG, &destinationLV, &destinationSize)
		if err != nil || sourceSize == 0 || sourceSize != destinationSize || sourceHost == destinationHost || sourceLV == destinationLV {
			return ErrRecoveryOperationBlocked
		}
		payload := map[string]any{"copy_operation_id": request.CopyOperationID, "copy_generation": uint64(1), "source_host_id": sourceHost, "source_volume_id": sourceVolume, "source_binding_id": sourceBinding, "source_binding_generation": sourceBindingGeneration, "source_vg_uuid": sourceVG, "source_lv_uuid": sourceLV, "destination_host_id": destinationHost, "destination_volume_id": destinationVolume, "destination_binding_id": destinationBinding, "destination_binding_generation": destinationBindingGeneration, "destination_vg_uuid": destinationVG, "destination_lv_uuid": destinationLV, "exact_byte_count": sourceSize, "digest_algorithm": "SHA-256", "copy_policy_revision": uint64(1), "desired_state": "CONTENT_IDENTICAL"}
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: request.JobID, CommandID: request.CommandID, HostID: destinationHost, ResourceType: "LOCAL_LVM_RELOCATION_COPY", ResourceID: request.CopyOperationID, DesiredRevision: 1, CommandType: LocalLVMRelocationCopyCommandType, SchemaVersion: LocalLVMRelocationCopySchema, TargetResourceID: "local-lvm-relocation:" + request.CopyOperationID, Payload: payload}); err != nil {
			return err
		}
		authorityDigest := digestReleaseBytes([]byte(fmt.Sprintf("RECOVERY/%s/%s/%d/%s/%d/%s/%s/%d/%s/%s/%d/%d/%s/%s", request.RecoveryOperationID, vmID, vmGeneration, sourceHost, sourceMaterialization, sourceVolume, sourceBinding, sourceBindingGeneration, destinationHost, destinationBinding, destinationBindingGeneration, sourceSize, proofDigest, observationDigest)))
		tag, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,source_materialization_generation,source_volume_id,source_binding_id,source_binding_generation,source_vg_uuid,source_lv_uuid,source_storage_safety_evidence_id,source_storage_safety_digest,destination_host_id,destination_admission_id,destination_volume_id,destination_binding_id,destination_binding_generation,destination_vg_uuid,destination_lv_uuid,expected_size_bytes,digest_algorithm,block_profile,copy_policy_revision,command_id,authority_digest,volume_ordinal,device_role,source_volume_safety_evidence_id,source_volume_safety_digest,recovery_operation_id,recovery_storage_safety_proof_id,recovery_storage_safety_proof_digest,recovery_source_attachment_evidence_id,recovery_source_attachment_observation_digest) VALUES($1,1,NULL,NULL,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,NULL,NULL,$11,$12,$13,$14,$15,$16,$17,$18,'SHA-256','EXACT_BYTE_RANGE_V1',1,$19,$20,1,'DATA',NULL,NULL,$21,$22,$23,$24,$25) ON CONFLICT(copy_operation_id) DO NOTHING`, request.CopyOperationID, vmID, vmGeneration, sourceHost, sourceMaterialization, sourceVolume, sourceBinding, sourceBindingGeneration, sourceVG, sourceLV, destinationHost, destinationAdmission, destinationVolume, destinationBinding, destinationBindingGeneration, destinationVG, destinationLV, sourceSize, request.CommandID, authorityDigest, request.RecoveryOperationID, request.StorageSafetyProofID, proofDigest, attachmentEvidenceID, observationDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_relocation_copy_operations_current(copy_operation_id,copy_generation,operation_state,response_state) VALUES($1,1,'PENDING','PENDING')`, request.CopyOperationID); err != nil {
				return err
			}
		}
		var accepted string
		if err := tx.QueryRow(ctx, `SELECT authority_digest FROM kim.local_lvm_relocation_copy_operation_evidence WHERE copy_operation_id=$1 AND recovery_operation_id=$2 AND command_id=$3`, request.CopyOperationID, request.RecoveryOperationID, request.CommandID).Scan(&accepted); err != nil || accepted != authorityDigest {
			return ErrRecoveryOperationConflict
		}
		out = LocalLVMRelocationCopyAuthority{CopyOperationID: request.CopyOperationID, CommandID: request.CommandID, SourceHostID: sourceHost, DestinationHostID: destinationHost, SourceVolumeID: sourceVolume, SourceBindingID: sourceBinding, SourceLVUUID: sourceLV, DestinationVolumeID: destinationVolume, DestinationBindingID: destinationBinding, DestinationLVUUID: destinationLV, ExpectedSizeBytes: sourceSize, CopyGeneration: 1, AuthorityDigest: authorityDigest}
		return nil
	})
	return out, err
}
