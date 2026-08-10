CREATE TABLE kim.ovn_logical_flow_observation_evidence (
    observation_id text PRIMARY KEY,
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL,
    sb_observation_id text NOT NULL REFERENCES kim.ovn_sb_observation_evidence(observation_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    expected_datapath_identity_digest char(64) NOT NULL CHECK (expected_datapath_identity_digest ~ '^[0-9a-f]{64}$'),
    observed_datapath_identity_digest char(64) NOT NULL CHECK (observed_datapath_identity_digest ~ '^[0-9a-f]{64}$'),
    logical_flow_set_digest char(64) NOT NULL CHECK (logical_flow_set_digest ~ '^[0-9a-f]{64}$'),
    ingress_flow_count bigint NOT NULL CHECK (ingress_flow_count >= 0),
    egress_flow_count bigint NOT NULL CHECK (egress_flow_count >= 0),
    required_pipeline_coverage boolean NOT NULL,
    required_port_identity_coverage boolean NOT NULL,
    logical_flow_state text NOT NULL CHECK (logical_flow_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN')),
    evaluator_artifact_digest char(64) NOT NULL CHECK (evaluator_artifact_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id, intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id, intent_generation),
    UNIQUE (intent_id, intent_generation, observation_generation)
);

CREATE TABLE kim.ovn_chassis_encap_observation_evidence (
    observation_id text PRIMARY KEY,
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL,
    sb_observation_id text NOT NULL REFERENCES kim.ovn_sb_observation_evidence(observation_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    expected_chassis_identity_digest char(64) NOT NULL CHECK (expected_chassis_identity_digest ~ '^[0-9a-f]{64}$'),
    observed_chassis_identity_digest char(64) NOT NULL CHECK (observed_chassis_identity_digest ~ '^[0-9a-f]{64}$'),
    encap_type text NOT NULL CHECK (encap_type IN ('GENEVE', 'STT', 'VXLAN', 'UNKNOWN')),
    tunnel_endpoint_digest char(64) NOT NULL CHECK (tunnel_endpoint_digest ~ '^[0-9a-f]{64}$'),
    encap_options_digest char(64) NOT NULL CHECK (encap_options_digest ~ '^[0-9a-f]{64}$'),
    chassis_registered boolean NOT NULL,
    encap_present boolean NOT NULL,
    tunnel_endpoint_observed boolean NOT NULL,
    chassis_encap_state text NOT NULL CHECK (chassis_encap_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN')),
    evaluator_artifact_digest char(64) NOT NULL CHECK (evaluator_artifact_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id, intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id, intent_generation),
    UNIQUE (intent_id, intent_generation, observation_generation)
);

CREATE TABLE kim.network_ovn_control_plane_state_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL CHECK (intent_generation > 0),
    sb_observation_id text NOT NULL REFERENCES kim.ovn_sb_observation_evidence(observation_id),
    sb_observation_generation bigint NOT NULL CHECK (sb_observation_generation > 0),
    logical_flow_observation_id text REFERENCES kim.ovn_logical_flow_observation_evidence(observation_id),
    logical_flow_observation_generation bigint,
    logical_flow_state text NOT NULL CHECK (logical_flow_state IN ('PENDING', 'MATCHED', 'CONFLICTING', 'UNKNOWN')),
    chassis_encap_observation_id text REFERENCES kim.ovn_chassis_encap_observation_evidence(observation_id),
    chassis_encap_observation_generation bigint,
    chassis_encap_state text NOT NULL CHECK (chassis_encap_state IN ('PENDING', 'MATCHED', 'CONFLICTING', 'UNKNOWN')),
    control_plane_status text NOT NULL CHECK (control_plane_status IN ('SB_REALIZED', 'LOGICAL_FLOW_PROGRAMMED', 'CHASSIS_ENCAP_READY', 'CONTROL_PLANE_CONVERGED', 'CONFLICTING', 'UNKNOWN')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id, intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id, intent_generation),
    CHECK ((logical_flow_observation_id IS NULL) = (logical_flow_observation_generation IS NULL)),
    CHECK ((chassis_encap_observation_id IS NULL) = (chassis_encap_observation_generation IS NULL))
);

CREATE TRIGGER ovn_logical_flow_observation_evidence_no_update
BEFORE UPDATE ON kim.ovn_logical_flow_observation_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER ovn_chassis_encap_observation_evidence_no_update
BEFORE UPDATE ON kim.ovn_chassis_encap_observation_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.ovn_logical_flow_observation_evidence IS
'Immutable typed logical-flow coverage evidence for one current OVN Port intent; it is not Host dataplane or reachability evidence.';
COMMENT ON TABLE kim.ovn_chassis_encap_observation_evidence IS
'Immutable Chassis/Encap registration evidence. Encap readiness does not prove a cross-chassis tunnel packet path.';
COMMENT ON TABLE kim.network_ovn_control_plane_state_current IS
'Rebuildable OVN logical-flow and chassis-encap projection. CONTROL_PLANE_CONVERGED does not imply Host OVS convergence, tunnel traffic, end-to-end reachability, or Guest readiness.';
