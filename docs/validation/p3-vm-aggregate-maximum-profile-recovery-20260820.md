# Phase 3 VM Aggregate Maximum-Profile Recovery Qualification

- Date: 2026-08-20
- Database: disposable PostgreSQL 17 (`postgres:17`)
- Migrations: fresh 001–093 and replay
- Migration added: 093, Recovery multi-Volume materialization authority
- Profile: one VM, two STANDARD Ports, one rebuildable ROOT Volume, one Local LVM DATA Volume, no PCI

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_MAXIMUM_PROFILE_RECOVERY` | PASS |
| `VM_AGGREGATE_MULTI_PORT_RECOVERY` | PASS |
| `VM_AGGREGATE_MULTI_VOLUME_RECOVERY` | PASS |
| `VM_AGGREGATE_RECOVERY_DATA_CONTENT_IDENTITY` | PASS |
| `VM_AGGREGATE_MAXIMUM_PROFILE_NO_DESIRED_DRIFT` | PASS |
| `VM_AGGREGATE_RECOVERY_COMPLETE_SET_FENCING` | PASS |
| `VM_AGGREGATE_RECOVERY_REPLAY_IDEMPOTENCY` | PASS |
| `REAL_MAXIMUM_PROFILE_RECOVERY` | BLOCKED |

Migration 093 closes a real authority gap. Historical Recovery materialization and terminal verification were ROOT-only, and aggregate association deliberately rejected `volume_count != 1`. Existing two-Port Recovery network authority already represented a complete set; Storage required an independent canonical ROOT+DATA producer and consumer.

## Qualified chain

```text
logical VM revision 1
+ canonical Port set [Port 0, Port 1]
+ canonical Volume set [ROOT, DATA]
→ Final Admission / materialization / READY / RUNNING on Host A
→ aggregate terminal VERIFIED
→ typed SHUTOFF read-back
→ exact DATA DETACHED / claim RELEASED / holder absent
→ Failure Epoch CONFIRMED
→ source execution fencing PROVEN / Host FENCED
→ ROOT safety + complete Storage safety proof
→ both source Port retirements and source quiescence evidence
→ source materialization RETIRED
→ Recovery eligibility / budget / two-Port+two-Volume destination request
→ destination Final Admission on Host B
→ ROOT destination allocation (verified Image rebuild authority)
→ DATA destination allocation
→ typed DATA Local LVM copy response LOST
→ exact source/destination SHA-256 read-back MATCHED
→ DATA copy terminal VERIFIED
→ Recovery materialization Volume set [ROOT image rebuild, DATA copy]
→ definition / image / both OVN+OVS realizations / READY
→ RUNNING read-back
→ ROOT and DATA attachment read-back complete set
→ Recovery Verification VERIFIED
→ Recovery Terminal VERIFIED / Failure Epoch RECOVERED
→ aggregate association port_count=2, volume_count=2
→ runtime Host A→B; logical VM, Port and Volume desired authority unchanged
```

ROOT and DATA provenance remain deliberately different. ROOT uses the existing Recovery contract, rebuilding from the exact verified Image revision and checksum. DATA may not infer correctness from ROOT, size, allocation, or VM boot. It requires an exact source safety input, exact source/destination binding incarnations, a typed bounded copy command, and equal whole-volume SHA-256 observations. The caller supplies neither `CONTENT_IDENTICAL` nor any backend path.

## Exact campaign identities

| Identity | Value |
|---|---|
| VM UUID | `83000001-0000-4000-8000-6f8994e29d48` |
| source Host | `vm-port-host-6f8994e29d48` |
| destination Host | `vm-port-recovery-6f8994e29d48` |
| source Admission | `admission:vm-placement:vm-port-create-operation-6f8994e29d48:1` |
| destination Admission | `admission:recovery-placement:vm-port-recovery-operation-6f8994e29d48` |
| source Plan / materialization | `vm-plan:vm-port-create-operation-6f8994e29d48:1` / `1` |
| destination Plan / materialization | `mixed-recovery-plan-b-aggregate-6f8994e29d48` / `2` |
| source DATA Volume | `vm-port-data-6f8994e29d48` |
| source DATA Binding / LV | `volume-binding:vm-port-data-6f8994e29d48:1` / `lv-vm-port-data-6f8994e29d48` |
| destination ROOT Volume | `recovery-volume-75aa3f92e7e9cc888283ea0c85976436-1` |
| destination ROOT content authority | `BASE_IMAGE_REBUILD`, `vm-port-image-6f8994e29d48:1` |
| destination DATA Volume | `recovery-volume-75aa3f92e7e9cc888283ea0c85976436-2` |
| destination DATA LV | `lv-mixed-recovery-data-aggregate-6f8994e29d48` |
| DATA Copy Operation | `recovery-local-lvm-data-copy-aggregate-6f8994e29d48` |
| DATA Copy Terminal | `recovery-local-lvm-data-copy-terminal-aggregate-6f8994e29d48` |
| DATA copy response | `LOST` |
| Recovery Verification | `mixed-recovery-verification-aggregate-6f8994e29d48` |
| Recovery Terminal | `mixed-recovery-terminal-aggregate-6f8994e29d48` |
| aggregate association | `vm-port-recovery-association-6f8994e29d48` |
| Port evidence-set digest | `c177e8cdeacee8cb808e71a17a2b6c9fb3a0cac863b081080d99c74b940694da` |
| Volume evidence-set digest | `b9a1f92dc06d447eab18bef0892a7a9ae2fe8ef942613fa798311df46760d901` |

The synthetic DATA content digest is derived from a unique per-campaign marker and is stored only as SHA-256 evidence; no guest block content is stored in evidence or logs.

## Negative and drift coverage

- one missing destination Port realization rejects aggregate association;
- a stale destination DATA binding rejects aggregate association;
- DATA still attached, claim not RELEASED, holder present, or stale safety input blocks Recovery eligibility/copy;
- destination DATA without a VERIFIED copy terminal blocks Recovery materialization;
- copy response `LOST` alone is neither success nor failure; only matching exact read-back converges;
- ROOT attachment success cannot stand in for DATA attachment success;
- terminal revalidates both current bindings and both attachment observation identities;
- replay with the same association identity returns the same digest;
- association identifier rebinding to Host EVACUATION is rejected;
- Recovery materialization and verification member evidence reject UPDATE.

## Safety assertions

```text
caller-supplied content-identical authority = none
caller-supplied READY/RUNNING authority     = none
caller-supplied backend path/LV UUID        = none
copy success inferred from response/exit    = no
ROOT success inferred as DATA success       = no
planned EVACUATE proof reused for Recovery  = no
fake Failure Epoch / Fencing Proof          = none
logical VM/Port/Volume desired mutation     = none
source LV deletion or capacity reclamation  = no
historical evidence rewritten               = none
production workload mutation                = none
```

## Bounded lifecycle symmetry

```text
Create:    2 STANDARD Ports + ROOT + DATA = PASS
Delete:    2 STANDARD Ports + ROOT + DATA = PASS
EVACUATE:  2 STANDARD Ports + ROOT + DATA = PASS
Recovery:  2 STANDARD Ports + ROOT + DATA = PASS
```

This is synthetic PostgreSQL authority qualification, not real Host qualification. Real OVN/OVS/libvirt/Local LVM transport, operational soak, SR-IOV and OVS-DPDK remain separate gates.

## Regression evidence

All positive commands below passed on 2026-08-20:

```text
fresh PostgreSQL 17 migrations 001–093 + exact replay
all persistence integration (105.757s)
go test -count=1 ./...
go test -race -count=1 ./...
isolated fresh-DB persistence race integration (128.166s)
libvirt-tagged Domain/VM/Volume/OVS/SR-IOV backend tests
OVN-tagged adapter tests
Geneve-tagged adapter tests
make check
git diff --check
```

The race unit suite and persistence race integration use separate database scopes. Running every integration-bearing package concurrently against one shared database is not a supported fixture topology because independent campaigns publish incompatible singleton release authorities.
