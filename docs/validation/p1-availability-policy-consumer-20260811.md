# AvailabilityPolicy Persistence / Consumer Authority Validation

Date: 2026-08-11
Scope: migration 048, generic Group Policy Binding, Availability-aware Placement, immutable VM Availability Binding

## Authority chain

```text
immutable AvailabilityPolicy revision
→ generic AVAILABILITY_POLICY / VM_PLACEMENT Binding
→ deterministic many-to-many Host resolution
→ read-only Availability-aware Dry Placement
→ exact Final revalidation
→ Admission + resource claims + immutable VM Availability Binding
```

AvailabilityPolicy is neither failure evidence nor recovery authority. This increment creates no failure detector, fencing proof, Recovery Eligibility, Recovery Operation, restart, evacuation, or Workload Resilience Intent authority.

## Closed policy schema

The persistence contract accepts only the documented responsibility values:

- `INFRASTRUCTURE_MANAGED` with `RESTART_ON_OTHER_HOST` or `EVACUATE`;
- `WORKLOAD_MANAGED` with `NO_AUTOMATIC_ACTION`;
- `MANUAL` with `NO_AUTOMATIC_ACTION`.

The remaining documented contracts are closed named references: failure confirmation, fencing, storage, network-device, recovery eligibility, failure-domain, recovery budget, escalation, notification, support tier, creator, and approver. Arbitrary JSON rules are not accepted.

## Resolution and Placement semantics

- highest numeric priority wins;
- exact same policy identity/revision/digest at highest priority resolves;
- incompatible equal-priority identities return `ASSIGNMENT_CONFLICT`;
- a stale highest-priority input returns `STALE_ASSIGNMENT` and never falls back;
- missing binding returns `NO_ASSIGNMENT` and never defaults responsibility;
- every non-`RESOLVED` result blocks the candidate in the Availability-aware Placement path.

Dry resolution uses a read-only repeatable-read transaction and creates no resolution evidence, Admission, resource claim, or VM Binding. Final persists the same generic resolution only after its input/resolution digests and exact effective policy match Dry. Policy or Binding drift returns stale and the outer transaction rolls back.

## Immutable VM responsibility

`vm_availability_binding_evidence` fixes workload, Admission, allocation, generic resolution, exact policy revision/digest, responsibility, Host failure action, and resolution input digest. `vm_availability_bindings_current` is a rebuildable pointer. Revision 1 is created in the same outer PostgreSQL transaction as Final Admission and resource claims.

Publishing a later policy revision, rebinding a Pool, or retiring a current policy does not update historical VM evidence. Explicit Availability Rebind remains a future authority gate.

## PostgreSQL 17 qualification

Fresh `postgres:17-alpine` migration and persistence integration passed with:

- all 48 migrations applied twice idempotently;
- valid policy publish and same-semantic replay;
- invalid responsibility/action rejection in Go and PostgreSQL constraints;
- Placement Pool-only Availability binding;
- exact revision binding and explicit revision advance;
- equal-priority conflict and exact-equivalent resolution;
- stale highest priority without lower fallback;
- competing exact-revision publishers: one commit, one conflict, one evidence row;
- Final Admission versus Policy current switch, 10 race-detector runs: old complete authority commit or stale rejection only; no mixed VM Binding;
- read-only Dry provenance with zero authority writes;
- policy drift after Dry rejected with no new claim;
- Admission, compute claim, generic resolution, and VM Binding atomic commit;
- Final response replay converged to one Admission, claim, resolution, and Binding;
- existing VM remained on revision 1 while a new VM bound revision 2;
- retirement blocked new resolution while preserving historical bindings;
- full PostgreSQL persistence regression suite passed, including Maintenance, Placement Scope, Compute/PCI/Network/Storage, HostGroup Set/Hierarchy/Selector/External Assertion, Upgrade, and Maintenance consumers.

## Compatibility and remaining gates

Migration 048 backfills only the generic catalog from real Maintenance evidence. It does not invent AvailabilityPolicy or VM Binding rows for pre-048 history. The old Placement APIs remain an explicit compatibility path; the new authoritative path requires Availability resolution.

Remaining Availability gates are explicit Rebind, current operational validation after retirement/distrust, Failure Epoch/evidence, fencing and storage proof consumers, Recovery Eligibility/Operation, Recovery Budget, failure-domain scheduling, and Workload Resilience Intent persistence.
