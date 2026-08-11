# P1 Recovery Operation Authority Validation — 2026-08-11

## Scope

Migration 054 connects an exact Recovery Eligibility Decision and its exact GLOBAL/PLANNING Budget Claim to the first Recovery mutation-authority boundary.

```text
Eligibility Decision + RESERVED Budget Claim
  -> explicit Operation Request
  -> immutable one-destination Operation / Plan
  -> start-time safety revalidation
  -> ordinary destination Final Admission
  -> CONSUMED Budget Claim
  -> closed typed destination-preparation Job / Command
```

The executable Phase 1 action is `RESTART_ON_OTHER_HOST` through destination admission and preparation dispatch. `EVACUATE` remains a closed action in authority evidence but its backend is fail-closed as unsupported. Actual VM rematerialization, power-on, Recovery `VERIFIED`, Failure Epoch `RECOVERED`, and terminal Budget release are not claimed by this increment.

## Persistence authority

Migration 054 adds:

- immutable `recovery_operation_request_evidence`;
- immutable `recovery_operation_evidence` and `recovery_plan_evidence`;
- append-only `recovery_operation_transition_evidence` plus rebuildable `recovery_operations_current`;
- `recovery_budget_claim_transition_evidence` and independent Budget `state_generation`;
- immutable source compute release, destination Admission, and typed Execution associations;
- immutable dangerous-step safety evaluations.

Pre-054 Eligibility Decisions and Budget Claim evidence are not rewritten or backfilled into fictional Operations.

## Request, Plan, and start separation

Request replay returns the same request digest. Plan replay returns the same Operation and Plan digests. Before explicit start the qualification observes:

```text
destination Admission = 0
Recovery Job          = 0
Recovery Command      = 0
Budget state          = RESERVED / state_generation 1
```

The Plan fixes one destination Host and exact Placement Scope, candidate, historical Availability Policy, and destination request. Plan drift never selects another Host silently.

## Start-time safety fault injection

Four deterministic outer-transaction fault fixtures alter one current authority immediately before start and roll the fixture transaction back after observing the result:

1. source Host authority `FENCED -> ARMED`;
2. source Storage Claim `RELEASED -> ACTIVE`;
3. Budget Claim `RESERVED -> FENCED / state_generation 2`;
4. destination Compliance `COMPLIANT -> NON_COMPLIANT`.

Every case returns stale, leaving no destination Admission, Recovery Job, Recovery Command, or Budget consumption.

## Atomic valid start

The valid start transaction revalidates exact Epoch/Decision/Claim/Binding/Policy/action, current Fencing and Storage proof usability, and the fixed destination snapshot. It then commits atomically:

```text
source compute accounting claim  RESERVED -> RELEASED
destination Final Admission      ACCEPTED
destination compute claim        RESERVED
Budget Claim                     RESERVED/gen1 -> CONSUMED/state-gen2
Recovery Operation               PLANNED/gen1 -> RUNNING/gen2
typed preparation Command        HOST_AGENT_STATE_MARKER_ENSURE/v1
```

The destination Admission uses the ordinary Final Admission transaction and its existing Compute/PCI/Network/Storage constraints. Recovery provenance remains a separate immutable association to the Operation, Failure Epoch, and Eligibility Decision.

Recovery start and an ordinary Final Admission are executed concurrently against the same workload active-claim boundary. Recovery commits the one destination claim; the ordinary request rolls back cleanly, and PostgreSQL retains exactly one active Compute claim for the workload. Recovery receives no private capacity path or double-claim privilege.

Start response replay returns the same Admission, Job, Command, Operation generation, and Budget generation. It does not duplicate an Operation, Plan, resource claim, or dispatch.

The max-active count continues to include `CONSUMED`; while this Operation is active, the second eligible Epoch remains Budget-exhausted.

## Execution ambiguity and dangerous-step gate

The closed marker validates reuse of the existing Job/Command/Lease/Attempt/Verification path without claiming libvirt recovery. Command ambiguity projects the Operation to `UNKNOWN`; it does not release Budget or create another mutation. A MATCHED read-back advances only to `VERIFYING`.

```text
Command SUCCEEDED / MATCHED
!= Recovery VERIFIED
```

The separate dangerous-step evaluation is initially `AUTHORIZED` only while Operation, Fencing, Storage, Budget, and destination Admission are current. Storage `RELEASED -> ACTIVE -> RELEASED` makes the old Storage Proof `STALE` and returns `BLOCKED_STORAGE`. Host `FENCED -> ARMED -> FENCED` makes the old Fencing Proof `STALE` and returns `BLOCKED_FENCING`.

The dangerous-step evaluation is permission evidence only and emits no power Command.

## Regression gates

- fresh PostgreSQL 17 migrations: PASS;
- Availability / Failure / Confirmation / Fencing / Storage / Eligibility / Operation integration: PASS;
- 25 PostgreSQL integration contracts on isolated fresh databases (the three ordered HostGroup fixture tests share one fresh database): PASS;
- exact Request/Plan/start response replay: PASS;
- start-time Fencing/Storage/Budget/destination rollback: PASS;
- Recovery start versus ordinary Final Admission race: one commit, one clean reject, one active workload claim: PASS;
- Budget active-after-consume behavior: PASS;
- Execution UNKNOWN/read-back projection: PASS;
- Fencing and Storage ABA dangerous-step blocking: PASS;
- `go test -race ./...`: PASS;
- `make check`: PASS;
- documentation contract lint: PASS, 470 requirements, 713 test contracts, 231 links.

## Remaining gate

The next increment must consume the current Operation/Admission and build the actual destination VM materialization sequence using existing typed LVM/Image/Network/libvirt authorities. Destination power-on must re-run the dangerous-step gate immediately before issuing the Command. Only full backend read-back may materialize Recovery `VERIFIED`, transition the Failure Epoch to `RECOVERED`, and release the CONSUMED Budget Claim.
