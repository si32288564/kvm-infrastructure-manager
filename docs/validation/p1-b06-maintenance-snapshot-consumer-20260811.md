# P1-B06 Maintenance Snapshot Consumer Integration Validation

Date: 2026-08-11

Status: PASS for the Phase 1 `HOST_DRAIN/v1` target-set authority vertical slice; P1-B06 remains In Progress.

## Authority chain

~~~text
HostGroup current
  -> accepted Membership Set generation
  -> immutable purpose=MAINTENANCE Snapshot
  -> independent immutable Maintenance Plan
  -> Maintenance Wave
  -> Snapshot-member-derived immutable Maintenance Target
  -> current Host/concurrency/disruptive-operation eligibility
  -> typed HOST_DRAIN/v1 execution claim
~~~

Maintenance does not reuse Upgrade Plan, Campaign, Wave, Target, or Coordinator tables. The common HostGroup Snapshot table is the only shared target-set authority, and each consumer transactionally checks its exact purpose. `UPGRADE` Snapshot input is rejected by Maintenance and `MAINTENANCE` Snapshot input is rejected by Upgrade.

Plan publication fixes the Snapshot identity/digest, Membership Set generation, operation/profile revision and digest, global Wave concurrency, failure-domain maximum-unavailable policy value, target-set digest, and immutable per-Host membership provenance. Plan, Wave, Targets, provenance, and current Plan switch are committed in one PostgreSQL transaction.

The Phase 1 operation contract is closed to `HOST_DRAIN/v1`; arbitrary shell, argv, path, Ansible, or system command input is not accepted.

## PostgreSQL 17 qualification

The fixture verifies:

- accepted A/B/C Set -> MAINTENANCE Snapshot M1 -> Plan/Wave -> immutable A/B/C Targets;
- live Set A/C/D and later Set drift do not add D or remove B, and Plan/Snapshot digests remain unchanged;
- Target -> Wave -> Plan -> Snapshot -> Membership Set/member evidence provenance is reconstructable without current membership;
- UPGRADE Snapshot -> Maintenance and MAINTENANCE Snapshot -> Upgrade are rejected;
- Snapshot creation racing Set publication records one complete old/new Set generation and no mixed members;
- identical Plan replay, including commit-response-loss replay, returns the original Plan digest and creates no duplicate evidence;
- different Snapshots racing for one Maintenance/revision yield one complete Plan/Wave/Target authority and one semantic conflict;
- Coordinator generation 2 uses `RECOVER_FROM_DB` after generation-1 expiry and retains the same Snapshot and Targets;
- PAUSE followed by explicit resume retains the same Plan/Snapshot/Targets and never evaluates live membership;
- Host operation authority loss retains immutable Target evidence and fences only current execution;
- Wave global concurrency is fail closed and an ambiguous claimed Target continues to consume its slot;
- an active Maintenance Host claim prevents the same Host Upgrade Target from obtaining mutation authority;
- disruptive claim expiry alone does not authorize the other domain; accepted completion is required to release it.

Commands:

~~~text
KIM_POSTGRES_TEST_URL=postgres://... \
  go test -count=1 -run '^TestMaintenanceHostGroupSnapshotConsumerPostgreSQLIntegration$' ./internal/persistence/postgres

KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -race -count=1 ./internal/persistence/postgres
make check
~~~

Result: PASS on fresh PostgreSQL 17 databases. The temporary container was removed after qualification.

## Deliberate boundaries

- `HOST_DRAIN/v1` currently establishes typed target/execution authority; broad backend operations are not added.
- Global Wave concurrency is enforced. Failure-domain-specific active-count evaluation, minimum-ready, drain completion/read-back, and calendar windows require additional immutable Failure Domain/maintenance policy bindings and remain P1-B06 gates.
- PostgreSQL synchronous failover and a real Maintenance Coordinator process were not combined with this increment. Database-backed `RECOVER_FROM_DB` is qualified; the existing Upgrade Coordinator HA suite remains the regression authority for process/DB failover semantics.
- Population-complete `EXACTLY_ONE`, Site authority, External Assertion verification, Group Policy Binding, Placement Scope, VMGroup/Affinity, topology API/UI, and Agent-side HostGroup logic remain out of scope.
