-- Phase 3 logical VM aggregate authority.  Logical desired identity and
-- runtime intent are deliberately separate from Placement, Host and backend
-- incarnations.  The first qualified producer profile is one verified boot
-- Volume, zero Ports and no PCI requirements.
CREATE TABLE kim.vm_resource_revision_evidence (
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL CHECK(vm_revision > 0),
    project_id text NOT NULL,
    vm_name text NOT NULL CHECK(length(vm_name) BETWEEN 1 AND 255),
    flavor_id text NOT NULL,
    flavor_revision bigint NOT NULL CHECK(flavor_revision > 0),
    image_id text NOT NULL,
    image_revision bigint NOT NULL CHECK(image_revision > 0),
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL CHECK(availability_policy_revision > 0),
    placement_scope_id text NOT NULL,
    placement_scope_generation bigint NOT NULL CHECK(placement_scope_generation > 0),
    desired_power_state text NOT NULL CHECK(desired_power_state IN('RUNNING','SHUTOFF')),
    delete_protection boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK(lifecycle_state IN('ACTIVE','RETIRE_PENDING','DELETED')),
    previous_revision bigint,
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(vm_id,vm_revision),
    FOREIGN KEY(flavor_id,flavor_revision) REFERENCES kim.flavor_revision_evidence(flavor_id,flavor_revision),
    FOREIGN KEY(image_id,image_revision) REFERENCES kim.image_revision_evidence(image_id,image_revision),
    FOREIGN KEY(availability_policy_id,availability_policy_revision) REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision),
    FOREIGN KEY(placement_scope_id,placement_scope_generation) REFERENCES kim.placement_scope_revision_evidence(placement_scope_id,scope_generation),
    CHECK((vm_revision=1 AND previous_revision IS NULL) OR (vm_revision>1 AND previous_revision=vm_revision-1))
);

CREATE TABLE kim.vm_dependency_snapshot_evidence (
    dependency_snapshot_id text PRIMARY KEY,
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL CHECK(runtime_intent_generation > 0),
    flavor_id text NOT NULL,
    flavor_revision bigint NOT NULL,
    flavor_revision_digest char(64) NOT NULL CHECK(flavor_revision_digest ~ '^[0-9a-f]{64}$'),
    image_id text NOT NULL,
    image_revision bigint NOT NULL,
    image_revision_digest char(64) NOT NULL CHECK(image_revision_digest ~ '^[0-9a-f]{64}$'),
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL,
    availability_policy_digest char(64) NOT NULL CHECK(availability_policy_digest ~ '^[0-9a-f]{64}$'),
    placement_scope_id text NOT NULL,
    placement_scope_generation bigint NOT NULL,
    placement_scope_digest char(64) NOT NULL CHECK(placement_scope_digest ~ '^[0-9a-f]{64}$'),
    port_count integer NOT NULL CHECK(port_count >= 0),
    volume_count integer NOT NULL CHECK(volume_count >= 0),
    dependency_payload jsonb NOT NULL CHECK(jsonb_typeof(dependency_payload)='object'),
    dependency_digest char(64) NOT NULL CHECK(dependency_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(vm_id,vm_revision,runtime_intent_generation),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(flavor_id,flavor_revision) REFERENCES kim.flavor_revision_evidence(flavor_id,flavor_revision),
    FOREIGN KEY(image_id,image_revision) REFERENCES kim.image_revision_evidence(image_id,image_revision),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(placement_scope_id,placement_scope_generation,placement_scope_digest)
      REFERENCES kim.placement_scope_revision_evidence(placement_scope_id,scope_generation,scope_digest)
);

CREATE TABLE kim.vm_dependency_port_evidence (
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    port_ordinal integer NOT NULL CHECK(port_ordinal >= 0),
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY(dependency_snapshot_id,port_ordinal),
    UNIQUE(dependency_snapshot_id,port_id),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision)
);

CREATE TABLE kim.vm_dependency_volume_evidence (
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    volume_ordinal integer NOT NULL CHECK(volume_ordinal >= 0),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    device_role text NOT NULL CHECK(device_role IN('ROOT','DATA')),
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    attachment_intent_id text NOT NULL UNIQUE,
    requested_attachment_id text NOT NULL UNIQUE,
    PRIMARY KEY(dependency_snapshot_id,volume_ordinal),
    UNIQUE(dependency_snapshot_id,volume_id),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision)
);

CREATE TABLE kim.vm_runtime_intent_evidence (
    vm_id uuid NOT NULL,
    runtime_intent_generation bigint NOT NULL CHECK(runtime_intent_generation > 0),
    vm_revision bigint NOT NULL,
    dependency_snapshot_id text NOT NULL UNIQUE REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    desired_power_state text NOT NULL CHECK(desired_power_state IN('RUNNING','SHUTOFF')),
    intent_digest char(64) NOT NULL CHECK(intent_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(vm_id,runtime_intent_generation),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision)
);

CREATE TABLE kim.vm_lifecycle_operation_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK(operation_generation > 0),
    request_id text NOT NULL UNIQUE,
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    operation_kind text NOT NULL CHECK(operation_kind IN('CREATE','POWER')),
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL,
    dependency_digest char(64) NOT NULL CHECK(dependency_digest ~ '^[0-9a-f]{64}$'),
    desired_power_state text NOT NULL CHECK(desired_power_state IN('RUNNING','SHUTOFF')),
    operation_digest char(64) NOT NULL CHECK(operation_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation),
    FOREIGN KEY(dependency_snapshot_id) REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id)
);

CREATE TABLE kim.vm_lifecycle_operations_current (
    operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    operation_state text NOT NULL CHECK(operation_state IN('PENDING','PLACING','MATERIALIZING','VERIFYING','VERIFIED','FAILED','UNKNOWN')),
    last_claim_generation bigint NOT NULL DEFAULT 0 CHECK(last_claim_generation >= 0),
    claim_owner text,
    claim_generation bigint,
    claim_expires_at timestamptz,
    response_state text CHECK(response_state IN('RECEIVED','LOST','UNKNOWN')),
    admission_id text REFERENCES kim.placement_admission_decisions(admission_id),
    plan_id text REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation),
    CHECK((claim_owner IS NULL AND claim_generation IS NULL AND claim_expires_at IS NULL) OR
          (claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL))
);

CREATE UNIQUE INDEX vm_one_nonterminal_lifecycle_operation
    ON kim.vm_lifecycle_operations_current(vm_id)
    WHERE operation_state NOT IN('VERIFIED','FAILED');

CREATE TABLE kim.vm_lifecycle_attempt_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL CHECK(claim_generation > 0),
    claim_owner text NOT NULL,
    claim_mode text NOT NULL CHECK(claim_mode IN('APPLY_ALLOWED','READ_BACK_FIRST')),
    operation_state text NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.vm_aggregate_admission_binding_evidence (
    binding_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL,
    dependency_digest char(64) NOT NULL CHECK(dependency_digest ~ '^[0-9a-f]{64}$'),
    admission_id text NOT NULL UNIQUE REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL,
    placement_scope_id text NOT NULL,
    placement_scope_generation bigint NOT NULL,
    placement_scope_digest char(64) NOT NULL CHECK(placement_scope_digest ~ '^[0-9a-f]{64}$'),
    admission_binding_digest char(64) NOT NULL CHECK(admission_binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.vm_aggregate_materialization_binding_evidence (
    binding_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    admission_binding_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_aggregate_admission_binding_evidence(binding_evidence_id),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    admission_id text NOT NULL,
    host_id text NOT NULL,
    plan_id text NOT NULL UNIQUE REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    plan_digest char(64) NOT NULL CHECK(plan_digest ~ '^[0-9a-f]{64}$'),
    materialization_generation bigint NOT NULL CHECK(materialization_generation > 0),
    materialization_binding_digest char(64) NOT NULL CHECK(materialization_binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.vm_aggregate_verification_evidence (
    verification_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL,
    dependency_digest char(64) NOT NULL CHECK(dependency_digest ~ '^[0-9a-f]{64}$'),
    admission_binding_evidence_id text NOT NULL REFERENCES kim.vm_aggregate_admission_binding_evidence(binding_evidence_id),
    materialization_binding_evidence_id text NOT NULL REFERENCES kim.vm_aggregate_materialization_binding_evidence(binding_evidence_id),
    admission_id text NOT NULL,
    host_id text NOT NULL,
    vm_generation bigint NOT NULL,
    plan_id text NOT NULL,
    plan_digest char(64) NOT NULL CHECK(plan_digest ~ '^[0-9a-f]{64}$'),
    readiness_observation_generation bigint NOT NULL CHECK(readiness_observation_generation > 0),
    definition_evidence_id text NOT NULL REFERENCES kim.vm_definition_observation_evidence(evidence_id),
    image_evidence_id text NOT NULL REFERENCES kim.vm_image_realization_evidence(evidence_id),
    network_evidence_set_digest char(64) NOT NULL CHECK(network_evidence_set_digest ~ '^[0-9a-f]{64}$'),
    power_observation_generation bigint NOT NULL CHECK(power_observation_generation > 0),
    power_evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    desired_power_state text NOT NULL CHECK(desired_power_state='RUNNING'),
    observed_power_state text NOT NULL CHECK(observed_power_state='RUNNING'),
    verification_state text NOT NULL CHECK(verification_state='VERIFIED'),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision)
);

CREATE TABLE kim.vm_aggregate_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    verification_id text NOT NULL UNIQUE REFERENCES kim.vm_aggregate_verification_evidence(verification_id),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    terminal_state text NOT NULL CHECK(terminal_state='VERIFIED'),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation)
);

ALTER TABLE kim.vm_lifecycle_operations_current
    ADD FOREIGN KEY(terminal_evidence_id) REFERENCES kim.vm_aggregate_terminal_evidence(terminal_evidence_id);

CREATE TABLE kim.vm_resources_current (
    vm_id uuid PRIMARY KEY,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    project_id text NOT NULL,
    vm_name text NOT NULL CHECK(length(vm_name) BETWEEN 1 AND 255),
    lifecycle_state text NOT NULL CHECK(lifecycle_state IN('CREATING','ACTIVE','BLOCKED','RETIRE_PENDING','DELETED')),
    convergence_state text NOT NULL CHECK(convergence_state IN('PENDING','CONVERGED','UNKNOWN','FAILED')),
    current_operation_id text NOT NULL REFERENCES kim.vm_lifecycle_operations_current(operation_id),
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation)
);

CREATE UNIQUE INDEX vm_resource_current_project_name
    ON kim.vm_resources_current(project_id,vm_name)
    WHERE lifecycle_state<>'DELETED';

CREATE TABLE kim.vm_resource_runtime_bindings_current (
    vm_id uuid PRIMARY KEY REFERENCES kim.vm_resources_current(vm_id),
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL,
    vm_generation bigint NOT NULL,
    plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    materialization_generation bigint NOT NULL,
    verification_id text NOT NULL REFERENCES kim.vm_aggregate_verification_evidence(verification_id),
    terminal_evidence_id text NOT NULL REFERENCES kim.vm_aggregate_terminal_evidence(terminal_evidence_id),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation)
);

CREATE TRIGGER vm_resource_revision_evidence_no_update BEFORE UPDATE ON kim.vm_resource_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_dependency_snapshot_evidence_no_update BEFORE UPDATE ON kim.vm_dependency_snapshot_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_dependency_port_evidence_no_update BEFORE UPDATE ON kim.vm_dependency_port_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_dependency_volume_evidence_no_update BEFORE UPDATE ON kim.vm_dependency_volume_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_runtime_intent_evidence_no_update BEFORE UPDATE ON kim.vm_runtime_intent_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_lifecycle_operation_evidence_no_update BEFORE UPDATE ON kim.vm_lifecycle_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_lifecycle_attempt_evidence_no_update BEFORE UPDATE ON kim.vm_lifecycle_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_aggregate_admission_binding_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_admission_binding_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_aggregate_materialization_binding_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_materialization_binding_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_aggregate_verification_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_aggregate_terminal_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_resource_revision_evidence IS 'Immutable logical VM desired revisions; no Host, Admission, Binding, LV or materialization incarnation is desired authority.';
COMMENT ON TABLE kim.vm_dependency_snapshot_evidence IS 'Exact immutable logical dependency set consumed by the KIM Placement compiler.';
COMMENT ON TABLE kim.vm_aggregate_verification_evidence IS 'DB-derived aggregate convergence over exact Admission, materialization readiness and RUNNING read-back evidence.';
COMMENT ON TABLE kim.vm_resource_runtime_bindings_current IS 'Rebuildable physical runtime projection. Recovery and EVACUATE may replace it without changing logical VM identity.';
