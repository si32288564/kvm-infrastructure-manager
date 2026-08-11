-- Typed failure confirmation is a positive decision authority. It is not a
-- fencing proof and cannot create Recovery Eligibility or a mutation.
CREATE TABLE kim.failure_confirmation_policy_revision_evidence (
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    applicable_failure_class text NOT NULL CHECK (applicable_failure_class IN ('HOST_CONNECTIVITY_LOSS','HOST_AUTHORITY_LOSS','VM_RUNTIME_UNAVAILABLE')),
    confirmation_mode text NOT NULL CHECK (confirmation_mode='ALL_REQUIRED_EVIDENCE'),
    require_distinct_sources boolean NOT NULL,
    requirements_digest char(64) NOT NULL CHECK (requirements_digest ~ '^[0-9a-f]{64}$'),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    created_by text NOT NULL CHECK (created_by <> ''),
    approved_by text NOT NULL CHECK (approved_by <> ''),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(policy_id,policy_revision),
    UNIQUE(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.failure_confirmation_policy_requirement_evidence (
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL,
    requirement_ordinal integer NOT NULL CHECK (requirement_ordinal > 0),
    evidence_type text NOT NULL CHECK (evidence_type IN ('AGENT_CONNECTIVITY_LOSS','HOST_OPERATION_AUTHORITY_STATE','VM_RUNTIME_OBSERVATION')),
    required_observed_state text NOT NULL CHECK (required_observed_state IN ('PRESENT','ABSENT')),
    required_freshness_state text NOT NULL CHECK (required_freshness_state='CURRENT'),
    required_source_type text NOT NULL CHECK (required_source_type IN ('CONTROL_PLANE','LIBVIRT_READ_BACK')),
    requirement_digest char(64) NOT NULL CHECK (requirement_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY(policy_id,policy_revision,requirement_ordinal),
    UNIQUE(policy_id,policy_revision,evidence_type,required_observed_state,required_freshness_state,required_source_type),
    FOREIGN KEY(policy_id,policy_revision)
      REFERENCES kim.failure_confirmation_policy_revision_evidence(policy_id,policy_revision)
);

CREATE TABLE kim.failure_confirmation_policies_current (
    policy_id text PRIMARY KEY,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(policy_id,policy_revision,policy_digest)
      REFERENCES kim.failure_confirmation_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.availability_policy_confirmation_binding_evidence (
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL,
    availability_policy_digest char(64) NOT NULL,
    confirmation_policy_id text NOT NULL,
    confirmation_policy_revision bigint NOT NULL,
    confirmation_policy_digest char(64) NOT NULL,
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(availability_policy_id,availability_policy_revision),
    UNIQUE(availability_policy_id,availability_policy_revision,availability_policy_digest,binding_digest),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(confirmation_policy_id,confirmation_policy_revision,confirmation_policy_digest)
      REFERENCES kim.failure_confirmation_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

ALTER TABLE kim.failure_observation_evidence
  ADD UNIQUE(evidence_id,evidence_generation,evidence_digest);
ALTER TABLE kim.failure_epoch_evidence
  ADD UNIQUE(failure_epoch_id,epoch_generation,availability_binding_revision,availability_binding_digest);

CREATE TABLE kim.failure_confirmation_evaluation_evidence (
    evaluation_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    failure_epoch_generation bigint NOT NULL CHECK (failure_epoch_generation=1),
    evaluated_transition_generation bigint NOT NULL CHECK (evaluated_transition_generation > 0),
    evaluated_epoch_state text NOT NULL,
    availability_binding_revision bigint NOT NULL,
    availability_binding_digest char(64) NOT NULL,
    confirmation_policy_id text,
    confirmation_policy_revision bigint,
    confirmation_policy_digest char(64),
    latest_evidence_generation bigint NOT NULL CHECK (latest_evidence_generation > 0),
    evidence_set_digest char(64) NOT NULL CHECK (evidence_set_digest ~ '^[0-9a-f]{64}$'),
    result_state text NOT NULL CHECK (result_state IN ('SATISFIED','NOT_SATISFIED','UNKNOWN','CONFLICTING_INPUT','STALE_EVIDENCE','STALE_POLICY','STALE_EPOCH','NO_CONFIRMATION_POLICY')),
    reason_code text NOT NULL CHECK (reason_code <> ''),
    evaluator_version text NOT NULL CHECK (evaluator_version <> ''),
    evaluator_digest char(64) NOT NULL CHECK (evaluator_digest ~ '^[0-9a-f]{64}$'),
    evaluation_digest char(64) NOT NULL CHECK (evaluation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(evaluation_id,evaluation_digest),
    FOREIGN KEY(failure_epoch_id,failure_epoch_generation,availability_binding_revision,availability_binding_digest)
      REFERENCES kim.failure_epoch_evidence(failure_epoch_id,epoch_generation,availability_binding_revision,availability_binding_digest),
    FOREIGN KEY(confirmation_policy_id,confirmation_policy_revision,confirmation_policy_digest)
      REFERENCES kim.failure_confirmation_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    CHECK ((result_state='NO_CONFIRMATION_POLICY' AND confirmation_policy_id IS NULL AND confirmation_policy_revision IS NULL AND confirmation_policy_digest IS NULL)
        OR (result_state<>'NO_CONFIRMATION_POLICY' AND confirmation_policy_id IS NOT NULL AND confirmation_policy_revision IS NOT NULL AND confirmation_policy_digest IS NOT NULL))
);

CREATE TABLE kim.failure_confirmation_evaluation_input_evidence (
    evaluation_id text NOT NULL REFERENCES kim.failure_confirmation_evaluation_evidence(evaluation_id),
    input_ordinal integer NOT NULL CHECK (input_ordinal > 0),
    evidence_id text NOT NULL,
    evidence_generation bigint NOT NULL,
    evidence_digest char(64) NOT NULL,
    evidence_type text NOT NULL,
    source_identity_digest char(64) NOT NULL CHECK (source_identity_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY(evaluation_id,input_ordinal),
    UNIQUE(evaluation_id,evidence_id),
    FOREIGN KEY(evidence_id,evidence_generation,evidence_digest)
      REFERENCES kim.failure_observation_evidence(evidence_id,evidence_generation,evidence_digest)
);

CREATE TABLE kim.failure_confirmation_decision_evidence (
    decision_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    expected_transition_generation bigint NOT NULL CHECK (expected_transition_generation > 0),
    evaluation_id text NOT NULL REFERENCES kim.failure_confirmation_evaluation_evidence(evaluation_id),
    confirmation_policy_id text NOT NULL,
    confirmation_policy_revision bigint NOT NULL,
    confirmation_policy_digest char(64) NOT NULL,
    evidence_set_digest char(64) NOT NULL,
    decision_state text NOT NULL CHECK (decision_state='ACCEPTED'),
    result_code text NOT NULL CHECK (result_code='CONFIRMED'),
    decided_by text NOT NULL CHECK (decided_by <> ''),
    decision_digest char(64) NOT NULL CHECK (decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(decision_id,decision_digest),
    FOREIGN KEY(confirmation_policy_id,confirmation_policy_revision,confirmation_policy_digest)
      REFERENCES kim.failure_confirmation_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

ALTER TABLE kim.failure_epoch_transition_evidence
  ALTER COLUMN cause_evidence_id DROP NOT NULL,
  ADD COLUMN confirmation_decision_id text REFERENCES kim.failure_confirmation_decision_evidence(decision_id),
  ADD CHECK ((to_state='CONFIRMED' AND cause_evidence_id IS NULL AND confirmation_decision_id IS NOT NULL)
          OR (to_state<>'CONFIRMED' AND cause_evidence_id IS NOT NULL AND confirmation_decision_id IS NULL));

CREATE TRIGGER failure_confirmation_policy_revision_evidence_no_update BEFORE UPDATE ON kim.failure_confirmation_policy_revision_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_confirmation_policy_requirement_evidence_no_update BEFORE UPDATE ON kim.failure_confirmation_policy_requirement_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER availability_policy_confirmation_binding_evidence_no_update BEFORE UPDATE ON kim.availability_policy_confirmation_binding_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_confirmation_evaluation_evidence_no_update BEFORE UPDATE ON kim.failure_confirmation_evaluation_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_confirmation_evaluation_input_evidence_no_update BEFORE UPDATE ON kim.failure_confirmation_evaluation_input_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_confirmation_decision_evidence_no_update BEFORE UPDATE ON kim.failure_confirmation_decision_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.failure_confirmation_policy_revision_evidence IS
'Immutable closed typed confirmation rule. It is neither Availability responsibility nor fencing/recovery authority.';
COMMENT ON TABLE kim.failure_confirmation_evaluation_evidence IS
'Immutable pure evaluation of one exact Epoch, Policy revision, and evidence snapshot; SATISFIED alone does not confirm an Epoch.';
COMMENT ON TABLE kim.failure_confirmation_decision_evidence IS
'Immutable positive confirmation authority committed atomically with SUSPECTED-to-CONFIRMED transition; it is not fencing or recovery authority.';
