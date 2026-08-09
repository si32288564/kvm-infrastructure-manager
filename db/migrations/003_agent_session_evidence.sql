ALTER TABLE kim.agent_transport_sessions
    RENAME TO agent_transport_sessions_current;

CREATE TABLE kim.agent_transport_session_attempts (
    session_attempt_id text PRIMARY KEY,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    connection_instance_id text NOT NULL,
    transport_profile text NOT NULL,
    protocol_version text NOT NULL,
    agent_artifact_digest char(64) NOT NULL CHECK (agent_artifact_digest ~ '^[0-9a-f]{64}$'),
    credential_binding_revision bigint NOT NULL CHECK (credential_binding_revision > 0),
    handshake_evidence jsonb NOT NULL,
    handshake_evidence_digest char(64) NOT NULL CHECK (handshake_evidence_digest ~ '^[0-9a-f]{64}$'),
    source_opened_at timestamptz,
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (host_id, connection_instance_id),
    UNIQUE (host_id, session_attempt_id)
);

CREATE TABLE kim.agent_transport_session_events (
    session_attempt_id text NOT NULL REFERENCES kim.agent_transport_session_attempts(session_attempt_id),
    event_sequence bigint NOT NULL CHECK (event_sequence > 0),
    event_type text NOT NULL CHECK (
        event_type IN ('OPENED', 'CURRENT_GRANTED', 'DRAINING', 'FENCED', 'CLOSED', 'LOST', 'UNKNOWN')
    ),
    session_generation bigint CHECK (session_generation > 0),
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    source_observed_at timestamptz,
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (session_attempt_id, event_sequence)
);

CREATE FUNCTION kim.reject_immutable_evidence_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'evidence row is immutable';
END;
$$;

CREATE TRIGGER agent_transport_session_attempts_no_update
    BEFORE UPDATE ON kim.agent_transport_session_attempts
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

CREATE TRIGGER agent_transport_session_events_no_update
    BEFORE UPDATE ON kim.agent_transport_session_events
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

ALTER TABLE kim.agent_transport_sessions_current
    ADD COLUMN current_session_attempt_id text,
    ADD CONSTRAINT agent_transport_sessions_current_attempt_fkey
        FOREIGN KEY (host_id, current_session_attempt_id)
        REFERENCES kim.agent_transport_session_attempts(host_id, session_attempt_id);

COMMENT ON TABLE kim.agent_transport_sessions_current IS
    'Mutable current session authority; one row per Host identity.';
COMMENT ON TABLE kim.agent_transport_session_attempts IS
    'Immutable connection/session attempt evidence. UPDATE is prohibited; retention deletion is governed separately.';
COMMENT ON TABLE kim.agent_transport_session_events IS
    'Append-only session lifecycle evidence. UPDATE is prohibited; no event alone grants current authority.';
