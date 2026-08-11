package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func loadVMAvailabilityBindingTx(ctx context.Context, tx pgx.Tx, admissionID string) (VMAvailabilityBinding, error) {
	var binding VMAvailabilityBinding
	err := tx.QueryRow(ctx, `SELECT workload_id,binding_revision,admission_id,allocation_id,COALESCE(policy_resolution_id,''),
		availability_policy_id,availability_policy_revision,availability_policy_digest,responsibility,
		host_failure_action,COALESCE(resolution_input_digest,''),binding_digest,binding_source,COALESCE(source_binding_revision,0),
		COALESCE(source_binding_digest,''),COALESCE(rebind_id,''),COALESCE(rebind_decision_generation,0)
		FROM kim.vm_availability_binding_evidence e
		WHERE e.admission_id=$1 AND e.binding_revision=1`, admissionID).Scan(
		&binding.WorkloadID, &binding.BindingRevision, &binding.AdmissionID, &binding.AllocationID,
		&binding.PolicyResolutionID, &binding.PolicyID, &binding.PolicyRevision, &binding.PolicyDigest,
		&binding.Responsibility, &binding.HostFailureAction, &binding.ResolutionInputDigest, &binding.BindingDigest,
		&binding.BindingSource, &binding.SourceBindingRevision, &binding.SourceBindingDigest, &binding.RebindID,
		&binding.RebindDecisionGeneration)
	return binding, err
}
