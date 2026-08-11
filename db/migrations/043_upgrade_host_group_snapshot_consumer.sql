-- Bind new HostGroup-targeted upgrade plans to immutable UPGRADE snapshots.
-- Existing plans and snapshots remain immutable compatibility history and are
-- deliberately not attributed to authority that did not exist when recorded.
ALTER TABLE kim.host_group_membership_snapshot_evidence
    ADD COLUMN selector_id text,
    ADD COLUMN selector_generation bigint,
    ADD COLUMN selector_evaluation_id text,
    ADD COLUMN selector_evaluation_generation bigint,
    ADD COLUMN cardinality_policy_id text,
    ADD COLUMN cardinality_policy_generation bigint,
    ADD COLUMN cardinality text,
    ADD COLUMN snapshot_digest char(64),
    ADD CHECK (selector_generation IS NULL OR selector_generation > 0),
    ADD CHECK (selector_evaluation_generation IS NULL OR selector_evaluation_generation > 0),
    ADD CHECK ((selector_id IS NULL) = (selector_generation IS NULL)),
    ADD CHECK ((selector_id IS NULL) = (selector_evaluation_id IS NULL)),
    ADD CHECK ((selector_id IS NULL) = (selector_evaluation_generation IS NULL)),
    ADD CHECK (cardinality_policy_generation IS NULL OR cardinality_policy_generation > 0),
    ADD CHECK (cardinality IS NULL OR cardinality IN ('EXACTLY_ONE','ZERO_OR_ONE','MANY')),
    ADD CHECK ((cardinality_policy_id IS NULL) = (cardinality_policy_generation IS NULL)),
    ADD CHECK ((cardinality_policy_id IS NULL) = (cardinality IS NULL)),
    ADD CHECK (snapshot_digest IS NULL OR snapshot_digest ~ '^[0-9a-f]{64}$'),
    ADD FOREIGN KEY (selector_id,selector_generation,selector_evaluation_generation,selector_evaluation_id)
        REFERENCES kim.host_group_selector_evaluation_evidence(selector_id,selector_generation,evaluation_generation,evaluation_id),
    ADD FOREIGN KEY (cardinality_policy_id,cardinality_policy_generation,cardinality)
        REFERENCES kim.host_group_cardinality_policy_evidence(cardinality_policy_id,policy_generation,cardinality),
    ADD UNIQUE (snapshot_id,host_group_id,snapshot_digest);

ALTER TABLE kim.host_group_membership_snapshot_members
    ADD UNIQUE (snapshot_id,host_group_id,host_id,membership_generation,membership_evidence_digest);

CREATE TABLE kim.upgrade_plan_host_group_snapshot_evidence (
    campaign_id text NOT NULL,
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
    wave_id text NOT NULL,
    source_snapshot_id text NOT NULL,
    source_snapshot_digest char(64) NOT NULL CHECK (source_snapshot_digest ~ '^[0-9a-f]{64}$'),
    source_host_group_id text NOT NULL,
    source_membership_set_generation bigint NOT NULL CHECK (source_membership_set_generation > 0),
    component_type text NOT NULL CHECK (component_type = 'HOST_AGENT'),
    target_release_id text NOT NULL,
    target_manifest_revision bigint NOT NULL CHECK (target_manifest_revision > 0),
    target_artifact_digest char(64) NOT NULL CHECK (target_artifact_digest ~ '^[0-9a-f]{64}$'),
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (campaign_id,plan_revision,wave_id,source_snapshot_id),
    FOREIGN KEY (campaign_id,plan_revision,wave_id)
        REFERENCES kim.upgrade_wave_evidence(campaign_id,plan_revision,wave_id),
    FOREIGN KEY (source_snapshot_id,source_host_group_id,source_snapshot_digest)
        REFERENCES kim.host_group_membership_snapshot_evidence(snapshot_id,host_group_id,snapshot_digest),
    FOREIGN KEY (target_release_id,target_manifest_revision)
        REFERENCES kim.release_manifest_evidence(release_id,manifest_revision)
);

CREATE TABLE kim.upgrade_target_host_group_member_evidence (
    target_id text PRIMARY KEY REFERENCES kim.upgrade_target_evidence(target_id),
    campaign_id text NOT NULL,
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
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
    FOREIGN KEY (campaign_id,plan_revision,wave_id,source_snapshot_id)
        REFERENCES kim.upgrade_plan_host_group_snapshot_evidence(campaign_id,plan_revision,wave_id,source_snapshot_id),
    FOREIGN KEY (source_snapshot_id,source_host_group_id,host_id,membership_generation,membership_evidence_digest)
        REFERENCES kim.host_group_membership_snapshot_members(snapshot_id,host_group_id,host_id,membership_generation,membership_evidence_digest),
    FOREIGN KEY (source_host_group_id,source_membership_set_generation,host_id,membership_generation,membership_evidence_digest)
        REFERENCES kim.host_group_membership_set_member_evidence(host_group_id,membership_set_generation,host_id,membership_generation,membership_evidence_digest),
    CHECK (host_id <> '')
);

CREATE TABLE kim.upgrade_campaign_resume_evidence (
    resume_id text PRIMARY KEY,
    campaign_id text NOT NULL,
    plan_revision bigint NOT NULL CHECK (plan_revision > 0),
    campaign_generation bigint NOT NULL CHECK (campaign_generation > 0),
    wave_id text NOT NULL,
    resumed_state text NOT NULL CHECK (resumed_state IN ('CANARY','ROLLING')),
    actor text NOT NULL CHECK (actor <> ''),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (campaign_id,plan_revision,wave_id)
        REFERENCES kim.upgrade_wave_evidence(campaign_id,plan_revision,wave_id)
);

CREATE TRIGGER upgrade_plan_host_group_snapshot_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_plan_host_group_snapshot_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_target_host_group_member_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_target_host_group_member_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER upgrade_campaign_resume_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_campaign_resume_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.upgrade_plan_host_group_snapshot_evidence IS
'Immutable Plan/Wave binding to an exact UPGRADE HostGroup snapshot. Live membership is never an upgrade target source.';
COMMENT ON TABLE kim.upgrade_target_host_group_member_evidence IS
'Immutable audit path from Upgrade Target through Snapshot and accepted Membership Set member evidence.';
COMMENT ON COLUMN kim.host_group_membership_snapshot_evidence.snapshot_digest IS
'Canonical digest of the complete snapshot authority and provenance. NULL only for immutable pre-043 history.';
COMMENT ON TABLE kim.upgrade_campaign_resume_evidence IS
'Explicit resume authority for the existing Plan, Wave, Snapshot bindings, and Target evidence; resume never regenerates targets.';
