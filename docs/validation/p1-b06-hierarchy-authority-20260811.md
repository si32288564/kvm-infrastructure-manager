# P1-B06 HostGroup Hierarchy Authority Validation

Date: 2026-08-11

Status: PASS for the implemented SYSTEM/TREE profile; P1-B06 remains In Progress.

## Scope

Migration 041 and the PostgreSQL HostGroup persistence path materialize a complete hierarchy authority for:

~~~text
group_type + dimension + SYSTEM/system
~~~

The accepted generation is split into immutable set, ordered-level, generation-bound node, and parent/child relation evidence plus one mutable current pointer. The initial graph mode is TREE: every non-root node has exactly one parent and multiple independent roots are allowed. DAG semantics are not inferred.

## Authority contract

~~~text
complete ordered levels + complete nodes + complete relations
        ↓ hierarchy-scope advisory lock
current HostGroup generation / type / dimension / level / lifecycle validation
        ↓
single-parent + strict parent-level rank validation
        ↓
immutable hierarchy evidence
        ↓ same PostgreSQL transaction
atomic current hierarchy switch
        ↓
new complete Membership Set rebind
        ↓
Snapshot / Placement dry and Final Admission
~~~

An accepted Membership Set records hierarchy ID and generation. A current hierarchy switch or any node HostGroup generation/level/lifecycle drift makes the old set ineligible for new snapshots and Placement authority. Past hierarchy, membership, snapshot, and admission evidence is not rewritten.

## PostgreSQL 17 qualification

The hierarchy integration fixture creates Site, two Rack, and Chassis Failure Domain groups in one unique dimension and verifies:

- generation 1 complete graph publication and stable replay;
- same request identity with different relations is rejected;
- multi-parent and inverted-level proposals are rejected with the old graph preserved;
- two valid generation-2 publishers serialize on the same scope, with one commit and one stale-generation conflict;
- a pre-hierarchy Membership Set cannot create a new snapshot after hierarchy publication;
- a complete Membership Set republished against current hierarchy generation creates a hierarchy-bound immutable snapshot;
- a hierarchy switch fences the older hierarchy-bound Membership Set;
- HostGroup node generation drift makes the current graph stale for new Membership Set publication;
- exact response-loss replay still recovers the original accepted hierarchy evidence after node drift;
- immutable hierarchy evidence rejects UPDATE.

The Placement integration fixture runs hierarchy changes inside an outer rollback-only transaction and verifies:

- a dry evaluation made before hierarchy publication is stale after publication;
- after Membership Set rebind, dry evaluation includes hierarchy ID/generation;
- a later hierarchy generation switch fences that dry evaluation in Final Admission;
- failed Final Admission leaves no Compute, HugePages, PCI, Network, Storage, Volume, Binding, or Attachment authority;
- the rollback-only fixture leaves no global service-class hierarchy evidence for other tests.

Command:

~~~text
KIM_POSTGRES_TEST_URL=postgres://... go test ./internal/persistence/postgres -count=1
~~~

Result: PASS on a fresh PostgreSQL 17 container.

The hierarchy integration fixture was also repeated 20 times against PostgreSQL 17. Every parallel generation switch converged to exactly one commit and one stale-generation rejection.

## Additional checks

~~~text
go test ./internal/placement ./internal/persistence/postgres ./db/migrations
git diff --check
~~~

Result: PASS.

## Deliberate boundaries

- The materialized profile is SYSTEM/system and TREE/single-parent forest. Explicit DAG policy is not implemented.
- Hierarchy membership inheritance is not implicit.
- Selector proposal/materialization is qualified separately in [P1-B06 Selector Materialization Authority Validation](p1-b06-selector-materialization-20260811.md).
- Upgrade and Maintenance consumers are not yet bound to hierarchy-backed Membership Snapshots.
- Population-wide EXACTLY_ONE completeness still depends on future Site/scope population authority.
