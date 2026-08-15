-- Persistent logical Port authority, independent identity allocation, and
-- standalone OVN Logical Switch Port convergence.
ALTER TABLE kim.network_ports_current
    ALTER COLUMN placement_admission_id DROP NOT NULL,
    ALTER COLUMN workload_id DROP NOT NULL,
    ALTER COLUMN subnet_id DROP NOT NULL,
    ADD COLUMN port_revision bigint,
    ADD COLUMN port_name text,
    ADD COLUMN mac_policy text,
    ADD COLUMN requested_mac macaddr,
    ADD COLUMN ip_allocation_mode text,
    ADD COLUMN requested_ip inet,
    ADD COLUMN attachment_policy text,
    ADD COLUMN attachment_state text,
    ADD COLUMN datapath_profile text,
    ADD COLUMN delete_protection boolean,
    ADD COLUMN desired_digest char(64),
    ADD COLUMN authority_source text,
    ADD COLUMN updated_at timestamptz;

UPDATE kim.network_ports_current p SET
    port_revision=1,
    port_name=p.port_id,
    mac_policy='LEGACY_CLAIM',
    ip_allocation_mode='LEGACY_CLAIM',
    attachment_policy='WORKLOAD',
    attachment_state=CASE WHEN p.desired_state IN ('RELEASE_PENDING','RELEASED') THEN 'RETIRE_PENDING' ELSE 'BOUND' END,
    datapath_profile='STANDARD',
    delete_protection=false,
    desired_digest=encode(sha256(convert_to(p.port_id||':legacy:'||p.port_generation::text,'UTF8')),'hex'),
    authority_source='LEGACY_ADMISSION',
    updated_at=p.created_at;

ALTER TABLE kim.network_ports_current
    ALTER COLUMN port_revision SET NOT NULL,
    ALTER COLUMN port_name SET NOT NULL,
    ALTER COLUMN mac_policy SET NOT NULL,
    ALTER COLUMN ip_allocation_mode SET NOT NULL,
    ALTER COLUMN attachment_policy SET NOT NULL,
    ALTER COLUMN attachment_state SET NOT NULL,
    ALTER COLUMN datapath_profile SET NOT NULL,
    ALTER COLUMN delete_protection SET NOT NULL,
    ALTER COLUMN desired_digest SET NOT NULL,
    ALTER COLUMN authority_source SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL,
    ADD CHECK (port_revision > 0),
    ADD CHECK (length(port_name) BETWEEN 1 AND 255),
    ADD CHECK (mac_policy IN ('AUTO','EXPLICIT','LEGACY_CLAIM')),
    ADD CHECK ((mac_policy='EXPLICIT')=(requested_mac IS NOT NULL)),
    ADD CHECK (ip_allocation_mode IN ('NONE','AUTO','EXPLICIT','LEGACY_CLAIM')),
    ADD CHECK ((ip_allocation_mode='EXPLICIT')=(requested_ip IS NOT NULL)),
    ADD CHECK (attachment_policy IN ('ON_DEMAND','WORKLOAD')),
    ADD CHECK (attachment_state IN ('UNATTACHED','ATTACHMENT_REQUESTED','BOUND','RETIRE_PENDING','DELETED')),
    ADD CHECK (datapath_profile='STANDARD'),
    ADD CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK (authority_source IN ('LEGACY_ADMISSION','PORT_RESOURCE'));

-- Compatibility defaults apply only to historical/internal Admission writers.
-- The independent Port producer always supplies every value explicitly.
ALTER TABLE kim.network_ports_current
    ALTER COLUMN port_revision SET DEFAULT 1,
    ALTER COLUMN port_name SET DEFAULT 'legacy-port',
    ALTER COLUMN mac_policy SET DEFAULT 'LEGACY_CLAIM',
    ALTER COLUMN ip_allocation_mode SET DEFAULT 'LEGACY_CLAIM',
    ALTER COLUMN attachment_policy SET DEFAULT 'WORKLOAD',
    ALTER COLUMN attachment_state SET DEFAULT 'BOUND',
    ALTER COLUMN datapath_profile SET DEFAULT 'STANDARD',
    ALTER COLUMN delete_protection SET DEFAULT false,
    ALTER COLUMN desired_digest SET DEFAULT '0000000000000000000000000000000000000000000000000000000000000000',
    ALTER COLUMN authority_source SET DEFAULT 'LEGACY_ADMISSION',
    ALTER COLUMN updated_at SET DEFAULT statement_timestamp();

CREATE TABLE kim.port_resource_revision_evidence (
    port_id text NOT NULL,
    port_revision bigint NOT NULL CHECK (port_revision > 0),
    project_id text NOT NULL,
    network_id text NOT NULL,
    network_revision bigint NOT NULL CHECK (network_revision > 0),
    subnet_id text,
    subnet_revision bigint,
    port_name text NOT NULL CHECK (length(port_name) BETWEEN 1 AND 255),
    mac_policy text NOT NULL CHECK (mac_policy IN ('AUTO','EXPLICIT')),
    requested_mac macaddr,
    ip_allocation_mode text NOT NULL CHECK (ip_allocation_mode IN ('NONE','AUTO','EXPLICIT')),
    requested_ip inet,
    attachment_policy text NOT NULL CHECK (attachment_policy IN ('ON_DEMAND','WORKLOAD')),
    datapath_profile text NOT NULL CHECK (datapath_profile='STANDARD'),
    delete_protection boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','RETIRE_PENDING','DELETED')),
    previous_revision bigint,
    desired_digest char(64) NOT NULL CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(port_id,port_revision),
    FOREIGN KEY(network_id,network_revision) REFERENCES kim.network_resource_revision_evidence(network_id,network_revision),
    FOREIGN KEY(subnet_id,subnet_revision) REFERENCES kim.subnet_resource_revision_evidence(subnet_id,subnet_revision),
    CHECK ((port_revision=1 AND previous_revision IS NULL) OR (port_revision>1 AND previous_revision=port_revision-1)),
    CHECK ((subnet_id IS NULL)=(subnet_revision IS NULL)),
    CHECK ((ip_allocation_mode='NONE' AND subnet_id IS NULL AND requested_ip IS NULL) OR
           (ip_allocation_mode='AUTO' AND subnet_id IS NOT NULL AND requested_ip IS NULL) OR
           (ip_allocation_mode='EXPLICIT' AND subnet_id IS NOT NULL AND requested_ip IS NOT NULL)),
    CHECK ((mac_policy='EXPLICIT')=(requested_mac IS NOT NULL))
);

CREATE TABLE kim.port_mac_allocation_decision_evidence (
    allocation_id text NOT NULL,
    allocation_generation bigint NOT NULL CHECK (allocation_generation > 0),
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    network_id text NOT NULL,
    allocation_mode text NOT NULL CHECK (allocation_mode IN ('AUTO','EXPLICIT')),
    requested_mac macaddr,
    assigned_mac macaddr NOT NULL,
    decision_digest char(64) NOT NULL CHECK (decision_digest ~ '^[0-9a-f]{64}$'),
    decision_state text NOT NULL CHECK (decision_state='ALLOCATED'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(allocation_id,allocation_generation),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision),
    CHECK ((allocation_mode='EXPLICIT')=(requested_mac IS NOT NULL))
);

CREATE TABLE kim.port_mac_allocations_current (
    allocation_id text PRIMARY KEY,
    allocation_generation bigint NOT NULL,
    port_id text NOT NULL UNIQUE,
    port_revision bigint NOT NULL,
    network_id text NOT NULL,
    assigned_mac macaddr NOT NULL,
    allocation_state text NOT NULL CHECK (allocation_state IN ('ALLOCATED','RELEASE_PENDING','RELEASED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(allocation_id,allocation_generation) REFERENCES kim.port_mac_allocation_decision_evidence(allocation_id,allocation_generation)
);
CREATE UNIQUE INDEX port_mac_one_unreleased ON kim.port_mac_allocations_current(network_id,assigned_mac)
    WHERE allocation_state IN ('ALLOCATED','RELEASE_PENDING');

CREATE TABLE kim.port_attachment_intent_evidence (
    attachment_intent_id text PRIMARY KEY,
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    attachment_generation bigint NOT NULL CHECK (attachment_generation > 0),
    workload_id text NOT NULL,
    intent_state text NOT NULL CHECK (intent_state IN ('REQUESTED','BOUND','RETIRED')),
    placement_admission_id text REFERENCES kim.placement_admission_decisions(admission_id),
    binding_generation bigint,
    intent_digest char(64) NOT NULL CHECK (intent_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision),
    CHECK ((intent_state='REQUESTED' AND placement_admission_id IS NULL AND binding_generation IS NULL) OR
           (intent_state IN ('BOUND','RETIRED') AND placement_admission_id IS NOT NULL AND binding_generation IS NOT NULL))
);
CREATE TABLE kim.port_attachment_intents_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    port_revision bigint NOT NULL,
    attachment_intent_id text NOT NULL REFERENCES kim.port_attachment_intent_evidence(attachment_intent_id),
    attachment_generation bigint NOT NULL,
    workload_id text NOT NULL,
    intent_state text NOT NULL CHECK (intent_state IN ('REQUESTED','BOUND','RETIRED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.port_realization_operation_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    operation_kind text NOT NULL CHECK (operation_kind IN ('REALIZE','RETIRE')),
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    network_id text NOT NULL,
    network_revision bigint NOT NULL,
    subnet_id text,
    subnet_revision bigint,
    mac_allocation_id text NOT NULL,
    mac_allocation_generation bigint NOT NULL,
    ip_allocation_id text,
    ip_allocation_generation bigint,
    binding_generation bigint,
    realization_generation bigint NOT NULL CHECK (realization_generation > 0),
    schema_version text NOT NULL CHECK (schema_version='kim.network-intent.ovn-port-resource/v1'),
    canonical_plan jsonb NOT NULL CHECK (jsonb_typeof(canonical_plan)='object'),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision),
    FOREIGN KEY(mac_allocation_id,mac_allocation_generation) REFERENCES kim.port_mac_allocation_decision_evidence(allocation_id,allocation_generation),
    FOREIGN KEY(ip_allocation_id,ip_allocation_generation) REFERENCES kim.subnet_ip_allocation_decision_evidence(allocation_id,allocation_generation),
    CHECK ((ip_allocation_id IS NULL)=(ip_allocation_generation IS NULL)),
    UNIQUE(port_id,realization_generation)
);

CREATE TABLE kim.port_realization_operations_current (
    operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL,
    port_id text NOT NULL UNIQUE,
    port_revision bigint NOT NULL,
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
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.port_realization_operation_evidence(operation_id,operation_generation),
    CHECK ((phase='CLAIMED' AND claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL) OR
           (phase<>'CLAIMED' AND claim_owner IS NULL AND claim_generation IS NULL AND claim_expires_at IS NULL))
);

CREATE TABLE kim.port_realization_attempt_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    claim_owner text NOT NULL,
    claim_mode text NOT NULL CHECK (claim_mode IN ('APPLY_ALLOWED','READ_BACK_FIRST')),
    lease_expires_at timestamptz NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.port_realization_operation_evidence(operation_id,operation_generation)
);
CREATE TABLE kim.port_realization_attempt_event_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('READ_BACK_STARTED','APPLY_AUTHORIZED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation,event_type),
    FOREIGN KEY(operation_id,operation_generation,claim_generation) REFERENCES kim.port_realization_attempt_evidence(operation_id,operation_generation,claim_generation)
);

CREATE TABLE kim.port_realization_observation_evidence (
    observation_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    realization_generation bigint NOT NULL,
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    apply_response_state text NOT NULL CHECK (apply_response_state IN ('RECEIVED','LOST','UNKNOWN')),
    logical_port_name text NOT NULL,
    backend_uuid text,
    object_present boolean NOT NULL,
    ownership_marker_matches boolean NOT NULL,
    plan_digest_matches boolean NOT NULL,
    network_matches boolean NOT NULL,
    mac_matches boolean NOT NULL,
    ip_matches boolean NOT NULL,
    binding_matches boolean NOT NULL,
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    adapter_artifact_digest char(64) NOT NULL CHECK (adapter_artifact_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.port_realization_operation_evidence(operation_id,operation_generation),
    UNIQUE(operation_id,operation_generation,observation_generation)
);
CREATE TABLE kim.port_realization_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    observation_id text NOT NULL REFERENCES kim.port_realization_observation_evidence(observation_id),
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    realization_generation bigint NOT NULL,
    terminal_state text NOT NULL CHECK (terminal_state IN ('VERIFIED','ABSENT')),
    terminal_digest char(64) NOT NULL CHECK (terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.port_realization_operation_evidence(operation_id,operation_generation)
);
ALTER TABLE kim.port_realization_operations_current ADD FOREIGN KEY(terminal_evidence_id) REFERENCES kim.port_realization_terminal_evidence(terminal_evidence_id);

CREATE TABLE kim.port_realizations_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    port_revision bigint NOT NULL,
    realization_generation bigint NOT NULL,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    realization_state text NOT NULL CHECK (realization_state IN ('PENDING','VERIFIED','UNKNOWN','ABSENT','FAILED')),
    terminal_evidence_id text REFERENCES kim.port_realization_terminal_evidence(terminal_evidence_id),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.port_realization_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.port_identity_release_evidence (
    release_evidence_id text PRIMARY KEY,
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    mac_allocation_id text NOT NULL,
    mac_allocation_generation bigint NOT NULL,
    ip_allocation_id text,
    ip_allocation_generation bigint,
    backend_absence_terminal_evidence_id text NOT NULL REFERENCES kim.port_realization_terminal_evidence(terminal_evidence_id),
    release_digest char(64) NOT NULL CHECK (release_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision),
    FOREIGN KEY(mac_allocation_id,mac_allocation_generation) REFERENCES kim.port_mac_allocation_decision_evidence(allocation_id,allocation_generation),
    FOREIGN KEY(ip_allocation_id,ip_allocation_generation) REFERENCES kim.subnet_ip_allocation_decision_evidence(allocation_id,allocation_generation),
    CHECK ((ip_allocation_id IS NULL)=(ip_allocation_generation IS NULL)),
    UNIQUE(port_id,port_revision)
);

CREATE TRIGGER port_resource_revision_evidence_no_update BEFORE UPDATE ON kim.port_resource_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_mac_allocation_decision_evidence_no_update BEFORE UPDATE ON kim.port_mac_allocation_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_attachment_intent_evidence_no_update BEFORE UPDATE ON kim.port_attachment_intent_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_realization_operation_evidence_no_update BEFORE UPDATE ON kim.port_realization_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_realization_attempt_evidence_no_update BEFORE UPDATE ON kim.port_realization_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_realization_attempt_event_evidence_no_update BEFORE UPDATE ON kim.port_realization_attempt_event_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_realization_observation_evidence_no_update BEFORE UPDATE ON kim.port_realization_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_realization_terminal_evidence_no_update BEFORE UPDATE ON kim.port_realization_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER port_identity_release_evidence_no_update BEFORE UPDATE ON kim.port_identity_release_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.port_resource_revision_evidence IS 'Immutable logical Port desired revisions. Host, chassis, binding generation and backend UUID are intentionally excluded.';
COMMENT ON TABLE kim.port_mac_allocation_decision_evidence IS 'KIM-owned exact MAC decisions; Final Admission is a consumer, not a MAC producer.';
COMMENT ON TABLE kim.port_realization_operations_current IS 'Standalone typed OVN LSP convergence. Command response is never terminal verification.';
COMMENT ON TABLE kim.port_identity_release_evidence IS 'MAC/IP reuse authority derived only after exact backend LSP absence.';
