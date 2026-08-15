# ADR-0036: VM is a logical aggregate over physical incarnations

- Status: Accepted
- Date: 2026-08-15

## Context

Phase 2 exposes Project、Flavor、Image、Availability Policy、Network、Subnet、Port、Volume as logical Northbound/Terraform resources. The remaining VM boundary is different: it must compose exact dependency revisions with Placement、Final Admission、Materialization、Power、Recovery、and planned EVACUATE.

Migration 017 `virtual_machines_current` is an internal runtime projection. It stores the current Admission、Host、plan、desired power、and lifecycle together and is created by materialization after Final Admission. Publishing it directly would make physical incarnation part of logical desired state and would provide no immutable public VM revision、dependency snapshot、aggregate Operation、or delete terminal.

## Decision

Phase 3 will introduce a Project-owned logical VM resource whose revision and runtime-affecting intent generation are distinct from VM、Admission、materialization、binding、and observation generations.

The VM aggregate will snapshot exact Flavor、Image、Availability Policy、Placement Scope、Port、and Volume revisions. A KIM compiler—not the caller—will derive Placement requirements. Existing Final Admission、materialization readiness、typed power read-back、Recovery、and EVACUATE authorities remain independent producers. A VM lifecycle Operation may reach `SUCCEEDED` only after a pure aggregate verification binds their exact positive evidence to the current runtime intent.

Host、Admission、allocation、plan、binding、backend、VG/LV UUID、PCI BDF、Command、Attempt、Observation、Recovery、and EVACUATE identities are not public desired fields. Recovery and EVACUATE may replace the current physical incarnation without changing logical VM desired state or Terraform identity.

Initial in-place mutation is limited to metadata and desired `RUNNING`/`SHUTOFF` power. Flavor、Image、Policy、Scope、Port set、and Volume set changes are replacement boundaries until separately qualified operations exist. Reboot、Recovery、EVACUATE、snapshot、rescue、retry、and cleanup remain administrative Operations rather than persistent VM fields.

The complete target contract is defined in [VM Aggregate Resource Architecture](../vm-aggregate-resource-architecture.md).

## Consequences

- `virtual_machines_current` remains an internal runtime projection and is not directly Northbound.
- A future Migration must add only the missing logical revision、runtime intent、dependency snapshot、aggregate Operation/verification/terminal、and tombstone authorities.
- Existing Migrations 011–072 and their immutable histories are not rewritten.
- Terraform refresh after Recovery/EVACUATE remains the same `kim_vm` when desired state is unchanged.
- Phase 3 requires aggregate negative/drift/response-loss/delete qualification before the VM API or Provider is marked ready.
## Rejected alternatives

- Expose `virtual_machines_current` directly: rejected because it mixes logical and physical state.
- Make Host or Admission a VM desired field: rejected because mobility would become desired drift.
- Treat READY or RUNNING alone as aggregate success: rejected because dependency and incarnation provenance would be incomplete.
- Implement VM orchestration in the Terraform Provider: rejected because Provider state is not KIM authority.
- Reuse Recovery/EVACUATE terminal as the VM resource terminal: rejected because administrative operations and persistent resource lifecycle have different authority and replay scopes.
