package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func qualifyBackendCleanupUnknownReadBackLifecycle(t *testing.T, ctx context.Context, pool *pgxpool.Pool, vmID, sourcePlanID, sourceHostID, destinationAdmissionID, destinationHostID, suffix string) {
	t.Helper()
	rollback := errors.New("rollback backend cleanup lifecycle qualification")
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var sourcePlanDigest string
		var vmGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT plan_digest,vm_generation FROM kim.vm_materialization_plan_evidence WHERE plan_id=$1 AND vm_id=$2::uuid AND host_id=$3`, sourcePlanID, vmID, sourceHostID).Scan(&sourcePlanDigest, &vmGeneration); err != nil {
			return err
		}
		destinationPlanID := "cleanup-destination-plan-" + suffix
		destinationPlanDigest := digestBytes([]byte(destinationPlanID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_materialization_plan_evidence(
			plan_id,vm_id,vm_generation,placement_admission_id,host_id,image_id,image_revision,
			flavor_id,flavor_revision,flavor_shape_digest,compute_allocation_id,root_volume_id,
			root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation,
			plan_payload,plan_digest)
			SELECT $2,vm_id,vm_generation,$3,$4,image_id,image_revision,flavor_id,flavor_revision,
			flavor_shape_digest,compute_allocation_id,root_volume_id,root_binding_id,
			root_binding_generation,root_attachment_id,root_attachment_generation,plan_payload,$5
			FROM kim.vm_materialization_plan_evidence WHERE plan_id=$1`, sourcePlanID, destinationPlanID, destinationAdmissionID, destinationHostID, destinationPlanDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET placement_admission_id=$2,host_id=$3,current_plan_id=$4 WHERE vm_id=$1::uuid`, vmID, destinationAdmissionID, destinationHostID, destinationPlanID); err != nil {
			return err
		}

		operationID := "cleanup-lifecycle-" + suffix
		identity := fmt.Sprintf("domain:%s/host:%s/plan:%s/materialization:1", vmID, sourceHostID, sourcePlanDigest)
		identityDigest := digestBytes([]byte(identity))
		policyDigest := digestBytes([]byte("POST_TERMINAL_EXACT_DOMAIN_RETIREMENT/v1"))
		eligibilityDigest := digestBytes([]byte(operationID + "/" + sourcePlanID + "/" + identityDigest + "/ELIGIBLE"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operation_evidence(
			cleanup_operation_id,cleanup_generation,resource_type,resource_id,resource_generation,
			backend_type,source_host_id,vm_id,vm_generation,source_materialization_generation,
			source_plan_id,source_plan_digest,backend_identity,backend_identity_digest,cleanup_reason,
			cleanup_policy_revision,cleanup_policy_digest,origin_authority_type,origin_authority_id,
			eligibility_state,eligibility_reason,eligibility_digest)
			VALUES($1,1,'LIBVIRT_DOMAIN',$2::text,1,'LIBVIRT',$3,$2::uuid,$4,1,$5,$6,$7,$8,
			'ABORTED_MOVE','POST_TERMINAL_EXACT_DOMAIN_RETIREMENT/v1',$9,'MATERIALIZATION',$5,
			'ELIGIBLE','qualification_exact_obsolete_materialization',$10)`, operationID, vmID, sourceHostID,
			vmGeneration, sourcePlanID, sourcePlanDigest, identity, identityDigest, policyDigest, eligibilityDigest); err != nil {
			return err
		}
		originEvidenceID := "cleanup-origin-materialization-" + suffix
		originDigest := digestBytes([]byte(originEvidenceID + "/ACCEPTED"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_origin_eligibility_evidence(
			origin_eligibility_id,cleanup_operation_id,cleanup_generation,origin_authority_type,
			origin_authority_id,origin_authority_state,producer_type,producer_evidence_digest,
			eligibility_digest,evidence_digest)
			VALUES($1,$2,1,'MATERIALIZATION',$3,'ACCEPTED','MATERIALIZATION',$4,$5,$6)`,
			originEvidenceID, operationID, sourcePlanID, sourcePlanDigest, eligibilityDigest, originDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operations_current(cleanup_operation_id,cleanup_generation,operation_state) VALUES($1,1,'PENDING'); INSERT INTO kim.source_backend_cleanup_current(vm_id,source_materialization_generation,source_host_id,domain_state,storage_state,network_state,pci_state) VALUES($2::uuid,1,$3,'PENDING','BLOCKED','NOT_REQUIRED','NOT_REQUIRED')`, pgx.QueryExecModeSimpleProtocol, operationID, vmID, sourceHostID); err != nil {
			return err
		}

		db := nestedTestTxBeginner{tx}
		claim1, err := ClaimBackendCleanup(ctx, db, operationID, 1, "cleanup-worker-1", time.Minute)
		if err != nil || claim1.Mode != "APPLY_ALLOWED" || claim1.ClaimGeneration != 1 {
			return fmt.Errorf("initial cleanup claim=%+v err=%w", claim1, err)
		}
		command1, err := AuthorizeBackendCleanupCommand(ctx, db, claim1, "cleanup-job-1-"+suffix, "cleanup-command-1-"+suffix)
		if err != nil {
			return err
		}
		unknownObservationDigest := digestBytes([]byte("cleanup-unknown-observation-" + suffix))
		unknownVerifierDigest := digestBytes([]byte("cleanup-verifier"))
		if err := seedCleanupCommandVerification(ctx, tx, command1.CommandID, sourceHostID, "cleanup-verification-1-"+suffix, "UNKNOWN", unknownObservationDigest, unknownVerifierDigest); err != nil {
			return err
		}
		present, running := true, false
		if err := CompleteBackendCleanup(ctx, db, claim1, BackendCleanupObservation{
			EvidenceID: "cleanup-evidence-1-" + suffix, OperationID: operationID, OperationGeneration: 1,
			ClaimGeneration: 1, ResourceType: "LIBVIRT_DOMAIN", ResourceID: vmID, ResourceGeneration: 1,
			SourceHostID: sourceHostID, VMID: vmID, VMGeneration: vmGeneration, MaterializationGeneration: 1,
			BackendIdentityDigest: identityDigest, BackendPresent: &present, BackendRunning: &running,
			IdentityMatches: true, ApplyResponseState: "LOST", CommandID: command1.CommandID, AttemptIndex: 1,
			VerificationID: "cleanup-verification-1-" + suffix, VerifierDigest: unknownVerifierDigest,
			ObservationGeneration: 1, ObservationDigest: unknownObservationDigest, ResultState: "UNKNOWN",
			ArtifactDigest: digestBytes([]byte("cleanup-artifact-1")), EvidenceDigest: digestBytes([]byte("cleanup-evidence-digest-1-" + suffix)),
		}); err != nil {
			return err
		}

		claim2, err := ClaimBackendCleanup(ctx, db, operationID, 1, "cleanup-worker-2", time.Minute)
		if err != nil || claim2.Mode != "READ_BACK_FIRST" || claim2.ClaimGeneration != 2 {
			return fmt.Errorf("successor cleanup claim=%+v err=%w", claim2, err)
		}
		if _, err := AuthorizeBackendCleanupCommand(ctx, db, claim2, "cleanup-blind-job-"+suffix, "cleanup-blind-command-"+suffix); !errors.Is(err, ErrBackendCleanupStale) {
			return fmt.Errorf("READ_BACK_FIRST granted blind apply: %v", err)
		}
		presentBranchRollback := errors.New("rollback exact-present read-back branch")
		if err := pgx.BeginTxFunc(ctx, nestedTestTxBeginner{tx}, pgx.TxOptions{}, func(branch pgx.Tx) error {
			branchDB := nestedTestTxBeginner{branch}
			readBack, err := AuthorizeBackendCleanupReadBackCommand(ctx, branchDB, claim2, "cleanup-present-read-back-job-"+suffix, "cleanup-present-read-back-command-"+suffix)
			if err != nil {
				return err
			}
			presentDigest := digestBytes([]byte("cleanup-present-observation-" + suffix))
			if err := seedCleanupCommandVerification(ctx, branch, readBack.CommandID, sourceHostID, "cleanup-present-verification-"+suffix, "NOT_APPLIED", presentDigest, unknownVerifierDigest); err != nil {
				return err
			}
			present, running := true, false
			if err := CompleteBackendCleanup(ctx, branchDB, claim2, BackendCleanupObservation{
				EvidenceID: "cleanup-present-evidence-" + suffix, OperationID: operationID, OperationGeneration: 1,
				ClaimGeneration: 2, ResourceType: "LIBVIRT_DOMAIN", ResourceID: vmID, ResourceGeneration: 1,
				SourceHostID: sourceHostID, VMID: vmID, VMGeneration: vmGeneration, MaterializationGeneration: 1,
				BackendIdentityDigest: identityDigest, BackendPresent: &present, BackendRunning: &running,
				IdentityMatches: true, ApplyResponseState: "NOT_APPLICABLE", CommandID: readBack.CommandID, AttemptIndex: 1,
				VerificationID: "cleanup-present-verification-" + suffix, VerifierDigest: unknownVerifierDigest,
				ObservationGeneration: 1, ObservationDigest: presentDigest, ResultState: "PRESENT",
				ArtifactDigest: digestBytes([]byte("cleanup-present-artifact")), EvidenceDigest: digestBytes([]byte("cleanup-present-evidence-digest-" + suffix)),
			}); err != nil {
				return err
			}
			if _, err := AuthorizeBackendCleanupCommand(ctx, branchDB, claim2, "cleanup-after-read-back-job-"+suffix, "cleanup-after-read-back-command-"+suffix); err != nil {
				return fmt.Errorf("exact PRESENT read-back did not grant explicit apply: %w", err)
			}
			return presentBranchRollback
		}); !errors.Is(err, presentBranchRollback) {
			return err
		}
		readBack, err := AuthorizeBackendCleanupReadBackCommand(ctx, db, claim2, "cleanup-read-back-job-"+suffix, "cleanup-read-back-command-"+suffix)
		if err != nil {
			return err
		}
		absentObservationDigest := digestBytes([]byte("cleanup-absent-observation-" + suffix))
		if err := seedCleanupCommandVerification(ctx, tx, readBack.CommandID, sourceHostID, "cleanup-verification-2-"+suffix, "MATCHED", absentObservationDigest, unknownVerifierDigest); err != nil {
			return err
		}
		present, running = false, false
		if err := CompleteBackendCleanup(ctx, db, claim2, BackendCleanupObservation{
			EvidenceID: "cleanup-evidence-2-" + suffix, OperationID: operationID, OperationGeneration: 1,
			ClaimGeneration: 2, ResourceType: "LIBVIRT_DOMAIN", ResourceID: vmID, ResourceGeneration: 1,
			SourceHostID: sourceHostID, VMID: vmID, VMGeneration: vmGeneration, MaterializationGeneration: 1,
			BackendIdentityDigest: identityDigest, BackendPresent: &present, BackendRunning: &running,
			IdentityMatches: true, ApplyResponseState: "NOT_APPLICABLE", CommandID: readBack.CommandID, AttemptIndex: 1,
			VerificationID: "cleanup-verification-2-" + suffix, VerifierDigest: unknownVerifierDigest,
			ObservationGeneration: 1, ObservationDigest: absentObservationDigest, ResultState: "ABSENT",
			ArtifactDigest: digestBytes([]byte("cleanup-artifact-2")), EvidenceDigest: digestBytes([]byte("cleanup-evidence-digest-2-" + suffix)),
		}); err != nil {
			return err
		}
		var state string
		var attempts, terminals int
		if err := tx.QueryRow(ctx, `SELECT operation_state,(SELECT count(*) FROM kim.backend_cleanup_attempt_evidence WHERE cleanup_operation_id=$1),(SELECT count(*) FROM kim.backend_cleanup_terminal_evidence WHERE cleanup_operation_id=$1) FROM kim.backend_cleanup_operations_current WHERE cleanup_operation_id=$1`, operationID).Scan(&state, &attempts, &terminals); err != nil || state != "VERIFIED" || attempts != 2 || terminals != 1 {
			return fmt.Errorf("cleanup terminal state=%s attempts=%d terminals=%d err=%w", state, attempts, terminals, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.backend_cleanup_observation_evidence SET result_state='CONFLICTING' WHERE cleanup_evidence_id=$1`, "cleanup-evidence-2-"+suffix); err == nil {
			return errors.New("immutable cleanup observation accepted update")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("backend cleanup UNKNOWN/read-back lifecycle: %v", err)
	}
}

func seedCleanupCommandVerification(ctx context.Context, tx pgx.Tx, commandID, hostID, verificationID, state, observationDigest, verifierDigest string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO kim.command_lease_grants(command_id,lease_generation,attempt_index,host_id,host_authority_generation,session_generation,token_digest,not_before,expires_at) VALUES($1,1,1,$2,1,1,$3,statement_timestamp()-interval '1 minute',statement_timestamp()+interval '1 minute'); INSERT INTO kim.command_attempts(command_id,attempt_index,lease_generation,host_authority_generation,session_generation) VALUES($1,1,1,1,1); INSERT INTO kim.command_verification_evidence(verification_id,command_id,attempt_index,observation_generation,observation_digest,verification_state,verifier_artifact_digest,evidence_payload) VALUES($4,$1,1,1,$5,$6,$7,'{}'::jsonb)`, pgx.QueryExecModeSimpleProtocol, commandID, hostID, digestBytes([]byte(commandID+"/lease")), verificationID, observationDigest, state, verifierDigest); err != nil {
		return err
	}
	return nil
}
