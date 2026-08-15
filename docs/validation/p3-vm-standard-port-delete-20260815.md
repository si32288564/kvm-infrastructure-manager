# Phase 3 VM one STANDARD Port verified delete qualification

Date: 2026-08-15  
Database: PostgreSQL 17  
Authority schema: Migration 089

## Result

| Gate | Result |
|---|---|
| `VM_DELETE_ONE_STANDARD_PORT_ONE_ROOT` | PASS |
| `VM_DELETE_LOGICAL_PORT_PRESERVATION` | PASS |
| `VM_DELETE_NETWORK_ABSENCE_BINDING` | PASS |
| `VM_DELETE_TWO_STANDARD_PORTS` | NOT RUN / fail-closed |
| `VM_DELETE_ROOT_PLUS_DATA` | NOT RUN / fail-closed |

## Qualified profile and chain

The PostgreSQL integration campaign creates one VM with one STANDARD logical Port, one ROOT Volume and no PCI. After aggregate `RUNNING` verification it uses the ordinary typed power command and exact `SHUTOFF` read-back, then executes:

```text
StartVMAggregateDelete
→ exact VM/runtime/Admission/Host/Port/binding snapshot
→ typed Domain undefine
→ exact Domain ABSENT observation
→ DB-derived OVN Port-binding retirement authorization
→ OVN/OVS retirement observation VERIFIED
→ VM delete network absence evidence
→ exact ROOT detach read-back
→ Storage absence
→ Compute release
→ Port attachment intent RETIRED
→ exact physical Port binding RELEASED
→ logical Port UNATTACHED
→ delete terminal VERIFIED
→ immutable VM tombstone
```

The caller supplies neither Port, Host, generation nor binding identity to the retirement consumer. Those values come from the immutable delete snapshot. The generic retirement evidence must prove ownership, logical switch Port preservation, requested chassis absence, inactive source chassis, and source OVS interface absence.

## Negative and immutability coverage

- terminal without network absence evidence is rejected;
- terminal after exact Port binding drift is rejected and rolled back;
- network absence and delete terminal exact-identifier replay are idempotent; identifier rebinding is rejected;
- generic retirement evidence must match the snapshotted Port generation, binding generation and source Host;
- `vm_delete_network_operation_evidence` and `vm_delete_network_absence_evidence` reject UPDATE;
- the existing zero-Port wrapper remains qualified and replay-compatible;
- two-Port and ROOT+DATA delete remain fail-closed.

## Identity outcome

VM terminal deletion does not delete or rewrite the logical Port resource. Its revision and desired digest are unchanged; MAC/IP allocation remains owned by the Port. Only the exact runtime attachment is retired, the physical binding becomes `RELEASED`, and the Port returns to `UNATTACHED` with no workload or Admission pointer.

This is not a Recovery or EVACUATE consumer, does not create Failure/Fencing evidence, and does not infer absence from command success.
