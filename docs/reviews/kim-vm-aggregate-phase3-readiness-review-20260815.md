# KIM VM Aggregate Phase 3 Readiness Review

> Completion addendum (2026-08-15): Migration 082 implements and qualifies the first internal aggregate slice: exact logical revision/dependency snapshot/runtime intent, KIM-compiled zero-Port/one-root Placement, Availability-aware Final Admission, generic materialization, READY/RUNNING read-back verification and immutable aggregate terminal. Northbound VM and Terraform VM remain blocked. See [Phase 3 internal qualification](../validation/p3-vm-aggregate-internal-authority-20260815.md).

> STANDARD Port addendum (2026-08-15): Migration 083 qualifies one logical STANDARD Port through exact dependency snapshot, Final Admission binding, attached OVN realization, typed OVS preboot observation and aggregate terminal drift fencing. Physical Port incarnation remains outside logical VM desired state. See [one STANDARD Port qualification](../validation/p3-vm-aggregate-one-standard-port-20260815.md).

> Planned mobility addendum (2026-08-15): Migration 084 adds a pure post-terminal aggregate association consumer and qualifies one STANDARD Port Host EVACUATE A→B with unchanged logical VM/Port desired authority. Aggregate-origin Recovery remains NOT RUN. See [planned EVACUATE no-drift qualification](../validation/p3-vm-aggregate-evacuate-no-drift-20260815.md).

> Multi-Port addendum (2026-08-15): Migration 085 qualifies a two STANDARD Port aggregate through canonical logical dependency ordering, ordinary Final Admission, exact per-Port binding/OVS evidence and all-Port terminal drift fencing. Multi-Port mobility remains NOT RUN. See [multi STANDARD Port qualification](../validation/p3-vm-aggregate-multi-standard-port-20260815.md).

> DATA Volume addendum (2026-08-15): Migration 086 qualifies one ROOT plus one DATA Volume through role-ordered dependency snapshot, atomic Final Admission consumption, exact attachment/backend binding evidence, independent materialization verification and all-Volume terminal fencing. Multi-Volume mobility remains NOT RUN. See [DATA Volume qualification](../validation/p3-vm-aggregate-data-volume-20260815.md).

> Recovery mobility addendum (2026-08-15): the Migration 084 `RECOVERY` consumer is now qualified by an aggregate-origin Recovery A→B followed by planned EVACUATE B→C. Both associations preserve the same logical VM/Port revision and desired digests while advancing runtime association generation 0→1→2. See [Recovery no-drift qualification](../validation/p3-vm-aggregate-recovery-no-drift-20260815.md).

- Date: 2026-08-15
- Baseline: `eb9f8ae3096e135bf7446b01dd92d19e72d0f837`
- Scope: repository architecture and authority inventory only
- Mutation performed: documentation only

## Decision

```text
PHASE3_VM_AGGREGATE_DESIGN          = ACCEPTED
PHASE3_VM_INTERNAL_INITIAL_SLICE    = IMPLEMENTED
VM_AGGREGATE_EVACUATE_NO_DRIFT      = PASS
VM_AGGREGATE_RECOVERY_NO_DRIFT      = PASS
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

## Implementation addendum: Migration 087

Migration 087でmetadata-only revision、desired power Operation、delete protection、zero-Port/one-ROOT delete terminal、immutable tombstoneをqualificationした。metadata updateはruntime intent/physical incarnationを変更せず、power updateはexact dependency snapshotを維持したままtyped power read-backへ収束する。deleteはSHUTOFF、typed Domain absence、ROOT absence/no-holder、compute releaseを独立evidenceとして要求する。

Port付き・複数Volume delete、Northbound `/api/v1/vms`、Terraform `kim_vm` は未qualificationであり、引き続きBLOCKEDとする。
