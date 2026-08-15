package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
)

var ErrNoVolumeMaterializationWork = errors.New("no Volume materialization work available")

type VolumeResourceRequest struct {
	VolumeID, ProjectID, Name, StorageClassID            string
	SourceType, SourceImageID, SourceArtifactDigest      string
	StorageClassRevision, SourceImageRevision, SizeBytes uint64
	Bootable, DeleteProtection                           bool
}

type VolumeResource struct {
	VolumeID, ProjectID, Name, StorageClassID                  string
	SourceType, SourceImageID, SourceArtifactDigest            string
	Lifecycle, MaterializationState, AttachmentState           string
	OperationID, AllocationID, BackendID, HostID, VGUUID       string
	BindingID, LVUUID                                          string
	Revision, StorageClassRevision, SourceImageRevision        uint64
	SizeBytes, AllocationGeneration, MaterializationGeneration uint64
	BindingGeneration                                          uint64
	Bootable, DeleteProtection                                 bool
	CreatedAt, UpdatedAt                                       time.Time
}

type VolumeCapacityAllocationRequest struct {
	VolumeID, BackendID                                                           string
	ExpectedVolumeRevision, ExpectedBackendGeneration, ExpectedCapacityGeneration uint64
}

type VolumeAttachmentIntentRequest struct {
	VolumeID, AttachmentIntentID, AttachmentID, WorkloadID string
	ExpectedVolumeRevision                                 uint64
}

type VolumeMaterializationClaim struct {
	OperationID, Owner, ClaimMode, OperationKind, PlanDigest string
	OperationGeneration, ClaimGeneration                     uint64
	CanonicalPlan                                            []byte
	LeaseExpiresAt                                           time.Time
}

type VolumeMaterializationCommand struct {
	JobID, CommandID, PayloadDigest string
}

type CompleteVolumeMaterializationRequest struct {
	OperationID, ObservationID, VerificationID string
	OperationGeneration, ClaimGeneration       uint64
}

type volumeMaterializationPlan struct {
	SchemaVersion             string `json:"schema_version"`
	OperationID               string `json:"operation_id"`
	OperationGeneration       uint64 `json:"operation_generation"`
	OperationKind             string `json:"operation_kind"`
	VolumeID                  string `json:"volume_id"`
	VolumeRevision            uint64 `json:"volume_revision"`
	AllocationID              string `json:"allocation_id"`
	AllocationGeneration      uint64 `json:"allocation_generation"`
	BindingID                 string `json:"binding_id"`
	BindingGeneration         uint64 `json:"binding_generation"`
	MaterializationGeneration uint64 `json:"materialization_generation"`
	BackendID                 string `json:"backend_id"`
	BackendGeneration         uint64 `json:"backend_generation"`
	HostID                    string `json:"host_id"`
	VGUUID                    string `json:"vg_uuid"`
	BackendResourceKey        string `json:"backend_resource_key"`
	ExpectedLVUUID            string `json:"expected_lv_uuid,omitempty"`
	ExpectedSizeBytes         uint64 `json:"expected_size_bytes"`
	SourceType                string `json:"source_type"`
	SourceImageID             string `json:"source_image_id,omitempty"`
	SourceImageRevision       uint64 `json:"source_image_revision,omitempty"`
	SourceArtifactDigest      string `json:"source_artifact_digest,omitempty"`
}

func canonicalVolumeResource(r VolumeResourceRequest) (VolumeResourceRequest, error) {
	if !networkResourceIDPattern.MatchString(r.VolumeID) || r.ProjectID == "" || r.Name == "" || len(r.Name) > 255 || r.StorageClassID == "" || r.StorageClassRevision == 0 || r.SizeBytes == 0 || r.SizeBytes%(1024*1024) != 0 || (r.SourceType != "BLANK" && r.SourceType != "IMAGE") {
		return r, errors.New("complete backend-neutral Volume desired authority is required")
	}
	if r.SourceType == "BLANK" {
		if r.SourceImageID != "" || r.SourceImageRevision != 0 || r.SourceArtifactDigest != "" {
			return r, errors.New("BLANK Volume cannot reference Image authority")
		}
	} else if r.SourceImageID == "" || r.SourceImageRevision == 0 || len(r.SourceArtifactDigest) != 64 {
		return r, errors.New("IMAGE Volume requires exact verified Image revision and digest")
	}
	return r, nil
}

func volumeDesiredDigest(r VolumeResourceRequest, revision uint64, lifecycle string) string {
	raw, _ := json.Marshal(struct {
		Request   VolumeResourceRequest
		Revision  uint64
		Lifecycle string
	}{r, revision, lifecycle})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func CreateVolumeResource(ctx context.Context, db TxBeginner, request VolumeResourceRequest) (VolumeResource, error) {
	r, err := canonicalVolumeResource(request)
	if err != nil {
		return VolumeResource{}, err
	}
	digest := volumeDesiredDigest(r, 1, "ACTIVE")
	err = RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "volume-resource/"+r.VolumeID); err != nil {
			return err
		}
		var oldDigest, source string
		if err := tx.QueryRow(ctx, `SELECT desired_digest,authority_source FROM kim.volumes_current WHERE volume_id=$1`, r.VolumeID).Scan(&oldDigest, &source); err == nil {
			if source == "VOLUME_RESOURCE" && oldDigest == digest {
				return nil
			}
			return ErrPlacementConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var allowedBackend, locality string
		if err := tx.QueryRow(ctx, `SELECT e.allowed_backend_type,e.locality FROM kim.storage_classes_current c JOIN kim.storage_class_revision_evidence e USING(storage_class_id) WHERE c.storage_class_id=$1 AND c.class_revision=$2 AND c.lifecycle_state='ACTIVE' AND 'SINGLE_WRITER'=ANY(e.access_modes) FOR SHARE OF c,e`, r.StorageClassID, r.StorageClassRevision).Scan(&allowedBackend, &locality); err != nil || allowedBackend != "LOCAL_LVM" || locality != "HOST_LOCAL" {
			return ErrPlacementConflict
		}
		if r.SourceType == "IMAGE" {
			var verified string
			if err := tx.QueryRow(ctx, `SELECT verified_digest FROM kim.northbound_images_current WHERE image_id=$1 AND image_revision=$2 AND lifecycle_state='ACTIVE' AND verification_state='VERIFIED'`, r.SourceImageID, r.SourceImageRevision).Scan(&verified); err != nil || verified != r.SourceArtifactDigest {
				return ErrPlacementConflict
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_resource_revision_evidence(volume_id,volume_revision,project_id,volume_name,size_bytes,storage_class_id,storage_class_revision,access_mode,bootable,source_type,source_image_id,source_image_revision,source_artifact_digest,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,1,$2,$3,$4,$5,$6,'SINGLE_WRITER',$7,$8,NULLIF($9,''),NULLIF($10,0),NULLIF($11,''),$12,'ACTIVE',NULL,$13)`, r.VolumeID, r.ProjectID, r.Name, r.SizeBytes, r.StorageClassID, r.StorageClassRevision, r.Bootable, r.SourceType, r.SourceImageID, r.SourceImageRevision, r.SourceArtifactDigest, r.DeleteProtection, digest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.volumes_current(volume_id,placement_admission_id,project_id,storage_class_id,storage_class_revision,desired_generation,size_bytes,access_mode,bootable,lifecycle_state,volume_revision,volume_name,source_type,source_image_id,source_image_revision,source_artifact_digest,delete_protection,desired_digest,authority_source,updated_at) VALUES($1,NULL,$2,$3,$4,1,$5,'SINGLE_WRITER',$6,'ACTIVE',1,$7,$8,NULLIF($9,''),NULLIF($10,0),NULLIF($11,''),$12,$13,'VOLUME_RESOURCE',statement_timestamp())`, r.VolumeID, r.ProjectID, r.StorageClassID, r.StorageClassRevision, r.SizeBytes, r.Bootable, r.Name, r.SourceType, r.SourceImageID, r.SourceImageRevision, r.SourceArtifactDigest, r.DeleteProtection, digest)
		return err
	})
	if err != nil {
		return VolumeResource{}, err
	}
	return GetVolumeResource(ctx, db, r.VolumeID)
}

func GetVolumeResource(ctx context.Context, db TxBeginner, id string) (VolumeResource, error) {
	var v VolumeResource
	var sourceRevision, allocationGeneration, materializationGeneration, bindingGeneration *int64
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT v.volume_id,v.project_id,v.volume_name,v.storage_class_id,v.storage_class_revision,v.size_bytes,v.bootable,v.source_type,COALESCE(v.source_image_id,''),v.source_image_revision,COALESCE(v.source_artifact_digest,''),v.lifecycle_state,v.volume_revision,v.delete_protection,v.created_at,v.updated_at,COALESCE((SELECT m.materialization_state FROM kim.volume_materializations_current m WHERE m.volume_id=v.volume_id ORDER BY m.materialization_generation DESC LIMIT 1),'NONE'),COALESCE((SELECT a.intent_state FROM kim.volume_attachment_intents_current a WHERE a.volume_id=v.volume_id),'UNATTACHED'),COALESCE((SELECT m.operation_id FROM kim.volume_materializations_current m WHERE m.volume_id=v.volume_id ORDER BY m.materialization_generation DESC LIMIT 1),''),COALESCE((SELECT c.allocation_decision_id FROM kim.storage_capacity_claims c WHERE c.volume_id=v.volume_id AND c.authority_source='VOLUME_RESOURCE' AND c.claim_state IN('RESERVED','ALLOCATED','RELEASE_PENDING') ORDER BY c.allocation_generation DESC LIMIT 1),''),(SELECT c.allocation_generation FROM kim.storage_capacity_claims c WHERE c.volume_id=v.volume_id AND c.authority_source='VOLUME_RESOURCE' AND c.claim_state IN('RESERVED','ALLOCATED','RELEASE_PENDING') ORDER BY c.allocation_generation DESC LIMIT 1),COALESCE((SELECT i.backend_id FROM kim.volume_backend_binding_intents i WHERE i.volume_id=v.volume_id AND i.authority_source='VOLUME_RESOURCE' ORDER BY i.materialization_generation DESC LIMIT 1),''),COALESCE((SELECT i.host_id FROM kim.volume_backend_binding_intents i WHERE i.volume_id=v.volume_id AND i.authority_source='VOLUME_RESOURCE' ORDER BY i.materialization_generation DESC LIMIT 1),''),COALESCE((SELECT i.vg_uuid FROM kim.volume_backend_binding_intents i WHERE i.volume_id=v.volume_id AND i.authority_source='VOLUME_RESOURCE' ORDER BY i.materialization_generation DESC LIMIT 1),''),COALESCE((SELECT i.binding_id FROM kim.volume_backend_binding_intents i WHERE i.volume_id=v.volume_id AND i.authority_source='VOLUME_RESOURCE' ORDER BY i.materialization_generation DESC LIMIT 1),''),(SELECT i.binding_generation FROM kim.volume_backend_binding_intents i WHERE i.volume_id=v.volume_id AND i.authority_source='VOLUME_RESOURCE' ORDER BY i.materialization_generation DESC LIMIT 1),(SELECT m.materialization_generation FROM kim.volume_materializations_current m WHERE m.volume_id=v.volume_id ORDER BY m.materialization_generation DESC LIMIT 1),COALESCE((SELECT b.lv_uuid FROM kim.volume_backend_bindings_current b JOIN kim.volume_backend_binding_intents i USING(binding_id) WHERE b.volume_id=v.volume_id AND b.binding_state='BOUND' ORDER BY i.materialization_generation DESC LIMIT 1),'') FROM kim.volumes_current v WHERE v.volume_id=$1 AND v.authority_source='VOLUME_RESOURCE'`, id).Scan(&v.VolumeID, &v.ProjectID, &v.Name, &v.StorageClassID, &v.StorageClassRevision, &v.SizeBytes, &v.Bootable, &v.SourceType, &v.SourceImageID, &sourceRevision, &v.SourceArtifactDigest, &v.Lifecycle, &v.Revision, &v.DeleteProtection, &v.CreatedAt, &v.UpdatedAt, &v.MaterializationState, &v.AttachmentState, &v.OperationID, &v.AllocationID, &allocationGeneration, &v.BackendID, &v.HostID, &v.VGUUID, &v.BindingID, &bindingGeneration, &materializationGeneration, &v.LVUUID)
	})
	if sourceRevision != nil {
		v.SourceImageRevision = uint64(*sourceRevision)
	}
	if allocationGeneration != nil {
		v.AllocationGeneration = uint64(*allocationGeneration)
	}
	if bindingGeneration != nil {
		v.BindingGeneration = uint64(*bindingGeneration)
	}
	if materializationGeneration != nil {
		v.MaterializationGeneration = uint64(*materializationGeneration)
	}
	return v, err
}

func UpdateVolumeResource(ctx context.Context, db TxBeginner, request VolumeResourceRequest, expected uint64) (VolumeResource, error) {
	r, err := canonicalVolumeResource(request)
	if err != nil || expected == 0 {
		return VolumeResource{}, errors.New("complete Volume replacement revision is required")
	}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var current VolumeResourceRequest
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT project_id,volume_name,storage_class_id,storage_class_revision,size_bytes,bootable,source_type,COALESCE(source_image_id,''),COALESCE(source_image_revision,0),COALESCE(source_artifact_digest,''),delete_protection,volume_revision FROM kim.volumes_current WHERE volume_id=$1 AND authority_source='VOLUME_RESOURCE' AND lifecycle_state='ACTIVE' FOR UPDATE`, r.VolumeID).Scan(&current.ProjectID, &current.Name, &current.StorageClassID, &current.StorageClassRevision, &current.SizeBytes, &current.Bootable, &current.SourceType, &current.SourceImageID, &current.SourceImageRevision, &current.SourceArtifactDigest, &current.DeleteProtection, &revision); err != nil || uint64(revision) != expected {
			return ErrPlacementConflict
		}
		if current.ProjectID != r.ProjectID || current.StorageClassID != r.StorageClassID || current.StorageClassRevision != r.StorageClassRevision || current.SizeBytes != r.SizeBytes || current.Bootable != r.Bootable || current.SourceType != r.SourceType || current.SourceImageID != r.SourceImageID || current.SourceImageRevision != r.SourceImageRevision || current.SourceArtifactDigest != r.SourceArtifactDigest {
			return ErrPlacementConflict
		}
		next := expected + 1
		digest := volumeDesiredDigest(r, next, "ACTIVE")
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_resource_revision_evidence(volume_id,volume_revision,project_id,volume_name,size_bytes,storage_class_id,storage_class_revision,access_mode,bootable,source_type,source_image_id,source_image_revision,source_artifact_digest,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,'SINGLE_WRITER',$8,$9,NULLIF($10,''),NULLIF($11,0),NULLIF($12,''),$13,'ACTIVE',$14,$15)`, r.VolumeID, next, r.ProjectID, r.Name, r.SizeBytes, r.StorageClassID, r.StorageClassRevision, r.Bootable, r.SourceType, r.SourceImageID, r.SourceImageRevision, r.SourceArtifactDigest, r.DeleteProtection, expected, digest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.volumes_current SET volume_revision=$2,desired_generation=$2,volume_name=$3,delete_protection=$4,desired_digest=$5,updated_at=statement_timestamp() WHERE volume_id=$1 AND volume_revision=$6`, r.VolumeID, next, r.Name, r.DeleteProtection, digest, expected)
		return err
	})
	if err != nil {
		return VolumeResource{}, err
	}
	return GetVolumeResource(ctx, db, r.VolumeID)
}

func AllocateVolumeCapacity(ctx context.Context, db TxBeginner, r VolumeCapacityAllocationRequest) (VolumeResource, error) {
	if r.VolumeID == "" || r.BackendID == "" || r.ExpectedVolumeRevision == 0 || r.ExpectedBackendGeneration == 0 || r.ExpectedCapacityGeneration == 0 {
		return VolumeResource{}, errors.New("exact Volume capacity allocation request is required")
	}
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 8, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "storage-backend/"+r.BackendID); err != nil {
			return err
		}
		var project, name, class, sourceType, imageID, imageDigest, lifecycle string
		var classRev, size, imageRev int64
		var boot, protect bool
		if err := tx.QueryRow(ctx, `SELECT project_id,volume_name,storage_class_id,storage_class_revision,size_bytes,bootable,source_type,COALESCE(source_image_id,''),COALESCE(source_image_revision,0),COALESCE(source_artifact_digest,''),delete_protection,lifecycle_state FROM kim.volumes_current WHERE volume_id=$1 AND volume_revision=$2 AND authority_source='VOLUME_RESOURCE' AND lifecycle_state IN('ACTIVE','CREATING') FOR UPDATE`, r.VolumeID, r.ExpectedVolumeRevision).Scan(&project, &name, &class, &classRev, &size, &boot, &sourceType, &imageID, &imageRev, &imageDigest, &protect, &lifecycle); err != nil {
			return ErrPlacementConflict
		}
		var oldID string
		if err := tx.QueryRow(ctx, `SELECT allocation_id FROM kim.volume_capacity_allocation_decision_evidence WHERE volume_id=$1 AND volume_revision=$2 AND backend_id=$3`, r.VolumeID, r.ExpectedVolumeRevision, r.BackendID).Scan(&oldID); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if lifecycle != "ACTIVE" {
			return ErrPlacementConflict
		}
		var host, vg, observation string
		var bg, cg, total, free, external, reserve, claimed int64
		if err := tx.QueryRow(ctx, `SELECT b.host_id,b.vg_uuid,b.backend_generation,p.capacity_generation,p.observation_id,o.total_bytes,o.observed_free_bytes,o.external_or_unknown_bytes,o.hard_reserve_bytes,COALESCE((SELECT sum(reserved_bytes) FROM kim.storage_capacity_claims c WHERE c.backend_id=b.backend_id AND c.claim_state IN('RESERVED','ALLOCATED','RELEASE_PENDING','QUARANTINED')),0) FROM kim.storage_backends_current b JOIN kim.storage_capacity_projections_current p USING(backend_id) JOIN kim.storage_capacity_observation_evidence o ON(o.observation_id,o.backend_id,o.capacity_generation)=(p.observation_id,p.backend_id,p.capacity_generation) JOIN kim.storage_class_revision_evidence s ON s.storage_class_id=$4 AND s.class_revision=$5 WHERE b.backend_id=$1 AND b.backend_generation=$2 AND p.capacity_generation=$3 AND b.lifecycle_state='ACTIVE' AND b.capability_state='CURRENT' AND p.projection_state='CURRENT' AND o.health_state='HEALTHY' AND s.allowed_backend_type=b.backend_type AND s.locality='HOST_LOCAL' FOR UPDATE OF b,p`, r.BackendID, r.ExpectedBackendGeneration, r.ExpectedCapacityGeneration, class, classRev).Scan(&host, &vg, &bg, &cg, &observation, &total, &free, &external, &reserve, &claimed); err != nil {
			return ErrPlacementConflict
		}
		ledger := total - reserve - claimed
		observed := free - external
		if ledger < int64(size) || observed < int64(size) {
			return ErrPlacementConflict
		}
		allocation := "volume-capacity:" + r.VolumeID + ":1"
		decision := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%s/%d/%d", allocation, r.VolumeID, r.ExpectedVolumeRevision, r.BackendID, cg, size))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_capacity_allocation_decision_evidence(allocation_id,allocation_generation,volume_id,volume_revision,storage_class_id,storage_class_revision,requested_bytes,backend_id,backend_generation,host_id,vg_uuid,capacity_generation,capacity_observation_id,decision_state,decision_digest) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'ALLOCATED',$13)`, allocation, r.VolumeID, r.ExpectedVolumeRevision, class, classRev, size, r.BackendID, bg, host, vg, cg, observation, decision); err != nil {
			return err
		}
		claim := "storage-capacity:volume-resource:" + r.VolumeID + ":1"
		if _, err := tx.Exec(ctx, `INSERT INTO kim.storage_capacity_claims(capacity_claim_id,placement_admission_id,backend_id,volume_id,capacity_generation,reserved_bytes,claim_state,volume_revision,allocation_generation,allocation_decision_id,authority_source) VALUES($1,NULL,$2,$3,$4,$5,'RESERVED',$6,1,$7,'VOLUME_RESOURCE')`, claim, r.BackendID, r.VolumeID, cg, size, r.ExpectedVolumeRevision, allocation); err != nil {
			return err
		}
		binding := "volume-binding:" + r.VolumeID + ":1"
		key := locallvm.ResourceKey(r.VolumeID)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_backend_binding_intents(binding_id,placement_admission_id,volume_id,binding_generation,backend_id,host_id,vg_uuid,backend_resource_key,binding_state,volume_revision,materialization_generation,capacity_allocation_id,capacity_allocation_generation,authority_source) VALUES($1,NULL,$2,1,$3,$4,$5,$6,'RESERVED',$7,1,$8,1,'VOLUME_RESOURCE')`, binding, r.VolumeID, r.BackendID, host, vg, key, r.ExpectedVolumeRevision, allocation); err != nil {
			return err
		}
		plan := volumeMaterializationPlan{SchemaVersion: "kim.storage.volume-materialization/v1", OperationID: "volume-materialization:" + r.VolumeID + ":1", OperationGeneration: 1, OperationKind: "MATERIALIZE", VolumeID: r.VolumeID, VolumeRevision: r.ExpectedVolumeRevision, AllocationID: allocation, AllocationGeneration: 1, BindingID: binding, BindingGeneration: 1, MaterializationGeneration: 1, BackendID: r.BackendID, BackendGeneration: uint64(bg), HostID: host, VGUUID: vg, BackendResourceKey: key, ExpectedSizeBytes: uint64(size), SourceType: sourceType, SourceImageID: imageID, SourceImageRevision: uint64(imageRev), SourceArtifactDigest: imageDigest}
		if err := insertVolumeMaterializationOperationTx(ctx, tx, plan); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.volumes_current SET lifecycle_state='CREATING',updated_at=statement_timestamp() WHERE volume_id=$1`, r.VolumeID)
		return err
	})
	if err != nil {
		return VolumeResource{}, err
	}
	return GetVolumeResource(ctx, db, r.VolumeID)
}

func digestVolumeAuthority(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func insertVolumeMaterializationOperationTx(ctx context.Context, tx pgx.Tx, p volumeMaterializationPlan) error {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_materialization_operation_evidence(operation_id,operation_generation,operation_kind,volume_id,volume_revision,allocation_id,allocation_generation,binding_id,binding_generation,materialization_generation,backend_id,backend_generation,host_id,vg_uuid,backend_resource_key,expected_lv_uuid,expected_size_bytes,source_type,source_image_id,source_image_revision,source_artifact_digest,schema_version,canonical_plan,plan_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16,''),$17,$18,NULLIF($19,''),NULLIF($20,0),NULLIF($21,''),$22,$23,$24)`, p.OperationID, p.OperationGeneration, p.OperationKind, p.VolumeID, p.VolumeRevision, p.AllocationID, p.AllocationGeneration, p.BindingID, p.BindingGeneration, p.MaterializationGeneration, p.BackendID, p.BackendGeneration, p.HostID, p.VGUUID, p.BackendResourceKey, p.ExpectedLVUUID, p.ExpectedSizeBytes, p.SourceType, p.SourceImageID, p.SourceImageRevision, p.SourceArtifactDigest, p.SchemaVersion, raw, digest); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_materialization_operations_current(operation_id,operation_generation,volume_id,volume_revision,materialization_generation,operation_kind,phase) VALUES($1,$2,$3,$4,$5,$6,'PENDING')`, p.OperationID, p.OperationGeneration, p.VolumeID, p.VolumeRevision, p.MaterializationGeneration, p.OperationKind); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO kim.volume_materializations_current(volume_id,materialization_generation,volume_revision,operation_id,operation_generation,binding_id,binding_generation,materialization_state) VALUES($1,$2,$3,$4,$5,$6,$7,'PENDING')`, p.VolumeID, p.MaterializationGeneration, p.VolumeRevision, p.OperationID, p.OperationGeneration, p.BindingID, p.BindingGeneration)
	return err
}

func ClaimVolumeMaterialization(ctx context.Context, db TxBeginner, operationID, owner string, lease time.Duration) (VolumeMaterializationClaim, error) {
	if owner == "" || lease <= 0 || lease > MaxOVNRuntimeClaimLifetime {
		return VolumeMaterializationClaim{}, errors.New("bounded Volume materialization claim is required")
	}
	var c VolumeMaterializationClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var phase string
		var gen int64
		if err := tx.QueryRow(ctx, `SELECT c.operation_id,c.operation_generation,c.operation_kind,c.phase,e.canonical_plan,e.plan_digest FROM kim.volume_materialization_operations_current c JOIN kim.volume_materialization_operation_evidence e USING(operation_id,operation_generation) WHERE ($1='' OR c.operation_id=$1) AND (c.phase IN('PENDING','DISPATCH_UNKNOWN') OR(c.phase='CLAIMED' AND c.claim_expires_at<=statement_timestamp())) ORDER BY c.updated_at FOR UPDATE OF c SKIP LOCKED LIMIT 1`, operationID).Scan(&c.OperationID, &c.OperationGeneration, &c.OperationKind, &phase, &c.CanonicalPlan, &c.PlanDigest); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoVolumeMaterializationWork
		} else if err != nil {
			return err
		}
		c.ClaimMode = "APPLY_ALLOWED"
		if phase != "PENDING" {
			c.ClaimMode = "READ_BACK_FIRST"
		}
		if err := tx.QueryRow(ctx, `UPDATE kim.volume_materialization_operations_current SET phase='CLAIMED',claim_owner=$2,claim_generation=last_claim_generation+1,last_claim_generation=last_claim_generation+1,claim_expires_at=statement_timestamp()+($3*interval '1 microsecond'),updated_at=statement_timestamp() WHERE operation_id=$1 RETURNING claim_generation,claim_expires_at`, c.OperationID, owner, lease.Microseconds()).Scan(&gen, &c.LeaseExpiresAt); err != nil {
			return err
		}
		c.Owner = owner
		c.ClaimGeneration = uint64(gen)
		_, err := tx.Exec(ctx, `INSERT INTO kim.volume_materialization_attempt_evidence(operation_id,operation_generation,claim_generation,claim_owner,claim_mode,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6)`, c.OperationID, c.OperationGeneration, gen, owner, c.ClaimMode, c.LeaseExpiresAt)
		return err
	})
	return c, err
}

func markVolumeClaimUnknown(ctx context.Context, db TxBeginner, c VolumeMaterializationClaim) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVolumeClaim(ctx, tx, c); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.volume_materialization_operations_current SET phase='DISPATCH_UNKNOWN',response_state='UNKNOWN',claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID)
		return err
	})
}
func MarkVolumeMaterializationDispatchUnknown(ctx context.Context, db TxBeginner, c VolumeMaterializationClaim) error {
	return markVolumeClaimUnknown(ctx, db, c)
}
func lockVolumeClaim(ctx context.Context, tx pgx.Tx, c VolumeMaterializationClaim) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.volume_materialization_operations_current WHERE operation_id=$1 AND operation_generation=$2 AND phase='CLAIMED' AND claim_owner=$3 AND claim_generation=$4 AND claim_expires_at>statement_timestamp())`, c.OperationID, c.OperationGeneration, c.Owner, c.ClaimGeneration).Scan(&ok); err != nil || !ok {
		return ErrPlacementConflict
	}
	return nil
}

func AuthorizeVolumeMaterializationCommand(ctx context.Context, db TxBeginner, c VolumeMaterializationClaim, jobID, commandID string, readBack bool) (VolumeMaterializationCommand, error) {
	var out VolumeMaterializationCommand
	if jobID == "" || commandID == "" || (!readBack && c.ClaimMode == "READ_BACK_FIRST") || (readBack && c.ClaimMode != "READ_BACK_FIRST") {
		return out, ErrPlacementConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVolumeClaim(ctx, tx, c); err != nil {
			return err
		}
		var p volumeMaterializationPlan
		var raw []byte
		var digest string
		if err := tx.QueryRow(ctx, `SELECT canonical_plan,plan_digest FROM kim.volume_materialization_operation_evidence WHERE operation_id=$1 AND operation_generation=$2`, c.OperationID, c.OperationGeneration).Scan(&raw, &digest); err != nil {
			return err
		}
		if json.Unmarshal(raw, &p) != nil {
			return ErrPlacementConflict
		}
		canonical, _ := json.Marshal(p)
		sum := sha256.Sum256(canonical)
		if hex.EncodeToString(sum[:]) != digest || c.PlanDigest != digest {
			return ErrPlacementConflict
		}
		commandType, schema := locallvm.CommandType, locallvm.SchemaVersion
		payload := map[string]any{"vg_uuid": p.VGUUID, "size_mib": p.ExpectedSizeBytes / (1024 * 1024), "desired_state": "PRESENT"}
		if readBack {
			commandType, schema = locallvm.ReadBackCommandType, locallvm.ReadBackSchemaVersion
		}
		if p.OperationKind == "RETIRE" {
			commandType, schema = locallvm.DeleteCommandType, locallvm.DeleteSchemaVersion
			if readBack {
				commandType, schema = locallvm.DeleteReadBackType, locallvm.DeleteReadBackSchema
			}
			payload = map[string]any{"backend_id": p.BackendID, "backend_generation": p.BackendGeneration, "vg_uuid": p.VGUUID, "expected_lv_uuid": p.ExpectedLVUUID, "backend_resource_key": p.BackendResourceKey, "binding_id": p.BindingID, "binding_generation": p.BindingGeneration, "cleanup_operation_id": p.OperationID, "cleanup_generation": p.OperationGeneration, "desired_state": "ABSENT"}
		}
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: jobID, CommandID: commandID, HostID: p.HostID, ResourceType: "VOLUME_MATERIALIZATION", ResourceID: p.OperationID, DesiredRevision: int64(p.MaterializationGeneration), CommandType: commandType, SchemaVersion: schema, TargetResourceID: "volume:" + p.VolumeID, Payload: payload}); err != nil {
			return err
		}
		encoded, _ := json.Marshal(payload)
		pd := digestBytes(encoded)
		mode := "APPLY"
		if readBack {
			mode = "READ_BACK"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_materialization_command_evidence(operation_id,operation_generation,claim_generation,command_id,job_id,command_mode,command_payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, c.OperationID, c.OperationGeneration, c.ClaimGeneration, commandID, jobID, mode, pd); err != nil {
			return err
		}
		out = VolumeMaterializationCommand{jobID, commandID, pd}
		return nil
	})
	return out, err
}

func CompleteVolumeMaterialization(ctx context.Context, db TxBeginner, c VolumeMaterializationClaim, r CompleteVolumeMaterializationRequest) (string, error) {
	if r.OperationID != c.OperationID || r.OperationGeneration != c.OperationGeneration || r.ClaimGeneration != c.ClaimGeneration || r.ObservationID == "" || r.VerificationID == "" {
		return "", ErrPlacementConflict
	}
	var terminal string
	if err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT t.terminal_evidence_id FROM kim.volume_materialization_terminal_evidence t JOIN kim.volume_materialization_observation_evidence o ON o.observation_id=t.observation_id WHERE t.operation_id=$1 AND t.operation_generation=$2 AND o.verification_id=$3 AND o.observation_id=$4`, r.OperationID, r.OperationGeneration, r.VerificationID, r.ObservationID).Scan(&terminal)
	}); err == nil {
		return terminal, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockVolumeClaim(ctx, tx, c); err != nil {
			return err
		}
		var p volumeMaterializationPlan
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT canonical_plan FROM kim.volume_materialization_operation_evidence WHERE operation_id=$1 AND operation_generation=$2`, c.OperationID, c.OperationGeneration).Scan(&raw); err != nil {
			return err
		}
		if json.Unmarshal(raw, &p) != nil {
			return ErrPlacementConflict
		}
		var commandID, ctype, schema, state, observationDigest, verifier string
		var attempt, observationGeneration int64
		var evidence []byte
		if err := tx.QueryRow(ctx, `SELECT command.command_id,command.command_type,command.schema_version,v.verification_state,v.attempt_index,v.observation_generation,v.observation_digest,v.verifier_artifact_digest,v.evidence_payload FROM kim.volume_materialization_command_evidence ce JOIN kim.execution_commands command USING(command_id) JOIN kim.command_verification_evidence v ON v.command_id=command.command_id AND v.verification_id=$4 WHERE ce.operation_id=$1 AND ce.operation_generation=$2 AND ce.claim_generation=$3`, c.OperationID, c.OperationGeneration, c.ClaimGeneration, r.VerificationID).Scan(&commandID, &ctype, &schema, &state, &attempt, &observationGeneration, &observationDigest, &verifier, &evidence); err != nil {
			return ErrPlacementConflict
		}
		var payload map[string]any
		if json.Unmarshal(evidence, &payload) != nil {
			return ErrPlacementConflict
		}
		response := "LOST"
		var hasResult bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.command_results WHERE command_id=$1 AND attempt_index=$2)`, commandID, attempt).Scan(&hasResult); err != nil {
			return err
		}
		if hasResult {
			response = "RECEIVED"
		}
		var observedVG, observedLV string
		var observedSize uint64
		present, identity, sizeMatches := false, false, false
		terminalState := "VERIFIED"
		if p.OperationKind == "MATERIALIZE" {
			if (ctype != locallvm.CommandType || schema != locallvm.SchemaVersion) &&
				(ctype != locallvm.ReadBackCommandType || schema != locallvm.ReadBackSchemaVersion) {
				return ErrPlacementConflict
			}
			observedVG, _ = payload["observed_vg_uuid"].(string)
			observedLV, _ = payload["observed_lv_uuid"].(string)
			observedSize, _ = volumeEvidenceUint64(payload["observed_size_bytes"])
			resource, _ := payload["backend_resource_key"].(string)
			present = observedLV != ""
			identity = present && observedVG == p.VGUUID && resource == p.BackendResourceKey
			sizeMatches = observedSize == p.ExpectedSizeBytes
			if state != "MATCHED" || !identity || !sizeMatches {
				return ErrPlacementConflict
			}
		} else {
			if (ctype != locallvm.DeleteCommandType || schema != locallvm.DeleteSchemaVersion) &&
				(ctype != locallvm.DeleteReadBackType || schema != locallvm.DeleteReadBackSchema) {
				return ErrPlacementConflict
			}
			observedVG, _ = payload["vg_uuid"].(string)
			observedLV, _ = payload["observed_lv_uuid"].(string)
			exactPresent, _ := payload["exact_source_lv_present"].(bool)
			foreign, _ := payload["foreign_replacement_present"].(bool)
			expected, _ := payload["expected_lv_uuid"].(string)
			resource, _ := payload["backend_resource_key"].(string)
			present = exactPresent
			identity = !exactPresent && !foreign && expected == p.ExpectedLVUUID && resource == p.BackendResourceKey && observedVG == p.VGUUID
			sizeMatches = true
			terminalState = "ABSENT"
			if state != "MATCHED" || !identity {
				return ErrPlacementConflict
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_materialization_observation_evidence(observation_id,operation_id,operation_generation,claim_generation,command_id,verification_id,volume_id,volume_revision,materialization_generation,binding_id,binding_generation,observed_vg_uuid,observed_lv_uuid,observed_size_bytes,object_present,identity_matches,size_matches,response_state,observation_generation,observation_digest,verifier_artifact_digest,evidence_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),NULLIF($14,0),$15,$16,$17,$18,$19,$20,$21,$22)`, r.ObservationID, c.OperationID, c.OperationGeneration, c.ClaimGeneration, commandID, r.VerificationID, p.VolumeID, p.VolumeRevision, p.MaterializationGeneration, p.BindingID, p.BindingGeneration, observedVG, observedLV, observedSize, present, identity, sizeMatches, response, observationGeneration, observationDigest, verifier, state); err != nil {
			return err
		}
		terminal = "volume-terminal:" + c.OperationID + ":" + fmt.Sprint(c.ClaimGeneration)
		td := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%d/%s", terminal, p.VolumeID, p.VolumeRevision, p.MaterializationGeneration, terminalState))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_materialization_terminal_evidence(terminal_evidence_id,operation_id,operation_generation,observation_id,volume_id,volume_revision,materialization_generation,binding_id,binding_generation,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, terminal, c.OperationID, c.OperationGeneration, r.ObservationID, p.VolumeID, p.VolumeRevision, p.MaterializationGeneration, p.BindingID, p.BindingGeneration, terminalState, td); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_materialization_operations_current SET phase='SUCCEEDED',response_state=$2,terminal_evidence_id=$3,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, c.OperationID, response, terminal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_materializations_current SET materialization_state=$3,terminal_evidence_id=$4,updated_at=statement_timestamp() WHERE volume_id=$1 AND materialization_generation=$2`, p.VolumeID, p.MaterializationGeneration, terminalState, terminal); err != nil {
			return err
		}
		if p.OperationKind == "MATERIALIZE" {
			evidenceID := "volume-binding-evidence:" + c.OperationID + ":" + fmt.Sprint(c.ClaimGeneration)
			if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_backend_binding_evidence(evidence_id,binding_id,volume_id,binding_generation,backend_id,host_id,vg_uuid,lv_uuid,backend_resource_key,observed_size_bytes,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,evidence_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'MATCHED')`, evidenceID, p.BindingID, p.VolumeID, p.BindingGeneration, p.BackendID, p.HostID, p.VGUUID, observedLV, p.BackendResourceKey, observedSize, commandID, attempt, r.VerificationID, observationGeneration, observationDigest, verifier); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_backend_bindings_current(binding_id,volume_id,binding_generation,observation_generation,evidence_id,binding_state,host_id,vg_uuid,lv_uuid,backend_resource_key) VALUES($1,$2,$3,$4,$5,'BOUND',$6,$7,$8,$9)`, p.BindingID, p.VolumeID, p.BindingGeneration, observationGeneration, evidenceID, p.HostID, p.VGUUID, observedLV, p.BackendResourceKey); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_binding_intents SET binding_state='BOUND',observed_lv_uuid=$2,updated_at=statement_timestamp() WHERE binding_id=$1`, p.BindingID, observedLV); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.storage_capacity_claims SET claim_state='ALLOCATED',updated_at=statement_timestamp() WHERE allocation_decision_id=$1 AND allocation_generation=$2`, p.AllocationID, p.AllocationGeneration); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE kim.volumes_current SET lifecycle_state='AVAILABLE',updated_at=statement_timestamp() WHERE volume_id=$1 AND volume_revision=$2`, p.VolumeID, p.VolumeRevision)
			return err
		}
		return completeVolumeRetirementTx(ctx, tx, p, terminal)
	})
	return terminal, err
}

func volumeEvidenceUint64(v any) (uint64, bool) {
	switch x := v.(type) {
	case float64:
		if x >= 0 && x == float64(uint64(x)) {
			return uint64(x), true
		}
	case json.Number:
		n, err := x.Int64()
		return uint64(n), err == nil && n >= 0
	}
	return 0, false
}

func RequestVolumeAttachment(ctx context.Context, db TxBeginner, r VolumeAttachmentIntentRequest) (VolumeResource, error) {
	if r.VolumeID == "" || r.AttachmentIntentID == "" || r.AttachmentID == "" || r.WorkloadID == "" || r.ExpectedVolumeRevision == 0 {
		return VolumeResource{}, errors.New("complete Volume attachment intent is required")
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state string
		var rev int64
		if err := tx.QueryRow(ctx, `SELECT lifecycle_state,volume_revision FROM kim.volumes_current WHERE volume_id=$1 AND authority_source='VOLUME_RESOURCE' FOR UPDATE`, r.VolumeID).Scan(&state, &rev); err != nil || uint64(rev) != r.ExpectedVolumeRevision || state != "AVAILABLE" {
			return ErrPlacementConflict
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.volume_attachment_intents_current WHERE volume_id=$1 AND intent_state IN('REQUESTED','ATTACHED'))`, r.VolumeID).Scan(&exists); err != nil || exists {
			return ErrPlacementConflict
		}
		digest := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%s/%s", r.AttachmentIntentID, r.VolumeID, rev, r.WorkloadID, r.AttachmentID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_intent_evidence(attachment_intent_id,volume_id,volume_revision,attachment_generation,workload_id,requested_attachment_id,requested_physical_attachment_generation,intent_state,intent_digest) VALUES($1,$2,$3,1,$4,$5,1,'REQUESTED',$6)`, r.AttachmentIntentID, r.VolumeID, rev, r.WorkloadID, r.AttachmentID, digest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_intents_current(volume_id,volume_revision,attachment_intent_id,attachment_generation,workload_id,requested_attachment_id,requested_physical_attachment_generation,intent_state) VALUES($1,$2,$3,1,$4,$5,1,'REQUESTED')`, r.VolumeID, rev, r.AttachmentIntentID, r.WorkloadID, r.AttachmentID)
		return err
	})
	if err != nil {
		return VolumeResource{}, err
	}
	return GetVolumeResource(ctx, db, r.VolumeID)
}

func CancelVolumeAttachmentIntent(ctx context.Context, db TxBeginner, volumeID, intentID string, expectedRevision uint64) (VolumeResource, error) {
	if volumeID == "" || intentID == "" || expectedRevision == 0 {
		return VolumeResource{}, errors.New("exact requested Volume attachment is required")
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var workload, attachmentID string
		var generation, requestedAttachmentGeneration int64
		if err := tx.QueryRow(ctx, `SELECT workload_id,requested_attachment_id,requested_physical_attachment_generation,attachment_generation FROM kim.volume_attachment_intents_current WHERE volume_id=$1 AND volume_revision=$2 AND attachment_intent_id=$3 AND intent_state='REQUESTED' FOR UPDATE`, volumeID, expectedRevision, intentID).Scan(&workload, &attachmentID, &requestedAttachmentGeneration, &generation); err != nil {
			return ErrPlacementConflict
		}
		retiredID := intentID + ":retired"
		digest := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%s/RETIRED", retiredID, volumeID, expectedRevision, workload))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_intent_evidence(attachment_intent_id,volume_id,volume_revision,attachment_generation,workload_id,requested_attachment_id,requested_physical_attachment_generation,intent_state,intent_digest) VALUES($1,$2,$3,$4,$5,$6,$7,'RETIRED',$8)`, retiredID, volumeID, expectedRevision, generation+1, workload, attachmentID, requestedAttachmentGeneration, digest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_intents_current SET attachment_intent_id=$2,attachment_generation=$3,intent_state='RETIRED',updated_at=statement_timestamp() WHERE volume_id=$1 AND attachment_intent_id=$4`, volumeID, retiredID, generation+1, intentID)
		return err
	})
	if err != nil {
		return VolumeResource{}, err
	}
	return GetVolumeResource(ctx, db, volumeID)
}

func RequestVolumeRetirement(ctx context.Context, db TxBeginner, id string, expected uint64) (VolumeResource, error) {
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var p volumeMaterializationPlan
		var protect bool
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT v.volume_revision,v.delete_protection,m.materialization_generation,i.binding_id,i.binding_generation,i.backend_id,b.backend_generation,i.host_id,i.vg_uuid,i.backend_resource_key,bound.lv_uuid,v.size_bytes,v.source_type,COALESCE(v.source_image_id,''),COALESCE(v.source_image_revision,0),COALESCE(v.source_artifact_digest,''),i.capacity_allocation_id,i.capacity_allocation_generation FROM kim.volumes_current v JOIN kim.volume_materializations_current m ON m.volume_id=v.volume_id AND m.materialization_state='VERIFIED' JOIN kim.volume_backend_binding_intents i ON i.binding_id=m.binding_id JOIN kim.volume_backend_bindings_current bound ON bound.binding_id=i.binding_id AND bound.binding_generation=i.binding_generation AND bound.binding_state='BOUND' JOIN kim.storage_backends_current b ON b.backend_id=i.backend_id WHERE v.volume_id=$1 AND v.authority_source='VOLUME_RESOURCE' AND v.lifecycle_state='AVAILABLE' AND NOT EXISTS(SELECT 1 FROM kim.volume_attachment_intents_current a WHERE a.volume_id=v.volume_id AND a.intent_state IN('REQUESTED','ATTACHED')) AND NOT EXISTS(SELECT 1 FROM kim.local_lvm_relocation_copy_operations_current c JOIN kim.local_lvm_relocation_copy_operation_evidence e USING(copy_operation_id,copy_generation) WHERE (e.source_volume_id=v.volume_id OR e.destination_volume_id=v.volume_id) AND c.operation_state IN('PENDING','COPYING','VERIFYING','UNKNOWN')) FOR UPDATE OF v`, id).Scan(&revision, &protect, &p.MaterializationGeneration, &p.BindingID, &p.BindingGeneration, &p.BackendID, &p.BackendGeneration, &p.HostID, &p.VGUUID, &p.BackendResourceKey, &p.ExpectedLVUUID, &p.ExpectedSizeBytes, &p.SourceType, &p.SourceImageID, &p.SourceImageRevision, &p.SourceArtifactDigest, &p.AllocationID, &p.AllocationGeneration); err != nil || uint64(revision) != expected || protect {
			return ErrPlacementConflict
		}
		p.SchemaVersion = "kim.storage.volume-materialization/v1"
		p.OperationID = fmt.Sprintf("volume-materialization:%s:%d", id, p.MaterializationGeneration+1)
		p.OperationGeneration = 1
		p.OperationKind = "RETIRE"
		p.VolumeID = id
		p.VolumeRevision = uint64(revision)
		p.MaterializationGeneration++
		if err := insertVolumeMaterializationOperationTx(ctx, tx, p); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.volumes_current SET lifecycle_state='RETIRE_PENDING',updated_at=statement_timestamp() WHERE volume_id=$1`, id)
		return err
	})
	if err != nil {
		return VolumeResource{}, err
	}
	return GetVolumeResource(ctx, db, id)
}

func completeVolumeRetirementTx(ctx context.Context, tx pgx.Tx, p volumeMaterializationPlan, terminal string) error {
	var claimID string
	var bytes int64
	if err := tx.QueryRow(ctx, `SELECT capacity_claim_id,reserved_bytes FROM kim.storage_capacity_claims WHERE allocation_decision_id=$1 AND allocation_generation=$2 AND claim_state IN('RESERVED','ALLOCATED','RELEASE_PENDING') FOR UPDATE`, p.AllocationID, p.AllocationGeneration).Scan(&claimID, &bytes); err != nil {
		return ErrPlacementConflict
	}
	release := "volume-capacity-release:" + p.VolumeID + ":" + fmt.Sprint(p.AllocationGeneration)
	digest := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%s/%d", release, p.VolumeID, p.VolumeRevision, p.AllocationID, p.AllocationGeneration))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_capacity_release_evidence(release_evidence_id,volume_id,volume_revision,allocation_id,allocation_generation,capacity_claim_id,absence_terminal_evidence_id,released_bytes,release_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, release, p.VolumeID, p.VolumeRevision, p.AllocationID, p.AllocationGeneration, claimID, terminal, bytes, digest); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.storage_capacity_claims SET claim_state='RELEASED',updated_at=statement_timestamp() WHERE capacity_claim_id=$1`, claimID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_binding_intents SET binding_state='RELEASED',updated_at=statement_timestamp() WHERE binding_id=$1`, p.BindingID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.volume_backend_bindings_current SET binding_state='REVOKED',updated_at=statement_timestamp() WHERE binding_id=$1 AND binding_generation=$2`, p.BindingID, p.BindingGeneration); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE kim.volumes_current SET lifecycle_state='DELETED',updated_at=statement_timestamp() WHERE volume_id=$1 AND volume_revision=$2`, p.VolumeID, p.VolumeRevision)
	return err
}
