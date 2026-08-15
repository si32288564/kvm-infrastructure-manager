-- Public VM Create replay authority. VM logical/runtime/terminal authorities
-- remain owned by Migrations 082-087; this table only binds an authenticated
-- Northbound mutation identity to the exact initial aggregate operation.
CREATE TABLE kim.northbound_vm_idempotency_evidence (
    principal_issuer text NOT NULL,
    principal_subject text NOT NULL,
    parent_project_id text NOT NULL,
    http_method text NOT NULL CHECK(http_method='POST'),
    canonical_path text NOT NULL CHECK(canonical_path='/api/v1/vms'),
    idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 255),
    request_digest char(64) NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
    vm_id uuid NOT NULL,
    vm_revision bigint NOT NULL CHECK(vm_revision=1),
    operation_id text NOT NULL,
    response_status integer NOT NULL CHECK(response_status=201),
    request_id text NOT NULL CHECK(request_id <> ''),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(principal_issuer,principal_subject,parent_project_id,http_method,canonical_path,idempotency_key),
    UNIQUE(vm_id),
    FOREIGN KEY(vm_id,vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(operation_id) REFERENCES kim.vm_lifecycle_operations_current(operation_id)
);

CREATE TRIGGER northbound_vm_idempotency_evidence_no_update
  BEFORE UPDATE ON kim.northbound_vm_idempotency_evidence
  FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.northbound_vm_idempotency_evidence IS
  'Immutable public Create replay binding for a logical VM aggregate. It grants no Placement, Host, Port binding, Volume backend, or mobility authority.';
