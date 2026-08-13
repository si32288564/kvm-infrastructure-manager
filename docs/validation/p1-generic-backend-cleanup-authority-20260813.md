# Generic Source / Backend Cleanup Authority Qualification — 2026-08-13

> Qualification note: the committed persistence lifecycle evidence for the
> UNKNOWN/read-back and repeated-Recovery cases was added by the subsequent
> `p1-cleanup-authority-qualification-hardening-20260813.md` report. Read the
> result matrix below together with that successor evidence.

## Result matrix

```text
GENERIC_BACKEND_CLEANUP_AUTHORITY          = PASS
POST_TERMINAL_CLEANUP_FENCING              = PASS
CLEANUP_UNKNOWN_READ_BACK                  = PASS
CLEANUP_REPLAY_IDEMPOTENCY                 = PASS
CLEANUP_ABA_FENCING                        = PASS
CLEANUP_IMMUTABLE_EVIDENCE                 = PASS
CLEANUP_CAPACITY_REUSE_FENCING             = PASS

GENERIC_LIBVIRT_DOMAIN_CLEANUP             = PASS
GENERIC_LOCAL_LVM_SOURCE_CLEANUP           = BLOCKED
GENERIC_NETWORK_SOURCE_CLEANUP             = PASS
GENERIC_PCI_SOURCE_CLEANUP                 = BLOCKED

REAL_POST_RECOVERY_SOURCE_CLEANUP          = BLOCKED
```

## Authority and implementation

Migration 064 adds immutable exact-incarnation cleanup eligibility, Attempt,
Observation, Terminal evidence, cleanup current state, and a per-artifact source
hygiene projection. A Recovery consumer derives source Host, VM UUID, source
plan digest, materialization generation and backend identity from an accepted
Recovery Terminal plus exact source materialization retirement. It cannot run
before Terminal and it does not update Recovery Operation, Failure Epoch, or
Budget state.

The Host Agent registers closed `VIRTUAL_MACHINE_UNDEFINE` /
`kim.command.virtual-machine-undefine/v1`. The payload is built by PostgreSQL
authority and accepts no XML, path, flags, libvirt method, shell, or argv.
The backend refuses a running Domain or a different KIM plan/materialization
metadata incarnation, calls standard libvirt `Undefine`, and verifies exact
absence. Unit qualification proves already-absent replay does not issue a
second mutation and running/foreign/open-ended requests are conflicting or
rejected.

Cleanup claims use PostgreSQL authority time. Initial work is `APPLY_ALLOWED`;
an ambiguous prior state produces successor `READ_BACK_FIRST`. Command Lease,
Attempt, Result and Verification remain the ordinary execution authority.

Local LVM deletion is intentionally not implemented: current evidence does not
prove destination data independence or authorize destruction of the source
root data. The source capacity therefore remains unreclaimed. Existing generic
OVN retirement already proves preserved NB ownership, absent source SB binding,
and absent source OVS interface; cleanup consumes that positive absence without
deleting LSP/Port/MAC/IP or mutating the destination. PCI physical post-release
driver state remains unqualified because neither real Host currently exposes a
disposable VF.

## PostgreSQL and regression

- fresh PostgreSQL 17 migrations 001–064: PASS
- full persistence integration: PASS
- libvirt VM cleanup unit/read-back tests: PASS
- generic cleanup schema FK/CHECK/immutable trigger coverage: PASS
- remaining repository/race/tagged/make regression: recorded in the commit handoff

## Real two-Host preflight

Read-only checks were run against:

- `kvm-base-g01-n001-p.core.s01.si1230.com`
- `kvm-base-g02-n001-p.core.s01.si1230.com`

The previously certified disposable UUID
`f1c06a00-0000-4000-8000-202608120058` was absent on both Hosts. No
`kimrr_campaign058` LV/VG and no `real-recovery-port060` Host OVS interface was
returned. The prior campaign report already records full physical cleanup.
There was therefore no disposable obsolete artifact on which to execute the new
authority, and no production artifact was fabricated or adopted.

```text
source Host                              = g01
destination Host                         = g02
VM UUID                                  = f1c06a00-0000-4000-8000-202608120058
Domain before/after                      = absent / absent (read-only)
source LV before/after                   = absent / absent (read-only)
source OVS before/after                  = absent / absent (read-only)
destination active artifact modified     = none
direct SSH backend mutation              = none
fixture evidence used for real PASS      = none
REAL_POST_RECOVERY_SOURCE_CLEANUP         = BLOCKED
```

This `BLOCKED` result is a safety result, not a failed cleanup. A future PASS
requires a new explicitly disposable Recovery campaign whose source Domain
still exists after Terminal. Physical Local LVM deletion additionally requires
new data-independence authority; PCI requires real disposable VFs.
