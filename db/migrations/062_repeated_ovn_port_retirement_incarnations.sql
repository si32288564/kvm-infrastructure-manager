ALTER TABLE kim.network_port_binding_retirements_current
    DROP CONSTRAINT network_port_binding_retirements_current_pkey;

ALTER TABLE kim.network_port_binding_retirements_current
    ADD CONSTRAINT network_port_binding_retirements_current_pkey
    PRIMARY KEY (port_id, port_generation, binding_generation);

ALTER TABLE kim.network_port_binding_retirement_evidence
    ADD CONSTRAINT network_port_binding_retirement_evidence_exact_incarnation_key
    UNIQUE (evidence_id, port_id, port_generation, binding_generation);

ALTER TABLE kim.network_port_binding_retirements_current
    DROP CONSTRAINT network_port_binding_retirement_terminal_fk,
    ADD CONSTRAINT network_port_binding_retirement_terminal_fk
    FOREIGN KEY (terminal_evidence_id, port_id, port_generation, binding_generation)
        REFERENCES kim.network_port_binding_retirement_evidence(
            evidence_id, port_id, port_generation, binding_generation
        );

CREATE TABLE kim.network_port_binding_retirement_latest_current (
    port_id text PRIMARY KEY REFERENCES kim.network_ports_current(port_id),
    port_generation bigint NOT NULL CHECK (port_generation > 0),
    binding_generation bigint NOT NULL CHECK (binding_generation > 0),
    operation_id text NOT NULL,
    operation_generation bigint NOT NULL CHECK (operation_generation > 0),
    retirement_state text NOT NULL CHECK (
        retirement_state IN ('PENDING','CLAIMED','DISPATCH_UNKNOWN','VERIFIED','CONFLICTING','STALE')
    ),
    terminal_evidence_id text,
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (port_id,port_generation,binding_generation)
        REFERENCES kim.network_port_binding_retirements_current(port_id,port_generation,binding_generation),
    FOREIGN KEY (terminal_evidence_id,port_id,port_generation,binding_generation)
        REFERENCES kim.network_port_binding_retirement_evidence(
            evidence_id,port_id,port_generation,binding_generation
        )
);

INSERT INTO kim.network_port_binding_retirement_latest_current(
    port_id,port_generation,binding_generation,operation_id,operation_generation,
    retirement_state,terminal_evidence_id,updated_at
)
SELECT DISTINCT ON (port_id)
    port_id,port_generation,binding_generation,operation_id,operation_generation,
    retirement_state,terminal_evidence_id,updated_at
FROM kim.network_port_binding_retirements_current
ORDER BY port_id,port_generation DESC,binding_generation DESC,operation_generation DESC;

COMMENT ON TABLE kim.network_port_binding_retirements_current IS
'Rebuildable exact-incarnation OVN binding-retirement projections. Multiple Port/Binding generations remain independently addressable and never replace immutable retirement evidence.';
COMMENT ON TABLE kim.network_port_binding_retirement_latest_current IS
'Rebuildable latest retirement projection per logical Port. It is explanatory convenience, not a substitute for exact Port/Binding incarnation checks.';
