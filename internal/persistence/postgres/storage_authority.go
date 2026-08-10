package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type LocalLVMFoundation struct {
	BackendID, HostID, VGUUID, BackendState, CapabilityState, SupportTier   string
	BackendGeneration, HostCapabilityGeneration                             uint64
	StorageClassID, ClassState                                              string
	StorageClassRevision, FencingPolicyRevision                             uint64
	ThinProvisioning, EncryptionRequired                                    bool
	CapacityObservationID, CapacityState, HealthState                       string
	CapacityGeneration                                                      uint64
	TotalBytes, ObservedFreeBytes, ExternalOrUnknownBytes, HardReserveBytes uint64
	ObservedAt                                                              time.Time
}

type LocalLVMBindingObservation struct {
	EvidenceID, BindingID, VolumeID, BackendID, HostID, VGUUID, LVUUID string
	BackendResourceKey, CommandID, VerificationID                      string
	ObservationDigest, VerifierDigest                                  string
	BindingGeneration, ObservationGeneration                           uint64
	AttemptIndex                                                       uint32
	ObservedSizeBytes                                                  uint64
	EvidenceState                                                      string
}

// AcceptLocalLVMBindingObservation turns typed read-back into current binding
// authority only after re-reading the complete Final Admission intent. It does
// not infer an LV identity from its name and does not accept conflicting,
// unknown, or not-applied evidence as BOUND.
func AcceptLocalLVMBindingObservation(ctx context.Context, db TxBeginner, observation LocalLVMBindingObservation) error {
	if err := validateLocalLVMBindingObservation(observation); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, observation.HostID); err != nil {
			return err
		}
		var volumeSize uint64
		var currentState string
		err := tx.QueryRow(ctx, `
			SELECT volume.size_bytes, intent.binding_state
			FROM kim.volume_backend_binding_intents intent
			JOIN kim.volumes_current volume ON volume.volume_id=intent.volume_id
			JOIN kim.storage_backends_current backend ON backend.backend_id=intent.backend_id
			JOIN kim.command_verification_evidence verification
			  ON verification.verification_id=$8
			 AND verification.command_id=$9
			 AND verification.attempt_index=$10
			 AND verification.observation_generation=$11
			 AND verification.observation_digest=$12
			 AND verification.verification_state='MATCHED'
			 AND verification.verifier_artifact_digest=$13
			WHERE intent.binding_id=$1 AND intent.volume_id=$2
			  AND intent.binding_generation=$3 AND intent.backend_id=$4
			  AND intent.host_id=$5 AND intent.vg_uuid=$6
			  AND intent.backend_resource_key=$7
			  AND verification.evidence_payload->>'vg_uuid'=$6
			  AND verification.evidence_payload->>'observed_vg_uuid'=$6
			  AND verification.evidence_payload->>'observed_lv_uuid'=$14
			  AND verification.evidence_payload->>'backend_resource_key'=$7
			  AND (verification.evidence_payload->>'observed_size_bytes')::bigint=volume.size_bytes
			  AND backend.lifecycle_state IN ('ACTIVE','DRAINING')
			FOR UPDATE OF intent, volume, backend
		`, observation.BindingID, observation.VolumeID, observation.BindingGeneration,
			observation.BackendID, observation.HostID, observation.VGUUID,
			observation.BackendResourceKey, observation.VerificationID,
			observation.CommandID, observation.AttemptIndex,
			observation.ObservationGeneration, observation.ObservationDigest,
			observation.VerifierDigest, observation.LVUUID).Scan(&volumeSize, &currentState)
		if err != nil {
			return ErrPlacementConflict
		}
		if volumeSize != observation.ObservedSizeBytes || observation.EvidenceState != "MATCHED" || currentState == "RELEASED" || currentState == "RELEASE_PENDING" {
			return ErrPlacementConflict
		}
		var currentLVUUID string
		err = tx.QueryRow(ctx, `
			SELECT lv_uuid FROM kim.volume_backend_bindings_current
			WHERE binding_id=$1 FOR UPDATE
		`, observation.BindingID).Scan(&currentLVUUID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && currentLVUUID != observation.LVUUID {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.volume_backend_binding_evidence (
				evidence_id, binding_id, volume_id, binding_generation, backend_id,
				host_id, vg_uuid, lv_uuid, backend_resource_key, observed_size_bytes,
				command_id, attempt_index, verification_id, observation_generation,
				observation_digest, verifier_digest, evidence_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT (evidence_id) DO NOTHING
		`, observation.EvidenceID, observation.BindingID, observation.VolumeID,
			observation.BindingGeneration, observation.BackendID, observation.HostID,
			observation.VGUUID, observation.LVUUID, observation.BackendResourceKey,
			observation.ObservedSizeBytes, observation.CommandID, observation.AttemptIndex,
			observation.VerificationID, observation.ObservationGeneration, observation.ObservationDigest,
			observation.VerifierDigest, observation.EvidenceState); err != nil {
			return err
		}
		var recordedBindingID, recordedLVUUID, recordedDigest string
		var recordedGeneration, recordedObservationGeneration uint64
		if err := tx.QueryRow(ctx, `
			SELECT binding_id, binding_generation, observation_generation, lv_uuid, observation_digest
			FROM kim.volume_backend_binding_evidence WHERE evidence_id=$1
		`, observation.EvidenceID).Scan(&recordedBindingID, &recordedGeneration,
			&recordedObservationGeneration, &recordedLVUUID, &recordedDigest); err != nil {
			return err
		}
		if recordedBindingID != observation.BindingID || recordedGeneration != observation.BindingGeneration || recordedObservationGeneration != observation.ObservationGeneration || recordedLVUUID != observation.LVUUID || recordedDigest != observation.ObservationDigest {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.volume_backend_bindings_current (
				binding_id, volume_id, binding_generation, observation_generation, evidence_id, binding_state,
				host_id, vg_uuid, lv_uuid, backend_resource_key
			) VALUES ($1,$2,$3,$4,$5,'BOUND',$6,$7,$8,$9)
			ON CONFLICT (binding_id) DO UPDATE SET
				volume_id=EXCLUDED.volume_id,
				binding_generation=EXCLUDED.binding_generation,
				observation_generation=EXCLUDED.observation_generation,
				evidence_id=EXCLUDED.evidence_id,
				binding_state='BOUND',
				host_id=EXCLUDED.host_id,
				vg_uuid=EXCLUDED.vg_uuid,
				lv_uuid=EXCLUDED.lv_uuid,
				backend_resource_key=EXCLUDED.backend_resource_key,
				updated_at=statement_timestamp()
			WHERE kim.volume_backend_bindings_current.binding_generation < EXCLUDED.binding_generation
			   OR (kim.volume_backend_bindings_current.binding_generation = EXCLUDED.binding_generation
			       AND kim.volume_backend_bindings_current.observation_generation < EXCLUDED.observation_generation)
		`, observation.BindingID, observation.VolumeID, observation.BindingGeneration,
			observation.ObservationGeneration, observation.EvidenceID, observation.HostID, observation.VGUUID,
			observation.LVUUID, observation.BackendResourceKey); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE kim.volume_backend_binding_intents
			SET binding_state='BOUND', observed_lv_uuid=$2
			WHERE binding_id=$1 AND binding_generation=$3
		`, observation.BindingID, observation.LVUUID, observation.BindingGeneration)
		return err
	})
}

func validateLocalLVMBindingObservation(value LocalLVMBindingObservation) error {
	if value.EvidenceID == "" || value.BindingID == "" || value.VolumeID == "" || value.BackendID == "" || value.HostID == "" || value.VGUUID == "" || value.LVUUID == "" || value.BackendResourceKey == "" || value.CommandID == "" || value.VerificationID == "" || value.BindingGeneration == 0 || value.AttemptIndex == 0 || value.ObservationGeneration == 0 || value.ObservedSizeBytes == 0 || len(value.ObservationDigest) != 64 || len(value.VerifierDigest) != 64 {
		return errors.New("complete Local LVM binding observation is required")
	}
	if value.EvidenceState != "MATCHED" {
		return errors.New("only MATCHED Local LVM evidence may establish a current binding")
	}
	return nil
}

// RegisterLocalLVMFoundation records current Local LVM backend/class authority
// and immutable capacity observation. It never creates or changes an LVM VG/LV.
func RegisterLocalLVMFoundation(ctx context.Context, db TxBeginner, foundation LocalLVMFoundation) error {
	if err := validateLocalLVMFoundation(foundation); err != nil {
		return err
	}
	classDigest, err := canonicalStorageDigest(map[string]any{
		"storage_class_id": foundation.StorageClassID, "revision": foundation.StorageClassRevision,
		"backend_type": "LOCAL_LVM", "locality": "HOST_LOCAL", "access_modes": []string{"SINGLE_WRITER"},
		"thin_provisioning": foundation.ThinProvisioning, "encryption_required": foundation.EncryptionRequired,
		"fencing_policy_revision": foundation.FencingPolicyRevision,
	})
	if err != nil {
		return err
	}
	observationDigest, err := canonicalStorageDigest(map[string]any{
		"backend_id": foundation.BackendID, "capacity_generation": foundation.CapacityGeneration,
		"host_capability_generation": foundation.HostCapabilityGeneration,
		"total_bytes":                foundation.TotalBytes, "observed_free_bytes": foundation.ObservedFreeBytes,
		"external_or_unknown_bytes": foundation.ExternalOrUnknownBytes,
		"hard_reserve_bytes":        foundation.HardReserveBytes, "health_state": foundation.HealthState,
	})
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, foundation.HostID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "storage-backend/"+foundation.BackendID); err != nil {
			return err
		}
		backendTag, err := tx.Exec(ctx, `
			INSERT INTO kim.storage_backends_current (
				backend_id, backend_type, backend_generation, lifecycle_state,
				host_id, vg_uuid, capability_generation, capability_state, support_tier
			) VALUES ($1,'LOCAL_LVM',$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (backend_id) DO UPDATE SET
				backend_generation=EXCLUDED.backend_generation,
				lifecycle_state=EXCLUDED.lifecycle_state,
				host_id=EXCLUDED.host_id,
				vg_uuid=EXCLUDED.vg_uuid,
				capability_generation=EXCLUDED.capability_generation,
				capability_state=EXCLUDED.capability_state,
				support_tier=EXCLUDED.support_tier,
				updated_at=statement_timestamp()
			WHERE kim.storage_backends_current.backend_generation < EXCLUDED.backend_generation
		`, foundation.BackendID, foundation.BackendGeneration, foundation.BackendState,
			foundation.HostID, foundation.VGUUID, foundation.HostCapabilityGeneration,
			foundation.CapabilityState, foundation.SupportTier)
		if err != nil {
			return err
		}
		if backendTag.RowsAffected() != 1 {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.storage_class_revision_evidence (
				storage_class_id, class_revision, allowed_backend_type, locality,
				access_modes, thin_provisioning, encryption_required,
				fencing_policy_revision, class_digest
			) VALUES ($1,$2,'LOCAL_LVM','HOST_LOCAL',ARRAY['SINGLE_WRITER']::text[],$3,$4,$5,$6)
			ON CONFLICT (storage_class_id, class_revision) DO NOTHING
		`, foundation.StorageClassID, foundation.StorageClassRevision,
			foundation.ThinProvisioning, foundation.EncryptionRequired,
			foundation.FencingPolicyRevision, classDigest); err != nil {
			return err
		}
		var recordedClassDigest string
		if err := tx.QueryRow(ctx, `
			SELECT class_digest
			FROM kim.storage_class_revision_evidence
			WHERE storage_class_id=$1 AND class_revision=$2
		`, foundation.StorageClassID, foundation.StorageClassRevision).Scan(&recordedClassDigest); err != nil {
			return err
		}
		if recordedClassDigest != classDigest {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.storage_classes_current (
				storage_class_id, class_revision, lifecycle_state
			) VALUES ($1,$2,$3)
			ON CONFLICT (storage_class_id) DO UPDATE SET
				class_revision=EXCLUDED.class_revision,
				lifecycle_state=EXCLUDED.lifecycle_state,
				updated_at=statement_timestamp()
			WHERE kim.storage_classes_current.class_revision < EXCLUDED.class_revision
		`, foundation.StorageClassID, foundation.StorageClassRevision, foundation.ClassState); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.storage_capacity_observation_evidence (
				observation_id, backend_id, capacity_generation, host_capability_generation,
				total_bytes, observed_free_bytes, external_or_unknown_bytes,
				hard_reserve_bytes, health_state, observation_digest, observed_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, foundation.CapacityObservationID, foundation.BackendID,
			foundation.CapacityGeneration, foundation.HostCapabilityGeneration,
			foundation.TotalBytes, foundation.ObservedFreeBytes,
			foundation.ExternalOrUnknownBytes, foundation.HardReserveBytes,
			foundation.HealthState, observationDigest, foundation.ObservedAt); err != nil {
			return err
		}
		capacityTag, err := tx.Exec(ctx, `
			INSERT INTO kim.storage_capacity_projections_current (
				backend_id, capacity_generation, observation_id, projection_state
			) VALUES ($1,$2,$3,$4)
			ON CONFLICT (backend_id) DO UPDATE SET
				capacity_generation=EXCLUDED.capacity_generation,
				observation_id=EXCLUDED.observation_id,
				projection_state=EXCLUDED.projection_state,
				updated_at=statement_timestamp()
			WHERE kim.storage_capacity_projections_current.capacity_generation < EXCLUDED.capacity_generation
		`, foundation.BackendID, foundation.CapacityGeneration,
			foundation.CapacityObservationID, foundation.CapacityState)
		if err != nil {
			return err
		}
		if capacityTag.RowsAffected() != 1 {
			return ErrPlacementConflict
		}
		return nil
	})
}

func validateLocalLVMFoundation(foundation LocalLVMFoundation) error {
	if foundation.BackendID == "" || foundation.HostID == "" || foundation.VGUUID == "" || foundation.BackendGeneration == 0 || foundation.HostCapabilityGeneration == 0 || foundation.StorageClassID == "" || foundation.StorageClassRevision == 0 || foundation.FencingPolicyRevision == 0 || foundation.CapacityObservationID == "" || foundation.CapacityGeneration == 0 || foundation.TotalBytes == 0 || foundation.ObservedAt.IsZero() {
		return errors.New("complete Local LVM foundation authority is required")
	}
	if foundation.ObservedFreeBytes > foundation.TotalBytes || foundation.ExternalOrUnknownBytes > foundation.TotalBytes || foundation.HardReserveBytes >= foundation.TotalBytes {
		return errors.New("invalid Local LVM capacity observation")
	}
	if foundation.BackendState != "ACTIVE" && foundation.BackendState != "DRAINING" && foundation.BackendState != "DISABLED" {
		return errors.New("invalid Storage Backend state")
	}
	if foundation.CapabilityState != "CURRENT" && foundation.CapabilityState != "STALE" && foundation.CapabilityState != "UNKNOWN" && foundation.CapabilityState != "BLOCKED" {
		return errors.New("invalid Storage capability state")
	}
	if foundation.SupportTier != "VALIDATED" && foundation.SupportTier != "SUPPORTED" && foundation.SupportTier != "EXPERIMENTAL" {
		return errors.New("invalid Storage support tier")
	}
	if foundation.ClassState != "ACTIVE" && foundation.ClassState != "DEPRECATED" && foundation.ClassState != "DISABLED" {
		return errors.New("invalid Storage Class state")
	}
	if foundation.CapacityState != "CURRENT" && foundation.CapacityState != "STALE" && foundation.CapacityState != "UNKNOWN" && foundation.CapacityState != "BLOCKED" {
		return errors.New("invalid Storage capacity projection state")
	}
	if foundation.HealthState != "HEALTHY" && foundation.HealthState != "DEGRADED" && foundation.HealthState != "UNTRUSTED" && foundation.HealthState != "UNKNOWN" {
		return errors.New("invalid Storage health state")
	}
	return nil
}

func canonicalStorageDigest(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
