CREATE TABLE kim.execution_jobs (
    job_id text PRIMARY KEY,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    desired_revision bigint NOT NULL CHECK (desired_revision > 0),
    job_state text NOT NULL CHECK (job_state IN ('PENDING', 'DISPATCHABLE', 'LEASED', 'EXECUTING', 'VERIFYING', 'SUCCEEDED', 'FAILED', 'ACTION_REQUIRED')),
    current_command_id text,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.execution_job_events (
    job_id text NOT NULL REFERENCES kim.execution_jobs(job_id),
    event_sequence bigint NOT NULL CHECK (event_sequence > 0),
    event_type text NOT NULL,
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (job_id, event_sequence)
);

CREATE TABLE kim.execution_commands (
    command_id text PRIMARY KEY,
    job_id text NOT NULL REFERENCES kim.execution_jobs(job_id),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    command_type text NOT NULL,
    schema_version text NOT NULL,
    target_resource_id text NOT NULL,
    payload jsonb NOT NULL,
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (job_id, command_id)
);

CREATE TABLE kim.execution_commands_current (
    command_id text PRIMARY KEY REFERENCES kim.execution_commands(command_id),
    command_state text NOT NULL CHECK (command_state IN ('PENDING', 'LEASED', 'EXECUTING', 'VERIFYING', 'SUCCEEDED', 'FAILED', 'UNKNOWN', 'REDISPATCHABLE')),
    current_attempt_index integer NOT NULL DEFAULT 0 CHECK (current_attempt_index >= 0),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

ALTER TABLE kim.execution_jobs
    ADD CONSTRAINT execution_jobs_current_command_fkey
    FOREIGN KEY (job_id, current_command_id)
    REFERENCES kim.execution_commands(job_id, command_id);

CREATE TABLE kim.command_lease_grants (
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    lease_generation bigint NOT NULL CHECK (lease_generation > 0),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    host_authority_generation bigint NOT NULL CHECK (host_authority_generation > 0),
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    token_digest char(64) NOT NULL CHECK (token_digest ~ '^[0-9a-f]{64}$'),
    not_before timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (command_id, lease_generation),
    UNIQUE (command_id, attempt_index),
    CHECK (expires_at > not_before)
);

CREATE TABLE kim.command_leases_current (
    command_id text PRIMARY KEY REFERENCES kim.execution_commands(command_id),
    lease_generation bigint NOT NULL,
    attempt_index integer NOT NULL,
    host_id text NOT NULL,
    host_authority_generation bigint NOT NULL,
    session_generation bigint NOT NULL,
    token_digest char(64) NOT NULL CHECK (token_digest ~ '^[0-9a-f]{64}$'),
    lease_state text NOT NULL CHECK (lease_state IN ('ACTIVE', 'CONSUMED', 'EXPIRED', 'FENCED')),
    not_before timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (command_id, lease_generation)
        REFERENCES kim.command_lease_grants(command_id, lease_generation),
    FOREIGN KEY (command_id, attempt_index)
        REFERENCES kim.command_lease_grants(command_id, attempt_index)
);

CREATE TABLE kim.command_lease_events (
    command_id text NOT NULL,
    lease_generation bigint NOT NULL,
    event_sequence bigint NOT NULL CHECK (event_sequence > 0),
    event_type text NOT NULL CHECK (event_type IN ('GRANTED', 'STARTED', 'CONSUMED', 'EXPIRED', 'FENCED')),
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (command_id, lease_generation, event_sequence),
    UNIQUE (command_id, lease_generation, event_type),
    FOREIGN KEY (command_id, lease_generation)
        REFERENCES kim.command_lease_grants(command_id, lease_generation)
);

CREATE TABLE kim.command_attempts (
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    lease_generation bigint NOT NULL,
    host_authority_generation bigint NOT NULL,
    session_generation bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (command_id, attempt_index),
    FOREIGN KEY (command_id, lease_generation)
        REFERENCES kim.command_lease_grants(command_id, lease_generation)
);

CREATE TABLE kim.command_attempt_events (
    command_id text NOT NULL,
    attempt_index integer NOT NULL,
    event_sequence bigint NOT NULL CHECK (event_sequence > 0),
    event_type text NOT NULL CHECK (event_type IN ('LEASED', 'JOURNALED', 'RESULT_ACCEPTED', 'UNKNOWN', 'STALE_RESULT_REJECTED')),
    reason_code text NOT NULL,
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (command_id, attempt_index, event_sequence),
    FOREIGN KEY (command_id, attempt_index)
        REFERENCES kim.command_attempts(command_id, attempt_index)
);

CREATE UNIQUE INDEX command_attempt_events_singleton_type
    ON kim.command_attempt_events (command_id, attempt_index, event_type)
    WHERE event_type <> 'STALE_RESULT_REJECTED';

CREATE TABLE kim.command_results (
    command_id text NOT NULL,
    attempt_index integer NOT NULL,
    result_id text NOT NULL,
    result_digest char(64) NOT NULL CHECK (result_digest ~ '^[0-9a-f]{64}$'),
    execution_outcome text NOT NULL CHECK (execution_outcome IN ('SUCCEEDED', 'FAILED', 'UNKNOWN')),
    result_payload jsonb NOT NULL,
    receipt_id text NOT NULL UNIQUE,
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (command_id, attempt_index),
    UNIQUE (result_id),
    FOREIGN KEY (command_id, attempt_index)
        REFERENCES kim.command_attempts(command_id, attempt_index)
);

CREATE TABLE kim.command_verification_evidence (
    verification_id text PRIMARY KEY,
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL,
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verification_state text NOT NULL CHECK (verification_state IN ('MATCHED', 'NOT_APPLIED', 'CONFLICTING', 'UNKNOWN')),
    verifier_artifact_digest char(64) NOT NULL CHECK (verifier_artifact_digest ~ '^[0-9a-f]{64}$'),
    evidence_payload jsonb NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (command_id, attempt_index)
        REFERENCES kim.command_attempts(command_id, attempt_index)
);

CREATE TRIGGER execution_job_events_no_update
    BEFORE UPDATE ON kim.execution_job_events
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER execution_commands_no_update
    BEFORE UPDATE ON kim.execution_commands
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER command_lease_grants_no_update
    BEFORE UPDATE ON kim.command_lease_grants
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER command_lease_events_no_update
    BEFORE UPDATE ON kim.command_lease_events
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER command_attempts_no_update
    BEFORE UPDATE ON kim.command_attempts
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER command_attempt_events_no_update
    BEFORE UPDATE ON kim.command_attempt_events
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER command_results_no_update
    BEFORE UPDATE ON kim.command_results
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER command_verification_evidence_no_update
    BEFORE UPDATE ON kim.command_verification_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.command_lease_grants IS
    'Immutable Lease grant evidence bound to Host authority and session generations. Expiry is not proof of non-execution.';
COMMENT ON TABLE kim.command_attempt_events IS
    'Append-only Attempt facts. UNKNOWN is never rewritten to SUCCEEDED or FAILED.';
COMMENT ON TABLE kim.command_verification_evidence IS
    'Typed read-back evidence that resolves Job state without rewriting Attempt history.';
