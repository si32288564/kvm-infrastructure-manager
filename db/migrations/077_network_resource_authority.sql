-- Independent Network resource authority. Existing rows remain explicit
-- LEGACY_FOUNDATION fixtures; new resource rows are revision/evidence backed.
ALTER TABLE kim.networks_current
    ADD COLUMN network_revision bigint,
    ADD COLUMN network_name text,
    ADD COLUMN network_profile text,
    ADD COLUMN segment_policy text,
    ADD COLUMN delete_protection boolean,
    ADD COLUMN desired_digest char(64),
    ADD COLUMN authority_source text,
    ADD COLUMN created_at timestamptz;

UPDATE kim.networks_current SET
    network_revision=network_generation,
    network_name=network_id,
    network_profile='LEGACY_FOUNDATION',
    segment_policy='LEGACY_EXPLICIT',
    delete_protection=false,
    desired_digest=encode(sha256(convert_to(network_id||':'||network_generation::text||':'||mtu::text,'UTF8')),'hex'),
    authority_source='LEGACY_FOUNDATION',
    created_at=updated_at;

ALTER TABLE kim.networks_current
    ALTER COLUMN network_revision SET NOT NULL,
    ALTER COLUMN network_name SET NOT NULL,
    ALTER COLUMN network_profile SET NOT NULL,
    ALTER COLUMN segment_policy SET NOT NULL,
    ALTER COLUMN delete_protection SET NOT NULL,
    ALTER COLUMN desired_digest SET NOT NULL,
    ALTER COLUMN authority_source SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ADD CHECK (network_revision > 0),
    ADD CHECK (length(network_name) BETWEEN 1 AND 255),
    ADD CHECK (network_profile IN ('LEGACY_FOUNDATION','STANDARD_OVERLAY','PROVIDER_VLAN')),
    ADD CHECK (segment_policy IN ('LEGACY_EXPLICIT','AUTO','EXPLICIT')),
    ADD CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK (authority_source IN ('LEGACY_FOUNDATION','NETWORK_RESOURCE'));

ALTER TABLE kim.networks_current DROP CONSTRAINT networks_current_lifecycle_state_check;
ALTER TABLE kim.networks_current ADD CHECK (lifecycle_state IN ('ACTIVE','DRAINING','DISABLED','RETIRE_PENDING','DELETED'));

CREATE TABLE kim.network_resource_revision_evidence (
    network_id text NOT NULL,
    network_revision bigint NOT NULL CHECK (network_revision > 0),
    project_id text NOT NULL,
    network_name text NOT NULL CHECK (length(network_name) BETWEEN 1 AND 255),
    network_profile text NOT NULL CHECK (network_profile IN ('STANDARD_OVERLAY','PROVIDER_VLAN')),
    mtu integer NOT NULL CHECK (mtu BETWEEN 576 AND 9216),
    segment_policy text NOT NULL CHECK (segment_policy IN ('AUTO','EXPLICIT')),
    requested_segment_id integer,
    delete_protection boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','RETIRE_PENDING','DELETED')),
    previous_revision bigint,
    desired_digest char(64) NOT NULL CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(network_id,network_revision),
    UNIQUE(network_id,network_revision,desired_digest),
    CHECK ((network_revision=1 AND previous_revision IS NULL) OR (network_revision>1 AND previous_revision=network_revision-1)),
    CHECK ((segment_policy='AUTO' AND requested_segment_id IS NULL) OR (segment_policy='EXPLICIT' AND requested_segment_id IS NOT NULL)),
    CHECK ((network_profile='STANDARD_OVERLAY' AND (requested_segment_id IS NULL OR requested_segment_id BETWEEN 1 AND 16777215)) OR
           (network_profile='PROVIDER_VLAN' AND requested_segment_id BETWEEN 1 AND 4094))
);

CREATE TABLE kim.network_segment_pools_current (
    segment_pool_id text PRIMARY KEY,
    segment_type text NOT NULL CHECK (segment_type IN ('VNI','VLAN')),
    namespace text NOT NULL,
    minimum_segment_id integer NOT NULL,
    maximum_segment_id integer NOT NULL,
    pool_generation bigint NOT NULL CHECK (pool_generation > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DRAINING','DISABLED')),
    CHECK (minimum_segment_id > 0 AND maximum_segment_id >= minimum_segment_id),
    UNIQUE(segment_type,namespace),
    UNIQUE(segment_pool_id,pool_generation)
);

INSERT INTO kim.network_segment_pools_current(segment_pool_id,segment_type,namespace,minimum_segment_id,maximum_segment_id,pool_generation,lifecycle_state)
VALUES('standard-overlay','VNI','overlay',10000,16777215,1,'ACTIVE'),
      ('provider-vlan','VLAN','provider',1,4094,1,'ACTIVE');

CREATE TABLE kim.network_segment_allocation_decision_evidence (
    allocation_id text PRIMARY KEY,
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    segment_pool_id text NOT NULL REFERENCES kim.network_segment_pools_current(segment_pool_id),
    pool_generation bigint NOT NULL,
    segment_type text NOT NULL CHECK (segment_type IN ('VNI','VLAN')),
    segment_id integer NOT NULL,
    allocation_generation bigint NOT NULL CHECK (allocation_generation > 0),
    decision_state text NOT NULL CHECK (decision_state='ALLOCATED'),
    decision_digest char(64) NOT NULL CHECK (decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(network_id,network_revision) REFERENCES kim.network_resource_revision_evidence(network_id,network_revision),
    FOREIGN KEY(segment_pool_id,pool_generation) REFERENCES kim.network_segment_pools_current(segment_pool_id,pool_generation),
    UNIQUE(network_id,allocation_generation),
    UNIQUE(allocation_id,network_id,allocation_generation)
);

CREATE TABLE kim.network_segment_allocations_current (
    network_id text PRIMARY KEY,
    allocation_id text NOT NULL UNIQUE REFERENCES kim.network_segment_allocation_decision_evidence(allocation_id),
    network_revision bigint NOT NULL,
    segment_pool_id text NOT NULL,
    segment_type text NOT NULL,
    segment_id integer NOT NULL,
    allocation_generation bigint NOT NULL,
    allocation_state text NOT NULL CHECK (allocation_state IN ('ALLOCATED','RELEASE_PENDING','RELEASED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(network_id,network_revision) REFERENCES kim.network_resource_revision_evidence(network_id,network_revision),
    FOREIGN KEY(allocation_id,network_id,allocation_generation)
      REFERENCES kim.network_segment_allocation_decision_evidence(allocation_id,network_id,allocation_generation),
    UNIQUE(segment_pool_id,segment_id),
    UNIQUE(network_id,allocation_generation)
);

CREATE TABLE kim.network_segment_release_evidence (
    release_evidence_id text PRIMARY KEY,
    allocation_id text NOT NULL REFERENCES kim.network_segment_allocation_decision_evidence(allocation_id),
    network_id text NOT NULL,
    allocation_generation bigint NOT NULL,
    retirement_terminal_evidence_id text NOT NULL,
    segment_pool_id text NOT NULL,
    segment_type text NOT NULL,
    segment_id integer NOT NULL,
    release_digest char(64) NOT NULL CHECK (release_digest ~ '^[0-9a-f]{64}$'),
    released_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(allocation_id,allocation_generation)
);

ALTER TABLE kim.network_segment_claims_current
    ADD COLUMN allocation_id text,
    ADD COLUMN allocation_generation bigint,
    ADD COLUMN network_revision bigint;

CREATE TABLE kim.network_realization_operation_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    operation_kind text NOT NULL CHECK (operation_kind IN ('REALIZE','RETIRE')),
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    allocation_id text NOT NULL REFERENCES kim.network_segment_allocation_decision_evidence(allocation_id),
    allocation_generation bigint NOT NULL,
    realization_generation bigint NOT NULL CHECK (realization_generation > 0),
    schema_version text NOT NULL CHECK (schema_version='kim.network-intent.ovn-network/v1'),
    canonical_plan jsonb NOT NULL CHECK (jsonb_typeof(canonical_plan)='object'),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation),
    FOREIGN KEY(network_id,network_revision) REFERENCES kim.network_resource_revision_evidence(network_id,network_revision),
    FOREIGN KEY(allocation_id,network_id,allocation_generation)
      REFERENCES kim.network_segment_allocation_decision_evidence(allocation_id,network_id,allocation_generation),
    UNIQUE(network_id,realization_generation)
);

CREATE TABLE kim.network_realization_operations_current (
    operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL,
    network_id text NOT NULL UNIQUE,
    network_revision bigint NOT NULL,
    allocation_id text NOT NULL,
    allocation_generation bigint NOT NULL,
    realization_generation bigint NOT NULL,
    operation_kind text NOT NULL,
    phase text NOT NULL CHECK (phase IN ('PENDING','CLAIMED','DISPATCH_UNKNOWN','VERIFYING','SUCCEEDED','FAILED')),
    last_claim_generation bigint NOT NULL DEFAULT 0,
    claim_owner text,
    claim_generation bigint,
    claim_expires_at timestamptz,
    response_state text CHECK (response_state IN ('RECEIVED','LOST','UNKNOWN')),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.network_realization_operation_evidence(operation_id,operation_generation),
    CHECK ((phase='CLAIMED' AND claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL) OR
           (phase<>'CLAIMED' AND claim_owner IS NULL AND claim_generation IS NULL AND claim_expires_at IS NULL))
);

CREATE TABLE kim.network_realization_attempt_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    claim_owner text NOT NULL,
    claim_mode text NOT NULL CHECK (claim_mode IN ('APPLY_ALLOWED','READ_BACK_FIRST')),
    lease_expires_at timestamptz NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.network_realization_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.network_realization_attempt_event_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('READ_BACK_STARTED','APPLY_AUTHORIZED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation,event_type),
    FOREIGN KEY(operation_id,operation_generation,claim_generation)
      REFERENCES kim.network_realization_attempt_evidence(operation_id,operation_generation,claim_generation)
);

CREATE TABLE kim.network_realization_observation_evidence (
    observation_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    allocation_generation bigint NOT NULL,
    realization_generation bigint NOT NULL,
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    apply_response_state text NOT NULL CHECK (apply_response_state IN ('RECEIVED','LOST','UNKNOWN')),
    logical_switch_name text NOT NULL,
    backend_uuid text,
    object_present boolean NOT NULL,
    ownership_marker_matches boolean NOT NULL,
    plan_digest_matches boolean NOT NULL,
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    adapter_artifact_digest char(64) NOT NULL CHECK (adapter_artifact_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.network_realization_operation_evidence(operation_id,operation_generation),
    UNIQUE(operation_id,operation_generation,observation_generation)
);

CREATE TABLE kim.network_realization_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    observation_id text NOT NULL REFERENCES kim.network_realization_observation_evidence(observation_id),
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    allocation_generation bigint NOT NULL,
    realization_generation bigint NOT NULL,
    terminal_state text NOT NULL CHECK (terminal_state IN ('VERIFIED','ABSENT')),
    terminal_digest char(64) NOT NULL CHECK (terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.network_realization_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.network_realizations_current (
    network_id text PRIMARY KEY REFERENCES kim.networks_current(network_id),
    network_revision bigint NOT NULL,
    allocation_generation bigint NOT NULL,
    realization_generation bigint NOT NULL,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    realization_state text NOT NULL CHECK (realization_state IN ('PENDING','VERIFIED','UNKNOWN','ABSENT','FAILED')),
    terminal_evidence_id text REFERENCES kim.network_realization_terminal_evidence(terminal_evidence_id),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.network_realization_operation_evidence(operation_id,operation_generation)
);

CREATE TRIGGER network_resource_revision_evidence_no_update BEFORE UPDATE ON kim.network_resource_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER network_segment_allocation_decision_evidence_no_update BEFORE UPDATE ON kim.network_segment_allocation_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER network_segment_release_evidence_no_update BEFORE UPDATE ON kim.network_segment_release_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER network_realization_operation_evidence_no_update BEFORE UPDATE ON kim.network_realization_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER network_realization_attempt_evidence_no_update BEFORE UPDATE ON kim.network_realization_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER network_realization_attempt_event_evidence_no_update BEFORE UPDATE ON kim.network_realization_attempt_event_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER network_realization_observation_evidence_no_update BEFORE UPDATE ON kim.network_realization_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER network_realization_terminal_evidence_no_update BEFORE UPDATE ON kim.network_realization_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.network_resource_revision_evidence IS 'Immutable logical Network desired revisions. OVN/segment/backend generations are not Network identity.';
COMMENT ON TABLE kim.network_segment_allocations_current IS 'KIM segment allocation authority. RELEASE_PENDING remains unavailable until exact backend absence terminal.';
COMMENT ON TABLE kim.network_realization_operations_current IS 'Standalone OVN Network realization work authority. A backend response is never a verified terminal.';
