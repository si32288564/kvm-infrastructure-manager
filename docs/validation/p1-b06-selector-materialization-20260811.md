# P1-B06 HostGroup Selector Materialization Authority Validation

Date: 2026-08-11

Status: PASS for the implemented closed typed Selector profile; P1-B06 remains In Progress.

## Authority contract

~~~text
versioned Selector authority
  -> pure evaluation of explicit Host population
  -> immutable evaluation and per-Host input/result evidence
  -> MATCHED / NOT_MATCHED / UNKNOWN / UNSUPPORTED
  -> current generation revalidation
  -> complete Membership Set atomic publish
~~~

Selector match and evaluation are proposals, not membership authority. Only an accepted complete Membership Set generation updates current membership. The evaluator is Control Plane/PostgreSQL-side; Host Agent behavior is unchanged.

The `kim.host-group.selector/v1` language is AND-only with `EQUALS` predicates. It exposes Host identity, `x86_64`/`aarch64` architecture, and an explicit allow-list of normalized capability availability states. Arbitrary SQL, JSONPath, shell, Go expressions, filesystem paths, backend commands, unknown capability keys, and caller-defined architectures are rejected. `UNKNOWN` remains distinct from `NOT_MATCHED` and the only Phase 1 unknown policy is `FAIL_CLOSED`.

## Persistence

Migration 042 adds immutable Selector revision evidence/current authority, immutable evaluation and per-Host input evidence, a current evaluation pointer, and Selector provenance on Membership Set evidence/current projection. Selector-bound Sets record selector ID/generation and evaluation ID/generation. Snapshot and Placement consumers require that provenance to match the current ACTIVE Selector; a Selector revision change therefore fences new authority use until re-evaluation and Set materialization without rewriting historical evidence.

## PostgreSQL 17 qualification

The integration fixture verifies:

- typed Selector generation 1 evaluates current normalized x86_64/aarch64 evidence and proposes only the expected Host;
- evaluation evidence is immutable and materialization is a separate operation;
- an UNKNOWN required input remains UNKNOWN and cannot materialize a Set;
- stable materialization replay returns Set generation 1 without amplification;
- Selector generation 2 immediately makes the generation-1-bound Set unavailable to new Snapshot authority;
- inventory generation drift after evaluation rejects materialization, while a new evaluation succeeds;
- a sibling `ZERO_OR_ONE` cardinality conflict rejects the selector Set and preserves the accepted Set;
- hierarchy generation drift rejects materialization until a new evaluation binds the current graph;
- parallel identical evaluations converge to one evaluation generation/evidence;
- parallel different semantic populations yield one current decision and one stale conflict;
- migration ledger/table checks include the Selector authority tables.

Commands:

~~~text
KIM_POSTGRES_TEST_URL=postgres://... \
  go test -count=1 -run '^TestHostGroupSelectorMaterializationPostgreSQLIntegration$' ./internal/persistence/postgres

KIM_POSTGRES_TEST_URL=postgres://... \
  go test -count=1 ./internal/persistence/postgres

go test -race ./internal/persistence/postgres
make check
~~~

Result: PASS on a fresh PostgreSQL 17 container. The temporary container was removed after qualification.

## Deliberate boundaries

- population-complete `EXACTLY_ONE` waits for Site/scope population authority;
- External Assertion verification is not implemented or treated as Selector authority;
- Upgrade Wave and Maintenance consumers remain future Membership Snapshot consumers;
- Group Policy Binding, Placement Scope, Site/Tenant/Project authority, VMGroup, AffinityPolicy, topology API/UI, and Agent-side HostGroup logic remain out of scope.
