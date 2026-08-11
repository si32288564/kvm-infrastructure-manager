# P1-B06 HostGroup Cardinality Authority Validation

Date: 2026-08-11

## Scope

Migration 040 and the PostgreSQL HostGroup persistence path add immutable/current cardinality policy authority for:

~~~text
group_type + dimension + level + SYSTEM/system
~~~

Accepted membership-set evidence records the exact policy identity, generation, and cardinality. Pre-040 membership sets remain immutable unbound compatibility history; generation-1 MANY is the only compatibility interpretation. A stronger/current policy requires a new complete-set publish.

## Authority contract

~~~text
HostGroup A publish ─┐
                    ├─ shared cardinality scope lock
HostGroup B publish ─┘
          ↓
proposed set + current ACTIVE sibling sets
          ↓
EXACTLY_ONE / ZERO_OR_ONE / MANY validation
          ↓
single atomic set/current projection commit
~~~

Cardinality never counts members inside one Group. It counts each Host's ACTIVE memberships across sibling HostGroups in the same class and scope.

## PostgreSQL 17 qualification

The integration fixture creates two rack Failure Domain groups and one Host, promotes the class policy from default MANY to generation 2 ZERO_OR_ONE, and publishes the Host into both groups concurrently.

Expected and observed:

- exactly one sibling publisher commits;
- the other returns ErrHostGroupConflict;
- current sibling ACTIVE membership count is one;
- policy generation 3 EXACTLY_ONE makes the generation-2-bound set stale;
- snapshot creation fails until a complete set is republished against generation 3;
- removing the last materialized membership under EXACTLY_ONE is rejected;
- immutable policy evidence rejects UPDATE.

Command:

~~~text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'TestHostGroup(CardinalityAuthority|MembershipSetAuthority|AuthorityAndSnapshot)PostgreSQLIntegration' ./internal/persistence/postgres
~~~

Result: PASS on PostgreSQL 17.

## Deliberate boundary

This increment does not claim global EXACTLY_ONE completeness for Hosts absent from every sibling Group. That requires a versioned population/scope authority (Site/Project/all-managed-Host population). The enforced minimum-one obligation is limited to the materialized Host population affected by a publish; exclusive multi-membership is enforced across all current ACTIVE sibling sets.
