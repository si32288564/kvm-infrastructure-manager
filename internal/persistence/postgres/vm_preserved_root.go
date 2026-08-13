package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// AcceptVMPreservedRootReadiness consumes the verified relocation copy as the
// destination content authority. It creates no base-Image copy Command because
// that would overwrite the preserved guest state. The caller supplies only a
// new immutable evidence identity; all positive fields are joined from DB.
func AcceptVMPreservedRootReadiness(ctx context.Context, db TxBeginner, vmID, planID, evidenceID string) error {
	if !vmUUIDPattern.MatchString(vmID) || planID == "" || evidenceID == "" {
		return ErrVMMaterializationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var vmGeneration, imageRevision, bindingGeneration, observationGeneration, expectedSize uint64
		var hostID, planDigest, imageID, volumeID, bindingID, vgUUID, lvUUID, resourceKey string
		var commandID, commandVerificationID, observationDigest, verifierDigest, contentDigest string
		var attempt int
		if err := tx.QueryRow(ctx, `SELECT vm.vm_generation,vm.host_id,plan.plan_digest,plan.image_id,plan.image_revision,plan.root_volume_id,plan.root_binding_id,plan.root_binding_generation,binding.vg_uuid,binding.lv_uuid,binding.backend_resource_key,
			copy.command_id,content.command_verification_id,content.observation_generation,content.observation_digest,content.verifier_artifact_digest,content.content_digest,content.attempt_index,copy.expected_size_bytes
			FROM kim.virtual_machines_current vm
			JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=$2 AND plan.vm_id=vm.vm_id AND plan.vm_generation=vm.vm_generation AND plan.host_id=vm.host_id AND plan.placement_admission_id=vm.placement_admission_id
			JOIN kim.vm_materialization_readiness_current readiness ON readiness.vm_id=vm.vm_id AND readiness.vm_generation=vm.vm_generation AND readiness.plan_id=plan.plan_id AND readiness.domain_state='DEFINED' AND readiness.storage_state='BOUND' AND readiness.image_state='PENDING'
			JOIN kim.vm_materialization_relocation_authority_evidence relocation ON relocation.vm_id=vm.vm_id AND relocation.vm_generation=vm.vm_generation AND relocation.destination_admission_id=vm.placement_admission_id AND relocation.destination_host_id=vm.host_id AND relocation.destination_materialization_generation=(plan.plan_payload->>'materialization_generation')::bigint
			JOIN kim.local_lvm_relocation_copy_terminal_evidence terminal ON terminal.terminal_evidence_id=relocation.local_lvm_copy_terminal_evidence_id AND terminal.terminal_digest=relocation.local_lvm_copy_terminal_digest AND terminal.terminal_state='VERIFIED'
			JOIN kim.local_lvm_relocation_copy_verification_evidence verified ON verified.verification_id=terminal.verification_id AND verified.copy_operation_id=terminal.copy_operation_id AND verified.copy_generation=terminal.copy_generation AND verified.content_identity_state='VERIFIED'
			JOIN kim.local_lvm_relocation_content_observation_evidence content ON content.content_evidence_id=verified.destination_content_evidence_id AND content.content_role='DESTINATION' AND content.content_digest=verified.destination_content_digest
			JOIN kim.local_lvm_relocation_copy_operation_evidence copy ON copy.copy_operation_id=terminal.copy_operation_id AND copy.copy_generation=terminal.copy_generation AND copy.destination_volume_id=plan.root_volume_id AND copy.destination_binding_id=plan.root_binding_id AND copy.destination_binding_generation=plan.root_binding_generation AND copy.destination_lv_uuid=terminal.destination_lv_uuid
			JOIN kim.local_lvm_relocation_copy_operations_current copy_current ON copy_current.copy_operation_id=copy.copy_operation_id AND copy_current.copy_generation=copy.copy_generation AND copy_current.terminal_evidence_id=terminal.terminal_evidence_id AND copy_current.operation_state='VERIFIED'
			JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=plan.root_binding_id AND binding.binding_generation=plan.root_binding_generation AND binding.volume_id=plan.root_volume_id AND binding.host_id=vm.host_id AND binding.lv_uuid=terminal.destination_lv_uuid AND binding.binding_state='BOUND'
			JOIN kim.command_verification_evidence command_verification ON command_verification.verification_id=content.command_verification_id AND command_verification.command_id=copy.command_id AND command_verification.attempt_index=content.attempt_index AND command_verification.observation_generation=content.observation_generation AND command_verification.observation_digest=content.observation_digest AND command_verification.verifier_artifact_digest=content.verifier_artifact_digest AND command_verification.verification_state='MATCHED'
			WHERE vm.vm_id=$1::uuid AND vm.current_plan_id=plan.plan_id AND vm.lifecycle_state='DEFINED' FOR UPDATE OF vm,readiness,binding`, vmID, planID).Scan(&vmGeneration, &hostID, &planDigest, &imageID, &imageRevision, &volumeID, &bindingID, &bindingGeneration, &vgUUID, &lvUUID, &resourceKey, &commandID, &commandVerificationID, &observationGeneration, &observationDigest, &verifierDigest, &contentDigest, &attempt, &expectedSize); err != nil {
			return ErrVMMaterializationConflict
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_image_realization_evidence(evidence_id,vm_id,vm_generation,plan_id,plan_digest,host_id,image_id,image_revision,expected_content_digest,observed_content_digest,image_size_bytes,volume_id,binding_id,binding_generation,observed_vg_uuid,observed_lv_uuid,backend_resource_key,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,holder_open,content_identity_matches,evidence_state,content_origin)
			VALUES($1,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,false,true,'MATCHED','PRESERVED_ROOT')`, evidenceID, vmID, vmGeneration, planID, planDigest, hostID, imageID, imageRevision, contentDigest, expectedSize, volumeID, bindingID, bindingGeneration, vgUUID, lvUUID, resourceKey, commandID, attempt, commandVerificationID, observationGeneration, observationDigest, verifierDigest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET image_state='REALIZED',image_observation_generation=$2,image_evidence_id=$3,boot_readiness='BLOCKED',blocking_reasons=ARRAY['network_pending'],updated_at=statement_timestamp() WHERE vm_id=$1::uuid AND vm_generation=$4 AND plan_id=$5 AND image_state='PENDING'`, vmID, observationGeneration, evidenceID, vmGeneration, planID)
		return err
	})
}
