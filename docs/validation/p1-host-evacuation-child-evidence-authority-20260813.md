# Host Evacuation Child Evidence Authority Validation

- Date: 2026-08-13
- Scope: Migration 067 closed source quiescence, destination evidence binding, pure child verification, terminal-time drift fencing
- Base: Migration 066 remains byte-for-byte unchanged
- Real Host scope: none; no production workload or backend mutation

## Implemented authority chain

```text
bounded evacuation child claim
-> host_evacuation_source_shutdown_authority_evidence
-> ordinary VIRTUAL_MACHINE_POWER_STATE_ENSURE(SHUTOFF) Command
-> accepted Command Attempt + Lease
-> MATCHED command_verification_evidence
-> immutable vm_power_observation_evidence(SHUTOFF)
-> planned_source_quiescence_execution_evidence
-> destination accepted Admission + exact current VM/plan
-> definition + image + materialization readiness evidence
-> immutable vm_power_observation_evidence(RUNNING)
-> host_evacuation_destination_evidence_binding
-> pure host_evacuation_child_verification_evidence(VERIFIED)
-> terminal-time current generation recheck
-> host_evacuation_child_terminal_evidence
-> parent aggregate/finalization
```

The source quiescence API now accepts only the new evidence identifier. It no
longer accepts a Command ID, response state, read-back ID, observed power
state, identity boolean, observation generation, or digest from its caller.
The destination verifier accepts only candidate identifiers; plan generation,
readiness, RUNNING, and all positive state strings are database-derived.

## Safety behavior

- a backend response never creates SHUTOFF or RUNNING authority
- an absent or `UNKNOWN` shutdown Result remains `LOST`; a later exact libvirt
  read-back may converge it through the normal verification projector
- fake SHUTOFF and fake destination RUNNING identifiers are rejected
- source Host authority generation drift rejects quiescence, verification, and terminal
- destination Admission, VM Host, current plan, readiness evidence IDs,
  materialization generation, power evidence ID, or power observation generation
  drift rejects terminal
- zero Storage/Port/PCI requirements derive `NOT_REQUIRED` only when both the
  immutable source snapshot and destination Admission contain empty arrays
- non-zero Storage and PCI profiles remain fail-closed in this increment
- verification is immutable and separate from terminal; terminal rechecks current state
- identical verification/terminal response-loss replay is idempotent; identity reuse conflicts
- cleanup remains independent from child and parent terminal authority
- no Failure Epoch or Fencing Proof is created by planned evacuation

## Qualification matrix

| Gate | Result | Evidence |
|---|---|---|
| EVACUATION_SOURCE_QUIESCENCE_AUTHORITY | PASS | caller assertions removed; typed shutdown authority plus Attempt/Lease/Verification/SHUTOFF FK chain |
| EVACUATION_CHILD_EVIDENCE_VERIFICATION | PASS | immutable pure verification table and DB-derived verifier joins |
| EVACUATION_CHILD_TERMINAL_AUTHORITY | PASS | terminal FK to exact child verification and current-state recheck |
| EVACUATION_DESTINATION_EVIDENCE_BINDING | PASS | exact Admission/VM/plan/definition/image/readiness/power binding |
| EVACUATION_TERMINAL_DRIFT_FENCING | PASS | exact current plan, readiness generation/evidence IDs, and power generation/evidence ID recheck |
| EVACUATION_FAKE_SHUTOFF_REJECTION | PASS | PostgreSQL integration negative campaign |
| EVACUATION_FAKE_DESTINATION_RUNNING_REJECTION | PASS | PostgreSQL integration negative campaign |
| EVACUATION_BOUNDED_CONCURRENCY | PASS | existing three-child/max-two campaign retained on Migration 067 |
| EVACUATION_SOURCE_AUTHORITY_LOSS | PASS | existing source loss campaign retained; no Failure/Fencing rows |
| EVACUATION_NONEMPTY_PARENT_TERMINAL | NOT RUN | no schema-legitimate positive one-workload materialization fixture was added in this change |
| EVACUATE_ZERO_PORT | IMPLEMENTED / NOT RUN | verifier closes empty source+destination requirement arrays, but positive end-to-end fixture is absent |
| EVACUATE_LOCAL_LVM | BLOCKED | data independence unproven |
| EVACUATE_PCI_SRIOV | BLOCKED | physical VF relocation qualification absent |
| REAL_TWO_HOST_KVM_HOST_EVACUATION | BLOCKED | no disposable workload/storage profile |

## Executed validation

```text
fresh PostgreSQL 16 Migration 001..067 + replay = PASS
Host EVACUATE PostgreSQL integration           = PASS
fake SHUTOFF rejection                         = PASS
fake destination RUNNING rejection             = PASS
all persistence PostgreSQL tests                = PASS
target Go packages                              = PASS
git diff --check                                = PASS
```

Commands:

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestMigratePostgreSQLIntegration -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestHostEvacuation -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -timeout 600s ./internal/persistence/postgres
go test ./internal/persistence/postgres ./db/migrations
git diff --check
```

## Remaining qualification gap

Migration 067 closes the caller-trust authority gap. It does not by itself
claim a positive non-empty zero-Port relocation campaign. That campaign must
construct a legitimate accepted destination Admission and ordinary definition,
image, readiness, and RUNNING evidence chain; direct positive-state row
injection is not an acceptable substitute.
