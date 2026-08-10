CREATE TABLE kim.network_identity_release_observation_evidence (
    observation_id text PRIMARY KEY CHECK (length(observation_id) BETWEEN 1 AND 512),
    identity_claim_id text NOT NULL REFERENCES kim.network_identity_claims(identity_claim_id),
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    evidence_state text NOT NULL CHECK (evidence_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN')),
    port_absent boolean NOT NULL,
    binding_absent boolean NOT NULL,
    ovn_nb_absent boolean NOT NULL,
    ovn_sb_absent boolean NOT NULL,
    host_absent boolean NOT NULL,
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_artifact_digest char(64) NOT NULL CHECK (verifier_artifact_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (identity_claim_id, observation_generation)
);

CREATE TRIGGER network_identity_release_observation_evidence_no_update
BEFORE UPDATE ON kim.network_identity_release_observation_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.network_identity_release_observation_evidence IS
'Immutable typed absence evidence for Network identity quarantine and safe reuse. Timeout or release request alone never proves absence.';
COMMENT ON COLUMN kim.network_identity_release_observation_evidence.evidence_state IS
'MATCHED requires independently observed Port, Binding, OVN NB/SB, and Host absence. UNKNOWN or CONFLICTING keeps the identity quarantined.';
