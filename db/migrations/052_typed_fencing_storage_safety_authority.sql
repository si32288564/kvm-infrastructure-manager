-- Fencing and storage safety are independent positive proof authorities.
-- Neither proof creates Recovery Eligibility or a mutation.
ALTER TABLE kim.volume_attachment_claims
  ADD COLUMN claim_state_generation bigint NOT NULL DEFAULT 1 CHECK (claim_state_generation > 0);

CREATE FUNCTION kim.bump_volume_attachment_claim_state_generation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.claim_state IS DISTINCT FROM OLD.claim_state THEN
    NEW.claim_state_generation := OLD.claim_state_generation + 1;
  ELSIF NEW.claim_state_generation IS DISTINCT FROM OLD.claim_state_generation THEN
    RAISE EXCEPTION 'claim_state_generation is authority managed';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER volume_attachment_claim_state_generation
  BEFORE UPDATE ON kim.volume_attachment_claims
  FOR EACH ROW EXECUTE FUNCTION kim.bump_volume_attachment_claim_state_generation();

ALTER TABLE kim.host_operation_authority_events
  ADD CONSTRAINT host_operation_authority_events_digest_identity
  UNIQUE(host_id,authority_generation,event_sequence,event_payload_digest);

ALTER TABLE kim.vm_power_observation_evidence
  ADD CONSTRAINT vm_power_observation_evidence_generation_identity
  UNIQUE(evidence_id,observation_generation);

CREATE TABLE kim.failure_fencing_policy_revision_evidence (
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    fencing_mode text NOT NULL CHECK (fencing_mode='KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF'),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    created_by text NOT NULL CHECK (created_by <> ''),
    approved_by text NOT NULL CHECK (approved_by <> ''),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(policy_id,policy_revision),
    UNIQUE(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.failure_fencing_policies_current (
    policy_id text PRIMARY KEY,
    policy_revision bigint NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(policy_id,policy_revision,policy_digest)
      REFERENCES kim.failure_fencing_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.storage_safety_policy_revision_evidence (
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    storage_class text NOT NULL CHECK (storage_class='LOCAL_LVM'),
    safety_mode text NOT NULL CHECK (safety_mode='SOURCE_DETACHED_NO_HOLDER'),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    created_by text NOT NULL CHECK (created_by <> ''),
    approved_by text NOT NULL CHECK (approved_by <> ''),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(policy_id,policy_revision),
    UNIQUE(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.storage_safety_policies_current (
    policy_id text PRIMARY KEY,
    policy_revision bigint NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(policy_id,policy_revision,policy_digest)
      REFERENCES kim.storage_safety_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.availability_policy_fencing_binding_evidence (
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL,
    availability_policy_digest char(64) NOT NULL,
    fencing_policy_id text NOT NULL,
    fencing_policy_revision bigint NOT NULL,
    fencing_policy_digest char(64) NOT NULL,
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(availability_policy_id,availability_policy_revision),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(fencing_policy_id,fencing_policy_revision,fencing_policy_digest)
      REFERENCES kim.failure_fencing_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.availability_policy_storage_safety_binding_evidence (
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL,
    availability_policy_digest char(64) NOT NULL,
    storage_safety_policy_id text NOT NULL,
    storage_safety_policy_revision bigint NOT NULL,
    storage_safety_policy_digest char(64) NOT NULL,
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(availability_policy_id,availability_policy_revision),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest)
      REFERENCES kim.storage_safety_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.source_execution_fencing_observation_evidence (
    evidence_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evidence_generation bigint NOT NULL CHECK (evidence_generation > 0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    host_authority_generation bigint,
    host_authority_event_sequence bigint,
    host_authority_event_digest char(64),
    vm_power_evidence_id text REFERENCES kim.vm_power_observation_evidence(evidence_id),
    vm_power_observation_generation bigint,
    observation_state text NOT NULL CHECK (observation_state IN ('PROVEN','NOT_PROVEN','UNKNOWN','CONFLICTING')),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(failure_epoch_id,evidence_generation),
    UNIQUE(evidence_id,evidence_generation,evidence_digest),
    FOREIGN KEY(source_host_id,host_authority_generation,host_authority_event_sequence,host_authority_event_digest)
      REFERENCES kim.host_operation_authority_events(host_id,authority_generation,event_sequence,event_payload_digest),
    FOREIGN KEY(vm_power_evidence_id,vm_power_observation_generation)
      REFERENCES kim.vm_power_observation_evidence(evidence_id,observation_generation),
    CHECK (observation_state<>'PROVEN' OR (host_authority_generation IS NOT NULL AND host_authority_event_sequence IS NOT NULL AND host_authority_event_digest IS NOT NULL AND vm_power_evidence_id IS NOT NULL AND vm_power_observation_generation IS NOT NULL))
);

CREATE TABLE kim.source_execution_fencing_observations_current (
    failure_epoch_id text PRIMARY KEY REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evidence_generation bigint NOT NULL,
    evidence_id text NOT NULL,
    observation_state text NOT NULL,
    evidence_digest char(64) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(evidence_id,evidence_generation,evidence_digest)
      REFERENCES kim.source_execution_fencing_observation_evidence(evidence_id,evidence_generation,evidence_digest)
);

CREATE TABLE kim.failure_fencing_evaluation_evidence (
    evaluation_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evaluated_transition_generation bigint NOT NULL,
    evaluated_epoch_state text NOT NULL,
    availability_binding_revision bigint NOT NULL,
    availability_binding_digest char(64) NOT NULL,
    confirmation_decision_id text REFERENCES kim.failure_confirmation_decision_evidence(decision_id),
    fencing_policy_id text,
    fencing_policy_revision bigint,
    fencing_policy_digest char(64),
    latest_fencing_evidence_generation bigint NOT NULL DEFAULT 0,
    fencing_evidence_id text,
    fencing_evidence_digest char(64),
    result_state text NOT NULL CHECK (result_state IN ('PROVEN','NOT_PROVEN','UNKNOWN','CONFLICTING_INPUT','STALE_POLICY','STALE_EPOCH','NO_FENCING_POLICY')),
    reason_code text NOT NULL,
    evaluator_version text NOT NULL,
    evaluator_digest char(64) NOT NULL,
    evaluation_digest char(64) NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(fencing_policy_id,fencing_policy_revision,fencing_policy_digest)
      REFERENCES kim.failure_fencing_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(fencing_evidence_id,latest_fencing_evidence_generation,fencing_evidence_digest)
      REFERENCES kim.source_execution_fencing_observation_evidence(evidence_id,evidence_generation,evidence_digest)
);

CREATE TABLE kim.failure_fencing_proof_evidence (
    proof_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    expected_transition_generation bigint NOT NULL,
    evaluation_id text NOT NULL REFERENCES kim.failure_fencing_evaluation_evidence(evaluation_id),
    proof_type text NOT NULL CHECK (proof_type='KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF'),
    proof_state text NOT NULL CHECK (proof_state='PROVEN'),
    fencing_policy_id text NOT NULL,
    fencing_policy_revision bigint NOT NULL,
    fencing_policy_digest char(64) NOT NULL,
    fencing_evidence_id text NOT NULL,
    fencing_evidence_digest char(64) NOT NULL,
    decided_by text NOT NULL,
    proof_digest char(64) NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(fencing_policy_id,fencing_policy_revision,fencing_policy_digest)
      REFERENCES kim.failure_fencing_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.storage_safety_evaluation_evidence (
    evaluation_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evaluated_transition_generation bigint NOT NULL,
    evaluated_epoch_state text NOT NULL,
    availability_binding_revision bigint NOT NULL,
    availability_binding_digest char(64) NOT NULL,
    storage_safety_policy_id text,
    storage_safety_policy_revision bigint,
    storage_safety_policy_digest char(64),
    evidence_set_digest char(64) NOT NULL,
    result_state text NOT NULL CHECK (result_state IN ('SAFE','NOT_SAFE','UNKNOWN','CONFLICTING_INPUT','STALE_POLICY','STALE_EPOCH','NO_STORAGE_SAFETY_POLICY')),
    reason_code text NOT NULL,
    evaluator_version text NOT NULL,
    evaluator_digest char(64) NOT NULL,
    evaluation_digest char(64) NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest)
      REFERENCES kim.storage_safety_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.storage_safety_evaluation_input_evidence (
    evaluation_id text NOT NULL REFERENCES kim.storage_safety_evaluation_evidence(evaluation_id),
    input_ordinal integer NOT NULL CHECK (input_ordinal > 0),
    attachment_evidence_id text NOT NULL,
    attachment_id text NOT NULL,
    attachment_generation bigint NOT NULL,
    observation_generation bigint NOT NULL,
    observation_digest char(64) NOT NULL,
    attachment_claim_id text NOT NULL REFERENCES kim.volume_attachment_claims(attachment_claim_id),
    claim_state text NOT NULL CHECK (claim_state IN ('RESERVED','ACTIVE','UNKNOWN','FENCE_REQUIRED','RELEASE_PENDING','RELEASED')),
    claim_state_generation bigint NOT NULL CHECK (claim_state_generation > 0),
    binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    binding_observation_generation bigint NOT NULL CHECK (binding_observation_generation > 0),
    binding_state text NOT NULL CHECK (binding_state IN ('BOUND','STALE','UNKNOWN','REVOKED')),
    PRIMARY KEY(evaluation_id,input_ordinal),
    UNIQUE(evaluation_id,attachment_evidence_id),
    FOREIGN KEY(attachment_evidence_id,attachment_id,attachment_generation)
      REFERENCES kim.volume_attachment_observation_evidence(evidence_id,attachment_id,attachment_generation)
);

CREATE TABLE kim.storage_safety_proof_evidence (
    proof_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    expected_transition_generation bigint NOT NULL,
    evaluation_id text NOT NULL REFERENCES kim.storage_safety_evaluation_evidence(evaluation_id),
    proof_type text NOT NULL CHECK (proof_type='LOCAL_LVM_SOURCE_DETACHED_NO_HOLDER'),
    proof_state text NOT NULL CHECK (proof_state='SAFE'),
    storage_safety_policy_id text NOT NULL,
    storage_safety_policy_revision bigint NOT NULL,
    storage_safety_policy_digest char(64) NOT NULL,
    evidence_set_digest char(64) NOT NULL,
    decided_by text NOT NULL,
    proof_digest char(64) NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(storage_safety_policy_id,storage_safety_policy_revision,storage_safety_policy_digest)
      REFERENCES kim.storage_safety_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

ALTER TABLE kim.failure_epoch_transition_evidence
  ADD COLUMN fencing_proof_id text REFERENCES kim.failure_fencing_proof_evidence(proof_id);

DO $$ DECLARE c record;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint
           WHERE conrelid='kim.failure_epoch_transition_evidence'::regclass
             AND contype='c' AND pg_get_constraintdef(oid) LIKE '%confirmation_decision_id%'
  LOOP
    EXECUTE format('ALTER TABLE kim.failure_epoch_transition_evidence DROP CONSTRAINT %I',c.conname);
  END LOOP;
END $$;

ALTER TABLE kim.failure_epoch_transition_evidence ADD CONSTRAINT failure_epoch_transition_cause_authority_check
  CHECK ((to_state='SUSPECTED' AND cause_evidence_id IS NOT NULL AND confirmation_decision_id IS NULL AND fencing_proof_id IS NULL)
      OR (to_state='CONFIRMED' AND cause_evidence_id IS NULL AND confirmation_decision_id IS NOT NULL AND fencing_proof_id IS NULL)
      OR (to_state='FENCED' AND cause_evidence_id IS NULL AND confirmation_decision_id IS NULL AND fencing_proof_id IS NOT NULL)
      OR (to_state NOT IN ('SUSPECTED','CONFIRMED','FENCED') AND cause_evidence_id IS NOT NULL AND confirmation_decision_id IS NULL AND fencing_proof_id IS NULL));

CREATE TRIGGER failure_fencing_policy_revision_evidence_no_update BEFORE UPDATE ON kim.failure_fencing_policy_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER storage_safety_policy_revision_evidence_no_update BEFORE UPDATE ON kim.storage_safety_policy_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER availability_policy_fencing_binding_evidence_no_update BEFORE UPDATE ON kim.availability_policy_fencing_binding_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER availability_policy_storage_safety_binding_evidence_no_update BEFORE UPDATE ON kim.availability_policy_storage_safety_binding_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER source_execution_fencing_observation_evidence_no_update BEFORE UPDATE ON kim.source_execution_fencing_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_fencing_evaluation_evidence_no_update BEFORE UPDATE ON kim.failure_fencing_evaluation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_fencing_proof_evidence_no_update BEFORE UPDATE ON kim.failure_fencing_proof_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER storage_safety_evaluation_evidence_no_update BEFORE UPDATE ON kim.storage_safety_evaluation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER storage_safety_evaluation_input_evidence_no_update BEFORE UPDATE ON kim.storage_safety_evaluation_input_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER storage_safety_proof_evidence_no_update BEFORE UPDATE ON kim.storage_safety_proof_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.failure_fencing_proof_evidence IS 'Positive bounded source-execution fencing proof. It is not physical power-off, storage safety, or Recovery authority.';
COMMENT ON TABLE kim.storage_safety_proof_evidence IS 'Independent Local LVM detached/no-holder safety proof. It is not compute fencing or Recovery authority.';
