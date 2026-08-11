CREATE TABLE kim.host_group_revision_evidence (
    host_group_id text NOT NULL CHECK (length(host_group_id) BETWEEN 1 AND 255),
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    group_type text NOT NULL CHECK (group_type IN ('PLACEMENT_POOL', 'FAILURE_DOMAIN', 'OPERATIONAL_COHORT')),
    dimension text NOT NULL CHECK (length(dimension) BETWEEN 1 AND 255),
    level text NOT NULL CHECK (length(level) BETWEEN 1 AND 255),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT', 'ACTIVE', 'DRAINING', 'RETIRED')),
    revision_digest char(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_group_id, host_group_generation),
    UNIQUE (host_group_id, host_group_generation, revision_digest)
);

CREATE TABLE kim.host_groups_current (
    host_group_id text PRIMARY KEY,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    group_type text NOT NULL CHECK (group_type IN ('PLACEMENT_POOL', 'FAILURE_DOMAIN', 'OPERATIONAL_COHORT')),
    dimension text NOT NULL,
    level text NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT', 'ACTIVE', 'DRAINING', 'RETIRED')),
    revision_digest char(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (host_group_id, host_group_generation, revision_digest)
        REFERENCES kim.host_group_revision_evidence(host_group_id, host_group_generation, revision_digest)
);

CREATE TABLE kim.host_group_membership_evidence (
    host_group_id text NOT NULL,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_state text NOT NULL CHECK (membership_state IN ('ACTIVE', 'STALE', 'BLOCKED', 'REMOVED')),
    source_type text NOT NULL CHECK (source_type IN ('EXPLICIT', 'SELECTOR', 'EXTERNAL_ASSERTION', 'PLACEMENT_POOL_COMPAT')),
    source_revision text NOT NULL CHECK (length(source_revision) BETWEEN 1 AND 255),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_group_id, host_id, membership_generation),
    UNIQUE (host_group_id, host_id, membership_generation, evidence_digest),
    FOREIGN KEY (host_group_id) REFERENCES kim.host_groups_current(host_group_id)
);

CREATE TABLE kim.host_group_memberships_current (
    host_group_id text NOT NULL,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_state text NOT NULL CHECK (membership_state IN ('ACTIVE', 'STALE', 'BLOCKED', 'REMOVED')),
    source_type text NOT NULL,
    source_revision text NOT NULL,
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_group_id, host_id),
    FOREIGN KEY (host_group_id, host_id, membership_generation, evidence_digest)
        REFERENCES kim.host_group_membership_evidence(host_group_id, host_id, membership_generation, evidence_digest)
);

CREATE INDEX host_group_memberships_by_host
    ON kim.host_group_memberships_current(host_id, membership_state, host_group_id);

CREATE TABLE kim.host_group_membership_snapshot_evidence (
    snapshot_id text PRIMARY KEY,
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    purpose text NOT NULL CHECK (purpose IN ('UPGRADE', 'MAINTENANCE', 'BASELINE_ROLLOUT', 'PLACEMENT_AUDIT')),
    member_count integer NOT NULL CHECK (member_count >= 0),
    membership_digest char(64) NOT NULL CHECK (membership_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (snapshot_id, host_group_id),
    FOREIGN KEY (host_group_id, host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id, host_group_generation)
);

CREATE TABLE kim.host_group_membership_snapshot_members (
    snapshot_id text NOT NULL,
    host_group_id text NOT NULL,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_evidence_digest char(64) NOT NULL CHECK (membership_evidence_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (snapshot_id, host_id),
    FOREIGN KEY (snapshot_id, host_group_id)
        REFERENCES kim.host_group_membership_snapshot_evidence(snapshot_id, host_group_id),
    FOREIGN KEY (host_group_id, host_id, membership_generation, membership_evidence_digest)
        REFERENCES kim.host_group_membership_evidence(host_group_id, host_id, membership_generation, evidence_digest)
);

CREATE TRIGGER host_group_revision_evidence_no_update
    BEFORE UPDATE ON kim.host_group_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_membership_evidence_no_update
    BEFORE UPDATE ON kim.host_group_membership_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_membership_snapshot_evidence_no_update
    BEFORE UPDATE ON kim.host_group_membership_snapshot_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_membership_snapshot_members_no_update
    BEFORE UPDATE ON kim.host_group_membership_snapshot_members
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

-- Preserve existing Placement Pool authority during an in-place upgrade. New
-- writes use the HostGroup persistence API and keep the legacy Pool projection
-- synchronized until its readers have been retired.
INSERT INTO kim.host_group_revision_evidence (
    host_group_id, host_group_generation, group_type, dimension, level,
    lifecycle_state, revision_digest
)
SELECT pool_id, pool_generation, 'PLACEMENT_POOL', 'service-class', 'pool',
       CASE lifecycle_state WHEN 'DISABLED' THEN 'DRAINING' ELSE lifecycle_state END,
       encode(sha256(convert_to(
           pool_id || E'\n' || pool_generation::text || E'\nPLACEMENT_POOL\nservice-class\npool\n' ||
           CASE lifecycle_state WHEN 'DISABLED' THEN 'DRAINING' ELSE lifecycle_state END,
           'UTF8')), 'hex')
FROM kim.placement_pools_current;

INSERT INTO kim.host_groups_current (
    host_group_id, host_group_generation, group_type, dimension, level,
    lifecycle_state, revision_digest
)
SELECT host_group_id, host_group_generation, group_type, dimension, level,
       lifecycle_state, revision_digest
FROM kim.host_group_revision_evidence;

INSERT INTO kim.host_group_membership_evidence (
    host_group_id, host_id, membership_generation, membership_state,
    source_type, source_revision, evidence_digest
)
SELECT membership.pool_id, membership.host_id, membership.membership_generation,
       membership.membership_state, 'PLACEMENT_POOL_COMPAT',
       membership.membership_generation::text,
       encode(sha256(convert_to(
           membership.pool_id || E'\n' || membership.host_id || E'\n' ||
           membership.membership_generation::text || E'\n' || membership.membership_state ||
           E'\nPLACEMENT_POOL_COMPAT\n' || membership.membership_generation::text,
           'UTF8')), 'hex')
FROM kim.host_placement_pool_memberships_current membership;

INSERT INTO kim.host_group_memberships_current (
    host_group_id, host_id, membership_generation, membership_state,
    source_type, source_revision, evidence_digest
)
SELECT host_group_id, host_id, membership_generation, membership_state,
       source_type, source_revision, evidence_digest
FROM kim.host_group_membership_evidence;

COMMENT ON TABLE kim.host_groups_current IS
'Current Host membership aggregate identity. Policy effects, capacity, and backend mutations remain separate authorities.';
COMMENT ON TABLE kim.host_group_memberships_current IS
'Many-to-many materialized HostGroup membership authority. Agent labels and unmaterialized selector results are not authority.';
COMMENT ON TABLE kim.host_group_membership_snapshot_evidence IS
'Immutable Host target set. Later membership changes do not alter an active upgrade, maintenance, or rollout scope.';
COMMENT ON TABLE kim.placement_pools_current IS
'Compatibility projection for HostGroup(type=PLACEMENT_POOL). New authority is recorded through HostGroup revision and membership evidence.';
