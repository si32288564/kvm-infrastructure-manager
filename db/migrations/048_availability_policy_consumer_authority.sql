-- Availability responsibility is immutable policy evidence.  This migration
-- also introduces the closed catalog used by the existing generic HostGroup
-- policy binding/resolution authority.
CREATE TABLE kim.group_policy_revision_catalog (
    policy_type text NOT NULL CHECK (policy_type IN ('MAINTENANCE','AVAILABILITY_POLICY')),
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (policy_type,policy_id,policy_revision),
    UNIQUE (policy_type,policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.group_policies_current (
    policy_type text NOT NULL,
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (policy_type,policy_id),
    FOREIGN KEY (policy_type,policy_id,policy_revision,policy_digest)
      REFERENCES kim.group_policy_revision_catalog(policy_type,policy_id,policy_revision,policy_digest)
);

INSERT INTO kim.group_policy_revision_catalog(policy_type,policy_id,policy_revision,policy_digest,lifecycle_state,recorded_at)
SELECT 'MAINTENANCE',policy_id,policy_revision,policy_digest,lifecycle_state,recorded_at
FROM kim.maintenance_policy_revision_evidence;
INSERT INTO kim.group_policies_current(policy_type,policy_id,policy_revision,policy_digest,lifecycle_state,updated_at)
SELECT 'MAINTENANCE',policy_id,policy_revision,policy_digest,lifecycle_state,updated_at
FROM kim.maintenance_policies_current;

CREATE TABLE kim.availability_policy_revision_evidence (
    policy_id text NOT NULL,
    policy_type text NOT NULL DEFAULT 'AVAILABILITY_POLICY' CHECK (policy_type='AVAILABILITY_POLICY'),
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    responsibility text NOT NULL CHECK (responsibility IN ('INFRASTRUCTURE_MANAGED','WORKLOAD_MANAGED','MANUAL')),
    host_failure_action text NOT NULL CHECK (host_failure_action IN ('RESTART_ON_OTHER_HOST','EVACUATE','NO_AUTOMATIC_ACTION')),
    failure_confirmation_policy text NOT NULL CHECK (failure_confirmation_policy <> ''),
    fencing_requirements text NOT NULL CHECK (fencing_requirements <> ''),
    storage_requirements text NOT NULL CHECK (storage_requirements <> ''),
    network_device_requirements text NOT NULL CHECK (network_device_requirements <> ''),
    recovery_eligibility_policy text NOT NULL CHECK (recovery_eligibility_policy <> ''),
    failure_domain_constraints text NOT NULL CHECK (failure_domain_constraints <> ''),
    recovery_budget_policy_reference text NOT NULL CHECK (recovery_budget_policy_reference <> ''),
    max_attempts integer NOT NULL CHECK (max_attempts > 0),
    escalation_policy text NOT NULL CHECK (escalation_policy <> ''),
    notification_policy text NOT NULL CHECK (notification_policy <> ''),
    support_tier text NOT NULL CHECK (support_tier <> ''),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    created_by text NOT NULL CHECK (created_by <> ''),
    approved_by text NOT NULL CHECK (approved_by <> ''),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(policy_id,policy_revision),
    UNIQUE(policy_id,policy_revision,policy_digest),
    FOREIGN KEY(policy_type,policy_id,policy_revision,policy_digest)
      REFERENCES kim.group_policy_revision_catalog(policy_type,policy_id,policy_revision,policy_digest),
    CHECK ((responsibility='INFRASTRUCTURE_MANAGED' AND host_failure_action IN ('RESTART_ON_OTHER_HOST','EVACUATE'))
        OR (responsibility IN ('WORKLOAD_MANAGED','MANUAL') AND host_failure_action='NO_AUTOMATIC_ACTION'))
);

CREATE TABLE kim.availability_policies_current (
    policy_id text PRIMARY KEY,
    policy_revision bigint NOT NULL CHECK (policy_revision > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('DRAFT','ACTIVE','DEPRECATED','RETIRED')),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(policy_id,policy_revision,policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

ALTER TABLE kim.host_group_policy_binding_revision_evidence
  DROP CONSTRAINT host_group_policy_binding_revision_evidence_policy_type_check,
  DROP CONSTRAINT host_group_policy_binding_revision_evidence_consumer_type_check;
ALTER TABLE kim.host_group_policy_binding_revision_evidence
  ADD CHECK ((policy_type='MAINTENANCE' AND consumer_type='MAINTENANCE_PLAN') OR
             (policy_type='AVAILABILITY_POLICY' AND consumer_type='VM_PLACEMENT'));
DO $$ DECLARE constraint_name text; BEGIN
  SELECT conname INTO constraint_name FROM pg_constraint
  WHERE conrelid='kim.host_group_policy_binding_revision_evidence'::regclass
    AND confrelid='kim.maintenance_policy_revision_evidence'::regclass AND contype='f';
  IF constraint_name IS NOT NULL THEN
    EXECUTE format('ALTER TABLE kim.host_group_policy_binding_revision_evidence DROP CONSTRAINT %I',constraint_name);
  END IF;
END $$;
ALTER TABLE kim.host_group_policy_binding_revision_evidence
  ADD FOREIGN KEY(policy_type,policy_id,policy_revision,policy_digest)
    REFERENCES kim.group_policy_revision_catalog(policy_type,policy_id,policy_revision,policy_digest);
ALTER TABLE kim.host_group_policy_bindings_current
  DROP CONSTRAINT host_group_policy_bindings_current_policy_type_check,
  DROP CONSTRAINT host_group_policy_bindings_current_consumer_type_check;
ALTER TABLE kim.host_group_policy_bindings_current
  ADD CHECK ((policy_type='MAINTENANCE' AND consumer_type='MAINTENANCE_PLAN') OR
             (policy_type='AVAILABILITY_POLICY' AND consumer_type='VM_PLACEMENT'));

ALTER TABLE kim.host_group_policy_resolution_evidence
  DROP CONSTRAINT host_group_policy_resolution_evidence_policy_type_check,
  DROP CONSTRAINT host_group_policy_resolution_evidence_consumer_type_check;
ALTER TABLE kim.host_group_policy_resolution_evidence
  ADD CHECK ((policy_type='MAINTENANCE' AND consumer_type='MAINTENANCE_PLAN') OR
             (policy_type='AVAILABILITY_POLICY' AND consumer_type='VM_PLACEMENT'));

CREATE TABLE kim.vm_availability_binding_evidence (
    workload_id text NOT NULL,
    binding_revision bigint NOT NULL CHECK (binding_revision > 0),
    admission_id text NOT NULL UNIQUE REFERENCES kim.placement_admission_decisions(admission_id),
    allocation_id text NOT NULL UNIQUE REFERENCES kim.compute_allocation_claims(allocation_id),
    policy_resolution_id text NOT NULL UNIQUE REFERENCES kim.host_group_policy_resolution_evidence(resolution_id),
    availability_policy_id text NOT NULL,
    availability_policy_revision bigint NOT NULL CHECK (availability_policy_revision > 0),
    availability_policy_digest char(64) NOT NULL CHECK (availability_policy_digest ~ '^[0-9a-f]{64}$'),
    responsibility text NOT NULL CHECK (responsibility IN ('INFRASTRUCTURE_MANAGED','WORKLOAD_MANAGED','MANUAL')),
    host_failure_action text NOT NULL CHECK (host_failure_action IN ('RESTART_ON_OTHER_HOST','EVACUATE','NO_AUTOMATIC_ACTION')),
    resolution_input_digest char(64) NOT NULL CHECK (resolution_input_digest ~ '^[0-9a-f]{64}$'),
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    bound_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(workload_id,binding_revision),
    UNIQUE(workload_id,binding_revision,binding_digest),
    FOREIGN KEY(availability_policy_id,availability_policy_revision,availability_policy_digest)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision,policy_digest)
);

CREATE TABLE kim.vm_availability_bindings_current (
    workload_id text PRIMARY KEY,
    binding_revision bigint NOT NULL CHECK (binding_revision > 0),
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY(workload_id,binding_revision,binding_digest)
      REFERENCES kim.vm_availability_binding_evidence(workload_id,binding_revision,binding_digest)
);

CREATE TRIGGER group_policy_revision_catalog_no_update BEFORE UPDATE ON kim.group_policy_revision_catalog
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER availability_policy_revision_evidence_no_update BEFORE UPDATE ON kim.availability_policy_revision_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_availability_binding_evidence_no_update BEFORE UPDATE ON kim.vm_availability_binding_evidence
 FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.availability_policy_revision_evidence IS
'Immutable closed Availability responsibility contract; it is neither failure evidence nor recovery authority.';
COMMENT ON TABLE kim.vm_availability_binding_evidence IS
'Immutable exact policy/resolution/admission responsibility fixed at Final Admission; live policy drift never rewrites history.';
