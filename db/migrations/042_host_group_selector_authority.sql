CREATE TABLE kim.host_group_selector_revision_evidence (
    selector_id text NOT NULL,
    selector_generation bigint NOT NULL CHECK (selector_generation > 0),
    host_group_id text NOT NULL,
    based_on_host_group_generation bigint NOT NULL CHECK (based_on_host_group_generation > 0),
    selector_schema_version text NOT NULL CHECK (selector_schema_version = 'kim.host-group.selector/v1'),
    normalized_expression jsonb NOT NULL CHECK (jsonb_typeof(normalized_expression) = 'object'),
    selector_digest char(64) NOT NULL CHECK (selector_digest ~ '^[0-9a-f]{64}$'),
    evaluator_artifact_digest char(64) NOT NULL CHECK (evaluator_artifact_digest ~ '^[0-9a-f]{64}$'),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DRAINING','RETIRED')),
    revision_digest char(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (selector_id,selector_generation),
    UNIQUE (host_group_id,selector_id,selector_generation),
    UNIQUE (selector_id,selector_generation,selector_digest),
    FOREIGN KEY (host_group_id,based_on_host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id,host_group_generation)
);

CREATE TABLE kim.host_group_selectors_current (
    selector_id text PRIMARY KEY,
    selector_generation bigint NOT NULL CHECK (selector_generation > 0),
    host_group_id text NOT NULL UNIQUE,
    based_on_host_group_generation bigint NOT NULL CHECK (based_on_host_group_generation > 0),
    selector_schema_version text NOT NULL,
    normalized_expression jsonb NOT NULL,
    selector_digest char(64) NOT NULL,
    evaluator_artifact_digest char(64) NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DRAINING','RETIRED')),
    revision_digest char(64) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (selector_id,selector_generation,selector_digest)
        REFERENCES kim.host_group_selector_revision_evidence(selector_id,selector_generation,selector_digest)
);

CREATE TABLE kim.host_group_selector_evaluation_evidence (
    evaluation_id text PRIMARY KEY,
    selector_id text NOT NULL,
    selector_generation bigint NOT NULL CHECK (selector_generation > 0),
    evaluation_generation bigint NOT NULL CHECK (evaluation_generation > 0),
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    cardinality_policy_id text NOT NULL,
    cardinality_policy_generation bigint NOT NULL CHECK (cardinality_policy_generation > 0),
    hierarchy_id text,
    hierarchy_generation bigint,
    evaluator_artifact_digest char(64) NOT NULL CHECK (evaluator_artifact_digest ~ '^[0-9a-f]{64}$'),
    selector_schema_version text NOT NULL CHECK (selector_schema_version = 'kim.host-group.selector/v1'),
    evaluated_population_digest char(64) NOT NULL CHECK (evaluated_population_digest ~ '^[0-9a-f]{64}$'),
    canonical_candidate_set_digest char(64) NOT NULL CHECK (canonical_candidate_set_digest ~ '^[0-9a-f]{64}$'),
    evaluated_host_count integer NOT NULL CHECK (evaluated_host_count > 0),
    candidate_host_count integer NOT NULL CHECK (candidate_host_count >= 0),
    result_state text NOT NULL CHECK (result_state IN ('MATCHED','NOT_MATCHED','UNKNOWN','UNSUPPORTED')),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (selector_id,evaluation_generation),
    UNIQUE (selector_id,selector_generation,evaluation_generation,evaluation_id),
    CHECK ((hierarchy_id IS NULL) = (hierarchy_generation IS NULL)),
    FOREIGN KEY (selector_id,selector_generation)
        REFERENCES kim.host_group_selector_revision_evidence(selector_id,selector_generation),
    FOREIGN KEY (host_group_id,host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id,host_group_generation),
    FOREIGN KEY (cardinality_policy_id,cardinality_policy_generation)
        REFERENCES kim.host_group_cardinality_policy_evidence(cardinality_policy_id,policy_generation),
    FOREIGN KEY (hierarchy_id,hierarchy_generation)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation)
);

CREATE TABLE kim.host_group_selector_evaluation_host_evidence (
    evaluation_id text NOT NULL REFERENCES kim.host_group_selector_evaluation_evidence(evaluation_id),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    observation_generation bigint,
    snapshot_digest char(64) CHECK (snapshot_digest IS NULL OR snapshot_digest ~ '^[0-9a-f]{64}$'),
    result_state text NOT NULL CHECK (result_state IN ('MATCHED','NOT_MATCHED','UNKNOWN','UNSUPPORTED')),
    reason_code text NOT NULL CHECK (length(reason_code) BETWEEN 1 AND 128),
    result_digest char(64) NOT NULL CHECK (result_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (evaluation_id,host_id),
    CHECK ((observation_generation IS NULL) = (snapshot_digest IS NULL)),
    CHECK (observation_generation IS NULL OR observation_generation > 0),
    FOREIGN KEY (host_id,observation_generation)
        REFERENCES kim.host_inventory_snapshots(host_id,observation_generation)
);

CREATE TABLE kim.host_group_selector_evaluations_current (
    selector_id text PRIMARY KEY,
    selector_generation bigint NOT NULL,
    evaluation_generation bigint NOT NULL,
    evaluation_id text NOT NULL,
    evaluated_population_digest char(64) NOT NULL,
    canonical_candidate_set_digest char(64) NOT NULL,
    result_state text NOT NULL CHECK (result_state IN ('MATCHED','NOT_MATCHED','UNKNOWN','UNSUPPORTED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (selector_id,selector_generation,evaluation_generation,evaluation_id)
        REFERENCES kim.host_group_selector_evaluation_evidence(selector_id,selector_generation,evaluation_generation,evaluation_id)
);

ALTER TABLE kim.host_group_membership_set_evidence
    ADD COLUMN selector_id text,
    ADD COLUMN selector_generation bigint,
    ADD COLUMN selector_evaluation_id text,
    ADD CHECK ((selector_id IS NULL) = (selector_generation IS NULL)),
    ADD CHECK ((selector_id IS NULL) = (selector_evaluation_id IS NULL)),
    ADD FOREIGN KEY (selector_id,selector_generation,selector_evaluation_generation,selector_evaluation_id)
        REFERENCES kim.host_group_selector_evaluation_evidence(selector_id,selector_generation,evaluation_generation,evaluation_id);

ALTER TABLE kim.host_group_membership_sets_current
    ADD COLUMN selector_id text,
    ADD COLUMN selector_generation bigint,
    ADD COLUMN selector_evaluation_generation bigint,
    ADD COLUMN selector_evaluation_id text,
    ADD CHECK ((selector_id IS NULL) = (selector_generation IS NULL)),
    ADD CHECK ((selector_id IS NULL) = (selector_evaluation_generation IS NULL)),
    ADD CHECK ((selector_id IS NULL) = (selector_evaluation_id IS NULL)),
    ADD FOREIGN KEY (selector_id,selector_generation,selector_evaluation_generation,selector_evaluation_id)
        REFERENCES kim.host_group_selector_evaluation_evidence(selector_id,selector_generation,evaluation_generation,evaluation_id);

CREATE TRIGGER host_group_selector_revision_evidence_no_update
    BEFORE UPDATE ON kim.host_group_selector_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_selector_evaluation_evidence_no_update
    BEFORE UPDATE ON kim.host_group_selector_evaluation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_selector_evaluation_host_evidence_no_update
    BEFORE UPDATE ON kim.host_group_selector_evaluation_host_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_group_selector_revision_evidence IS
'Immutable closed typed selector authority. It cannot contain arbitrary SQL, JSONPath, shell, filesystem paths, or backend commands.';
COMMENT ON TABLE kim.host_group_selector_evaluation_evidence IS
'Immutable proposal evidence. A selector match is not HostGroup membership authority.';
COMMENT ON TABLE kim.host_group_selector_evaluation_host_evidence IS
'Per-Host typed result preserving UNKNOWN and UNSUPPORTED separately from NOT_MATCHED.';
COMMENT ON COLUMN kim.host_group_membership_set_evidence.selector_evaluation_id IS
'Selector provenance only. Membership authority starts at this accepted complete Membership Set generation.';
