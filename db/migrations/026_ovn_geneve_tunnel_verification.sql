CREATE TABLE kim.ovn_geneve_tunnel_observation_evidence (
    observation_id text PRIMARY KEY,
    source_port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    destination_port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    source_chassis_observation_id text NOT NULL REFERENCES kim.ovn_chassis_encap_observation_evidence(observation_id),
    destination_chassis_observation_id text NOT NULL REFERENCES kim.ovn_chassis_encap_observation_evidence(observation_id),
    segment_claim_id text NOT NULL REFERENCES kim.network_segment_claims_current(segment_claim_id),
    source_mapping_generation bigint NOT NULL CHECK (source_mapping_generation > 0),
    destination_mapping_generation bigint NOT NULL CHECK (destination_mapping_generation > 0),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    source_tunnel_interface_digest char(64) NOT NULL CHECK (source_tunnel_interface_digest ~ '^[0-9a-f]{64}$'),
    destination_tunnel_interface_digest char(64) NOT NULL CHECK (destination_tunnel_interface_digest ~ '^[0-9a-f]{64}$'),
    probe_protocol text NOT NULL CHECK (probe_protocol IN ('ICMP', 'UDP')),
    packets_sent bigint NOT NULL CHECK (packets_sent >= 0),
    packets_received bigint NOT NULL CHECK (packets_received >= 0 AND packets_received <= packets_sent),
    tunnel_state text NOT NULL CHECK (tunnel_state IN ('VERIFIED', 'DEGRADED', 'CONFLICTING', 'UNKNOWN')),
    verifier_artifact_digest char(64) NOT NULL CHECK (verifier_artifact_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK (source_port_id <> destination_port_id),
    UNIQUE (source_port_id, destination_port_id, segment_claim_id, observation_generation)
);

CREATE TABLE kim.network_ovn_tunnel_state_current (
    source_port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    destination_port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    segment_claim_id text NOT NULL REFERENCES kim.network_segment_claims_current(segment_claim_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    evidence_id text NOT NULL REFERENCES kim.ovn_geneve_tunnel_observation_evidence(observation_id),
    tunnel_state text NOT NULL CHECK (tunnel_state IN ('VERIFIED', 'DEGRADED', 'CONFLICTING', 'UNKNOWN')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (source_port_id, destination_port_id, segment_claim_id)
);

CREATE TRIGGER ovn_geneve_tunnel_observation_evidence_no_update
BEFORE UPDATE ON kim.ovn_geneve_tunnel_observation_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.ovn_geneve_tunnel_observation_evidence IS
'Immutable directed Geneve packet-path evidence between two distinct current KIM Port/chassis authorities.';
COMMENT ON TABLE kim.network_ovn_tunnel_state_current IS
'Rebuildable directed tunnel projection. VERIFIED does not imply tenant L3 reachability, Guest readiness, or application health.';
