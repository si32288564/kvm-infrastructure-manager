# Phase 3 VM Aggregate Maximum-Profile Mobility Qualification

- Date: 2026-08-15
- Database: disposable PostgreSQL 17 (`postgres:17-alpine`)
- Migrations: fresh 001–092
- Migration added by this change: none
- Profile: one VM, two STANDARD Ports, one ROOT Volume, one DATA Volume, no PCI

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_MAXIMUM_PROFILE_EVACUATE` | PASS |
| `VM_AGGREGATE_MAXIMUM_PROFILE_NO_DESIRED_DRIFT` | PASS |
| `VM_AGGREGATE_COMPOSITE_COMPLETE_SET_FENCING` | PASS |
| `VM_AGGREGATE_MAXIMUM_PROFILE_MOBILITY_REPLAY` | PASS |
| `VM_AGGREGATE_MULTI_PORT_RECOVERY` | NOT RUN |
| `VM_AGGREGATE_MULTI_VOLUME_RECOVERY` | NOT RUN |

No schema gap was found. Migration 091 already supplies the canonical complete two-Port authority, and Migration 092 supplies the canonical complete ROOT+DATA authority. The qualification composes both independent sets in one existing Host EVACUATE child, parent terminal, and aggregate mobility association.

## Qualified chain

```text
logical VM revision 1
+ canonical Port set [Port 0, Port 1]
+ canonical Volume set [ROOT, DATA]
→ Final Admission / materialization / READY / RUNNING on Host A
→ aggregate terminal VERIFIED
→ StartHostEvacuation(A), workload snapshot count 1
→ typed SHUTOFF / MATCHED read-back
→ Planned Source Quiescence
→ ROOT and DATA exact no-holder safety set
→ Port 0 and Port 1 retirement / source quiescence
→ source Placement release
→ destination Final Admission with two Port handoffs and two Volumes
→ both destination Port realizations
→ ROOT copy/content identity VERIFIED
→ DATA copy LOST / READ_BACK_FIRST / content identity VERIFIED
→ two-member relocation authority
→ destination materialization / READY
→ both OVS preboot realizations / RUNNING read-back
→ child verification: Network complete set + Storage complete set
→ child terminal / parent terminal VERIFIED
→ aggregate association port_count=2, volume_count=2
→ runtime Host A→B; all logical desired authorities unchanged
```

## Exact campaign identities

| Identity | Value |
|---|---|
| VM UUID | `83000001-0000-4000-8000-f204e9b244f8` |
| source Host | `vm-port-host-f204e9b244f8` |
| destination Host | `vm-port-recovery-f204e9b244f8` |
| source Admission | `admission:vm-placement:vm-port-create-operation-f204e9b244f8:1` |
| destination Admission | `admission:evacuation-repeated-destination-aggregate-maximum-profile-f204e9b244f8` |
| source Plan / materialization | `vm-plan:vm-port-create-operation-f204e9b244f8:1` / `1` |
| destination Plan / materialization | `evacuation-repeated-destination-plan-aggregate-maximum-profile-f204e9b244f8` / `2` |
| Port 0 | `vm-port-resource-0-f204e9b244f8`, revision `1`, destination generation/binding `2/2` |
| Port 1 | `vm-port-resource-1-f204e9b244f8`, revision `1`, destination generation/binding `2/2` |
| ROOT source Volume / LV | `vm-port-root-f204e9b244f8` / `lv-volume-resource` |
| ROOT destination Volume / LV | `evacuation-repeated-destination-aggregate-maximum-profile-f204e9b244f8:root` / `lv-evacuation-repeated-destination-aggregate-maximum-profile-f204e9b244f8` |
| DATA source Volume / LV | `vm-port-data-f204e9b244f8` / `lv-vm-port-data-f204e9b244f8` |
| DATA destination Volume / LV | `evacuation-repeated-destination-aggregate-maximum-profile-f204e9b244f8:data` / `lv-evacuation-repeated-destination-aggregate-maximum-profile-f204e9b244f8-data` |
| ROOT Copy Terminal | `local-lvm-copy-terminal-repeated-aggregate-maximum-profile-f204e9b244f8` |
| DATA Copy Terminal | `local-lvm-copy-terminal-repeated-data-aggregate-maximum-profile-f204e9b244f8` |
| child Verification | `evacuation-repeated-child-verification-aggregate-maximum-profile-f204e9b244f8` |
| child Terminal | `evacuation-repeated-child-terminal-aggregate-maximum-profile-f204e9b244f8` |
| parent Terminal | `evacuation-repeated-parent-terminal-aggregate-maximum-profile-f204e9b244f8` |
| aggregate association | `vm-aggregate-maximum-profile-evacuation-association-f204e9b244f8` |
| Port evidence-set digest | `05ec611427084665d6d43ddc3d38168bc0dfb1fe88ac3a3793cf6c7f0befc4a6` |
| Volume evidence-set digest | `61b50726ff9796bdcabbbd6613b7e09fc00d0c236fc5fe281c8f92235d886731` |

ROOT source/destination SHA-256 is `426a4782f7766b510edb917ddbc76e877f17046c0154dcee16e8b9a8a0890468`. DATA source/destination SHA-256 is `4a0955f6376e517c98b55e5ca790937618dc127b192e098d9857ad6513362a5b`; its response state is `LOST`, with success derived only after exact source/destination read-back.

## Complete-set, drift and replay coverage

- deleting only Port 1 destination realization makes association fail closed;
- stale DATA destination binding makes child verification, terminal completion, and association fail closed;
- source release without both Storage safety members is rejected;
- relocation with only ROOT copy terminal is rejected;
- child and parent terminals require both Port members and both Volume members;
- replaying the same association identifier and parent terminal returns the same association digest;
- association evidence records `port_count=2` and `volume_count=2` with independent set digests;
- VM revision, runtime intent generation, dependency digest and desired digest do not change;
- both logical Port revisions/digests and both logical Volume revisions/digests do not change;
- physical Port/binding generations advance `1 → 2`, materialization advances `1 → 2`;
- source capacity remains reserved; relocation performs no source LV deletion or cleanup;
- planned EVACUATE creates no Failure Epoch or Fencing Proof.

## Public profile symmetry

The bounded public maximum profile now has synthetic authority qualification for:

```text
Create:    2 STANDARD Ports + ROOT + DATA = PASS
Delete:    2 STANDARD Ports + ROOT + DATA = PASS
EVACUATE:  2 STANDARD Ports + ROOT + DATA = PASS
```

This is not production qualification. Real Host/OVN/OVS/Local LVM transport, maximum-profile Recovery, SR-IOV, OVS-DPDK and operational soak remain separate gates.
