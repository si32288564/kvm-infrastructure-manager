ALTER TABLE kim.ovn_runtime_work_current
    ADD COLUMN claim_maximum_expires_at timestamptz,
    ADD COLUMN last_renewal_generation bigint NOT NULL DEFAULT 0 CHECK (last_renewal_generation >= 0);

UPDATE kim.ovn_runtime_work_current
SET claim_maximum_expires_at = claim_expires_at
WHERE work_state = 'CLAIMED';

ALTER TABLE kim.ovn_runtime_work_current
    ADD CONSTRAINT ovn_runtime_work_claim_maximum_check CHECK (
        (work_state = 'CLAIMED' AND claim_maximum_expires_at IS NOT NULL AND claim_maximum_expires_at >= claim_expires_at)
        OR (work_state <> 'CLAIMED' AND claim_maximum_expires_at IS NULL)
    );

ALTER TABLE kim.ovn_runtime_work_attempt_evidence
    ADD COLUMN maximum_expires_at timestamptz;

UPDATE kim.ovn_runtime_work_attempt_evidence
SET maximum_expires_at = lease_expires_at;

ALTER TABLE kim.ovn_runtime_work_attempt_evidence
    ALTER COLUMN maximum_expires_at SET NOT NULL,
    ADD CONSTRAINT ovn_runtime_attempt_maximum_check CHECK (maximum_expires_at >= lease_expires_at);

CREATE TABLE kim.ovn_runtime_work_renewal_evidence (
    work_id text NOT NULL,
    claim_generation bigint NOT NULL,
    renewal_generation bigint NOT NULL CHECK (renewal_generation > 0),
    claim_owner text NOT NULL,
    prior_expires_at timestamptz NOT NULL,
    renewed_expires_at timestamptz NOT NULL,
    maximum_expires_at timestamptz NOT NULL,
    renewed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (work_id, claim_generation, renewal_generation),
    FOREIGN KEY (work_id, claim_generation)
        REFERENCES kim.ovn_runtime_work_attempt_evidence(work_id, claim_generation),
    CHECK (renewed_expires_at > prior_expires_at),
    CHECK (maximum_expires_at >= renewed_expires_at)
);

CREATE TRIGGER ovn_runtime_work_renewal_evidence_no_update
    BEFORE UPDATE ON kim.ovn_runtime_work_renewal_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.ovn_runtime_work_renewal_evidence IS
'Immutable renewal decisions for the same claim owner/generation. Renewal extends future authority only and cannot revive an expired claim or exceed maximum_expires_at.';
