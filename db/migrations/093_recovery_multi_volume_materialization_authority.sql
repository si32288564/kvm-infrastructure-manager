-- Extend Recovery from the historical single rebuildable ROOT profile to a
-- bounded canonical ROOT+DATA set. ROOT retains the existing verified-image
-- rebuild semantics; DATA must be an exact content-verified derivative of the
-- safely detached source Local LVM incarnation.

ALTER TABLE kim.local_lvm_relocation_copy_operation_evidence
    ALTER COLUMN child_operation_id DROP NOT NULL,
    ALTER COLUMN child_generation DROP NOT NULL,
    ALTER COLUMN source_storage_safety_evidence_id DROP NOT NULL,
    ALTER COLUMN source_storage_safety_digest DROP NOT NULL,
    ADD COLUMN recovery_operation_id text REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    ADD COLUMN recovery_storage_safety_proof_id text REFERENCES kim.storage_safety_proof_evidence(proof_id),
    ADD COLUMN recovery_storage_safety_proof_digest char(64),
    ADD COLUMN recovery_source_attachment_evidence_id text REFERENCES kim.volume_attachment_observation_evidence(evidence_id),
    ADD COLUMN recovery_source_attachment_observation_digest char(64),
    ADD CONSTRAINT local_lvm_copy_authority_consumer_check CHECK(
        (child_operation_id IS NOT NULL AND child_generation IS NOT NULL
          AND source_storage_safety_evidence_id IS NOT NULL AND source_storage_safety_digest IS NOT NULL
          AND recovery_operation_id IS NULL AND recovery_storage_safety_proof_id IS NULL
          AND recovery_storage_safety_proof_digest IS NULL AND recovery_source_attachment_evidence_id IS NULL
          AND recovery_source_attachment_observation_digest IS NULL)
        OR
        (child_operation_id IS NULL AND child_generation IS NULL
          AND source_storage_safety_evidence_id IS NULL AND source_storage_safety_digest IS NULL
          AND recovery_operation_id IS NOT NULL AND recovery_storage_safety_proof_id IS NOT NULL
          AND recovery_storage_safety_proof_digest ~ '^[0-9a-f]{64}$'
          AND recovery_source_attachment_evidence_id IS NOT NULL
          AND recovery_source_attachment_observation_digest ~ '^[0-9a-f]{64}$')),
    ADD CONSTRAINT local_lvm_copy_recovery_data_only CHECK(
        recovery_operation_id IS NULL OR (volume_ordinal=1 AND device_role='DATA')),
    ADD UNIQUE(recovery_operation_id,volume_ordinal);

CREATE TABLE kim.recovery_materialization_volume_evidence (
    materialization_id text NOT NULL REFERENCES kim.recovery_materialization_evidence(materialization_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal BETWEEN 0 AND 1),
    device_role text NOT NULL CHECK(device_role IN('ROOT','DATA')),
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    volume_revision bigint NOT NULL CHECK(volume_revision>0),
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    lv_uuid text NOT NULL,
    attachment_id text NOT NULL REFERENCES kim.volume_attachments_current(attachment_id),
    attachment_generation bigint NOT NULL CHECK(attachment_generation>0),
    content_authority_kind text NOT NULL CHECK(content_authority_kind IN('BASE_IMAGE_REBUILD','LOCAL_LVM_COPY')),
    content_authority_id text NOT NULL,
    content_authority_digest char(64) NOT NULL CHECK(content_authority_digest ~ '^[0-9a-f]{64}$'),
    member_digest char(64) NOT NULL CHECK(member_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(materialization_id,volume_ordinal),
    UNIQUE(materialization_id,volume_id),
    UNIQUE(materialization_id,attachment_id),
    CHECK((volume_ordinal=0 AND device_role='ROOT' AND content_authority_kind='BASE_IMAGE_REBUILD')
       OR (volume_ordinal=1 AND device_role='DATA' AND content_authority_kind='LOCAL_LVM_COPY'))
);

ALTER TABLE kim.recovery_materialization_evidence
    ADD COLUMN volume_count integer NOT NULL DEFAULT 1 CHECK(volume_count BETWEEN 1 AND 2),
    ADD COLUMN volume_evidence_set_digest char(64),
    ADD CONSTRAINT recovery_materialization_volume_set_digest_shape CHECK(
        volume_evidence_set_digest IS NULL OR volume_evidence_set_digest ~ '^[0-9a-f]{64}$');

CREATE TABLE kim.recovery_storage_volume_verification_evidence (
    verification_id text NOT NULL REFERENCES kim.recovery_verification_evidence(verification_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal BETWEEN 0 AND 1),
    device_role text NOT NULL CHECK(device_role IN('ROOT','DATA')),
    materialization_id text NOT NULL,
    volume_id text NOT NULL,
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    lv_uuid text NOT NULL,
    attachment_id text NOT NULL,
    attachment_generation bigint NOT NULL CHECK(attachment_generation>0),
    attachment_evidence_id text NOT NULL REFERENCES kim.volume_attachment_observation_evidence(evidence_id),
    attachment_observation_generation bigint NOT NULL CHECK(attachment_observation_generation>0),
    member_digest char(64) NOT NULL CHECK(member_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(verification_id,volume_ordinal),
    FOREIGN KEY(materialization_id,volume_ordinal)
      REFERENCES kim.recovery_materialization_volume_evidence(materialization_id,volume_ordinal),
    CHECK((volume_ordinal=0 AND device_role='ROOT') OR (volume_ordinal=1 AND device_role='DATA'))
);

ALTER TABLE kim.recovery_verification_evidence
    ADD COLUMN volume_count integer NOT NULL DEFAULT 1 CHECK(volume_count BETWEEN 1 AND 2),
    ADD COLUMN storage_evidence_set_digest char(64),
    ADD CONSTRAINT recovery_verification_storage_set_digest_shape CHECK(
        storage_evidence_set_digest IS NULL OR storage_evidence_set_digest ~ '^[0-9a-f]{64}$');

CREATE TRIGGER recovery_materialization_volume_evidence_no_update BEFORE UPDATE ON kim.recovery_materialization_volume_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_storage_volume_verification_evidence_no_update BEFORE UPDATE ON kim.recovery_storage_volume_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.recovery_materialization_volume_evidence IS 'Canonical bounded Recovery Volume set. ROOT is rebuilt only from the exact verified Image revision; DATA is admitted only from an exact VERIFIED Local LVM copy terminal derived from Recovery storage safety.';
COMMENT ON TABLE kim.recovery_storage_volume_verification_evidence IS 'Per-Volume destination ATTACHED read-back consumed as a complete set by Recovery verification and terminal revalidation.';
