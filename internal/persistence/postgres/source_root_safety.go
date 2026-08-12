package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

type SourceRootSafetyEvaluation struct {
	EvaluationID, FailureEpochID, SourceAdmissionID, SourcePlanID, SourcePlanDigest string
	VMID, SourceHostID, RootVolumeID, RootBindingID, RootLVUUID                     string
	RootAttachmentID, RootObservationEvidenceID, RootObservationDigest              string
	TargetDevice, ObservedLVUUID, RootEvidenceState                                 string
	PowerEvidenceID, PowerObservationDigest, ObservedPowerState                     string
	PowerConvergenceState, ResultState, ReasonCode                                  string
	EvaluatorVersion, EvaluatorDigest, EvaluationDigest                             string
	EvaluatedTransitionGeneration, VMGeneration, SourceMaterializationGeneration    uint64
	RootBindingGeneration, RootBindingObservationGeneration                         uint64
	RootAttachmentGeneration, RootObservationGeneration                             uint64
	PowerObservationGeneration                                                      uint64
	RootBindingState                                                                string
	DevicePresent, DeviceIdentityMatches, SourceIdentityMatches, HolderOpen         bool
}

type SourceRootSafetyProof struct {
	ProofID, FailureEpochID, EvaluationID, ProofType, ProofState                    string
	SourceAdmissionID, SourcePlanID, VMID, SourceHostID                             string
	RootVolumeID, RootBindingID, RootAttachmentID                                   string
	PowerEvidenceID, RootObservationEvidenceID, DecidedBy, ProofDigest              string
	VMGeneration, SourceMaterializationGeneration, RootBindingGeneration            uint64
	RootAttachmentGeneration, PowerObservationGeneration, RootObservationGeneration uint64
}

type SourceMaterializationRetirementDecision struct {
	RetirementDecisionID, FailureEpochID, SourceAdmissionID, SourcePlanID, SourcePlanDigest string
	VMID, SourceHostID, FencingProofID, FencingProofDigest                                  string
	RootSafetyProofID, RootSafetyProofDigest, DecisionState, DecidedBy, DecisionDigest      string
	VMGeneration, SourceMaterializationGeneration                                           uint64
}

const (
	SourceRootSafetyReadBackCommandType = "SOURCE_ROOT_SAFETY_READ_BACK"
	SourceRootSafetyReadBackSchema      = "kim.command.source-root-safety-read-back/v1"
)

// AcceptSourceRootSafetyObservation accepts only an observation-only, typed
// read-back of the exact root identity from the current source materialization
// plan. It intentionally cannot attach, detach, delete, or otherwise mutate
// vda. A configured vda on an inactive Domain may be present while holder_open
// is false; that is the safety fact this authority records.
func AcceptSourceRootSafetyObservation(ctx context.Context, db TxBeginner, observation LocalLVMAttachmentObservation) error {
	if observation.EvidenceID == "" || observation.AttachmentID == "" || observation.VolumeID == "" || observation.BindingID == "" || observation.HostID == "" || observation.DomainUUID == "" || observation.TargetDevice != "vda" || observation.ObservedLVUUID == "" || observation.CommandID == "" || observation.VerificationID == "" || observation.AttachmentGeneration < 1 || observation.BindingGeneration < 1 || observation.ObservationGeneration < 1 || observation.AttemptIndex < 1 || len(observation.ObservationDigest) != 64 || len(observation.VerifierDigest) != 64 || observation.EvidenceState != "MATCHED" || !observation.DevicePresent || !observation.DeviceIdentityMatches || !observation.SourceIdentityMatches || observation.HolderOpen || observation.ReadOnly {
		return ErrFailureSafetyConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, observation.HostID); err != nil {
			return err
		}
		var currentBindingState, currentLVUUID string
		err := tx.QueryRow(ctx, `
			SELECT binding.binding_state,binding.lv_uuid
			FROM kim.vm_materialization_plan_evidence plan
			JOIN kim.virtual_machines_current vm ON vm.vm_id=plan.vm_id AND vm.vm_generation=plan.vm_generation
			JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=plan.root_binding_id AND binding.binding_generation=plan.root_binding_generation AND binding.volume_id=plan.root_volume_id
			JOIN kim.command_verification_evidence verification ON verification.verification_id=$6 AND verification.command_id=$7 AND verification.attempt_index=$8 AND verification.observation_generation=$9 AND verification.observation_digest=$10 AND verification.verification_state='MATCHED' AND verification.verifier_artifact_digest=$11
			JOIN kim.execution_commands command ON command.command_id=verification.command_id AND command.host_id=$5 AND command.command_type=$12 AND command.schema_version=$13 AND command.target_resource_id='attachment:' || $1
			WHERE plan.root_attachment_id=$1 AND plan.root_volume_id=$2 AND plan.root_attachment_generation=$3 AND plan.root_binding_id=$4 AND plan.host_id=$5 AND plan.vm_id=$14::uuid
			  AND binding.binding_generation=$15 AND binding.lv_uuid=$16
			  AND verification.evidence_payload->>'attachment_id'=$1
			  AND verification.evidence_payload->>'volume_id'=$2
			  AND verification.evidence_payload->>'binding_id'=$4
			  AND verification.evidence_payload->>'domain_uuid'=($14::uuid)::text
			  AND verification.evidence_payload->>'target_device'='vda'
			  AND verification.evidence_payload->>'observed_lv_uuid'=$16
			  AND (verification.evidence_payload->>'device_present')::boolean=true
			  AND (verification.evidence_payload->>'device_identity_matches')::boolean=true
			  AND (verification.evidence_payload->>'source_identity_matches')::boolean=true
			  AND (verification.evidence_payload->>'holder_open')::boolean=false
			FOR UPDATE OF vm,binding
		`, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration,
			observation.BindingID, observation.HostID, observation.VerificationID,
			observation.CommandID, observation.AttemptIndex, observation.ObservationGeneration,
			observation.ObservationDigest, observation.VerifierDigest,
			SourceRootSafetyReadBackCommandType, SourceRootSafetyReadBackSchema,
			observation.DomainUUID, observation.BindingGeneration, observation.ObservedLVUUID).Scan(&currentBindingState, &currentLVUUID)
		if err != nil {
			return err
		}
		if currentBindingState != "BOUND" || currentLVUUID != observation.ObservedLVUUID {
			return ErrFailureSafetyConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_observation_evidence(evidence_id,attachment_id,volume_id,attachment_generation,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,desired_state,device_present,device_identity_matches,source_identity_matches,holder_open,read_only,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,evidence_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'vda',$9,'ATTACHED',true,true,true,false,false,$10,$11,$12,$13,$14,$15,'MATCHED') ON CONFLICT(evidence_id) DO NOTHING`, observation.EvidenceID, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration, observation.BindingID, observation.BindingGeneration, observation.HostID, observation.DomainUUID, observation.ObservedLVUUID, observation.CommandID, observation.AttemptIndex, observation.VerificationID, observation.ObservationGeneration, observation.ObservationDigest, observation.VerifierDigest); err != nil {
			return err
		}
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.volume_attachment_observation_evidence WHERE evidence_id=$1 AND attachment_id=$2 AND volume_id=$3 AND attachment_generation=$4 AND binding_id=$5 AND binding_generation=$6 AND host_id=$7 AND domain_uuid=$8::uuid AND target_device='vda' AND observed_lv_uuid=$9 AND desired_state='ATTACHED' AND device_present AND device_identity_matches AND source_identity_matches AND NOT holder_open AND NOT read_only AND command_id=$10 AND attempt_index=$11 AND verification_id=$12 AND observation_generation=$13 AND observation_digest=$14 AND verifier_digest=$15 AND evidence_state='MATCHED')`, observation.EvidenceID, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration, observation.BindingID, observation.BindingGeneration, observation.HostID, observation.DomainUUID, observation.ObservedLVUUID, observation.CommandID, observation.AttemptIndex, observation.VerificationID, observation.ObservationGeneration, observation.ObservationDigest, observation.VerifierDigest).Scan(&identical); err != nil || !identical {
			return ErrFailureSafetyConflict
		}
		var currentGeneration uint64
		err = tx.QueryRow(ctx, `SELECT observation_generation FROM kim.volume_attachment_observations_current WHERE attachment_id=$1 FOR UPDATE`, observation.AttachmentID).Scan(&currentGeneration)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && currentGeneration >= observation.ObservationGeneration {
			return ErrFailureSafetyStale
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.volume_attachment_observations_current(attachment_id,volume_id,attachment_generation,observation_generation,evidence_id,attachment_state,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,device_present,holder_open) VALUES($1,$2,$3,$4,$5,'ATTACHED',$6,$7,$8,$9::uuid,'vda',$10,true,false) ON CONFLICT(attachment_id) DO UPDATE SET volume_id=EXCLUDED.volume_id,attachment_generation=EXCLUDED.attachment_generation,observation_generation=EXCLUDED.observation_generation,evidence_id=EXCLUDED.evidence_id,attachment_state='ATTACHED',binding_id=EXCLUDED.binding_id,binding_generation=EXCLUDED.binding_generation,host_id=EXCLUDED.host_id,domain_uuid=EXCLUDED.domain_uuid,target_device='vda',observed_lv_uuid=EXCLUDED.observed_lv_uuid,device_present=true,holder_open=false,updated_at=statement_timestamp()`, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration, observation.ObservationGeneration, observation.EvidenceID, observation.BindingID, observation.BindingGeneration, observation.HostID, observation.DomainUUID, observation.ObservedLVUUID)
		return err
	})
}

func scanSourceRootEvaluation(row pgx.Row, out *SourceRootSafetyEvaluation) error {
	return row.Scan(&out.EvaluationID, &out.FailureEpochID, &out.EvaluatedTransitionGeneration,
		&out.SourceAdmissionID, &out.SourcePlanID, &out.SourcePlanDigest, &out.VMID,
		&out.VMGeneration, &out.SourceMaterializationGeneration, &out.SourceHostID,
		&out.RootVolumeID, &out.RootBindingID, &out.RootBindingGeneration,
		&out.RootBindingObservationGeneration, &out.RootBindingState, &out.RootLVUUID,
		&out.RootAttachmentID, &out.RootAttachmentGeneration, &out.RootObservationEvidenceID,
		&out.RootObservationGeneration, &out.RootObservationDigest, &out.TargetDevice,
		&out.ObservedLVUUID, &out.RootEvidenceState, &out.DevicePresent,
		&out.DeviceIdentityMatches, &out.SourceIdentityMatches, &out.HolderOpen,
		&out.PowerEvidenceID, &out.PowerObservationGeneration, &out.PowerObservationDigest,
		&out.ObservedPowerState, &out.PowerConvergenceState, &out.ResultState,
		&out.ReasonCode, &out.EvaluatorVersion, &out.EvaluatorDigest, &out.EvaluationDigest)
}

const sourceRootEvaluationSelect = `SELECT evaluation_id,failure_epoch_id,evaluated_transition_generation,source_admission_id,source_plan_id,source_plan_digest,vm_id::text,vm_generation,source_materialization_generation,source_host_id,root_volume_id,root_binding_id,root_binding_generation,root_binding_observation_generation,root_binding_state,root_lv_uuid,root_attachment_id,root_attachment_generation,root_observation_evidence_id,root_observation_generation,root_observation_digest,target_device,observed_lv_uuid,root_evidence_state,device_present,device_identity_matches,source_identity_matches,holder_open,power_evidence_id,power_observation_generation,power_observation_digest,observed_power_state,power_convergence_state,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.source_root_safety_evaluation_evidence WHERE evaluation_id=$1`

// EvaluateSourceRootSafety derives the root identity exclusively from the
// Failure Epoch's source Admission and immutable VM materialization plan.
func EvaluateSourceRootSafety(ctx context.Context, db TxBeginner, evaluationID, epochID, evaluatorVersion, evaluatorDigest string) (SourceRootSafetyEvaluation, error) {
	var out SourceRootSafetyEvaluation
	if evaluationID == "" || epochID == "" || evaluatorVersion == "" || len(evaluatorDigest) != 64 {
		return out, ErrFailureSafetyConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "source-root-safety-evaluation/"+evaluationID); err != nil {
			return err
		}
		err := scanSourceRootEvaluation(tx.QueryRow(ctx, sourceRootEvaluationSelect, evaluationID), &out)
		if err == nil {
			if out.FailureEpochID != epochID || out.EvaluatorVersion != evaluatorVersion || out.EvaluatorDigest != evaluatorDigest {
				return ErrFailureSafetyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		epoch, err := loadFailureEpochTx(ctx, tx, epochID)
		if err != nil {
			return err
		}
		out.EvaluationID, out.FailureEpochID = evaluationID, epochID
		out.EvaluatedTransitionGeneration = epoch.TransitionGeneration
		out.SourceAdmissionID, out.SourceHostID = epoch.AdmissionID, epoch.SourceHostID
		out.EvaluatorVersion, out.EvaluatorDigest = evaluatorVersion, evaluatorDigest
		err = tx.QueryRow(ctx, `
			SELECT p.plan_id,p.plan_digest,p.vm_id::text,p.vm_generation,p.vm_generation,
			       p.root_volume_id,p.root_binding_id,p.root_binding_generation,
			       b.observation_generation,b.binding_state,b.lv_uuid,
			       p.root_attachment_id,p.root_attachment_generation,
			       o.evidence_id,o.observation_generation,o.observation_digest,o.target_device,o.observed_lv_uuid,o.evidence_state,
			       o.device_present,o.device_identity_matches,o.source_identity_matches,o.holder_open,
			       pe.evidence_id,pc.observation_generation,pe.observation_digest,pc.observed_power_state,pc.convergence_state
			FROM kim.vm_materialization_plan_evidence p
			JOIN kim.volume_backend_bindings_current b ON b.binding_id=p.root_binding_id AND b.binding_generation=p.root_binding_generation AND b.volume_id=p.root_volume_id
			JOIN kim.volume_attachment_observations_current oc ON oc.attachment_id=p.root_attachment_id AND oc.attachment_generation=p.root_attachment_generation
			JOIN kim.volume_attachment_observation_evidence o ON o.evidence_id=oc.evidence_id AND o.attachment_id=oc.attachment_id AND o.attachment_generation=oc.attachment_generation AND o.binding_id=p.root_binding_id AND o.binding_generation=p.root_binding_generation AND o.volume_id=p.root_volume_id
			JOIN kim.vm_power_state_current pc ON pc.vm_id=p.vm_id AND pc.vm_generation=p.vm_generation
			JOIN kim.vm_power_observation_evidence pe ON pe.evidence_id=pc.evidence_id AND pe.observation_generation=pc.observation_generation AND pe.vm_id=p.vm_id AND pe.vm_generation=p.vm_generation
			WHERE p.placement_admission_id=$1 AND p.host_id=$2 AND pe.host_id=$2
		`, epoch.AdmissionID, epoch.SourceHostID).Scan(&out.SourcePlanID, &out.SourcePlanDigest, &out.VMID, &out.VMGeneration, &out.SourceMaterializationGeneration,
			&out.RootVolumeID, &out.RootBindingID, &out.RootBindingGeneration,
			&out.RootBindingObservationGeneration, &out.RootBindingState, &out.RootLVUUID,
			&out.RootAttachmentID, &out.RootAttachmentGeneration, &out.RootObservationEvidenceID,
			&out.RootObservationGeneration, &out.RootObservationDigest, &out.TargetDevice,
			&out.ObservedLVUUID, &out.RootEvidenceState, &out.DevicePresent,
			&out.DeviceIdentityMatches, &out.SourceIdentityMatches, &out.HolderOpen,
			&out.PowerEvidenceID, &out.PowerObservationGeneration, &out.PowerObservationDigest,
			&out.ObservedPowerState, &out.PowerConvergenceState)
		if errors.Is(err, pgx.ErrNoRows) {
			out.ResultState, out.ReasonCode = "UNKNOWN", "exact_source_root_observation_missing"
		} else if err != nil {
			return err
		} else if epoch.EpochState != "CONFIRMED" {
			out.ResultState, out.ReasonCode = "STALE_EPOCH", "epoch_not_confirmed"
		} else if out.RootEvidenceState == "UNKNOWN" || out.PowerConvergenceState == "UNKNOWN" {
			out.ResultState, out.ReasonCode = "UNKNOWN", "source_root_or_power_unknown"
		} else if out.RootEvidenceState == "CONFLICTING" || out.PowerConvergenceState == "CONFLICTING" || out.ObservedPowerState != "SHUTOFF" || out.HolderOpen || out.TargetDevice != "vda" || out.RootLVUUID != out.ObservedLVUUID || !out.DeviceIdentityMatches || !out.SourceIdentityMatches {
			out.ResultState, out.ReasonCode = "CONFLICTING_INPUT", "source_root_not_quiesced_or_identity_conflicting"
		} else if out.RootBindingState != "BOUND" || out.RootEvidenceState != "MATCHED" {
			out.ResultState, out.ReasonCode = "NOT_SAFE", "source_root_binding_or_observation_not_safe"
		} else {
			out.ResultState, out.ReasonCode = "SAFE", "exact_source_root_shutoff_no_holder"
		}
		raw, _ := json.Marshal(out)
		out.EvaluationDigest = digestReleaseBytes(raw)
		_, err = tx.Exec(ctx, `INSERT INTO kim.source_root_safety_evaluation_evidence(evaluation_id,failure_epoch_id,evaluated_transition_generation,source_admission_id,source_plan_id,source_plan_digest,vm_id,vm_generation,source_materialization_generation,source_host_id,root_volume_id,root_binding_id,root_binding_generation,root_binding_observation_generation,root_binding_state,root_lv_uuid,root_attachment_id,root_attachment_generation,root_observation_evidence_id,root_observation_generation,root_observation_digest,target_device,observed_lv_uuid,root_evidence_state,device_present,device_identity_matches,source_identity_matches,holder_open,power_evidence_id,power_observation_generation,power_observation_digest,observed_power_state,power_convergence_state,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest) VALUES($1,$2,$3,$4,$5,$6,$7::uuid,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38)`, out.EvaluationID, out.FailureEpochID, out.EvaluatedTransitionGeneration, out.SourceAdmissionID, out.SourcePlanID, out.SourcePlanDigest, out.VMID, out.VMGeneration, out.SourceMaterializationGeneration, out.SourceHostID, out.RootVolumeID, out.RootBindingID, out.RootBindingGeneration, out.RootBindingObservationGeneration, out.RootBindingState, out.RootLVUUID, out.RootAttachmentID, out.RootAttachmentGeneration, out.RootObservationEvidenceID, out.RootObservationGeneration, out.RootObservationDigest, out.TargetDevice, out.ObservedLVUUID, out.RootEvidenceState, out.DevicePresent, out.DeviceIdentityMatches, out.SourceIdentityMatches, out.HolderOpen, out.PowerEvidenceID, out.PowerObservationGeneration, out.PowerObservationDigest, out.ObservedPowerState, out.PowerConvergenceState, out.ResultState, out.ReasonCode, out.EvaluatorVersion, out.EvaluatorDigest, out.EvaluationDigest)
		return err
	})
	return out, err
}

func sourceRootEvaluationCurrentTx(ctx context.Context, tx pgx.Tx, e SourceRootSafetyEvaluation) bool {
	var bindingGeneration, bindingObservation, attachmentGeneration, observationGeneration, powerGeneration uint64
	var bindingState, lvUUID, evidenceID, target, observedLV, evidenceState, powerEvidence, powerState, convergence string
	var identity, sourceIdentity, holder bool
	err := tx.QueryRow(ctx, `SELECT b.binding_generation,b.observation_generation,b.binding_state,b.lv_uuid,oc.attachment_generation,oc.observation_generation,o.evidence_id,o.target_device,o.observed_lv_uuid,o.evidence_state,o.device_identity_matches,o.source_identity_matches,o.holder_open,pc.observation_generation,pc.evidence_id,pc.observed_power_state,pc.convergence_state FROM kim.virtual_machines_current vm JOIN kim.volume_backend_bindings_current b ON b.binding_id=$3 JOIN kim.volume_attachment_observations_current oc ON oc.binding_id=b.binding_id AND oc.binding_generation=b.binding_generation JOIN kim.volume_attachment_observation_evidence o ON o.evidence_id=oc.evidence_id AND o.attachment_id=oc.attachment_id AND o.attachment_generation=oc.attachment_generation JOIN kim.vm_power_state_current pc ON pc.vm_id=vm.vm_id AND pc.vm_generation=vm.vm_generation WHERE vm.vm_id=$1::uuid AND vm.vm_generation=$2 AND vm.current_plan_id=$5 AND oc.attachment_id=$4`, e.VMID, e.VMGeneration, e.RootBindingID, e.RootAttachmentID, e.SourcePlanID).Scan(&bindingGeneration, &bindingObservation, &bindingState, &lvUUID, &attachmentGeneration, &observationGeneration, &evidenceID, &target, &observedLV, &evidenceState, &identity, &sourceIdentity, &holder, &powerGeneration, &powerEvidence, &powerState, &convergence)
	return err == nil && bindingGeneration == e.RootBindingGeneration && bindingObservation == e.RootBindingObservationGeneration && bindingState == "BOUND" && lvUUID == e.RootLVUUID && attachmentGeneration == e.RootAttachmentGeneration && observationGeneration == e.RootObservationGeneration && evidenceID == e.RootObservationEvidenceID && target == "vda" && observedLV == e.RootLVUUID && evidenceState == "MATCHED" && identity && sourceIdentity && !holder && powerGeneration == e.PowerObservationGeneration && powerEvidence == e.PowerEvidenceID && powerState == "SHUTOFF" && convergence == "MATCHED"
}

func sourceRootStorageCurrentTx(ctx context.Context, tx pgx.Tx, e SourceRootSafetyEvaluation) bool {
	var bindingGeneration, bindingObservation, attachmentGeneration, observationGeneration uint64
	var bindingState, lvUUID, evidenceID, target, observedLV, evidenceState string
	var identity, sourceIdentity, holder bool
	err := tx.QueryRow(ctx, `SELECT b.binding_generation,b.observation_generation,b.binding_state,b.lv_uuid,oc.attachment_generation,oc.observation_generation,o.evidence_id,o.target_device,o.observed_lv_uuid,o.evidence_state,o.device_identity_matches,o.source_identity_matches,o.holder_open FROM kim.volume_backend_bindings_current b JOIN kim.volume_attachment_observations_current oc ON oc.binding_id=b.binding_id AND oc.binding_generation=b.binding_generation JOIN kim.volume_attachment_observation_evidence o ON o.evidence_id=oc.evidence_id AND o.attachment_id=oc.attachment_id AND o.attachment_generation=oc.attachment_generation WHERE b.binding_id=$1 AND oc.attachment_id=$2`, e.RootBindingID, e.RootAttachmentID).Scan(&bindingGeneration, &bindingObservation, &bindingState, &lvUUID, &attachmentGeneration, &observationGeneration, &evidenceID, &target, &observedLV, &evidenceState, &identity, &sourceIdentity, &holder)
	return err == nil && bindingGeneration == e.RootBindingGeneration && bindingObservation == e.RootBindingObservationGeneration && bindingState == "BOUND" && lvUUID == e.RootLVUUID && attachmentGeneration == e.RootAttachmentGeneration && observationGeneration == e.RootObservationGeneration && evidenceID == e.RootObservationEvidenceID && target == "vda" && observedLV == e.RootLVUUID && evidenceState == "MATCHED" && identity && sourceIdentity && !holder
}

func loadSourceRootSafetyProofUsabilityTx(ctx context.Context, tx pgx.Tx, epoch FailureEpoch) (proofID, proofDigest, usability string, err error) {
	var evaluationID string
	err = tx.QueryRow(ctx, `SELECT proof_id,proof_digest,evaluation_id FROM kim.source_root_safety_proof_evidence WHERE failure_epoch_id=$1`, epoch.FailureEpochID).Scan(&proofID, &proofDigest, &evaluationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "MISSING", nil
	}
	if err != nil {
		return "", "", "UNKNOWN", err
	}
	var e SourceRootSafetyEvaluation
	if err := scanSourceRootEvaluation(tx.QueryRow(ctx, sourceRootEvaluationSelect, evaluationID), &e); err != nil {
		return proofID, proofDigest, "UNKNOWN", err
	}
	if !sourceRootStorageCurrentTx(ctx, tx, e) {
		return proofID, proofDigest, "STALE", nil
	}
	if sourceRootEvaluationCurrentTx(ctx, tx, e) {
		return proofID, proofDigest, "USABLE", nil
	}
	// Retirement is not a waiver for source-side power/holder/binding ABA
	// while the source plan is still current. It only preserves the exact
	// historical proof after authority has moved to a different materialization.
	var currentPlanID *string
	if err := tx.QueryRow(ctx, `SELECT current_plan_id FROM kim.virtual_machines_current WHERE vm_id=$1::uuid AND vm_generation=$2`, e.VMID, e.VMGeneration).Scan(&currentPlanID); err != nil {
		return proofID, proofDigest, "UNKNOWN", err
	}
	if currentPlanID == nil || *currentPlanID == e.SourcePlanID {
		return proofID, proofDigest, "STALE", nil
	}
	var retirementID, retirementRootID, retirementRootDigest, state string
	err = tx.QueryRow(ctx, `SELECT c.retirement_decision_id,d.root_safety_proof_id,d.root_safety_proof_digest,c.retirement_state FROM kim.source_materialization_retirements_current c JOIN kim.source_materialization_retirement_decision_evidence d ON d.retirement_decision_id=c.retirement_decision_id AND d.decision_digest=c.decision_digest WHERE c.vm_id=$1::uuid AND c.source_materialization_generation=$2`, e.VMID, e.SourceMaterializationGeneration).Scan(&retirementID, &retirementRootID, &retirementRootDigest, &state)
	if err == nil && retirementID != "" && retirementRootID == proofID && retirementRootDigest == proofDigest && state == "RETIRED" {
		return proofID, proofDigest, "USABLE", nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return proofID, proofDigest, "STALE", nil
	}
	if err != nil {
		return proofID, proofDigest, "UNKNOWN", err
	}
	return proofID, proofDigest, "STALE", nil
}

func loadSourceRetirementUsabilityTx(ctx context.Context, tx pgx.Tx, epoch FailureEpoch, rootProofID, rootProofDigest string) (string, error) {
	var decisionID, state, acceptedRootID, acceptedRootDigest, fencingID, fencingDigest string
	var vmID string
	var materializationGeneration uint64
	err := tx.QueryRow(ctx, `SELECT d.retirement_decision_id,d.decision_state,d.root_safety_proof_id,d.root_safety_proof_digest,d.fencing_proof_id,d.fencing_proof_digest,d.vm_id::text,d.source_materialization_generation FROM kim.source_materialization_retirement_decision_evidence d JOIN kim.source_materialization_retirements_current c ON c.retirement_decision_id=d.retirement_decision_id AND c.vm_id=d.vm_id AND c.source_materialization_generation=d.source_materialization_generation AND c.retirement_state='RETIRED' AND c.decision_digest=d.decision_digest WHERE d.failure_epoch_id=$1`, epoch.FailureEpochID).Scan(&decisionID, &state, &acceptedRootID, &acceptedRootDigest, &fencingID, &fencingDigest, &vmID, &materializationGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return "MISSING", nil
	}
	if err != nil {
		return "UNKNOWN", err
	}
	if decisionID == "" || state != "RETIRED" || acceptedRootID != rootProofID || acceptedRootDigest != rootProofDigest {
		return "STALE", nil
	}
	// A retired source materialization remains guarded by the exact source
	// FENCED event and the newest source-Host power observation. Once the
	// rebuildable VM projection points at the destination Admission, generic
	// current-VM lookup must not erase that historical source authority.
	fid, fdig, fu, err := loadRecoverySourceFencingProofUsabilityTx(ctx, tx, epoch)
	if err != nil {
		return "UNKNOWN", err
	}
	if fu != "USABLE" || fid != fencingID || fdig != fencingDigest {
		return "STALE", nil
	}
	var planVM string
	var planGeneration uint64
	if err := tx.QueryRow(ctx, `SELECT vm_id::text,vm_generation FROM kim.vm_materialization_plan_evidence WHERE placement_admission_id=$1 AND host_id=$2`, epoch.AdmissionID, epoch.SourceHostID).Scan(&planVM, &planGeneration); err != nil {
		return "UNKNOWN", err
	}
	if planVM != vmID || planGeneration != materializationGeneration {
		return "STALE", nil
	}
	return "USABLE", nil
}

func MaterializeSourceRootSafetyProof(ctx context.Context, db TxBeginner, proofID, evaluationID, decidedBy string) (SourceRootSafetyProof, error) {
	var out SourceRootSafetyProof
	if proofID == "" || evaluationID == "" || decidedBy == "" {
		return out, ErrFailureSafetyConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "source-root-safety-proof/"+proofID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT proof_id,failure_epoch_id,evaluation_id,proof_type,proof_state,source_admission_id,source_plan_id,vm_id::text,vm_generation,source_materialization_generation,source_host_id,root_volume_id,root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation,power_evidence_id,power_observation_generation,root_observation_evidence_id,root_observation_generation,decided_by,proof_digest FROM kim.source_root_safety_proof_evidence WHERE proof_id=$1`, proofID).Scan(&out.ProofID, &out.FailureEpochID, &out.EvaluationID, &out.ProofType, &out.ProofState, &out.SourceAdmissionID, &out.SourcePlanID, &out.VMID, &out.VMGeneration, &out.SourceMaterializationGeneration, &out.SourceHostID, &out.RootVolumeID, &out.RootBindingID, &out.RootBindingGeneration, &out.RootAttachmentID, &out.RootAttachmentGeneration, &out.PowerEvidenceID, &out.PowerObservationGeneration, &out.RootObservationEvidenceID, &out.RootObservationGeneration, &out.DecidedBy, &out.ProofDigest)
		if err == nil {
			if out.EvaluationID != evaluationID || out.DecidedBy != decidedBy {
				return ErrFailureSafetyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var e SourceRootSafetyEvaluation
		if err := scanSourceRootEvaluation(tx.QueryRow(ctx, sourceRootEvaluationSelect, evaluationID), &e); err != nil {
			return err
		}
		if e.ResultState != "SAFE" {
			return ErrFailureSafetyBlocked
		}
		epoch, err := loadFailureEpochTx(ctx, tx, e.FailureEpochID)
		if err != nil {
			return err
		}
		if epoch.EpochState != "CONFIRMED" || epoch.TransitionGeneration != e.EvaluatedTransitionGeneration || !sourceRootEvaluationCurrentTx(ctx, tx, e) {
			return ErrFailureSafetyStale
		}
		out = SourceRootSafetyProof{ProofID: proofID, FailureEpochID: e.FailureEpochID, EvaluationID: evaluationID, ProofType: "LOCAL_LVM_SOURCE_ROOT_QUIESCED_NO_HOLDER", ProofState: "SAFE", SourceAdmissionID: e.SourceAdmissionID, SourcePlanID: e.SourcePlanID, VMID: e.VMID, VMGeneration: e.VMGeneration, SourceMaterializationGeneration: e.SourceMaterializationGeneration, SourceHostID: e.SourceHostID, RootVolumeID: e.RootVolumeID, RootBindingID: e.RootBindingID, RootBindingGeneration: e.RootBindingGeneration, RootAttachmentID: e.RootAttachmentID, RootAttachmentGeneration: e.RootAttachmentGeneration, PowerEvidenceID: e.PowerEvidenceID, PowerObservationGeneration: e.PowerObservationGeneration, RootObservationEvidenceID: e.RootObservationEvidenceID, RootObservationGeneration: e.RootObservationGeneration, DecidedBy: decidedBy}
		raw, _ := json.Marshal(out)
		out.ProofDigest = digestReleaseBytes(raw)
		_, err = tx.Exec(ctx, `INSERT INTO kim.source_root_safety_proof_evidence(proof_id,failure_epoch_id,evaluation_id,proof_type,proof_state,source_admission_id,source_plan_id,vm_id,vm_generation,source_materialization_generation,source_host_id,root_volume_id,root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation,power_evidence_id,power_observation_generation,root_observation_evidence_id,root_observation_generation,decided_by,proof_digest) VALUES($1,$2,$3,$4,'SAFE',$5,$6,$7::uuid,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, out.ProofID, out.FailureEpochID, out.EvaluationID, out.ProofType, out.SourceAdmissionID, out.SourcePlanID, out.VMID, out.VMGeneration, out.SourceMaterializationGeneration, out.SourceHostID, out.RootVolumeID, out.RootBindingID, out.RootBindingGeneration, out.RootAttachmentID, out.RootAttachmentGeneration, out.PowerEvidenceID, out.PowerObservationGeneration, out.RootObservationEvidenceID, out.RootObservationGeneration, out.DecidedBy, out.ProofDigest)
		return err
	})
	return out, err
}

func RetireSourceMaterialization(ctx context.Context, db TxBeginner, decisionID, epochID, rootProofID, fencingProofID, decidedBy string) (SourceMaterializationRetirementDecision, error) {
	var out SourceMaterializationRetirementDecision
	if decisionID == "" || epochID == "" || rootProofID == "" || fencingProofID == "" || decidedBy == "" {
		return out, ErrFailureSafetyConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "source-materialization-retirement/"+decisionID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT retirement_decision_id,failure_epoch_id,source_admission_id,source_plan_id,source_plan_digest,vm_id::text,vm_generation,source_materialization_generation,source_host_id,fencing_proof_id,fencing_proof_digest,root_safety_proof_id,root_safety_proof_digest,decision_state,decided_by,decision_digest FROM kim.source_materialization_retirement_decision_evidence WHERE retirement_decision_id=$1`, decisionID).Scan(&out.RetirementDecisionID, &out.FailureEpochID, &out.SourceAdmissionID, &out.SourcePlanID, &out.SourcePlanDigest, &out.VMID, &out.VMGeneration, &out.SourceMaterializationGeneration, &out.SourceHostID, &out.FencingProofID, &out.FencingProofDigest, &out.RootSafetyProofID, &out.RootSafetyProofDigest, &out.DecisionState, &out.DecidedBy, &out.DecisionDigest)
		if err == nil {
			if out.FailureEpochID != epochID || out.RootSafetyProofID != rootProofID || out.FencingProofID != fencingProofID || out.DecidedBy != decidedBy {
				return ErrFailureSafetyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		epoch, err := loadFailureEpochTx(ctx, tx, epochID)
		if err != nil {
			return err
		}
		fid, fdig, fu, err := loadFencingProofUsabilityTx(ctx, tx, epoch)
		if err != nil || fu != "USABLE" || fid != fencingProofID {
			return ErrFailureSafetyStale
		}
		var rp SourceRootSafetyProof
		if err := tx.QueryRow(ctx, `SELECT proof_id,failure_epoch_id,evaluation_id,source_admission_id,source_plan_id,vm_id::text,vm_generation,source_materialization_generation,source_host_id,root_volume_id,root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation,power_evidence_id,power_observation_generation,root_observation_evidence_id,root_observation_generation,decided_by,proof_digest FROM kim.source_root_safety_proof_evidence WHERE proof_id=$1`, rootProofID).Scan(&rp.ProofID, &rp.FailureEpochID, &rp.EvaluationID, &rp.SourceAdmissionID, &rp.SourcePlanID, &rp.VMID, &rp.VMGeneration, &rp.SourceMaterializationGeneration, &rp.SourceHostID, &rp.RootVolumeID, &rp.RootBindingID, &rp.RootBindingGeneration, &rp.RootAttachmentID, &rp.RootAttachmentGeneration, &rp.PowerEvidenceID, &rp.PowerObservationGeneration, &rp.RootObservationEvidenceID, &rp.RootObservationGeneration, &rp.DecidedBy, &rp.ProofDigest); err != nil {
			return err
		}
		if rp.FailureEpochID != epochID {
			return ErrFailureSafetyConflict
		}
		var e SourceRootSafetyEvaluation
		if err := scanSourceRootEvaluation(tx.QueryRow(ctx, sourceRootEvaluationSelect, rp.EvaluationID), &e); err != nil || !sourceRootEvaluationCurrentTx(ctx, tx, e) {
			return ErrFailureSafetyStale
		}
		out = SourceMaterializationRetirementDecision{RetirementDecisionID: decisionID, FailureEpochID: epochID, SourceAdmissionID: rp.SourceAdmissionID, SourcePlanID: rp.SourcePlanID, SourcePlanDigest: e.SourcePlanDigest, VMID: rp.VMID, VMGeneration: rp.VMGeneration, SourceMaterializationGeneration: rp.SourceMaterializationGeneration, SourceHostID: rp.SourceHostID, FencingProofID: fid, FencingProofDigest: fdig, RootSafetyProofID: rp.ProofID, RootSafetyProofDigest: rp.ProofDigest, DecisionState: "RETIRED", DecidedBy: decidedBy}
		raw, _ := json.Marshal(out)
		out.DecisionDigest = digestReleaseBytes(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.source_materialization_retirement_decision_evidence(retirement_decision_id,failure_epoch_id,source_admission_id,source_plan_id,source_plan_digest,vm_id,vm_generation,source_materialization_generation,source_host_id,fencing_proof_id,fencing_proof_digest,root_safety_proof_id,root_safety_proof_digest,decision_state,decided_by,decision_digest) VALUES($1,$2,$3,$4,$5,$6::uuid,$7,$8,$9,$10,$11,$12,$13,'RETIRED',$14,$15)`, out.RetirementDecisionID, out.FailureEpochID, out.SourceAdmissionID, out.SourcePlanID, out.SourcePlanDigest, out.VMID, out.VMGeneration, out.SourceMaterializationGeneration, out.SourceHostID, out.FencingProofID, out.FencingProofDigest, out.RootSafetyProofID, out.RootSafetyProofDigest, out.DecidedBy, out.DecisionDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.source_materialization_retirements_current(vm_id,source_materialization_generation,retirement_decision_id,retirement_state,decision_digest) VALUES($1::uuid,$2,$3,'RETIRED',$4)`, out.VMID, out.SourceMaterializationGeneration, out.RetirementDecisionID, out.DecisionDigest)
		return err
	})
	return out, err
}
