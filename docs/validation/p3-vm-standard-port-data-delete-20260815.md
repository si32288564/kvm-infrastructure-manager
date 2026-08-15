# Phase 3 one STANDARD Port + ROOT + DATA VM delete qualification

Date: 2026-08-15  
Database: fresh PostgreSQL 17, migrations 001–090

## Gate result

| Gate | Result |
|---|---|
| `VM_DELETE_ONE_STANDARD_PORT_ROOT_PLUS_DATA` | PASS |
| `VM_DELETE_COMBINED_NETWORK_STORAGE_ABSENCE` | PASS |
| `VM_DELETE_LOGICAL_PORT_IDENTITY_PRESERVATION` | PASS |
| `VM_DELETE_LOGICAL_VOLUME_AUTHORITY_PRESERVATION` | PASS |
| `VM_DELETE_TWO_STANDARD_PORTS` | NOT RUN / fail-closed |

No schema gap was found. Migration 089 network-retirement evidence and Migration 090 DATA-storage-absence evidence already have independent immutable identities and nullable terminal bindings, so the combined profile is a consumer/qualification change rather than Migration 091.

## Qualified profile

```text
one VM
+ one STANDARD logical Port
+ one ROOT Local LVM Volume
+ one DATA Local LVM Volume
+ no PCI
```

The ordinary aggregate producer creates and verifies the exact Port and both Volumes. Delete starts only after typed power authority converges to an exact `SHUTOFF` observation.

## Authority chain

```text
VM aggregate VERIFIED/RUNNING
→ typed SHUTOFF command
→ exact SHUTOFF read-back
→ StartVMAggregateDelete
→ immutable Port/ROOT/DATA incarnation snapshots
→ Domain undefine/read-back
→ Domain ABSENT evidence
→ OVN/OVS Port retirement read-back
→ network absence evidence
→ typed ROOT detach read-back
→ ROOT storage absence evidence
→ typed DATA detach read-back
→ DATA storage absence evidence
→ compute allocation RELEASED
→ Port and ROOT/DATA attachment intents RETIRED
→ delete terminal VERIFIED
→ immutable VM tombstone
→ VM DELETED
```

The terminal rejects missing network evidence, missing DATA evidence, and Port binding drift. Replay with the same evidence identifiers is idempotent.

## Preserved resource authority

VM deletion retires only runtime incarnations:

- logical Port remains the same revision and desired digest;
- MAC/IP identity remains allocated to that Port;
- Port becomes `UNATTACHED` and its physical binding becomes `RELEASED`;
- ROOT and DATA logical Volume revisions and desired digests remain unchanged;
- both Volume materializations remain `VERIFIED`;
- both storage capacity claims remain `RESERVED` or `ALLOCATED`;
- only the VM attachment intents become `RETIRED`.

Therefore `delete kim_vm` does not imply `delete kim_port`, `delete kim_volume`, or capacity reclamation.

## Safety assertions

```text
caller-supplied Port/Host/binding authority = none
caller-supplied ROOT/DATA backend authority = none
terminal inferred from command success       = no
logical Port deleted with VM                 = no
logical Volumes deleted with VM              = no
storage capacity reclaimed with VM           = no
historical evidence rewritten                = none
two-Port profile opened                      = no
```

