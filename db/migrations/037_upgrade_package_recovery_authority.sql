CREATE TABLE kim.upgrade_target_recovery_plan_evidence (
    recovery_plan_id text PRIMARY KEY,
    target_id text NOT NULL REFERENCES kim.upgrade_target_evidence(target_id),
    recovery_generation bigint NOT NULL CHECK (recovery_generation > 0),
    source_attempt_generation bigint NOT NULL CHECK (source_attempt_generation > 0),
    source_observation_id text NOT NULL REFERENCES kim.upgrade_target_observation_evidence(observation_id),
    strategy text NOT NULL CHECK (strategy IN ('CONFIGURE_EXISTING')),
    authorization_id text NOT NULL,
    authorization_digest char(64) NOT NULL CHECK (authorization_digest ~ '^[0-9a-f]{64}$'),
    recovery_profile_revision bigint NOT NULL CHECK (recovery_profile_revision > 0),
    target_digest char(64) NOT NULL CHECK (target_digest ~ '^[0-9a-f]{64}$'),
    target_artifact_digest char(64) NOT NULL CHECK (target_artifact_digest ~ '^[0-9a-f]{64}$'),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    approved_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (target_id, recovery_generation),
    FOREIGN KEY (target_id, source_attempt_generation)
        REFERENCES kim.upgrade_target_attempt_evidence(target_id, attempt_generation)
);

CREATE TABLE kim.upgrade_target_recoveries_current (
    target_id text PRIMARY KEY REFERENCES kim.upgrade_target_evidence(target_id),
    recovery_plan_id text NOT NULL UNIQUE REFERENCES kim.upgrade_target_recovery_plan_evidence(recovery_plan_id),
    recovery_generation bigint NOT NULL CHECK (recovery_generation > 0),
    recovery_state text NOT NULL CHECK (recovery_state IN (
        'APPROVED', 'CLAIMED', 'UNKNOWN', 'VERIFIED', 'FAILED', 'FENCED', 'REARMED'
    )),
    executor_owner text,
    attempt_generation bigint NOT NULL DEFAULT 0 CHECK (attempt_generation >= 0),
    attempt_mode text CHECK (attempt_mode IN ('READ_BACK_FIRST', 'RECOVERY_APPLY_ALLOWED')),
    renewal_generation bigint NOT NULL DEFAULT 0 CHECK (renewal_generation >= 0),
    claim_expires_at timestamptz,
    maximum_expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK (
        (executor_owner IS NULL AND attempt_mode IS NULL AND claim_expires_at IS NULL AND maximum_expires_at IS NULL)
        OR
        (executor_owner IS NOT NULL AND attempt_generation > 0 AND attempt_mode IS NOT NULL
         AND claim_expires_at IS NOT NULL AND maximum_expires_at IS NOT NULL
         AND claim_expires_at <= maximum_expires_at)
    )
);

CREATE TABLE kim.upgrade_target_recovery_attempt_evidence (
    target_id text NOT NULL REFERENCES kim.upgrade_target_evidence(target_id),
    recovery_generation bigint NOT NULL CHECK (recovery_generation > 0),
    attempt_generation bigint NOT NULL CHECK (attempt_generation > 0),
    recovery_plan_id text NOT NULL REFERENCES kim.upgrade_target_recovery_plan_evidence(recovery_plan_id),
    executor_owner text NOT NULL,
    attempt_mode text NOT NULL CHECK (attempt_mode IN ('READ_BACK_FIRST', 'RECOVERY_APPLY_ALLOWED')),
    lease_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    authorization_digest char(64) NOT NULL CHECK (authorization_digest ~ '^[0-9a-f]{64}$'),
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (target_id, recovery_generation, attempt_generation),
    CHECK (lease_expires_at <= maximum_expires_at)
);

CREATE TABLE kim.upgrade_target_recovery_observation_evidence (
    observation_id text PRIMARY KEY,
    target_id text NOT NULL,
    recovery_generation bigint NOT NULL,
    attempt_generation bigint NOT NULL,
    observation_state text NOT NULL CHECK (observation_state IN ('MATCHED', 'CONFLICTING', 'UNKNOWN')),
    observed_condition text NOT NULL CHECK (observed_condition IN (
        'DESIRED_RELEASE_MATCHED', 'PACKAGE_HALF_CONFIGURED', 'PACKAGE_STATE_CONFLICT', 'OBSERVATION_UNKNOWN'
    )),
    observed_digest char(64) NOT NULL CHECK (observed_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (target_id, recovery_generation, attempt_generation)
        REFERENCES kim.upgrade_target_recovery_attempt_evidence(target_id, recovery_generation, attempt_generation)
);

CREATE TABLE kim.upgrade_target_recovery_renewal_evidence (
    target_id text NOT NULL,
    recovery_generation bigint NOT NULL,
    attempt_generation bigint NOT NULL,
    renewal_generation bigint NOT NULL CHECK (renewal_generation > 0),
    executor_owner text NOT NULL,
    prior_expires_at timestamptz NOT NULL,
    renewed_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    renewed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (target_id, recovery_generation, attempt_generation, renewal_generation),
    FOREIGN KEY (target_id, recovery_generation, attempt_generation)
        REFERENCES kim.upgrade_target_recovery_attempt_evidence(target_id, recovery_generation, attempt_generation),
    CHECK (renewed_expires_at > prior_expires_at),
    CHECK (renewed_expires_at <= maximum_expires_at)
);

CREATE TABLE kim.upgrade_target_recovery_result_evidence (
    result_id text PRIMARY KEY,
    target_id text NOT NULL,
    recovery_generation bigint NOT NULL,
    attempt_generation bigint NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('VERIFIED', 'FAILED')),
    result_digest char(64) NOT NULL CHECK (result_digest ~ '^[0-9a-f]{64}$'),
    observed_digest char(64) NOT NULL CHECK (observed_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (target_id, recovery_generation, attempt_generation),
    FOREIGN KEY (target_id, recovery_generation, attempt_generation)
        REFERENCES kim.upgrade_target_recovery_attempt_evidence(target_id, recovery_generation, attempt_generation)
);

CREATE TABLE kim.upgrade_target_recovery_rearm_evidence (
    rearm_id text PRIMARY KEY,
    target_id text NOT NULL,
    recovery_generation bigint NOT NULL,
    recovery_result_id text NOT NULL REFERENCES kim.upgrade_target_recovery_result_evidence(result_id),
    authorization_id text NOT NULL,
    authorization_digest char(64) NOT NULL CHECK (authorization_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    authorized_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (target_id, recovery_generation),
    FOREIGN KEY (target_id, recovery_generation)
        REFERENCES kim.upgrade_target_recovery_plan_evidence(target_id, recovery_generation)
);

CREATE TABLE kim.upgrade_target_recovery_event_evidence (
    event_id text PRIMARY KEY,
    target_id text NOT NULL REFERENCES kim.upgrade_target_evidence(target_id),
    recovery_generation bigint NOT NULL CHECK (recovery_generation > 0),
    attempt_generation bigint,
    event_type text NOT NULL CHECK (event_type IN (
        'PLAN_APPROVED', 'CLAIM_GRANTED', 'RECOVERY_UNKNOWN', 'READ_BACK_STARTED',
        'RECOVERY_APPLY_AUTHORIZED', 'RESULT_ACCEPTED', 'REARM_AUTHORIZED', 'STALE_RESULT_FENCED'
    )),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TRIGGER upgrade_target_recovery_plan_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_recovery_plan_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_recovery_attempt_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_recovery_attempt_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_recovery_observation_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_recovery_observation_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_recovery_renewal_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_recovery_renewal_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_recovery_result_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_recovery_result_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_recovery_rearm_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_recovery_rearm_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_recovery_event_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_recovery_event_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.upgrade_target_recovery_plan_evidence IS
'Immutable explicit recovery authority for one quarantined Upgrade Target. It is separate from the normal Upgrade Attempt authority.';
COMMENT ON COLUMN kim.upgrade_target_recovery_plan_evidence.strategy IS
'Closed recovery strategy. CONFIGURE_EXISTING never means arbitrary dpkg arguments, reinstall, downgrade, or rollback.';
COMMENT ON TABLE kim.upgrade_target_recovery_attempt_evidence IS
'Immutable recovery Attempt authority. Expiry never proves that the package recovery side effect did not occur.';
COMMENT ON TABLE kim.upgrade_target_recovery_result_evidence IS
'Immutable verified recovery result. Verification does not implicitly rearm the original Upgrade Target.';
