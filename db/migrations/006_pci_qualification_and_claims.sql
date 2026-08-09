CREATE TABLE kim.host_pci_device_projections (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    device_address text NOT NULL,
    observation_generation bigint NOT NULL CHECK (observation_generation > 0),
    source_message_id text NOT NULL,
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    observation_state text NOT NULL CHECK (observation_state IN ('AVAILABLE', 'UNAVAILABLE', 'UNKNOWN', 'UNSUPPORTED')),
    vendor_id char(4) NOT NULL,
    device_id char(4) NOT NULL,
    subsystem_vendor_id char(4),
    subsystem_device_id char(4),
    driver text,
    device_revision text,
    firmware_revision text,
    numa_node_id integer NOT NULL CHECK (numa_node_id >= -1),
    iommu_group text,
    sriov_total_vfs integer NOT NULL CHECK (sriov_total_vfs >= 0),
    sriov_enabled_vfs integer NOT NULL CHECK (sriov_enabled_vfs >= 0 AND sriov_enabled_vfs <= sriov_total_vfs),
    pf_address text,
    vf_index integer CHECK (vf_index >= 0),
    relationship_state text NOT NULL CHECK (relationship_state IN ('AVAILABLE', 'UNAVAILABLE', 'UNKNOWN', 'UNSUPPORTED')),
    relationship_reason text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, device_address),
    FOREIGN KEY (host_id, observation_generation, source_message_id)
        REFERENCES kim.host_inventory_snapshots(host_id, observation_generation, message_id),
    CHECK ((pf_address IS NULL) = (vf_index IS NULL))
);

CREATE TABLE kim.pci_qualification_evidence (
    qualification_id text NOT NULL,
    qualification_revision bigint NOT NULL CHECK (qualification_revision > 0),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    device_address text NOT NULL,
    qualification_profile_revision text NOT NULL,
    test_artifact_digest char(64) NOT NULL CHECK (test_artifact_digest ~ '^[0-9a-f]{64}$'),
    evaluator_digest char(64) NOT NULL CHECK (evaluator_digest ~ '^[0-9a-f]{64}$'),
    observed_generation bigint NOT NULL CHECK (observed_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    binding_fingerprint jsonb NOT NULL,
    binding_digest char(64) NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
    validated_operations text[] NOT NULL,
    evidence_state text NOT NULL CHECK (evidence_state IN ('QUALIFIED', 'REJECTED', 'REVOKED')),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (qualification_id, qualification_revision),
    UNIQUE (host_id, device_address, qualification_id, qualification_revision)
);

CREATE TABLE kim.pci_qualification_bindings_current (
    host_id text NOT NULL,
    device_address text NOT NULL,
    qualification_id text NOT NULL,
    qualification_revision bigint NOT NULL,
    qualification_profile_revision text NOT NULL,
    observed_generation bigint NOT NULL CHECK (observed_generation > 0),
    observation_digest char(64) NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    current_binding_digest char(64) NOT NULL CHECK (current_binding_digest ~ '^[0-9a-f]{64}$'),
    binding_state text NOT NULL CHECK (binding_state IN ('CURRENT', 'STALE', 'UNKNOWN', 'REVOKED')),
    reason_code text NOT NULL,
    evaluated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, device_address),
    FOREIGN KEY (host_id, device_address)
        REFERENCES kim.host_pci_device_projections(host_id, device_address),
    FOREIGN KEY (qualification_id, qualification_revision)
        REFERENCES kim.pci_qualification_evidence(qualification_id, qualification_revision)
);

CREATE TABLE kim.pci_allocation_policy_bindings (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    policy_id text NOT NULL,
    policy_generation bigint NOT NULL CHECK (policy_generation > 0),
    policy_state text NOT NULL CHECK (policy_state IN ('ALLOWED', 'BLOCKED')),
    qualification_profile_revision text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, policy_id)
);

CREATE TABLE kim.pci_vf_allocation_claims (
    claim_id text PRIMARY KEY,
    host_id text NOT NULL,
    device_address text NOT NULL,
    project_id text NOT NULL,
    workload_id text NOT NULL,
    policy_id text NOT NULL,
    policy_generation bigint NOT NULL,
    host_capability_generation bigint NOT NULL,
    qualification_id text NOT NULL,
    qualification_revision bigint NOT NULL,
    claim_state text NOT NULL CHECK (claim_state IN ('ACTIVE', 'RELEASE_PENDING', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    released_at timestamptz,
    FOREIGN KEY (host_id, device_address)
        REFERENCES kim.host_pci_device_projections(host_id, device_address),
    FOREIGN KEY (qualification_id, qualification_revision)
        REFERENCES kim.pci_qualification_evidence(qualification_id, qualification_revision),
    FOREIGN KEY (host_id, policy_id)
        REFERENCES kim.pci_allocation_policy_bindings(host_id, policy_id)
);

CREATE UNIQUE INDEX pci_vf_one_active_claim
    ON kim.pci_vf_allocation_claims(host_id, device_address)
    WHERE claim_state IN ('ACTIVE', 'RELEASE_PENDING');

CREATE TRIGGER pci_qualification_evidence_no_update
    BEFORE UPDATE ON kim.pci_qualification_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_pci_device_projections IS
    'Rebuildable normalized PCI observation. Observation is not qualification or allocation authority.';
COMMENT ON TABLE kim.pci_qualification_evidence IS
    'Immutable certification evidence bound to one observed device and software-stack fingerprint.';
COMMENT ON TABLE kim.pci_qualification_bindings_current IS
    'Current binding decision. Only CURRENT may participate in allocation admission.';
COMMENT ON TABLE kim.pci_vf_allocation_claims IS
    'PostgreSQL authority for exclusive VF allocation after current observation and qualification revalidation.';
