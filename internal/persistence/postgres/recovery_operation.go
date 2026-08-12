package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/statemarker"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

var (
	ErrRecoveryOperationConflict    = errors.New("Recovery Operation authority conflict")
	ErrRecoveryOperationStale       = errors.New("Recovery Operation safety authority is stale")
	ErrRecoveryOperationBlocked     = errors.New("Recovery Operation is blocked")
	ErrRecoveryOperationUnsupported = errors.New("Recovery Operation backend is not implemented")
)

type RecoveryOperationRequest struct {
	RecoveryOperationID, FailureEpochID, EligibilityDecisionID, EligibilityDecisionDigest string
	RecoveryBudgetClaimID, RecoveryBudgetClaimDigest, RequestedAction, RequestedBy        string
	RecoveryBudgetClaimGeneration                                                         uint64
	RequestDigest                                                                         string
}

type RecoveryOperation struct {
	RecoveryOperationID, FailureEpochID, EligibilityDecisionID, EligibilityDecisionDigest string
	RecoveryBudgetClaimID, RecoveryBudgetClaimDigest, RecoveryAction                      string
	AvailabilityBindingDigest, AvailabilityPolicyID, AvailabilityPolicyDigest             string
	SourceHostID, DestinationHostID, PlanID, OperationDigest, LifecycleState              string
	OperationGeneration, RecoveryBudgetClaimGeneration                                    uint64
	AvailabilityBindingRevision, AvailabilityPolicyRevision, StateGeneration              uint64
}

type RecoveryPlan struct {
	PlanID, RecoveryOperationID, SourceHostID, DestinationHostID, RecoveryAction string
	DestinationRequestDigest, DestinationCandidateDigest                         string
	DestinationSnapshotDigest, PlacementScopeID, PlacementScopeDigest            string
	AvailabilityPolicyID, AvailabilityPolicyDigest, PlanDigest                   string
	PlanRevision, PlacementScopeGeneration, AvailabilityPolicyRevision           uint64
	DestinationRequest                                                           PlacementAdmissionRequest
}

type RecoveryOperationStart struct {
	RecoveryOperationID, PlanID, DestinationHostID, DestinationAdmissionID string
	ExecutionJobID, ExecutionCommandID, BudgetClaimID, LifecycleState      string
	BudgetStateGeneration, OperationStateGeneration                        uint64
}

type RecoveryDangerousStepEvaluation struct {
	EvaluationID, RecoveryOperationID, FencingProofID, FencingProofDigest string
	StorageSafetyProofID, StorageSafetyProofDigest, BudgetClaimID         string
	DestinationAdmissionID, ResultState, ReasonCode                       string
	FencingUsability, StorageUsability, EvaluatorDigest, EvaluationDigest string
	OperationStateGeneration, BudgetStateGeneration                       uint64
}

func RecordRecoveryOperationRequest(ctx context.Context, db TxBeginner, operationID, decisionID, claimID, action, requestedBy string) (RecoveryOperationRequest, error) {
	var out RecoveryOperationRequest
	if operationID == "" || decisionID == "" || claimID == "" || requestedBy == "" || (action != "RESTART_ON_OTHER_HOST" && action != "EVACUATE") {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+operationID); err != nil {
			return err
		}
		var responsibility, historicalAction string
		err := tx.QueryRow(ctx, `SELECT d.failure_epoch_id,d.decision_digest,c.claim_generation,c.claim_digest,b.responsibility,b.host_failure_action FROM kim.recovery_eligibility_decision_evidence d JOIN kim.recovery_budget_claim_evidence c ON c.decision_id=d.decision_id AND c.claim_id=$2 JOIN kim.failure_epoch_evidence e ON e.failure_epoch_id=d.failure_epoch_id JOIN kim.vm_availability_binding_evidence b ON b.workload_id=e.workload_id AND b.binding_revision=e.availability_binding_revision AND b.binding_digest=e.availability_binding_digest WHERE d.decision_id=$1 AND d.decision_state='ACCEPTED' AND d.result_state='ELIGIBLE'`, decisionID, claimID).Scan(&out.FailureEpochID, &out.EligibilityDecisionDigest, &out.RecoveryBudgetClaimGeneration, &out.RecoveryBudgetClaimDigest, &responsibility, &historicalAction)
		if err != nil || responsibility != "INFRASTRUCTURE_MANAGED" || historicalAction != action {
			return ErrRecoveryOperationBlocked
		}
		out.RecoveryOperationID, out.EligibilityDecisionID = operationID, decisionID
		out.RecoveryBudgetClaimID, out.RequestedAction, out.RequestedBy = claimID, action, requestedBy
		copy := out
		copy.RequestDigest = ""
		raw, _ := json.Marshal(copy)
		out.RequestDigest = digestReleaseBytes(raw)
		var existingDigest string
		err = tx.QueryRow(ctx, `SELECT request_digest FROM kim.recovery_operation_request_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&existingDigest)
		if err == nil {
			if existingDigest != out.RequestDigest {
				return ErrRecoveryOperationConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.recovery_operation_request_evidence(recovery_operation_id,failure_epoch_id,eligibility_decision_id,eligibility_decision_digest,recovery_budget_claim_id,recovery_budget_claim_generation,recovery_budget_claim_digest,requested_action,requested_by,request_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, out.RecoveryOperationID, out.FailureEpochID, out.EligibilityDecisionID, out.EligibilityDecisionDigest, out.RecoveryBudgetClaimID, out.RecoveryBudgetClaimGeneration, out.RecoveryBudgetClaimDigest, out.RequestedAction, out.RequestedBy, out.RequestDigest)
		return err
	})
	return out, err
}

func recoveryPlacementRequest(operationID string, source PlacementAdmissionRequest) PlacementAdmissionRequest {
	request := source
	request.RequestID = "recovery-placement:" + operationID
	request.PlacementScopeGeneration, request.PlacementScopeDigest, request.VisibilityProvenanceDigest = 0, "", ""
	for index := range request.Network {
		request.Network[index].PortID = fmt.Sprintf("recovery-port:%s:%d", operationID, index+1)
		request.Network[index].AllocationSource = "AUTOMATIC"
		request.Network[index].IPAddress, request.Network[index].MACAddress = "", ""
	}
	for index := range request.Storage {
		request.Storage[index].VolumeID = recoveryPlacementResourceID("volume", operationID, index+1)
		request.Storage[index].AttachmentID = recoveryPlacementResourceID("attachment", operationID, index+1)
	}
	return request
}

func recoveryPlacementResourceID(kind, operationID string, index int) string {
	digest := digestReleaseBytes([]byte(operationID))
	return fmt.Sprintf("recovery-%s-%s-%d", kind, digest[:32], index)
}

// addRecoveryBootStorageRequirementTx converts the source boot-volume shape
// into an ordinary destination Placement storage requirement. It selects no
// backend outside the fixed destination Host and creates no storage authority;
// Final Admission remains the only reservation boundary.
func addRecoveryBootStorageRequirementTx(ctx context.Context, tx pgx.Tx, operationID, sourceAdmissionID, destinationHostID string, request *PlacementAdmissionRequest) error {
	if len(request.Storage) != 0 {
		return nil
	}
	var classID, accessMode string
	var classRevision, fencingRevision, sizeBytes uint64
	var bootable bool
	if err := tx.QueryRow(ctx, `SELECT v.storage_class_id,v.storage_class_revision,v.size_bytes,v.access_mode,v.bootable,c.fencing_policy_revision FROM kim.volumes_current v JOIN kim.storage_class_revision_evidence c ON c.storage_class_id=v.storage_class_id AND c.class_revision=v.storage_class_revision WHERE v.placement_admission_id=$1 AND v.bootable AND v.lifecycle_state IN ('RESERVED','CREATING','AVAILABLE')`, sourceAdmissionID).Scan(&classID, &classRevision, &sizeBytes, &accessMode, &bootable, &fencingRevision); err != nil {
		return ErrRecoveryOperationBlocked
	}
	var backendID, vgUUID string
	var backendGeneration, capacityGeneration uint64
	var candidates int
	if err := tx.QueryRow(ctx, `SELECT count(*),coalesce(min(b.backend_id),''),coalesce(min(b.vg_uuid),''),coalesce(min(b.backend_generation),0),coalesce(min(p.capacity_generation),0) FROM kim.storage_backends_current b JOIN kim.storage_capacity_projections_current p ON p.backend_id=b.backend_id AND p.projection_state='CURRENT' JOIN kim.storage_classes_current cc ON cc.storage_class_id=$2 AND cc.class_revision=$3 AND cc.lifecycle_state='ACTIVE' WHERE b.host_id=$1 AND b.backend_type='LOCAL_LVM' AND b.lifecycle_state='ACTIVE' AND b.capability_state='CURRENT'`, destinationHostID, classID, classRevision).Scan(&candidates, &backendID, &vgUUID, &backendGeneration, &capacityGeneration); err != nil || candidates != 1 {
		return ErrRecoveryOperationBlocked
	}
	request.Storage = []placement.StorageRequirement{{
		VolumeID: recoveryPlacementResourceID("volume", operationID, 1), AttachmentID: recoveryPlacementResourceID("attachment", operationID, 1),
		BackendID: backendID, VGUUID: vgUUID, StorageClassID: classID,
		BackendGeneration: backendGeneration, StorageClassRevision: classRevision,
		CapacityGeneration: capacityGeneration, AttachmentGeneration: 1,
		FencingPolicyRevision: fencingRevision, SizeBytes: sizeBytes,
		AccessMode: accessMode, Bootable: bootable,
	}}
	return nil
}

func recoveryPlanCandidateDigest(candidate AvailabilityPlacementCandidate) string {
	p := candidate.Placement
	provenance := append([]PlacementVisibilityProvenance(nil), p.Provenance...)
	sort.Slice(provenance, func(i, j int) bool { return provenance[i].HostGroupID < provenance[j].HostGroupID })
	raw, _ := json.Marshal(map[string]any{"host_id": p.HostID, "eligible": p.Eligible, "evaluation_digest": p.Evaluation.EvaluationDigest, "request_digest": p.Evaluation.RequestDigest, "provenance": provenance, "availability_result": candidate.AvailabilityStatus, "availability_policy_id": candidate.AvailabilityResolution.EffectivePolicyID, "availability_policy_revision": candidate.AvailabilityResolution.EffectivePolicyRevision, "availability_policy_digest": candidate.AvailabilityResolution.EffectivePolicyDigest, "availability_resolution_digest": candidate.AvailabilityResolution.ResolutionDigest})
	return digestReleaseBytes(raw)
}

func selectRecoveryDestination(dry AvailabilityPlacementDryResult, hostID string) (AvailabilityPlacementCandidate, error) {
	for _, candidate := range dry.Candidates {
		if candidate.Placement.HostID == hostID {
			if !candidate.Placement.Eligible || candidate.AvailabilityStatus != "RESOLVED" {
				return AvailabilityPlacementCandidate{}, ErrRecoveryOperationBlocked
			}
			return candidate, nil
		}
	}
	return AvailabilityPlacementCandidate{}, ErrRecoveryOperationBlocked
}

func PlanRecoveryOperation(ctx context.Context, db TxBeginner, operationID, planID, destinationHostID string) (RecoveryOperation, RecoveryPlan, error) {
	var operation RecoveryOperation
	var plan RecoveryPlan
	if operationID == "" || planID == "" || destinationHostID == "" {
		return operation, plan, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+operationID); err != nil {
			return err
		}
		var request RecoveryOperationRequest
		if err := tx.QueryRow(ctx, `SELECT recovery_operation_id,failure_epoch_id,eligibility_decision_id,eligibility_decision_digest,recovery_budget_claim_id,recovery_budget_claim_generation,recovery_budget_claim_digest,requested_action,requested_by,request_digest FROM kim.recovery_operation_request_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&request.RecoveryOperationID, &request.FailureEpochID, &request.EligibilityDecisionID, &request.EligibilityDecisionDigest, &request.RecoveryBudgetClaimID, &request.RecoveryBudgetClaimGeneration, &request.RecoveryBudgetClaimDigest, &request.RequestedAction, &request.RequestedBy, &request.RequestDigest); err != nil {
			return err
		}
		var existingPlanID string
		err := tx.QueryRow(ctx, `SELECT plan_id FROM kim.recovery_operation_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&existingPlanID)
		if err == nil {
			if existingPlanID != planID {
				return ErrRecoveryOperationConflict
			}
			return loadRecoveryOperationPlanTx(ctx, tx, operationID, &operation, &plan)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var epoch FailureEpoch
		epoch, err = loadFailureEpochTx(ctx, tx, request.FailureEpochID)
		if err != nil || epoch.SourceHostID == destinationHostID {
			return ErrRecoveryOperationBlocked
		}
		var evaluationID, destinationSnapshotDigest string
		var eligibilityRequestRaw []byte
		if err := tx.QueryRow(ctx, `SELECT d.evaluation_id,d.destination_snapshot_digest,e.destination_request FROM kim.recovery_eligibility_decision_evidence d JOIN kim.recovery_eligibility_evaluation_evidence e ON e.evaluation_id=d.evaluation_id WHERE d.decision_id=$1 AND d.decision_digest=$2`, request.EligibilityDecisionID, request.EligibilityDecisionDigest).Scan(&evaluationID, &destinationSnapshotDigest, &eligibilityRequestRaw); err != nil {
			return ErrRecoveryOperationConflict
		}
		var eligibilityCandidateDigest string
		if err := tx.QueryRow(ctx, `SELECT candidate_digest FROM kim.recovery_eligibility_destination_candidate_evidence WHERE evaluation_id=$1 AND host_id=$2 AND candidate_state='ELIGIBLE'`, evaluationID, destinationHostID).Scan(&eligibilityCandidateDigest); err != nil {
			return ErrRecoveryOperationBlocked
		}
		var baseRequest PlacementAdmissionRequest
		if err := json.Unmarshal(eligibilityRequestRaw, &baseRequest); err != nil {
			return err
		}
		destinationRequest := recoveryPlacementRequest(operationID, baseRequest)
		if err := addRecoveryBootStorageRequirementTx(ctx, tx, operationID, epoch.AdmissionID, destinationHostID, &destinationRequest); err != nil {
			return err
		}
		dry, err := DryEvaluateAvailabilityPlacementScope(ctx, scopeTxBeginner{tx}, destinationRequest)
		if err != nil {
			return err
		}
		candidate, err := selectRecoveryDestination(dry, destinationHostID)
		if err != nil || candidate.AvailabilityResolution.EffectivePolicyID != epoch.PolicyID || candidate.AvailabilityResolution.EffectivePolicyRevision != epoch.PolicyRevision || candidate.AvailabilityResolution.EffectivePolicyDigest != epoch.PolicyDigest {
			return ErrRecoveryOperationBlocked
		}
		requestRaw, _ := json.Marshal(destinationRequest)
		plan = RecoveryPlan{PlanID: planID, RecoveryOperationID: operationID, PlanRevision: 1, SourceHostID: epoch.SourceHostID, DestinationHostID: destinationHostID, RecoveryAction: request.RequestedAction, DestinationRequest: destinationRequest, DestinationRequestDigest: digestReleaseBytes(requestRaw), DestinationCandidateDigest: recoveryPlanCandidateDigest(candidate), DestinationSnapshotDigest: destinationSnapshotDigest, PlacementScopeID: dry.Scope.PlacementScopeID, PlacementScopeGeneration: dry.Scope.ScopeGeneration, PlacementScopeDigest: dry.Scope.ScopeDigest, AvailabilityPolicyID: epoch.PolicyID, AvailabilityPolicyRevision: epoch.PolicyRevision, AvailabilityPolicyDigest: epoch.PolicyDigest}
		planCopy := plan
		planCopy.PlanDigest = ""
		raw, _ := json.Marshal(planCopy)
		plan.PlanDigest = digestReleaseBytes(raw)
		operation = RecoveryOperation{RecoveryOperationID: operationID, OperationGeneration: 1, FailureEpochID: epoch.FailureEpochID, EligibilityDecisionID: request.EligibilityDecisionID, EligibilityDecisionDigest: request.EligibilityDecisionDigest, RecoveryBudgetClaimID: request.RecoveryBudgetClaimID, RecoveryBudgetClaimGeneration: request.RecoveryBudgetClaimGeneration, RecoveryBudgetClaimDigest: request.RecoveryBudgetClaimDigest, AvailabilityBindingRevision: epoch.AvailabilityBindingRevision, AvailabilityBindingDigest: epoch.AvailabilityBindingDigest, AvailabilityPolicyID: epoch.PolicyID, AvailabilityPolicyRevision: epoch.PolicyRevision, AvailabilityPolicyDigest: epoch.PolicyDigest, RecoveryAction: request.RequestedAction, SourceHostID: epoch.SourceHostID, DestinationHostID: destinationHostID, PlanID: planID, LifecycleState: "PLANNED", StateGeneration: 1}
		operationCopy := operation
		operationCopy.OperationDigest, operationCopy.LifecycleState, operationCopy.StateGeneration = "", "", 0
		raw, _ = json.Marshal(operationCopy)
		operation.OperationDigest = digestReleaseBytes(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_operation_evidence(recovery_operation_id,operation_generation,request_digest,failure_epoch_id,eligibility_decision_id,eligibility_decision_digest,recovery_budget_claim_id,recovery_budget_claim_generation,recovery_budget_claim_digest,availability_binding_revision,availability_binding_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,recovery_action,source_host_id,selected_destination_host_id,plan_id,operation_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, operationID, request.RequestDigest, epoch.FailureEpochID, request.EligibilityDecisionID, request.EligibilityDecisionDigest, request.RecoveryBudgetClaimID, request.RecoveryBudgetClaimGeneration, request.RecoveryBudgetClaimDigest, epoch.AvailabilityBindingRevision, epoch.AvailabilityBindingDigest, epoch.PolicyID, epoch.PolicyRevision, epoch.PolicyDigest, request.RequestedAction, epoch.SourceHostID, destinationHostID, planID, operation.OperationDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_plan_evidence(plan_id,recovery_operation_id,plan_revision,source_host_id,destination_host_id,recovery_action,destination_request,destination_request_digest,destination_candidate_digest,destination_snapshot_digest,placement_scope_id,placement_scope_generation,placement_scope_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,plan_digest) VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, plan.PlanID, operationID, plan.SourceHostID, plan.DestinationHostID, plan.RecoveryAction, plan.DestinationRequest, plan.DestinationRequestDigest, plan.DestinationCandidateDigest, plan.DestinationSnapshotDigest, plan.PlacementScopeID, plan.PlacementScopeGeneration, plan.PlacementScopeDigest, plan.AvailabilityPolicyID, plan.AvailabilityPolicyRevision, plan.AvailabilityPolicyDigest, plan.PlanDigest); err != nil {
			return err
		}
		transitionDigest := digestReleaseBytes([]byte(operationID + "/1/PLANNED/" + planID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_operation_transition_evidence(recovery_operation_id,state_generation,from_state,to_state,reason_code,cause_type,cause_id,transition_digest) VALUES($1,1,NULL,'PLANNED','immutable_plan_accepted','PLAN',$2,$3)`, operationID, planID, transitionDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.recovery_operations_current(recovery_operation_id,operation_generation,plan_id,lifecycle_state,state_generation) VALUES($1,1,$2,'PLANNED',1)`, operationID, planID)
		return err
	})
	return operation, plan, err
}

func loadRecoveryOperationPlanTx(ctx context.Context, tx pgx.Tx, operationID string, operation *RecoveryOperation, plan *RecoveryPlan) error {
	var requestRaw []byte
	if err := tx.QueryRow(ctx, `SELECT o.recovery_operation_id,o.operation_generation,o.failure_epoch_id,o.eligibility_decision_id,o.eligibility_decision_digest,o.recovery_budget_claim_id,o.recovery_budget_claim_generation,o.recovery_budget_claim_digest,o.availability_binding_revision,o.availability_binding_digest,o.availability_policy_id,o.availability_policy_revision,o.availability_policy_digest,o.recovery_action,o.source_host_id,o.selected_destination_host_id,o.plan_id,o.operation_digest,c.lifecycle_state,c.state_generation FROM kim.recovery_operation_evidence o JOIN kim.recovery_operations_current c USING(recovery_operation_id) WHERE o.recovery_operation_id=$1`, operationID).Scan(&operation.RecoveryOperationID, &operation.OperationGeneration, &operation.FailureEpochID, &operation.EligibilityDecisionID, &operation.EligibilityDecisionDigest, &operation.RecoveryBudgetClaimID, &operation.RecoveryBudgetClaimGeneration, &operation.RecoveryBudgetClaimDigest, &operation.AvailabilityBindingRevision, &operation.AvailabilityBindingDigest, &operation.AvailabilityPolicyID, &operation.AvailabilityPolicyRevision, &operation.AvailabilityPolicyDigest, &operation.RecoveryAction, &operation.SourceHostID, &operation.DestinationHostID, &operation.PlanID, &operation.OperationDigest, &operation.LifecycleState, &operation.StateGeneration); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT plan_id,recovery_operation_id,plan_revision,source_host_id,destination_host_id,recovery_action,destination_request,destination_request_digest,destination_candidate_digest,destination_snapshot_digest,placement_scope_id,placement_scope_generation,placement_scope_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,plan_digest FROM kim.recovery_plan_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&plan.PlanID, &plan.RecoveryOperationID, &plan.PlanRevision, &plan.SourceHostID, &plan.DestinationHostID, &plan.RecoveryAction, &requestRaw, &plan.DestinationRequestDigest, &plan.DestinationCandidateDigest, &plan.DestinationSnapshotDigest, &plan.PlacementScopeID, &plan.PlacementScopeGeneration, &plan.PlacementScopeDigest, &plan.AvailabilityPolicyID, &plan.AvailabilityPolicyRevision, &plan.AvailabilityPolicyDigest, &plan.PlanDigest); err != nil {
		return err
	}
	return json.Unmarshal(requestRaw, &plan.DestinationRequest)
}

func StartRecoveryOperation(ctx context.Context, db TxBeginner, operationID, jobID, commandID string) (RecoveryOperationStart, error) {
	var out RecoveryOperationStart
	if operationID == "" || jobID == "" || commandID == "" {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+operationID); err != nil {
			return err
		}
		var operation RecoveryOperation
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, operationID, &operation, &plan); err != nil {
			return err
		}
		if operation.LifecycleState != "PLANNED" {
			if err := tx.QueryRow(ctx, `SELECT destination_admission_id,execution_job_id,execution_command_id,lifecycle_state,state_generation FROM kim.recovery_operations_current WHERE recovery_operation_id=$1`, operationID).Scan(&out.DestinationAdmissionID, &out.ExecutionJobID, &out.ExecutionCommandID, &out.LifecycleState, &out.OperationStateGeneration); err != nil || out.ExecutionJobID != jobID || out.ExecutionCommandID != commandID {
				return ErrRecoveryOperationConflict
			}
			if err := tx.QueryRow(ctx, `SELECT claim_id,state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$1`, operation.RecoveryBudgetClaimID).Scan(&out.BudgetClaimID, &out.BudgetStateGeneration); err != nil {
				return err
			}
			out.RecoveryOperationID, out.PlanID, out.DestinationHostID = operationID, plan.PlanID, plan.DestinationHostID
			return nil
		}
		if operation.RecoveryAction != "RESTART_ON_OTHER_HOST" {
			return ErrRecoveryOperationUnsupported
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-epoch/"+operation.FailureEpochID); err != nil {
			return err
		}
		epoch, err := loadFailureEpochTx(ctx, tx, operation.FailureEpochID)
		if err != nil || epoch.EpochState != "FENCED" || epoch.AvailabilityBindingRevision != operation.AvailabilityBindingRevision || epoch.AvailabilityBindingDigest != operation.AvailabilityBindingDigest || epoch.PolicyID != operation.AvailabilityPolicyID || epoch.PolicyRevision != operation.AvailabilityPolicyRevision || epoch.PolicyDigest != operation.AvailabilityPolicyDigest {
			return ErrRecoveryOperationStale
		}
		var responsibility, action string
		if err := tx.QueryRow(ctx, `SELECT responsibility,host_failure_action FROM kim.vm_availability_binding_evidence WHERE workload_id=$1 AND binding_revision=$2 AND binding_digest=$3`, epoch.WorkloadID, operation.AvailabilityBindingRevision, operation.AvailabilityBindingDigest).Scan(&responsibility, &action); err != nil || responsibility != "INFRASTRUCTURE_MANAGED" || action != operation.RecoveryAction {
			return ErrRecoveryOperationBlocked
		}
		fencingID, fencingDigest, fencingUsability, err := loadFencingProofUsabilityTx(ctx, tx, epoch)
		if err != nil || fencingUsability != "USABLE" {
			return ErrRecoveryOperationStale
		}
		storageID, storageDigest, storageUsability, err := loadStorageProofUsabilityTx(ctx, tx, epoch)
		if err != nil || storageUsability != "USABLE" {
			return ErrRecoveryOperationStale
		}
		var decisionFencingID, decisionFencingDigest, decisionStorageID, decisionStorageDigest string
		if err := tx.QueryRow(ctx, `SELECT fencing_proof_id,fencing_proof_digest,storage_safety_proof_id,storage_safety_proof_digest FROM kim.recovery_eligibility_decision_evidence WHERE decision_id=$1 AND decision_digest=$2`, operation.EligibilityDecisionID, operation.EligibilityDecisionDigest).Scan(&decisionFencingID, &decisionFencingDigest, &decisionStorageID, &decisionStorageDigest); err != nil || decisionFencingID != fencingID || decisionFencingDigest != fencingDigest || decisionStorageID != storageID || decisionStorageDigest != storageDigest {
			return ErrRecoveryOperationStale
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-budget-claim/"+operation.RecoveryBudgetClaimID); err != nil {
			return err
		}
		var claimGeneration, stateGeneration uint64
		var claimDigest, claimState, claimDecision string
		if err := tx.QueryRow(ctx, `SELECT c.claim_generation,c.state_generation,c.claim_digest,c.claim_state,c.decision_id FROM kim.recovery_budget_claims_current c WHERE c.claim_id=$1 FOR UPDATE`, operation.RecoveryBudgetClaimID).Scan(&claimGeneration, &stateGeneration, &claimDigest, &claimState, &claimDecision); err != nil || claimGeneration != operation.RecoveryBudgetClaimGeneration || stateGeneration != 1 || claimDigest != operation.RecoveryBudgetClaimDigest || claimState != "RESERVED" || claimDecision != operation.EligibilityDecisionID {
			return ErrRecoveryOperationStale
		}
		dry, err := DryEvaluateAvailabilityPlacementScope(ctx, scopeTxBeginner{tx}, plan.DestinationRequest)
		if err != nil {
			return err
		}
		candidate, err := selectRecoveryDestination(dry, plan.DestinationHostID)
		if err != nil || recoveryPlanCandidateDigest(candidate) != plan.DestinationCandidateDigest || dry.Scope.ScopeGeneration != plan.PlacementScopeGeneration || dry.Scope.ScopeDigest != plan.PlacementScopeDigest || candidate.AvailabilityResolution.EffectivePolicyID != plan.AvailabilityPolicyID || candidate.AvailabilityResolution.EffectivePolicyRevision != plan.AvailabilityPolicyRevision || candidate.AvailabilityResolution.EffectivePolicyDigest != plan.AvailabilityPolicyDigest {
			return ErrRecoveryOperationStale
		}
		var sourceAllocationID, priorClaimState string
		if err := tx.QueryRow(ctx, `SELECT allocation_id,claim_state FROM kim.compute_allocation_claims WHERE admission_id=$1 FOR UPDATE`, epoch.AdmissionID).Scan(&sourceAllocationID, &priorClaimState); err != nil || (priorClaimState != "RESERVED" && priorClaimState != "ALLOCATED") {
			return ErrRecoveryOperationStale
		}
		releaseDigest := digestReleaseBytes([]byte(operationID + "/" + epoch.AdmissionID + "/" + sourceAllocationID + "/" + priorClaimState + "/RELEASED"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_source_compute_release_evidence(recovery_operation_id,source_admission_id,allocation_id,prior_claim_state,release_digest) VALUES($1,$2,$3,$4,$5)`, operationID, epoch.AdmissionID, sourceAllocationID, priorClaimState, releaseDigest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.compute_allocation_claims SET claim_state='RELEASED',released_at=statement_timestamp() WHERE allocation_id=$1 AND claim_state=$2`, sourceAllocationID, priorClaimState); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		admission, err := FinalAdmitPlacementScope(ctx, scopeTxBeginner{tx}, dry.Scope, plan.DestinationRequest, candidate.Placement)
		if err != nil {
			return err
		}
		admissionDigest := digestReleaseBytes([]byte(operationID + "/" + plan.PlanID + "/" + admission.AdmissionID + "/" + admission.RequestDigest + "/" + plan.DestinationHostID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_destination_admission_evidence(recovery_operation_id,plan_id,admission_id,failure_epoch_id,eligibility_decision_id,destination_host_id,admission_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, operationID, plan.PlanID, admission.AdmissionID, operation.FailureEpochID, operation.EligibilityDecisionID, plan.DestinationHostID, admissionDigest); err != nil {
			return err
		}
		payload := map[string]any{"value": "destination-admitted:" + admission.AdmissionID}
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: jobID, CommandID: commandID, HostID: plan.DestinationHostID, ResourceType: "RECOVERY_OPERATION", ResourceID: operationID, DesiredRevision: 1, CommandType: statemarker.CommandType, SchemaVersion: statemarker.SchemaVersion, TargetResourceID: "recovery-operation:" + operationID, Payload: payload}); err != nil {
			return err
		}
		payloadRaw, _ := json.Marshal(payload)
		payloadDigest := digestBytes(payloadRaw)
		executionDigest := digestReleaseBytes([]byte(operationID + "/" + jobID + "/" + commandID + "/" + payloadDigest))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_operation_execution_evidence(recovery_operation_id,job_id,command_id,execution_step,command_type,command_schema,command_payload_digest,evidence_digest) VALUES($1,$2,$3,'DESTINATION_PREPARATION',$4,$5,$6,$7)`, operationID, jobID, commandID, statemarker.CommandType, statemarker.SchemaVersion, payloadDigest, executionDigest); err != nil {
			return err
		}
		budgetTransitionDigest := digestReleaseBytes([]byte(operation.RecoveryBudgetClaimID + "/1/RESERVED/2/CONSUMED/" + operationID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_budget_claim_transition_evidence(claim_id,state_generation,from_state,to_state,recovery_operation_id,transition_digest) VALUES($1,2,'RESERVED','CONSUMED',$2,$3)`, operation.RecoveryBudgetClaimID, operationID, budgetTransitionDigest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.recovery_budget_claims_current SET claim_state='CONSUMED',state_generation=2,updated_at=statement_timestamp() WHERE claim_id=$1 AND claim_state='RESERVED' AND state_generation=1`, operation.RecoveryBudgetClaimID); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		transitionDigest := digestReleaseBytes([]byte(operationID + "/1/PLANNED/2/RUNNING/" + admission.AdmissionID + "/" + commandID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_operation_transition_evidence(recovery_operation_id,state_generation,from_state,to_state,reason_code,cause_type,cause_id,transition_digest) VALUES($1,2,'PLANNED','RUNNING','destination_admitted_and_preparation_dispatched','START_AUTHORITY',$2,$3)`, operationID, operation.EligibilityDecisionID, transitionDigest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.recovery_operations_current SET lifecycle_state='RUNNING',state_generation=2,destination_admission_id=$2,execution_job_id=$3,execution_command_id=$4,updated_at=statement_timestamp() WHERE recovery_operation_id=$1 AND lifecycle_state='PLANNED' AND state_generation=1`, operationID, admission.AdmissionID, jobID, commandID); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		out = RecoveryOperationStart{RecoveryOperationID: operationID, PlanID: plan.PlanID, DestinationHostID: plan.DestinationHostID, DestinationAdmissionID: admission.AdmissionID, ExecutionJobID: jobID, ExecutionCommandID: commandID, BudgetClaimID: operation.RecoveryBudgetClaimID, LifecycleState: "RUNNING", BudgetStateGeneration: 2, OperationStateGeneration: 2}
		return nil
	})
	return out, err
}

func RefreshRecoveryOperationExecution(ctx context.Context, db TxBeginner, operationID, verificationID string) (RecoveryOperation, error) {
	var operation RecoveryOperation
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-operation/"+operationID); err != nil {
			return err
		}
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, operationID, &operation, &plan); err != nil {
			return err
		}
		var jobID, commandID, jobState, commandState string
		if err := tx.QueryRow(ctx, `SELECT e.job_id,e.command_id,j.job_state,c.command_state FROM kim.recovery_operation_execution_evidence e JOIN kim.execution_jobs j ON j.job_id=e.job_id JOIN kim.execution_commands_current c ON c.command_id=e.command_id WHERE e.recovery_operation_id=$1`, operationID).Scan(&jobID, &commandID, &jobState, &commandState); err != nil {
			return err
		}
		targetState, reason, causeID := "", "", commandID
		if commandState == "UNKNOWN" {
			targetState, reason = "UNKNOWN", "execution_outcome_unknown_read_back_required"
		} else if verificationID != "" && commandState == "SUCCEEDED" && jobState == "SUCCEEDED" {
			var verificationState string
			if err := tx.QueryRow(ctx, `SELECT verification_state FROM kim.command_verification_evidence WHERE verification_id=$1 AND command_id=$2`, verificationID, commandID).Scan(&verificationState); err != nil || verificationState != "MATCHED" {
				return ErrRecoveryOperationStale
			}
			targetState, reason, causeID = "VERIFYING", "destination_preparation_verified_recovery_not_yet_verified", verificationID
		} else {
			return ErrRecoveryOperationBlocked
		}
		if operation.LifecycleState == targetState {
			return nil
		}
		if operation.LifecycleState != "RUNNING" && !(operation.LifecycleState == "UNKNOWN" && targetState == "VERIFYING") {
			return ErrRecoveryOperationConflict
		}
		generation := operation.StateGeneration + 1
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%s/%s", operationID, generation, operation.LifecycleState, targetState, causeID)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_operation_transition_evidence(recovery_operation_id,state_generation,from_state,to_state,reason_code,cause_type,cause_id,transition_digest) VALUES($1,$2,$3,$4,$5,'EXECUTION_OBSERVATION',$6,$7)`, operationID, generation, operation.LifecycleState, targetState, reason, causeID, digest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.recovery_operations_current SET lifecycle_state=$2,state_generation=$3,updated_at=statement_timestamp() WHERE recovery_operation_id=$1 AND lifecycle_state=$4 AND state_generation=$5`, operationID, targetState, generation, operation.LifecycleState, operation.StateGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		operation.LifecycleState, operation.StateGeneration = targetState, generation
		return nil
	})
	return operation, err
}

func EvaluateRecoveryDangerousStep(ctx context.Context, db TxBeginner, evaluationID, operationID, evaluatorDigest string) (RecoveryDangerousStepEvaluation, error) {
	var out RecoveryDangerousStepEvaluation
	if evaluationID == "" || operationID == "" || evaluatorDigest == "" {
		return out, ErrRecoveryOperationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var storedDigest string
		err := tx.QueryRow(ctx, `SELECT evaluation_digest FROM kim.recovery_dangerous_step_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&storedDigest)
		if err == nil {
			if err := tx.QueryRow(ctx, `SELECT evaluation_id,recovery_operation_id,operation_state_generation,fencing_proof_id,fencing_proof_digest,fencing_usability,storage_safety_proof_id,storage_safety_proof_digest,storage_usability,budget_claim_id,budget_state_generation,destination_admission_id,result_state,reason_code,evaluator_digest,evaluation_digest FROM kim.recovery_dangerous_step_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&out.EvaluationID, &out.RecoveryOperationID, &out.OperationStateGeneration, &out.FencingProofID, &out.FencingProofDigest, &out.FencingUsability, &out.StorageSafetyProofID, &out.StorageSafetyProofDigest, &out.StorageUsability, &out.BudgetClaimID, &out.BudgetStateGeneration, &out.DestinationAdmissionID, &out.ResultState, &out.ReasonCode, &out.EvaluatorDigest, &out.EvaluationDigest); err != nil || out.RecoveryOperationID != operationID || out.EvaluatorDigest != evaluatorDigest || out.EvaluationDigest != storedDigest {
				return ErrRecoveryOperationConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var operation RecoveryOperation
		var plan RecoveryPlan
		if err := loadRecoveryOperationPlanTx(ctx, tx, operationID, &operation, &plan); err != nil {
			return err
		}
		out = RecoveryDangerousStepEvaluation{EvaluationID: evaluationID, RecoveryOperationID: operationID, OperationStateGeneration: operation.StateGeneration, BudgetClaimID: operation.RecoveryBudgetClaimID, EvaluatorDigest: evaluatorDigest}
		epoch, err := loadFailureEpochTx(ctx, tx, operation.FailureEpochID)
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
		var claimState string
		if err := tx.QueryRow(ctx, `SELECT claim_state,state_generation FROM kim.recovery_budget_claims_current WHERE claim_id=$1`, operation.RecoveryBudgetClaimID).Scan(&claimState, &out.BudgetStateGeneration); err != nil {
			claimState = "UNKNOWN"
		}
		var destinationHost string
		if err := tx.QueryRow(ctx, `SELECT admission_id,destination_host_id FROM kim.recovery_destination_admission_evidence WHERE recovery_operation_id=$1`, operationID).Scan(&out.DestinationAdmissionID, &destinationHost); err != nil {
			out.DestinationAdmissionID = ""
		}
		switch {
		case operation.LifecycleState != "RUNNING" && operation.LifecycleState != "VERIFYING":
			out.ResultState, out.ReasonCode = "BLOCKED_OPERATION", "operation_not_at_dangerous_step"
		case out.FencingUsability != "USABLE":
			out.ResultState, out.ReasonCode = "BLOCKED_FENCING", "source_fencing_proof_not_current_usable"
		case out.StorageUsability != "USABLE":
			out.ResultState, out.ReasonCode = "BLOCKED_STORAGE", "storage_safety_proof_not_current_usable"
		case claimState != "CONSUMED" || out.BudgetStateGeneration != 2:
			out.ResultState, out.ReasonCode = "BLOCKED_BUDGET", "budget_claim_not_current_consumed"
		case out.DestinationAdmissionID == "" || destinationHost != plan.DestinationHostID:
			out.ResultState, out.ReasonCode = "BLOCKED_DESTINATION", "destination_admission_not_current_exact"
		default:
			out.ResultState, out.ReasonCode = "AUTHORIZED", "all_current_dangerous_step_inputs_satisfied"
		}
		copy := out
		copy.EvaluationDigest = ""
		raw, _ := json.Marshal(copy)
		out.EvaluationDigest = digestReleaseBytes(raw)
		_, err = tx.Exec(ctx, `INSERT INTO kim.recovery_dangerous_step_evaluation_evidence(evaluation_id,recovery_operation_id,operation_state_generation,fencing_proof_id,fencing_proof_digest,fencing_usability,storage_safety_proof_id,storage_safety_proof_digest,storage_usability,budget_claim_id,budget_state_generation,destination_admission_id,result_state,reason_code,evaluator_digest,evaluation_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, out.EvaluationID, out.RecoveryOperationID, out.OperationStateGeneration, out.FencingProofID, out.FencingProofDigest, out.FencingUsability, out.StorageSafetyProofID, out.StorageSafetyProofDigest, out.StorageUsability, out.BudgetClaimID, out.BudgetStateGeneration, out.DestinationAdmissionID, out.ResultState, out.ReasonCode, out.EvaluatorDigest, out.EvaluationDigest)
		return err
	})
	return out, err
}
