package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtvolume"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

func TestVMAggregateAuthorityPostgreSQLIntegration(t *testing.T) {
	testVMAggregateVolumeProfilePostgreSQLIntegration(t, false)
}

func TestVMAggregateDataVolumePostgreSQLIntegration(t *testing.T) {
	testVMAggregateVolumeProfilePostgreSQLIntegration(t, true)
}

func TestVMAggregateDataVolumeDeletePostgreSQLIntegration(t *testing.T) {
	testVMAggregateVolumeProfilePostgreSQLIntegrationAfter(t, true, qualifyVMAggregateDataVolumeDelete)
}

func testVMAggregateVolumeProfilePostgreSQLIntegration(t *testing.T, withData bool) {
	testVMAggregateVolumeProfilePostgreSQLIntegrationAfter(t, withData, nil)
}

func qualifyVMAggregateDataVolumeDelete(t *testing.T, ctx context.Context, db recoveryQualificationDB, suffix, vmID, host string, aggregate VMAggregate) {
	var vmGeneration, powerObservationGeneration uint64
	if err := db.QueryRow(ctx, `SELECT b.vm_generation,p.observation_generation FROM kim.vm_resource_runtime_bindings_current b JOIN kim.vm_power_state_current p ON p.vm_id=b.vm_id AND p.vm_generation=b.vm_generation WHERE b.vm_id=$1`, vmID).Scan(&vmGeneration, &powerObservationGeneration); err != nil {
		t.Fatal(err)
	}
	var rootDesiredDigest, dataDesiredDigest string
	if err := db.QueryRow(ctx, `SELECT (SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$1),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$2)`, aggregate.RootVolumeID, aggregate.DataVolumes[0].VolumeID).Scan(&rootDesiredDigest, &dataDesiredDigest); err != nil {
		t.Fatal(err)
	}
	shutdownCommand := "vm-data-delete-shutoff-command-" + suffix
	if err := AuthorizeVMPowerOff(ctx, db, vmID, vmGeneration, host, "vm-data-delete-shutoff-job-"+suffix, shutdownCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationCommand(t, ctx, db, host, shutdownCommand, "vm-data-delete-shutoff-verification-"+suffix, powerObservationGeneration+1, map[string]any{"domain_uuid": vmID, "desired_state": "SHUTOFF", "observed_state": "SHUTOFF", "source": "libvirt_domain_state"}, "SUCCEEDED")
	deleting, err := StartVMAggregateDelete(ctx, db, VMAggregateDeleteRequest{RequestID: "vm-data-delete-request-" + suffix, OperationID: "vm-data-delete-operation-" + suffix, VMID: vmID, ExpectedRevision: aggregate.VMRevision})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimVMAggregateLifecycle(ctx, db, deleting.OperationID, "vm-data-delete-worker-"+suffix, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	domainCommand := "vm-data-delete-domain-command-" + suffix
	if _, err = AuthorizeVMAggregateDeleteDomainCommand(ctx, db, claim, "vm-data-delete-domain-job-"+suffix, domainCommand); err != nil {
		t.Fatal(err)
	}
	var planDigest, backendDigest string
	var materializationGeneration uint64
	if err = db.QueryRow(ctx, `SELECT payload->>'source_plan_digest',(payload->>'source_materialization_generation')::bigint,payload->>'backend_identity_digest' FROM kim.execution_commands WHERE command_id=$1`, domainCommand).Scan(&planDigest, &materializationGeneration, &backendDigest); err != nil {
		t.Fatal(err)
	}
	domainVerification := "vm-data-delete-domain-verification-" + suffix
	domainAttempt := recordCleanupLeaseLossVerification(t, ctx, db, host, domainCommand, domainVerification, "MATCHED", 1, map[string]any{"cleanup_operation_id": deleting.OperationID, "cleanup_generation": 1, "domain_uuid": vmID, "vm_generation": vmGeneration, "source_host_id": host, "source_plan_digest": planDigest, "source_materialization_generation": materializationGeneration, "backend_identity_digest": backendDigest, "domain_present": false, "domain_running": false, "identity_matches": true})
	domainAbsenceID := "vm-data-delete-domain-absence-" + suffix
	if err = RecordVMAggregateDeleteDomainAbsence(ctx, db, claim, domainAbsenceID, domainCommand, domainVerification, uint32(domainAttempt), 1, digestBytes([]byte(domainCommand+"/observation")), digestBytes([]byte(domainCommand+"/verifier"))); err != nil {
		t.Fatal(err)
	}

	type detachAuthority struct {
		label, commandID, verificationID, evidenceID, volumeID, attachmentID, bindingID, lvUUID string
		attachmentGeneration, bindingGeneration                                                 uint64
	}
	readDetach := func(label string, authorize func(string, string) (VMAggregateDeleteCommand, error)) detachAuthority {
		t.Helper()
		a := detachAuthority{label: label, commandID: "vm-data-delete-" + label + "-command-" + suffix, verificationID: "vm-data-delete-" + label + "-verification-" + suffix, evidenceID: "vm-data-delete-" + label + "-observation-" + suffix}
		if _, err := authorize("vm-data-delete-"+label+"-job-"+suffix, a.commandID); err != nil {
			t.Fatal(err)
		}
		query := `SELECT root_volume_id,root_attachment_id,root_attachment_generation,root_binding_id,root_binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=root_binding_id AND binding_generation=root_binding_generation) FROM kim.vm_delete_operation_evidence WHERE delete_operation_id=$1`
		if label == "data" {
			query = `SELECT volume_id,physical_attachment_id,physical_attachment_generation,binding_id,binding_generation,(SELECT lv_uuid FROM kim.volume_backend_bindings_current WHERE binding_id=kim.vm_delete_data_volume_operation_evidence.binding_id AND binding_generation=kim.vm_delete_data_volume_operation_evidence.binding_generation) FROM kim.vm_delete_data_volume_operation_evidence WHERE delete_operation_id=$1`
		}
		if err := db.QueryRow(ctx, query, deleting.OperationID).Scan(&a.volumeID, &a.attachmentID, &a.attachmentGeneration, &a.bindingID, &a.bindingGeneration, &a.lvUUID); err != nil {
			t.Fatal(err)
		}
		attempt := recordCleanupLeaseLossVerification(t, ctx, db, host, a.commandID, a.verificationID, "MATCHED", 1, map[string]any{"attachment_id": a.attachmentID, "volume_id": a.volumeID, "domain_uuid": vmID, "target_device": map[bool]string{true: "vdb", false: "vda"}[label == "data"], "observed_lv_uuid": a.lvUUID, "desired_state": libvirtvolume.StateDetached, "device_present": false, "device_identity_matches": false, "source_identity_matches": false, "holder_open": false, "read_only": false})
		target := "vda"
		if label == "data" {
			target = "vdb"
		}
		if err := AcceptLocalLVMAttachmentObservation(ctx, db, LocalLVMAttachmentObservation{EvidenceID: a.evidenceID, AttachmentID: a.attachmentID, VolumeID: a.volumeID, BindingID: a.bindingID, HostID: host, DomainUUID: vmID, TargetDevice: target, ObservedLVUUID: a.lvUUID, DesiredState: libvirtvolume.StateDetached, CommandID: a.commandID, VerificationID: a.verificationID, ObservationDigest: digestBytes([]byte(a.commandID + "/observation")), VerifierDigest: digestBytes([]byte(a.commandID + "/verifier")), EvidenceState: "MATCHED", AttachmentGeneration: a.attachmentGeneration, BindingGeneration: a.bindingGeneration, ObservationGeneration: 1, AttemptIndex: uint32(attempt)}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	root := readDetach("root", func(jobID, commandID string) (VMAggregateDeleteCommand, error) {
		return AuthorizeVMAggregateDeleteRootAbsenceReadBack(ctx, db, claim, domainAbsenceID, jobID, commandID)
	})
	data := readDetach("data", func(jobID, commandID string) (VMAggregateDeleteCommand, error) {
		return AuthorizeVMAggregateDeleteDataAbsenceReadBack(ctx, db, claim, domainAbsenceID, jobID, commandID)
	})
	rootAbsenceID, dataAbsenceID := "vm-data-delete-root-absence-"+suffix, "vm-data-delete-data-absence-"+suffix
	releaseID, terminalID, tombstoneID := "vm-data-delete-compute-release-"+suffix, "vm-data-delete-terminal-"+suffix, "vm-data-delete-tombstone-"+suffix
	if _, err = CompleteVMAggregateDelete(ctx, db, claim, domainAbsenceID, root.evidenceID, rootAbsenceID+"-missing-data", releaseID+"-missing-data", terminalID+"-missing-data", tombstoneID+"-missing-data"); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("ROOT+DATA delete accepted without DATA absence: %v", err)
	}
	driftRollback := errors.New("rollback DATA binding drift")
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1 AND binding_generation=$2`, data.bindingID, data.bindingGeneration); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateDeleteWithData(ctx, scopeTxBeginner{tx}, claim, domainAbsenceID, root.evidenceID, data.evidenceID, rootAbsenceID+"-drift", dataAbsenceID+"-drift", releaseID+"-drift", terminalID+"-drift", tombstoneID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("DATA binding drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	if terminal, err := CompleteVMAggregateDeleteWithData(ctx, db, claim, domainAbsenceID, root.evidenceID, data.evidenceID, rootAbsenceID, dataAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("ROOT+DATA delete terminal=%s err=%v", terminal, err)
	}
	if terminal, err := CompleteVMAggregateDeleteWithData(ctx, db, claim, domainAbsenceID, root.evidenceID, data.evidenceID, rootAbsenceID, dataAbsenceID, releaseID, terminalID, tombstoneID); err != nil || terminal != terminalID {
		t.Fatalf("ROOT+DATA delete replay=%s err=%v", terminal, err)
	}
	var retired, available, verified, capacityHeld int
	var resultingRootDigest, resultingDataDigest, computeState string
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.volume_attachment_intents_current WHERE workload_id=$1 AND intent_state='RETIRED'),(SELECT count(*) FROM kim.volumes_current WHERE volume_id IN($2,$3) AND lifecycle_state='AVAILABLE'),(SELECT count(*) FROM kim.volume_materializations_current WHERE volume_id IN($2,$3) AND materialization_state='VERIFIED'),(SELECT count(*) FROM kim.storage_capacity_claims WHERE volume_id IN($2,$3) AND claim_state IN('RESERVED','ALLOCATED')),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$2),(SELECT desired_digest FROM kim.volumes_current WHERE volume_id=$3),(SELECT claim_state FROM kim.compute_allocation_claims WHERE workload_id=$1)`, vmID, root.volumeID, data.volumeID).Scan(&retired, &available, &verified, &capacityHeld, &resultingRootDigest, &resultingDataDigest, &computeState); err != nil || retired != 2 || available != 2 || verified != 2 || capacityHeld != 2 || resultingRootDigest != rootDesiredDigest || resultingDataDigest != dataDesiredDigest || computeState != "RELEASED" {
		t.Fatalf("post-delete storage retired=%d available=%d verified=%d capacityHeld=%d err=%v", retired, available, verified, capacityHeld, err)
	}
	deleted, err := GetVMAggregate(ctx, db, vmID)
	if err != nil || deleted.LifecycleState != "DELETED" || deleted.ConvergenceState != "CONVERGED" || deleted.OperationState != "VERIFIED" {
		t.Fatalf("deleted ROOT+DATA aggregate=%+v err=%v", deleted, err)
	}
	for _, table := range []string{"vm_delete_data_volume_operation_evidence", "vm_delete_data_storage_absence_evidence"} {
		if _, err = db.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
}

type vmAggregateProfileCallback func(*testing.T, context.Context, recoveryQualificationDB, string, string, string, VMAggregate)

func testVMAggregateVolumeProfilePostgreSQLIntegrationAfter(t *testing.T, withData bool, after vmAggregateProfileCallback) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, url, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('vm-aggregate',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	vmID := fmt.Sprintf("82000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	host, group := "vm-aggregate-host-"+suffix, "vm-aggregate-pool-"+suffix
	prepareEvacuationHost(t, ctx, pool, host)
	if err = UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: group, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "vm-aggregate-placement", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{PublishRequestID: "vm-aggregate-members-" + suffix, HostGroupID: group, SourceType: "EXPLICIT", SourceRevision: suffix, BasedOnHostGroupGeneration: 1, Members: []HostGroupMembership{{HostGroupID: group, HostID: host, Generation: 1, State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: suffix}}}); err != nil {
		t.Fatal(err)
	}

	policyID := "vm-aggregate-policy-" + suffix
	policy := availabilityPolicyFixture(policyID, 1, "WORKLOAD_MANAGED", "NO_AUTOMATIC_ACTION", "ACTIVE")
	policyDigest, err := PublishAvailabilityPolicy(ctx, pool, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PublishGroupPolicyBinding(ctx, pool, GroupPolicyBindingRequest{PublishRequestID: "vm-aggregate-policy-binding-" + suffix, BindingID: "vm-aggregate-policy-binding-" + suffix, HostGroupID: group, HostGroupGeneration: 1, PolicyType: "AVAILABILITY_POLICY", ConsumerType: "VM_PLACEMENT", PolicyID: policyID, PolicyRevision: 1, PolicyDigest: policyDigest, Priority: 100, LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	scopeID := "vm-aggregate-scope-" + suffix
	if _, err = PublishPlacementScope(ctx, pool, PlacementScopePublishRequest{PublishRequestID: "vm-aggregate-scope-publish-" + suffix, PlacementScopeID: scopeID, ConsumerType: "VM_PLACEMENT", ProjectID: "project", LifecycleState: "ACTIVE", Exposures: []PlacementScopeExposure{{HostGroupID: group, HostGroupGeneration: 1}}}); err != nil {
		t.Fatal(err)
	}

	backendID, vgUUID, classID := "vm-aggregate-backend-"+suffix, "vm-aggregate-vg-"+suffix, "vm-aggregate-class-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, pool, LocalLVMFoundation{BackendID: backendID, HostID: host, VGUUID: vgUUID, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: classID, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "vm-aggregate-capacity-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	imageID, flavorID := "vm-aggregate-image-"+suffix, "vm-aggregate-flavor-"+suffix
	imageDigest := digestBytes([]byte("vm-aggregate-image"))
	if _, err = RegisterImageRevision(ctx, pool, ImageRevision{ImageID: imageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: imageDigest, ObservedChecksum: imageDigest, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("signature")), SourceURI: "qualification://vm-aggregate", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: flavorID, Revision: 1, OwnerProjectID: "project", Name: "vm-aggregate.small", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}

	volumeRequest := VolumeResourceRequest{VolumeID: "vm-aggregate-root-" + suffix, ProjectID: "project", Name: "root", StorageClassID: classID, StorageClassRevision: 1, SizeBytes: 16 << 20, Bootable: true, SourceType: "BLANK"}
	volume, err := CreateVolumeResource(ctx, pool, volumeRequest)
	if err != nil {
		t.Fatal(err)
	}
	volume, err = AllocateVolumeCapacity(ctx, pool, VolumeCapacityAllocationRequest{VolumeID: volume.VolumeID, BackendID: backendID, ExpectedVolumeRevision: 1, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := &volumeResourceLVMClient{vgUUID: vgUUID}
	mutation := locallvm.Backend{Client: client, VolumeGroups: map[string]string{vgUUID: "kim_test_vg"}}
	readOnly := locallvm.ReadBackBackend{Backend: mutation}
	first, err := ClaimVolumeMaterialization(ctx, pool, volume.OperationID, "vm-aggregate-volume-worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := AuthorizeVolumeMaterializationCommand(ctx, pool, first, "vm-aggregate-volume-apply-job-"+suffix, "vm-aggregate-volume-apply-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, pool, apply.CommandID, 1, CommandLeaseScopeMutation, mutation)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, pool, first); err != nil {
		t.Fatal(err)
	}
	successor, err := ClaimVolumeMaterialization(ctx, pool, volume.OperationID, "vm-aggregate-volume-worker-b", time.Minute)
	if err != nil || successor.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("successor=%+v err=%v", successor, err)
	}
	read, err := AuthorizeVolumeMaterializationCommand(ctx, pool, successor, "vm-aggregate-volume-read-job-"+suffix, "vm-aggregate-volume-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	volumeVerification := observeVolumeBackendAfterLostResponse(t, ctx, pool, read.CommandID, "vm-aggregate-volume-verification-"+suffix, 1, readOnly)
	if _, err = CompleteVolumeMaterialization(ctx, pool, successor, CompleteVolumeMaterializationRequest{OperationID: successor.OperationID, OperationGeneration: successor.OperationGeneration, ClaimGeneration: successor.ClaimGeneration, ObservationID: "vm-aggregate-volume-observation-" + suffix, VerificationID: volumeVerification}); err != nil {
		t.Fatal(err)
	}
	volume, err = GetVolumeResource(ctx, pool, volume.VolumeID)
	if err != nil || volume.Lifecycle != "AVAILABLE" || volume.MaterializationState != "VERIFIED" {
		t.Fatalf("volume=%+v err=%v", volume, err)
	}
	var dataVolume VolumeResource
	if withData {
		dataVolume, err = CreateVolumeResource(ctx, pool, VolumeResourceRequest{VolumeID: "vm-aggregate-data-" + suffix, ProjectID: "project", Name: "data", StorageClassID: classID, StorageClassRevision: 1, SizeBytes: 8 << 20, Bootable: false, SourceType: "BLANK"})
		if err != nil {
			t.Fatal(err)
		}
		dataVolume, err = AllocateVolumeCapacity(ctx, pool, VolumeCapacityAllocationRequest{VolumeID: dataVolume.VolumeID, BackendID: backendID, ExpectedVolumeRevision: 1, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1})
		if err != nil {
			t.Fatal(err)
		}
		dataClient := &volumeResourceLVMClient{vgUUID: vgUUID, lvUUID: "lv-vm-aggregate-data-" + suffix}
		dataMutation := locallvm.Backend{Client: dataClient, VolumeGroups: map[string]string{vgUUID: "kim_test_vg"}}
		dataReadOnly := locallvm.ReadBackBackend{Backend: dataMutation}
		dataClaim, err := ClaimVolumeMaterialization(ctx, pool, dataVolume.OperationID, "vm-aggregate-data-worker-a", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		dataApply, err := AuthorizeVolumeMaterializationCommand(ctx, pool, dataClaim, "vm-aggregate-data-apply-job-"+suffix, "vm-aggregate-data-apply-command-"+suffix, false)
		if err != nil {
			t.Fatal(err)
		}
		runVolumeBackendWithLostResponse(t, ctx, pool, dataApply.CommandID, 1, CommandLeaseScopeMutation, dataMutation)
		if err = MarkVolumeMaterializationDispatchUnknown(ctx, pool, dataClaim); err != nil {
			t.Fatal(err)
		}
		dataClaim, err = ClaimVolumeMaterialization(ctx, pool, dataVolume.OperationID, "vm-aggregate-data-worker-b", time.Minute)
		if err != nil || dataClaim.ClaimMode != "READ_BACK_FIRST" {
			t.Fatalf("data successor=%+v err=%v", dataClaim, err)
		}
		dataRead, err := AuthorizeVolumeMaterializationCommand(ctx, pool, dataClaim, "vm-aggregate-data-read-job-"+suffix, "vm-aggregate-data-read-command-"+suffix, true)
		if err != nil {
			t.Fatal(err)
		}
		dataVerification := observeVolumeBackendAfterLostResponse(t, ctx, pool, dataRead.CommandID, "vm-aggregate-data-verification-"+suffix, 1, dataReadOnly)
		if _, err = CompleteVolumeMaterialization(ctx, pool, dataClaim, CompleteVolumeMaterializationRequest{OperationID: dataClaim.OperationID, OperationGeneration: dataClaim.OperationGeneration, ClaimGeneration: dataClaim.ClaimGeneration, ObservationID: "vm-aggregate-data-observation-" + suffix, VerificationID: dataVerification}); err != nil {
			t.Fatal(err)
		}
		dataVolume, err = GetVolumeResource(ctx, pool, dataVolume.VolumeID)
		if err != nil || dataVolume.Lifecycle != "AVAILABLE" || dataVolume.MaterializationState != "VERIFIED" || dataVolume.Bootable {
			t.Fatalf("data volume=%+v err=%v", dataVolume, err)
		}
	}

	create := VMAggregateCreateRequest{RequestID: "vm-aggregate-create-request-" + suffix, OperationID: "vm-aggregate-create-operation-" + suffix, VMID: vmID, ProjectID: "project", Name: "qualified-vm-" + suffix, FlavorID: flavorID, FlavorRevision: 1, ImageID: imageID, ImageRevision: 1, AvailabilityPolicyID: policyID, AvailabilityPolicyRevision: 1, PlacementScopeID: scopeID, PlacementScopeGeneration: 1, RootVolumeID: volume.VolumeID, RootVolumeRevision: 1, DesiredPowerState: "RUNNING"}
	if withData {
		create.DataVolumes = []VMAggregateVolumeRequest{{VolumeID: dataVolume.VolumeID, VolumeRevision: 1}}
	}
	wrong := create
	wrong.RequestID, wrong.OperationID, wrong.VMID, wrong.RootVolumeRevision = wrong.RequestID+"-wrong", wrong.OperationID+"-wrong", "82000000-0000-4000-8000-000000000002", 2
	if _, err = CreateVMAggregate(ctx, pool, wrong); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("stale Volume revision accepted: %v", err)
	}
	if withData {
		staleData := create
		staleData.DataVolumes = append([]VMAggregateVolumeRequest(nil), create.DataVolumes...)
		staleData.RequestID, staleData.OperationID, staleData.VMID = staleData.RequestID+"-stale-data", staleData.OperationID+"-stale-data", "82000000-0000-4000-8000-000000000003"
		staleData.DataVolumes[0].VolumeRevision = 2
		if _, err = CreateVMAggregate(ctx, pool, staleData); !errors.Is(err, ErrVMAggregateConflict) {
			t.Fatalf("stale DATA revision accepted: %v", err)
		}
		duplicate := create
		duplicate.RequestID, duplicate.OperationID, duplicate.VMID = duplicate.RequestID+"-duplicate", duplicate.OperationID+"-duplicate", "82000000-0000-4000-8000-000000000004"
		duplicate.DataVolumes = []VMAggregateVolumeRequest{{VolumeID: volume.VolumeID, VolumeRevision: 1}}
		if _, err = CreateVMAggregate(ctx, pool, duplicate); !errors.Is(err, ErrVMAggregateConflict) {
			t.Fatalf("ROOT reused as DATA accepted: %v", err)
		}
		oversized := create
		oversized.RequestID, oversized.OperationID, oversized.VMID = oversized.RequestID+"-oversized", oversized.OperationID+"-oversized", "82000000-0000-4000-8000-000000000005"
		oversized.DataVolumes = append(append([]VMAggregateVolumeRequest(nil), create.DataVolumes...), VMAggregateVolumeRequest{VolumeID: "unqualified-third", VolumeRevision: 1})
		if _, err = CreateVMAggregate(ctx, pool, oversized); !errors.Is(err, ErrVMAggregateConflict) {
			t.Fatalf("unqualified Volume cardinality accepted: %v", err)
		}
	}
	aggregate, err := CreateVMAggregate(ctx, pool, create)
	if err != nil || aggregate.VMRevision != 1 || aggregate.RuntimeIntentGeneration != 1 || aggregate.OperationState != "PENDING" || len(aggregate.DataVolumes) != len(create.DataVolumes) {
		t.Fatalf("aggregate=%+v err=%v", aggregate, err)
	}
	if replay, err := CreateVMAggregate(ctx, pool, create); err != nil || replay.DependencyDigest != aggregate.DependencyDigest {
		t.Fatalf("create replay=%+v err=%v", replay, err)
	}

	claim, err := ClaimVMAggregateLifecycle(ctx, pool, aggregate.OperationID, "vm-aggregate-worker-placement", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	placementRequest, err := CompileVMAggregatePlacement(ctx, pool, claim)
	expectedStorage := 1
	if withData {
		expectedStorage = 2
	}
	if err != nil || placementRequest.WorkloadID != vmID || len(placementRequest.Network) != 0 || len(placementRequest.PCI) != 0 || len(placementRequest.Storage) != expectedStorage || placementRequest.Storage[0].VolumeID != volume.VolumeID || (withData && placementRequest.Storage[1].VolumeID != dataVolume.VolumeID) {
		t.Fatalf("compiled placement=%+v err=%v", placementRequest, err)
	}
	dry, err := DryEvaluateAvailabilityPlacementScope(ctx, pool, placementRequest)
	if err != nil || dry.Status != "READY" || len(dry.Candidates) != 1 {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	admission, err := FinalAdmitAvailabilityPlacementScope(ctx, pool, dry, placementRequest, dry.Candidates[0])
	if err != nil || admission.HostID != host {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}
	if _, err = BindVMAggregateAdmission(ctx, pool, claim, admission.AdmissionID); err != nil {
		t.Fatal(err)
	}

	claim, err = ClaimVMAggregateLifecycle(ctx, pool, aggregate.OperationID, "vm-aggregate-worker-materialization", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := PrepareVMAggregateMaterialization(ctx, pool, claim)
	if err != nil || decision.HostID != host {
		t.Fatalf("materialization=%+v err=%v", decision, err)
	}
	defineCommand := "vm-define-command:" + aggregate.OperationID + ":1"
	defineVerification := "vm-aggregate-define-verification-" + suffix
	defineEvidence := map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "plan_digest": decision.PlanDigest, "domain_present": true, "domain_identity_matches": true, "plan_identity_matches": true, "compute_shape_matches": true, "root_volume_identity_matches": true, "image_materialization_state": "PENDING", "network_realization_state": "PENDING"}
	defineAttempt := acceptEvacuationCommand(t, ctx, pool, host, defineCommand, defineVerification, 1, defineEvidence, "SUCCEEDED")
	if err = AcceptVMDefinitionObservation(ctx, pool, VMDefinitionObservation{EvidenceID: "vm-aggregate-define-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: decision.PlanID, PlanDigest: decision.PlanDigest, HostID: host, CommandID: defineCommand, AttemptIndex: uint32(defineAttempt), VerificationID: defineVerification, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(defineCommand + "/observation")), VerifierDigest: digestBytes([]byte(defineCommand + "/verifier")), EvidenceState: "MATCHED", DomainPresent: true, DomainIdentityMatches: true, PlanIdentityMatches: true, ComputeShapeMatches: true, RootVolumeIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}
	imageRequest := VMImageMaterializationRequest{VMID: vmID, PlanID: decision.PlanID, JobID: "vm-aggregate-image-job-" + suffix, CommandID: "vm-aggregate-image-command-" + suffix}
	if _, err = PrepareVMImageMaterialization(ctx, pool, imageRequest); err != nil {
		t.Fatal(err)
	}
	var resourceKey string
	if err = pool.QueryRow(ctx, `SELECT backend_resource_key FROM kim.volume_backend_binding_intents WHERE binding_id=$1`, volume.BindingID).Scan(&resourceKey); err != nil {
		t.Fatal(err)
	}
	imageVerification := "vm-aggregate-image-verification-" + suffix
	imageEvidence := map[string]any{"domain_uuid": vmID, "materialization_generation": float64(1), "image_id": imageID, "image_revision": float64(1), "expected_content_digest": imageDigest, "observed_content_digest": imageDigest, "image_size_bytes": float64(4096), "volume_id": volume.VolumeID, "observed_vg_uuid": vgUUID, "observed_lv_uuid": volume.LVUUID, "backend_resource_key": resourceKey, "holder_open": false, "content_identity_matches": true}
	imageAttempt := acceptEvacuationCommand(t, ctx, pool, host, imageRequest.CommandID, imageVerification, 1, imageEvidence, "SUCCEEDED")
	if err = AcceptVMImageRealizationObservation(ctx, pool, VMImageRealizationObservation{EvidenceID: "vm-aggregate-image-evidence-" + suffix, VMID: vmID, VMGeneration: 1, PlanID: decision.PlanID, PlanDigest: decision.PlanDigest, HostID: host, ImageID: imageID, ImageRevision: 1, ExpectedDigest: imageDigest, ObservedDigest: imageDigest, ImageSizeBytes: 4096, VolumeID: volume.VolumeID, BindingID: volume.BindingID, BindingGeneration: volume.BindingGeneration, VGUUID: vgUUID, LVUUID: volume.LVUUID, BackendResourceKey: resourceKey, CommandID: imageRequest.CommandID, AttemptIndex: uint32(imageAttempt), VerificationID: imageVerification, ObservationGeneration: 1, ObservationDigest: digestBytes([]byte(imageRequest.CommandID + "/observation")), VerifierDigest: digestBytes([]byte(imageRequest.CommandID + "/verifier")), EvidenceState: "MATCHED", ContentIdentityMatches: true}); err != nil {
		t.Fatal(err)
	}

	claim, err = ClaimVMAggregateLifecycle(ctx, pool, aggregate.OperationID, "vm-aggregate-worker-verification", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EvaluateVMAggregateEvidence(ctx, pool, claim, "vm-aggregate-pre-power-verification-"+suffix); !errors.Is(err, ErrVMAggregateConflict) {
		t.Fatalf("missing RUNNING read-back accepted: %v", err)
	}
	powerCommand := "vm-aggregate-power-command-" + suffix
	if err = AuthorizeZeroPortVMPowerOn(ctx, pool, vmID, 1, host, "vm-aggregate-power-job-"+suffix, powerCommand); err != nil {
		t.Fatal(err)
	}
	acceptEvacuationLostReadBack(t, ctx, pool, host, powerCommand, "vm-aggregate-power-verification-"+suffix, 1, map[string]any{"domain_uuid": vmID, "desired_state": "RUNNING", "observed_state": "RUNNING", "source": "libvirt_domain_state"})
	if withData {
		partialRollback := errors.New("rollback missing DATA materialization")
		err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM kim.volume_materializations_current WHERE volume_id=$1`, dataVolume.VolumeID); err != nil {
				return err
			}
			if _, err := EvaluateVMAggregateEvidence(ctx, scopeTxBeginner{tx}, claim, "vm-aggregate-missing-data-verification-"+suffix); !errors.Is(err, ErrVMAggregateConflict) {
				return fmt.Errorf("missing DATA materialization accepted: %v", err)
			}
			return partialRollback
		})
		if !errors.Is(err, partialRollback) {
			t.Fatal(err)
		}
	}
	verificationID := "vm-aggregate-verification-" + suffix
	verification, err := EvaluateVMAggregateEvidence(ctx, pool, claim, verificationID)
	if err != nil || verification.VerificationState != "VERIFIED" {
		t.Fatalf("verification=%+v err=%v", verification, err)
	}
	if replay, err := EvaluateVMAggregateEvidence(ctx, pool, claim, verificationID); err != nil || replay.VerificationDigest != verification.VerificationDigest {
		t.Fatalf("verification replay=%+v err=%v", replay, err)
	}
	terminalID := "vm-aggregate-terminal-" + suffix
	driftRollback := errors.New("rollback terminal drift branch")
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if withData {
			if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE binding_id=$1`, dataVolume.BindingID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET observation_generation=observation_generation+1 WHERE vm_id=$1`, vmID); err != nil {
			return err
		}
		if _, err := CompleteVMAggregateLifecycle(ctx, scopeTxBeginner{tx}, claim, verificationID, terminalID+"-drift"); !errors.Is(err, ErrVMAggregateConflict) {
			return fmt.Errorf("terminal readiness drift accepted: %v", err)
		}
		return driftRollback
	})
	if !errors.Is(err, driftRollback) {
		t.Fatal(err)
	}
	if terminal, err := CompleteVMAggregateLifecycle(ctx, pool, claim, verificationID, terminalID); err != nil || terminal != terminalID {
		t.Fatalf("terminal=%s err=%v", terminal, err)
	}
	if terminal, err := CompleteVMAggregateLifecycle(ctx, pool, claim, verificationID, terminalID); err != nil || terminal != terminalID {
		t.Fatalf("terminal replay=%s err=%v", terminal, err)
	}
	aggregate, err = GetVMAggregate(ctx, pool, vmID)
	if err != nil || aggregate.LifecycleState != "ACTIVE" || aggregate.ConvergenceState != "CONVERGED" || aggregate.OperationState != "VERIFIED" {
		t.Fatalf("terminal aggregate=%+v err=%v", aggregate, err)
	}
	if withData {
		var bindings, verifications int
		if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM kim.vm_aggregate_volume_binding_evidence WHERE operation_id=$1),(SELECT count(*) FROM kim.vm_aggregate_storage_volume_verification_evidence WHERE verification_id=$2)`, aggregate.OperationID, verificationID).Scan(&bindings, &verifications); err != nil || bindings != 2 || verifications != 2 {
			t.Fatalf("Volume evidence cardinality=%d/%d err=%v", bindings, verifications, err)
		}
		updates := map[string]string{"vm_dependency_volume_evidence": "desired_digest=desired_digest", "vm_aggregate_volume_binding_evidence": "binding_digest=binding_digest", "vm_aggregate_storage_volume_verification_evidence": "verification_digest=verification_digest"}
		for table, assignment := range updates {
			if _, err = pool.Exec(ctx, fmt.Sprintf(`UPDATE kim.%s SET %s WHERE true`, table, assignment)); err == nil {
				t.Fatalf("immutable %s accepted UPDATE", table)
			}
		}
	}
	var runtimeHost, runtimeAdmission, runtimePlan string
	if err = pool.QueryRow(ctx, `SELECT host_id,admission_id,plan_id FROM kim.vm_resource_runtime_bindings_current WHERE vm_id=$1`, vmID).Scan(&runtimeHost, &runtimeAdmission, &runtimePlan); err != nil || runtimeHost != host || runtimeAdmission != admission.AdmissionID || runtimePlan != decision.PlanID {
		t.Fatalf("runtime=%s/%s/%s err=%v", runtimeHost, runtimeAdmission, runtimePlan, err)
	}
	for _, table := range []string{"vm_dependency_snapshot_evidence", "vm_runtime_intent_evidence", "vm_aggregate_verification_evidence", "vm_aggregate_terminal_evidence"} {
		if _, err = pool.Exec(ctx, `UPDATE kim.`+table+` SET recorded_at=recorded_at`); err == nil {
			t.Fatalf("immutable UPDATE succeeded: %s", table)
		}
	}
	if after != nil {
		after(t, ctx, pool, suffix, vmID, host, aggregate)
	}
}
