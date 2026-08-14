-- AvailabilityPolicy already has immutable revisions, a SYSTEM catalog,
-- exact workload bindings, and Recovery consumers. Add only the public
-- logical metadata and replay/lifecycle compatibility required by the
-- Northbound closed non-automatic profiles.
ALTER TABLE kim.availability_policies_current
  ADD COLUMN delete_protection boolean NOT NULL DEFAULT false,
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
  ADD COLUMN deleted_from_revision bigint CHECK (deleted_from_revision IS NULL OR deleted_from_revision > 0),
  ADD CHECK (deleted_from_revision IS NULL OR lifecycle_state='RETIRED');

CREATE TABLE kim.northbound_availability_policy_revision_metadata (
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK(policy_revision>0),
    policy_name text NOT NULL CHECK(length(policy_name) BETWEEN 1 AND 255),
    availability_mode text NOT NULL CHECK(availability_mode IN ('MANUAL','WORKLOAD_MANAGED')),
    delete_protection boolean NOT NULL,
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(policy_id,policy_revision),
    FOREIGN KEY(policy_id,policy_revision)
      REFERENCES kim.availability_policy_revision_evidence(policy_id,policy_revision)
);

CREATE TABLE kim.northbound_availability_policy_idempotency_evidence (
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    parent_scope text NOT NULL CHECK(parent_scope='SYSTEM'),
    http_method text NOT NULL CHECK(http_method='POST'),
    canonical_path text NOT NULL CHECK(canonical_path='/api/v1/availability-policies'),
    idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL CHECK(policy_revision>0),
    response_status integer NOT NULL CHECK(response_status=201),
    request_id text NOT NULL CHECK(request_id<>''),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(principal_issuer,principal_subject,parent_scope,http_method,canonical_path,idempotency_key),
    FOREIGN KEY(policy_id,policy_revision)
      REFERENCES kim.northbound_availability_policy_revision_metadata(policy_id,policy_revision)
);

CREATE TRIGGER northbound_availability_policy_revision_metadata_no_update
  BEFORE UPDATE ON kim.northbound_availability_policy_revision_metadata
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER northbound_availability_policy_idempotency_evidence_no_update
  BEFORE UPDATE ON kim.northbound_availability_policy_idempotency_evidence
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.northbound_availability_policy_revision_metadata IS
  'Immutable public name/profile/protection paired with the exact existing AvailabilityPolicy revision.';
COMMENT ON COLUMN kim.availability_policies_current.deleted_from_revision IS
  'Northbound tombstone replay provenance; retirement never rewrites workload bindings.';
