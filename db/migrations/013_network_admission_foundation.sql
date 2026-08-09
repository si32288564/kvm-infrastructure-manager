CREATE TABLE kim.networks_current (
    network_id text PRIMARY KEY CHECK (length(network_id) BETWEEN 1 AND 255),
    project_id text NOT NULL CHECK (length(project_id) BETWEEN 1 AND 255),
    network_generation bigint NOT NULL CHECK (network_generation > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DRAINING', 'DISABLED')),
    mtu integer NOT NULL CHECK (mtu BETWEEN 576 AND 9216),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE kim.network_subnets_current (
    subnet_id text PRIMARY KEY CHECK (length(subnet_id) BETWEEN 1 AND 255),
    network_id text NOT NULL REFERENCES kim.networks_current(network_id),
    subnet_generation bigint NOT NULL CHECK (subnet_generation > 0),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('ACTIVE', 'DRAINING', 'DISABLED')),
    cidr cidr NOT NULL,
    allocation_start inet NOT NULL,
    allocation_end inet NOT NULL,
    excluded_addresses inet[] NOT NULL DEFAULT '{}'::inet[],
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK (family(allocation_start) = family(cidr)),
    CHECK (family(allocation_end) = family(cidr)),
    CHECK (allocation_start <<= cidr),
    CHECK (allocation_end <<= cidr),
    CHECK (allocation_start <= allocation_end)
);

CREATE TABLE kim.network_segment_claims_current (
    segment_claim_id text PRIMARY KEY CHECK (length(segment_claim_id) BETWEEN 1 AND 255),
    network_id text NOT NULL REFERENCES kim.networks_current(network_id),
    segment_generation bigint NOT NULL CHECK (segment_generation > 0),
    segment_type text NOT NULL CHECK (segment_type IN ('VLAN', 'VNI')),
    scope_id text NOT NULL CHECK (length(scope_id) BETWEEN 1 AND 255),
    segment_id integer NOT NULL CHECK (
        (segment_type = 'VLAN' AND segment_id BETWEEN 1 AND 4094)
        OR (segment_type = 'VNI' AND segment_id BETWEEN 1 AND 16777215)
    ),
    provider_mapping_revision bigint NOT NULL CHECK (provider_mapping_revision > 0),
    claim_state text NOT NULL CHECK (claim_state IN ('ACTIVE', 'RELEASE_PENDING', 'QUARANTINED', 'RELEASED')),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE UNIQUE INDEX network_segment_one_active_scope_id
    ON kim.network_segment_claims_current(segment_type, scope_id, segment_id)
    WHERE claim_state IN ('ACTIVE', 'RELEASE_PENDING', 'QUARANTINED');

CREATE TABLE kim.host_network_mappings_current (
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    segment_claim_id text NOT NULL REFERENCES kim.network_segment_claims_current(segment_claim_id),
    mapping_generation bigint NOT NULL CHECK (mapping_generation > 0),
    mapping_state text NOT NULL CHECK (mapping_state IN ('CURRENT', 'STALE', 'UNKNOWN', 'BLOCKED')),
    maximum_mtu integer NOT NULL CHECK (maximum_mtu BETWEEN 576 AND 9216),
    supported_binding_types text[] NOT NULL CHECK (
        cardinality(supported_binding_types) > 0
        AND supported_binding_types <@ ARRAY['OVS', 'SRIOV_DIRECT']::text[]
    ),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (host_id, segment_claim_id)
);

ALTER TABLE kim.placement_admission_decisions
    ADD COLUMN network_requirements jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(network_requirements) = 'array'),
    ADD COLUMN network_requirements_digest char(64) NOT NULL DEFAULT '4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945'
        CHECK (network_requirements_digest ~ '^[0-9a-f]{64}$');

ALTER TABLE kim.placement_admission_decisions
    ALTER COLUMN network_requirements DROP DEFAULT,
    ALTER COLUMN network_requirements_digest DROP DEFAULT;

CREATE TABLE kim.network_ports_current (
    port_id text PRIMARY KEY CHECK (length(port_id) BETWEEN 1 AND 255),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    project_id text NOT NULL CHECK (length(project_id) BETWEEN 1 AND 255),
    workload_id text NOT NULL CHECK (length(workload_id) BETWEEN 1 AND 255),
    network_id text NOT NULL REFERENCES kim.networks_current(network_id),
    subnet_id text NOT NULL REFERENCES kim.network_subnets_current(subnet_id),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    desired_state text NOT NULL CHECK (desired_state IN ('RESERVED', 'BINDING', 'ACTIVE', 'RELEASE_PENDING', 'QUARANTINED', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (placement_admission_id, port_id)
);

CREATE TABLE kim.network_identity_claims (
    identity_claim_id text PRIMARY KEY CHECK (length(identity_claim_id) BETWEEN 1 AND 512),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    project_id text NOT NULL CHECK (length(project_id) BETWEEN 1 AND 255),
    network_id text NOT NULL REFERENCES kim.networks_current(network_id),
    subnet_id text NOT NULL REFERENCES kim.network_subnets_current(subnet_id),
    claim_type text NOT NULL CHECK (claim_type IN ('IP', 'MAC')),
    ip_address inet,
    mac_address macaddr,
    allocation_source text NOT NULL CHECK (allocation_source IN ('EXPLICIT', 'AUTOMATIC', 'EXTERNAL')),
    claim_generation bigint NOT NULL CHECK (claim_generation > 0),
    claim_state text NOT NULL CHECK (claim_state IN ('RESERVED', 'ACTIVE', 'RELEASE_PENDING', 'QUARANTINED', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK ((claim_type = 'IP' AND ip_address IS NOT NULL AND mac_address IS NULL)
        OR (claim_type = 'MAC' AND mac_address IS NOT NULL AND ip_address IS NULL)),
    UNIQUE (placement_admission_id, port_id, claim_type)
);

CREATE UNIQUE INDEX network_identity_one_active_ip
    ON kim.network_identity_claims(subnet_id, ip_address)
    WHERE claim_type = 'IP' AND claim_state IN ('RESERVED', 'ACTIVE', 'RELEASE_PENDING', 'QUARANTINED');
CREATE UNIQUE INDEX network_identity_one_active_mac
    ON kim.network_identity_claims(network_id, mac_address)
    WHERE claim_type = 'MAC' AND claim_state IN ('RESERVED', 'ACTIVE', 'RELEASE_PENDING', 'QUARANTINED');

CREATE TABLE kim.port_bindings_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    segment_claim_id text NOT NULL REFERENCES kim.network_segment_claims_current(segment_claim_id),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    binding_type text NOT NULL CHECK (binding_type IN ('OVS', 'SRIOV_DIRECT')),
    device_address text,
    binding_state text NOT NULL CHECK (binding_state IN ('RESERVED', 'BINDING', 'VERIFYING', 'ACTIVE', 'UNKNOWN', 'BLOCKED', 'RELEASE_PENDING', 'RELEASED')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK ((binding_type = 'SRIOV_DIRECT' AND device_address IS NOT NULL)
        OR (binding_type = 'OVS' AND device_address IS NULL)),
    UNIQUE (placement_admission_id, port_id)
);

COMMENT ON TABLE kim.network_identity_claims IS
    'PostgreSQL IP/MAC reservation authority. RELEASE_PENDING and QUARANTINED identities remain unavailable until verified absence and reuse policy completion.';
COMMENT ON TABLE kim.port_bindings_current IS
    'Desired Port binding authority reserved by Final Admission. It is not evidence of OVS, OVN, libvirt, or dataplane realization.';
COMMENT ON COLUMN kim.placement_admission_decisions.network_requirements IS
    'Canonical Network/Port/IP/MAC/Segment/Binding requirements fixed by Final Admission for audit and idempotency scope.';
COMMENT ON TABLE kim.compute_allocation_claims IS
    'PostgreSQL compute reservation authority committed atomically with qualified PCI VF and Network Port/IP/MAC/Binding claims. Storage and Quota join in later work packages.';
