package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
)

type VMAggregateMetadataUpdateRequest struct {
	RequestID, UpdateEvidenceID, VMID, Name string
	ExpectedRevision                        uint64
	DeleteProtection                        bool
}

type VMAggregatePowerUpdateRequest struct {
	RequestID, OperationID, VMID, DesiredPowerState string
	ExpectedRevision                                uint64
}

type VMAggregateDeleteRequest struct {
	RequestID, OperationID, VMID string
	ExpectedRevision             uint64
}

type VMAggregateDeleteCommand struct {
	JobID, CommandID, PayloadDigest string
}

// UpdateVMAggregateMetadata commits a logical revision only. It deliberately
// preserves the runtime intent generation, dependency snapshot and exact
// physical runtime binding.
func UpdateVMAggregateMetadata(ctx context.Context, db TxBeginner, r VMAggregateMetadataUpdateRequest) (VMAggregate, error) {
	if r.RequestID == "" || r.UpdateEvidenceID == "" || !vmUUIDPattern.MatchString(r.VMID) || r.ExpectedRevision == 0 || r.Name == "" || len(r.Name) > 255 {
		return VMAggregate{}, ErrVMAggregateConflict
	}
	requestDigest := digestVMAggregate(r)
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "vm-resource/"+r.VMID); err != nil {
			return err
		}
		var replayVM, replayDigest, replayEvidence string
		if err := tx.QueryRow(ctx, `SELECT vm_id::text,request_digest,update_evidence_id FROM kim.vm_logical_update_evidence WHERE request_id=$1`, r.RequestID).Scan(&replayVM, &replayDigest, &replayEvidence); err == nil {
			if replayVM != r.VMID || replayDigest != requestDigest || replayEvidence != r.UpdateEvidenceID {
				return ErrVMAggregateConflict
			}
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var revision, runtime uint64
		var projectID, name, lifecycle, convergence, operationState, priorDigest string
		var desiredPower string
		var flavorID, imageID, policyID, scopeID string
		var flavorRevision, imageRevision, policyRevision, scopeGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT c.vm_revision,c.runtime_intent_generation,c.project_id,e.vm_name,c.lifecycle_state,c.convergence_state,o.operation_state,c.desired_digest,e.desired_power_state,e.flavor_id,e.flavor_revision,e.image_id,e.image_revision,e.availability_policy_id,e.availability_policy_revision,e.placement_scope_id,e.placement_scope_generation FROM kim.vm_resources_current c JOIN kim.vm_resource_revision_evidence e ON(e.vm_id,e.vm_revision)=(c.vm_id,c.vm_revision) JOIN kim.vm_lifecycle_operations_current o ON o.operation_id=c.current_operation_id WHERE c.vm_id=$1 FOR UPDATE OF c`, r.VMID).Scan(&revision, &runtime, &projectID, &name, &lifecycle, &convergence, &operationState, &priorDigest, &desiredPower, &flavorID, &flavorRevision, &imageID, &imageRevision, &policyID, &policyRevision, &scopeID, &scopeGeneration); err != nil || revision != r.ExpectedRevision || lifecycle != "ACTIVE" || convergence != "CONVERGED" || operationState != "VERIFIED" {
			return ErrVMAggregateConflict
		}
		var currentProtection bool
		if err := tx.QueryRow(ctx, `SELECT delete_protection FROM kim.vm_resource_revision_evidence WHERE vm_id=$1 AND vm_revision=$2`, r.VMID, revision).Scan(&currentProtection); err != nil || (name == r.Name && currentProtection == r.DeleteProtection) {
			return ErrVMAggregateConflict
		}
		next := revision + 1
		resultingDigest := digestVMAggregate(map[string]any{"vm_id": r.VMID, "revision": next, "project_id": projectID, "name": r.Name, "flavor_id": flavorID, "flavor_revision": flavorRevision, "image_id": imageID, "image_revision": imageRevision, "availability_policy_id": policyID, "availability_policy_revision": policyRevision, "placement_scope_id": scopeID, "placement_scope_generation": scopeGeneration, "desired_power_state": desiredPower, "delete_protection": r.DeleteProtection, "lifecycle": "ACTIVE"})
		updateDigest := digestVMAggregate(map[string]any{"evidence_id": r.UpdateEvidenceID, "request_digest": requestDigest, "vm_id": r.VMID, "prior_revision": revision, "resulting_revision": next, "runtime_generation": runtime, "prior_digest": priorDigest, "resulting_digest": resultingDigest})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_resource_revision_evidence(vm_id,vm_revision,project_id,vm_name,flavor_id,flavor_revision,image_id,image_revision,availability_policy_id,availability_policy_revision,placement_scope_id,placement_scope_generation,desired_power_state,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'ACTIVE',$15,$16)`, r.VMID, next, projectID, r.Name, flavorID, flavorRevision, imageID, imageRevision, policyID, policyRevision, scopeID, scopeGeneration, desiredPower, r.DeleteProtection, revision, resultingDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_logical_update_evidence(update_evidence_id,request_id,request_digest,vm_id,prior_vm_revision,resulting_vm_revision,runtime_intent_generation,prior_desired_digest,resulting_desired_digest,vm_name,delete_protection,update_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, r.UpdateEvidenceID, r.RequestID, requestDigest, r.VMID, revision, next, runtime, priorDigest, resultingDigest, r.Name, r.DeleteProtection, updateDigest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resources_current SET vm_revision=$2,vm_name=$3,desired_digest=$4,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$5 AND runtime_intent_generation=$6`, r.VMID, next, r.Name, resultingDigest, revision, runtime); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resource_runtime_bindings_current SET vm_revision=$2,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$3 AND runtime_intent_generation=$4`, r.VMID, next, revision, runtime); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	if err != nil {
		return VMAggregate{}, err
	}
	return GetVMAggregate(ctx, db, r.VMID)
}

// StartVMAggregatePowerUpdate creates a new logical desired revision and a
// new runtime intent generation while reusing the exact immutable dependency
// snapshot. No Placement or physical incarnation is changed.
func StartVMAggregatePowerUpdate(ctx context.Context, db TxBeginner, r VMAggregatePowerUpdateRequest) (VMAggregate, error) {
	if r.RequestID == "" || r.OperationID == "" || !vmUUIDPattern.MatchString(r.VMID) || r.ExpectedRevision == 0 || (r.DesiredPowerState != "RUNNING" && r.DesiredPowerState != "SHUTOFF") {
		return VMAggregate{}, ErrVMAggregateConflict
	}
	requestDigest := digestVMAggregate(r)
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "vm-resource/"+r.VMID); err != nil {
			return err
		}
		var replayVM, replayOperation, replayDigest string
		if err := tx.QueryRow(ctx, `SELECT vm_id::text,operation_id,request_digest FROM kim.vm_lifecycle_operation_evidence WHERE request_id=$1`, r.RequestID).Scan(&replayVM, &replayOperation, &replayDigest); err == nil {
			if replayVM != r.VMID || replayOperation != r.OperationID || replayDigest != requestDigest {
				return ErrVMAggregateConflict
			}
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var revision, runtime uint64
		var projectID, name, lifecycle, convergence, operationState, priorDigest, currentPower, snapshotID, dependencyDigest string
		var deleteProtection bool
		var flavorID, imageID, policyID, scopeID string
		var flavorRevision, imageRevision, policyRevision, scopeGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT c.vm_revision,c.runtime_intent_generation,c.project_id,e.vm_name,c.lifecycle_state,c.convergence_state,o.operation_state,c.desired_digest,e.desired_power_state,e.delete_protection,e.flavor_id,e.flavor_revision,e.image_id,e.image_revision,e.availability_policy_id,e.availability_policy_revision,e.placement_scope_id,e.placement_scope_generation,i.dependency_snapshot_id,s.dependency_digest FROM kim.vm_resources_current c JOIN kim.vm_resource_revision_evidence e ON(e.vm_id,e.vm_revision)=(c.vm_id,c.vm_revision) JOIN kim.vm_lifecycle_operations_current o ON o.operation_id=c.current_operation_id JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.runtime_intent_generation)=(c.vm_id,c.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id WHERE c.vm_id=$1 FOR UPDATE OF c`, r.VMID).Scan(&revision, &runtime, &projectID, &name, &lifecycle, &convergence, &operationState, &priorDigest, &currentPower, &deleteProtection, &flavorID, &flavorRevision, &imageID, &imageRevision, &policyID, &policyRevision, &scopeID, &scopeGeneration, &snapshotID, &dependencyDigest); err != nil || revision != r.ExpectedRevision || lifecycle != "ACTIVE" || convergence != "CONVERGED" || operationState != "VERIFIED" || currentPower == r.DesiredPowerState {
			return ErrVMAggregateConflict
		}
		nextRevision, nextRuntime := revision+1, runtime+1
		resultingDigest := digestVMAggregate(map[string]any{"vm_id": r.VMID, "revision": nextRevision, "project_id": projectID, "name": name, "flavor_id": flavorID, "flavor_revision": flavorRevision, "image_id": imageID, "image_revision": imageRevision, "availability_policy_id": policyID, "availability_policy_revision": policyRevision, "placement_scope_id": scopeID, "placement_scope_generation": scopeGeneration, "desired_power_state": r.DesiredPowerState, "delete_protection": deleteProtection, "lifecycle": "ACTIVE"})
		intentDigest := digestVMAggregate(map[string]any{"vm_id": r.VMID, "vm_revision": nextRevision, "runtime_generation": nextRuntime, "snapshot": snapshotID, "dependency_digest": dependencyDigest, "power": r.DesiredPowerState})
		operationDigest := digestVMAggregate(map[string]any{"operation_id": r.OperationID, "generation": 1, "request_digest": requestDigest, "vm_id": r.VMID, "intent_digest": intentDigest})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_resource_revision_evidence(vm_id,vm_revision,project_id,vm_name,flavor_id,flavor_revision,image_id,image_revision,availability_policy_id,availability_policy_revision,placement_scope_id,placement_scope_generation,desired_power_state,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'ACTIVE',$15,$16)`, r.VMID, nextRevision, projectID, name, flavorID, flavorRevision, imageID, imageRevision, policyID, policyRevision, scopeID, scopeGeneration, r.DesiredPowerState, deleteProtection, revision, resultingDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation,vm_revision,dependency_snapshot_id,desired_power_state,intent_digest) VALUES($1,$2,$3,$4,$5,$6)`, r.VMID, nextRuntime, nextRevision, snapshotID, r.DesiredPowerState, intentDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_lifecycle_operation_evidence(operation_id,operation_generation,request_id,request_digest,operation_kind,vm_id,vm_revision,runtime_intent_generation,dependency_snapshot_id,dependency_digest,desired_power_state,operation_digest) VALUES($1,1,$2,$3,'POWER',$4,$5,$6,$7,$8,$9,$10)`, r.OperationID, r.RequestID, requestDigest, r.VMID, nextRevision, nextRuntime, snapshotID, dependencyDigest, r.DesiredPowerState, operationDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_lifecycle_operations_current(operation_id,operation_generation,vm_id,vm_revision,runtime_intent_generation,operation_state) VALUES($1,1,$2,$3,$4,'PENDING')`, r.OperationID, r.VMID, nextRevision, nextRuntime); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resources_current SET vm_revision=$2,runtime_intent_generation=$3,convergence_state='PENDING',current_operation_id=$4,desired_digest=$5,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$6 AND runtime_intent_generation=$7`, r.VMID, nextRevision, nextRuntime, r.OperationID, resultingDigest, revision, runtime); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	if err != nil {
		return VMAggregate{}, err
	}
	return GetVMAggregate(ctx, db, r.VMID)
}

func AuthorizeVMAggregatePowerCommand(ctx context.Context, db TxBeginner, claim VMAggregateClaim, authorityID, jobID, commandID string) error {
	if authorityID == "" || jobID == "" || commandID == "" {
		return ErrVMAggregateConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVMAggregateClaim(ctx, tx, claim); err != nil {
			return err
		}
		var vmID, desiredPower, admissionID, hostID, planID, priorPowerEvidence string
		var vmRevision, runtimeGeneration, vmGeneration, materializationGeneration, portCount uint64
		if err := tx.QueryRow(ctx, `SELECT o.vm_id::text,o.vm_revision,o.runtime_intent_generation,o.desired_power_state,b.admission_id,b.host_id,b.vm_generation,b.plan_id,b.materialization_generation,p.evidence_id,s.port_count FROM kim.vm_lifecycle_operation_evidence o JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=o.vm_id JOIN kim.vm_power_state_current p ON p.vm_id=b.vm_id AND p.vm_generation=b.vm_generation AND p.convergence_state='MATCHED' JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=o.dependency_snapshot_id WHERE o.operation_id=$1 AND o.operation_generation=$2 AND o.operation_kind='POWER'`, claim.OperationID, claim.OperationGeneration).Scan(&vmID, &vmRevision, &runtimeGeneration, &desiredPower, &admissionID, &hostID, &vmGeneration, &planID, &materializationGeneration, &priorPowerEvidence, &portCount); err != nil || portCount != 0 {
			return ErrVMAggregateConflict
		}
		if desiredPower == libvirtdomain.StateShutoff {
			var running bool
			if err := tx.QueryRow(ctx, `SELECT observed_power_state='RUNNING' FROM kim.vm_power_state_current WHERE vm_id=$1 AND vm_generation=$2`, vmID, vmGeneration).Scan(&running); err != nil || !running {
				return ErrVMAggregateConflict
			}
		} else {
			var ready, shutoff bool
			if err := tx.QueryRow(ctx, `SELECT r.boot_readiness='READY',p.observed_power_state='SHUTOFF' FROM kim.vm_materialization_readiness_current r JOIN kim.vm_power_state_current p ON p.vm_id=r.vm_id AND p.vm_generation=r.vm_generation WHERE r.vm_id=$1 AND r.vm_generation=$2 AND r.plan_id=$3`, vmID, vmGeneration, planID).Scan(&ready, &shutoff); err != nil || !ready || !shutoff {
				return ErrVMAggregateConflict
			}
		}
		var nextObservation uint64
		if err := tx.QueryRow(ctx, `SELECT observation_generation+1 FROM kim.vm_power_state_current WHERE vm_id=$1 AND vm_generation=$2`, vmID, vmGeneration).Scan(&nextObservation); err != nil {
			return ErrVMAggregateConflict
		}
		if err := createTypedVMPowerCommandTx(ctx, tx, vmID, vmGeneration, nextObservation, hostID, desiredPower, jobID, commandID); err != nil {
			return err
		}
		digest := digestVMAggregate(map[string]any{"authority_id": authorityID, "operation_id": claim.OperationID, "generation": claim.OperationGeneration, "vm_id": vmID, "vm_revision": vmRevision, "runtime_generation": runtimeGeneration, "admission_id": admissionID, "host_id": hostID, "vm_generation": vmGeneration, "plan_id": planID, "materialization_generation": materializationGeneration, "prior_power_evidence_id": priorPowerEvidence, "desired_power_state": desiredPower, "job_id": jobID, "command_id": commandID})
		tag, err := tx.Exec(ctx, `INSERT INTO kim.vm_power_update_command_authority_evidence(authority_evidence_id,operation_id,operation_generation,vm_id,vm_revision,runtime_intent_generation,admission_id,host_id,vm_generation,plan_id,materialization_generation,prior_power_evidence_id,desired_power_state,job_id,command_id,authority_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(authority_evidence_id) DO NOTHING`, authorityID, claim.OperationID, claim.OperationGeneration, vmID, vmRevision, runtimeGeneration, admissionID, hostID, vmGeneration, planID, materializationGeneration, priorPowerEvidence, desiredPower, jobID, commandID, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var accepted string
			if err := tx.QueryRow(ctx, `SELECT authority_digest FROM kim.vm_power_update_command_authority_evidence WHERE authority_evidence_id=$1`, authorityID).Scan(&accepted); err != nil || accepted != digest {
				return ErrVMAggregateConflict
			}
		}
		_, err = tx.Exec(ctx, `UPDATE kim.vm_lifecycle_operations_current SET operation_state='VERIFYING',updated_at=statement_timestamp() WHERE operation_id=$1`, claim.OperationID)
		return err
	})
}

func CompleteVMAggregatePowerUpdate(ctx context.Context, db TxBeginner, claim VMAggregateClaim, verificationID, terminalID string) (string, error) {
	if verificationID == "" || terminalID == "" {
		return "", ErrVMAggregateConflict
	}
	var replayVerification string
	if err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT verification_id FROM kim.vm_power_update_terminal_evidence WHERE terminal_evidence_id=$1 AND operation_id=$2 AND operation_generation=$3`, terminalID, claim.OperationID, claim.OperationGeneration).Scan(&replayVerification)
	}); err == nil {
		if replayVerification != verificationID {
			return "", ErrVMAggregateConflict
		}
		return terminalID, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVMAggregateClaim(ctx, tx, claim); err != nil {
			return err
		}
		var authorityID, vmID, desiredPower, admissionID, hostID, planID, commandID, powerEvidence string
		var vmRevision, runtimeGeneration, vmGeneration, powerGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT a.authority_evidence_id,a.vm_id::text,a.vm_revision,a.runtime_intent_generation,a.desired_power_state,a.admission_id,a.host_id,a.vm_generation,a.plan_id,a.command_id,p.evidence_id,p.observation_generation FROM kim.vm_power_update_command_authority_evidence a JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=a.vm_id AND b.admission_id=a.admission_id AND b.host_id=a.host_id AND b.vm_generation=a.vm_generation AND b.plan_id=a.plan_id JOIN kim.vm_power_state_current p ON p.vm_id=a.vm_id AND p.vm_generation=a.vm_generation AND p.desired_power_state=a.desired_power_state AND p.observed_power_state=a.desired_power_state AND p.convergence_state='MATCHED' JOIN kim.vm_power_observation_evidence pe ON pe.evidence_id=p.evidence_id AND pe.command_id=a.command_id AND pe.host_id=a.host_id WHERE a.operation_id=$1 AND a.operation_generation=$2`, claim.OperationID, claim.OperationGeneration).Scan(&authorityID, &vmID, &vmRevision, &runtimeGeneration, &desiredPower, &admissionID, &hostID, &vmGeneration, &planID, &commandID, &powerEvidence, &powerGeneration); err != nil {
			return ErrVMAggregateConflict
		}
		verificationDigest := digestVMAggregate(map[string]any{"verification_id": verificationID, "authority_id": authorityID, "vm_id": vmID, "vm_revision": vmRevision, "runtime_generation": runtimeGeneration, "admission_id": admissionID, "host_id": hostID, "vm_generation": vmGeneration, "plan_id": planID, "power_evidence_id": powerEvidence, "power_generation": powerGeneration, "desired": desiredPower})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_power_update_verification_evidence(verification_id,operation_id,operation_generation,command_authority_evidence_id,vm_id,vm_revision,runtime_intent_generation,admission_id,host_id,vm_generation,plan_id,power_evidence_id,power_observation_generation,desired_power_state,observed_power_state,verification_state,verification_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14,'VERIFIED',$15)`, verificationID, claim.OperationID, claim.OperationGeneration, authorityID, vmID, vmRevision, runtimeGeneration, admissionID, hostID, vmGeneration, planID, powerEvidence, powerGeneration, desiredPower, verificationDigest); err != nil {
			return err
		}
		terminalDigest := digestVMAggregate(map[string]any{"terminal_id": terminalID, "operation_id": claim.OperationID, "verification_id": verificationID, "verification_digest": verificationDigest, "state": "VERIFIED"})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_power_update_terminal_evidence(terminal_evidence_id,operation_id,operation_generation,verification_id,verification_digest,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,'VERIFIED',$6)`, terminalID, claim.OperationID, claim.OperationGeneration, verificationID, verificationDigest, terminalDigest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resource_runtime_bindings_current SET vm_revision=$2,runtime_intent_generation=$3,updated_at=statement_timestamp() WHERE vm_id=$1 AND admission_id=$4 AND host_id=$5 AND vm_generation=$6 AND plan_id=$7`, vmID, vmRevision, runtimeGeneration, admissionID, hostID, vmGeneration, planID); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resources_current SET lifecycle_state='ACTIVE',convergence_state='CONVERGED',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$2 AND runtime_intent_generation=$3 AND current_operation_id=$4`, vmID, vmRevision, runtimeGeneration, claim.OperationID); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_lifecycle_operations_current SET operation_state='VERIFIED',terminal_evidence_id=$2,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state='RECEIVED',updated_at=statement_timestamp() WHERE operation_id=$1 AND claim_owner=$3 AND claim_generation=$4`, claim.OperationID, terminalID, claim.Owner, claim.ClaimGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	return terminalID, err
}

// StartVMAggregateDelete fences the first qualified delete profile only after
// exact SHUTOFF read-back: zero Ports, one ROOT Volume, no PCI requirements.
func StartVMAggregateDelete(ctx context.Context, db TxBeginner, r VMAggregateDeleteRequest) (VMAggregate, error) {
	if r.RequestID == "" || r.OperationID == "" || !vmUUIDPattern.MatchString(r.VMID) || r.ExpectedRevision == 0 {
		return VMAggregate{}, ErrVMAggregateConflict
	}
	requestDigest := digestVMAggregate(r)
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "vm-resource/"+r.VMID); err != nil {
			return err
		}
		var replayVM, replayOperation, replayDigest string
		if err := tx.QueryRow(ctx, `SELECT vm_id::text,operation_id,request_digest FROM kim.vm_lifecycle_operation_evidence WHERE request_id=$1`, r.RequestID).Scan(&replayVM, &replayOperation, &replayDigest); err == nil {
			if replayVM != r.VMID || replayOperation != r.OperationID || replayDigest != requestDigest {
				return ErrVMAggregateConflict
			}
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var revision, runtime, vmGeneration, materializationGeneration, rootRevision, rootAttachmentGeneration, rootBindingGeneration uint64
		var projectID, name, lifecycle, convergence, operationState, priorDigest, snapshotID, dependencyDigest, admissionID, hostID, planID, rootVolumeID, rootAttachmentID, rootBindingID, computeID, powerEvidence string
		var flavorID, imageID, policyID, scopeID string
		var flavorRevision, imageRevision, policyRevision, scopeGeneration uint64
		var protected bool
		var portCount, volumeCount, pciCount int
		if err := tx.QueryRow(ctx, `SELECT c.vm_revision,c.runtime_intent_generation,c.project_id,e.vm_name,c.lifecycle_state,c.convergence_state,o.operation_state,c.desired_digest,e.delete_protection,e.flavor_id,e.flavor_revision,e.image_id,e.image_revision,e.availability_policy_id,e.availability_policy_revision,e.placement_scope_id,e.placement_scope_generation,s.dependency_snapshot_id,s.dependency_digest,s.port_count,s.volume_count,b.admission_id,b.host_id,b.vm_generation,b.plan_id,b.materialization_generation,d.volume_id,d.volume_revision,plan.root_attachment_id,plan.root_attachment_generation,plan.root_binding_id,plan.root_binding_generation,compute.allocation_id,power.evidence_id,jsonb_array_length(admission.pci_requirements) FROM kim.vm_resources_current c JOIN kim.vm_resource_revision_evidence e ON(e.vm_id,e.vm_revision)=(c.vm_id,c.vm_revision) JOIN kim.vm_lifecycle_operations_current o ON o.operation_id=c.current_operation_id JOIN kim.vm_runtime_intent_evidence i ON(i.vm_id,i.runtime_intent_generation)=(c.vm_id,c.runtime_intent_generation) JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=i.dependency_snapshot_id JOIN kim.vm_dependency_volume_evidence d ON d.dependency_snapshot_id=s.dependency_snapshot_id AND d.volume_ordinal=0 AND d.device_role='ROOT' JOIN kim.vm_resource_runtime_bindings_current b ON b.vm_id=c.vm_id AND b.vm_revision=c.vm_revision AND b.runtime_intent_generation=c.runtime_intent_generation JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=b.plan_id AND plan.vm_id=c.vm_id AND plan.vm_generation=b.vm_generation AND plan.placement_admission_id=b.admission_id AND plan.host_id=b.host_id AND plan.root_volume_id=d.volume_id JOIN kim.volume_attachments_current physical ON physical.attachment_id=plan.root_attachment_id AND physical.attachment_generation=plan.root_attachment_generation AND physical.placement_admission_id=b.admission_id AND physical.volume_id=d.volume_id AND physical.desired_host_id=b.host_id JOIN kim.volume_materializations_current materialized ON materialized.volume_id=d.volume_id AND materialized.volume_revision=d.volume_revision AND materialized.binding_id=plan.root_binding_id AND materialized.binding_generation=plan.root_binding_generation AND materialized.materialization_state='VERIFIED' JOIN kim.volume_backend_bindings_current root_binding ON root_binding.binding_id=plan.root_binding_id AND root_binding.binding_generation=plan.root_binding_generation AND root_binding.volume_id=d.volume_id AND root_binding.host_id=b.host_id AND root_binding.binding_state='BOUND' JOIN kim.compute_allocation_claims compute ON compute.admission_id=b.admission_id AND compute.workload_id=c.vm_id::text AND compute.claim_state IN('RESERVED','ALLOCATED') JOIN kim.vm_power_state_current power ON power.vm_id=c.vm_id AND power.vm_generation=b.vm_generation AND power.desired_power_state='SHUTOFF' AND power.observed_power_state='SHUTOFF' AND power.convergence_state='MATCHED' JOIN kim.placement_admission_decisions admission ON admission.admission_id=b.admission_id WHERE c.vm_id=$1 FOR UPDATE OF c,physical,root_binding,compute,power`, r.VMID).Scan(&revision, &runtime, &projectID, &name, &lifecycle, &convergence, &operationState, &priorDigest, &protected, &flavorID, &flavorRevision, &imageID, &imageRevision, &policyID, &policyRevision, &scopeID, &scopeGeneration, &snapshotID, &dependencyDigest, &portCount, &volumeCount, &admissionID, &hostID, &vmGeneration, &planID, &materializationGeneration, &rootVolumeID, &rootRevision, &rootAttachmentID, &rootAttachmentGeneration, &rootBindingID, &rootBindingGeneration, &computeID, &powerEvidence, &pciCount); err != nil || revision != r.ExpectedRevision || lifecycle != "ACTIVE" || convergence != "CONVERGED" || operationState != "VERIFIED" || protected || portCount > 1 || volumeCount != 1 || pciCount != 0 {
			return ErrVMAggregateConflict
		}
		var portID, portIntentID string
		var portRevision, portGeneration, portIntentGeneration, portBindingGeneration, retirementIntentGeneration uint64
		if portCount == 1 {
			if err := tx.QueryRow(ctx, `SELECT d.port_id,d.port_revision,p.port_generation,i.attachment_intent_id,i.attachment_generation,b.binding_generation,ovn.intent_generation+1 FROM kim.vm_dependency_port_evidence d JOIN kim.network_ports_current p ON p.port_id=d.port_id AND p.port_revision=d.port_revision AND p.desired_digest=d.desired_digest AND p.placement_admission_id=$2 AND p.workload_id=$3 AND p.authority_source='PORT_RESOURCE' AND p.datapath_profile='STANDARD' AND p.attachment_state='BOUND' JOIN kim.port_attachment_intents_current i ON i.port_id=p.port_id AND i.port_revision=p.port_revision AND i.workload_id=$3 AND i.intent_state='BOUND' JOIN kim.port_attachment_intent_evidence bound ON bound.attachment_intent_id=i.attachment_intent_id AND bound.attachment_generation=i.attachment_generation AND bound.placement_admission_id=$2 JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=$2 AND b.host_id=$4 AND b.binding_generation=bound.binding_generation AND b.binding_type='OVS' AND b.binding_state IN('RESERVED','ACTIVE') JOIN kim.network_ovn_state_current ovn ON ovn.port_id=p.port_id AND ovn.port_generation=p.port_generation AND ovn.binding_generation=b.binding_generation AND ovn.nb_state='MATCHED' WHERE d.dependency_snapshot_id=$1 AND d.port_ordinal=0 FOR UPDATE OF p,i,b`, snapshotID, admissionID, r.VMID, hostID).Scan(&portID, &portRevision, &portGeneration, &portIntentID, &portIntentGeneration, &portBindingGeneration, &retirementIntentGeneration); err != nil {
				return ErrVMAggregateConflict
			}
		}
		retireRevision := revision + 1
		retireDigest := digestVMAggregate(map[string]any{"vm_id": r.VMID, "revision": retireRevision, "project_id": projectID, "name": name, "flavor_id": flavorID, "flavor_revision": flavorRevision, "image_id": imageID, "image_revision": imageRevision, "availability_policy_id": policyID, "availability_policy_revision": policyRevision, "placement_scope_id": scopeID, "placement_scope_generation": scopeGeneration, "desired_power_state": "SHUTOFF", "delete_protection": false, "lifecycle": "RETIRE_PENDING"})
		operationDigest := digestVMAggregate(map[string]any{"operation_id": r.OperationID, "generation": 1, "request_digest": requestDigest, "vm_id": r.VMID, "retire_revision": retireRevision, "runtime_generation": runtime, "dependency_digest": dependencyDigest})
		authorityDigest := digestVMAggregate(map[string]any{"operation_id": r.OperationID, "vm_id": r.VMID, "source_revision": revision, "retire_revision": retireRevision, "runtime_generation": runtime, "admission_id": admissionID, "host_id": hostID, "vm_generation": vmGeneration, "plan_id": planID, "materialization_generation": materializationGeneration, "root_volume_id": rootVolumeID, "root_attachment_id": rootAttachmentID, "root_binding_id": rootBindingID, "compute_allocation_id": computeID, "power_evidence_id": powerEvidence})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_resource_revision_evidence(vm_id,vm_revision,project_id,vm_name,flavor_id,flavor_revision,image_id,image_revision,availability_policy_id,availability_policy_revision,placement_scope_id,placement_scope_generation,desired_power_state,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'SHUTOFF',false,'RETIRE_PENDING',$13,$14)`, r.VMID, retireRevision, projectID, name, flavorID, flavorRevision, imageID, imageRevision, policyID, policyRevision, scopeID, scopeGeneration, revision, retireDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_lifecycle_operation_evidence(operation_id,operation_generation,request_id,request_digest,operation_kind,vm_id,vm_revision,runtime_intent_generation,dependency_snapshot_id,dependency_digest,desired_power_state,operation_digest) VALUES($1,1,$2,$3,'DELETE',$4,$5,$6,$7,$8,'SHUTOFF',$9)`, r.OperationID, r.RequestID, requestDigest, r.VMID, retireRevision, runtime, snapshotID, dependencyDigest, operationDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_lifecycle_operations_current(operation_id,operation_generation,vm_id,vm_revision,runtime_intent_generation,operation_state) VALUES($1,1,$2,$3,$4,'PENDING')`, r.OperationID, r.VMID, retireRevision, runtime); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_delete_operation_evidence(delete_operation_id,operation_generation,request_id,request_digest,vm_id,source_vm_revision,retire_vm_revision,runtime_intent_generation,dependency_snapshot_id,dependency_digest,admission_id,host_id,vm_generation,plan_id,materialization_generation,root_volume_id,root_volume_revision,root_attachment_id,root_attachment_generation,root_binding_id,root_binding_generation,compute_allocation_id,shutoff_power_evidence_id,authority_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, r.OperationID, r.RequestID, requestDigest, r.VMID, revision, retireRevision, runtime, snapshotID, dependencyDigest, admissionID, hostID, vmGeneration, planID, materializationGeneration, rootVolumeID, rootRevision, rootAttachmentID, rootAttachmentGeneration, rootBindingID, rootBindingGeneration, computeID, powerEvidence, authorityDigest); err != nil {
			return err
		}
		if portCount == 1 {
			retirementOperationID := "vm-delete-port-retirement:" + r.OperationID
			retirementIntentID := "vm-delete-port-intent:" + r.OperationID
			networkDigest := digestVMAggregate(map[string]any{"delete_operation_id": r.OperationID, "vm_id": r.VMID, "retire_revision": retireRevision, "runtime_generation": runtime, "dependency_snapshot_id": snapshotID, "admission_id": admissionID, "host_id": hostID, "port_id": portID, "port_revision": portRevision, "port_generation": portGeneration, "attachment_intent_id": portIntentID, "attachment_generation": portIntentGeneration, "binding_generation": portBindingGeneration, "retirement_operation_id": retirementOperationID, "retirement_intent_id": retirementIntentID, "retirement_intent_generation": retirementIntentGeneration})
			if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_delete_network_operation_evidence(delete_operation_id,operation_generation,vm_id,retire_vm_revision,runtime_intent_generation,dependency_snapshot_id,admission_id,host_id,port_id,port_revision,port_generation,attachment_intent_id,attachment_generation,binding_generation,retirement_operation_id,retirement_intent_id,retirement_intent_generation,authority_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, r.OperationID, r.VMID, retireRevision, runtime, snapshotID, admissionID, hostID, portID, portRevision, portGeneration, portIntentID, portIntentGeneration, portBindingGeneration, retirementOperationID, retirementIntentID, retirementIntentGeneration, networkDigest); err != nil {
				return err
			}
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resources_current SET vm_revision=$2,lifecycle_state='RETIRE_PENDING',convergence_state='PENDING',current_operation_id=$3,desired_digest=$4,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$5 AND runtime_intent_generation=$6`, r.VMID, retireRevision, r.OperationID, retireDigest, revision, runtime); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resource_runtime_bindings_current SET vm_revision=$2,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$3 AND runtime_intent_generation=$4`, r.VMID, retireRevision, revision, runtime); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	if err != nil {
		return VMAggregate{}, err
	}
	return GetVMAggregate(ctx, db, r.VMID)
}

func loadVMAggregateDeleteTx(ctx context.Context, tx pgx.Tx, claim VMAggregateClaim) (map[string]any, error) {
	if err := lockVMAggregateClaim(ctx, tx, claim); err != nil {
		return nil, err
	}
	var vmID, hostID, planID, planDigest, rootVolumeID, rootAttachmentID, rootBindingID, vgUUID, lvUUID, resourceKey string
	var vmGeneration, materializationGeneration, rootAttachmentGeneration, rootBindingGeneration uint64
	if err := tx.QueryRow(ctx, `SELECT d.vm_id::text,d.host_id,d.vm_generation,d.plan_id,plan.plan_digest,d.materialization_generation,d.root_volume_id,d.root_attachment_id,d.root_attachment_generation,d.root_binding_id,d.root_binding_generation,b.vg_uuid,b.lv_uuid,b.backend_resource_key FROM kim.vm_delete_operation_evidence d JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=d.plan_id AND plan.vm_id=d.vm_id AND plan.vm_generation=d.vm_generation JOIN kim.volume_backend_bindings_current b ON b.binding_id=d.root_binding_id AND b.binding_generation=d.root_binding_generation AND b.binding_state='BOUND' JOIN kim.vm_power_state_current power ON power.vm_id=d.vm_id AND power.vm_generation=d.vm_generation AND power.evidence_id=d.shutoff_power_evidence_id AND power.observed_power_state='SHUTOFF' AND power.convergence_state='MATCHED' WHERE d.delete_operation_id=$1 AND d.operation_generation=$2`, claim.OperationID, claim.OperationGeneration).Scan(&vmID, &hostID, &vmGeneration, &planID, &planDigest, &materializationGeneration, &rootVolumeID, &rootAttachmentID, &rootAttachmentGeneration, &rootBindingID, &rootBindingGeneration, &vgUUID, &lvUUID, &resourceKey); err != nil {
		return nil, ErrVMAggregateConflict
	}
	return map[string]any{"vm_id": vmID, "host_id": hostID, "vm_generation": vmGeneration, "plan_id": planID, "plan_digest": planDigest, "materialization_generation": materializationGeneration, "root_volume_id": rootVolumeID, "root_attachment_id": rootAttachmentID, "root_attachment_generation": rootAttachmentGeneration, "root_binding_id": rootBindingID, "root_binding_generation": rootBindingGeneration, "vg_uuid": vgUUID, "lv_uuid": lvUUID, "backend_resource_key": resourceKey}, nil
}

func AuthorizeVMAggregateDeleteDomainCommand(ctx context.Context, db TxBeginner, claim VMAggregateClaim, jobID, commandID string) (VMAggregateDeleteCommand, error) {
	out := VMAggregateDeleteCommand{JobID: jobID, CommandID: commandID}
	if jobID == "" || commandID == "" {
		return out, ErrVMAggregateConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		d, err := loadVMAggregateDeleteTx(ctx, tx, claim)
		if err != nil {
			return err
		}
		backendDigest := digestVMAggregate(map[string]any{"vm_id": d["vm_id"], "host_id": d["host_id"], "plan_id": d["plan_id"], "plan_digest": d["plan_digest"], "materialization_generation": d["materialization_generation"]})
		payload := map[string]any{"cleanup_operation_id": claim.OperationID, "cleanup_generation": claim.OperationGeneration, "domain_uuid": d["vm_id"], "vm_generation": d["vm_generation"], "source_host_id": d["host_id"], "source_plan_digest": d["plan_digest"], "source_materialization_generation": d["materialization_generation"], "backend_identity_digest": backendDigest, "desired_state": "ABSENT"}
		out.PayloadDigest = digestVMAggregate(payload)
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: jobID, CommandID: commandID, HostID: d["host_id"].(string), ResourceType: "VM_DELETE_DOMAIN", ResourceID: claim.OperationID, DesiredRevision: int64(d["materialization_generation"].(uint64)), CommandType: libvirtvm.CleanupCommandType, SchemaVersion: libvirtvm.CleanupSchemaVersion, TargetResourceID: "vm:" + d["vm_id"].(string), Payload: payload}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.vm_lifecycle_operations_current SET operation_state='VERIFYING',updated_at=statement_timestamp() WHERE operation_id=$1`, claim.OperationID)
		return err
	})
	return out, err
}

func RecordVMAggregateDeleteDomainAbsence(ctx context.Context, db TxBeginner, claim VMAggregateClaim, evidenceID, commandID, verificationID string, attemptIndex uint32, observationGeneration uint64, observationDigest, verifierDigest string) error {
	if evidenceID == "" || commandID == "" || verificationID == "" || attemptIndex == 0 || observationGeneration == 0 || len(observationDigest) != 64 || len(verifierDigest) != 64 {
		return ErrVMAggregateConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		d, err := loadVMAggregateDeleteTx(ctx, tx, claim)
		if err != nil {
			return err
		}
		var accepted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.execution_commands c JOIN kim.command_verification_evidence v ON v.command_id=c.command_id AND v.verification_id=$3 AND v.attempt_index=$4 AND v.observation_generation=$5 AND v.observation_digest=$6 AND v.verifier_artifact_digest=$7 AND v.verification_state='MATCHED' WHERE c.command_id=$2 AND c.command_type=$8 AND c.schema_version=$9 AND c.host_id=$10 AND c.target_resource_id='vm:'||$11 AND c.payload->>'cleanup_operation_id'=$1 AND (c.payload->>'cleanup_generation')::bigint=$12 AND c.payload->>'domain_uuid'=$11 AND (c.payload->>'vm_generation')::bigint=$13 AND c.payload->>'source_host_id'=$10 AND c.payload->>'source_plan_digest'=$14 AND (c.payload->>'source_materialization_generation')::bigint=$15 AND c.payload->>'desired_state'='ABSENT' AND v.evidence_payload->>'cleanup_operation_id'=c.payload->>'cleanup_operation_id' AND v.evidence_payload->>'cleanup_generation'=c.payload->>'cleanup_generation' AND v.evidence_payload->>'domain_uuid'=c.payload->>'domain_uuid' AND v.evidence_payload->>'vm_generation'=c.payload->>'vm_generation' AND v.evidence_payload->>'source_host_id'=c.payload->>'source_host_id' AND v.evidence_payload->>'source_plan_digest'=c.payload->>'source_plan_digest' AND v.evidence_payload->>'source_materialization_generation'=c.payload->>'source_materialization_generation' AND v.evidence_payload->>'backend_identity_digest'=c.payload->>'backend_identity_digest' AND NOT (v.evidence_payload->>'domain_present')::boolean AND NOT (v.evidence_payload->>'domain_running')::boolean AND (v.evidence_payload->>'identity_matches')::boolean)`, claim.OperationID, commandID, verificationID, attemptIndex, observationGeneration, observationDigest, verifierDigest, libvirtvm.CleanupCommandType, libvirtvm.CleanupSchemaVersion, d["host_id"], d["vm_id"], claim.OperationGeneration, d["vm_generation"], d["plan_digest"], d["materialization_generation"]).Scan(&accepted); err != nil || !accepted {
			return ErrVMAggregateConflict
		}
		digest := digestVMAggregate(map[string]any{"evidence_id": evidenceID, "operation_id": claim.OperationID, "command_id": commandID, "verification_id": verificationID, "observation_generation": observationGeneration, "observation_digest": observationDigest, "verifier_digest": verifierDigest})
		_, err = tx.Exec(ctx, `INSERT INTO kim.vm_delete_domain_absence_evidence(absence_evidence_id,delete_operation_id,operation_generation,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,domain_present,identity_matches,absence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,false,true,$10)`, evidenceID, claim.OperationID, claim.OperationGeneration, commandID, attemptIndex, verificationID, observationGeneration, observationDigest, verifierDigest, digest)
		return err
	})
}

func AuthorizeVMAggregateDeleteRootAbsenceReadBack(ctx context.Context, db TxBeginner, claim VMAggregateClaim, domainAbsenceID, jobID, commandID string) (VMAggregateDeleteCommand, error) {
	out := VMAggregateDeleteCommand{JobID: jobID, CommandID: commandID}
	if domainAbsenceID == "" || jobID == "" || commandID == "" {
		return out, ErrVMAggregateConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		d, err := loadVMAggregateDeleteTx(ctx, tx, claim)
		if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_delete_domain_absence_evidence WHERE absence_evidence_id=$1 AND delete_operation_id=$2 AND operation_generation=$3)`, domainAbsenceID, claim.OperationID, claim.OperationGeneration).Scan(&exists); err != nil || !exists {
			return ErrVMAggregateConflict
		}
		payload := map[string]any{"domain_uuid": d["vm_id"], "volume_id": d["root_volume_id"], "vg_uuid": d["vg_uuid"], "lv_uuid": d["lv_uuid"], "backend_resource_key": d["backend_resource_key"], "disk_slot": 0, "desired_state": libvirtvolume.StateDetached, "access_mode": libvirtvolume.SingleWriter}
		out.PayloadDigest = digestVMAggregate(payload)
		return CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: jobID, CommandID: commandID, HostID: d["host_id"].(string), ResourceType: "VM_DELETE_ROOT_ABSENCE", ResourceID: d["root_attachment_id"].(string), DesiredRevision: int64(d["root_attachment_generation"].(uint64)), CommandType: libvirtvolume.CommandType, SchemaVersion: libvirtvolume.SchemaVersion, TargetResourceID: "attachment:" + d["root_attachment_id"].(string), Payload: payload})
	})
	return out, err
}

// AuthorizeVMAggregateDeletePortRetirement derives the exact runtime Port
// incarnation from the immutable delete snapshot. The caller supplies no
// Host, Port generation or binding generation authority.
func AuthorizeVMAggregateDeletePortRetirement(ctx context.Context, db TxBeginner, claim VMAggregateClaim, domainAbsenceID string) (OVNPortBindingRetirementDecision, error) {
	var out OVNPortBindingRetirementDecision
	if domainAbsenceID == "" {
		return out, ErrVMAggregateConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := loadVMAggregateDeleteTx(ctx, tx, claim); err != nil {
			return err
		}
		var operationID, intentID, portID, hostID string
		var operationGeneration, intentGeneration, portGeneration, bindingGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT n.retirement_operation_id,n.retirement_intent_id,n.port_id,n.host_id,n.operation_generation,n.retirement_intent_generation,n.port_generation,n.binding_generation FROM kim.vm_delete_network_operation_evidence n JOIN kim.vm_delete_domain_absence_evidence d ON d.delete_operation_id=n.delete_operation_id AND d.operation_generation=n.operation_generation AND d.absence_evidence_id=$3 WHERE n.delete_operation_id=$1 AND n.operation_generation=$2`, claim.OperationID, claim.OperationGeneration, domainAbsenceID).Scan(&operationID, &intentID, &portID, &hostID, &operationGeneration, &intentGeneration, &portGeneration, &bindingGeneration); err != nil {
			return ErrVMAggregateConflict
		}
		decision, err := CommitOVNPortBindingRetirement(ctx, scopeTxBeginner{tx}, OVNPortBindingRetirementRequest{OperationID: operationID, OperationGeneration: operationGeneration, IntentID: intentID, IntentGeneration: intentGeneration, PortID: portID, PortGeneration: portGeneration, BindingGeneration: bindingGeneration, SourceHostID: hostID})
		if err != nil {
			return ErrVMAggregateConflict
		}
		out = decision
		return nil
	})
	return out, err
}

// RecordVMAggregateDeleteNetworkAbsence consumes only an immutable generic
// retirement evidence identifier and re-derives every identity from DB state.
func RecordVMAggregateDeleteNetworkAbsence(ctx context.Context, db TxBeginner, claim VMAggregateClaim, absenceEvidenceID, retirementEvidenceID string) error {
	if absenceEvidenceID == "" || retirementEvidenceID == "" {
		return ErrVMAggregateConflict
	}
	var replayRetirement string
	if err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT retirement_evidence_id FROM kim.vm_delete_network_absence_evidence WHERE absence_evidence_id=$1 AND delete_operation_id=$2 AND operation_generation=$3`, absenceEvidenceID, claim.OperationID, claim.OperationGeneration).Scan(&replayRetirement)
	}); err == nil {
		if replayRetirement != retirementEvidenceID {
			return ErrVMAggregateConflict
		}
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := loadVMAggregateDeleteTx(ctx, tx, claim); err != nil {
			return err
		}
		var portID, hostID, retirementOperationID string
		var portRevision, portGeneration, bindingGeneration uint64
		var retirementDigest string
		if err := tx.QueryRow(ctx, `SELECT n.port_id,n.port_revision,n.port_generation,n.binding_generation,n.host_id,n.retirement_operation_id,e.evidence_digest FROM kim.vm_delete_network_operation_evidence n JOIN kim.network_port_binding_retirement_evidence e ON e.evidence_id=$3 AND e.operation_id=n.retirement_operation_id AND e.operation_generation=n.operation_generation AND e.port_id=n.port_id AND e.port_generation=n.port_generation AND e.binding_generation=n.binding_generation AND e.source_host_id=n.host_id AND e.retirement_state='VERIFIED' AND e.ownership_marker_matches AND e.logical_port_preserved AND e.requested_chassis_absent AND e.source_chassis_inactive AND e.source_ovs_interface_absent JOIN kim.network_port_binding_retirements_current c ON c.operation_id=e.operation_id AND c.operation_generation=e.operation_generation AND c.port_id=e.port_id AND c.port_generation=e.port_generation AND c.binding_generation=e.binding_generation AND c.retirement_state='VERIFIED' AND c.terminal_evidence_id=e.evidence_id WHERE n.delete_operation_id=$1 AND n.operation_generation=$2`, claim.OperationID, claim.OperationGeneration, retirementEvidenceID).Scan(&portID, &portRevision, &portGeneration, &bindingGeneration, &hostID, &retirementOperationID, &retirementDigest); err != nil {
			return ErrVMAggregateConflict
		}
		digest := digestVMAggregate(map[string]any{"absence_evidence_id": absenceEvidenceID, "delete_operation_id": claim.OperationID, "retirement_operation_id": retirementOperationID, "retirement_evidence_id": retirementEvidenceID, "retirement_digest": retirementDigest, "port_id": portID, "port_revision": portRevision, "port_generation": portGeneration, "binding_generation": bindingGeneration, "source_host_id": hostID})
		_, err := tx.Exec(ctx, `INSERT INTO kim.vm_delete_network_absence_evidence(absence_evidence_id,delete_operation_id,operation_generation,retirement_evidence_id,port_id,port_revision,port_generation,binding_generation,source_host_id,absence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, absenceEvidenceID, claim.OperationID, claim.OperationGeneration, retirementEvidenceID, portID, portRevision, portGeneration, bindingGeneration, hostID, digest)
		return err
	})
}

func CompleteVMAggregateDelete(ctx context.Context, db TxBeginner, claim VMAggregateClaim, domainAbsenceID, attachmentEvidenceID, storageAbsenceID, computeReleaseID, terminalID, tombstoneID string) (string, error) {
	return completeVMAggregateDelete(ctx, db, claim, domainAbsenceID, attachmentEvidenceID, "", storageAbsenceID, computeReleaseID, terminalID, tombstoneID)
}

func CompleteVMAggregateDeleteWithNetwork(ctx context.Context, db TxBeginner, claim VMAggregateClaim, domainAbsenceID, attachmentEvidenceID, networkAbsenceID, storageAbsenceID, computeReleaseID, terminalID, tombstoneID string) (string, error) {
	if networkAbsenceID == "" {
		return "", ErrVMAggregateConflict
	}
	return completeVMAggregateDelete(ctx, db, claim, domainAbsenceID, attachmentEvidenceID, networkAbsenceID, storageAbsenceID, computeReleaseID, terminalID, tombstoneID)
}

func completeVMAggregateDelete(ctx context.Context, db TxBeginner, claim VMAggregateClaim, domainAbsenceID, attachmentEvidenceID, networkAbsenceID, storageAbsenceID, computeReleaseID, terminalID, tombstoneID string) (string, error) {
	if domainAbsenceID == "" || attachmentEvidenceID == "" || storageAbsenceID == "" || computeReleaseID == "" || terminalID == "" || tombstoneID == "" {
		return "", ErrVMAggregateConflict
	}
	var replayDomain, replayNetwork, replayStorage, replayRelease string
	if err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT domain_absence_evidence_id,COALESCE(network_absence_evidence_id,''),storage_absence_evidence_id,compute_release_evidence_id FROM kim.vm_delete_terminal_evidence WHERE terminal_evidence_id=$1 AND delete_operation_id=$2 AND operation_generation=$3`, terminalID, claim.OperationID, claim.OperationGeneration).Scan(&replayDomain, &replayNetwork, &replayStorage, &replayRelease)
	}); err == nil {
		if replayDomain != domainAbsenceID || replayNetwork != networkAbsenceID || replayStorage != storageAbsenceID || replayRelease != computeReleaseID {
			return "", ErrVMAggregateConflict
		}
		return terminalID, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		d, err := loadVMAggregateDeleteTx(ctx, tx, claim)
		if err != nil {
			return err
		}
		var domainDigest string
		if err := tx.QueryRow(ctx, `SELECT absence_digest FROM kim.vm_delete_domain_absence_evidence WHERE absence_evidence_id=$1 AND delete_operation_id=$2 AND operation_generation=$3`, domainAbsenceID, claim.OperationID, claim.OperationGeneration).Scan(&domainDigest); err != nil {
			return ErrVMAggregateConflict
		}
		var expectedPorts int
		if err := tx.QueryRow(ctx, `SELECT s.port_count FROM kim.vm_delete_operation_evidence d JOIN kim.vm_dependency_snapshot_evidence s ON s.dependency_snapshot_id=d.dependency_snapshot_id WHERE d.delete_operation_id=$1 AND d.operation_generation=$2`, claim.OperationID, claim.OperationGeneration).Scan(&expectedPorts); err != nil || expectedPorts > 1 || (expectedPorts == 0) != (networkAbsenceID == "") {
			return ErrVMAggregateConflict
		}
		var networkDigest string
		if expectedPorts == 1 {
			if err := tx.QueryRow(ctx, `SELECT a.absence_digest FROM kim.vm_delete_network_absence_evidence a JOIN kim.vm_delete_network_operation_evidence n ON n.delete_operation_id=a.delete_operation_id AND n.operation_generation=a.operation_generation AND n.port_id=a.port_id AND n.port_revision=a.port_revision AND n.port_generation=a.port_generation AND n.binding_generation=a.binding_generation AND n.host_id=a.source_host_id JOIN kim.network_port_binding_retirements_current c ON c.operation_id=n.retirement_operation_id AND c.operation_generation=n.operation_generation AND c.port_id=n.port_id AND c.port_generation=n.port_generation AND c.binding_generation=n.binding_generation AND c.retirement_state='VERIFIED' AND c.terminal_evidence_id=a.retirement_evidence_id WHERE a.absence_evidence_id=$1 AND a.delete_operation_id=$2 AND a.operation_generation=$3`, networkAbsenceID, claim.OperationID, claim.OperationGeneration).Scan(&networkDigest); err != nil {
				return ErrVMAggregateConflict
			}
		}
		var accepted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.volume_attachment_observation_evidence e JOIN kim.volume_attachment_observations_current c ON c.attachment_id=e.attachment_id AND c.evidence_id=e.evidence_id AND c.observation_generation=e.observation_generation AND c.attachment_state='DETACHED' AND NOT c.device_present AND NOT c.holder_open JOIN kim.volume_attachment_claims claim ON claim.attachment_id=e.attachment_id AND claim.attachment_generation=e.attachment_generation AND claim.claim_state='RELEASED' WHERE e.evidence_id=$1 AND e.attachment_id=$2 AND e.volume_id=$3 AND e.attachment_generation=$4 AND e.binding_id=$5 AND e.binding_generation=$6 AND e.host_id=$7 AND e.domain_uuid=$8 AND e.desired_state='DETACHED' AND NOT e.device_present AND NOT e.holder_open AND e.evidence_state='MATCHED')`, attachmentEvidenceID, d["root_attachment_id"], d["root_volume_id"], d["root_attachment_generation"], d["root_binding_id"], d["root_binding_generation"], d["host_id"], d["vm_id"]).Scan(&accepted); err != nil || !accepted {
			return ErrVMAggregateConflict
		}
		storageDigest := digestVMAggregate(map[string]any{"evidence_id": storageAbsenceID, "operation_id": claim.OperationID, "domain_absence_id": domainAbsenceID, "domain_digest": domainDigest, "attachment_evidence_id": attachmentEvidenceID, "root_volume_id": d["root_volume_id"], "root_attachment_id": d["root_attachment_id"], "root_binding_id": d["root_binding_id"]})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_delete_storage_absence_evidence(absence_evidence_id,delete_operation_id,operation_generation,domain_absence_evidence_id,attachment_observation_evidence_id,root_volume_id,root_attachment_id,root_attachment_generation,root_binding_id,root_binding_generation,absence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, storageAbsenceID, claim.OperationID, claim.OperationGeneration, domainAbsenceID, attachmentEvidenceID, d["root_volume_id"], d["root_attachment_id"], d["root_attachment_generation"], d["root_binding_id"], d["root_binding_generation"], storageDigest); err != nil {
			return err
		}
		var admissionID, computeID, priorClaim string
		if err := tx.QueryRow(ctx, `SELECT admission_id,compute_allocation_id,(SELECT claim_state FROM kim.compute_allocation_claims WHERE allocation_id=compute_allocation_id) FROM kim.vm_delete_operation_evidence WHERE delete_operation_id=$1 FOR UPDATE`, claim.OperationID).Scan(&admissionID, &computeID, &priorClaim); err != nil || (priorClaim != "RESERVED" && priorClaim != "ALLOCATED" && priorClaim != "RELEASE_PENDING") {
			return ErrVMAggregateConflict
		}
		releaseDigest := digestVMAggregate(map[string]any{"release_id": computeReleaseID, "operation_id": claim.OperationID, "storage_absence_id": storageAbsenceID, "admission_id": admissionID, "compute_allocation_id": computeID, "prior_state": priorClaim, "resulting_state": "RELEASED"})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_delete_compute_release_evidence(release_evidence_id,delete_operation_id,operation_generation,storage_absence_evidence_id,admission_id,compute_allocation_id,prior_claim_state,resulting_claim_state,release_digest) VALUES($1,$2,$3,$4,$5,$6,$7,'RELEASED',$8)`, computeReleaseID, claim.OperationID, claim.OperationGeneration, storageAbsenceID, admissionID, computeID, priorClaim, releaseDigest); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.compute_allocation_claims SET claim_state='RELEASED',released_at=statement_timestamp() WHERE allocation_id=$1 AND claim_state=$2`, computeID, priorClaim); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		var oldIntentID string
		var oldIntentGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT attachment_intent_id,attachment_generation FROM kim.volume_attachment_intents_current WHERE volume_id=$1 AND workload_id=$2 FOR UPDATE`, d["root_volume_id"], d["vm_id"]).Scan(&oldIntentID, &oldIntentGeneration); err != nil {
			return ErrVMAggregateConflict
		}
		retiredIntentID := "vm-delete-volume-retirement:" + claim.OperationID
		intentDigest := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%s/%s/RETIRED", retiredIntentID, d["root_volume_id"], oldIntentGeneration+1, d["vm_id"], d["root_attachment_id"]))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_intent_evidence(attachment_intent_id,volume_id,volume_revision,attachment_generation,workload_id,requested_attachment_id,requested_physical_attachment_generation,intent_state,placement_admission_id,physical_attachment_id,binding_id,binding_generation,intent_digest) SELECT $1,volume_id,volume_revision,$2,workload_id,requested_attachment_id,requested_physical_attachment_generation,'RETIRED',placement_admission_id,physical_attachment_id,binding_id,binding_generation,$3 FROM kim.volume_attachment_intent_evidence WHERE attachment_intent_id=$4`, retiredIntentID, oldIntentGeneration+1, intentDigest, oldIntentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_intents_current SET attachment_intent_id=$2,attachment_generation=$3,intent_state='RETIRED',updated_at=statement_timestamp() WHERE volume_id=$1`, d["root_volume_id"], retiredIntentID, oldIntentGeneration+1); err != nil {
			return err
		}
		if expectedPorts == 1 {
			var portID, attachedIntentID, admissionID, hostID string
			var portRevision, portGeneration, attachedGeneration, bindingGeneration uint64
			if err := tx.QueryRow(ctx, `SELECT port_id,port_revision,port_generation,attachment_intent_id,attachment_generation,binding_generation,admission_id,host_id FROM kim.vm_delete_network_operation_evidence WHERE delete_operation_id=$1 AND operation_generation=$2`, claim.OperationID, claim.OperationGeneration).Scan(&portID, &portRevision, &portGeneration, &attachedIntentID, &attachedGeneration, &bindingGeneration, &admissionID, &hostID); err != nil {
				return ErrVMAggregateConflict
			}
			retiredPortIntentID := "vm-delete-port-attachment-retirement:" + claim.OperationID
			portIntentDigest := digestVMAggregate(map[string]any{"attachment_intent_id": retiredPortIntentID, "port_id": portID, "port_revision": portRevision, "attachment_generation": attachedGeneration + 1, "workload_id": d["vm_id"], "placement_admission_id": admissionID, "binding_generation": bindingGeneration, "intent_state": "RETIRED", "network_absence_digest": networkDigest})
			if _, err := tx.Exec(ctx, `INSERT INTO kim.port_attachment_intent_evidence(attachment_intent_id,port_id,port_revision,attachment_generation,workload_id,intent_state,placement_admission_id,binding_generation,intent_digest) SELECT $1,port_id,port_revision,$2,workload_id,'RETIRED',placement_admission_id,binding_generation,$3 FROM kim.port_attachment_intent_evidence WHERE attachment_intent_id=$4 AND attachment_generation=$5`, retiredPortIntentID, attachedGeneration+1, portIntentDigest, attachedIntentID, attachedGeneration); err != nil {
				return err
			}
			if tag, err := tx.Exec(ctx, `UPDATE kim.port_attachment_intents_current SET attachment_intent_id=$2,attachment_generation=$3,intent_state='RETIRED',updated_at=statement_timestamp() WHERE port_id=$1 AND port_revision=$4 AND attachment_intent_id=$5 AND attachment_generation=$6 AND workload_id=$7`, portID, retiredPortIntentID, attachedGeneration+1, portRevision, attachedIntentID, attachedGeneration, d["vm_id"]); err != nil || tag.RowsAffected() != 1 {
				return ErrVMAggregateConflict
			}
			if tag, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET binding_state='RELEASED' WHERE port_id=$1 AND placement_admission_id=$2 AND host_id=$3 AND binding_generation=$4 AND binding_state IN('RESERVED','ACTIVE')`, portID, admissionID, hostID, bindingGeneration); err != nil || tag.RowsAffected() != 1 {
				return ErrVMAggregateConflict
			}
			if tag, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET placement_admission_id=NULL,workload_id=NULL,desired_state='ACTIVE',attachment_state='UNATTACHED',updated_at=statement_timestamp() WHERE port_id=$1 AND port_revision=$2 AND port_generation=$3 AND placement_admission_id=$4 AND workload_id=$5 AND attachment_state='BOUND'`, portID, portRevision, portGeneration, admissionID, d["vm_id"]); err != nil || tag.RowsAffected() != 1 {
				return ErrVMAggregateConflict
			}
		}
		terminalDigest := digestVMAggregate(map[string]any{"terminal_id": terminalID, "operation_id": claim.OperationID, "domain_absence_id": domainAbsenceID, "network_absence_id": networkAbsenceID, "storage_absence_id": storageAbsenceID, "compute_release_id": computeReleaseID, "state": "VERIFIED"})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_delete_terminal_evidence(terminal_evidence_id,delete_operation_id,operation_generation,domain_absence_evidence_id,network_absence_evidence_id,storage_absence_evidence_id,compute_release_evidence_id,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,'VERIFIED',$8)`, terminalID, claim.OperationID, claim.OperationGeneration, domainAbsenceID, networkAbsenceID, storageAbsenceID, computeReleaseID, terminalDigest); err != nil {
			return err
		}
		var retireRevision, runtime uint64
		var projectID, name, flavorID, imageID, policyID, scopeID, retireDigest string
		var flavorRevision, imageRevision, policyRevision, scopeGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT d.retire_vm_revision,d.runtime_intent_generation,e.project_id,e.vm_name,e.flavor_id,e.flavor_revision,e.image_id,e.image_revision,e.availability_policy_id,e.availability_policy_revision,e.placement_scope_id,e.placement_scope_generation,e.desired_digest FROM kim.vm_delete_operation_evidence d JOIN kim.vm_resource_revision_evidence e ON(e.vm_id,e.vm_revision)=(d.vm_id,d.retire_vm_revision) WHERE d.delete_operation_id=$1`, claim.OperationID).Scan(&retireRevision, &runtime, &projectID, &name, &flavorID, &flavorRevision, &imageID, &imageRevision, &policyID, &policyRevision, &scopeID, &scopeGeneration, &retireDigest); err != nil {
			return err
		}
		finalRevision := retireRevision + 1
		deletedDigest := digestVMAggregate(map[string]any{"vm_id": d["vm_id"], "revision": finalRevision, "project_id": projectID, "name": name, "flavor_id": flavorID, "flavor_revision": flavorRevision, "image_id": imageID, "image_revision": imageRevision, "availability_policy_id": policyID, "availability_policy_revision": policyRevision, "placement_scope_id": scopeID, "placement_scope_generation": scopeGeneration, "desired_power_state": "SHUTOFF", "delete_protection": false, "lifecycle": "DELETED"})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_resource_revision_evidence(vm_id,vm_revision,project_id,vm_name,flavor_id,flavor_revision,image_id,image_revision,availability_policy_id,availability_policy_revision,placement_scope_id,placement_scope_generation,desired_power_state,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'SHUTOFF',false,'DELETED',$13,$14)`, d["vm_id"], finalRevision, projectID, name, flavorID, flavorRevision, imageID, imageRevision, policyID, policyRevision, scopeID, scopeGeneration, retireRevision, deletedDigest); err != nil {
			return err
		}
		tombstoneDigest := digestVMAggregate(map[string]any{"tombstone_id": tombstoneID, "vm_id": d["vm_id"], "final_revision": finalRevision, "delete_operation_id": claim.OperationID, "delete_terminal_id": terminalID, "terminal_digest": terminalDigest, "prior_desired_digest": retireDigest})
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_resource_tombstone_evidence(tombstone_evidence_id,vm_id,final_vm_revision,delete_operation_id,delete_terminal_evidence_id,prior_desired_digest,tombstone_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, tombstoneID, d["vm_id"], finalRevision, claim.OperationID, terminalID, retireDigest, tombstoneDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM kim.vm_resource_runtime_bindings_current WHERE vm_id=$1`, d["vm_id"]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM kim.vm_power_state_current WHERE vm_id=$1; DELETE FROM kim.vm_materialization_readiness_current WHERE vm_id=$1; UPDATE kim.virtual_machines_current SET lifecycle_state='DELETED',desired_power_state='SHUTOFF',updated_at=statement_timestamp() WHERE vm_id=$1`, pgx.QueryExecModeSimpleProtocol, d["vm_id"]); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_resources_current SET vm_revision=$2,lifecycle_state='DELETED',convergence_state='CONVERGED',desired_digest=$3,updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_revision=$4 AND runtime_intent_generation=$5 AND current_operation_id=$6`, d["vm_id"], finalRevision, deletedDigest, retireRevision, runtime, claim.OperationID); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_lifecycle_operations_current SET operation_state='VERIFIED',terminal_evidence_id=$2,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state='RECEIVED',updated_at=statement_timestamp() WHERE operation_id=$1 AND claim_owner=$3 AND claim_generation=$4`, claim.OperationID, terminalID, claim.Owner, claim.ClaimGeneration); err != nil || tag.RowsAffected() != 1 {
			return ErrVMAggregateConflict
		}
		return nil
	})
	return terminalID, err
}
