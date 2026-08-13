package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

type LocalLVMSourceCleanupAuthority struct {
	BackendCleanupOperation
	ChildTerminalID, CopyTerminalID, SourceSafetyID, SourceAdmissionID              string
	SourceVolumeID, SourceBindingID, SourceBackendID, SourceVGUUID, SourceLVUUID    string
	SourceBackendResourceKey, SourceAttachmentID, SourceCapacityClaimID             string
	DestinationAdmissionID, DestinationPlanID, AuthorityDigest                      string
	SourceBindingGeneration, SourceBackendGeneration, SourceCapacityGeneration      uint64
	SourceAttachmentGeneration, DestinationMaterializationGeneration, ReservedBytes uint64
}

type LocalLVMCleanupObservation struct {
	BackendCleanupObservation
	ObservedLVUUID                                  string
	ExactSourceLVPresent, ForeignReplacementPresent bool
}

type LocalLVMCapacityReclamation struct {
	EvidenceID, CleanupTerminalID, CapacityClaimID, VolumeID, BindingID, LVUUID string
	ReleasedBytes                                                               uint64
	Digest                                                                      string
}

type LocalLVMCleanupMetricsSnapshot struct {
	Active, Attempts, Unknown, Present, Absent, CapacityReleasePending int64
	ReleasedBytes, DurationNanoseconds                                 int64
}

// LoadLocalLVMCleanupMetrics exposes bounded aggregate counters only. Volume,
// Binding, LV and Host identities are intentionally not metric labels.
func LoadLocalLVMCleanupMetrics(ctx context.Context, row QueryRower) (LocalLVMCleanupMetricsSnapshot, error) {
	var out LocalLVMCleanupMetricsSnapshot
	err := row.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kim.backend_cleanup_operations_current c JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation) WHERE e.backend_type='LOCAL_LVM' AND c.operation_state NOT IN ('VERIFIED','BLOCKED','CONFLICTING','STALE')),
		(SELECT count(*) FROM kim.backend_cleanup_attempt_evidence a JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation) WHERE e.backend_type='LOCAL_LVM'),
		(SELECT count(*) FROM kim.backend_cleanup_observation_evidence o JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation) WHERE e.backend_type='LOCAL_LVM' AND o.result_state='UNKNOWN'),
		(SELECT count(*) FROM kim.backend_cleanup_observation_evidence o JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation) WHERE e.backend_type='LOCAL_LVM' AND o.result_state='PRESENT'),
		(SELECT count(*) FROM kim.backend_cleanup_observation_evidence o JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation) WHERE e.backend_type='LOCAL_LVM' AND o.result_state IN ('ABSENT','ALREADY_ABSENT')),
		(SELECT count(*) FROM kim.storage_capacity_claims capacity JOIN kim.local_lvm_source_cleanup_authority_evidence d ON d.source_capacity_claim_id=capacity.capacity_claim_id WHERE capacity.claim_state='RELEASE_PENDING'),
		coalesce((SELECT sum(released_bytes) FROM kim.local_lvm_capacity_reclamation_evidence),0),
		coalesce((SELECT sum((extract(epoch FROM terminal.recorded_at-operation.recorded_at)*1000000000)::bigint) FROM kim.backend_cleanup_terminal_evidence terminal JOIN kim.backend_cleanup_operation_evidence operation USING(cleanup_operation_id,cleanup_generation) WHERE operation.backend_type='LOCAL_LVM'),0)`).Scan(&out.Active, &out.Attempts, &out.Unknown, &out.Present, &out.Absent, &out.CapacityReleasePending, &out.ReleasedBytes, &out.DurationNanoseconds)
	return out, err
}

// CommitHostEvacuationSourceLocalLVMCleanup is the MATERIALIZATION producer
// adapter for generic cleanup. The caller names only the cleanup operation and
// an immutable child terminal; every backend identity is derived in PostgreSQL.
func CommitHostEvacuationSourceLocalLVMCleanup(ctx context.Context, db TxBeginner, operationID string, generation uint64, childTerminalID string) (LocalLVMSourceCleanupAuthority, error) {
	var out LocalLVMSourceCleanupAuthority
	if operationID == "" || generation == 0 || childTerminalID == "" {
		return out, ErrBackendCleanupStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "backend-cleanup/"+operationID); err != nil {
			return err
		}
		var childDigest, copyDigest, safetyDigest, sourcePlanDigest string
		err := tx.QueryRow(ctx, `SELECT t.vm_id::text,t.vm_generation,t.source_host_id,e.source_admission_id,e.source_plan_id,e.source_materialization_generation,
			t.terminal_digest,copy_terminal.terminal_evidence_id,copy_terminal.terminal_digest,copy.source_storage_safety_evidence_id,safety.safety_digest,
			copy.source_volume_id,copy.source_binding_id,copy.source_binding_generation,binding_intent.backend_id,backend.backend_generation,copy.source_vg_uuid,copy.source_lv_uuid,binding_intent.backend_resource_key,
			safety.root_attachment_id,safety.root_attachment_generation,capacity.capacity_claim_id,capacity.capacity_generation,capacity.reserved_bytes,
			t.destination_admission_id,vm.current_plan_id,v.destination_materialization_generation,source_plan.plan_digest
		FROM kim.host_evacuation_child_terminal_evidence t
		JOIN kim.host_evacuation_child_verification_evidence v ON v.verification_id=t.child_verification_id AND v.verification_digest=t.child_verification_digest AND v.verification_state='VERIFIED'
		JOIN kim.host_evacuation_destination_evidence_binding destination_binding ON destination_binding.destination_binding_id=v.destination_binding_id AND destination_binding.child_operation_id=t.child_operation_id AND destination_binding.destination_host_id=t.destination_host_id AND destination_binding.destination_admission_id=t.destination_admission_id
		JOIN kim.host_evacuation_workload_evidence e ON e.child_operation_id=t.child_operation_id AND e.child_generation=t.child_generation
		JOIN kim.local_lvm_relocation_copy_operation_evidence copy ON copy.child_operation_id=t.child_operation_id AND copy.child_generation=t.child_generation
		JOIN kim.local_lvm_relocation_copy_operations_current copy_current ON copy_current.copy_operation_id=copy.copy_operation_id AND copy_current.copy_generation=copy.copy_generation AND copy_current.operation_state='VERIFIED'
		JOIN kim.local_lvm_relocation_copy_terminal_evidence copy_terminal ON copy_terminal.terminal_evidence_id=copy_current.terminal_evidence_id AND copy_terminal.copy_operation_id=copy.copy_operation_id AND copy_terminal.copy_generation=copy.copy_generation AND copy_terminal.terminal_state='VERIFIED'
		JOIN kim.host_evacuation_source_storage_safety_evidence safety ON safety.safety_evidence_id=copy.source_storage_safety_evidence_id AND safety.child_operation_id=t.child_operation_id AND safety.source_materialization_generation=e.source_materialization_generation AND safety.safety_state='SAFE'
		JOIN kim.vm_materialization_plan_evidence source_plan ON source_plan.plan_id=e.source_plan_id AND source_plan.root_volume_id=copy.source_volume_id AND source_plan.root_binding_id=copy.source_binding_id AND source_plan.root_binding_generation=copy.source_binding_generation
		JOIN kim.volume_backend_binding_intents binding_intent ON binding_intent.binding_id=copy.source_binding_id AND binding_intent.volume_id=copy.source_volume_id AND binding_intent.binding_generation=copy.source_binding_generation AND binding_intent.host_id=e.source_host_id AND binding_intent.vg_uuid=copy.source_vg_uuid AND binding_intent.observed_lv_uuid=copy.source_lv_uuid
		JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=binding_intent.binding_id AND binding.binding_generation=binding_intent.binding_generation AND binding.lv_uuid=copy.source_lv_uuid AND binding.binding_state='BOUND'
		JOIN kim.storage_backends_current backend ON backend.backend_id=binding_intent.backend_id AND backend.host_id=e.source_host_id AND backend.vg_uuid=copy.source_vg_uuid AND backend.lifecycle_state='ACTIVE' AND backend.capability_state='CURRENT'
		JOIN kim.storage_capacity_claims capacity ON capacity.volume_id=copy.source_volume_id AND capacity.backend_id=backend.backend_id AND capacity.placement_admission_id=e.source_admission_id AND capacity.claim_state IN ('RESERVED','ALLOCATED','RELEASE_PENDING')
		JOIN kim.volume_attachment_observations_current attachment ON attachment.attachment_id=safety.root_attachment_id AND attachment.attachment_generation=safety.root_attachment_generation AND attachment.binding_id=copy.source_binding_id AND attachment.binding_generation=copy.source_binding_generation AND attachment.host_id=e.source_host_id AND NOT attachment.holder_open
		JOIN kim.volume_attachment_observation_evidence attachment_evidence ON attachment_evidence.evidence_id=attachment.evidence_id AND attachment_evidence.evidence_state='MATCHED' AND NOT attachment_evidence.holder_open AND attachment_evidence.observed_lv_uuid=copy.source_lv_uuid
		JOIN kim.virtual_machines_current vm ON vm.vm_id=t.vm_id AND vm.vm_generation=t.vm_generation AND vm.host_id<>e.source_host_id AND vm.current_plan_id<>e.source_plan_id
		JOIN kim.vm_materialization_plan_evidence destination_plan ON destination_plan.plan_id=destination_binding.destination_plan_id AND destination_plan.host_id=t.destination_host_id AND destination_plan.placement_admission_id=t.destination_admission_id
		JOIN kim.vm_power_observation_evidence destination_power ON destination_power.evidence_id=destination_binding.power_evidence_id AND destination_power.host_id=t.destination_host_id AND destination_power.observed_power_state='RUNNING'
		JOIN kim.host_operation_authorities_current authority ON authority.host_id=e.source_host_id AND authority.authority_state='ARMED'
		WHERE t.terminal_evidence_id=$1 AND t.terminal_state='VERIFIED'`, childTerminalID).Scan(
			&out.VMID, &out.VMGeneration, &out.SourceHostID, &out.SourceAdmissionID, &out.SourcePlanID, &out.MaterializationGeneration,
			&childDigest, &out.CopyTerminalID, &copyDigest, &out.SourceSafetyID, &safetyDigest,
			&out.SourceVolumeID, &out.SourceBindingID, &out.SourceBindingGeneration, &out.SourceBackendID, &out.SourceBackendGeneration, &out.SourceVGUUID, &out.SourceLVUUID, &out.SourceBackendResourceKey,
			&out.SourceAttachmentID, &out.SourceAttachmentGeneration, &out.SourceCapacityClaimID, &out.SourceCapacityGeneration, &out.ReservedBytes,
			&out.DestinationAdmissionID, &out.DestinationPlanID, &out.DestinationMaterializationGeneration, &sourcePlanDigest)
		if err != nil {
			return ErrBackendCleanupStale
		}
		out.OperationID, out.OperationGeneration, out.ChildTerminalID = operationID, generation, childTerminalID
		out.ResourceType, out.ResourceID, out.ResourceGeneration, out.BackendType = "LOCAL_LVM_VOLUME", out.SourceVolumeID, out.SourceBindingGeneration, "LOCAL_LVM"
		out.SourcePlanDigest = sourcePlanDigest
		out.BackendIdentity = fmt.Sprintf("backend:%s/%d/vg:%s/volume:%s/binding:%s/%d/lv:%s", out.SourceBackendID, out.SourceBackendGeneration, out.SourceVGUUID, out.SourceVolumeID, out.SourceBindingID, out.SourceBindingGeneration, out.SourceLVUUID)
		out.BackendIdentityDigest = digestReleaseBytes([]byte(out.BackendIdentity))
		out.EligibilityState = "ELIGIBLE"
		out.EligibilityDigest = digestReleaseBytes([]byte(operationID + "/" + childTerminalID + "/" + out.BackendIdentityDigest + "/ELIGIBLE"))
		out.AuthorityDigest = digestReleaseBytes([]byte(childDigest + "/" + copyDigest + "/" + safetyDigest + "/" + out.EligibilityDigest))
		policyDigest := digestReleaseBytes([]byte("POST_RELOCATION_EXACT_LOCAL_LVM_ABSENCE/v1"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation,resource_type,resource_id,resource_generation,backend_type,source_host_id,vm_id,vm_generation,source_materialization_generation,source_plan_id,source_plan_digest,backend_identity,backend_identity_digest,cleanup_reason,cleanup_policy_revision,cleanup_policy_digest,origin_authority_type,origin_authority_id,eligibility_state,eligibility_reason,eligibility_digest) VALUES($1,$2,'LOCAL_LVM_VOLUME',$3,$4,'LOCAL_LVM',$5,$6::uuid,$7,$8,$9,$10,$11,$12,'MATERIALIZATION_SUPERSEDED','POST_RELOCATION_EXACT_LOCAL_LVM_ABSENCE/v1',$13,'MATERIALIZATION',$14,'ELIGIBLE','verified_destination_preserves_exact_source_content',$15) ON CONFLICT(cleanup_operation_id,cleanup_generation) DO NOTHING`, operationID, generation, out.SourceVolumeID, out.SourceBindingGeneration, out.SourceHostID, out.VMID, out.VMGeneration, out.MaterializationGeneration, out.SourcePlanID, sourcePlanDigest, out.BackendIdentity, out.BackendIdentityDigest, policyDigest, childTerminalID, out.EligibilityDigest); err != nil {
			return err
		}
		originID := "cleanup-origin-materialization-" + operationID
		originDigest := digestReleaseBytes([]byte(originID + "/" + childTerminalID + "/" + childDigest + "/" + out.EligibilityDigest + "/ACCEPTED"))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_origin_eligibility_evidence(origin_eligibility_id,cleanup_operation_id,cleanup_generation,origin_authority_type,origin_authority_id,origin_authority_state,producer_type,producer_evidence_digest,eligibility_digest,evidence_digest) VALUES($1,$2,$3,'MATERIALIZATION',$4,'ACCEPTED','MATERIALIZATION',$5,$6,$7) ON CONFLICT(origin_eligibility_id) DO NOTHING`, originID, operationID, generation, childTerminalID, childDigest, out.EligibilityDigest, originDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_source_cleanup_authority_evidence(cleanup_operation_id,cleanup_generation,child_terminal_id,child_terminal_digest,copy_terminal_id,copy_terminal_digest,source_storage_safety_evidence_id,source_storage_safety_digest,source_admission_id,source_volume_id,source_binding_id,source_binding_generation,source_backend_id,source_backend_generation,source_vg_uuid,source_lv_uuid,source_backend_resource_key,source_attachment_id,source_attachment_generation,source_capacity_claim_id,source_capacity_generation,reserved_bytes,destination_admission_id,destination_plan_id,destination_materialization_generation,authority_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26) ON CONFLICT(cleanup_operation_id,cleanup_generation) DO NOTHING`, operationID, generation, childTerminalID, childDigest, out.CopyTerminalID, copyDigest, out.SourceSafetyID, safetyDigest, out.SourceAdmissionID, out.SourceVolumeID, out.SourceBindingID, out.SourceBindingGeneration, out.SourceBackendID, out.SourceBackendGeneration, out.SourceVGUUID, out.SourceLVUUID, out.SourceBackendResourceKey, out.SourceAttachmentID, out.SourceAttachmentGeneration, out.SourceCapacityClaimID, out.SourceCapacityGeneration, out.ReservedBytes, out.DestinationAdmissionID, out.DestinationPlanID, out.DestinationMaterializationGeneration, out.AuthorityDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_operations_current(cleanup_operation_id,cleanup_generation,operation_state) VALUES($1,$2,'PENDING') ON CONFLICT DO NOTHING`, operationID, generation); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.storage_capacity_claims SET claim_state='RELEASE_PENDING' WHERE capacity_claim_id=$1 AND claim_state IN ('RESERVED','ALLOCATED','RELEASE_PENDING')`, out.SourceCapacityClaimID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_binding_intents SET binding_state='RELEASE_PENDING' WHERE binding_id=$1 AND binding_generation=$2 AND binding_state IN ('BOUND','RELEASE_PENDING')`, out.SourceBindingID, out.SourceBindingGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_claims SET claim_state='RELEASED' WHERE attachment_id=$1 AND attachment_generation=$2 AND claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','RELEASED'); UPDATE kim.volume_attachments_current SET desired_state='DETACHED' WHERE attachment_id=$1 AND attachment_generation=$2`, pgx.QueryExecModeSimpleProtocol, out.SourceAttachmentID, out.SourceAttachmentGeneration); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.source_backend_cleanup_current(vm_id,source_materialization_generation,source_host_id,domain_state,storage_state,network_state,pci_state,cleanup_complete) VALUES($1::uuid,$2,$3,'NOT_REQUIRED','PENDING','NOT_REQUIRED','NOT_REQUIRED',false) ON CONFLICT(vm_id,source_materialization_generation) DO UPDATE SET storage_state='PENDING',cleanup_complete=false,updated_at=statement_timestamp()`, out.VMID, out.MaterializationGeneration, out.SourceHostID)
		return err
	})
	return out, err
}

func localLVMCleanupPayload(claim BackendCleanupClaim, authority LocalLVMSourceCleanupAuthority) ([]byte, string) {
	payload, _ := json.Marshal(map[string]any{"backend_id": authority.SourceBackendID, "backend_generation": authority.SourceBackendGeneration, "vg_uuid": authority.SourceVGUUID, "expected_lv_uuid": authority.SourceLVUUID, "backend_resource_key": authority.SourceBackendResourceKey, "binding_id": authority.SourceBindingID, "binding_generation": authority.SourceBindingGeneration, "cleanup_operation_id": claim.OperationID, "cleanup_generation": claim.OperationGeneration, "desired_state": "ABSENT"})
	return payload, digestReleaseBytes(payload)
}

func cleanupEvidenceUint64(value any) (uint64, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || number != float64(uint64(number)) {
		return 0, false
	}
	return uint64(number), true
}

func loadLocalLVMCleanupClaimTx(ctx context.Context, tx pgx.Tx, claim BackendCleanupClaim) (LocalLVMSourceCleanupAuthority, string, error) {
	var a LocalLVMSourceCleanupAuthority
	var mode string
	err := tx.QueryRow(ctx, `SELECT d.source_volume_id,d.source_binding_id,d.source_binding_generation,d.source_backend_id,d.source_backend_generation,d.source_vg_uuid,d.source_lv_uuid,d.source_backend_resource_key,d.source_attachment_id,d.source_attachment_generation,d.source_capacity_claim_id,d.source_capacity_generation,d.reserved_bytes,d.destination_admission_id,d.destination_plan_id,d.destination_materialization_generation,d.authority_digest,e.source_host_id,e.vm_id::text,e.vm_generation,e.source_materialization_generation,e.source_plan_id,e.source_plan_digest,e.backend_identity_digest,attempt.claim_mode
	FROM kim.backend_cleanup_operations_current c JOIN kim.backend_cleanup_operation_evidence e USING(cleanup_operation_id,cleanup_generation) JOIN kim.local_lvm_source_cleanup_authority_evidence d USING(cleanup_operation_id,cleanup_generation) JOIN kim.backend_cleanup_attempt_evidence attempt ON attempt.cleanup_operation_id=c.cleanup_operation_id AND attempt.cleanup_generation=c.cleanup_generation AND attempt.claim_generation=c.claim_generation
	JOIN kim.storage_backends_current backend ON backend.backend_id=d.source_backend_id AND backend.backend_generation=d.source_backend_generation AND backend.vg_uuid=d.source_vg_uuid AND backend.lifecycle_state='ACTIVE' AND backend.capability_state='CURRENT'
	JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=d.source_binding_id AND binding.binding_generation=d.source_binding_generation AND binding.lv_uuid=d.source_lv_uuid AND binding.binding_state='BOUND'
	JOIN kim.storage_capacity_claims capacity ON capacity.capacity_claim_id=d.source_capacity_claim_id AND capacity.claim_state='RELEASE_PENDING'
	JOIN kim.virtual_machines_current vm ON vm.vm_id=e.vm_id AND vm.host_id<>e.source_host_id AND vm.current_plan_id<>e.source_plan_id
	WHERE c.cleanup_operation_id=$1 AND c.cleanup_generation=$2 AND c.operation_state='CLAIMED' AND c.claim_owner=$3 AND c.claim_generation=$4 AND c.claim_expires_at>statement_timestamp() FOR UPDATE OF c`, claim.OperationID, claim.OperationGeneration, claim.Owner, claim.ClaimGeneration).Scan(&a.SourceVolumeID, &a.SourceBindingID, &a.SourceBindingGeneration, &a.SourceBackendID, &a.SourceBackendGeneration, &a.SourceVGUUID, &a.SourceLVUUID, &a.SourceBackendResourceKey, &a.SourceAttachmentID, &a.SourceAttachmentGeneration, &a.SourceCapacityClaimID, &a.SourceCapacityGeneration, &a.ReservedBytes, &a.DestinationAdmissionID, &a.DestinationPlanID, &a.DestinationMaterializationGeneration, &a.AuthorityDigest, &a.SourceHostID, &a.VMID, &a.VMGeneration, &a.MaterializationGeneration, &a.SourcePlanID, &a.SourcePlanDigest, &a.BackendIdentityDigest, &mode)
	a.OperationID, a.OperationGeneration, a.ResourceType, a.ResourceID, a.ResourceGeneration, a.BackendType = claim.OperationID, claim.OperationGeneration, "LOCAL_LVM_VOLUME", a.SourceVolumeID, a.SourceBindingGeneration, "LOCAL_LVM"
	if err != nil || mode != claim.Mode {
		return a, "", ErrBackendCleanupStale
	}
	return a, mode, nil
}

func authorizeLocalLVMCleanup(ctx context.Context, db TxBeginner, claim BackendCleanupClaim, jobID, commandID string, readBack bool) (BackendCleanupCommand, error) {
	var out BackendCleanupCommand
	if jobID == "" || commandID == "" || (readBack && claim.Mode != "READ_BACK_FIRST") {
		return out, ErrBackendCleanupStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if !backendCleanupStillEligibleTx(ctx, tx, claim.OperationID, claim.OperationGeneration) {
			return ErrBackendCleanupStale
		}
		a, mode, err := loadLocalLVMCleanupClaimTx(ctx, tx, claim)
		if err != nil {
			return fmt.Errorf("load exact Local LVM cleanup claim: %w", err)
		}
		if !readBack && mode == "READ_BACK_FIRST" {
			var present bool
			err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.backend_cleanup_observation_evidence o JOIN kim.execution_commands c ON c.command_id=o.command_id WHERE o.cleanup_operation_id=$1 AND o.cleanup_generation=$2 AND o.claim_generation=$3 AND o.result_state='PRESENT' AND o.backend_present AND o.identity_matches AND c.command_type=$4 AND c.schema_version=$5)`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration, locallvm.DeleteReadBackType, locallvm.DeleteReadBackSchema).Scan(&present)
			if err != nil || !present {
				return ErrBackendCleanupStale
			}
		}
		payload, digest := localLVMCleanupPayload(claim, a)
		out = BackendCleanupCommand{jobID, commandID, digest}
		commandType, schema, resourceType := locallvm.DeleteCommandType, locallvm.DeleteSchemaVersion, "BACKEND_CLEANUP"
		if readBack {
			commandType, schema, resourceType = locallvm.DeleteReadBackType, locallvm.DeleteReadBackSchema, "BACKEND_CLEANUP_READ_BACK"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,$2,$3,$4,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, jobID, resourceType, claim.OperationID, claim.OperationGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(command_id) DO NOTHING`, commandID, jobID, a.SourceHostID, commandType, schema, "volume:"+a.SourceVolumeID, payload, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($1,'PENDING') ON CONFLICT(command_id) DO NOTHING`, commandID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1 AND (current_command_id IS NULL OR current_command_id=$2)`, jobID, commandID)
		return err
	})
	return out, err
}

func AuthorizeLocalLVMCleanupCommand(ctx context.Context, db TxBeginner, claim BackendCleanupClaim, jobID, commandID string) (BackendCleanupCommand, error) {
	return authorizeLocalLVMCleanup(ctx, db, claim, jobID, commandID, false)
}
func AuthorizeLocalLVMCleanupReadBackCommand(ctx context.Context, db TxBeginner, claim BackendCleanupClaim, jobID, commandID string) (BackendCleanupCommand, error) {
	return authorizeLocalLVMCleanup(ctx, db, claim, jobID, commandID, true)
}

func CompleteLocalLVMCleanup(ctx context.Context, db TxBeginner, claim BackendCleanupClaim, o LocalLVMCleanupObservation) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if !backendCleanupStillEligibleTx(ctx, tx, claim.OperationID, claim.OperationGeneration) {
			return fmt.Errorf("Local LVM cleanup observation identity mismatch: %w", ErrBackendCleanupStale)
		}
		a, mode, err := loadLocalLVMCleanupClaimTx(ctx, tx, claim)
		if err != nil {
			return err
		}
		b := o.BackendCleanupObservation
		if b.OperationID != claim.OperationID || b.OperationGeneration != claim.OperationGeneration || b.ClaimGeneration != claim.ClaimGeneration || b.ResourceType != "LOCAL_LVM_VOLUME" || b.ResourceID != a.SourceVolumeID || b.ResourceGeneration != a.SourceBindingGeneration || b.SourceHostID != a.SourceHostID || b.VMID != a.VMID || b.VMGeneration != a.VMGeneration || b.MaterializationGeneration != a.MaterializationGeneration || b.BackendIdentityDigest != a.BackendIdentityDigest || b.AttemptIndex < 1 || b.ObservationGeneration == 0 || len(b.ObservationDigest) != 64 || len(b.ArtifactDigest) != 64 || len(b.EvidenceDigest) != 64 {
			return fmt.Errorf("Local LVM cleanup observation mismatch got=%+v expected operation=%s/%d claim=%d resource=%s/%d host=%s vm=%s/%d materialization=%d identity=%s: %w", b, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration, a.SourceVolumeID, a.SourceBindingGeneration, a.SourceHostID, a.VMID, a.VMGeneration, a.MaterializationGeneration, a.BackendIdentityDigest, ErrBackendCleanupStale)
		}
		if b.BackendPresent == nil || b.BackendRunning == nil || *b.BackendRunning || o.ExactSourceLVPresent != *b.BackendPresent || (o.ExactSourceLVPresent && o.ForeignReplacementPresent) || (b.ResultState == "ABSENT" && (o.ExactSourceLVPresent || !b.IdentityMatches)) || (b.ResultState == "PRESENT" && (!o.ExactSourceLVPresent || !b.IdentityMatches)) {
			return fmt.Errorf("Local LVM cleanup command verification mismatch: %w", ErrBackendCleanupStale)
		}
		var state, ctype, schema string
		var evidence []byte
		if err := tx.QueryRow(ctx, `SELECT v.verification_state,c.command_type,c.schema_version,v.evidence_payload FROM kim.execution_commands c JOIN kim.command_verification_evidence v ON v.command_id=c.command_id WHERE c.command_id=$1 AND c.host_id=$2 AND c.target_resource_id=$3 AND v.verification_id=$4 AND v.attempt_index=$5 AND v.verifier_artifact_digest=$6 AND v.observation_digest=$7`, b.CommandID, a.SourceHostID, "volume:"+a.SourceVolumeID, b.VerificationID, b.AttemptIndex, b.VerifierDigest, b.ObservationDigest).Scan(&state, &ctype, &schema, &evidence); err != nil {
			return fmt.Errorf("Local LVM cleanup evidence payload mismatch: %w", ErrBackendCleanupStale)
		}
		want := map[string]any{}
		if json.Unmarshal(evidence, &want) != nil {
			return fmt.Errorf("decode Local LVM cleanup evidence payload: %w", ErrBackendCleanupStale)
		}
		backendGeneration, backendGenerationOK := cleanupEvidenceUint64(want["backend_generation"])
		bindingGeneration, bindingGenerationOK := cleanupEvidenceUint64(want["binding_generation"])
		cleanupGeneration, cleanupGenerationOK := cleanupEvidenceUint64(want["cleanup_generation"])
		observedLV, _ := want["observed_lv_uuid"].(string)
		exactPresent, exactPresentOK := want["exact_source_lv_present"].(bool)
		foreignPresent, foreignPresentOK := want["foreign_replacement_present"].(bool)
		if want["expected_lv_uuid"] != a.SourceLVUUID || want["backend_id"] != a.SourceBackendID || want["vg_uuid"] != a.SourceVGUUID || want["backend_resource_key"] != a.SourceBackendResourceKey || want["binding_id"] != a.SourceBindingID || want["cleanup_operation_id"] != claim.OperationID || want["desired_state"] != "ABSENT" || !backendGenerationOK || backendGeneration != a.SourceBackendGeneration || !bindingGenerationOK || bindingGeneration != a.SourceBindingGeneration || !cleanupGenerationOK || cleanupGeneration != claim.OperationGeneration || !exactPresentOK || exactPresent != o.ExactSourceLVPresent || !foreignPresentOK || foreignPresent != o.ForeignReplacementPresent || observedLV != o.ObservedLVUUID || (exactPresent && observedLV != a.SourceLVUUID) {
			return fmt.Errorf("Local LVM cleanup read-back claim mode mismatch: %w", ErrBackendCleanupStale)
		}
		readBack := ctype == locallvm.DeleteReadBackType && schema == locallvm.DeleteReadBackSchema
		apply := ctype == locallvm.DeleteCommandType && schema == locallvm.DeleteSchemaVersion
		if readBack && mode != "READ_BACK_FIRST" {
			return fmt.Errorf("Local LVM cleanup command schema mismatch: %w", ErrBackendCleanupStale)
		}
		if !readBack && !apply {
			return ErrBackendCleanupStale
		}
		if apply && mode == "READ_BACK_FIRST" {
			var present bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.backend_cleanup_observation_evidence p JOIN kim.execution_commands c ON c.command_id=p.command_id WHERE p.cleanup_operation_id=$1 AND p.cleanup_generation=$2 AND p.claim_generation=$3 AND p.result_state='PRESENT' AND c.command_type=$4)`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration, locallvm.DeleteReadBackType).Scan(&present); err != nil || !present {
				return fmt.Errorf("Local LVM cleanup apply preceded by no PRESENT read-back: %w", ErrBackendCleanupStale)
			}
		}
		if (b.ResultState == "ABSENT" && state != "MATCHED") || (b.ResultState == "PRESENT" && state != "NOT_APPLIED") || (b.ResultState == "UNKNOWN" && state != "UNKNOWN") || (b.ResultState == "CONFLICTING" && state != "CONFLICTING") {
			return fmt.Errorf("Local LVM cleanup result/verification mismatch: %w", ErrBackendCleanupStale)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_observation_evidence(cleanup_evidence_id,cleanup_operation_id,cleanup_generation,claim_generation,resource_type,resource_id,resource_generation,source_host_id,vm_id,vm_generation,source_materialization_generation,backend_identity_digest,backend_present,backend_running,identity_matches,apply_response_state,command_id,attempt_index,verification_id,verifier_digest,observation_generation,observation_digest,result_state,artifact_digest,evidence_digest) VALUES($1,$2,$3,$4,'LOCAL_LVM_VOLUME',$5,$6,$7,$8::uuid,$9,$10,$11,$12,false,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, b.EvidenceID, b.OperationID, b.OperationGeneration, b.ClaimGeneration, b.ResourceID, b.ResourceGeneration, b.SourceHostID, b.VMID, b.VMGeneration, b.MaterializationGeneration, b.BackendIdentityDigest, b.BackendPresent, b.IdentityMatches, b.ApplyResponseState, b.CommandID, b.AttemptIndex, b.VerificationID, b.VerifierDigest, b.ObservationGeneration, b.ObservationDigest, b.ResultState, b.ArtifactDigest, b.EvidenceDigest); err != nil {
			return err
		}
		identityDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%t/%t", a.SourceLVUUID, o.ObservedLVUUID, o.ExactSourceLVPresent, o.ForeignReplacementPresent)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_source_cleanup_observation_identity_evidence(cleanup_evidence_id,cleanup_operation_id,cleanup_generation,expected_backend_id,expected_backend_generation,expected_vg_uuid,expected_lv_uuid,expected_backend_resource_key,observed_lv_uuid,exact_source_lv_present,foreign_replacement_present,identity_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12)`, b.EvidenceID, b.OperationID, b.OperationGeneration, a.SourceBackendID, a.SourceBackendGeneration, a.SourceVGUUID, a.SourceLVUUID, a.SourceBackendResourceKey, o.ObservedLVUUID, o.ExactSourceLVPresent, o.ForeignReplacementPresent, identityDigest); err != nil {
			return err
		}
		if b.ResultState == "PRESENT" {
			return nil
		}
		operationState := "DISPATCH_UNKNOWN"
		var terminal any
		if b.ResultState == "ABSENT" {
			operationState = "VERIFIED"
			terminal = "cleanup-terminal-" + b.EvidenceID
			td := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/VERIFIED", b.OperationID, b.OperationGeneration, b.EvidenceDigest)))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.backend_cleanup_terminal_evidence(cleanup_terminal_id,cleanup_operation_id,cleanup_generation,cleanup_evidence_id,cleanup_evidence_digest,terminal_state,terminal_reason,terminal_digest) VALUES($1,$2,$3,$4,$5,'VERIFIED','exact_local_lvm_uuid_absence_observed',$6)`, terminal, b.OperationID, b.OperationGeneration, b.EvidenceID, b.EvidenceDigest, td); err != nil {
				return err
			}
		} else if b.ResultState == "CONFLICTING" {
			operationState = "CONFLICTING"
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.backend_cleanup_operations_current SET operation_state=$5,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,terminal_evidence_id=$6,updated_at=statement_timestamp() WHERE cleanup_operation_id=$1 AND cleanup_generation=$2 AND claim_owner=$3 AND claim_generation=$4`, claim.OperationID, claim.OperationGeneration, claim.Owner, claim.ClaimGeneration, operationState, terminal); err != nil {
			return err
		}
		projection := "UNKNOWN"
		if operationState == "VERIFIED" {
			projection = "VERIFIED"
		} else if operationState == "CONFLICTING" {
			projection = "CONFLICTING"
		}
		_, err = tx.Exec(ctx, `UPDATE kim.source_backend_cleanup_current SET storage_state=$3,cleanup_complete=(domain_state IN ('VERIFIED','NOT_REQUIRED') AND $3='VERIFIED' AND network_state IN ('VERIFIED','NOT_REQUIRED') AND pci_state IN ('VERIFIED','NOT_REQUIRED')),updated_at=statement_timestamp() WHERE vm_id=$1::uuid AND source_materialization_generation=$2`, a.VMID, a.MaterializationGeneration, projection)
		return err
	})
}

func ReclaimLocalLVMSourceCapacity(ctx context.Context, db TxBeginner, operationID string, generation uint64, evidenceID string) (LocalLVMCapacityReclamation, error) {
	var out LocalLVMCapacityReclamation
	if operationID == "" || generation == 0 || evidenceID == "" {
		return out, ErrBackendCleanupStale
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var existingID, existingDigest string
		if err := tx.QueryRow(ctx, `SELECT reclamation_evidence_id,reclamation_digest FROM kim.local_lvm_capacity_reclamation_evidence WHERE cleanup_operation_id=$1 AND cleanup_generation=$2`, operationID, generation).Scan(&existingID, &existingDigest); err == nil {
			if existingID != evidenceID {
				return ErrBackendCleanupStale
			}
			out.EvidenceID, out.Digest = existingID, existingDigest
			return nil
		}
		var terminalDigest string
		var backendID, bindingID, lvUUID, volumeID, claimID string
		var backendGen, bindingGen, capacityGen, bytes uint64
		if err := tx.QueryRow(ctx, `SELECT terminal.cleanup_terminal_id,terminal.terminal_digest,d.source_backend_id,d.source_backend_generation,d.source_capacity_claim_id,d.source_capacity_generation,d.reserved_bytes,d.source_volume_id,d.source_binding_id,d.source_binding_generation,d.source_lv_uuid FROM kim.backend_cleanup_operations_current c JOIN kim.backend_cleanup_terminal_evidence terminal ON terminal.cleanup_terminal_id=c.terminal_evidence_id AND terminal.terminal_state='VERIFIED' JOIN kim.local_lvm_source_cleanup_authority_evidence d ON d.cleanup_operation_id=c.cleanup_operation_id AND d.cleanup_generation=c.cleanup_generation JOIN kim.storage_backends_current backend ON backend.backend_id=d.source_backend_id AND backend.backend_generation=d.source_backend_generation AND backend.vg_uuid=d.source_vg_uuid JOIN kim.storage_capacity_claims capacity ON capacity.capacity_claim_id=d.source_capacity_claim_id AND capacity.claim_state='RELEASE_PENDING' JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=d.source_binding_id AND binding.binding_generation=d.source_binding_generation AND binding.lv_uuid=d.source_lv_uuid AND binding.binding_state='BOUND' WHERE c.cleanup_operation_id=$1 AND c.cleanup_generation=$2 AND c.operation_state='VERIFIED' FOR UPDATE OF capacity,binding`, operationID, generation).Scan(&out.CleanupTerminalID, &terminalDigest, &backendID, &backendGen, &claimID, &capacityGen, &bytes, &volumeID, &bindingID, &bindingGen, &lvUUID); err != nil {
			return ErrBackendCleanupStale
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%s/%d/%d/RELEASED", operationID, generation, terminalDigest, claimID, capacityGen, bytes)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.local_lvm_capacity_reclamation_evidence(reclamation_evidence_id,cleanup_operation_id,cleanup_generation,cleanup_terminal_id,cleanup_terminal_digest,capacity_claim_id,backend_id,backend_generation,capacity_generation,volume_id,binding_id,binding_generation,lv_uuid,released_bytes,prior_claim_state,resulting_claim_state,reclamation_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'RELEASE_PENDING','RELEASED',$15)`, evidenceID, operationID, generation, out.CleanupTerminalID, terminalDigest, claimID, backendID, backendGen, capacityGen, volumeID, bindingID, bindingGen, lvUUID, bytes, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.storage_capacity_claims SET claim_state='RELEASED' WHERE capacity_claim_id=$1 AND claim_state='RELEASE_PENDING'; UPDATE kim.volume_backend_binding_intents SET binding_state='RELEASED' WHERE binding_id=$2 AND binding_generation=$3 AND binding_state='RELEASE_PENDING'; UPDATE kim.volume_backend_bindings_current SET binding_state='REVOKED',updated_at=statement_timestamp() WHERE binding_id=$2 AND binding_generation=$3 AND binding_state='BOUND'; UPDATE kim.volumes_current SET lifecycle_state='DELETED' WHERE volume_id=$4 AND lifecycle_state<>'DELETED'`, pgx.QueryExecModeSimpleProtocol, claimID, bindingID, bindingGen, volumeID); err != nil {
			return err
		}
		out.EvidenceID, out.CapacityClaimID, out.VolumeID, out.BindingID, out.LVUUID, out.ReleasedBytes, out.Digest = evidenceID, claimID, volumeID, bindingID, lvUUID, bytes, digest
		return nil
	})
	return out, err
}
