-- Failure observations and epochs are incident evidence only. They are not
-- confirmation, fencing, Recovery Eligibility, or mutation authority.
CREATE TABLE kim.failure_epoch_open_request_evidence (
    open_request_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL,
    incident_key text NOT NULL CHECK (incident_key <> ''),
    workload_id text NOT NULL,
    subject_type text NOT NULL CHECK (subject_type='VIRTUAL_MACHINE'),
    failure_class text NOT NULL CHECK (failure_class IN ('HOST_CONNECTIVITY_LOSS','HOST_AUTHORITY_LOSS','VM_RUNTIME_UNAVAILABLE')),
    expected_binding_revision bigint NOT NULL CHECK (expected_binding_revision > 0),
    expected_binding_digest char(64) NOT NULL CHECK (expected_binding_digest ~ '^[0-9a-f]{64}$'),
    triggering_evidence_id text NOT NULL,
    requested_by text NOT NULL CHECK (requested_by <> ''),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(open_request_id,request_digest),
    FOREIGN KEY(workload_id,expected_binding_revision,expected_binding_digest)
      REFERENCES kim.vm_availability_binding_evidence(workload_id,binding_revision,binding_digest)
);

CREATE TABLE kim.failure_epoch_evidence (
    failure_epoch_id text PRIMARY KEY,
    open_request_id text NOT NULL UNIQUE REFERENCES kim.failure_epoch_open_request_evidence(open_request_id),
    incident_key text NOT NULL,
    epoch_generation bigint NOT NULL CHECK (epoch_generation=1),
    subject_type text NOT NULL CHECK (subject_type='VIRTUAL_MACHINE'),
    workload_id text NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    failure_class text NOT NULL CHECK (failure_class IN ('HOST_CONNECTIVITY_LOSS','HOST_AUTHORITY_LOSS','VM_RUNTIME_UNAVAILABLE')),
    availability_binding_revision bigint NOT NULL CHECK (availability_binding_revision > 0),
    availability_binding_digest char(64) NOT NULL CHECK (availability_binding_digest ~ '^[0-9a-f]{64}$'),
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL CHECK (availability_policy_revision > 0),
    availability_policy_digest char(64) NOT NULL CHECK (availability_policy_digest ~ '^[0-9a-f]{64}$'),
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    allocation_id text NOT NULL REFERENCES kim.compute_allocation_claims(allocation_id),
    source_host_authority_generation bigint,
    source_host_authority_state text CHECK (source_host_authority_state IN ('ARMED','DISARMED','FENCED','UNKNOWN')),
    source_session_generation bigint,
    triggering_evidence_id text NOT NULL,
    epoch_digest char(64) NOT NULL CHECK (epoch_digest ~ '^[0-9a-f]{64}$'),
    opened_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(workload_id,failure_class,incident_key),
    UNIQUE(workload_id,failure_epoch_id,epoch_generation,epoch_digest),
    UNIQUE(failure_epoch_id,epoch_generation,workload_id,failure_class),
    FOREIGN KEY(workload_id,availability_binding_revision,availability_binding_digest)
      REFERENCES kim.vm_availability_binding_evidence(workload_id,binding_revision,binding_digest),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.failure_observation_evidence (
    evidence_id text PRIMARY KEY,
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    evidence_generation bigint NOT NULL CHECK (evidence_generation > 0),
    evidence_type text NOT NULL CHECK (evidence_type IN ('AGENT_CONNECTIVITY_LOSS','HOST_OPERATION_AUTHORITY_STATE','VM_RUNTIME_OBSERVATION')),
    source_type text NOT NULL CHECK (source_type IN ('CONTROL_PLANE','LIBVIRT_READ_BACK')),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_session_generation bigint,
    source_credential_binding_revision bigint,
    source_host_authority_generation bigint,
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    observed_state text NOT NULL CHECK (observed_state IN ('PRESENT','ABSENT','UNKNOWN','CONFLICTING')),
    freshness_state text NOT NULL CHECK (freshness_state IN ('CURRENT','STALE','UNKNOWN')),
    observed_at timestamptz NOT NULL,
    payload_digest char(64) NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(failure_epoch_id,evidence_generation),
    UNIQUE(evidence_id,evidence_digest),
    FOREIGN KEY(source_host_id,source_credential_binding_revision)
      REFERENCES kim.agent_credential_binding_evidence(host_id,binding_revision),
    CHECK ((evidence_type='AGENT_CONNECTIVITY_LOSS' AND source_type='CONTROL_PLANE'
              AND source_session_generation IS NOT NULL AND source_credential_binding_revision IS NOT NULL)
        OR (evidence_type='HOST_OPERATION_AUTHORITY_STATE' AND source_type='CONTROL_PLANE'
              AND source_host_authority_generation IS NOT NULL)
        OR (evidence_type='VM_RUNTIME_OBSERVATION' AND source_type='LIBVIRT_READ_BACK'))
);

ALTER TABLE kim.failure_epoch_open_request_evidence
  ADD CONSTRAINT failure_epoch_open_trigger_evidence_fk
  FOREIGN KEY(triggering_evidence_id) REFERENCES kim.failure_observation_evidence(evidence_id)
  DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE kim.failure_epoch_evidence
  ADD CONSTRAINT failure_epoch_trigger_evidence_fk
  FOREIGN KEY(triggering_evidence_id) REFERENCES kim.failure_observation_evidence(evidence_id)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE kim.failure_epoch_transition_evidence (
    failure_epoch_id text NOT NULL REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    transition_generation bigint NOT NULL CHECK (transition_generation > 0),
    from_state text,
    to_state text NOT NULL CHECK (to_state IN ('SUSPECTED','CLEARED','CONFIRMED','FENCING','FENCED','FENCE_UNKNOWN','BLOCKED')),
    cause_evidence_id text NOT NULL REFERENCES kim.failure_observation_evidence(evidence_id),
    transition_digest char(64) NOT NULL CHECK (transition_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(failure_epoch_id,transition_generation),
    CHECK (transition_generation>1 OR (from_state IS NULL AND to_state='SUSPECTED'))
);

CREATE TABLE kim.failure_epochs_current (
    failure_epoch_id text PRIMARY KEY REFERENCES kim.failure_epoch_evidence(failure_epoch_id),
    epoch_generation bigint NOT NULL CHECK (epoch_generation=1),
    workload_id text NOT NULL,
    failure_class text NOT NULL,
    epoch_state text NOT NULL CHECK (epoch_state IN ('SUSPECTED','CLEARED','CONFIRMED','FENCING','FENCED','FENCE_UNKNOWN','BLOCKED')),
    transition_generation bigint NOT NULL CHECK (transition_generation > 0),
    latest_evidence_generation bigint NOT NULL CHECK (latest_evidence_generation > 0),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(failure_epoch_id,transition_generation)
      REFERENCES kim.failure_epoch_transition_evidence(failure_epoch_id,transition_generation),
    FOREIGN KEY(failure_epoch_id,epoch_generation,workload_id,failure_class)
      REFERENCES kim.failure_epoch_evidence(failure_epoch_id,epoch_generation,workload_id,failure_class)
);

CREATE TRIGGER failure_epoch_open_request_evidence_no_update BEFORE UPDATE ON kim.failure_epoch_open_request_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_epoch_evidence_no_update BEFORE UPDATE ON kim.failure_epoch_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_observation_evidence_no_update BEFORE UPDATE ON kim.failure_observation_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER failure_epoch_transition_evidence_no_update BEFORE UPDATE ON kim.failure_epoch_transition_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.failure_epoch_evidence IS
'Immutable incident identity bound to one exact VM Availability Binding, Policy, Admission, allocation, and source Host provenance. It is not recovery authority.';
COMMENT ON TABLE kim.failure_observation_evidence IS
'Append-only closed typed failure observations. UNKNOWN is not CONFIRMED and connectivity/heartbeat loss is not fencing proof.';
COMMENT ON TABLE kim.failure_epochs_current IS
'Rebuildable failure-epoch projection. Migration 050 opens SUSPECTED epochs only; automatic confirmation is not implemented.';
