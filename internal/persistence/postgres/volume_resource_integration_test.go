package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	agentexecution "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

type volumeResourceLVMClient struct {
	vgUUID  string
	lvUUID  string
	volume  locallvm.LogicalVolume
	present bool
}

func (c *volumeResourceLVMClient) VerifyVolumeGroup(_ context.Context, _ string, uuid string) error {
	if uuid != c.vgUUID {
		return errors.New("wrong VG UUID")
	}
	return nil
}
func (c *volumeResourceLVMClient) LogicalVolume(_ context.Context, _, _ string) (locallvm.LogicalVolume, bool, error) {
	return c.volume, c.present, nil
}
func (c *volumeResourceLVMClient) CreateLogicalVolume(_ context.Context, _, name string, size uint64) error {
	if c.present {
		return errors.New("already present")
	}
	lvUUID := c.lvUUID
	if lvUUID == "" {
		lvUUID = "lv-volume-resource"
	}
	c.volume = locallvm.LogicalVolume{VGUUID: c.vgUUID, LVUUID: lvUUID, Name: name, SizeBytes: size * locallvm.MiB}
	c.present = true
	return nil
}
func (c *volumeResourceLVMClient) RemoveLogicalVolume(_ context.Context, _, _ string) error {
	c.present = false
	return nil
}

func TestVolumeResourceAuthorityPostgreSQL(t *testing.T) {
	url := os.Getenv("KIM_POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, url, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode) VALUES('volume-resource',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	host := "volume-host-" + suffix
	fingerprint := digestBytes([]byte(host + "-cert"))
	prepareSessionIdentityFixture(t, ctx, pool, host, 1, fingerprint)
	if _, err = AdmitAgentSession(ctx, pool, AgentSessionAdmission{SessionAttemptID: host + "-attempt", HostID: host, ConnectionInstanceID: "connection", TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")), CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint, ExpectedSessionGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	acceptPlacementInventory(t, ctx, pool, host)
	if err = UpdateHostReadinessGate(ctx, pool, HostReadinessGate{HostID: host, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"}); err != nil {
		t.Fatal(err)
	}
	authority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: host, PolicyID: "volume-resource", PolicyGeneration: 1, ActorID: "integration", ReasonCode: "volume_resource_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	backendID, vg, class := "volume-backend-"+suffix, "volume-vg-"+suffix, "volume-class-"+suffix
	if err = RegisterLocalLVMFoundation(ctx, pool, LocalLVMFoundation{BackendID: backendID, HostID: host, VGUUID: vg, BackendState: "ACTIVE", CapabilityState: "CURRENT", SupportTier: "VALIDATED", BackendGeneration: 1, HostCapabilityGeneration: 1, StorageClassID: class, ClassState: "ACTIVE", StorageClassRevision: 1, FencingPolicyRevision: 1, CapacityObservationID: "volume-capacity-observation-" + suffix, CapacityState: "CURRENT", HealthState: "HEALTHY", CapacityGeneration: 1, TotalBytes: 64 << 20, ObservedFreeBytes: 64 << 20, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	imageID, imageDigest := "volume-image-"+suffix, digestBytes([]byte("volume-image-v1"))
	if _, err = pool.Exec(ctx, `INSERT INTO kim.northbound_image_revision_evidence(image_id,image_revision,owner_project_id,image_name,architecture,image_format,expected_digest,source_id,visibility,delete_protection,desired_digest) VALUES($1,1,'project','volume-image','X86_64','RAW',$2,$3,'PRIVATE',false,$4)`, imageID, imageDigest, "volume-source-"+suffix, digestBytes([]byte("volume-image-desired-v1"))); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO kim.northbound_images_current(image_id,image_revision,owner_project_id,lifecycle_state,verification_state,verified_digest,verified_size_bytes,authority_generation) VALUES($1,1,'project','ACTIVE','VERIFIED',$2,16777216,1)`, imageID, imageDigest); err != nil {
		t.Fatal(err)
	}
	imageDesired := VolumeResourceRequest{VolumeID: "volume-image-backed-" + suffix, ProjectID: "project", Name: "image-backed", StorageClassID: class, StorageClassRevision: 1, SizeBytes: 16 << 20, Bootable: true, SourceType: "IMAGE", SourceImageID: imageID, SourceImageRevision: 1, SourceArtifactDigest: imageDigest}
	imageVolume, err := CreateVolumeResource(ctx, pool, imageDesired)
	if err != nil || imageVolume.SourceImageRevision != 1 || imageVolume.SourceArtifactDigest != imageDigest {
		t.Fatalf("exact Image-backed Volume = %+v/%v", imageVolume, err)
	}
	wrongImage := imageDesired
	wrongImage.VolumeID, wrongImage.Name, wrongImage.SourceArtifactDigest = "volume-wrong-image-"+suffix, "wrong-image", digestBytes([]byte("wrong"))
	if _, err = CreateVolumeResource(ctx, pool, wrongImage); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("wrong Image digest error = %v", err)
	}
	imageDigest2 := digestBytes([]byte("volume-image-v2"))
	if _, err = pool.Exec(ctx, `INSERT INTO kim.northbound_image_revision_evidence(image_id,image_revision,owner_project_id,image_name,architecture,image_format,expected_digest,source_id,visibility,delete_protection,desired_digest) VALUES($1,2,'project','volume-image-v2','X86_64','RAW',$2,$3,'PRIVATE',false,$4)`, imageID, imageDigest2, "volume-source-v2-"+suffix, digestBytes([]byte("volume-image-desired-v2"))); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE kim.northbound_images_current SET image_revision=2,verified_digest=$2,verified_size_bytes=16777216,authority_generation=2,updated_at=statement_timestamp() WHERE image_id=$1`, imageID, imageDigest2); err != nil {
		t.Fatal(err)
	}
	if imageVolume, err = GetVolumeResource(ctx, pool, imageDesired.VolumeID); err != nil || imageVolume.SourceImageRevision != 1 || imageVolume.SourceArtifactDigest != imageDigest {
		t.Fatalf("Image revision no-retrofit = %+v/%v", imageVolume, err)
	}
	staleImage := imageDesired
	staleImage.VolumeID, staleImage.Name = "volume-stale-image-"+suffix, "stale-image"
	if _, err = CreateVolumeResource(ctx, pool, staleImage); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("stale Image revision error = %v", err)
	}

	desired := VolumeResourceRequest{VolumeID: "volume-resource-" + suffix, ProjectID: "project", Name: "persistent-root", StorageClassID: class, StorageClassRevision: 1, SizeBytes: 16 << 20, Bootable: true, SourceType: "BLANK"}
	volume, err := CreateVolumeResource(ctx, pool, desired)
	if err != nil || volume.Revision != 1 || volume.MaterializationState != "NONE" || volume.AttachmentState != "UNATTACHED" {
		t.Fatalf("create unattached Volume=%+v/%v", volume, err)
	}
	if replay, err := CreateVolumeResource(ctx, pool, desired); err != nil || replay.Revision != 1 {
		t.Fatalf("create replay=%+v/%v", replay, err)
	}
	desired.Name, desired.DeleteProtection = "renamed-root", true
	volume, err = UpdateVolumeResource(ctx, pool, desired, 1)
	if err != nil || volume.Revision != 2 || !volume.DeleteProtection {
		t.Fatalf("metadata revision=%+v/%v", volume, err)
	}
	bad := desired
	bad.SizeBytes += 1 << 20
	if _, err = UpdateVolumeResource(ctx, pool, bad, 2); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("unsafe resize=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE kim.volume_resource_revision_evidence SET volume_name='forged' WHERE volume_id=$1 AND volume_revision=1`, desired.VolumeID); err == nil {
		t.Fatal("immutable revision UPDATE succeeded")
	}
	desired.DeleteProtection = false
	volume, err = UpdateVolumeResource(ctx, pool, desired, 2)
	if err != nil || volume.Revision != 3 {
		t.Fatalf("clear protection=%+v/%v", volume, err)
	}
	allocation := VolumeCapacityAllocationRequest{VolumeID: desired.VolumeID, BackendID: backendID, ExpectedVolumeRevision: 3, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1}
	volume, err = AllocateVolumeCapacity(ctx, pool, allocation)
	if err != nil || volume.AllocationID == "" || volume.MaterializationState != "PENDING" {
		t.Fatalf("allocate=%+v/%v", volume, err)
	}
	if replay, err := AllocateVolumeCapacity(ctx, pool, allocation); err != nil || replay.AllocationID != volume.AllocationID {
		t.Fatalf("allocation replay=%+v/%v", replay, err)
	}
	large := desired
	large.VolumeID, large.Name, large.SizeBytes = "volume-large-"+suffix, "large", 60<<20
	if _, err = CreateVolumeResource(ctx, pool, large); err != nil {
		t.Fatal(err)
	}
	if _, err = AllocateVolumeCapacity(ctx, pool, VolumeCapacityAllocationRequest{VolumeID: large.VolumeID, BackendID: backendID, ExpectedVolumeRevision: 1, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1}); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("insufficient capacity=%v", err)
	}

	client := &volumeResourceLVMClient{vgUUID: vg}
	vgMap := map[string]string{vg: "kim_test_vg"}
	mutation := locallvm.Backend{Client: client, VolumeGroups: vgMap}
	readOnly := locallvm.ReadBackBackend{Backend: mutation}
	deletion := locallvm.DeleteBackend{Client: client, VolumeGroups: vgMap}
	deleteReadOnly := locallvm.DeleteBackend{Client: client, VolumeGroups: vgMap, ReadBackOnly: true}
	first, err := ClaimVolumeMaterialization(ctx, pool, volume.OperationID, "worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := AuthorizeVolumeMaterializationCommand(ctx, pool, first, "volume-apply-job-"+suffix, "volume-apply-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, pool, apply.CommandID, authority.AuthorityGeneration, CommandLeaseScopeMutation, mutation)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, pool, first); err != nil {
		t.Fatal(err)
	}
	successor, err := ClaimVolumeMaterialization(ctx, pool, volume.OperationID, "worker-b", time.Minute)
	if err != nil || successor.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("read-back claim=%+v/%v", successor, err)
	}
	read, err := AuthorizeVolumeMaterializationCommand(ctx, pool, successor, "volume-read-job-"+suffix, "volume-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	verification := observeVolumeBackendAfterLostResponse(t, ctx, pool, read.CommandID, "volume-verification-"+suffix, authority.AuthorityGeneration, readOnly)
	complete := CompleteVolumeMaterializationRequest{OperationID: successor.OperationID, OperationGeneration: 1, ClaimGeneration: successor.ClaimGeneration, ObservationID: "volume-observation-" + suffix, VerificationID: verification}
	terminal, err := CompleteVolumeMaterialization(ctx, pool, successor, complete)
	if err != nil || terminal == "" {
		t.Fatalf("terminal=%s/%v", terminal, err)
	}
	if replay, err := CompleteVolumeMaterialization(ctx, pool, successor, complete); err != nil || replay != terminal {
		t.Fatalf("terminal replay=%s/%v", replay, err)
	}
	volume, err = GetVolumeResource(ctx, pool, desired.VolumeID)
	if err != nil || volume.Lifecycle != "AVAILABLE" || volume.MaterializationState != "VERIFIED" || volume.LVUUID != "lv-volume-resource" || volume.Revision != 3 {
		t.Fatalf("verified Volume=%+v/%v", volume, err)
	}
	var response string
	if err = pool.QueryRow(ctx, `SELECT response_state FROM kim.volume_materialization_operations_current WHERE operation_id=$1`, successor.OperationID).Scan(&response); err != nil || response != "LOST" {
		t.Fatalf("response=%s/%v", response, err)
	}

	attachment := VolumeAttachmentIntentRequest{VolumeID: desired.VolumeID, AttachmentIntentID: "volume-intent-" + suffix, AttachmentID: "volume-attachment-" + suffix, WorkloadID: "volume-workload-" + suffix, ExpectedVolumeRevision: 3}
	if volume, err = RequestVolumeAttachment(ctx, pool, attachment); err != nil || volume.AttachmentState != "REQUESTED" {
		t.Fatalf("attachment=%+v/%v", volume, err)
	}
	resourceStorage := placement.StorageRequirement{
		VolumeID: desired.VolumeID, VolumeRevision: 3,
		AttachmentID: attachment.AttachmentID, AttachmentGeneration: 1,
		AttachmentIntentID: attachment.AttachmentIntentID, AttachmentIntentGeneration: 1,
		CapacityAllocationID: volume.AllocationID, CapacityAllocationGeneration: volume.AllocationGeneration,
		BackendID: backendID, BackendGeneration: 1, VGUUID: vg,
		StorageClassID: class, StorageClassRevision: 1, CapacityGeneration: 1,
		FencingPolicyRevision: 1, SizeBytes: desired.SizeBytes,
		AccessMode: "SINGLE_WRITER", Bootable: true,
	}
	storageAuthority, found, err := loadStorageAuthority(ctx, pool, "project", attachment.WorkloadID, host, resourceStorage)
	if err != nil || !found || !storageAuthority.ResourceConsumerReady || storageAuthority.ClaimedBytes != 0 {
		t.Fatalf("standalone Volume Placement authority=%+v found=%v err=%v", storageAuthority, found, err)
	}
	for name, mutate := range map[string]func(*placement.StorageRequirement){
		"revision":              func(r *placement.StorageRequirement) { r.VolumeRevision++ },
		"allocation generation": func(r *placement.StorageRequirement) { r.CapacityAllocationGeneration++ },
		"wrong backend":         func(r *placement.StorageRequirement) { r.BackendID += "-wrong" },
		"wrong attachment":      func(r *placement.StorageRequirement) { r.AttachmentIntentID += "-wrong" },
		"wrong workload":        func(r *placement.StorageRequirement) {},
	} {
		t.Run("placement rejects "+name, func(t *testing.T) {
			candidate := resourceStorage
			mutate(&candidate)
			workload := attachment.WorkloadID
			if name == "wrong workload" {
				workload += "-wrong"
			}
			authority, _, err := loadStorageAuthority(ctx, pool, "project", workload, host, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if authority.ResourceConsumerReady {
				t.Fatalf("mismatched %s was accepted", name)
			}
		})
	}
	for name, update := range map[string]string{
		"materialization not VERIFIED": `UPDATE kim.volume_materializations_current SET materialization_state='UNKNOWN' WHERE volume_id=$1`,
		"binding incarnation stale":    `UPDATE kim.volume_backend_bindings_current SET binding_state='STALE' WHERE volume_id=$1`,
		"capacity RELEASE_PENDING":     `UPDATE kim.storage_capacity_claims SET claim_state='RELEASE_PENDING' WHERE volume_id=$1`,
	} {
		t.Run("placement rejects "+name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, update, desired.VolumeID); err != nil {
				t.Fatal(err)
			}
			authority, _, err := loadStorageAuthority(ctx, tx, "project", attachment.WorkloadID, host, resourceStorage)
			if err != nil {
				t.Fatal(err)
			}
			if authority.ResourceConsumerReady {
				t.Fatalf("drift %s was accepted", name)
			}
		})
	}
	placementPoolID := "volume-placement-pool-" + suffix
	placementImageID := "volume-placement-image-" + suffix
	placementFlavorID := "volume-placement-flavor-" + suffix
	if err = UpsertPlacementPool(ctx, pool, PlacementPoolBinding{PoolID: placementPoolID, PoolGeneration: 1, LifecycleState: "ACTIVE", PolicyID: "volume-placement", PolicyGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if err = AssignHostPlacementPool(ctx, pool, HostPlacementMembership{HostID: host, PoolID: placementPoolID, Generation: 1, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	placementImageDigest := digestBytes([]byte("volume-placement-image"))
	if _, err = RegisterImageRevision(ctx, pool, ImageRevision{ImageID: placementImageID, Revision: 1, OwnerProjectID: "project", Format: "RAW", SizeBytes: 4096, DeclaredChecksum: placementImageDigest, ObservedChecksum: placementImageDigest, SignatureState: "VERIFIED", SignatureDigest: digestBytes([]byte("volume-placement-signature")), SourceURI: "https://images.invalid/volume-placement.raw", Visibility: "PRIVATE"}); err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterFlavorRevision(ctx, pool, FlavorRevision{FlavorID: placementFlavorID, Revision: 1, OwnerProjectID: "project", Name: "volume-placement", VCPUs: 1, MemoryMiB: 256, RootDiskGiB: 1, NUMAPolicy: "NONE", CPUAllocation: "SHARED"}); err != nil {
		t.Fatal(err)
	}
	placementRequest := PlacementAdmissionRequest{
		RequestID: "volume-placement-request-" + suffix, ProjectID: "project",
		WorkloadID: attachment.WorkloadID, ImageID: placementImageID,
		FlavorID: placementFlavorID, PoolID: placementPoolID,
		Storage: []placement.StorageRequirement{resourceStorage},
	}
	dry, err := DryEvaluatePlacement(ctx, pool, placementRequest, host)
	if err != nil || !dry.Eligible {
		t.Fatalf("standalone Volume dry Placement=%+v/%v", dry, err)
	}
	placementTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := FinalAdmitPlacement(ctx, nestedTestTxBeginner{placementTx}, placementRequest, dry)
	if err != nil {
		placementTx.Rollback(ctx)
		t.Fatalf("standalone Volume Final Admission=%v", err)
	}
	var attachedState, claimStateInAdmission string
	var reservedBytes uint64
	if err = placementTx.QueryRow(ctx, `SELECT attachment.intent_state,claim.claim_state,claim.reserved_bytes
		FROM kim.volume_attachment_intents_current attachment
		JOIN kim.storage_capacity_claims claim ON claim.volume_id=attachment.volume_id
		WHERE attachment.volume_id=$1 AND attachment.attachment_intent_id=$2
		  AND claim.placement_admission_id=$3`, desired.VolumeID,
		attachment.AttachmentIntentID+":attached:"+admission.AdmissionID,
		admission.AdmissionID).Scan(&attachedState, &claimStateInAdmission, &reservedBytes); err != nil {
		placementTx.Rollback(ctx)
		t.Fatal(err)
	}
	if attachedState != "ATTACHED" || claimStateInAdmission != "ALLOCATED" || reservedBytes != desired.SizeBytes {
		placementTx.Rollback(ctx)
		t.Fatalf("Final Admission consumed authority=%s/%s/%d", attachedState, claimStateInAdmission, reservedBytes)
	}
	if err = placementTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	staleTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = staleTx.Exec(ctx, `UPDATE kim.volume_attachment_intents_current SET attachment_generation=attachment_generation+1 WHERE volume_id=$1`, desired.VolumeID); err != nil {
		staleTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = FinalAdmitPlacement(ctx, nestedTestTxBeginner{staleTx}, placementRequest, dry); !errors.Is(err, ErrPlacementIneligible) && !errors.Is(err, ErrPlacementStale) {
		staleTx.Rollback(ctx)
		t.Fatalf("stale Admission replay error=%v", err)
	}
	if err = staleTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = RequestVolumeRetirement(ctx, pool, desired.VolumeID, 3); !errors.Is(err, ErrPlacementConflict) {
		t.Fatalf("attachment did not block delete=%v", err)
	}
	if _, err = CancelVolumeAttachmentIntent(ctx, pool, desired.VolumeID, attachment.AttachmentIntentID, 3); err != nil {
		t.Fatal(err)
	}
	retiring, err := RequestVolumeRetirement(ctx, pool, desired.VolumeID, 3)
	if err != nil || retiring.Lifecycle != "RETIRE_PENDING" || retiring.MaterializationGeneration != 2 {
		t.Fatalf("retire=%+v/%v", retiring, err)
	}
	deleteFirst, err := ClaimVolumeMaterialization(ctx, pool, retiring.OperationID, "delete-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	deleteApply, err := AuthorizeVolumeMaterializationCommand(ctx, pool, deleteFirst, "delete-job-"+suffix, "delete-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, pool, deleteApply.CommandID, authority.AuthorityGeneration, CommandLeaseScopeMutation, deletion)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, pool, deleteFirst); err != nil {
		t.Fatal(err)
	}
	deleteSuccessor, err := ClaimVolumeMaterialization(ctx, pool, retiring.OperationID, "delete-b", time.Minute)
	if err != nil || deleteSuccessor.ClaimMode != "READ_BACK_FIRST" {
		t.Fatalf("delete read-back=%+v/%v", deleteSuccessor, err)
	}
	deleteRead, err := AuthorizeVolumeMaterializationCommand(ctx, pool, deleteSuccessor, "delete-read-job-"+suffix, "delete-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	deleteVerification := observeVolumeBackendAfterLostResponse(t, ctx, pool, deleteRead.CommandID, "delete-verification-"+suffix, authority.AuthorityGeneration, deleteReadOnly)
	deleteTerminal, err := CompleteVolumeMaterialization(ctx, pool, deleteSuccessor, CompleteVolumeMaterializationRequest{OperationID: deleteSuccessor.OperationID, OperationGeneration: 1, ClaimGeneration: deleteSuccessor.ClaimGeneration, ObservationID: "delete-observation-" + suffix, VerificationID: deleteVerification})
	if err != nil || deleteTerminal == "" {
		t.Fatalf("delete terminal=%s/%v", deleteTerminal, err)
	}
	volume, err = GetVolumeResource(ctx, pool, desired.VolumeID)
	if err != nil || volume.Lifecycle != "DELETED" || volume.MaterializationState != "ABSENT" || volume.Revision != 3 {
		t.Fatalf("tombstone=%+v/%v", volume, err)
	}
	var claimState string
	var releases int
	if err = pool.QueryRow(ctx, `SELECT c.claim_state,(SELECT count(*) FROM kim.volume_capacity_release_evidence r WHERE r.volume_id=c.volume_id) FROM kim.storage_capacity_claims c WHERE c.volume_id=$1 AND c.authority_source='VOLUME_RESOURCE'`, desired.VolumeID).Scan(&claimState, &releases); err != nil || claimState != "RELEASED" || releases != 1 {
		t.Fatalf("release=%s/%d/%v", claimState, releases, err)
	}

	concurrentDesired := VolumeResourceRequest{VolumeID: "volume-concurrent-" + suffix, ProjectID: "project", Name: "concurrent-root", StorageClassID: class, StorageClassRevision: 1, SizeBytes: 16 << 20, Bootable: true, SourceType: "BLANK"}
	concurrentVolume, err := CreateVolumeResource(ctx, pool, concurrentDesired)
	if err != nil {
		t.Fatal(err)
	}
	concurrentVolume, err = AllocateVolumeCapacity(ctx, pool, VolumeCapacityAllocationRequest{VolumeID: concurrentDesired.VolumeID, BackendID: backendID, ExpectedVolumeRevision: concurrentVolume.Revision, ExpectedBackendGeneration: 1, ExpectedCapacityGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	concurrentClaim, err := ClaimVolumeMaterialization(ctx, pool, concurrentVolume.OperationID, "concurrent-worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	concurrentApply, err := AuthorizeVolumeMaterializationCommand(ctx, pool, concurrentClaim, "concurrent-apply-job-"+suffix, "concurrent-apply-command-"+suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	runVolumeBackendWithLostResponse(t, ctx, pool, concurrentApply.CommandID, authority.AuthorityGeneration, CommandLeaseScopeMutation, mutation)
	if err = MarkVolumeMaterializationDispatchUnknown(ctx, pool, concurrentClaim); err != nil {
		t.Fatal(err)
	}
	concurrentReadClaim, err := ClaimVolumeMaterialization(ctx, pool, concurrentVolume.OperationID, "concurrent-worker-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	concurrentRead, err := AuthorizeVolumeMaterializationCommand(ctx, pool, concurrentReadClaim, "concurrent-read-job-"+suffix, "concurrent-read-command-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	concurrentVerification := observeVolumeBackendAfterLostResponse(t, ctx, pool, concurrentRead.CommandID, "concurrent-verification-"+suffix, authority.AuthorityGeneration, readOnly)
	if _, err = CompleteVolumeMaterialization(ctx, pool, concurrentReadClaim, CompleteVolumeMaterializationRequest{OperationID: concurrentReadClaim.OperationID, OperationGeneration: 1, ClaimGeneration: concurrentReadClaim.ClaimGeneration, ObservationID: "concurrent-observation-" + suffix, VerificationID: concurrentVerification}); err != nil {
		t.Fatal(err)
	}
	concurrentAttachment := VolumeAttachmentIntentRequest{VolumeID: concurrentDesired.VolumeID, AttachmentIntentID: "concurrent-intent-" + suffix, AttachmentID: "concurrent-attachment-" + suffix, WorkloadID: "concurrent-workload-" + suffix, ExpectedVolumeRevision: 1}
	concurrentVolume, err = RequestVolumeAttachment(ctx, pool, concurrentAttachment)
	if err != nil {
		t.Fatal(err)
	}
	concurrentStorage := resourceStorage
	concurrentStorage.VolumeID = concurrentDesired.VolumeID
	concurrentStorage.VolumeRevision = 1
	concurrentStorage.AttachmentID = concurrentAttachment.AttachmentID
	concurrentStorage.AttachmentIntentID = concurrentAttachment.AttachmentIntentID
	concurrentStorage.CapacityAllocationID = concurrentVolume.AllocationID
	concurrentStorage.CapacityAllocationGeneration = concurrentVolume.AllocationGeneration
	requestA := placementRequest
	requestA.RequestID, requestA.WorkloadID = "concurrent-admission-a-"+suffix, concurrentAttachment.WorkloadID
	requestA.Storage = []placement.StorageRequirement{concurrentStorage}
	requestB := requestA
	requestB.RequestID = "concurrent-admission-b-" + suffix
	dryA, err := DryEvaluatePlacement(ctx, pool, requestA, host)
	if err != nil || !dryA.Eligible {
		t.Fatalf("concurrent dry A=%+v/%v", dryA, err)
	}
	dryB, err := DryEvaluatePlacement(ctx, pool, requestB, host)
	if err != nil || !dryB.Eligible {
		t.Fatalf("concurrent dry B=%+v/%v", dryB, err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, candidate := range []struct {
		request PlacementAdmissionRequest
		dry     placement.Evaluation
	}{{requestA, dryA}, {requestB, dryB}} {
		candidate := candidate
		go func() {
			<-start
			_, err := FinalAdmitPlacement(ctx, pool, candidate.request, candidate.dry)
			results <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrPlacementIneligible), errors.Is(err, ErrPlacementStale), errors.Is(err, ErrPlacementConflict):
			conflicts++
		default:
			t.Fatalf("concurrent Final Admission error=%v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent standalone Volume consumers=%d/%d", successes, conflicts)
	}
	var activeAttachments int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM kim.volume_attachment_claims WHERE volume_id=$1 AND claim_state='RESERVED'`, concurrentDesired.VolumeID).Scan(&activeAttachments); err != nil || activeAttachments != 1 {
		t.Fatalf("exact single consumer=%d/%v", activeAttachments, err)
	}
	if err = pool.QueryRow(ctx, `SELECT claim_state,reserved_bytes FROM kim.storage_capacity_claims WHERE volume_id=$1`, concurrentDesired.VolumeID).Scan(&claimState, &reservedBytes); err != nil || claimState != "ALLOCATED" || reservedBytes != concurrentDesired.SizeBytes {
		t.Fatalf("reservation retained=%s/%d/%v", claimState, reservedBytes, err)
	}
}

func runVolumeBackendWithLostResponse(t *testing.T, ctx context.Context, db TxBeginner, commandID string, authorityGeneration int64, scope string, backend agentexecution.Backend) {
	t.Helper()
	candidate, err := LoadCommandDispatchCandidate(ctx, db, commandID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := AcquireCommandLease(ctx, db, CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authorityGeneration, Duration: 100 * time.Millisecond, AuthorityScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	lease := contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: commandID, LeaseGeneration: grant.LeaseGeneration, AttemptIndex: grant.AttemptIndex, HostID: grant.HostID, HostAuthorityGeneration: grant.HostAuthorityGeneration, SessionGeneration: grant.SessionGeneration, LeaseToken: grant.Token, CommandType: candidate.CommandType, CommandSchemaVersion: candidate.SchemaVersion, TargetResourceID: candidate.TargetResourceID, CommandPayload: candidate.Payload, CommandPayloadDigest: candidate.PayloadDigest, ExecutionTimeoutMillis: 1000}
	if _, err = backend.Execute(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err = MarkCommandAttemptJournaled(ctx, db, CommandAttemptStart{CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, JournalEvidenceDigest: digestBytes([]byte(commandID + "-journal"))}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err = ExpireDueCommandLeases(ctx, db, 16); err != nil {
		t.Fatal(err)
	}
}

func observeVolumeBackendAfterLostResponse(t *testing.T, ctx context.Context, db TxBeginner, commandID, verificationID string, authorityGeneration int64, backend agentexecution.Backend) string {
	t.Helper()
	candidate, err := LoadCommandDispatchCandidate(ctx, db, commandID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := AcquireCommandLease(ctx, db, CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authorityGeneration, Duration: 100 * time.Millisecond, AuthorityScope: CommandLeaseScopeReadOnlyVerification})
	if err != nil {
		t.Fatal(err)
	}
	lease := contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: commandID, LeaseGeneration: grant.LeaseGeneration, AttemptIndex: grant.AttemptIndex, HostID: grant.HostID, HostAuthorityGeneration: grant.HostAuthorityGeneration, SessionGeneration: grant.SessionGeneration, LeaseToken: grant.Token, CommandType: candidate.CommandType, CommandSchemaVersion: candidate.SchemaVersion, TargetResourceID: candidate.TargetResourceID, CommandPayload: candidate.Payload, CommandPayloadDigest: candidate.PayloadDigest, ExecutionTimeoutMillis: 1000}
	result, err := backend.Execute(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = MarkCommandAttemptJournaled(ctx, db, CommandAttemptStart{CommandID: commandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, JournalEvidenceDigest: digestBytes([]byte(commandID + "-journal"))}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err = ExpireDueCommandLeases(ctx, db, 16); err != nil {
		t.Fatal(err)
	}
	if err = RecordCommandVerification(ctx, db, CommandVerification{VerificationID: verificationID, CommandID: commandID, AttemptIndex: grant.AttemptIndex, ObservationGeneration: result.Observation.Generation, ObservationDigest: result.Observation.Digest, State: result.Observation.State, VerifierArtifactDigest: digestBytes([]byte("volume-backend-verifier")), Evidence: result.Observation.Evidence}); err != nil {
		t.Fatal(err)
	}
	return verificationID
}
