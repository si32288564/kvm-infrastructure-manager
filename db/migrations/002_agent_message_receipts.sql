CREATE TABLE kim.agent_message_receipts (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    logical_stream text NOT NULL,
    message_id text NOT NULL,
    sequence_scope text NOT NULL,
    sequence_number bigint NOT NULL CHECK (sequence_number >= 0),
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    disposition text NOT NULL CHECK (disposition IN ('ACCEPTED', 'STALE', 'REJECTED', 'QUARANTINED')),
    receipt_payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, message_id),
    UNIQUE (host_id, logical_stream, sequence_scope, sequence_number)
);

CREATE TABLE kim.agent_resync_checkpoints (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    journal_digest char(64) NOT NULL CHECK (journal_digest ~ '^[0-9a-f]{64}$'),
    checkpoint_payload jsonb NOT NULL,
    verified_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
