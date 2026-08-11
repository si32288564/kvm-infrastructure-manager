CREATE TABLE kim.host_group_membership_set_evidence (
    host_group_id text NOT NULL,
    membership_set_generation bigint NOT NULL CHECK (membership_set_generation > 0),
    based_on_host_group_generation bigint NOT NULL CHECK (based_on_host_group_generation > 0),
    publish_request_id text NOT NULL UNIQUE CHECK (length(publish_request_id) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    source_type text NOT NULL CHECK (source_type IN ('EXPLICIT', 'SELECTOR', 'EXTERNAL_ASSERTION', 'PLACEMENT_POOL_COMPAT', 'MIGRATION_BACKFILL')),
    source_revision text NOT NULL CHECK (length(source_revision) BETWEEN 1 AND 255),
    selector_evaluation_generation bigint CHECK (selector_evaluation_generation > 0),
    hierarchy_generation bigint CHECK (hierarchy_generation > 0),
    canonical_member_set_digest char(64) NOT NULL CHECK (canonical_member_set_digest ~ '^[0-9a-f]{64}$'),
    member_count integer NOT NULL CHECK (member_count >= 0),
    validation_state text NOT NULL CHECK (validation_state = 'ACCEPTED'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_group_id, membership_set_generation),
    UNIQUE (host_group_id, membership_set_generation, canonical_member_set_digest),
    FOREIGN KEY (host_group_id, based_on_host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id, host_group_generation)
);

CREATE TABLE kim.host_group_membership_set_member_evidence (
    host_group_id text NOT NULL,
    membership_set_generation bigint NOT NULL CHECK (membership_set_generation > 0),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_state text NOT NULL CHECK (membership_state IN ('ACTIVE', 'STALE', 'BLOCKED', 'REMOVED')),
    source_type text NOT NULL CHECK (source_type IN ('EXPLICIT', 'SELECTOR', 'EXTERNAL_ASSERTION', 'PLACEMENT_POOL_COMPAT')),
    source_revision text NOT NULL CHECK (length(source_revision) BETWEEN 1 AND 255),
    membership_evidence_digest char(64) NOT NULL CHECK (membership_evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_group_id, membership_set_generation, host_id),
    FOREIGN KEY (host_group_id, membership_set_generation)
        REFERENCES kim.host_group_membership_set_evidence(host_group_id, membership_set_generation),
    FOREIGN KEY (host_group_id, host_id, membership_generation, membership_evidence_digest)
        REFERENCES kim.host_group_membership_evidence(host_group_id, host_id, membership_generation, evidence_digest),
    UNIQUE (host_group_id, membership_set_generation, host_id, membership_generation, membership_evidence_digest)
);

CREATE TABLE kim.host_group_membership_sets_current (
    host_group_id text PRIMARY KEY,
    membership_set_generation bigint NOT NULL CHECK (membership_set_generation > 0),
    based_on_host_group_generation bigint NOT NULL CHECK (based_on_host_group_generation > 0),
    canonical_member_set_digest char(64) NOT NULL CHECK (canonical_member_set_digest ~ '^[0-9a-f]{64}$'),
    member_count integer NOT NULL CHECK (member_count >= 0),
    validation_state text NOT NULL CHECK (validation_state = 'ACCEPTED'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (host_group_id, membership_set_generation, canonical_member_set_digest)
        REFERENCES kim.host_group_membership_set_evidence(host_group_id, membership_set_generation, canonical_member_set_digest)
);

-- Preserve every pre-039 snapshot as a historical accepted set.
WITH numbered AS (
    SELECT snapshot_id, host_group_id, host_group_generation, membership_digest,
           member_count, row_number() OVER (PARTITION BY host_group_id ORDER BY recorded_at, snapshot_id)::bigint AS set_generation
    FROM kim.host_group_membership_snapshot_evidence
)
INSERT INTO kim.host_group_membership_set_evidence (
    host_group_id, membership_set_generation, based_on_host_group_generation,
    publish_request_id, request_digest, source_type, source_revision,
    canonical_member_set_digest, member_count, validation_state
)
SELECT host_group_id, set_generation, host_group_generation,
       'migration-039-snapshot/' || snapshot_id,
       encode(sha256(convert_to(host_group_id || E'\n' || set_generation::text || E'\n' ||
           host_group_generation::text || E'\nMIGRATION_BACKFILL\n' || snapshot_id || E'\n' || membership_digest, 'UTF8')), 'hex'),
       'MIGRATION_BACKFILL', snapshot_id, membership_digest, member_count, 'ACCEPTED'
FROM numbered;

WITH numbered AS (
    SELECT snapshot_id, host_group_id,
           row_number() OVER (PARTITION BY host_group_id ORDER BY recorded_at, snapshot_id)::bigint AS set_generation
    FROM kim.host_group_membership_snapshot_evidence
)
INSERT INTO kim.host_group_membership_set_member_evidence (
    host_group_id, membership_set_generation, host_id, membership_generation,
    membership_state, source_type, source_revision, membership_evidence_digest
)
SELECT member.host_group_id, numbered.set_generation, member.host_id,
       member.membership_generation, evidence.membership_state,
       evidence.source_type, evidence.source_revision, member.membership_evidence_digest
FROM kim.host_group_membership_snapshot_members member
JOIN numbered ON numbered.snapshot_id=member.snapshot_id
JOIN kim.host_group_membership_evidence evidence
  ON evidence.host_group_id=member.host_group_id
 AND evidence.host_id=member.host_id
 AND evidence.membership_generation=member.membership_generation
 AND evidence.evidence_digest=member.membership_evidence_digest;

WITH current_sets AS (
    SELECT group_current.host_group_id,
           (SELECT count(*) + 1 FROM kim.host_group_membership_snapshot_evidence snapshot
             WHERE snapshot.host_group_id=group_current.host_group_id)::bigint AS set_generation,
           group_current.host_group_generation,
           COALESCE((
             SELECT encode(sha256(convert_to(string_agg(
                 member.host_id || E'\n' || member.membership_generation::text || E'\n' || member.evidence_digest,
                 E'\n' ORDER BY member.host_id), 'UTF8')), 'hex')
             FROM kim.host_group_memberships_current member
             WHERE member.host_group_id=group_current.host_group_id
           ), encode(sha256(convert_to('', 'UTF8')), 'hex')) AS set_digest,
           (SELECT count(*) FROM kim.host_group_memberships_current member
             WHERE member.host_group_id=group_current.host_group_id AND member.membership_state <> 'REMOVED')::integer AS member_count
    FROM kim.host_groups_current group_current
)
INSERT INTO kim.host_group_membership_set_evidence (
    host_group_id, membership_set_generation, based_on_host_group_generation,
    publish_request_id, request_digest, source_type, source_revision,
    canonical_member_set_digest, member_count, validation_state
)
SELECT host_group_id, set_generation, host_group_generation,
       'migration-039-current/' || host_group_id,
       encode(sha256(convert_to(host_group_id || E'\n' || set_generation::text || E'\n' ||
           host_group_generation::text || E'\nMIGRATION_BACKFILL\n038-current\n' || set_digest, 'UTF8')), 'hex'),
       'MIGRATION_BACKFILL', '038-current', set_digest, member_count, 'ACCEPTED'
FROM current_sets;

WITH current_generations AS (
    SELECT host_group_id, max(membership_set_generation) AS set_generation
    FROM kim.host_group_membership_set_evidence GROUP BY host_group_id
)
INSERT INTO kim.host_group_membership_set_member_evidence (
    host_group_id, membership_set_generation, host_id, membership_generation,
    membership_state, source_type, source_revision, membership_evidence_digest
)
SELECT member.host_group_id, generation.set_generation, member.host_id,
       member.membership_generation, member.membership_state,
       member.source_type, member.source_revision, member.evidence_digest
FROM kim.host_group_memberships_current member
JOIN current_generations generation ON generation.host_group_id=member.host_group_id;

INSERT INTO kim.host_group_membership_sets_current (
    host_group_id, membership_set_generation, based_on_host_group_generation,
    canonical_member_set_digest, member_count, validation_state
)
SELECT evidence.host_group_id, evidence.membership_set_generation,
       evidence.based_on_host_group_generation, evidence.canonical_member_set_digest,
       evidence.member_count, evidence.validation_state
FROM kim.host_group_membership_set_evidence evidence
JOIN (
    SELECT host_group_id, max(membership_set_generation) AS set_generation
    FROM kim.host_group_membership_set_evidence GROUP BY host_group_id
) current_generation
  ON current_generation.host_group_id=evidence.host_group_id
 AND current_generation.set_generation=evidence.membership_set_generation;

ALTER TABLE kim.host_group_memberships_current ADD COLUMN membership_set_generation bigint;
UPDATE kim.host_group_memberships_current current_member
SET membership_set_generation=current_set.membership_set_generation
FROM kim.host_group_membership_sets_current current_set
WHERE current_set.host_group_id=current_member.host_group_id;
ALTER TABLE kim.host_group_memberships_current
    ALTER COLUMN membership_set_generation SET NOT NULL,
    ADD CHECK (membership_set_generation > 0),
    ADD FOREIGN KEY (host_group_id, membership_set_generation, host_id, membership_generation, evidence_digest)
        REFERENCES kim.host_group_membership_set_member_evidence(host_group_id, membership_set_generation, host_id, membership_generation, membership_evidence_digest);

ALTER TABLE kim.host_group_membership_snapshot_evidence
    ADD COLUMN membership_set_generation bigint,
    ADD COLUMN membership_set_digest char(64);
WITH numbered AS (
    SELECT snapshot_id, host_group_id, membership_digest,
           row_number() OVER (PARTITION BY host_group_id ORDER BY recorded_at, snapshot_id)::bigint AS set_generation
    FROM kim.host_group_membership_snapshot_evidence
)
UPDATE kim.host_group_membership_snapshot_evidence snapshot
SET membership_set_generation=numbered.set_generation,
    membership_set_digest=numbered.membership_digest
FROM numbered WHERE numbered.snapshot_id=snapshot.snapshot_id;
ALTER TABLE kim.host_group_membership_snapshot_evidence
    ALTER COLUMN membership_set_generation SET NOT NULL,
    ALTER COLUMN membership_set_digest SET NOT NULL,
    ADD CHECK (membership_set_generation > 0),
    ADD CHECK (membership_set_digest ~ '^[0-9a-f]{64}$'),
    ADD FOREIGN KEY (host_group_id, membership_set_generation, membership_set_digest)
        REFERENCES kim.host_group_membership_set_evidence(host_group_id, membership_set_generation, canonical_member_set_digest);

ALTER TABLE kim.host_group_membership_snapshot_members ADD COLUMN membership_set_generation bigint;
UPDATE kim.host_group_membership_snapshot_members member
SET membership_set_generation=snapshot.membership_set_generation
FROM kim.host_group_membership_snapshot_evidence snapshot
WHERE snapshot.snapshot_id=member.snapshot_id;
ALTER TABLE kim.host_group_membership_snapshot_members
    ALTER COLUMN membership_set_generation SET NOT NULL,
    ADD CHECK (membership_set_generation > 0),
    ADD FOREIGN KEY (host_group_id, membership_set_generation, host_id, membership_generation, membership_evidence_digest)
        REFERENCES kim.host_group_membership_set_member_evidence(host_group_id, membership_set_generation, host_id, membership_generation, membership_evidence_digest);

ALTER TABLE kim.placement_admission_decisions ADD COLUMN membership_set_generation bigint;
UPDATE kim.placement_admission_decisions decision
SET membership_set_generation=current_set.membership_set_generation
FROM kim.host_group_membership_sets_current current_set
WHERE current_set.host_group_id=decision.pool_id;
ALTER TABLE kim.placement_admission_decisions
    ALTER COLUMN membership_set_generation SET NOT NULL,
    ADD CHECK (membership_set_generation > 0);

CREATE TRIGGER host_group_membership_set_evidence_no_update
    BEFORE UPDATE ON kim.host_group_membership_set_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_membership_set_member_evidence_no_update
    BEFORE UPDATE ON kim.host_group_membership_set_member_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_group_membership_set_evidence IS
'Immutable whole-group membership-set authority. Individual rows and partial bulk writes are not accepted authority.';
COMMENT ON TABLE kim.host_group_membership_sets_current IS
'Atomic pointer to the one accepted complete membership set for a HostGroup.';
COMMENT ON COLUMN kim.host_group_membership_set_evidence.based_on_host_group_generation IS
'HostGroup semantic/lifecycle generation against which the complete set was validated.';
COMMENT ON COLUMN kim.placement_admission_decisions.membership_set_generation IS
'Accepted whole-group membership set revalidated by Final Admission; individual membership generation alone is insufficient.';
