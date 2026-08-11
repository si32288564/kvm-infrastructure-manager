# P1-B06 Group Policy Binding Authority Validation

Date: 2026-08-11

## Certified scope

The implemented closed typed combination is:

```text
policy_type   = MAINTENANCE
consumer_type = MAINTENANCE_PLAN
```

Availability policy remains architecture/requirement scope only; this change does not fabricate Availability persistence evidence.

The authority chain is:

```text
current Host membership authorities
  -> exact HostGroup generations
  -> versioned Group Policy Bindings
  -> exact Maintenance Policy revisions/digests
  -> immutable per-Host resolution evidence
  -> immutable Maintenance Plan policy provenance
```

HostGroup membership, hierarchy, cardinality, Membership Snapshot, and policy association remain separate authorities.

## Resolution contract

- Higher numeric priority wins.
- Equal highest priority with different exact policy identity/revision/digest is `ASSIGNMENT_CONFLICT`.
- Equal highest priority resolving to the same exact identity/revision/digest is `RESOLVED`.
- A stale highest-priority assignment is `STALE_ASSIGNMENT`; lower-priority fallback is forbidden.
- `NO_ASSIGNMENT`, `ASSIGNMENT_CONFLICT`, `STALE_ASSIGNMENT`, and `UNSUPPORTED` are distinct.
- `ACTIVE`, `DRAINING`, and `RETIRED` Binding lifecycle is explicit; only current `ACTIVE` evidence is usable.
- Conflict recovery requires an explicit new Binding generation. No resolver mutates priority or lifecycle.

## PostgreSQL 17 qualification

The fresh migration and persistence integration cover:

- basic Binding publication and deterministic resolution;
- exact Policy revision preservation and explicit rebind;
- many-to-many priority selection;
- equal-priority conflict and Maintenance Plan fail-closed behavior;
- equal-priority semantic equivalence;
- stale Policy assignment without lower-priority fallback;
- membership drift with immutable historical resolution inputs;
- immutable Maintenance Plan policy provenance across live Policy drift;
- concurrent publishers with one current winner;
- resolver/update serialization to one complete Binding generation;
- stable request replay without generation amplification;
- unsupported typed combination handling;
- existing HostGroup, Placement, Upgrade Snapshot, Maintenance Snapshot, and disruptive-operation regressions.

## Migration compatibility

Migration `045_host_group_policy_binding_authority.sql` performs no semantic backfill. Pre-045 Maintenance Plans remain immutable compatibility history. New Maintenance Plans require successful current resolution for every Snapshot member and persist that resolution separately from the Membership Snapshot.

## Remaining P1-B06 gates

- Availability Policy persistence and its `PLACEMENT_POOL` Binding consumer;
- Group Policy Binding active-reference delete guards;
- Placement Scope;
- External Assertion verifier;
- population-complete `EXACTLY_ONE` after Site/managed-Host population authority;
- failure-domain Maintenance scheduling and minimum-ready gates.
