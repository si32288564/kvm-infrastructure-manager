ALTER TABLE kim.pci_vf_allocation_claims
    ADD COLUMN allocation_generation bigint NOT NULL DEFAULT 1 CHECK (allocation_generation > 0),
    ADD COLUMN port_id text REFERENCES kim.network_ports_current(port_id),
    ADD COLUMN port_generation bigint CHECK (port_generation > 0),
    ADD COLUMN binding_generation bigint CHECK (binding_generation > 0),
    ADD CONSTRAINT pci_vf_claim_port_incarnation_complete CHECK (
        (port_id IS NULL AND port_generation IS NULL AND binding_generation IS NULL)
        OR (port_id IS NOT NULL AND port_generation IS NOT NULL AND binding_generation IS NOT NULL)
    );

ALTER TABLE kim.pci_vf_allocation_claims ALTER COLUMN allocation_generation DROP DEFAULT;
ALTER TABLE kim.pci_vf_allocation_claims
    ADD CONSTRAINT pci_vf_allocation_claim_exact_incarnation
    UNIQUE (claim_id,allocation_generation,host_id,device_address);

CREATE TABLE kim.pci_vf_retirement_operations_current (
    claim_id text NOT NULL,
    allocation_generation bigint NOT NULL CHECK (allocation_generation > 0),
    operation_id text NOT NULL UNIQUE,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    source_host_id text NOT NULL,
    device_address text NOT NULL,
    port_id text NOT NULL,
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    workload_id text NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    ownership_marker char(64) NOT NULL CHECK (ownership_marker ~ '^[0-9a-f]{64}$'),
    operation_state text NOT NULL CHECK (operation_state IN
        ('PENDING','CLAIMED','DISPATCH_UNKNOWN','VERIFIED','CONFLICTING','STALE')),
    claim_owner text,
    claim_generation bigint,
    last_claim_generation bigint NOT NULL DEFAULT 0 CHECK (last_claim_generation >= 0),
    claim_expires_at timestamptz,
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (claim_id,allocation_generation),
    FOREIGN KEY (claim_id,allocation_generation,source_host_id,device_address)
        REFERENCES kim.pci_vf_allocation_claims(claim_id,allocation_generation,host_id,device_address),
    CHECK ((claim_owner IS NULL)=(claim_generation IS NULL)),
    CHECK ((claim_owner IS NULL)=(claim_expires_at IS NULL))
);

CREATE TABLE kim.pci_vf_retirement_attempt_evidence (
    claim_id text NOT NULL,
    allocation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    claim_owner text NOT NULL,
    claim_mode text NOT NULL CHECK (claim_mode IN ('APPLY_ALLOWED','READ_BACK_FIRST')),
    lease_expires_at timestamptz NOT NULL,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (claim_id,allocation_generation,claim_generation),
    FOREIGN KEY (claim_id,allocation_generation)
        REFERENCES kim.pci_vf_retirement_operations_current(claim_id,allocation_generation)
);

CREATE TABLE kim.pci_vf_retirement_evidence (
    evidence_id text PRIMARY KEY,
    claim_id text NOT NULL,
    allocation_generation bigint NOT NULL,
    claim_generation bigint NOT NULL,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    source_host_id text NOT NULL,
    device_address text NOT NULL,
    port_id text NOT NULL,
    port_generation bigint NOT NULL,
    binding_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL,
    ownership_marker char(64) NOT NULL CHECK (ownership_marker ~ '^[0-9a-f]{64}$'),
    ownership_marker_matches boolean NOT NULL,
    source_domain_not_running boolean NOT NULL,
    source_hostdev_absent boolean NOT NULL,
    vf_driver_released boolean NOT NULL,
    vf_holder_absent boolean NOT NULL,
    iommu_group_matches boolean NOT NULL,
    pci_observation_generation bigint NOT NULL CHECK (pci_observation_generation > 0),
    pci_observation_digest char(64) NOT NULL CHECK (pci_observation_digest ~ '^[0-9a-f]{64}$'),
    libvirt_observation_generation bigint NOT NULL CHECK (libvirt_observation_generation > 0),
    libvirt_observation_digest char(64) NOT NULL CHECK (libvirt_observation_digest ~ '^[0-9a-f]{64}$'),
    command_id text NOT NULL REFERENCES kim.execution_commands(command_id),
    attempt_index integer NOT NULL CHECK (attempt_index > 0),
    verification_id text NOT NULL,
    verifier_digest char(64) NOT NULL CHECK (verifier_digest ~ '^[0-9a-f]{64}$'),
    apply_response_state text NOT NULL CHECK (apply_response_state IN ('RECEIVED','LOST','UNKNOWN')),
    result_state text NOT NULL CHECK (result_state IN ('VERIFIED','CONFLICTING','UNKNOWN')),
    evidence_digest char(64) NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (claim_id,allocation_generation,claim_generation)
        REFERENCES kim.pci_vf_retirement_attempt_evidence(claim_id,allocation_generation,claim_generation),
    FOREIGN KEY (verification_id)
        REFERENCES kim.command_verification_evidence(verification_id),
    UNIQUE (evidence_id,claim_id,allocation_generation)
);

ALTER TABLE kim.pci_vf_retirement_operations_current
    ADD CONSTRAINT pci_vf_retirement_terminal_fk
    FOREIGN KEY (terminal_evidence_id,claim_id,allocation_generation)
        REFERENCES kim.pci_vf_retirement_evidence(evidence_id,claim_id,allocation_generation);

CREATE TABLE kim.pci_vf_retirement_latest_current (
    claim_id text PRIMARY KEY,
    allocation_generation bigint NOT NULL,
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL,
    operation_state text NOT NULL CHECK (operation_state IN
        ('PENDING','CLAIMED','DISPATCH_UNKNOWN','VERIFIED','CONFLICTING','STALE')),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (claim_id,allocation_generation)
        REFERENCES kim.pci_vf_retirement_operations_current(claim_id,allocation_generation)
);

CREATE TABLE kim.pci_vf_handoff_evidence (
    handoff_id text PRIMARY KEY,
    workload_id text NOT NULL,
    port_id text NOT NULL,
    port_generation bigint NOT NULL,
    source_claim_id text NOT NULL,
    source_allocation_generation bigint NOT NULL,
    source_host_id text NOT NULL,
    source_device_address text NOT NULL,
    source_retirement_evidence_id text NOT NULL,
    destination_claim_id text NOT NULL,
    destination_allocation_generation bigint NOT NULL,
    destination_host_id text NOT NULL,
    destination_device_address text NOT NULL,
    destination_admission_id text NOT NULL,
    handoff_state text NOT NULL CHECK (handoff_state IN ('DESTINATION_RESERVED','DESTINATION_REALIZED','VERIFIED','STALE')),
    handoff_digest char(64) NOT NULL CHECK (handoff_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (source_retirement_evidence_id,source_claim_id,source_allocation_generation)
        REFERENCES kim.pci_vf_retirement_evidence(evidence_id,claim_id,allocation_generation),
    FOREIGN KEY (destination_claim_id,destination_allocation_generation,destination_host_id,destination_device_address)
        REFERENCES kim.pci_vf_allocation_claims(claim_id,allocation_generation,host_id,device_address),
    CHECK (source_host_id<>destination_host_id),
    CHECK (source_claim_id<>destination_claim_id)
);

CREATE TABLE kim.pci_vf_handoffs_current (
    handoff_id text PRIMARY KEY REFERENCES kim.pci_vf_handoff_evidence(handoff_id),
    workload_id text NOT NULL,
    port_id text NOT NULL,
    port_generation bigint NOT NULL,
    source_claim_id text NOT NULL,
    source_allocation_generation bigint NOT NULL,
    destination_claim_id text NOT NULL,
    destination_allocation_generation bigint NOT NULL,
    handoff_state text NOT NULL CHECK (handoff_state IN ('DESTINATION_RESERVED','DESTINATION_REALIZED','VERIFIED','STALE')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TRIGGER pci_vf_retirement_attempt_evidence_no_update BEFORE UPDATE ON kim.pci_vf_retirement_attempt_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER pci_vf_retirement_evidence_no_update BEFORE UPDATE ON kim.pci_vf_retirement_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();
CREATE TRIGGER pci_vf_handoff_evidence_no_update BEFORE UPDATE ON kim.pci_vf_handoff_evidence FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

ALTER TABLE kim.recovery_verification_evidence
    ADD COLUMN pci_observation_generation bigint CHECK (pci_observation_generation > 0),
    ADD COLUMN pci_evidence_set_digest char(64) CHECK (pci_evidence_set_digest ~ '^[0-9a-f]{64}$');

COMMENT ON TABLE kim.pci_vf_retirement_operations_current IS 'Generic exact-incarnation VF retirement work authority. Lease expiry is not proof of detach or physical release.';
COMMENT ON TABLE kim.pci_vf_retirement_evidence IS 'Immutable libvirt plus physical VF read-back. Command success alone cannot produce VERIFIED.';
COMMENT ON TABLE kim.pci_vf_handoff_evidence IS 'Generic VF ownership handoff. It preserves workload/Port identity while source and destination allocation incarnations remain distinct.';
COMMENT ON COLUMN kim.recovery_verification_evidence.pci_evidence_set_digest IS 'Exact current destination VF allocation, generic source-retirement, handoff, and libvirt hostdev realization evidence-set digest; NULL only for zero-PCI Recovery.';
