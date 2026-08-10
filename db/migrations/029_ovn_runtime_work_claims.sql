CREATE TABLE kim.ovn_runtime_work_current (
    work_id text PRIMARY KEY,
    intent_id text NOT NULL,
    intent_generation bigint NOT NULL CHECK (intent_generation > 0),
    port_id text NOT NULL,
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    object_set_digest char(64) NOT NULL CHECK (object_set_digest ~ '^[0-9a-f]{64}$'),
    work_state text NOT NULL CHECK (
        work_state IN ('PENDING', 'CLAIMED', 'DISPATCH_UNKNOWN', 'OBSERVED', 'CONFLICTING', 'SUPERSEDED')
    ),
    last_claim_generation bigint NOT NULL DEFAULT 0 CHECK (last_claim_generation >= 0),
    claim_owner text,
    claim_generation bigint CHECK (claim_generation > 0),
    claim_expires_at timestamptz,
    attempt_count bigint NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    terminal_observation_id text,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (intent_id, intent_generation),
    FOREIGN KEY (intent_id, intent_generation)
        REFERENCES kim.network_intent_revision_evidence(intent_id, intent_generation),
    CHECK (
        (work_state = 'CLAIMED' AND claim_owner IS NOT NULL AND claim_generation IS NOT NULL AND claim_expires_at IS NOT NULL)
        OR (work_state <> 'CLAIMED' AND claim_owner IS NULL AND claim_generation IS NULL AND claim_expires_at IS NULL)
    )
);

CREATE TABLE kim.ovn_runtime_work_attempt_evidence (
    work_id text NOT NULL REFERENCES kim.ovn_runtime_work_current(work_id),
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    claim_owner text NOT NULL,
    claim_mode text NOT NULL CHECK (claim_mode IN ('APPLY_ALLOWED', 'READ_BACK_FIRST')),
    lease_expires_at timestamptz NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (work_id, claim_generation)
);

CREATE TABLE kim.ovn_runtime_work_event_evidence (
    work_id text NOT NULL,
    claim_generation bigint NOT NULL,
    event_type text NOT NULL CHECK (
        event_type IN ('CLAIM_GRANTED', 'READ_BACK_STARTED', 'APPLY_AUTHORIZED', 'DISPATCH_UNKNOWN', 'OBSERVATION_ACCEPTED', 'CONFLICT_QUARANTINED', 'STALE_RESULT_FENCED')
    ),
    event_payload jsonb NOT NULL,
    event_payload_digest char(64) NOT NULL CHECK (event_payload_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (work_id, claim_generation, event_type),
    FOREIGN KEY (work_id, claim_generation)
        REFERENCES kim.ovn_runtime_work_attempt_evidence(work_id, claim_generation)
);

CREATE INDEX ovn_runtime_work_claimable_idx
    ON kim.ovn_runtime_work_current(work_state, claim_expires_at, created_at, work_id)
    WHERE work_state IN ('PENDING', 'CLAIMED', 'DISPATCH_UNKNOWN');

INSERT INTO kim.ovn_runtime_work_current(
    work_id,intent_id,intent_generation,port_id,port_generation,binding_generation,
    object_set_digest,work_state,terminal_observation_id
)
SELECT 'ovn-runtime:' || current.intent_id || ':' || current.intent_generation::text,
    current.intent_id,current.intent_generation,current.port_id,current.port_generation,current.binding_generation,
    intent.object_set_digest,
    CASE current.layer_status
        WHEN 'SB_REALIZED' THEN 'OBSERVED'
        WHEN 'CONFLICTING' THEN 'CONFLICTING'
        ELSE 'PENDING'
    END,
    CASE WHEN current.layer_status IN ('SB_REALIZED','CONFLICTING') THEN current.sb_observation_id ELSE NULL END
FROM kim.network_ovn_state_current current
JOIN kim.network_intent_revision_evidence intent
  ON intent.intent_id=current.intent_id AND intent.intent_generation=current.intent_generation;

CREATE TRIGGER ovn_runtime_work_attempt_evidence_no_update
    BEFORE UPDATE ON kim.ovn_runtime_work_attempt_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

CREATE TRIGGER ovn_runtime_work_event_evidence_no_update
    BEFORE UPDATE ON kim.ovn_runtime_work_event_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.ovn_runtime_work_current IS
'Mutable PostgreSQL authority for OVN runtime work ownership. Claim expiry permits read-back-first recovery but never proves that OVN apply did not occur.';
COMMENT ON TABLE kim.ovn_runtime_work_attempt_evidence IS
'Immutable worker claim evidence. READ_BACK_FIRST is mandatory after an uncertain or expired prior claim.';
COMMENT ON TABLE kim.ovn_runtime_work_event_evidence IS
'Append-only OVN runtime dispatch evidence. DISPATCH_UNKNOWN is not converted to non-execution by time alone.';
