-- Recovery Operation is the first mutation-authority boundary after an
-- Eligibility Decision.  A Request or Plan alone has no Placement, Budget,
-- Job, Command, Lease, or backend authority.
ALTER TABLE kim.recovery_budget_claims_current
  ADD COLUMN state_generation bigint NOT NULL DEFAULT 1 CHECK (state_generation > 0);

ALTER TABLE kim.recovery_eligibility_decision_evidence
  ADD CONSTRAINT recovery_eligibility_decision_identity_digest_unique UNIQUE(decision_id,decision_digest);

CREATE TABLE kim.recovery_operation_request_evidence (
    recovery_operation_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    eligibility_decision_id text NOT NULL UNIQUE REFERENCES kim.recovery_eligibility_decision_evidence(decision_id),
    eligibility_decision_digest char(64) NOT NULL CHECK (eligibility_decision_digest ~ '^[0-9a-f]{64}$'),
    recovery_budget_claim_id text NOT NULL UNIQUE REFERENCES kim.recovery_budget_claim_evidence(claim_id),
    recovery_budget_claim_generation bigint NOT NULL CHECK (recovery_budget_claim_generation > 0),
    recovery_budget_claim_digest char(64) NOT NULL CHECK (recovery_budget_claim_digest ~ '^[0-9a-f]{64}$'),
    requested_action text NOT NULL CHECK (requested_action IN ('RESTART_ON_OTHER_HOST','EVACUATE')),
    requested_by text NOT NULL CHECK (requested_by<>''),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(recovery_budget_claim_id,recovery_budget_claim_generation,recovery_budget_claim_digest)
      REFERENCES kim.recovery_budget_claim_evidence(claim_id,claim_generation,claim_digest)
);

CREATE TABLE kim.recovery_operation_evidence (
    recovery_operation_id text PRIMARY KEY REFERENCES kim.recovery_operation_request_evidence(recovery_operation_id),
    operation_generation bigint NOT NULL CHECK (operation_generation=1),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    eligibility_decision_id text NOT NULL UNIQUE,
    eligibility_decision_digest char(64) NOT NULL CHECK (eligibility_decision_digest ~ '^[0-9a-f]{64}$'),
    recovery_budget_claim_id text NOT NULL UNIQUE,
    recovery_budget_claim_generation bigint NOT NULL CHECK (recovery_budget_claim_generation > 0),
    recovery_budget_claim_digest char(64) NOT NULL CHECK (recovery_budget_claim_digest ~ '^[0-9a-f]{64}$'),
    availability_binding_revision bigint NOT NULL CHECK (availability_binding_revision > 0),
    availability_binding_digest char(64) NOT NULL CHECK (availability_binding_digest ~ '^[0-9a-f]{64}$'),
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL CHECK (availability_policy_revision > 0),
    availability_policy_digest char(64) NOT NULL CHECK (availability_policy_digest ~ '^[0-9a-f]{64}$'),
    recovery_action text NOT NULL CHECK (recovery_action IN ('RESTART_ON_OTHER_HOST','EVACUATE')),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    selected_destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    plan_id text NOT NULL UNIQUE,
    operation_digest char(64) NOT NULL CHECK (operation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(eligibility_decision_id,eligibility_decision_digest)
      REFERENCES kim.recovery_eligibility_decision_evidence(decision_id,decision_digest),
    FOREIGN KEY(recovery_budget_claim_id,recovery_budget_claim_generation,recovery_budget_claim_digest)
      REFERENCES kim.recovery_budget_claim_evidence(claim_id,claim_generation,claim_digest),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    CHECK (source_host_id<>selected_destination_host_id)
);

CREATE TABLE kim.recovery_plan_evidence (
    plan_id text PRIMARY KEY,
    recovery_operation_id text NOT NULL UNIQUE REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    plan_revision bigint NOT NULL CHECK (plan_revision=1),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    recovery_action text NOT NULL CHECK (recovery_action IN ('RESTART_ON_OTHER_HOST','EVACUATE')),
    destination_request jsonb NOT NULL CHECK (jsonb_typeof(destination_request)='object'),
    destination_request_digest char(64) NOT NULL CHECK (destination_request_digest ~ '^[0-9a-f]{64}$'),
    destination_candidate_digest char(64) NOT NULL CHECK (destination_candidate_digest ~ '^[0-9a-f]{64}$'),
    destination_snapshot_digest char(64) NOT NULL CHECK (destination_snapshot_digest ~ '^[0-9a-f]{64}$'),
    placement_scope_id text NOT NULL,
    placement_scope_generation bigint NOT NULL CHECK (placement_scope_generation > 0),
    placement_scope_digest char(64) NOT NULL CHECK (placement_scope_digest ~ '^[0-9a-f]{64}$'),
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL CHECK (availability_policy_revision > 0),
    availability_policy_digest char(64) NOT NULL CHECK (availability_policy_digest ~ '^[0-9a-f]{64}$'),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(placement_scope_id,placement_scope_generation,placement_scope_digest)
      REFERENCES kim.placement_scope_revision_evidence(placement_scope_id,scope_generation,scope_digest),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    CHECK (source_host_id<>destination_host_id)
);

CREATE TABLE kim.recovery_operations_current (
    recovery_operation_id text PRIMARY KEY REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    plan_id text NOT NULL REFERENCES kim.recovery_plan_evidence(plan_id),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('PLANNED','RUNNING','VERIFYING','VERIFIED','BLOCKED','UNKNOWN','FAILED')),
    state_generation bigint NOT NULL CHECK (state_generation > 0),
    destination_admission_id text REFERENCES kim.placement_admission_decisions(admission_id),
    execution_job_id text REFERENCES kim.execution_jobs(job_id),
    execution_command_id text REFERENCES kim.execution_commands(command_id),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.recovery_operation_transition_evidence (
    recovery_operation_id text NOT NULL REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    state_generation bigint NOT NULL CHECK (state_generation > 0),
    from_state text,
    to_state text NOT NULL CHECK (to_state IN ('PLANNED','RUNNING','VERIFYING','VERIFIED','BLOCKED','UNKNOWN','FAILED')),
    reason_code text NOT NULL CHECK (reason_code<>''),
    cause_type text NOT NULL CHECK (cause_type IN ('PLAN','START_AUTHORITY','EXECUTION_OBSERVATION','RECOVERY_VERIFICATION')),
    cause_id text NOT NULL CHECK (cause_id<>''),
    transition_digest char(64) NOT NULL CHECK (transition_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(recovery_operation_id,state_generation),
    CHECK ((state_generation=1 AND from_state IS NULL AND to_state='PLANNED') OR
           (state_generation>1 AND from_state IS NOT NULL))
);

CREATE TABLE kim.recovery_budget_claim_transition_evidence (
    claim_id text NOT NULL REFERENCES kim.recovery_budget_claim_evidence(claim_id),
    state_generation bigint NOT NULL CHECK (state_generation > 1),
    from_state text NOT NULL CHECK (from_state IN ('RESERVED','CONSUMED','FENCED')),
    to_state text NOT NULL CHECK (to_state IN ('CONSUMED','RELEASED','FENCED')),
    recovery_operation_id text NOT NULL REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    transition_digest char(64) NOT NULL CHECK (transition_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(claim_id,state_generation)
);

CREATE TABLE kim.recovery_source_compute_release_evidence (
    recovery_operation_id text PRIMARY KEY REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    allocation_id text NOT NULL UNIQUE REFERENCES kim.compute_allocation_claims(allocation_id),
    prior_claim_state text NOT NULL CHECK (prior_claim_state IN ('RESERVED','ALLOCATED')),
    release_digest char(64) NOT NULL CHECK (release_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.recovery_destination_admission_evidence (
    recovery_operation_id text PRIMARY KEY REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    plan_id text NOT NULL UNIQUE REFERENCES kim.recovery_plan_evidence(plan_id),
    admission_id text NOT NULL UNIQUE REFERENCES kim.placement_admission_decisions(admission_id),
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    eligibility_decision_id text NOT NULL REFERENCES kim.recovery_eligibility_decision_evidence(decision_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    admission_digest char(64) NOT NULL CHECK (admission_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.recovery_operation_execution_evidence (
    recovery_operation_id text PRIMARY KEY REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    job_id text NOT NULL UNIQUE REFERENCES kim.execution_jobs(job_id),
    command_id text NOT NULL UNIQUE REFERENCES kim.execution_commands(command_id),
    execution_step text NOT NULL CHECK (execution_step='DESTINATION_PREPARATION'),
    command_type text NOT NULL CHECK (command_type='HOST_AGENT_STATE_MARKER_ENSURE'),
    command_schema text NOT NULL CHECK (command_schema='kim.command.host-agent-state-marker/v1'),
    command_payload_digest char(64) NOT NULL CHECK (command_payload_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.recovery_dangerous_step_evaluation_evidence (
    evaluation_id text PRIMARY KEY,
    recovery_operation_id text NOT NULL REFERENCES kim.recovery_operation_evidence(recovery_operation_id),
    operation_state_generation bigint NOT NULL CHECK (operation_state_generation > 0),
    fencing_proof_id text NOT NULL,
    fencing_proof_digest char(64) NOT NULL CHECK (fencing_proof_digest ~ '^[0-9a-f]{64}$'),
    fencing_usability text NOT NULL CHECK (fencing_usability IN ('USABLE','MISSING','STALE','UNKNOWN')),
    storage_safety_proof_id text NOT NULL,
    storage_safety_proof_digest char(64) NOT NULL CHECK (storage_safety_proof_digest ~ '^[0-9a-f]{64}$'),
    storage_usability text NOT NULL CHECK (storage_usability IN ('USABLE','MISSING','STALE','UNKNOWN')),
    budget_claim_id text NOT NULL REFERENCES kim.recovery_budget_claim_evidence(claim_id),
    budget_state_generation bigint NOT NULL CHECK (budget_state_generation > 0),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    result_state text NOT NULL CHECK (result_state IN ('AUTHORIZED','BLOCKED_OPERATION','BLOCKED_FENCING','BLOCKED_STORAGE','BLOCKED_BUDGET','BLOCKED_DESTINATION')),
    reason_code text NOT NULL CHECK (reason_code<>''),
    evaluator_digest char(64) NOT NULL CHECK (evaluator_digest ~ '^[0-9a-f]{64}$'),
    evaluation_digest char(64) NOT NULL CHECK (evaluation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(fencing_proof_id,fencing_proof_digest) REFERENCES kim.failure_fencing_proof_evidence(proof_id,proof_digest),
    FOREIGN KEY(storage_safety_proof_id,storage_safety_proof_digest) REFERENCES kim.storage_safety_proof_evidence(proof_id,proof_digest)
);

CREATE TRIGGER recovery_operation_request_evidence_no_update BEFORE UPDATE ON kim.recovery_operation_request_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_operation_evidence_no_update BEFORE UPDATE ON kim.recovery_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_plan_evidence_no_update BEFORE UPDATE ON kim.recovery_plan_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_operation_transition_evidence_no_update BEFORE UPDATE ON kim.recovery_operation_transition_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_budget_claim_transition_evidence_no_update BEFORE UPDATE ON kim.recovery_budget_claim_transition_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_source_compute_release_evidence_no_update BEFORE UPDATE ON kim.recovery_source_compute_release_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_destination_admission_evidence_no_update BEFORE UPDATE ON kim.recovery_destination_admission_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_operation_execution_evidence_no_update BEFORE UPDATE ON kim.recovery_operation_execution_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_dangerous_step_evaluation_evidence_no_update BEFORE UPDATE ON kim.recovery_dangerous_step_evaluation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.recovery_operation_request_evidence IS 'Explicit recovery intent only. A Request does not consume Budget, admit resources, dispatch, or mutate a backend.';
COMMENT ON TABLE kim.recovery_operation_evidence IS 'Immutable Recovery Operation identity and historical permission provenance. Operation authorization is not recovery success.';
COMMENT ON TABLE kim.recovery_plan_evidence IS 'One immutable Phase 1 destination Plan. The selected Host is never silently substituted.';
COMMENT ON TABLE kim.recovery_operation_execution_evidence IS 'Closed typed destination-preparation dispatch only; marker success is not VM recovery or Recovery VERIFIED.';
COMMENT ON TABLE kim.recovery_dangerous_step_evaluation_evidence IS 'Read-only current safety gate for a future destination power-on. AUTHORIZED evidence does not itself issue a Command.';
