package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
)

func TestVMAggregateLogicalUpdateDeletePostgreSQLIntegration(t *testing.T) {
	testVMAggregateVolumeProfilePostgreSQLIntegrationAfter(t, false, qualifyVMAggregateLogicalUpdateDelete)
}

func qualifyVMAggregateLogicalUpdateDelete(t *testing.T, ctx context.Context, db recoveryQualificationDB, suffix, vmID, host string, aggregate VMAggregate) {
	t.Helper()
	initialDependency, initialRuntime := aggregate.DependencyDigest, aggregate.RuntimeIntentGeneration
	metadata := VMAggregateMetadataUpdateRequest{RequestID: "vm-metadata-request-" + suffix, UpdateEvidenceID: "vm-metadata-evidence-" + suffix, VMID: vmID, ExpectedRevision: aggregate.VMRevision, Name: "renamed-vm-" + suffix}
	updated, err := UpdateVMAggregateMetadata(ctx, db, metadata)
	if err != nil || updated.VMRevision != aggregate.VMRevision+1 || updated.RuntimeIntentGeneration != initialRuntime || updated.DependencyDigest != initialDependency || updated.Name != metadata.Name {
		t.Fatalf("metadata update=%+v err=%v", updated, err)
	}
	if replay, err := UpdateVMAggregateMetadata(ctx, db, metadata); err != nil || replay.DesiredDigest != updated.DesiredDigest {
		t.Fatalf("metadata replay=%+v err=%v", replay, err)
	}
	staleMetadata := metadata
	staleMetadata.RequestID, staleMetadata.UpdateEvidenceID, staleMetadata.Name = staleMetadata.RequestID+"-stale", staleMetadata.UpdateEvidenceID+"-stale", metadata.Name+"-stale"
	if _, err := UpdateVMAggregateMetadata(ctx, db, staleMetadata); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("stale metadata revision accepted: %v", err)
	}

	powerTransition := func(current VMAggregate, label, desired string, lost bool) VMAggregate {
		t.Helper()
		request := VMAggregatePowerUpdateRequest{RequestID: "vm-power-" + label + "-request-" + suffix, OperationID: "vm-power-" + label + "-operation-" + suffix, VMID: vmID, ExpectedRevision: current.VMRevision, DesiredPowerState: desired}
		pending, err := StartVMAggregatePowerUpdate(ctx, db, request)
		if err != nil || pending.VMRevision != current.VMRevision+1 || pending.RuntimeIntentGeneration != current.RuntimeIntentGeneration+1 || pending.DependencyDigest != initialDependency || pending.OperationState != "PENDING" {
			t.Fatalf("%s pending=%+v err=%v", label, pending, err)
		}
		if replay, err := StartVMAggregatePowerUpdate(ctx, db, request); err != nil || replay.OperationID != pending.OperationID {
			t.Fatalf("%s start replay=%+v err=%v", label, replay, err)
		}
		claim, err := ClaimVMAggregateLifecycle(ctx, db, pending.OperationID, "vm-power-"+label+"-worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		commandID := "vm-power-" + label + "-command-" + suffix
		if err := AuthorizeVMAggregatePowerCommand(ctx, db, claim, "vm-power-"+label+"-authority-"+suffix, "vm-power-"+label+"-job-"+suffix, commandID); err != nil {
			t.Fatal(err)
		}
		verificationID, terminalID := "vm-power-"+label+"-aggregate-verification-"+suffix, "vm-power-"+label+"-terminal-"+suffix
		if _, err := CompleteVMAggregatePowerUpdate(ctx, db, claim, verificationID, terminalID); !errors.Is(err, ErrVMAggregateConflict) {
			t.Fatalf("%s command-only power accepted: %v", label, err)
		}
		commandVerification := "vm-power-" + label + "-command-verification-" + suffix
		evidence := map[string]any{"domain_uuid": vmID, "desired_state": desired, "observed_state": desired, "source": "libvirt_domain_state"}
		if lost {
			acceptEvacuationLostReadBack(t, ctx, db, host, commandID, commandVerification, uint64(current.RuntimeIntentGeneration+1), evidence)
		} else {
			acceptEvacuationCommand(t, ctx, db, host, commandID, commandVerification, uint64(current.RuntimeIntentGeneration+1), evidence, "SUCCEEDED")
		}
		if terminal, err := CompleteVMAggregatePowerUpdate(ctx, db, claim, verificationID, terminalID); err != nil || terminal != terminalID {
			t.Fatalf("%s terminal=%s err=%v", label, terminal, err)
		}
		if terminal, err := CompleteVMAggregatePowerUpdate(ctx, db, claim, verificationID, terminalID); err != nil || terminal != terminalID {
			t.Fatalf("%s terminal replay=%s err=%v", label, terminal, err)
		}
		converged, err := GetVMAggregate(ctx, db, vmID)
		if err != nil || converged.DesiredPowerState != desired || converged.ConvergenceState != "CONVERGED" || converged.OperationState != "VERIFIED" || converged.DependencyDigest != initialDependency {
			t.Fatalf("%s converged=%+v err=%v", label, converged, err)
		}
		return converged
	}

	updated = powerTransition(updated, "off-a", "SHUTOFF", true)
	updated = powerTransition(updated, "on", "RUNNING", false)
	if _, err := StartVMAggregateDelete(ctx, db, VMAggregateDeleteRequest{RequestID: "vm-delete-running-request-" + suffix, OperationID: "vm-delete-running-operation-" + suffix, VMID: vmID, ExpectedRevision: updated.VMRevision}); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("RUNNING delete accepted: %v", err)
	}
	updated = powerTransition(updated, "off-b", "SHUTOFF", true)

	protect := VMAggregateMetadataUpdateRequest{RequestID: "vm-protect-request-" + suffix, UpdateEvidenceID: "vm-protect-evidence-" + suffix, VMID: vmID, ExpectedRevision: updated.VMRevision, Name: updated.Name, DeleteProtection: true}
	updated, err = UpdateVMAggregateMetadata(ctx, db, protect)
	if err != nil || !updated.DeleteProtection {
		t.Fatalf("protect=%+v err=%v", updated, err)
	}
	if _, err := StartVMAggregateDelete(ctx, db, VMAggregateDeleteRequest{RequestID: "vm-delete-protected-request-" + suffix, OperationID: "vm-delete-protected-operation-" + suffix, VMID: vmID, ExpectedRevision: updated.VMRevision}); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("protected delete accepted: %v", err)
	}
	unprotect := VMAggregateMetadataUpdateRequest{RequestID: "vm-unprotect-request-" + suffix, UpdateEvidenceID: "vm-unprotect-evidence-" + suffix, VMID: vmID, ExpectedRevision: updated.VMRevision, Name: updated.Name}
	updated, err = UpdateVMAggregateMetadata(ctx, db, unprotect)
	if err != nil || updated.DeleteProtection {
		t.Fatalf("unprotect=%+v err=%v", updated, err)
	}

	deleteRequest := VMAggregateDeleteRequest{RequestID: "vm-delete-request-" + suffix, OperationID: "vm-delete-operation-" + suffix, VMID: vmID, ExpectedRevision: updated.VMRevision}
	deleting, err := StartVMAggregateDelete(ctx, db, deleteRequest)
	if err != nil || deleting.LifecycleState != "RETIRE_PENDING" || deleting.OperationState != "PENDING" || deleting.VMRevision != updated.VMRevision+1 {
		t.Fatalf("delete start=%+v err=%v", deleting, err)
	}
	if replay, err := StartVMAggregateDelete(ctx, db, deleteRequest); err != nil || replay.OperationID != deleting.OperationID {
		t.Fatalf("delete replay=%+v err=%v", replay, err)
	}
	claim, err := ClaimVMAggregateLifecycle(ctx, db, deleting.OperationID, "vm-delete-worker-"+suffix, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	domainCommand := "vm-delete-domain-command-" + suffix
	if _, err := AuthorizeVMAggregateDeleteDomainCommand(ctx, db, claim, "vm-delete-domain-job-"+suffix, domainCommand); err != nil {
		t.Fatal(err)
	}
	var planDigest, backendDigest string
	var vmGeneration, materializationGeneration uint64
	if err := db.QueryRow(ctx, `SELECT plan.plan_digest,d.vm_generation,d.materialization_generation FROM kim.vm_delete_operation_evidence d JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=d.plan_id WHERE d.delete_operation_id=$1`, deleting.OperationID).Scan(&planDigest, &vmGeneration, &materializationGeneration); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT payload->>'backend_identity_digest' FROM kim.execution_commands WHERE command_id=$1`, domainCommand).Scan(&backendDigest); err != nil {
		t.Fatal(err)
	}
	// The verifier payload is authoritative; no caller-provided boolean is
	// consumed by the delete terminal without this exact typed Command link.
	domainEvidence := map[string]any{"cleanup_operation_id": deleting.OperationID, "cleanup_generation": float64(1), "domain_uuid": vmID, "vm_generation": float64(vmGeneration), "source_host_id": host, "source_plan_digest": planDigest, "source_materialization_generation": float64(materializationGeneration), "backend_identity_digest": backendDigest, "domain_present": false, "domain_running": false, "identity_matches": true, "observed_plan_digest": planDigest, "observed_materialization_generation": float64(materializationGeneration)}
	domainCommandVerification := "vm-delete-domain-command-verification-" + suffix
	domainAttempt := acceptEvacuationCommand(t, ctx, db, host, domainCommand, domainCommandVerification, 1, domainEvidence, "SUCCEEDED")
	domainAbsenceID := "vm-delete-domain-absence-" + suffix
	if err := RecordVMAggregateDeleteDomainAbsence(ctx, db, claim, domainAbsenceID, domainCommand, domainCommandVerification, uint32(domainAttempt), 1, digestBytes([]byte(domainCommand+"/observation")), digestBytes([]byte(domainCommand+"/verifier"))); err != nil {
		t.Fatal(err)
	}

	rootCommand := "vm-delete-root-readback-command-" + suffix
	if _, err := AuthorizeVMAggregateDeleteRootAbsenceReadBack(ctx, db, claim, domainAbsenceID, "vm-delete-root-readback-job-"+suffix, rootCommand); err != nil {
		t.Fatal(err)
	}
	var volumeID, attachmentID, bindingID, lvUUID string
	var attachmentGeneration, bindingGeneration uint64
	if err := db.QueryRow(ctx, `SELECT root_volume_id,root_attachment_id,root_attachment_generation,root_binding_id,root_binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=root_binding_id AND binding_generation=root_binding_generation) FROM kim.vm_delete_operation_evidence WHERE delete_operation_id=$1`, deleting.OperationID).Scan(&volumeID, &attachmentID, &attachmentGeneration, &bindingID, &bindingGeneration, &lvUUID); err != nil {
		t.Fatal(err)
	}
	rootVerification := "vm-delete-root-readback-verification-" + suffix
	rootEvidence := map[string]any{"attachment_id": attachmentID, "volume_id": volumeID, "domain_uuid": vmID, "target_device": "vda", "observed_lv_uuid": lvUUID, "desired_state": libvirtvolume.StateDetached, "device_present": false, "device_identity_matches": false, "source_identity_matches": false, "holder_open": false, "read_only": false}
	rootAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, rootCommand, rootVerification, "MATCHED", 1, rootEvidence)
	rootObservation := "vm-delete-root-absence-observation-" + suffix
	if err := AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: rootObservation, AttachmentID: attachmentID, VolumeID: volumeID, BindingID: bindingID, HostID: host, DomainUUID: vmID, TargetDevice: "vda", ObservedLVUUID: lvUUID, DesiredState: libvirtvolume.StateDetached, CommandID: rootCommand, VerificationID: rootVerification, ObservationDigest: digestBytes([]byte(rootCommand + "/observation")), VerifierDigest: digestBytes([]byte(rootCommand + "/verifier")), EvidenceState: "MATCHED", AttachmentGeneration: attachmentGeneration, BindingGeneration: bindingGeneration, ObservationGeneration: 1, AttemptIndex: uint32(rootAttempt)}); err != nil {
		t.Fatal(err)
	}

	storageAbsenceID, releaseID := "vm-delete-storage-absence-"+suffix, "vm-delete-compute-release-"+suffix
	terminalID, tombstoneID := "vm-delete-terminal-"+suffix, "vm-delete-tombstone-"+suffix
	driftRollback := errors.New("rollback delete binding drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1`, bindingID); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateDelete(ctx, scopeTxBeginner{tx}, claim, domainAbsenceID, rootObservation, storageAbsenceID, releaseID, terminalID+"-drift", tombstoneID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("delete binding drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	if terminal, err := CompleteVMAggregateDelete(ctx, db, claim, domainAbsenceID, rootObservation, storageAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("delete terminal=%s err=%v", terminal, err)
	}
	if terminal, err := CompleteVMAggregateDelete(ctx, db, claim, domainAbsenceID, rootObservation, storageAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("delete terminal replay=%s err=%v", terminal, err)
	}
	deleted, err := GetVMAggregate(ctx, db, vmID)
	if err != nil || deleted.LifecycleState != "DELETED" || deleted.ConvergenceState != "CONVERGED" || deleted.OperationState != "VERIFIED" || deleted.DesiredPowerState != "SHUTOFF" {
		t.Fatalf("deleted aggregate=%+v err=%v", deleted, err)
	}
	var runtimeCount, tombstones int
	var computeState, volumeState, materializationState string
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.vm_resource_runtime_bindings_current WHERE vm_id=$1),(SELECT count(*) FROM kim.vm_resource_tombstone_evidence WHERE vm_id=$1),(SELECT claim_state FROM kim.compute_allocation_claims WHERE workload_id=$1::text),(SELECT lifecycle_state FROM kim.volumes_current WHERE volume_id=$2),(SELECT materialization_state FROM kim.volume_materializations_current WHERE volume_id=$2)`, vmID, volumeID).Scan(&runtimeCount, &tombstones, &computeState, &volumeState, &materializationState); err != nil || runtimeCount != 0 || tombstones != 1 || computeState != "RELEASED" || volumeState != "AVAILABLE" || materializationState != "VERIFIED" {
		t.Fatalf("delete projection runtime=%d tombstones=%d compute=%s volume=%s materialization=%s err=%v", runtimeCount, tombstones, computeState, volumeState, materializationState, err)
	}
	for _, table := range []string{"vm_logical_update_evidence", "vm_power_update_command_authority_evidence", "vm_power_update_verification_evidence", "vm_power_update_terminal_evidence", "vm_delete_operation_evidence", "vm_delete_domain_absence_evidence", "vm_delete_storage_absence_evidence", "vm_delete_compute_release_evidence", "vm_delete_terminal_evidence", "vm_resource_tombstone_evidence"} {
		if _, err := db.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
}
