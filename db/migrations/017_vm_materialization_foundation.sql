CREATE TABLE kim.virtual_machines_current (
    vm_id uuid PRIMARY KEY,
    placement_admission_id text NOT NULL UNIQUE REFERENCES kim.placement_admission_decisions(admission_id),
    project_id text NOT NULL CHECK (length(project_id) BETWEEN 1 AND 255),
    workload_id text NOT NULL CHECK (length(workload_id) BETWEEN 1 AND 255),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    desired_power_state text NOT NULL CHECK (desired_power_state IN ('SHUTOFF', 'RUNNING')),
    lifecycle_state text NOT NULL CHECK (lifecycle_state IN (
        'MATERIALIZATION_PENDING', 'DEFINING', 'DEFINED', 'UNKNOWN',
        'BLOCKED', 'DELETE_PENDING', 'DELETED'
    )),
    current_plan_id text,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (project_id, workload_id),
    UNIQUE (vm_id, vm_generation)
);

CREATE TABLE kim.vm_materialization_plan_evidence (
    plan_id text PRIMARY KEY CHECK (length(plan_id) BETWEEN 1 AND 512),
    vm_id uuid NOT NULL,
    vm_generation bigint NOT NULL CHECK (vm_generation > 0),
    placement_admission_id text NOT NULL REFERENCES kim.placement_admission_decisions(admission_id),
    host_id text NOT NULL REFERENCES kim.host_identities(host_id),
    image_id text NOT NULL,
    image_revision bigint NOT NULL CHECK (image_revision > 0),
    flavor_id text NOT NULL,
    flavor_revision bigint NOT NULL CHECK (flavor_revision > 0),
    flavor_shape_digest char(64) NOT NULL CHECK (flavor_shape_digest ~ '^[0-9a-f]{64}$'),
    compute_allocation_id text NOT NULL REFERENCES kim.compute_allocation_claims(allocation_id),
    root_volume_id text NOT NULL REFERENCES kim.volumes_current(volume_id),
    root_binding_id text NOT NULL REFERENCES kim.volume_backend_bindings_current(binding_id),
    root_binding_generation bigint NOT NULL CHECK (root_binding_generation > 0),
    root_attachment_id text NOT NULL REFERENCES kim.volume_attachments_current(attachment_id),
    root_attachment_generation bigint NOT NULL CHECK (root_attachment_generation > 0),
    plan_payload jsonb NOT NULL CHECK (jsonb_typeof(plan_payload) = 'object'),
    plan_digest char(64) NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (vm_id, vm_generation)
        REFERENCES kim.virtual_machines_current(vm_id, vm_generation),
    FOREIGN KEY (image_id, image_revision)
        REFERENCES kim.image_revision_evidence(image_id, image_revision),
    FOREIGN KEY (flavor_id, flavor_revision)
        REFERENCES kim.flavor_revision_evidence(flavor_id, flavor_revision),
    UNIQUE (vm_id, vm_generation)
);

ALTER TABLE kim.virtual_machines_current
    ADD CONSTRAINT virtual_machines_current_plan_fkey
    FOREIGN KEY (current_plan_id) REFERENCES kim.vm_materialization_plan_evidence(plan_id);

CREATE TRIGGER vm_materialization_plan_evidence_no_update
    BEFORE UPDATE ON kim.vm_materialization_plan_evidence
    FOR EACH ROW EXECUTE FUNCTION kim.reject_immutable_evidence_update();

COMMENT ON TABLE kim.virtual_machines_current IS
    'Current VM desired authority. DEFINED does not imply Image materialization, Network realization, power-on, or guest readiness.';
COMMENT ON TABLE kim.vm_materialization_plan_evidence IS
    'Immutable plan derived only from an accepted Final Admission and current resource claims. The plan contains no caller-supplied XML, path, libvirt method, or flag.';
