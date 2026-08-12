package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

var (
	ErrRecoveryEligibilityConflict        = errors.New("Recovery Eligibility authority conflict")
	ErrRecoveryEligibilityStale           = errors.New("Recovery Eligibility input is stale")
	ErrRecoveryEligibilityBlocked         = errors.New("Recovery Eligibility evaluation does not authorize permission")
	ErrRecoveryEligibilityBudgetExhausted = errors.New("Recovery Budget is exhausted")
)

type RecoveryBudgetPolicy struct {
	PolicyID, ScopeType, Phase, LifecycleState, CreatedBy, ApprovedBy, PolicyDigest string
	PolicyRevision                                                                  uint64
	MaxActiveRecoveries                                                             int
}

type RecoveryDestinationCandidate struct {
	HostID, CandidateState, ReasonCode                    string
	PlacementScopeID, PlacementScopeDigest                string
	VisibilityProvenanceDigest, PlacementEvaluationDigest string
	AvailabilityPolicyID, AvailabilityPolicyDigest        string
	PoolID, HierarchyID, PoolPolicyID                     string
	CandidateDigest                                       string
	PlacementScopeGeneration, CapabilityGeneration        uint64
	BaselineAssignmentGeneration, PreflightGeneration     uint64
	ComplianceGeneration, PoolGeneration                  uint64
	MembershipSetGeneration, MembershipGeneration         uint64
	HierarchyGeneration, PoolPolicyGeneration             uint64
	AvailabilityPolicyRevision                            uint64
	VisibilityProvenance                                  []PlacementVisibilityProvenance
}

type RecoveryEligibilityEvaluation struct {
	EvaluationID, FailureEpochID, EvaluatedEpochState                         string
	AvailabilityBindingDigest, AvailabilityPolicyID, AvailabilityPolicyDigest string
	Responsibility, HostFailureAction, ConfirmationDecisionID                 string
	FencingProofID, FencingProofDigest, FencingUsability                      string
	StorageSafetyProofID, StorageSafetyProofDigest, StorageUsability          string
	RecoveryBudgetPolicyID, RecoveryBudgetPolicyDigest                        string
	DestinationRequestDigest, DestinationSnapshotDigest                       string
	ResultState, ReasonCode, EvaluatorVersion, EvaluatorDigest                string
	EvaluationDigest                                                          string
	EvaluatedTransitionGeneration, AvailabilityBindingRevision                uint64
	AvailabilityPolicyRevision, RecoveryBudgetPolicyRevision                  uint64
	BudgetActiveCount, BudgetMaxActive                                        int
	DestinationCandidateCount, EligibleDestinationCount                       int
	DestinationRequest                                                        PlacementAdmissionRequest
	Candidates                                                                []RecoveryDestinationCandidate
}

type RecoveryEligibilityDecision struct {
	DecisionID, FailureEpochID, EvaluationID, FencingProofID, FencingProofDigest string
	StorageSafetyProofID, StorageSafetyProofDigest                               string
	RecoveryBudgetPolicyID, RecoveryBudgetPolicyDigest                           string
	DestinationSnapshotDigest, DecisionState, ResultState, DecidedBy             string
	EvaluationDigest, DecisionDigest, BudgetClaimID, BudgetClaimDigest           string
	ExpectedTransitionGeneration, RecoveryBudgetPolicyRevision                   uint64
	BudgetClaimGeneration                                                        uint64
}

func PublishRecoveryBudgetPolicy(ctx context.Context, db TxBeginner, p RecoveryBudgetPolicy) (RecoveryBudgetPolicy, error) {
	if p.PolicyID == "" || p.PolicyRevision == 0 || p.ScopeType != "GLOBAL" || p.Phase != "PLANNING" || p.MaxActiveRecoveries <= 0 || p.LifecycleState == "" || p.CreatedBy == "" || p.ApprovedBy == "" {
		return RecoveryBudgetPolicy{}, ErrRecoveryEligibilityConflict
	}
	copy := p
	copy.PolicyDigest = ""
	raw, _ := json.Marshal(copy)
	p.PolicyDigest = digestReleaseBytes(raw)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-budget-policy/"+p.PolicyID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.recovery_budget_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, p.PolicyID, p.PolicyRevision).Scan(&existing)
		if err == nil {
			if existing != p.PolicyDigest {
				return ErrRecoveryEligibilityConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_budget_policy_revision_evidence(policy_id,policy_revision,scope_type,phase,max_active_recoveries,lifecycle_state,created_by,approved_by,policy_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.PolicyID, p.PolicyRevision, p.ScopeType, p.Phase, p.MaxActiveRecoveries, p.LifecycleState, p.CreatedBy, p.ApprovedBy, p.PolicyDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.recovery_budget_policies_current(policy_id,policy_revision,lifecycle_state,policy_digest) VALUES($1,$2,$3,$4) ON CONFLICT(policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,lifecycle_state=EXCLUDED.lifecycle_state,policy_digest=EXCLUDED.policy_digest,updated_at=statement_timestamp() WHERE kim.recovery_budget_policies_current.policy_revision<EXCLUDED.policy_revision`, p.PolicyID, p.PolicyRevision, p.LifecycleState, p.PolicyDigest)
		return err
	})
	return p, err
}

func loadRecoveryDestinationRequestTx(ctx context.Context, tx pgx.Tx, epoch FailureEpoch, evaluationID, placementScopeID string) (PlacementAdmissionRequest, error) {
	var request PlacementAdmissionRequest
	var pciRaw, networkRaw, storageRaw []byte
	err := tx.QueryRow(ctx, `SELECT project_id,workload_id,image_id,flavor_id,pci_requirements,network_requirements,storage_requirements FROM kim.placement_admission_decisions WHERE admission_id=$1`, epoch.AdmissionID).Scan(&request.ProjectID, &request.WorkloadID, &request.ImageID, &request.FlavorID, &pciRaw, &networkRaw, &storageRaw)
	if err != nil {
		return request, err
	}
	if err := json.Unmarshal(pciRaw, &request.PCI); err != nil {
		return request, err
	}
	if err := json.Unmarshal(networkRaw, &request.Network); err != nil {
		return request, err
	}
	if err := json.Unmarshal(storageRaw, &request.Storage); err != nil {
		return request, err
	}
	// Source-local storage identity is a safety input, not destination
	// visibility. Recovery Planning fixes one destination Host and then derives
	// its exact boot backend/capacity requirement from the immutable source
	// Volume shape. Carrying the source backend ID into this multi-Host dry
	// snapshot would make every other Local LVM Host ineligible by definition.
	request.Storage = nil
	request.RequestID = "recovery-eligibility:" + evaluationID
	request.PlacementScopeID = placementScopeID
	return request, nil
}

func recoveryDestinationSnapshotTx(ctx context.Context, tx pgx.Tx, epoch FailureEpoch, policyID string, policyRevision uint64, policyDigest string, request PlacementAdmissionRequest) ([]RecoveryDestinationCandidate, string, int, error) {
	dry, err := DryEvaluateAvailabilityPlacementScope(ctx, scopeTxBeginner{tx}, request)
	if err != nil {
		return nil, "", 0, err
	}
	candidates := make([]RecoveryDestinationCandidate, 0, len(dry.Candidates))
	eligible := 0
	for _, candidate := range dry.Candidates {
		provenanceRaw, _ := json.Marshal(candidate.Placement.Provenance)
		entry := RecoveryDestinationCandidate{HostID: candidate.Placement.HostID, PlacementScopeID: dry.Scope.PlacementScopeID,
			PlacementScopeGeneration: dry.Scope.ScopeGeneration, PlacementScopeDigest: dry.Scope.ScopeDigest,
			VisibilityProvenanceDigest: digestReleaseBytes(provenanceRaw), PlacementEvaluationDigest: candidate.Placement.Evaluation.EvaluationDigest,
			CapabilityGeneration:         candidate.Placement.Evaluation.CapabilityGeneration,
			BaselineAssignmentGeneration: candidate.Placement.Evaluation.BaselineAssignmentGeneration,
			PreflightGeneration:          candidate.Placement.Evaluation.PreflightGeneration,
			ComplianceGeneration:         candidate.Placement.Evaluation.ComplianceGeneration,
			PoolID:                       candidate.Placement.Evaluation.PoolID,
			PoolGeneration:               candidate.Placement.Evaluation.PoolGeneration,
			MembershipSetGeneration:      candidate.Placement.Evaluation.MembershipSetGeneration,
			MembershipGeneration:         candidate.Placement.Evaluation.MembershipGeneration,
			HierarchyID:                  candidate.Placement.Evaluation.HierarchyID,
			HierarchyGeneration:          candidate.Placement.Evaluation.HierarchyGeneration,
			PoolPolicyID:                 candidate.Placement.Evaluation.PoolPolicyID,
			PoolPolicyGeneration:         candidate.Placement.Evaluation.PoolPolicyGeneration,
			AvailabilityPolicyID:         candidate.AvailabilityResolution.EffectivePolicyID,
			AvailabilityPolicyRevision:   candidate.AvailabilityResolution.EffectivePolicyRevision,
			AvailabilityPolicyDigest:     candidate.AvailabilityResolution.EffectivePolicyDigest,
			VisibilityProvenance:         append([]PlacementVisibilityProvenance(nil), candidate.Placement.Provenance...)}
		if entry.PlacementEvaluationDigest == "" {
			entry.PlacementEvaluationDigest = digestReleaseBytes(nil)
		}
		switch {
		case entry.HostID == epoch.SourceHostID:
			entry.CandidateState, entry.ReasonCode = "SOURCE_EXCLUDED", "source_host_is_not_a_recovery_destination"
		case !candidate.Placement.Eligible:
			entry.CandidateState, entry.ReasonCode = "INELIGIBLE", "ordinary_placement_ineligible"
		case candidate.AvailabilityStatus != "RESOLVED" || entry.AvailabilityPolicyID != policyID || entry.AvailabilityPolicyRevision != policyRevision || entry.AvailabilityPolicyDigest != policyDigest:
			entry.CandidateState, entry.ReasonCode = "POLICY_INCOMPATIBLE", "destination_availability_policy_incompatible"
		default:
			entry.CandidateState, entry.ReasonCode = "ELIGIBLE", "current_compatible_destination"
			eligible++
		}
		raw, _ := json.Marshal(entry)
		entry.CandidateDigest = digestReleaseBytes(raw)
		candidates = append(candidates, entry)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].HostID < candidates[j].HostID })
	raw, _ := json.Marshal(candidates)
	return candidates, digestReleaseBytes(raw), eligible, nil
}

func loadFencingProofUsabilityTx(ctx context.Context, tx pgx.Tx, epoch FailureEpoch) (proofID, proofDigest, usability string, err error) {
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
	var currentHostState string
	if err := tx.QueryRow(ctx, `SELECT authority_generation,authority_state FROM kim.host_operation_authorities_current WHERE host_id=$1 FOR SHARE`, sourceHost).Scan(&currentHostGeneration, &currentHostState); err != nil {
		return proofID, proofDigest, "UNKNOWN", nil
	}
	var currentEventDigest string
	if err := tx.QueryRow(ctx, `SELECT event_payload_digest FROM kim.host_operation_authority_events WHERE host_id=$1 AND authority_generation=$2 AND event_sequence=$3 AND event_type='FENCED'`, sourceHost, hostGeneration, eventSequence).Scan(&currentEventDigest); err != nil || currentEventDigest != eventDigest || currentHostGeneration != hostGeneration || currentHostState != "FENCED" {
		return proofID, proofDigest, "STALE", nil
	}
	var currentPowerEvidence, observedPower, convergence string
	var currentPowerGeneration uint64
	if err := tx.QueryRow(ctx, `SELECT p.evidence_id,p.observation_generation,p.observed_power_state,p.convergence_state FROM kim.virtual_machines_current v JOIN kim.vm_power_state_current p ON p.vm_id=v.vm_id AND p.vm_generation=v.vm_generation WHERE v.workload_id=$1 AND v.host_id=$2 FOR SHARE OF v,p`, epoch.WorkloadID, sourceHost).Scan(&currentPowerEvidence, &currentPowerGeneration, &observedPower, &convergence); err != nil {
		return proofID, proofDigest, "UNKNOWN", nil
	}
	if currentPowerEvidence != powerEvidenceID || currentPowerGeneration != powerGeneration || observedPower != "SHUTOFF" || convergence != "MATCHED" {
		return proofID, proofDigest, "STALE", nil
	}
	return proofID, proofDigest, "USABLE", nil
}

func loadStorageProofUsabilityTx(ctx context.Context, tx pgx.Tx, epoch FailureEpoch) (proofID, proofDigest, usability string, err error) {
	var evaluationID, proofType string
	err = tx.QueryRow(ctx, `SELECT proof_id,proof_digest,evaluation_id,proof_type FROM kim.storage_safety_proof_evidence WHERE failure_epoch_id=$1`, epoch.FailureEpochID).Scan(&proofID, &proofDigest, &evaluationID, &proofType)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "MISSING", nil
	}
	if err != nil {
		return "", "", "UNKNOWN", err
	}
	var inputCount, currentCount int
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE c.evidence_id IS NOT NULL AND cl.attachment_claim_id IS NOT NULL AND b.binding_id IS NOT NULL) FROM kim.storage_safety_evaluation_input_evidence i LEFT JOIN kim.volume_attachment_observations_current c ON c.evidence_id=i.attachment_evidence_id AND c.attachment_id=i.attachment_id AND c.attachment_generation=i.attachment_generation AND c.observation_generation=i.observation_generation AND c.attachment_state='DETACHED' AND c.device_present=false AND c.holder_open=false LEFT JOIN kim.volume_attachment_claims cl ON cl.attachment_claim_id=i.attachment_claim_id AND cl.claim_state='RELEASED' AND cl.claim_state=i.claim_state AND cl.claim_state_generation=i.claim_state_generation LEFT JOIN kim.volume_backend_bindings_current b ON b.binding_id=i.binding_id AND b.binding_generation=i.binding_generation AND b.observation_generation=i.binding_observation_generation AND b.binding_state='BOUND' AND b.binding_state=i.binding_state WHERE i.evaluation_id=$1`, evaluationID).Scan(&inputCount, &currentCount)
	if err != nil {
		return proofID, proofDigest, "UNKNOWN", err
	}
	if inputCount != currentCount {
		return proofID, proofDigest, "STALE", nil
	}
	if proofType == "LOCAL_LVM_SOURCE_ROOT_QUIESCED_DATA_DETACHED" {
		var rootID, rootDigest string
		if err := tx.QueryRow(ctx, `SELECT root_safety_proof_id,root_safety_proof_digest FROM kim.storage_safety_root_input_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&rootID, &rootDigest); err != nil {
			return proofID, proofDigest, "UNKNOWN", err
		}
		currentRootID, currentRootDigest, rootUsability, err := loadSourceRootSafetyProofUsabilityTx(ctx, tx, epoch)
		if err != nil {
			return proofID, proofDigest, "UNKNOWN", err
		}
		if rootUsability != "USABLE" || rootID != currentRootID || rootDigest != currentRootDigest {
			return proofID, proofDigest, "STALE", nil
		}
		retirementUsability, err := loadSourceRetirementUsabilityTx(ctx, tx, epoch, rootID, rootDigest)
		if err != nil {
			return proofID, proofDigest, "UNKNOWN", err
		}
		if retirementUsability != "USABLE" {
			return proofID, proofDigest, "STALE", nil
		}
	} else if inputCount == 0 {
		return proofID, proofDigest, "STALE", nil
	}
	return proofID, proofDigest, "USABLE", nil
}

func recoveryEligibilityResult(e *RecoveryEligibilityEvaluation) {
	switch {
	case e.EvaluatedEpochState != "CONFIRMED" && e.EvaluatedEpochState != "FENCED":
		e.ResultState, e.ReasonCode = "EPOCH_NOT_CONFIRMED", "epoch_not_confirmed"
	case e.Responsibility == "WORKLOAD_MANAGED":
		e.ResultState, e.ReasonCode = "RESPONSIBILITY_BLOCKED", "workload_managed_has_no_automatic_recovery"
	case e.Responsibility == "MANUAL" || e.HostFailureAction == "NO_AUTOMATIC_ACTION":
		e.ResultState, e.ReasonCode = "NO_AUTOMATIC_ACTION", "manual_or_no_automatic_action"
	case e.Responsibility != "INFRASTRUCTURE_MANAGED" || (e.HostFailureAction != "RESTART_ON_OTHER_HOST" && e.HostFailureAction != "EVACUATE"):
		e.ResultState, e.ReasonCode = "RESPONSIBILITY_BLOCKED", "unsupported_responsibility_action"
	case e.FencingUsability == "MISSING":
		e.ResultState, e.ReasonCode = "FENCING_PROOF_MISSING", "fencing_proof_missing"
	case e.FencingUsability == "STALE":
		e.ResultState, e.ReasonCode = "FENCING_PROOF_STALE", "fencing_proof_not_current_usable"
	case e.FencingUsability != "USABLE":
		e.ResultState, e.ReasonCode = "FENCING_PROOF_UNKNOWN", "fencing_proof_usability_unknown"
	case e.StorageUsability == "MISSING":
		e.ResultState, e.ReasonCode = "STORAGE_PROOF_MISSING", "storage_safety_proof_missing"
	case e.StorageUsability == "STALE":
		e.ResultState, e.ReasonCode = "STORAGE_PROOF_STALE", "storage_safety_proof_not_current_usable"
	case e.StorageUsability != "USABLE":
		e.ResultState, e.ReasonCode = "STORAGE_PROOF_UNKNOWN", "storage_safety_proof_usability_unknown"
	case e.EvaluatedEpochState != "FENCED":
		e.ResultState, e.ReasonCode = "STALE_EPOCH", "current_usable_fencing_transition_missing"
	case e.RecoveryBudgetPolicyID == "":
		e.ResultState, e.ReasonCode = "NO_RECOVERY_BUDGET_POLICY", "typed_recovery_budget_policy_missing"
	case e.BudgetMaxActive == 0:
		e.ResultState, e.ReasonCode = "STALE_POLICY", "recovery_budget_policy_not_current_active"
	case e.BudgetActiveCount >= e.BudgetMaxActive:
		e.ResultState, e.ReasonCode = "BUDGET_EXHAUSTED", "global_planning_budget_exhausted"
	case e.EligibleDestinationCount == 0:
		e.ResultState, e.ReasonCode = "NO_DESTINATION", "no_current_compatible_destination"
	default:
		e.ResultState, e.ReasonCode = "ELIGIBLE", "all_permission_inputs_satisfied"
	}
}

func EvaluateRecoveryEligibility(ctx context.Context, db TxBeginner, evaluationID, epochID, placementScopeID, evaluatorVersion, evaluatorDigest string) (RecoveryEligibilityEvaluation, error) {
	var out RecoveryEligibilityEvaluation
	if evaluationID == "" || epochID == "" || placementScopeID == "" || evaluatorVersion == "" || evaluatorDigest == "" {
		return out, ErrRecoveryEligibilityConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-eligibility-evaluation/"+evaluationID); err != nil {
			return err
		}
		var storedRequest []byte
		err := tx.QueryRow(ctx, `SELECT evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,responsibility,host_failure_action,COALESCE(confirmation_decision_id,''),COALESCE(fencing_proof_id,''),COALESCE(fencing_proof_digest,''),fencing_usability,COALESCE(storage_safety_proof_id,''),COALESCE(storage_safety_proof_digest,''),storage_usability,COALESCE(recovery_budget_policy_id,''),COALESCE(recovery_budget_policy_revision,0),COALESCE(recovery_budget_policy_digest,''),budget_active_count,budget_max_active,destination_request,destination_request_digest,destination_snapshot_digest,destination_candidate_count,eligible_destination_count,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.recovery_eligibility_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&out.EvaluationID, &out.FailureEpochID, &out.EvaluatedTransitionGeneration, &out.EvaluatedEpochState, &out.AvailabilityBindingRevision, &out.AvailabilityBindingDigest, &out.AvailabilityPolicyID, &out.AvailabilityPolicyRevision, &out.AvailabilityPolicyDigest, &out.Responsibility, &out.HostFailureAction, &out.ConfirmationDecisionID, &out.FencingProofID, &out.FencingProofDigest, &out.FencingUsability, &out.StorageSafetyProofID, &out.StorageSafetyProofDigest, &out.StorageUsability, &out.RecoveryBudgetPolicyID, &out.RecoveryBudgetPolicyRevision, &out.RecoveryBudgetPolicyDigest, &out.BudgetActiveCount, &out.BudgetMaxActive, &storedRequest, &out.DestinationRequestDigest, &out.DestinationSnapshotDigest, &out.DestinationCandidateCount, &out.EligibleDestinationCount, &out.ResultState, &out.ReasonCode, &out.EvaluatorVersion, &out.EvaluatorDigest, &out.EvaluationDigest)
		if err == nil {
			if out.FailureEpochID != epochID || out.EvaluatorVersion != evaluatorVersion || out.EvaluatorDigest != evaluatorDigest || json.Unmarshal(storedRequest, &out.DestinationRequest) != nil || out.DestinationRequest.PlacementScopeID != placementScopeID {
				return ErrRecoveryEligibilityConflict
			}
			rows, err := tx.Query(ctx, `SELECT host_id,candidate_state,reason_code,placement_scope_id,placement_scope_generation,placement_scope_digest,visibility_provenance_digest,placement_evaluation_digest,capability_generation,baseline_assignment_generation,preflight_generation,compliance_generation,pool_id,pool_generation,membership_set_generation,membership_generation,COALESCE(hierarchy_id,''),COALESCE(hierarchy_generation,0),pool_policy_id,pool_policy_generation,COALESCE(availability_policy_id,''),COALESCE(availability_policy_revision,0),COALESCE(availability_policy_digest,''),candidate_digest FROM kim.recovery_eligibility_destination_candidate_evidence WHERE evaluation_id=$1 ORDER BY candidate_ordinal`, evaluationID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var candidate RecoveryDestinationCandidate
				if err := rows.Scan(&candidate.HostID, &candidate.CandidateState, &candidate.ReasonCode, &candidate.PlacementScopeID, &candidate.PlacementScopeGeneration, &candidate.PlacementScopeDigest, &candidate.VisibilityProvenanceDigest, &candidate.PlacementEvaluationDigest, &candidate.CapabilityGeneration, &candidate.BaselineAssignmentGeneration, &candidate.PreflightGeneration, &candidate.ComplianceGeneration, &candidate.PoolID, &candidate.PoolGeneration, &candidate.MembershipSetGeneration, &candidate.MembershipGeneration, &candidate.HierarchyID, &candidate.HierarchyGeneration, &candidate.PoolPolicyID, &candidate.PoolPolicyGeneration, &candidate.AvailabilityPolicyID, &candidate.AvailabilityPolicyRevision, &candidate.AvailabilityPolicyDigest, &candidate.CandidateDigest); err != nil {
					return err
				}
				out.Candidates = append(out.Candidates, candidate)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			rows.Close()
			visibilityRows, err := tx.Query(ctx, `SELECT candidate_ordinal,host_group_id,host_group_generation,membership_set_generation,membership_generation,membership_evidence_digest,provenance_digest FROM kim.recovery_eligibility_destination_visibility_evidence WHERE evaluation_id=$1 ORDER BY candidate_ordinal,provenance_ordinal`, evaluationID)
			if err != nil {
				return err
			}
			defer visibilityRows.Close()
			for visibilityRows.Next() {
				var candidateOrdinal int
				var provenance PlacementVisibilityProvenance
				if err := visibilityRows.Scan(&candidateOrdinal, &provenance.HostGroupID, &provenance.HostGroupGeneration, &provenance.MembershipSetGeneration, &provenance.MembershipGeneration, &provenance.MembershipEvidenceDigest, &provenance.ProvenanceDigest); err != nil {
					return err
				}
				if candidateOrdinal <= 0 || candidateOrdinal > len(out.Candidates) {
					return ErrRecoveryEligibilityConflict
				}
				out.Candidates[candidateOrdinal-1].VisibilityProvenance = append(out.Candidates[candidateOrdinal-1].VisibilityProvenance, provenance)
			}
			if err := visibilityRows.Err(); err != nil {
				return err
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-epoch/"+epochID); err != nil {
			return err
		}
		epoch, err := loadFailureEpochTx(ctx, tx, epochID)
		if err != nil {
			return err
		}
		out = RecoveryEligibilityEvaluation{EvaluationID: evaluationID, FailureEpochID: epochID, EvaluatedTransitionGeneration: epoch.TransitionGeneration, EvaluatedEpochState: epoch.EpochState, AvailabilityBindingRevision: epoch.AvailabilityBindingRevision, AvailabilityBindingDigest: epoch.AvailabilityBindingDigest, AvailabilityPolicyID: epoch.PolicyID, AvailabilityPolicyRevision: epoch.PolicyRevision, AvailabilityPolicyDigest: epoch.PolicyDigest, EvaluatorVersion: evaluatorVersion, EvaluatorDigest: evaluatorDigest}
		if err := tx.QueryRow(ctx, `SELECT b.responsibility,b.host_failure_action FROM kim.vm_availability_binding_evidence b WHERE b.workload_id=$1 AND b.binding_revision=$2 AND b.binding_digest=$3`, epoch.WorkloadID, epoch.AvailabilityBindingRevision, epoch.AvailabilityBindingDigest).Scan(&out.Responsibility, &out.HostFailureAction); err != nil {
			return err
		}
		_ = tx.QueryRow(ctx, `SELECT confirmation_decision_id FROM kim.failure_epoch_transition_evidence WHERE failure_epoch_id=$1 AND to_state='CONFIRMED'`, epochID).Scan(&out.ConfirmationDecisionID)
		out.FencingProofID, out.FencingProofDigest, out.FencingUsability, err = loadFencingProofUsabilityTx(ctx, tx, epoch)
		if err != nil {
			return err
		}
		out.StorageSafetyProofID, out.StorageSafetyProofDigest, out.StorageUsability, err = loadStorageProofUsabilityTx(ctx, tx, epoch)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT b.recovery_budget_policy_id,b.recovery_budget_policy_revision,b.recovery_budget_policy_digest FROM kim.availability_policy_recovery_budget_binding_evidence b WHERE b.availability_policy_id=$1 AND b.availability_policy_revision=$2 AND b.availability_policy_digest=$3`, epoch.PolicyID, epoch.PolicyRevision, epoch.PolicyDigest).Scan(&out.RecoveryBudgetPolicyID, &out.RecoveryBudgetPolicyRevision, &out.RecoveryBudgetPolicyDigest)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if out.RecoveryBudgetPolicyID != "" {
			var revision uint64
			var digest, lifecycle string
			if err := tx.QueryRow(ctx, `SELECT c.policy_revision,c.policy_digest,c.lifecycle_state,r.max_active_recoveries FROM kim.recovery_budget_policies_current c JOIN kim.recovery_budget_policy_revision_evidence r ON r.policy_id=c.policy_id AND r.policy_revision=c.policy_revision AND r.policy_digest=c.policy_digest WHERE c.policy_id=$1 FOR SHARE OF c`, out.RecoveryBudgetPolicyID).Scan(&revision, &digest, &lifecycle, &out.BudgetMaxActive); err != nil || revision != out.RecoveryBudgetPolicyRevision || digest != out.RecoveryBudgetPolicyDigest || lifecycle != "ACTIVE" {
				out.BudgetMaxActive = 0
			} else if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.recovery_budget_claims_current WHERE recovery_budget_policy_id=$1 AND recovery_budget_policy_revision=$2 AND scope_type='GLOBAL' AND phase='PLANNING' AND claim_state IN ('RESERVED','CONSUMED')`, out.RecoveryBudgetPolicyID, out.RecoveryBudgetPolicyRevision).Scan(&out.BudgetActiveCount); err != nil {
				return err
			}
		}
		out.DestinationRequest, err = loadRecoveryDestinationRequestTx(ctx, tx, epoch, evaluationID, placementScopeID)
		if err != nil {
			return err
		}
		requestRaw, _ := json.Marshal(out.DestinationRequest)
		out.DestinationRequestDigest = digestReleaseBytes(requestRaw)
		out.Candidates, out.DestinationSnapshotDigest, out.EligibleDestinationCount, err = recoveryDestinationSnapshotTx(ctx, tx, epoch, epoch.PolicyID, epoch.PolicyRevision, epoch.PolicyDigest, out.DestinationRequest)
		if err != nil {
			return err
		}
		out.DestinationCandidateCount = len(out.Candidates)
		recoveryEligibilityResult(&out)
		digestCopy := out
		digestCopy.Candidates = nil
		digestCopy.EvaluationDigest = ""
		raw, _ := json.Marshal(digestCopy)
		out.EvaluationDigest = digestReleaseBytes(raw)
		var confirmationID, fencingID, fencingDigest, storageID, storageDigest, budgetID, budgetRevision, budgetDigest any
		if out.ConfirmationDecisionID != "" {
			confirmationID = out.ConfirmationDecisionID
		}
		if out.FencingProofID != "" {
			fencingID, fencingDigest = out.FencingProofID, out.FencingProofDigest
		}
		if out.StorageSafetyProofID != "" {
			storageID, storageDigest = out.StorageSafetyProofID, out.StorageSafetyProofDigest
		}
		if out.RecoveryBudgetPolicyID != "" {
			budgetID, budgetRevision, budgetDigest = out.RecoveryBudgetPolicyID, out.RecoveryBudgetPolicyRevision, out.RecoveryBudgetPolicyDigest
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_eligibility_evaluation_evidence(evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,responsibility,host_failure_action,confirmation_decision_id,fencing_proof_id,fencing_proof_digest,fencing_usability,storage_safety_proof_id,storage_safety_proof_digest,storage_usability,recovery_budget_policy_id,recovery_budget_policy_revision,recovery_budget_policy_digest,budget_active_count,budget_max_active,destination_request,destination_request_digest,destination_snapshot_digest,destination_candidate_count,eligible_destination_count,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33)`, out.EvaluationID, out.FailureEpochID, out.EvaluatedTransitionGeneration, out.EvaluatedEpochState, out.AvailabilityBindingRevision, out.AvailabilityBindingDigest, out.AvailabilityPolicyID, out.AvailabilityPolicyRevision, out.AvailabilityPolicyDigest, out.Responsibility, out.HostFailureAction, confirmationID, fencingID, fencingDigest, out.FencingUsability, storageID, storageDigest, out.StorageUsability, budgetID, budgetRevision, budgetDigest, out.BudgetActiveCount, out.BudgetMaxActive, out.DestinationRequest, out.DestinationRequestDigest, out.DestinationSnapshotDigest, out.DestinationCandidateCount, out.EligibleDestinationCount, out.ResultState, out.ReasonCode, out.EvaluatorVersion, out.EvaluatorDigest, out.EvaluationDigest); err != nil {
			return err
		}
		for index, candidate := range out.Candidates {
			var policyID, policyRevision, policyDigest any
			if candidate.AvailabilityPolicyID != "" {
				policyID, policyRevision, policyDigest = candidate.AvailabilityPolicyID, candidate.AvailabilityPolicyRevision, candidate.AvailabilityPolicyDigest
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_eligibility_destination_candidate_evidence(evaluation_id,candidate_ordinal,host_id,candidate_state,reason_code,placement_scope_id,placement_scope_generation,placement_scope_digest,visibility_provenance_digest,placement_evaluation_digest,capability_generation,baseline_assignment_generation,preflight_generation,compliance_generation,pool_id,pool_generation,membership_set_generation,membership_generation,hierarchy_id,hierarchy_generation,pool_policy_id,pool_policy_generation,availability_policy_id,availability_policy_revision,availability_policy_digest,candidate_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,NULLIF($19,''),NULLIF($20,0),$21,$22,$23,$24,$25,$26)`, out.EvaluationID, index+1, candidate.HostID, candidate.CandidateState, candidate.ReasonCode, candidate.PlacementScopeID, candidate.PlacementScopeGeneration, candidate.PlacementScopeDigest, candidate.VisibilityProvenanceDigest, candidate.PlacementEvaluationDigest, candidate.CapabilityGeneration, candidate.BaselineAssignmentGeneration, candidate.PreflightGeneration, candidate.ComplianceGeneration, candidate.PoolID, candidate.PoolGeneration, candidate.MembershipSetGeneration, candidate.MembershipGeneration, candidate.HierarchyID, candidate.HierarchyGeneration, candidate.PoolPolicyID, candidate.PoolPolicyGeneration, policyID, policyRevision, policyDigest, candidate.CandidateDigest); err != nil {
				return err
			}
			for provenanceIndex, provenance := range candidate.VisibilityProvenance {
				if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_eligibility_destination_visibility_evidence(evaluation_id,candidate_ordinal,provenance_ordinal,host_id,host_group_id,host_group_generation,membership_set_generation,membership_generation,membership_evidence_digest,provenance_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, out.EvaluationID, index+1, provenanceIndex+1, candidate.HostID, provenance.HostGroupID, provenance.HostGroupGeneration, provenance.MembershipSetGeneration, provenance.MembershipGeneration, provenance.MembershipEvidenceDigest, provenance.ProvenanceDigest); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return out, err
}

func MaterializeRecoveryEligibilityDecision(ctx context.Context, db TxBeginner, decisionID, evaluationID, decidedBy string) (RecoveryEligibilityDecision, error) {
	var out RecoveryEligibilityDecision
	if decisionID == "" || evaluationID == "" || decidedBy == "" {
		return out, ErrRecoveryEligibilityConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-eligibility-decision/"+decisionID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT d.decision_id,d.failure_epoch_id,d.evaluation_id,d.evaluation_digest,d.expected_transition_generation,d.fencing_proof_id,d.fencing_proof_digest,d.storage_safety_proof_id,d.storage_safety_proof_digest,d.recovery_budget_policy_id,d.recovery_budget_policy_revision,d.recovery_budget_policy_digest,d.destination_snapshot_digest,d.decision_state,d.result_state,d.decided_by,d.decision_digest,c.claim_id,c.claim_generation,c.claim_digest FROM kim.recovery_eligibility_decision_evidence d JOIN kim.recovery_budget_claims_current c ON c.decision_id=d.decision_id WHERE d.decision_id=$1`, decisionID).Scan(&out.DecisionID, &out.FailureEpochID, &out.EvaluationID, &out.EvaluationDigest, &out.ExpectedTransitionGeneration, &out.FencingProofID, &out.FencingProofDigest, &out.StorageSafetyProofID, &out.StorageSafetyProofDigest, &out.RecoveryBudgetPolicyID, &out.RecoveryBudgetPolicyRevision, &out.RecoveryBudgetPolicyDigest, &out.DestinationSnapshotDigest, &out.DecisionState, &out.ResultState, &out.DecidedBy, &out.DecisionDigest, &out.BudgetClaimID, &out.BudgetClaimGeneration, &out.BudgetClaimDigest)
		if err == nil {
			if out.EvaluationID != evaluationID || out.DecidedBy != decidedBy {
				return ErrRecoveryEligibilityConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var e RecoveryEligibilityEvaluation
		var requestRaw []byte
		if err := tx.QueryRow(ctx, `SELECT evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,responsibility,host_failure_action,COALESCE(confirmation_decision_id,''),COALESCE(fencing_proof_id,''),COALESCE(fencing_proof_digest,''),fencing_usability,COALESCE(storage_safety_proof_id,''),COALESCE(storage_safety_proof_digest,''),storage_usability,COALESCE(recovery_budget_policy_id,''),COALESCE(recovery_budget_policy_revision,0),COALESCE(recovery_budget_policy_digest,''),budget_active_count,budget_max_active,destination_request,destination_request_digest,destination_snapshot_digest,destination_candidate_count,eligible_destination_count,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.recovery_eligibility_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&e.EvaluationID, &e.FailureEpochID, &e.EvaluatedTransitionGeneration, &e.EvaluatedEpochState, &e.AvailabilityBindingRevision, &e.AvailabilityBindingDigest, &e.AvailabilityPolicyID, &e.AvailabilityPolicyRevision, &e.AvailabilityPolicyDigest, &e.Responsibility, &e.HostFailureAction, &e.ConfirmationDecisionID, &e.FencingProofID, &e.FencingProofDigest, &e.FencingUsability, &e.StorageSafetyProofID, &e.StorageSafetyProofDigest, &e.StorageUsability, &e.RecoveryBudgetPolicyID, &e.RecoveryBudgetPolicyRevision, &e.RecoveryBudgetPolicyDigest, &e.BudgetActiveCount, &e.BudgetMaxActive, &requestRaw, &e.DestinationRequestDigest, &e.DestinationSnapshotDigest, &e.DestinationCandidateCount, &e.EligibleDestinationCount, &e.ResultState, &e.ReasonCode, &e.EvaluatorVersion, &e.EvaluatorDigest, &e.EvaluationDigest); err != nil {
			return err
		}
		if e.ResultState != "ELIGIBLE" {
			return ErrRecoveryEligibilityBlocked
		}
		if err := json.Unmarshal(requestRaw, &e.DestinationRequest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-epoch/"+e.FailureEpochID); err != nil {
			return err
		}
		var existingEpochDecision string
		err = tx.QueryRow(ctx, `SELECT decision_id FROM kim.recovery_eligibility_decision_evidence WHERE failure_epoch_id=$1`, e.FailureEpochID).Scan(&existingEpochDecision)
		if err == nil {
			return ErrRecoveryEligibilityStale
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("recovery-budget/%s/%d/GLOBAL/PLANNING", e.RecoveryBudgetPolicyID, e.RecoveryBudgetPolicyRevision)); err != nil {
			return err
		}
		epoch, err := loadFailureEpochTx(ctx, tx, e.FailureEpochID)
		if err != nil {
			return err
		}
		if epoch.EpochState != "FENCED" || epoch.TransitionGeneration != e.EvaluatedTransitionGeneration || epoch.AvailabilityBindingRevision != e.AvailabilityBindingRevision || epoch.AvailabilityBindingDigest != e.AvailabilityBindingDigest || epoch.PolicyID != e.AvailabilityPolicyID || epoch.PolicyRevision != e.AvailabilityPolicyRevision || epoch.PolicyDigest != e.AvailabilityPolicyDigest {
			return ErrRecoveryEligibilityStale
		}
		var confirmationDecisionID string
		if err := tx.QueryRow(ctx, `SELECT confirmation_decision_id FROM kim.failure_epoch_transition_evidence WHERE failure_epoch_id=$1 AND to_state='CONFIRMED'`, e.FailureEpochID).Scan(&confirmationDecisionID); err != nil || confirmationDecisionID != e.ConfirmationDecisionID {
			return ErrRecoveryEligibilityStale
		}
		fencingID, fencingDigest, fencingUsability, err := loadFencingProofUsabilityTx(ctx, tx, epoch)
		if err != nil || fencingUsability != "USABLE" || fencingID != e.FencingProofID || fencingDigest != e.FencingProofDigest {
			return ErrRecoveryEligibilityStale
		}
		storageID, storageDigest, storageUsability, err := loadStorageProofUsabilityTx(ctx, tx, epoch)
		if err != nil || storageUsability != "USABLE" || storageID != e.StorageSafetyProofID || storageDigest != e.StorageSafetyProofDigest {
			return ErrRecoveryEligibilityStale
		}
		var budgetRevision uint64
		var budgetDigest, lifecycle string
		var maxActive int
		if err := tx.QueryRow(ctx, `SELECT c.policy_revision,c.policy_digest,c.lifecycle_state,r.max_active_recoveries FROM kim.recovery_budget_policies_current c JOIN kim.recovery_budget_policy_revision_evidence r ON r.policy_id=c.policy_id AND r.policy_revision=c.policy_revision AND r.policy_digest=c.policy_digest WHERE c.policy_id=$1 FOR UPDATE OF c`, e.RecoveryBudgetPolicyID).Scan(&budgetRevision, &budgetDigest, &lifecycle, &maxActive); err != nil || budgetRevision != e.RecoveryBudgetPolicyRevision || budgetDigest != e.RecoveryBudgetPolicyDigest || lifecycle != "ACTIVE" {
			return ErrRecoveryEligibilityStale
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.recovery_budget_claims_current WHERE recovery_budget_policy_id=$1 AND recovery_budget_policy_revision=$2 AND scope_type='GLOBAL' AND phase='PLANNING' AND claim_state IN ('RESERVED','CONSUMED')`, e.RecoveryBudgetPolicyID, e.RecoveryBudgetPolicyRevision).Scan(&active); err != nil {
			return err
		}
		if active >= maxActive {
			return ErrRecoveryEligibilityBudgetExhausted
		}
		candidates, snapshotDigest, eligible, err := recoveryDestinationSnapshotTx(ctx, tx, epoch, e.AvailabilityPolicyID, e.AvailabilityPolicyRevision, e.AvailabilityPolicyDigest, e.DestinationRequest)
		if err != nil || snapshotDigest != e.DestinationSnapshotDigest || len(candidates) != e.DestinationCandidateCount || eligible != e.EligibleDestinationCount || eligible == 0 {
			return ErrRecoveryEligibilityStale
		}
		out = RecoveryEligibilityDecision{DecisionID: decisionID, FailureEpochID: e.FailureEpochID, EvaluationID: evaluationID, EvaluationDigest: e.EvaluationDigest, ExpectedTransitionGeneration: e.EvaluatedTransitionGeneration, FencingProofID: e.FencingProofID, FencingProofDigest: e.FencingProofDigest, StorageSafetyProofID: e.StorageSafetyProofID, StorageSafetyProofDigest: e.StorageSafetyProofDigest, RecoveryBudgetPolicyID: e.RecoveryBudgetPolicyID, RecoveryBudgetPolicyRevision: e.RecoveryBudgetPolicyRevision, RecoveryBudgetPolicyDigest: e.RecoveryBudgetPolicyDigest, DestinationSnapshotDigest: e.DestinationSnapshotDigest, DecisionState: "ACCEPTED", ResultState: "ELIGIBLE", DecidedBy: decidedBy, BudgetClaimID: "recovery-budget-claim:" + decisionID, BudgetClaimGeneration: 1}
		decisionCopy := out
		decisionCopy.DecisionDigest, decisionCopy.BudgetClaimDigest = "", ""
		raw, _ := json.Marshal(decisionCopy)
		out.DecisionDigest = digestReleaseBytes(raw)
		claimRaw, _ := json.Marshal([]any{out.BudgetClaimID, out.DecisionID, out.FailureEpochID, out.RecoveryBudgetPolicyID, out.RecoveryBudgetPolicyRevision, out.RecoveryBudgetPolicyDigest, "GLOBAL", "PLANNING", 1, "RESERVED"})
		out.BudgetClaimDigest = digestReleaseBytes(claimRaw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_eligibility_decision_evidence(decision_id,failure_epoch_id,evaluation_id,evaluation_digest,expected_transition_generation,fencing_proof_id,fencing_proof_digest,storage_safety_proof_id,storage_safety_proof_digest,recovery_budget_policy_id,recovery_budget_policy_revision,recovery_budget_policy_digest,destination_snapshot_digest,decision_state,result_state,decided_by,decision_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'ACCEPTED','ELIGIBLE',$14,$15)`, out.DecisionID, out.FailureEpochID, out.EvaluationID, out.EvaluationDigest, out.ExpectedTransitionGeneration, out.FencingProofID, out.FencingProofDigest, out.StorageSafetyProofID, out.StorageSafetyProofDigest, out.RecoveryBudgetPolicyID, out.RecoveryBudgetPolicyRevision, out.RecoveryBudgetPolicyDigest, out.DestinationSnapshotDigest, out.DecidedBy, out.DecisionDigest); err != nil {
			return ErrRecoveryEligibilityStale
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.recovery_budget_claim_evidence(claim_id,decision_id,failure_epoch_id,recovery_budget_policy_id,recovery_budget_policy_revision,recovery_budget_policy_digest,scope_type,phase,claim_generation,claim_state,claim_digest) VALUES($1,$2,$3,$4,$5,$6,'GLOBAL','PLANNING',1,'RESERVED',$7)`, out.BudgetClaimID, out.DecisionID, out.FailureEpochID, out.RecoveryBudgetPolicyID, out.RecoveryBudgetPolicyRevision, out.RecoveryBudgetPolicyDigest, out.BudgetClaimDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.recovery_budget_claims_current(claim_id,decision_id,failure_epoch_id,recovery_budget_policy_id,recovery_budget_policy_revision,scope_type,phase,claim_generation,claim_state,claim_digest) VALUES($1,$2,$3,$4,$5,'GLOBAL','PLANNING',1,'RESERVED',$6)`, out.BudgetClaimID, out.DecisionID, out.FailureEpochID, out.RecoveryBudgetPolicyID, out.RecoveryBudgetPolicyRevision, out.BudgetClaimDigest)
		return err
	})
	return out, err
}
