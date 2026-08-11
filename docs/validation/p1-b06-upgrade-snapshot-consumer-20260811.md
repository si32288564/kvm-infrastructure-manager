# P1-B06 Upgrade Snapshot Consumer Integration Validation

Date: 2026-08-11

Status: PASS for HostGroup-targeted Upgrade Campaigns; P1-B06 remains In Progress.

## Authority chain

~~~text
HostGroup current
  -> accepted Membership Set generation
  -> immutable purpose=UPGRADE Snapshot
  -> immutable Plan/Snapshot binding
  -> ordered Wave
  -> Snapshot-member-derived immutable Target
  -> Coordinator / Target executor
~~~

The Snapshot fixes HostGroup generation, Membership Set generation/digest, selector evaluation provenance when present, Cardinality Policy generation, hierarchy generation when present, member evidence, member count, and a canonical snapshot digest. Pre-043 snapshots and upgrade evidence remain immutable compatibility history and are not retroactively attributed to a Snapshot.

Plan publication loads only an immutable UPGRADE Snapshot, derives deterministic Target identities from its member evidence, and commits Plan, Snapshot binding, Wave, Target, provenance, current Target execution rows, and Campaign current switch in one PostgreSQL transaction. Canonical serialization order is not rollout priority.

## PostgreSQL 17 qualification

The fixture verifies:

- accepted A/B/C Set -> Snapshot S1 -> Plan/Wave -> immutable A/B/C Targets;
- live Set A/C/D and later Set drift do not add D or remove B from the active Wave;
- Snapshot creation racing Set publication records one complete old/new Set generation and never mixed member generations;
- identical Plan replay returns the same Plan digest without new Wave/Target evidence;
- two different Snapshot publishers for one Campaign/revision yield one complete Plan and one semantic conflict, with no partial loser rows;
- Coordinator generation 2 uses `RECOVER_FROM_DB` with the same Plan revision, Snapshot digest, and Targets after generation-1 expiry and membership drift;
- PAUSE followed by explicit resume keeps the same Plan/Snapshot/Targets and does not evaluate live membership;
- Target audit joins Target -> Wave -> Plan Snapshot binding -> Snapshot -> Set member -> membership evidence;
- changing a Snapshot member Host operation authority to `FENCED` preserves immutable Target evidence and fences current Target execution.

Commands:

~~~text
KIM_POSTGRES_TEST_URL=postgres://... \
  go test -count=1 -run '^TestUpgradeHostGroupSnapshotConsumerPostgreSQLIntegration$' ./internal/persistence/postgres

KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 ./internal/persistence/postgres
go test -race ./internal/persistence/postgres
make check
~~~

Result: PASS on a fresh PostgreSQL 17 container. The temporary container was removed after qualification.

## Deliberate boundaries

- Existing non-HostGroup Campaigns remain supported.
- Site/population-complete `EXACTLY_ONE`, External Assertion verification, Maintenance Snapshot consumer, Group Policy Binding, Placement Scope, VMGroup/Affinity, topology API/UI, and Agent-side HostGroup logic remain out of scope.
- PostgreSQL synchronous failover was not combined with this increment; the existing Coordinator HA suite remains the regression authority for database failover semantics.
