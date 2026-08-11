package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrAvailabilityPolicyConflict = errors.New("Availability Policy authority conflict")

type AvailabilityPolicyRevision struct {
	PolicyID, Responsibility, HostFailureAction                         string
	FailureConfirmationPolicy, FencingRequirements, StorageRequirements string
	NetworkDeviceRequirements, RecoveryEligibilityPolicy                string
	FailureDomainConstraints, RecoveryBudgetPolicyReference             string
	EscalationPolicy, NotificationPolicy, SupportTier                   string
	LifecycleState, CreatedBy, ApprovedBy                               string
	FailureConfirmationPolicyID, FailureConfirmationPolicyDigest        string
	FencingPolicyID, FencingPolicyDigest                                string
	StorageSafetyPolicyID, StorageSafetyPolicyDigest                    string
	PolicyRevision                                                      uint64
	FailureConfirmationPolicyRevision                                   uint64
	FencingPolicyRevision, StorageSafetyPolicyRevision                  uint64
	MaxAttempts                                                         int
}

// availabilityPolicyDigestRevisionV1 preserves the pre-051 canonical digest
// shape. The typed confirmation association is separate immutable authority;
// adding it must not reinterpret historical Availability Policy evidence.
type availabilityPolicyDigestRevisionV1 struct {
	PolicyID, Responsibility, HostFailureAction                         string
	FailureConfirmationPolicy, FencingRequirements, StorageRequirements string
	NetworkDeviceRequirements, RecoveryEligibilityPolicy                string
	FailureDomainConstraints, RecoveryBudgetPolicyReference             string
	EscalationPolicy, NotificationPolicy, SupportTier                   string
	LifecycleState, CreatedBy, ApprovedBy                               string
	PolicyRevision                                                      uint64
	MaxAttempts                                                         int
}

func availabilityPolicyDigestValue(policy AvailabilityPolicyRevision) string {
	v1 := availabilityPolicyDigestRevisionV1{
		PolicyID: policy.PolicyID, Responsibility: policy.Responsibility, HostFailureAction: policy.HostFailureAction,
		FailureConfirmationPolicy: policy.FailureConfirmationPolicy, FencingRequirements: policy.FencingRequirements,
		StorageRequirements: policy.StorageRequirements, NetworkDeviceRequirements: policy.NetworkDeviceRequirements,
		RecoveryEligibilityPolicy: policy.RecoveryEligibilityPolicy, FailureDomainConstraints: policy.FailureDomainConstraints,
		RecoveryBudgetPolicyReference: policy.RecoveryBudgetPolicyReference, EscalationPolicy: policy.EscalationPolicy,
		NotificationPolicy: policy.NotificationPolicy, SupportTier: policy.SupportTier, LifecycleState: policy.LifecycleState,
		CreatedBy: policy.CreatedBy, ApprovedBy: policy.ApprovedBy, PolicyRevision: policy.PolicyRevision, MaxAttempts: policy.MaxAttempts,
	}
	raw, _ := json.Marshal(v1)
	return digestReleaseBytes(raw)
}

type VMAvailabilityBinding struct {
	WorkloadID, AdmissionID, AllocationID, PolicyResolutionID string
	PolicyID, PolicyDigest, Responsibility, HostFailureAction string
	ResolutionInputDigest, BindingDigest                      string
	BindingSource, SourceBindingDigest, RebindID              string
	BindingRevision, PolicyRevision                           uint64
	SourceBindingRevision, RebindDecisionGeneration           uint64
}

func validAvailabilityPolicy(policy AvailabilityPolicyRevision) bool {
	if policy.PolicyID == "" || policy.PolicyRevision == 0 || policy.MaxAttempts <= 0 ||
		policy.FailureConfirmationPolicy == "" || policy.FencingRequirements == "" ||
		policy.StorageRequirements == "" || policy.NetworkDeviceRequirements == "" ||
		policy.RecoveryEligibilityPolicy == "" || policy.FailureDomainConstraints == "" ||
		policy.RecoveryBudgetPolicyReference == "" || policy.EscalationPolicy == "" ||
		policy.NotificationPolicy == "" || policy.SupportTier == "" || policy.CreatedBy == "" || policy.ApprovedBy == "" {
		return false
	}
	if policy.LifecycleState != "DRAFT" && policy.LifecycleState != "ACTIVE" &&
		policy.LifecycleState != "DEPRECATED" && policy.LifecycleState != "RETIRED" {
		return false
	}
	hasConfirmationRef := policy.FailureConfirmationPolicyID != "" || policy.FailureConfirmationPolicyRevision != 0 || policy.FailureConfirmationPolicyDigest != ""
	if hasConfirmationRef && (policy.FailureConfirmationPolicyID == "" || policy.FailureConfirmationPolicyRevision == 0 || policy.FailureConfirmationPolicyDigest == "") {
		return false
	}
	hasFencingRef := policy.FencingPolicyID != "" || policy.FencingPolicyRevision != 0 || policy.FencingPolicyDigest != ""
	if hasFencingRef && (policy.FencingPolicyID == "" || policy.FencingPolicyRevision == 0 || policy.FencingPolicyDigest == "") {
		return false
	}
	hasStorageSafetyRef := policy.StorageSafetyPolicyID != "" || policy.StorageSafetyPolicyRevision != 0 || policy.StorageSafetyPolicyDigest != ""
	if hasStorageSafetyRef && (policy.StorageSafetyPolicyID == "" || policy.StorageSafetyPolicyRevision == 0 || policy.StorageSafetyPolicyDigest == "") {
		return false
	}
	return (policy.Responsibility == "INFRASTRUCTURE_MANAGED" &&
		(policy.HostFailureAction == "RESTART_ON_OTHER_HOST" || policy.HostFailureAction == "EVACUATE")) ||
		((policy.Responsibility == "WORKLOAD_MANAGED" || policy.Responsibility == "MANUAL") &&
			policy.HostFailureAction == "NO_AUTOMATIC_ACTION")
}

func PublishAvailabilityPolicy(ctx context.Context, db TxBeginner, policy AvailabilityPolicyRevision) (string, error) {
	if !validAvailabilityPolicy(policy) {
		return "", ErrAvailabilityPolicyConflict
	}
	digest := availabilityPolicyDigestValue(policy)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-policy/"+policy.PolicyID); err != nil {
			return err
		}
		var recorded string
		err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.availability_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&recorded)
		if err == nil {
			if recorded != digest {
				return ErrAvailabilityPolicyConflict
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		revisionExists := err == nil
		if err := publishGroupPolicyCatalogTx(ctx, tx, "AVAILABILITY_POLICY", policy.PolicyID, policy.PolicyRevision, digest, policy.LifecycleState); err != nil {
			return err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO kim.availability_policy_revision_evidence(
				policy_id,policy_revision,responsibility,host_failure_action,failure_confirmation_policy,
				fencing_requirements,storage_requirements,network_device_requirements,recovery_eligibility_policy,
				failure_domain_constraints,recovery_budget_policy_reference,max_attempts,escalation_policy,
				notification_policy,support_tier,lifecycle_state,created_by,approved_by,policy_digest)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
				policy.PolicyID, policy.PolicyRevision, policy.Responsibility, policy.HostFailureAction,
				policy.FailureConfirmationPolicy, policy.FencingRequirements, policy.StorageRequirements,
				policy.NetworkDeviceRequirements, policy.RecoveryEligibilityPolicy, policy.FailureDomainConstraints,
				policy.RecoveryBudgetPolicyReference, policy.MaxAttempts, policy.EscalationPolicy,
				policy.NotificationPolicy, policy.SupportTier, policy.LifecycleState, policy.CreatedBy, policy.ApprovedBy, digest)
			if err != nil {
				return err
			}
		}
		if policy.FailureConfirmationPolicyID != "" {
			var lifecycle string
			if err := tx.QueryRow(ctx, `SELECT lifecycle_state FROM kim.failure_confirmation_policies_current
				WHERE policy_id=$1 AND policy_revision=$2 AND policy_digest=$3 FOR SHARE`, policy.FailureConfirmationPolicyID,
				policy.FailureConfirmationPolicyRevision, policy.FailureConfirmationPolicyDigest).Scan(&lifecycle); err != nil || lifecycle != "ACTIVE" {
				return ErrAvailabilityPolicyConflict
			}
			bindingRaw, _ := json.Marshal([]any{policy.PolicyID, policy.PolicyRevision, digest,
				policy.FailureConfirmationPolicyID, policy.FailureConfirmationPolicyRevision, policy.FailureConfirmationPolicyDigest})
			bindingDigest := digestReleaseBytes(bindingRaw)
			if revisionExists {
				var existing string
				if err := tx.QueryRow(ctx, `SELECT binding_digest FROM kim.availability_policy_confirmation_binding_evidence WHERE availability_policy_id=$1 AND availability_policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&existing); err != nil || existing != bindingDigest {
					return ErrAvailabilityPolicyConflict
				}
			} else if _, err := tx.Exec(ctx, `INSERT INTO kim.availability_policy_confirmation_binding_evidence(
				availability_policy_id,availability_policy_revision,availability_policy_digest,confirmation_policy_id,
				confirmation_policy_revision,confirmation_policy_digest,binding_digest) VALUES($1,$2,$3,$4,$5,$6,$7)
				`, policy.PolicyID, policy.PolicyRevision, digest, policy.FailureConfirmationPolicyID,
				policy.FailureConfirmationPolicyRevision, policy.FailureConfirmationPolicyDigest, bindingDigest); err != nil {
				return err
			}
		} else if revisionExists {
			var associationCount int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.availability_policy_confirmation_binding_evidence WHERE availability_policy_id=$1 AND availability_policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&associationCount); err != nil || associationCount != 0 {
				return ErrAvailabilityPolicyConflict
			}
		}
		if policy.FencingPolicyID != "" {
			var lifecycle string
			if err := tx.QueryRow(ctx, `SELECT lifecycle_state FROM kim.failure_fencing_policies_current WHERE policy_id=$1 AND policy_revision=$2 AND policy_digest=$3 FOR SHARE`, policy.FencingPolicyID, policy.FencingPolicyRevision, policy.FencingPolicyDigest).Scan(&lifecycle); err != nil || lifecycle != "ACTIVE" {
				return ErrAvailabilityPolicyConflict
			}
			bindingRaw, _ := json.Marshal([]any{policy.PolicyID, policy.PolicyRevision, digest, policy.FencingPolicyID, policy.FencingPolicyRevision, policy.FencingPolicyDigest})
			bindingDigest := digestReleaseBytes(bindingRaw)
			if revisionExists {
				var existing string
				if err := tx.QueryRow(ctx, `SELECT binding_digest FROM kim.availability_policy_fencing_binding_evidence WHERE availability_policy_id=$1 AND availability_policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&existing); err != nil || existing != bindingDigest {
					return ErrAvailabilityPolicyConflict
				}
			} else if _, err := tx.Exec(ctx, `INSERT INTO kim.availability_policy_fencing_binding_evidence(availability_policy_id,availability_policy_revision,availability_policy_digest,fencing_policy_id,fencing_policy_revision,fencing_policy_digest,binding_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, policy.PolicyID, policy.PolicyRevision, digest, policy.FencingPolicyID, policy.FencingPolicyRevision, policy.FencingPolicyDigest, bindingDigest); err != nil {
				return err
			}
		} else if revisionExists {
			var associationCount int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.availability_policy_fencing_binding_evidence WHERE availability_policy_id=$1 AND availability_policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&associationCount); err != nil || associationCount != 0 {
				return ErrAvailabilityPolicyConflict
			}
		}
		if policy.StorageSafetyPolicyID != "" {
			var lifecycle string
			if err := tx.QueryRow(ctx, `SELECT lifecycle_state FROM kim.storage_safety_policies_current WHERE policy_id=$1 AND policy_revision=$2 AND policy_digest=$3 FOR SHARE`, policy.StorageSafetyPolicyID, policy.StorageSafetyPolicyRevision, policy.StorageSafetyPolicyDigest).Scan(&lifecycle); err != nil || lifecycle != "ACTIVE" {
				return ErrAvailabilityPolicyConflict
			}
			bindingRaw, _ := json.Marshal([]any{policy.PolicyID, policy.PolicyRevision, digest, policy.StorageSafetyPolicyID, policy.StorageSafetyPolicyRevision, policy.StorageSafetyPolicyDigest})
			bindingDigest := digestReleaseBytes(bindingRaw)
			if revisionExists {
				var existing string
				if err := tx.QueryRow(ctx, `SELECT binding_digest FROM kim.availability_policy_storage_safety_binding_evidence WHERE availability_policy_id=$1 AND availability_policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&existing); err != nil || existing != bindingDigest {
					return ErrAvailabilityPolicyConflict
				}
			} else if _, err := tx.Exec(ctx, `INSERT INTO kim.availability_policy_storage_safety_binding_evidence(availability_policy_id,availability_policy_revision,availability_policy_digest,storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest,binding_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, policy.PolicyID, policy.PolicyRevision, digest, policy.StorageSafetyPolicyID, policy.StorageSafetyPolicyRevision, policy.StorageSafetyPolicyDigest, bindingDigest); err != nil {
				return err
			}
		} else if revisionExists {
			var associationCount int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.availability_policy_storage_safety_binding_evidence WHERE availability_policy_id=$1 AND availability_policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&associationCount); err != nil || associationCount != 0 {
				return ErrAvailabilityPolicyConflict
			}
		}
		command, err := tx.Exec(ctx, `INSERT INTO kim.availability_policies_current(policy_id,policy_revision,lifecycle_state,policy_digest)
			VALUES($1,$2,$3,$4) ON CONFLICT(policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,
			lifecycle_state=EXCLUDED.lifecycle_state,policy_digest=EXCLUDED.policy_digest,updated_at=statement_timestamp()
			WHERE kim.availability_policies_current.policy_revision<EXCLUDED.policy_revision`, policy.PolicyID,
			policy.PolicyRevision, policy.LifecycleState, digest)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			var revision uint64
			var currentDigest string
			if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest FROM kim.availability_policies_current WHERE policy_id=$1`, policy.PolicyID).Scan(&revision, &currentDigest); err != nil || revision != policy.PolicyRevision || currentDigest != digest {
				return ErrAvailabilityPolicyConflict
			}
		}
		return nil
	})
	return digest, err
}

func publishGroupPolicyCatalogTx(ctx context.Context, tx pgx.Tx, policyType, policyID string, revision uint64, digest, lifecycle string) error {
	var recorded string
	err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.group_policy_revision_catalog WHERE policy_type=$1 AND policy_id=$2 AND policy_revision=$3`, policyType, policyID, revision).Scan(&recorded)
	if err == nil && recorded != digest {
		return ErrGroupPolicyConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `INSERT INTO kim.group_policy_revision_catalog(policy_type,policy_id,policy_revision,policy_digest,lifecycle_state) VALUES($1,$2,$3,$4,$5)`, policyType, policyID, revision, digest, lifecycle); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `INSERT INTO kim.group_policies_current(policy_type,policy_id,policy_revision,policy_digest,lifecycle_state)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(policy_type,policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,
		policy_digest=EXCLUDED.policy_digest,lifecycle_state=EXCLUDED.lifecycle_state,updated_at=statement_timestamp()
		WHERE kim.group_policies_current.policy_revision<EXCLUDED.policy_revision`, policyType, policyID, revision, digest, lifecycle)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		var currentRevision uint64
		var currentDigest string
		if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest FROM kim.group_policies_current WHERE policy_type=$1 AND policy_id=$2`, policyType, policyID).Scan(&currentRevision, &currentDigest); err != nil || currentRevision != revision || currentDigest != digest {
			return ErrGroupPolicyConflict
		}
	}
	return nil
}

func recordVMAvailabilityBindingTx(ctx context.Context, tx pgx.Tx, request PlacementAdmissionRequest, admission PlacementAdmission, resolution GroupPolicyResolution) (VMAvailabilityBinding, error) {
	if resolution.Result != "RESOLVED" {
		return VMAvailabilityBinding{}, ErrAvailabilityPolicyConflict
	}
	var responsibility, action string
	if err := tx.QueryRow(ctx, `SELECT responsibility,host_failure_action FROM kim.availability_policy_revision_evidence
		WHERE policy_id=$1 AND policy_revision=$2 AND policy_digest=$3`, resolution.EffectivePolicyID,
		resolution.EffectivePolicyRevision, resolution.EffectivePolicyDigest).Scan(&responsibility, &action); err != nil {
		return VMAvailabilityBinding{}, err
	}
	binding := VMAvailabilityBinding{WorkloadID: request.WorkloadID, AdmissionID: admission.AdmissionID,
		AllocationID: admission.AllocationID, PolicyResolutionID: resolution.ResolutionID,
		PolicyID: resolution.EffectivePolicyID, PolicyRevision: resolution.EffectivePolicyRevision,
		PolicyDigest: resolution.EffectivePolicyDigest, Responsibility: responsibility,
		HostFailureAction: action, ResolutionInputDigest: resolution.InputDigest, BindingRevision: 1,
		BindingSource: "FINAL_ADMISSION"}
	raw, _ := json.Marshal(binding)
	binding.BindingDigest = digestReleaseBytes(raw)
	command, err := tx.Exec(ctx, `INSERT INTO kim.vm_availability_binding_evidence(
		workload_id,binding_revision,admission_id,allocation_id,policy_resolution_id,availability_policy_id,
		availability_policy_revision,availability_policy_digest,responsibility,host_failure_action,
		resolution_input_digest,binding_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(workload_id,binding_revision) DO NOTHING`, binding.WorkloadID, binding.AdmissionID,
		binding.AllocationID, binding.PolicyResolutionID, binding.PolicyID, binding.PolicyRevision,
		binding.PolicyDigest, binding.Responsibility, binding.HostFailureAction, binding.ResolutionInputDigest, binding.BindingDigest)
	if err != nil {
		return VMAvailabilityBinding{}, err
	}
	if command.RowsAffected() == 0 {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT binding_digest FROM kim.vm_availability_binding_evidence WHERE workload_id=$1 AND binding_revision=1`, binding.WorkloadID).Scan(&existing); err != nil || existing != binding.BindingDigest {
			return VMAvailabilityBinding{}, fmt.Errorf("Availability Binding replay conflict: %w", ErrAvailabilityPolicyConflict)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_availability_bindings_current(workload_id,binding_revision,binding_digest)
		VALUES($1,1,$2) ON CONFLICT(workload_id) DO NOTHING`, binding.WorkloadID, binding.BindingDigest); err != nil {
		return VMAvailabilityBinding{}, err
	}
	return binding, nil
}
