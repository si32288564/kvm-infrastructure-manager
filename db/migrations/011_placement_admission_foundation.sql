CREATE TABLE kim.placement_pools_current (
    pool_id text PRIMARY KEY CHECK (length(pool_id) BETWEEN 1 AND 255),
    pool_generation bigint NOT NULL CHECK (pool_generation > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DRAINING', 'DISABLED')),
    policy_id text NOT NULL,
    policy_generation bigint NOT NULL CHECK (policy_generation > 0),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.host_placement_pool_memberships_current (
    host_id text PRIMARY KEY REFERENCES kim.host_identities(host_id),
    pool_id text NOT NULL REFERENCES kim.placement_pools_current(pool_id),
    membership_generation bigint NOT NULL CHECK (membership_generation > 0),
    membership_state text NOT NULL CHECK (membership_state IN ('ACTIVE', 'STALE', 'BLOCKED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.placement_admission_decisions (
    admission_id text PRIMARY KEY,
    request_id text NOT NULL,
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    evaluation_digest char(64) NOT NULL CHECK (evaluation_digest ~ '^[0-9a-f]{64}$'),
    project_id text NOT NULL,
    workload_id text NOT NULL,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    pool_id text NOT NULL REFERENCES kim.placement_pools_current(pool_id),
    pool_generation bigint NOT NULL,
    pool_policy_id text NOT NULL,
    pool_policy_generation bigint NOT NULL,
    membership_generation bigint NOT NULL,
    image_id text NOT NULL,
    image_revision bigint NOT NULL,
    flavor_id text NOT NULL,
    flavor_revision bigint NOT NULL,
    flavor_shape_digest char(64) NOT NULL CHECK (flavor_shape_digest ~ '^[0-9a-f]{64}$'),
    capability_generation bigint NOT NULL,
    baseline_assignment_generation bigint NOT NULL,
    preflight_generation bigint NOT NULL,
    compliance_generation bigint NOT NULL,
    decision_state text NOT NULL CHECK (decision_state = 'ACCEPTED'),
    explanation jsonb NOT NULL CHECK (jsonb_typeof(explanation) = 'object'),
    decided_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (request_id),
    FOREIGN KEY (image_id, image_revision)
        REFERENCES kim.image_revision_evidence(image_id, image_revision),
    FOREIGN KEY (flavor_id, flavor_revision)
        REFERENCES kim.flavor_revision_evidence(flavor_id, flavor_revision)
);

CREATE TABLE kim.compute_allocation_claims (
    allocation_id text PRIMARY KEY,
    admission_id text NOT NULL UNIQUE REFERENCES kim.placement_admission_decisions(admission_id),
    request_id text NOT NULL UNIQUE,
    request_digest char(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    project_id text NOT NULL,
    workload_id text NOT NULL,
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    pool_id text NOT NULL REFERENCES kim.placement_pools_current(pool_id),
    vcpus integer NOT NULL CHECK (vcpus > 0),
    memory_mib bigint NOT NULL CHECK (memory_mib > 0),
    hugepage_size_kib bigint,
    hugepage_pages bigint NOT NULL DEFAULT 0 CHECK (hugepage_pages >= 0),
    cpu_allocation text NOT NULL CHECK (cpu_allocation IN ('SHARED', 'DEDICATED')),
    claim_state text NOT NULL CHECK (claim_state IN ('RESERVED', 'ALLOCATED', 'RELEASE_PENDING', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    released_at timestamptz,
    CHECK ((hugepage_size_kib IS NULL AND hugepage_pages = 0) OR (hugepage_size_kib > 0 AND hugepage_pages > 0))
);

CREATE UNIQUE INDEX compute_allocation_one_active_workload
    ON kim.compute_allocation_claims(project_id, workload_id)
    WHERE claim_state IN ('RESERVED', 'ALLOCATED', 'RELEASE_PENDING');
CREATE INDEX compute_allocation_host_capacity
    ON kim.compute_allocation_claims(host_id, claim_state);

CREATE TRIGGER placement_admission_decisions_no_update
    BEFORE UPDATE ON kim.placement_admission_decisions
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_placement_pool_memberships_current IS
    'One authoritative Placement Pool per Host for the Phase 1 foundation. Missing or ambiguous membership fails closed.';
COMMENT ON TABLE kim.placement_admission_decisions IS
    'Immutable accepted Final Admission decision. It is committed atomically with the compute claim and causes no backend side effect.';
COMMENT ON TABLE kim.compute_allocation_claims IS
    'PostgreSQL compute reservation authority. Network, Storage, and PCI claims join the same Final Admission transaction in later work packages.';
