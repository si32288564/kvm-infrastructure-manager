-- Cleanup origin eligibility is a producer adapter.  Each origin domain must
-- first validate its own terminal/current authority, then immutably bind that
-- result to the exact generic cleanup operation.  Generic cleanup consumers
-- do not reach back into one producer's projection.
CREATE TABLE kim.backend_cleanup_origin_eligibility_evidence (
    origin_eligibility_id text PRIMARY KEY,
    cleanup_operation_id text NOT NULL,
    cleanup_generation bigint NOT NULL CHECK(cleanup_generation>0),
    origin_authority_type text NOT NULL CHECK(origin_authority_type IN ('RECOVERY_TERMINAL','MATERIALIZATION','DELETE_OPERATION')),
    origin_authority_id text NOT NULL CHECK(origin_authority_id<>''),
    origin_authority_state text NOT NULL CHECK(origin_authority_state='ACCEPTED'),
    producer_type text NOT NULL CHECK(producer_type IN ('RECOVERY','MATERIALIZATION','DELETE')),
    producer_evidence_digest char(64) NOT NULL CHECK(producer_evidence_digest ~ '^[0-9a-f]{64}$'),
    eligibility_digest char(64) NOT NULL CHECK(eligibility_digest ~ '^[0-9a-f]{64}$'),
    evidence_digest char(64) NOT NULL CHECK(evidence_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(cleanup_operation_id,cleanup_generation),
    UNIQUE(origin_authority_type,origin_authority_id,cleanup_operation_id,cleanup_generation),
    UNIQUE(origin_eligibility_id,evidence_digest),
    FOREIGN KEY(cleanup_operation_id,cleanup_generation,eligibility_digest)
      REFERENCES kim.backend_cleanup_operation_evidence(cleanup_operation_id,cleanup_generation,eligibility_digest),
    CHECK((origin_authority_type='RECOVERY_TERMINAL' AND producer_type='RECOVERY')
       OR (origin_authority_type='MATERIALIZATION' AND producer_type='MATERIALIZATION')
       OR (origin_authority_type='DELETE_OPERATION' AND producer_type='DELETE'))
);

ALTER TABLE kim.backend_cleanup_observation_evidence
  DROP CONSTRAINT backend_cleanup_observation_evidence_result_state_check,
  ADD CONSTRAINT backend_cleanup_observation_evidence_result_state_check
  CHECK(result_state IN ('ABSENT','PRESENT','UNKNOWN','CONFLICTING','ALREADY_ABSENT','BLOCKED'));

CREATE TRIGGER backend_cleanup_origin_eligibility_evidence_no_update
BEFORE UPDATE ON kim.backend_cleanup_origin_eligibility_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.backend_cleanup_origin_eligibility_evidence IS
'Immutable producer adapter into generic cleanup. Recovery is the only implemented producer; future producers require their own closed authority validation before inserting this evidence.';
COMMENT ON COLUMN kim.backend_cleanup_observation_evidence.result_state IS
'PRESENT is a non-terminal observation used to authorize apply only after a READ_BACK_FIRST successor has proved the exact inactive incarnation still exists.';
