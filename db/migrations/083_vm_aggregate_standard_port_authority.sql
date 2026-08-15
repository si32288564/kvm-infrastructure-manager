-- Phase 3 VM aggregate STANDARD Port producer profile. Logical Port identity
-- remains in the dependency snapshot while Host binding and OVS realization
-- are immutable runtime-incarnation evidence.
ALTER TABLE kim.vm_dependency_port_evidence ADD COLUMN attachment_intent_id text;
ALTER TABLE kim.vm_dependency_port_evidence
    ALTER COLUMN attachment_intent_id SET NOT NULL,
    ADD UNIQUE(attachment_intent_id),
    ADD FOREIGN KEY(attachment_intent_id) REFERENCES kim.port_attachment_intent_evidence(attachment_intent_id);

CREATE TABLE kim.vm_aggregate_port_binding_evidence (
    binding_evidence_id text PRIMARY KEY,
    aggregate_admission_binding_evidence_id text NOT NULL REFERENCES kim.vm_aggregate_admission_binding_evidence(binding_evidence_id),
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    dependency_snapshot_id text NOT NULL REFERENCES kim.vm_dependency_snapshot_evidence(dependency_snapshot_id),
    port_ordinal integer NOT NULL,
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    desired_digest char(64) NOT NULL CHECK(desired_digest ~ '^[0-9a-f]{64}$'),
    requested_attachment_intent_id text NOT NULL REFERENCES kim.port_attachment_intent_evidence(attachment_intent_id),
    bound_attachment_intent_id text NOT NULL REFERENCES kim.port_attachment_intent_evidence(attachment_intent_id),
    bound_attachment_generation bigint NOT NULL CHECK(bound_attachment_generation > 0),
    admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL,
    binding_generation bigint NOT NULL CHECK(binding_generation > 0),
    binding_type text NOT NULL CHECK(binding_type='OVS'),
    binding_digest char(64) NOT NULL CHECK(binding_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(operation_id,operation_generation,port_ordinal),
    UNIQUE(operation_id,operation_generation,port_id),
    FOREIGN KEY(operation_id,operation_generation) REFERENCES kim.vm_lifecycle_operation_evidence(operation_id,operation_generation),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision)
);

CREATE TABLE kim.vm_aggregate_network_port_verification_evidence (
    verification_id text NOT NULL REFERENCES kim.vm_aggregate_verification_evidence(verification_id),
    port_ordinal integer NOT NULL,
    port_binding_evidence_id text NOT NULL REFERENCES kim.vm_aggregate_port_binding_evidence(binding_evidence_id),
    port_id text NOT NULL,
    port_revision bigint NOT NULL,
    admission_id text NOT NULL,
    host_id text NOT NULL,
    binding_generation bigint NOT NULL,
    realization_evidence_id text NOT NULL REFERENCES kim.vm_network_port_realization_evidence(evidence_id),
    realization_observation_generation bigint NOT NULL,
    realization_observation_digest char(64) NOT NULL CHECK(realization_observation_digest ~ '^[0-9a-f]{64}$'),
    verification_digest char(64) NOT NULL CHECK(verification_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY(verification_id,port_ordinal),
    UNIQUE(verification_id,port_id),
    UNIQUE(verification_id,realization_evidence_id),
    FOREIGN KEY(port_id,port_revision) REFERENCES kim.port_resource_revision_evidence(port_id,port_revision)
);

CREATE TRIGGER vm_aggregate_port_binding_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_port_binding_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER vm_aggregate_network_port_verification_evidence_no_update BEFORE UPDATE ON kim.vm_aggregate_network_port_verification_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.vm_aggregate_port_binding_evidence IS 'Exact logical Port dependency to Final Admission Host-binding incarnation; no physical identity is part of VM desired state.';
COMMENT ON TABLE kim.vm_aggregate_network_port_verification_evidence IS 'Exact STANDARD OVS preboot realization consumed by VM aggregate verification.';
