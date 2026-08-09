CREATE TABLE kim.inbox_message_conflicts (
    consumer text NOT NULL,
    message_id text NOT NULL,
    accepted_payload_digest char(64) NOT NULL CHECK (accepted_payload_digest ~ '^[0-9a-f]{64}$'),
    conflicting_payload_digest char(64) NOT NULL CHECK (conflicting_payload_digest ~ '^[0-9a-f]{64}$'),
    conflict_reason text NOT NULL,
    quarantined_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (consumer, message_id, conflicting_payload_digest),
    FOREIGN KEY (consumer, message_id) REFERENCES kim.inbox_messages(consumer, message_id)
);

CREATE TABLE kim.gateway_command_delivery_events (
    consumer text NOT NULL,
    message_id text NOT NULL,
    route_attempt bigint NOT NULL CHECK (route_attempt > 0),
    event_type text NOT NULL CHECK (event_type IN ('ROUTE_STARTED', 'ROUTE_ACCEPTED', 'ROUTE_UNKNOWN')),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (consumer, message_id, route_attempt, event_type),
    FOREIGN KEY (consumer, message_id) REFERENCES kim.inbox_messages(consumer, message_id)
);

CREATE TRIGGER inbox_messages_no_update
    BEFORE UPDATE ON kim.inbox_messages
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER inbox_message_conflicts_no_update
    BEFORE UPDATE ON kim.inbox_message_conflicts
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER gateway_command_delivery_events_no_update
    BEFORE UPDATE ON kim.gateway_command_delivery_events
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.inbox_messages IS
    'Durable Gateway acceptance decision. It is not Agent receipt or backend execution evidence.';
COMMENT ON TABLE kim.inbox_message_conflicts IS
    'Quarantined reuse of an internal message identity with a different payload digest.';
COMMENT ON TABLE kim.gateway_command_delivery_events IS
    'Append-only live-stream routing evidence. ROUTE_ACCEPTED is not Agent receipt or execution proof.';
