-- Phase 3 VM aggregate ROOT + DATA Volume authority.  Logical Volume roles
-- remain desired authority; Admission attachment and backend incarnation are
-- immutable runtime evidence.  The first qualified bound is 1 ROOT + 1 DATA.
CREATE TABLE kim.vm_aggregate_volume_binding_evidence (
    binding_evidence_id text PRIMARY KEY,
    aggregate_admission_binding_evidence_id text NOT NULL REFERENCES kim.vm_aggregate_admission_binding_evidence(binding_evidence_id),
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal BETWEEN 0 AND 1),
    device_role text NOT NULL CHECK(device_role IN('ROOT','DATA')),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    requested_attachment_intent_id text NOT NULL REFERENCES kim.volume_attachment_intent_evidence(attachment_intent_id),
    attached_attachment_intent_id text NOT NULL REFERENCES kim.volume_attachment_intent_evidence(attachment_intent_id),
    attached_attachment_generation bigint NOT NULL CHECK(attached_attachment_generation > 0),
    physical_attachment_id text NOT NULL,
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL,
    backend_binding_id text NOT NULL,
    backend_binding_generation bigint NOT NULL CHECK(backend_binding_generation > 0),
    binding_digest char(64) NOT NULL CHECK(binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation,volume_ordinal),
    UNIQUE(operation_id,operation_generation,volume_id),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision),
    CHECK((volume_ordinal=0 AND device_role='ROOT') OR (volume_ordinal=1 AND device_role='DATA'))
);

CREATE TABLE kim.vm_aggregate_storage_volume_verification_evidence (
    verification_id text NOT NULL REFERENCES kim.vm_aggregate_verification_evidence(verification_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal BETWEEN 0 AND 1),
    volume_binding_evidence_id text NOT NULL REFERENCES kim.vm_aggregate_volume_binding_evidence(binding_evidence_id),
    device_role text NOT NULL CHECK(device_role IN('ROOT','DATA')),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    admission_id text NOT NULL,
    host_id text NOT NULL,
    physical_attachment_id text NOT NULL,
    backend_binding_id text NOT NULL,
    backend_binding_generation bigint NOT NULL CHECK(backend_binding_generation > 0),
    materialization_terminal_evidence_id text NOT NULL REFERENCES kim.volume_materialization_terminal_evidence(terminal_evidence_id),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(verification_id,volume_ordinal),
    UNIQUE(verification_id,volume_id),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision),
    CHECK((volume_ordinal=0 AND device_role='ROOT') OR (volume_ordinal=1 AND device_role='DATA'))
);

CREATE TRIGGER vm_aggregate_volume_binding_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_volume_binding_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_aggregate_storage_volume_verification_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_storage_volume_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_aggregate_volume_binding_evidence IS 'Exact logical ROOT/DATA Volume dependency to Final Admission attachment and backend binding incarnation.';
COMMENT ON TABLE kim.vm_aggregate_storage_volume_verification_evidence IS 'Exact current VERIFIED Volume materialization and attachment set consumed by VM aggregate verification.';
