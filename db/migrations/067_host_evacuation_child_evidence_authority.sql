CREATE TABLE kim.host_evacuation_source_shutdown_authority_evidence (
    shutdown_authority_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_host_authority_generation bigint NOT NULL CHECK(source_host_authority_generation>0),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    shutdown_command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    FOREIGN KEY(child_operation_id,child_generation)
      REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation)
);

CREATE TABLE kim.planned_source_quiescence_execution_evidence (
    quiescence_evidence_id text PRIMARY KEY REFERENCES kim.planned_source_quiescence_evidence(quiescence_evidence_id),
    shutdown_authority_id text NOT NULL UNIQUE REFERENCES kim.host_evacuation_source_shutdown_authority_evidence(shutdown_authority_id),
    shutdown_command_id text NOT NULL,
    shutdown_attempt_index integer NOT NULL CHECK(shutdown_attempt_index>0),
    shutdown_lease_generation bigint NOT NULL CHECK(shutdown_lease_generation>0),
    read_back_verification_id text NOT NULL UNIQUE REFERENCES kim.command_verification_evidence(verification_id),
    power_observation_evidence_id text NOT NULL UNIQUE REFERENCES kim.vm_power_observation_evidence(evidence_id),
    power_observation_generation bigint NOT NULL CHECK(power_observation_generation>0),
    power_observation_digest char(64) NOT NULL CHECK(power_observation_digest ~ '^[0-9a-f]{64}$'),
    verifier_digest char(64) NOT NULL CHECK(verifier_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK(evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(shutdown_command_id,shutdown_attempt_index)
      REFERENCES kim.command_attempts(command_id,attempt_index),
    FOREIGN KEY(shutdown_command_id,shutdown_lease_generation)
      REFERENCES kim.command_lease_grants(command_id,lease_generation)
);

CREATE TABLE kim.host_evacuation_destination_evidence_binding (
    destination_binding_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    child_plan_generation bigint NOT NULL CHECK(child_plan_generation>0),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    destination_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    destination_plan_digest char(64) NOT NULL CHECK(destination_plan_digest ~ '^[0-9a-f]{64}$'),
    definition_evidence_id text NOT NULL REFERENCES kim.vm_definition_observation_evidence(evidence_id),
    image_evidence_id text NOT NULL REFERENCES kim.vm_image_realization_evidence(evidence_id),
    power_evidence_id text NOT NULL REFERENCES kim.vm_power_observation_evidence(evidence_id),
    materialization_observation_generation bigint NOT NULL CHECK(materialization_observation_generation>0),
    power_observation_generation bigint NOT NULL CHECK(power_observation_generation>0),
    binding_digest char(64) NOT NULL CHECK(binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation,child_plan_generation),
    FOREIGN KEY(child_operation_id,child_generation)
      REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation),
    CHECK(destination_host_id<>'')
);

CREATE TABLE kim.host_evacuation_child_verification_evidence (
    verification_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    child_plan_generation bigint NOT NULL CHECK(child_plan_generation>0),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    quiescence_evidence_id text NOT NULL REFERENCES kim.planned_source_quiescence_evidence(quiescence_evidence_id),
    destination_binding_id text NOT NULL REFERENCES kim.host_evacuation_destination_evidence_binding(destination_binding_id),
    source_storage_state text NOT NULL CHECK(source_storage_state IN ('SAFE','NOT_REQUIRED')),
    source_network_state text NOT NULL CHECK(source_network_state IN ('RETIRED','NOT_REQUIRED')),
    source_pci_state text NOT NULL CHECK(source_pci_state IN ('RETIRED','NOT_REQUIRED')),
    destination_power_state text NOT NULL CHECK(destination_power_state='RUNNING'),
    destination_storage_state text NOT NULL CHECK(destination_storage_state IN ('CURRENT','NOT_REQUIRED')),
    destination_network_state text NOT NULL CHECK(destination_network_state IN ('CURRENT','NOT_REQUIRED')),
    destination_pci_state text NOT NULL CHECK(destination_pci_state IN ('CURRENT','NOT_REQUIRED')),
    source_ownership_state text NOT NULL CHECK(source_ownership_state='RETIRED'),
    source_host_authority_generation bigint NOT NULL CHECK(source_host_authority_generation>0),
    destination_materialization_generation bigint NOT NULL CHECK(destination_materialization_generation>0),
    destination_power_observation_generation bigint NOT NULL CHECK(destination_power_observation_generation>0),
    verification_state text NOT NULL CHECK(verification_state='VERIFIED'),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation,child_plan_generation),
    UNIQUE(verification_id,verification_digest),
    FOREIGN KEY(child_operation_id,child_generation)
      REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation),
    CHECK(source_host_id<>destination_host_id)
);

ALTER TABLE kim.host_evacuation_child_terminal_evidence
  ADD COLUMN child_verification_id text,
  ADD COLUMN child_verification_digest char(64),
  ADD CONSTRAINT host_evacuation_child_terminal_verification_fk
    FOREIGN KEY(child_verification_id,child_verification_digest)
    REFERENCES kim.host_evacuation_child_verification_evidence(verification_id,verification_digest),
  ADD CONSTRAINT host_evacuation_child_terminal_verification_complete
    CHECK((child_verification_id IS NULL)=(child_verification_digest IS NULL));

CREATE TRIGGER host_evacuation_source_shutdown_authority_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_source_shutdown_authority_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER planned_source_quiescence_execution_evidence_no_update BEFORE UPDATE ON kim.planned_source_quiescence_execution_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_destination_evidence_binding_no_update BEFORE UPDATE ON kim.host_evacuation_destination_evidence_binding FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_child_verification_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_child_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.planned_source_quiescence_execution_evidence IS 'Closed provenance from an evacuation child to the ordinary typed VM SHUTOFF Command, accepted Attempt/Lease, immutable verification and exact SHUTOFF power observation.';
COMMENT ON TABLE kim.host_evacuation_child_verification_evidence IS 'Pure PostgreSQL-derived child verification. Caller assertions never create source retirement, destination readiness, or RUNNING authority.';
COMMENT ON COLUMN kim.host_evacuation_child_terminal_evidence.child_verification_id IS 'Migration 067 terminal authority consumes a separately committed VERIFIED child evidence row; NULL is reserved only for pre-067 history.';
