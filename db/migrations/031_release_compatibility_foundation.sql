CREATE TABLE kim.release_manifest_evidence (
    release_id text NOT NULL,
    manifest_revision bigint NOT NULL CHECK (manifest_revision > 0),
    product_version text NOT NULL,
    channel text NOT NULL CHECK (channel IN ('DEVELOPER_PREVIEW', 'TECHNICAL_PREVIEW', 'GENERAL_AVAILABILITY')),
    manifest_digest char(64) NOT NULL CHECK (manifest_digest ~ '^[0-9a-f]{64}$'),
    certification_state text NOT NULL CHECK (certification_state IN ('VALIDATED', 'CANDIDATE', 'REVOKED')),
    published_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (release_id, manifest_revision)
);

CREATE TABLE kim.release_manifest_component_evidence (
    release_id text NOT NULL,
    manifest_revision bigint NOT NULL,
    component_type text NOT NULL CHECK (component_type IN ('OVN_RUNTIME_WORKER')),
    artifact_digest char(64) NOT NULL CHECK (artifact_digest ~ '^[0-9a-f]{64}$'),
    supported_work_schema_versions text[] NOT NULL CHECK (cardinality(supported_work_schema_versions) > 0),
    component_contract_digest char(64) NOT NULL CHECK (component_contract_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (release_id, manifest_revision, component_type),
    FOREIGN KEY (release_id, manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision)
);

CREATE TABLE kim.release_compatibility_edge_evidence (
    source_release_id text NOT NULL,
    source_manifest_revision bigint NOT NULL,
    target_release_id text NOT NULL,
    target_manifest_revision bigint NOT NULL,
    edge_type text NOT NULL CHECK (edge_type = 'N_MINUS_ONE'),
    allowed_work_schema_versions text[] NOT NULL CHECK (cardinality(allowed_work_schema_versions) > 0),
    edge_digest char(64) NOT NULL CHECK (edge_digest ~ '^[0-9a-f]{64}$'),
    certification_state text NOT NULL CHECK (certification_state IN ('VALIDATED', 'REVOKED')),
    PRIMARY KEY (source_release_id, source_manifest_revision, target_release_id, target_manifest_revision),
    FOREIGN KEY (source_release_id, source_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    FOREIGN KEY (target_release_id, target_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    CHECK ((source_release_id, source_manifest_revision) <> (target_release_id, target_manifest_revision))
);

CREATE TABLE kim.release_authority_current (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    release_id text NOT NULL,
    manifest_revision bigint NOT NULL,
    authority_generation bigint NOT NULL CHECK (authority_generation > 0),
    write_work_schema_version text NOT NULL,
    write_schema_generation bigint NOT NULL DEFAULT 1 CHECK (write_schema_generation > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'ROLLING', 'PAUSED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (release_id, manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision)
);

CREATE TABLE kim.compatibility_decision_evidence (
    decision_id text PRIMARY KEY,
    subject_id text NOT NULL,
    component_type text NOT NULL CHECK (component_type = 'OVN_RUNTIME_WORKER'),
    observed_release_id text NOT NULL,
    observed_manifest_revision bigint NOT NULL,
    observed_artifact_digest char(64) NOT NULL CHECK (observed_artifact_digest ~ '^[0-9a-f]{64}$'),
    supported_work_schema_versions text[] NOT NULL CHECK (cardinality(supported_work_schema_versions) > 0),
    target_release_id text NOT NULL,
    target_manifest_revision bigint NOT NULL,
    release_authority_generation bigint NOT NULL CHECK (release_authority_generation > 0),
    decision text NOT NULL CHECK (decision IN ('VALIDATED', 'COMPATIBLE', 'INCOMPATIBLE', 'UNKNOWN')),
    reason text NOT NULL,
    evaluator_artifact_digest char(64) NOT NULL CHECK (evaluator_artifact_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    decided_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (observed_release_id, observed_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    FOREIGN KEY (target_release_id, target_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision)
);

CREATE TABLE kim.component_release_bindings_current (
    component_id text PRIMARY KEY,
    component_type text NOT NULL CHECK (component_type = 'OVN_RUNTIME_WORKER'),
    release_id text NOT NULL,
    manifest_revision bigint NOT NULL,
    artifact_digest char(64) NOT NULL CHECK (artifact_digest ~ '^[0-9a-f]{64}$'),
    supported_work_schema_versions text[] NOT NULL CHECK (cardinality(supported_work_schema_versions) > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    compatibility_decision_id text NOT NULL REFERENCES kim.compatibility_decision_evidence(decision_id),
    compatibility_state text NOT NULL CHECK (compatibility_state IN ('VALIDATED', 'COMPATIBLE', 'INCOMPATIBLE', 'UNKNOWN')),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DRAINING', 'STOPPED', 'FENCED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (release_id, manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    CHECK (
        (compatibility_state IN ('VALIDATED', 'COMPATIBLE') AND lifecycle_state IN ('ACTIVE', 'DRAINING', 'STOPPED'))
        OR (compatibility_state IN ('INCOMPATIBLE', 'UNKNOWN') AND lifecycle_state = 'FENCED')
    )
);

ALTER TABLE kim.ovn_runtime_work_current
    ADD COLUMN required_work_schema_version text NOT NULL DEFAULT 'kim.network.ovn-runtime-work/v1';

ALTER TABLE kim.ovn_runtime_work_attempt_evidence
    ADD COLUMN release_binding_generation bigint CHECK (release_binding_generation > 0);

CREATE TRIGGER release_manifest_evidence_no_update
    BEFORE UPDATE ON kim.release_manifest_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER release_manifest_component_evidence_no_update
    BEFORE UPDATE ON kim.release_manifest_component_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER release_compatibility_edge_evidence_no_update
    BEFORE UPDATE ON kim.release_compatibility_edge_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER compatibility_decision_evidence_no_update
    BEFORE UPDATE ON kim.compatibility_decision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.release_manifest_evidence IS
'Immutable release identity. Product version ordering alone is not compatibility authority.';
COMMENT ON TABLE kim.release_compatibility_edge_evidence IS
'Immutable explicit N/N-1 edge. Missing edge is incompatible, never inferred from adjacent version strings.';
COMMENT ON TABLE kim.compatibility_decision_evidence IS
'Immutable observed component versus current release decision. Credential or process liveness is not compatibility.';
COMMENT ON TABLE kim.component_release_bindings_current IS
'Current component release projection. DRAINING stops new claims but does not revoke an already granted claim.';
COMMENT ON COLUMN kim.ovn_runtime_work_current.required_work_schema_version IS
'Schema semantics required by this work. Only a current compatible release binding advertising this exact schema may claim it.';
COMMENT ON COLUMN kim.ovn_runtime_work_attempt_evidence.release_binding_generation IS
'Release compatibility binding used at claim time. NULL is retained only for pre-ReleaseManifest historical attempts.';
