package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrBackendCleanupStale = errors.New("stale backend cleanup authority")

func backendCleanupStillEligibleTx(ctx context.Context, tx pgx.Tx, operationID string, generation uint64) bool {
	var accepted bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM kim.backend_cleanup_operation_evidence cleanup
		JOIN kim.recovery_terminal_decision_evidence terminal ON terminal.terminal_decision_id=cleanup.recovery_terminal_decision_id AND terminal.decision_state='VERIFIED'
		JOIN kim.recovery_operations_current operation ON operation.recovery_operation_id=terminal.recovery_operation_id AND operation.lifecycle_state='VERIFIED'
		JOIN kim.failure_epochs_current epoch ON epoch.failure_epoch_id=terminal.failure_epoch_id AND epoch.epoch_state='RECOVERED'
		JOIN kim.source_materialization_retirements_current retirement ON retirement.retirement_decision_id=cleanup.source_retirement_decision_id AND retirement.vm_id=cleanup.vm_id AND retirement.source_materialization_generation=cleanup.source_materialization_generation AND retirement.retirement_state='RETIRED'
		JOIN kim.virtual_machines_current vm ON vm.vm_id=cleanup.vm_id AND vm.vm_generation=cleanup.vm_generation AND vm.host_id<>cleanup.source_host_id AND vm.current_plan_id<>cleanup.source_plan_id
		WHERE cleanup.cleanup_operation_id=$1 AND cleanup.cleanup_generation=$2 AND cleanup.origin_authority_type='RECOVERY_TERMINAL' AND cleanup.eligibility_state='ELIGIBLE')`, operationID, generation).Scan(&accepted)
	return err == nil && accepted
}

type BackendCleanupOperation struct {
	OperationID, ResourceType, ResourceID, BackendType, SourceHostID                 string
	VMID, SourcePlanID, SourcePlanDigest, BackendIdentity, BackendIdentityDigest     string
	TerminalDecisionID, SourceRetirementDecisionID, EligibilityState, State          string
	EligibilityDigest                                                                string
	OperationGeneration, ResourceGeneration, VMGeneration, MaterializationGeneration uint64
}

type BackendCleanupClaim struct {
	OperationID                          string
	OperationGeneration, ClaimGeneration uint64
	Owner, Mode                          string
	ExpiresAt                            time.Time
}

type BackendCleanupCommand struct{ JobID, CommandID, PayloadDigest string }

type BackendCleanupObservation struct {
	EvidenceID, OperationID, ResourceType, ResourceID, SourceHostID, VMID          string
	BackendIdentityDigest, ApplyResponseState, CommandID, VerificationID           string
	VerifierDigest, ObservationDigest, ResultState, ArtifactDigest, EvidenceDigest string
	OperationGeneration, ClaimGeneration, ResourceGeneration, VMGeneration         uint64
	MaterializationGeneration, ObservationGeneration                               uint64
	AttemptIndex                                                                   int
	BackendPresent, BackendRunning                                                 *bool
	IdentityMatches                                                                bool
}

// CommitRecoverySourceDomainCleanup is a Recovery consumer of the generic
// cleanup aggregate.  The caller supplies only stable logical operation
// identity.  VM UUID, source Host, materialization generation, plan digest and
// backend identity are derived from immutable PostgreSQL authority.
func CommitRecoverySourceDomainCleanup(ctx context.Context, db TxBeginner, operationID string, operationGeneration uint64, terminalDecisionID string) (BackendCleanupOperation, error) {
	var out BackendCleanupOperation
	if operationID == "" || operationGeneration == 0 || terminalDecisionID == "" {
		return out, ErrBackendCleanupStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "backend-cleanup/"+operationID); err != nil {
			return err
		}
		var recoveryOperationID, recoveryState, epochState, destinationHost, destinationPlan string
		if err := tx.QueryRow(ctx, `SELECT t.recovery_operation_id,c.lifecycle_state,e.epoch_state,m.destination_host_id,m.vm_plan_id
			FROM kim.recovery_terminal_decision_evidence t
			JOIN kim.recovery_operations_current c ON c.recovery_operation_id=t.recovery_operation_id
			JOIN kim.failure_epochs_current e ON e.failure_epoch_id=t.failure_epoch_id
			JOIN kim.recovery_materialization_evidence m ON m.recovery_operation_id=t.recovery_operation_id
			WHERE t.terminal_decision_id=$1 AND t.decision_state='VERIFIED'`, terminalDecisionID).Scan(&recoveryOperationID, &recoveryState, &epochState, &destinationHost, &destinationPlan); err != nil || recoveryState != "VERIFIED" || epochState != "RECOVERED" {
			return ErrBackendCleanupStale
		}
		var networkCount, pciCount int
		if err := tx.QueryRow(ctx, `SELECT jsonb_array_length(a.network_requirements),jsonb_array_length(a.pci_requirements) FROM kim.recovery_materialization_evidence m JOIN kim.placement_admission_decisions a ON a.admission_id=m.destination_admission_id WHERE m.recovery_operation_id=$1`, recoveryOperationID).Scan(&networkCount, &pciCount); err != nil {
			return ErrBackendCleanupStale
		}
		if err := tx.QueryRow(ctx, `SELECT d.vm_id::text,d.vm_generation,d.source_materialization_generation,d.source_host_id,d.source_plan_id,d.source_plan_digest,d.retirement_decision_id
			FROM kim.source_materialization_retirement_decision_evidence d
			JOIN kim.recovery_terminal_decision_evidence t ON t.failure_epoch_id=d.failure_epoch_id
			JOIN kim.source_materialization_retirements_current current ON current.retirement_decision_id=d.retirement_decision_id AND current.retirement_state='RETIRED'
			WHERE t.terminal_decision_id=$1`, terminalDecisionID).Scan(&out.VMID, &out.VMGeneration, &out.MaterializationGeneration, &out.SourceHostID, &out.SourcePlanID, &out.SourcePlanDigest, &out.SourceRetirementDecisionID); err != nil {
			return ErrBackendCleanupStale
		}
		var currentHost, currentPlan string
		if err := tx.QueryRow(ctx, `SELECT host_id,current_plan_id FROM kim.virtual_machines_current WHERE vm_id=$1 AND vm_generation=$2 FOR SHARE`, out.VMID, out.VMGeneration).Scan(&currentHost, &currentPlan); err != nil || currentHost != destinationHost || currentPlan != destinationPlan || currentHost == out.SourceHostID || currentPlan == out.SourcePlanID {
			return ErrBackendCleanupStale
		}
		out.OperationID, out.OperationGeneration = operationID, operationGeneration
		out.ResourceType, out.ResourceID, out.ResourceGeneration = "LIBVIRT_DOMAIN", out.VMID, out.MaterializationGeneration
		out.BackendType, out.TerminalDecisionID = "LIBVIRT", terminalDecisionID
		out.BackendIdentity = fmt.Sprintf("domain:%s/host:%s/plan:%s/materialization:%d", out.VMID, out.SourceHostID, out.SourcePlanDigest, out.MaterializationGeneration)
		out.BackendIdentityDigest = digestReleaseBytes([]byte(out.BackendIdentity))
		policyDigest := digestReleaseBytes([]byte("POST_TERMINAL_EXACT_DOMAIN_RETIREMENT/v1"))
		out.EligibilityState = "ELIGIBLE"
		out.EligibilityDigest = digestReleaseBytes([]byte(operationID + "/" + terminalDecisionID + "/" + out.BackendIdentityDigest + "/ELIGIBLE"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation,resource_type,resource_id,resource_generation,backend_type,source_host_id,vm_id,vm_generation,source_materialization_generation,source_plan_id,source_plan_digest,backend_identity,backend_identity_digest,cleanup_reason,cleanup_policy_revision,cleanup_policy_digest,origin_authority_type,origin_authority_id,recovery_terminal_decision_id,source_retirement_decision_id,eligibility_state,eligibility_reason,eligibility_digest)
			VALUES($1,$2,'LIBVIRT_DOMAIN',$3,$4,'LIBVIRT',$5,$3::uuid,$6,$4,$7,$8,$9,$10,'RECOVERY_SUPERSEDED','POST_TERMINAL_EXACT_DOMAIN_RETIREMENT/v1',$11,'RECOVERY_TERMINAL',$12,$12,$13,'ELIGIBLE','exact_terminal_source_incarnation',$14) ON CONFLICT(cleanup_operation_id,cleanup_generation) DO NOTHING`, operationID, operationGeneration, out.VMID, out.MaterializationGeneration, out.SourceHostID, out.VMGeneration, out.SourcePlanID, out.SourcePlanDigest, out.BackendIdentity, out.BackendIdentityDigest, policyDigest, terminalDecisionID, out.SourceRetirementDecisionID, out.EligibilityDigest); err != nil {
			return err
		}
		var accepted string
		if err := tx.QueryRow(ctx, `SELECT eligibility_digest FROM kim.backend_cleanup_operation_evidence WHERE cleanup_operation_id=$1 AND cleanup_generation=$2`, operationID, operationGeneration).Scan(&accepted); err != nil || accepted != out.EligibilityDigest {
			return ErrBackendCleanupStale
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operations_current(cleanup_operation_id,cleanup_generation,operation_state) VALUES($1,$2,'PENDING') ON CONFLICT DO NOTHING`, operationID, operationGeneration); err != nil {
			return err
		}
		networkState, pciState := "PENDING", "BLOCKED"
		if networkCount == 0 {
			networkState = "NOT_REQUIRED"
		}
		if pciCount == 0 {
			pciState = "NOT_REQUIRED"
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.source_backend_cleanup_current(vm_id,source_materialization_generation,source_host_id,domain_state,storage_state,network_state,pci_state,cleanup_complete)
			VALUES($1::uuid,$2,$3,'PENDING','BLOCKED',$4,$5,false)
			ON CONFLICT(vm_id,source_materialization_generation) DO NOTHING`, out.VMID, out.MaterializationGeneration, out.SourceHostID, networkState, pciState)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT operation_state FROM kim.backend_cleanup_operations_current WHERE cleanup_operation_id=$1 AND cleanup_generation=$2`, operationID, operationGeneration).Scan(&out.State)
	})
	return out, err
}

// CommitRecoverySourceNetworkCleanup records a no-mutation cleanup only when
// the existing generic Port retirement/Handoff evidence proves every source
// Host binding and OVS interface absent while the logical Port/MAC/IP remains
// active at the destination.
func CommitRecoverySourceNetworkCleanup(ctx context.Context, db TxBeginner, operationID string, operationGeneration uint64, terminalDecisionID string) (BackendCleanupOperation, error) {
	var out BackendCleanupOperation
	if operationID == "" || operationGeneration == 0 || terminalDecisionID == "" {
		return out, ErrBackendCleanupStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var recoveryOperationID, recoveryState, epochState, destinationAdmission string
		if err := tx.QueryRow(ctx, `SELECT t.recovery_operation_id,c.lifecycle_state,e.epoch_state,m.destination_admission_id FROM kim.recovery_terminal_decision_evidence t JOIN kim.recovery_operations_current c ON c.recovery_operation_id=t.recovery_operation_id JOIN kim.failure_epochs_current e ON e.failure_epoch_id=t.failure_epoch_id JOIN kim.recovery_materialization_evidence m ON m.recovery_operation_id=t.recovery_operation_id WHERE t.terminal_decision_id=$1 AND t.decision_state='VERIFIED'`, terminalDecisionID).Scan(&recoveryOperationID, &recoveryState, &epochState, &destinationAdmission); err != nil || recoveryState != "VERIFIED" || epochState != "RECOVERED" {
			return ErrBackendCleanupStale
		}
		if err := tx.QueryRow(ctx, `SELECT d.vm_id::text,d.vm_generation,d.source_materialization_generation,d.source_host_id,d.source_plan_id,d.source_plan_digest,d.retirement_decision_id FROM kim.source_materialization_retirement_decision_evidence d JOIN kim.recovery_terminal_decision_evidence t ON t.failure_epoch_id=d.failure_epoch_id JOIN kim.source_materialization_retirements_current current ON current.retirement_decision_id=d.retirement_decision_id AND current.retirement_state='RETIRED' WHERE t.terminal_decision_id=$1`, terminalDecisionID).Scan(&out.VMID, &out.VMGeneration, &out.MaterializationGeneration, &out.SourceHostID, &out.SourcePlanID, &out.SourcePlanDigest, &out.SourceRetirementDecisionID); err != nil {
			return ErrBackendCleanupStale
		}
		var required, matched int
		var evidenceSet string
		if err := tx.QueryRow(ctx, `SELECT jsonb_array_length(a.network_requirements),count(*) FILTER(WHERE hc.handoff_id IS NOT NULL AND quiescence.evidence_id IS NOT NULL AND retirement.evidence_id IS NOT NULL AND rc.terminal_evidence_id IS NOT NULL AND ip.claim_id IS NOT NULL AND mac.claim_id IS NOT NULL),coalesce(string_agg(p.port_id||':'||p.port_generation||':'||handoff.source_binding_generation||':'||handoff.destination_binding_generation||':'||retirement.nb_observation_digest||':'||retirement.sb_observation_digest||':'||retirement.ovs_observation_digest,',' ORDER BY p.port_id) FILTER(WHERE hc.handoff_id IS NOT NULL AND quiescence.evidence_id IS NOT NULL AND retirement.evidence_id IS NOT NULL AND rc.terminal_evidence_id IS NOT NULL AND ip.claim_id IS NOT NULL AND mac.claim_id IS NOT NULL),'')
			FROM kim.placement_admission_decisions a
			LEFT JOIN kim.network_ports_current p ON p.placement_admission_id=a.admission_id
			LEFT JOIN kim.port_binding_handoff_evidence handoff ON handoff.port_id=p.port_id AND handoff.destination_admission_id=a.admission_id AND handoff.source_host_id=$2
			LEFT JOIN kim.port_binding_handoffs_current hc ON hc.handoff_id=handoff.handoff_id AND hc.handoff_state IN ('DESTINATION_REALIZED','VERIFIED')
			LEFT JOIN kim.network_port_source_quiescence_evidence quiescence ON quiescence.evidence_id=handoff.source_quiescence_evidence_id AND quiescence.quiescence_state='QUIESCED' AND quiescence.source_interface_absent
			LEFT JOIN kim.network_port_binding_retirement_evidence retirement ON retirement.evidence_id=quiescence.retirement_evidence_id AND retirement.retirement_state='VERIFIED' AND retirement.logical_port_preserved AND retirement.ownership_marker_matches AND retirement.requested_chassis_absent AND retirement.source_chassis_inactive AND retirement.source_ovs_interface_absent
			LEFT JOIN kim.network_port_binding_retirements_current rc ON rc.terminal_evidence_id=retirement.evidence_id AND rc.retirement_state='VERIFIED'
			LEFT JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' AND ip.claim_state IN ('RESERVED','ACTIVE')
			LEFT JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE')
			WHERE a.admission_id=$1 GROUP BY a.network_requirements`, destinationAdmission, out.SourceHostID).Scan(&required, &matched, &evidenceSet); err != nil || required == 0 || matched != required || evidenceSet == "" {
			return ErrBackendCleanupStale
		}
		out.OperationID, out.OperationGeneration = operationID, operationGeneration
		out.ResourceType, out.ResourceID, out.ResourceGeneration = "HOST_NETWORK_ARTIFACT", out.VMID+":source-network", out.MaterializationGeneration
		out.BackendType, out.TerminalDecisionID = "OVN_OVS", terminalDecisionID
		out.BackendIdentity = evidenceSet
		out.BackendIdentityDigest = digestReleaseBytes([]byte(evidenceSet))
		out.EligibilityState = "ALREADY_ABSENT"
		out.EligibilityDigest = digestReleaseBytes([]byte(operationID + "/" + terminalDecisionID + "/" + out.BackendIdentityDigest + "/ALREADY_ABSENT"))
		policyDigest := digestReleaseBytes([]byte("SOURCE_NETWORK_RETIREMENT_ABSENCE/v1"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation,resource_type,resource_id,resource_generation,backend_type,source_host_id,vm_id,vm_generation,source_materialization_generation,source_plan_id,source_plan_digest,backend_identity,backend_identity_digest,cleanup_reason,cleanup_policy_revision,cleanup_policy_digest,origin_authority_type,origin_authority_id,recovery_terminal_decision_id,source_retirement_decision_id,eligibility_state,eligibility_reason,eligibility_digest) VALUES($1,$2,'HOST_NETWORK_ARTIFACT',$3,$4,'OVN_OVS',$5,$6::uuid,$7,$4,$8,$9,$10,$11,'RECOVERY_SUPERSEDED','SOURCE_NETWORK_RETIREMENT_ABSENCE/v1',$12,'RECOVERY_TERMINAL',$13,$13,$14,'ALREADY_ABSENT','generic_source_retirement_already_absent',$15) ON CONFLICT(cleanup_operation_id,cleanup_generation) DO NOTHING`, operationID, operationGeneration, out.ResourceID, out.MaterializationGeneration, out.SourceHostID, out.VMID, out.VMGeneration, out.SourcePlanID, out.SourcePlanDigest, out.BackendIdentity, out.BackendIdentityDigest, policyDigest, terminalDecisionID, out.SourceRetirementDecisionID, out.EligibilityDigest); err != nil {
			return err
		}
		var accepted string
		if err := tx.QueryRow(ctx, `SELECT eligibility_digest FROM kim.backend_cleanup_operation_evidence WHERE cleanup_operation_id=$1 AND cleanup_generation=$2`, operationID, operationGeneration).Scan(&accepted); err != nil || accepted != out.EligibilityDigest {
			return ErrBackendCleanupStale
		}
		var existingState string
		if err := tx.QueryRow(ctx, `SELECT operation_state FROM kim.backend_cleanup_operations_current WHERE cleanup_operation_id=$1 AND cleanup_generation=$2`, operationID, operationGeneration).Scan(&existingState); err == nil {
			if existingState != "VERIFIED" {
				return ErrBackendCleanupStale
			}
			out.State = existingState
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var expires time.Time
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()+interval '1 minute'`).Scan(&expires); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_attempt_evidence(cleanup_operation_id,cleanup_generation,claim_generation,claim_owner,claim_mode,lease_expires_at) VALUES($1,$2,1,'network-retirement-evidence-consumer','READ_BACK_FIRST',$3)`, operationID, operationGeneration, expires); err != nil {
			return err
		}
		evidenceID := "cleanup-network-evidence-" + operationID
		evidenceDigest := digestReleaseBytes([]byte(out.EligibilityDigest + "/ALREADY_ABSENT"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_observation_evidence(cleanup_evidence_id,cleanup_operation_id,cleanup_generation,claim_generation,resource_type,resource_id,resource_generation,source_host_id,vm_id,vm_generation,source_materialization_generation,backend_identity_digest,backend_present,backend_running,identity_matches,apply_response_state,observation_generation,observation_digest,result_state,artifact_digest,evidence_digest) VALUES($1,$2,$3,1,'HOST_NETWORK_ARTIFACT',$4,$5,$6,$7::uuid,$8,$5,$9,false,false,true,'NOT_APPLICABLE',1,$9,'ALREADY_ABSENT',$9,$10)`, evidenceID, operationID, operationGeneration, out.ResourceID, out.MaterializationGeneration, out.SourceHostID, out.VMID, out.VMGeneration, out.BackendIdentityDigest, evidenceDigest); err != nil {
			return err
		}
		terminalID := "cleanup-network-terminal-" + operationID
		terminalDigest := digestReleaseBytes([]byte(evidenceDigest + "/VERIFIED"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_terminal_evidence(cleanup_terminal_id,cleanup_operation_id,cleanup_generation,cleanup_evidence_id,cleanup_evidence_digest,terminal_state,terminal_reason,terminal_digest) VALUES($1,$2,$3,$4,$5,'VERIFIED','existing_exact_source_network_absence',$6)`, terminalID, operationID, operationGeneration, evidenceID, evidenceDigest, terminalDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operations_current(cleanup_operation_id,cleanup_generation,operation_state,last_claim_generation,terminal_evidence_id) VALUES($1,$2,'VERIFIED',1,$3)`, operationID, operationGeneration, terminalID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.source_backend_cleanup_current SET network_state='VERIFIED',cleanup_complete=(domain_state='VERIFIED' AND storage_state IN ('VERIFIED','NOT_REQUIRED') AND pci_state IN ('VERIFIED','NOT_REQUIRED')),updated_at=statement_timestamp() WHERE vm_id=$1::uuid AND source_materialization_generation=$2`, out.VMID, out.MaterializationGeneration)
		out.State = "VERIFIED"
		return err
	})
	return out, err
}

func ClaimBackendCleanup(ctx context.Context, db TxBeginner, operationID string, generation uint64, owner string, lease time.Duration) (BackendCleanupClaim, error) {
	var out BackendCleanupClaim
	if operationID == "" || generation == 0 || owner == "" || lease <= 0 || lease > 24*time.Hour {
		return out, ErrBackendCleanupStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if !backendCleanupStillEligibleTx(ctx, tx, operationID, generation) {
			return ErrBackendCleanupStale
		}
		var state string
		var expired bool
		var last uint64
		if err := tx.QueryRow(ctx, `SELECT operation_state,coalesce(claim_expires_at<=statement_timestamp(),false),last_claim_generation FROM kim.backend_cleanup_operations_current WHERE cleanup_operation_id=$1 AND cleanup_generation=$2 FOR UPDATE`, operationID, generation).Scan(&state, &expired, &last); err != nil {
			return err
		}
		if state == "VERIFIED" || state == "BLOCKED" || state == "CONFLICTING" || state == "STALE" || (state == "CLAIMED" && !expired) {
			return ErrBackendCleanupStale
		}
		mode := "APPLY_ALLOWED"
		if state != "PENDING" {
			mode = "READ_BACK_FIRST"
		}
		claimGeneration := last + 1
		if err := tx.QueryRow(ctx, `UPDATE kim.backend_cleanup_operations_current SET operation_state='CLAIMED',claim_owner=$3,claim_generation=$4,last_claim_generation=$4,claim_expires_at=statement_timestamp()+($5*interval '1 microsecond'),updated_at=statement_timestamp() WHERE cleanup_operation_id=$1 AND cleanup_generation=$2 RETURNING claim_expires_at`, operationID, generation, owner, claimGeneration, lease.Microseconds()).Scan(&out.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_attempt_evidence(cleanup_operation_id,cleanup_generation,claim_generation,claim_owner,claim_mode,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6)`, operationID, generation, claimGeneration, owner, mode, out.ExpiresAt); err != nil {
			return err
		}
		out = BackendCleanupClaim{operationID, generation, claimGeneration, owner, mode, out.ExpiresAt}
		return nil
	})
	return out, err
}

func AuthorizeBackendCleanupCommand(ctx context.Context, db TxBeginner, claim BackendCleanupClaim, jobID, commandID string) (BackendCleanupCommand, error) {
	var out BackendCleanupCommand
	if jobID == "" || commandID == "" {
		return out, ErrBackendCleanupStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if !backendCleanupStillEligibleTx(ctx, tx, claim.OperationID, claim.OperationGeneration) {
			return ErrBackendCleanupStale
		}
		var op BackendCleanupOperation
		if err := tx.QueryRow(ctx, `SELECT e.resource_type,e.resource_id,e.resource_generation,e.source_host_id,e.vm_id::text,e.vm_generation,e.source_materialization_generation,e.source_plan_digest,e.backend_identity_digest
			FROM kim.backend_cleanup_operations_current c JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation)
			WHERE c.cleanup_operation_id=$1 AND c.cleanup_generation=$2 AND c.operation_state='CLAIMED' AND c.claim_owner=$3 AND c.claim_generation=$4 AND c.claim_expires_at>statement_timestamp() FOR UPDATE OF c`, claim.OperationID, claim.OperationGeneration, claim.Owner, claim.ClaimGeneration).Scan(&op.ResourceType, &op.ResourceID, &op.ResourceGeneration, &op.SourceHostID, &op.VMID, &op.VMGeneration, &op.MaterializationGeneration, &op.SourcePlanDigest, &op.BackendIdentityDigest); err != nil || op.ResourceType != "LIBVIRT_DOMAIN" {
			return ErrBackendCleanupStale
		}
		payload, _ := json.Marshal(map[string]any{"cleanup_operation_id": claim.OperationID, "cleanup_generation": claim.OperationGeneration, "domain_uuid": op.VMID, "vm_generation": op.VMGeneration, "source_host_id": op.SourceHostID, "source_plan_digest": op.SourcePlanDigest, "source_materialization_generation": op.MaterializationGeneration, "backend_identity_digest": op.BackendIdentityDigest, "desired_state": "ABSENT"})
		out = BackendCleanupCommand{jobID, commandID, digestReleaseBytes(payload)}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'BACKEND_CLEANUP',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, jobID, claim.OperationID, claim.OperationGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,'VIRTUAL_MACHINE_UNDEFINE','kim.command.virtual-machine-undefine/v1',$4,$5,$6) ON CONFLICT(command_id) DO NOTHING`, commandID, jobID, op.SourceHostID, "vm:"+op.VMID, payload, out.PayloadDigest); err != nil {
			return err
		}
		var accepted string
		if err := tx.QueryRow(ctx, `SELECT payload_digest FROM kim.execution_commands WHERE command_id=$1`, commandID).Scan(&accepted); err != nil || accepted != out.PayloadDigest {
			return ErrBackendCleanupStale
		}
		return nil
	})
	return out, err
}

func CompleteBackendCleanup(ctx context.Context, db TxBeginner, claim BackendCleanupClaim, o BackendCleanupObservation) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if !backendCleanupStillEligibleTx(ctx, tx, claim.OperationID, claim.OperationGeneration) {
			return ErrBackendCleanupStale
		}
		var resourceType, resourceID, host, vmID, identityDigest string
		var resourceGeneration, vmGeneration, materializationGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT e.resource_type,e.resource_id,e.resource_generation,e.source_host_id,e.vm_id::text,e.vm_generation,e.source_materialization_generation,e.backend_identity_digest
			FROM kim.backend_cleanup_operations_current c JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation)
			WHERE c.cleanup_operation_id=$1 AND c.cleanup_generation=$2 AND c.operation_state='CLAIMED' AND c.claim_owner=$3 AND c.claim_generation=$4 AND c.claim_expires_at>statement_timestamp() FOR UPDATE OF c`, claim.OperationID, claim.OperationGeneration, claim.Owner, claim.ClaimGeneration).Scan(&resourceType, &resourceID, &resourceGeneration, &host, &vmID, &vmGeneration, &materializationGeneration, &identityDigest); err != nil {
			return ErrBackendCleanupStale
		}
		if o.OperationID != claim.OperationID || o.OperationGeneration != claim.OperationGeneration || o.ClaimGeneration != claim.ClaimGeneration || o.ResourceType != resourceType || o.ResourceID != resourceID || o.ResourceGeneration != resourceGeneration || o.SourceHostID != host || o.VMID != vmID || o.VMGeneration != vmGeneration || o.MaterializationGeneration != materializationGeneration || o.BackendIdentityDigest != identityDigest || o.AttemptIndex < 1 || o.ObservationGeneration == 0 || len(o.ObservationDigest) != 64 || len(o.ArtifactDigest) != 64 || len(o.EvidenceDigest) != 64 {
			return ErrBackendCleanupStale
		}
		if o.ResultState != "ABSENT" && o.ResultState != "UNKNOWN" && o.ResultState != "CONFLICTING" {
			return ErrBackendCleanupStale
		}
		if o.BackendPresent == nil || o.BackendRunning == nil || (o.ResultState == "ABSENT" && (*o.BackendPresent || *o.BackendRunning || !o.IdentityMatches)) {
			return ErrBackendCleanupStale
		}
		var verificationState string
		if err := tx.QueryRow(ctx, `SELECT v.verification_state FROM kim.execution_commands c JOIN kim.command_verification_evidence v ON v.command_id=c.command_id WHERE c.command_id=$1 AND c.command_type='VIRTUAL_MACHINE_UNDEFINE' AND c.schema_version='kim.command.virtual-machine-undefine/v1' AND c.host_id=$2 AND c.target_resource_id='vm:'||$3 AND v.verification_id=$4 AND v.attempt_index=$5 AND v.verifier_artifact_digest=$6 AND v.observation_digest=$7`, o.CommandID, host, vmID, o.VerificationID, o.AttemptIndex, o.VerifierDigest, o.ObservationDigest).Scan(&verificationState); err != nil || (o.ResultState == "ABSENT" && verificationState != "MATCHED") || (o.ResultState == "UNKNOWN" && verificationState != "UNKNOWN") || (o.ResultState == "CONFLICTING" && verificationState != "CONFLICTING") {
			return ErrBackendCleanupStale
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_observation_evidence(cleanup_evidence_id,cleanup_operation_id,cleanup_generation,claim_generation,resource_type,resource_id,resource_generation,source_host_id,vm_id,vm_generation,source_materialization_generation,backend_identity_digest,backend_present,backend_running,identity_matches,apply_response_state,command_id,attempt_index,verification_id,verifier_digest,observation_generation,observation_digest,result_state,artifact_digest,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::uuid,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`, o.EvidenceID, o.OperationID, o.OperationGeneration, o.ClaimGeneration, o.ResourceType, o.ResourceID, o.ResourceGeneration, o.SourceHostID, o.VMID, o.VMGeneration, o.MaterializationGeneration, o.BackendIdentityDigest, o.BackendPresent, o.BackendRunning, o.IdentityMatches, o.ApplyResponseState, o.CommandID, o.AttemptIndex, o.VerificationID, o.VerifierDigest, o.ObservationGeneration, o.ObservationDigest, o.ResultState, o.ArtifactDigest, o.EvidenceDigest); err != nil {
			return err
		}
		state := "DISPATCH_UNKNOWN"
		var terminalID any
		if o.ResultState == "ABSENT" {
			state, terminalID = "VERIFIED", "cleanup-terminal-"+o.EvidenceID
			terminalDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/VERIFIED", o.OperationID, o.OperationGeneration, o.EvidenceDigest)))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_terminal_evidence(cleanup_terminal_id,cleanup_operation_id,cleanup_generation,cleanup_evidence_id,cleanup_evidence_digest,terminal_state,terminal_reason,terminal_digest) VALUES($1,$2,$3,$4,$5,'VERIFIED','exact_backend_absence_observed',$6)`, terminalID, o.OperationID, o.OperationGeneration, o.EvidenceID, o.EvidenceDigest, terminalDigest); err != nil {
				return err
			}
		} else if o.ResultState == "CONFLICTING" {
			state = "CONFLICTING"
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.backend_cleanup_operations_current SET operation_state=$5,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,terminal_evidence_id=$6,updated_at=statement_timestamp() WHERE cleanup_operation_id=$1 AND cleanup_generation=$2 AND claim_owner=$3 AND claim_generation=$4`, claim.OperationID, claim.OperationGeneration, claim.Owner, claim.ClaimGeneration, state, terminalID); err != nil {
			return err
		}
		domainProjection := "UNKNOWN"
		if state == "VERIFIED" {
			domainProjection = "VERIFIED"
		} else if state == "CONFLICTING" {
			domainProjection = "CONFLICTING"
		}
		_, err := tx.Exec(ctx, `UPDATE kim.source_backend_cleanup_current SET domain_state=$3,cleanup_complete=($3='VERIFIED' AND storage_state IN ('VERIFIED','NOT_REQUIRED') AND network_state IN ('VERIFIED','NOT_REQUIRED') AND pci_state IN ('VERIFIED','NOT_REQUIRED')),updated_at=statement_timestamp() WHERE vm_id=$1::uuid AND source_materialization_generation=$2`, vmID, materializationGeneration, domainProjection)
		return err
	})
}
