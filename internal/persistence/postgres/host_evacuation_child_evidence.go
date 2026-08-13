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
		if phase != "SOURCE_QUIESCED" || currentAuthority != sourceAuthority || childPlanGeneration == 0 || pciCount != 0 || destinationPCICount != 0 || networkCount != destinationNetworkCount || storageCount != destinationStorageCount || storageCount > 1 {
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
		networkBindingCount := 0
		sourceNetworkState, destinationNetworkState := "NOT_REQUIRED", "NOT_REQUIRED"
		var sourceNetworkDigest, destinationNetworkDigest *string
		var sourceNetworkDigestValue, destinationNetworkDigestValue string
		if networkCount > 0 {
			count, sourceDigest, destinationDigest, err := bindHostEvacuationNetworkEvidenceTx(ctx, tx, claim.ChildOperationID, verificationID, destinationAdmissionID, vmID, vmGeneration, planID, destinationHost)
			if err != nil || count != networkCount {
				return ErrHostEvacuationBlocked
			}
			networkBindingCount = count
			sourceNetworkState, destinationNetworkState = "RETIRED", "CURRENT"
			sourceNetworkDigestValue, destinationNetworkDigestValue = sourceDigest, destinationDigest
			sourceNetworkDigest, destinationNetworkDigest = &sourceNetworkDigestValue, &destinationNetworkDigestValue
		}
		bindingDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%d/%s/%s/%s/%s/%s/%d/%d/%d", claim.ChildOperationID, vmID, vmGeneration, destinationAdmissionID, destinationHost, planDigest, definitionID, imageID, readyGeneration, powerGeneration, networkBindingCount)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_destination_evidence_binding(destination_binding_id,child_operation_id,child_generation,child_plan_generation,vm_id,vm_generation,destination_host_id,destination_admission_id,destination_plan_id,destination_plan_digest,definition_evidence_id,image_evidence_id,power_evidence_id,materialization_observation_generation,power_observation_generation,binding_digest) VALUES($1,$2,1,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, destinationBindingID, claim.ChildOperationID, childPlanGeneration, vmID, vmGeneration, destinationHost, destinationAdmissionID, planID, planDigest, definitionID, imageID, powerID, readyGeneration, powerGeneration, bindingDigest); err != nil {
			return err
		}
		verificationDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%d/%d/%d/%s/%s", claim.ChildOperationID, quiescenceID, destinationBindingID, bindingDigest, readyGeneration, powerGeneration, networkBindingCount, sourceNetworkDigestValue, destinationNetworkDigestValue)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_child_verification_evidence(verification_id,child_operation_id,child_generation,child_plan_generation,vm_id,vm_generation,source_host_id,destination_host_id,destination_admission_id,quiescence_evidence_id,destination_binding_id,source_storage_state,source_network_state,source_pci_state,destination_power_state,destination_storage_state,destination_network_state,destination_pci_state,source_ownership_state,source_host_authority_generation,destination_materialization_generation,destination_power_observation_generation,verification_state,verification_digest,network_binding_count,source_network_evidence_set_digest,destination_network_evidence_set_digest) VALUES($1,$2,1,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,'NOT_REQUIRED','RUNNING',$13,$14,'NOT_REQUIRED','RETIRED',$15,$16,$17,'VERIFIED',$18,$19,$20,$21)`, verificationID, claim.ChildOperationID, childPlanGeneration, vmID, vmGeneration, sourceHost, destinationHost, destinationAdmissionID, quiescenceID, destinationBindingID, sourceStorageState, sourceNetworkState, destinationStorageState, destinationNetworkState, sourceAuthority, readyGeneration, powerGeneration, verificationDigest, networkBindingCount, sourceNetworkDigest, destinationNetworkDigest); err != nil {
			return err
		}
		out = HostEvacuationChildVerification{VerificationID: verificationID, ChildOperationID: claim.ChildOperationID, DestinationAdmissionID: destinationAdmissionID, DestinationHostID: destinationHost, DestinationBindingID: destinationBindingID, VerificationDigest: verificationDigest, ChildPlanGeneration: childPlanGeneration}
		_ = workloadID
		return nil
	})
	return out, err
}

func bindHostEvacuationNetworkEvidenceTx(ctx context.Context, tx pgx.Tx, childID, verificationID, destinationAdmissionID, vmID string, vmGeneration uint64, planID, destinationHost string) (int, string, string, error) {
	rows, err := tx.Query(ctx, `SELECT p.network_id,p.subnet_id,p.port_id,mac.mac_address::text,host(ip.ip_address),
		h.source_host_id,h.destination_host_id,h.source_port_generation,h.source_binding_generation,h.destination_port_generation,h.destination_binding_generation,
		re.evidence_id,re.evidence_digest,q.evidence_id,q.evidence_digest,h.handoff_id,h.handoff_digest,
		pre.evidence_id,preobs.observation_digest,ovn.nb_observation_id,nb.observation_digest,ovn.sb_observation_id,sb.observation_digest,dp.evidence_id,dpobs.observation_digest
		FROM kim.network_ports_current p
		JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=p.placement_admission_id AND b.host_id=$5
		JOIN kim.port_binding_handoff_evidence h ON h.port_id=p.port_id AND h.destination_admission_id=p.placement_admission_id AND h.destination_host_id=b.host_id AND h.destination_port_generation=p.port_generation AND h.destination_binding_generation=b.binding_generation
		JOIN kim.port_binding_handoffs_current hc ON hc.port_id=p.port_id AND hc.handoff_id=h.handoff_id AND hc.destination_binding_generation=b.binding_generation AND hc.handoff_state IN ('DESTINATION_REALIZED','VERIFIED')
		JOIN kim.network_port_source_quiescence_evidence q ON q.evidence_id=h.source_quiescence_evidence_id AND q.evidence_digest=h.source_quiescence_evidence_digest AND q.quiescence_state='QUIESCED'
		JOIN kim.network_port_binding_retirement_evidence re ON re.evidence_id=q.retirement_evidence_id AND re.port_id=h.port_id AND re.port_generation=h.source_port_generation AND re.binding_generation=h.source_binding_generation AND re.source_host_id=h.source_host_id AND re.retirement_state='VERIFIED'
		JOIN kim.network_port_binding_retirements_current rc ON rc.port_id=h.port_id AND rc.port_generation=h.source_port_generation AND rc.binding_generation=h.source_binding_generation AND rc.terminal_evidence_id=re.evidence_id AND rc.retirement_state='VERIFIED'
		JOIN kim.placement_admission_decisions source_admission ON source_admission.admission_id=h.source_admission_id
		JOIN kim.placement_admission_decisions destination_admission ON destination_admission.admission_id=h.destination_admission_id
		JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' AND ip.claim_state IN ('RESERVED','ACTIVE')
		JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE')
		JOIN kim.vm_network_port_realizations_current pre ON pre.vm_id=$2::uuid AND pre.vm_generation=$3 AND pre.port_id=p.port_id AND pre.port_generation=p.port_generation AND pre.binding_generation=b.binding_generation AND pre.preboot_state='REALIZED'
		JOIN kim.vm_network_port_realization_evidence preobs ON preobs.evidence_id=pre.evidence_id AND preobs.plan_id=$4 AND preobs.host_id=$5 AND preobs.preboot_state='REALIZED'
		JOIN kim.network_ovn_state_current ovn ON ovn.port_id=p.port_id AND ovn.port_generation=p.port_generation AND ovn.binding_generation=b.binding_generation AND ovn.nb_state='MATCHED' AND ovn.sb_state='MATCHED' AND ovn.layer_status='SB_REALIZED'
		JOIN kim.ovn_nb_observation_evidence nb ON nb.observation_id=ovn.nb_observation_id AND nb.nb_state='MATCHED'
		JOIN kim.ovn_sb_observation_evidence sb ON sb.observation_id=ovn.sb_observation_id AND sb.sb_state='MATCHED' AND sb.expected_chassis_matches
		JOIN kim.vm_port_dataplane_state_current dpc ON dpc.vm_id=$2::uuid AND dpc.vm_generation=$3 AND dpc.port_id=p.port_id AND dpc.port_generation=p.port_generation AND dpc.binding_generation=b.binding_generation AND dpc.convergence_state='CONVERGED'
		JOIN kim.vm_port_dataplane_observation_evidence dp ON dp.evidence_id=dpc.evidence_id AND dp.plan_id=$4 AND dp.host_id=$5 AND dp.convergence_state='CONVERGED'
		JOIN kim.vm_port_dataplane_observation_evidence dpobs ON dpobs.evidence_id=dp.evidence_id
		WHERE p.placement_admission_id=$1
		AND EXISTS(SELECT 1 FROM jsonb_array_elements(source_admission.network_requirements) required WHERE required->>'PortID'=p.port_id AND required->>'NetworkID'=p.network_id AND required->>'SubnetID'=p.subnet_id AND required->>'MACAddress'=mac.mac_address::text AND required->>'IPAddress'=host(ip.ip_address))
		AND EXISTS(SELECT 1 FROM jsonb_array_elements(destination_admission.network_requirements) required WHERE required->>'PortID'=p.port_id AND required->>'NetworkID'=p.network_id AND required->>'SubnetID'=p.subnet_id AND required->>'MACAddress'=mac.mac_address::text AND required->>'IPAddress'=host(ip.ip_address))
		ORDER BY p.port_id`, destinationAdmissionID, vmID, vmGeneration, planID, destinationHost)
	if err != nil {
		return 0, "", "", err
	}
	defer rows.Close()
	type evidence struct {
		networkID, subnetID, portID, mac, ip, sourceHost, destinationHost                                      string
		retirementID, retirementDigest, quiescenceID, quiescenceDigest, handoffID, handoffDigest               string
		prebootID, prebootDigest, nbID, nbDigest, sbID, sbDigest, dataplaneID, dataplaneDigest                 string
		sourcePortGeneration, sourceBindingGeneration, destinationPortGeneration, destinationBindingGeneration uint64
	}
	var items []evidence
	for rows.Next() {
		var item evidence
		if err := rows.Scan(&item.networkID, &item.subnetID, &item.portID, &item.mac, &item.ip, &item.sourceHost, &item.destinationHost, &item.sourcePortGeneration, &item.sourceBindingGeneration, &item.destinationPortGeneration, &item.destinationBindingGeneration, &item.retirementID, &item.retirementDigest, &item.quiescenceID, &item.quiescenceDigest, &item.handoffID, &item.handoffDigest, &item.prebootID, &item.prebootDigest, &item.nbID, &item.nbDigest, &item.sbID, &item.sbDigest, &item.dataplaneID, &item.dataplaneDigest); err != nil {
			return 0, "", "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil || len(items) == 0 {
		return 0, "", "", ErrHostEvacuationBlocked
	}
	var sourceSet, destinationSet string
	for _, item := range items {
		sourceDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%s/%d/%d", item.portID, item.mac, item.ip, item.retirementDigest, item.quiescenceDigest, item.sourcePortGeneration, item.sourceBindingGeneration)))
		destinationDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%s/%s/%s/%d/%d", item.portID, item.mac, item.ip, item.handoffDigest, item.prebootDigest, item.nbDigest, item.sbDigest+item.dataplaneDigest, item.destinationPortGeneration, item.destinationBindingGeneration)))
		setDigest := digestReleaseBytes([]byte(sourceDigest + "/" + destinationDigest))
		bindingID := verificationID + ":network:" + item.portID
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_child_network_evidence_binding(network_binding_id,verification_id,child_operation_id,child_generation,vm_id,vm_generation,network_id,subnet_id,port_id,mac_address,ip_address,source_host_id,destination_host_id,source_port_generation,source_binding_generation,destination_port_generation,destination_binding_generation,source_retirement_evidence_id,source_quiescence_evidence_id,handoff_id,destination_realization_evidence_id,destination_nb_observation_id,destination_sb_observation_id,destination_dataplane_evidence_id,source_evidence_digest,destination_evidence_digest,evidence_set_digest) SELECT $1,$2,$3,child_generation,$4::uuid,$5,$6,$7,$8,$9::macaddr,$10::inet,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26 FROM kim.host_evacuation_workload_evidence WHERE child_operation_id=$3`, bindingID, verificationID, childID, vmID, vmGeneration, item.networkID, item.subnetID, item.portID, item.mac, item.ip, item.sourceHost, item.destinationHost, item.sourcePortGeneration, item.sourceBindingGeneration, item.destinationPortGeneration, item.destinationBindingGeneration, item.retirementID, item.quiescenceID, item.handoffID, item.prebootID, item.nbID, item.sbID, item.dataplaneID, sourceDigest, destinationDigest, setDigest); err != nil {
			return 0, "", "", err
		}
		sourceSet += item.portID + ":" + sourceDigest + ","
		destinationSet += item.portID + ":" + destinationDigest + ","
		if _, err := tx.Exec(ctx, `UPDATE kim.port_binding_handoffs_current SET handoff_state='VERIFIED',updated_at=statement_timestamp() WHERE port_id=$1 AND handoff_id=$2 AND destination_binding_generation=$3 AND handoff_state IN ('DESTINATION_REALIZED','VERIFIED')`, item.portID, item.handoffID, item.destinationBindingGeneration); err != nil {
			return 0, "", "", err
		}
	}
	return len(items), digestReleaseBytes([]byte(sourceSet)), digestReleaseBytes([]byte(destinationSet)), nil
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
		var networkBindingCount int
		if err := tx.QueryRow(ctx, `SELECT v.vm_id::text,v.vm_generation,v.source_host_id,v.destination_host_id,v.destination_admission_id,v.child_plan_generation,
			v.quiescence_evidence_id,q.quiescence_digest,v.verification_digest,v.source_storage_state,v.source_network_state,v.source_pci_state,v.destination_storage_state,v.destination_network_state,v.destination_pci_state,v.network_binding_count
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
			WHERE v.verification_id=$1 AND v.child_operation_id=$2 AND v.verification_state='VERIFIED' FOR UPDATE OF c,vm,ready,current_power`, verificationID, claim.ChildOperationID).Scan(&vmID, &vmGeneration, &sourceHost, &destinationHost, &admissionID, &childPlanGeneration, &quiescenceID, &quiescenceDigest, &verificationDigest, &sourceStorage, &sourceNetwork, &sourcePCI, &destinationStorage, &destinationNetwork, &destinationPCI, &networkBindingCount); err != nil {
			return ErrHostEvacuationStale
		}
		if networkBindingCount > 0 {
			current, err := hostEvacuationTerminalNetworkCurrentTx(ctx, tx, verificationID, vmID, vmGeneration, admissionID, destinationHost)
			if err != nil || current != networkBindingCount {
				return ErrHostEvacuationStale
			}
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

func hostEvacuationTerminalNetworkCurrentTx(ctx context.Context, tx pgx.Tx, verificationID, vmID string, vmGeneration uint64, destinationAdmissionID, destinationHost string) (int, error) {
	var current int
	err := tx.QueryRow(ctx, `SELECT count(*)
		FROM kim.host_evacuation_child_network_evidence_binding n
		JOIN kim.network_ports_current p ON p.port_id=n.port_id AND p.placement_admission_id=$4 AND p.network_id=n.network_id AND p.subnet_id=n.subnet_id AND p.port_generation=n.destination_port_generation
		JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.host_id=$5 AND b.binding_generation=n.destination_binding_generation
		JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE') AND mac.mac_address=n.mac_address
		JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' AND ip.claim_state IN ('RESERVED','ACTIVE') AND ip.ip_address=n.ip_address
		JOIN kim.port_binding_handoff_evidence h ON h.handoff_id=n.handoff_id AND h.port_id=p.port_id AND h.source_port_generation=n.source_port_generation AND h.source_binding_generation=n.source_binding_generation AND h.destination_port_generation=n.destination_port_generation AND h.destination_binding_generation=n.destination_binding_generation
		JOIN kim.port_binding_handoffs_current hc ON hc.port_id=p.port_id AND hc.handoff_id=h.handoff_id AND hc.destination_binding_generation=n.destination_binding_generation AND hc.handoff_state='VERIFIED'
		JOIN kim.network_port_source_quiescence_evidence q ON q.evidence_id=n.source_quiescence_evidence_id AND q.retirement_evidence_id=n.source_retirement_evidence_id AND q.quiescence_state='QUIESCED'
		JOIN kim.network_port_binding_retirements_current rc ON rc.port_id=n.port_id AND rc.port_generation=n.source_port_generation AND rc.binding_generation=n.source_binding_generation AND rc.terminal_evidence_id=n.source_retirement_evidence_id AND rc.retirement_state='VERIFIED'
		JOIN kim.vm_network_port_realizations_current pre ON pre.vm_id=$2::uuid AND pre.vm_generation=$3 AND pre.port_id=p.port_id AND pre.port_generation=n.destination_port_generation AND pre.binding_generation=n.destination_binding_generation AND pre.evidence_id=n.destination_realization_evidence_id AND pre.preboot_state='REALIZED'
		JOIN kim.vm_network_port_realization_evidence pre_evidence ON pre_evidence.evidence_id=pre.evidence_id AND pre_evidence.observation_generation=pre.observation_generation AND pre_evidence.preboot_state='REALIZED'
		JOIN kim.network_ovn_state_current ovn ON ovn.port_id=p.port_id AND ovn.port_generation=n.destination_port_generation AND ovn.binding_generation=n.destination_binding_generation AND ovn.nb_observation_id=n.destination_nb_observation_id AND ovn.sb_observation_id=n.destination_sb_observation_id AND ovn.nb_state='MATCHED' AND ovn.sb_state='MATCHED' AND ovn.layer_status='SB_REALIZED'
		JOIN kim.vm_port_dataplane_state_current dp ON dp.vm_id=$2::uuid AND dp.vm_generation=$3 AND dp.port_id=p.port_id AND dp.port_generation=n.destination_port_generation AND dp.binding_generation=n.destination_binding_generation AND dp.evidence_id=n.destination_dataplane_evidence_id AND dp.convergence_state='CONVERGED'
		JOIN kim.vm_port_dataplane_observation_evidence dp_evidence ON dp_evidence.evidence_id=dp.evidence_id AND dp_evidence.observation_generation=dp.observation_generation AND dp_evidence.convergence_state='CONVERGED'
		WHERE n.verification_id=$1`, verificationID, vmID, vmGeneration, destinationAdmissionID, destinationHost).Scan(&current)
	return current, err
}
