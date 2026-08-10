CREATE TABLE kim.upgrade_plan_evidence (
    campaign_id text NOT NULL,
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
    source_release_id text NOT NULL,
    source_manifest_revision bigint NOT NULL CHECK (source_manifest_revision > 0),
    target_release_id text NOT NULL,
    target_manifest_revision bigint NOT NULL CHECK (target_manifest_revision > 0),
    strategy text NOT NULL CHECK (strategy IN ('CANARY_ROLLING')),
    component_graph jsonb NOT NULL CHECK (jsonb_typeof(component_graph) = 'object'),
    component_graph_digest char(64) NOT NULL CHECK (component_graph_digest ~ '^[0-9a-f]{64}$'),
    provenance_snapshot jsonb NOT NULL CHECK (jsonb_typeof(provenance_snapshot) = 'object'),
    provenance_snapshot_digest char(64) NOT NULL CHECK (provenance_snapshot_digest ~ '^[0-9a-f]{64}$'),
    sbom_snapshot_digest char(64) NOT NULL CHECK (sbom_snapshot_digest ~ '^[0-9a-f]{64}$'),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    published_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (campaign_id, plan_revision),
    FOREIGN KEY (source_release_id, source_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    FOREIGN KEY (target_release_id, target_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision),
    CHECK ((source_release_id, source_manifest_revision) <> (target_release_id, target_manifest_revision))
);

CREATE TABLE kim.upgrade_wave_evidence (
    campaign_id text NOT NULL,
    plan_revision bigint NOT NULL,
    wave_id text NOT NULL,
    wave_ordinal integer NOT NULL CHECK (wave_ordinal > 0),
    wave_type text NOT NULL CHECK (wave_type IN ('CANARY', 'BATCH')),
    maximum_unavailable integer NOT NULL CHECK (maximum_unavailable > 0),
    failure_threshold integer NOT NULL CHECK (failure_threshold >= 0),
    target_snapshot_digest char(64) NOT NULL CHECK (target_snapshot_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (campaign_id, plan_revision, wave_id),
    UNIQUE (campaign_id, plan_revision, wave_ordinal),
    FOREIGN KEY (campaign_id, plan_revision)
        REFERENCES kim.upgrade_plan_evidence(campaign_id, plan_revision)
);

CREATE TABLE kim.upgrade_target_evidence (
    target_id text PRIMARY KEY,
    campaign_id text NOT NULL,
    plan_revision bigint NOT NULL,
    wave_id text NOT NULL,
    component_type text NOT NULL CHECK (component_type IN (
        'API', 'AGENT_GATEWAY', 'CONTROL_WORKER', 'OVN_RUNTIME_WORKER', 'HOST_AGENT'
    )),
    component_id text NOT NULL,
    target_release_id text NOT NULL,
    target_manifest_revision bigint NOT NULL CHECK (target_manifest_revision > 0),
    target_artifact_digest char(64) NOT NULL CHECK (target_artifact_digest ~ '^[0-9a-f]{64}$'),
    target_digest char(64) NOT NULL CHECK (target_digest ~ '^[0-9a-f]{64}$'),
    UNIQUE (campaign_id, plan_revision, component_type, component_id),
    FOREIGN KEY (campaign_id, plan_revision, wave_id)
        REFERENCES kim.upgrade_wave_evidence(campaign_id, plan_revision, wave_id),
    FOREIGN KEY (target_release_id, target_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id, manifest_revision)
);

CREATE TABLE kim.upgrade_campaigns_current (
    campaign_id text PRIMARY KEY,
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
    campaign_generation bigint NOT NULL CHECK (campaign_generation > 0),
    campaign_state text NOT NULL CHECK (campaign_state IN (
        'PREPARED', 'CANARY', 'ROLLING', 'PAUSED', 'ABORTING', 'VERIFYING', 'SUCCEEDED', 'FAILED', 'BLOCKED'
    )),
    current_wave_id text,
    coordinator_owner text,
    coordinator_claim_generation bigint NOT NULL DEFAULT 0 CHECK (coordinator_claim_generation >= 0),
    coordinator_claim_expires_at timestamptz,
    coordinator_maximum_expires_at timestamptz,
    latest_canary_decision_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (campaign_id, plan_revision)
        REFERENCES kim.upgrade_plan_evidence(campaign_id, plan_revision),
    FOREIGN KEY (campaign_id, plan_revision, current_wave_id)
        REFERENCES kim.upgrade_wave_evidence(campaign_id, plan_revision, wave_id),
    CHECK (
        (coordinator_owner IS NULL AND coordinator_claim_expires_at IS NULL AND coordinator_maximum_expires_at IS NULL)
        OR
        (coordinator_owner IS NOT NULL AND coordinator_claim_generation > 0
         AND coordinator_claim_expires_at IS NOT NULL AND coordinator_maximum_expires_at IS NOT NULL
         AND coordinator_claim_expires_at <= coordinator_maximum_expires_at)
    )
);

CREATE TABLE kim.upgrade_targets_current (
    target_id text PRIMARY KEY REFERENCES kim.upgrade_target_evidence(target_id),
    target_state text NOT NULL CHECK (target_state IN ('PENDING', 'IN_PROGRESS', 'SUCCEEDED', 'FAILED', 'UNKNOWN', 'FENCED')),
    attempt_generation bigint NOT NULL DEFAULT 0 CHECK (attempt_generation >= 0),
    result_digest char(64) CHECK (result_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.upgrade_coordinator_attempt_evidence (
    campaign_id text NOT NULL,
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    coordinator_owner text NOT NULL,
    claim_mode text NOT NULL CHECK (claim_mode IN ('EXECUTE', 'RECOVER_FROM_DB')),
    lease_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    plan_revision bigint NOT NULL,
    campaign_generation bigint NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (campaign_id, claim_generation),
    FOREIGN KEY (campaign_id) REFERENCES kim.upgrade_campaigns_current(campaign_id)
);

CREATE TABLE kim.upgrade_campaign_event_evidence (
    event_id text PRIMARY KEY,
    campaign_id text NOT NULL REFERENCES kim.upgrade_campaigns_current(campaign_id),
    claim_generation bigint,
    event_type text NOT NULL CHECK (event_type IN (
        'CLAIM_GRANTED', 'COORDINATOR_UNKNOWN', 'TARGET_RESULT_ACCEPTED',
        'CANARY_CONTINUE', 'CANARY_PAUSED', 'CANARY_HELD'
    )),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.upgrade_canary_decision_evidence (
    decision_id text PRIMARY KEY,
    campaign_id text NOT NULL,
    plan_revision bigint NOT NULL,
    wave_id text NOT NULL,
    campaign_generation bigint NOT NULL CHECK (campaign_generation > 0),
    decision text NOT NULL CHECK (decision IN ('CONTINUE', 'PAUSE', 'HOLD')),
    succeeded_targets integer NOT NULL CHECK (succeeded_targets >= 0),
    failed_targets integer NOT NULL CHECK (failed_targets >= 0),
    unknown_targets integer NOT NULL CHECK (unknown_targets >= 0),
    pending_targets integer NOT NULL CHECK (pending_targets >= 0),
    failure_threshold integer NOT NULL CHECK (failure_threshold >= 0),
    evaluator_artifact_digest char(64) NOT NULL CHECK (evaluator_artifact_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    decided_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (campaign_id, plan_revision, wave_id)
        REFERENCES kim.upgrade_wave_evidence(campaign_id, plan_revision, wave_id)
);

ALTER TABLE kim.upgrade_campaigns_current
    ADD CONSTRAINT upgrade_campaign_latest_canary_decision_fk
    FOREIGN KEY (latest_canary_decision_id)
    REFERENCES kim.upgrade_canary_decision_evidence(decision_id);

CREATE TRIGGER upgrade_plan_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_plan_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_wave_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_wave_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_coordinator_attempt_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_coordinator_attempt_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_campaign_event_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_campaign_event_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_canary_decision_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_canary_decision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.upgrade_plan_evidence IS
'Immutable product-wide upgrade plan. Component graph, provenance, SBOM, waves, and targets are evidence, not in-memory coordinator state.';
COMMENT ON COLUMN kim.upgrade_campaigns_current.coordinator_claim_expires_at IS
'Expiry ends future coordinator authority but is not proof that an external upgrade side effect did not occur.';
COMMENT ON TABLE kim.upgrade_canary_decision_evidence IS
'Immutable threshold decision over the current target snapshot. HOLD/PAUSE does not roll back prior target evidence.';
