ALTER TABLE kim.vm_materialization_readiness_current
    ADD COLUMN image_observation_generation bigint CHECK (image_observation_generation > 0),
    ADD COLUMN image_evidence_id text;

CREATE TABLE kim.vm_image_realization_evidence (
    evidence_id text PRIMARY KEY CHECK (length(evidence_id) BETWEEN 1 AND 512),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    image_id text NOT NULL,
    image_revision bigint NOT NULL CHECK (image_revision > 0),
    expected_content_digest char(64) NOT NULL CHECK (expected_content_digest ~ '^[0-9a-f]{64}$'),
    observed_content_digest char(64) NOT NULL CHECK (observed_content_digest ~ '^[0-9a-f]{64}$'),
    image_size_bytes bigint NOT NULL CHECK (image_size_bytes > 0),
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    binding_id text NOT NULL REFERENCES kim.volume_backend_binding_intents(binding_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    observed_vg_uuid text NOT NULL,
    observed_lv_uuid text NOT NULL,
    backend_resource_key text NOT NULL,
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    holder_open boolean NOT NULL,
    content_identity_matches boolean NOT NULL,
    evidence_state text NOT NULL CHECK (evidence_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN', 'NOT_APPLIED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (vm_id, vm_generation) REFERENCES kim.virtual_machines_current(vm_id, vm_generation),
    FOREIGN KEY (image_id, image_revision) REFERENCES kim.image_revision_evidence(image_id, image_revision),
    UNIQUE (evidence_id, vm_id, vm_generation)
);

ALTER TABLE kim.vm_materialization_readiness_current
    ADD CONSTRAINT vm_readiness_image_evidence_fk
    FOREIGN KEY (image_evidence_id, vm_id, vm_generation)
    REFERENCES kim.vm_image_realization_evidence(evidence_id, vm_id, vm_generation);

CREATE TRIGGER vm_image_realization_evidence_no_update
    BEFORE UPDATE ON kim.vm_image_realization_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_image_realization_evidence IS
    'Immutable bounded destination content read-back. Retrieval/copy success alone is not Image realization or boot authority.';
COMMENT ON COLUMN kim.vm_image_realization_evidence.observed_content_digest IS
    'SHA-256 of exactly image_size_bytes read from the identity-verified target LV; it is not a digest of trailing Volume capacity.';
