-- Connect immutable MAINTENANCE HostGroup snapshots to an independent
-- maintenance plan/wave/target authority. Upgrade authority is deliberately
-- not reused.
CREATE TABLE kim.maintenance_plan_evidence (
    maintenance_id text NOT NULL,
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
    source_snapshot_id text NOT NULL,
    source_snapshot_digest char(64) NOT NULL CHECK (source_snapshot_digest ~ '^[0-9a-f]{64}$'),
    source_host_group_id text NOT NULL,
    source_membership_set_generation bigint NOT NULL CHECK (source_membership_set_generation > 0),
    operation_type text NOT NULL CHECK (operation_type = 'HOST_DRAIN'),
    operation_schema_version text NOT NULL CHECK (operation_schema_version = 'v1'),
    profile_id text NOT NULL CHECK (profile_id <> ''),
    profile_revision bigint NOT NULL CHECK (profile_revision > 0),
    profile_digest char(64) NOT NULL CHECK (profile_digest ~ '^[0-9a-f]{64}$'),
    maximum_concurrent integer NOT NULL CHECK (maximum_concurrent > 0),
    failure_domain_maximum_unavailable integer NOT NULL CHECK (failure_domain_maximum_unavailable > 0),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    published_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (maintenance_id,plan_revision),
    FOREIGN KEY (source_snapshot_id,source_host_group_id,source_snapshot_digest)
        REFERENCES kim.host_group_membership_snapshot_evidence(snapshot_id,host_group_id,snapshot_digest)
);

CREATE TABLE kim.maintenance_wave_evidence (
    maintenance_id text NOT NULL,
    plan_revision bigint NOT NULL,
    wave_id text NOT NULL,
    wave_ordinal integer NOT NULL CHECK (wave_ordinal > 0),
    maximum_concurrent integer NOT NULL CHECK (maximum_concurrent > 0),
    failure_domain_maximum_unavailable integer NOT NULL CHECK (failure_domain_maximum_unavailable > 0),
    target_snapshot_digest char(64) NOT NULL CHECK (target_snapshot_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (maintenance_id,plan_revision,wave_id),
    UNIQUE (maintenance_id,plan_revision,wave_ordinal),
    FOREIGN KEY (maintenance_id,plan_revision)
        REFERENCES kim.maintenance_plan_evidence(maintenance_id,plan_revision)
);

CREATE TABLE kim.maintenance_target_evidence (
    target_id text PRIMARY KEY,
    maintenance_id text NOT NULL,
    plan_revision bigint NOT NULL,
    wave_id text NOT NULL,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    operation_type text NOT NULL CHECK (operation_type = 'HOST_DRAIN'),
    operation_schema_version text NOT NULL CHECK (operation_schema_version = 'v1'),
    target_digest char(64) NOT NULL CHECK (target_digest ~ '^[0-9a-f]{64}$'),
    UNIQUE (maintenance_id,plan_revision,host_id),
    FOREIGN KEY (maintenance_id,plan_revision,wave_id)
        REFERENCES kim.maintenance_wave_evidence(maintenance_id,plan_revision,wave_id)
);

CREATE TABLE kim.maintenance_target_host_group_member_evidence (
    target_id text PRIMARY KEY REFERENCES kim.maintenance_target_evidence(target_id),
    maintenance_id text NOT NULL,
    plan_revision bigint NOT NULL,
    wave_id text NOT NULL,
    source_snapshot_id text NOT NULL,
    source_snapshot_digest char(64) NOT NULL CHECK (source_snapshot_digest ~ '^[0-9a-f]{64}$'),
    source_host_group_id text NOT NULL,
    source_membership_set_generation bigint NOT NULL CHECK (source_membership_set_generation > 0),
    host_id text NOT NULL,
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_evidence_digest char(64) NOT NULL CHECK (membership_evidence_digest ~ '^[0-9a-f]{64}$'),
    provenance_digest char(64) NOT NULL CHECK (provenance_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (maintenance_id,plan_revision,wave_id)
        REFERENCES kim.maintenance_wave_evidence(maintenance_id,plan_revision,wave_id),
    FOREIGN KEY (source_snapshot_id,source_host_group_id,host_id,membership_generation,membership_evidence_digest)
        REFERENCES kim.host_group_membership_snapshot_members(snapshot_id,host_group_id,host_id,membership_generation,membership_evidence_digest),
    FOREIGN KEY (source_host_group_id,source_membership_set_generation,host_id,membership_generation,membership_evidence_digest)
        REFERENCES kim.host_group_membership_set_member_evidence(host_group_id,membership_set_generation,host_id,membership_generation,membership_evidence_digest)
);

CREATE TABLE kim.maintenance_plans_current (
    maintenance_id text PRIMARY KEY,
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
    maintenance_generation bigint NOT NULL CHECK (maintenance_generation > 0),
    maintenance_state text NOT NULL CHECK (maintenance_state IN ('PREPARED','ACTIVE','PAUSED','SUCCEEDED','FAILED','BLOCKED')),
    current_wave_id text NOT NULL,
    coordinator_owner text,
    coordinator_claim_generation bigint NOT NULL DEFAULT 0 CHECK (coordinator_claim_generation >= 0),
    coordinator_claim_expires_at timestamptz,
    coordinator_maximum_expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (maintenance_id,plan_revision,current_wave_id)
        REFERENCES kim.maintenance_wave_evidence(maintenance_id,plan_revision,wave_id),
    CHECK ((coordinator_owner IS NULL AND coordinator_claim_expires_at IS NULL AND coordinator_maximum_expires_at IS NULL)
        OR (coordinator_owner IS NOT NULL AND coordinator_claim_generation > 0
            AND coordinator_claim_expires_at IS NOT NULL AND coordinator_maximum_expires_at IS NOT NULL
            AND coordinator_claim_expires_at <= coordinator_maximum_expires_at))
);

CREATE TABLE kim.maintenance_targets_current (
    target_id text PRIMARY KEY REFERENCES kim.maintenance_target_evidence(target_id),
    target_state text NOT NULL CHECK (target_state IN ('PENDING','CLAIMED','UNKNOWN','SUCCEEDED','FAILED','FENCED','BLOCKED')),
    executor_owner text,
    attempt_generation bigint NOT NULL DEFAULT 0 CHECK (attempt_generation >= 0),
    attempt_mode text CHECK (attempt_mode IN ('APPLY_ALLOWED','READ_BACK_FIRST')),
    coordinator_claim_generation bigint NOT NULL DEFAULT 0 CHECK (coordinator_claim_generation >= 0),
    claim_expires_at timestamptz,
    maximum_expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK ((executor_owner IS NULL AND attempt_mode IS NULL AND claim_expires_at IS NULL AND maximum_expires_at IS NULL)
        OR (executor_owner IS NOT NULL AND attempt_generation > 0 AND attempt_mode IS NOT NULL
            AND coordinator_claim_generation > 0 AND claim_expires_at IS NOT NULL AND maximum_expires_at IS NOT NULL
            AND claim_expires_at <= maximum_expires_at))
);

CREATE TABLE kim.maintenance_coordinator_attempt_evidence (
    maintenance_id text NOT NULL,
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    coordinator_owner text NOT NULL,
    claim_mode text NOT NULL CHECK (claim_mode IN ('EXECUTE','RECOVER_FROM_DB')),
    lease_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    plan_revision bigint NOT NULL,
    maintenance_generation bigint NOT NULL CHECK (maintenance_generation > 0),
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (maintenance_id,claim_generation),
    FOREIGN KEY (maintenance_id) REFERENCES kim.maintenance_plans_current(maintenance_id)
);

CREATE TABLE kim.maintenance_target_attempt_evidence (
    target_id text NOT NULL REFERENCES kim.maintenance_target_evidence(target_id),
    attempt_generation bigint NOT NULL CHECK (attempt_generation > 0),
    maintenance_id text NOT NULL REFERENCES kim.maintenance_plans_current(maintenance_id),
    plan_revision bigint NOT NULL,
    wave_id text NOT NULL,
    executor_owner text NOT NULL,
    attempt_mode text NOT NULL CHECK (attempt_mode IN ('APPLY_ALLOWED','READ_BACK_FIRST')),
    coordinator_claim_generation bigint NOT NULL CHECK (coordinator_claim_generation > 0),
    lease_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    target_digest char(64) NOT NULL CHECK (target_digest ~ '^[0-9a-f]{64}$'),
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (target_id,attempt_generation),
    FOREIGN KEY (maintenance_id,plan_revision,wave_id)
        REFERENCES kim.maintenance_wave_evidence(maintenance_id,plan_revision,wave_id)
);

CREATE TABLE kim.maintenance_resume_evidence (
    resume_id text PRIMARY KEY,
    maintenance_id text NOT NULL,
    plan_revision bigint NOT NULL,
    maintenance_generation bigint NOT NULL CHECK (maintenance_generation > 0),
    wave_id text NOT NULL,
    actor text NOT NULL CHECK (actor <> ''),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (maintenance_id,plan_revision,wave_id)
        REFERENCES kim.maintenance_wave_evidence(maintenance_id,plan_revision,wave_id)
);

-- Cross-domain exclusion for typed disruptive Host operations. Expiry of a
-- subsystem Lease does not release this claim because it is not proof that the
-- side effect did not occur. Only the same Target may recover it, or accepted
-- completion may release it.
CREATE TABLE kim.host_disruptive_operation_claim_evidence (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    operation_domain text NOT NULL CHECK (operation_domain IN ('UPGRADE','MAINTENANCE')),
    authority_id text NOT NULL,
    target_id text NOT NULL,
    operation_type text NOT NULL CHECK (operation_type IN ('COMPONENT_UPGRADE','HOST_DRAIN')),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id,claim_generation),
    UNIQUE (host_id,claim_generation,operation_domain,authority_id,target_id,evidence_digest)
);

CREATE TABLE kim.host_disruptive_operation_claims_current (
    host_id text PRIMARY KEY,
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    operation_domain text NOT NULL,
    authority_id text NOT NULL,
    target_id text NOT NULL,
    operation_type text NOT NULL,
    claim_state text NOT NULL CHECK (claim_state IN ('ACTIVE','RELEASED')),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (host_id,claim_generation,operation_domain,authority_id,target_id,evidence_digest)
        REFERENCES kim.host_disruptive_operation_claim_evidence(host_id,claim_generation,operation_domain,authority_id,target_id,evidence_digest)
);

CREATE TRIGGER maintenance_plan_evidence_no_update BEFORE UPDATE ON kim.maintenance_plan_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER maintenance_wave_evidence_no_update BEFORE UPDATE ON kim.maintenance_wave_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER maintenance_target_evidence_no_update BEFORE UPDATE ON kim.maintenance_target_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER maintenance_target_host_group_member_evidence_no_update BEFORE UPDATE ON kim.maintenance_target_host_group_member_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER maintenance_coordinator_attempt_evidence_no_update BEFORE UPDATE ON kim.maintenance_coordinator_attempt_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER maintenance_target_attempt_evidence_no_update BEFORE UPDATE ON kim.maintenance_target_attempt_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER maintenance_resume_evidence_no_update BEFORE UPDATE ON kim.maintenance_resume_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_disruptive_operation_claim_evidence_no_update BEFORE UPDATE ON kim.host_disruptive_operation_claim_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.maintenance_plan_evidence IS
'Independent immutable maintenance authority bound to one exact purpose=MAINTENANCE HostGroup snapshot.';
COMMENT ON TABLE kim.maintenance_target_evidence IS
'Immutable Host target identity. Current Host trust, eligibility, or membership drift never rewrites or deletes it.';
COMMENT ON TABLE kim.host_disruptive_operation_claims_current IS
'Cross-domain exclusion for disruptive Host mutations. Lease expiry alone never releases an ambiguous operation.';
