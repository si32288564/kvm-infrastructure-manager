# Generic Backend Cleanup Authority

Backend cleanup is post-authority hygiene. It is not part of Recovery success:

```text
accepted terminal/source retirement
  -> exact cleanup eligibility
  -> Cleanup Operation
  -> ordinary Job / Command / Lease / Attempt
  -> closed typed backend mutation
  -> backend read-back
  -> immutable cleanup Observation / Terminal
```

Recovery is one producer of obsolete materializations. Failed materialization,
explicit delete, aborted move, and future EVACUATE flows may use the same exact
artifact aggregate. Callers cannot provide libvirt XML, paths, flags, LVM names,
OVS commands, PCI BDF replacements, or arbitrary argv. Backend identity is
derived from immutable PostgreSQL authority.

## Resource profiles

| Artifact | Current profile |
|---|---|
| source libvirt Domain | typed `VIRTUAL_MACHINE_UNDEFINE` after terminal, logical retirement, exact Host/plan/materialization and SHUTOFF identity; standard libvirt absence read-back |
| source Local LVM LV | `BLOCKED` until destination data-independence and explicit physical cleanup policy are first-class positive authority; no capacity reclaim |
| source Network | existing generic NB/SB/source-OVS retirement is `NO_MUTATION_REQUIRED`; logical Port/LSP/MAC/IP and destination dataplane remain active |
| source PCI VF | logical retirement/handoff history remains immutable; physical driver post-release cleanup remains `BLOCKED` until disposable real-VF qualification |

Cleanup history, exact operation current state, and per-artifact source cleanup
projection are separate. Repeated A->B->C recovery therefore cannot replace an
older source incarnation with a Port-wide or VM-wide current key.

`DISPATCH_UNKNOWN` does not mean deletion failed or did not happen. A successor
claim is `READ_BACK_FIRST`; physical absence is the only positive terminal fact.
Cleanup `BLOCKED`, `UNKNOWN`, or `CONFLICTING` leaves Recovery Operation
`VERIFIED`, Failure Epoch `RECOVERED`, and the released Recovery Budget intact.

