CREATE TABLE kim.host_evacuation_source_storage_safety_evidence (
    safety_evidence_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    quiescence_evidence_id text NOT NULL UNIQUE REFERENCES kim.planned_source_quiescence_evidence(quiescence_evidence_id),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    root_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    root_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    root_binding_generation bigint NOT NULL CHECK(root_binding_generation>0),
    root_attachment_id text NOT NULL REFERENCES kim.volume_attachments_current(attachment_id),
    root_attachment_generation bigint NOT NULL CHECK(root_attachment_generation>0),
    root_observation_evidence_id text NOT NULL REFERENCES kim.volume_attachment_observation_evidence(evidence_id),
    root_observation_generation bigint NOT NULL CHECK(root_observation_generation>0),
    power_observation_evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    power_observation_generation bigint NOT NULL CHECK(power_observation_generation>0),
    safety_state text NOT NULL CHECK(safety_state='SAFE'),
    safety_digest char(64) NOT NULL CHECK(safety_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    UNIQUE(safety_evidence_id,safety_digest),
    FOREIGN KEY(child_operation_id,child_generation) REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation)
);

CREATE TABLE kim.host_evacuation_source_placement_release_evidence (
    release_evidence_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_compute_allocation_id text NOT NULL REFERENCES kim.compute_allocation_claims(allocation_id),
    source_storage_safety_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_storage_safety_evidence(safety_evidence_id),
    release_digest char(64) NOT NULL CHECK(release_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    FOREIGN KEY(child_operation_id,child_generation) REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation)
);

CREATE TABLE kim.vm_materialization_relocation_authority_evidence (
    relocation_authority_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    source_quiescence_evidence_id text NOT NULL REFERENCES kim.planned_source_quiescence_evidence(quiescence_evidence_id),
    source_storage_safety_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_storage_safety_evidence(safety_evidence_id),
    source_placement_release_evidence_id text NOT NULL REFERENCES kim.host_evacuation_source_placement_release_evidence(release_evidence_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_admission_id text NOT NULL UNIQUE REFERENCES kim.placement_admission_decisions(admission_id),
    destination_materialization_generation bigint NOT NULL CHECK(destination_materialization_generation>0),
    source_requirements_digest char(64) NOT NULL CHECK(source_requirements_digest ~ '^[0-9a-f]{64}$'),
    destination_requirements_digest char(64) NOT NULL CHECK(destination_requirements_digest ~ '^[0-9a-f]{64}$'),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    FOREIGN KEY(child_operation_id,child_generation) REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation),
    CHECK(source_host_id<>destination_host_id),
    CHECK(destination_materialization_generation>source_materialization_generation)
);

CREATE TRIGGER host_evacuation_source_storage_safety_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_source_storage_safety_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_source_placement_release_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_source_placement_release_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_materialization_relocation_authority_evidence_no_update BEFORE UPDATE ON kim.vm_materialization_relocation_authority_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_evacuation_source_storage_safety_evidence IS 'Planned, reachable-Host root safety: exact SHUTOFF plus exact vda identity and no QEMU holder. It is not Failure fencing or physical cleanup authority.';
COMMENT ON TABLE kim.vm_materialization_relocation_authority_evidence IS 'Generic materialization relocation provenance consumed by the ordinary VM plan/define/image/power path; no Recovery or Failure authority is implied.';
