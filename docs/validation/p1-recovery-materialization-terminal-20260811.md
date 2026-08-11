# P1 Recovery Materialization / Verification / Terminal Authority Validation

Date: 2026-08-11  
Scope: Migration 055, closed `RESTART_ON_OTHER_HOST` recovery action

## Authority path

```text
Failure Epoch FENCED
→ Recovery Eligibility / Budget Claim
→ Recovery Operation / exact destination Plan
→ ordinary destination Final Admission
→ ordinary Local LVM binding/image/attachment
→ ordinary VM define and zero-Port network readiness
→ explicit pre-power safety revalidation
→ typed VIRTUAL_MACHINE_POWER_SET Job/Command
→ UNKNOWN / standard read-back
→ immutable Recovery Verification
→ explicit Recovery Terminal Decision
→ Operation VERIFIED + Epoch RECOVERED + Budget RELEASED
```

Recovery keeps the same workload identity and VM UUID while preserving immutable source Admission/materialization history. Migration 055 replaces only the old `(vm_id, vm_generation)` Plan uniqueness with `(vm_id, vm_generation, placement_admission_id)`; the same incarnation/Admission cannot acquire duplicate Plan authority.

## PostgreSQL qualification

Fresh PostgreSQL 17 migration and the Availability/Recovery integration fixture verify:

- exact destination Admission and one ordinary boot-volume requirement;
- existing Local LVM Binding, image materialization, Attachment device/holder read-back, VM define, and readiness projections;
- dangerous-step evaluation is pure and emits no power authority;
- source Host re-arm, source Storage Claim reactivation, Budget drift, and destination readiness drift immediately before power each produce zero power authority/Job/Command;
- explicit power authority emits the existing closed typed libvirt power Command;
- ambiguous power execution moves the Operation to `UNKNOWN`, keeps Budget `CONSUMED`, and does not issue a second Start;
- standard power read-back moves the Operation only to `VERIFYING`;
- immutable Recovery Verification records exact power, Attachment, Network, Fencing, Storage Proof, Admission, materialization, and Budget generations;
- Verification `VERIFIED` is pure: Operation remains `VERIFYING`, Epoch remains `FENCED`, Budget remains `CONSUMED`;
- power, Attachment, or Network current-generation drift after Verification rejects terminal commit;
- exact Terminal Decision atomically creates one Operation `VERIFIED` transition, one Epoch `RECOVERED` transition caused only by that Terminal Decision, and one Budget `RELEASED` transition;
- terminal response-loss replay returns the same Decision without generation amplification;
- after `RELEASED`, a fresh independent failure incarnation with separately reconciled resource availability can obtain a new Planning Budget Claim; the old Evaluation is not reused;
- immutable historical source/destination evidence is not deleted or rewritten;
- existing `EVACUATE` start remains fail closed and has no fallback to `RESTART_ON_OTHER_HOST`.

## Constraint evolution

The approved schema evolution is limited to:

1. replacing the old VM Plan uniqueness needed for multiple historical Admissions of the same workload; and
2. extending only the named Failure Epoch transition/current-state CHECK constraints for `RECOVERED`, whose sole positive cause is an exact Recovery Terminal Decision.

No pre-existing evidence is backfilled or rewritten and no unrelated UNIQUE/CHECK constraint is relaxed.

## Real KVM status

This increment reuses the previously qualified standard libvirt define/power/read-back implementation, but the complete two-physical-Host Recovery chain was **not** executed in this increment. The available second Host is production-sensitive, so no destructive destination rematerialization was performed. Therefore this report certifies PostgreSQL authority integration and existing typed backend reuse; it does **not** claim a new real two-Host KVM Recovery qualification.

Remaining gates include real two-Host KVM recovery, non-empty Network/OVN readiness, PCI/SR-IOV recovery, `EVACUATE`, cleanup/reconciliation authority, and coordinated multi-VM/cross-site recovery. The subsequent [real two-Host preflight](p1-real-two-host-kvm-recovery-20260811.md) was fail-closed as `BLOCKED` because the only available second Host is an important production Host.

## Commands

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestMigratePostgreSQLIntegration ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestAvailabilityPolicyPlacementConsumerPostgreSQLIntegration ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 ./internal/persistence/postgres
go test -race ./...
make check
```

Temporary PostgreSQL fixtures are removed after validation.

Final results: fresh PostgreSQL 17 full persistence integration PASS, `go test -race ./...` PASS, `make check` PASS, and documentation lint PASS (`471 requirements`, `715 test contracts`, `232 links`).
