CREATE TABLE kim.placement_scope_revision_evidence (
    placement_scope_id text NOT NULL,
    scope_generation bigint NOT NULL CHECK (scope_generation > 0),
    publish_request_id text NOT NULL UNIQUE,
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    consumer_type text NOT NULL CHECK (consumer_type = 'VM_PLACEMENT'),
    project_id text NOT NULL CHECK (project_id <> ''),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DRAINING','RETIRED')),
    exposure_set_digest char(64) NOT NULL CHECK (exposure_set_digest ~ '^[0-9a-f]{64}$'),
    exposure_count integer NOT NULL CHECK (exposure_count >= 0),
    scope_digest char(64) NOT NULL CHECK (scope_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (placement_scope_id,scope_generation),
    UNIQUE (placement_scope_id,scope_generation,scope_digest)
);

CREATE TABLE kim.placement_scope_host_group_evidence (
    placement_scope_id text NOT NULL,
    scope_generation bigint NOT NULL CHECK (scope_generation > 0),
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    exposure_mode text NOT NULL CHECK (exposure_mode = 'CANDIDATE'),
    exposure_digest char(64) NOT NULL CHECK (exposure_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (placement_scope_id,scope_generation,host_group_id),
    UNIQUE (placement_scope_id,scope_generation,host_group_id,host_group_generation),
    UNIQUE (placement_scope_id,scope_generation,host_group_id,host_group_generation,exposure_digest),
    FOREIGN KEY (placement_scope_id,scope_generation)
        REFERENCES kim.placement_scope_revision_evidence(placement_scope_id,scope_generation),
    FOREIGN KEY (host_group_id,host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id,host_group_generation)
);

CREATE TABLE kim.placement_scopes_current (
    placement_scope_id text PRIMARY KEY,
    scope_generation bigint NOT NULL CHECK (scope_generation > 0),
    consumer_type text NOT NULL CHECK (consumer_type = 'VM_PLACEMENT'),
    project_id text NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DRAINING','RETIRED')),
    exposure_set_digest char(64) NOT NULL CHECK (exposure_set_digest ~ '^[0-9a-f]{64}$'),
    exposure_count integer NOT NULL CHECK (exposure_count >= 0),
    scope_digest char(64) NOT NULL CHECK (scope_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (placement_scope_id,scope_generation,scope_digest)
        REFERENCES kim.placement_scope_revision_evidence(placement_scope_id,scope_generation,scope_digest)
);

ALTER TABLE kim.placement_admission_decisions
    ADD COLUMN placement_scope_id text,
    ADD COLUMN placement_scope_generation bigint,
    ADD COLUMN placement_scope_digest char(64),
    ADD COLUMN visibility_provenance_digest char(64),
    ADD CHECK (placement_scope_generation IS NULL OR placement_scope_generation > 0),
    ADD CHECK (placement_scope_digest IS NULL OR placement_scope_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK (visibility_provenance_digest IS NULL OR visibility_provenance_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK ((placement_scope_id IS NULL) = (placement_scope_generation IS NULL)),
    ADD CHECK ((placement_scope_id IS NULL) = (placement_scope_digest IS NULL)),
    ADD CHECK ((placement_scope_id IS NULL) = (visibility_provenance_digest IS NULL)),
    ADD FOREIGN KEY (placement_scope_id,placement_scope_generation,placement_scope_digest)
        REFERENCES kim.placement_scope_revision_evidence(placement_scope_id,scope_generation,scope_digest);

CREATE TABLE kim.placement_admission_scope_visibility_evidence (
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    placement_scope_id text NOT NULL,
    scope_generation bigint NOT NULL CHECK (scope_generation > 0),
    host_id text NOT NULL,
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    membership_set_generation bigint NOT NULL CHECK (membership_set_generation > 0),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_evidence_digest char(64) NOT NULL CHECK (membership_evidence_digest ~ '^[0-9a-f]{64}$'),
    provenance_digest char(64) NOT NULL CHECK (provenance_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (admission_id,host_group_id),
    FOREIGN KEY (placement_scope_id,scope_generation,host_group_id,host_group_generation)
        REFERENCES kim.placement_scope_host_group_evidence(placement_scope_id,scope_generation,host_group_id,host_group_generation),
    FOREIGN KEY (host_group_id,membership_set_generation,host_id,membership_generation,membership_evidence_digest)
        REFERENCES kim.host_group_membership_set_member_evidence(host_group_id,membership_set_generation,host_id,membership_generation,membership_evidence_digest)
);

CREATE TRIGGER placement_scope_revision_evidence_no_update BEFORE UPDATE ON kim.placement_scope_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER placement_scope_host_group_evidence_no_update BEFORE UPDATE ON kim.placement_scope_host_group_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER placement_admission_scope_visibility_evidence_no_update BEFORE UPDATE ON kim.placement_admission_scope_visibility_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.placement_scope_revision_evidence IS
'Immutable complete Placement exposure authority. It names HostGroups, never caller-supplied Host identities or resource reservations.';
COMMENT ON TABLE kim.placement_admission_scope_visibility_evidence IS
'Immutable selected-Host visibility provenance, separate from eligibility and atomic resource claim authority.';
COMMENT ON COLUMN kim.placement_scope_revision_evidence.project_id IS
'Compatibility Project/domain identifier. Project generation authority is not yet implemented.';
