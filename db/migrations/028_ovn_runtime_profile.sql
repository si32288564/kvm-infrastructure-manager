ALTER TABLE kim.host_network_mappings_current
    ADD COLUMN ovn_chassis_name text;

ALTER TABLE kim.network_intent_revision_evidence
    DROP CONSTRAINT network_intent_revision_evidence_schema_version_check;

ALTER TABLE kim.network_intent_revision_evidence
    ADD CONSTRAINT network_intent_revision_evidence_schema_version_check
    CHECK (schema_version IN ('kim.network-intent.ovn-port/v1', 'kim.network-intent.ovn-port/v2'));

COMMENT ON COLUMN kim.host_network_mappings_current.ovn_chassis_name IS
'Current typed OVN Chassis binding reference for this Host/Segment mapping. It is derived from the mapping authority and is not supplied by a Port Command.';
