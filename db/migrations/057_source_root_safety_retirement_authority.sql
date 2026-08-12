-- Source root safety is observation authority, not generic root detach. An
-- inactive Domain may retain its configured vda while the exact Local LVM LV
-- has no QEMU holder. Source materialization retirement closes one exact
-- incarnation logically; it does not delete Domain, Volume, LV, or evidence.

DO $$ DECLARE c record;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint
           WHERE conrelid='kim.storage_safety_policy_revision_evidence'::regclass
             AND contype='c' AND pg_get_constraintdef(oid) LIKE '%safety_mode%'
  LOOP
    EXECUTE format('ALTER TABLE kim.storage_safety_policy_revision_evidence DROP CONSTRAINT %I',c.conname);
  END LOOP;
END $$;
ALTER TABLE kim.storage_safety_policy_revision_evidence
  ADD CONSTRAINT storage_safety_policy_closed_mode_check
  CHECK (safety_mode IN ('SOURCE_DETACHED_NO_HOLDER','SOURCE_ROOT_QUIESCED_DATA_DETACHED'));

CREATE TABLE kim.source_root_safety_evaluation_evidence (
    evaluation_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evaluated_transition_generation bigint NOT NULL CHECK(evaluated_transition_generation>0),
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_plan_digest char(64) NOT NULL CHECK(source_plan_digest ~ '^[0-9a-f]{64}$'),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    root_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    root_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    root_binding_generation bigint NOT NULL CHECK(root_binding_generation>0),
    root_binding_observation_generation bigint NOT NULL CHECK(root_binding_observation_generation>0),
    root_binding_state text NOT NULL CHECK(root_binding_state IN ('BOUND','STALE','UNKNOWN','REVOKED')),
    root_lv_uuid text NOT NULL CHECK(root_lv_uuid<>''),
    root_attachment_id text NOT NULL REFERENCES kim.volume_attachments_current(attachment_id),
    root_attachment_generation bigint NOT NULL CHECK(root_attachment_generation>0),
    root_observation_evidence_id text NOT NULL,
    root_observation_generation bigint NOT NULL CHECK(root_observation_generation>0),
    root_observation_digest char(64) NOT NULL CHECK(root_observation_digest ~ '^[0-9a-f]{64}$'),
    -- Evaluations retain wrong-device evidence (for example vdb) so it can be
    -- classified CONFLICTING_INPUT. Only SAFE proof materialization requires
    -- exact vda; this does not expand mutation authority.
    target_device text NOT NULL CHECK(target_device ~ '^vd[a-z]$'),
    observed_lv_uuid text NOT NULL CHECK(observed_lv_uuid<>''),
    root_evidence_state text NOT NULL CHECK(root_evidence_state IN ('MATCHED','CONFLICTING','UNKNOWN','NOT_APPLIED')),
    device_present boolean NOT NULL,
    device_identity_matches boolean NOT NULL,
    source_identity_matches boolean NOT NULL,
    holder_open boolean NOT NULL,
    power_evidence_id text NOT NULL,
    power_observation_generation bigint NOT NULL CHECK(power_observation_generation>0),
    power_observation_digest char(64) NOT NULL CHECK(power_observation_digest ~ '^[0-9a-f]{64}$'),
    observed_power_state text NOT NULL CHECK(observed_power_state IN ('RUNNING','SHUTOFF')),
    power_convergence_state text NOT NULL CHECK(power_convergence_state IN ('MATCHED','CONFLICTING','UNKNOWN')),
    result_state text NOT NULL CHECK(result_state IN ('SAFE','NOT_SAFE','UNKNOWN','CONFLICTING_INPUT','STALE_EPOCH')),
    reason_code text NOT NULL CHECK(reason_code<>''),
    evaluator_version text NOT NULL CHECK(evaluator_version<>''),
    evaluator_digest char(64) NOT NULL CHECK(evaluator_digest ~ '^[0-9a-f]{64}$'),
    evaluation_digest char(64) NOT NULL CHECK(evaluation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(vm_id,vm_generation) REFERENCES kim.virtual_machines_current(vm_id,vm_generation),
    FOREIGN KEY(root_observation_evidence_id,root_attachment_id,root_attachment_generation)
      REFERENCES kim.volume_attachment_observation_evidence(evidence_id,attachment_id,attachment_generation),
    FOREIGN KEY(power_evidence_id,power_observation_generation)
      REFERENCES kim.vm_power_observation_evidence(evidence_id,observation_generation),
    UNIQUE(evaluation_id,evaluation_digest)
);

CREATE TABLE kim.source_root_safety_proof_evidence (
    proof_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evaluation_id text NOT NULL UNIQUE REFERENCES kim.source_root_safety_evaluation_evidence(evaluation_id),
    proof_type text NOT NULL CHECK(proof_type='LOCAL_LVM_SOURCE_ROOT_QUIESCED_NO_HOLDER'),
    proof_state text NOT NULL CHECK(proof_state='SAFE'),
    source_admission_id text NOT NULL,
    source_plan_id text NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_materialization_generation bigint NOT NULL,
    source_host_id text NOT NULL,
    root_volume_id text NOT NULL,
    root_binding_id text NOT NULL,
    root_binding_generation bigint NOT NULL,
    root_attachment_id text NOT NULL,
    root_attachment_generation bigint NOT NULL,
    power_evidence_id text NOT NULL,
    power_observation_generation bigint NOT NULL,
    root_observation_evidence_id text NOT NULL,
    root_observation_generation bigint NOT NULL,
    decided_by text NOT NULL CHECK(decided_by<>''),
    proof_digest char(64) NOT NULL CHECK(proof_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(proof_id,proof_digest),
    FOREIGN KEY(evaluation_id) REFERENCES kim.source_root_safety_evaluation_evidence(evaluation_id)
);

CREATE TABLE kim.storage_safety_root_input_evidence (
    evaluation_id text PRIMARY KEY REFERENCES kim.storage_safety_evaluation_evidence(evaluation_id),
    volume_role text NOT NULL CHECK(volume_role='ROOT'),
    root_safety_proof_id text NOT NULL,
    root_safety_proof_digest char(64) NOT NULL,
    root_volume_id text NOT NULL,
    root_binding_id text NOT NULL,
    root_binding_generation bigint NOT NULL,
    root_attachment_id text NOT NULL,
    root_attachment_generation bigint NOT NULL,
    source_materialization_generation bigint NOT NULL,
    FOREIGN KEY(root_safety_proof_id,root_safety_proof_digest)
      REFERENCES kim.source_root_safety_proof_evidence(proof_id,proof_digest)
);

DO $$ DECLARE c record;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint
           WHERE conrelid='kim.storage_safety_proof_evidence'::regclass
             AND contype='c' AND pg_get_constraintdef(oid) LIKE '%proof_type%'
  LOOP
    EXECUTE format('ALTER TABLE kim.storage_safety_proof_evidence DROP CONSTRAINT %I',c.conname);
  END LOOP;
END $$;
ALTER TABLE kim.storage_safety_proof_evidence
  ADD CONSTRAINT storage_safety_proof_closed_type_check
  CHECK(proof_type IN ('LOCAL_LVM_SOURCE_DETACHED_NO_HOLDER','LOCAL_LVM_SOURCE_ROOT_QUIESCED_DATA_DETACHED'));

CREATE TABLE kim.source_materialization_retirement_decision_evidence (
    retirement_decision_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    source_admission_id text NOT NULL,
    source_plan_id text NOT NULL,
    source_plan_digest char(64) NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_materialization_generation bigint NOT NULL,
    source_host_id text NOT NULL,
    fencing_proof_id text NOT NULL,
    fencing_proof_digest char(64) NOT NULL,
    root_safety_proof_id text NOT NULL,
    root_safety_proof_digest char(64) NOT NULL,
    decision_state text NOT NULL CHECK(decision_state='RETIRED'),
    decided_by text NOT NULL CHECK(decided_by<>''),
    decision_digest char(64) NOT NULL CHECK(decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(vm_id,source_materialization_generation),
    UNIQUE(retirement_decision_id,decision_digest),
    FOREIGN KEY(fencing_proof_id,fencing_proof_digest)
      REFERENCES kim.failure_fencing_proof_evidence(proof_id,proof_digest),
    FOREIGN KEY(root_safety_proof_id,root_safety_proof_digest)
      REFERENCES kim.source_root_safety_proof_evidence(proof_id,proof_digest)
);

CREATE TABLE kim.source_materialization_retirements_current (
    vm_id uuid NOT NULL,
    source_materialization_generation bigint NOT NULL,
    retirement_decision_id text NOT NULL,
    retirement_state text NOT NULL CHECK(retirement_state='RETIRED'),
    decision_digest char(64) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(vm_id,source_materialization_generation),
    FOREIGN KEY(retirement_decision_id,decision_digest)
      REFERENCES kim.source_materialization_retirement_decision_evidence(retirement_decision_id,decision_digest)
);

CREATE TRIGGER source_root_safety_evaluation_evidence_no_update BEFORE UPDATE ON kim.source_root_safety_evaluation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER source_root_safety_proof_evidence_no_update BEFORE UPDATE ON kim.source_root_safety_proof_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER storage_safety_root_input_evidence_no_update BEFORE UPDATE ON kim.storage_safety_root_input_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER source_materialization_retirement_decision_evidence_no_update BEFORE UPDATE ON kim.source_materialization_retirement_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.source_root_safety_proof_evidence IS 'Positive exact source boot-root quiescence proof. Configured inactive vda is allowed; QEMU holder, wrong LV, non-SHUTOFF power, UNKNOWN, or stale identity is not. This is not generic root detach or compute fencing.';
COMMENT ON TABLE kim.source_materialization_retirement_decision_evidence IS 'Logical retirement of one exact fenced source materialization incarnation. It grants no delete, detach, undefine, or backend cleanup authority.';
