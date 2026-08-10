CREATE TABLE kim.volume_attachment_observation_evidence (
    evidence_id text PRIMARY KEY CHECK (length(evidence_id) BETWEEN 1 AND 512),
    attachment_id text NOT NULL,
    volume_id text NOT NULL,
    attachment_generation bigint NOT NULL CHECK (attachment_generation > 0),
    binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    domain_uuid uuid NOT NULL,
    target_device text NOT NULL CHECK (target_device ~ '^vd[b-z]$'),
    observed_lv_uuid text NOT NULL CHECK (length(observed_lv_uuid) BETWEEN 1 AND 255),
    desired_state text NOT NULL CHECK (desired_state IN ('ATTACHED', 'DETACHED')),
    device_present boolean NOT NULL,
    device_identity_matches boolean NOT NULL,
    source_identity_matches boolean NOT NULL,
    holder_open boolean NOT NULL,
    read_only boolean NOT NULL,
    command_id text NOT NULL CHECK (length(command_id) BETWEEN 1 AND 255),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    evidence_state text NOT NULL CHECK (evidence_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN', 'NOT_APPLIED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (attachment_id, volume_id)
        REFERENCES kim.volume_attachments_current(attachment_id, volume_id),
    UNIQUE (evidence_id, attachment_id, attachment_generation)
);

CREATE TABLE kim.volume_attachment_observations_current (
    attachment_id text PRIMARY KEY,
    volume_id text NOT NULL,
    attachment_generation bigint NOT NULL CHECK (attachment_generation > 0),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    evidence_id text NOT NULL,
    attachment_state text NOT NULL CHECK (attachment_state IN ('ATTACHED', 'DETACHED', 'UNKNOWN', 'CONFLICTING')),
    binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    domain_uuid uuid NOT NULL,
    target_device text NOT NULL CHECK (target_device ~ '^vd[b-z]$'),
    observed_lv_uuid text NOT NULL CHECK (length(observed_lv_uuid) BETWEEN 1 AND 255),
    device_present boolean NOT NULL,
    holder_open boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (attachment_id, volume_id)
        REFERENCES kim.volume_attachments_current(attachment_id, volume_id),
    FOREIGN KEY (evidence_id, attachment_id, attachment_generation)
        REFERENCES kim.volume_attachment_observation_evidence(evidence_id, attachment_id, attachment_generation)
);

CREATE TRIGGER volume_attachment_observation_evidence_no_update
    BEFORE UPDATE ON kim.volume_attachment_observation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.volume_attachment_observation_evidence IS
    'Immutable typed libvirt device plus Local LVM open-holder read-back. A libvirt response alone is not ATTACHED or DETACHED authority.';
COMMENT ON TABLE kim.volume_attachment_observations_current IS
    'Current attachment convergence projection rebuilt from accepted immutable device/holder evidence.';
