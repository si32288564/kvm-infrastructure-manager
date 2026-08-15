-- Phase 3 VM aggregate multi-Port authority.  Port ordinals are canonicalized
-- by logical Port ID by the producer; physical bindings remain per-incarnation
-- evidence and every consumer must match the exact snapshot cardinality.
ALTER TABLE kim.vm_aggregate_mobility_association_evidence
    DROP CONSTRAINT vm_aggregate_mobility_association_evidence_port_count_check,
    ADD CONSTRAINT vm_aggregate_mobility_association_evidence_port_count_check
        CHECK(port_count BETWEEN 0 AND 2);

COMMENT ON COLUMN kim.vm_dependency_port_evidence.port_ordinal IS 'Dense zero-based ordinal after canonical logical Port ID ordering; never a Host interface order supplied by a caller.';
COMMENT ON COLUMN kim.vm_aggregate_mobility_association_evidence.port_count IS 'Exact immutable logical Port dependency cardinality, qualified for zero through two STANDARD Ports.';
