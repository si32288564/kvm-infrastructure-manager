package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type recoveryQualificationDB interface {
	TxBeginner
	QueryRower
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func qualifyRecoveryMaterializationTerminal(t *testing.T, ctx context.Context, pool recoveryQualificationDB, suffix, operationID, vmID, imageID, checksum, destinationBackendID, destinationVGUUID, nextEligibilityEvaluationID string, plan RecoveryPlan, start RecoveryOperationStart, budgetClaimID string) {
	if len(plan.DestinationRequest.Storage) != 1 || !plan.DestinationRequest.Storage[0].Bootable || plan.DestinationRequest.Storage[0].BackendID != destinationBackendID {
		t.Fatalf("Recovery Plan did not fix one ordinary destination boot volume: %+v", plan.DestinationRequest.Storage)
	}
	required := plan.DestinationRequest.Storage[0]
	bindingID := "storage-binding:" + plan.DestinationRequest.RequestID + ":" + required.VolumeID
	var resourceKey string
	if err := pool.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1 AND placement_admission_id=$2`, bindingID, start.DestinationAdmissionID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	lvUUID := "recovery-lv-" + suffix
	bindingCommand, bindingVerification := "recovery-binding-command-"+suffix, "recovery-binding-verification-"+suffix
	bindingObservationDigest, bindingVerifierDigest := digestBytes([]byte("recovery binding observation")), digestBytes([]byte("recovery binding verifier"))
	if err := seedLocalLVMVerification(ctx, pool, localLVMVerificationFixture{JobID: "recovery-binding-job-" + suffix, VolumeID: required.VolumeID, HostID: start.DestinationHostID, CommandID: bindingCommand, VerificationID: bindingVerification, ObservationDigest: bindingObservationDigest, VerifierDigest: bindingVerifierDigest, VGUUID: destinationVGUUID, LVUUID: lvUUID, ResourceKey: resourceKey, SizeBytes: required.SizeBytes}); err != nil {
		t.Fatal(err)
	}
	if err := AcceptLocalLVMBindingObservation(ctx, pool, LocalLVMBindingObservation{EvidenceID: "recovery-binding-evidence-" + suffix, BindingID: bindingID, VolumeID: required.VolumeID, BackendID: destinationBackendID, HostID: start.DestinationHostID, VGUUID: destinationVGUUID, LVUUID: lvUUID, BackendResourceKey: resourceKey, BindingGeneration: 1, CommandID: bindingCommand, VerificationID: bindingVerification, AttemptIndex: 1, ObservationGeneration: 1, ObservationDigest: bindingObservationDigest, VerifierDigest: bindingVerifierDigest, EvidenceState: "MATCHED", ObservedSizeBytes: required.SizeBytes}); err != nil {
		t.Fatal(err)
	}

	materializationRequest := RecoveryMaterializationRequest{RecoveryOperationID: operationID, MaterializationID: "recovery-materialization-" + suffix, VMID: vmID, VMPlanID: "recovery-vm-plan-" + suffix, DefineJobID: "recovery-vm-define-job-" + suffix, DefineCommandID: "recovery-vm-define-command-" + suffix}
	materialization, err := PrepareRecoveryMaterialization(ctx, pool, materializationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := PrepareRecoveryMaterialization(ctx, pool, materializationRequest); err != nil || replay.MaterializationDigest != materialization.MaterializationDigest {
		t.Fatalf("Recovery materialization replay=%+v err=%v", replay, err)
	}

	seedAttemptVerification := func(jobID, commandID, verificationID, hostID string, generation uint64, observationDigest, verifierDigest string, payload map[string]any) {
		payloadJSON, _ := json.Marshal(payload)
		if _, err := pool.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute'); INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1); INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($4,$1,1,$5,$6,'MATCHED',$7,$8::jsonb); UPDATE kim.execution_commands_current SET command_state='SUCCEEDED',current_attempt_index=1 WHERE command_id=$1; UPDATE kim.execution_jobs SET job_state='SUCCEEDED' WHERE job_id=$9`, pgx.QueryExecModeSimpleProtocol, commandID, hostID, digestBytes([]byte(commandID+"/token")), verificationID, generation, observationDigest, verifierDigest, string(payloadJSON), jobID); err != nil {
			t.Fatal(err)
		}
	}

	defineObsDigest, defineVerifierDigest := digestBytes([]byte("recovery define observation")), digestBytes([]byte("recovery define verifier"))
	defineVerification := "recovery-define-verification-" + suffix
	seedAttemptVerification(materializationRequest.DefineJobID, materializationRequest.DefineCommandID, defineVerification, start.DestinationHostID, 1, defineObsDigest, defineVerifierDigest, map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "plan_digest": materialization.VMPlanDigest, "domain_present": true, "domain_identity_matches": true, "plan_identity_matches": true, "compute_shape_matches": true, "root_volume_identity_matches": true, "image_materialization_state": "PENDING", "network_realization_state": "PENDING"})
	if err := AcceptVMDefinitionObservation(ctx, pool, VMDefinitionObservation{EvidenceID: "recovery-define-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: materialization.VMPlanID, PlanDigest: materialization.VMPlanDigest, HostID: start.DestinationHostID, CommandID: materializationRequest.DefineCommandID, AttemptIndex: 1, VerificationID: defineVerification, ObservationGeneration: 1, ObservationDigest: defineObsDigest, VerifierDigest: defineVerifierDigest, EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}

	imageRequest := VMImageMaterializationRequest{VMID: vmID, PlanID: materialization.VMPlanID, JobID: "recovery-image-job-" + suffix, CommandID: "recovery-image-command-" + suffix}
	if _, err := PrepareVMImageMaterialization(ctx, pool, imageRequest); err != nil {
		t.Fatal(err)
	}
	imageObsDigest, imageVerifierDigest := digestBytes([]byte("recovery image observation")), digestBytes([]byte("recovery image verifier"))
	imageVerification := "recovery-image-verification-" + suffix
	seedAttemptVerification(imageRequest.JobID, imageRequest.CommandID, imageVerification, start.DestinationHostID, 1, imageObsDigest, imageVerifierDigest, map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "image_id": imageID, "image_revision": float64(1), "expected_content_digest": checksum, "observed_content_digest": checksum, "image_size_bytes": float64(4096), "volume_id": required.VolumeID, "observed_vg_uuid": destinationVGUUID, "observed_lv_uuid": lvUUID, "backend_resource_key": resourceKey, "holder_open": false, "content_identity_matches": true})
	if err := AcceptVMImageRealizationObservation(ctx, pool, VMImageRealizationObservation{EvidenceID: "recovery-image-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: materialization.VMPlanID, PlanDigest: materialization.VMPlanDigest, HostID: start.DestinationHostID, ImageID: imageID, ImageRevision: 1, ExpectedDigest: checksum, ObservedDigest: checksum, ImageSizeBytes: 4096, VolumeID: required.VolumeID, BindingID: bindingID, BindingGeneration: 1, VGUUID: destinationVGUUID, LVUUID: lvUUID, BackendResourceKey: resourceKey, CommandID: imageRequest.CommandID, AttemptIndex: 1, VerificationID: imageVerification, ObservationGeneration: 1, ObservationDigest: imageObsDigest, VerifierDigest: imageVerifierDigest, EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}

	if err := MarkRecoveryNoNetworkReady(ctx, pool, operationID); err != nil {
		t.Fatal(err)
	}

	dangerous, err := EvaluateRecoveryDangerousStep(ctx, pool, "recovery-power-dangerous-"+suffix, operationID, digestBytes([]byte("recovery dangerous power gate")))
	if err != nil || dangerous.ResultState != "AUTHORIZED" {
		t.Fatalf("dangerous=%+v err=%v", dangerous, err)
	}
	// AUTHORIZED evaluation is pure: no power command exists until explicit authorization.
	var prePower int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.recovery_power_authority_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&prePower); err != nil || prePower != 0 {
		t.Fatalf("dangerous gate emitted power authority count=%d err=%v", prePower, err)
	}
	// The immutable dangerous-step evaluation is not a capability. Every
	// power authority transaction must re-read all current safety inputs.
	assertPowerBlocked := func(label, mutation string, args ...any) {
		t.Helper()
		rollback := errors.New("rollback pre-power drift " + label)
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if tag, err := tx.Exec(ctx, mutation, args...); err != nil || tag.RowsAffected() == 0 {
				t.Fatalf("%s mutation rows=%d err=%v", label, tag.RowsAffected(), err)
			}
			_, authErr := AuthorizeRecoveryPowerOn(ctx, scopeTxBeginner{tx}, "blocked-power-authority-"+label+"-"+suffix, operationID, dangerous.EvaluationID, "blocked-power-job-"+label+"-"+suffix, "blocked-power-command-"+label+"-"+suffix)
			if !errors.Is(authErr, ErrRecoveryOperationStale) {
				t.Fatalf("%s power authority error=%v", label, authErr)
			}
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.recovery_power_authority_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("%s emitted power authority count=%d err=%v", label, count, err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("%s rollback=%v", label, err)
		}
	}
	assertPowerBlocked("fencing", `UPDATE kim.host_operation_authorities_current SET authority_state='ARMED' WHERE host_id=$1`, plan.SourceHostID)
	assertPowerBlocked("storage", `UPDATE kim.volume_attachment_claims SET claim_state='ACTIVE' WHERE host_id=$1 AND claim_state='RELEASED'`, plan.SourceHostID)
	assertPowerBlocked("budget", `UPDATE kim.recovery_budget_claims_current SET claim_state='FENCED',state_generation=state_generation+1 WHERE claim_id=$1`, budgetClaimID)
	assertPowerBlocked("destination", `UPDATE kim.vm_materialization_readiness_current SET boot_readiness='BLOCKED',blocking_reasons=ARRAY['qualification_drift'] WHERE vm_id=$1`, vmID)
	powerAuthority, err := AuthorizeRecoveryPowerOn(ctx, pool, "recovery-power-authority-"+suffix, operationID, dangerous.EvaluationID, "recovery-power-job-"+suffix, "recovery-power-command-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := AuthorizeRecoveryPowerOn(ctx, pool, powerAuthority.PowerAuthorityID, operationID, dangerous.EvaluationID, powerAuthority.PowerJobID, powerAuthority.PowerCommandID); err != nil || replay.AuthorityDigest != powerAuthority.AuthorityDigest {
		t.Fatalf("power authority replay=%+v err=%v", replay, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute'); INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1); UPDATE kim.execution_commands_current SET command_state='UNKNOWN',current_attempt_index=1 WHERE command_id=$1`, pgx.QueryExecModeSimpleProtocol, powerAuthority.PowerCommandID, start.DestinationHostID, digestBytes([]byte("recovery power token"))); err != nil {
		t.Fatal(err)
	}
	unknown, err := RefreshRecoveryPowerExecution(ctx, pool, operationID, "")
	if err != nil || unknown.LifecycleState != "UNKNOWN" {
		t.Fatalf("power UNKNOWN=%+v err=%v", unknown, err)
	}
	var budgetState string
	if err := pool.QueryRow(ctx, `SELECT claim_state FROM kim.recovery_budget_claims_current WHERE claim_id=$1`, budgetClaimID).Scan(&budgetState); err != nil || budgetState != "CONSUMED" {
		t.Fatalf("UNKNOWN budget=%s err=%v", budgetState, err)
	}
	powerVerification := "recovery-power-verification-" + suffix
	powerObsDigest, powerVerifierDigest := digestBytes([]byte("recovery power observation")), digestBytes([]byte("recovery power verifier"))
	if err := RecordCommandVerification(ctx, pool, CommandVerification{VerificationID: powerVerification, CommandID: powerAuthority.PowerCommandID, AttemptIndex: 1, ObservationGeneration: 1, ObservationDigest: powerObsDigest, State: "MATCHED", VerifierArtifactDigest: powerVerifierDigest, Evidence: map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state='SUCCEEDED' WHERE command_id=$1; UPDATE kim.execution_jobs SET job_state='SUCCEEDED' WHERE job_id=$2`, pgx.QueryExecModeSimpleProtocol, powerAuthority.PowerCommandID, powerAuthority.PowerJobID); err != nil {
		t.Fatal(err)
	}
	verifying, err := RefreshRecoveryPowerExecution(ctx, pool, operationID, powerVerification)
	if err != nil || verifying.LifecycleState != "VERIFYING" {
		t.Fatalf("power read-back=%+v err=%v", verifying, err)
	}

	// Root holder evidence is necessarily post-power. The typed root-disk
	// command is observation-only for vda and cannot attach or detach it.
	attachmentJob, attachmentCommand, attachmentVerification := "recovery-attach-job-"+suffix, "recovery-attach-command-"+suffix, "recovery-attach-verification-"+suffix
	attachmentPayload := map[string]any{"attachment_id": required.AttachmentID, "volume_id": required.VolumeID, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": lvUUID, "desired_state": "ATTACHED", "device_present": true, "device_identity_matches": true, "source_identity_matches": true, "holder_open": true, "read_only": false}
	payloadRaw, _ := json.Marshal(attachmentPayload)
	payloadDigest := digestBytes(payloadRaw)
	if _, err := pool.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VOLUME_ATTACHMENT',$2,1,'DISPATCHABLE'); INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($3,$1,$4,'LOCAL_LVM_VOLUME_ATTACHMENT_ENSURE','kim.command.local-lvm-volume-attachment/v1',$5,$6::jsonb,$7); INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($3,'PENDING'); UPDATE kim.execution_jobs SET current_command_id=$3 WHERE job_id=$1`, pgx.QueryExecModeSimpleProtocol, attachmentJob, required.AttachmentID, attachmentCommand, start.DestinationHostID, "attachment:"+required.AttachmentID, string(payloadRaw), payloadDigest); err != nil {
		t.Fatal(err)
	}
	attachmentObsDigest, attachmentVerifierDigest := digestBytes([]byte("recovery attachment observation")), digestBytes([]byte("recovery attachment verifier"))
	seedAttemptVerification(attachmentJob, attachmentCommand, attachmentVerification, start.DestinationHostID, 1, attachmentObsDigest, attachmentVerifierDigest, attachmentPayload)
	if err := AcceptLocalLVMAttachmentObservation(ctx, pool, LocalLVMAttachmentObservation{EvidenceID: "recovery-attachment-evidence-" + suffix, AttachmentID: required.AttachmentID, VolumeID: required.VolumeID, AttachmentGeneration: 1, BindingID: bindingID, BindingGeneration: 1, HostID: start.DestinationHostID, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvUUID, DesiredState: "ATTACHED", CommandID: attachmentCommand, VerificationID: attachmentVerification, ObservationGeneration: 1, AttemptIndex: 1, ObservationDigest: attachmentObsDigest, VerifierDigest: attachmentVerifierDigest, EvidenceState: "MATCHED", DevicePresent: true, DeviceIdentityMatches: true, SourceIdentityMatches: true, HolderOpen: true}); err != nil {
		t.Fatal(err)
	}

	verification, err := EvaluateRecoveryVerification(ctx, pool, "recovery-terminal-verification-"+suffix, operationID, "recovery-verifier/v1", digestBytes([]byte("recovery verifier/v1")))
	if err != nil || verification.ResultState != "VERIFIED" {
		t.Fatalf("Recovery Verification=%+v err=%v", verification, err)
	}
	var operationState, epochState string
	if err := pool.QueryRow(ctx, `SELECT c.lifecycle_state,e.epoch_state FROM kim.recovery_operations_current c JOIN kim.recovery_operation_evidence o USING(recovery_operation_id) JOIN kim.failure_epochs_current e ON e.failure_epoch_id=o.failure_epoch_id WHERE c.recovery_operation_id=$1`, operationID).Scan(&operationState, &epochState); err != nil || operationState != "VERIFYING" || epochState != "FENCED" {
		t.Fatalf("pure Verification mutated terminal state operation=%s epoch=%s err=%v", operationState, epochState, err)
	}

	// An old VERIFIED evaluation is not a terminal capability: power,
	// attachment, or network drift is fenced by exact current generations.
	assertTerminalStale := func(label, mutation string, args ...any) {
		t.Helper()
		rollback := errors.New("rollback terminal drift " + label)
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if tag, err := tx.Exec(ctx, mutation, args...); err != nil || tag.RowsAffected() == 0 {
				t.Fatalf("%s terminal mutation rows=%d err=%v", label, tag.RowsAffected(), err)
			}
			_, terminalErr := CommitRecoveryTerminalDecision(ctx, scopeTxBeginner{tx}, "stale-terminal-"+label+"-"+suffix, verification.VerificationID, "recovery-authority/v1")
			if !errors.Is(terminalErr, ErrRecoveryOperationStale) {
				t.Fatalf("%s terminal error=%v", label, terminalErr)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("%s terminal rollback=%v", label, err)
		}
	}
	assertTerminalStale("power", `UPDATE kim.vm_power_state_current SET observed_power_state='SHUTOFF',convergence_state='CONFLICTING',observation_generation=observation_generation+1 WHERE vm_id=$1`, vmID)
	assertTerminalStale("attachment", `UPDATE kim.volume_attachment_observations_current SET attachment_state='DETACHED',device_present=false,holder_open=false,observation_generation=observation_generation+1 WHERE attachment_id=$1`, required.AttachmentID)
	assertTerminalStale("network", `UPDATE kim.vm_materialization_readiness_current SET network_state='PENDING',boot_readiness='BLOCKED',network_observation_generation=network_observation_generation+1,blocking_reasons=ARRAY['network_drift'] WHERE vm_id=$1`, vmID)

	decision, err := CommitRecoveryTerminalDecision(ctx, pool, "recovery-terminal-decision-"+suffix, verification.VerificationID, "recovery-authority/v1")
	if err != nil {
		t.Fatal(err)
	}
	replayDecision, err := CommitRecoveryTerminalDecision(ctx, pool, decision.TerminalDecisionID, verification.VerificationID, "recovery-authority/v1")
	if err != nil || decision.DecisionDigest != replayDecision.DecisionDigest {
		t.Fatalf("terminal response-loss replay decision=%+v replay=%+v err=%v", decision, replayDecision, err)
	}
	var terminalCount, operationTransitions, epochTransitions, budgetTransitions int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.recovery_terminal_decision_evidence WHERE recovery_operation_id=$1),(SELECT count(*) FROM kim.recovery_operation_transition_evidence WHERE recovery_operation_id=$1 AND to_state='VERIFIED'),(SELECT count(*) FROM kim.failure_epoch_transition_evidence t JOIN kim.recovery_operation_evidence o ON o.failure_epoch_id=t.failure_epoch_id WHERE o.recovery_operation_id=$1 AND t.to_state='RECOVERED'),(SELECT count(*) FROM kim.recovery_budget_claim_transition_evidence WHERE claim_id=$2 AND to_state='RELEASED')`, operationID, budgetClaimID).Scan(&terminalCount, &operationTransitions, &epochTransitions, &budgetTransitions); err != nil || terminalCount != 1 || operationTransitions != 1 || epochTransitions != 1 || budgetTransitions != 1 {
		t.Fatalf("terminal atomic counts=%d/%d/%d/%d err=%v", terminalCount, operationTransitions, epochTransitions, budgetTransitions, err)
	}
	if err := pool.QueryRow(ctx, `SELECT c.lifecycle_state,e.epoch_state,b.claim_state FROM kim.recovery_operations_current c JOIN kim.recovery_operation_evidence o USING(recovery_operation_id) JOIN kim.failure_epochs_current e ON e.failure_epoch_id=o.failure_epoch_id JOIN kim.recovery_budget_claims_current b ON b.claim_id=o.recovery_budget_claim_id WHERE c.recovery_operation_id=$1`, operationID).Scan(&operationState, &epochState, &budgetState); err != nil || operationState != "VERIFIED" || epochState != "RECOVERED" || budgetState != "RELEASED" {
		t.Fatalf("terminal projection=%s/%s/%s err=%v", operationState, epochState, budgetState, err)
	}
	// Budget release is independent from destination cleanup. Model a later,
	// separately-authorized resource reconciliation only inside this rollback
	// fixture, then require a fresh Evaluation before the next Planning Claim.
	if _, err := pool.Exec(ctx, `UPDATE kim.compute_allocation_claims SET claim_state='RELEASED',released_at=statement_timestamp() WHERE admission_id=$1; UPDATE kim.storage_capacity_claims SET claim_state='RELEASED' WHERE placement_admission_id=$1`, pgx.QueryExecModeSimpleProtocol, start.DestinationAdmissionID); err != nil {
		t.Fatal(err)
	}
	var nextFailureEpochID, nextScopeID string
	if err := pool.QueryRow(ctx, `SELECT e.failure_epoch_id,min(c.placement_scope_id) FROM kim.recovery_eligibility_evaluation_evidence e JOIN kim.recovery_eligibility_destination_candidate_evidence c USING(evaluation_id) WHERE e.evaluation_id=$1 GROUP BY e.failure_epoch_id`, nextEligibilityEvaluationID).Scan(&nextFailureEpochID, &nextScopeID); err != nil {
		t.Fatal(err)
	}
	// Model a later independent failure incarnation by restoring that fixture's
	// exact source read model. This is test setup only; Recovery terminal success
	// does not perform source cleanup or reverse the destination materialization.
	var nextSourceHost, nextPowerEvidence string
	var nextPowerGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT f.source_host_id,o.vm_power_evidence_id,o.vm_power_observation_generation FROM kim.failure_epoch_evidence f JOIN kim.failure_fencing_proof_evidence p ON p.failure_epoch_id=f.failure_epoch_id JOIN kim.failure_fencing_evaluation_evidence e ON e.evaluation_id=p.evaluation_id JOIN kim.source_execution_fencing_observation_evidence o ON o.evidence_id=e.fencing_evidence_id AND o.evidence_generation=e.latest_fencing_evidence_generation WHERE f.failure_epoch_id=$1`, nextFailureEpochID).Scan(&nextSourceHost, &nextPowerEvidence, &nextPowerGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kim.virtual_machines_current SET host_id=$2 WHERE vm_id=$1; UPDATE kim.vm_power_state_current SET desired_power_state='SHUTOFF',observed_power_state='SHUTOFF',convergence_state='MATCHED',observation_generation=$3,evidence_id=$4 WHERE vm_id=$1`, pgx.QueryExecModeSimpleProtocol, vmID, nextSourceHost, nextPowerGeneration, nextPowerEvidence); err != nil {
		t.Fatal(err)
	}
	nextEvaluation, err := EvaluateRecoveryEligibility(ctx, pool, "recovery-eligibility-after-release-evaluation-"+suffix, nextFailureEpochID, nextScopeID, "recovery-eligibility-evaluator/v1", digestBytes([]byte("recovery-eligibility-evaluator/v1")))
	if err != nil || nextEvaluation.ResultState != "ELIGIBLE" {
		t.Fatalf("released Budget/resource reconciliation did not produce fresh eligible Evaluation=%+v err=%v", nextEvaluation, err)
	}
	nextDecision, err := MaterializeRecoveryEligibilityDecision(ctx, pool, "recovery-eligibility-after-release-"+suffix, nextEvaluation.EvaluationID, "recovery-authority/v1")
	if err != nil || nextDecision.DecisionState != "ACCEPTED" {
		t.Fatalf("released Budget did not admit next eligible Recovery decision=%+v err=%v", nextDecision, err)
	}
	if err := pool.QueryRow(ctx, `SELECT claim_state FROM kim.recovery_budget_claims_current WHERE claim_id=$1`, nextDecision.BudgetClaimID).Scan(&budgetState); err != nil || budgetState != "RESERVED" {
		t.Fatalf("next Recovery Planning Claim=%s err=%v", budgetState, err)
	}
}
