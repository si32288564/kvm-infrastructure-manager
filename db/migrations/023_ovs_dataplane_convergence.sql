CREATE TABLE kim.vm_port_dataplane_observation_evidence (
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
    preboot_evidence_id text NOT NULL REFERENCES kim.vm_network_port_realization_evidence(evidence_id),
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    target_device text NOT NULL,
    bridge_observed text NOT NULL,
    link_state text NOT NULL CHECK (link_state = 'up'),
    convergence_state text NOT NULL CHECK (convergence_state = 'CONVERGED'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (vm_id, vm_generation) REFERENCES kim.virtual_machines_current(vm_id, vm_generation),
    UNIQUE (vm_id, vm_generation, port_id, observation_generation),
    UNIQUE (evidence_id, vm_id, vm_generation, port_id)
);

CREATE TABLE kim.vm_port_dataplane_state_current (
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    port_id text NOT NULL,
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    evidence_id text NOT NULL,
    convergence_state text NOT NULL CHECK (convergence_state IN ('CONVERGED', 'DEGRADED', 'UNKNOWN')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (vm_id, port_id),
    FOREIGN KEY (vm_id, vm_generation) REFERENCES kim.virtual_machines_current(vm_id, vm_generation),
    FOREIGN KEY (evidence_id, vm_id, vm_generation, port_id)
        REFERENCES kim.vm_port_dataplane_observation_evidence(evidence_id, vm_id, vm_generation, port_id)
);

CREATE TRIGGER vm_port_dataplane_observation_evidence_no_update
BEFORE UPDATE ON kim.vm_port_dataplane_observation_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_port_dataplane_observation_evidence IS
'Immutable Host-side OVS dataplane evidence. It does not prove end-to-end reachability, OVN convergence, or guest readiness.';
COMMENT ON TABLE kim.vm_port_dataplane_state_current IS
'Rebuildable post-boot Host-side OVS convergence projection; RUNNING and pre-boot REALIZED do not implicitly advance it.';
