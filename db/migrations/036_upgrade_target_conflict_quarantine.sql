ALTER TABLE kim.upgrade_target_execution_event_evidence
    DROP CONSTRAINT upgrade_target_execution_event_evidence_event_type_check;

ALTER TABLE kim.upgrade_target_execution_event_evidence
    ADD CONSTRAINT upgrade_target_execution_event_evidence_event_type_check CHECK (event_type IN (
        'CLAIM_GRANTED', 'TARGET_UNKNOWN', 'READ_BACK_STARTED', 'APPLY_AUTHORIZED',
        'RESULT_ACCEPTED', 'STALE_RESULT_FENCED', 'CONFLICT_QUARANTINED'
    ));

COMMENT ON COLUMN kim.upgrade_target_executions_current.execution_state IS
'CONFLICTING typed read-back is terminal FENCED authority. Recovery requires a separate explicit plan; claim expiry or process restart cannot rearm it.';
