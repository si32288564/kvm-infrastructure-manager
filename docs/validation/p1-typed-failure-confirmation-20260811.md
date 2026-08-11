# Typed Failure Confirmation Authority Validation

Date: 2026-08-11
Migration: `051_typed_failure_confirmation_authority.sql`

## Certified authority path

```text
closed typed Failure Confirmation Policy revision
→ exact AvailabilityPolicy revision association
→ exact historical Failure Epoch / VM Availability Binding
→ immutable Evidence snapshot
→ immutable pure Confirmation Evaluation
→ explicit Confirmation Decision
→ immutable SUSPECTED → CONFIRMED transition
→ rebuildable current Epoch projection
```

Phase 1 implements only `ALL_REQUIRED_EVIDENCE`. Each requirement fixes a closed evidence type, required positive observed state, `CURRENT` freshness, and closed source type. Optional source diversity uses the exact source identity digest; duplicate rows from one source are not independent confirmations. No generic expression language or AvailabilityPolicy text parser exists.

The Evaluation results are `SATISFIED`, `NOT_SATISFIED`, `UNKNOWN`, `CONFLICTING_INPUT`, `STALE_EVIDENCE`, `STALE_POLICY`, `STALE_EPOCH`, and `NO_CONFIRMATION_POLICY`. Evaluation is immutable and does not change the Epoch. Only a separate accepted Decision transaction may append `CONFIRMED`.

## PostgreSQL 17 qualification

- A fresh PostgreSQL 17 database applied migrations 001–051.
- A pre-051 AvailabilityPolicy/Epoch produced `NO_CONFIRMATION_POLICY`; no default typed Policy was backfilled.
- Policy revision 1 publication and response-loss replay converged to one immutable digest and requirement set.
- An exact AvailabilityPolicy revision association fixed Policy ID, revision, and digest.
- A two-source `ALL_REQUIRED_EVIDENCE` snapshot evaluated `SATISFIED`, while the Epoch remained `SUSPECTED` with one transition.
- Explicit Decision atomically inserted Decision evidence, appended transition generation 2, and switched current state to `CONFIRMED`.
- Evaluation and Decision replay recovered the original evidence without generation amplification.
- Required `UNKNOWN`, `STALE`, and contradictory `CURRENT PRESENT/ABSENT` inputs produced distinct fail-closed results and could not confirm the Epoch.
- Evidence appended after Evaluation made the old Decision stale; Evaluation evidence remained unchanged.
- Two different parallel Decisions produced one accepted `CONFIRMED` transition and one stale result.
- Confirmation racing an explicit Availability Rebind retained the Epoch's original Binding revision and exact AvailabilityPolicy responsibility.
- Confirmation racing a Policy revision switch produced either a complete revision 1 Decision or stale rejection; no revision 2 uplift occurred.
- Immutable joins reconstructed transition → Decision → Evaluation → exact Policy → exact Evidence set → Epoch → VM Availability Binding → AvailabilityPolicy without current tables.

The initial implementation exposed and fixed one replay defect during qualification: precomputed requirement digest fields were being included in the next digest calculation. Canonical publication now clears derived digest fields before hashing, and the fresh PostgreSQL test passes.

## Authority boundaries

```text
SATISFIED Evaluation != Confirmation Decision
CONFIRMED != FENCED
CONFIRMED != Recovery Eligible
CONFIRMED != Recovery Operation
```

Compute, qualified VF, Network identity, Volume attachment, VM power observation, and Execution Job counts were unchanged across confirmation. Host Operation Authority remained `ARMED` at the same generation. Migration 051 introduces no fencing proof, Recovery Eligibility, Recovery Operation, Job, Command, Lease, restart, evacuation, or failure-clearance authority.

## Compatibility and regression

Pre-051 AvailabilityPolicy revisions, VM Availability Bindings, Failure Epochs, and observations remain immutable historical evidence. They receive no fabricated typed confirmation association. A new explicit Policy revision and association are required before confirmation can be evaluated positively.

- Targeted PostgreSQL 17 integration with race detector: PASS
- Full PostgreSQL persistence integration: PASS
- Full repository race detector: PASS
- `make check`: PASS
- Documentation lint: PASS
- Documentation contract counts: 467 requirements, 707 test contracts, 228 traceability links

The next Availability gate is typed Fencing Proof and Storage Safety evidence. A confirmed failure remains blocked from Recovery until those independent authorities and Recovery Eligibility are implemented.
