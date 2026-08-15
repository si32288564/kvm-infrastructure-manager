# Phase 3 VM ROOT plus DATA verified delete qualification

Date: 2026-08-15  
Database: PostgreSQL 17  
Authority schema: Migration 090

## Result

| Gate | Result |
|---|---|
| `VM_DELETE_ROOT_PLUS_DATA` | PASS |
| `VM_DELETE_ALL_STORAGE_ABSENCE` | PASS |
| `VM_DELETE_LOGICAL_VOLUME_PRESERVATION` | PASS |
| `VM_DELETE_CAPACITY_PRESERVATION` | PASS |
| `VM_DELETE_ONE_PORT_PLUS_DATA` | NOT RUN / fail-closed |
| `VM_DELETE_TWO_STANDARD_PORTS` | NOT RUN / fail-closed |

## Qualified authority chain

```text
one VM / zero Port / ROOT + DATA / no PCI
→ exact SHUTOFF read-back
→ StartVMAggregateDelete
→ ROOT and DATA logical revision + attachment + binding snapshot
→ typed Domain undefine
→ Domain ABSENT read-back
→ typed ROOT detach read-back
→ ROOT absence VERIFIED
→ typed DATA detach read-back
→ DATA absence VERIFIED
→ both attachment claims RELEASED
→ compute allocation RELEASED
→ ROOT and DATA attachment intents RETIRED
→ delete terminal VERIFIED
→ immutable VM tombstone
```

The DATA command accepts no caller path, LV, Host, binding, attachment or generation assertion. `AuthorizeVMAggregateDeleteDataAbsenceReadBack` derives the exact closed Local LVM read-back payload from `vm_delete_data_volume_operation_evidence`. Terminal authority consumes the immutable matched observation and revalidates the exact current backend binding.

## Preservation boundary

VM deletion is not Volume deletion or storage cleanup. After terminal verification:

- ROOT and DATA logical Volume revisions and desired digests are unchanged;
- both Volume resources remain `AVAILABLE`;
- both materializations remain `VERIFIED`;
- both capacity claims remain `RESERVED` or `ALLOCATED` and are not returned to free capacity;
- only the VM attachment claims/intents are released or retired;
- compute allocation is released and the VM is tombstoned.

## Negative, drift and replay coverage

- ROOT-only terminal input for a ROOT+DATA snapshot is rejected;
- missing DATA observation/absence evidence is rejected;
- DATA backend binding drift after verification and before terminal is rejected in a rollback branch;
- exact terminal replay is idempotent;
- DATA operation and absence evidence reject UPDATE;
- existing zero-Port ROOT-only and one STANDARD Port ROOT-only delete campaigns remain regression coverage;
- one-Port-plus-DATA and two-Port delete remain fail-closed.

No raw guest data, fake Recovery/EVACUATE authority, direct backend mutation or caller-supplied storage state is used.
