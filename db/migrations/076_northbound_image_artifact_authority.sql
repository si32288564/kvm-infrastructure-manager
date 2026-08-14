CREATE TABLE kim.northbound_image_revision_evidence (
    image_id text NOT NULL,
    image_revision bigint NOT NULL CHECK(image_revision > 0),
    owner_project_id text NOT NULL,
    image_name text NOT NULL CHECK(length(image_name) BETWEEN 1 AND 255),
    architecture text NOT NULL CHECK(architecture IN ('X86_64','AARCH64')),
    image_format text NOT NULL CHECK(image_format IN ('RAW','QCOW2')),
    expected_digest char(64) NOT NULL CHECK(expected_digest ~ '^[0-9a-f]{64}$'),
    source_id text NOT NULL CHECK(source_id ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
    visibility text NOT NULL CHECK(visibility IN ('PRIVATE','SHARED','PUBLIC')),
    delete_protection boolean NOT NULL,
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(image_id,image_revision)
);

CREATE TABLE kim.northbound_images_current (
    image_id text PRIMARY KEY,
    image_revision bigint NOT NULL CHECK(image_revision > 0),
    owner_project_id text NOT NULL,
    lifecycle_state text NOT NULL CHECK(lifecycle_state IN ('ACTIVE','DEPRECATED','DELETED')),
    verification_state text NOT NULL CHECK(verification_state IN ('PENDING','VERIFYING','VERIFIED','FAILED')),
    verified_digest char(64) CHECK(verified_digest IS NULL OR verified_digest ~ '^[0-9a-f]{64}$'),
    verified_size_bytes bigint CHECK(verified_size_bytes IS NULL OR verified_size_bytes >= 0),
    current_ingestion_operation_id text,
    authority_generation bigint NOT NULL CHECK(authority_generation > 0),
    deleted_from_revision bigint CHECK(deleted_from_revision IS NULL OR deleted_from_revision > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(image_id,image_revision) REFERENCES kim.northbound_image_revision_evidence(image_id,image_revision),
    CHECK((verification_state='VERIFIED')=(verified_digest IS NOT NULL AND verified_size_bytes IS NOT NULL)),
    CHECK((lifecycle_state='DELETED')=(deleted_from_revision IS NOT NULL))
);

CREATE TABLE kim.image_ingestion_operation_evidence (
    operation_id text PRIMARY KEY,
    image_id text NOT NULL,
    image_revision bigint NOT NULL CHECK(image_revision > 0),
    artifact_generation bigint NOT NULL CHECK(artifact_generation > 0),
    source_id text NOT NULL,
    expected_digest char(64) NOT NULL CHECK(expected_digest ~ '^[0-9a-f]{64}$'),
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    request_id text NOT NULL,
    idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(image_id,image_revision,artifact_generation),
    UNIQUE(principal_issuer,principal_subject,image_id,image_revision,idempotency_key),
    FOREIGN KEY(image_id,image_revision) REFERENCES kim.northbound_image_revision_evidence(image_id,image_revision)
);

CREATE TABLE kim.image_ingestion_operations_current (
    operation_id text PRIMARY KEY REFERENCES kim.image_ingestion_operation_evidence(operation_id),
    phase text NOT NULL CHECK(phase IN ('PENDING','RUNNING','VERIFYING','SUCCEEDED','FAILED','UNKNOWN','CANCELLED')),
    terminal_state text CHECK(terminal_state IS NULL OR terminal_state IN ('SUCCEEDED','FAILED','CANCELLED')),
    error_code text,
    retryable boolean NOT NULL,
    cancellable boolean NOT NULL DEFAULT false,
    attempt_index bigint NOT NULL DEFAULT 0 CHECK(attempt_index >= 0),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK((terminal_state IS NULL)=(completed_at IS NULL)),
    CHECK(phase<>'SUCCEEDED' OR terminal_state='SUCCEEDED')
);

CREATE TABLE kim.image_ingestion_attempt_evidence (
    operation_id text NOT NULL REFERENCES kim.image_ingestion_operation_evidence(operation_id),
    attempt_index bigint NOT NULL CHECK(attempt_index > 0),
    execution_identity char(64) NOT NULL CHECK(execution_identity ~ '^[0-9a-f]{64}$'),
    response_state text NOT NULL CHECK(response_state IN ('PENDING','RECEIVED','LOST')),
    started_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,attempt_index)
);

CREATE TABLE kim.image_ingestion_command_evidence (
    operation_id text PRIMARY KEY REFERENCES kim.image_ingestion_operation_evidence(operation_id),
    job_id text NOT NULL,
    command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    host_id text NOT NULL,
    host_authority_generation bigint NOT NULL CHECK(host_authority_generation > 0),
    command_payload_digest char(64) NOT NULL CHECK(command_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.image_artifact_observation_evidence (
    observation_id text PRIMARY KEY,
    operation_id text NOT NULL,
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    attempt_index bigint NOT NULL,
    image_id text NOT NULL,
    image_revision bigint NOT NULL,
    artifact_generation bigint NOT NULL,
    source_id text NOT NULL,
    observed_size_bytes bigint NOT NULL CHECK(observed_size_bytes >= 0),
    digest_algorithm text NOT NULL CHECK(digest_algorithm='SHA256'),
    observed_digest char(64) NOT NULL CHECK(observed_digest ~ '^[0-9a-f]{64}$'),
    artifact_identity char(64) NOT NULL CHECK(artifact_identity ~ '^[0-9a-f]{64}$'),
    observation_generation bigint NOT NULL CHECK(observation_generation > 0),
    verifier_artifact_digest char(64) NOT NULL CHECK(verifier_artifact_digest ~ '^[0-9a-f]{64}$'),
    read_back_state text NOT NULL CHECK(read_back_state IN ('COMPLETE','PARTIAL','ABSENT','CONFLICTING')),
    evidence_digest char(64) NOT NULL CHECK(evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,attempt_index,observation_generation),
    FOREIGN KEY(operation_id,attempt_index) REFERENCES kim.image_ingestion_attempt_evidence(operation_id,attempt_index)
);

CREATE TABLE kim.image_artifact_verification_evidence (
    verification_id text PRIMARY KEY,
    operation_id text NOT NULL UNIQUE REFERENCES kim.image_ingestion_operation_evidence(operation_id),
    observation_id text NOT NULL UNIQUE REFERENCES kim.image_artifact_observation_evidence(observation_id),
    expected_digest char(64) NOT NULL CHECK(expected_digest ~ '^[0-9a-f]{64}$'),
    observed_digest char(64) NOT NULL CHECK(observed_digest ~ '^[0-9a-f]{64}$'),
    verified_size_bytes bigint NOT NULL CHECK(verified_size_bytes >= 0),
    verification_state text NOT NULL CHECK(verification_state IN ('VERIFIED','REJECTED')),
    reason_code text NOT NULL,
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK(verification_state<>'VERIFIED' OR expected_digest=observed_digest)
);

CREATE TABLE kim.image_ingestion_terminal_evidence (
    terminal_id text PRIMARY KEY,
    operation_id text NOT NULL UNIQUE REFERENCES kim.image_ingestion_operation_evidence(operation_id),
    verification_id text NOT NULL UNIQUE REFERENCES kim.image_artifact_verification_evidence(verification_id),
    terminal_state text NOT NULL CHECK(terminal_state IN ('SUCCEEDED','FAILED')),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.northbound_image_idempotency_evidence (
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    parent_project_id text NOT NULL,
    idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    image_id text NOT NULL,
    image_revision bigint NOT NULL,
    request_id text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(principal_issuer,principal_subject,parent_project_id,idempotency_key),
    FOREIGN KEY(image_id,image_revision) REFERENCES kim.northbound_image_revision_evidence(image_id,image_revision)
);

CREATE TRIGGER northbound_image_revision_evidence_no_update BEFORE UPDATE ON kim.northbound_image_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER image_ingestion_operation_evidence_no_update BEFORE UPDATE ON kim.image_ingestion_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER image_ingestion_attempt_evidence_no_update BEFORE UPDATE ON kim.image_ingestion_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER image_ingestion_command_evidence_no_update BEFORE UPDATE ON kim.image_ingestion_command_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER image_artifact_observation_evidence_no_update BEFORE UPDATE ON kim.image_artifact_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER image_artifact_verification_evidence_no_update BEFORE UPDATE ON kim.image_artifact_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER image_ingestion_terminal_evidence_no_update BEFORE UPDATE ON kim.image_ingestion_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER northbound_image_idempotency_evidence_no_update BEFORE UPDATE ON kim.northbound_image_idempotency_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

ALTER TABLE kim.northbound_images_current ADD CONSTRAINT northbound_images_current_ingestion_fk FOREIGN KEY(current_ingestion_operation_id) REFERENCES kim.image_ingestion_operation_evidence(operation_id);

COMMENT ON TABLE kim.northbound_image_revision_evidence IS 'Immutable Northbound logical Image intent; expected digest is caller input, observed digest is deliberately absent.';
COMMENT ON TABLE kim.image_artifact_observation_evidence IS 'Independent whole-artifact read-back. No public caller can produce observed_digest.';
COMMENT ON TABLE kim.image_artifact_verification_evidence IS 'Immutable expected-versus-observed decision consumed before publishing Migration 010 materialization authority.';
