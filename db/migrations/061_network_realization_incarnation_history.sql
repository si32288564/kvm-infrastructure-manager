ALTER TABLE kim.vm_network_port_realization_evidence
    DROP CONSTRAINT vm_network_port_realization_e_vm_id_vm_generation_port_id_o_key;

ALTER TABLE kim.vm_network_port_realization_evidence
    ADD CONSTRAINT vm_network_port_realization_incarnation_observation_key
    UNIQUE (vm_id, vm_generation, port_id, port_generation, binding_generation, observation_generation);

ALTER TABLE kim.vm_port_dataplane_observation_evidence
    DROP CONSTRAINT vm_port_dataplane_observation_vm_id_vm_generation_port_id_o_key;

ALTER TABLE kim.vm_port_dataplane_observation_evidence
    ADD CONSTRAINT vm_port_dataplane_incarnation_observation_key
    UNIQUE (vm_id, vm_generation, port_id, port_generation, binding_generation, observation_generation);

COMMENT ON CONSTRAINT vm_network_port_realization_incarnation_observation_key ON kim.vm_network_port_realization_evidence IS
    'One immutable pre-boot observation per exact Port/Binding incarnation generation; Recovery handoff preserves prior incarnation history.';
COMMENT ON CONSTRAINT vm_port_dataplane_incarnation_observation_key ON kim.vm_port_dataplane_observation_evidence IS
    'One immutable dataplane observation per exact Port/Binding incarnation generation; Recovery handoff preserves prior incarnation history.';
