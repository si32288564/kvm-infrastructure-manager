CREATE TABLE kim.database_authority (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    restore_epoch text NOT NULL CHECK (length(restore_epoch) BETWEEN 1 AND 128),
    authority_generation bigint NOT NULL CHECK (authority_generation > 0),
    mode text NOT NULL CHECK (mode IN ('ACTIVE', 'RECOVERY_READ_ONLY')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.host_identities (
    host_id text PRIMARY KEY CHECK (length(host_id) BETWEEN 1 AND 255),
    enrollment_state text NOT NULL CHECK (
        enrollment_state IN ('DISCOVERED', 'AUTHENTICATED', 'APPROVED', 'QUARANTINED', 'DECOMMISSIONED')
    ),
    host_authority_generation bigint NOT NULL DEFAULT 0 CHECK (host_authority_generation >= 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.agent_transport_sessions (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    state text NOT NULL CHECK (state IN ('CURRENT', 'DRAINING', 'FENCED')),
    protocol_version text NOT NULL,
    agent_artifact_digest char(64) NOT NULL CHECK (agent_artifact_digest ~ '^[0-9a-f]{64}$'),
    credential_binding_revision bigint NOT NULL CHECK (credential_binding_revision > 0),
    connected_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.inbox_messages (
    consumer text NOT NULL,
    message_id text NOT NULL,
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    decision_state text NOT NULL CHECK (decision_state IN ('ACCEPTED', 'REJECTED', 'QUARANTINED')),
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    decided_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (consumer, message_id)
);

CREATE TABLE kim.outbox_messages (
    message_id text PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    event_type text NOT NULL,
    schema_version text NOT NULL,
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    payload jsonb NOT NULL,
    state text NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING', 'CLAIMED', 'DELIVERED', 'DEAD_LETTER')),
    available_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    claim_owner text,
    claim_generation bigint,
    claim_expires_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    delivered_at timestamptz,
    CHECK (
        (state = 'CLAIMED' AND claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL)
        OR (state <> 'CLAIMED' AND claim_owner IS NULL AND claim_generation IS NULL AND claim_expires_at IS NULL)
    )
);

CREATE INDEX outbox_messages_dispatch_idx
    ON kim.outbox_messages (state, available_at, created_at)
    WHERE state IN ('PENDING', 'CLAIMED');
