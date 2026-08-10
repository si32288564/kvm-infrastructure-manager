package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
)

func projectMatchedVMPowerVerificationTx(ctx context.Context, tx pgx.Tx, verification CommandVerification) error {
	var vmID, hostID, commandType, schemaVersion string
	var vmGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT job.resource_id,job.desired_revision,command.host_id,command.command_type,command.schema_version
		FROM kim.execution_commands command
		JOIN kim.execution_jobs job USING(job_id)
		WHERE command.command_id=$1 AND job.resource_type='VIRTUAL_MACHINE_POWER'
	`, verification.CommandID).Scan(&vmID, &vmGeneration, &hostID, &commandType, &schemaVersion); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	if commandType != libvirtdomain.CommandType || schemaVersion != libvirtdomain.SchemaVersion {
		return ErrVerificationConflict
	}
	var commandPayload []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM kim.execution_commands WHERE command_id=$1`, verification.CommandID).Scan(&commandPayload); err != nil {
		return err
	}
	var desired struct {
		DesiredState string `json:"desired_state"`
	}
	if err := json.Unmarshal(commandPayload, &desired); err != nil || (desired.DesiredState != libvirtdomain.StateRunning && desired.DesiredState != libvirtdomain.StateShutoff) {
		return ErrVerificationConflict
	}
	evidence := verification.Evidence
	if readBack, ok := evidence["read_back"].(map[string]any); ok {
		evidence = readBack
	}
	domainUUID, _ := evidence["domain_uuid"].(string)
	observedState, _ := evidence["observed_state"].(string)
	observedDesired, _ := evidence["desired_state"].(string)
	if domainUUID != vmID || observedDesired != desired.DesiredState || observedState != desired.DesiredState {
		return ErrVerificationConflict
	}
	var current bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM kim.virtual_machines_current vm
		JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation
		WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND vm.host_id=$3
		  AND vm.desired_power_state=$4 AND ready.boot_readiness='READY'
	)`, vmID, vmGeneration, hostID, desired.DesiredState).Scan(&current); err != nil || !current {
		return ErrVerificationConflict
	}
	evidenceID := fmt.Sprintf("vm-power/%s/%d", verification.CommandID, verification.AttemptIndex)
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.vm_power_observation_evidence(
			evidence_id,vm_id,vm_generation,host_id,command_id,attempt_index,verification_id,
			desired_power_state,observed_power_state,observation_generation,observation_digest,verifier_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(evidence_id) DO NOTHING
	`, evidenceID, vmID, vmGeneration, hostID, verification.CommandID, verification.AttemptIndex,
		verification.VerificationID, desired.DesiredState, observedState, verification.ObservationGeneration,
		verification.ObservationDigest, verification.VerifierArtifactDigest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_power_observation_evidence
			WHERE evidence_id=$1 AND vm_id=$2 AND vm_generation=$3 AND command_id=$4 AND attempt_index=$5
			  AND verification_id=$6 AND desired_power_state=$7 AND observed_power_state=$7
			  AND observation_generation=$8 AND observation_digest=$9 AND verifier_digest=$10)`,
			evidenceID, vmID, vmGeneration, verification.CommandID, verification.AttemptIndex,
			verification.VerificationID, desired.DesiredState, verification.ObservationGeneration,
			verification.ObservationDigest, verification.VerifierArtifactDigest).Scan(&identical); err != nil || !identical {
			return ErrVerificationConflict
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.vm_power_state_current(vm_id,vm_generation,desired_power_state,observed_power_state,convergence_state,observation_generation,evidence_id)
		VALUES($1,$2,$3,$3,'MATCHED',$4,$5)
		ON CONFLICT(vm_id) DO UPDATE SET
			vm_generation=EXCLUDED.vm_generation,desired_power_state=EXCLUDED.desired_power_state,
			observed_power_state=EXCLUDED.observed_power_state,convergence_state='MATCHED',
			observation_generation=EXCLUDED.observation_generation,evidence_id=EXCLUDED.evidence_id,
			updated_at=statement_timestamp()
		WHERE kim.vm_power_state_current.vm_generation<EXCLUDED.vm_generation
		   OR (kim.vm_power_state_current.vm_generation=EXCLUDED.vm_generation
		       AND kim.vm_power_state_current.observation_generation<EXCLUDED.observation_generation)
	`, vmID, vmGeneration, desired.DesiredState, verification.ObservationGeneration, evidenceID)
	return err
}
