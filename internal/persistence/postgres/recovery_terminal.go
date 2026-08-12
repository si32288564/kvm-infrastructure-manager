package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
)

type RecoveryMaterializationRequest struct {
	RecoveryOperationID, MaterializationID, VMID, VMPlanID, DefineJobID, DefineCommandID string
}

type RecoveryMaterialization struct {
	RecoveryOperationID, MaterializationID, DestinationAdmissionID, DestinationHostID string
	VMID, WorkloadID, VMPlanID, VMPlanDigest, RootVolumeID, RootBindingID             string
	RootAttachmentID, MaterializationDigest                                           string
	VMGeneration, MaterializationGeneration, RootBindingGeneration                    uint64
	RootAttachmentGeneration                                                          uint64
}

type RecoveryPowerAuthority struct {
	PowerAuthorityID, RecoveryOperationID, MaterializationID, DangerousStepEvaluationID string
	DestinationAdmissionID, DestinationHostID, VMID, PowerJobID, PowerCommandID         string
	AuthorityDigest                                                                     string
	VMGeneration, ReadinessObservationGeneration, OperationStateGeneration              uint64
	BudgetStateGeneration                                                               uint64
}

type RecoveryVerification struct {
	VerificationID, RecoveryOperationID, MaterializationID, DestinationAdmissionID string
	DestinationHostID, VMID, PowerEvidenceID, PowerObservationDigest               string
	StorageAttachmentEvidenceID, NetworkEvidenceSetDigest, PCIRequirementsDigest   string
	FencingProofID, FencingProofDigest, FencingUsability                           string
	StorageSafetyProofID, StorageSafetyProofDigest, StorageUsability               string
	BudgetClaimID, ResultState, ReasonCode, VerifierVersion, VerifierDigest        string
	VerificationDigest                                                             string
	OperationGeneration, OperationStateGeneration, VMGeneration                    uint64
	PowerObservationGeneration, StorageAttachmentGeneration                        uint64
	StorageObservationGeneration, NetworkObservationGeneration                     uint64
	BudgetStateGeneration                                                          uint64
}

type RecoveryTerminalDecision struct {
	TerminalDecisionID, RecoveryOperationID, VerificationID, VerificationDigest string
	FailureEpochID, BudgetClaimID, DecisionState, DecidedBy, DecisionDigest     string
	OperationStateGeneration, EpochTransitionGeneration, BudgetStateGeneration  uint64
}

// loadRecoverySourceFencingProofUsabilityTx is stricter than accepting a
// historical SHUTOFF fact: the exact proof observation must still be the
// newest observation for the source Host, while the Host authority generation
// must remain at the exact immutable FENCED event. Destination observations
// never satisfy or invalidate this source-only predicate.
func loadRecoverySourceFencingProofUsabilityTx(ctx context.Context, tx pgx.Tx, epoch FailureEpoch) (proofID, proofDigest, usability string, err error) {
	var sourceHost, eventDigest, powerEvidenceID string
	var hostGeneration, eventSequence, powerGeneration uint64
	err = tx.QueryRow(ctx, `SELECT p.proof_id,p.proof_digest,o.source_host_id,o.host_authority_generation,o.host_authority_event_sequence,o.host_authority_event_digest,o.vm_power_evidence_id,o.vm_power_observation_generation FROM kim.failure_fencing_proof_evidence p JOIN kim.failure_fencing_evaluation_evidence e ON e.evaluation_id=p.evaluation_id JOIN kim.source_execution_fencing_observation_evidence o ON o.evidence_id=e.fencing_evidence_id AND o.evidence_generation=e.latest_fencing_evidence_generation AND o.evidence_digest=e.fencing_evidence_digest JOIN kim.failure_epoch_transition_evidence t ON t.failure_epoch_id=p.failure_epoch_id AND t.to_state='FENCED' AND t.fencing_proof_id=p.proof_id WHERE p.failure_epoch_id=$1`, epoch.FailureEpochID).Scan(&proofID, &proofDigest, &sourceHost, &hostGeneration, &eventSequence, &eventDigest, &powerEvidenceID, &powerGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "MISSING", nil
	}
	if err != nil {
		return "", "", "UNKNOWN", err
	}
	if sourceHost != epoch.SourceHostID {
		return proofID, proofDigest, "STALE", nil
	}
	var currentHostGeneration uint64
	var currentHostState, currentEventDigest string
	if err := tx.QueryRow(ctx, `SELECT authority_generation,authority_state FROM kim.host_operation_authorities_current WHERE host_id=$1 FOR SHARE`, sourceHost).Scan(&currentHostGeneration, &currentHostState); err != nil {
		return proofID, proofDigest, "UNKNOWN", nil
	}
	if err := tx.QueryRow(ctx, `SELECT event_payload_digest FROM kim.host_operation_authority_events WHERE host_id=$1 AND authority_generation=$2 AND event_sequence=$3 AND event_type='FENCED'`, sourceHost, hostGeneration, eventSequence).Scan(&currentEventDigest); err != nil || currentEventDigest != eventDigest || currentHostGeneration != hostGeneration || currentHostState != "FENCED" {
		return proofID, proofDigest, "STALE", nil
	}
	var observedState string
	var observedGeneration, latestSourceGeneration uint64
	if err := tx.QueryRow(ctx, `SELECT observed_power_state,observation_generation FROM kim.vm_power_observation_evidence WHERE evidence_id=$1 AND host_id=$2`, powerEvidenceID, sourceHost).Scan(&observedState, &observedGeneration); err != nil {
		return proofID, proofDigest, "UNKNOWN", nil
	}
	if err := tx.QueryRow(ctx, `SELECT max(observation_generation) FROM kim.vm_power_observation_evidence WHERE vm_id=(SELECT vm_id FROM kim.vm_power_observation_evidence WHERE evidence_id=$1) AND host_id=$2`, powerEvidenceID, sourceHost).Scan(&latestSourceGeneration); err != nil {
		return proofID, proofDigest, "UNKNOWN", nil
	}
	if observedState != "SHUTOFF" || observedGeneration != powerGeneration || latestSourceGeneration != powerGeneration {
		return proofID, proofDigest, "STALE", nil
	}
	return proofID, proofDigest, "USABLE", nil
}

// PrepareRecoveryMaterialization switches only the rebuildable current VM
// desired projection to the exact destination Admission, then invokes the
// ordinary VM materialization path. Historical source evidence is untouched.
func PrepareRecoveryMaterialization(ctx context.Context, db TxBeginner, request RecoveryMaterializationRequest) (RecoveryMaterialization, error) {
	var out RecoveryMaterialization
	if request.RecoveryOperationID == "" || request.MaterializationID == "" || !vmUUIDPattern.MatchString(request.VMID) || request.VMPlanID == "" || request.DefineJobID == "" || request.DefineCommandID == "" {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+request.RecoveryOperationID); err != nil {
			return err
		}
		var existingDigest string
		err := tx.QueryRow(ctx, `SELECT materialization_digest FROM kim.recovery_materialization_evidence WHERE materialization_id=$1`, request.MaterializationID).Scan(&existingDigest)
		if err == nil {
			if err := loadRecoveryMaterializationTx(ctx, tx, request.MaterializationID, &out); err != nil || out.RecoveryOperationID != request.RecoveryOperationID || out.VMID != request.VMID || out.VMPlanID != request.VMPlanID || out.MaterializationDigest != existingDigest {
				return ErrRecoveryOperationConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var operation RecoveryOperation
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, request.RecoveryOperationID, &operation, &plan); err != nil {
			return err
		}
		if operation.RecoveryAction != "RESTART_ON_OTHER_HOST" {
			return ErrRecoveryOperationUnsupported
		}
		if operation.LifecycleState != "RUNNING" && operation.LifecycleState != "VERIFYING" {
			return ErrRecoveryOperationBlocked
		}
		var admissionID, destinationHost string
		if err := tx.QueryRow(ctx, `SELECT admission_id,destination_host_id FROM kim.recovery_destination_admission_evidence WHERE recovery_operation_id=$1`, request.RecoveryOperationID).Scan(&admissionID, &destinationHost); err != nil || admissionID == "" || destinationHost != plan.DestinationHostID {
			return ErrRecoveryOperationStale
		}
		if err := lockHostAuthorityTx(ctx, tx, destinationHost); err != nil {
			return err
		}
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, destinationHost); err != nil {
			return ErrRecoveryOperationStale
		}
		var sourceAdmission, projectID, workloadID, sourceHost string
		var vmGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT placement_admission_id,project_id,workload_id,host_id,vm_generation FROM kim.virtual_machines_current WHERE vm_id=$1 FOR UPDATE`, request.VMID).Scan(&sourceAdmission, &projectID, &workloadID, &sourceHost, &vmGeneration); err != nil || sourceHost != operation.SourceHostID {
			return ErrRecoveryOperationStale
		}
		var admittedProject, admittedWorkload string
		if err := tx.QueryRow(ctx, `SELECT project_id,workload_id FROM kim.placement_admission_decisions WHERE admission_id=$1 AND host_id=$2 AND decision_state='ACCEPTED'`, admissionID, destinationHost).Scan(&admittedProject, &admittedWorkload); err != nil || admittedProject != projectID || admittedWorkload != workloadID {
			return ErrRecoveryOperationStale
		}
		// Current projections are rebuildable; immutable source observations and
		// the source Admission/allocation history remain intact.
		if _, err := tx.Exec(ctx, `DELETE FROM kim.vm_power_state_current WHERE vm_id=$1; DELETE FROM kim.vm_materialization_readiness_current WHERE vm_id=$1; DELETE FROM kim.vm_network_port_realizations_current WHERE vm_id=$1`, pgx.QueryExecModeSimpleProtocol, request.VMID); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET placement_admission_id=$2,host_id=$3,desired_power_state='SHUTOFF',lifecycle_state='MATERIALIZATION_PENDING',current_plan_id=NULL,updated_at=statement_timestamp() WHERE vm_id=$1 AND placement_admission_id=$4 AND host_id=$5 AND vm_generation=$6`, request.VMID, admissionID, destinationHost, sourceAdmission, sourceHost, vmGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		var materializationGeneration uint64
		// Older immutable source plans predate the explicit materialization
		// generation field. Their VM generation is the historical incarnation
		// authority; use it as the read-only compatibility value without
		// rewriting any evidence.
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(coalesce((plan_payload->>'materialization_generation')::bigint,vm_generation)),0)+1 FROM kim.vm_materialization_plan_evidence WHERE vm_id=$1`, request.VMID).Scan(&materializationGeneration); err != nil || materializationGeneration < 2 {
			return ErrRecoveryOperationStale
		}
		decision, err := PrepareVMMaterialization(ctx, scopeTxBeginner{tx}, VMMaterializationRequest{VMID: request.VMID, AdmissionID: admissionID, PlanID: request.VMPlanID, JobID: request.DefineJobID, CommandID: request.DefineCommandID, MaterializationGeneration: materializationGeneration})
		if err != nil {
			return err
		}
		var imageID, flavorID, rootVolume, rootBinding, rootAttachment string
		var imageRevision, flavorRevision, bindingGeneration, attachmentGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT image_id,image_revision,flavor_id,flavor_revision,root_volume_id,root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation FROM kim.vm_materialization_plan_evidence WHERE plan_id=$1`, request.VMPlanID).Scan(&imageID, &imageRevision, &flavorID, &flavorRevision, &rootVolume, &rootBinding, &bindingGeneration, &rootAttachment, &attachmentGeneration); err != nil {
			return err
		}
		var networkDigest, pciDigest string
		if err := tx.QueryRow(ctx, `SELECT network_requirements_digest,pci_requirements_digest FROM kim.placement_admission_decisions WHERE admission_id=$1`, admissionID).Scan(&networkDigest, &pciDigest); err != nil {
			return err
		}
		payload := map[string]any{"recovery_operation_id": request.RecoveryOperationID, "destination_admission_id": admissionID, "destination_host_id": destinationHost, "vm_id": request.VMID, "workload_id": workloadID, "vm_generation": vmGeneration, "materialization_generation": materializationGeneration, "vm_plan_id": request.VMPlanID, "vm_plan_digest": decision.PlanDigest, "image_id": imageID, "image_revision": imageRevision, "flavor_id": flavorID, "flavor_revision": flavorRevision, "root_volume_id": rootVolume, "root_binding_id": rootBinding, "root_binding_generation": bindingGeneration, "root_attachment_id": rootAttachment, "root_attachment_generation": attachmentGeneration, "network_requirements_digest": networkDigest, "pci_requirements_digest": pciDigest}
		raw, _ := json.Marshal(payload)
		digest := digestReleaseBytes(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_materialization_evidence(materialization_id,recovery_operation_id,operation_generation,destination_admission_id,destination_host_id,vm_id,workload_id,vm_generation,materialization_generation,vm_plan_id,vm_plan_digest,image_id,image_revision,flavor_id,flavor_revision,root_volume_id,root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation,network_requirements_digest,pci_requirements_digest,materialization_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, request.MaterializationID, request.RecoveryOperationID, operation.OperationGeneration, admissionID, destinationHost, request.VMID, workloadID, vmGeneration, materializationGeneration, request.VMPlanID, decision.PlanDigest, imageID, imageRevision, flavorID, flavorRevision, rootVolume, rootBinding, bindingGeneration, rootAttachment, attachmentGeneration, networkDigest, pciDigest, digest); err != nil {
			return err
		}
		out = RecoveryMaterialization{request.RecoveryOperationID, request.MaterializationID, admissionID, destinationHost, request.VMID, workloadID, request.VMPlanID, decision.PlanDigest, rootVolume, rootBinding, rootAttachment, digest, vmGeneration, materializationGeneration, bindingGeneration, attachmentGeneration}
		return nil
	})
	return out, err
}

func loadRecoveryMaterializationTx(ctx context.Context, tx pgx.Tx, id string, out *RecoveryMaterialization) error {
	return tx.QueryRow(ctx, `SELECT recovery_operation_id,materialization_id,destination_admission_id,destination_host_id,vm_id::text,workload_id,vm_plan_id,vm_plan_digest,root_volume_id,root_binding_id,root_attachment_id,materialization_digest,vm_generation,materialization_generation,root_binding_generation,root_attachment_generation FROM kim.recovery_materialization_evidence WHERE materialization_id=$1`, id).Scan(&out.RecoveryOperationID, &out.MaterializationID, &out.DestinationAdmissionID, &out.DestinationHostID, &out.VMID, &out.WorkloadID, &out.VMPlanID, &out.VMPlanDigest, &out.RootVolumeID, &out.RootBindingID, &out.RootAttachmentID, &out.MaterializationDigest, &out.VMGeneration, &out.MaterializationGeneration, &out.RootBindingGeneration, &out.RootAttachmentGeneration)
}

// MarkRecoveryNoNetworkReady is the closed zero-port path. It emits no power
// command; non-empty Network requirements must use existing OVS/SR-IOV paths.
func MarkRecoveryNoNetworkReady(ctx context.Context, db TxBeginner, operationID string) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var vmID, admissionID string
		var generation uint64
		var count int
		if err := tx.QueryRow(ctx, `SELECT m.vm_id::text,m.vm_generation,m.destination_admission_id,jsonb_array_length(a.network_requirements) FROM kim.recovery_materialization_evidence m JOIN kim.placement_admission_decisions a ON a.admission_id=m.destination_admission_id WHERE m.recovery_operation_id=$1`, operationID).Scan(&vmID, &generation, &admissionID, &count); err != nil || count != 0 {
			return ErrRecoveryOperationBlocked
		}
		// Before power-on a Local LVM root LV cannot have an open QEMU holder.
		// Require the current immutable inactive-domain read-back to prove the
		// exact root identity and the current root claim/binding instead. The
		// post-power Recovery Verification still requires ATTACHED + holder_open.
		var rootConfigured bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.recovery_materialization_evidence m JOIN kim.vm_materialization_readiness_current r ON r.vm_id=m.vm_id AND r.vm_generation=m.vm_generation AND r.plan_id=m.vm_plan_id JOIN kim.vm_definition_observation_evidence d ON d.evidence_id=r.definition_evidence_id AND d.vm_id=r.vm_id AND d.vm_generation=r.vm_generation AND d.domain_present AND d.domain_identity_matches AND d.plan_identity_matches AND d.compute_shape_matches AND d.root_volume_identity_matches AND d.evidence_state='MATCHED' JOIN kim.volume_backend_bindings_current b ON b.binding_id=m.root_binding_id AND b.binding_generation=m.root_binding_generation AND b.volume_id=m.root_volume_id AND b.host_id=m.destination_host_id AND b.binding_state='BOUND' JOIN kim.volume_attachment_claims c ON c.attachment_id=m.root_attachment_id AND c.attachment_generation=m.root_attachment_generation AND c.volume_id=m.root_volume_id AND c.host_id=m.destination_host_id AND c.claim_state IN ('RESERVED','ACTIVE') WHERE m.recovery_operation_id=$1)`, operationID).Scan(&rootConfigured); err != nil || !rootConfigured {
			return ErrRecoveryOperationBlocked
		}
		setDigest := digestBytes([]byte("[]"))
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET network_state='REALIZED',network_observation_generation=1,network_evidence_set_digest=$2,boot_readiness='READY',blocking_reasons='{}',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$3 AND domain_state='DEFINED' AND storage_state='BOUND' AND image_state='REALIZED'`, vmID, setDigest, generation); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationBlocked
		}
		return nil
	})
}

func AuthorizeRecoveryPowerOn(ctx context.Context, db TxBeginner, authorityID, operationID, evaluationID, jobID, commandID string) (RecoveryPowerAuthority, error) {
	var out RecoveryPowerAuthority
	if authorityID == "" || operationID == "" || evaluationID == "" || jobID == "" || commandID == "" {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+operationID); err != nil {
			return err
		}
		var existing string
		if err := tx.QueryRow(ctx, `SELECT authority_digest FROM kim.recovery_power_authority_evidence WHERE power_authority_id=$1`, authorityID).Scan(&existing); err == nil {
			if err := tx.QueryRow(ctx, `SELECT power_authority_id,recovery_operation_id,materialization_id,dangerous_step_evaluation_id,destination_admission_id,destination_host_id,vm_id::text,vm_generation,readiness_observation_generation,operation_state_generation,budget_state_generation,power_job_id,power_command_id,authority_digest FROM kim.recovery_power_authority_evidence WHERE power_authority_id=$1`, authorityID).Scan(&out.PowerAuthorityID, &out.RecoveryOperationID, &out.MaterializationID, &out.DangerousStepEvaluationID, &out.DestinationAdmissionID, &out.DestinationHostID, &out.VMID, &out.VMGeneration, &out.ReadinessObservationGeneration, &out.OperationStateGeneration, &out.BudgetStateGeneration, &out.PowerJobID, &out.PowerCommandID, &out.AuthorityDigest); err != nil || out.RecoveryOperationID != operationID || out.DangerousStepEvaluationID != evaluationID || out.PowerJobID != jobID || out.PowerCommandID != commandID || out.AuthorityDigest != existing {
				return ErrRecoveryOperationConflict
			}
			return nil
		}
		var op RecoveryOperation
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, operationID, &op, &plan); err != nil {
			return err
		}
		if op.LifecycleState != "RUNNING" && op.LifecycleState != "VERIFYING" {
			return ErrRecoveryOperationBlocked
		}
		var eval RecoveryDangerousStepEvaluation
		if err := tx.QueryRow(ctx, `SELECT evaluation_id,recovery_operation_id,operation_state_generation,fencing_proof_id,fencing_proof_digest,fencing_usability,storage_safety_proof_id,storage_safety_proof_digest,storage_usability,budget_claim_id,budget_state_generation,destination_admission_id,result_state,reason_code,evaluator_digest,evaluation_digest FROM kim.recovery_dangerous_step_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&eval.EvaluationID, &eval.RecoveryOperationID, &eval.OperationStateGeneration, &eval.FencingProofID, &eval.FencingProofDigest, &eval.FencingUsability, &eval.StorageSafetyProofID, &eval.StorageSafetyProofDigest, &eval.StorageUsability, &eval.BudgetClaimID, &eval.BudgetStateGeneration, &eval.DestinationAdmissionID, &eval.ResultState, &eval.ReasonCode, &eval.EvaluatorDigest, &eval.EvaluationDigest); err != nil || eval.RecoveryOperationID != operationID || eval.ResultState != "AUTHORIZED" {
			return fmt.Errorf("%w: dangerous-step evidence", ErrRecoveryOperationStale)
		}
		epoch, err := loadFailureEpochTx(ctx, tx, op.FailureEpochID)
		if err != nil {
			return err
		}
		fid, fdig, fu, err := loadRecoverySourceFencingProofUsabilityTx(ctx, tx, epoch)
		if err != nil || fu != "USABLE" || fid != eval.FencingProofID || fdig != eval.FencingProofDigest {
			return fmt.Errorf("%w: source fencing proof", ErrRecoveryOperationStale)
		}
		sid, sdig, su, err := loadStorageProofUsabilityTx(ctx, tx, epoch)
		if err != nil || su != "USABLE" || sid != eval.StorageSafetyProofID || sdig != eval.StorageSafetyProofDigest {
			return fmt.Errorf("%w: source storage proof", ErrRecoveryOperationStale)
		}
		var materializationID, admissionID, hostID, vmID string
		var vmGeneration, readyGeneration uint64
		var readinessIdentity, readinessDigest string
		if err := tx.QueryRow(ctx, `SELECT m.materialization_id,m.destination_admission_id,m.destination_host_id,m.vm_id::text,m.vm_generation,r.observation_generation,concat_ws('/',r.plan_id,r.observation_generation,r.definition_evidence_id,r.domain_state,r.image_state,r.network_state,r.storage_state,r.boot_readiness,coalesce(r.network_evidence_set_digest,'')) FROM kim.recovery_materialization_evidence m JOIN kim.virtual_machines_current vm ON vm.vm_id=m.vm_id AND vm.vm_generation=m.vm_generation AND vm.placement_admission_id=m.destination_admission_id AND vm.host_id=m.destination_host_id JOIN kim.vm_materialization_readiness_current r ON r.vm_id=vm.vm_id AND r.vm_generation=vm.vm_generation AND r.plan_id=m.vm_plan_id AND r.boot_readiness='READY' WHERE m.recovery_operation_id=$1 FOR UPDATE OF vm,r`, operationID).Scan(&materializationID, &admissionID, &hostID, &vmID, &vmGeneration, &readyGeneration, &readinessIdentity); err != nil {
			return fmt.Errorf("%w: destination readiness: %v", ErrRecoveryOperationStale, err)
		}
		readinessDigest = digestReleaseBytes([]byte(readinessIdentity))
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, hostID); err != nil {
			return fmt.Errorf("%w: destination Host authority", ErrRecoveryOperationStale)
		}
		var claimState string
		var budgetGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT claim_state,state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$1 FOR UPDATE`, op.RecoveryBudgetClaimID).Scan(&claimState, &budgetGeneration); err != nil || claimState != "CONSUMED" || budgetGeneration != 2 {
			return fmt.Errorf("%w: Recovery Budget", ErrRecoveryOperationStale)
		}
		payload := []byte(`{"desired_state":"RUNNING"}`)
		powerDigest := digestBytes(payload)
		if _, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET desired_power_state='RUNNING',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$2`, vmID, vmGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VIRTUAL_MACHINE_POWER',$2,$3,'DISPATCHABLE')`, jobID, vmID, vmGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, commandID, jobID, hostID, libvirtdomain.CommandType, libvirtdomain.SchemaVersion, "vm:"+vmID, payload, powerDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($2,'PENDING'); UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, pgx.QueryExecModeSimpleProtocol, jobID, commandID); err != nil {
			return err
		}
		authorityRaw, _ := json.Marshal(map[string]any{"operation_id": operationID, "materialization_id": materializationID, "evaluation_id": evaluationID, "evaluation_digest": eval.EvaluationDigest, "admission_id": admissionID, "host_id": hostID, "vm_id": vmID, "vm_generation": vmGeneration, "readiness_generation": readyGeneration, "readiness_digest": readinessDigest, "fencing_proof": fdig, "storage_proof": sdig, "budget_generation": budgetGeneration, "power_job_id": jobID, "power_command_id": commandID})
		authorityDigest := digestReleaseBytes(authorityRaw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_power_authority_evidence(power_authority_id,recovery_operation_id,materialization_id,dangerous_step_evaluation_id,dangerous_step_evaluation_digest,operation_state_generation,destination_admission_id,destination_host_id,vm_id,vm_generation,readiness_observation_generation,readiness_digest,fencing_proof_id,fencing_proof_digest,storage_safety_proof_id,storage_safety_proof_digest,budget_claim_id,budget_state_generation,power_job_id,power_command_id,authority_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, authorityID, operationID, materializationID, evaluationID, eval.EvaluationDigest, op.StateGeneration, admissionID, hostID, vmID, vmGeneration, readyGeneration, readinessDigest, fid, fdig, sid, sdig, op.RecoveryBudgetClaimID, budgetGeneration, jobID, commandID, authorityDigest); err != nil {
			return err
		}
		if err := appendJobEventTx(ctx, tx, jobID, "COMMAND_CREATED", map[string]any{"command_id": commandID, "payload_digest": powerDigest, "recovery_power_authority_id": authorityID}); err != nil {
			return err
		}
		out = RecoveryPowerAuthority{authorityID, operationID, materializationID, evaluationID, admissionID, hostID, vmID, jobID, commandID, authorityDigest, vmGeneration, readyGeneration, op.StateGeneration, budgetGeneration}
		return nil
	})
	return out, err
}

func RefreshRecoveryPowerExecution(ctx context.Context, db TxBeginner, operationID, verificationID string) (RecoveryOperation, error) {
	var out RecoveryOperation
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+operationID); err != nil {
			return err
		}
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, operationID, &out, &plan); err != nil {
			return err
		}
		var commandID, state string
		if err := tx.QueryRow(ctx, `SELECT p.power_command_id,c.command_state FROM kim.recovery_power_authority_evidence p JOIN kim.execution_commands_current c ON c.command_id=p.power_command_id WHERE p.recovery_operation_id=$1`, operationID).Scan(&commandID, &state); err != nil {
			return err
		}
		target, reason, cause := "", "", commandID
		if state == "UNKNOWN" {
			target, reason = "UNKNOWN", "destination_power_outcome_unknown_read_back_required"
		} else if verificationID != "" && state == "SUCCEEDED" {
			var matched bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.command_verification_evidence WHERE verification_id=$1 AND command_id=$2 AND verification_state='MATCHED')`, verificationID, commandID).Scan(&matched); err != nil || !matched {
				return ErrRecoveryOperationStale
			}
			target, reason, cause = "VERIFYING", "destination_power_read_back_matched_recovery_verification_required", verificationID
		} else {
			return ErrRecoveryOperationBlocked
		}
		if out.LifecycleState == target {
			return nil
		}
		if (target == "UNKNOWN" && (out.LifecycleState != "RUNNING" && out.LifecycleState != "VERIFYING")) || (target == "VERIFYING" && out.LifecycleState != "UNKNOWN") {
			return ErrRecoveryOperationConflict
		}
		generation := out.StateGeneration + 1
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%s/%s", operationID, generation, out.LifecycleState, target, cause)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_operation_transition_evidence(recovery_operation_id,state_generation,from_state,to_state,reason_code,cause_type,cause_id,transition_digest) VALUES($1,$2,$3,$4,$5,'EXECUTION_OBSERVATION',$6,$7)`, operationID, generation, out.LifecycleState, target, reason, cause, digest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.recovery_operations_current SET lifecycle_state=$2,state_generation=$3,updated_at=statement_timestamp() WHERE recovery_operation_id=$1 AND lifecycle_state=$4 AND state_generation=$5`, operationID, target, generation, out.LifecycleState, out.StateGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		out.LifecycleState, out.StateGeneration = target, generation
		return nil
	})
	return out, err
}

func EvaluateRecoveryVerification(ctx context.Context, db TxBeginner, verificationID, operationID, verifierVersion, verifierDigest string) (RecoveryVerification, error) {
	var out RecoveryVerification
	if verificationID == "" || operationID == "" || verifierVersion == "" || len(verifierDigest) != 64 {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		var stored string
		if err := tx.QueryRow(ctx, `SELECT verification_digest FROM kim.recovery_verification_evidence WHERE verification_id=$1`, verificationID).Scan(&stored); err == nil {
			if err := loadRecoveryVerificationTx(ctx, tx, verificationID, &out); err != nil || out.RecoveryOperationID != operationID || out.VerifierVersion != verifierVersion || out.VerifierDigest != verifierDigest || out.VerificationDigest != stored {
				return ErrRecoveryOperationConflict
			}
			return nil
		}
		var op RecoveryOperation
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, operationID, &op, &plan); err != nil {
			return err
		}
		out.VerificationID, out.RecoveryOperationID, out.OperationGeneration, out.OperationStateGeneration, out.VerifierVersion, out.VerifierDigest = verificationID, operationID, op.OperationGeneration, op.StateGeneration, verifierVersion, verifierDigest
		var materialization RecoveryMaterialization
		if err := tx.QueryRow(ctx, `SELECT materialization_id FROM kim.recovery_materialization_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&materialization.MaterializationID); err != nil {
			out.ResultState, out.ReasonCode = "STALE_DESTINATION", "recovery_materialization_missing"
		} else if err := loadRecoveryMaterializationTx(ctx, tx, materialization.MaterializationID, &materialization); err != nil {
			return err
		}
		out.MaterializationID, out.DestinationAdmissionID, out.DestinationHostID, out.VMID, out.VMGeneration = materialization.MaterializationID, materialization.DestinationAdmissionID, materialization.DestinationHostID, materialization.VMID, materialization.VMGeneration
		epoch, err := loadFailureEpochTx(ctx, tx, op.FailureEpochID)
		if err != nil {
			return err
		}
		out.FencingProofID, out.FencingProofDigest, out.FencingUsability, err = loadRecoverySourceFencingProofUsabilityTx(ctx, tx, epoch)
		if err != nil {
			return err
		}
		out.StorageSafetyProofID, out.StorageSafetyProofDigest, out.StorageUsability, err = loadStorageProofUsabilityTx(ctx, tx, epoch)
		if err != nil {
			return err
		}
		out.BudgetClaimID = op.RecoveryBudgetClaimID
		var claimState string
		_ = tx.QueryRow(ctx, `SELECT claim_state,state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$1`, out.BudgetClaimID).Scan(&claimState, &out.BudgetStateGeneration)
		var currentAdmission, currentHost, currentPlan string
		var readyState, networkState string
		networkEvidenceCurrent := true
		if materialization.MaterializationID != "" {
			_ = tx.QueryRow(ctx, `SELECT vm.placement_admission_id,vm.host_id,coalesce(vm.current_plan_id,''),coalesce(r.boot_readiness,'UNKNOWN'),coalesce(r.network_state,'UNKNOWN'),coalesce(r.network_observation_generation,0),coalesce(r.network_evidence_set_digest,'') FROM kim.virtual_machines_current vm LEFT JOIN kim.vm_materialization_readiness_current r ON r.vm_id=vm.vm_id AND r.vm_generation=vm.vm_generation WHERE vm.vm_id=$1 AND vm.vm_generation=$2`, out.VMID, out.VMGeneration).Scan(&currentAdmission, &currentHost, &currentPlan, &readyState, &networkState, &out.NetworkObservationGeneration, &out.NetworkEvidenceSetDigest)
			var networkCount int
			_ = tx.QueryRow(ctx, `SELECT jsonb_array_length(network_requirements) FROM kim.placement_admission_decisions WHERE admission_id=$1`, out.DestinationAdmissionID).Scan(&networkCount)
			if networkCount > 0 {
				generation, digest, matched, err := currentRecoveryNetworkEvidenceSetTx(ctx, tx, operationID)
				networkEvidenceCurrent = err == nil && matched && generation == out.NetworkObservationGeneration && digest == out.NetworkEvidenceSetDigest
			}
		}
		_ = tx.QueryRow(ctx, `SELECT p.evidence_id,p.observation_generation,e.observation_digest FROM kim.vm_power_state_current p JOIN kim.vm_power_observation_evidence e ON e.evidence_id=p.evidence_id AND e.observation_generation=p.observation_generation WHERE p.vm_id=$1 AND p.vm_generation=$2 AND p.desired_power_state='RUNNING' AND p.observed_power_state='RUNNING' AND p.convergence_state='MATCHED'`, out.VMID, out.VMGeneration).Scan(&out.PowerEvidenceID, &out.PowerObservationGeneration, &out.PowerObservationDigest)
		var attachmentState string
		var holderOpen bool
		_ = tx.QueryRow(ctx, `SELECT o.evidence_id,o.attachment_generation,o.observation_generation,o.attachment_state,o.holder_open FROM kim.recovery_materialization_evidence m JOIN kim.volume_attachment_claims c ON c.attachment_id=m.root_attachment_id AND c.attachment_generation=m.root_attachment_generation AND c.claim_state='ACTIVE' JOIN kim.volume_attachment_observations_current o ON o.attachment_id=c.attachment_id AND o.attachment_generation=c.attachment_generation WHERE m.recovery_operation_id=$1`, operationID).Scan(&out.StorageAttachmentEvidenceID, &out.StorageAttachmentGeneration, &out.StorageObservationGeneration, &attachmentState, &holderOpen)
		out.PCIRequirementsDigest = digestBytes([]byte("[]"))
		var pciCount int
		_ = tx.QueryRow(ctx, `SELECT jsonb_array_length(pci_requirements) FROM kim.placement_admission_decisions WHERE admission_id=$1`, out.DestinationAdmissionID).Scan(&pciCount)
		switch {
		case op.LifecycleState != "VERIFYING":
			out.ResultState, out.ReasonCode = "STALE_OPERATION", "operation_not_current_verifying"
		case out.FencingUsability != "USABLE":
			out.ResultState, out.ReasonCode = "STALE_FENCING", "source_fencing_proof_not_current_usable"
		case out.StorageUsability != "USABLE":
			out.ResultState, out.ReasonCode = "STALE_STORAGE", "source_storage_proof_not_current_usable"
		case claimState != "CONSUMED" || out.BudgetStateGeneration != 2:
			out.ResultState, out.ReasonCode = "STALE_OPERATION", "budget_not_current_consumed"
		case currentAdmission != out.DestinationAdmissionID || currentHost != out.DestinationHostID || currentPlan != materialization.VMPlanID || readyState != "READY":
			out.ResultState, out.ReasonCode = "STALE_DESTINATION", "destination_materialization_not_current_ready"
		case out.PowerEvidenceID == "":
			out.ResultState, out.ReasonCode = "UNKNOWN", "destination_power_read_back_missing"
		case attachmentState != "ATTACHED" || !holderOpen:
			out.ResultState, out.ReasonCode = "NOT_VERIFIED", "destination_storage_attachment_not_matched"
		case networkState != "REALIZED":
			out.ResultState, out.ReasonCode = "NOT_VERIFIED", "destination_network_not_realized"
		case !networkEvidenceCurrent:
			out.ResultState, out.ReasonCode = "NOT_VERIFIED", "destination_network_evidence_set_not_current"
		case pciCount != 0:
			out.ResultState, out.ReasonCode = "NOT_VERIFIED", "pci_recovery_verification_not_qualified"
		default:
			out.ResultState, out.ReasonCode = "VERIFIED", "exact_destination_multi_domain_read_back_converged"
		}
		copy := out
		copy.VerificationDigest = ""
		raw, _ := json.Marshal(copy)
		out.VerificationDigest = digestReleaseBytes(raw)
		_, err = tx.Exec(ctx, `INSERT INTO kim.recovery_verification_evidence(verification_id,recovery_operation_id,operation_generation,operation_state_generation,materialization_id,destination_admission_id,destination_host_id,vm_id,vm_generation,power_evidence_id,power_observation_generation,power_observation_digest,storage_attachment_evidence_id,storage_attachment_generation,storage_observation_generation,network_observation_generation,network_evidence_set_digest,pci_requirements_digest,fencing_proof_id,fencing_proof_digest,fencing_usability,storage_safety_proof_id,storage_safety_proof_digest,storage_usability,budget_claim_id,budget_state_generation,result_state,reason_code,verifier_version,verifier_digest,verification_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,0),NULLIF($12,''),NULLIF($13,''),NULLIF($14,0),NULLIF($15,0),NULLIF($16,0),NULLIF($17,''),$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31)`, out.VerificationID, out.RecoveryOperationID, out.OperationGeneration, out.OperationStateGeneration, out.MaterializationID, out.DestinationAdmissionID, out.DestinationHostID, out.VMID, out.VMGeneration, out.PowerEvidenceID, out.PowerObservationGeneration, out.PowerObservationDigest, out.StorageAttachmentEvidenceID, out.StorageAttachmentGeneration, out.StorageObservationGeneration, out.NetworkObservationGeneration, out.NetworkEvidenceSetDigest, out.PCIRequirementsDigest, out.FencingProofID, out.FencingProofDigest, out.FencingUsability, out.StorageSafetyProofID, out.StorageSafetyProofDigest, out.StorageUsability, out.BudgetClaimID, out.BudgetStateGeneration, out.ResultState, out.ReasonCode, out.VerifierVersion, out.VerifierDigest, out.VerificationDigest)
		return err
	})
	return out, err
}

func loadRecoveryVerificationTx(ctx context.Context, tx pgx.Tx, id string, out *RecoveryVerification) error {
	return tx.QueryRow(ctx, `SELECT verification_id,recovery_operation_id,operation_generation,operation_state_generation,materialization_id,destination_admission_id,destination_host_id,vm_id::text,vm_generation,coalesce(power_evidence_id,''),coalesce(power_observation_generation,0),coalesce(power_observation_digest,''),coalesce(storage_attachment_evidence_id,''),coalesce(storage_attachment_generation,0),coalesce(storage_observation_generation,0),coalesce(network_observation_generation,0),coalesce(network_evidence_set_digest,''),pci_requirements_digest,fencing_proof_id,fencing_proof_digest,fencing_usability,storage_safety_proof_id,storage_safety_proof_digest,storage_usability,budget_claim_id,budget_state_generation,result_state,reason_code,verifier_version,verifier_digest,verification_digest FROM kim.recovery_verification_evidence WHERE verification_id=$1`, id).Scan(&out.VerificationID, &out.RecoveryOperationID, &out.OperationGeneration, &out.OperationStateGeneration, &out.MaterializationID, &out.DestinationAdmissionID, &out.DestinationHostID, &out.VMID, &out.VMGeneration, &out.PowerEvidenceID, &out.PowerObservationGeneration, &out.PowerObservationDigest, &out.StorageAttachmentEvidenceID, &out.StorageAttachmentGeneration, &out.StorageObservationGeneration, &out.NetworkObservationGeneration, &out.NetworkEvidenceSetDigest, &out.PCIRequirementsDigest, &out.FencingProofID, &out.FencingProofDigest, &out.FencingUsability, &out.StorageSafetyProofID, &out.StorageSafetyProofDigest, &out.StorageUsability, &out.BudgetClaimID, &out.BudgetStateGeneration, &out.ResultState, &out.ReasonCode, &out.VerifierVersion, &out.VerifierDigest, &out.VerificationDigest)
}

func CommitRecoveryTerminalDecision(ctx context.Context, db TxBeginner, decisionID, verificationID, decidedBy string) (RecoveryTerminalDecision, error) {
	var out RecoveryTerminalDecision
	if decisionID == "" || verificationID == "" || decidedBy == "" {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var existingOperation string
		if err := tx.QueryRow(ctx, `SELECT recovery_operation_id FROM kim.recovery_terminal_decision_evidence WHERE terminal_decision_id=$1`, decisionID).Scan(&existingOperation); err == nil {
			if err := tx.QueryRow(ctx, `SELECT terminal_decision_id,recovery_operation_id,verification_id,verification_digest,failure_epoch_id,budget_claim_id,decision_state,decided_by,decision_digest FROM kim.recovery_terminal_decision_evidence WHERE terminal_decision_id=$1`, decisionID).Scan(&out.TerminalDecisionID, &out.RecoveryOperationID, &out.VerificationID, &out.VerificationDigest, &out.FailureEpochID, &out.BudgetClaimID, &out.DecisionState, &out.DecidedBy, &out.DecisionDigest); err != nil || out.VerificationID != verificationID || out.DecidedBy != decidedBy {
				return ErrRecoveryOperationConflict
			}
			_ = tx.QueryRow(ctx, `SELECT state_generation FROM kim.recovery_operations_current WHERE recovery_operation_id=$1`, out.RecoveryOperationID).Scan(&out.OperationStateGeneration)
			_ = tx.QueryRow(ctx, `SELECT transition_generation FROM kim.failure_epochs_current WHERE failure_epoch_id=$1`, out.FailureEpochID).Scan(&out.EpochTransitionGeneration)
			_ = tx.QueryRow(ctx, `SELECT state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$1`, out.BudgetClaimID).Scan(&out.BudgetStateGeneration)
			return nil
		}
		var verification RecoveryVerification
		if err := loadRecoveryVerificationTx(ctx, tx, verificationID, &verification); err != nil || verification.ResultState != "VERIFIED" {
			return ErrRecoveryOperationBlocked
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+verification.RecoveryOperationID); err != nil {
			return err
		}
		// A parallel caller may have committed while this transaction waited on
		// the operation lock. Recover that exact terminal evidence rather than
		// revalidating already-transitioned current projections.
		var committedDecisionID string
		if err := tx.QueryRow(ctx, `SELECT terminal_decision_id FROM kim.recovery_terminal_decision_evidence WHERE recovery_operation_id=$1`, verification.RecoveryOperationID).Scan(&committedDecisionID); err == nil {
			if committedDecisionID != decisionID {
				return ErrRecoveryOperationConflict
			}
			if err := tx.QueryRow(ctx, `SELECT terminal_decision_id,recovery_operation_id,verification_id,verification_digest,failure_epoch_id,budget_claim_id,decision_state,decided_by,decision_digest FROM kim.recovery_terminal_decision_evidence WHERE terminal_decision_id=$1`, decisionID).Scan(&out.TerminalDecisionID, &out.RecoveryOperationID, &out.VerificationID, &out.VerificationDigest, &out.FailureEpochID, &out.BudgetClaimID, &out.DecisionState, &out.DecidedBy, &out.DecisionDigest); err != nil || out.VerificationID != verificationID || out.DecidedBy != decidedBy {
				return ErrRecoveryOperationConflict
			}
			_ = tx.QueryRow(ctx, `SELECT state_generation FROM kim.recovery_operations_current WHERE recovery_operation_id=$1`, out.RecoveryOperationID).Scan(&out.OperationStateGeneration)
			_ = tx.QueryRow(ctx, `SELECT transition_generation FROM kim.failure_epochs_current WHERE failure_epoch_id=$1`, out.FailureEpochID).Scan(&out.EpochTransitionGeneration)
			_ = tx.QueryRow(ctx, `SELECT state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$1`, out.BudgetClaimID).Scan(&out.BudgetStateGeneration)
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var op RecoveryOperation
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, verification.RecoveryOperationID, &op, &plan); err != nil {
			return err
		}
		if op.LifecycleState != "VERIFYING" || op.StateGeneration != verification.OperationStateGeneration {
			return ErrRecoveryOperationStale
		}
		epoch, err := loadFailureEpochTx(ctx, tx, op.FailureEpochID)
		if err != nil || epoch.EpochState != "FENCED" {
			return ErrRecoveryOperationStale
		}
		fid, fdig, fu, err := loadRecoverySourceFencingProofUsabilityTx(ctx, tx, epoch)
		if err != nil || fu != "USABLE" || fid != verification.FencingProofID || fdig != verification.FencingProofDigest {
			return ErrRecoveryOperationStale
		}
		sid, sdig, su, err := loadStorageProofUsabilityTx(ctx, tx, epoch)
		if err != nil || su != "USABLE" || sid != verification.StorageSafetyProofID || sdig != verification.StorageSafetyProofDigest {
			return ErrRecoveryOperationStale
		}
		var powerEvidence string
		var powerGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT evidence_id,observation_generation FROM kim.vm_power_state_current WHERE vm_id=$1 AND vm_generation=$2 AND observed_power_state='RUNNING' AND convergence_state='MATCHED' FOR UPDATE`, verification.VMID, verification.VMGeneration).Scan(&powerEvidence, &powerGeneration); err != nil || powerEvidence != verification.PowerEvidenceID || powerGeneration != verification.PowerObservationGeneration {
			return ErrRecoveryOperationStale
		}
		var attachmentEvidence string
		var attachmentGeneration, attachmentObservation uint64
		if err := tx.QueryRow(ctx, `SELECT o.evidence_id,o.attachment_generation,o.observation_generation FROM kim.recovery_materialization_evidence m JOIN kim.volume_attachment_claims c ON c.attachment_id=m.root_attachment_id AND c.attachment_generation=m.root_attachment_generation AND c.claim_state='ACTIVE' JOIN kim.volume_attachment_observations_current o ON o.attachment_id=c.attachment_id AND o.attachment_generation=c.attachment_generation AND o.attachment_state='ATTACHED' AND o.device_present AND o.holder_open WHERE m.recovery_operation_id=$1`, op.RecoveryOperationID).Scan(&attachmentEvidence, &attachmentGeneration, &attachmentObservation); err != nil || attachmentEvidence != verification.StorageAttachmentEvidenceID || attachmentGeneration != verification.StorageAttachmentGeneration || attachmentObservation != verification.StorageObservationGeneration {
			return ErrRecoveryOperationStale
		}
		var currentReadiness, currentNetworkState, currentNetworkDigest string
		var currentNetworkGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT r.boot_readiness,r.network_state,coalesce(r.network_observation_generation,0),coalesce(r.network_evidence_set_digest,'') FROM kim.vm_materialization_readiness_current r JOIN kim.recovery_materialization_evidence m ON m.vm_id=r.vm_id AND m.vm_generation=r.vm_generation AND m.vm_plan_id=r.plan_id WHERE m.recovery_operation_id=$1 FOR UPDATE OF r`, op.RecoveryOperationID).Scan(&currentReadiness, &currentNetworkState, &currentNetworkGeneration, &currentNetworkDigest); err != nil || currentReadiness != "READY" || currentNetworkState != "REALIZED" || currentNetworkGeneration != verification.NetworkObservationGeneration || currentNetworkDigest != verification.NetworkEvidenceSetDigest {
			return ErrRecoveryOperationStale
		}
		var networkCount int
		_ = tx.QueryRow(ctx, `SELECT jsonb_array_length(network_requirements) FROM kim.placement_admission_decisions WHERE admission_id=$1`, verification.DestinationAdmissionID).Scan(&networkCount)
		if networkCount > 0 {
			generation, digest, matched, err := currentRecoveryNetworkEvidenceSetTx(ctx, tx, op.RecoveryOperationID)
			if err != nil || !matched || generation != verification.NetworkObservationGeneration || digest != verification.NetworkEvidenceSetDigest {
				return ErrRecoveryOperationStale
			}
		}
		var budgetState string
		var budgetGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT claim_state,state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$1 FOR UPDATE`, op.RecoveryBudgetClaimID).Scan(&budgetState, &budgetGeneration); err != nil || budgetState != "CONSUMED" || budgetGeneration != verification.BudgetStateGeneration {
			return ErrRecoveryOperationStale
		}
		decisionDigest := digestReleaseBytes([]byte(decisionID + "/" + op.RecoveryOperationID + "/" + verificationID + "/" + verification.VerificationDigest + "/" + epoch.FailureEpochID + "/" + op.RecoveryBudgetClaimID + "/VERIFIED/" + decidedBy))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_terminal_decision_evidence(terminal_decision_id,recovery_operation_id,verification_id,verification_digest,failure_epoch_id,budget_claim_id,decision_state,decided_by,decision_digest) VALUES($1,$2,$3,$4,$5,$6,'VERIFIED',$7,$8)`, decisionID, op.RecoveryOperationID, verificationID, verification.VerificationDigest, epoch.FailureEpochID, op.RecoveryBudgetClaimID, decidedBy, decisionDigest); err != nil {
			return err
		}
		opGeneration := op.StateGeneration + 1
		opDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/VERIFYING/VERIFIED/%s", op.RecoveryOperationID, opGeneration, decisionID)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_operation_transition_evidence(recovery_operation_id,state_generation,from_state,to_state,reason_code,cause_type,cause_id,transition_digest) VALUES($1,$2,'VERIFYING','VERIFIED','exact_recovery_verification_terminally_accepted','RECOVERY_VERIFICATION',$3,$4)`, op.RecoveryOperationID, opGeneration, decisionID, opDigest); err != nil {
			return err
		}
		epochGeneration := epoch.TransitionGeneration + 1
		epochDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/FENCED/RECOVERED/%s", epoch.FailureEpochID, epochGeneration, decisionID)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_epoch_transition_evidence(failure_epoch_id,transition_generation,from_state,to_state,cause_evidence_id,confirmation_decision_id,fencing_proof_id,recovery_terminal_decision_id,transition_digest) VALUES($1,$2,'FENCED','RECOVERED',NULL,NULL,NULL,$3,$4)`, epoch.FailureEpochID, epochGeneration, decisionID, epochDigest); err != nil {
			return err
		}
		budgetNext := budgetGeneration + 1
		budgetDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/CONSUMED/RELEASED/%s", op.RecoveryBudgetClaimID, budgetNext, op.RecoveryOperationID)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_budget_claim_transition_evidence(claim_id,state_generation,from_state,to_state,recovery_operation_id,transition_digest) VALUES($1,$2,'CONSUMED','RELEASED',$3,$4)`, op.RecoveryBudgetClaimID, budgetNext, op.RecoveryOperationID, budgetDigest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.recovery_operations_current SET lifecycle_state='VERIFIED',state_generation=$2,updated_at=statement_timestamp() WHERE recovery_operation_id=$1 AND lifecycle_state='VERIFYING' AND state_generation=$3`, op.RecoveryOperationID, opGeneration, op.StateGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.failure_epochs_current SET epoch_state='RECOVERED',transition_generation=$2,updated_at=statement_timestamp() WHERE failure_epoch_id=$1 AND epoch_state='FENCED' AND transition_generation=$3`, epoch.FailureEpochID, epochGeneration, epoch.TransitionGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.recovery_budget_claims_current SET claim_state='RELEASED',state_generation=$2,updated_at=statement_timestamp() WHERE claim_id=$1 AND claim_state='CONSUMED' AND state_generation=$3`, op.RecoveryBudgetClaimID, budgetNext, budgetGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		out = RecoveryTerminalDecision{decisionID, op.RecoveryOperationID, verificationID, verification.VerificationDigest, epoch.FailureEpochID, op.RecoveryBudgetClaimID, "VERIFIED", decidedBy, decisionDigest, opGeneration, epochGeneration, budgetNext}
		return nil
	})
	return out, err
}
