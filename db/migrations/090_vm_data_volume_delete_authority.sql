-- Phase 3 verified deletion for the zero-Port ROOT+DATA profile. Logical
-- Volumes, capacity allocations, backend materializations and data remain;
-- only the exact VM attachment incarnations are retired.
CREATE TABLE kim.vm_delete_data_volume_operation_evidence (
    delete_operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL CHECK(operation_generation=1),
    vm_id uuid NOT NULL,
    retire_vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal=1),
    device_role text NOT NULL CHECK(device_role='DATA'),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    attachment_intent_id text NOT NULL REFERENCES kim.volume_attachment_intent_evidence(attachment_intent_id),
    attachment_generation bigint NOT NULL CHECK(attachment_generation>0),
    physical_attachment_id text NOT NULL,
    physical_attachment_generation bigint NOT NULL CHECK(physical_attachment_generation>0),
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL,
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_operation_evidence(delete_operation_id,operation_generation),
    FOREIGN KEY(vm_id,retire_vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision)
);

CREATE TABLE kim.vm_delete_data_storage_absence_evidence (
    absence_evidence_id text PRIMARY KEY,
    delete_operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    domain_absence_evidence_id text NOT NULL REFERENCES kim.vm_delete_domain_absence_evidence(absence_evidence_id),
    attachment_observation_evidence_id text NOT NULL UNIQUE REFERENCES kim.volume_attachment_observation_evidence(evidence_id),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    attachment_id text NOT NULL,
    attachment_generation bigint NOT NULL,
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL,
    absence_digest char(64) NOT NULL CHECK(absence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_data_volume_operation_evidence(delete_operation_id,operation_generation),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision)
);

ALTER TABLE kim.vm_delete_terminal_evidence
    ADD COLUMN data_storage_absence_evidence_id text UNIQUE REFERENCES kim.vm_delete_data_storage_absence_evidence(absence_evidence_id);

CREATE TRIGGER vm_delete_data_volume_operation_evidence_no_update BEFORE UPDATE ON kim.vm_delete_data_volume_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_delete_data_storage_absence_evidence_no_update BEFORE UPDATE ON kim.vm_delete_data_storage_absence_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_delete_data_volume_operation_evidence IS 'Exact DATA logical Volume and runtime attachment/backend incarnation snapshotted by VM delete authority.';
COMMENT ON TABLE kim.vm_delete_data_storage_absence_evidence IS 'Verified DATA attachment absence consumed by VM delete terminal; it is not Volume deletion or capacity release authority.';
