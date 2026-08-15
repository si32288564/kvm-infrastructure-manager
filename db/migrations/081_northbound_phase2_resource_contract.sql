-- Phase 2 public Create replay authority. The logical/realization authorities
-- remain owned by Migrations 077-080; this table only binds the authenticated
-- Northbound mutation identity to their exact first revision.
CREATE TABLE kim.northbound_phase2_idempotency_evidence (
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    parent_project_id text NOT NULL,
    resource_type text NOT NULL CHECK (resource_type IN ('NETWORK','SUBNET','PORT','VOLUME')),
    http_method text NOT NULL CHECK (http_method='POST'),
    canonical_path text NOT NULL CHECK (canonical_path <> ''),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    resource_id text NOT NULL,
    resource_revision bigint NOT NULL CHECK (resource_revision > 0),
    operation_id text NOT NULL,
    response_status integer NOT NULL CHECK (response_status=201),
    request_id text NOT NULL CHECK (request_id <> ''),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(principal_issuer,principal_subject,parent_project_id,resource_type,http_method,canonical_path,idempotency_key),
    UNIQUE(resource_type,resource_id)
);

CREATE TRIGGER northbound_phase2_idempotency_evidence_no_update
  BEFORE UPDATE ON kim.northbound_phase2_idempotency_evidence
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.northbound_phase2_idempotency_evidence IS
  'Immutable public Create replay binding for logical Network, Subnet, Port, and Volume. It grants no physical backend authority.';
