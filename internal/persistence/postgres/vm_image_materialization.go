package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/localimage"
)

type VMImageMaterializationRequest struct {
	VMID, PlanID, JobID, CommandID string
}

type VMImageMaterializationDecision struct {
	VMID, HostID, PlanID, JobID, CommandID, PayloadDigest string
}

// PrepareVMImageMaterialization creates a closed typed command from current
// VM, Image, Local LVM, and materialization authority. It never exposes an
// Image URI, cache path, target path, or copy arguments to the caller.
func PrepareVMImageMaterialization(ctx context.Context, db TxBeginner, request VMImageMaterializationRequest) (VMImageMaterializationDecision, error) {
	if !vmUUIDPattern.MatchString(request.VMID) || request.PlanID == "" || request.JobID == "" || request.CommandID == "" {
		return VMImageMaterializationDecision{}, ErrVMMaterializationConflict
	}
	var decision VMImageMaterializationDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var existingHost, existingPayloadDigest string
		err := tx.QueryRow(ctx, `
			SELECT command.host_id,command.payload_digest
			FROM kim.execution_jobs job
			JOIN kim.execution_commands command USING (job_id)
			JOIN kim.virtual_machines_current vm ON vm.vm_id::text=job.resource_id
			JOIN kim.vm_materialization_plan_evidence plan
			  ON plan.plan_id=vm.current_plan_id AND plan.vm_id=vm.vm_id
			 AND plan.vm_generation=vm.vm_generation AND plan.host_id=vm.host_id
			JOIN kim.image_revision_evidence image
			  ON image.image_id=plan.image_id AND image.image_revision=plan.image_revision
			 AND image.image_format='RAW'
			JOIN kim.volume_backend_bindings_current binding
			  ON binding.binding_id=plan.root_binding_id
			 AND binding.binding_generation=plan.root_binding_generation
			WHERE job.job_id=$1 AND command.command_id=$2
			  AND job.resource_type='VM_IMAGE_MATERIALIZATION' AND job.resource_id=$3
			  AND command.command_type=$4 AND command.schema_version=$5
			  AND command.target_resource_id='vm:' || $3
			  AND plan.plan_id=$6
			  AND command.payload->>'domain_uuid'=vm.vm_id::text
			  AND (command.payload->>'materialization_generation')::bigint=(plan.plan_payload->>'materialization_generation')::bigint
			  AND command.payload->>'image_id'=plan.image_id
			  AND (command.payload->>'image_revision')::bigint=plan.image_revision
			  AND command.payload->>'image_checksum'=image.observed_checksum
			  AND (command.payload->>'image_size_bytes')::bigint=image.size_bytes
			  AND command.payload->>'volume_id'=plan.root_volume_id
			  AND command.payload->>'vg_uuid'=binding.vg_uuid
			  AND command.payload->>'lv_uuid'=binding.lv_uuid
			  AND command.payload->>'backend_resource_key'=binding.backend_resource_key
			  AND command.payload->>'desired_state'=$7
		`, request.JobID, request.CommandID, request.VMID, localimage.CommandType,
			localimage.SchemaVersion, request.PlanID, localimage.StateRealized).Scan(&existingHost, &existingPayloadDigest)
		if err == nil {
			var currentPlan string
			if scanErr := tx.QueryRow(ctx, `SELECT current_plan_id FROM kim.virtual_machines_current WHERE vm_id=$1`, request.VMID).Scan(&currentPlan); scanErr != nil || currentPlan != request.PlanID {
				return ErrVMMaterializationConflict
			}
			decision = VMImageMaterializationDecision{VMID: request.VMID, HostID: existingHost, PlanID: request.PlanID, JobID: request.JobID, CommandID: request.CommandID, PayloadDigest: existingPayloadDigest}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var hostID, imageID, checksum, volumeID, bindingID, vgUUID, lvUUID, resourceKey, planDigest string
		var vmGeneration, materializationGeneration, imageRevision, imageSize, bindingGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT vm.host_id,vm.vm_generation,(plan.plan_payload->>'materialization_generation')::bigint,plan.image_id,plan.image_revision,
			       image.observed_checksum,image.size_bytes,plan.root_volume_id,
			       plan.root_binding_id,plan.root_binding_generation,binding.vg_uuid,
			       binding.lv_uuid,binding.backend_resource_key,plan.plan_digest
			FROM kim.virtual_machines_current vm
			JOIN kim.vm_materialization_plan_evidence plan
			  ON plan.plan_id=vm.current_plan_id AND plan.vm_id=vm.vm_id
			 AND plan.vm_generation=vm.vm_generation AND plan.host_id=vm.host_id
			JOIN kim.vm_materialization_readiness_current readiness
			  ON readiness.vm_id=vm.vm_id AND readiness.vm_generation=vm.vm_generation
			 AND readiness.plan_id=plan.plan_id AND readiness.domain_state='DEFINED'
			 AND readiness.storage_state='BOUND' AND readiness.image_state='PENDING'
			JOIN kim.image_revision_evidence image
			  ON image.image_id=plan.image_id AND image.image_revision=plan.image_revision
			 AND image.validation_state='VERIFIED' AND image.image_format='RAW'
			JOIN kim.images_current current_image
			  ON current_image.image_id=image.image_id AND current_image.image_revision=image.image_revision
			 AND current_image.lifecycle_state='ACTIVE'
			JOIN kim.volume_backend_bindings_current binding
			  ON binding.binding_id=plan.root_binding_id
			 AND binding.binding_generation=plan.root_binding_generation
			 AND binding.volume_id=plan.root_volume_id AND binding.host_id=vm.host_id
			 AND binding.binding_state='BOUND'
			JOIN kim.volume_attachment_claims claim
			  ON claim.attachment_id=plan.root_attachment_id
			 AND claim.attachment_generation=plan.root_attachment_generation
			 AND claim.volume_id=plan.root_volume_id AND claim.host_id=vm.host_id
			 AND claim.claim_state='RESERVED'
			WHERE vm.vm_id=$1 AND vm.current_plan_id=$2 AND vm.lifecycle_state='DEFINED'
			FOR UPDATE OF vm,readiness,binding,claim
		`, request.VMID, request.PlanID).Scan(&hostID, &vmGeneration, &materializationGeneration, &imageID,
			&imageRevision, &checksum, &imageSize, &volumeID, &bindingID,
			&bindingGeneration, &vgUUID, &lvUUID, &resourceKey, &planDigest); err != nil {
			return ErrVMMaterializationConflict
		}
		// A verified relocation copy already contains mutable guest state. A
		// normal base-Image realization would overwrite it; the preserved-root
		// readiness consumer must be used instead.
		var preservedRoot bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_materialization_relocation_authority_evidence r JOIN kim.local_lvm_relocation_copy_terminal_evidence t ON t.terminal_evidence_id=r.local_lvm_copy_terminal_evidence_id AND t.terminal_digest=r.local_lvm_copy_terminal_digest AND t.terminal_state='VERIFIED' WHERE r.vm_id=$1::uuid AND r.destination_admission_id=(SELECT placement_admission_id FROM kim.virtual_machines_current WHERE vm_id=$1::uuid) AND r.destination_host_id=$2)`, request.VMID, hostID).Scan(&preservedRoot); err != nil || preservedRoot {
			return ErrVMMaterializationConflict
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"domain_uuid": request.VMID, "materialization_generation": materializationGeneration,
			"image_id": imageID, "image_revision": imageRevision,
			"image_checksum": checksum, "image_size_bytes": imageSize,
			"volume_id": volumeID, "vg_uuid": vgUUID, "lv_uuid": lvUUID,
			"backend_resource_key": resourceKey, "desired_state": localimage.StateRealized,
		})
		if err != nil {
			return err
		}
		payloadDigest := digestBytes(payload)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs (job_id,resource_type,resource_id,desired_revision,job_state) VALUES ($1,'VM_IMAGE_MATERIALIZATION',$2,$3,'DISPATCHABLE')`, request.JobID, request.VMID, vmGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.execution_commands (command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, request.CommandID, request.JobID, hostID, localimage.CommandType, localimage.SchemaVersion, "vm:"+request.VMID, payload, payloadDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current (command_id,command_state) VALUES ($1,'PENDING')`, request.CommandID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, request.JobID, request.CommandID); err != nil {
			return err
		}
		if err := appendJobEventTx(ctx, tx, request.JobID, "COMMAND_CREATED", map[string]any{"command_id": request.CommandID, "payload_digest": payloadDigest, "plan_digest": planDigest}); err != nil {
			return err
		}
		decision = VMImageMaterializationDecision{VMID: request.VMID, HostID: hostID, PlanID: request.PlanID, JobID: request.JobID, CommandID: request.CommandID, PayloadDigest: payloadDigest}
		return nil
	})
	return decision, err
}

type VMImageRealizationObservation struct {
	EvidenceID, VMID, PlanID, PlanDigest, HostID     string
	ImageID, ExpectedDigest, ObservedDigest          string
	VolumeID, BindingID, VGUUID, LVUUID              string
	BackendResourceKey, CommandID, VerificationID    string
	ObservationDigest, VerifierDigest, EvidenceState string
	VMGeneration, ImageRevision, ImageSizeBytes      uint64
	BindingGeneration, ObservationGeneration         uint64
	AttemptIndex                                     uint32
	HolderOpen, ContentIdentityMatches               bool
}

// AcceptVMImageRealizationObservation advances only Image readiness after a
// standard target-LV bounded content read-back. Network remains independent.
func AcceptVMImageRealizationObservation(ctx context.Context, db TxBeginner, value VMImageRealizationObservation) error {
	if value.EvidenceID == "" || !vmUUIDPattern.MatchString(value.VMID) || value.PlanID == "" || len(value.PlanDigest) != 64 || value.HostID == "" || value.ImageID == "" || value.ImageRevision == 0 || len(value.ExpectedDigest) != 64 || len(value.ObservedDigest) != 64 || value.ImageSizeBytes == 0 || value.VolumeID == "" || value.BindingID == "" || value.BindingGeneration == 0 || value.VGUUID == "" || value.LVUUID == "" || value.BackendResourceKey == "" || value.CommandID == "" || value.VerificationID == "" || value.VMGeneration == 0 || value.AttemptIndex == 0 || value.ObservationGeneration == 0 || len(value.ObservationDigest) != 64 || len(value.VerifierDigest) != 64 || value.EvidenceState != "MATCHED" || value.HolderOpen || !value.ContentIdentityMatches || value.ExpectedDigest != value.ObservedDigest {
		return ErrVMMaterializationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, value.HostID); err != nil {
			return err
		}
		var acceptedDigest string
		// Bind every evidence field explicitly rather than trusting the
		// transport payload as current resource authority.
		if err := tx.QueryRow(ctx, `
			SELECT image.observed_checksum
			FROM kim.virtual_machines_current vm
			JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id AND plan.vm_id=vm.vm_id AND plan.vm_generation=vm.vm_generation AND plan.host_id=vm.host_id
			JOIN kim.vm_materialization_readiness_current readiness ON readiness.vm_id=vm.vm_id AND readiness.vm_generation=vm.vm_generation AND readiness.plan_id=plan.plan_id AND readiness.domain_state='DEFINED' AND readiness.storage_state='BOUND'
			JOIN kim.image_revision_evidence image ON image.image_id=plan.image_id AND image.image_revision=plan.image_revision AND image.validation_state='VERIFIED' AND image.image_format='RAW'
			JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=plan.root_binding_id AND binding.binding_generation=plan.root_binding_generation AND binding.volume_id=plan.root_volume_id AND binding.host_id=vm.host_id AND binding.binding_state='BOUND'
			JOIN kim.volume_attachment_claims claim ON claim.attachment_id=plan.root_attachment_id AND claim.attachment_generation=plan.root_attachment_generation AND claim.volume_id=plan.root_volume_id AND claim.host_id=vm.host_id AND claim.claim_state='RESERVED'
			JOIN kim.execution_commands command ON command.command_id=$13 AND command.host_id=vm.host_id AND command.command_type=$14 AND command.schema_version=$15 AND command.target_resource_id='vm:' || vm.vm_id::text
			JOIN kim.command_verification_evidence verification ON verification.verification_id=$16 AND verification.command_id=command.command_id AND verification.attempt_index=$17 AND verification.observation_generation=$18 AND verification.observation_digest=$19 AND verification.verification_state='MATCHED' AND verification.verifier_artifact_digest=$20
			WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND plan.plan_id=$3 AND plan.plan_digest=$4 AND vm.host_id=$5
			  AND plan.image_id=$6 AND plan.image_revision=$7 AND plan.root_volume_id=$8 AND plan.root_binding_id=$9 AND plan.root_binding_generation=$10
			  AND binding.vg_uuid=$11 AND binding.lv_uuid=$12
			  AND verification.evidence_payload->>'expected_content_digest'=$21 AND verification.evidence_payload->>'observed_content_digest'=$22
			  AND verification.evidence_payload->>'domain_uuid'=vm.vm_id::text
			  AND (verification.evidence_payload->>'materialization_generation')::bigint=(plan.plan_payload->>'materialization_generation')::bigint
			  AND verification.evidence_payload->>'image_id'=plan.image_id
			  AND (verification.evidence_payload->>'image_revision')::bigint=plan.image_revision
			  AND (verification.evidence_payload->>'image_size_bytes')::bigint=$23 AND verification.evidence_payload->>'volume_id'=$8
			  AND verification.evidence_payload->>'observed_vg_uuid'=$11 AND verification.evidence_payload->>'observed_lv_uuid'=$12
			  AND verification.evidence_payload->>'backend_resource_key'=$24 AND (verification.evidence_payload->>'holder_open')::boolean=$25
			  AND (verification.evidence_payload->>'content_identity_matches')::boolean=$26
			  AND command.payload->>'domain_uuid'=vm.vm_id::text
			  AND (command.payload->>'materialization_generation')::bigint=(plan.plan_payload->>'materialization_generation')::bigint
			  AND command.payload->>'image_id'=plan.image_id
			  AND (command.payload->>'image_revision')::bigint=plan.image_revision
			  AND command.payload->>'image_checksum'=image.observed_checksum
			  AND (command.payload->>'image_size_bytes')::bigint=image.size_bytes
			  AND command.payload->>'volume_id'=plan.root_volume_id
			  AND command.payload->>'vg_uuid'=binding.vg_uuid
			  AND command.payload->>'lv_uuid'=binding.lv_uuid
			  AND command.payload->>'backend_resource_key'=binding.backend_resource_key
			  AND command.payload->>'desired_state'='REALIZED'
			FOR UPDATE OF vm,readiness,binding,claim
		`, value.VMID, value.VMGeneration, value.PlanID, value.PlanDigest, value.HostID,
			value.ImageID, value.ImageRevision, value.VolumeID, value.BindingID,
			value.BindingGeneration, value.VGUUID, value.LVUUID, value.CommandID,
			localimage.CommandType, localimage.SchemaVersion, value.VerificationID,
			value.AttemptIndex, value.ObservationGeneration, value.ObservationDigest,
			value.VerifierDigest, value.ExpectedDigest, value.ObservedDigest,
			value.ImageSizeBytes, value.BackendResourceKey, value.HolderOpen,
			value.ContentIdentityMatches).Scan(&acceptedDigest); err != nil || acceptedDigest != value.ExpectedDigest {
			return ErrVMMaterializationConflict
		}
		var currentEvidence *string
		var currentGeneration *uint64
		if err := tx.QueryRow(ctx, `SELECT image_evidence_id,image_observation_generation FROM kim.vm_materialization_readiness_current WHERE vm_id=$1 FOR UPDATE`, value.VMID).Scan(&currentEvidence, &currentGeneration); err != nil {
			return err
		}
		if currentGeneration != nil && (value.ObservationGeneration < *currentGeneration || (value.ObservationGeneration == *currentGeneration && (currentEvidence == nil || *currentEvidence != value.EvidenceID))) {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.vm_image_realization_evidence (evidence_id,vm_id,vm_generation,plan_id,plan_digest,host_id,image_id,image_revision,expected_content_digest,observed_content_digest,image_size_bytes,volume_id,binding_id,binding_generation,observed_vg_uuid,observed_lv_uuid,backend_resource_key,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,holder_open,content_identity_matches,evidence_state)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
			ON CONFLICT (evidence_id) DO NOTHING
		`, value.EvidenceID, value.VMID, value.VMGeneration, value.PlanID, value.PlanDigest, value.HostID, value.ImageID, value.ImageRevision, value.ExpectedDigest, value.ObservedDigest, value.ImageSizeBytes, value.VolumeID, value.BindingID, value.BindingGeneration, value.VGUUID, value.LVUUID, value.BackendResourceKey, value.CommandID, value.AttemptIndex, value.VerificationID, value.ObservationGeneration, value.ObservationDigest, value.VerifierDigest, value.HolderOpen, value.ContentIdentityMatches, value.EvidenceState); err != nil {
			return err
		}
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kim.vm_image_realization_evidence WHERE evidence_id=$1 AND observation_digest=$2 AND observed_content_digest=$3 AND command_id=$4 AND verification_id=$5)`, value.EvidenceID, value.ObservationDigest, value.ObservedDigest, value.CommandID, value.VerificationID).Scan(&matches); err != nil || !matches {
			return ErrVMMaterializationConflict
		}
		_, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET image_state='REALIZED',image_observation_generation=$2,image_evidence_id=$3,boot_readiness='BLOCKED',blocking_reasons=ARRAY['network_pending'],updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$4 AND (image_observation_generation IS NULL OR image_observation_generation < $2 OR (image_observation_generation=$2 AND image_evidence_id=$3))`, value.VMID, value.ObservationGeneration, value.EvidenceID, value.VMGeneration)
		return err
	})
}
