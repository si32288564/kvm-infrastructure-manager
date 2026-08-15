-- Phase 3 VM logical update, power intent, and verified deletion authority.
-- Logical revisions remain separate from physical runtime incarnations.  The
-- first qualified delete profile is zero Port and exactly one ROOT Volume.
ALTER TABLE kim.vm_lifecycle_operation_evidence
    DROP CONSTRAINT vm_lifecycle_operation_evidence_operation_kind_check,
    ADD CONSTRAINT vm_lifecycle_operation_evidence_operation_kind_check
        CHECK(operation_kind IN('CREATE','POWER','DELETE'));

ALTER TABLE kim.vm_lifecycle_operations_current
    DROP CONSTRAINT vm_lifecycle_operations_current_terminal_evidence_id_fkey;

-- A power-only runtime intent consumes the same immutable logical dependency
-- snapshot.  The snapshot is not a physical incarnation and need not be
-- duplicated when only desired power changes.
ALTER TABLE kim.vm_runtime_intent_evidence
    DROP CONSTRAINT vm_runtime_intent_evidence_dependency_snapshot_id_key;

CREATE TABLE kim.vm_logical_update_evidence (
    update_evidence_id text PRIMARY KEY,
    request_id text NOT NULL UNIQUE,
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    vm_id uuid NOT NULL,
    prior_vm_revision bigint NOT NULL,
    resulting_vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    prior_desired_digest char(64) NOT NULL CHECK(prior_desired_digest ~ '^[0-9a-f]{64}$'),
    resulting_desired_digest char(64) NOT NULL CHECK(resulting_desired_digest ~ '^[0-9a-f]{64}$'),
    vm_name text NOT NULL CHECK(length(vm_name) BETWEEN 1 AND 255),
    delete_protection boolean NOT NULL,
    update_digest char(64) NOT NULL CHECK(update_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(vm_id,resulting_vm_revision),
    FOREIGN KEY(vm_id,prior_vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,resulting_vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation),
    CHECK(resulting_vm_revision=prior_vm_revision+1)
);

CREATE TABLE kim.vm_power_update_command_authority_evidence (
    authority_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    admission_id text NOT NULL,
    host_id text NOT NULL,
    vm_generation bigint NOT NULL,
    plan_id text NOT NULL,
    materialization_generation bigint NOT NULL,
    prior_power_evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    desired_power_state text NOT NULL CHECK(desired_power_state IN('RUNNING','SHUTOFF')),
    job_id text NOT NULL UNIQUE REFERENCES kim.execution_jobs(job_id),
    command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation)
);

CREATE TABLE kim.vm_power_update_verification_evidence (
    verification_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    command_authority_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_power_update_command_authority_evidence(authority_evidence_id),
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    admission_id text NOT NULL,
    host_id text NOT NULL,
    vm_generation bigint NOT NULL,
    plan_id text NOT NULL,
    power_evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    power_observation_generation bigint NOT NULL CHECK(power_observation_generation>0),
    desired_power_state text NOT NULL CHECK(desired_power_state IN('RUNNING','SHUTOFF')),
    observed_power_state text NOT NULL CHECK(observed_power_state=desired_power_state),
    verification_state text NOT NULL CHECK(verification_state='VERIFIED'),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation)
);

CREATE TABLE kim.vm_power_update_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    verification_id text NOT NULL UNIQUE REFERENCES kim.vm_power_update_verification_evidence(verification_id),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    terminal_state text NOT NULL CHECK(terminal_state='VERIFIED'),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation)
);

CREATE TABLE kim.vm_delete_operation_evidence (
    delete_operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL CHECK(operation_generation=1),
    request_id text NOT NULL UNIQUE,
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    vm_id uuid NOT NULL,
    source_vm_revision bigint NOT NULL,
    retire_vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    dependency_digest char(64) NOT NULL CHECK(dependency_digest ~ '^[0-9a-f]{64}$'),
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL,
    vm_generation bigint NOT NULL,
    plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    materialization_generation bigint NOT NULL,
    root_volume_id text NOT NULL,
    root_volume_revision bigint NOT NULL,
    root_attachment_id text NOT NULL,
    root_attachment_generation bigint NOT NULL,
    root_binding_id text NOT NULL,
    root_binding_generation bigint NOT NULL,
    compute_allocation_id text NOT NULL,
    shutoff_power_evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    accepted_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation),
    FOREIGN KEY(vm_id,source_vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,retire_vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation),
    FOREIGN KEY(root_volume_id,root_volume_revision) REFERENCES kim.volume_resource_revision_evidence(volume_id,volume_revision),
    FOREIGN KEY(compute_allocation_id) REFERENCES kim.compute_allocation_claims(allocation_id),
    CHECK(retire_vm_revision=source_vm_revision+1)
);

CREATE TABLE kim.vm_delete_domain_absence_evidence (
    absence_evidence_id text PRIMARY KEY,
    delete_operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK(attempt_index>0),
    verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    observation_generation bigint NOT NULL CHECK(observation_generation>0),
    observation_digest char(64) NOT NULL CHECK(observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK(verifier_digest ~ '^[0-9a-f]{64}$'),
    domain_present boolean NOT NULL CHECK(NOT domain_present),
    identity_matches boolean NOT NULL CHECK(identity_matches),
    absence_digest char(64) NOT NULL CHECK(absence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_operation_evidence(delete_operation_id,operation_generation)
);

CREATE TABLE kim.vm_delete_storage_absence_evidence (
    absence_evidence_id text PRIMARY KEY,
    delete_operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    domain_absence_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_delete_domain_absence_evidence(absence_evidence_id),
    attachment_observation_evidence_id text NOT NULL UNIQUE REFERENCES kim.volume_attachment_observation_evidence(evidence_id),
    root_volume_id text NOT NULL,
    root_attachment_id text NOT NULL,
    root_attachment_generation bigint NOT NULL,
    root_binding_id text NOT NULL,
    root_binding_generation bigint NOT NULL,
    absence_digest char(64) NOT NULL CHECK(absence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_operation_evidence(delete_operation_id,operation_generation)
);

CREATE TABLE kim.vm_delete_compute_release_evidence (
    release_evidence_id text PRIMARY KEY,
    delete_operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    storage_absence_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_delete_storage_absence_evidence(absence_evidence_id),
    admission_id text NOT NULL,
    compute_allocation_id text NOT NULL,
    prior_claim_state text NOT NULL CHECK(prior_claim_state IN('RESERVED','ALLOCATED','RELEASE_PENDING')),
    resulting_claim_state text NOT NULL CHECK(resulting_claim_state='RELEASED'),
    release_digest char(64) NOT NULL CHECK(release_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_operation_evidence(delete_operation_id,operation_generation),
    FOREIGN KEY(compute_allocation_id) REFERENCES kim.compute_allocation_claims(allocation_id)
);

CREATE TABLE kim.vm_delete_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    delete_operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    domain_absence_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_delete_domain_absence_evidence(absence_evidence_id),
    storage_absence_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_delete_storage_absence_evidence(absence_evidence_id),
    compute_release_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_delete_compute_release_evidence(release_evidence_id),
    terminal_state text NOT NULL CHECK(terminal_state='VERIFIED'),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_operation_evidence(delete_operation_id,operation_generation)
);

CREATE TABLE kim.vm_resource_tombstone_evidence (
    tombstone_evidence_id text PRIMARY KEY,
    vm_id uuid NOT NULL UNIQUE,
    final_vm_revision bigint NOT NULL,
    delete_operation_id text NOT NULL,
    delete_terminal_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_delete_terminal_evidence(terminal_evidence_id),
    prior_desired_digest char(64) NOT NULL CHECK(prior_desired_digest ~ '^[0-9a-f]{64}$'),
    tombstone_digest char(64) NOT NULL CHECK(tombstone_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(vm_id,final_vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(delete_operation_id) REFERENCES kim.vm_delete_operation_evidence(delete_operation_id)
);

CREATE TRIGGER vm_logical_update_evidence_no_update BEFORE UPDATE ON kim.vm_logical_update_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_power_update_command_authority_evidence_no_update BEFORE UPDATE ON kim.vm_power_update_command_authority_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_power_update_verification_evidence_no_update BEFORE UPDATE ON kim.vm_power_update_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_power_update_terminal_evidence_no_update BEFORE UPDATE ON kim.vm_power_update_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_delete_operation_evidence_no_update BEFORE UPDATE ON kim.vm_delete_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_delete_domain_absence_evidence_no_update BEFORE UPDATE ON kim.vm_delete_domain_absence_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_delete_storage_absence_evidence_no_update BEFORE UPDATE ON kim.vm_delete_storage_absence_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_delete_compute_release_evidence_no_update BEFORE UPDATE ON kim.vm_delete_compute_release_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_delete_terminal_evidence_no_update BEFORE UPDATE ON kim.vm_delete_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_resource_tombstone_evidence_no_update BEFORE UPDATE ON kim.vm_resource_tombstone_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_logical_update_evidence IS 'Metadata-only VM revision authority; runtime intent and physical incarnation remain unchanged.';
COMMENT ON TABLE kim.vm_power_update_terminal_evidence IS 'Typed desired/observed power convergence for an exact current VM runtime incarnation.';
COMMENT ON TABLE kim.vm_delete_operation_evidence IS 'Exact zero-Port, one-ROOT delete authority accepted only after SHUTOFF read-back.';
COMMENT ON TABLE kim.vm_resource_tombstone_evidence IS 'Immutable logical VM deletion identity after Domain, attachment and compute absence/release verification.';
