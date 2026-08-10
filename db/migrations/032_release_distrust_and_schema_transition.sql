CREATE TABLE kim.release_distrust_evidence (
    distrust_id text PRIMARY KEY,
    distrust_scope text NOT NULL CHECK (distrust_scope IN ('MANIFEST', 'COMPATIBILITY_EDGE')),
    source_release_id text NOT NULL,
    source_manifest_revision bigint NOT NULL CHECK (source_manifest_revision > 0),
    target_release_id text,
    target_manifest_revision bigint CHECK (target_manifest_revision > 0),
    reason text NOT NULL,
    evaluator_artifact_digest char(64) NOT NULL CHECK (evaluator_artifact_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (source_release_id, source_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    FOREIGN KEY (target_release_id, target_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    CHECK (
        (distrust_scope = 'MANIFEST' AND target_release_id IS NULL AND target_manifest_revision IS NULL)
        OR
        (distrust_scope = 'COMPATIBILITY_EDGE' AND target_release_id IS NOT NULL AND target_manifest_revision IS NOT NULL)
    )
);

CREATE UNIQUE INDEX release_distrust_manifest_once
    ON kim.release_distrust_evidence(source_release_id, source_manifest_revision)
    WHERE distrust_scope = 'MANIFEST';
CREATE UNIQUE INDEX release_distrust_edge_once
    ON kim.release_distrust_evidence(
        source_release_id, source_manifest_revision, target_release_id, target_manifest_revision
    ) WHERE distrust_scope = 'COMPATIBILITY_EDGE';

CREATE TABLE kim.release_work_schema_transition_evidence (
    transition_id text PRIMARY KEY,
    release_id text NOT NULL,
    manifest_revision bigint NOT NULL CHECK (manifest_revision > 0),
    release_authority_generation bigint NOT NULL CHECK (release_authority_generation > 0),
    prior_work_schema_version text NOT NULL,
    new_work_schema_version text NOT NULL,
    prior_schema_generation bigint NOT NULL CHECK (prior_schema_generation > 0),
    new_schema_generation bigint NOT NULL CHECK (new_schema_generation > prior_schema_generation),
    transition_type text NOT NULL CHECK (transition_type IN ('ACTIVATE', 'ROLLBACK')),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (release_id, manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    CHECK (prior_work_schema_version <> new_work_schema_version)
);

CREATE TRIGGER release_distrust_evidence_no_update
    BEFORE UPDATE ON kim.release_distrust_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER release_work_schema_transition_evidence_no_update
    BEFORE UPDATE ON kim.release_work_schema_transition_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.release_distrust_evidence IS
'Immutable distrust intent. Revocation fences current component authority but never rewrites the original Manifest, edge, Decision, claim, or Attempt evidence.';
COMMENT ON TABLE kim.release_work_schema_transition_evidence IS
'Immutable FeatureGate activation or rollback evidence. A schema transition is not component compatibility authority and does not revoke already granted work claims.';
