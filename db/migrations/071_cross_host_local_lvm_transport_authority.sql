CREATE TABLE kim.local_lvm_relocation_transport_session_evidence (
    transport_session_id text PRIMARY KEY,
    transport_generation bigint NOT NULL CHECK(transport_generation>0),
    copy_operation_id text NOT NULL,
    copy_generation bigint NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_host_authority_generation bigint NOT NULL CHECK(source_host_authority_generation>0),
    source_credential_binding_revision bigint NOT NULL CHECK(source_credential_binding_revision>0),
    source_session_generation bigint NOT NULL CHECK(source_session_generation>0),
    source_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    source_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    source_binding_generation bigint NOT NULL CHECK(source_binding_generation>0),
    source_vg_uuid text NOT NULL,
    source_lv_uuid text NOT NULL,
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_host_authority_generation bigint NOT NULL CHECK(destination_host_authority_generation>0),
    destination_credential_binding_revision bigint NOT NULL CHECK(destination_credential_binding_revision>0),
    destination_session_generation bigint NOT NULL CHECK(destination_session_generation>0),
    destination_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    destination_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    destination_binding_generation bigint NOT NULL CHECK(destination_binding_generation>0),
    destination_vg_uuid text NOT NULL,
    destination_lv_uuid text NOT NULL,
    exact_byte_count bigint NOT NULL CHECK(exact_byte_count>0),
    chunk_size_bytes integer NOT NULL CHECK(chunk_size_bytes BETWEEN 4096 AND 4194304),
    chunk_profile text NOT NULL CHECK(chunk_profile='EXACT_OFFSET_SHA256_V1'),
    digest_algorithm text NOT NULL CHECK(digest_algorithm='SHA-256'),
    transport_policy_revision bigint NOT NULL CHECK(transport_policy_revision>0),
    maximum_concurrent_per_host integer NOT NULL CHECK(maximum_concurrent_per_host BETWEEN 1 AND 64),
    bandwidth_limit_bytes_per_second bigint CHECK(bandwidth_limit_bytes_per_second IS NULL OR bandwidth_limit_bytes_per_second>0),
    expires_at timestamptz NOT NULL,
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(copy_operation_id,copy_generation),
    UNIQUE(transport_session_id,transport_generation),
    FOREIGN KEY(copy_operation_id,copy_generation) REFERENCES kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation),
    CHECK(source_host_id<>destination_host_id),
    CHECK(source_lv_uuid<>destination_lv_uuid),
    CHECK(expires_at>recorded_at)
);

CREATE TABLE kim.local_lvm_relocation_transport_sessions_current (
    transport_session_id text PRIMARY KEY,
    transport_generation bigint NOT NULL,
    session_state text NOT NULL CHECK(session_state IN ('AUTHORIZED','STREAMING','PARTIAL','UNKNOWN','COMPLETED','VERIFIED','CONFLICTING','EXPIRED')),
    attempt_index integer NOT NULL DEFAULT 0 CHECK(attempt_index>=0),
    bytes_transferred bigint NOT NULL DEFAULT 0 CHECK(bytes_transferred>=0),
    last_verified_offset bigint NOT NULL DEFAULT 0 CHECK(last_verified_offset>=0),
    response_state text NOT NULL DEFAULT 'PENDING' CHECK(response_state IN ('PENDING','RECEIVED','LOST','UNKNOWN')),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(transport_session_id,transport_generation) REFERENCES kim.local_lvm_relocation_transport_session_evidence(transport_session_id,transport_generation),
    CHECK(last_verified_offset<=bytes_transferred)
);

CREATE TABLE kim.local_lvm_relocation_transport_event_evidence (
    transport_session_id text NOT NULL,
    transport_generation bigint NOT NULL,
    event_sequence bigint NOT NULL CHECK(event_sequence>0),
    attempt_index integer NOT NULL CHECK(attempt_index>0),
    event_type text NOT NULL CHECK(event_type IN ('STARTED','PROGRESS','DISCONNECTED','LEASE_EXPIRED','READ_BACK','COMPLETED','CONFLICT')),
    bytes_transferred bigint NOT NULL CHECK(bytes_transferred>=0),
    last_verified_offset bigint NOT NULL CHECK(last_verified_offset>=0),
    response_state text NOT NULL CHECK(response_state IN ('PENDING','RECEIVED','LOST','UNKNOWN')),
    event_digest char(64) NOT NULL CHECK(event_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(transport_session_id,transport_generation,event_sequence),
    FOREIGN KEY(transport_session_id,transport_generation) REFERENCES kim.local_lvm_relocation_transport_session_evidence(transport_session_id,transport_generation),
    CHECK(last_verified_offset<=bytes_transferred)
);

CREATE TABLE kim.local_lvm_relocation_transport_peer_observation_evidence (
    peer_evidence_id text PRIMARY KEY,
    transport_session_id text NOT NULL,
    transport_generation bigint NOT NULL,
    peer_role text NOT NULL CHECK(peer_role IN ('SOURCE','DESTINATION')),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    credential_binding_revision bigint NOT NULL CHECK(credential_binding_revision>0),
    session_generation bigint NOT NULL CHECK(session_generation>0),
    peer_certificate_fingerprint char(64) NOT NULL CHECK(peer_certificate_fingerprint ~ '^[0-9a-f]{64}$'),
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    lv_uuid text NOT NULL,
    size_bytes bigint NOT NULL CHECK(size_bytes>0),
    content_digest char(64) NOT NULL CHECK(content_digest ~ '^[0-9a-f]{64}$'),
    holder_open boolean NOT NULL,
    observation_generation bigint NOT NULL CHECK(observation_generation>0),
    observation_digest char(64) NOT NULL CHECK(observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_artifact_digest char(64) NOT NULL CHECK(verifier_artifact_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(transport_session_id,transport_generation,peer_role),
    FOREIGN KEY(transport_session_id,transport_generation) REFERENCES kim.local_lvm_relocation_transport_session_evidence(transport_session_id,transport_generation)
);

CREATE TABLE kim.local_lvm_relocation_transport_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    transport_session_id text NOT NULL,
    transport_generation bigint NOT NULL,
    copy_operation_id text NOT NULL,
    copy_generation bigint NOT NULL,
    attempt_index integer NOT NULL CHECK(attempt_index>0),
    bytes_transferred bigint NOT NULL CHECK(bytes_transferred>0),
    response_state text NOT NULL CHECK(response_state IN ('RECEIVED','LOST')),
    source_peer_evidence_id text NOT NULL REFERENCES kim.local_lvm_relocation_transport_peer_observation_evidence(peer_evidence_id),
    destination_peer_evidence_id text NOT NULL REFERENCES kim.local_lvm_relocation_transport_peer_observation_evidence(peer_evidence_id),
    source_content_digest char(64) NOT NULL CHECK(source_content_digest ~ '^[0-9a-f]{64}$'),
    destination_content_digest char(64) NOT NULL CHECK(destination_content_digest ~ '^[0-9a-f]{64}$'),
    terminal_state text NOT NULL CHECK(terminal_state='VERIFIED'),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(transport_session_id,transport_generation),
    UNIQUE(terminal_evidence_id,terminal_digest),
    FOREIGN KEY(transport_session_id,transport_generation) REFERENCES kim.local_lvm_relocation_transport_session_evidence(transport_session_id,transport_generation),
    FOREIGN KEY(copy_operation_id,copy_generation) REFERENCES kim.local_lvm_relocation_copy_operation_evidence(copy_operation_id,copy_generation),
    CHECK(source_content_digest=destination_content_digest)
);

ALTER TABLE kim.local_lvm_relocation_transport_sessions_current ADD CONSTRAINT local_lvm_transport_current_terminal_fkey FOREIGN KEY(terminal_evidence_id) REFERENCES kim.local_lvm_relocation_transport_terminal_evidence(terminal_evidence_id);
ALTER TABLE kim.local_lvm_relocation_copy_verification_evidence ADD COLUMN transport_terminal_evidence_id text REFERENCES kim.local_lvm_relocation_transport_terminal_evidence(terminal_evidence_id);

CREATE INDEX local_lvm_transport_source_host ON kim.local_lvm_relocation_transport_session_evidence(source_host_id,recorded_at);
CREATE INDEX local_lvm_transport_destination_host ON kim.local_lvm_relocation_transport_session_evidence(destination_host_id,recorded_at);

CREATE TRIGGER local_lvm_relocation_transport_session_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_transport_session_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_relocation_transport_event_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_transport_event_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_relocation_transport_peer_observation_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_transport_peer_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER local_lvm_relocation_transport_terminal_evidence_no_update BEFORE UPDATE ON kim.local_lvm_relocation_transport_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.local_lvm_relocation_transport_session_evidence IS 'Bounded cross-Host Local LVM data-plane authority. It binds both current Agent credential/session generations and exact source/destination LV incarnations; it contains no device path, shell, argv, or guest blocks.';
COMMENT ON TABLE kim.local_lvm_relocation_transport_event_evidence IS 'Progress and disconnect facts are immutable observations, never content-preservation authority.';
COMMENT ON TABLE kim.local_lvm_relocation_transport_terminal_evidence IS 'Cross-Agent transport terminal derived only after exact source/destination whole-volume read-back equality; TLS or stream completion alone is insufficient.';
