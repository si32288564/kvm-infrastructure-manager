CREATE TABLE kim.volume_backend_binding_evidence (
    evidence_id text PRIMARY KEY CHECK (length(evidence_id) BETWEEN 1 AND 512),
    binding_id text NOT NULL REFERENCES kim.volume_backend_binding_intents(binding_id),
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    backend_id text NOT NULL REFERENCES kim.storage_backends_current(backend_id),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vg_uuid text NOT NULL CHECK (length(vg_uuid) BETWEEN 1 AND 255),
    lv_uuid text NOT NULL CHECK (length(lv_uuid) BETWEEN 1 AND 255),
    backend_resource_key text NOT NULL CHECK (backend_resource_key ~ '^kim-[0-9a-f]{32}$'),
    observed_size_bytes bigint NOT NULL CHECK (observed_size_bytes > 0),
    command_id text NOT NULL CHECK (length(command_id) BETWEEN 1 AND 255),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    evidence_state text NOT NULL CHECK (evidence_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN', 'NOT_APPLIED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (binding_id, binding_generation, command_id, attempt_index, observation_digest),
    UNIQUE (evidence_id, binding_id, binding_generation)
);

CREATE TABLE kim.volume_backend_bindings_current (
    binding_id text PRIMARY KEY REFERENCES kim.volume_backend_binding_intents(binding_id),
    volume_id text NOT NULL UNIQUE REFERENCES kim.volumes_current(volume_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    evidence_id text NOT NULL,
    binding_state text NOT NULL CHECK (binding_state IN ('BOUND', 'STALE', 'UNKNOWN', 'REVOKED')),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vg_uuid text NOT NULL CHECK (length(vg_uuid) BETWEEN 1 AND 255),
    lv_uuid text NOT NULL CHECK (length(lv_uuid) BETWEEN 1 AND 255),
    backend_resource_key text NOT NULL CHECK (backend_resource_key ~ '^kim-[0-9a-f]{32}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (evidence_id, binding_id, binding_generation)
        REFERENCES kim.volume_backend_binding_evidence(evidence_id, binding_id, binding_generation)
);

CREATE TRIGGER volume_backend_binding_evidence_no_update
    BEFORE UPDATE ON kim.volume_backend_binding_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.volume_backend_binding_evidence IS
    'Immutable typed Local LVM read-back evidence. LV UUID is observed from standard LVM and is never derived from Final Admission intent.';
COMMENT ON TABLE kim.volume_backend_bindings_current IS
    'Current Local LVM binding projection rebuilt from accepted immutable evidence. It is separate from desired Binding Intent.';
