# ADR-0034: Independent Volume capacity, materialization, and attachment authority

- Status: Accepted
- Date: 2026-08-15
- Scope: Migration 080 internal authority; no public API or Terraform surface

## Context

Before Migration 080, Final Admission created `volumes_current`, a capacity claim, one Local LVM binding, and one workload attachment together. Migrations 068–072 already prove Local LVM copy content identity, cross-Host transport, exact source cleanup, and capacity reclamation, but those histories are physical mobility consumers rather than a persistent backend-neutral Volume producer.

## Decision

KIM keeps stable `volume_id` and separates five authorities:

1. `volume_resource_revision_evidence` owns immutable Project, name, size, Storage Class, source intent, delete protection, and lifecycle revisions. Host, backend, VG/LV UUID, device path, binding, attachment, copy, and cleanup generation are excluded.
2. `volume_capacity_allocation_decision_evidence` records the exact KIM allocation decision against a current backend capacity observation. `storage_capacity_claims` remains the reserved/allocated ledger and is not free capacity.
3. `volume_materialization_*` owns a closed typed Local LVM create/read-back/retire pipeline. A response, exit status, reserved resource key, or LV name cannot establish convergence. Exact observed VG UUID, LV UUID, size, binding generation, and immutable Command Verification are required.
4. `volume_attachment_intent_*` separates a valid persistent unattached Volume from a workload request and the physical attachment created by Final Admission.
5. Existing copy, transport, content verification, relocation, cleanup, and reclamation evidence remains authoritative for physical mobility. Migration 080 does not duplicate or rewrite it.

Final Admission retains the historical `LEGACY_ADMISSION` producer. For `VOLUME_RESOURCE`, it is only a consumer: the request must name the exact current Volume revision, capacity allocation ID/generation, verified materialization/binding incarnation, and attachment intent/generation. The same reserved bytes are removed only from that Admission's incremental demand calculation; the capacity claim remains `RESERVED` or `ALLOCATED`. A concurrent consumer cannot acquire the same SINGLE_WRITER intent.

Retirement is ordered `RETIRE_PENDING -> typed delete/read-back -> exact ABSENT terminal -> capacity release evidence -> DELETED`. Logical deletion, physical deletion, and capacity reuse are not interchangeable.

## Consequences

- A Volume may be `AVAILABLE` and unattached.
- Safe metadata changes create new desired revisions; size is immutable until a separate expansion authority exists.
- BLANK and exact VERIFIED Image revision sources are closed source intents. An Image update never retrofits an existing Volume revision.
- Local LVM is `HOST_LOCAL` and planned-mobility-only; it is not unexpected Host-failure data HA.
- Backend incarnation changes are computed/internal and are not Terraform desired drift.
- `/api/v1/volumes`, Terraform `kim_volume`, a public Attachment resource, Ceph RBD, and VM Phase 3 remain out of scope.

## Alternatives rejected

- Adopting any existing Volume/LV by ID or name: existence is not authority.
- Returning a standalone reservation to free capacity during Admission: this permits overcommit and double ownership.
- Treating command success as materialization or deletion: response loss and partial side effects make that unsafe.
- Putting Host/VG/LV identity in desired state: relocation would become false desired drift.
- Replacing Migrations 068–072 evidence: their exact content/copy/cleanup history remains stronger than a new compatibility summary.
