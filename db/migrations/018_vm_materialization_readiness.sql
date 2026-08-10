CREATE TABLE kim.vm_definition_observation_evidence (
    evidence_id text PRIMARY KEY CHECK (length(evidence_id) BETWEEN 1 AND 512),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    domain_present boolean NOT NULL,
    domain_identity_matches boolean NOT NULL,
    plan_identity_matches boolean NOT NULL,
    compute_shape_matches boolean NOT NULL,
    root_volume_identity_matches boolean NOT NULL,
    evidence_state text NOT NULL CHECK (evidence_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN', 'NOT_APPLIED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (vm_id, vm_generation)
        REFERENCES kim.virtual_machines_current(vm_id, vm_generation),
    UNIQUE (evidence_id, vm_id, vm_generation)
);

CREATE TABLE kim.vm_materialization_readiness_current (
    vm_id uuid PRIMARY KEY REFERENCES kim.virtual_machines_current(vm_id),
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    definition_evidence_id text NOT NULL,
    domain_state text NOT NULL CHECK (domain_state IN ('DEFINED', 'ABSENT', 'CONFLICTING', 'UNKNOWN')),
    image_state text NOT NULL CHECK (image_state IN ('PENDING', 'REALIZED', 'CONFLICTING', 'UNKNOWN')),
    network_state text NOT NULL CHECK (network_state IN ('PENDING', 'REALIZED', 'CONFLICTING', 'UNKNOWN')),
    storage_state text NOT NULL CHECK (storage_state IN ('BOUND', 'CONFLICTING', 'UNKNOWN')),
    boot_readiness text NOT NULL CHECK (boot_readiness IN ('BLOCKED', 'READY', 'UNKNOWN')),
    blocking_reasons text[] NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (definition_evidence_id, vm_id, vm_generation)
        REFERENCES kim.vm_definition_observation_evidence(evidence_id, vm_id, vm_generation)
);

CREATE TRIGGER vm_definition_observation_evidence_no_update
    BEFORE UPDATE ON kim.vm_definition_observation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_definition_observation_evidence IS
    'Immutable standard-libvirt inactive Domain read-back. Define response alone is not definition or boot authority.';
COMMENT ON TABLE kim.vm_materialization_readiness_current IS
    'Current materialization gate. Domain DEFINED never implies Image/Network realization, boot readiness, power-on authority, or guest readiness.';
