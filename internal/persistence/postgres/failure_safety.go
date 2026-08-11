package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrFailureSafetyConflict = errors.New("Failure safety authority conflict")
	ErrFailureSafetyStale    = errors.New("Failure safety input is stale")
	ErrFailureSafetyBlocked  = errors.New("Failure safety evaluation does not authorize proof")
)

type FailureFencingPolicy struct {
	PolicyID, FencingMode, LifecycleState, CreatedBy, ApprovedBy, PolicyDigest string
	PolicyRevision                                                             uint64
}

type StorageSafetyPolicy struct {
	PolicyID, StorageClass, SafetyMode, LifecycleState, CreatedBy, ApprovedBy, PolicyDigest string
	PolicyRevision                                                                          uint64
}

type SourceExecutionFencingObservation struct {
	EvidenceID, FailureEpochID, SourceHostID, HostAuthorityEventDigest string
	VMPowerEvidenceID, ObservationState, EvidenceDigest                string
	EvidenceGeneration, HostAuthorityGeneration                        uint64
	HostAuthorityEventSequence, VMPowerObservationGeneration           uint64
}

type FailureFencingEvaluation struct {
	EvaluationID, FailureEpochID, EvaluatedEpochState, AvailabilityBindingDigest string
	ConfirmationDecisionID, PolicyID, PolicyDigest, FencingEvidenceID            string
	FencingEvidenceDigest, ResultState, ReasonCode                               string
	EvaluatorVersion, EvaluatorDigest, EvaluationDigest                          string
	EvaluatedTransitionGeneration, AvailabilityBindingRevision                   uint64
	PolicyRevision, LatestFencingEvidenceGeneration                              uint64
}

type FailureFencingProof struct {
	ProofID, FailureEpochID, EvaluationID, ProofType, ProofState string
	PolicyID, PolicyDigest, FencingEvidenceID                    string
	FencingEvidenceDigest, DecidedBy, ProofDigest                string
	ExpectedTransitionGeneration, PolicyRevision                 uint64
}

type StorageSafetyEvaluation struct {
	EvaluationID, FailureEpochID, EvaluatedEpochState, AvailabilityBindingDigest string
	PolicyID, PolicyDigest, EvidenceSetDigest, ResultState, ReasonCode           string
	EvaluatorVersion, EvaluatorDigest, EvaluationDigest                          string
	EvaluatedTransitionGeneration, AvailabilityBindingRevision, PolicyRevision   uint64
}

type StorageSafetyProof struct {
	ProofID, FailureEpochID, EvaluationID, ProofType, ProofState string
	PolicyID, PolicyDigest, EvidenceSetDigest, DecidedBy         string
	ProofDigest                                                  string
	ExpectedTransitionGeneration, PolicyRevision                 uint64
}

type storageSafetyInput struct {
	EvidenceID, AttachmentID, ObservationDigest  string
	AttachmentClaimID, ClaimState, BindingID     string
	BindingState, AttachmentState, EvidenceState string
	AttachmentGeneration, ObservationGeneration  uint64
	ClaimStateGeneration, BindingGeneration      uint64
	BindingObservationGeneration                 uint64
	DevicePresent, HolderOpen                    bool
}

func PublishFailureFencingPolicy(ctx context.Context, db TxBeginner, p FailureFencingPolicy) (FailureFencingPolicy, error) {
	if p.PolicyID == "" || p.PolicyRevision == 0 || p.FencingMode != "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF" || p.LifecycleState == "" || p.CreatedBy == "" || p.ApprovedBy == "" {
		return FailureFencingPolicy{}, ErrFailureSafetyConflict
	}
	copy := p
	copy.PolicyDigest = ""
	raw, _ := json.Marshal(copy)
	p.PolicyDigest = digestReleaseBytes(raw)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-fencing-policy/"+p.PolicyID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.failure_fencing_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, p.PolicyID, p.PolicyRevision).Scan(&existing)
		if err == nil {
			if existing != p.PolicyDigest {
				return ErrFailureSafetyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_fencing_policy_revision_evidence(policy_id,policy_revision,fencing_mode,lifecycle_state,created_by,approved_by,policy_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, p.PolicyID, p.PolicyRevision, p.FencingMode, p.LifecycleState, p.CreatedBy, p.ApprovedBy, p.PolicyDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.failure_fencing_policies_current(policy_id,policy_revision,lifecycle_state,policy_digest) VALUES($1,$2,$3,$4) ON CONFLICT(policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,lifecycle_state=EXCLUDED.lifecycle_state,policy_digest=EXCLUDED.policy_digest,updated_at=statement_timestamp() WHERE kim.failure_fencing_policies_current.policy_revision<EXCLUDED.policy_revision`, p.PolicyID, p.PolicyRevision, p.LifecycleState, p.PolicyDigest)
		return err
	})
	return p, err
}

func PublishStorageSafetyPolicy(ctx context.Context, db TxBeginner, p StorageSafetyPolicy) (StorageSafetyPolicy, error) {
	if p.PolicyID == "" || p.PolicyRevision == 0 || p.StorageClass != "LOCAL_LVM" || p.SafetyMode != "SOURCE_DETACHED_NO_HOLDER" || p.LifecycleState == "" || p.CreatedBy == "" || p.ApprovedBy == "" {
		return StorageSafetyPolicy{}, ErrFailureSafetyConflict
	}
	copy := p
	copy.PolicyDigest = ""
	raw, _ := json.Marshal(copy)
	p.PolicyDigest = digestReleaseBytes(raw)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "storage-safety-policy/"+p.PolicyID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.storage_safety_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, p.PolicyID, p.PolicyRevision).Scan(&existing)
		if err == nil {
			if existing != p.PolicyDigest {
				return ErrFailureSafetyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.storage_safety_policy_revision_evidence(policy_id,policy_revision,storage_class,safety_mode,lifecycle_state,created_by,approved_by,policy_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.PolicyID, p.PolicyRevision, p.StorageClass, p.SafetyMode, p.LifecycleState, p.CreatedBy, p.ApprovedBy, p.PolicyDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.storage_safety_policies_current(policy_id,policy_revision,lifecycle_state,policy_digest) VALUES($1,$2,$3,$4) ON CONFLICT(policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,lifecycle_state=EXCLUDED.lifecycle_state,policy_digest=EXCLUDED.policy_digest,updated_at=statement_timestamp() WHERE kim.storage_safety_policies_current.policy_revision<EXCLUDED.policy_revision`, p.PolicyID, p.PolicyRevision, p.LifecycleState, p.PolicyDigest)
		return err
	})
	return p, err
}

// RecordSourceExecutionFencingObservation derives a bounded positive proof
// input from exact Host authority and standard libvirt read-back evidence.
func RecordSourceExecutionFencingObservation(ctx context.Context, db TxBeginner, evidenceID, epochID string) (SourceExecutionFencingObservation, error) {
	if evidenceID == "" || epochID == "" {
		return SourceExecutionFencingObservation{}, ErrFailureSafetyConflict
	}
	var out SourceExecutionFencingObservation
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "source-fencing-evidence/"+evidenceID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRow(ctx, `SELECT evidence_digest FROM kim.source_execution_fencing_observation_evidence WHERE evidence_id=$1`, evidenceID).Scan(&existing)
		if err == nil {
			if err := tx.QueryRow(ctx, `SELECT evidence_id,failure_epoch_id,evidence_generation,source_host_id,COALESCE(host_authority_generation,0),COALESCE(host_authority_event_sequence,0),COALESCE(host_authority_event_digest,''),COALESCE(vm_power_evidence_id,''),COALESCE(vm_power_observation_generation,0),observation_state,evidence_digest FROM kim.source_execution_fencing_observation_evidence WHERE evidence_id=$1`, evidenceID).Scan(&out.EvidenceID, &out.FailureEpochID, &out.EvidenceGeneration, &out.SourceHostID, &out.HostAuthorityGeneration, &out.HostAuthorityEventSequence, &out.HostAuthorityEventDigest, &out.VMPowerEvidenceID, &out.VMPowerObservationGeneration, &out.ObservationState, &out.EvidenceDigest); err != nil {
				return err
			}
			if out.FailureEpochID != epochID {
				return ErrFailureSafetyConflict
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
		if epoch.EpochState != "CONFIRMED" {
			return ErrFailureSafetyStale
		}
		var priorGeneration uint64
		_ = tx.QueryRow(ctx, `SELECT evidence_generation FROM kim.source_execution_fencing_observations_current WHERE failure_epoch_id=$1 FOR UPDATE`, epochID).Scan(&priorGeneration)
		out = SourceExecutionFencingObservation{EvidenceID: evidenceID, FailureEpochID: epochID, SourceHostID: epoch.SourceHostID, EvidenceGeneration: priorGeneration + 1, ObservationState: "UNKNOWN"}
		_ = tx.QueryRow(ctx, `SELECT authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1 AND authority_state='FENCED' FOR SHARE`, epoch.SourceHostID).Scan(&out.HostAuthorityGeneration)
		if out.HostAuthorityGeneration > 0 {
			_ = tx.QueryRow(ctx, `SELECT event_sequence,event_payload_digest FROM kim.host_operation_authority_events WHERE host_id=$1 AND authority_generation=$2 AND event_type='FENCED' ORDER BY event_sequence DESC LIMIT 1`, epoch.SourceHostID, out.HostAuthorityGeneration).Scan(&out.HostAuthorityEventSequence, &out.HostAuthorityEventDigest)
		}
		var observedPower, convergence string
		_ = tx.QueryRow(ctx, `SELECT p.evidence_id,p.observation_generation,p.observed_power_state,p.convergence_state FROM kim.virtual_machines_current v JOIN kim.vm_power_state_current p ON p.vm_id=v.vm_id AND p.vm_generation=v.vm_generation WHERE v.workload_id=$1 AND v.host_id=$2 FOR SHARE OF v,p`, epoch.WorkloadID, epoch.SourceHostID).Scan(&out.VMPowerEvidenceID, &out.VMPowerObservationGeneration, &observedPower, &convergence)
		if out.HostAuthorityGeneration > 0 && out.HostAuthorityEventSequence > 0 && out.VMPowerEvidenceID != "" {
			out.ObservationState = "NOT_PROVEN"
			if observedPower == "SHUTOFF" && convergence == "MATCHED" {
				out.ObservationState = "PROVEN"
			}
		}
		raw, _ := json.Marshal(out)
		out.EvidenceDigest = digestReleaseBytes(raw)
		var hostGen, eventSeq, powerGen any
		var eventDigest, powerID any
		if out.HostAuthorityGeneration > 0 {
			hostGen, eventSeq, eventDigest = out.HostAuthorityGeneration, out.HostAuthorityEventSequence, out.HostAuthorityEventDigest
		}
		if out.VMPowerEvidenceID != "" {
			powerID, powerGen = out.VMPowerEvidenceID, out.VMPowerObservationGeneration
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.source_execution_fencing_observation_evidence(evidence_id,failure_epoch_id,evidence_generation,source_host_id,host_authority_generation,host_authority_event_sequence,host_authority_event_digest,vm_power_evidence_id,vm_power_observation_generation,observation_state,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, out.EvidenceID, out.FailureEpochID, out.EvidenceGeneration, out.SourceHostID, hostGen, eventSeq, eventDigest, powerID, powerGen, out.ObservationState, out.EvidenceDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.source_execution_fencing_observations_current(failure_epoch_id,evidence_generation,evidence_id,observation_state,evidence_digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT(failure_epoch_id) DO UPDATE SET evidence_generation=EXCLUDED.evidence_generation,evidence_id=EXCLUDED.evidence_id,observation_state=EXCLUDED.observation_state,evidence_digest=EXCLUDED.evidence_digest,updated_at=statement_timestamp()`, out.FailureEpochID, out.EvidenceGeneration, out.EvidenceID, out.ObservationState, out.EvidenceDigest)
		return err
	})
	return out, err
}

func EvaluateFailureFencing(ctx context.Context, db TxBeginner, evaluationID, epochID, evaluatorVersion, evaluatorDigest string) (FailureFencingEvaluation, error) {
	var out FailureFencingEvaluation
	if evaluationID == "" || epochID == "" || evaluatorVersion == "" || evaluatorDigest == "" {
		return out, ErrFailureSafetyConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "fencing-evaluation/"+evaluationID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,COALESCE(confirmation_decision_id,''),COALESCE(fencing_policy_id,''),COALESCE(fencing_policy_revision,0),COALESCE(fencing_policy_digest,''),latest_fencing_evidence_generation,COALESCE(fencing_evidence_id,''),COALESCE(fencing_evidence_digest,''),result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.failure_fencing_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&out.EvaluationID, &out.FailureEpochID, &out.EvaluatedTransitionGeneration, &out.EvaluatedEpochState, &out.AvailabilityBindingRevision, &out.AvailabilityBindingDigest, &out.ConfirmationDecisionID, &out.PolicyID, &out.PolicyRevision, &out.PolicyDigest, &out.LatestFencingEvidenceGeneration, &out.FencingEvidenceID, &out.FencingEvidenceDigest, &out.ResultState, &out.ReasonCode, &out.EvaluatorVersion, &out.EvaluatorDigest, &out.EvaluationDigest)
		if err == nil {
			if out.FailureEpochID != epochID || out.EvaluatorVersion != evaluatorVersion || out.EvaluatorDigest != evaluatorDigest {
				return ErrFailureSafetyConflict
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
		out = FailureFencingEvaluation{EvaluationID: evaluationID, FailureEpochID: epochID, EvaluatedTransitionGeneration: epoch.TransitionGeneration, EvaluatedEpochState: epoch.EpochState, AvailabilityBindingRevision: epoch.AvailabilityBindingRevision, AvailabilityBindingDigest: epoch.AvailabilityBindingDigest, EvaluatorVersion: evaluatorVersion, EvaluatorDigest: evaluatorDigest}
		_ = tx.QueryRow(ctx, `SELECT confirmation_decision_id FROM kim.failure_epoch_transition_evidence WHERE failure_epoch_id=$1 AND to_state='CONFIRMED'`, epochID).Scan(&out.ConfirmationDecisionID)
		err = tx.QueryRow(ctx, `SELECT b.fencing_policy_id,b.fencing_policy_revision,b.fencing_policy_digest FROM kim.failure_epoch_evidence e JOIN kim.availability_policy_fencing_binding_evidence b ON b.availability_policy_id=e.availability_policy_id AND b.availability_policy_revision=e.availability_policy_revision AND b.availability_policy_digest=e.availability_policy_digest WHERE e.failure_epoch_id=$1`, epochID).Scan(&out.PolicyID, &out.PolicyRevision, &out.PolicyDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			out.ResultState, out.ReasonCode = "NO_FENCING_POLICY", "availability_policy_has_no_typed_fencing_reference"
		} else if err != nil {
			return err
		} else if epoch.EpochState != "CONFIRMED" {
			out.ResultState, out.ReasonCode = "STALE_EPOCH", "epoch_not_confirmed"
		} else {
			var rev uint64
			var digest, lifecycle string
			if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest,lifecycle_state FROM kim.failure_fencing_policies_current WHERE policy_id=$1 FOR SHARE`, out.PolicyID).Scan(&rev, &digest, &lifecycle); err != nil || rev != out.PolicyRevision || digest != out.PolicyDigest || lifecycle != "ACTIVE" {
				out.ResultState, out.ReasonCode = "STALE_POLICY", "fencing_policy_not_current_active"
			} else {
				var state string
				err := tx.QueryRow(ctx, `SELECT c.evidence_generation,c.evidence_id,c.evidence_digest,c.observation_state FROM kim.source_execution_fencing_observations_current c WHERE c.failure_epoch_id=$1`, epochID).Scan(&out.LatestFencingEvidenceGeneration, &out.FencingEvidenceID, &out.FencingEvidenceDigest, &state)
				if errors.Is(err, pgx.ErrNoRows) {
					out.ResultState, out.ReasonCode = "NOT_PROVEN", "fencing_evidence_missing"
				} else if err != nil {
					return err
				} else {
					switch state {
					case "PROVEN":
						out.ResultState, out.ReasonCode = "PROVEN", "source_execution_fence_proven"
					case "UNKNOWN":
						out.ResultState, out.ReasonCode = "UNKNOWN", "fencing_evidence_unknown"
					case "CONFLICTING":
						out.ResultState, out.ReasonCode = "CONFLICTING_INPUT", "fencing_evidence_conflicting"
					default:
						out.ResultState, out.ReasonCode = "NOT_PROVEN", "fencing_evidence_not_proven"
					}
				}
			}
		}
		raw, _ := json.Marshal(out)
		out.EvaluationDigest = digestReleaseBytes(raw)
		var policyID, policyRev, policyDigest, evidenceID, evidenceDigest any
		if out.PolicyID != "" {
			policyID, policyRev, policyDigest = out.PolicyID, out.PolicyRevision, out.PolicyDigest
		}
		if out.FencingEvidenceID != "" {
			evidenceID, evidenceDigest = out.FencingEvidenceID, out.FencingEvidenceDigest
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.failure_fencing_evaluation_evidence(evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,confirmation_decision_id,fencing_policy_id,fencing_policy_revision,fencing_policy_digest,latest_fencing_evidence_generation,fencing_evidence_id,fencing_evidence_digest,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, out.EvaluationID, out.FailureEpochID, out.EvaluatedTransitionGeneration, out.EvaluatedEpochState, out.AvailabilityBindingRevision, out.AvailabilityBindingDigest, out.ConfirmationDecisionID, policyID, policyRev, policyDigest, out.LatestFencingEvidenceGeneration, evidenceID, evidenceDigest, out.ResultState, out.ReasonCode, out.EvaluatorVersion, out.EvaluatorDigest, out.EvaluationDigest)
		return err
	})
	return out, err
}

func MaterializeFailureFencingProof(ctx context.Context, db TxBeginner, proofID, evaluationID, decidedBy string) (FailureFencingProof, FailureEpoch, error) {
	var proof FailureFencingProof
	var epoch FailureEpoch
	if proofID == "" || evaluationID == "" || decidedBy == "" {
		return proof, epoch, ErrFailureSafetyConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "fencing-proof/"+proofID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT proof_id,failure_epoch_id,expected_transition_generation,evaluation_id,proof_type,proof_state,fencing_policy_id,fencing_policy_revision,fencing_policy_digest,fencing_evidence_id,fencing_evidence_digest,decided_by,proof_digest FROM kim.failure_fencing_proof_evidence WHERE proof_id=$1`, proofID).Scan(&proof.ProofID, &proof.FailureEpochID, &proof.ExpectedTransitionGeneration, &proof.EvaluationID, &proof.ProofType, &proof.ProofState, &proof.PolicyID, &proof.PolicyRevision, &proof.PolicyDigest, &proof.FencingEvidenceID, &proof.FencingEvidenceDigest, &proof.DecidedBy, &proof.ProofDigest)
		if err == nil {
			if proof.EvaluationID != evaluationID || proof.DecidedBy != decidedBy {
				return ErrFailureSafetyConflict
			}
			epoch, err = loadFailureEpochTx(ctx, tx, proof.FailureEpochID)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var e FailureFencingEvaluation
		if err := tx.QueryRow(ctx, `SELECT evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,COALESCE(confirmation_decision_id,''),fencing_policy_id,fencing_policy_revision,fencing_policy_digest,latest_fencing_evidence_generation,fencing_evidence_id,fencing_evidence_digest,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.failure_fencing_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&e.EvaluationID, &e.FailureEpochID, &e.EvaluatedTransitionGeneration, &e.EvaluatedEpochState, &e.AvailabilityBindingRevision, &e.AvailabilityBindingDigest, &e.ConfirmationDecisionID, &e.PolicyID, &e.PolicyRevision, &e.PolicyDigest, &e.LatestFencingEvidenceGeneration, &e.FencingEvidenceID, &e.FencingEvidenceDigest, &e.ResultState, &e.ReasonCode, &e.EvaluatorVersion, &e.EvaluatorDigest, &e.EvaluationDigest); err != nil {
			return err
		}
		if e.ResultState != "PROVEN" {
			return ErrFailureSafetyBlocked
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-epoch/"+e.FailureEpochID); err != nil {
			return err
		}
		epoch, err = loadFailureEpochTx(ctx, tx, e.FailureEpochID)
		if err != nil {
			return err
		}
		if epoch.EpochState != "CONFIRMED" || epoch.TransitionGeneration != e.EvaluatedTransitionGeneration {
			return ErrFailureSafetyStale
		}
		var rev, evidenceGen uint64
		var digest, lifecycle, evidenceID, evidenceDigest string
		if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest,lifecycle_state FROM kim.failure_fencing_policies_current WHERE policy_id=$1 FOR SHARE`, e.PolicyID).Scan(&rev, &digest, &lifecycle); err != nil || rev != e.PolicyRevision || digest != e.PolicyDigest || lifecycle != "ACTIVE" {
			return ErrFailureSafetyStale
		}
		if err := tx.QueryRow(ctx, `SELECT evidence_generation,evidence_id,evidence_digest FROM kim.source_execution_fencing_observations_current WHERE failure_epoch_id=$1 FOR SHARE`, e.FailureEpochID).Scan(&evidenceGen, &evidenceID, &evidenceDigest); err != nil || evidenceGen != e.LatestFencingEvidenceGeneration || evidenceID != e.FencingEvidenceID || evidenceDigest != e.FencingEvidenceDigest {
			return ErrFailureSafetyStale
		}
		proof = FailureFencingProof{ProofID: proofID, FailureEpochID: e.FailureEpochID, ExpectedTransitionGeneration: e.EvaluatedTransitionGeneration, EvaluationID: evaluationID, ProofType: "KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF", ProofState: "PROVEN", PolicyID: e.PolicyID, PolicyRevision: e.PolicyRevision, PolicyDigest: e.PolicyDigest, FencingEvidenceID: e.FencingEvidenceID, FencingEvidenceDigest: e.FencingEvidenceDigest, DecidedBy: decidedBy}
		raw, _ := json.Marshal(proof)
		proof.ProofDigest = digestReleaseBytes(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_fencing_proof_evidence(proof_id,failure_epoch_id,expected_transition_generation,evaluation_id,proof_type,proof_state,fencing_policy_id,fencing_policy_revision,fencing_policy_digest,fencing_evidence_id,fencing_evidence_digest,decided_by,proof_digest) VALUES($1,$2,$3,$4,$5,'PROVEN',$6,$7,$8,$9,$10,$11,$12)`, proof.ProofID, proof.FailureEpochID, proof.ExpectedTransitionGeneration, proof.EvaluationID, proof.ProofType, proof.PolicyID, proof.PolicyRevision, proof.PolicyDigest, proof.FencingEvidenceID, proof.FencingEvidenceDigest, proof.DecidedBy, proof.ProofDigest); err != nil {
			return ErrFailureSafetyStale
		}
		next := epoch.TransitionGeneration + 1
		td := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/FENCED/%s", epoch.FailureEpochID, next, proof.ProofDigest)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_epoch_transition_evidence(failure_epoch_id,transition_generation,from_state,to_state,cause_evidence_id,confirmation_decision_id,fencing_proof_id,transition_digest) VALUES($1,$2,'CONFIRMED','FENCED',NULL,NULL,$3,$4)`, epoch.FailureEpochID, next, proof.ProofID, td); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.failure_epochs_current SET epoch_state='FENCED',transition_generation=$2,updated_at=statement_timestamp() WHERE failure_epoch_id=$1 AND epoch_state='CONFIRMED' AND transition_generation=$3`, epoch.FailureEpochID, next, epoch.TransitionGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrFailureSafetyStale
		}
		epoch.EpochState = "FENCED"
		epoch.TransitionGeneration = next
		return nil
	})
	return proof, epoch, err
}

func loadStorageSafetyInputs(ctx context.Context, tx pgx.Tx, epoch FailureEpoch) ([]storageSafetyInput, error) {
	rows, err := tx.Query(ctx, `SELECT o.evidence_id,o.attachment_id,o.attachment_generation,o.observation_generation,o.observation_digest,cl.attachment_claim_id,cl.claim_state,cl.claim_state_generation,b.binding_id,b.binding_generation,b.observation_generation,b.binding_state,c.attachment_state,o.evidence_state,o.device_present,o.holder_open FROM kim.volume_attachments_current a JOIN kim.volume_attachment_claims cl ON cl.attachment_id=a.attachment_id AND cl.attachment_generation=a.attachment_generation JOIN kim.volume_attachment_observations_current c ON c.attachment_id=a.attachment_id AND c.attachment_generation=a.attachment_generation JOIN kim.volume_attachment_observation_evidence o ON o.evidence_id=c.evidence_id AND o.attachment_id=c.attachment_id AND o.attachment_generation=c.attachment_generation JOIN kim.volume_backend_bindings_current b ON b.binding_id=o.binding_id AND b.binding_generation=o.binding_generation WHERE a.placement_admission_id=$1 ORDER BY a.attachment_id`, epoch.AdmissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storageSafetyInput
	for rows.Next() {
		var i storageSafetyInput
		if err := rows.Scan(&i.EvidenceID, &i.AttachmentID, &i.AttachmentGeneration, &i.ObservationGeneration, &i.ObservationDigest, &i.AttachmentClaimID, &i.ClaimState, &i.ClaimStateGeneration, &i.BindingID, &i.BindingGeneration, &i.BindingObservationGeneration, &i.BindingState, &i.AttachmentState, &i.EvidenceState, &i.DevicePresent, &i.HolderOpen); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func EvaluateStorageSafety(ctx context.Context, db TxBeginner, evaluationID, epochID, evaluatorVersion, evaluatorDigest string) (StorageSafetyEvaluation, error) {
	var out StorageSafetyEvaluation
	if evaluationID == "" || epochID == "" || evaluatorVersion == "" || evaluatorDigest == "" {
		return out, ErrFailureSafetyConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "storage-safety-evaluation/"+evaluationID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,COALESCE(storage_safety_policy_id,''),COALESCE(storage_safety_policy_revision,0),COALESCE(storage_safety_policy_digest,''),evidence_set_digest,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.storage_safety_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&out.EvaluationID, &out.FailureEpochID, &out.EvaluatedTransitionGeneration, &out.EvaluatedEpochState, &out.AvailabilityBindingRevision, &out.AvailabilityBindingDigest, &out.PolicyID, &out.PolicyRevision, &out.PolicyDigest, &out.EvidenceSetDigest, &out.ResultState, &out.ReasonCode, &out.EvaluatorVersion, &out.EvaluatorDigest, &out.EvaluationDigest)
		if err == nil {
			if out.FailureEpochID != epochID || out.EvaluatorVersion != evaluatorVersion || out.EvaluatorDigest != evaluatorDigest {
				return ErrFailureSafetyConflict
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
		out = StorageSafetyEvaluation{EvaluationID: evaluationID, FailureEpochID: epochID, EvaluatedTransitionGeneration: epoch.TransitionGeneration, EvaluatedEpochState: epoch.EpochState, AvailabilityBindingRevision: epoch.AvailabilityBindingRevision, AvailabilityBindingDigest: epoch.AvailabilityBindingDigest, EvaluatorVersion: evaluatorVersion, EvaluatorDigest: evaluatorDigest}
		inputs, err := loadStorageSafetyInputs(ctx, tx, epoch)
		if err != nil {
			return err
		}
		rawInputs, _ := json.Marshal(inputs)
		out.EvidenceSetDigest = digestReleaseBytes(rawInputs)
		err = tx.QueryRow(ctx, `SELECT b.storage_safety_policy_id,b.storage_safety_policy_revision,b.storage_safety_policy_digest FROM kim.failure_epoch_evidence e JOIN kim.availability_policy_storage_safety_binding_evidence b ON b.availability_policy_id=e.availability_policy_id AND b.availability_policy_revision=e.availability_policy_revision AND b.availability_policy_digest=e.availability_policy_digest WHERE e.failure_epoch_id=$1`, epochID).Scan(&out.PolicyID, &out.PolicyRevision, &out.PolicyDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			out.ResultState, out.ReasonCode = "NO_STORAGE_SAFETY_POLICY", "availability_policy_has_no_typed_storage_safety_reference"
		} else if err != nil {
			return err
		} else if epoch.EpochState != "CONFIRMED" {
			out.ResultState, out.ReasonCode = "STALE_EPOCH", "epoch_not_confirmed"
		} else {
			var rev uint64
			var digest, lifecycle string
			if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest,lifecycle_state FROM kim.storage_safety_policies_current WHERE policy_id=$1 FOR SHARE`, out.PolicyID).Scan(&rev, &digest, &lifecycle); err != nil || rev != out.PolicyRevision || digest != out.PolicyDigest || lifecycle != "ACTIVE" {
				out.ResultState, out.ReasonCode = "STALE_POLICY", "storage_safety_policy_not_current_active"
			} else if len(inputs) == 0 {
				out.ResultState, out.ReasonCode = "NOT_SAFE", "local_lvm_attachment_evidence_missing"
			} else {
				out.ResultState, out.ReasonCode = "SAFE", "all_local_lvm_sources_detached_no_holder"
				for _, i := range inputs {
					if i.AttachmentState == "UNKNOWN" || i.EvidenceState == "UNKNOWN" {
						out.ResultState, out.ReasonCode = "UNKNOWN", "storage_evidence_unknown"
						break
					}
					if i.AttachmentState == "CONFLICTING" || i.EvidenceState == "CONFLICTING" {
						out.ResultState, out.ReasonCode = "CONFLICTING_INPUT", "storage_evidence_conflicting"
						break
					}
					if i.AttachmentState != "DETACHED" || i.ClaimState != "RELEASED" || i.BindingState != "BOUND" || i.EvidenceState != "MATCHED" || i.DevicePresent || i.HolderOpen {
						out.ResultState, out.ReasonCode = "NOT_SAFE", "source_attachment_not_safely_detached"
						break
					}
				}
			}
		}
		raw, _ := json.Marshal(out)
		out.EvaluationDigest = digestReleaseBytes(raw)
		var pid, prev, pdig any
		if out.PolicyID != "" {
			pid, prev, pdig = out.PolicyID, out.PolicyRevision, out.PolicyDigest
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.storage_safety_evaluation_evidence(evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest,evidence_set_digest,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, out.EvaluationID, out.FailureEpochID, out.EvaluatedTransitionGeneration, out.EvaluatedEpochState, out.AvailabilityBindingRevision, out.AvailabilityBindingDigest, pid, prev, pdig, out.EvidenceSetDigest, out.ResultState, out.ReasonCode, out.EvaluatorVersion, out.EvaluatorDigest, out.EvaluationDigest); err != nil {
			return err
		}
		for n, i := range inputs {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.storage_safety_evaluation_input_evidence(evaluation_id,input_ordinal,attachment_evidence_id,attachment_id,attachment_generation,observation_generation,observation_digest,attachment_claim_id,claim_state,claim_state_generation,binding_id,binding_generation,binding_observation_generation,binding_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, out.EvaluationID, n+1, i.EvidenceID, i.AttachmentID, i.AttachmentGeneration, i.ObservationGeneration, i.ObservationDigest, i.AttachmentClaimID, i.ClaimState, i.ClaimStateGeneration, i.BindingID, i.BindingGeneration, i.BindingObservationGeneration, i.BindingState); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func MaterializeStorageSafetyProof(ctx context.Context, db TxBeginner, proofID, evaluationID, decidedBy string) (StorageSafetyProof, error) {
	var proof StorageSafetyProof
	if proofID == "" || evaluationID == "" || decidedBy == "" {
		return proof, ErrFailureSafetyConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "storage-safety-proof/"+proofID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT proof_id,failure_epoch_id,expected_transition_generation,evaluation_id,proof_type,proof_state,storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest,evidence_set_digest,decided_by,proof_digest FROM kim.storage_safety_proof_evidence WHERE proof_id=$1`, proofID).Scan(&proof.ProofID, &proof.FailureEpochID, &proof.ExpectedTransitionGeneration, &proof.EvaluationID, &proof.ProofType, &proof.ProofState, &proof.PolicyID, &proof.PolicyRevision, &proof.PolicyDigest, &proof.EvidenceSetDigest, &proof.DecidedBy, &proof.ProofDigest)
		if err == nil {
			if proof.EvaluationID != evaluationID || proof.DecidedBy != decidedBy {
				return ErrFailureSafetyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var e StorageSafetyEvaluation
		if err := tx.QueryRow(ctx, `SELECT evaluation_id,failure_epoch_id,evaluated_transition_generation,evaluated_epoch_state,availability_binding_revision,availability_binding_digest,storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest,evidence_set_digest,result_state,reason_code,evaluator_version,evaluator_digest,evaluation_digest FROM kim.storage_safety_evaluation_evidence WHERE evaluation_id=$1`, evaluationID).Scan(&e.EvaluationID, &e.FailureEpochID, &e.EvaluatedTransitionGeneration, &e.EvaluatedEpochState, &e.AvailabilityBindingRevision, &e.AvailabilityBindingDigest, &e.PolicyID, &e.PolicyRevision, &e.PolicyDigest, &e.EvidenceSetDigest, &e.ResultState, &e.ReasonCode, &e.EvaluatorVersion, &e.EvaluatorDigest, &e.EvaluationDigest); err != nil {
			return err
		}
		if e.ResultState != "SAFE" {
			return ErrFailureSafetyBlocked
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-epoch/"+e.FailureEpochID); err != nil {
			return err
		}
		epoch, err := loadFailureEpochTx(ctx, tx, e.FailureEpochID)
		if err != nil {
			return err
		}
		if epoch.EpochState != "CONFIRMED" || epoch.TransitionGeneration != e.EvaluatedTransitionGeneration {
			return ErrFailureSafetyStale
		}
		var rev uint64
		var digest, lifecycle string
		if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest,lifecycle_state FROM kim.storage_safety_policies_current WHERE policy_id=$1 FOR SHARE`, e.PolicyID).Scan(&rev, &digest, &lifecycle); err != nil || rev != e.PolicyRevision || digest != e.PolicyDigest || lifecycle != "ACTIVE" {
			return ErrFailureSafetyStale
		}
		var inputCount, currentCount int
		if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE c.evidence_id IS NOT NULL AND cl.attachment_claim_id IS NOT NULL AND b.binding_id IS NOT NULL) FROM kim.storage_safety_evaluation_input_evidence i LEFT JOIN kim.volume_attachment_observations_current c ON c.evidence_id=i.attachment_evidence_id AND c.attachment_id=i.attachment_id AND c.attachment_generation=i.attachment_generation AND c.observation_generation=i.observation_generation LEFT JOIN kim.volume_attachment_claims cl ON cl.attachment_claim_id=i.attachment_claim_id AND cl.claim_state=i.claim_state AND cl.claim_state_generation=i.claim_state_generation LEFT JOIN kim.volume_backend_bindings_current b ON b.binding_id=i.binding_id AND b.binding_generation=i.binding_generation AND b.observation_generation=i.binding_observation_generation AND b.binding_state=i.binding_state WHERE i.evaluation_id=$1`, evaluationID).Scan(&inputCount, &currentCount); err != nil || inputCount == 0 || inputCount != currentCount {
			return ErrFailureSafetyStale
		}
		proof = StorageSafetyProof{ProofID: proofID, FailureEpochID: e.FailureEpochID, ExpectedTransitionGeneration: e.EvaluatedTransitionGeneration, EvaluationID: evaluationID, ProofType: "LOCAL_LVM_SOURCE_DETACHED_NO_HOLDER", ProofState: "SAFE", PolicyID: e.PolicyID, PolicyRevision: e.PolicyRevision, PolicyDigest: e.PolicyDigest, EvidenceSetDigest: e.EvidenceSetDigest, DecidedBy: decidedBy}
		raw, _ := json.Marshal(proof)
		proof.ProofDigest = digestReleaseBytes(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.storage_safety_proof_evidence(proof_id,failure_epoch_id,expected_transition_generation,evaluation_id,proof_type,proof_state,storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest,evidence_set_digest,decided_by,proof_digest) VALUES($1,$2,$3,$4,$5,'SAFE',$6,$7,$8,$9,$10,$11)`, proof.ProofID, proof.FailureEpochID, proof.ExpectedTransitionGeneration, proof.EvaluationID, proof.ProofType, proof.PolicyID, proof.PolicyRevision, proof.PolicyDigest, proof.EvidenceSetDigest, proof.DecidedBy, proof.ProofDigest); err != nil {
			return ErrFailureSafetyStale
		}
		return nil
	})
	return proof, err
}
