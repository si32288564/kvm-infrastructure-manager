ALTER TABLE kim.vm_materialization_readiness_current
    ADD COLUMN network_observation_generation bigint CHECK (network_observation_generation > 0),
    ADD COLUMN network_evidence_set_digest char(64)
        CHECK (network_evidence_set_digest IS NULL OR network_evidence_set_digest ~ '^[0-9a-f]{64}$');

CREATE TABLE kim.vm_network_port_realization_evidence (
    evidence_id text PRIMARY KEY,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    network_id text NOT NULL REFERENCES kim.networks_current(network_id),
    network_generation bigint NOT NULL CHECK (network_generation > 0),
    segment_claim_id text NOT NULL REFERENCES kim.network_segment_claims_current(segment_claim_id),
    segment_generation bigint NOT NULL CHECK (segment_generation > 0),
    host_mapping_generation bigint NOT NULL CHECK (host_mapping_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    binding_type text NOT NULL CHECK (binding_type IN ('OVS','SRIOV_DIRECT')),
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    preboot_state text NOT NULL CHECK (preboot_state IN ('REALIZED','CONFLICTING','UNKNOWN')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (vm_id,vm_generation) REFERENCES kim.virtual_machines_current(vm_id,vm_generation),
    UNIQUE (vm_id,vm_generation,port_id,observation_generation),
    UNIQUE (evidence_id,vm_id,vm_generation)
);
CREATE TABLE kim.vm_network_port_realizations_current (
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    port_id text NOT NULL,
    port_generation bigint NOT NULL,
    binding_generation bigint NOT NULL,
    observation_generation bigint NOT NULL,
    evidence_id text NOT NULL,
    preboot_state text NOT NULL CHECK (preboot_state IN ('REALIZED','CONFLICTING','UNKNOWN')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (vm_id,port_id),
    FOREIGN KEY (evidence_id,vm_id,vm_generation)
        REFERENCES kim.vm_network_port_realization_evidence(evidence_id,vm_id,vm_generation)
);
CREATE TRIGGER vm_network_port_realization_evidence_no_update
    BEFORE UPDATE ON kim.vm_network_port_realization_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
COMMENT ON TABLE kim.vm_network_port_realization_evidence IS
    'Immutable pre-boot NIC/provider evidence; it is never post-boot dataplane convergence evidence.';
