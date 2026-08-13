# Cleanup Authority Qualification Hardening — 2026-08-13

## Result

```text
CLEANUP_POSTGRESQL_LIFECYCLE            = PASS
CLEANUP_UNKNOWN_READ_BACK               = PASS
CLEANUP_READ_BACK_FIRST_APPLY_FENCING   = PASS
CLEANUP_REPLAY_IDEMPOTENCY              = PASS
CLEANUP_IMMUTABLE_EVIDENCE              = PASS
CLEANUP_GENERIC_ORIGIN_ADAPTER          = PASS
CLEANUP_DELAYED_A_TO_B_AFTER_B_TO_C     = PASS

MATERIALIZATION_CLEANUP_PRODUCER_API     = BLOCKED
DELETE_CLEANUP_PRODUCER_API              = BLOCKED
REAL_POST_RECOVERY_SOURCE_CLEANUP        = BLOCKED
```

Migration 065 adds an immutable producer adapter between origin-specific
authority and the generic cleanup aggregate. Recovery is the only production
producer implemented today. `MATERIALIZATION` and `DELETE_OPERATION` remain
schema identities without production producer APIs and therefore cannot grant
cleanup through application code.

## PostgreSQL lifecycle

The committed PostgreSQL 17 integration fixture executes:

```text
exact obsolete materialization cleanup
→ claim 1 APPLY_ALLOWED
→ typed undefine Command authority
→ UNKNOWN observation / response LOST
→ DISPATCH_UNKNOWN
→ claim 2 READ_BACK_FIRST
→ direct undefine authorization rejected
→ observation-only VIRTUAL_MACHINE_CLEANUP_READ_BACK/v1
→ ABSENT observation
→ one immutable Terminal VERIFIED
```

A rollback branch on the same current claim also proves:

```text
READ_BACK_FIRST
→ exact inactive PRESENT observation
→ immutable non-terminal observation
→ separate explicit VIRTUAL_MACHINE_UNDEFINE/v1 authorization allowed
```

UNKNOWN, running, foreign identity, missing observation, stale owner, or stale
claim generation cannot reach that apply authorization. The libvirt backend
unit test separately proves the read-back command never invokes `Undefine()`
and an already-absent replay leaves the physical mutation count unchanged.

The lifecycle fixture uses a transaction-scoped synthetic generic
materialization producer adapter and rolls the fixture back. This is database
qualification, not real-Host evidence and not a claim that the future
Materialization producer API exists.

## Delayed network cleanup

The existing PostgreSQL repeated-incarnation test now advances one logical Port
through:

```text
A binding 1/1 retirement + A→B Handoff 1/1→2/2
→ B binding 2/2 retirement + B→C Handoff 2/2→3/3
→ delayed A cleanup evidence selection
```

After the Port-wide current Handoff points to B→C, the delayed A cleanup still
resolves exactly one required Port from immutable A→B Handoff, A quiescence,
and A NB/SB/OVS retirement evidence. Active IP and MAC Claims and the current
logical Port remain present. No Network mutation is introduced.

## Safety boundaries

- No production Host, VM, OVN/OVS object, LV, route, service, or PCI device was
  mutated.
- No real cleanup Operation was created.
- Local LVM destructive cleanup and PCI physical cleanup remain BLOCKED.
- Real post-Recovery cleanup remains BLOCKED because no dedicated disposable
  obsolete source artifact was available and none was fabricated.

## Regression

- fresh PostgreSQL 17 migrations 001–065: PASS
- full PostgreSQL persistence integration: PASS (6.219s)
- targeted cleanup lifecycle and delayed A→B after B→C integration: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- libvirt-tag backend/helper/Host Agent tests: PASS
- `make check`: PASS
- documentation lint: PASS (487 requirements, 745 test contracts, 233 links)
- `git diff --check`: PASS
