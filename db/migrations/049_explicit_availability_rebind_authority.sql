-- Explicit Availability Rebind is the only authority that advances an
-- existing VM Availability Binding. Existing revision-one evidence is not
-- rewritten and no historical Rebind is fabricated.
ALTER TABLE kim.vm_availability_binding_evidence
  DROP CONSTRAINT vm_availability_binding_evidence_admission_id_key,
  DROP CONSTRAINT vm_availability_binding_evidence_allocation_id_key,
  DROP CONSTRAINT vm_availability_binding_evidence_policy_resolution_id_key,
  ALTER COLUMN policy_resolution_id DROP NOT NULL,
  ALTER COLUMN resolution_input_digest DROP NOT NULL,
  ADD COLUMN binding_source text NOT NULL DEFAULT 'FINAL_ADMISSION'
    CHECK (binding_source IN ('FINAL_ADMISSION','EXPLICIT_REBIND')),
  ADD COLUMN source_binding_revision bigint,
  ADD COLUMN source_binding_digest char(64),
  ADD COLUMN rebind_id text,
  ADD COLUMN rebind_decision_generation bigint;

CREATE TABLE kim.vm_availability_rebind_request_evidence (
    rebind_id text PRIMARY KEY,
    workload_id text NOT NULL,
    expected_current_binding_revision bigint NOT NULL CHECK (expected_current_binding_revision > 0),
    source_binding_digest char(64) NOT NULL CHECK (source_binding_digest ~ '^[0-9a-f]{64}$'),
    target_policy_id text NOT NULL,
    target_policy_revision bigint NOT NULL CHECK (target_policy_revision > 0),
    target_policy_digest char(64) NOT NULL CHECK (target_policy_digest ~ '^[0-9a-f]{64}$'),
    requested_by text NOT NULL CHECK (requested_by <> ''),
    authorized_by text NOT NULL CHECK (authorized_by <> ''),
    authorization_reference text NOT NULL CHECK (authorization_reference <> ''),
    reason text NOT NULL CHECK (reason <> ''),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(rebind_id,request_digest),
    FOREIGN KEY(workload_id,expected_current_binding_revision,source_binding_digest)
      REFERENCES kim.vm_availability_binding_evidence(workload_id,binding_revision,binding_digest),
    FOREIGN KEY(target_policy_id,target_policy_revision,target_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.vm_availability_rebind_decision_evidence (
    rebind_id text NOT NULL,
    decision_generation bigint NOT NULL CHECK (decision_generation = 1),
    workload_id text NOT NULL,
    decision_state text NOT NULL CHECK (decision_state IN ('ACCEPTED','REJECTED')),
    result_code text NOT NULL CHECK (result_code IN ('ACCEPTED','STALE_SOURCE_BINDING','STALE_TARGET_POLICY','POLICY_NOT_ACTIVE','INVALID_TARGET_POLICY','AUTHORIZATION_DENIED','CONFLICT')),
    source_binding_revision bigint NOT NULL CHECK (source_binding_revision > 0),
    source_binding_digest char(64) NOT NULL CHECK (source_binding_digest ~ '^[0-9a-f]{64}$'),
    target_binding_revision bigint,
    target_policy_id text NOT NULL,
    target_policy_revision bigint NOT NULL CHECK (target_policy_revision > 0),
    target_policy_digest char(64) NOT NULL CHECK (target_policy_digest ~ '^[0-9a-f]{64}$'),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    decided_by text NOT NULL CHECK (decided_by <> ''),
    authorization_reference text NOT NULL CHECK (authorization_reference <> ''),
    decision_reason text NOT NULL CHECK (decision_reason <> ''),
    decision_digest char(64) NOT NULL CHECK (decision_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(rebind_id,decision_generation),
    UNIQUE(rebind_id,decision_generation,decision_digest),
    FOREIGN KEY(rebind_id,request_digest)
      REFERENCES kim.vm_availability_rebind_request_evidence(rebind_id,request_digest),
    FOREIGN KEY(workload_id,source_binding_revision,source_binding_digest)
      REFERENCES kim.vm_availability_binding_evidence(workload_id,binding_revision,binding_digest),
    CHECK ((decision_state='ACCEPTED' AND result_code='ACCEPTED' AND target_binding_revision=source_binding_revision+1)
        OR (decision_state='REJECTED' AND result_code<>'ACCEPTED' AND target_binding_revision IS NULL))
);

ALTER TABLE kim.vm_availability_binding_evidence
  ADD CONSTRAINT vm_availability_binding_source_fk
    FOREIGN KEY(workload_id,source_binding_revision,source_binding_digest)
    REFERENCES kim.vm_availability_binding_evidence(workload_id,binding_revision,binding_digest),
  ADD CONSTRAINT vm_availability_binding_rebind_decision_fk
    FOREIGN KEY(rebind_id,rebind_decision_generation)
    REFERENCES kim.vm_availability_rebind_decision_evidence(rebind_id,decision_generation),
  ADD CHECK ((binding_source='FINAL_ADMISSION' AND binding_revision=1 AND policy_resolution_id IS NOT NULL
              AND resolution_input_digest IS NOT NULL
              AND source_binding_revision IS NULL AND source_binding_digest IS NULL
              AND rebind_id IS NULL AND rebind_decision_generation IS NULL)
          OR (binding_source='EXPLICIT_REBIND' AND binding_revision>1 AND policy_resolution_id IS NULL
              AND resolution_input_digest IS NULL
              AND source_binding_revision=binding_revision-1 AND source_binding_digest IS NOT NULL
              AND rebind_id IS NOT NULL AND rebind_decision_generation=1));

CREATE TRIGGER vm_availability_rebind_request_evidence_no_update BEFORE UPDATE ON kim.vm_availability_rebind_request_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_availability_rebind_decision_evidence_no_update BEFORE UPDATE ON kim.vm_availability_rebind_decision_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_availability_rebind_request_evidence IS
'Immutable authorized operator intent for one exact source Binding and exact AvailabilityPolicy revision; intent alone changes no authority.';
COMMENT ON TABLE kim.vm_availability_rebind_decision_evidence IS
'Immutable Rebind decision. ACCEPTED is committed atomically with exactly one next Binding revision and current-pointer switch.';
COMMENT ON COLUMN kim.vm_availability_binding_evidence.binding_source IS
'FINAL_ADMISSION or EXPLICIT_REBIND provenance; neither Policy drift nor failure evidence advances this revision.';
