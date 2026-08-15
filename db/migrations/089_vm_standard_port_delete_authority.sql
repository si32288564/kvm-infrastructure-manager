-- Phase 3 verified delete for one STANDARD Port.  The logical Port, MAC and
-- IP identities survive VM deletion; only the exact runtime Host binding is
-- retired after OVN/OVS absence has been observed.
CREATE TABLE kim.vm_delete_network_operation_evidence (
    delete_operation_id text PRIMARY KEY,
    operation_generation bigint NOT NULL CHECK(operation_generation=1),
    vm_id uuid NOT NULL,
    retire_vm_revision bigint NOT NULL,
    runtime_intent_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL,
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    port_generation bigint NOT NULL CHECK(port_generation>0),
    attachment_intent_id text NOT NULL REFERENCES kim.port_attachment_intent_evidence(attachment_intent_id),
    attachment_generation bigint NOT NULL CHECK(attachment_generation>0),
    binding_generation bigint NOT NULL CHECK(binding_generation>0),
    retirement_operation_id text NOT NULL UNIQUE,
    retirement_intent_id text NOT NULL UNIQUE,
    retirement_intent_generation bigint NOT NULL CHECK(retirement_intent_generation>0),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_operation_evidence(delete_operation_id,operation_generation),
    FOREIGN KEY(vm_id,retire_vm_revision) REFERENCES kim.vm_resource_revision_evidence(vm_id,vm_revision),
    FOREIGN KEY(vm_id,runtime_intent_generation) REFERENCES kim.vm_runtime_intent_evidence(vm_id,runtime_intent_generation),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision)
);

CREATE TABLE kim.vm_delete_network_absence_evidence (
    absence_evidence_id text PRIMARY KEY,
    delete_operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    retirement_evidence_id text NOT NULL UNIQUE REFERENCES kim.network_port_binding_retirement_evidence(evidence_id),
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    port_generation bigint NOT NULL,
    binding_generation bigint NOT NULL,
    source_host_id text NOT NULL,
    absence_digest char(64) NOT NULL CHECK(absence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(delete_operation_id,operation_generation),
    FOREIGN KEY(delete_operation_id,operation_generation) REFERENCES kim.vm_delete_network_operation_evidence(delete_operation_id,operation_generation),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision)
);

ALTER TABLE kim.vm_delete_terminal_evidence
    ADD COLUMN network_absence_evidence_id text UNIQUE REFERENCES kim.vm_delete_network_absence_evidence(absence_evidence_id);

CREATE TRIGGER vm_delete_network_operation_evidence_no_update BEFORE UPDATE ON kim.vm_delete_network_operation_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_delete_network_absence_evidence_no_update BEFORE UPDATE ON kim.vm_delete_network_absence_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_delete_network_operation_evidence IS 'Exact one-STANDARD-Port runtime incarnation consumed by VM delete; logical Port identity is preserved.';
COMMENT ON TABLE kim.vm_delete_network_absence_evidence IS 'DB-derived bridge from exact OVN/OVS Port-binding retirement evidence to VM delete terminal authority.';
