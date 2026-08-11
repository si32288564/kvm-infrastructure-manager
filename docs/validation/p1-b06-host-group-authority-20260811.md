# P1-B06 HostGroup Authority Validation

- Date: 2026-08-11
- Scope: HostGroup core, immutable membership snapshot, Placement compatibility/fencing
- Status: PASS for the implemented foundation; P1-B06 remains In Progress

## Implemented authority

Migration 038 introduces immutable HostGroup revision and membership evidence,
current HostGroup and many-to-many membership authority, and immutable snapshot
evidence with members bound by foreign key to accepted membership evidence.

HostGroup type is closed to `PLACEMENT_POOL`, `FAILURE_DOMAIN`, and
`OPERATIONAL_COHORT`. A HostGroup identity cannot change type, dimension, or
level through a later generation. Materialized membership preserves source,
source revision, generation, state, and digest.

## Placement compatibility and fencing

The existing Placement Pool entry points atomically record the corresponding
`PLACEMENT_POOL` HostGroup authority and keep the legacy Phase 1 tables as a
compatibility projection. Stable identical replay is idempotent; the same
generation with different semantics is rejected.

Dry Placement reads the exact requested Placement Pool HostGroup membership.
Final Admission takes the same HostGroup lock, repeats evaluation, and rejects
a changed HostGroup or membership generation without retaining partial claims.

## Snapshot contract

A snapshot captures the sorted set of active materialized members and their
accepted membership generations and digests. Later membership removal does not
alter an existing snapshot. Replay with the same snapshot identity recovers the
original evidence instead of re-evaluating current membership.

The snapshot is target evidence only. It does not authorize an Upgrade,
Maintenance, Baseline, Placement, or backend mutation.

## Verification

- migration, placement, and PostgreSQL persistence package tests: PASS
- fresh PostgreSQL 17 HostGroup authority/snapshot integration: PASS
- Placement membership and HostGroup generation fencing: PASS
- same-generation semantic conflict: rejected
- non-monotonic lifecycle transition and new snapshot while `DRAINING`: rejected
- immutable evidence update: rejected by PostgreSQL trigger
- one Host in Placement Pool, Failure Domain, and Operational Cohort: PASS
- snapshot replay after membership removal retains the original target: PASS
- legacy schema 001-037 Pool generation 7 / membership generation 9 backfill through migration 038: PASS

## Explicitly not qualified

- cardinality rules and hierarchy graph
- selector evaluation and authenticated external assertion verification
- Group Policy Binding and effective Availability Policy resolution
- Tenant-facing Placement Scope
- Baseline, Maintenance, and Upgrade consumers bound to snapshots
- bulk membership commit and response-loss qualification
- delete/reference guard

These remain P1-B06 follow-up work and are not implied by this PASS.
