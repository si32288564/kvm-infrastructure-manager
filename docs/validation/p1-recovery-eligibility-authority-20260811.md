# Recovery Eligibility Authority Validation

Date: 2026-08-11

## Scope

Migration 053 adds a permission authority between typed safety proofs and a future Recovery Operation.

```text
FENCED Failure Epoch
+ exact historical Availability Binding / Policy / responsibility / action
+ current-usable Fencing Proof
+ current-usable Storage Safety Proof
+ typed GLOBAL / PLANNING Recovery Budget
+ read-only destination candidate snapshot
  -> immutable Recovery Eligibility Evaluation
  -> explicit Recovery Eligibility Decision
  -> atomic Planning Budget Claim
```

`ELIGIBLE` is not a Recovery Operation, Placement Admission, restart, evacuation, Job, Command, Lease, or destination resource reservation.

## Persistence authority

- `recovery_budget_policy_revision_evidence` is immutable typed policy evidence. Phase 1 only supports `GLOBAL / PLANNING / max_active_recoveries`.
- `availability_policy_recovery_budget_binding_evidence` binds an exact AvailabilityPolicy revision/digest to an exact Budget Policy revision/digest. Historical policies are not rewritten.
- `recovery_eligibility_evaluation_evidence` records the exact Epoch transition, historical Binding/Policy, responsibility/action, Confirmation Decision, proof identities/current-usability classifications, budget snapshot, candidate snapshot, evaluator identity, and digest.
- destination candidate and visibility evidence preserve Placement Scope, HostGroup/Membership Set, Host capability/readiness, ordinary Placement evaluation, hierarchy/pool policy, and Availability Policy provenance.
- `recovery_eligibility_decision_evidence` is immutable explicit permission evidence. One Failure Epoch and one Evaluation can have at most one accepted Decision.
- `recovery_budget_claim_evidence/current` is the durable planning admission committed with the Decision. This increment emits generation 1 `RESERVED` only; consumption/release belongs to a future Recovery Operation/terminal verification gate.

## Proof current usability and ABA

Historical proof rows remain immutable. Eligibility follows the proof to its exact source authority and rechecks current state:

- Fencing: source Host identity, exact Host authority generation/FENCED event identity, exact libvirt power evidence/generation, `SHUTOFF / MATCHED`.
- Storage: exact Attachment evidence/generation, `DETACHED`, device absent, holder closed, Claim `RELEASED` plus `claim_state_generation`, Binding generation/observation and `BOUND`.

The PostgreSQL qualification advances `RELEASED -> ACTIVE -> RELEASED` and verifies the old Storage Proof becomes `STALE`. It also advances `FENCED -> ARMED -> FENCED` and verifies the old Fencing Proof remains historical but becomes unusable. Matching terminal state names do not defeat generation/evidence fencing.

## Destination semantics

The implementation reuses `DryEvaluateAvailabilityPlacementScope`; it does not add a recovery-specific scheduler. The Failure Epoch source Host is always `SOURCE_EXCLUDED`. A different Host is `ELIGIBLE` only when ordinary Placement eligibility and exact current effective Availability Policy compatibility both hold.

Evaluation records candidates but makes no resource claims. Decision re-runs the same read-only evaluation and requires the candidate snapshot digest/count to match. A readiness/compliance drift between Evaluation and Decision returns stale; it does not silently select another current candidate.

## Budget concurrency and replay

The certified fixture creates two FENCED Failure Epochs with positive Evaluations against a policy whose `max_active_recoveries` is 1. Concurrent distinct Decisions serialize on the exact Budget Policy/scope authority:

- one Decision and one Budget Claim commit;
- the other returns budget exhausted;
- active planning claims remain 1;
- same-ID parallel replay returns the original Decision and Claim digests;
- a distinct Decision identity for the already-authorized Epoch is fenced;
- no Decision/Claim count amplification occurs.

## Responsibility and result states

Automatic positive eligibility is limited to `INFRASTRUCTURE_MANAGED` with `RESTART_ON_OTHER_HOST` or `EVACUATE`. `WORKLOAD_MANAGED`, `MANUAL`, and `NO_AUTOMATIC_ACTION` fail closed.

The closed result set distinguishes:

- `ELIGIBLE`
- `EPOCH_NOT_CONFIRMED`, `STALE_EPOCH`
- `FENCING_PROOF_MISSING / STALE / UNKNOWN`
- `STORAGE_PROOF_MISSING / STALE / UNKNOWN`
- `RESPONSIBILITY_BLOCKED`, `NO_AUTOMATIC_ACTION`
- `NO_RECOVERY_BUDGET_POLICY`, `BUDGET_EXHAUSTED`, `STALE_POLICY`
- `NO_DESTINATION`, `DESTINATION_STALE`, `DESTINATION_CONFLICT`

## PostgreSQL qualification

Fresh PostgreSQL 17 migrations and the Availability integration qualification verify:

- pre-053 Availability policies are not backfilled and fail closed without a typed budget association;
- missing Fencing Proof blocks Decision;
- source-only visibility returns `NO_DESTINATION`;
- exact historical Binding/Policy remains in use after explicit Rebind;
- two valid destination snapshots are immutable and replayable;
- Evaluation alone creates no Decision or Budget Claim;
- destination readiness drift makes old Evaluation stale;
- proof ABA makes old proofs stale;
- budget race/replay/one-per-Epoch constraints hold;
- Decision creates no Recovery Operation, new Placement Admission, compute/PCI/network/storage allocation, VM power mutation, Job, Command, or Lease.

Recorded result:

- all 25 PostgreSQL persistence integration tests: PASS with fresh-database isolation; the three HostGroup core tests that intentionally share authority fixtures were run together on one fresh database.
- `go test -race ./...`: PASS.
- `make check`: PASS.
- documentation contract lint: PASS, 469 requirements, 711 test contracts, 230 links.
- the dedicated PostgreSQL 17 fixture was removed after qualification.

## Remaining gate

Recovery Operation is intentionally not implemented. The next Availability gate is a first-class Recovery Operation/Plan that consumes the exact Eligibility Decision and Budget Claim, revalidates their current usability, performs destination Final Admission, and retains UNKNOWN/read-back semantics. Budget claim consumption/release must be tied to that future operation and verified terminal evidence; it must not be inferred from timeout or process loss.
