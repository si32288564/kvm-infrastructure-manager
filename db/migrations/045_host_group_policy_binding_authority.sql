-- Closed Phase-1 Group Policy Binding authority. MAINTENANCE is the only
-- policy type with a persistence consumer in this increment; Availability is
-- deliberately not fabricated from its architecture-only contract.
CREATE TABLE kim.maintenance_policy_revision_evidence (
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    operation_type text NOT NULL CHECK (operation_type = 'HOST_DRAIN'),
    operation_schema_version text NOT NULL CHECK (operation_schema_version = 'v1'),
    profile_id text NOT NULL CHECK (profile_id <> ''),
    profile_revision bigint NOT NULL CHECK (profile_revision > 0),
    profile_digest char(64) NOT NULL CHECK (profile_digest ~ '^[0-9a-f]{64}$'),
    maximum_concurrent integer NOT NULL CHECK (maximum_concurrent > 0),
    failure_domain_maximum_unavailable integer NOT NULL CHECK (failure_domain_maximum_unavailable > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','RETIRED')),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (policy_id,policy_revision),
    UNIQUE (policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.maintenance_policies_current (
    policy_id text PRIMARY KEY,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','RETIRED')),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (policy_id,policy_revision,policy_digest)
        REFERENCES kim.maintenance_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.host_group_policy_binding_revision_evidence (
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    publish_request_id text NOT NULL UNIQUE,
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    policy_type text NOT NULL CHECK (policy_type = 'MAINTENANCE'),
    consumer_type text NOT NULL CHECK (consumer_type = 'MAINTENANCE_PLAN'),
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    priority integer NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DRAINING','RETIRED')),
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (binding_id,binding_generation),
    UNIQUE (binding_id,binding_generation,binding_digest),
    FOREIGN KEY (host_group_id,host_group_generation)
        REFERENCES kim.host_group_revision_evidence(host_group_id,host_group_generation),
    FOREIGN KEY (policy_id,policy_revision,policy_digest)
        REFERENCES kim.maintenance_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.host_group_policy_bindings_current (
    binding_id text PRIMARY KEY,
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    policy_type text NOT NULL CHECK (policy_type = 'MAINTENANCE'),
    consumer_type text NOT NULL CHECK (consumer_type = 'MAINTENANCE_PLAN'),
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    priority integer NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DRAINING','RETIRED')),
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (binding_id,binding_generation,binding_digest)
        REFERENCES kim.host_group_policy_binding_revision_evidence(binding_id,binding_generation,binding_digest)
);
CREATE INDEX host_group_policy_bindings_by_group
    ON kim.host_group_policy_bindings_current(host_group_id,consumer_type,policy_type,priority DESC);

CREATE TABLE kim.host_group_policy_resolution_evidence (
    resolution_id text PRIMARY KEY,
    subject_type text NOT NULL CHECK (subject_type = 'HOST'),
    subject_id text NOT NULL REFERENCES kim.host_identities(host_id),
    consumer_type text NOT NULL CHECK (consumer_type = 'MAINTENANCE_PLAN'),
    policy_type text NOT NULL CHECK (policy_type = 'MAINTENANCE'),
    resolution_result text NOT NULL CHECK (resolution_result IN
        ('RESOLVED','NO_ASSIGNMENT','ASSIGNMENT_CONFLICT','STALE_ASSIGNMENT','UNSUPPORTED')),
    winning_priority integer,
    effective_policy_id text,
    effective_policy_revision bigint CHECK (effective_policy_revision IS NULL OR effective_policy_revision > 0),
    effective_policy_digest char(64) CHECK (effective_policy_digest IS NULL OR effective_policy_digest ~ '^[0-9a-f]{64}$'),
    input_digest char(64) NOT NULL CHECK (input_digest ~ '^[0-9a-f]{64}$'),
    resolution_digest char(64) NOT NULL CHECK (resolution_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK ((resolution_result='RESOLVED') = (effective_policy_id IS NOT NULL)),
    CHECK ((effective_policy_id IS NULL) = (effective_policy_revision IS NULL)),
    CHECK ((effective_policy_id IS NULL) = (effective_policy_digest IS NULL))
);

CREATE TABLE kim.host_group_policy_resolution_input_evidence (
    resolution_id text NOT NULL REFERENCES kim.host_group_policy_resolution_evidence(resolution_id),
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    membership_set_generation bigint NOT NULL CHECK (membership_set_generation > 0),
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    priority integer NOT NULL,
    input_state text NOT NULL CHECK (input_state IN ('CURRENT','STALE')),
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (resolution_id,binding_id),
    FOREIGN KEY (binding_id,binding_generation,binding_digest)
        REFERENCES kim.host_group_policy_binding_revision_evidence(binding_id,binding_generation,binding_digest),
    FOREIGN KEY (host_group_id,membership_set_generation)
        REFERENCES kim.host_group_membership_set_evidence(host_group_id,membership_set_generation)
);

CREATE TABLE kim.maintenance_plan_policy_resolution_evidence (
    maintenance_id text NOT NULL,
    plan_revision bigint NOT NULL,
    host_id text NOT NULL,
    resolution_id text NOT NULL REFERENCES kim.host_group_policy_resolution_evidence(resolution_id),
    effective_policy_id text NOT NULL,
    effective_policy_revision bigint NOT NULL CHECK (effective_policy_revision > 0),
    effective_policy_digest char(64) NOT NULL CHECK (effective_policy_digest ~ '^[0-9a-f]{64}$'),
    provenance_digest char(64) NOT NULL CHECK (provenance_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (maintenance_id,plan_revision,host_id),
    UNIQUE (resolution_id),
    FOREIGN KEY (maintenance_id,plan_revision)
        REFERENCES kim.maintenance_plan_evidence(maintenance_id,plan_revision),
    FOREIGN KEY (effective_policy_id,effective_policy_revision,effective_policy_digest)
        REFERENCES kim.maintenance_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TRIGGER maintenance_policy_revision_evidence_no_update BEFORE UPDATE ON kim.maintenance_policy_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_policy_binding_revision_evidence_no_update BEFORE UPDATE ON kim.host_group_policy_binding_revision_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_policy_resolution_evidence_no_update BEFORE UPDATE ON kim.host_group_policy_resolution_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_group_policy_resolution_input_evidence_no_update BEFORE UPDATE ON kim.host_group_policy_resolution_input_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER maintenance_plan_policy_resolution_evidence_no_update BEFORE UPDATE ON kim.maintenance_plan_policy_resolution_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_group_policy_binding_revision_evidence IS
'Immutable exact HostGroup generation to exact typed Policy revision association; membership is a separate authority.';
COMMENT ON TABLE kim.host_group_policy_resolution_evidence IS
'Immutable deterministic consumer resolution. Equal-priority incompatible assignments conflict; stale highest priority never falls back.';
COMMENT ON TABLE kim.maintenance_plan_policy_resolution_evidence IS
'Exact policy resolution provenance for a binding-aware Maintenance Plan; membership Snapshot remains a separate authority.';
