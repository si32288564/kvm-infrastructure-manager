ALTER TABLE kim.upgrade_campaigns_current
    ADD COLUMN coordinator_renewal_generation bigint NOT NULL DEFAULT 0
        CHECK (coordinator_renewal_generation >= 0);

CREATE TABLE kim.upgrade_coordinator_renewal_evidence (
    campaign_id text NOT NULL,
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    renewal_generation bigint NOT NULL CHECK (renewal_generation > 0),
    coordinator_owner text NOT NULL,
    prior_expires_at timestamptz NOT NULL,
    renewed_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    renewed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (campaign_id, claim_generation, renewal_generation),
    FOREIGN KEY (campaign_id, claim_generation)
        REFERENCES kim.upgrade_coordinator_attempt_evidence(campaign_id, claim_generation),
    CHECK (renewed_expires_at > prior_expires_at),
    CHECK (renewed_expires_at <= maximum_expires_at)
);

CREATE TRIGGER upgrade_coordinator_renewal_evidence_no_update
    BEFORE UPDATE ON kim.upgrade_coordinator_renewal_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.upgrade_coordinator_renewal_evidence IS
'Immutable DB-time coordinator claim renewal evidence. Renewal response loss does not roll back the committed expiry and never proves Campaign Target side-effect absence.';
