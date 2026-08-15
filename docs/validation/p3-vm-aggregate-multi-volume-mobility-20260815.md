# Phase 3 VM Aggregate Multi-Volume Mobility Qualification

- Date: 2026-08-15
- Database: fresh disposable PostgreSQL 17 (`postgres:17-alpine`)
- Migration: 092, after fresh migrations 001–092
- Profile: one VM, one STANDARD Port, one ROOT Volume, one DATA Volume, no PCI

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_MULTI_VOLUME_MOBILITY` | PASS |
| `VM_AGGREGATE_ROOT_DATA_EVACUATE_NO_DESIRED_DRIFT` | PASS |
| `VM_AGGREGATE_MULTI_VOLUME_COMPLETE_SET_FENCING` | PASS |
| `VM_AGGREGATE_MULTI_VOLUME_MOBILITY_REPLAY` | PASS |
| `VM_AGGREGATE_MULTI_VOLUME_RECOVERY` | NOT RUN |
| `GENERIC_LOCAL_LVM_SOURCE_CLEANUP` | unchanged; not required for parent VERIFIED |

## Authority change

Migration 092 closes the real cardinality gap rather than treating ROOT success as DATA success. It adds immutable per-Volume planned source safety evidence, a canonical complete safety set, per-Volume relocation members, and Volume-set cardinality/digests on relocation and aggregate mobility association evidence.

Canonical role order is derived from the immutable `Bootable` property, not JSON/caller order:

```text
ordinal 0 = ROOT (bootable=true)
ordinal 1 = DATA (bootable=false)
```

The DATA source read-back uses the closed `PLANNED_SOURCE_VOLUME_SAFETY_READ_BACK` Command. It accepts no path, LV UUID, holder state, or success boolean from the caller; exact attachment, binding, Host, VM and observed LV identity are matched against current authority and typed Command Verification.

## Qualified chain

```text
logical VM revision 1 + exact ROOT/DATA dependency snapshot
→ Final Admission / materialization / READY / RUNNING on Host A
→ aggregate terminal VERIFIED
→ StartHostEvacuation(A), immutable workload snapshot count 1
→ typed SHUTOFF and MATCHED read-back
→ Planned Source Quiescence
→ exact ROOT no-holder read-back
→ exact DATA no-holder read-back
→ canonical ROOT+DATA source Storage safety set
→ source Placement release
→ destination request compiled as canonical ROOT+DATA requirements
→ Final Admission on Host B
→ exact destination ROOT and DATA bindings
→ ROOT cross-Host copy / SHA-256 content identity VERIFIED
→ DATA cross-Host copy response LOST / READ_BACK_FIRST
→ DATA SHA-256 content identity VERIFIED
→ two-member relocation authority
→ destination materialization generation 2 / READY
→ destination RUNNING read-back
→ child verification and terminal VERIFIED
→ parent terminal VERIFIED / source Host DRAINED
→ aggregate mobility association volume_count=2
→ runtime binding A→B; logical VM/Port/ROOT/DATA desired unchanged
```

## Exact campaign identities

| Identity | Value |
|---|---|
| VM UUID | `83000001-0000-4000-8000-f0a984fd7908` |
| source Host | `vm-port-host-f0a984fd7908` |
| destination Host | `vm-port-recovery-f0a984fd7908` |
| source Admission | `admission:vm-placement:vm-port-create-operation-f0a984fd7908:1` |
| destination Admission | `admission:evacuation-repeated-destination-aggregate-root-data-f0a984fd7908` |
| source Plan / materialization | `vm-plan:vm-port-create-operation-f0a984fd7908:1` / `1` |
| destination Plan / materialization | `evacuation-repeated-destination-plan-aggregate-root-data-f0a984fd7908` / `2` |
| ROOT source Volume / Binding / LV | `vm-port-root-f0a984fd7908` / `volume-binding:vm-port-root-f0a984fd7908:1` / `lv-volume-resource` |
| ROOT destination Volume / LV | `evacuation-repeated-destination-aggregate-root-data-f0a984fd7908:root` / `lv-evacuation-repeated-destination-aggregate-root-data-f0a984fd7908` |
| DATA source Volume / Binding / LV | `vm-port-data-f0a984fd7908` / `volume-binding:vm-port-data-f0a984fd7908:1` / `lv-vm-port-data-f0a984fd7908` |
| DATA destination Volume / LV | `evacuation-repeated-destination-aggregate-root-data-f0a984fd7908:data` / `lv-evacuation-repeated-destination-aggregate-root-data-f0a984fd7908-data` |
| ROOT Copy Operation / Terminal | `local-lvm-copy-repeated-aggregate-root-data-f0a984fd7908` / `local-lvm-copy-terminal-repeated-aggregate-root-data-f0a984fd7908` |
| DATA Copy Operation / Terminal | `local-lvm-copy-repeated-data-aggregate-root-data-f0a984fd7908` / `local-lvm-copy-terminal-repeated-data-aggregate-root-data-f0a984fd7908` |
| child Verification / Terminal | `evacuation-repeated-child-verification-aggregate-root-data-f0a984fd7908` / `evacuation-repeated-child-terminal-aggregate-root-data-f0a984fd7908` |
| parent Terminal | `evacuation-repeated-parent-terminal-aggregate-root-data-f0a984fd7908` |
| aggregate association | `vm-root-data-evacuation-association-f0a984fd7908` |
| Volume evidence-set digest | `0d58ebbacbb6f988a2597992fd11f3d009e4c61e508e2a212a2d41306c33f27e` |

ROOT used SHA-256 digest `1d5aa06378228155e875fcf7d037a089c14fa463cdcb224d78beb0ebfa984fa7`. DATA used SHA-256 digest `c1384b6740fa583821d459736cf1117d83b8ed633607f0acb71ea8b57a5e2a49`; the DATA Command response was `LOST`, and the exact source/destination read-backs converged to the same digest before verification.

## Negative, replay and safety coverage

- source Placement release without the complete ROOT+DATA safety set is rejected;
- relocation with only the ROOT copy terminal is rejected;
- stale destination DATA binding before association is rejected;
- child verification and terminal require both exact copy terminals and both current destination bindings;
- the same association identifier/terminal replay returns the same immutable association;
- VM revision, runtime intent generation, dependency digest and desired digest remain unchanged;
- logical Port, ROOT Volume and DATA Volume desired digests remain unchanged;
- old source capacity claims remain reserved and are not reclaimed by relocation;
- no Failure Epoch or Fencing Proof is introduced by planned EVACUATE;
- no caller-supplied Storage state, copy success boolean, path, shell or argv is accepted;
- no source LV deletion or cleanup is required for parent `VERIFIED`.

## Scope

This campaign qualifies planned EVACUATE for ROOT+DATA and one STANDARD Port. It does not infer ROOT+DATA Recovery, real-host Local LVM transport, multi-Volume cleanup, SR-IOV, OVS-DPDK, or production readiness. Two-Port plus ROOT+DATA EVACUATE remains a separate composite-cardinality campaign.

## Regression commands

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'Test(MigratePostgreSQLIntegration|VMAggregate.*PostgreSQLIntegration|HostEvacuationRepeatedIncarnationPostgreSQLIntegration)' -timeout 900s -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -timeout 900s ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -race -count=1 -timeout 1200s ./internal/persistence/postgres
go test ./...
go test -race ./...
make check
git diff --check
```
