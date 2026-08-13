-- Backend cleanup is post-terminal hygiene authority.  It never changes the
-- Recovery terminal decision and never authorizes deletion by a caller-supplied
-- backend path/name.  Every operation is bound to one exact materialization
-- incarnation and immutable eligibility provenance.

CREATE TABLE kim.backend_cleanup_operation_evidence (
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL CHECK(cleanup_generation>0),
    resource_type text NOT NULL CHECK(resource_type IN ('LIBVIRT_DOMAIN','LOCAL_LVM_VOLUME','HOST_NETWORK_ARTIFACT','PCI_VF_ARTIFACT')),
    resource_id text NOT NULL CHECK(resource_id<>''),
    resource_generation bigint NOT NULL CHECK(resource_generation>0),
    backend_type text NOT NULL CHECK(backend_type IN ('LIBVIRT','LOCAL_LVM','OVN_OVS','PCI_SRIOV')),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_plan_digest char(64) NOT NULL CHECK(source_plan_digest ~ '^[0-9a-f]{64}$'),
    backend_identity text NOT NULL CHECK(backend_identity<>''),
    backend_identity_digest char(64) NOT NULL CHECK(backend_identity_digest ~ '^[0-9a-f]{64}$'),
    cleanup_reason text NOT NULL CHECK(cleanup_reason IN ('RECOVERY_SUPERSEDED','FAILED_MATERIALIZATION','EXPLICIT_DELETE','ABORTED_MOVE')),
    cleanup_policy_revision text NOT NULL CHECK(cleanup_policy_revision<>''),
    cleanup_policy_digest char(64) NOT NULL CHECK(cleanup_policy_digest ~ '^[0-9a-f]{64}$'),
    origin_authority_type text NOT NULL CHECK(origin_authority_type IN ('RECOVERY_TERMINAL','MATERIALIZATION','DELETE_OPERATION')),
    origin_authority_id text NOT NULL CHECK(origin_authority_id<>''),
    recovery_terminal_decision_id text REFERENCES kim.recovery_terminal_decision_evidence(terminal_decision_id),
    source_retirement_decision_id text REFERENCES kim.source_materialization_retirement_decision_evidence(retirement_decision_id),
    eligibility_state text NOT NULL CHECK(eligibility_state IN ('ELIGIBLE','ALREADY_ABSENT','BLOCKED')),
    eligibility_reason text NOT NULL CHECK(eligibility_reason<>''),
    eligibility_digest char(64) NOT NULL CHECK(eligibility_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(cleanup_operation_id,cleanup_generation),
    UNIQUE(resource_type,resource_id,resource_generation,source_host_id,source_materialization_generation),
    UNIQUE(cleanup_operation_id,cleanup_generation,eligibility_digest),
    FOREIGN KEY(vm_id,vm_generation) REFERENCES kim.virtual_machines_current(vm_id,vm_generation),
    CHECK((origin_authority_type='RECOVERY_TERMINAL' AND recovery_terminal_decision_id IS NOT NULL AND source_retirement_decision_id IS NOT NULL)
       OR (origin_authority_type<>'RECOVERY_TERMINAL' AND recovery_terminal_decision_id IS NULL))
);

CREATE TABLE kim.backend_cleanup_operations_current (
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL,
    operation_state text NOT NULL CHECK(operation_state IN ('PENDING','CLAIMED','DISPATCH_UNKNOWN','VERIFIED','CONFLICTING','BLOCKED','STALE')),
    claim_owner text,
    claim_generation bigint,
    last_claim_generation bigint NOT NULL DEFAULT 0 CHECK(last_claim_generation>=0),
    claim_expires_at timestamptz,
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(cleanup_operation_id,cleanup_generation),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation)
      REFERENCES kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation),
    CHECK((operation_state='CLAIMED' AND claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL)
       OR (operation_state<>'CLAIMED' AND claim_owner IS NULL AND claim_generation IS NULL AND claim_expires_at IS NULL))
);

CREATE TABLE kim.backend_cleanup_attempt_evidence (
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL,
    claim_generation bigint NOT NULL CHECK(claim_generation>0),
    claim_owner text NOT NULL CHECK(claim_owner<>''),
    claim_mode text NOT NULL CHECK(claim_mode IN ('APPLY_ALLOWED','READ_BACK_FIRST')),
    lease_expires_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(cleanup_operation_id,cleanup_generation,claim_generation),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation)
      REFERENCES kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation)
);

CREATE TABLE kim.backend_cleanup_observation_evidence (
    cleanup_evidence_id text PRIMARY KEY,
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL,
    claim_generation bigint NOT NULL CHECK(claim_generation>0),
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    resource_generation bigint NOT NULL,
    source_host_id text NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    source_materialization_generation bigint NOT NULL,
    backend_identity_digest char(64) NOT NULL CHECK(backend_identity_digest ~ '^[0-9a-f]{64}$'),
    backend_present boolean,
    backend_running boolean,
    identity_matches boolean NOT NULL,
    apply_response_state text NOT NULL CHECK(apply_response_state IN ('RECEIVED','LOST','NOT_APPLICABLE')),
    command_id text REFERENCES kim.execution_commands(command_id),
    attempt_index integer CHECK(attempt_index IS NULL OR attempt_index>0),
    verification_id text,
    verifier_digest char(64) CHECK(verifier_digest IS NULL OR verifier_digest ~ '^[0-9a-f]{64}$'),
    observation_generation bigint NOT NULL CHECK(observation_generation>0),
    observation_digest char(64) NOT NULL CHECK(observation_digest ~ '^[0-9a-f]{64}$'),
    result_state text NOT NULL CHECK(result_state IN ('ABSENT','UNKNOWN','CONFLICTING','ALREADY_ABSENT','BLOCKED')),
    artifact_digest char(64) NOT NULL CHECK(artifact_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK(evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(cleanup_operation_id,cleanup_generation,claim_generation,observation_generation),
    UNIQUE(cleanup_evidence_id,evidence_digest),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation,claim_generation)
      REFERENCES kim.backend_cleanup_attempt_evidence(cleanup_operation_id,cleanup_generation,claim_generation),
    CHECK((result_state IN ('ABSENT','ALREADY_ABSENT') AND backend_present=false AND backend_running=false AND identity_matches=true)
       OR result_state NOT IN ('ABSENT','ALREADY_ABSENT'))
);

CREATE TABLE kim.backend_cleanup_terminal_evidence (
    cleanup_terminal_id text PRIMARY KEY,
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL,
    cleanup_evidence_id text NOT NULL,
    cleanup_evidence_digest char(64) NOT NULL CHECK(cleanup_evidence_digest ~ '^[0-9a-f]{64}$'),
    terminal_state text NOT NULL CHECK(terminal_state IN ('VERIFIED','BLOCKED','CONFLICTING')),
    terminal_reason text NOT NULL CHECK(terminal_reason<>''),
    terminal_digest char(64) NOT NULL CHECK(terminal_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(cleanup_operation_id,cleanup_generation),
    UNIQUE(cleanup_terminal_id,terminal_digest),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation)
      REFERENCES kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation),
    FOREIGN KEY(cleanup_evidence_id,cleanup_evidence_digest)
      REFERENCES kim.backend_cleanup_observation_evidence(cleanup_evidence_id,evidence_digest)
);

ALTER TABLE kim.backend_cleanup_operations_current
  ADD CONSTRAINT backend_cleanup_current_terminal_fk
  FOREIGN KEY(terminal_evidence_id) REFERENCES kim.backend_cleanup_terminal_evidence(cleanup_terminal_id);

CREATE TABLE kim.source_backend_cleanup_current (
    vm_id uuid NOT NULL,
    source_materialization_generation bigint NOT NULL,
    source_host_id text NOT NULL,
    domain_state text NOT NULL CHECK(domain_state IN ('PENDING','VERIFIED','BLOCKED','UNKNOWN','CONFLICTING','NOT_REQUIRED')),
    storage_state text NOT NULL CHECK(storage_state IN ('PENDING','VERIFIED','BLOCKED','UNKNOWN','CONFLICTING','NOT_REQUIRED')),
    network_state text NOT NULL CHECK(network_state IN ('PENDING','VERIFIED','BLOCKED','UNKNOWN','CONFLICTING','NOT_REQUIRED')),
    pci_state text NOT NULL CHECK(pci_state IN ('PENDING','VERIFIED','BLOCKED','UNKNOWN','CONFLICTING','NOT_REQUIRED')),
    cleanup_complete boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(vm_id,source_materialization_generation)
);

CREATE TRIGGER backend_cleanup_operation_evidence_no_update BEFORE UPDATE ON kim.backend_cleanup_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER backend_cleanup_attempt_evidence_no_update BEFORE UPDATE ON kim.backend_cleanup_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER backend_cleanup_observation_evidence_no_update BEFORE UPDATE ON kim.backend_cleanup_observation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER backend_cleanup_terminal_evidence_no_update BEFORE UPDATE ON kim.backend_cleanup_terminal_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.backend_cleanup_operation_evidence IS 'Generic exact-incarnation post-authority cleanup eligibility. Recovery is one producer; this evidence never changes Recovery terminal state and never accepts caller backend paths or argv.';
COMMENT ON TABLE kim.backend_cleanup_observation_evidence IS 'Immutable physical read-back. Command success and Lease expiry are not absence; only exact backend absence can support VERIFIED cleanup.';
COMMENT ON TABLE kim.source_backend_cleanup_current IS 'Rebuildable per-artifact hygiene projection. BLOCKED/UNKNOWN cleanup never rolls back Recovery VERIFIED/RECOVERED authority.';
