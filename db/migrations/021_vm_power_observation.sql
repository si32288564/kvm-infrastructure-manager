CREATE TABLE kim.vm_power_observation_evidence (
    evidence_id text PRIMARY KEY,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    desired_power_state text NOT NULL CHECK (desired_power_state IN ('RUNNING', 'SHUTOFF')),
    observed_power_state text NOT NULL CHECK (observed_power_state IN ('RUNNING', 'SHUTOFF')),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (vm_id, vm_generation) REFERENCES kim.virtual_machines_current(vm_id, vm_generation),
    FOREIGN KEY (command_id, attempt_index) REFERENCES kim.command_attempts(command_id, attempt_index)
);

CREATE TABLE kim.vm_power_state_current (
    vm_id uuid PRIMARY KEY REFERENCES kim.virtual_machines_current(vm_id),
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    desired_power_state text NOT NULL CHECK (desired_power_state IN ('RUNNING', 'SHUTOFF')),
    observed_power_state text NOT NULL CHECK (observed_power_state IN ('RUNNING', 'SHUTOFF')),
    convergence_state text NOT NULL CHECK (convergence_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN')),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (vm_id, vm_generation) REFERENCES kim.virtual_machines_current(vm_id, vm_generation)
);

CREATE TRIGGER vm_power_observation_evidence_no_update
    BEFORE UPDATE ON kim.vm_power_observation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_power_observation_evidence IS
    'Immutable standard libvirt power-state read-back evidence. A power API response alone cannot create this authority.';
COMMENT ON TABLE kim.vm_power_state_current IS
    'Rebuildable current VM runtime power projection. RUNNING does not imply post-boot dataplane or guest readiness.';
