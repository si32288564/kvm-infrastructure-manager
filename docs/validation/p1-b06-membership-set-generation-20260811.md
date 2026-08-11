# P1-B06 Membership Set Generation Validation

- Date: 2026-08-11
- Scope: whole-group Membership Set Generation authority, atomic publication, Placement and snapshot fencing
- Status: PASS for this increment; P1-B06 remains In Progress

## Implemented authority

Migration 039 separates HostGroup semantic/lifecycle generation from the
whole-group membership-set generation. The persistence chain is:

```text
host_group_revision_evidence
  -> host_group_membership_set_evidence (immutable)
  -> host_group_membership_set_member_evidence (immutable)
  -> host_group_membership_sets_current
  -> host_group_memberships_current
```

Each accepted set fixes the source type/revision, optional selector and
hierarchy provenance generations, canonical member-set digest, member count,
validation state, and `based_on_host_group_generation`. Individual membership
evidence remains immutable lower-level provenance and is not accepted set
authority by itself.

## Atomic publication and replay

The publisher takes a HostGroup-scoped PostgreSQL authority lock, validates the
current ACTIVE HostGroup generation and expected current set generation, writes
all immutable member/set evidence, then changes the current set pointer and all
current member projections in one transaction. An omitted prior member becomes
a `REMOVED` tombstone in the new set; no historical evidence is deleted.

A stable publish request replay returns the original accepted evidence. A new
request with identical group generation, source provenance, optional selector/
hierarchy generations, and canonical set digest does not create a new set
generation. Reusing a request identity with different semantics is rejected.

## Placement and snapshot binding

Dry Placement includes `membership_set_generation` in the authority snapshot
and evaluation digest. Final Admission revalidates HostGroup generation,
accepted set generation, current candidate member generation/state, and then
commits the set generation into immutable Admission evidence. A stale set or
member causes all resource claims to roll back.

Membership Snapshot creation reads immutable members from the accepted set,
not a live-row scan. It records source set generation and set digest. Later set
publication cannot alter the snapshot or its member rows.

## PostgreSQL 17 qualification

- fresh migration 001 through 039: PASS
- legacy 001 through 038 authority backfill into historical/current accepted sets: PASS
- set generation 1 with A/B/C and set generation 2 with A/C/D: PASS
- atomic current pointer and member projection switch: PASS
- failed complete-set transaction leaves old set current and no partial evidence: PASS
- request replay and semantic replay do not amplify generation: PASS
- same request identity with different member semantics: rejected
- stale expected set generation and parallel publisher: one commit, stale peer rejected
- stale `based_on_host_group_generation`: rejected
- current member projection that does not match accepted set-member evidence: rejected by composite foreign key
- `DRAINING` HostGroup publication: rejected
- omitted Host B retained as immutable `REMOVED` evidence and excluded from active membership
- set 1 evidence remains unchanged after set 2/current projection switch
- snapshot remains bound to set 2 after a later set 3 publish
- immutable set evidence UPDATE: rejected by PostgreSQL trigger
- Placement dry/final stale membership/group generation rollback: PASS
- all persistence integration tests: PASS
- race detector: PASS
- `make check`: PASS
- docs lint: PASS

## Explicitly not implemented or qualified

- cardinality authority
- hierarchy authority or graph validation
- selector evaluator/materializer
- Group Policy Binding
- Placement Scope
- Upgrade and Maintenance consumers of snapshots
- UI and Agent-side membership logic

The optional selector/hierarchy provenance fields do not imply those authority
domains are implemented. They only prevent future materialization from losing
the generation against which a set was evaluated.
