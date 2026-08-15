# Phase 3 VM Logical Update and Delete Qualification

Date: 2026-08-15
Database: PostgreSQL 17
Migration: 087
Profile: one VM, zero Ports, one ROOT Volume, no PCI

## Result

```text
VM_LOGICAL_METADATA_UPDATE_AUTHORITY     = PASS
VM_DESIRED_POWER_UPDATE_AUTHORITY        = PASS
VM_POWER_RESPONSE_LOSS_CONVERGENCE       = PASS
VM_DELETE_PROTECTION_AUTHORITY           = PASS
VM_DELETE_DOMAIN_ABSENCE_AUTHORITY       = PASS
VM_DELETE_ROOT_ABSENCE_AUTHORITY         = PASS
VM_DELETE_COMPUTE_RELEASE_AUTHORITY       = PASS
VM_DELETE_TOMBSTONE_AUTHORITY             = PASS
VM_LOGICAL_UPDATE_DELETE_REPLAY           = PASS
VM_LOGICAL_UPDATE_DELETE_ABA_FENCING      = PASS

VM_DELETE_STANDARD_PORT_PROFILE           = NOT RUN
VM_DELETE_MULTI_VOLUME_PROFILE            = NOT RUN
NORTHBOUND_VM_RESOURCE                    = BLOCKED
TERRAFORM_VM_RESOURCE                     = BLOCKED
```

## Qualified authority chain

```text
aggregate CREATE terminal VERIFIED / RUNNING
→ metadata revision 1 → 2 (runtime intent remains 1)
→ desired SHUTOFF revision / runtime intent generation 2
→ typed VIRTUAL_MACHINE_POWER_STATE_ENSURE
→ response LOST / read-back
→ exact SHUTOFF MATCHED power evidence
→ power terminal VERIFIED
→ desired RUNNING revision / runtime intent generation 3
→ typed power / RUNNING MATCHED / terminal VERIFIED
→ desired SHUTOFF revision / runtime intent generation 4
→ response LOST / SHUTOFF MATCHED / terminal VERIFIED
→ delete protection true blocks delete
→ metadata-only unprotect revision (runtime intent remains 4)
→ delete authority snapshots exact current runtime incarnation
→ typed VIRTUAL_MACHINE_UNDEFINE
→ exact Domain absence observation
→ ROOT attachment READ_BACK_FIRST
→ exact DETACHED / no-device / no-holder observation
→ attachment claim RELEASED
→ compute allocation RELEASED with immutable release evidence
→ delete terminal VERIFIED
→ immutable VM tombstone
→ logical VM DELETED
```

ROOT disk hot-detach remains prohibited by the Host Agent backend. The qualified delete path first undefines the exact inactive Domain, then uses the ordinary typed attachment verifier in read-back mode to prove the ROOT device is absent. It does not issue an unsupported root hot-detach mutation.

Logical metadata changes do not change the dependency snapshot, runtime intent generation, Host, Admission, plan, Port binding, or Volume backend incarnation. Desired power changes create a new logical revision and runtime intent generation but reuse the exact immutable dependency snapshot; they do not re-run Placement.

## Exact campaign identities

```text
VM UUID                  = 82000000-0000-4000-8000-529006333000
Host                     = vm-aggregate-host-1786774529006327000
Admission                = admission:vm-placement:vm-aggregate-create-operation-1786774529006327000:1
VM generation            = 1
Plan                     = vm-plan:vm-aggregate-create-operation-1786774529006327000:1
ROOT Volume              = vm-aggregate-root-1786774529006327000
ROOT attachment          = vm-volume-attachment:82000000-0000-4000-8000-529006333000:1:0
ROOT binding             = volume-binding:vm-aggregate-root-1786774529006327000:1
Compute allocation       = allocation:vm-placement:vm-aggregate-create-operation-1786774529006327000:1

Metadata evidence        = vm-metadata-evidence-1786774529006327000
Power OFF terminal A     = vm-power-off-a-terminal-1786774529006327000
Power ON terminal        = vm-power-on-terminal-1786774529006327000
Power OFF terminal B     = vm-power-off-b-terminal-1786774529006327000
Final SHUTOFF evidence   = vm-power/vm-power-off-b-command-1786774529006327000/1

Delete Operation         = vm-delete-operation-1786774529006327000
ROOT absence evidence    = vm-delete-root-absence-observation-1786774529006327000
Compute release evidence = vm-delete-compute-release-1786774529006327000
Delete terminal          = vm-delete-terminal-1786774529006327000
Tombstone                = vm-delete-tombstone-1786774529006327000
Final VM revision        = 9
```

## Negative and replay coverage

- stale metadata `ExpectedRevision`: rejected;
- same metadata request and digest: idempotent replay;
- power command without MATCHED physical read-back: rejected;
- power response loss: successor verification converges from read-back;
- delete while RUNNING: rejected;
- delete protection enabled: rejected;
- non-zero Port, multiple Volume, or PCI requirement: fail-closed by the producer;
- stale runtime plan, attachment, backend binding, power evidence, or compute claim: rejected;
- backend binding drift after absence verification and before terminal: rejected in a rollback branch;
- delete terminal replay with the same evidence set: idempotent;
- immutable metadata, power, delete, release, terminal, and tombstone evidence UPDATE: rejected.

## Safety assertions

```text
caller-supplied SHUTOFF authority          = none
caller-supplied Domain absence authority   = none
caller-supplied attachment absence         = none
command success treated as power state     = no
command success treated as Domain absence  = no
ROOT arbitrary path / argv                  = none
Volume resource deleted with VM             = no
Volume capacity reservation released        = no
physical backend cleanup inferred            = no
Recovery or EVACUATE proof reused            = no
historical evidence rewritten                = none
production workload mutation                 = none
```

The final commit was qualified with fresh PostgreSQL 17 migrations 001–087, exact migration replay, all persistence integration, race integration, `go test ./...`, `go test -race ./...`, `make check`, documentation lint and `git diff --check`.
