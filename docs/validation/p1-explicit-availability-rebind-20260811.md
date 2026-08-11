# Explicit Availability Rebind Authority Validation

Date: 2026-08-11  
Migration: `049_explicit_availability_rebind_authority.sql`

## Certified authority path

```text
current VM Availability Binding rev N
→ immutable authorized Rebind Request
→ exact source / exact active target Policy revalidation
→ immutable ACCEPTED Rebind Decision
→ immutable Binding rev N+1
→ atomic current pointer switch
```

The implemented mode is direct exact-policy Rebind. It does not evaluate current HostGroup assignment. Policy, membership, or Group Policy Binding drift is not a Rebind trigger.

Revision-one evidence created by migration 048 is unchanged. Migration 049 removes the per-Admission/allocation uniqueness that prevented multiple historical Binding revisions, keeps the original provenance, and requires every revision above one to name its immediate source Binding and Rebind Decision. No historical Request or Decision is backfilled.

## Qualification

- Fresh PostgreSQL 17 applied all 49 migrations.
- Basic Rebind preserved rev1, committed one Decision and rev2, and switched only the current pointer.
- Exact request replay returned the same Request digest.
- Same Rebind identity with different semantics was rejected without changing original evidence.
- Commit-response-loss replay returned the same Decision digest and Binding digest; no rev3 was created.
- A stale rev2 request remained stale after another explicit Rebind created rev3; it was not reinterpreted as rev3→rev4.
- Two distinct concurrent intents against one rev1 source produced one accepted rev2 and one stale result.
- Rebind versus target Policy current switch produced either complete exact-old authority or stale rejection; no new revision was silently selected.
- Policy retirement/current drift did not rewrite historical Bindings or trigger Rebind.
- Audit traversal is `Binding rev N+1 → Decision → Request → source Binding rev N → exact target Policy`, including actor, approval reference, and reason.
- Compute, qualified VF, Network identity, Volume attachment, and VM power evidence counts were unchanged by Rebind.
- Rebind produced no Failure Epoch, Recovery Operation, restart, evacuation, network, storage, or power mutation.

Transient stale-source and stale-target races are transaction errors and do not create immutable `REJECTED` evidence. Terminal accepted authority is immutable and replayable. This avoids freezing a transient observation as a permanent rejection while preserving every actual responsibility transition.

## Regression

- PostgreSQL persistence integration: PASS
- Availability-aware Dry / Final Admission and historical response replay: PASS
- Availability Group Policy resolution and publication: PASS
- HostGroup / Placement / Upgrade / Maintenance regressions: PASS through full repository check
- Race detector: PASS
- `make check`: PASS
- Documentation lint: PASS

This increment does not implement Failure Epoch, failure detection, fencing/storage safety proof, Recovery Eligibility, Recovery Operation, Recovery Budget, or automatic VM mutation.
