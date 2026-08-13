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

Recovery is the currently implemented producer of obsolete materializations.
Failed materialization, explicit delete, aborted move, and future EVACUATE
flows may use the same exact artifact aggregate only after their own closed
producer validates current authority and emits an immutable generic origin
eligibility adapter. Merely selecting a schema enum does not grant cleanup.
Callers cannot provide libvirt XML, paths, flags, LVM names, OVS commands, PCI
BDF replacements, or arbitrary argv. Backend identity is derived from immutable
PostgreSQL authority.

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
claim is `READ_BACK_FIRST` and PostgreSQL permits only the observation-only
`VIRTUAL_MACHINE_CLEANUP_READ_BACK/v1` Command first. Physical absence is the
only positive terminal fact. If that read-back instead proves the exact inactive
Domain remains present, its immutable evidence permits a separate explicit
`VIRTUAL_MACHINE_UNDEFINE/v1` authorization in the same current Claim. UNKNOWN,
running, or foreign identity never permits apply.
Cleanup `BLOCKED`, `UNKNOWN`, or `CONFLICTING` leaves Recovery Operation
`VERIFIED`, Failure Epoch `RECOVERED`, and the released Recovery Budget intact.

Network cleanup binds the immutable exact A→B Handoff, source quiescence, and
Port/Binding retirement evidence. It does not require A→B to remain the
Port-wide current Handoff after a later B→C move; current logical Port/IP/MAC and
the destination dataplane are still required and preserved.

Migration 066はplanned Host Evacuation child terminalまでを実装するが、
`MATERIALIZATION_CLEANUP_PRODUCER_API`は未実装である。EVACUATE parent/childは
generic cleanup rowを直接insertせず、cleanup未実施またはBLOCKEDでもevacuation
terminalを変更しない。future producerはexact old materialization、new verified
materialization、source Host/plan/generationをproducer-specific immutable evidenceで
検証してからMigration 065 origin adapterを発行する必要がある。
