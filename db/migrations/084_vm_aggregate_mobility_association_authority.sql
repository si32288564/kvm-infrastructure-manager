-- Associate already-terminal Recovery/EVACUATE physical incarnations with a
-- stable logical VM aggregate. This is an evidence consumer, not a mobility
-- primitive, and never revises logical VM or dependency desired authority.
CREATE TABLE kim.vm_aggregate_mobility_association_evidence (
    association_id text PRIMARY KEY,
    association_generation bigint NOT NULL CHECK(association_generation > 0),
    mobility_kind text NOT NULL CHECK(mobility_kind IN ('RECOVERY','HOST_EVACUATION')),
    mobility_terminal_evidence_id text NOT NULL,
    mobility_terminal_digest char(64) NOT NULL CHECK(mobility_terminal_digest ~ '^[0-9a-f]{64}$'),
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    dependency_digest char(64) NOT NULL CHECK(dependency_digest ~ '^[0-9a-f]{64}$'),
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    source_aggregate_terminal_id text NOT NULL REFERENCES kim.vm_aggregate_terminal_evidence(terminal_evidence_id),
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_host_id text NOT NULL,
    source_vm_generation bigint NOT NULL,
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_materialization_generation bigint NOT NULL,
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    destination_host_id text NOT NULL,
    destination_vm_generation bigint NOT NULL,
    destination_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    destination_plan_digest char(64) NOT NULL CHECK(destination_plan_digest ~ '^[0-9a-f]{64}$'),
    destination_materialization_generation bigint NOT NULL,
    destination_readiness_observation_generation bigint NOT NULL,
    destination_network_evidence_set_digest char(64) NOT NULL CHECK(destination_network_evidence_set_digest ~ '^[0-9a-f]{64}$'),
    destination_power_evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    destination_power_observation_generation bigint NOT NULL,
    port_count integer NOT NULL CHECK(port_count BETWEEN 0 AND 1),
    port_evidence_set_digest char(64) NOT NULL CHECK(port_evidence_set_digest ~ '^[0-9a-f]{64}$'),
    association_digest char(64) NOT NULL CHECK(association_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(mobility_kind,mobility_terminal_evidence_id,vm_id),
    UNIQUE(vm_id,association_generation),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation),
    CHECK(source_host_id<>destination_host_id),
    CHECK(destination_materialization_generation>source_materialization_generation)
);

CREATE TABLE kim.vm_aggregate_mobility_associations_current (
    vm_id uuid PRIMARY KEY REFERENCES kim.vm_resources_current(vm_id),
    association_generation bigint NOT NULL,
    association_id text NOT NULL UNIQUE REFERENCES kim.vm_aggregate_mobility_association_evidence(association_id),
    mobility_kind text NOT NULL CHECK(mobility_kind IN ('RECOVERY','HOST_EVACUATION')),
    mobility_terminal_evidence_id text NOT NULL,
    destination_admission_id text NOT NULL,
    destination_host_id text NOT NULL,
    destination_vm_generation bigint NOT NULL,
    destination_plan_id text NOT NULL,
    destination_materialization_generation bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(vm_id,association_generation) REFERENCES kim.vm_aggregate_mobility_association_evidence(vm_id,association_generation)
);

ALTER TABLE kim.vm_resource_runtime_bindings_current
    ADD COLUMN mobility_association_generation bigint NOT NULL DEFAULT 0 CHECK(mobility_association_generation>=0),
    ADD COLUMN mobility_association_id text REFERENCES kim.vm_aggregate_mobility_association_evidence(association_id),
    ADD CHECK((mobility_association_generation=0 AND mobility_association_id IS NULL) OR
              (mobility_association_generation>0 AND mobility_association_id IS NOT NULL));

CREATE TRIGGER vm_aggregate_mobility_association_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_mobility_association_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_aggregate_mobility_association_evidence IS 'Immutable proof that a terminal Recovery or planned Host EVACUATE replaced only a VM aggregate physical incarnation while exact logical desired authority remained unchanged.';
COMMENT ON TABLE kim.vm_aggregate_mobility_associations_current IS 'Rebuildable pointer to the latest terminal mobility association for a logical VM aggregate.';
