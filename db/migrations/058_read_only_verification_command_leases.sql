ALTER TABLE kim.command_lease_grants
    ADD COLUMN authority_scope text NOT NULL DEFAULT 'MUTATION'
        CHECK (authority_scope IN ('MUTATION','READ_ONLY_VERIFICATION'));

ALTER TABLE kim.command_leases_current
    ADD COLUMN authority_scope text NOT NULL DEFAULT 'MUTATION'
        CHECK (authority_scope IN ('MUTATION','READ_ONLY_VERIFICATION'));

COMMENT ON COLUMN kim.command_lease_grants.authority_scope IS
    'MUTATION requires current ARMED Host authority. READ_ONLY_VERIFICATION is restricted to the closed SOURCE_ROOT_SAFETY_READ_BACK command on a current AUTHORIZED session while Host authority remains FENCED; it cannot rearm or authorize mutation.';

COMMENT ON COLUMN kim.command_leases_current.authority_scope IS
    'Current Lease validation scope. FENCED Host read-only verification remains distinct from mutation authority.';
