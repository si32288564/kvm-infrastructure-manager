# Host Evacuation Orchestration Authority Validation

- Date: 2026-08-13
- Scope: Migration 066 planned Host drain, immutable workload set, parent/child authority, bounded concurrency, planned quiescence, aggregate terminal
- Real Host scope: read-only preflight only; no production workload mutation

## Status separation

### Implemented

- first-class planned `host_evacuation_operation_evidence` and current projection
- per-Host Placement drain serialized with Final Admission by `host-placement/<host_id>` advisory lock
- caller-independent current managed-workload snapshot with VM/Admission/plan/Availability/Network/Storage/PCI provenance
- parent/child current state, immutable transitions, durable slot claims/transitions, aggregate metrics
- typed planned source quiescence requiring exact identity and `SHUTOFF` read-back
- destination Admission/source-drain/current VM revalidation and child/parent terminal evidence
- source authority loss to `RECOVERY_REQUIRED`/`SOURCE_UNREACHABLE` without creating failure authority
- explicit undrain separated from evacuation terminal

### Integration-qualified

- fresh PostgreSQL 17 migrations 001–066 and replay
- same evacuation request replay; different policy conflict
- drain remains `DRAINED` after parent terminal
- Failure Epoch and Fencing Proof row counts unchanged by planned start/terminal
- synthetic PostgreSQL orchestration campaign: three current child projections, maximum concurrency two, third waits, one release admits third
- dangerous-phase Lease expiry becomes `UNKNOWN` and is not reused
- source authority loss releases slots and marks remaining child `RECOVERY_REQUIRED`

The concurrency campaign injects rebuildable child current projections to isolate claim/restart/failure reconciliation. It does not claim full destination materialization qualification.

### Real-qualified

None in this increment. No disposable multi-VM set with proven cross-Host data semantics was available.

Read-only preflight reached both intended Hosts. g01 exposed four existing Domains and g02 exposed fifteen existing production Domains. Neither Host returned a physical VF `physfn`; no dedicated disposable evacuation workload set or positively identified isolated Local LVM profile was present. No Domain state, Host lifecycle, Placement, LVM, Network, PCI, or service was changed.

### Blocked

- `EVACUATE_LOCAL_LVM`: source/destination guest-data independence is not proven; no cross-Host copy/replication system was invented
- `EVACUATE_PCI_SRIOV_REAL`: no disposable physical VF profile
- `MATERIALIZATION_CLEANUP_PRODUCER_API`: producer-specific old/new materialization terminal adapter is not implemented
- `REAL_TWO_HOST_KVM_HOST_EVACUATION`: no safe disposable two-or-three-VM set with proven storage semantics

### Not run

- real g01→g02 Host drain/multi-VM campaign
- repeated planned A→B→C physical campaign
- mixed Recovery A→B then planned EVACUATE B→C physical campaign

## PASS / BLOCKED matrix

| Gate | Result | Evidence / blocker |
|---|---|---|
| HOST_EVACUATION_AUTHORITY | PASS | Migration 066 first-class parent/current/history and replay integration |
| HOST_DRAIN_PLACEMENT_FENCING | PASS | shared Final Admission/start Host lock plus dry/final drain exclusion; fresh migration/integration |
| EVACUATION_IMMUTABLE_WORKLOAD_SET | PASS | DB-derived snapshot schema/API; caller accepts no VM list/backend target |
| EVACUATION_CHILD_AUTHORITY | PASS | separate child current/transition/claim/quiescence/terminal aggregates; parent has no backend adapter |
| EVACUATION_BOUNDED_CONCURRENCY | PASS | 3 workloads, max 2, maximum observed in-flight 2 |
| EVACUATION_RESTART_RESUME | PASS | current projection/slot authority rebuilt from PostgreSQL; no in-memory queue |
| EVACUATION_PARTIAL_OUTCOME | PASS | blocked child releases its slot and parent becomes PARTIAL without reverting siblings |
| EVACUATION_SOURCE_QUIESCENCE | IMPLEMENTED / NOT RUN | exact typed shutdown + SHUTOFF read-back API; materialized VM integration not run |
| EVACUATION_DESTINATION_ADMISSION | IMPLEMENTED / NOT RUN | exact existing Final Admission revalidation; destination planning/materialization campaign not run |
| EVACUATION_NETWORK_HANDOFF | BLOCKED | existing generic handoff remains reusable, but planned child E2E campaign is not qualified |
| EVACUATION_PARENT_TERMINAL | PASS | all verified/source active 0/post-drain Admission 0 and DRAINED persistence |
| EVACUATION_CLEANUP_INDEPENDENCE | PASS | terminal has no cleanup FK/call; cleanup producer remains explicitly blocked |
| EVACUATION_FAILURE_ESCALATION | PASS | `RECOVERY_REQUIRED`/`SOURCE_UNREACHABLE`, failure/fencing row count unchanged |
| EVACUATION_REPEATED_INCARNATION | NOT RUN | schema preserves VM/plan/materialization child generations; repeated campaign absent |
| EVACUATE_ZERO_PORT | IMPLEMENTED / NOT RUN | eligibility permits profile; real/materialization campaign absent |
| EVACUATE_OVN_PORT | IMPLEMENTED / NOT RUN | generic Port generation/Handoff is not forked; planned E2E absent |
| EVACUATE_LOCAL_LVM | BLOCKED | data independence unproven |
| EVACUATE_PCI_SRIOV | BLOCKED | physical VF qualification absent |
| REAL_TWO_HOST_KVM_HOST_EVACUATION | BLOCKED | safe disposable workload/storage profile absent |

```text
source Host read-only preflight      = kvm-base-g01-n001-p.core.s01.si1230.com (4 existing Domains)
destination Host read-only preflight = kvm-base-g02-n001-p.core.s01.si1230.com (15 production Domains)
physical VF set                      = empty on both Hosts
real campaign mutation count         = 0
```

## Authority chain

```text
Host Drain
-> immutable workload set
-> bounded child claim
-> planned source quiescence
-> source retirement
-> destination Final Admission
-> materialization
-> verification
-> child terminal
-> parent terminal
-> generic cleanup (future producer; independent)
```

## Integration qualification

```text
workloads                  = 3 synthetic current child projections
maximum concurrency        = 2
maximum observed in-flight = 2
child attempts             = 3 claims
UNKNOWN count              = 1 before source-failure reconciliation
replan count               = 0
blocked child count        = 2
parent outcome             = SOURCE_UNREACHABLE after explicit source authority loss
```

## Safety assertions

```text
fake Failure Epoch                  = none
fake Fencing Proof                 = none
direct SSH backend mutation        = none
production workload mutated        = none
cleanup failure changed evacuation = no
historical evidence rewritten      = no
```

## Commands

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'Test(MigratePostgreSQLIntegration|HostEvacuation)' -v ./internal/persistence/postgres
go test ./...
go test -race ./...
make check
git diff --check
```

Final full-regression results are recorded in the delivery commit handoff.
