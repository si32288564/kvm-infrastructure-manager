# Failure Epoch / Failure Evidence Authority Validation

Date: 2026-08-11  
Migration: `050_failure_epoch_evidence_authority.sql`

## Certified authority path

```text
closed typed Failure Observation
→ explicit incident/open request
→ exact current VM Availability Binding revalidation
→ immutable SUSPECTED Failure Epoch
→ append-only observation and transition evidence
→ rebuildable current projection
```

Supported evidence types are `AGENT_CONNECTIVITY_LOSS`, `HOST_OPERATION_AUTHORITY_STATE`, and `VM_RUNTIME_OBSERVATION`. Sources are closed to `CONTROL_PLANE` and `LIBVIRT_READ_BACK` as appropriate. Evidence fixes source Host plus applicable session, credential, Host authority, and observation generations. States are `PRESENT`, `ABSENT`, `UNKNOWN`, and `CONFLICTING`; freshness is independently `CURRENT`, `STALE`, or `UNKNOWN`.

Migration 050 implements the canonical initial `SUSPECTED` transition only. It does not interpret the AvailabilityPolicy text confirmation slot and cannot issue `CONFIRMED`, fencing proof, Recovery Eligibility, Recovery Operation, restart, or evacuation.

## Qualification

- Fresh PostgreSQL 17 applied all 50 migrations.
- Epoch open fixed exact Binding revision/digest, exact Policy revision/digest, Admission, allocation, source Host, and available Host authority/session provenance.
- Request and Epoch response-loss replay returned the original Epoch without duplicate transition/evidence.
- Multiple observations appended immutable generations; `UNKNOWN` remained `UNKNOWN` evidence and the Epoch stayed `SUSPECTED`.
- Older `observed_at` evidence appended after newer evidence without rewriting the initial transition.
- Same evidence identity/digest replay converged; different semantics with the same identity conflicted and preserved original evidence.
- Parallel requests with one explicit incident key converged to one authoritative Epoch.
- An Epoch opened before Rebind retained its historical Binding; a later incident bound the new current Binding.
- Ten parallel Epoch-open/Rebind races produced complete old Binding provenance or stale open only. No mixed Binding/Policy provenance occurred.
- Compute, qualified VF, Network identity, Volume attachment, VM power observation, and Execution Job counts were unchanged.
- No fencing or Recovery authority tables/operations were created.

## Compatibility and regression

No pre-050 VM or Availability Binding is backfilled with a fabricated historical Epoch. Epoch authority begins only with a new explicit open request and typed observation.

- PostgreSQL persistence integration: PASS
- Availability Policy/Placement/Binding/Rebind regression: PASS
- HostGroup/Placement/Upgrade/Maintenance regression: PASS through full repository check
- Race detector: PASS
- `make check`: PASS
- Documentation lint: PASS

The next gate is typed Failure Confirmation plus Fencing/Storage Proof consumers. Confirmation remains separate from fencing, and neither alone will authorize recovery.
