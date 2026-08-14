-- Flavor already has immutable revisions, a current projection, Project
-- ownership, and Placement consumers. This migration adds only public
-- lifecycle compatibility; Flavor shape authority remains Migration 010.
ALTER TABLE kim.flavors_current
  ADD COLUMN delete_protection boolean NOT NULL DEFAULT false,
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
  ADD COLUMN deleted_from_revision bigint CHECK (deleted_from_revision IS NULL OR deleted_from_revision > 0),
  ADD CHECK ((lifecycle_state='ACTIVE' AND deleted_from_revision IS NULL) OR
             (lifecycle_state='DELETED' AND (deleted_from_revision IS NULL OR deleted_from_revision=flavor_revision-1)));

CREATE TABLE kim.northbound_flavor_idempotency_evidence (
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    parent_project_id text NOT NULL,
    http_method text NOT NULL CHECK (http_method='POST'),
    canonical_path text NOT NULL CHECK (canonical_path <> ''),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    flavor_id text NOT NULL,
    flavor_revision bigint NOT NULL CHECK (flavor_revision > 0),
    response_status integer NOT NULL CHECK (response_status=201),
    request_id text NOT NULL CHECK (request_id <> ''),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(principal_issuer,principal_subject,parent_project_id,http_method,canonical_path,idempotency_key),
    FOREIGN KEY(flavor_id,flavor_revision)
      REFERENCES kim.flavor_revision_evidence(flavor_id,flavor_revision)
);

CREATE TRIGGER northbound_flavor_idempotency_evidence_no_update
  BEFORE UPDATE ON kim.northbound_flavor_idempotency_evidence
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON COLUMN kim.flavors_current.delete_protection IS
  'Northbound desired deletion guard; not Placement or physical realization.';
COMMENT ON TABLE kim.northbound_flavor_idempotency_evidence IS
  'Immutable Flavor create replay authority with an exact Flavor revision foreign key.';
