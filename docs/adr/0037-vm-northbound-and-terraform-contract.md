# ADR-0037: VM public contract consumes the verified aggregate authority

- Status: Accepted
- Date: 2026-08-15

## Decision

`/api/v1/vms` and Terraform `kim_vm` expose the logical VM aggregate from ADR-0036. They do not serialize `virtual_machines_current` and do not expose Admission, Host, materialization plan, Port binding, storage backend/LV, Recovery, or EVACUATE incarnation identities.

The first public create profile is bounded to zero through two STANDARD logical Ports, exactly one ROOT Volume plus at most one DATA Volume, no PCI, and initial desired power `RUNNING`. Dependency references are exact immutable revisions and Port/DATA sets are canonicalized. Flavor, Image, Availability Policy, Placement Scope, Port set, ROOT, and DATA set changes are replacement boundaries. Metadata and delete protection are synchronous logical revisions. `RUNNING`/`SHUTOFF` is a separate asynchronous power Operation.

Public delete is narrower than create. It is accepted only for the Migration 087 qualified profile: zero Ports, exactly one ROOT Volume, no PCI, delete protection disabled, and exact observed `SHUTOFF`. Terraform first requests typed SHUTOFF convergence and then requests verified deletion. Port-attached or DATA-Volume delete remains fail-closed.

Migration 088 adds only immutable authenticated Create replay binding. All Placement, execution, materialization, power, delete, Recovery, and EVACUATE authority remains in Migrations 082–087 and their existing consumers.

## Consequences

- Recovery/EVACUATE physical changes do not alter logical VM desired state or Terraform state.
- `If-Match` fences update/delete; stale callers never overwrite current authority.
- Operation `SUCCEEDED` requires the applicable immutable aggregate/power/delete terminal.
- Provider import accepts only `vm/<uuid>` and cannot adopt backend Domains.
- Multi-Port/multi-Volume mobility and delete remain separate qualification gates.
