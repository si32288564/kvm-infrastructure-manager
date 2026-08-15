# Phase 3 VM Aggregate DATA Volume Qualification

Date: 2026-08-15
Database: PostgreSQL 17
Migration: 086
Profile: one VM, zero Ports, one ROOT Volume plus one DATA Volume, no PCI

## Result

```text
VM_AGGREGATE_DATA_VOLUME_PROFILE          = PASS
VM_AGGREGATE_VOLUME_ROLE_AUTHORITY        = PASS
VM_AGGREGATE_VOLUME_ATTACHMENT_BINDING    = PASS
VM_AGGREGATE_STORAGE_SET_VERIFICATION     = PASS
VM_AGGREGATE_DATA_VOLUME_TERMINAL_FENCING = PASS

VM_AGGREGATE_MULTI_VOLUME_MOBILITY        = NOT RUN
NORTHBOUND_VM_RESOURCE                    = BLOCKED
TERRAFORM_VM_RESOURCE                     = BLOCKED
```

## Authority chain

```text
independently VERIFIED ROOT and DATA logical Volume revisions
→ CreateVMAggregate role ordering (ROOT ordinal 0, DATA ordinal 1)
→ immutable dependency snapshot (volume_count=2)
→ compiler re-derives two exact ordinary Storage requirements
→ Final Admission atomically consumes both existing capacity reservations
→ exact physical attachment and backend binding for each Volume
→ immutable aggregate Volume-binding evidence set
→ generic VM materialization boots from ROOT only
→ definition/image/readiness and RUNNING read-back
→ DB verifier revalidates both exact VERIFIED Volume materialization terminals
→ immutable aggregate storage Volume-verification evidence set
→ aggregate verification VERIFIED
→ terminal-time all-Volume current-authority fencing
→ aggregate terminal VERIFIED
```

`ROOT` and `DATA` are logical device roles. Backend, VG/LV, Host, attachment generation and binding generation remain physical incarnation. The generic VM plan continues to identify the single boot ROOT; DATA success is not inferred from ROOT readiness and is independently required by the aggregate verifier.

## Exact campaign identities

```text
VM UUID             = 82000000-0000-4000-8000-708236883000
Operation           = vm-aggregate-create-operation-1786772708236875000
Dependency snapshot = vm-dependencies:82000000-0000-4000-8000-708236883000:1
Admission           = admission:vm-placement:vm-aggregate-create-operation-1786772708236875000:1
Host                = vm-aggregate-host-1786772708236875000
Plan                = vm-plan:vm-aggregate-create-operation-1786772708236875000:1
Verification        = vm-aggregate-verification-1786772708236875000
Terminal            = vm-aggregate-terminal-1786772708236875000

ROOT Volume         = vm-aggregate-root-1786772708236875000 revision 1
ROOT attachment     = vm-volume-attachment:82000000-0000-4000-8000-708236883000:1:0
ROOT binding        = volume-binding:vm-aggregate-root-1786772708236875000:1 generation 1
ROOT materialization= volume-terminal:volume-materialization:vm-aggregate-root-1786772708236875000:1:2

DATA Volume         = vm-aggregate-data-1786772708236875000 revision 1
DATA attachment     = vm-volume-attachment:82000000-0000-4000-8000-708236883000:1:1
DATA binding        = volume-binding:vm-aggregate-data-1786772708236875000:1 generation 1
DATA materialization= volume-terminal:volume-materialization:vm-aggregate-data-1786772708236875000:1:2
```

## Negative and replay coverage

- stale ROOT or DATA revision: rejected;
- ROOT reused as DATA: rejected before mutation;
- unqualified third Volume: rejected before mutation;
- non-bootable ROOT or bootable DATA: rejected;
- missing DATA materialization current while VM READY/RUNNING remains: aggregate verification rejected in a rollback branch;
- DATA backend binding changed to `STALE` after verification: aggregate terminal rejected in a rollback branch;
- dependency Volume, aggregate Volume-binding and aggregate storage-verification immutable rows: UPDATE rejected;
- create, verification and terminal replay: idempotent;
- existing ROOT-only zero-Port, one/two-Port and one-Volume mobility profiles: PASS unchanged;
- Recovery/EVACUATE association for `volume_count>1`: fail-closed until a separate mobility campaign.

No caller supplies Volume attachment success, backend binding success, materialization verification or aggregate Storage state. Existing capacity reservations remain allocated authority; Final Admission removes only duplicate demand accounting and does not return them to free capacity.

## Regression evidence

The final commit was qualified with fresh PostgreSQL 17 migrations 001–086, migration replay, all persistence integration, race integration, `go test ./...`, `go test -race ./...`, `make check`, documentation lint and `git diff --check`.
