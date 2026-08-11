CREATE TABLE kim.host_group_external_assertion_issuer_revision_evidence (
    issuer_id text NOT NULL,
    issuer_generation bigint NOT NULL CHECK (issuer_generation > 0),
    publish_request_id text NOT NULL UNIQUE,
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    issuer_type text NOT NULL CHECK (issuer_type = 'HOST_GROUP_MEMBERSHIP'),
    schema_version text NOT NULL CHECK (schema_version = 'kim.host-group.external-assertion/v1'),
    audience text NOT NULL CHECK (audience = 'kim-control-plane'),
    verification_algorithm text NOT NULL CHECK (verification_algorithm = 'ED25519'),
    public_key bytea NOT NULL CHECK (octet_length(public_key) = 32),
    public_key_digest char(64) NOT NULL CHECK (public_key_digest ~ '^[0-9a-f]{64}$'),
    scope_type text NOT NULL CHECK (scope_type = 'SYSTEM'),
    scope_id text NOT NULL CHECK (scope_id = 'system'),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('TRUSTED','RETIRED','REVOKED')),
    trust_digest char(64) NOT NULL CHECK (trust_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (issuer_id,issuer_generation),
    UNIQUE (issuer_id,issuer_generation,trust_digest)
);

CREATE TABLE kim.host_group_external_assertion_issuer_scope_evidence (
    issuer_id text NOT NULL,
    issuer_generation bigint NOT NULL,
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    scope_digest char(64) NOT NULL CHECK (scope_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (issuer_id,issuer_generation,host_group_id),
    FOREIGN KEY (issuer_id,issuer_generation)
        REFERENCES kim.host_group_external_assertion_issuer_revision_evidence(issuer_id,issuer_generation),
    FOREIGN KEY (host_group_id,host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id,host_group_generation)
);

CREATE TABLE kim.host_group_external_assertion_issuers_current (
    issuer_id text PRIMARY KEY,
    issuer_generation bigint NOT NULL CHECK (issuer_generation > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('TRUSTED','RETIRED','REVOKED')),
    schema_version text NOT NULL,
    audience text NOT NULL,
    verification_algorithm text NOT NULL,
    public_key_digest char(64) NOT NULL,
    trust_digest char(64) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (issuer_id,issuer_generation,trust_digest)
        REFERENCES kim.host_group_external_assertion_issuer_revision_evidence(issuer_id,issuer_generation,trust_digest)
);

CREATE TABLE kim.host_group_external_assertion_evidence (
    assertion_id text PRIMARY KEY,
    issuer_id text NOT NULL,
    issuer_generation bigint,
    schema_version text NOT NULL,
    subject_type text NOT NULL,
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    audience text NOT NULL,
    nonce text NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    canonical_payload_digest char(64) NOT NULL CHECK (canonical_payload_digest ~ '^[0-9a-f]{64}$'),
    signature_digest char(64) NOT NULL CHECK (signature_digest ~ '^[0-9a-f]{64}$'),
    canonical_member_set_digest char(64) NOT NULL CHECK (canonical_member_set_digest ~ '^[0-9a-f]{64}$'),
    member_count integer NOT NULL CHECK (member_count >= 0),
    verified_hierarchy_id text,
    verified_hierarchy_generation bigint,
    verification_result text NOT NULL CHECK (verification_result IN (
        'VERIFIED','INVALID_SIGNATURE','UNTRUSTED_ISSUER','EXPIRED','REPLAY_CONFLICT',
        'UNSUPPORTED_SCHEMA','AUDIENCE_MISMATCH','PAYLOAD_DIGEST_MISMATCH',
        'STALE_HOST_GROUP','UNKNOWN_HOST','UNKNOWN')),
    verifier_version text NOT NULL CHECK (verifier_version = 'kim.external-assertion.ed25519-verifier/v1'),
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    verification_digest char(64) NOT NULL CHECK (verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK (issuer_generation IS NOT NULL OR verification_result = 'UNTRUSTED_ISSUER'),
    CHECK ((verified_hierarchy_id IS NULL) = (verified_hierarchy_generation IS NULL)),
    CHECK (verified_hierarchy_generation IS NULL OR verified_hierarchy_generation > 0),
    UNIQUE (assertion_id,issuer_id,issuer_generation,payload_digest,verification_digest),
    FOREIGN KEY (issuer_id,issuer_generation)
        REFERENCES kim.host_group_external_assertion_issuer_revision_evidence(issuer_id,issuer_generation),
    FOREIGN KEY (verified_hierarchy_id,verified_hierarchy_generation)
        REFERENCES kim.host_group_hierarchy_set_evidence(hierarchy_id,hierarchy_generation)
);

CREATE TABLE kim.host_group_external_assertion_member_evidence (
    assertion_id text NOT NULL REFERENCES kim.host_group_external_assertion_evidence(assertion_id),
    host_id text NOT NULL,
    member_digest char(64) NOT NULL CHECK (member_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (assertion_id,host_id)
);

CREATE TABLE kim.host_group_external_assertion_nonce_evidence (
    issuer_id text NOT NULL,
    issuer_generation bigint NOT NULL,
    nonce text NOT NULL,
    assertion_id text NOT NULL REFERENCES kim.host_group_external_assertion_evidence(assertion_id),
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (issuer_id,nonce),
    FOREIGN KEY (issuer_id,issuer_generation)
        REFERENCES kim.host_group_external_assertion_issuer_revision_evidence(issuer_id,issuer_generation)
);

CREATE TABLE kim.host_group_external_assertion_conflict_evidence (
    conflict_id text PRIMARY KEY,
    assertion_id text NOT NULL,
    issuer_id text NOT NULL,
    nonce text NOT NULL,
    original_payload_digest char(64) NOT NULL CHECK (original_payload_digest ~ '^[0-9a-f]{64}$'),
    conflicting_payload_digest char(64) NOT NULL CHECK (conflicting_payload_digest ~ '^[0-9a-f]{64}$'),
    conflict_type text NOT NULL CHECK (conflict_type IN ('ASSERTION_ID','NONCE')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

ALTER TABLE kim.host_group_membership_set_evidence
    ADD COLUMN external_assertion_id text,
    ADD COLUMN external_assertion_issuer_id text,
    ADD COLUMN external_assertion_issuer_generation bigint,
    ADD COLUMN external_assertion_payload_digest char(64),
    ADD COLUMN external_assertion_verification_digest char(64),
    ADD CHECK (external_assertion_issuer_generation IS NULL OR external_assertion_issuer_generation > 0),
    ADD CHECK (external_assertion_payload_digest IS NULL OR external_assertion_payload_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK (external_assertion_verification_digest IS NULL OR external_assertion_verification_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK ((source_type = 'EXTERNAL_ASSERTION') = (external_assertion_id IS NOT NULL)),
    ADD CHECK ((external_assertion_id IS NULL) = (external_assertion_issuer_id IS NULL)),
    ADD CHECK ((external_assertion_id IS NULL) = (external_assertion_issuer_generation IS NULL)),
    ADD CHECK ((external_assertion_id IS NULL) = (external_assertion_payload_digest IS NULL)),
    ADD CHECK ((external_assertion_id IS NULL) = (external_assertion_verification_digest IS NULL)),
    ADD FOREIGN KEY (external_assertion_id,external_assertion_issuer_id,external_assertion_issuer_generation,
                     external_assertion_payload_digest,external_assertion_verification_digest)
        REFERENCES kim.host_group_external_assertion_evidence(
            assertion_id,issuer_id,issuer_generation,payload_digest,verification_digest);

CREATE TRIGGER host_group_external_assertion_issuer_revision_evidence_no_update
    BEFORE UPDATE ON kim.host_group_external_assertion_issuer_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_external_assertion_issuer_scope_evidence_no_update
    BEFORE UPDATE ON kim.host_group_external_assertion_issuer_scope_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_external_assertion_evidence_no_update
    BEFORE UPDATE ON kim.host_group_external_assertion_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_external_assertion_member_evidence_no_update
    BEFORE UPDATE ON kim.host_group_external_assertion_member_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_external_assertion_nonce_evidence_no_update
    BEFORE UPDATE ON kim.host_group_external_assertion_nonce_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_external_assertion_conflict_evidence_no_update
    BEFORE UPDATE ON kim.host_group_external_assertion_conflict_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_group_external_assertion_evidence IS
'Immutable verification evidence for a closed complete-set external claim. VERIFIED is not membership authority.';
COMMENT ON TABLE kim.host_group_external_assertion_issuer_revision_evidence IS
'Purpose-limited external assertion issuer trust; this is not a general PKI or tenant authority.';
COMMENT ON COLUMN kim.host_group_membership_set_evidence.external_assertion_id IS
'Exact verified assertion provenance required only for source_type EXTERNAL_ASSERTION; pre-047 evidence remains NULL compatibility history.';
