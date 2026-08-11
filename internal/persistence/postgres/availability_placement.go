package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// AvailabilityPlacementCandidate keeps ordinary placement and availability
// responsibility separate while carrying both complete dry provenances.
type AvailabilityPlacementCandidate struct {
	Placement              PlacementScopeCandidate
	AvailabilityStatus     string
	AvailabilityResolution GroupPolicyResolution
}

type AvailabilityPlacementDryResult struct {
	Scope      PlacementScopeDryResult
	Status     string
	Candidates []AvailabilityPlacementCandidate
}

// DryEvaluateAvailabilityPlacementScope performs no writes. The ordinary
// Placement Scope evaluation remains a compatibility API; this is the new
// authoritative path that additionally requires an exact AvailabilityPolicy.
func DryEvaluateAvailabilityPlacementScope(ctx context.Context, db TxBeginner, request PlacementAdmissionRequest) (AvailabilityPlacementDryResult, error) {
	scopeDry, err := DryEvaluatePlacementScope(ctx, db, request)
	if err != nil {
		return AvailabilityPlacementDryResult{}, err
	}
	result := AvailabilityPlacementDryResult{Scope: scopeDry, Status: scopeDry.Status}
	if scopeDry.Status != "READY" && scopeDry.Status != "VISIBLE_BUT_NO_ELIGIBLE_HOST" {
		return result, nil
	}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		result.Status = "VISIBLE_BUT_NO_ELIGIBLE_HOST"
		for _, candidate := range scopeDry.Candidates {
			availability := AvailabilityPlacementCandidate{Placement: candidate}
			resolution, err := resolveGroupPolicyTx(ctx, tx, GroupPolicyResolutionRequest{
				ResolutionID: "availability-resolution:" + request.RequestID + ":" + candidate.HostID,
				HostID:       candidate.HostID, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", ReadOnly: true,
			})
			if err != nil {
				return err
			}
			availability.AvailabilityStatus = resolution.Result
			availability.AvailabilityResolution = resolution
			if resolution.Result != "RESOLVED" {
				availability.Placement.Eligible = false
			}
			if availability.Placement.Eligible {
				result.Status = "READY"
			}
			result.Candidates = append(result.Candidates, availability)
		}
		return nil
	})
	return result, err
}

// FinalAdmitAvailabilityPlacementScope persists the generic resolution,
// ordinary Admission/resource claims, and immutable VM Availability Binding
// under one outer PostgreSQL transaction. It never silently re-resolves to a
// different current policy after Dry.
func FinalAdmitAvailabilityPlacementScope(ctx context.Context, db TxBeginner, dry AvailabilityPlacementDryResult, request PlacementAdmissionRequest, candidate AvailabilityPlacementCandidate) (PlacementAdmission, error) {
	if dry.Status != "READY" || candidate.AvailabilityStatus != "RESOLVED" ||
		candidate.AvailabilityResolution.ResolutionID == "" || !candidate.Placement.Eligible {
		return PlacementAdmission{}, ErrPlacementIneligible
	}
	var admission PlacementAdmission
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var admissionAlreadyCommitted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.placement_admission_decisions WHERE request_id=$1)`, request.RequestID).Scan(&admissionAlreadyCommitted); err != nil {
			return err
		}
		if admissionAlreadyCommitted {
			var err error
			admission, err = FinalAdmitPlacementScope(ctx, scopeTxBeginner{tx}, dry.Scope, request, candidate.Placement)
			if err != nil {
				return err
			}
			binding, err := loadVMAvailabilityBindingTx(ctx, tx, admission.AdmissionID)
			if err != nil {
				return ErrAvailabilityPolicyConflict
			}
			if binding.PolicyResolutionID != candidate.AvailabilityResolution.ResolutionID ||
				binding.PolicyID != candidate.AvailabilityResolution.EffectivePolicyID ||
				binding.PolicyRevision != candidate.AvailabilityResolution.EffectivePolicyRevision ||
				binding.PolicyDigest != candidate.AvailabilityResolution.EffectivePolicyDigest {
				return ErrAvailabilityPolicyConflict
			}
			admission.AvailabilityBinding = &binding
			return nil
		}
		current, err := resolveGroupPolicyTx(ctx, tx, GroupPolicyResolutionRequest{
			ResolutionID: candidate.AvailabilityResolution.ResolutionID,
			HostID:       candidate.Placement.HostID, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT",
		})
		if err != nil {
			return err
		}
		dryResolution := candidate.AvailabilityResolution
		if current.Result != "RESOLVED" || current.InputDigest != dryResolution.InputDigest ||
			current.ResolutionDigest != dryResolution.ResolutionDigest || current.EffectivePolicyID != dryResolution.EffectivePolicyID ||
			current.EffectivePolicyRevision != dryResolution.EffectivePolicyRevision || current.EffectivePolicyDigest != dryResolution.EffectivePolicyDigest {
			return ErrPlacementStale
		}
		admission, err = FinalAdmitPlacementScope(ctx, scopeTxBeginner{tx}, dry.Scope, request, candidate.Placement)
		if err != nil {
			return err
		}
		binding, err := recordVMAvailabilityBindingTx(ctx, tx, request, admission, current)
		if err != nil {
			return err
		}
		admission.AvailabilityBinding = &binding
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrGroupPolicyConflict) {
			return PlacementAdmission{}, ErrPlacementStale
		}
		return PlacementAdmission{}, err
	}
	return admission, nil
}
