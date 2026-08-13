package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
)

// AuthorizeZeroPortVMPowerOn is the closed zero-Port counterpart of the
// OVS/SR-IOV pre-boot gate. It proves that the accepted Admission requires no
// network Ports, atomically publishes READY, and emits the ordinary typed
// power Command. It does not claim that the Domain is RUNNING.
func AuthorizeZeroPortVMPowerOn(ctx context.Context, db TxBeginner, vmID string, vmGeneration uint64, hostID, jobID, commandID string) error {
	if !vmUUIDPattern.MatchString(vmID) || vmGeneration == 0 || hostID == "" || jobID == "" || commandID == "" {
		return ErrVMMaterializationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		var admissionID string
		var networkCount int
		var priorPowerObservation uint64
		var ready bool
		if err := tx.QueryRow(ctx, `
			SELECT vm.placement_admission_id,jsonb_array_length(admission.network_requirements),
			       ready.domain_state='DEFINED' AND ready.storage_state='BOUND' AND ready.image_state='REALIZED'
			FROM kim.virtual_machines_current vm
			JOIN kim.placement_admission_decisions admission ON admission.admission_id=vm.placement_admission_id
			JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation
			JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id AND plan.vm_id=vm.vm_id AND plan.vm_generation=vm.vm_generation
			JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=plan.root_binding_id AND binding.binding_generation=plan.root_binding_generation AND binding.host_id=vm.host_id AND binding.binding_state='BOUND'
			JOIN kim.volume_attachment_claims claim ON claim.attachment_id=plan.root_attachment_id AND claim.attachment_generation=plan.root_attachment_generation AND claim.host_id=vm.host_id AND claim.claim_state IN ('RESERVED','ACTIVE')
			WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND vm.host_id=$3
			FOR UPDATE OF vm,ready,binding,claim
		`, vmID, vmGeneration, hostID).Scan(&admissionID, &networkCount, &ready); err != nil || admissionID == "" || networkCount != 0 || !ready {
			return ErrVMMaterializationConflict
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT observation_generation FROM kim.vm_power_state_current WHERE vm_id=$1 AND vm_generation=$2),0)`, vmID, vmGeneration).Scan(&priorPowerObservation); err != nil {
			return ErrVMMaterializationConflict
		}
		setDigest := digestBytes([]byte("[]"))
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET network_state='REALIZED',network_observation_generation=1,network_evidence_set_digest=$2,boot_readiness='READY',blocking_reasons='{}',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$3`, vmID, setDigest, vmGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrVMMaterializationConflict
		}
		return createTypedVMPowerCommandTx(ctx, tx, vmID, vmGeneration, priorPowerObservation+1, hostID, libvirtdomain.StateRunning, jobID, commandID)
	})
}

// AuthorizeVMPowerOff emits the ordinary closed SHUTOFF Command only for a
// current RUNNING/MATCHED incarnation on an ARMED Host. Backend success is not
// a SHUTOFF observation; the ordinary Result projector must still read it back.
func AuthorizeVMPowerOff(ctx context.Context, db TxBeginner, vmID string, vmGeneration uint64, hostID, jobID, commandID string) error {
	if !vmUUIDPattern.MatchString(vmID) || vmGeneration == 0 || hostID == "" || jobID == "" || commandID == "" {
		return ErrVMMaterializationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		var current bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.virtual_machines_current vm JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id AND power.vm_generation=vm.vm_generation WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND vm.host_id=$3 AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED')`, vmID, vmGeneration, hostID).Scan(&current); err != nil || !current {
			return ErrVMMaterializationConflict
		}
		var nextObservationGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT observation_generation+1 FROM kim.vm_power_state_current WHERE vm_id=$1 AND vm_generation=$2`, vmID, vmGeneration).Scan(&nextObservationGeneration); err != nil {
			return ErrVMMaterializationConflict
		}
		return createTypedVMPowerCommandTx(ctx, tx, vmID, vmGeneration, nextObservationGeneration, hostID, libvirtdomain.StateShutoff, jobID, commandID)
	})
}

func createTypedVMPowerCommandTx(ctx context.Context, tx pgx.Tx, vmID string, vmGeneration, observationGeneration uint64, hostID, desiredState, jobID, commandID string) error {
	payload := []byte(fmt.Sprintf(`{"desired_state":%q,"observation_generation":%d}`, desiredState, observationGeneration))
	payloadDigest := digestBytes(payload)
	if _, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET desired_power_state=$3,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$2 AND host_id=$4`, vmID, vmGeneration, desiredState, hostID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VIRTUAL_MACHINE_POWER',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, jobID, vmID, vmGeneration); err != nil {
		return err
	}
	var acceptedResource, acceptedType string
	var acceptedRevision uint64
	if err := tx.QueryRow(ctx, `SELECT resource_id,resource_type,desired_revision FROM kim.execution_jobs WHERE job_id=$1`, jobID).Scan(&acceptedResource, &acceptedType, &acceptedRevision); err != nil || acceptedResource != vmID || acceptedType != "VIRTUAL_MACHINE_POWER" || acceptedRevision != vmGeneration {
		return ErrVMMaterializationConflict
	}
	tag, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(command_id) DO NOTHING`, commandID, jobID, hostID, libvirtdomain.CommandType, libvirtdomain.SchemaVersion, "vm:"+vmID, payload, payloadDigest)
	if err != nil {
		return err
	}
	var acceptedJob, acceptedHost, acceptedTypeName, acceptedSchema, acceptedTarget, acceptedDigest string
	if err := tx.QueryRow(ctx, `SELECT job_id,host_id,command_type,schema_version,target_resource_id,payload_digest FROM kim.execution_commands WHERE command_id=$1`, commandID).Scan(&acceptedJob, &acceptedHost, &acceptedTypeName, &acceptedSchema, &acceptedTarget, &acceptedDigest); err != nil || acceptedJob != jobID || acceptedHost != hostID || acceptedTypeName != libvirtdomain.CommandType || acceptedSchema != libvirtdomain.SchemaVersion || acceptedTarget != "vm:"+vmID || acceptedDigest != payloadDigest {
		return ErrVMMaterializationConflict
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($1,'PENDING')`, commandID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, jobID, commandID); err != nil {
		return err
	}
	if err := appendJobEventTx(ctx, tx, jobID, "COMMAND_CREATED", map[string]any{"command_id": commandID, "payload_digest": payloadDigest}); err != nil {
		return err
	}
	return nil
}
