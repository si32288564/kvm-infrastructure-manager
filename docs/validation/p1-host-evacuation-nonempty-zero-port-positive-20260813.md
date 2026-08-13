# Host EVACUATE Non-Empty Zero-Port Positive E2E Qualification

- Date: 2026-08-13
- Database: disposable PostgreSQL 17
- Migration range: 001–068, fresh apply and replay
- Workload profile: one managed VM, zero Network Ports, zero PCI, one synthetic Local LVM boot root
- Production or SSH mutation: none

## Result

The schema-legitimate public/internal authority path completed:

```text
StartHostEvacuation
-> PostgreSQL workload snapshot (1)
-> bounded child claim (1/1)
-> source shutdown authority
-> ordinary Command Lease / Attempt
-> lost response / exact libvirt SHUTOFF read-back
-> Planned Source Quiescence
-> planned source root SAFE read-back (exact vda, exact LV, no holder)
-> source Placement allocation release
-> destination Dry Evaluation / Final Admission
-> generic relocation materialization authority
-> ordinary define / definition read-back
-> ordinary image realization / read-back
-> zero-Port readiness
-> ordinary power-on / RUNNING read-back
-> pure Child Verification VERIFIED
-> Child Terminal VERIFIED
-> Parent Terminal VERIFIED
-> source Host DRAINED
```

Parent terminal facts were read from `host_evacuation_terminal_evidence`:

```text
workload_count                  = 1
verified_count                  = 1
active_source_workload_count    = 0
post_drain_admission_count      = 0
parent lifecycle               = VERIFIED
source drain                   = DRAINED
destination VM power           = RUNNING / MATCHED
cleanup operations             = 0
```

## Authority gap and Migration 068

Storage-less VM materialization is not legitimate in the current model:
`PrepareVMMaterialization` requires exactly one boot Volume, bound backend,
and reserved Attachment. Migration 068 therefore adds only the missing planned
relocation consumers:

- planned source root safety derived from exact SHUTOFF and typed vda/no-holder read-back
- explicit source Placement allocation release after that proof
- generic relocation provenance consumed by ordinary `PrepareVMMaterialization`

No Recovery Operation, Failure Epoch, or Fencing Proof is involved. The
generic VM materialization primitive now permits an existing VM projection to
change Admission/Host only when the exact relocation authority is present.
The readiness projector also resets old plan/image/network provenance when a
higher materialization incarnation is defined.

The fixture uses separate synthetic Local LVM source and destination boot
roots and ordinary image realization. It proves planned source quiescence,
root no-holder safety, and closed destination materialization authority. It
does not prove preservation of mutable guest data or real cross-Host Local LVM
semantics; `EVACUATE_LOCAL_LVM` remains BLOCKED.

## Derived child verification

```text
verification_state   = VERIFIED
source storage       = SAFE
source network       = NOT_REQUIRED
source PCI           = NOT_REQUIRED
destination storage  = CURRENT
destination network  = NOT_REQUIRED
destination PCI      = NOT_REQUIRED
destination power    = RUNNING
source ownership     = RETIRED
```

No state above is supplied by the caller.

## Negative and drift qualification

- stale pre-drain source Dry Evaluation cannot Final Admit after drain
- destination power Command without RUNNING observation cannot verify child
- verification ID replay is identical; different binding reuse conflicts
- terminal rejects current destination plan drift
- terminal rejects readiness observation-generation drift
- terminal rejects readiness definition-evidence drift
- terminal rejects destination power-evidence drift
- terminal rejects power observation-generation drift
- terminal rejects source Host authority-generation drift
- terminal rejects missing drain projection
- terminal replay is idempotent; different verification reuse conflicts
- immutable quiescence-execution, destination-binding, child-verification,
  child-terminal, and parent-terminal evidence reject UPDATE
- Failure Epoch and Fencing Proof counts are unchanged

## PASS / BLOCKED matrix

| Gate | Result |
|---|---|
| EVACUATION_SOURCE_QUIESCENCE_AUTHORITY | PASS |
| EVACUATION_CHILD_EVIDENCE_VERIFICATION | PASS |
| EVACUATION_CHILD_TERMINAL_AUTHORITY | PASS |
| EVACUATION_DESTINATION_EVIDENCE_BINDING | PASS |
| EVACUATION_TERMINAL_DRIFT_FENCING | PASS |
| EVACUATION_NONEMPTY_PARENT_TERMINAL | PASS |
| EVACUATE_ZERO_PORT | PASS |
| EVACUATION_NETWORK_HANDOFF | NOT RUN |
| EVACUATION_REPEATED_INCARNATION | NOT RUN |
| EVACUATE_LOCAL_LVM | BLOCKED |
| EVACUATE_PCI_SRIOV | BLOCKED |
| REAL_TWO_HOST_KVM_HOST_EVACUATION | BLOCKED |

## Campaign metrics

```text
workloads                   = 1
maximum concurrency         = 1
child claims                = 1
shutdown attempts           = 1
shutdown UNKNOWN/LOST       = 1
source SHUTOFF observations = 1
destination Admissions      = 1
destination plans           = 1
destination power attempts  = 1
child verifications         = 1
child terminals             = 1
parent terminals            = 1
parent outcome              = VERIFIED
```

## Exact successful campaign identities

```text
source Host ID                      = evacuation-positive-source-1786603680024379000
destination Host ID                 = evacuation-positive-destination-1786603680024379000
VM UUID                             = 68000000-0000-4000-8000-680024379000
VM generation                       = 1
source Admission                    = admission:evacuation-source-1786603680024379000
source Plan                         = evacuation-source-plan-1786603680024379000
source materialization generation   = 1
destination Admission               = admission:evacuation-destination-1786603680024379000
destination Plan                    = evacuation-destination-plan-1786603680024379000
destination materialization gen     = 2
source SHUTOFF evidence ID           = vm-power/evacuation-shutdown-command-1786603680024379000/1
destination RUNNING evidence ID      = vm-power/destination-power-command-1786603680024379000/1
child verification ID               = evacuation-child-verification-1786603680024379000
child terminal ID                   = evacuation-child-terminal-1786603680024379000
parent terminal ID                  = evacuation-parent-terminal-1786603680024379000
```

## Safety assertions

```text
caller-supplied SHUTOFF authority           = none
caller-supplied RUNNING authority           = none
caller-supplied Storage/Network/PCI state   = none
fake Failure Epoch                          = none
fake Fencing Proof                          = none
direct SSH backend mutation                 = none
production workload mutation                = none
historical evidence rewritten               = none
cleanup required for parent VERIFIED        = no
Host automatically undrained                = no
```

## Validation commands

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestMigratePostgreSQLIntegration -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestHostEvacuation -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -timeout 600s ./internal/persistence/postgres
go test ./...
go test -race ./...
make check
git diff --check
```
