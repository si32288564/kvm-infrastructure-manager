CREATE TABLE kim.host_group_cardinality_policy_evidence (
    cardinality_policy_id text NOT NULL,
    policy_generation bigint NOT NULL CHECK (policy_generation > 0),
    group_type text NOT NULL CHECK (group_type IN ('PLACEMENT_POOL','FAILURE_DOMAIN','OPERATIONAL_COHORT')),
    dimension text NOT NULL CHECK (length(dimension) BETWEEN 1 AND 128),
    level text NOT NULL CHECK (length(level) BETWEEN 1 AND 128),
    scope_type text NOT NULL CHECK (scope_type = 'SYSTEM'),
    scope_id text NOT NULL CHECK (scope_id = 'system'),
    cardinality text NOT NULL CHECK (cardinality IN ('EXACTLY_ONE','ZERO_OR_ONE','MANY')),
    policy_state text NOT NULL CHECK (policy_state IN ('ACTIVE','RETIRED')),
    revision_digest char(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (cardinality_policy_id, policy_generation),
    UNIQUE (group_type,dimension,level,scope_type,scope_id,policy_generation),
    UNIQUE (cardinality_policy_id,policy_generation,cardinality)
);

CREATE TABLE kim.host_group_cardinality_policies_current (
    group_type text NOT NULL,
    dimension text NOT NULL,
    level text NOT NULL,
    scope_type text NOT NULL,
    scope_id text NOT NULL,
    cardinality_policy_id text NOT NULL,
    policy_generation bigint NOT NULL CHECK (policy_generation > 0),
    cardinality text NOT NULL CHECK (cardinality IN ('EXACTLY_ONE','ZERO_OR_ONE','MANY')),
    policy_state text NOT NULL CHECK (policy_state IN ('ACTIVE','RETIRED')),
    revision_digest char(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (group_type,dimension,level,scope_type,scope_id),
    FOREIGN KEY (cardinality_policy_id,policy_generation,cardinality)
        REFERENCES kim.host_group_cardinality_policy_evidence(cardinality_policy_id,policy_generation,cardinality)
);

WITH classes AS (
    SELECT DISTINCT group_type,dimension,level FROM kim.host_groups_current
), policies AS (
    SELECT 'cardinality:' || encode(sha256(convert_to(group_type || E'\n' || dimension || E'\n' || level || E'\nSYSTEM\nsystem','UTF8')),'hex') AS policy_id,
           group_type,dimension,level
    FROM classes
)
INSERT INTO kim.host_group_cardinality_policy_evidence (
    cardinality_policy_id,policy_generation,group_type,dimension,level,
    scope_type,scope_id,cardinality,policy_state,revision_digest
)
SELECT policy_id,1,group_type,dimension,level,'SYSTEM','system','MANY','ACTIVE',
       encode(sha256(convert_to(policy_id || E'\n1\n' || group_type || E'\n' || dimension || E'\n' || level || E'\nSYSTEM\nsystem\nMANY\nACTIVE','UTF8')),'hex')
FROM policies;

INSERT INTO kim.host_group_cardinality_policies_current (
    group_type,dimension,level,scope_type,scope_id,cardinality_policy_id,
    policy_generation,cardinality,policy_state,revision_digest
)
SELECT group_type,dimension,level,scope_type,scope_id,cardinality_policy_id,
       policy_generation,cardinality,policy_state,revision_digest
FROM kim.host_group_cardinality_policy_evidence;

-- Pre-040 sets remain immutable and unbound compatibility evidence. Every set
-- published by cardinality-aware code records the exact policy generation.
ALTER TABLE kim.host_group_membership_set_evidence
    ADD COLUMN cardinality_policy_id text,
    ADD COLUMN cardinality_policy_generation bigint,
    ADD COLUMN cardinality text,
    ADD CHECK (cardinality_policy_generation IS NULL OR cardinality_policy_generation > 0),
    ADD CHECK (cardinality IS NULL OR cardinality IN ('EXACTLY_ONE','ZERO_OR_ONE','MANY')),
    ADD CHECK ((cardinality_policy_id IS NULL) = (cardinality_policy_generation IS NULL)),
    ADD CHECK ((cardinality_policy_id IS NULL) = (cardinality IS NULL)),
    ADD FOREIGN KEY (cardinality_policy_id,cardinality_policy_generation,cardinality)
        REFERENCES kim.host_group_cardinality_policy_evidence(cardinality_policy_id,policy_generation,cardinality);

ALTER TABLE kim.host_group_membership_sets_current
    ADD COLUMN cardinality_policy_id text,
    ADD COLUMN cardinality_policy_generation bigint,
    ADD COLUMN cardinality text,
    ADD CHECK (cardinality_policy_generation IS NULL OR cardinality_policy_generation > 0),
    ADD CHECK (cardinality IS NULL OR cardinality IN ('EXACTLY_ONE','ZERO_OR_ONE','MANY')),
    ADD CHECK ((cardinality_policy_id IS NULL) = (cardinality_policy_generation IS NULL)),
    ADD CHECK ((cardinality_policy_id IS NULL) = (cardinality IS NULL)),
    ADD FOREIGN KEY (cardinality_policy_id,cardinality_policy_generation,cardinality)
        REFERENCES kim.host_group_cardinality_policy_evidence(cardinality_policy_id,policy_generation,cardinality);

CREATE TRIGGER host_group_cardinality_policy_evidence_no_update
    BEFORE UPDATE ON kim.host_group_cardinality_policy_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_group_cardinality_policy_evidence IS
'Immutable cardinality authority for one HostGroup sibling class and scope.';
COMMENT ON COLUMN kim.host_group_cardinality_policy_evidence.cardinality IS
'Constraint over each Host membership count across ACTIVE sibling HostGroups, not member count within one group.';
COMMENT ON COLUMN kim.host_group_membership_set_evidence.cardinality_policy_generation IS
'Cardinality policy generation under which this complete set was accepted; NULL only for immutable pre-040 compatibility history.';
