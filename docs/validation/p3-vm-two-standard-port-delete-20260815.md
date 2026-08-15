# Phase 3 two STANDARD Port VM delete qualification

Date: 2026-08-15  
Database: fresh PostgreSQL 17, migrations 001–091

## Gate result

| Gate | Result |
|---|---|
| `VM_DELETE_TWO_STANDARD_PORTS_ROOT_ONLY` | PASS |
| `VM_DELETE_NETWORK_ABSENCE_SET_AUTHORITY` | PASS |
| `VM_DELETE_ALL_PORT_TERMINAL_FENCING` | PASS |
| `VM_DELETE_TWO_PORT_LOGICAL_IDENTITY_PRESERVATION` | PASS |
| `VM_DELETE_TWO_STANDARD_PORTS_ROOT_PLUS_DATA` | NOT RUN / fail-closed |

## Authority model

Migration 089 is intentionally unchanged as the one-Port compatibility authority. Migration 091 adds:

- canonical `port_ordinal` 0 and 1 operation snapshots;
- exact per-Port OVN/OVS retirement consumers;
- immutable per-Port absence evidence;
- one immutable complete absence-set evidence;
- an exact absence-set reference in the delete terminal.

Caller order is not authority. The dependency snapshot's canonical logical Port order is preserved through delete evidence.

## Qualified chain

```text
two-Port VM VERIFIED/RUNNING
→ typed SHUTOFF and exact read-back
→ StartVMAggregateDelete
→ immutable Port ordinal 0/1 snapshots
→ Domain ABSENT
→ Port 0 OVN/OVS retirement VERIFIED
→ Port 0 absence
→ Port 1 OVN/OVS retirement VERIFIED
→ Port 1 absence
→ complete canonical absence set
→ ROOT detach/absence
→ compute RELEASED
→ both Port attachment intents RETIRED
→ both bindings RELEASED
→ delete terminal VERIFIED
→ VM tombstone / DELETED
```

The incomplete one-member set, a member identifier passed as a set, an out-of-range ordinal, and second-Port binding drift are rejected. Exact terminal and absence-set replay are idempotent.

## Preservation and safety

Both logical Ports retain their exact revisions, desired digests, MAC and IP identities. Each becomes `UNATTACHED`; only its physical Host binding is `RELEASED`. The ROOT logical Volume, capacity and materialization authority are preserved under the existing delete contract.

```text
caller-supplied Port/Host/binding authority = none
partial Port success accepted               = no
logical Port deleted with VM                = no
MAC/IP released with VM                     = no
historical evidence rewritten               = none
two-Port-plus-DATA profile opened           = no
```

