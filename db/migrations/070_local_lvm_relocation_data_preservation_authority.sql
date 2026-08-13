CREATE TABLE kim.local_lvm_relocation_copy_operation_evidence (
    copy_operation_id text PRIMARY KEY,
    copy_generation bigint NOT NULL CHECK(copy_generation>0),
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    source_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    source_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    source_binding_generation bigint NOT NULL CHECK(source_binding_generation>0),
    source_vg_uuid text NOT NULL,
    source_lv_uuid text NOT NULL,
    source_storage_safety_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_storage_safety_evidence(safety_evidence_id),
    source_storage_safety_digest char(64) NOT NULL CHECK(source_storage_safety_digest ~ '^[0-9a-f]{64}$'),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    destination_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    destination_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    destination_binding_generation bigint NOT NULL CHECK(destination_binding_generation>0),
    destination_vg_uuid text NOT NULL,
    destination_lv_uuid text NOT NULL,
    expected_size_bytes bigint NOT NULL CHECK(expected_size_bytes>0),
    digest_algorithm text NOT NULL CHECK(digest_algorithm='SHA-256'),
    block_profile text NOT NULL CHECK(block_profile='EXACT_BYTE_RANGE_V1'),
    copy_policy_revision bigint NOT NULL CHECK(copy_policy_revision>0),
    command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    UNIQUE(copy_operation_id,copy_generation),
    FOREIGN KEY(child_operation_id,child_generation) REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation),
    CHECK(source_host_id<>destination_host_id),
    CHECK(source_lv_uuid<>destination_lv_uuid)
);

CREATE TABLE kim.local_lvm_relocation_copy_operations_current (
    copy_operation_id text PRIMARY KEY,
    copy_generation bigint NOT NULL,
    operation_state text NOT NULL CHECK(operation_state IN ('PENDING','VERIFYING','VERIFIED','CONFLICTING','UNKNOWN')),
    latest_attempt_index integer NOT NULL DEFAULT 0 CHECK(latest_attempt_index>=0),
    response_state text NOT NULL CHECK(response_state IN ('PENDING','RECEIVED','LOST','UNKNOWN')),
    verification_id text,
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(copy_operation_id,copy_generation) REFERENCES kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation)
);

CREATE TABLE kim.local_lvm_relocation_copy_attempt_evidence (
    copy_operation_id text NOT NULL,
    copy_generation bigint NOT NULL,
    attempt_index integer NOT NULL CHECK(attempt_index>0),
    command_id text NOT NULL,
    lease_generation bigint NOT NULL CHECK(lease_generation>0),
    host_authority_generation bigint NOT NULL CHECK(host_authority_generation>0),
    session_generation bigint NOT NULL CHECK(session_generation>0),
    response_state text NOT NULL CHECK(response_state IN ('RECEIVED','LOST','UNKNOWN')),
    attempt_digest char(64) NOT NULL CHECK(attempt_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(copy_operation_id,copy_generation,attempt_index),
    FOREIGN KEY(copy_operation_id,copy_generation) REFERENCES kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation),
    FOREIGN KEY(command_id,attempt_index) REFERENCES kim.command_attempts(command_id,attempt_index)
);

CREATE TABLE kim.local_lvm_relocation_content_observation_evidence (
    content_evidence_id text PRIMARY KEY,
    copy_operation_id text NOT NULL,
    copy_generation bigint NOT NULL,
    content_role text NOT NULL CHECK(content_role IN ('SOURCE_POINT','DESTINATION')),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    lv_uuid text NOT NULL,
    size_bytes bigint NOT NULL CHECK(size_bytes>0),
    digest_algorithm text NOT NULL CHECK(digest_algorithm='SHA-256'),
    content_digest char(64) NOT NULL CHECK(content_digest ~ '^[0-9a-f]{64}$'),
    observation_generation bigint NOT NULL CHECK(observation_generation>0),
    command_id text NOT NULL,
    attempt_index integer NOT NULL CHECK(attempt_index>0),
    command_verification_id text NOT NULL REFERENCES kim.command_verification_evidence(verification_id),
    observation_digest char(64) NOT NULL CHECK(observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_artifact_digest char(64) NOT NULL CHECK(verifier_artifact_digest ~ '^[0-9a-f]{64}$'),
    content_evidence_digest char(64) NOT NULL CHECK(content_evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(copy_operation_id,copy_generation,content_role),
    FOREIGN KEY(copy_operation_id,copy_generation) REFERENCES kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation),
    FOREIGN KEY(command_id,attempt_index) REFERENCES kim.command_attempts(command_id,attempt_index)
);

CREATE TABLE kim.local_lvm_relocation_copy_verification_evidence (
    verification_id text PRIMARY KEY,
    copy_operation_id text NOT NULL,
    copy_generation bigint NOT NULL,
    source_content_evidence_id text NOT NULL REFERENCES kim.local_lvm_relocation_content_observation_evidence(content_evidence_id),
    destination_content_evidence_id text NOT NULL REFERENCES kim.local_lvm_relocation_content_observation_evidence(content_evidence_id),
    expected_size_bytes bigint NOT NULL CHECK(expected_size_bytes>0),
    digest_algorithm text NOT NULL CHECK(digest_algorithm='SHA-256'),
    source_content_digest char(64) NOT NULL CHECK(source_content_digest ~ '^[0-9a-f]{64}$'),
    destination_content_digest char(64) NOT NULL CHECK(destination_content_digest ~ '^[0-9a-f]{64}$'),
    content_identity_state text NOT NULL CHECK(content_identity_state='VERIFIED'),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(copy_operation_id,copy_generation),
    UNIQUE(verification_id,verification_digest),
    FOREIGN KEY(copy_operation_id,copy_generation) REFERENCES kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation),
    CHECK(source_content_digest=destination_content_digest)
);

CREATE TABLE kim.local_lvm_relocation_copy_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    copy_operation_id text NOT NULL,
    copy_generation bigint NOT NULL,
    verification_id text NOT NULL REFERENCES kim.local_lvm_relocation_copy_verification_evidence(verification_id),
    destination_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    destination_binding_generation bigint NOT NULL CHECK(destination_binding_generation>0),
    destination_lv_uuid text NOT NULL,
    terminal_state text NOT NULL CHECK(terminal_state='VERIFIED'),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(copy_operation_id,copy_generation),
    UNIQUE(terminal_evidence_id,terminal_digest),
    FOREIGN KEY(copy_operation_id,copy_generation) REFERENCES kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation)
);

ALTER TABLE kim.vm_materialization_relocation_authority_evidence
  ADD COLUMN local_lvm_copy_terminal_evidence_id text REFERENCES kim.local_lvm_relocation_copy_terminal_evidence(terminal_evidence_id),
  ADD COLUMN local_lvm_copy_terminal_digest char(64),
  ADD CONSTRAINT vm_materialization_relocation_copy_digest_shape CHECK(
    (local_lvm_copy_terminal_evidence_id IS NULL AND local_lvm_copy_terminal_digest IS NULL)
    OR (local_lvm_copy_terminal_evidence_id IS NOT NULL AND local_lvm_copy_terminal_digest ~ '^[0-9a-f]{64}$'));

ALTER TABLE kim.vm_image_realization_evidence
  ADD COLUMN content_origin text NOT NULL DEFAULT 'BASE_IMAGE' CHECK(content_origin IN ('BASE_IMAGE','PRESERVED_ROOT'));

ALTER TABLE kim.host_evacuation_child_verification_evidence
  ADD COLUMN source_storage_evidence_set_digest char(64),
  ADD COLUMN destination_storage_evidence_set_digest char(64),
  ADD CONSTRAINT host_evacuation_child_storage_digest_shape CHECK(
    (source_storage_state='NOT_REQUIRED' AND destination_storage_state='NOT_REQUIRED' AND source_storage_evidence_set_digest IS NULL AND destination_storage_evidence_set_digest IS NULL)
    OR (source_storage_state='SAFE' AND destination_storage_state='CURRENT' AND source_storage_evidence_set_digest ~ '^[0-9a-f]{64}$' AND destination_storage_evidence_set_digest ~ '^[0-9a-f]{64}$'));

ALTER TABLE kim.local_lvm_relocation_copy_operations_current
  ADD CONSTRAINT local_lvm_copy_current_verification_fkey FOREIGN KEY(verification_id) REFERENCES kim.local_lvm_relocation_copy_verification_evidence(verification_id),
  ADD CONSTRAINT local_lvm_copy_current_terminal_fkey FOREIGN KEY(terminal_evidence_id) REFERENCES kim.local_lvm_relocation_copy_terminal_evidence(terminal_evidence_id);

CREATE TRIGGER local_lvm_relocation_copy_operation_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_copy_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_relocation_copy_attempt_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_copy_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_relocation_content_observation_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_content_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_relocation_copy_verification_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_copy_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_relocation_copy_terminal_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_copy_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.local_lvm_relocation_copy_operation_evidence IS 'Closed planned-relocation copy authority derived from exact quiesced source and exact destination Local LVM bindings. No caller path, shell, or content-identical assertion is accepted.';
COMMENT ON TABLE kim.local_lvm_relocation_content_observation_evidence IS 'Immutable whole-device SHA-256 read-back identity; raw guest blocks are never persisted.';
COMMENT ON TABLE kim.local_lvm_relocation_copy_terminal_evidence IS 'Exact destination boot-volume prerequisite. VERIFIED copy is independent of source LV deletion or capacity reclamation.';
