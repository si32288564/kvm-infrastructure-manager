-- Recovery Eligibility is a permission authority. It does not create a
-- Recovery Operation, Placement Admission, resource claim, Job, or Command.
ALTER TABLE kim.failure_fencing_proof_evidence
  ADD CONSTRAINT failure_fencing_proof_identity_digest_unique UNIQUE(proof_id,proof_digest);

ALTER TABLE kim.storage_safety_proof_evidence
  ADD CONSTRAINT storage_safety_proof_identity_digest_unique UNIQUE(proof_id,proof_digest);

CREATE TABLE kim.recovery_budget_policy_revision_evidence (
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    scope_type text NOT NULL CHECK (scope_type='GLOBAL'),
    phase text NOT NULL CHECK (phase='PLANNING'),
    max_active_recoveries integer NOT NULL CHECK (max_active_recoveries > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    created_by text NOT NULL CHECK (created_by<>''),
    approved_by text NOT NULL CHECK (approved_by<>''),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(policy_id,policy_revision),
    UNIQUE(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.recovery_budget_policies_current (
    policy_id text PRIMARY KEY,
    policy_revision bigint NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(policy_id,policy_revision,policy_digest)
      REFERENCES kim.recovery_budget_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.availability_policy_recovery_budget_binding_evidence (
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL,
    availability_policy_digest char(64) NOT NULL CHECK (availability_policy_digest ~ '^[0-9a-f]{64}$'),
    recovery_budget_policy_id text NOT NULL,
    recovery_budget_policy_revision bigint NOT NULL,
    recovery_budget_policy_digest char(64) NOT NULL CHECK (recovery_budget_policy_digest ~ '^[0-9a-f]{64}$'),
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(availability_policy_id,availability_policy_revision),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(recovery_budget_policy_id,recovery_budget_policy_revision,recovery_budget_policy_digest)
      REFERENCES kim.recovery_budget_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.recovery_eligibility_evaluation_evidence (
    evaluation_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evaluated_transition_generation bigint NOT NULL CHECK (evaluated_transition_generation > 0),
    evaluated_epoch_state text NOT NULL,
    availability_binding_revision bigint NOT NULL CHECK (availability_binding_revision > 0),
    availability_binding_digest char(64) NOT NULL CHECK (availability_binding_digest ~ '^[0-9a-f]{64}$'),
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL,
    availability_policy_digest char(64) NOT NULL CHECK (availability_policy_digest ~ '^[0-9a-f]{64}$'),
    responsibility text NOT NULL CHECK (responsibility IN ('INFRASTRUCTURE_MANAGED','WORKLOAD_MANAGED','MANUAL')),
    host_failure_action text NOT NULL CHECK (host_failure_action IN ('RESTART_ON_OTHER_HOST','EVACUATE','NO_AUTOMATIC_ACTION')),
    confirmation_decision_id text REFERENCES kim.failure_confirmation_decision_evidence(decision_id),
    fencing_proof_id text REFERENCES kim.failure_fencing_proof_evidence(proof_id),
    fencing_proof_digest char(64),
    fencing_usability text NOT NULL CHECK (fencing_usability IN ('USABLE','MISSING','STALE','UNKNOWN')),
    storage_safety_proof_id text REFERENCES kim.storage_safety_proof_evidence(proof_id),
    storage_safety_proof_digest char(64),
    storage_usability text NOT NULL CHECK (storage_usability IN ('USABLE','MISSING','STALE','UNKNOWN')),
    recovery_budget_policy_id text,
    recovery_budget_policy_revision bigint,
    recovery_budget_policy_digest char(64),
    budget_active_count integer NOT NULL DEFAULT 0 CHECK (budget_active_count >= 0),
    budget_max_active integer NOT NULL DEFAULT 0 CHECK (budget_max_active >= 0),
    destination_request jsonb NOT NULL CHECK (jsonb_typeof(destination_request)='object'),
    destination_request_digest char(64) NOT NULL CHECK (destination_request_digest ~ '^[0-9a-f]{64}$'),
    destination_snapshot_digest char(64) NOT NULL CHECK (destination_snapshot_digest ~ '^[0-9a-f]{64}$'),
    destination_candidate_count integer NOT NULL CHECK (destination_candidate_count >= 0),
    eligible_destination_count integer NOT NULL CHECK (eligible_destination_count >= 0),
    result_state text NOT NULL CHECK (result_state IN ('ELIGIBLE','EPOCH_NOT_CONFIRMED','FENCING_PROOF_MISSING','FENCING_PROOF_STALE','FENCING_PROOF_UNKNOWN','STORAGE_PROOF_MISSING','STORAGE_PROOF_STALE','STORAGE_PROOF_UNKNOWN','RESPONSIBILITY_BLOCKED','NO_AUTOMATIC_ACTION','NO_RECOVERY_BUDGET_POLICY','BUDGET_EXHAUSTED','NO_DESTINATION','DESTINATION_STALE','DESTINATION_CONFLICT','STALE_POLICY','STALE_EPOCH')),
    reason_code text NOT NULL CHECK (reason_code<>''),
    evaluator_version text NOT NULL CHECK (evaluator_version<>''),
    evaluator_digest char(64) NOT NULL CHECK (evaluator_digest ~ '^[0-9a-f]{64}$'),
    evaluation_digest char(64) NOT NULL CHECK (evaluation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(recovery_budget_policy_id,recovery_budget_policy_revision,recovery_budget_policy_digest)
      REFERENCES kim.recovery_budget_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.recovery_eligibility_destination_candidate_evidence (
    evaluation_id text NOT NULL REFERENCES kim.recovery_eligibility_evaluation_evidence(evaluation_id),
    candidate_ordinal integer NOT NULL CHECK (candidate_ordinal > 0),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    candidate_state text NOT NULL CHECK (candidate_state IN ('SOURCE_EXCLUDED','INELIGIBLE','POLICY_INCOMPATIBLE','ELIGIBLE')),
    reason_code text NOT NULL CHECK (reason_code<>''),
    placement_scope_id text NOT NULL,
    placement_scope_generation bigint NOT NULL CHECK (placement_scope_generation > 0),
    placement_scope_digest char(64) NOT NULL CHECK (placement_scope_digest ~ '^[0-9a-f]{64}$'),
    visibility_provenance_digest char(64) NOT NULL CHECK (visibility_provenance_digest ~ '^[0-9a-f]{64}$'),
    placement_evaluation_digest char(64) NOT NULL CHECK (placement_evaluation_digest ~ '^[0-9a-f]{64}$'),
    capability_generation bigint NOT NULL DEFAULT 0 CHECK (capability_generation >= 0),
    baseline_assignment_generation bigint NOT NULL DEFAULT 0 CHECK (baseline_assignment_generation >= 0),
    preflight_generation bigint NOT NULL DEFAULT 0 CHECK (preflight_generation >= 0),
    compliance_generation bigint NOT NULL DEFAULT 0 CHECK (compliance_generation >= 0),
    pool_id text NOT NULL,
    pool_generation bigint NOT NULL CHECK (pool_generation > 0),
    membership_set_generation bigint NOT NULL CHECK (membership_set_generation > 0),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    hierarchy_id text,
    hierarchy_generation bigint,
    pool_policy_id text NOT NULL,
    pool_policy_generation bigint NOT NULL CHECK (pool_policy_generation > 0),
    availability_policy_id text,
    availability_policy_revision bigint,
    availability_policy_digest char(64),
    candidate_digest char(64) NOT NULL CHECK (candidate_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY(evaluation_id,candidate_ordinal),
    UNIQUE(evaluation_id,host_id),
    CHECK ((hierarchy_id IS NULL) = (hierarchy_generation IS NULL)),
    FOREIGN KEY(placement_scope_id,placement_scope_generation,placement_scope_digest)
      REFERENCES kim.placement_scope_revision_evidence(placement_scope_id,scope_generation,scope_digest),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.recovery_eligibility_destination_visibility_evidence (
    evaluation_id text NOT NULL,
    candidate_ordinal integer NOT NULL,
    provenance_ordinal integer NOT NULL CHECK (provenance_ordinal > 0),
    host_id text NOT NULL,
    host_group_id text NOT NULL,
    host_group_generation bigint NOT NULL CHECK (host_group_generation > 0),
    membership_set_generation bigint NOT NULL CHECK (membership_set_generation > 0),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_evidence_digest char(64) NOT NULL CHECK (membership_evidence_digest ~ '^[0-9a-f]{64}$'),
    provenance_digest char(64) NOT NULL CHECK (provenance_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY(evaluation_id,candidate_ordinal,provenance_ordinal),
    UNIQUE(evaluation_id,candidate_ordinal,host_group_id),
    FOREIGN KEY(evaluation_id,candidate_ordinal)
      REFERENCES kim.recovery_eligibility_destination_candidate_evidence(evaluation_id,candidate_ordinal),
    FOREIGN KEY(host_group_id,membership_set_generation,host_id,membership_generation,membership_evidence_digest)
      REFERENCES kim.host_group_membership_set_member_evidence(host_group_id,membership_set_generation,host_id,membership_generation,membership_evidence_digest)
);

CREATE TABLE kim.recovery_eligibility_decision_evidence (
    decision_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evaluation_id text NOT NULL UNIQUE REFERENCES kim.recovery_eligibility_evaluation_evidence(evaluation_id),
    evaluation_digest char(64) NOT NULL CHECK (evaluation_digest ~ '^[0-9a-f]{64}$'),
    expected_transition_generation bigint NOT NULL CHECK (expected_transition_generation > 0),
    fencing_proof_id text NOT NULL REFERENCES kim.failure_fencing_proof_evidence(proof_id),
    fencing_proof_digest char(64) NOT NULL CHECK (fencing_proof_digest ~ '^[0-9a-f]{64}$'),
    storage_safety_proof_id text NOT NULL REFERENCES kim.storage_safety_proof_evidence(proof_id),
    storage_safety_proof_digest char(64) NOT NULL CHECK (storage_safety_proof_digest ~ '^[0-9a-f]{64}$'),
    recovery_budget_policy_id text NOT NULL,
    recovery_budget_policy_revision bigint NOT NULL,
    recovery_budget_policy_digest char(64) NOT NULL CHECK (recovery_budget_policy_digest ~ '^[0-9a-f]{64}$'),
    destination_snapshot_digest char(64) NOT NULL CHECK (destination_snapshot_digest ~ '^[0-9a-f]{64}$'),
    decision_state text NOT NULL CHECK (decision_state='ACCEPTED'),
    result_state text NOT NULL CHECK (result_state='ELIGIBLE'),
    decided_by text NOT NULL CHECK (decided_by<>''),
    decision_digest char(64) NOT NULL CHECK (decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(recovery_budget_policy_id,recovery_budget_policy_revision,recovery_budget_policy_digest)
      REFERENCES kim.recovery_budget_policy_revision_evidence(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(fencing_proof_id,fencing_proof_digest)
      REFERENCES kim.failure_fencing_proof_evidence(proof_id,proof_digest),
    FOREIGN KEY(storage_safety_proof_id,storage_safety_proof_digest)
      REFERENCES kim.storage_safety_proof_evidence(proof_id,proof_digest)
);

CREATE TABLE kim.recovery_budget_claim_evidence (
    claim_id text PRIMARY KEY,
    decision_id text NOT NULL UNIQUE REFERENCES kim.recovery_eligibility_decision_evidence(decision_id),
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    recovery_budget_policy_id text NOT NULL,
    recovery_budget_policy_revision bigint NOT NULL,
    recovery_budget_policy_digest char(64) NOT NULL CHECK (recovery_budget_policy_digest ~ '^[0-9a-f]{64}$'),
    scope_type text NOT NULL CHECK (scope_type='GLOBAL'),
    phase text NOT NULL CHECK (phase='PLANNING'),
    claim_generation bigint NOT NULL CHECK (claim_generation=1),
    claim_state text NOT NULL CHECK (claim_state='RESERVED'),
    claim_digest char(64) NOT NULL CHECK (claim_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(claim_id,claim_generation,claim_digest),
    FOREIGN KEY(recovery_budget_policy_id,recovery_budget_policy_revision,recovery_budget_policy_digest)
      REFERENCES kim.recovery_budget_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.recovery_budget_claims_current (
    claim_id text PRIMARY KEY REFERENCES kim.recovery_budget_claim_evidence(claim_id),
    decision_id text NOT NULL UNIQUE REFERENCES kim.recovery_eligibility_decision_evidence(decision_id),
    failure_epoch_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    recovery_budget_policy_id text NOT NULL,
    recovery_budget_policy_revision bigint NOT NULL,
    scope_type text NOT NULL CHECK (scope_type='GLOBAL'),
    phase text NOT NULL CHECK (phase='PLANNING'),
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    claim_state text NOT NULL CHECK (claim_state IN ('RESERVED','CONSUMED','RELEASED','FENCED')),
    claim_digest char(64) NOT NULL CHECK (claim_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(claim_id,claim_generation,claim_digest)
      REFERENCES kim.recovery_budget_claim_evidence(claim_id,claim_generation,claim_digest)
);

CREATE INDEX recovery_budget_claims_active
  ON kim.recovery_budget_claims_current(recovery_budget_policy_id,recovery_budget_policy_revision,scope_type,phase)
  WHERE claim_state IN ('RESERVED','CONSUMED');

CREATE TRIGGER recovery_budget_policy_revision_evidence_no_update BEFORE UPDATE ON kim.recovery_budget_policy_revision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER availability_policy_recovery_budget_binding_evidence_no_update BEFORE UPDATE ON kim.availability_policy_recovery_budget_binding_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_eligibility_evaluation_evidence_no_update BEFORE UPDATE ON kim.recovery_eligibility_evaluation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_eligibility_destination_candidate_evidence_no_update BEFORE UPDATE ON kim.recovery_eligibility_destination_candidate_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_eligibility_destination_visibility_evidence_no_update BEFORE UPDATE ON kim.recovery_eligibility_destination_visibility_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_eligibility_decision_evidence_no_update BEFORE UPDATE ON kim.recovery_eligibility_decision_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER recovery_budget_claim_evidence_no_update BEFORE UPDATE ON kim.recovery_budget_claim_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.recovery_eligibility_decision_evidence IS 'Immutable permission authority only. ELIGIBLE is not a Recovery Operation, Placement Admission, resource reservation, Job, Command, Lease, restart, or evacuation.';
COMMENT ON TABLE kim.recovery_budget_claim_evidence IS 'Durable GLOBAL/PLANNING budget admission committed atomically with Eligibility. It is not dispatch or mutation authority.';
