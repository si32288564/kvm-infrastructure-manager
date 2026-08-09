ALTER TABLE kim.pci_vf_allocation_claims
    ADD COLUMN placement_admission_id text
        REFERENCES kim.placement_admission_decisions(admission_id);

ALTER TABLE kim.placement_admission_decisions
    ADD COLUMN pci_requirements jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(pci_requirements) = 'array'),
    ADD COLUMN pci_requirements_digest char(64) NOT NULL DEFAULT '4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945'
        CHECK (pci_requirements_digest ~ '^[0-9a-f]{64}$');

-- The defaults above backfill pre-integration decisions. New authority rows must
-- always persist the explicit canonical requirements and matching digest.
ALTER TABLE kim.placement_admission_decisions
    ALTER COLUMN pci_requirements DROP DEFAULT,
    ALTER COLUMN pci_requirements_digest DROP DEFAULT;

CREATE UNIQUE INDEX pci_vf_one_claim_per_admission_device
    ON kim.pci_vf_allocation_claims(placement_admission_id, host_id, device_address)
    WHERE placement_admission_id IS NOT NULL;

COMMENT ON COLUMN kim.pci_vf_allocation_claims.placement_admission_id IS
    'Final Admission that atomically committed this VF claim. NULL is retained only for pre-integration qualification fixtures.';
COMMENT ON COLUMN kim.placement_admission_decisions.pci_requirements IS
    'Canonical typed PCI requirements fixed by Final Admission for audit and idempotency scope.';
