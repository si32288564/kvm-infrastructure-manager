CREATE TABLE kim.image_revision_evidence (
    image_id text NOT NULL,
    image_revision bigint NOT NULL CHECK (image_revision > 0),
    owner_project_id text NOT NULL CHECK (length(owner_project_id) BETWEEN 1 AND 255),
    image_format text NOT NULL CHECK (image_format IN ('QCOW2', 'RAW')),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    checksum_algorithm text NOT NULL CHECK (checksum_algorithm = 'SHA256'),
    declared_checksum char(64) NOT NULL CHECK (declared_checksum ~ '^[0-9a-f]{64}$'),
    observed_checksum char(64) NOT NULL CHECK (observed_checksum ~ '^[0-9a-f]{64}$'),
    signature_state text NOT NULL CHECK (signature_state IN ('VERIFIED', 'UNVERIFIED', 'FAILED')),
    signature_digest char(64) CHECK (signature_digest IS NULL OR signature_digest ~ '^[0-9a-f]{64}$'),
    source_uri text NOT NULL CHECK (length(source_uri) BETWEEN 1 AND 4096),
    visibility text NOT NULL CHECK (visibility IN ('PRIVATE', 'SHARED', 'PUBLIC')),
    validation_state text NOT NULL CHECK (validation_state IN ('VERIFIED', 'REJECTED')),
    validation_reason text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    metadata_digest char(64) NOT NULL CHECK (metadata_digest ~ '^[0-9a-f]{64}$'),
    revision_digest char(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (image_id, image_revision),
    CHECK (
        (validation_state = 'VERIFIED' AND declared_checksum = observed_checksum AND signature_state <> 'FAILED')
        OR validation_state = 'REJECTED'
    )
);

CREATE TABLE kim.images_current (
    image_id text PRIMARY KEY,
    image_revision bigint NOT NULL,
    owner_project_id text NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DELETING', 'DELETED')),
    authority_generation bigint NOT NULL CHECK (authority_generation > 0),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (image_id, image_revision)
        REFERENCES kim.image_revision_evidence(image_id, image_revision),
    CHECK (lifecycle_state <> 'ACTIVE' OR image_revision > 0)
);

CREATE TABLE kim.flavor_revision_evidence (
    flavor_id text NOT NULL,
    flavor_revision bigint NOT NULL CHECK (flavor_revision > 0),
    owner_project_id text NOT NULL CHECK (length(owner_project_id) BETWEEN 1 AND 255),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    vcpus integer NOT NULL CHECK (vcpus > 0),
    memory_mib bigint NOT NULL CHECK (memory_mib > 0),
    root_disk_gib bigint NOT NULL CHECK (root_disk_gib > 0),
    numa_policy text NOT NULL CHECK (numa_policy IN ('NONE', 'REQUIRED')),
    numa_nodes integer CHECK (numa_nodes IS NULL OR numa_nodes > 0),
    hugepage_size_kib bigint CHECK (hugepage_size_kib IS NULL OR hugepage_size_kib > 0),
    cpu_allocation text NOT NULL CHECK (cpu_allocation IN ('SHARED', 'DEDICATED')),
    cpu_pinning boolean NOT NULL,
    extra_specs jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(extra_specs) = 'object'),
    shape_digest char(64) NOT NULL CHECK (shape_digest ~ '^[0-9a-f]{64}$'),
    revision_digest char(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (flavor_id, flavor_revision),
    CHECK ((numa_policy = 'REQUIRED' AND numa_nodes IS NOT NULL) OR (numa_policy = 'NONE' AND numa_nodes IS NULL)),
    CHECK (NOT cpu_pinning OR cpu_allocation = 'DEDICATED')
);

CREATE TABLE kim.flavors_current (
    flavor_id text PRIMARY KEY,
    flavor_revision bigint NOT NULL,
    owner_project_id text NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DELETED')),
    authority_generation bigint NOT NULL CHECK (authority_generation > 0),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (flavor_id, flavor_revision)
        REFERENCES kim.flavor_revision_evidence(flavor_id, flavor_revision)
);

CREATE TRIGGER image_revision_evidence_no_update
    BEFORE UPDATE ON kim.image_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER flavor_revision_evidence_no_update
    BEFORE UPDATE ON kim.flavor_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

CREATE FUNCTION kim.assert_current_image_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    evidence_state text;
    evidence_owner text;
BEGIN
    SELECT validation_state, owner_project_id
      INTO evidence_state, evidence_owner
      FROM kim.image_revision_evidence
     WHERE image_id = NEW.image_id AND image_revision = NEW.image_revision;
    IF evidence_state IS DISTINCT FROM 'VERIFIED' OR evidence_owner IS DISTINCT FROM NEW.owner_project_id THEN
        RAISE EXCEPTION 'current Image must reference a verified revision with the same owner';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION kim.assert_current_flavor_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    evidence_owner text;
BEGIN
    SELECT owner_project_id
      INTO evidence_owner
      FROM kim.flavor_revision_evidence
     WHERE flavor_id = NEW.flavor_id AND flavor_revision = NEW.flavor_revision;
    IF evidence_owner IS DISTINCT FROM NEW.owner_project_id THEN
        RAISE EXCEPTION 'current Flavor must reference a revision with the same owner';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER images_current_verified_revision
    BEFORE INSERT OR UPDATE OF image_revision, owner_project_id ON kim.images_current
    FOR EACH ROW EXECUTE FUNCTION kim.assert_current_image_revision();
CREATE TRIGGER flavors_current_owned_revision
    BEFORE INSERT OR UPDATE OF flavor_revision, owner_project_id ON kim.flavors_current
    FOR EACH ROW EXECUTE FUNCTION kim.assert_current_flavor_revision();

COMMENT ON TABLE kim.image_revision_evidence IS
    'Immutable Image metadata and validation evidence. A rejected revision is never boot authority.';
COMMENT ON TABLE kim.images_current IS
    'Current Image lifecycle authority. ACTIVE always references a verified immutable revision.';
COMMENT ON TABLE kim.flavor_revision_evidence IS
    'Immutable, canonical workload shape. Every placement-relevant field is included in shape_digest.';
COMMENT ON TABLE kim.flavors_current IS
    'Current Flavor lifecycle authority. Placement snapshots bind the immutable revision and digest.';
