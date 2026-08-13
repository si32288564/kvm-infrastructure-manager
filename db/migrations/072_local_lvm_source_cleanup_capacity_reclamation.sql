-- Local LVM is a closed consumer of the generic Backend Cleanup aggregate.
-- A materialization terminal may nominate only its exact obsolete source LV;
-- capacity remains RELEASE_PENDING until an immutable exact-UUID absence
-- observation has produced the generic cleanup terminal.

ALTER TABLE kim.backend_cleanup_operation_evidence
  DROP CONSTRAINT backend_cleanup_operation_evidence_cleanup_reason_check,
  ADD CONSTRAINT backend_cleanup_operation_evidence_cleanup_reason_check
  CHECK(cleanup_reason IN ('RECOVERY_SUPERSEDED','MATERIALIZATION_SUPERSEDED','FAILED_MATERIALIZATION','EXPLICIT_DELETE','ABORTED_MOVE'));

CREATE TABLE kim.local_lvm_source_cleanup_authority_evidence (
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL CHECK(cleanup_generation>0),
    child_terminal_id text NOT NULL REFERENCES kim.host_evacuation_child_terminal_evidence(terminal_evidence_id),
    child_terminal_digest char(64) NOT NULL CHECK(child_terminal_digest ~ '^[0-9a-f]{64}$'),
    copy_terminal_id text NOT NULL REFERENCES kim.local_lvm_relocation_copy_terminal_evidence(terminal_evidence_id),
    copy_terminal_digest char(64) NOT NULL CHECK(copy_terminal_digest ~ '^[0-9a-f]{64}$'),
    source_storage_safety_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_storage_safety_evidence(safety_evidence_id),
    source_storage_safety_digest char(64) NOT NULL CHECK(source_storage_safety_digest ~ '^[0-9a-f]{64}$'),
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    source_binding_id text NOT NULL REFERENCES kim.volume_backend_binding_intents(binding_id),
    source_binding_generation bigint NOT NULL CHECK(source_binding_generation>0),
    source_backend_id text NOT NULL REFERENCES kim.storage_backends_current(backend_id),
    source_backend_generation bigint NOT NULL CHECK(source_backend_generation>0),
    source_vg_uuid text NOT NULL,
    source_lv_uuid text NOT NULL,
    source_backend_resource_key text NOT NULL CHECK(source_backend_resource_key ~ '^kim-[0-9a-f]{32}$'),
    source_attachment_id text NOT NULL REFERENCES kim.volume_attachments_current(attachment_id),
    source_attachment_generation bigint NOT NULL CHECK(source_attachment_generation>0),
    source_capacity_claim_id text NOT NULL REFERENCES kim.storage_capacity_claims(capacity_claim_id),
    source_capacity_generation bigint NOT NULL CHECK(source_capacity_generation>0),
    reserved_bytes bigint NOT NULL CHECK(reserved_bytes>0),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    destination_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    destination_materialization_generation bigint NOT NULL CHECK(destination_materialization_generation>0),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(cleanup_operation_id,cleanup_generation),
    UNIQUE(source_binding_id,source_binding_generation),
    UNIQUE(source_capacity_claim_id),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation)
      REFERENCES kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation)
);

CREATE TABLE kim.local_lvm_source_cleanup_observation_identity_evidence (
    cleanup_evidence_id text PRIMARY KEY REFERENCES kim.backend_cleanup_observation_evidence(cleanup_evidence_id),
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL,
    expected_backend_id text NOT NULL,
    expected_backend_generation bigint NOT NULL CHECK(expected_backend_generation>0),
    expected_vg_uuid text NOT NULL,
    expected_lv_uuid text NOT NULL,
    expected_backend_resource_key text NOT NULL CHECK(expected_backend_resource_key ~ '^kim-[0-9a-f]{32}$'),
    observed_lv_uuid text,
    exact_source_lv_present boolean NOT NULL,
    foreign_replacement_present boolean NOT NULL,
    identity_digest char(64) NOT NULL CHECK(identity_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation)
      REFERENCES kim.local_lvm_source_cleanup_authority_evidence(cleanup_operation_id,cleanup_generation),
    CHECK(NOT (exact_source_lv_present AND foreign_replacement_present))
);

CREATE TABLE kim.local_lvm_capacity_reclamation_evidence (
    reclamation_evidence_id text PRIMARY KEY,
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL,
    cleanup_terminal_id text NOT NULL REFERENCES kim.backend_cleanup_terminal_evidence(cleanup_terminal_id),
    cleanup_terminal_digest char(64) NOT NULL CHECK(cleanup_terminal_digest ~ '^[0-9a-f]{64}$'),
    capacity_claim_id text NOT NULL REFERENCES kim.storage_capacity_claims(capacity_claim_id),
    backend_id text NOT NULL,
    backend_generation bigint NOT NULL CHECK(backend_generation>0),
    capacity_generation bigint NOT NULL CHECK(capacity_generation>0),
    volume_id text NOT NULL,
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    lv_uuid text NOT NULL,
    released_bytes bigint NOT NULL CHECK(released_bytes>0),
    prior_claim_state text NOT NULL CHECK(prior_claim_state='RELEASE_PENDING'),
    resulting_claim_state text NOT NULL CHECK(resulting_claim_state='RELEASED'),
    reclamation_digest char(64) NOT NULL CHECK(reclamation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(cleanup_operation_id,cleanup_generation),
    UNIQUE(capacity_claim_id),
    UNIQUE(reclamation_evidence_id,reclamation_digest),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation)
      REFERENCES kim.local_lvm_source_cleanup_authority_evidence(cleanup_operation_id,cleanup_generation)
);

CREATE TRIGGER local_lvm_source_cleanup_authority_evidence_no_update BEFORE UPDATE ON kim.local_lvm_source_cleanup_authority_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_source_cleanup_observation_identity_evidence_no_update BEFORE UPDATE ON kim.local_lvm_source_cleanup_observation_identity_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_capacity_reclamation_evidence_no_update BEFORE UPDATE ON kim.local_lvm_capacity_reclamation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.local_lvm_source_cleanup_authority_evidence IS 'Materialization producer adapter for exact obsolete Local LVM source incarnation cleanup. It carries UUID identities only; no device path, VG/LV name, shell, or argv.';
COMMENT ON TABLE kim.local_lvm_capacity_reclamation_evidence IS 'Immutable capacity release decision made only after generic cleanup terminal proves exact source LV absence. Copy/EVACUATE terminal authority is independent.';
