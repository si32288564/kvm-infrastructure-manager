# KIM VM Aggregate Phase 3 Readiness Review

> Completion addendum (2026-08-15): Migration 082 implements and qualifies the first internal aggregate slice: exact logical revision/dependency snapshot/runtime intent, KIM-compiled zero-Port/one-root Placement, Availability-aware Final Admission, generic materialization, READY/RUNNING read-back verification and immutable aggregate terminal. Northbound VM and Terraform VM remain blocked. See [Phase 3 internal qualification](../validation/p3-vm-aggregate-internal-authority-20260815.md).

> STANDARD Port addendum (2026-08-15): Migration 083 qualifies one logical STANDARD Port through exact dependency snapshot, Final Admission binding, attached OVN realization, typed OVS preboot observation and aggregate terminal drift fencing. Physical Port incarnation remains outside logical VM desired state. See [one STANDARD Port qualification](../validation/p3-vm-aggregate-one-standard-port-20260815.md).

- Date: 2026-08-15
- Baseline: `eb9f8ae3096e135bf7446b01dd92d19e72d0f837`
- Scope: repository architecture and authority inventory only
- Mutation performed: documentation only

## Decision

```text
PHASE3_VM_AGGREGATE_DESIGN          = ACCEPTED
PHASE3_VM_INTERNAL_INITIAL_SLICE    = IMPLEMENTED
NORTHBOUND_VM_RESOURCE     = BLOCKED
TERRAFORM_VM_RESOURCE      = BLOCKED
```

Implementation did not serialize `virtual_machines_current`. Migration 082 added the logical aggregate producer and terminal while retaining that table as an internal runtime projection. The remaining public resource work must preserve the same boundary.

## Ready prerequisites

- Project、Flavor、Image、Availability Policy、Network、Subnet、Port、Volume are public resource contracts through Migration 081.
- Final Admission atomically commits compute、availability、network、storage、and PCI claims.
- VM definition、image、storage、network、PCI readiness and power observations are generation-fenced authorities.
- Recovery and EVACUATE preserve historical Admission/materialization incarnations and have positive terminal chains.
- Terraform Provider already implements idempotency、ETag、Operation polling、import、and physical-incarnation exclusion patterns.

## Blocking gaps

- immutable logical VM revision/current authority;
- exact aggregate dependency snapshot;
- runtime intent generation independent of metadata revision;
- aggregate Create/Power/Delete Operation;
- pure aggregate verification and terminal evidence;
- logical/runtime association across Recovery and EVACUATE;
- safe delete quiescence/detach/absence/tombstone contract;
- VM OpenAPI/Provider schema and acceptance campaign.

## Recommended first implementation profile

```text
VM count             = 1
datapath             = STANDARD
OVN Port             = 0, then 1
boot/root Volume     = 1
data Volume          = 0
PCI                  = 0
desired power        = RUNNING
Placement candidates = at least 2 Hosts in PostgreSQL qualification
```

The first gate should prove create through observed RUNNING, then SHUTOFF/RUNNING desired update, import/no-op, EVACUATE no-drift, Recovery no-drift, and verified delete. HIGH_PERFORMANCE、DIRECT_IO、live mutation、shared storage、and production Host qualification remain separate gates.

## Safety conclusion

The proposed design adds no shortcut authority. It requires existing Final Admission、typed execution、read-back、readiness、Recovery、and EVACUATE evidence and introduces only the missing aggregate provenance. No Migration、code、OpenAPI、Provider、qualification result、or production status is changed by this review.
