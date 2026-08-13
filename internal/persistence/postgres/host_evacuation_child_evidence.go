package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EvaluateHostEvacuationChildEvidence is a pure database verifier: identifiers
// select candidate evidence, while every positive state is derived by joins.
func EvaluateHostEvacuationChildEvidence(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, verificationID, destinationBindingID, destinationAdmissionID string) (HostEvacuationChildVerification, error) {
	var out HostEvacuationChildVerification
	if verificationID == "" || destinationBindingID == "" || destinationAdmissionID == "" {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var existing HostEvacuationChildVerification
		if err := tx.QueryRow(ctx, `SELECT verification_id,child_operation_id,destination_admission_id,destination_host_id,destination_binding_id,verification_digest,child_plan_generation FROM kim.host_evacuation_child_verification_evidence WHERE verification_id=$1`, verificationID).Scan(&existing.VerificationID, &existing.ChildOperationID, &existing.DestinationAdmissionID, &existing.DestinationHostID, &existing.DestinationBindingID, &existing.VerificationDigest, &existing.ChildPlanGeneration); err == nil {
			if existing.ChildOperationID != claim.ChildOperationID || existing.DestinationAdmissionID != destinationAdmissionID || existing.DestinationBindingID != destinationBindingID {
				return ErrHostEvacuationConflict
			}
			out = existing
			return nil
		} else if err != pgx.ErrNoRows {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var phase, vmID, workloadID, sourceHost, destinationHost, planID, planDigest string
		var quiescenceID, definitionID, imageID, powerID string
		var vmGeneration, sourceAuthority, currentAuthority, readyGeneration, powerGeneration, childPlanGeneration uint64
		var storageCount, networkCount, pciCount, destinationStorageCount, destinationNetworkCount, destinationPCICount int
		if err := tx.QueryRow(ctx, `SELECT c.phase,e.vm_id::text,e.vm_generation,e.workload_id,e.source_host_id,
			a.host_id,plan.plan_id,plan.plan_digest,q.quiescence_evidence_id,
			ready.definition_evidence_id,ready.image_evidence_id,power.evidence_id,
			o.source_host_authority_generation,hoa.authority_generation,ready.observation_generation,power.observation_generation,
			jsonb_array_length(e.storage_requirements),jsonb_array_length(e.network_requirements),jsonb_array_length(e.pci_requirements),
			jsonb_array_length(a.storage_requirements),jsonb_array_length(a.network_requirements),jsonb_array_length(a.pci_requirements),
			COALESCE((plan.plan_payload->>'materialization_generation')::bigint,ready.observation_generation)
			FROM kim.host_evacuation_workloads_current c
			JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.host_evacuation_operation_evidence o ON o.evacuation_operation_id=c.evacuation_operation_id
			JOIN kim.host_operation_authorities_current hoa ON hoa.host_id=e.source_host_id AND hoa.authority_state='ARMED'
			JOIN kim.host_placement_drains_current drain ON drain.source_host_id=e.source_host_id AND drain.drain_state='DRAINING'
			JOIN kim.planned_source_quiescence_evidence q ON q.child_operation_id=c.child_operation_id AND q.child_generation=c.child_generation
			JOIN kim.planned_source_quiescence_execution_evidence qe ON qe.quiescence_evidence_id=q.quiescence_evidence_id
			LEFT JOIN kim.host_evacuation_source_storage_safety_evidence storage_safety ON storage_safety.child_operation_id=c.child_operation_id AND storage_safety.child_generation=c.child_generation AND storage_safety.quiescence_evidence_id=q.quiescence_evidence_id AND storage_safety.safety_state='SAFE'
			JOIN kim.placement_admission_decisions a ON a.admission_id=$2 AND a.decision_state='ACCEPTED' AND a.workload_id=e.workload_id AND a.host_id<>e.source_host_id
			LEFT JOIN kim.vm_materialization_relocation_authority_evidence relocation ON relocation.child_operation_id=c.child_operation_id AND relocation.child_generation=c.child_generation AND relocation.destination_admission_id=a.admission_id AND relocation.source_storage_safety_evidence_id=storage_safety.safety_evidence_id
			JOIN kim.virtual_machines_current vm ON vm.vm_id=e.vm_id AND vm.vm_generation=e.vm_generation AND vm.workload_id=e.workload_id AND vm.host_id=a.host_id AND vm.placement_admission_id=a.admission_id
			JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=vm.current_plan_id AND plan.vm_id=vm.vm_id AND plan.vm_generation=vm.vm_generation AND plan.placement_admission_id=a.admission_id AND plan.host_id=a.host_id
			JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.plan_id=plan.plan_id AND ready.domain_state='DEFINED' AND ready.image_state='REALIZED' AND ready.network_state='REALIZED' AND ready.storage_state='BOUND' AND ready.boot_readiness='READY'
			JOIN kim.vm_definition_observation_evidence definition ON definition.evidence_id=ready.definition_evidence_id AND definition.vm_id=vm.vm_id AND definition.vm_generation=vm.vm_generation AND definition.plan_id=plan.plan_id AND definition.host_id=a.host_id AND definition.domain_present AND definition.domain_identity_matches AND definition.plan_identity_matches AND definition.compute_shape_matches AND definition.root_volume_identity_matches AND definition.evidence_state='MATCHED'
			JOIN kim.vm_image_realization_evidence image ON image.evidence_id=ready.image_evidence_id AND image.vm_id=vm.vm_id AND image.vm_generation=vm.vm_generation AND image.plan_id=plan.plan_id AND image.host_id=a.host_id AND image.content_identity_matches AND image.evidence_state='MATCHED'
			JOIN kim.vm_power_state_current current_power ON current_power.vm_id=vm.vm_id AND current_power.vm_generation=vm.vm_generation AND current_power.observed_power_state='RUNNING' AND current_power.convergence_state='MATCHED'
			JOIN kim.vm_power_observation_evidence power ON power.evidence_id=current_power.evidence_id AND power.vm_id=vm.vm_id AND power.vm_generation=vm.vm_generation AND power.host_id=a.host_id AND power.desired_power_state='RUNNING' AND power.observed_power_state='RUNNING'
			JOIN kim.command_verification_evidence power_verification ON power_verification.verification_id=power.verification_id AND power_verification.command_id=power.command_id AND power_verification.attempt_index=power.attempt_index AND power_verification.verification_state='MATCHED' AND power_verification.observation_generation=power.observation_generation AND power_verification.observation_digest=power.observation_digest
			WHERE c.child_operation_id=$1`, claim.ChildOperationID, destinationAdmissionID).Scan(&phase, &vmID, &vmGeneration, &workloadID, &sourceHost, &destinationHost, &planID, &planDigest, &quiescenceID, &definitionID, &imageID, &powerID, &sourceAuthority, &currentAuthority, &readyGeneration, &powerGeneration, &storageCount, &networkCount, &pciCount, &destinationStorageCount, &destinationNetworkCount, &destinationPCICount, &childPlanGeneration); err != nil {
			return ErrHostEvacuationBlocked
		}
		if phase != "SOURCE_QUIESCED" || currentAuthority != sourceAuthority || childPlanGeneration == 0 || networkCount != 0 || pciCount != 0 || destinationNetworkCount != 0 || destinationPCICount != 0 || storageCount != destinationStorageCount || storageCount > 1 {
			return ErrHostEvacuationBlocked
		}
		sourceStorageState, destinationStorageState := "NOT_REQUIRED", "NOT_REQUIRED"
		if storageCount == 1 {
			var closed bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_evacuation_source_storage_safety_evidence s JOIN kim.vm_materialization_relocation_authority_evidence r ON r.source_storage_safety_evidence_id=s.safety_evidence_id AND r.destination_admission_id=$2 WHERE s.child_operation_id=$1 AND s.safety_state='SAFE')`, claim.ChildOperationID, destinationAdmissionID).Scan(&closed); err != nil || !closed {
				return ErrHostEvacuationBlocked
			}
			sourceStorageState, destinationStorageState = "SAFE", "CURRENT"
		}
		bindingDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%s/%s/%s/%s/%s/%d/%d", claim.ChildOperationID, vmID, vmGeneration, destinationAdmissionID, destinationHost, planDigest, definitionID, imageID, readyGeneration, powerGeneration)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_destination_evidence_binding(destination_binding_id,child_operation_id,child_generation,child_plan_generation,vm_id,vm_generation,destination_host_id,destination_admission_id,destination_plan_id,destination_plan_digest,definition_evidence_id,image_evidence_id,power_evidence_id,materialization_observation_generation,power_observation_generation,binding_digest) VALUES($1,$2,1,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, destinationBindingID, claim.ChildOperationID, childPlanGeneration, vmID, vmGeneration, destinationHost, destinationAdmissionID, planID, planDigest, definitionID, imageID, powerID, readyGeneration, powerGeneration, bindingDigest); err != nil {
			return err
		}
		verificationDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%d/%d/NOT_REQUIRED", claim.ChildOperationID, quiescenceID, destinationBindingID, bindingDigest, readyGeneration, powerGeneration)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_child_verification_evidence(verification_id,child_operation_id,child_generation,child_plan_generation,vm_id,vm_generation,source_host_id,destination_host_id,destination_admission_id,quiescence_evidence_id,destination_binding_id,source_storage_state,source_network_state,source_pci_state,destination_power_state,destination_storage_state,destination_network_state,destination_pci_state,source_ownership_state,source_host_authority_generation,destination_materialization_generation,destination_power_observation_generation,verification_state,verification_digest) VALUES($1,$2,1,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,'NOT_REQUIRED','NOT_REQUIRED','RUNNING',$12,'NOT_REQUIRED','NOT_REQUIRED','RETIRED',$13,$14,$15,'VERIFIED',$16)`, verificationID, claim.ChildOperationID, childPlanGeneration, vmID, vmGeneration, sourceHost, destinationHost, destinationAdmissionID, quiescenceID, destinationBindingID, sourceStorageState, destinationStorageState, sourceAuthority, readyGeneration, powerGeneration, verificationDigest); err != nil {
			return err
		}
		out = HostEvacuationChildVerification{VerificationID: verificationID, ChildOperationID: claim.ChildOperationID, DestinationAdmissionID: destinationAdmissionID, DestinationHostID: destinationHost, DestinationBindingID: destinationBindingID, VerificationDigest: verificationDigest, ChildPlanGeneration: childPlanGeneration}
		_ = workloadID
		return nil
	})
	return out, err
}

// CompleteHostEvacuationChild consumes a prior VERIFIED row and rechecks all
// current identities and observation generations to fence terminal-time drift.
func CompleteHostEvacuationChild(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, verificationID, terminalEvidenceID string) error {
	if verificationID == "" || terminalEvidenceID == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var replayVerification, replayChild string
		if err := tx.QueryRow(ctx, `SELECT child_verification_id,child_operation_id FROM kim.host_evacuation_child_terminal_evidence WHERE terminal_evidence_id=$1`, terminalEvidenceID).Scan(&replayVerification, &replayChild); err == nil {
			if replayVerification != verificationID || replayChild != claim.ChildOperationID {
				return ErrHostEvacuationConflict
			}
			return nil
		} else if err != pgx.ErrNoRows {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var vmID, sourceHost, destinationHost, admissionID, quiescenceID, quiescenceDigest, verificationDigest string
		var sourceStorage, sourceNetwork, sourcePCI, destinationStorage, destinationNetwork, destinationPCI string
		var vmGeneration, childPlanGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT v.vm_id::text,v.vm_generation,v.source_host_id,v.destination_host_id,v.destination_admission_id,v.child_plan_generation,
			v.quiescence_evidence_id,q.quiescence_digest,v.verification_digest,v.source_storage_state,v.source_network_state,v.source_pci_state,v.destination_storage_state,v.destination_network_state,v.destination_pci_state
			FROM kim.host_evacuation_child_verification_evidence v
			JOIN kim.planned_source_quiescence_evidence q ON q.quiescence_evidence_id=v.quiescence_evidence_id
			JOIN kim.host_evacuation_workloads_current c ON c.child_operation_id=v.child_operation_id AND c.child_generation=v.child_generation AND c.phase='SOURCE_QUIESCED'
			JOIN kim.host_evacuation_operation_evidence o ON o.evacuation_operation_id=c.evacuation_operation_id
			JOIN kim.host_operation_authorities_current hoa ON hoa.host_id=v.source_host_id AND hoa.authority_state='ARMED' AND hoa.authority_generation=o.source_host_authority_generation AND hoa.authority_generation=v.source_host_authority_generation
			JOIN kim.host_placement_drains_current drain ON drain.source_host_id=v.source_host_id AND drain.drain_state='DRAINING'
			JOIN kim.host_evacuation_destination_evidence_binding b ON b.destination_binding_id=v.destination_binding_id AND b.child_operation_id=v.child_operation_id AND b.child_plan_generation=v.child_plan_generation
			JOIN kim.virtual_machines_current vm ON vm.vm_id=v.vm_id AND vm.vm_generation=v.vm_generation AND vm.host_id=v.destination_host_id AND vm.placement_admission_id=v.destination_admission_id AND vm.current_plan_id=b.destination_plan_id
			JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.plan_id=b.destination_plan_id AND ready.observation_generation=v.destination_materialization_generation AND ready.definition_evidence_id=b.definition_evidence_id AND ready.image_evidence_id=b.image_evidence_id AND ready.domain_state='DEFINED' AND ready.image_state='REALIZED' AND ready.network_state='REALIZED' AND ready.storage_state='BOUND' AND ready.boot_readiness='READY'
			JOIN kim.vm_power_state_current current_power ON current_power.vm_id=vm.vm_id AND current_power.vm_generation=vm.vm_generation AND current_power.evidence_id=b.power_evidence_id AND current_power.observation_generation=v.destination_power_observation_generation AND current_power.observed_power_state='RUNNING' AND current_power.convergence_state='MATCHED'
			WHERE v.verification_id=$1 AND v.child_operation_id=$2 AND v.verification_state='VERIFIED' FOR UPDATE OF c,vm,ready,current_power`, verificationID, claim.ChildOperationID).Scan(&vmID, &vmGeneration, &sourceHost, &destinationHost, &admissionID, &childPlanGeneration, &quiescenceID, &quiescenceDigest, &verificationDigest, &sourceStorage, &sourceNetwork, &sourcePCI, &destinationStorage, &destinationNetwork, &destinationPCI); err != nil {
			return ErrHostEvacuationStale
		}
		terminalDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%d/%s", claim.ChildOperationID, quiescenceDigest, admissionID, childPlanGeneration, verificationDigest)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_child_terminal_evidence(terminal_evidence_id,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,destination_host_id,destination_admission_id,child_plan_generation,quiescence_evidence_id,quiescence_digest,source_storage_state,source_network_state,source_pci_state,destination_power_state,destination_storage_state,destination_network_state,destination_pci_state,source_ownership_state,verification_evidence_digest,terminal_state,terminal_digest,child_verification_id,child_verification_digest) VALUES($1,$2,1,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'RUNNING',$14,$15,$16,'RETIRED',$17,'VERIFIED',$18,$19,$17)`, terminalEvidenceID, claim.ChildOperationID, vmID, vmGeneration, sourceHost, destinationHost, admissionID, childPlanGeneration, quiescenceID, quiescenceDigest, sourceStorage, sourceNetwork, sourcePCI, destinationStorage, destinationNetwork, destinationPCI, verificationDigest, terminalDigest, verificationID); err != nil {
			return err
		}
		if err := transitionHostEvacuationChildTx(ctx, tx, claim.ChildOperationID, "VERIFIED", "VERIFIED", "closed_child_evidence_verified", "TERMINAL", terminalEvidenceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_evacuation_workloads_current SET destination_host_id=$2,destination_admission_id=$3,child_plan_generation=$4,terminal_evidence_id=$5 WHERE child_operation_id=$1`, claim.ChildOperationID, destinationHost, admissionID, childPlanGeneration, terminalEvidenceID); err != nil {
			return err
		}
		if err := transitionHostEvacuationSlotTx(ctx, tx, claim.OperationID, claim.ChildOperationID, "RELEASED", "child_verified"); err != nil {
			return err
		}
		return updateHostEvacuationAggregateTx(ctx, tx, claim.OperationID)
	})
}
