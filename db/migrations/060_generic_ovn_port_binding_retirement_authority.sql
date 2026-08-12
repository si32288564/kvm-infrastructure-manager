ALTER TABLE kim.network_intent_revision_evidence
    DROP CONSTRAINT network_intent_revision_evidence_schema_version_check;
ALTER TABLE kim.network_intent_revision_evidence
    ADD CONSTRAINT network_intent_revision_evidence_schema_version_check CHECK (
        schema_version IN (
            'kim.network-intent.ovn-port/v1',
            'kim.network-intent.ovn-port/v2',
            'kim.network-intent.ovn-port-binding-retirement/v1'
        )
    );

ALTER TABLE kim.ovn_runtime_work_current
    ADD COLUMN operation_kind text NOT NULL DEFAULT 'RECONCILE'
        CHECK (operation_kind IN ('RECONCILE','UNBIND'));
ALTER TABLE kim.ovn_runtime_work_attempt_evidence
    ADD COLUMN operation_kind text NOT NULL DEFAULT 'RECONCILE'
        CHECK (operation_kind IN ('RECONCILE','UNBIND'));

CREATE TABLE kim.network_port_binding_retirements_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    operation_id text NOT NULL UNIQUE,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL CHECK (intent_generation > 0),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    retirement_state text NOT NULL CHECK (
        retirement_state IN ('PENDING','CLAIMED','DISPATCH_UNKNOWN','VERIFIED','CONFLICTING','STALE')
    ),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id,intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id,intent_generation),
    UNIQUE (port_id,binding_generation,operation_generation)
);

CREATE TABLE kim.network_port_binding_retirement_evidence (
    evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    work_id text NOT NULL REFERENCES kim.ovn_runtime_work_current(work_id),
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL CHECK (intent_generation > 0),
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    ownership_marker_matches boolean NOT NULL,
    logical_port_preserved boolean NOT NULL,
    requested_chassis_absent boolean NOT NULL,
    source_chassis_inactive boolean NOT NULL,
    source_ovs_interface_absent boolean NOT NULL,
    apply_response_state text NOT NULL CHECK (apply_response_state IN ('RECEIVED','LOST','UNKNOWN')),
    nb_observation_generation bigint NOT NULL CHECK (nb_observation_generation > 0),
    nb_observation_digest char(64) NOT NULL CHECK (nb_observation_digest ~ '^[0-9a-f]{64}$'),
    sb_observation_generation bigint NOT NULL CHECK (sb_observation_generation > 0),
    sb_observation_digest char(64) NOT NULL CHECK (sb_observation_digest ~ '^[0-9a-f]{64}$'),
    ovs_observation_generation bigint NOT NULL CHECK (ovs_observation_generation > 0),
    ovs_observation_digest char(64) NOT NULL CHECK (ovs_observation_digest ~ '^[0-9a-f]{64}$'),
    adapter_artifact_digest char(64) NOT NULL CHECK (adapter_artifact_digest ~ '^[0-9a-f]{64}$'),
    retirement_state text NOT NULL CHECK (retirement_state IN ('VERIFIED','CONFLICTING','UNKNOWN')),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id,intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id,intent_generation),
    FOREIGN KEY (work_id,claim_generation)
        REFERENCES kim.ovn_runtime_work_attempt_evidence(work_id,claim_generation),
    UNIQUE (operation_id,operation_generation,claim_generation)
);

ALTER TABLE kim.network_port_binding_retirements_current
    ADD CONSTRAINT network_port_binding_retirement_terminal_fk
    FOREIGN KEY (terminal_evidence_id)
        REFERENCES kim.network_port_binding_retirement_evidence(evidence_id);

ALTER TABLE kim.network_port_source_quiescence_evidence
    ADD COLUMN retirement_evidence_id text
        REFERENCES kim.network_port_binding_retirement_evidence(evidence_id);

COMMENT ON COLUMN kim.network_port_source_quiescence_evidence.retirement_evidence_id IS
'Exact generic OVN UNBIND proof consumed by source quiescence. NULL is historical pre-060 evidence only and is not accepted for new OVN handoff.';

CREATE TRIGGER network_port_binding_retirement_evidence_no_update
BEFORE UPDATE ON kim.network_port_binding_retirement_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.network_port_binding_retirements_current IS
'Generic current OVN Host/chassis binding retirement authority. It preserves logical Port and IP/MAC ownership and is not Recovery-specific.';
COMMENT ON TABLE kim.network_port_binding_retirement_evidence IS
'Immutable exact NB/SB/source-OVS read-back proof. Command success and claim expiry are not positive UNBOUND evidence.';
COMMENT ON COLUMN kim.ovn_runtime_work_current.operation_kind IS
'Closed typed OVN operation. UNBIND retires only one exact Host binding incarnation; it does not delete logical Port identity.';
