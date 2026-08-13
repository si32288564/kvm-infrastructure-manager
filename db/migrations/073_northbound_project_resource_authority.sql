-- Northbound Project is the reference synchronous persistent resource.  The
-- desired authority, public idempotency decision, owner binding, and accepted
-- audit event are committed together.  Security denials are separately
-- immutable because no resource transaction exists to commit them with.
CREATE TABLE kim.project_revision_evidence (
    project_id text NOT NULL CHECK (project_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    project_revision bigint NOT NULL CHECK (project_revision > 0),
    project_name text NOT NULL CHECK (length(project_name) BETWEEN 1 AND 255),
    delete_protection boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DELETED')),
    previous_revision bigint CHECK (previous_revision IS NULL OR previous_revision > 0),
    desired_digest char(64) NOT NULL CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    actor_issuer text NOT NULL CHECK (actor_issuer <> ''),
    actor_subject text NOT NULL CHECK (actor_subject <> ''),
    request_id text NOT NULL CHECK (request_id <> ''),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(project_id,project_revision),
    UNIQUE(project_id,project_revision,desired_digest),
    CHECK ((project_revision=1 AND previous_revision IS NULL) OR
           (project_revision>1 AND previous_revision=project_revision-1))
);

CREATE TABLE kim.projects_current (
    project_id text PRIMARY KEY,
    project_revision bigint NOT NULL CHECK (project_revision > 0),
    project_name text NOT NULL,
    delete_protection boolean NOT NULL,
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','DELETED')),
    desired_digest char(64) NOT NULL CHECK (desired_digest ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    deleted_from_revision bigint CHECK (deleted_from_revision IS NULL OR deleted_from_revision > 0),
    FOREIGN KEY(project_id,project_revision,desired_digest)
      REFERENCES kim.project_revision_evidence(project_id,project_revision,desired_digest),
    CHECK ((lifecycle_state='ACTIVE' AND deleted_from_revision IS NULL) OR
           (lifecycle_state='DELETED' AND deleted_from_revision=project_revision-1))
);

CREATE TABLE kim.northbound_role_bindings_current (
    binding_id text PRIMARY KEY CHECK (binding_id <> ''),
    principal_issuer text NOT NULL CHECK (principal_issuer <> ''),
    principal_subject text NOT NULL CHECK (principal_subject <> ''),
    principal_type text NOT NULL CHECK (principal_type IN ('HUMAN','AUTOMATION')),
    scope_type text NOT NULL CHECK (scope_type IN ('SYSTEM','PROJECT')),
    scope_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('READER','WRITER','ADMIN')),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE','REVOKED')),
    binding_revision bigint NOT NULL CHECK (binding_revision > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK ((scope_type='SYSTEM' AND scope_id='') OR
           (scope_type='PROJECT' AND scope_id<>'')),
    UNIQUE(principal_issuer,principal_subject,scope_type,scope_id)
);

CREATE TABLE kim.northbound_idempotency_evidence (
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    parent_scope text NOT NULL,
    http_method text NOT NULL CHECK (http_method IN ('POST','PUT','PATCH','DELETE')),
    canonical_path text NOT NULL CHECK (canonical_path <> ''),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    resource_type text NOT NULL CHECK (resource_type='PROJECT'),
    resource_id text NOT NULL,
    resource_revision bigint NOT NULL CHECK (resource_revision > 0),
    response_status integer NOT NULL CHECK (response_status BETWEEN 200 AND 299),
    request_id text NOT NULL CHECK (request_id <> ''),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(principal_issuer,principal_subject,parent_scope,http_method,canonical_path,idempotency_key),
    FOREIGN KEY(resource_id,resource_revision)
      REFERENCES kim.project_revision_evidence(project_id,project_revision)
);

CREATE TABLE kim.northbound_audit_evidence (
    audit_id text PRIMARY KEY CHECK (audit_id <> ''),
    request_id text NOT NULL CHECK (request_id <> ''),
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    principal_type text NOT NULL CHECK (principal_type IN ('HUMAN','AUTOMATION','UNKNOWN')),
    action text NOT NULL CHECK (action <> ''),
    resource_type text NOT NULL CHECK (resource_type <> ''),
    resource_id text NOT NULL,
    scope_type text NOT NULL CHECK (scope_type IN ('SYSTEM','PROJECT','UNKNOWN')),
    scope_id text NOT NULL,
    resource_revision bigint CHECK (resource_revision IS NULL OR resource_revision > 0),
    result text NOT NULL CHECK (result IN ('ALLOWED','DENIED','SUCCEEDED','FAILED')),
    reason_code text NOT NULL CHECK (reason_code <> ''),
    idempotency_digest char(64) CHECK (idempotency_digest IS NULL OR idempotency_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE INDEX northbound_audit_by_request
  ON kim.northbound_audit_evidence(request_id,recorded_at,audit_id);
CREATE INDEX northbound_role_bindings_by_principal
  ON kim.northbound_role_bindings_current(principal_issuer,principal_subject,lifecycle_state);

CREATE TRIGGER project_revision_evidence_no_update
  BEFORE UPDATE ON kim.project_revision_evidence
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER northbound_idempotency_evidence_no_update
  BEFORE UPDATE ON kim.northbound_idempotency_evidence
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER northbound_audit_evidence_no_update
  BEFORE UPDATE ON kim.northbound_audit_evidence
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.projects_current IS
  'Northbound Project desired authority. Project revision is unrelated to Host, backend, Placement, Materialization, Recovery, or EVACUATE generations.';
COMMENT ON TABLE kim.northbound_idempotency_evidence IS
  'Immutable public mutation replay authority scoped by authenticated principal, parent scope, method, canonical path, and key.';
COMMENT ON TABLE kim.northbound_audit_evidence IS
  'Immutable Northbound security and resource decision audit. Authorization headers and credentials are never stored.';
