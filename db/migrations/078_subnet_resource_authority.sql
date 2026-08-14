-- Independent IPv4 Subnet desired, IPAM, and OVN DHCP realization authority.
-- Existing rows remain explicit legacy foundation projections.
ALTER TABLE kim.network_subnets_current
    ADD COLUMN project_id text,
    ADD COLUMN subnet_revision bigint,
    ADD COLUMN subnet_name text,
    ADD COLUMN ip_family text,
    ADD COLUMN gateway_policy text,
    ADD COLUMN gateway_address inet,
    ADD COLUMN allocation_policy text,
    ADD COLUMN dhcp_enabled boolean,
    ADD COLUMN dns_servers inet[],
    ADD COLUMN delete_protection boolean,
    ADD COLUMN desired_digest char(64),
    ADD COLUMN authority_source text,
    ADD COLUMN created_at timestamptz;

UPDATE kim.network_subnets_current subnet SET
    project_id=network.project_id,
    subnet_revision=subnet.subnet_generation,
    subnet_name=subnet.subnet_id,
    ip_family='IPV4',
    gateway_policy='NONE',
    allocation_policy='RANGE',
    dhcp_enabled=false,
    dns_servers='{}'::inet[],
    delete_protection=false,
    desired_digest=encode(sha256(convert_to(subnet.subnet_id||':'||subnet.subnet_generation::text||':'||subnet.cidr::text,'UTF8')),'hex'),
    authority_source='LEGACY_FOUNDATION',
    created_at=subnet.updated_at
FROM kim.networks_current network
WHERE network.network_id=subnet.network_id;

ALTER TABLE kim.network_subnets_current
    ALTER COLUMN project_id SET NOT NULL,
    ALTER COLUMN subnet_revision SET NOT NULL,
    ALTER COLUMN subnet_name SET NOT NULL,
    ALTER COLUMN ip_family SET NOT NULL,
    ALTER COLUMN gateway_policy SET NOT NULL,
    ALTER COLUMN allocation_policy SET NOT NULL,
    ALTER COLUMN dhcp_enabled SET NOT NULL,
    ALTER COLUMN dns_servers SET NOT NULL,
    ALTER COLUMN delete_protection SET NOT NULL,
    ALTER COLUMN desired_digest SET NOT NULL,
    ALTER COLUMN authority_source SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ADD CHECK (subnet_revision > 0),
    ADD CHECK (length(subnet_name) BETWEEN 1 AND 255),
    ADD CHECK (ip_family='IPV4'),
    ADD CHECK (gateway_policy IN ('NONE','AUTO','EXPLICIT')),
    ADD CHECK ((gateway_policy='NONE' AND gateway_address IS NULL) OR
               (gateway_policy IN ('AUTO','EXPLICIT') AND gateway_address IS NOT NULL)),
    ADD CHECK (gateway_address IS NULL OR (family(gateway_address)=family(cidr) AND gateway_address <<= cidr)),
    ADD CHECK (allocation_policy='RANGE'),
    ADD CHECK (family(cidr)=4),
    ADD CHECK (cardinality(dns_servers) <= 8),
    ADD CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK (authority_source IN ('LEGACY_FOUNDATION','SUBNET_RESOURCE'));

ALTER TABLE kim.network_subnets_current DROP CONSTRAINT network_subnets_current_lifecycle_state_check;
ALTER TABLE kim.network_subnets_current ADD CHECK (lifecycle_state IN ('ACTIVE','DRAINING','DISABLED','RETIRE_PENDING','DELETED'));

CREATE TABLE kim.subnet_resource_revision_evidence (
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL CHECK (subnet_revision > 0),
    project_id text NOT NULL,
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    subnet_name text NOT NULL CHECK (length(subnet_name) BETWEEN 1 AND 255),
    ip_family text NOT NULL CHECK (ip_family='IPV4'),
    cidr cidr NOT NULL CHECK (family(cidr)=4),
    gateway_policy text NOT NULL CHECK (gateway_policy IN ('NONE','AUTO','EXPLICIT')),
    gateway_address inet,
    allocation_policy text NOT NULL CHECK (allocation_policy='RANGE'),
    allocation_start inet NOT NULL,
    allocation_end inet NOT NULL,
    reserved_addresses inet[] NOT NULL,
    dhcp_enabled boolean NOT NULL,
    dns_servers inet[] NOT NULL,
    delete_protection boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','RETIRE_PENDING','DELETED')),
    previous_revision bigint,
    desired_digest char(64) NOT NULL CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(subnet_id,subnet_revision),
    UNIQUE(subnet_id,subnet_revision,desired_digest),
    FOREIGN KEY(network_id,network_revision) REFERENCES kim.network_resource_revision_evidence(network_id,network_revision),
    CHECK ((subnet_revision=1 AND previous_revision IS NULL) OR (subnet_revision>1 AND previous_revision=subnet_revision-1)),
    CHECK ((gateway_policy='NONE' AND gateway_address IS NULL) OR (gateway_policy IN ('AUTO','EXPLICIT') AND gateway_address IS NOT NULL)),
    CHECK (gateway_address IS NULL OR (gateway_address <<= cidr)),
    CHECK (allocation_start <<= cidr AND allocation_end <<= cidr AND allocation_start <= allocation_end),
    CHECK (cardinality(dns_servers) <= 8)
);

CREATE TABLE kim.subnet_ipam_pool_decision_evidence (
    pool_id text NOT NULL,
    pool_generation bigint NOT NULL CHECK (pool_generation > 0),
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    address_family text NOT NULL CHECK (address_family='IPV4'),
    allocation_start inet NOT NULL,
    allocation_end inet NOT NULL,
    reserved_addresses inet[] NOT NULL,
    decision_state text NOT NULL CHECK (decision_state='ACTIVE'),
    decision_digest char(64) NOT NULL CHECK (decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(pool_id,pool_generation),
    FOREIGN KEY(subnet_id,subnet_revision) REFERENCES kim.subnet_resource_revision_evidence(subnet_id,subnet_revision),
    UNIQUE(subnet_id,pool_generation),
    UNIQUE(pool_id,subnet_id,pool_generation)
);

CREATE TABLE kim.subnet_ipam_pools_current (
    subnet_id text PRIMARY KEY REFERENCES kim.network_subnets_current(subnet_id),
    subnet_revision bigint NOT NULL,
    pool_id text NOT NULL,
    pool_generation bigint NOT NULL,
    address_family text NOT NULL CHECK (address_family='IPV4'),
    allocation_start inet NOT NULL,
    allocation_end inet NOT NULL,
    reserved_addresses inet[] NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','RETIRE_PENDING','RETIRED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(pool_id,pool_generation) REFERENCES kim.subnet_ipam_pool_decision_evidence(pool_id,pool_generation),
    FOREIGN KEY(subnet_id,subnet_revision) REFERENCES kim.subnet_resource_revision_evidence(subnet_id,subnet_revision),
    UNIQUE(pool_id,subnet_id,pool_generation)
);

CREATE TABLE kim.subnet_ipam_pool_lifecycle_evidence (
    lifecycle_evidence_id text PRIMARY KEY,
    pool_id text NOT NULL,
    pool_generation bigint NOT NULL,
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('RETIRE_PENDING','RETIRED')),
    realization_terminal_evidence_id text,
    lifecycle_digest char(64) NOT NULL CHECK (lifecycle_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(pool_id,pool_generation) REFERENCES kim.subnet_ipam_pool_decision_evidence(pool_id,pool_generation),
    FOREIGN KEY(subnet_id,subnet_revision) REFERENCES kim.subnet_resource_revision_evidence(subnet_id,subnet_revision),
    CHECK ((lifecycle_state='RETIRE_PENDING' AND realization_terminal_evidence_id IS NULL) OR
           (lifecycle_state='RETIRED' AND realization_terminal_evidence_id IS NOT NULL)),
    UNIQUE(pool_id,pool_generation,lifecycle_state)
);

CREATE TABLE kim.subnet_ip_allocation_decision_evidence (
    allocation_id text NOT NULL,
    allocation_generation bigint NOT NULL CHECK (allocation_generation > 0),
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    pool_id text NOT NULL,
    pool_generation bigint NOT NULL,
    allocation_mode text NOT NULL CHECK (allocation_mode IN ('AUTO','EXPLICIT')),
    requested_address inet,
    assigned_address inet NOT NULL,
    owner_resource_type text NOT NULL CHECK (owner_resource_type='PORT'),
    owner_resource_id text NOT NULL,
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    decision_state text NOT NULL CHECK (decision_state='ALLOCATED'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(allocation_id,allocation_generation),
    FOREIGN KEY(pool_id,subnet_id,pool_generation) REFERENCES kim.subnet_ipam_pool_decision_evidence(pool_id,subnet_id,pool_generation),
    FOREIGN KEY(subnet_id,subnet_revision) REFERENCES kim.subnet_resource_revision_evidence(subnet_id,subnet_revision),
    CHECK ((allocation_mode='AUTO' AND requested_address IS NULL) OR (allocation_mode='EXPLICIT' AND requested_address=assigned_address)),
    UNIQUE(allocation_id,subnet_id,subnet_revision,allocation_generation),
    UNIQUE(allocation_id,subnet_id,subnet_revision,allocation_generation,assigned_address)
);

CREATE TABLE kim.subnet_ip_allocations_current (
    allocation_id text PRIMARY KEY,
    allocation_generation bigint NOT NULL,
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    pool_id text NOT NULL,
    pool_generation bigint NOT NULL,
    assigned_address inet NOT NULL,
    owner_resource_type text NOT NULL,
    owner_resource_id text NOT NULL,
    allocation_state text NOT NULL CHECK (allocation_state IN ('ALLOCATED','RELEASE_PENDING','RELEASED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(allocation_id,subnet_id,subnet_revision,allocation_generation)
      REFERENCES kim.subnet_ip_allocation_decision_evidence(allocation_id,subnet_id,subnet_revision,allocation_generation)
);

CREATE UNIQUE INDEX subnet_ip_one_unreleased_address
    ON kim.subnet_ip_allocations_current(subnet_id,assigned_address)
    WHERE allocation_state IN ('ALLOCATED','RELEASE_PENDING');

CREATE TABLE kim.subnet_ip_allocation_release_evidence (
    release_evidence_id text PRIMARY KEY,
    allocation_id text NOT NULL,
    allocation_generation bigint NOT NULL,
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    release_observation_id text NOT NULL REFERENCES kim.network_identity_release_observation_evidence(observation_id),
    release_digest char(64) NOT NULL CHECK (release_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(allocation_id,subnet_id,subnet_revision,allocation_generation)
      REFERENCES kim.subnet_ip_allocation_decision_evidence(allocation_id,subnet_id,subnet_revision,allocation_generation),
    UNIQUE(allocation_id,allocation_generation)
);

ALTER TABLE kim.network_identity_claims
    ADD COLUMN subnet_revision bigint,
    ADD COLUMN ip_allocation_id text,
    ADD COLUMN ip_allocation_generation bigint;

ALTER TABLE kim.network_identity_claims
    ADD CHECK ((ip_allocation_id IS NULL AND ip_allocation_generation IS NULL AND subnet_revision IS NULL) OR
               (claim_type='IP' AND ip_allocation_id IS NOT NULL AND ip_allocation_generation IS NOT NULL AND subnet_revision IS NOT NULL)),
    ADD FOREIGN KEY(ip_allocation_id,subnet_id,subnet_revision,ip_allocation_generation,ip_address)
      REFERENCES kim.subnet_ip_allocation_decision_evidence(allocation_id,subnet_id,subnet_revision,allocation_generation,assigned_address);

CREATE TABLE kim.subnet_realization_operation_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    operation_kind text NOT NULL CHECK (operation_kind IN ('REALIZE','RETIRE')),
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    realization_generation bigint NOT NULL CHECK (realization_generation > 0),
    schema_version text NOT NULL CHECK (schema_version='kim.network-intent.ovn-subnet/v1'),
    canonical_plan jsonb NOT NULL CHECK (jsonb_typeof(canonical_plan)='object'),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation),
    FOREIGN KEY(subnet_id,subnet_revision) REFERENCES kim.subnet_resource_revision_evidence(subnet_id,subnet_revision),
    FOREIGN KEY(network_id,network_revision) REFERENCES kim.network_resource_revision_evidence(network_id,network_revision),
    UNIQUE(subnet_id,realization_generation)
);

CREATE TABLE kim.subnet_realization_operations_current (
    operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL,
    subnet_id text NOT NULL UNIQUE,
    subnet_revision bigint NOT NULL,
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    realization_generation bigint NOT NULL,
    operation_kind text NOT NULL,
    phase text NOT NULL CHECK (phase IN ('PENDING','CLAIMED','DISPATCH_UNKNOWN','SUCCEEDED','FAILED')),
    last_claim_generation bigint NOT NULL DEFAULT 0,
    claim_owner text,
    claim_generation bigint,
    claim_expires_at timestamptz,
    response_state text CHECK (response_state IN ('RECEIVED','LOST','UNKNOWN')),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.subnet_realization_operation_evidence(operation_id,operation_generation),
    CHECK ((phase='CLAIMED' AND claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL) OR
           (phase<>'CLAIMED' AND claim_owner IS NULL AND claim_generation IS NULL AND claim_expires_at IS NULL))
);

CREATE TABLE kim.subnet_realization_attempt_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    claim_owner text NOT NULL,
    claim_mode text NOT NULL CHECK (claim_mode IN ('APPLY_ALLOWED','READ_BACK_FIRST')),
    lease_expires_at timestamptz NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.subnet_realization_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.subnet_realization_attempt_event_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('READ_BACK_STARTED','APPLY_AUTHORIZED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation,event_type),
    FOREIGN KEY(operation_id,operation_generation,claim_generation)
      REFERENCES kim.subnet_realization_attempt_evidence(operation_id,operation_generation,claim_generation)
);

CREATE TABLE kim.subnet_realization_observation_evidence (
    observation_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    network_revision bigint NOT NULL,
    realization_generation bigint NOT NULL,
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    apply_response_state text NOT NULL CHECK (apply_response_state IN ('RECEIVED','LOST','UNKNOWN')),
    dhcp_object_name text NOT NULL,
    backend_uuid text,
    object_present boolean NOT NULL,
    ownership_marker_matches boolean NOT NULL,
    plan_digest_matches boolean NOT NULL,
    cidr_matches boolean NOT NULL,
    options_match boolean NOT NULL,
    network_association_matches boolean NOT NULL,
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    adapter_artifact_digest char(64) NOT NULL CHECK (adapter_artifact_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.subnet_realization_operation_evidence(operation_id,operation_generation),
    UNIQUE(operation_id,operation_generation,observation_generation)
);

CREATE TABLE kim.subnet_realization_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    observation_id text NOT NULL REFERENCES kim.subnet_realization_observation_evidence(observation_id),
    subnet_id text NOT NULL,
    subnet_revision bigint NOT NULL,
    network_revision bigint NOT NULL,
    realization_generation bigint NOT NULL,
    terminal_state text NOT NULL CHECK (terminal_state IN ('VERIFIED','ABSENT')),
    terminal_digest char(64) NOT NULL CHECK (terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.subnet_realization_operation_evidence(operation_id,operation_generation)
);

ALTER TABLE kim.subnet_ipam_pool_lifecycle_evidence
    ADD FOREIGN KEY(realization_terminal_evidence_id)
    REFERENCES kim.subnet_realization_terminal_evidence(terminal_evidence_id);

CREATE TABLE kim.subnet_realizations_current (
    subnet_id text PRIMARY KEY REFERENCES kim.network_subnets_current(subnet_id),
    subnet_revision bigint NOT NULL,
    network_revision bigint NOT NULL,
    realization_generation bigint NOT NULL,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    realization_state text NOT NULL CHECK (realization_state IN ('PENDING','VERIFIED','UNKNOWN','ABSENT','FAILED')),
    terminal_evidence_id text REFERENCES kim.subnet_realization_terminal_evidence(terminal_evidence_id),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.subnet_realization_operation_evidence(operation_id,operation_generation)
);

CREATE TRIGGER subnet_resource_revision_evidence_no_update BEFORE UPDATE ON kim.subnet_resource_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_ipam_pool_decision_evidence_no_update BEFORE UPDATE ON kim.subnet_ipam_pool_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_ipam_pool_lifecycle_evidence_no_update BEFORE UPDATE ON kim.subnet_ipam_pool_lifecycle_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_ip_allocation_decision_evidence_no_update BEFORE UPDATE ON kim.subnet_ip_allocation_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_ip_allocation_release_evidence_no_update BEFORE UPDATE ON kim.subnet_ip_allocation_release_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_realization_operation_evidence_no_update BEFORE UPDATE ON kim.subnet_realization_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_realization_attempt_evidence_no_update BEFORE UPDATE ON kim.subnet_realization_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_realization_attempt_event_evidence_no_update BEFORE UPDATE ON kim.subnet_realization_attempt_event_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_realization_observation_evidence_no_update BEFORE UPDATE ON kim.subnet_realization_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER subnet_realization_terminal_evidence_no_update BEFORE UPDATE ON kim.subnet_realization_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.subnet_resource_revision_evidence IS 'Immutable logical IPv4 Subnet desired revisions; backend DHCP incarnation and Port allocations are separate.';
COMMENT ON TABLE kim.subnet_ipam_pools_current IS 'Current KIM IPAM pool authority. RETIRE_PENDING freezes all new allocations.';
COMMENT ON TABLE kim.subnet_realization_operations_current IS 'Standalone typed OVN DHCP realization authority; command response is not terminal verification.';
