CREATE TABLE kim.network_intent_revision_evidence (
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL CHECK (intent_generation > 0),
    aggregate_type text NOT NULL CHECK (aggregate_type = 'PORT'),
    aggregate_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    project_id text NOT NULL,
    network_id text NOT NULL REFERENCES kim.networks_current(network_id),
    network_generation bigint NOT NULL CHECK (network_generation > 0),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    segment_claim_id text NOT NULL REFERENCES kim.network_segment_claims_current(segment_claim_id),
    segment_generation bigint NOT NULL CHECK (segment_generation > 0),
    host_mapping_generation bigint NOT NULL CHECK (host_mapping_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    schema_version text NOT NULL CHECK (schema_version = 'kim.network-intent.ovn-port/v1'),
    canonical_object_set jsonb NOT NULL CHECK (jsonb_typeof(canonical_object_set) = 'object'),
    object_set_digest char(64) NOT NULL CHECK (object_set_digest ~ '^[0-9a-f]{64}$'),
    intent_state text NOT NULL CHECK (intent_state = 'COMMITTED'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (intent_id, intent_generation),
    UNIQUE (aggregate_type, aggregate_id, intent_generation)
);

CREATE TABLE kim.ovn_nb_observation_evidence (
    observation_id text PRIMARY KEY,
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL,
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    apply_response_state text NOT NULL CHECK (apply_response_state IN ('RECEIVED', 'LOST', 'UNKNOWN')),
    ownership_marker_matches boolean NOT NULL,
    object_set_digest_matches boolean NOT NULL,
    logical_switch_present boolean NOT NULL,
    logical_switch_port_present boolean NOT NULL,
    nb_state text NOT NULL CHECK (nb_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN')),
    adapter_artifact_digest char(64) NOT NULL CHECK (adapter_artifact_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id, intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id, intent_generation),
    UNIQUE (intent_id, intent_generation, observation_generation)
);

CREATE TABLE kim.ovn_sb_observation_evidence (
    observation_id text PRIMARY KEY,
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL,
    nb_observation_id text NOT NULL REFERENCES kim.ovn_nb_observation_evidence(observation_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    port_binding_present boolean NOT NULL,
    datapath_present boolean NOT NULL,
    expected_chassis_matches boolean NOT NULL,
    chassis_identity_digest char(64) NOT NULL CHECK (chassis_identity_digest ~ '^[0-9a-f]{64}$'),
    sb_state text NOT NULL CHECK (sb_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN')),
    adapter_artifact_digest char(64) NOT NULL CHECK (adapter_artifact_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id, intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id, intent_generation),
    UNIQUE (intent_id, intent_generation, observation_generation)
);

CREATE TABLE kim.network_ovn_state_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL CHECK (intent_generation > 0),
    nb_observation_id text REFERENCES kim.ovn_nb_observation_evidence(observation_id),
    nb_observation_generation bigint,
    nb_state text NOT NULL CHECK (nb_state IN ('PENDING', 'MATCHED', 'CONFLICTING', 'UNKNOWN')),
    sb_observation_id text REFERENCES kim.ovn_sb_observation_evidence(observation_id),
    sb_observation_generation bigint,
    sb_state text NOT NULL CHECK (sb_state IN ('PENDING', 'MATCHED', 'CONFLICTING', 'UNKNOWN')),
    layer_status text NOT NULL CHECK (layer_status IN ('INTENT_COMMITTED', 'NB_APPLIED', 'SB_REALIZED', 'CONFLICTING', 'UNKNOWN')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (intent_id, intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id, intent_generation),
    CHECK ((nb_observation_id IS NULL) = (nb_observation_generation IS NULL)),
    CHECK ((sb_observation_id IS NULL) = (sb_observation_generation IS NULL))
);

CREATE TRIGGER network_intent_revision_evidence_no_update
BEFORE UPDATE ON kim.network_intent_revision_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER ovn_nb_observation_evidence_no_update
BEFORE UPDATE ON kim.ovn_nb_observation_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER ovn_sb_observation_evidence_no_update
BEFORE UPDATE ON kim.ovn_sb_observation_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.network_intent_revision_evidence IS
'Immutable typed OVN desired-object intent derived from KIM authority; OVN is not the resource ownership authority.';
COMMENT ON TABLE kim.network_ovn_state_current IS
'Rebuildable OVN layer projection. NB_APPLIED and SB_REALIZED do not imply Host OVS convergence, end-to-end reachability, or Guest readiness.';
