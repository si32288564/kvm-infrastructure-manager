CREATE TABLE kim.host_enrollment_decisions (
    decision_id text PRIMARY KEY,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    decision_revision bigint NOT NULL CHECK (decision_revision > 0),
    policy_id text NOT NULL,
    policy_generation bigint NOT NULL CHECK (policy_generation > 0),
    hardware_evidence_digest char(64) NOT NULL CHECK (hardware_evidence_digest ~ '^[0-9a-f]{64}$'),
    decision_state text NOT NULL CHECK (decision_state IN ('APPROVED', 'REJECTED', 'QUARANTINED', 'DECOMMISSIONED')),
    actor_id text NOT NULL,
    reason_code text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (host_id, decision_revision),
    UNIQUE (host_id, decision_id, decision_revision)
);

CREATE TABLE kim.host_enrollment_bindings_current (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    decision_id text NOT NULL,
    decision_revision bigint NOT NULL,
    binding_state text NOT NULL CHECK (binding_state IN ('ENROLLED', 'REJECTED', 'QUARANTINED', 'DECOMMISSIONED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (host_id, decision_id, decision_revision),
    FOREIGN KEY (host_id, decision_id, decision_revision)
        REFERENCES kim.host_enrollment_decisions(host_id, decision_id, decision_revision)
);

CREATE TABLE kim.agent_credential_binding_evidence (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    binding_revision bigint NOT NULL CHECK (binding_revision > 0),
    certificate_fingerprint_sha256 char(64) NOT NULL CHECK (certificate_fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
    public_key_digest char(64) NOT NULL CHECK (public_key_digest ~ '^[0-9a-f]{64}$'),
    issuer_id text NOT NULL,
    certificate_profile_revision text NOT NULL,
    trust_generation bigint NOT NULL CHECK (trust_generation > 0),
    enrollment_decision_id text NOT NULL,
    enrollment_decision_revision bigint NOT NULL,
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    binding_state text NOT NULL CHECK (binding_state IN ('ACTIVE', 'REVOKED', 'EXPIRED', 'QUARANTINED')),
    valid_not_before timestamptz NOT NULL,
    valid_not_after timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, binding_revision),
    CHECK (valid_not_after > valid_not_before),
    FOREIGN KEY (host_id, enrollment_decision_id, enrollment_decision_revision)
        REFERENCES kim.host_enrollment_decisions(host_id, decision_id, decision_revision)
);

CREATE TABLE kim.agent_credential_bindings_current (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    binding_revision bigint NOT NULL,
    binding_state text NOT NULL CHECK (binding_state IN ('CURRENT', 'REVOKED', 'EXPIRED', 'QUARANTINED', 'UNKNOWN')),
    trust_generation bigint NOT NULL CHECK (trust_generation > 0),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (host_id, binding_revision)
        REFERENCES kim.agent_credential_binding_evidence(host_id, binding_revision)
);

CREATE TABLE kim.host_session_authorizations_current (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    session_attempt_id text NOT NULL REFERENCES kim.agent_transport_session_attempts(session_attempt_id),
    credential_binding_revision bigint NOT NULL,
    enrollment_decision_revision bigint NOT NULL,
    capability_generation bigint,
    authorization_state text NOT NULL CHECK (authorization_state IN ('PENDING_CAPABILITY', 'AUTHORIZED', 'STALE', 'FENCED')),
    reason_code text NOT NULL,
    evaluated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (host_id, credential_binding_revision)
        REFERENCES kim.agent_credential_binding_evidence(host_id, binding_revision),
    FOREIGN KEY (host_id, enrollment_decision_revision)
        REFERENCES kim.host_enrollment_decisions(host_id, decision_revision)
);

CREATE TABLE kim.host_readiness_gates_current (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    capability_generation bigint NOT NULL CHECK (capability_generation > 0),
    baseline_assignment_generation bigint NOT NULL CHECK (baseline_assignment_generation > 0),
    preflight_generation bigint NOT NULL CHECK (preflight_generation > 0),
    preflight_state text NOT NULL CHECK (preflight_state IN ('PASSED', 'FAILED', 'UNKNOWN')),
    compliance_generation bigint NOT NULL CHECK (compliance_generation > 0),
    compliance_state text NOT NULL CHECK (compliance_state IN ('COMPLIANT', 'NON_COMPLIANT', 'DEGRADED', 'UNKNOWN')),
    gate_state text NOT NULL CHECK (gate_state IN ('READY', 'BLOCKED', 'UNKNOWN')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.host_operation_authorities_current (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    authority_generation bigint NOT NULL CHECK (authority_generation > 0),
    authority_state text NOT NULL CHECK (authority_state IN ('ARMED', 'DISARMED', 'FENCED')),
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    credential_binding_revision bigint NOT NULL,
    enrollment_decision_revision bigint NOT NULL,
    capability_generation bigint NOT NULL CHECK (capability_generation > 0),
    baseline_assignment_generation bigint NOT NULL CHECK (baseline_assignment_generation > 0),
    preflight_generation bigint NOT NULL CHECK (preflight_generation > 0),
    compliance_generation bigint NOT NULL CHECK (compliance_generation > 0),
    policy_id text NOT NULL,
    policy_generation bigint NOT NULL CHECK (policy_generation > 0),
    armed_by text NOT NULL,
    reason_code text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.host_operation_authority_events (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    authority_generation bigint NOT NULL CHECK (authority_generation > 0),
    event_sequence bigint NOT NULL CHECK (event_sequence > 0),
    event_type text NOT NULL CHECK (event_type IN ('ARMED', 'DISARMED', 'FENCED')),
    reason_code text NOT NULL,
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, authority_generation, event_sequence)
);

CREATE TRIGGER host_enrollment_decisions_no_update
    BEFORE UPDATE ON kim.host_enrollment_decisions
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER agent_credential_binding_evidence_no_update
    BEFORE UPDATE ON kim.agent_credential_binding_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_operation_authority_events_no_update
    BEFORE UPDATE ON kim.host_operation_authority_events
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.agent_credential_binding_evidence IS
    'Immutable identity binding evidence. A valid certificate is not Host mutation authority.';
COMMENT ON TABLE kim.host_session_authorizations_current IS
    'Current authenticated session authorization bound to Enrollment, credential, session, and capability generations.';
COMMENT ON TABLE kim.host_operation_authorities_current IS
    'Explicit mutation authority. Renewal, reconnect, Enrollment, or capability changes never arm this row.';
