-- Recovery terminal verification needs post-power holder evidence for the
-- root disk defined by VIRTUAL_MACHINE_DEFINE. Permit vda as an observation
-- identity without widening typed mutation authority: the Agent backend keeps
-- disk slot zero observation-only and rejects root attach/detach mutations.

ALTER TABLE kim.volume_attachment_observation_evidence
  DROP CONSTRAINT volume_attachment_observation_evidence_target_device_check,
  ADD CONSTRAINT volume_attachment_observation_evidence_target_device_check
    CHECK (target_device ~ '^vd[a-z]$');

ALTER TABLE kim.volume_attachment_observations_current
  DROP CONSTRAINT volume_attachment_observations_current_target_device_check,
  ADD CONSTRAINT volume_attachment_observations_current_target_device_check
    CHECK (target_device ~ '^vd[a-z]$');

COMMENT ON COLUMN kim.volume_attachment_observation_evidence.target_device IS
  'Typed libvirt disk identity. vda is accepted only for observation-only root verification; secondary attach/detach remains vdb-vdz.';
