-- Recovery materialization reuses the ordinary VM/storage/network/execution
-- authorities. These tables add immutable Recovery provenance, verification,
-- and the explicit terminal decision only.

ALTER TABLE kim.vm_materialization_plan_evidence
  DROP CONSTRAINT vm_materialization_plan_evidence_vm_id_vm_generation_key;
ALTER TABLE kim.vm_materialization_plan_evidence
  ADD CONSTRAINT vm_materialization_plan_admission_identity_unique
  UNIQUE(vm_id,vm_generation,placement_admission_id);

CREATE TABLE kim.recovery_materialization_evidence (
    materialization_id text PRIMARY KEY,
    recovery_operation_id text NOT NULL UNIQUE REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    operation_generation bigint NOT NULL CHECK(operation_generation>0),
    destination_admission_id text NOT NULL UNIQUE REFERENCES kim.placement_admission_decisions(admission_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vm_id uuid NOT NULL,
    workload_id text NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    materialization_generation bigint NOT NULL CHECK(materialization_generation>0),
    vm_plan_id text NOT NULL UNIQUE REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    vm_plan_digest char(64) NOT NULL CHECK(vm_plan_digest ~ '^[0-9a-f]{64}$'),
    image_id text NOT NULL,
    image_revision bigint NOT NULL CHECK(image_revision>0),
    flavor_id text NOT NULL,
    flavor_revision bigint NOT NULL CHECK(flavor_revision>0),
    root_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    root_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    root_binding_generation bigint NOT NULL CHECK(root_binding_generation>0),
    root_attachment_id text NOT NULL REFERENCES kim.volume_attachments_current(attachment_id),
    root_attachment_generation bigint NOT NULL CHECK(root_attachment_generation>0),
    network_requirements_digest char(64) NOT NULL CHECK(network_requirements_digest ~ '^[0-9a-f]{64}$'),
    pci_requirements_digest char(64) NOT NULL CHECK(pci_requirements_digest ~ '^[0-9a-f]{64}$'),
    materialization_digest char(64) NOT NULL CHECK(materialization_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(vm_id,vm_generation) REFERENCES kim.virtual_machines_current(vm_id,vm_generation),
    UNIQUE(vm_id,materialization_generation),
    CHECK(materialization_generation>=vm_generation)
);

CREATE TABLE kim.recovery_power_authority_evidence (
    power_authority_id text PRIMARY KEY,
    recovery_operation_id text NOT NULL UNIQUE REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    materialization_id text NOT NULL UNIQUE REFERENCES kim.recovery_materialization_evidence(materialization_id),
    dangerous_step_evaluation_id text NOT NULL REFERENCES kim.recovery_dangerous_step_evaluation_evidence(evaluation_id),
    dangerous_step_evaluation_digest char(64) NOT NULL CHECK(dangerous_step_evaluation_digest ~ '^[0-9a-f]{64}$'),
    operation_state_generation bigint NOT NULL CHECK(operation_state_generation>0),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    readiness_observation_generation bigint NOT NULL CHECK(readiness_observation_generation>0),
    readiness_digest char(64) NOT NULL CHECK(readiness_digest ~ '^[0-9a-f]{64}$'),
    fencing_proof_id text NOT NULL,
    fencing_proof_digest char(64) NOT NULL,
    storage_safety_proof_id text NOT NULL,
    storage_safety_proof_digest char(64) NOT NULL,
    budget_claim_id text NOT NULL REFERENCES kim.recovery_budget_claim_evidence(claim_id),
    budget_state_generation bigint NOT NULL CHECK(budget_state_generation>0),
    power_job_id text NOT NULL UNIQUE REFERENCES kim.execution_jobs(job_id),
    power_command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(fencing_proof_id,fencing_proof_digest) REFERENCES kim.failure_fencing_proof_evidence(proof_id,proof_digest),
    FOREIGN KEY(storage_safety_proof_id,storage_safety_proof_digest) REFERENCES kim.storage_safety_proof_evidence(proof_id,proof_digest),
    FOREIGN KEY(vm_id,vm_generation) REFERENCES kim.virtual_machines_current(vm_id,vm_generation)
);

CREATE TABLE kim.recovery_verification_evidence (
    verification_id text PRIMARY KEY,
    recovery_operation_id text NOT NULL REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    operation_generation bigint NOT NULL CHECK(operation_generation>0),
    operation_state_generation bigint NOT NULL CHECK(operation_state_generation>0),
    materialization_id text NOT NULL REFERENCES kim.recovery_materialization_evidence(materialization_id),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    power_evidence_id text,
    power_observation_generation bigint,
    power_observation_digest char(64),
    storage_attachment_evidence_id text,
    storage_attachment_generation bigint,
    storage_observation_generation bigint,
    network_observation_generation bigint,
    network_evidence_set_digest char(64),
    pci_requirements_digest char(64) NOT NULL,
    fencing_proof_id text NOT NULL,
    fencing_proof_digest char(64) NOT NULL,
    fencing_usability text NOT NULL CHECK(fencing_usability IN ('USABLE','MISSING','STALE','UNKNOWN')),
    storage_safety_proof_id text NOT NULL,
    storage_safety_proof_digest char(64) NOT NULL,
    storage_usability text NOT NULL CHECK(storage_usability IN ('USABLE','MISSING','STALE','UNKNOWN')),
    budget_claim_id text NOT NULL REFERENCES kim.recovery_budget_claim_evidence(claim_id),
    budget_state_generation bigint NOT NULL CHECK(budget_state_generation>0),
    result_state text NOT NULL CHECK(result_state IN ('VERIFIED','NOT_VERIFIED','UNKNOWN','CONFLICTING_INPUT','STALE_OPERATION','STALE_FENCING','STALE_STORAGE','STALE_DESTINATION')),
    reason_code text NOT NULL CHECK(reason_code<>''),
    verifier_version text NOT NULL CHECK(verifier_version<>''),
    verifier_digest char(64) NOT NULL CHECK(verifier_digest ~ '^[0-9a-f]{64}$'),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(vm_id,vm_generation) REFERENCES kim.virtual_machines_current(vm_id,vm_generation),
    FOREIGN KEY(power_evidence_id,power_observation_generation) REFERENCES kim.vm_power_observation_evidence(evidence_id,observation_generation),
    FOREIGN KEY(fencing_proof_id,fencing_proof_digest) REFERENCES kim.failure_fencing_proof_evidence(proof_id,proof_digest),
    FOREIGN KEY(storage_safety_proof_id,storage_safety_proof_digest) REFERENCES kim.storage_safety_proof_evidence(proof_id,proof_digest),
    UNIQUE(recovery_operation_id,verification_digest)
);

CREATE TABLE kim.recovery_terminal_decision_evidence (
    terminal_decision_id text PRIMARY KEY,
    recovery_operation_id text NOT NULL UNIQUE REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    verification_id text NOT NULL UNIQUE REFERENCES kim.recovery_verification_evidence(verification_id),
    verification_digest char(64) NOT NULL,
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    budget_claim_id text NOT NULL UNIQUE REFERENCES kim.recovery_budget_claim_evidence(claim_id),
    decision_state text NOT NULL CHECK(decision_state='VERIFIED'),
    decided_by text NOT NULL CHECK(decided_by<>''),
    decision_digest char(64) NOT NULL CHECK(decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

ALTER TABLE kim.failure_epoch_transition_evidence
  ADD COLUMN recovery_terminal_decision_id text REFERENCES kim.recovery_terminal_decision_evidence(terminal_decision_id);
ALTER TABLE kim.failure_epoch_transition_evidence
  DROP CONSTRAINT failure_epoch_transition_evidence_to_state_check,
  DROP CONSTRAINT failure_epoch_transition_cause_authority_check;
ALTER TABLE kim.failure_epoch_transition_evidence
  ADD CONSTRAINT failure_epoch_transition_state_check CHECK(to_state IN ('SUSPECTED','CLEARED','CONFIRMED','FENCING','FENCED','FENCE_UNKNOWN','BLOCKED','RECOVERED')),
  ADD CONSTRAINT failure_epoch_transition_cause_authority_check CHECK(
       (to_state='SUSPECTED' AND cause_evidence_id IS NOT NULL AND confirmation_decision_id IS NULL AND fencing_proof_id IS NULL AND recovery_terminal_decision_id IS NULL)
    OR (to_state='CONFIRMED' AND cause_evidence_id IS NULL AND confirmation_decision_id IS NOT NULL AND fencing_proof_id IS NULL AND recovery_terminal_decision_id IS NULL)
    OR (to_state='FENCED' AND cause_evidence_id IS NULL AND confirmation_decision_id IS NULL AND fencing_proof_id IS NOT NULL AND recovery_terminal_decision_id IS NULL)
    OR (to_state='RECOVERED' AND cause_evidence_id IS NULL AND confirmation_decision_id IS NULL AND fencing_proof_id IS NULL AND recovery_terminal_decision_id IS NOT NULL)
    OR (to_state NOT IN ('SUSPECTED','CONFIRMED','FENCED','RECOVERED') AND cause_evidence_id IS NOT NULL AND confirmation_decision_id IS NULL AND fencing_proof_id IS NULL AND recovery_terminal_decision_id IS NULL));

ALTER TABLE kim.failure_epochs_current DROP CONSTRAINT failure_epochs_current_epoch_state_check;
ALTER TABLE kim.failure_epochs_current ADD CONSTRAINT failure_epochs_current_state_check
  CHECK(epoch_state IN ('SUSPECTED','CLEARED','CONFIRMED','FENCING','FENCED','FENCE_UNKNOWN','BLOCKED','RECOVERED'));

CREATE TRIGGER recovery_materialization_evidence_no_update BEFORE UPDATE ON kim.recovery_materialization_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_power_authority_evidence_no_update BEFORE UPDATE ON kim.recovery_power_authority_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_verification_evidence_no_update BEFORE UPDATE ON kim.recovery_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_terminal_decision_evidence_no_update BEFORE UPDATE ON kim.recovery_terminal_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.recovery_materialization_evidence IS 'Immutable Recovery orchestration provenance over ordinary destination Admission, VM, storage, network, PCI, and materialization authorities. It is not backend success or Recovery verification.';
COMMENT ON TABLE kim.recovery_power_authority_evidence IS 'Explicit power-on authority issued only after current dangerous-step and destination readiness revalidation. AUTHORIZED evaluation alone is not a power mutation.';
COMMENT ON TABLE kim.recovery_verification_evidence IS 'Pure immutable exact multi-domain read-back evaluation. VERIFIED evidence alone does not transition Operation, Failure Epoch, or Budget.';
COMMENT ON TABLE kim.recovery_terminal_decision_evidence IS 'Explicit positive terminal authority atomically linked to Operation VERIFIED, Failure Epoch RECOVERED, and Budget RELEASED transitions.';
