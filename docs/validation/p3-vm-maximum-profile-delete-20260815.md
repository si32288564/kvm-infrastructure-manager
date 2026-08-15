# Phase 3 maximum public VM profile delete qualification

Date: 2026-08-15  
Database: fresh PostgreSQL 17, migrations 001–091

## Gate result

| Gate | Result |
|---|---|
| `VM_DELETE_TWO_STANDARD_PORTS_ROOT_PLUS_DATA` | PASS |
| `VM_DELETE_MAXIMUM_PROFILE_COMPOSITE_TERMINAL` | PASS |
| `VM_CREATE_DELETE_PROFILE_SYMMETRY` | PASS |
| `VM_DELETE_LOGICAL_RESOURCE_PRESERVATION` | PASS |

No schema gap was found. Migration 091 already gives the canonical two-Port absence set an independent terminal reference, and Migration 090 gives DATA absence another independent reference. The maximum profile is therefore a consumer and qualification change, not Migration 092.

## Qualified profile and chain

```text
two STANDARD logical Ports
+ one ROOT Local LVM Volume
+ one DATA Local LVM Volume
+ no PCI

VM VERIFIED/RUNNING
→ typed SHUTOFF / exact read-back
→ immutable Port 0/1 and ROOT/DATA snapshots
→ Domain ABSENT
→ Port 0 and Port 1 retirement VERIFIED
→ complete canonical network absence set
→ ROOT detach/absence VERIFIED
→ DATA detach/absence VERIFIED
→ compute RELEASED
→ all Port and Volume attachment intents RETIRED
→ delete terminal VERIFIED
→ immutable tombstone / VM DELETED
```

Terminal qualification rejects a single Port member used as the set, an incomplete set, missing DATA evidence, second-Port binding drift and DATA backend-binding drift. Exact replay is idempotent.

## Preservation boundary

- both logical Ports retain exact revisions, desired digests, MAC and IP identities;
- both Ports become `UNATTACHED`, and only physical bindings become `RELEASED`;
- ROOT and DATA logical Volume revisions and desired digests remain unchanged;
- both Volume materializations remain `VERIFIED`;
- both capacity claims remain reserved/allocated;
- VM compute allocation alone becomes `RELEASED`;
- VM alone becomes tombstoned and `DELETED`.

```text
caller-supplied Port/Host/binding authority = none
caller-supplied Storage/backend authority  = none
partial network/storage success accepted   = no
logical Port or Volume deleted with VM      = no
capacity reclaimed with VM                  = no
historical evidence rewritten               = none
```

