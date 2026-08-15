package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type LocalLVMAttachmentObservation struct {
	EvidenceID, AttachmentID, VolumeID, BindingID, HostID string
	DomainUUID, TargetDevice, ObservedLVUUID              string
	DesiredState, CommandID, VerificationID               string
	ObservationDigest, VerifierDigest, EvidenceState      string
	AttachmentGeneration, BindingGeneration               uint64
	ObservationGeneration                                 uint64
	AttemptIndex                                          uint32
	DevicePresent, DeviceIdentityMatches                  bool
	SourceIdentityMatches, HolderOpen, ReadOnly           bool
}

// AcceptLocalLVMAttachmentObservation advances current Attachment authority
// only from an existing typed Command Verification whose device and holder
// evidence matches the current BOUND Local LVM identity and Claim generation.
func AcceptLocalLVMAttachmentObservation(ctx context.Context, db TxBeginner, observation LocalLVMAttachmentObservation) error {
	if err := validateLocalLVMAttachmentObservation(observation); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, observation.HostID); err != nil {
			return err
		}
		var claimState, currentBindingState, currentLVUUID string
		err := tx.QueryRow(ctx, `
			SELECT claim.claim_state, binding.binding_state, binding.lv_uuid
			FROM kim.volume_attachments_current attachment
			JOIN kim.volume_attachment_claims claim
			  ON claim.attachment_id=attachment.attachment_id
			 AND claim.volume_id=attachment.volume_id
			JOIN kim.volume_backend_bindings_current binding
			  ON binding.binding_id=$4 AND binding.volume_id=attachment.volume_id
			JOIN kim.command_verification_evidence verification
			  ON verification.verification_id=$6
			 AND verification.command_id=$7
			 AND verification.attempt_index=$8
			 AND verification.observation_generation=$9
			 AND verification.observation_digest=$10
			 AND verification.verification_state='MATCHED'
			 AND verification.verifier_artifact_digest=$11
			JOIN kim.execution_commands execution_command
			  ON execution_command.command_id=verification.command_id
			 AND execution_command.host_id=$5
			 AND execution_command.command_type='LOCAL_LVM_VOLUME_ATTACHMENT_ENSURE'
			 AND execution_command.schema_version='kim.command.local-lvm-volume-attachment/v1'
			 AND execution_command.target_resource_id='attachment:' || $1
			WHERE attachment.attachment_id=$1 AND attachment.volume_id=$2
			  AND attachment.attachment_generation=$3
			  AND attachment.desired_host_id=$5
			  AND binding.binding_generation=$12 AND binding.binding_state='BOUND'
			  AND binding.lv_uuid=$13
			  AND verification.evidence_payload->>'attachment_id'=$1
			  AND verification.evidence_payload->>'volume_id'=$2
			  AND verification.evidence_payload->>'domain_uuid'=$14
			  AND verification.evidence_payload->>'target_device'=$15
			  AND verification.evidence_payload->>'observed_lv_uuid'=$13
			  AND verification.evidence_payload->>'desired_state'=$16
			  AND (verification.evidence_payload->>'device_present')::boolean=$17
			  AND (verification.evidence_payload->>'device_identity_matches')::boolean=$18
			  AND (verification.evidence_payload->>'source_identity_matches')::boolean=$19
			  AND (verification.evidence_payload->>'holder_open')::boolean=$20
			  AND (verification.evidence_payload->>'read_only')::boolean=$21
			FOR UPDATE OF attachment, claim, binding
		`, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration,
			observation.BindingID, observation.HostID, observation.VerificationID,
			observation.CommandID, observation.AttemptIndex, observation.ObservationGeneration,
			observation.ObservationDigest, observation.VerifierDigest,
			observation.BindingGeneration, observation.ObservedLVUUID,
			observation.DomainUUID, observation.TargetDevice, observation.DesiredState,
			observation.DevicePresent, observation.DeviceIdentityMatches,
			observation.SourceIdentityMatches, observation.HolderOpen,
			observation.ReadOnly).Scan(&claimState, &currentBindingState, &currentLVUUID)
		if err != nil || currentBindingState != "BOUND" || currentLVUUID != observation.ObservedLVUUID {
			return ErrPlacementConflict
		}
		if observation.DesiredState == "ATTACHED" {
			if !observation.DevicePresent || !observation.DeviceIdentityMatches || !observation.SourceIdentityMatches || !observation.HolderOpen || observation.ReadOnly || (claimState != "RESERVED" && claimState != "ACTIVE" && claimState != "UNKNOWN") {
				return ErrPlacementConflict
			}
		} else if observation.DevicePresent || observation.DeviceIdentityMatches || observation.SourceIdentityMatches || observation.HolderOpen || (claimState != "RESERVED" && claimState != "ACTIVE" && claimState != "RELEASE_PENDING" && claimState != "UNKNOWN" && claimState != "RELEASED") {
			return ErrPlacementConflict
		}
		var existingEvidenceID, existingState, existingDomain, existingTarget, existingLV string
		var existingObservationGeneration uint64
		err = tx.QueryRow(ctx, `
			SELECT evidence_id, attachment_state, observation_generation,
			       domain_uuid::text, target_device, observed_lv_uuid
			FROM kim.volume_attachment_observations_current
			WHERE attachment_id=$1 FOR UPDATE
		`, observation.AttachmentID).Scan(&existingEvidenceID, &existingState,
			&existingObservationGeneration, &existingDomain, &existingTarget, &existingLV)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && (existingDomain != observation.DomainUUID || existingTarget != observation.TargetDevice || existingLV != observation.ObservedLVUUID) {
			return ErrPlacementConflict
		}
		if err == nil && (observation.ObservationGeneration < existingObservationGeneration ||
			(observation.ObservationGeneration == existingObservationGeneration &&
				(existingEvidenceID != observation.EvidenceID || existingState != observation.DesiredState))) {
			return ErrPlacementConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.volume_attachment_observation_evidence (
				evidence_id, attachment_id, volume_id, attachment_generation,
				binding_id, binding_generation, host_id, domain_uuid, target_device,
				observed_lv_uuid, desired_state, device_present,
				device_identity_matches, source_identity_matches, holder_open, read_only,
				command_id, attempt_index, verification_id, observation_generation,
				observation_digest, verifier_digest, evidence_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
			ON CONFLICT (evidence_id) DO NOTHING
		`, observation.EvidenceID, observation.AttachmentID, observation.VolumeID,
			observation.AttachmentGeneration, observation.BindingID, observation.BindingGeneration,
			observation.HostID, observation.DomainUUID, observation.TargetDevice,
			observation.ObservedLVUUID, observation.DesiredState, observation.DevicePresent,
			observation.DeviceIdentityMatches, observation.SourceIdentityMatches,
			observation.HolderOpen, observation.ReadOnly, observation.CommandID,
			observation.AttemptIndex, observation.VerificationID,
			observation.ObservationGeneration, observation.ObservationDigest,
			observation.VerifierDigest, observation.EvidenceState); err != nil {
			return err
		}
		var evidenceMatches bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM kim.volume_attachment_observation_evidence
				WHERE evidence_id=$1 AND attachment_id=$2 AND volume_id=$3
				  AND attachment_generation=$4 AND binding_id=$5 AND binding_generation=$6
				  AND host_id=$7 AND domain_uuid=$8 AND target_device=$9
				  AND observed_lv_uuid=$10 AND desired_state=$11
				  AND device_present=$12 AND device_identity_matches=$13
				  AND source_identity_matches=$14 AND holder_open=$15 AND read_only=$16
				  AND command_id=$17 AND attempt_index=$18 AND verification_id=$19
				  AND observation_generation=$20 AND observation_digest=$21
				  AND verifier_digest=$22 AND evidence_state=$23
			)
		`, observation.EvidenceID, observation.AttachmentID, observation.VolumeID,
			observation.AttachmentGeneration, observation.BindingID, observation.BindingGeneration,
			observation.HostID, observation.DomainUUID, observation.TargetDevice,
			observation.ObservedLVUUID, observation.DesiredState, observation.DevicePresent,
			observation.DeviceIdentityMatches, observation.SourceIdentityMatches,
			observation.HolderOpen, observation.ReadOnly, observation.CommandID,
			observation.AttemptIndex, observation.VerificationID,
			observation.ObservationGeneration, observation.ObservationDigest,
			observation.VerifierDigest, observation.EvidenceState).Scan(&evidenceMatches); err != nil || !evidenceMatches {
			return ErrPlacementConflict
		}
		attachmentState, claimNext := observation.DesiredState, "ACTIVE"
		if observation.DesiredState == "DETACHED" {
			claimNext = "RELEASED"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.volume_attachment_observations_current (
				attachment_id, volume_id, attachment_generation, observation_generation,
				evidence_id, attachment_state, binding_id, binding_generation,
				host_id, domain_uuid, target_device, observed_lv_uuid,
				device_present, holder_open
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (attachment_id) DO UPDATE SET
				observation_generation=EXCLUDED.observation_generation,
				evidence_id=EXCLUDED.evidence_id,
				attachment_state=EXCLUDED.attachment_state,
				device_present=EXCLUDED.device_present,
				holder_open=EXCLUDED.holder_open,
				updated_at=statement_timestamp()
			WHERE kim.volume_attachment_observations_current.attachment_generation=EXCLUDED.attachment_generation
			  AND kim.volume_attachment_observations_current.observation_generation < EXCLUDED.observation_generation
		`, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration,
			observation.ObservationGeneration, observation.EvidenceID, attachmentState,
			observation.BindingID, observation.BindingGeneration, observation.HostID,
			observation.DomainUUID, observation.TargetDevice, observation.ObservedLVUUID,
			observation.DevicePresent, observation.HolderOpen); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.volume_attachments_current SET desired_state=$2 WHERE attachment_id=$1 AND attachment_generation=$3`, observation.AttachmentID, attachmentState, observation.AttachmentGeneration); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.volume_attachment_claims SET claim_state=$2 WHERE attachment_id=$1 AND attachment_generation=$3`, observation.AttachmentID, claimNext, observation.AttachmentGeneration)
		return err
	})
}

func validateLocalLVMAttachmentObservation(value LocalLVMAttachmentObservation) error {
	if value.EvidenceID == "" || value.AttachmentID == "" || value.VolumeID == "" || value.BindingID == "" || value.HostID == "" || value.DomainUUID == "" || value.TargetDevice == "" || value.ObservedLVUUID == "" || value.CommandID == "" || value.VerificationID == "" || value.AttachmentGeneration == 0 || value.BindingGeneration == 0 || value.ObservationGeneration == 0 || value.AttemptIndex == 0 || len(value.ObservationDigest) != 64 || len(value.VerifierDigest) != 64 || value.EvidenceState != "MATCHED" || (value.DesiredState != "ATTACHED" && value.DesiredState != "DETACHED") {
		return errors.New("complete MATCHED Local LVM Attachment observation is required")
	}
	return nil
}
