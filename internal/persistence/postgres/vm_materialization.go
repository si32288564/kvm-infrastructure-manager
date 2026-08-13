package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

var (
	ErrVMMaterializationConflict = errors.New("VM materialization authority conflict")
	vmUUIDPattern                = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type VMMaterializationRequest struct {
	VMID, AdmissionID, PlanID, JobID, CommandID, RelocationAuthorityID string
	MaterializationGeneration                                          uint64
}

type VMMaterializationDecision struct {
	VMID, HostID, PlanID, PlanDigest, JobID, CommandID string
}

// PrepareVMMaterialization derives an immutable closed typed Domain plan only
// from an accepted Final Admission and current resource authority. It does not
// define, start, attach, or otherwise mutate a backend resource.
func PrepareVMMaterialization(ctx context.Context, db TxBeginner, request VMMaterializationRequest) (VMMaterializationDecision, error) {
	if !vmUUIDPattern.MatchString(request.VMID) || request.AdmissionID == "" || request.PlanID == "" || request.JobID == "" || request.CommandID == "" {
		return VMMaterializationDecision{}, ErrVMMaterializationConflict
	}
	var decision VMMaterializationDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var existingVMID, existingAdmission, existingHost, existingDigest string
		var existingMaterializationGeneration uint64
		existingErr := tx.QueryRow(ctx, `
			SELECT vm_id::text, placement_admission_id, host_id, plan_digest,
			       (plan_payload->>'materialization_generation')::bigint
			FROM kim.vm_materialization_plan_evidence WHERE plan_id=$1
		`, request.PlanID).Scan(&existingVMID, &existingAdmission, &existingHost, &existingDigest, &existingMaterializationGeneration)
		if existingErr == nil {
			expectedMaterializationGeneration := request.MaterializationGeneration
			if expectedMaterializationGeneration == 0 {
				expectedMaterializationGeneration = 1
			}
			var jobResourceType, jobResourceID, commandJobID, commandHost, commandType, commandSchema, commandTarget, commandDigest string
			if err := tx.QueryRow(ctx, `
				SELECT job.resource_type,job.resource_id,command.job_id,command.host_id,
				       command.command_type,command.schema_version,command.target_resource_id,command.payload_digest
				FROM kim.execution_jobs job
				JOIN kim.execution_commands command ON command.job_id=job.job_id
				WHERE job.job_id=$1 AND command.command_id=$2
			`, request.JobID, request.CommandID).Scan(&jobResourceType, &jobResourceID,
				&commandJobID, &commandHost, &commandType, &commandSchema,
				&commandTarget, &commandDigest); err != nil || existingVMID != request.VMID ||
				existingAdmission != request.AdmissionID || jobResourceType != "VIRTUAL_MACHINE" ||
				jobResourceID != request.VMID || commandJobID != request.JobID ||
				commandHost != existingHost || commandType != "VIRTUAL_MACHINE_DEFINE" ||
				commandSchema != "kim.command.virtual-machine-define/v1" ||
				commandTarget != "vm:"+request.VMID || commandDigest != existingDigest || existingMaterializationGeneration != expectedMaterializationGeneration {
				return fmt.Errorf("existing materialization replay identity: %w", ErrVMMaterializationConflict)
			}
			decision = VMMaterializationDecision{VMID: request.VMID, HostID: existingHost,
				PlanID: request.PlanID, PlanDigest: existingDigest, JobID: request.JobID,
				CommandID: request.CommandID}
			return nil
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		var projectID, workloadID, hostID, imageID, flavorID, shapeDigest string
		var imageRevision, flavorRevision, vcpus, memoryMiB int64
		if err := tx.QueryRow(ctx, `
			SELECT admission.project_id, admission.workload_id, admission.host_id,
			       admission.image_id, admission.image_revision,
			       admission.flavor_id, admission.flavor_revision,
			       admission.flavor_shape_digest, compute.vcpus, compute.memory_mib
			FROM kim.placement_admission_decisions admission
			JOIN kim.compute_allocation_claims compute ON compute.admission_id=admission.admission_id
			JOIN kim.image_revision_evidence image
			  ON image.image_id=admission.image_id AND image.image_revision=admission.image_revision
			 AND image.validation_state='VERIFIED'
			JOIN kim.flavor_revision_evidence flavor
			  ON flavor.flavor_id=admission.flavor_id AND flavor.flavor_revision=admission.flavor_revision
			 AND flavor.shape_digest=admission.flavor_shape_digest
			WHERE admission.admission_id=$1 AND admission.decision_state='ACCEPTED'
			  AND compute.claim_state='RESERVED'
		`, request.AdmissionID).Scan(&projectID, &workloadID, &hostID, &imageID,
			&imageRevision, &flavorID, &flavorRevision, &shapeDigest, &vcpus, &memoryMiB); err != nil {
			return fmt.Errorf("admission authority: %w", ErrVMMaterializationConflict)
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		var bootVolumes int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.volumes_current WHERE placement_admission_id=$1 AND bootable`, request.AdmissionID).Scan(&bootVolumes); err != nil || bootVolumes != 1 {
			return fmt.Errorf("boot volume authority: %w", ErrVMMaterializationConflict)
		}
		var allocationID, volumeID, bindingID, vgUUID, lvUUID, resourceKey, attachmentID string
		var bindingGeneration, attachmentGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT compute.allocation_id, volume.volume_id, binding.binding_id,
			       binding.binding_generation, binding.vg_uuid, binding.lv_uuid,
			       binding.backend_resource_key, attachment.attachment_id,
			       attachment.attachment_generation
			FROM kim.compute_allocation_claims compute
			JOIN kim.volumes_current volume
			  ON volume.placement_admission_id=compute.admission_id AND volume.bootable
			JOIN kim.volume_backend_bindings_current binding
			  ON binding.volume_id=volume.volume_id AND binding.host_id=compute.host_id
			 AND binding.binding_state='BOUND'
			JOIN kim.volume_attachments_current attachment
			  ON attachment.volume_id=volume.volume_id AND attachment.placement_admission_id=compute.admission_id
			JOIN kim.volume_attachment_claims claim
			  ON claim.attachment_id=attachment.attachment_id
			 AND claim.attachment_generation=attachment.attachment_generation
			 AND claim.host_id=compute.host_id
			WHERE compute.admission_id=$1 AND compute.claim_state='RESERVED'
			  AND volume.lifecycle_state IN ('RESERVED','CREATING','AVAILABLE')
			  AND attachment.desired_state='RESERVED' AND claim.claim_state='RESERVED'
			FOR UPDATE OF compute, volume, binding, attachment, claim
		`, request.AdmissionID).Scan(&allocationID, &volumeID, &bindingID,
			&bindingGeneration, &vgUUID, &lvUUID, &resourceKey, &attachmentID,
			&attachmentGeneration); err != nil {
			return fmt.Errorf("root binding authority: %w", ErrVMMaterializationConflict)
		}
		var vmGeneration int64
		relocation := false
		err := tx.QueryRow(ctx, `SELECT placement_admission_id,project_id,workload_id,host_id,vm_generation FROM kim.virtual_machines_current WHERE vm_id=$1 FOR UPDATE`, request.VMID).Scan(&existingAdmission, &existingVMID, &existingDigest, &existingHost, &vmGeneration)
		if errors.Is(err, pgx.ErrNoRows) {
			vmGeneration = 1
		} else if err != nil || existingVMID != projectID || existingDigest != workloadID || vmGeneration < 1 {
			return fmt.Errorf("current VM identity: %w", ErrVMMaterializationConflict)
		} else if existingAdmission != request.AdmissionID || existingHost != hostID {
			if request.RelocationAuthorityID == "" || request.MaterializationGeneration == 0 {
				return fmt.Errorf("relocation identity missing: %w", ErrVMMaterializationConflict)
			}
			var authorized bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1
				FROM kim.vm_materialization_relocation_authority_evidence r
				JOIN kim.host_evacuation_workloads_current c ON c.child_operation_id=r.child_operation_id AND c.child_generation=r.child_generation AND c.phase='SOURCE_QUIESCED'
				JOIN kim.local_lvm_relocation_copy_terminal_evidence terminal ON terminal.terminal_evidence_id=r.local_lvm_copy_terminal_evidence_id AND terminal.terminal_digest=r.local_lvm_copy_terminal_digest AND terminal.terminal_state='VERIFIED'
				JOIN kim.local_lvm_relocation_copy_operations_current copy_current ON copy_current.copy_operation_id=terminal.copy_operation_id AND copy_current.copy_generation=terminal.copy_generation AND copy_current.terminal_evidence_id=terminal.terminal_evidence_id AND copy_current.operation_state='VERIFIED'
				JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=terminal.destination_binding_id AND binding.binding_generation=terminal.destination_binding_generation AND binding.lv_uuid=terminal.destination_lv_uuid AND binding.host_id=r.destination_host_id AND binding.binding_state='BOUND'
				JOIN kim.volumes_current volume ON volume.volume_id=binding.volume_id AND volume.placement_admission_id=r.destination_admission_id AND volume.bootable
				WHERE r.relocation_authority_id=$1 AND r.vm_id=$2 AND r.vm_generation=$3 AND r.source_admission_id=$4 AND r.source_host_id=$5 AND r.destination_admission_id=$6 AND r.destination_host_id=$7 AND r.destination_materialization_generation=$8)`, request.RelocationAuthorityID, request.VMID, vmGeneration, existingAdmission, existingHost, request.AdmissionID, hostID, request.MaterializationGeneration).Scan(&authorized); err != nil || !authorized {
				return fmt.Errorf("relocation authority mismatch: %w", ErrVMMaterializationConflict)
			}
			relocation = true
		}
		materializationGeneration := request.MaterializationGeneration
		if materializationGeneration == 0 {
			materializationGeneration = 1
		}
		payload := map[string]any{
			"domain_uuid": request.VMID, "materialization_generation": materializationGeneration,
			"vcpus": vcpus, "memory_mib": memoryMiB, "desired_state": "DEFINED",
			"image_id": imageID, "image_revision": imageRevision,
			"image_materialization_state": "PENDING",
			"network_realization_state":   "PENDING",
			"root_volume": map[string]any{
				"volume_id": volumeID, "vg_uuid": vgUUID, "lv_uuid": lvUUID,
				"backend_resource_key": resourceKey,
			},
		}
		planPayload, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		planDigest := digestBytes(planPayload)
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.virtual_machines_current (
				vm_id, placement_admission_id, project_id, workload_id, host_id,
				vm_generation, desired_power_state, lifecycle_state
			) VALUES ($1,$2,$3,$4,$5,$6,'SHUTOFF','MATERIALIZATION_PENDING')
			ON CONFLICT (vm_id) DO NOTHING
		`, request.VMID, request.AdmissionID, projectID, workloadID, hostID, vmGeneration); err != nil {
			return err
		}
		if relocation {
			if tag, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET placement_admission_id=$2,host_id=$3,desired_power_state='SHUTOFF',lifecycle_state='MATERIALIZATION_PENDING',current_plan_id=NULL,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$4`, request.VMID, request.AdmissionID, hostID, vmGeneration); err != nil || tag.RowsAffected() != 1 {
				return fmt.Errorf("relocation projection update: %w", ErrVMMaterializationConflict)
			}
		}
		var acceptedVMAdmission, acceptedVMProject, acceptedVMWorkload, acceptedVMHost string
		var acceptedVMGeneration int64
		if err := tx.QueryRow(ctx, `SELECT placement_admission_id,project_id,workload_id,host_id,vm_generation FROM kim.virtual_machines_current WHERE vm_id=$1 FOR UPDATE`, request.VMID).Scan(&acceptedVMAdmission, &acceptedVMProject, &acceptedVMWorkload, &acceptedVMHost, &acceptedVMGeneration); err != nil || acceptedVMAdmission != request.AdmissionID || acceptedVMProject != projectID || acceptedVMWorkload != workloadID || acceptedVMHost != hostID || acceptedVMGeneration != vmGeneration {
			return fmt.Errorf("accepted VM projection: %w", ErrVMMaterializationConflict)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.vm_materialization_plan_evidence (
				plan_id, vm_id, vm_generation, placement_admission_id, host_id,
				image_id, image_revision, flavor_id, flavor_revision, flavor_shape_digest,
				compute_allocation_id, root_volume_id, root_binding_id,
				root_binding_generation, root_attachment_id, root_attachment_generation,
				plan_payload, plan_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT (plan_id) DO NOTHING
		`, request.PlanID, request.VMID, vmGeneration, request.AdmissionID, hostID, imageID,
			imageRevision, flavorID, flavorRevision, shapeDigest, allocationID,
			volumeID, bindingID, bindingGeneration, attachmentID,
			attachmentGeneration, planPayload, planDigest); err != nil {
			return err
		}
		var acceptedVMID, acceptedAdmission, acceptedHost, acceptedDigest string
		if err := tx.QueryRow(ctx, `
			SELECT vm_id::text, placement_admission_id, host_id, plan_digest
			FROM kim.vm_materialization_plan_evidence WHERE plan_id=$1
		`, request.PlanID).Scan(&acceptedVMID, &acceptedAdmission, &acceptedHost, &acceptedDigest); err != nil || acceptedVMID != request.VMID || acceptedAdmission != request.AdmissionID || acceptedHost != hostID || acceptedDigest != planDigest {
			return fmt.Errorf("accepted materialization plan: %w", ErrVMMaterializationConflict)
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET current_plan_id=$2 WHERE vm_id=$1 AND vm_generation=$3`, request.VMID, request.PlanID, vmGeneration); err != nil {
			return err
		} else if tag.RowsAffected() != 1 {
			return fmt.Errorf("current plan projection: %w", ErrVMMaterializationConflict)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.execution_jobs (job_id, resource_type, resource_id, desired_revision, job_state)
			VALUES ($1,'VIRTUAL_MACHINE',$2,$3,'DISPATCHABLE') ON CONFLICT (job_id) DO NOTHING
		`, request.JobID, request.VMID, vmGeneration); err != nil {
			return err
		}
		var acceptedResourceType, acceptedResourceID string
		var acceptedDesiredRevision int64
		if err := tx.QueryRow(ctx, `SELECT resource_type,resource_id,desired_revision FROM kim.execution_jobs WHERE job_id=$1`, request.JobID).Scan(&acceptedResourceType, &acceptedResourceID, &acceptedDesiredRevision); err != nil || acceptedResourceType != "VIRTUAL_MACHINE" || acceptedResourceID != request.VMID || acceptedDesiredRevision != vmGeneration {
			return fmt.Errorf("execution job identity: %w", ErrVMMaterializationConflict)
		}
		commandDigest := digestBytes(planPayload)
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.execution_commands (
				command_id, job_id, host_id, command_type, schema_version,
				target_resource_id, payload, payload_digest
			) VALUES ($1,$2,$3,'VIRTUAL_MACHINE_DEFINE','kim.command.virtual-machine-define/v1',$4,$5,$6)
			ON CONFLICT (command_id) DO NOTHING
		`, request.CommandID, request.JobID, hostID, "vm:"+request.VMID, planPayload, commandDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current (command_id, command_state) VALUES ($1,'PENDING')`, request.CommandID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, request.JobID, request.CommandID); err != nil {
				return err
			}
			if err := appendJobEventTx(ctx, tx, request.JobID, "COMMAND_CREATED", map[string]any{"command_id": request.CommandID, "payload_digest": commandDigest}); err != nil {
				return err
			}
		} else {
			var acceptedJob, acceptedHost, acceptedType, acceptedSchema, acceptedTarget, acceptedCommandDigest string
			if err := tx.QueryRow(ctx, `SELECT job_id,host_id,command_type,schema_version,target_resource_id,payload_digest FROM kim.execution_commands WHERE command_id=$1`, request.CommandID).Scan(&acceptedJob, &acceptedHost, &acceptedType, &acceptedSchema, &acceptedTarget, &acceptedCommandDigest); err != nil || acceptedJob != request.JobID || acceptedHost != hostID || acceptedType != "VIRTUAL_MACHINE_DEFINE" || acceptedSchema != "kim.command.virtual-machine-define/v1" || acceptedTarget != "vm:"+request.VMID || acceptedCommandDigest != commandDigest {
				return fmt.Errorf("existing define command identity: %w", ErrVMMaterializationConflict)
			}
		}
		decision = VMMaterializationDecision{VMID: request.VMID, HostID: hostID, PlanID: request.PlanID, PlanDigest: planDigest, JobID: request.JobID, CommandID: request.CommandID}
		return nil
	})
	return decision, err
}
