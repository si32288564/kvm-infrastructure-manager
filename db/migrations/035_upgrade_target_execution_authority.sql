CREATE TABLE kim.upgrade_target_executions_current (
    target_id text PRIMARY KEY REFERENCES kim.upgrade_target_evidence(target_id),
    execution_state text NOT NULL CHECK (execution_state IN (
        'PENDING', 'CLAIMED', 'UNKNOWN', 'SUCCEEDED', 'FAILED', 'FENCED'
    )),
    executor_owner text,
    attempt_generation bigint NOT NULL DEFAULT 0 CHECK (attempt_generation >= 0),
    attempt_mode text CHECK (attempt_mode IN ('APPLY_ALLOWED', 'READ_BACK_FIRST')),
    coordinator_claim_generation bigint NOT NULL DEFAULT 0 CHECK (coordinator_claim_generation >= 0),
    renewal_generation bigint NOT NULL DEFAULT 0 CHECK (renewal_generation >= 0),
    claim_expires_at timestamptz,
    maximum_expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK (
        (executor_owner IS NULL AND attempt_mode IS NULL AND claim_expires_at IS NULL AND maximum_expires_at IS NULL)
        OR
        (executor_owner IS NOT NULL AND attempt_generation > 0 AND attempt_mode IS NOT NULL
         AND coordinator_claim_generation > 0 AND claim_expires_at IS NOT NULL AND maximum_expires_at IS NOT NULL
         AND claim_expires_at <= maximum_expires_at)
    )
);

INSERT INTO kim.upgrade_target_executions_current(target_id, execution_state)
SELECT target_id, CASE target_state
    WHEN 'SUCCEEDED' THEN 'SUCCEEDED'
    WHEN 'FAILED' THEN 'FAILED'
    WHEN 'UNKNOWN' THEN 'UNKNOWN'
    WHEN 'FENCED' THEN 'FENCED'
    ELSE 'PENDING'
END
FROM kim.upgrade_targets_current;

CREATE TABLE kim.upgrade_target_attempt_evidence (
    target_id text NOT NULL REFERENCES kim.upgrade_target_evidence(target_id),
    attempt_generation bigint NOT NULL CHECK (attempt_generation > 0),
    campaign_id text NOT NULL REFERENCES kim.upgrade_campaigns_current(campaign_id),
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
    wave_id text NOT NULL,
    executor_owner text NOT NULL,
    attempt_mode text NOT NULL CHECK (attempt_mode IN ('APPLY_ALLOWED', 'READ_BACK_FIRST')),
    coordinator_claim_generation bigint NOT NULL CHECK (coordinator_claim_generation > 0),
    lease_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    target_digest char(64) NOT NULL CHECK (target_digest ~ '^[0-9a-f]{64}$'),
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (target_id, attempt_generation),
    FOREIGN KEY (campaign_id, plan_revision, wave_id)
        REFERENCES kim.upgrade_wave_evidence(campaign_id, plan_revision, wave_id),
    CHECK (lease_expires_at <= maximum_expires_at)
);

CREATE TABLE kim.upgrade_target_renewal_evidence (
    target_id text NOT NULL,
    attempt_generation bigint NOT NULL,
    renewal_generation bigint NOT NULL CHECK (renewal_generation > 0),
    executor_owner text NOT NULL,
    prior_expires_at timestamptz NOT NULL,
    renewed_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    renewed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (target_id, attempt_generation, renewal_generation),
    FOREIGN KEY (target_id, attempt_generation)
        REFERENCES kim.upgrade_target_attempt_evidence(target_id, attempt_generation),
    CHECK (renewed_expires_at > prior_expires_at),
    CHECK (renewed_expires_at <= maximum_expires_at)
);

CREATE TABLE kim.upgrade_target_observation_evidence (
    observation_id text PRIMARY KEY,
    target_id text NOT NULL,
    attempt_generation bigint NOT NULL,
    observation_state text NOT NULL CHECK (observation_state IN ('MATCHED', 'ABSENT', 'CONFLICTING', 'UNKNOWN')),
    observed_digest char(64) NOT NULL CHECK (observed_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (target_id, attempt_generation)
        REFERENCES kim.upgrade_target_attempt_evidence(target_id, attempt_generation)
);

CREATE TABLE kim.upgrade_target_result_evidence (
    result_id text PRIMARY KEY,
    target_id text NOT NULL,
    attempt_generation bigint NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('SUCCEEDED', 'FAILED')),
    result_digest char(64) NOT NULL CHECK (result_digest ~ '^[0-9a-f]{64}$'),
    observed_digest char(64) NOT NULL CHECK (observed_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (target_id, attempt_generation),
    FOREIGN KEY (target_id, attempt_generation)
        REFERENCES kim.upgrade_target_attempt_evidence(target_id, attempt_generation)
);

CREATE TABLE kim.upgrade_target_execution_event_evidence (
    event_id text PRIMARY KEY,
    target_id text NOT NULL REFERENCES kim.upgrade_target_evidence(target_id),
    attempt_generation bigint,
    event_type text NOT NULL CHECK (event_type IN (
        'CLAIM_GRANTED', 'TARGET_UNKNOWN', 'READ_BACK_STARTED', 'APPLY_AUTHORIZED',
        'RESULT_ACCEPTED', 'STALE_RESULT_FENCED'
    )),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TRIGGER upgrade_target_attempt_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_attempt_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_renewal_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_renewal_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_observation_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_observation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_result_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_result_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_execution_event_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_execution_event_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.upgrade_target_attempt_evidence IS
'Immutable Target execution authority. Claim expiry ends future executor authority but never proves that the component side effect did not occur.';
COMMENT ON TABLE kim.upgrade_target_observation_evidence IS
'Immutable typed read-back evidence. READ_BACK_FIRST is required after an ambiguous prior Target Attempt.';
COMMENT ON TABLE kim.upgrade_target_result_evidence IS
'Immutable accepted Target outcome. Process exit, transport response, and coordinator liveness are not Target completion evidence.';
