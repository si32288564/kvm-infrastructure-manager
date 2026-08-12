CREATE TABLE kim.network_port_source_quiescence_evidence (
    evidence_id text PRIMARY KEY,
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_binding_generation bigint NOT NULL CHECK (source_binding_generation > 0),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    source_vm_not_running boolean NOT NULL,
    source_interface_absent boolean NOT NULL,
    quiescence_state text NOT NULL CHECK (quiescence_state IN ('QUIESCED','CONFLICTING','UNKNOWN')),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (port_id, source_binding_generation, observation_generation)
);

CREATE TABLE kim.port_binding_handoff_evidence (
    handoff_id text PRIMARY KEY,
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    workload_id text NOT NULL,
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_port_generation bigint NOT NULL CHECK (source_port_generation > 0),
    destination_port_generation bigint NOT NULL CHECK (destination_port_generation = source_port_generation + 1),
    source_binding_generation bigint NOT NULL CHECK (source_binding_generation > 0),
    destination_binding_generation bigint NOT NULL CHECK (destination_binding_generation = source_binding_generation + 1),
    source_quiescence_evidence_id text NOT NULL REFERENCES kim.network_port_source_quiescence_evidence(evidence_id),
    source_quiescence_evidence_digest char(64) NOT NULL CHECK (source_quiescence_evidence_digest ~ '^[0-9a-f]{64}$'),
    handoff_state text NOT NULL CHECK (handoff_state = 'DESTINATION_RESERVED'),
    handoff_digest char(64) NOT NULL CHECK (handoff_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (port_id, source_binding_generation, destination_binding_generation),
    CHECK (source_host_id <> destination_host_id)
);

CREATE TABLE kim.port_binding_handoffs_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    handoff_id text NOT NULL UNIQUE REFERENCES kim.port_binding_handoff_evidence(handoff_id),
    destination_binding_generation bigint NOT NULL CHECK (destination_binding_generation > 1),
    handoff_state text NOT NULL CHECK (handoff_state IN ('DESTINATION_RESERVED','DESTINATION_REALIZED','VERIFIED','STALE','CONFLICTING','UNKNOWN')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TRIGGER network_port_source_quiescence_evidence_no_update
BEFORE UPDATE ON kim.network_port_source_quiescence_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_binding_handoff_evidence_no_update
BEFORE UPDATE ON kim.port_binding_handoff_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.network_port_source_quiescence_evidence IS
'Immutable read-back evidence that an exact source Port binding is no longer active. Host fencing or VM SHUTOFF alone is not Network quiescence.';
COMMENT ON TABLE kim.port_binding_handoff_evidence IS
'Immutable generic PortBindingHandoff authority. Logical Port/MAC/IP ownership is preserved while the exact Host binding incarnation advances.';
COMMENT ON TABLE kim.port_binding_handoffs_current IS
'Rebuildable current handoff projection. UNKNOWN or source-binding revival never implies safe destination activation.';
