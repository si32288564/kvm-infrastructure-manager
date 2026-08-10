ALTER TABLE kim.vm_network_port_realization_evidence
    ADD COLUMN device_address text,
    ADD COLUMN vf_claim_id text REFERENCES kim.pci_vf_allocation_claims(claim_id),
    ADD COLUMN pci_observation_generation bigint CHECK (pci_observation_generation > 0),
    ADD COLUMN pci_observation_digest char(64) CHECK (pci_observation_digest ~ '^[0-9a-f]{64}$'),
    ADD COLUMN qualification_id text,
    ADD COLUMN qualification_revision bigint CHECK (qualification_revision > 0),
    ADD COLUMN policy_id text,
    ADD COLUMN policy_generation bigint CHECK (policy_generation > 0),
    ADD CONSTRAINT vm_network_port_realization_sriov_identity CHECK (
        (binding_type='OVS' AND device_address IS NULL AND vf_claim_id IS NULL
            AND pci_observation_generation IS NULL AND pci_observation_digest IS NULL
            AND qualification_id IS NULL AND qualification_revision IS NULL
            AND policy_id IS NULL AND policy_generation IS NULL)
        OR
        (binding_type='SRIOV_DIRECT' AND device_address IS NOT NULL AND vf_claim_id IS NOT NULL
            AND pci_observation_generation IS NOT NULL AND pci_observation_digest IS NOT NULL
            AND qualification_id IS NOT NULL AND qualification_revision IS NOT NULL
            AND policy_id IS NOT NULL AND policy_generation IS NOT NULL)
    );

COMMENT ON COLUMN kim.vm_network_port_realization_evidence.vf_claim_id IS
    'Exclusive PostgreSQL VF Claim authority used by this SRIOV_DIRECT realization evidence.';
