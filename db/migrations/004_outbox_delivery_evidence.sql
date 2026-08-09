ALTER TABLE kim.outbox_messages
    ADD COLUMN last_claim_generation bigint NOT NULL DEFAULT 0
        CHECK (last_claim_generation >= 0);

UPDATE kim.outbox_messages
SET last_claim_generation = claim_generation
WHERE claim_generation IS NOT NULL;

CREATE TABLE kim.outbox_delivery_attempts (
    message_id text NOT NULL REFERENCES kim.outbox_messages(message_id),
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    claim_owner text NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    started_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (message_id, claim_generation)
);

CREATE TABLE kim.outbox_delivery_events (
    message_id text NOT NULL,
    claim_generation bigint NOT NULL,
    event_type text NOT NULL CHECK (
        event_type IN ('CLAIM_GRANTED', 'DISPATCH_STARTED', 'DELIVERY_ACKNOWLEDGED', 'DISPATCH_UNKNOWN', 'DEAD_LETTERED')
    ),
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (message_id, claim_generation, event_type),
    FOREIGN KEY (message_id, claim_generation)
        REFERENCES kim.outbox_delivery_attempts(message_id, claim_generation)
);

CREATE TRIGGER outbox_delivery_attempts_no_update
    BEFORE UPDATE ON kim.outbox_delivery_attempts
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

CREATE TRIGGER outbox_delivery_events_no_update
    BEFORE UPDATE ON kim.outbox_delivery_events
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.outbox_delivery_attempts IS
    'Immutable durable delivery attempt. Lease expiry permits a new claim but does not prove that delivery did not occur.';
COMMENT ON TABLE kim.outbox_delivery_events IS
    'Append-only delivery evidence. DISPATCH_UNKNOWN is not converted to failure by time alone.';
