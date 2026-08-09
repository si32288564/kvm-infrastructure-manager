CREATE TABLE kim.storage_backends_current (
    backend_id text PRIMARY KEY CHECK (length(backend_id) BETWEEN 1 AND 255),
    backend_type text NOT NULL CHECK (backend_type = 'LOCAL_LVM'),
    backend_generation bigint NOT NULL CHECK (backend_generation > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DRAINING', 'DISABLED')),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vg_uuid text NOT NULL CHECK (length(vg_uuid) BETWEEN 1 AND 255),
    capability_generation bigint NOT NULL CHECK (capability_generation > 0),
    capability_state text NOT NULL CHECK (capability_state IN ('CURRENT', 'STALE', 'UNKNOWN', 'BLOCKED')),
    support_tier text NOT NULL CHECK (support_tier IN ('VALIDATED', 'SUPPORTED', 'EXPERIMENTAL')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (host_id, vg_uuid),
    UNIQUE (backend_id, host_id, vg_uuid)
);

CREATE TABLE kim.storage_class_revision_evidence (
    storage_class_id text NOT NULL CHECK (length(storage_class_id) BETWEEN 1 AND 255),
    class_revision bigint NOT NULL CHECK (class_revision > 0),
    allowed_backend_type text NOT NULL CHECK (allowed_backend_type = 'LOCAL_LVM'),
    locality text NOT NULL CHECK (locality = 'HOST_LOCAL'),
    access_modes text[] NOT NULL CHECK (
        cardinality(access_modes) > 0
        AND access_modes <@ ARRAY['SINGLE_WRITER']::text[]
    ),
    thin_provisioning boolean NOT NULL,
    encryption_required boolean NOT NULL,
    fencing_policy_revision bigint NOT NULL CHECK (fencing_policy_revision > 0),
    class_digest char(64) NOT NULL CHECK (class_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (storage_class_id, class_revision)
);

CREATE TABLE kim.storage_classes_current (
    storage_class_id text PRIMARY KEY,
    class_revision bigint NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DEPRECATED', 'DISABLED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (storage_class_id, class_revision)
        REFERENCES kim.storage_class_revision_evidence(storage_class_id, class_revision)
);

CREATE TABLE kim.storage_capacity_observation_evidence (
    observation_id text PRIMARY KEY CHECK (length(observation_id) BETWEEN 1 AND 255),
    backend_id text NOT NULL REFERENCES kim.storage_backends_current(backend_id),
    capacity_generation bigint NOT NULL CHECK (capacity_generation > 0),
    host_capability_generation bigint NOT NULL CHECK (host_capability_generation > 0),
    total_bytes bigint NOT NULL CHECK (total_bytes > 0),
    observed_free_bytes bigint NOT NULL CHECK (observed_free_bytes >= 0),
    external_or_unknown_bytes bigint NOT NULL CHECK (external_or_unknown_bytes >= 0),
    hard_reserve_bytes bigint NOT NULL CHECK (hard_reserve_bytes >= 0),
    health_state text NOT NULL CHECK (health_state IN ('HEALTHY', 'DEGRADED', 'UNTRUSTED', 'UNKNOWN')),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (backend_id, capacity_generation),
    UNIQUE (observation_id, backend_id, capacity_generation),
    CHECK (observed_free_bytes <= total_bytes),
    CHECK (external_or_unknown_bytes <= total_bytes),
    CHECK (hard_reserve_bytes < total_bytes)
);

CREATE TABLE kim.storage_capacity_projections_current (
    backend_id text PRIMARY KEY REFERENCES kim.storage_backends_current(backend_id),
    capacity_generation bigint NOT NULL CHECK (capacity_generation > 0),
    observation_id text NOT NULL,
    projection_state text NOT NULL CHECK (projection_state IN ('CURRENT', 'STALE', 'UNKNOWN', 'BLOCKED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (observation_id, backend_id, capacity_generation)
        REFERENCES kim.storage_capacity_observation_evidence(observation_id, backend_id, capacity_generation)
);

ALTER TABLE kim.placement_admission_decisions
    ADD COLUMN storage_requirements jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(storage_requirements) = 'array'),
    ADD COLUMN storage_requirements_digest char(64) NOT NULL DEFAULT '4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945'
        CHECK (storage_requirements_digest ~ '^[0-9a-f]{64}$');

ALTER TABLE kim.placement_admission_decisions
    ALTER COLUMN storage_requirements DROP DEFAULT,
    ALTER COLUMN storage_requirements_digest DROP DEFAULT;

CREATE TABLE kim.volumes_current (
    volume_id text PRIMARY KEY CHECK (length(volume_id) BETWEEN 1 AND 255),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    project_id text NOT NULL CHECK (length(project_id) BETWEEN 1 AND 255),
    storage_class_id text NOT NULL,
    storage_class_revision bigint NOT NULL,
    desired_generation bigint NOT NULL CHECK (desired_generation > 0),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    access_mode text NOT NULL CHECK (access_mode = 'SINGLE_WRITER'),
    bootable boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('RESERVED', 'CREATING', 'AVAILABLE', 'UNKNOWN', 'BLOCKED', 'DELETE_PENDING', 'DELETED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (placement_admission_id, volume_id),
    FOREIGN KEY (storage_class_id, storage_class_revision)
        REFERENCES kim.storage_class_revision_evidence(storage_class_id, class_revision)
);

CREATE TABLE kim.storage_capacity_claims (
    capacity_claim_id text PRIMARY KEY CHECK (length(capacity_claim_id) BETWEEN 1 AND 512),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    backend_id text NOT NULL REFERENCES kim.storage_backends_current(backend_id),
    volume_id text NOT NULL UNIQUE REFERENCES kim.volumes_current(volume_id),
    capacity_generation bigint NOT NULL CHECK (capacity_generation > 0),
    reserved_bytes bigint NOT NULL CHECK (reserved_bytes > 0),
    claim_state text NOT NULL CHECK (claim_state IN ('RESERVED', 'ALLOCATED', 'RELEASE_PENDING', 'QUARANTINED', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (placement_admission_id, volume_id),
    FOREIGN KEY (backend_id, capacity_generation)
        REFERENCES kim.storage_capacity_observation_evidence(backend_id, capacity_generation)
);

CREATE INDEX storage_capacity_claims_backend_ledger
    ON kim.storage_capacity_claims(backend_id, claim_state);

CREATE TABLE kim.volume_backend_binding_intents (
    binding_id text PRIMARY KEY CHECK (length(binding_id) BETWEEN 1 AND 512),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    volume_id text NOT NULL UNIQUE REFERENCES kim.volumes_current(volume_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    backend_id text NOT NULL REFERENCES kim.storage_backends_current(backend_id),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vg_uuid text NOT NULL CHECK (length(vg_uuid) BETWEEN 1 AND 255),
    backend_resource_key text NOT NULL CHECK (backend_resource_key ~ '^kim-[0-9a-f]{32}$'),
    binding_state text NOT NULL CHECK (binding_state IN ('RESERVED', 'PREPARING', 'VERIFYING', 'BOUND', 'UNKNOWN', 'BLOCKED', 'RELEASE_PENDING', 'RELEASED')),
    observed_lv_uuid text,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK ((binding_state = 'BOUND' AND observed_lv_uuid IS NOT NULL)
        OR (binding_state <> 'BOUND')),
    UNIQUE (host_id, vg_uuid, backend_resource_key),
    FOREIGN KEY (backend_id, host_id, vg_uuid)
        REFERENCES kim.storage_backends_current(backend_id, host_id, vg_uuid)
);

CREATE TABLE kim.volume_attachments_current (
    attachment_id text PRIMARY KEY CHECK (length(attachment_id) BETWEEN 1 AND 255),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    workload_id text NOT NULL CHECK (length(workload_id) BETWEEN 1 AND 255),
    desired_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    attachment_generation bigint NOT NULL CHECK (attachment_generation > 0),
    access_mode text NOT NULL CHECK (access_mode = 'SINGLE_WRITER'),
    desired_state text NOT NULL CHECK (desired_state IN ('RESERVED', 'PREPARING', 'ATTACHING', 'VERIFYING', 'ATTACHED', 'UNKNOWN', 'BLOCKED', 'FENCE_REQUIRED', 'RELEASE_PENDING', 'DETACHED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (placement_admission_id, attachment_id),
    UNIQUE (attachment_id, volume_id)
);

CREATE TABLE kim.volume_attachment_claims (
    attachment_claim_id text PRIMARY KEY CHECK (length(attachment_claim_id) BETWEEN 1 AND 512),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    attachment_id text NOT NULL UNIQUE,
    volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    workload_id text NOT NULL CHECK (length(workload_id) BETWEEN 1 AND 255),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    attachment_generation bigint NOT NULL CHECK (attachment_generation > 0),
    access_mode text NOT NULL CHECK (access_mode = 'SINGLE_WRITER'),
    fencing_policy_revision bigint NOT NULL CHECK (fencing_policy_revision > 0),
    claim_state text NOT NULL CHECK (claim_state IN ('RESERVED', 'ACTIVE', 'UNKNOWN', 'FENCE_REQUIRED', 'RELEASE_PENDING', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (attachment_id, volume_id)
        REFERENCES kim.volume_attachments_current(attachment_id, volume_id)
);

CREATE UNIQUE INDEX volume_single_writer_active_claim
    ON kim.volume_attachment_claims(volume_id)
    WHERE access_mode = 'SINGLE_WRITER'
      AND claim_state IN ('RESERVED', 'ACTIVE', 'UNKNOWN', 'FENCE_REQUIRED', 'RELEASE_PENDING');

CREATE TRIGGER storage_class_revision_evidence_no_update
    BEFORE UPDATE ON kim.storage_class_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER storage_capacity_observation_evidence_no_update
    BEFORE UPDATE ON kim.storage_capacity_observation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.storage_capacity_claims IS
    'PostgreSQL Local LVM capacity reservation authority. Backend free/used values remain observation and do not replace this ledger.';
COMMENT ON TABLE kim.volume_backend_binding_intents IS
    'Desired Local LVM binding intent. RESERVED does not assert that an LV exists; observed_lv_uuid is established only by typed realization/read-back evidence.';
COMMENT ON TABLE kim.volume_attachment_claims IS
    'Logical single-writer attachment authority. A DB Claim alone is not physical I/O exclusion or detach/fencing evidence.';
COMMENT ON COLUMN kim.placement_admission_decisions.storage_requirements IS
    'Canonical Storage/Volume/Attachment requirements fixed by Final Admission for audit and idempotency scope.';
