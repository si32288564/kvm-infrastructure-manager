package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
	vmapi "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/vm"
)

func TestNorthboundVMContractPostgreSQLIntegration(t *testing.T) {
	testVMAggregateVolumeProfilePostgreSQLIntegrationAfter(t, false, qualifyNorthboundVMContract)
}

func qualifyNorthboundVMContract(t *testing.T, ctx context.Context, db recoveryQualificationDB, suffix, vmID, _ string, aggregate VMAggregate) {
	t.Helper()
	p := resource.Principal{Issuer: "northbound-vm", Subject: "automation-" + suffix, Type: "AUTOMATION"}
	if _, err := db.Exec(ctx, `INSERT INTO kim.northbound_role_bindings_current(binding_id,principal_issuer,principal_subject,principal_type,scope_type,scope_id,role,lifecycle_state,binding_revision) VALUES($1,$2,$3,$4,'SYSTEM','','ADMIN','ACTIVE',1)`, `northbound-vm-admin-`+suffix, p.Issuer, p.Subject, p.Type); err != nil {
		t.Fatal(err)
	}
	store := NorthboundVMStore{DB: db}
	read, err := store.Get(ctx, p, vmID, "northbound-vm-read-"+suffix)
	if err != nil || read.ID != vmID || read.Revision != aggregate.VMRevision || read.RuntimeIntentGeneration != aggregate.RuntimeIntentGeneration || len(read.Ports) != 0 || len(read.DataVolumes) != 0 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	page, err := store.List(ctx, p, vmapi.ListRequest{ProjectID: read.ProjectID, Limit: 10}, "northbound-vm-list-"+suffix)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	name := "northbound-renamed-" + suffix
	updated, err := store.Patch(ctx, p, vmID, read.Revision, vmapi.Patch{Name: &name}, "northbound-vm-update-"+suffix, "northbound-vm-update-evidence-"+suffix, "")
	if err != nil || updated.Name != name || updated.Revision != read.Revision+1 || updated.RuntimeIntentGeneration != read.RuntimeIntentGeneration {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	if _, err = store.Patch(ctx, p, vmID, read.Revision, vmapi.Patch{Name: &name}, "northbound-vm-stale-"+suffix, "northbound-vm-stale-evidence-"+suffix, ""); !errors.Is(err, resource.ErrStaleRevision) {
		t.Fatalf("stale err=%v", err)
	}
	if _, err = store.Delete(ctx, p, vmID, updated.Revision, "northbound-vm-running-delete-"+suffix, "northbound-vm-running-delete-operation-"+suffix); !errors.Is(err, resource.ErrConflict) {
		t.Fatalf("RUNNING delete err=%v", err)
	}

	var storageClass string
	if err = db.QueryRow(ctx, `SELECT r.storage_class_id FROM kim.volume_resource_revision_evidence r WHERE r.volume_id=$1 AND r.volume_revision=$2`, read.RootVolume.ID, read.RootVolume.Revision).Scan(&storageClass); err != nil {
		t.Fatal(err)
	}
	newRoot, err := CreateVolumeResource(ctx, db, VolumeResourceRequest{VolumeID: "northbound-vm-root-" + suffix, ProjectID: read.ProjectID, Name: "northbound-root", StorageClassID: storageClass, StorageClassRevision: 1, SizeBytes: 16 << 20, Bootable: true, SourceType: "BLANK"})
	if err != nil {
		t.Fatal(err)
	}
	newRoot, err = AllocateVolumeCapacityAutomatically(ctx, db, newRoot.VolumeID, newRoot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	var vgUUID string
	if err = db.QueryRow(ctx, `SELECT vg_uuid FROM kim.volume_materialization_operation_evidence WHERE operation_id=$1`, newRoot.OperationID).Scan(&vgUUID); err != nil {
		t.Fatal(err)
	}
	client := &volumeResourceLVMClient{vgUUID: vgUUID}
	mutation := locallvm.Backend{Client: client, VolumeGroups: map[string]string{vgUUID: "kim_test_vg"}}
	readOnly := locallvm.ReadBackBackend{Backend: mutation}
	claim, err := ClaimVolumeMaterialization(ctx, db, newRoot.OperationID, "northbound-volume-worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := AuthorizeVolumeMaterializationCommand(ctx, db, claim, "northbound-volume-apply-job-"+suffix, "northbound-volume-apply-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, db, apply.CommandID, 1, CommandLeaseScopeMutation, mutation)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, db, claim); err != nil {
		t.Fatal(err)
	}
	successor, err := ClaimVolumeMaterialization(ctx, db, newRoot.OperationID, "northbound-volume-worker-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	readCommand, err := AuthorizeVolumeMaterializationCommand(ctx, db, successor, "northbound-volume-read-job-"+suffix, "northbound-volume-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	verification := observeVolumeBackendAfterLostResponse(t, ctx, db, readCommand.CommandID, "northbound-volume-verification-"+suffix, 1, readOnly)
	if _, err = CompleteVolumeMaterialization(ctx, db, successor, CompleteVolumeMaterializationRequest{OperationID: successor.OperationID, OperationGeneration: successor.OperationGeneration, ClaimGeneration: successor.ClaimGeneration, ObservationID: "northbound-volume-observation-" + suffix, VerificationID: verification}); err != nil {
		t.Fatal(err)
	}

	createDesired := read.Desired
	createDesired.Name = "northbound-created-" + suffix
	createDesired.RootVolume = vmapi.Reference{ID: newRoot.VolumeID, Revision: newRoot.Revision}
	digest, err := vmapi.DesiredDigest(createDesired)
	if err != nil {
		t.Fatal(err)
	}
	create := vmapi.CreateRequest{Desired: createDesired, IdempotencyKey: "stable-client-reference", RequestID: "northbound-vm-create-request-" + suffix, CanonicalPath: "/api/v1/vms"}
	tail := suffix
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	createdID := "88000000-0000-4000-8000-" + tail
	created, replay, err := store.Create(ctx, p, create, createdID, "northbound-vm-create-operation-"+suffix, digest)
	if err != nil || replay || created.ID != createdID || created.Revision != 1 || created.ConvergenceState != "PENDING" {
		t.Fatalf("created=%+v replay=%v err=%v", created, replay, err)
	}
	replayed, replay, err := store.Create(ctx, p, create, "88000000-0000-4000-8000-999999999999", "unused-operation", digest)
	if err != nil || !replay || replayed.ID != created.ID || replayed.OperationID != created.OperationID {
		t.Fatalf("replayed=%+v replay=%v err=%v", replayed, replay, err)
	}
	conflict := create
	conflict.Desired.Name += "-different"
	conflictDigest, _ := vmapi.DesiredDigest(conflict.Desired)
	if _, _, err = store.Create(ctx, p, conflict, "88000000-0000-4000-8000-999999999998", "unused-operation-2", conflictDigest); !errors.Is(err, resource.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict err=%v", err)
	}
	if _, err = db.Exec(ctx, `UPDATE kim.northbound_vm_idempotency_evidence SET response_status=202 WHERE vm_id=$1`, created.ID); err == nil {
		t.Fatal("northbound VM replay evidence accepted UPDATE")
	}
}
