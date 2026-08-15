# Phase 3 VM Aggregate Multi-Port Mobility Qualification

- Date: 2026-08-15
- Database: disposable PostgreSQL 17 (`postgres:17-alpine`)
- Migration: none; existing Migration 066–69 and 084/085 authorities
- Profile: one VM, two STANDARD Ports, one ROOT Volume, no DATA Volume, no PCI

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_MULTI_PORT_MOBILITY` | PASS |
| `VM_AGGREGATE_TWO_PORT_EVACUATE_NO_DESIRED_DRIFT` | PASS |
| `VM_AGGREGATE_MULTI_PORT_COMPLETE_SET_FENCING` | PASS |
| `VM_AGGREGATE_MULTI_PORT_MOBILITY_REPLAY` | PASS |
| `VM_AGGREGATE_MULTI_VOLUME_MOBILITY` | NOT RUN |

## Qualified authority chain

```text
logical VM revision 1
+ canonical two-Port dependency snapshot
+ one VERIFIED ROOT Volume
→ Final Admission / materialization / READY / RUNNING on Host A
→ aggregate terminal VERIFIED
→ StartHostEvacuation(A)
→ immutable workload snapshot with network cardinality 2
→ typed SHUTOFF / read-back MATCHED
→ planned source quiescence / ROOT Storage SAFE
→ Port 0 retirement / source OVS absence VERIFIED
→ Port 1 retirement / source OVS absence VERIFIED
→ two exact source-quiescence evidence rows
→ source Placement release
→ destination request compiled from immutable two-Port snapshot
→ Final Admission on Host B with two handoffs
→ both logical Port realization Operations VERIFIED
→ ROOT Local LVM copy/content identity VERIFIED
→ destination materialization generation 2
→ both typed OVS preboot realizations
→ RUNNING read-back and both dataplanes CONVERGED
→ child verification with exact network binding count 2
→ child terminal / parent terminal VERIFIED
→ AssociateVMAggregateMobility
→ runtime binding A→B; logical desired unchanged
```

No caller supplies a Host binding, network success boolean, READY state or RUNNING state. Each Port traverses the ordinary retirement, quiescence, Final Admission handoff, Port resource realization and OVS observation authorities. The aggregate association requires the exact dependency cardinality and canonical destination preboot evidence set.

## Negative and replay coverage

- removing one destination Port realization after the EVACUATE terminal makes association fail closed;
- drifting only the second Port binding generation makes association fail closed;
- child verification requires both Port handoffs and both destination evidence chains;
- terminal-time per-Port current-generation fencing remains active;
- the same association identifier and terminal replay returns the same immutable association;
- association evidence records `port_count = 2`;
- logical VM revision/runtime intent/dependency digest/desired digest remain unchanged;
- both logical Port desired digests remain unchanged while Port and binding generations advance `1 → 2`.

## Scope boundary

This campaign qualifies planned Host EVACUATE for two STANDARD Ports. It does not infer multi-Port Recovery PASS, and it does not open DATA Volume mobility. Host EVACUATE currently blocks `storageCount > 1`; ROOT and DATA relocation requires an explicit multi-Volume copy/materialization authority before that gate can change.

Production OVN/OVS, real-host mutation, SR-IOV, OVS-DPDK and production Local LVM status are unchanged.
