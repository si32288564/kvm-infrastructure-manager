CREATE TABLE kim.host_evacuation_source_network_retirement_authority_evidence (
    retirement_authority_id text PRIMARY KEY,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    source_plan_id text NOT NULL REFERENCES kim.vm_materialization_plan_evidence(plan_id),
    source_materialization_generation bigint NOT NULL CHECK(source_materialization_generation>0),
    planned_quiescence_evidence_id text NOT NULL REFERENCES kim.planned_source_quiescence_evidence(quiescence_evidence_id),
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    source_port_generation bigint NOT NULL CHECK(source_port_generation>0),
    source_binding_generation bigint NOT NULL CHECK(source_binding_generation>0),
    retirement_operation_id text NOT NULL UNIQUE,
    retirement_operation_generation bigint NOT NULL CHECK(retirement_operation_generation>0),
    retirement_intent_id text NOT NULL,
    retirement_intent_generation bigint NOT NULL CHECK(retirement_intent_generation>0),
    authority_digest char(64) NOT NULL CHECK(authority_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(child_operation_id,child_generation,port_id),
    FOREIGN KEY(child_operation_id,child_generation)
      REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation)
);

CREATE TABLE kim.host_evacuation_child_network_evidence_binding (
    network_binding_id text PRIMARY KEY,
    verification_id text NOT NULL,
    child_operation_id text NOT NULL,
    child_generation bigint NOT NULL,
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK(vm_generation>0),
    network_id text NOT NULL,
    subnet_id text NOT NULL,
    port_id text NOT NULL REFERENCES kim.network_ports_current(port_id),
    mac_address macaddr NOT NULL,
    ip_address inet NOT NULL,
    source_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    destination_host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    source_port_generation bigint NOT NULL CHECK(source_port_generation>0),
    source_binding_generation bigint NOT NULL CHECK(source_binding_generation>0),
    destination_port_generation bigint NOT NULL CHECK(destination_port_generation=source_port_generation+1),
    destination_binding_generation bigint NOT NULL CHECK(destination_binding_generation=source_binding_generation+1),
    source_retirement_evidence_id text NOT NULL REFERENCES kim.network_port_binding_retirement_evidence(evidence_id),
    source_quiescence_evidence_id text NOT NULL REFERENCES kim.network_port_source_quiescence_evidence(evidence_id),
    handoff_id text NOT NULL REFERENCES kim.port_binding_handoff_evidence(handoff_id),
    destination_realization_evidence_id text NOT NULL REFERENCES kim.vm_network_port_realization_evidence(evidence_id),
    destination_nb_observation_id text NOT NULL REFERENCES kim.ovn_nb_observation_evidence(observation_id),
    destination_sb_observation_id text NOT NULL REFERENCES kim.ovn_sb_observation_evidence(observation_id),
    destination_dataplane_evidence_id text NOT NULL REFERENCES kim.vm_port_dataplane_observation_evidence(evidence_id),
    source_evidence_digest char(64) NOT NULL CHECK(source_evidence_digest ~ '^[0-9a-f]{64}$'),
    destination_evidence_digest char(64) NOT NULL CHECK(destination_evidence_digest ~ '^[0-9a-f]{64}$'),
    evidence_set_digest char(64) NOT NULL CHECK(evidence_set_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(verification_id,port_id),
    UNIQUE(child_operation_id,child_generation,port_id),
    FOREIGN KEY(child_operation_id,child_generation)
      REFERENCES kim.host_evacuation_workload_evidence(child_operation_id,child_generation),
    CHECK(source_host_id<>destination_host_id)
);

ALTER TABLE kim.host_evacuation_child_verification_evidence
  ADD COLUMN network_binding_count integer NOT NULL DEFAULT 0 CHECK(network_binding_count>=0),
  ADD COLUMN source_network_evidence_set_digest char(64),
  ADD COLUMN destination_network_evidence_set_digest char(64),
  ADD CONSTRAINT host_evacuation_child_network_digest_shape CHECK(
    (network_binding_count=0 AND source_network_state='NOT_REQUIRED' AND destination_network_state='NOT_REQUIRED'
      AND source_network_evidence_set_digest IS NULL AND destination_network_evidence_set_digest IS NULL)
    OR
    (network_binding_count>0 AND source_network_state='RETIRED' AND destination_network_state='CURRENT'
      AND source_network_evidence_set_digest ~ '^[0-9a-f]{64}$'
      AND destination_network_evidence_set_digest ~ '^[0-9a-f]{64}$')
  );

CREATE TRIGGER host_evacuation_source_network_retirement_authority_evidence_no_update
BEFORE UPDATE ON kim.host_evacuation_source_network_retirement_authority_evidence
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

CREATE TRIGGER host_evacuation_child_network_evidence_binding_no_update
BEFORE UPDATE ON kim.host_evacuation_child_network_evidence_binding
FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.host_evacuation_source_network_retirement_authority_evidence IS
'Planned-EVACUATE consumer authorization for one exact generic OVN binding-retirement operation. It derives source SHUTOFF/planned quiescence and creates no Network mutation primitive.';
COMMENT ON TABLE kim.host_evacuation_child_network_evidence_binding IS
'Immutable exact Port identity and source retirement/quiescence/Handoff plus destination NB/SB/preboot/OVS dataplane provenance consumed by Host EVACUATE child verification.';
