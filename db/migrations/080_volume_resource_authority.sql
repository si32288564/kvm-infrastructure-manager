-- Independent logical Volume desired authority over the existing Local LVM
-- capacity, binding, copy, and cleanup evidence chains.
ALTER TABLE kim.volumes_current
    ALTER COLUMN placement_admission_id DROP NOT NULL,
    ADD COLUMN volume_revision bigint,
    ADD COLUMN volume_name text,
    ADD COLUMN source_type text,
    ADD COLUMN source_image_id text,
    ADD COLUMN source_image_revision bigint,
    ADD COLUMN source_artifact_digest char(64),
    ADD COLUMN delete_protection boolean,
    ADD COLUMN desired_digest char(64),
    ADD COLUMN authority_source text,
    ADD COLUMN updated_at timestamptz;

UPDATE kim.volumes_current SET
    volume_revision=desired_generation,
    volume_name=volume_id,
    source_type='LEGACY_ADMISSION',
    delete_protection=false,
    desired_digest=encode(sha256(convert_to(volume_id||':legacy:'||desired_generation::text,'UTF8')),'hex'),
    authority_source='LEGACY_ADMISSION',
    updated_at=created_at;

ALTER TABLE kim.volumes_current DROP CONSTRAINT volumes_current_lifecycle_state_check;
ALTER TABLE kim.volumes_current
    ALTER COLUMN volume_revision SET NOT NULL,
    ALTER COLUMN volume_name SET NOT NULL,
    ALTER COLUMN source_type SET NOT NULL,
    ALTER COLUMN delete_protection SET NOT NULL,
    ALTER COLUMN desired_digest SET NOT NULL,
    ALTER COLUMN authority_source SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL,
    ADD CHECK(volume_revision>0),
    ADD CHECK(length(volume_name) BETWEEN 1 AND 255),
    ADD CHECK(source_type IN('LEGACY_ADMISSION','BLANK','IMAGE')),
    ADD CHECK((source_type='IMAGE')=(source_image_id IS NOT NULL AND source_image_revision IS NOT NULL AND source_artifact_digest IS NOT NULL)),
    ADD CHECK(source_artifact_digest IS NULL OR source_artifact_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    ADD CHECK(authority_source IN('LEGACY_ADMISSION','VOLUME_RESOURCE')),
    ADD CHECK(lifecycle_state IN('ACTIVE','RESERVED','CREATING','AVAILABLE','UNKNOWN','BLOCKED','RETIRE_PENDING','DELETE_PENDING','DELETED'));

-- Defaults exist only for the historical/internal Admission producer. The
-- Volume resource producer supplies the complete logical contract explicitly.
ALTER TABLE kim.volumes_current
    ALTER COLUMN volume_revision SET DEFAULT 1,
    ALTER COLUMN volume_name SET DEFAULT 'legacy-volume',
    ALTER COLUMN source_type SET DEFAULT 'LEGACY_ADMISSION',
    ALTER COLUMN delete_protection SET DEFAULT false,
    ALTER COLUMN desired_digest SET DEFAULT '0000000000000000000000000000000000000000000000000000000000000000',
    ALTER COLUMN authority_source SET DEFAULT 'LEGACY_ADMISSION',
    ALTER COLUMN updated_at SET DEFAULT statement_timestamp();

CREATE TABLE kim.volume_resource_revision_evidence (
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL CHECK(volume_revision>0),
    project_id text NOT NULL,
    volume_name text NOT NULL CHECK(length(volume_name) BETWEEN 1 AND 255),
    size_bytes bigint NOT NULL CHECK(size_bytes>0 AND size_bytes%(1024*1024)=0),
    storage_class_id text NOT NULL,
    storage_class_revision bigint NOT NULL,
    access_mode text NOT NULL CHECK(access_mode='SINGLE_WRITER'),
    bootable boolean NOT NULL,
    source_type text NOT NULL CHECK(source_type IN('BLANK','IMAGE')),
    source_image_id text,
    source_image_revision bigint,
    source_artifact_digest char(64),
    delete_protection boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK(lifecycle_state IN('ACTIVE','RETIRE_PENDING','DELETED')),
    previous_revision bigint,
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(volume_id,volume_revision),
    FOREIGN KEY(storage_class_id,storage_class_revision) REFERENCES kim.storage_class_revision_evidence(storage_class_id,class_revision),
    FOREIGN KEY(source_image_id,source_image_revision) REFERENCES kim.northbound_image_revision_evidence(image_id,image_revision),
    CHECK((volume_revision=1 AND previous_revision IS NULL) OR (volume_revision>1 AND previous_revision=volume_revision-1)),
    CHECK((source_type='IMAGE')=(source_image_id IS NOT NULL AND source_image_revision IS NOT NULL AND source_artifact_digest IS NOT NULL)),
    CHECK(source_artifact_digest IS NULL OR source_artifact_digest ~ '^[0-9a-f]{64}$')
);

CREATE TABLE kim.volume_capacity_allocation_decision_evidence (
    allocation_id text NOT NULL,
    allocation_generation bigint NOT NULL CHECK(allocation_generation>0),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    storage_class_id text NOT NULL,
    storage_class_revision bigint NOT NULL,
    requested_bytes bigint NOT NULL CHECK(requested_bytes>0),
    backend_id text NOT NULL,
    backend_generation bigint NOT NULL,
    host_id text NOT NULL,
    vg_uuid text NOT NULL,
    capacity_generation bigint NOT NULL,
    capacity_observation_id text NOT NULL,
    decision_state text NOT NULL CHECK(decision_state='ALLOCATED'),
    decision_digest char(64) NOT NULL CHECK(decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(allocation_id,allocation_generation),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision),
    FOREIGN KEY(backend_id,host_id,vg_uuid) REFERENCES kim.storage_backends_current(backend_id,host_id,vg_uuid),
    FOREIGN KEY(capacity_observation_id,backend_id,capacity_generation) REFERENCES kim.storage_capacity_observation_evidence(observation_id,backend_id,capacity_generation)
);

ALTER TABLE kim.storage_capacity_claims DROP CONSTRAINT storage_capacity_claims_volume_id_key;
ALTER TABLE kim.storage_capacity_claims
    ALTER COLUMN placement_admission_id DROP NOT NULL,
    ADD COLUMN volume_revision bigint,
    ADD COLUMN allocation_generation bigint,
    ADD COLUMN allocation_decision_id text,
    ADD COLUMN authority_source text,
    ADD COLUMN updated_at timestamptz;
UPDATE kim.storage_capacity_claims SET volume_revision=1,allocation_generation=1,authority_source='LEGACY_ADMISSION',updated_at=created_at;
ALTER TABLE kim.storage_capacity_claims
    ALTER COLUMN volume_revision SET NOT NULL,
    ALTER COLUMN allocation_generation SET NOT NULL,
    ALTER COLUMN authority_source SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL,
    ALTER COLUMN volume_revision SET DEFAULT 1,
    ALTER COLUMN allocation_generation SET DEFAULT 1,
    ALTER COLUMN authority_source SET DEFAULT 'LEGACY_ADMISSION',
    ALTER COLUMN updated_at SET DEFAULT statement_timestamp(),
    ADD CHECK(authority_source IN('LEGACY_ADMISSION','VOLUME_RESOURCE')),
    ADD FOREIGN KEY(allocation_decision_id,allocation_generation) REFERENCES kim.volume_capacity_allocation_decision_evidence(allocation_id,allocation_generation);
CREATE UNIQUE INDEX volume_capacity_one_unreleased_incarnation
    ON kim.storage_capacity_claims(volume_id,backend_id)
    WHERE claim_state IN('RESERVED','ALLOCATED','RELEASE_PENDING','QUARANTINED');

ALTER TABLE kim.volume_backend_binding_intents DROP CONSTRAINT volume_backend_binding_intents_volume_id_key;
ALTER TABLE kim.volume_backend_binding_intents
    ALTER COLUMN placement_admission_id DROP NOT NULL,
    ADD COLUMN volume_revision bigint,
    ADD COLUMN materialization_generation bigint,
    ADD COLUMN capacity_allocation_id text,
    ADD COLUMN capacity_allocation_generation bigint,
    ADD COLUMN authority_source text,
    ADD COLUMN updated_at timestamptz;
UPDATE kim.volume_backend_binding_intents SET volume_revision=1,materialization_generation=binding_generation,authority_source='LEGACY_ADMISSION',updated_at=created_at;
ALTER TABLE kim.volume_backend_binding_intents
    ALTER COLUMN volume_revision SET NOT NULL,
    ALTER COLUMN materialization_generation SET NOT NULL,
    ALTER COLUMN authority_source SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL,
    ALTER COLUMN volume_revision SET DEFAULT 1,
    ALTER COLUMN materialization_generation SET DEFAULT 1,
    ALTER COLUMN authority_source SET DEFAULT 'LEGACY_ADMISSION',
    ALTER COLUMN updated_at SET DEFAULT statement_timestamp(),
    ADD CHECK(authority_source IN('LEGACY_ADMISSION','VOLUME_RESOURCE')),
    ADD FOREIGN KEY(capacity_allocation_id,capacity_allocation_generation) REFERENCES kim.volume_capacity_allocation_decision_evidence(allocation_id,allocation_generation),
    ADD UNIQUE(volume_id,binding_generation);
ALTER TABLE kim.volume_backend_bindings_current DROP CONSTRAINT volume_backend_bindings_current_volume_id_key;

CREATE TABLE kim.volume_materialization_operation_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK(operation_generation>0),
    operation_kind text NOT NULL CHECK(operation_kind IN('MATERIALIZE','RETIRE')),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    allocation_id text NOT NULL,
    allocation_generation bigint NOT NULL,
    binding_id text NOT NULL REFERENCES kim.volume_backend_binding_intents(binding_id),
    binding_generation bigint NOT NULL,
    materialization_generation bigint NOT NULL,
    backend_id text NOT NULL,
    backend_generation bigint NOT NULL,
    host_id text NOT NULL,
    vg_uuid text NOT NULL,
    backend_resource_key text NOT NULL CHECK(backend_resource_key ~ '^kim-[0-9a-f]{32}$'),
    expected_lv_uuid text,
    expected_size_bytes bigint NOT NULL CHECK(expected_size_bytes>0),
    source_type text NOT NULL CHECK(source_type IN('BLANK','IMAGE')),
    source_image_id text,
    source_image_revision bigint,
    source_artifact_digest char(64),
    schema_version text NOT NULL CHECK(schema_version='kim.storage.volume-materialization/v1'),
    canonical_plan jsonb NOT NULL CHECK(jsonb_typeof(canonical_plan)='object'),
    plan_digest char(64) NOT NULL CHECK(plan_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation),
    UNIQUE(volume_id,materialization_generation),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision),
    FOREIGN KEY(allocation_id,allocation_generation) REFERENCES kim.volume_capacity_allocation_decision_evidence(allocation_id,allocation_generation),
    CHECK((operation_kind='MATERIALIZE' AND expected_lv_uuid IS NULL) OR (operation_kind='RETIRE' AND expected_lv_uuid IS NOT NULL)),
    CHECK((source_type='IMAGE')=(source_image_id IS NOT NULL AND source_image_revision IS NOT NULL AND source_artifact_digest IS NOT NULL))
);
CREATE TABLE kim.volume_materialization_operations_current (
    operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL,
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    materialization_generation bigint NOT NULL,
    operation_kind text NOT NULL,
    phase text NOT NULL CHECK(phase IN('PENDING','CLAIMED','DISPATCH_UNKNOWN','SUCCEEDED','FAILED')),
    last_claim_generation bigint NOT NULL DEFAULT 0,
    claim_owner text,
    claim_generation bigint,
    claim_expires_at timestamptz,
    response_state text CHECK(response_state IN('RECEIVED','LOST','UNKNOWN')),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.volume_materialization_operation_evidence(operation_id,operation_generation),
    CHECK((phase='CLAIMED')=(claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL))
);
CREATE UNIQUE INDEX volume_one_active_materialization_operation ON kim.volume_materialization_operations_current(volume_id) WHERE phase IN('PENDING','CLAIMED','DISPATCH_UNKNOWN');
CREATE TABLE kim.volume_materialization_attempt_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL CHECK(claim_generation>0),
    claim_owner text NOT NULL,
    claim_mode text NOT NULL CHECK(claim_mode IN('APPLY_ALLOWED','READ_BACK_FIRST')),
    lease_expires_at timestamptz NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.volume_materialization_operation_evidence(operation_id,operation_generation)
);
CREATE TABLE kim.volume_materialization_command_evidence (
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    job_id text NOT NULL,
    command_mode text NOT NULL CHECK(command_mode IN('APPLY','READ_BACK')),
    command_payload_digest char(64) NOT NULL CHECK(command_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(operation_id,operation_generation,claim_generation,command_mode),
    FOREIGN KEY(operation_id,operation_generation,claim_generation) REFERENCES kim.volume_materialization_attempt_evidence(operation_id,operation_generation,claim_generation)
);
CREATE TABLE kim.volume_materialization_observation_evidence (
    observation_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    materialization_generation bigint NOT NULL,
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL,
    observed_vg_uuid text NOT NULL,
    observed_lv_uuid text,
    observed_size_bytes bigint,
    object_present boolean NOT NULL,
    identity_matches boolean NOT NULL,
    size_matches boolean NOT NULL,
    response_state text NOT NULL CHECK(response_state IN('RECEIVED','LOST','UNKNOWN')),
    observation_generation bigint NOT NULL,
    observation_digest char(64) NOT NULL CHECK(observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_artifact_digest char(64) NOT NULL CHECK(verifier_artifact_digest ~ '^[0-9a-f]{64}$'),
    evidence_state text NOT NULL CHECK(evidence_state IN('MATCHED','CONFLICTING','UNKNOWN','NOT_APPLIED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation,claim_generation) REFERENCES kim.volume_materialization_attempt_evidence(operation_id,operation_generation,claim_generation),
    UNIQUE(operation_id,operation_generation,claim_generation,observation_generation)
);
CREATE TABLE kim.volume_materialization_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    observation_id text NOT NULL UNIQUE REFERENCES kim.volume_materialization_observation_evidence(observation_id),
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    materialization_generation bigint NOT NULL,
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL,
    terminal_state text NOT NULL CHECK(terminal_state IN('VERIFIED','ABSENT')),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.volume_materialization_operation_evidence(operation_id,operation_generation)
);
ALTER TABLE kim.volume_materialization_operations_current ADD FOREIGN KEY(terminal_evidence_id) REFERENCES kim.volume_materialization_terminal_evidence(terminal_evidence_id);

CREATE TABLE kim.volume_materializations_current (
    volume_id text NOT NULL,
    materialization_generation bigint NOT NULL,
    volume_revision bigint NOT NULL,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL,
    materialization_state text NOT NULL CHECK(materialization_state IN('PENDING','UNKNOWN','VERIFIED','ABSENT','FAILED')),
    terminal_evidence_id text REFERENCES kim.volume_materialization_terminal_evidence(terminal_evidence_id),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(volume_id,materialization_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.volume_materialization_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.volume_attachment_intent_evidence (
    attachment_intent_id text PRIMARY KEY,
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    attachment_generation bigint NOT NULL CHECK(attachment_generation>0),
    workload_id text NOT NULL,
    requested_attachment_id text NOT NULL,
    requested_physical_attachment_generation bigint NOT NULL CHECK(requested_physical_attachment_generation>0),
    intent_state text NOT NULL CHECK(intent_state IN('REQUESTED','ATTACHED','RETIRED')),
    placement_admission_id text REFERENCES kim.placement_admission_decisions(admission_id),
    physical_attachment_id text REFERENCES kim.volume_attachments_current(attachment_id),
    binding_id text REFERENCES kim.volume_backend_binding_intents(binding_id),
    binding_generation bigint,
    intent_digest char(64) NOT NULL CHECK(intent_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision),
    CHECK((intent_state IN('REQUESTED','RETIRED') AND placement_admission_id IS NULL AND physical_attachment_id IS NULL AND binding_id IS NULL AND binding_generation IS NULL) OR
          (intent_state IN('ATTACHED','RETIRED') AND placement_admission_id IS NOT NULL AND physical_attachment_id IS NOT NULL AND binding_id IS NOT NULL AND binding_generation IS NOT NULL))
);
CREATE TABLE kim.volume_attachment_intents_current (
    volume_id text PRIMARY KEY REFERENCES kim.volumes_current(volume_id),
    volume_revision bigint NOT NULL,
    attachment_intent_id text NOT NULL REFERENCES kim.volume_attachment_intent_evidence(attachment_intent_id),
    attachment_generation bigint NOT NULL,
    workload_id text NOT NULL,
    requested_attachment_id text NOT NULL,
    requested_physical_attachment_generation bigint NOT NULL CHECK(requested_physical_attachment_generation>0),
    intent_state text NOT NULL CHECK(intent_state IN('REQUESTED','ATTACHED','RETIRED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.volume_capacity_release_evidence (
    release_evidence_id text PRIMARY KEY,
    volume_id text NOT NULL,
    volume_revision bigint NOT NULL,
    allocation_id text NOT NULL,
    allocation_generation bigint NOT NULL,
    capacity_claim_id text NOT NULL REFERENCES kim.storage_capacity_claims(capacity_claim_id),
    absence_terminal_evidence_id text NOT NULL REFERENCES kim.volume_materialization_terminal_evidence(terminal_evidence_id),
    released_bytes bigint NOT NULL CHECK(released_bytes>0),
    release_digest char(64) NOT NULL CHECK(release_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(allocation_id,allocation_generation),
    FOREIGN KEY(volume_id,volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision),
    FOREIGN KEY(allocation_id,allocation_generation) REFERENCES kim.volume_capacity_allocation_decision_evidence(allocation_id,allocation_generation)
);

CREATE TRIGGER volume_resource_revision_evidence_no_update BEFORE UPDATE ON kim.volume_resource_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_capacity_allocation_decision_evidence_no_update BEFORE UPDATE ON kim.volume_capacity_allocation_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_materialization_operation_evidence_no_update BEFORE UPDATE ON kim.volume_materialization_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_materialization_attempt_evidence_no_update BEFORE UPDATE ON kim.volume_materialization_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_materialization_command_evidence_no_update BEFORE UPDATE ON kim.volume_materialization_command_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_materialization_observation_evidence_no_update BEFORE UPDATE ON kim.volume_materialization_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_materialization_terminal_evidence_no_update BEFORE UPDATE ON kim.volume_materialization_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_attachment_intent_evidence_no_update BEFORE UPDATE ON kim.volume_attachment_intent_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER volume_capacity_release_evidence_no_update BEFORE UPDATE ON kim.volume_capacity_release_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.volume_resource_revision_evidence IS 'Immutable backend-neutral logical Volume desired revisions. Host, VG, LV, path, attachment, copy, and cleanup identity are excluded.';
COMMENT ON TABLE kim.volume_capacity_allocation_decision_evidence IS 'KIM capacity allocation authority; backend capacity observation is an input, not the decision.';
COMMENT ON TABLE kim.volume_materialization_operation_evidence IS 'Closed Local LVM incarnation operation consuming exact logical revision and capacity allocation. Future backends use separate typed consumers.';
COMMENT ON TABLE kim.volume_attachment_intent_evidence IS 'Logical workload attachment intent separated from persistent Volume existence and physical attachment rows.';
COMMENT ON TABLE kim.volume_capacity_release_evidence IS 'Capacity release is derived only from exact immutable backend ABSENT terminal evidence.';
