CREATE TABLE kim.host_group_hierarchy_set_evidence (
    hierarchy_id text NOT NULL,
    hierarchy_generation bigint NOT NULL CHECK (hierarchy_generation > 0),
    publish_request_id text NOT NULL UNIQUE CHECK (length(publish_request_id) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    group_type text NOT NULL CHECK (group_type IN ('PLACEMENT_POOL','FAILURE_DOMAIN','OPERATIONAL_COHORT')),
    dimension text NOT NULL CHECK (length(dimension) BETWEEN 1 AND 128),
    scope_type text NOT NULL CHECK (scope_type = 'SYSTEM'),
    scope_id text NOT NULL CHECK (scope_id = 'system'),
    graph_mode text NOT NULL CHECK (graph_mode = 'TREE'),
    canonical_level_order_digest char(64) NOT NULL CHECK (canonical_level_order_digest ~ '^[0-9a-f]{64}$'),
    canonical_node_set_digest char(64) NOT NULL CHECK (canonical_node_set_digest ~ '^[0-9a-f]{64}$'),
    canonical_relation_set_digest char(64) NOT NULL CHECK (canonical_relation_set_digest ~ '^[0-9a-f]{64}$'),
    level_count integer NOT NULL CHECK (level_count >= 1),
    node_count integer NOT NULL CHECK (node_count >= 1),
    relation_count integer NOT NULL CHECK (relation_count >= 0),
    validation_state text NOT NULL CHECK (validation_state = 'ACCEPTED'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (hierarchy_id,hierarchy_generation),
    UNIQUE (group_type,dimension,scope_type,scope_id,hierarchy_generation),
    UNIQUE (hierarchy_id,hierarchy_generation,canonical_relation_set_digest)
);

CREATE TABLE kim.host_group_hierarchy_level_evidence (
    hierarchy_id text NOT NULL,
    hierarchy_generation bigint NOT NULL,
    level text NOT NULL CHECK (length(level) BETWEEN 1 AND 128),
    level_rank integer NOT NULL CHECK (level_rank >= 0),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (hierarchy_id,hierarchy_generation,level),
    UNIQUE (hierarchy_id,hierarchy_generation,level_rank),
    FOREIGN KEY (hierarchy_id,hierarchy_generation)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation)
);

CREATE TABLE kim.host_group_hierarchy_node_evidence (
    hierarchy_id text NOT NULL,
    hierarchy_generation bigint NOT NULL,
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    level text NOT NULL,
    node_digest char(64) NOT NULL CHECK (node_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (hierarchy_id,hierarchy_generation,host_group_id),
    FOREIGN KEY (hierarchy_id,hierarchy_generation,level)
        REFERENCES kim.host_group_hierarchy_level_evidence(hierarchy_id,hierarchy_generation,level),
    FOREIGN KEY (host_group_id,host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id,host_group_generation)
);

CREATE TABLE kim.host_group_hierarchy_relation_evidence (
    hierarchy_id text NOT NULL,
    hierarchy_generation bigint NOT NULL,
    parent_group_id text NOT NULL,
    child_group_id text NOT NULL,
    parent_level text NOT NULL,
    child_level text NOT NULL,
    relation_digest char(64) NOT NULL CHECK (relation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (hierarchy_id,hierarchy_generation,parent_group_id,child_group_id),
    UNIQUE (hierarchy_id,hierarchy_generation,child_group_id),
    CHECK (parent_group_id <> child_group_id),
    FOREIGN KEY (hierarchy_id,hierarchy_generation,parent_group_id)
        REFERENCES kim.host_group_hierarchy_node_evidence(hierarchy_id,hierarchy_generation,host_group_id),
    FOREIGN KEY (hierarchy_id,hierarchy_generation,child_group_id)
        REFERENCES kim.host_group_hierarchy_node_evidence(hierarchy_id,hierarchy_generation,host_group_id)
);

CREATE TABLE kim.host_group_hierarchy_sets_current (
    group_type text NOT NULL,
    dimension text NOT NULL,
    scope_type text NOT NULL,
    scope_id text NOT NULL,
    hierarchy_id text NOT NULL,
    hierarchy_generation bigint NOT NULL CHECK (hierarchy_generation > 0),
    graph_mode text NOT NULL CHECK (graph_mode = 'TREE'),
    canonical_relation_set_digest char(64) NOT NULL CHECK (canonical_relation_set_digest ~ '^[0-9a-f]{64}$'),
    validation_state text NOT NULL CHECK (validation_state = 'ACCEPTED'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (group_type,dimension,scope_type,scope_id),
    FOREIGN KEY (hierarchy_id,hierarchy_generation,canonical_relation_set_digest)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation,canonical_relation_set_digest)
);

ALTER TABLE kim.host_group_membership_set_evidence
    ADD COLUMN hierarchy_id text,
    ADD CHECK ((hierarchy_id IS NULL) OR (hierarchy_generation IS NOT NULL)),
    ADD FOREIGN KEY (hierarchy_id,hierarchy_generation)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation);

ALTER TABLE kim.host_group_membership_sets_current
    ADD COLUMN hierarchy_id text,
    ADD COLUMN hierarchy_generation bigint,
    ADD CHECK (hierarchy_generation IS NULL OR hierarchy_generation > 0),
    ADD CHECK ((hierarchy_id IS NULL) = (hierarchy_generation IS NULL)),
    ADD FOREIGN KEY (hierarchy_id,hierarchy_generation)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation);

ALTER TABLE kim.host_group_membership_snapshot_evidence
    ADD COLUMN hierarchy_id text,
    ADD COLUMN hierarchy_generation bigint,
    ADD CHECK (hierarchy_generation IS NULL OR hierarchy_generation > 0),
    ADD CHECK ((hierarchy_id IS NULL) = (hierarchy_generation IS NULL)),
    ADD FOREIGN KEY (hierarchy_id,hierarchy_generation)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation);

ALTER TABLE kim.placement_admission_decisions
    ADD COLUMN hierarchy_id text,
    ADD COLUMN hierarchy_generation bigint,
    ADD CHECK (hierarchy_generation IS NULL OR hierarchy_generation > 0),
    ADD CHECK ((hierarchy_id IS NULL) = (hierarchy_generation IS NULL)),
    ADD FOREIGN KEY (hierarchy_id,hierarchy_generation)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation);

CREATE TRIGGER host_group_hierarchy_set_evidence_no_update
    BEFORE UPDATE ON kim.host_group_hierarchy_set_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_hierarchy_level_evidence_no_update
    BEFORE UPDATE ON kim.host_group_hierarchy_level_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_hierarchy_node_evidence_no_update
    BEFORE UPDATE ON kim.host_group_hierarchy_node_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_hierarchy_relation_evidence_no_update
    BEFORE UPDATE ON kim.host_group_hierarchy_relation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_group_hierarchy_set_evidence IS
'Immutable complete hierarchy generation. Partial node/relation writes and caller parent_id fields are not hierarchy authority.';
COMMENT ON TABLE kim.host_group_hierarchy_sets_current IS
'Atomic pointer to one accepted hierarchy generation for a HostGroup type/dimension/scope graph.';
COMMENT ON COLUMN kim.host_group_hierarchy_relation_evidence.parent_level IS
'Denormalized read evidence; publication verifies parent rank is strictly less than child rank.';
COMMENT ON COLUMN kim.host_group_membership_set_evidence.hierarchy_id IS
'Current hierarchy authority against which the complete membership set was validated; NULL only when no hierarchy authority exists or for pre-041 history.';
