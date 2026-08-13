-- Planned Host evacuation is an authority distinct from failure Recovery.
-- No table in this aggregate references Failure Epoch, Confirmation, Fencing,
-- or Recovery Budget authority.

CREATE TABLE kim.host_placement_drain_evidence (
    drain_id text PRIMARY KEY,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    drain_generation bigint NOT NULL CHECK(drain_generation>0),
    source_host_authority_generation bigint NOT NULL CHECK(source_host_authority_generation>0),
    placement_pool_id text NOT NULL REFERENCES kim.placement_pools_current(pool_id),
    placement_pool_generation bigint NOT NULL CHECK(placement_pool_generation>0),
    membership_generation bigint NOT NULL CHECK(membership_generation>0),
    drain_policy_id text NOT NULL CHECK(drain_policy_id<>''),
    drain_policy_revision bigint NOT NULL CHECK(drain_policy_revision>0),
    drain_state text NOT NULL CHECK(drain_state IN ('DRAINING','DRAINED','RELEASED')),
    reason text NOT NULL CHECK(reason<>''),
    actor text NOT NULL CHECK(actor<>''),
    drain_digest char(64) NOT NULL CHECK(drain_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(source_host_id,drain_generation),
    UNIQUE(drain_id,drain_digest)
);

CREATE TABLE kim.host_placement_drains_current (
    source_host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    drain_id text NOT NULL,
    drain_generation bigint NOT NULL CHECK(drain_generation>0),
    drain_state text NOT NULL CHECK(drain_state IN ('DRAINING','DRAINED')),
    drain_digest char(64) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(drain_id,drain_digest) REFERENCES kim.host_placement_drain_evidence(drain_id,drain_digest)
);

CREATE TABLE kim.host_placement_drain_transition_evidence (
    drain_id text NOT NULL REFERENCES kim.host_placement_drain_evidence(drain_id),
    transition_generation bigint NOT NULL CHECK(transition_generation>0),
    from_state text NOT NULL CHECK(from_state IN ('DRAINING','DRAINED')),
    to_state text NOT NULL CHECK(to_state IN ('DRAINED','RELEASED')),
    cause_type text NOT NULL CHECK(cause_type IN ('EVACUATION_TERMINAL','EXPLICIT_UNDRAIN')),
    cause_id text NOT NULL CHECK(cause_id<>''),
    transition_digest char(64) NOT NULL CHECK(transition_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(drain_id,transition_generation)
);

CREATE TABLE kim.host_evacuation_operation_evidence (
    evacuation_operation_id text NOT NULL,
    evacuation_generation bigint NOT NULL CHECK(evacuation_generation>0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_host_authority_generation bigint NOT NULL CHECK(source_host_authority_generation>0),
    placement_pool_id text NOT NULL,
    placement_pool_generation bigint NOT NULL CHECK(placement_pool_generation>0),
    membership_generation bigint NOT NULL CHECK(membership_generation>0),
    drain_id text NOT NULL REFERENCES kim.host_placement_drain_evidence(drain_id),
    drain_policy_id text NOT NULL CHECK(drain_policy_id<>''),
    drain_policy_revision bigint NOT NULL CHECK(drain_policy_revision>0),
    workload_set_id text NOT NULL,
    workload_set_generation bigint NOT NULL CHECK(workload_set_generation>0),
    workload_set_digest char(64) NOT NULL CHECK(workload_set_digest ~ '^[0-9a-f]{64}$'),
    maximum_concurrent_workloads integer NOT NULL CHECK(maximum_concurrent_workloads>0),
    evacuation_policy_revision bigint NOT NULL CHECK(evacuation_policy_revision>0),
    reason text NOT NULL CHECK(reason<>''),
    requested_by text NOT NULL CHECK(requested_by<>''),
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    operation_digest char(64) NOT NULL CHECK(operation_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(evacuation_operation_id,evacuation_generation),
    UNIQUE(evacuation_operation_id,evacuation_generation,operation_digest),
    UNIQUE(source_host_id,drain_id),
    UNIQUE(workload_set_id,workload_set_generation)
);

CREATE TABLE kim.host_evacuation_workload_set_evidence (
    workload_set_id text NOT NULL,
    workload_set_generation bigint NOT NULL CHECK(workload_set_generation>0),
    evacuation_operation_id text NOT NULL,
    evacuation_generation bigint NOT NULL,
    workload_count integer NOT NULL CHECK(workload_count>=0),
    workload_set_digest char(64) NOT NULL CHECK(workload_set_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(workload_set_id,workload_set_generation),
    UNIQUE(workload_set_id,workload_set_generation,workload_set_digest),
    FOREIGN KEY(evacuation_operation_id,evacuation_generation)
      REFERENCES kim.host_evacuation_operation_evidence(evacuation_operation_id,evacuation_generation)
      DEFERRABLE INITIALLY DEFERRED
);

ALTER TABLE kim.host_evacuation_operation_evidence
  ADD CONSTRAINT host_evacuation_operation_workload_set_fk
  FOREIGN KEY(workload_set_id,workload_set_generation,workload_set_digest)
  REFERENCES kim.host_evacuation_workload_set_evidence(workload_set_id,workload_set_generation,workload_set_digest)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE kim.host_evacuation_workload_evidence (
    workload_set_id text NOT NULL,
    workload_set_generation bigint NOT NULL,
    child_operation_id text NOT NULL UNIQUE,
    child_generation bigint NOT NULL CHECK(child_generation>0),
    ordinal integer NOT NULL CHECK(ordinal>0),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    workload_id text NOT NULL CHECK(workload_id<>''),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_plan_digest char(64) NOT NULL CHECK(source_plan_digest ~ '^[0-9a-f]{64}$'),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    availability_binding_revision bigint,
    availability_binding_digest char(64),
    network_requirements jsonb NOT NULL CHECK(jsonb_typeof(network_requirements)='array'),
    storage_requirements jsonb NOT NULL CHECK(jsonb_typeof(storage_requirements)='array'),
    pci_requirements jsonb NOT NULL CHECK(jsonb_typeof(pci_requirements)='array'),
    admission_provenance_digest char(64) NOT NULL CHECK(admission_provenance_digest ~ '^[0-9a-f]{64}$'),
    snapshot_digest char(64) NOT NULL CHECK(snapshot_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(workload_set_id,workload_set_generation,vm_id,vm_generation),
    UNIQUE(workload_set_id,workload_set_generation,ordinal),
    UNIQUE(child_operation_id,child_generation),
    UNIQUE(child_operation_id,child_generation,snapshot_digest),
    FOREIGN KEY(workload_set_id,workload_set_generation)
      REFERENCES kim.host_evacuation_workload_set_evidence(workload_set_id,workload_set_generation),
    CHECK((availability_binding_revision IS NULL AND availability_binding_digest IS NULL)
       OR (availability_binding_revision>0 AND availability_binding_digest ~ '^[0-9a-f]{64}$'))
);

CREATE TABLE kim.host_evacuation_operations_current (
    evacuation_operation_id text PRIMARY KEY,
    evacuation_generation bigint NOT NULL CHECK(evacuation_generation>0),
    lifecycle_state text NOT NULL CHECK(lifecycle_state IN ('DRAINING','RUNNING','PARTIAL','BLOCKED','VERIFIED','SOURCE_UNREACHABLE','CANCELLED')),
    state_generation bigint NOT NULL CHECK(state_generation>0),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(evacuation_operation_id,evacuation_generation)
      REFERENCES kim.host_evacuation_operation_evidence(evacuation_operation_id,evacuation_generation)
);

CREATE TABLE kim.host_evacuation_workloads_current (
    child_operation_id text PRIMARY KEY,
    child_generation bigint NOT NULL CHECK(child_generation>0),
    evacuation_operation_id text NOT NULL REFERENCES kim.host_evacuation_operations_current(evacuation_operation_id),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    phase text NOT NULL CHECK(phase IN ('DISCOVERED','ELIGIBILITY_PENDING','READY_TO_QUIESCE','QUIESCING_SOURCE','SOURCE_QUIESCED','SOURCE_RETIRING','DESTINATION_ADMITTING','DESTINATION_MATERIALIZING','POWERING','VERIFYING','VERIFIED','BLOCKED','CONFLICTING','STALE','SKIPPED_NOT_CURRENT','CANCELLED','RECOVERY_REQUIRED')),
    state_generation bigint NOT NULL CHECK(state_generation>0),
    result_state text NOT NULL CHECK(result_state IN ('PENDING','ELIGIBLE','RUNNING','VERIFIED','BLOCKED','CONFLICTING','STALE','SKIPPED_NOT_CURRENT','CANCELLED','RECOVERY_REQUIRED')),
    reason_code text NOT NULL CHECK(reason_code<>''),
    destination_host_id text REFERENCES kim.host_identities(host_id),
    destination_admission_id text REFERENCES kim.placement_admission_decisions(admission_id),
    child_plan_generation bigint CHECK(child_plan_generation IS NULL OR child_plan_generation>0),
    last_claim_generation bigint NOT NULL DEFAULT 0 CHECK(last_claim_generation>=0),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(evacuation_operation_id,vm_id,vm_generation)
);

-- Source/destination inequality is revalidated by the transaction API and by
-- immutable terminal evidence, because source identity lives in the snapshot.

CREATE TABLE kim.host_evacuation_child_transition_evidence (
    child_operation_id text NOT NULL REFERENCES kim.host_evacuation_workloads_current(child_operation_id),
    child_generation bigint NOT NULL CHECK(child_generation>0),
    state_generation bigint NOT NULL CHECK(state_generation>0),
    from_phase text,
    to_phase text NOT NULL,
    result_state text NOT NULL,
    reason_code text NOT NULL CHECK(reason_code<>''),
    cause_type text NOT NULL CHECK(cause_type IN ('SNAPSHOT','ELIGIBILITY','SLOT_CLAIM','OBSERVATION','RECONCILIATION','CANCELLATION','TERMINAL')),
    cause_id text NOT NULL CHECK(cause_id<>''),
    transition_digest char(64) NOT NULL CHECK(transition_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(child_operation_id,state_generation)
);

CREATE TABLE kim.host_evacuation_slot_claim_evidence (
    evacuation_operation_id text NOT NULL REFERENCES kim.host_evacuation_operations_current(evacuation_operation_id),
    child_operation_id text NOT NULL REFERENCES kim.host_evacuation_workloads_current(child_operation_id),
    claim_generation bigint NOT NULL CHECK(claim_generation>0),
    claim_owner text NOT NULL CHECK(claim_owner<>''),
    claim_state text NOT NULL CHECK(claim_state IN ('CLAIMED','UNKNOWN','RELEASED')),
    lease_expires_at timestamptz NOT NULL,
    claim_digest char(64) NOT NULL CHECK(claim_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(evacuation_operation_id,child_operation_id,claim_generation),
    UNIQUE(evacuation_operation_id,child_operation_id,claim_generation,claim_digest)
);

CREATE TABLE kim.host_evacuation_slot_claims_current (
    evacuation_operation_id text NOT NULL,
    child_operation_id text NOT NULL,
    claim_generation bigint NOT NULL,
    claim_owner text NOT NULL,
    claim_state text NOT NULL CHECK(claim_state IN ('CLAIMED','UNKNOWN')),
    lease_expires_at timestamptz NOT NULL,
    claim_digest char(64) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(evacuation_operation_id,child_operation_id),
    FOREIGN KEY(evacuation_operation_id,child_operation_id,claim_generation,claim_digest)
      REFERENCES kim.host_evacuation_slot_claim_evidence(evacuation_operation_id,child_operation_id,claim_generation,claim_digest)
);

CREATE TABLE kim.host_evacuation_slot_transition_evidence (
    evacuation_operation_id text NOT NULL,
    child_operation_id text NOT NULL,
    claim_generation bigint NOT NULL,
    transition_generation bigint NOT NULL CHECK(transition_generation>0),
    from_state text NOT NULL CHECK(from_state IN ('CLAIMED','UNKNOWN')),
    to_state text NOT NULL CHECK(to_state IN ('UNKNOWN','RELEASED','RECLAIMED')),
    reason_code text NOT NULL CHECK(reason_code<>''),
    claim_digest char(64) NOT NULL,
    transition_digest char(64) NOT NULL CHECK(transition_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(evacuation_operation_id,child_operation_id,claim_generation,transition_generation),
    FOREIGN KEY(evacuation_operation_id,child_operation_id,claim_generation,claim_digest)
      REFERENCES kim.host_evacuation_slot_claim_evidence(evacuation_operation_id,child_operation_id,claim_generation,claim_digest)
);

CREATE TABLE kim.planned_source_quiescence_evidence (
    quiescence_evidence_id text PRIMARY KEY,
    child_operation_id text NOT NULL REFERENCES kim.host_evacuation_workloads_current(child_operation_id),
    child_generation bigint NOT NULL CHECK(child_generation>0),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_host_authority_generation bigint NOT NULL CHECK(source_host_authority_generation>0),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_plan_digest char(64) NOT NULL CHECK(source_plan_digest ~ '^[0-9a-f]{64}$'),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    shutdown_command_id text NOT NULL CHECK(shutdown_command_id<>''),
    shutdown_response_state text NOT NULL CHECK(shutdown_response_state IN ('RECEIVED','LOST')),
    read_back_evidence_id text NOT NULL CHECK(read_back_evidence_id<>''),
    read_back_observation_generation bigint NOT NULL CHECK(read_back_observation_generation>0),
    observed_power_state text NOT NULL CHECK(observed_power_state='SHUTOFF'),
    identity_matches boolean NOT NULL CHECK(identity_matches),
    observation_digest char(64) NOT NULL CHECK(observation_digest ~ '^[0-9a-f]{64}$'),
    quiescence_digest char(64) NOT NULL CHECK(quiescence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    UNIQUE(quiescence_evidence_id,quiescence_digest)
);

CREATE TABLE kim.host_evacuation_child_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    child_plan_generation bigint NOT NULL CHECK(child_plan_generation>0),
    quiescence_evidence_id text NOT NULL,
    quiescence_digest char(64) NOT NULL,
    source_storage_state text NOT NULL CHECK(source_storage_state IN ('SAFE','NOT_REQUIRED')),
    source_network_state text NOT NULL CHECK(source_network_state IN ('RETIRED','NOT_REQUIRED')),
    source_pci_state text NOT NULL CHECK(source_pci_state IN ('RETIRED','NOT_REQUIRED')),
    destination_power_state text NOT NULL CHECK(destination_power_state='RUNNING'),
    destination_storage_state text NOT NULL CHECK(destination_storage_state IN ('CURRENT','NOT_REQUIRED')),
    destination_network_state text NOT NULL CHECK(destination_network_state IN ('CURRENT','NOT_REQUIRED')),
    destination_pci_state text NOT NULL CHECK(destination_pci_state IN ('CURRENT','NOT_REQUIRED')),
    source_ownership_state text NOT NULL CHECK(source_ownership_state='RETIRED'),
    verification_evidence_digest char(64) NOT NULL CHECK(verification_evidence_digest ~ '^[0-9a-f]{64}$'),
    terminal_state text NOT NULL CHECK(terminal_state='VERIFIED'),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation),
    UNIQUE(terminal_evidence_id,terminal_digest),
    FOREIGN KEY(child_operation_id,child_generation)
      REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation),
    FOREIGN KEY(quiescence_evidence_id,quiescence_digest)
      REFERENCES kim.planned_source_quiescence_evidence(quiescence_evidence_id,quiescence_digest),
    CHECK(source_host_id<>destination_host_id)
);

CREATE TABLE kim.host_evacuation_terminal_evidence (
    terminal_evidence_id text PRIMARY KEY,
    evacuation_operation_id text NOT NULL,
    evacuation_generation bigint NOT NULL,
    workload_set_digest char(64) NOT NULL,
    drain_id text NOT NULL REFERENCES kim.host_placement_drain_evidence(drain_id),
    workload_count integer NOT NULL CHECK(workload_count>=0),
    verified_count integer NOT NULL CHECK(verified_count>=0),
    active_source_workload_count integer NOT NULL CHECK(active_source_workload_count=0),
    post_drain_admission_count integer NOT NULL CHECK(post_drain_admission_count=0),
    terminal_state text NOT NULL CHECK(terminal_state='VERIFIED'),
    decision_digest char(64) NOT NULL CHECK(decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(evacuation_operation_id,evacuation_generation),
    UNIQUE(terminal_evidence_id,decision_digest),
    FOREIGN KEY(evacuation_operation_id,evacuation_generation)
      REFERENCES kim.host_evacuation_operation_evidence(evacuation_operation_id,evacuation_generation),
    CHECK(workload_count=verified_count)
);

ALTER TABLE kim.host_evacuation_operations_current
  ADD CONSTRAINT host_evacuation_current_terminal_fk
  FOREIGN KEY(terminal_evidence_id) REFERENCES kim.host_evacuation_terminal_evidence(terminal_evidence_id);
ALTER TABLE kim.host_evacuation_workloads_current
  ADD CONSTRAINT host_evacuation_child_current_terminal_fk
  FOREIGN KEY(terminal_evidence_id) REFERENCES kim.host_evacuation_child_terminal_evidence(terminal_evidence_id);

CREATE TABLE kim.host_evacuation_event_evidence (
    evacuation_operation_id text NOT NULL,
    evacuation_generation bigint NOT NULL,
    event_sequence bigint NOT NULL CHECK(event_sequence>0),
    event_type text NOT NULL CHECK(event_type IN ('STARTED','RESUMED','PARTIAL','SOURCE_UNREACHABLE','VERIFIED')),
    event_payload jsonb NOT NULL CHECK(jsonb_typeof(event_payload)='object'),
    event_digest char(64) NOT NULL CHECK(event_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(evacuation_operation_id,evacuation_generation,event_sequence),
    FOREIGN KEY(evacuation_operation_id,evacuation_generation)
      REFERENCES kim.host_evacuation_operation_evidence(evacuation_operation_id,evacuation_generation)
);

CREATE TRIGGER host_placement_drain_evidence_no_update BEFORE UPDATE ON kim.host_placement_drain_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_placement_drain_transition_evidence_no_update BEFORE UPDATE ON kim.host_placement_drain_transition_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_operation_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_workload_set_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_workload_set_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_workload_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_workload_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_child_transition_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_child_transition_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_slot_claim_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_slot_claim_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_slot_transition_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_slot_transition_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER planned_source_quiescence_evidence_no_update BEFORE UPDATE ON kim.planned_source_quiescence_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_child_terminal_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_child_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_terminal_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER host_evacuation_event_evidence_no_update BEFORE UPDATE ON kim.host_evacuation_event_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_placement_drains_current IS 'Per-Host placement admission fence. DRAINING is reachable planned authority, never FENCED failure authority.';
COMMENT ON TABLE kim.host_evacuation_operation_evidence IS 'First-class planned multi-workload orchestration authority with no Failure Epoch or Fencing provenance.';
COMMENT ON TABLE kim.host_evacuation_workload_evidence IS 'Immutable PostgreSQL-derived impact set. Callers cannot supply arbitrary workload/backend targets.';
COMMENT ON TABLE kim.host_evacuation_slot_claim_evidence IS 'Blast-radius authority; expiry alone never proves backend side-effect absence.';
COMMENT ON TABLE kim.planned_source_quiescence_evidence IS 'Exact typed shutdown plus SHUTOFF read-back. It is not and cannot become Fencing Proof.';
COMMENT ON TABLE kim.host_evacuation_terminal_evidence IS 'All snapshotted workloads moved and source active count zero; backend cleanup is independent post-terminal hygiene.';
