package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrFailureConfirmationConflict = errors.New("Failure Confirmation authority conflict")
	ErrFailureConfirmationStale    = errors.New("Failure Confirmation input is stale")
	ErrFailureConfirmationBlocked  = errors.New("Failure Confirmation evaluation does not authorize confirmation")
)

type FailureConfirmationRequirement struct {
	Ordinal                                                 int
	EvidenceType, ObservedState, FreshnessState, SourceType string
	RequirementDigest                                       string
}

type FailureConfirmationPolicy struct {
	PolicyID, ApplicableFailureClass, ConfirmationMode string
	LifecycleState, CreatedBy, ApprovedBy              string
	RequirementsDigest, PolicyDigest                   string
	PolicyRevision                                     uint64
	RequireDistinctSources                             bool
	Requirements                                       []FailureConfirmationRequirement
}

type FailureConfirmationEvaluation struct {
	EvaluationID, FailureEpochID, EvaluatedEpochState     string
	AvailabilityBindingDigest, PolicyID, PolicyDigest     string
	EvidenceSetDigest                                     string
	ResultState, ReasonCode, EvaluatorVersion             string
	EvaluatorDigest, EvaluationDigest                     string
	FailureEpochGeneration, EvaluatedTransitionGeneration uint64
	AvailabilityBindingRevision, PolicyRevision           uint64
	LatestEvidenceGeneration                              uint64
}

type FailureConfirmationDecision struct {
	DecisionID, FailureEpochID, EvaluationID, PolicyID string
	PolicyDigest, EvidenceSetDigest, DecisionState     string
	ResultCode, DecidedBy, DecisionDigest              string
	ExpectedTransitionGeneration, PolicyRevision       uint64
}

type confirmationObservationRow struct {
	EvidenceID, EvidenceType, SourceType, SourceHostID string
	ObservedState, FreshnessState, EvidenceDigest      string
	EvidenceGeneration, SessionGeneration              uint64
	CredentialRevision, AuthorityGeneration            uint64
	ObservationGeneration                              uint64
	SourceIdentityDigest                               string
}

func validFailureConfirmationPolicy(p FailureConfirmationPolicy) bool {
	if p.PolicyID == "" || p.PolicyRevision == 0 || p.ConfirmationMode != "ALL_REQUIRED_EVIDENCE" || p.CreatedBy == "" || p.ApprovedBy == "" || len(p.Requirements) == 0 {
		return false
	}
	if p.LifecycleState != "DRAFT" && p.LifecycleState != "ACTIVE" && p.LifecycleState != "DEPRECATED" && p.LifecycleState != "RETIRED" {
		return false
	}
	if p.ApplicableFailureClass != "HOST_CONNECTIVITY_LOSS" && p.ApplicableFailureClass != "HOST_AUTHORITY_LOSS" && p.ApplicableFailureClass != "VM_RUNTIME_UNAVAILABLE" {
		return false
	}
	seen := map[string]bool{}
	for i, r := range p.Requirements {
		if r.Ordinal != i+1 || (r.ObservedState != "PRESENT" && r.ObservedState != "ABSENT") || r.FreshnessState != "CURRENT" {
			return false
		}
		if r.EvidenceType != "AGENT_CONNECTIVITY_LOSS" && r.EvidenceType != "HOST_OPERATION_AUTHORITY_STATE" && r.EvidenceType != "VM_RUNTIME_OBSERVATION" {
			return false
		}
		if r.SourceType != "CONTROL_PLANE" && r.SourceType != "LIBVIRT_READ_BACK" {
			return false
		}
		key := r.EvidenceType + "/" + r.ObservedState + "/" + r.FreshnessState + "/" + r.SourceType
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func PublishFailureConfirmationPolicy(ctx context.Context, db TxBeginner, p FailureConfirmationPolicy) (FailureConfirmationPolicy, error) {
	if !validFailureConfirmationPolicy(p) {
		return FailureConfirmationPolicy{}, ErrFailureConfirmationConflict
	}
	reqs := append([]FailureConfirmationRequirement(nil), p.Requirements...)
	for i := range reqs {
		requirement := reqs[i]
		requirement.RequirementDigest = ""
		raw, _ := json.Marshal(requirement)
		reqs[i].RequirementDigest = digestReleaseBytes(raw)
	}
	reqRaw, _ := json.Marshal(reqs)
	p.RequirementsDigest = digestReleaseBytes(reqRaw)
	p.Requirements = reqs
	policyCopy := p
	policyCopy.PolicyDigest = ""
	raw, _ := json.Marshal(policyCopy)
	p.PolicyDigest = digestReleaseBytes(raw)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-confirmation-policy/"+p.PolicyID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.failure_confirmation_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, p.PolicyID, p.PolicyRevision).Scan(&existing)
		if err == nil {
			if existing != p.PolicyDigest {
				return ErrFailureConfirmationConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_confirmation_policy_revision_evidence(policy_id,policy_revision,applicable_failure_class,confirmation_mode,require_distinct_sources,requirements_digest,lifecycle_state,created_by,approved_by,policy_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, p.PolicyID, p.PolicyRevision, p.ApplicableFailureClass, p.ConfirmationMode, p.RequireDistinctSources, p.RequirementsDigest, p.LifecycleState, p.CreatedBy, p.ApprovedBy, p.PolicyDigest); err != nil {
			return err
		}
		for _, r := range p.Requirements {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_confirmation_policy_requirement_evidence(policy_id,policy_revision,requirement_ordinal,evidence_type,required_observed_state,required_freshness_state,required_source_type,requirement_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.PolicyID, p.PolicyRevision, r.Ordinal, r.EvidenceType, r.ObservedState, r.FreshnessState, r.SourceType, r.RequirementDigest); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO kim.failure_confirmation_policies_current(policy_id,policy_revision,lifecycle_state,policy_digest) VALUES($1,$2,$3,$4) ON CONFLICT(policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,lifecycle_state=EXCLUDED.lifecycle_state,policy_digest=EXCLUDED.policy_digest,updated_at=statement_timestamp() WHERE kim.failure_confirmation_policies_current.policy_revision<EXCLUDED.policy_revision`, p.PolicyID, p.PolicyRevision, p.LifecycleState, p.PolicyDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var rev uint64
			var digest string
			if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest FROM kim.failure_confirmation_policies_current WHERE policy_id=$1`, p.PolicyID).Scan(&rev, &digest); err != nil || rev != p.PolicyRevision || digest != p.PolicyDigest {
				return ErrFailureConfirmationConflict
			}
		}
		return nil
	})
	return p, err
}

func loadConfirmationEvaluationTx(ctx context.Context, tx pgx.Tx, id string) (FailureConfirmationEvaluation, error) {
	var e FailureConfirmationEvaluation
	err := tx.QueryRow(ctx, `SELECT evaluation_id,failure_epoch_id,failure_epoch_generation,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,COALESCE(confirmation_policy_id,''),COALESCE(confirmation_policy_revision,0),COALESCE(confirmation_policy_digest,''),latest_evidence_generation,evidence_set_digest,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.failure_confirmation_evaluation_evidence WHERE evaluation_id=$1`, id).Scan(&e.EvaluationID, &e.FailureEpochID, &e.FailureEpochGeneration, &e.EvaluatedTransitionGeneration, &e.EvaluatedEpochState, &e.AvailabilityBindingRevision, &e.AvailabilityBindingDigest, &e.PolicyID, &e.PolicyRevision, &e.PolicyDigest, &e.LatestEvidenceGeneration, &e.EvidenceSetDigest, &e.ResultState, &e.ReasonCode, &e.EvaluatorVersion, &e.EvaluatorDigest, &e.EvaluationDigest)
	return e, err
}

func loadConfirmationObservationsTx(ctx context.Context, tx pgx.Tx, epochID string, latest uint64) ([]confirmationObservationRow, error) {
	rows, err := tx.Query(ctx, `SELECT evidence_id,evidence_generation,evidence_type,source_type,source_host_id,COALESCE(source_session_generation,0),COALESCE(source_credential_binding_revision,0),COALESCE(source_host_authority_generation,0),observation_generation,observed_state,freshness_state,evidence_digest FROM kim.failure_observation_evidence WHERE failure_epoch_id=$1 AND evidence_generation<=$2 ORDER BY evidence_generation`, epochID, latest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []confirmationObservationRow
	for rows.Next() {
		var o confirmationObservationRow
		if err := rows.Scan(&o.EvidenceID, &o.EvidenceGeneration, &o.EvidenceType, &o.SourceType, &o.SourceHostID, &o.SessionGeneration, &o.CredentialRevision, &o.AuthorityGeneration, &o.ObservationGeneration, &o.ObservedState, &o.FreshnessState, &o.EvidenceDigest); err != nil {
			return nil, err
		}
		sourceRaw, _ := json.Marshal([]any{o.SourceType, o.SourceHostID, o.SessionGeneration, o.CredentialRevision, o.AuthorityGeneration, o.ObservationGeneration})
		o.SourceIdentityDigest = digestReleaseBytes(sourceRaw)
		out = append(out, o)
	}
	return out, rows.Err()
}

func evaluateConfirmationRequirements(p FailureConfirmationPolicy, observations []confirmationObservationRow) (string, string) {
	relevant := map[string][]confirmationObservationRow{}
	for _, o := range observations {
		relevant[o.EvidenceType+"/"+o.SourceType] = append(relevant[o.EvidenceType+"/"+o.SourceType], o)
	}
	for _, r := range p.Requirements {
		states := map[string]bool{}
		for _, o := range relevant[r.EvidenceType+"/"+r.SourceType] {
			if o.FreshnessState == "CURRENT" {
				states[o.ObservedState] = true
			}
		}
		if len(states) > 1 {
			return "CONFLICTING_INPUT", "current_evidence_conflicts"
		}
	}
	usedSources := map[string]bool{}
	for _, r := range p.Requirements {
		rows := relevant[r.EvidenceType+"/"+r.SourceType]
		matched := false
		unknown := false
		stale := false
		for _, o := range rows {
			if o.ObservedState == "UNKNOWN" || o.FreshnessState == "UNKNOWN" {
				unknown = true
			}
			if o.FreshnessState == "STALE" {
				stale = true
			}
			if o.ObservedState == r.ObservedState && o.FreshnessState == r.FreshnessState && (!p.RequireDistinctSources || !usedSources[o.SourceIdentityDigest]) {
				matched = true
				usedSources[o.SourceIdentityDigest] = true
				break
			}
		}
		if !matched {
			if unknown {
				return "UNKNOWN", "required_evidence_unknown"
			}
			if stale {
				return "STALE_EVIDENCE", "required_evidence_stale"
			}
			return "NOT_SATISFIED", "required_evidence_missing"
		}
	}
	return "SATISFIED", "all_required_evidence_satisfied"
}

func EvaluateFailureConfirmation(ctx context.Context, db TxBeginner, evaluationID, epochID, evaluatorVersion, evaluatorDigest string) (FailureConfirmationEvaluation, error) {
	if evaluationID == "" || epochID == "" || evaluatorVersion == "" || evaluatorDigest == "" {
		return FailureConfirmationEvaluation{}, ErrFailureConfirmationConflict
	}
	var evaluation FailureConfirmationEvaluation
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-confirmation-evaluation/"+evaluationID); err != nil {
			return err
		}
		existing, err := loadConfirmationEvaluationTx(ctx, tx, evaluationID)
		if err == nil {
			if existing.FailureEpochID != epochID || existing.EvaluatorVersion != evaluatorVersion || existing.EvaluatorDigest != evaluatorDigest {
				return ErrFailureConfirmationConflict
			}
			evaluation = existing
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
		evaluation = FailureConfirmationEvaluation{EvaluationID: evaluationID, FailureEpochID: epochID, FailureEpochGeneration: epoch.EpochGeneration, EvaluatedTransitionGeneration: epoch.TransitionGeneration, EvaluatedEpochState: epoch.EpochState, AvailabilityBindingRevision: epoch.AvailabilityBindingRevision, AvailabilityBindingDigest: epoch.AvailabilityBindingDigest, LatestEvidenceGeneration: epoch.LatestEvidenceGeneration, EvaluatorVersion: evaluatorVersion, EvaluatorDigest: evaluatorDigest}
		var bindingDigest string
		if err := tx.QueryRow(ctx, `SELECT availability_binding_digest FROM kim.failure_epoch_evidence WHERE failure_epoch_id=$1`, epochID).Scan(&bindingDigest); err != nil {
			return err
		}
		observations, err := loadConfirmationObservationsTx(ctx, tx, epochID, epoch.LatestEvidenceGeneration)
		if err != nil {
			return err
		}
		evidenceRaw, _ := json.Marshal(observations)
		evaluation.EvidenceSetDigest = digestReleaseBytes(evidenceRaw)
		var p FailureConfirmationPolicy
		err = tx.QueryRow(ctx, `SELECT b.confirmation_policy_id,b.confirmation_policy_revision,b.confirmation_policy_digest FROM kim.failure_epoch_evidence e JOIN kim.availability_policy_confirmation_binding_evidence b ON b.availability_policy_id=e.availability_policy_id AND b.availability_policy_revision=e.availability_policy_revision AND b.availability_policy_digest=e.availability_policy_digest WHERE e.failure_epoch_id=$1`, epochID).Scan(&p.PolicyID, &p.PolicyRevision, &p.PolicyDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			evaluation.ResultState = "NO_CONFIRMATION_POLICY"
			evaluation.ReasonCode = "availability_policy_has_no_typed_confirmation_reference"
		} else if err != nil {
			return err
		} else {
			evaluation.PolicyID = p.PolicyID
			evaluation.PolicyRevision = p.PolicyRevision
			evaluation.PolicyDigest = p.PolicyDigest
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-confirmation-policy/"+p.PolicyID); err != nil {
				return err
			}
			var currentRevision uint64
			var currentDigest, lifecycle string
			if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest,lifecycle_state FROM kim.failure_confirmation_policies_current WHERE policy_id=$1 FOR SHARE`, p.PolicyID).Scan(&currentRevision, &currentDigest, &lifecycle); err != nil || currentRevision != p.PolicyRevision || currentDigest != p.PolicyDigest || lifecycle != "ACTIVE" {
				evaluation.ResultState = "STALE_POLICY"
				evaluation.ReasonCode = "confirmation_policy_not_current_active"
			} else {
				if err := tx.QueryRow(ctx, `SELECT applicable_failure_class,confirmation_mode,require_distinct_sources,lifecycle_state,created_by,approved_by,requirements_digest FROM kim.failure_confirmation_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2 AND policy_digest=$3`, p.PolicyID, p.PolicyRevision, p.PolicyDigest).Scan(&p.ApplicableFailureClass, &p.ConfirmationMode, &p.RequireDistinctSources, &p.LifecycleState, &p.CreatedBy, &p.ApprovedBy, &p.RequirementsDigest); err != nil {
					return err
				}
				rows, err := tx.Query(ctx, `SELECT requirement_ordinal,evidence_type,required_observed_state,required_freshness_state,required_source_type,requirement_digest FROM kim.failure_confirmation_policy_requirement_evidence WHERE policy_id=$1 AND policy_revision=$2 ORDER BY requirement_ordinal`, p.PolicyID, p.PolicyRevision)
				if err != nil {
					return err
				}
				for rows.Next() {
					var r FailureConfirmationRequirement
					if err := rows.Scan(&r.Ordinal, &r.EvidenceType, &r.ObservedState, &r.FreshnessState, &r.SourceType, &r.RequirementDigest); err != nil {
						rows.Close()
						return err
					}
					p.Requirements = append(p.Requirements, r)
				}
				rows.Close()
				if epoch.EpochState != "SUSPECTED" {
					evaluation.ResultState = "STALE_EPOCH"
					evaluation.ReasonCode = "epoch_not_suspected"
				} else if p.ApplicableFailureClass != epoch.FailureClass {
					evaluation.ResultState = "NOT_SATISFIED"
					evaluation.ReasonCode = "failure_class_mismatch"
				} else {
					evaluation.ResultState, evaluation.ReasonCode = evaluateConfirmationRequirements(p, observations)
				}
			}
		}
		evaluation.PolicyID = p.PolicyID
		evaluation.PolicyRevision = p.PolicyRevision
		evaluation.PolicyDigest = p.PolicyDigest
		rawEval, _ := json.Marshal(evaluation)
		evaluation.EvaluationDigest = digestReleaseBytes(rawEval)
		var policyID any
		var policyRevision any
		var policyDigest any
		if evaluation.PolicyID != "" {
			policyID = evaluation.PolicyID
			policyRevision = evaluation.PolicyRevision
			policyDigest = evaluation.PolicyDigest
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_confirmation_evaluation_evidence(evaluation_id,failure_epoch_id,failure_epoch_generation,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,confirmation_policy_id,confirmation_policy_revision,confirmation_policy_digest,latest_evidence_generation,evidence_set_digest,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, evaluation.EvaluationID, evaluation.FailureEpochID, evaluation.FailureEpochGeneration, evaluation.EvaluatedTransitionGeneration, evaluation.EvaluatedEpochState, evaluation.AvailabilityBindingRevision, bindingDigest, policyID, policyRevision, policyDigest, evaluation.LatestEvidenceGeneration, evaluation.EvidenceSetDigest, evaluation.ResultState, evaluation.ReasonCode, evaluation.EvaluatorVersion, evaluation.EvaluatorDigest, evaluation.EvaluationDigest); err != nil {
			return err
		}
		for i, o := range observations {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_confirmation_evaluation_input_evidence(evaluation_id,input_ordinal,evidence_id,evidence_generation,evidence_digest,evidence_type,source_identity_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, evaluation.EvaluationID, i+1, o.EvidenceID, o.EvidenceGeneration, o.EvidenceDigest, o.EvidenceType, o.SourceIdentityDigest); err != nil {
				return err
			}
		}
		return nil
	})
	return evaluation, err
}

func loadConfirmationDecisionTx(ctx context.Context, tx pgx.Tx, id string) (FailureConfirmationDecision, error) {
	var d FailureConfirmationDecision
	err := tx.QueryRow(ctx, `SELECT decision_id,failure_epoch_id,expected_transition_generation,evaluation_id,confirmation_policy_id,confirmation_policy_revision,confirmation_policy_digest,evidence_set_digest,decision_state,result_code,decided_by,decision_digest FROM kim.failure_confirmation_decision_evidence WHERE decision_id=$1`, id).Scan(&d.DecisionID, &d.FailureEpochID, &d.ExpectedTransitionGeneration, &d.EvaluationID, &d.PolicyID, &d.PolicyRevision, &d.PolicyDigest, &d.EvidenceSetDigest, &d.DecisionState, &d.ResultCode, &d.DecidedBy, &d.DecisionDigest)
	return d, err
}

func ConfirmFailureEpoch(ctx context.Context, db TxBeginner, decisionID, evaluationID, decidedBy string) (FailureConfirmationDecision, FailureEpoch, error) {
	if decisionID == "" || evaluationID == "" || decidedBy == "" {
		return FailureConfirmationDecision{}, FailureEpoch{}, ErrFailureConfirmationConflict
	}
	var decision FailureConfirmationDecision
	var epoch FailureEpoch
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-confirmation-decision/"+decisionID); err != nil {
			return err
		}
		existing, err := loadConfirmationDecisionTx(ctx, tx, decisionID)
		if err == nil {
			if existing.EvaluationID != evaluationID || existing.DecidedBy != decidedBy {
				return ErrFailureConfirmationConflict
			}
			decision = existing
			epoch, err = loadFailureEpochTx(ctx, tx, existing.FailureEpochID)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		evaluation, err := loadConfirmationEvaluationTx(ctx, tx, evaluationID)
		if err != nil {
			return err
		}
		if evaluation.ResultState != "SATISFIED" {
			return ErrFailureConfirmationBlocked
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-epoch/"+evaluation.FailureEpochID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-confirmation-policy/"+evaluation.PolicyID); err != nil {
			return err
		}
		epoch, err = loadFailureEpochTx(ctx, tx, evaluation.FailureEpochID)
		if err != nil {
			return err
		}
		if epoch.EpochState != "SUSPECTED" || epoch.TransitionGeneration != evaluation.EvaluatedTransitionGeneration || epoch.LatestEvidenceGeneration != evaluation.LatestEvidenceGeneration {
			return ErrFailureConfirmationStale
		}
		var currentRevision uint64
		var currentDigest, lifecycle string
		if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest,lifecycle_state FROM kim.failure_confirmation_policies_current WHERE policy_id=$1 FOR SHARE`, evaluation.PolicyID).Scan(&currentRevision, &currentDigest, &lifecycle); err != nil || currentRevision != evaluation.PolicyRevision || currentDigest != evaluation.PolicyDigest || lifecycle != "ACTIVE" {
			return ErrFailureConfirmationStale
		}
		var inputCount, exactCount int
		if err := tx.QueryRow(ctx, `SELECT count(*),count(o.evidence_id) FROM kim.failure_confirmation_evaluation_input_evidence i LEFT JOIN kim.failure_observation_evidence o ON o.evidence_id=i.evidence_id AND o.evidence_generation=i.evidence_generation AND o.evidence_digest=i.evidence_digest WHERE i.evaluation_id=$1`, evaluationID).Scan(&inputCount, &exactCount); err != nil || inputCount != exactCount {
			return ErrFailureConfirmationStale
		}
		decision = FailureConfirmationDecision{DecisionID: decisionID, FailureEpochID: epoch.FailureEpochID, ExpectedTransitionGeneration: epoch.TransitionGeneration, EvaluationID: evaluationID, PolicyID: evaluation.PolicyID, PolicyRevision: evaluation.PolicyRevision, PolicyDigest: evaluation.PolicyDigest, EvidenceSetDigest: evaluation.EvidenceSetDigest, DecisionState: "ACCEPTED", ResultCode: "CONFIRMED", DecidedBy: decidedBy}
		raw, _ := json.Marshal(decision)
		decision.DecisionDigest = digestReleaseBytes(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_confirmation_decision_evidence(decision_id,failure_epoch_id,expected_transition_generation,evaluation_id,confirmation_policy_id,confirmation_policy_revision,confirmation_policy_digest,evidence_set_digest,decision_state,result_code,decided_by,decision_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'ACCEPTED','CONFIRMED',$9,$10)`, decision.DecisionID, decision.FailureEpochID, decision.ExpectedTransitionGeneration, decision.EvaluationID, decision.PolicyID, decision.PolicyRevision, decision.PolicyDigest, decision.EvidenceSetDigest, decision.DecidedBy, decision.DecisionDigest); err != nil {
			return err
		}
		transitionGeneration := epoch.TransitionGeneration + 1
		transitionDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/CONFIRMED/%s", epoch.FailureEpochID, transitionGeneration, decision.DecisionDigest)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_epoch_transition_evidence(failure_epoch_id,transition_generation,from_state,to_state,cause_evidence_id,confirmation_decision_id,transition_digest) VALUES($1,$2,'SUSPECTED','CONFIRMED',NULL,$3,$4)`, epoch.FailureEpochID, transitionGeneration, decision.DecisionID, transitionDigest); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE kim.failure_epochs_current SET epoch_state='CONFIRMED',transition_generation=$2,updated_at=statement_timestamp() WHERE failure_epoch_id=$1 AND epoch_state='SUSPECTED' AND transition_generation=$3 AND latest_evidence_generation=$4`, epoch.FailureEpochID, transitionGeneration, epoch.TransitionGeneration, epoch.LatestEvidenceGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrFailureConfirmationStale
		}
		epoch.EpochState = "CONFIRMED"
		epoch.TransitionGeneration = transitionGeneration
		return nil
	})
	if err != nil {
		return FailureConfirmationDecision{}, FailureEpoch{}, err
	}
	return decision, epoch, nil
}
