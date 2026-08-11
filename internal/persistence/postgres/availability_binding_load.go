package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func loadVMAvailabilityBindingTx(ctx context.Context, tx pgx.Tx, admissionID string) (VMAvailabilityBinding, error) {
	var binding VMAvailabilityBinding
	err := tx.QueryRow(ctx, `SELECT workload_id,binding_revision,admission_id,allocation_id,policy_resolution_id,
		availability_policy_id,availability_policy_revision,availability_policy_digest,responsibility,
		host_failure_action,resolution_input_digest,binding_digest
		FROM kim.vm_availability_binding_evidence WHERE admission_id=$1`, admissionID).Scan(
		&binding.WorkloadID, &binding.BindingRevision, &binding.AdmissionID, &binding.AllocationID,
		&binding.PolicyResolutionID, &binding.PolicyID, &binding.PolicyRevision, &binding.PolicyDigest,
		&binding.Responsibility, &binding.HostFailureAction, &binding.ResolutionInputDigest, &binding.BindingDigest)
	return binding, err
}
