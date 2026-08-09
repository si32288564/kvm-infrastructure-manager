CREATE TABLE kim.host_inventory_snapshots (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    message_id text NOT NULL,
    schema_version text NOT NULL,
    collection_status text NOT NULL CHECK (collection_status IN ('COMPLETE', 'DEGRADED')),
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    capability_digest char(64) NOT NULL CHECK (capability_digest ~ '^[0-9a-f]{64}$'),
    snapshot_payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, observation_generation),
    UNIQUE (host_id, message_id),
    UNIQUE (host_id, observation_generation, message_id),
    FOREIGN KEY (host_id, message_id)
        REFERENCES kim.agent_message_receipts(host_id, message_id)
);

CREATE TABLE kim.host_capability_projections (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    source_message_id text NOT NULL,
    schema_version text NOT NULL,
    projection_state text NOT NULL CHECK (projection_state IN ('CURRENT', 'DEGRADED')),
    snapshot_digest char(64) NOT NULL CHECK (snapshot_digest ~ '^[0-9a-f]{64}$'),
    capability_digest char(64) NOT NULL CHECK (capability_digest ~ '^[0-9a-f]{64}$'),
    capability_payload jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (host_id, observation_generation, source_message_id)
        REFERENCES kim.host_inventory_snapshots(host_id, observation_generation, message_id)
);

CREATE TRIGGER host_inventory_snapshots_no_update
    BEFORE UPDATE ON kim.host_inventory_snapshots
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_inventory_snapshots IS
    'Immutable normalized Host observation evidence. It is not placement or mutation authority by itself.';
COMMENT ON TABLE kim.host_capability_projections IS
    'Rebuildable current capability projection derived from accepted Host inventory evidence.';
