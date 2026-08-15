-- Extend planned Local LVM relocation from the historical single ROOT proof to
-- a bounded canonical ROOT+DATA evidence set.  Existing ROOT evidence remains
-- valid and immutable; new producers must bind every exact Volume incarnation.
CREATE TABLE kim.host_evacuation_source_storage_volume_safety_evidence (
    safety_member_evidence_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    root_storage_safety_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_storage_safety_evidence(safety_evidence_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal BETWEEN 0 AND 1),
    device_role text NOT NULL CHECK(device_role IN('ROOT','DATA')),
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    attachment_id text NOT NULL REFERENCES kim.volume_attachments_current(attachment_id),
    attachment_generation bigint NOT NULL CHECK(attachment_generation>0),
    lv_uuid text NOT NULL,
    observation_evidence_id text NOT NULL REFERENCES kim.volume_attachment_observation_evidence(evidence_id),
    observation_generation bigint NOT NULL CHECK(observation_generation>0),
    safety_state text NOT NULL CHECK(safety_state='SAFE'),
    safety_member_digest char(64) NOT NULL CHECK(safety_member_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation,volume_ordinal),
    UNIQUE(child_operation_id,child_generation,volume_id),
    FOREIGN KEY(child_operation_id,child_generation) REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation),
    CHECK((volume_ordinal=0 AND device_role='ROOT') OR (volume_ordinal=1 AND device_role='DATA'))
);

CREATE TABLE kim.host_evacuation_source_storage_safety_set_evidence (
    safety_set_evidence_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    root_storage_safety_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_storage_safety_evidence(safety_evidence_id),
    volume_count integer NOT NULL CHECK(volume_count BETWEEN 1 AND 2),
    member_set_digest char(64) NOT NULL CHECK(member_set_digest ~ '^[0-9a-f]{64}$'),
    safety_state text NOT NULL CHECK(safety_state='SAFE'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    FOREIGN KEY(child_operation_id,child_generation) REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation)
);

ALTER TABLE kim.local_lvm_relocation_copy_operation_evidence
    DROP CONSTRAINT local_lvm_relocation_copy_ope_child_operation_id_child_gene_key,
    ADD COLUMN volume_ordinal integer NOT NULL DEFAULT 0 CHECK(volume_ordinal BETWEEN 0 AND 1),
    ADD COLUMN device_role text NOT NULL DEFAULT 'ROOT' CHECK(device_role IN('ROOT','DATA')),
    ADD COLUMN source_volume_safety_evidence_id text REFERENCES kim.host_evacuation_source_storage_volume_safety_evidence(safety_member_evidence_id),
    ADD COLUMN source_volume_safety_digest char(64),
    ADD CONSTRAINT local_lvm_copy_volume_safety_digest_shape CHECK(
        (source_volume_safety_evidence_id IS NULL AND source_volume_safety_digest IS NULL)
        OR (source_volume_safety_evidence_id IS NOT NULL AND source_volume_safety_digest ~ '^[0-9a-f]{64}$')),
    ADD CONSTRAINT local_lvm_copy_volume_role_matches_ordinal CHECK(
        (volume_ordinal=0 AND device_role='ROOT') OR (volume_ordinal=1 AND device_role='DATA')),
    ADD UNIQUE(child_operation_id,child_generation,volume_ordinal);

CREATE TABLE kim.vm_materialization_relocation_volume_evidence (
    relocation_authority_id text NOT NULL REFERENCES kim.vm_materialization_relocation_authority_evidence(relocation_authority_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal BETWEEN 0 AND 1),
    device_role text NOT NULL CHECK(device_role IN('ROOT','DATA')),
    source_volume_safety_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_storage_volume_safety_evidence(safety_member_evidence_id),
    source_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    source_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    source_binding_generation bigint NOT NULL CHECK(source_binding_generation>0),
    source_lv_uuid text NOT NULL,
    destination_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    destination_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    destination_binding_generation bigint NOT NULL CHECK(destination_binding_generation>0),
    destination_lv_uuid text NOT NULL,
    copy_terminal_evidence_id text NOT NULL REFERENCES kim.local_lvm_relocation_copy_terminal_evidence(terminal_evidence_id),
    copy_terminal_digest char(64) NOT NULL CHECK(copy_terminal_digest ~ '^[0-9a-f]{64}$'),
    member_digest char(64) NOT NULL CHECK(member_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(relocation_authority_id,volume_ordinal),
    UNIQUE(relocation_authority_id,source_volume_id),
    UNIQUE(relocation_authority_id,destination_volume_id),
    CHECK((volume_ordinal=0 AND device_role='ROOT') OR (volume_ordinal=1 AND device_role='DATA'))
);

ALTER TABLE kim.vm_materialization_relocation_authority_evidence
    ADD COLUMN volume_count integer NOT NULL DEFAULT 1 CHECK(volume_count BETWEEN 1 AND 2),
    ADD COLUMN storage_evidence_set_digest char(64),
    ADD CONSTRAINT vm_relocation_storage_set_digest_shape CHECK(
        storage_evidence_set_digest IS NULL OR storage_evidence_set_digest ~ '^[0-9a-f]{64}$');

ALTER TABLE kim.vm_aggregate_mobility_association_evidence
    ADD COLUMN volume_count integer NOT NULL DEFAULT 1 CHECK(volume_count BETWEEN 1 AND 2),
    ADD COLUMN volume_evidence_set_digest char(64),
    ADD CONSTRAINT vm_aggregate_mobility_volume_set_digest_shape CHECK(
        volume_evidence_set_digest IS NULL OR volume_evidence_set_digest ~ '^[0-9a-f]{64}$');

CREATE TRIGGER host_evacuation_source_storage_volume_safety_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_source_storage_volume_safety_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_source_storage_safety_set_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_source_storage_safety_set_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_materialization_relocation_volume_evidence_no_update BEFORE UPDATE ON kim.vm_materialization_relocation_volume_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_evacuation_source_storage_volume_safety_evidence IS 'Per-Volume planned SHUTOFF/no-holder proof. ROOT and DATA identities are derived from the immutable workload snapshot and exact current attachment incarnation.';
COMMENT ON TABLE kim.host_evacuation_source_storage_safety_set_evidence IS 'Canonical complete ROOT+DATA source safety set; no member may be inferred from another Volume.';
COMMENT ON TABLE kim.vm_materialization_relocation_volume_evidence IS 'Exact per-Volume copy terminal and destination binding consumed by a generic VM relocation authority.';
