package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrAvailabilityRebindConflict        = errors.New("Availability Rebind authority conflict")
	ErrAvailabilityRebindStaleSource     = errors.New("Availability Rebind source Binding is stale")
	ErrAvailabilityRebindStaleTarget     = errors.New("Availability Rebind target Policy is stale")
	ErrAvailabilityRebindPolicyNotActive = errors.New("Availability Rebind target Policy is not active")
	ErrAvailabilityRebindInvalidTarget   = errors.New("Availability Rebind target Policy is invalid")
	ErrAvailabilityRebindUnauthorized    = errors.New("Availability Rebind is not authorized")
)

type VMAvailabilityRebindRequest struct {
	RebindID, WorkloadID, SourceBindingDigest                 string
	TargetPolicyID, TargetPolicyDigest                        string
	RequestedBy, AuthorizedBy, AuthorizationReference, Reason string
	ExpectedCurrentBindingRevision, TargetPolicyRevision      uint64
	RequestDigest                                             string
}

type VMAvailabilityRebindDecision struct {
	RebindID, WorkloadID, DecisionState, ResultCode         string
	SourceBindingDigest, TargetPolicyID, TargetPolicyDigest string
	RequestDigest, DecidedBy, AuthorizationReference        string
	DecisionReason, DecisionDigest                          string
	DecisionGeneration, SourceBindingRevision               uint64
	TargetBindingRevision, TargetPolicyRevision             uint64
}

type availabilityRebindRequestDigest struct {
	RebindID, WorkloadID, SourceBindingDigest                 string
	TargetPolicyID, TargetPolicyDigest                        string
	RequestedBy, AuthorizedBy, AuthorizationReference, Reason string
	ExpectedCurrentBindingRevision, TargetPolicyRevision      uint64
}

func validateAvailabilityRebindRequest(request VMAvailabilityRebindRequest) error {
	if request.RebindID == "" || request.WorkloadID == "" || request.ExpectedCurrentBindingRevision == 0 ||
		request.SourceBindingDigest == "" || request.TargetPolicyID == "" || request.TargetPolicyRevision == 0 ||
		request.TargetPolicyDigest == "" || request.RequestedBy == "" || request.AuthorizedBy == "" ||
		request.AuthorizationReference == "" || request.Reason == "" {
		return ErrAvailabilityRebindUnauthorized
	}
	return nil
}

func availabilityRebindRequestDigestValue(request VMAvailabilityRebindRequest) string {
	raw, _ := json.Marshal(availabilityRebindRequestDigest{
		RebindID: request.RebindID, WorkloadID: request.WorkloadID,
		SourceBindingDigest: request.SourceBindingDigest, TargetPolicyID: request.TargetPolicyID,
		TargetPolicyDigest: request.TargetPolicyDigest, RequestedBy: request.RequestedBy,
		AuthorizedBy: request.AuthorizedBy, AuthorizationReference: request.AuthorizationReference,
		Reason: request.Reason, ExpectedCurrentBindingRevision: request.ExpectedCurrentBindingRevision,
		TargetPolicyRevision: request.TargetPolicyRevision,
	})
	return digestReleaseBytes(raw)
}

// RecordVMAvailabilityRebindRequest records authorized intent only. It does
// not advance the VM Binding and never resolves a Policy from live HostGroups.
func RecordVMAvailabilityRebindRequest(ctx context.Context, db TxBeginner, request VMAvailabilityRebindRequest) (VMAvailabilityRebindRequest, error) {
	if err := validateAvailabilityRebindRequest(request); err != nil {
		return VMAvailabilityRebindRequest{}, err
	}
	request.RequestDigest = availabilityRebindRequestDigestValue(request)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-rebind/"+request.RebindID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRow(ctx, `SELECT request_digest FROM kim.vm_availability_rebind_request_evidence WHERE rebind_id=$1`, request.RebindID).Scan(&existing)
		if err == nil {
			if existing != request.RequestDigest {
				return ErrAvailabilityRebindConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var revision uint64
		var digest string
		if err := tx.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1 FOR SHARE`, request.WorkloadID).Scan(&revision, &digest); err != nil {
			return ErrAvailabilityRebindStaleSource
		}
		if revision != request.ExpectedCurrentBindingRevision || digest != request.SourceBindingDigest {
			return ErrAvailabilityRebindStaleSource
		}
		var lifecycle string
		if err := tx.QueryRow(ctx, `SELECT lifecycle_state FROM kim.availability_policies_current WHERE policy_id=$1 AND policy_revision=$2 AND policy_digest=$3 FOR SHARE`, request.TargetPolicyID, request.TargetPolicyRevision, request.TargetPolicyDigest).Scan(&lifecycle); err != nil {
			return ErrAvailabilityRebindStaleTarget
		}
		if lifecycle != "ACTIVE" {
			return ErrAvailabilityRebindPolicyNotActive
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.vm_availability_rebind_request_evidence(
			rebind_id,workload_id,expected_current_binding_revision,source_binding_digest,target_policy_id,
			target_policy_revision,target_policy_digest,requested_by,authorized_by,authorization_reference,reason,request_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, request.RebindID, request.WorkloadID,
			request.ExpectedCurrentBindingRevision, request.SourceBindingDigest, request.TargetPolicyID,
			request.TargetPolicyRevision, request.TargetPolicyDigest, request.RequestedBy, request.AuthorizedBy,
			request.AuthorizationReference, request.Reason, request.RequestDigest)
		return err
	})
	return request, err
}

func loadAvailabilityRebindDecisionTx(ctx context.Context, tx pgx.Tx, rebindID string) (VMAvailabilityRebindDecision, error) {
	var d VMAvailabilityRebindDecision
	err := tx.QueryRow(ctx, `SELECT rebind_id,decision_generation,workload_id,decision_state,result_code,
		source_binding_revision,source_binding_digest,COALESCE(target_binding_revision,0),target_policy_id,
		target_policy_revision,target_policy_digest,request_digest,decided_by,authorization_reference,
		decision_reason,decision_digest FROM kim.vm_availability_rebind_decision_evidence WHERE rebind_id=$1`, rebindID).Scan(
		&d.RebindID, &d.DecisionGeneration, &d.WorkloadID, &d.DecisionState, &d.ResultCode, &d.SourceBindingRevision,
		&d.SourceBindingDigest, &d.TargetBindingRevision, &d.TargetPolicyID, &d.TargetPolicyRevision,
		&d.TargetPolicyDigest, &d.RequestDigest, &d.DecidedBy, &d.AuthorizationReference, &d.DecisionReason, &d.DecisionDigest)
	return d, err
}

// DecideVMAvailabilityRebind commits the ACCEPTED Decision, next immutable
// Binding revision, and current pointer switch in one transaction.
func DecideVMAvailabilityRebind(ctx context.Context, db TxBeginner, rebindID, decidedBy string) (VMAvailabilityRebindDecision, VMAvailabilityBinding, error) {
	if rebindID == "" || decidedBy == "" {
		return VMAvailabilityRebindDecision{}, VMAvailabilityBinding{}, ErrAvailabilityRebindUnauthorized
	}
	var decision VMAvailabilityRebindDecision
	var binding VMAvailabilityBinding
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-rebind/"+rebindID); err != nil {
			return err
		}
		existing, err := loadAvailabilityRebindDecisionTx(ctx, tx, rebindID)
		if err == nil {
			decision = existing
			if existing.DecisionState != "ACCEPTED" {
				return ErrAvailabilityRebindConflict
			}
			binding, err = loadVMAvailabilityBindingRevisionTx(ctx, tx, existing.WorkloadID, existing.TargetBindingRevision)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var request VMAvailabilityRebindRequest
		if err := tx.QueryRow(ctx, `SELECT rebind_id,workload_id,expected_current_binding_revision,source_binding_digest,
			target_policy_id,target_policy_revision,target_policy_digest,requested_by,authorized_by,
			authorization_reference,reason,request_digest FROM kim.vm_availability_rebind_request_evidence WHERE rebind_id=$1`, rebindID).Scan(
			&request.RebindID, &request.WorkloadID, &request.ExpectedCurrentBindingRevision, &request.SourceBindingDigest,
			&request.TargetPolicyID, &request.TargetPolicyRevision, &request.TargetPolicyDigest, &request.RequestedBy,
			&request.AuthorizedBy, &request.AuthorizationReference, &request.Reason, &request.RequestDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-binding/"+request.WorkloadID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-policy/"+request.TargetPolicyID); err != nil {
			return err
		}
		var currentRevision uint64
		var currentDigest string
		if err := tx.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1 FOR UPDATE`, request.WorkloadID).Scan(&currentRevision, &currentDigest); err != nil || currentRevision != request.ExpectedCurrentBindingRevision || currentDigest != request.SourceBindingDigest {
			return ErrAvailabilityRebindStaleSource
		}
		var lifecycle, responsibility, action string
		if err := tx.QueryRow(ctx, `SELECT c.lifecycle_state,e.responsibility,e.host_failure_action
			FROM kim.availability_policies_current c JOIN kim.availability_policy_revision_evidence e
			ON e.policy_id=c.policy_id AND e.policy_revision=c.policy_revision AND e.policy_digest=c.policy_digest
			WHERE c.policy_id=$1 AND c.policy_revision=$2 AND c.policy_digest=$3 FOR SHARE OF c,e`, request.TargetPolicyID, request.TargetPolicyRevision, request.TargetPolicyDigest).Scan(&lifecycle, &responsibility, &action); err != nil {
			return ErrAvailabilityRebindStaleTarget
		}
		if lifecycle != "ACTIVE" {
			return ErrAvailabilityRebindPolicyNotActive
		}
		if !((responsibility == "INFRASTRUCTURE_MANAGED" && (action == "RESTART_ON_OTHER_HOST" || action == "EVACUATE")) || ((responsibility == "WORKLOAD_MANAGED" || responsibility == "MANUAL") && action == "NO_AUTOMATIC_ACTION")) {
			return ErrAvailabilityRebindInvalidTarget
		}
		source, err := loadVMAvailabilityBindingRevisionTx(ctx, tx, request.WorkloadID, currentRevision)
		if err != nil || source.BindingDigest != currentDigest {
			return ErrAvailabilityRebindStaleSource
		}
		var admissionWorkload, allocationWorkload string
		if err := tx.QueryRow(ctx, `SELECT d.workload_id,c.workload_id FROM kim.placement_admission_decisions d
			JOIN kim.compute_allocation_claims c ON c.allocation_id=$2 AND c.admission_id=d.admission_id
			WHERE d.admission_id=$1`, source.AdmissionID, source.AllocationID).Scan(&admissionWorkload, &allocationWorkload); err != nil || admissionWorkload != request.WorkloadID || allocationWorkload != request.WorkloadID {
			return ErrAvailabilityRebindConflict
		}
		decision = VMAvailabilityRebindDecision{RebindID: rebindID, DecisionGeneration: 1, WorkloadID: request.WorkloadID,
			DecisionState: "ACCEPTED", ResultCode: "ACCEPTED", SourceBindingRevision: currentRevision,
			SourceBindingDigest: currentDigest, TargetBindingRevision: currentRevision + 1, TargetPolicyID: request.TargetPolicyID,
			TargetPolicyRevision: request.TargetPolicyRevision, TargetPolicyDigest: request.TargetPolicyDigest,
			RequestDigest: request.RequestDigest, DecidedBy: decidedBy, AuthorizationReference: request.AuthorizationReference,
			DecisionReason: request.Reason}
		rawDecision, _ := json.Marshal(decision)
		decision.DecisionDigest = digestReleaseBytes(rawDecision)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_availability_rebind_decision_evidence(
			rebind_id,decision_generation,workload_id,decision_state,result_code,source_binding_revision,
			source_binding_digest,target_binding_revision,target_policy_id,target_policy_revision,target_policy_digest,
			request_digest,decided_by,authorization_reference,decision_reason,decision_digest)
			VALUES($1,1,$2,'ACCEPTED','ACCEPTED',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, decision.RebindID,
			decision.WorkloadID, decision.SourceBindingRevision, decision.SourceBindingDigest, decision.TargetBindingRevision,
			decision.TargetPolicyID, decision.TargetPolicyRevision, decision.TargetPolicyDigest, decision.RequestDigest,
			decision.DecidedBy, decision.AuthorizationReference, decision.DecisionReason, decision.DecisionDigest); err != nil {
			return err
		}
		binding = VMAvailabilityBinding{WorkloadID: request.WorkloadID, BindingRevision: decision.TargetBindingRevision,
			AdmissionID: source.AdmissionID, AllocationID: source.AllocationID, PolicyID: request.TargetPolicyID,
			PolicyRevision: request.TargetPolicyRevision, PolicyDigest: request.TargetPolicyDigest, Responsibility: responsibility,
			HostFailureAction: action, BindingSource: "EXPLICIT_REBIND", SourceBindingRevision: currentRevision,
			SourceBindingDigest: currentDigest, RebindID: rebindID, RebindDecisionGeneration: 1}
		rawBinding, _ := json.Marshal(binding)
		binding.BindingDigest = digestReleaseBytes(rawBinding)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_availability_binding_evidence(workload_id,binding_revision,
			admission_id,allocation_id,policy_resolution_id,availability_policy_id,availability_policy_revision,
			availability_policy_digest,responsibility,host_failure_action,resolution_input_digest,binding_digest,
			binding_source,source_binding_revision,source_binding_digest,rebind_id,rebind_decision_generation)
			VALUES($1,$2,$3,$4,NULL,$5,$6,$7,$8,$9,NULL,$10,'EXPLICIT_REBIND',$11,$12,$13,1)`, binding.WorkloadID,
			binding.BindingRevision, binding.AdmissionID, binding.AllocationID, binding.PolicyID, binding.PolicyRevision,
			binding.PolicyDigest, binding.Responsibility, binding.HostFailureAction, binding.BindingDigest,
			binding.SourceBindingRevision, binding.SourceBindingDigest, binding.RebindID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE kim.vm_availability_bindings_current SET binding_revision=$2,binding_digest=$3,
			updated_at=statement_timestamp() WHERE workload_id=$1 AND binding_revision=$4 AND binding_digest=$5`, binding.WorkloadID,
			binding.BindingRevision, binding.BindingDigest, currentRevision, currentDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrAvailabilityRebindStaleSource
		}
		return nil
	})
	if err != nil {
		return VMAvailabilityRebindDecision{}, VMAvailabilityBinding{}, err
	}
	return decision, binding, nil
}

func loadVMAvailabilityBindingRevisionTx(ctx context.Context, tx pgx.Tx, workloadID string, revision uint64) (VMAvailabilityBinding, error) {
	var b VMAvailabilityBinding
	err := tx.QueryRow(ctx, `SELECT workload_id,binding_revision,admission_id,allocation_id,COALESCE(policy_resolution_id,''),
		availability_policy_id,availability_policy_revision,availability_policy_digest,responsibility,host_failure_action,
		COALESCE(resolution_input_digest,''),binding_digest,binding_source,COALESCE(source_binding_revision,0),
		COALESCE(source_binding_digest,''),COALESCE(rebind_id,''),COALESCE(rebind_decision_generation,0)
		FROM kim.vm_availability_binding_evidence WHERE workload_id=$1 AND binding_revision=$2`, workloadID, revision).Scan(
		&b.WorkloadID, &b.BindingRevision, &b.AdmissionID, &b.AllocationID, &b.PolicyResolutionID, &b.PolicyID,
		&b.PolicyRevision, &b.PolicyDigest, &b.Responsibility, &b.HostFailureAction, &b.ResolutionInputDigest,
		&b.BindingDigest, &b.BindingSource, &b.SourceBindingRevision, &b.SourceBindingDigest, &b.RebindID, &b.RebindDecisionGeneration)
	return b, err
}

func (d VMAvailabilityRebindDecision) String() string {
	return fmt.Sprintf("%s/%s", d.RebindID, d.ResultCode)
}
