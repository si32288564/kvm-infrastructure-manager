# Phase 2 Volume Resource Authority Decomposition Validation — 2026-08-15

## Scope and environment

- Baseline: `9ebed2940a6bb000e6e038646de7d0a6bb940ecb`
- Schema: fresh Migration 001–080 plus exact migration replay
- Database: disposable PostgreSQL 17
- Profile: synthetic Local LVM, one Host per materialization, SINGLE_WRITER, no public API
- Public `/api/v1/volumes`, Terraform `kim_volume`, Ceph RBD, and VM API: not implemented or exercised

## Qualified authority chain

`CreateVolumeResource -> immutable revision -> capacity allocation -> materialization claim -> typed LOCAL_LVM_VOLUME_ENSURE -> response LOST -> Lease expiry -> READ_BACK_FIRST -> exact MATCHED VG/LV/size observation -> materialization VERIFIED -> attachment REQUESTED -> DryEvaluatePlacement -> exact Final Admission consumer -> attachment ATTACHED`

Retirement independently passed `attachment clear -> RETIRE_PENDING -> typed LOCAL_LVM_VOLUME_DELETE -> response LOST -> READ_BACK_FIRST -> exact ABSENT -> immutable release evidence -> capacity RELEASED -> logical DELETED`.

The fixture also created an Image-backed Volume from one exact `VERIFIED` Image revision/digest, then advanced the Image and proved that the existing Volume retained the original source revision and artifact digest.

## Capacity and Final Admission result

The standalone Volume reserved 16 MiB before Placement. Dry and Final Admission subtracted exactly those 16 MiB from only that request's incremental storage demand; the PostgreSQL capacity claim remained `ALLOCATED` with all 16 MiB reserved. Two concurrent Final Admissions were both dry-eligible, but exact current attachment locking and re-evaluation committed one consumer and rejected one. Legacy Admission-created Volume accounting remains unchanged.

Negative branches rejected wrong Volume revision, capacity allocation generation, backend, attachment intent, workload, non-VERIFIED materialization, stale binding, and `RELEASE_PENDING` capacity. A stale dry result did not advance current authority.

## Identity boundaries

| Logical or physical identity | Qualified rule |
|---|---|
| `volume_id` | stable logical identity |
| `volume_revision` | immutable desired revision; not Host/VG/LV identity |
| capacity allocation | immutable ID/generation tied to exact revision/backend observation |
| binding/materialization | physical Host/backend/VG/LV incarnation |
| attachment intent | separate logical workload request and generation |
| physical attachment | created only by exact Final Admission consumer |
| copy/cleanup | existing Migrations 068–072 immutable histories; not desired drift |

## Gate matrix

| Gate | Result | Evidence |
|---|---|---|
| `VOLUME_RESOURCE_AUTHORITY` | PASS | persistent backend-neutral create/current producer |
| `VOLUME_IMMUTABLE_REVISION` | PASS | stable ID, revisions 1→2→3, UPDATE rejection |
| `VOLUME_STORAGE_CLASS_AUTHORITY` | PASS | exact immutable class revision and closed Local LVM capability |
| `VOLUME_CAPACITY_ALLOCATION_AUTHORITY` | PASS | immutable decision plus retained ledger claim |
| `VOLUME_CAPACITY_REPLAY` | PASS | same allocation converges; insufficient capacity rejects |
| `VOLUME_STANDALONE_MATERIALIZATION` | PASS | closed typed Local LVM operation |
| `VOLUME_MATERIALIZATION_READ_BACK` | PASS | exact VG/LV/size MATCHED evidence only |
| `VOLUME_RESPONSE_LOSS` | PASS | LOST → READ_BACK_FIRST → VERIFIED/ABSENT |
| `VOLUME_IMAGE_REVISION_BINDING` | PASS | exact verified source revision/digest, no retrofit |
| `VOLUME_ATTACHMENT_SEPARATION` | PASS | AVAILABLE unattached and explicit REQUESTED/ATTACHED intent |
| `VOLUME_FINAL_ADMISSION_COMPATIBILITY` | PASS | exact existing-authority consumer plus legacy regression |
| `VOLUME_LOCAL_LVM_INCARCATION_SEPARATION` | PASS | Host/VG/LV/binding generations excluded from desired revision |
| `VOLUME_PLANNED_RELOCATION_IDENTITY_CONTINUITY` | PASS | desired identity invariant plus Migration 068–072 relocation regression |
| `VOLUME_SOURCE_CLEANUP_COMPATIBILITY` | PASS | existing exact source cleanup regression remains green |
| `VOLUME_CAPACITY_RELEASE_ORDERING` | PASS | exact ABSENT terminal precedes release and tombstone |
| `VOLUME_DELAYED_CLEANUP_ABA_FENCING` | PASS | existing exact LV UUID/binding cleanup regression |
| `VOLUME_NO_PHYSICAL_IDENTITY_LEAKAGE` | PASS | desired evidence excludes Host/VG/LV/path |
| `VOLUME_TERRAFORM_DRIFT_INVARIANT` | PASS | physical generations do not mutate desired revision |
| `NORTHBOUND_VOLUME_RESOURCE_READINESS` | CONTRACT_READY | internal contract only |
| `NORTHBOUND_VOLUME_RESOURCE` | NOT RUN | endpoint intentionally absent |
| `TERRAFORM_VOLUME_RESOURCE` | NOT RUN | Provider resource intentionally absent |
| `VM_PHASE3_READINESS` | NO | Phase 2 public API/Provider and VM aggregate contracts remain |

## Qualification class and safety

This is a synthetic PostgreSQL/typed-backend qualification. It does not raise the Production score and does not claim real two-Host Volume materialization. Migrations 068–072 real/synthetic classifications remain unchanged.

- caller-supplied VERIFIED/materialized/ABSENT authority = none
- arbitrary path, VG name, shell, argv = none
- capacity claim returned to free capacity during Admission = no
- command success treated as convergence = no
- backend object auto-adopted = no
- existing Image revision retrofitted = no
- historical evidence rewritten = none
- production workload/backend mutated = none

## Regression result

- fresh PostgreSQL 17 Migration 001–080 and replay: PASS
- all PostgreSQL persistence integration: PASS
- standalone Volume/response-loss/capacity concurrency/Final Admission integration: PASS
- all PostgreSQL persistence integration under `-race`: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- `go vet ./...`: PASS
- Terraform Provider `go vet ./...` and `go test ./...`: PASS
- `make check`: PASS
- documentation lint/link: PASS (`552` requirements, `812` test contracts, `300` links)
- `git diff --check`: PASS
