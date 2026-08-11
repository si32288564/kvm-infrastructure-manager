# P1-B06 Placement Scope Authority Validation

- Date: 2026-08-11
- Scope: first-class Placement Scope publication, dry visibility, and Final Admission fencing
- Status: PASS for the closed `VM_PLACEMENT` + `PLACEMENT_POOL` profile; P1-B06 remains In Progress

## Authority model

Migration 046 introduces immutable Placement Scope revision and exposed-HostGroup evidence plus a generation-bearing current projection. A Scope fixes exact HostGroup semantic generations and never copies Host IDs or owns capacity. The visible population is derived at dry-evaluation time from each exposed Group's current accepted Membership Set and current member evidence.

```text
Placement Request (placement_scope_id)
  -> current ACTIVE Placement Scope generation
  -> explicit PLACEMENT_POOL HostGroup generations
  -> current accepted Membership Set/member evidence
  -> canonical visible Host union
  -> existing Eligibility / Scoring
  -> transactional Final Admission
```

The implemented combination is closed to consumer `VM_PLACEMENT`, exposure mode `CANDIDATE`, and HostGroup type `PLACEMENT_POOL`. Failure Domain and Operational Cohort semantics are not inferred as candidate exposure. Hierarchy does not imply exposure, Selector results are consumed only through accepted Membership Sets, and Group Policy Binding remains an independent policy association authority.

`project_id` is the current compatibility domain identifier. No first-class Project/Tenant revision authority exists in this repository, so this increment checks exact identifier equality but does not claim Project generation fencing.

## Lifecycle and status semantics

- `ACTIVE`: new dry evaluation and Final Admission are allowed.
- `DRAINING`: immutable history remains; new dry/Final authority is blocked.
- `RETIRED`: immutable history remains; new dry/Final authority is blocked.

Dry evaluation is a read-only repeatable-read transaction. It returns distinct `NO_SCOPE`, `SCOPE_BLOCKED`, `NO_VISIBLE_HOST`, `VISIBLE_BUT_NO_ELIGIBLE_HOST`, and `READY` states. A Host can be visible and remain ineligible; eligibility creates no claim. Overlapping exposed Groups produce one Host candidate with every exact Group/Set/member provenance path retained.

## Final Admission

Final Admission serializes against the Scope and every provenance HostGroup, then revalidates in one PostgreSQL transaction:

- exact current Scope generation, lifecycle, consumer, Project identifier, and digest;
- every exposed HostGroup generation and current active state;
- every captured current accepted Membership Set generation and selected member evidence;
- current Host, Compute, PCI, Network, and Storage authority through the existing Final Admission path.

Only after all checks pass are immutable Placement decision, selected visibility provenance, and resource claims committed. Scope removal, widening, retirement, Membership Set switch/member removal, Host/resource drift, or conflicting admission rejects the stale dry result. Final Admission never re-runs selection against a newer Scope. Any failure rolls back the outer transaction, including nested Compute/PCI/Network/Storage claims.

Pre-migration Placement decisions retain NULL Scope provenance as immutable compatibility history. No synthetic Scope revision was backfilled. New Scope-aware decisions store exact Scope generation/digest and selected Group/Set/member provenance.

## Qualification results

Fresh PostgreSQL 17 qualification covered:

- G1 A/B visibility, G2 D exclusion, and visible-but-ineligible B;
- multi-Group Host deduplication with both provenance paths;
- `NO_SCOPE`, empty active Scope (`NO_VISIBLE_HOST`), Project mismatch, `DRAINING`, and `RETIRED`;
- Scope removal, widening, and Membership Set removal after dry: stale reject and no partial claims;
- two eligible dry decisions competing for full Host capacity: one admission, loser no claim;
- unsupported Failure Domain exposure: rejected;
- same request/digest replay: same Scope generation, no amplification;
- conflicting concurrent publishers: one complete winner and one stale/conflict loser;
- 20 Scope publish/dry races: complete old or new Scope only;
- 20 Membership Set publish/dry races: complete old or new member Set only;
- historical accepted decision remains bound to its original Scope generation after current Scope changes.

Commands:

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'TestPlacementScope(PostgreSQLIntegration|PublicationRacesPostgreSQLIntegration)' -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -race -count=1 ./internal/persistence/postgres
make check
```

The qualification container is temporary and must be removed after all suites complete.

## Remaining P1-B06 gates

- authenticated External Assertion verifier;
- Site/managed-Host population authority and population-complete `EXACTLY_ONE`;
- Availability/Placement policy consumers beyond the implemented Maintenance binding;
- failure-domain Maintenance scheduling and minimum-ready;
- first-class Project/Tenant generation authority;
- public topology/API/UI and Agent-side HostGroup logic remain outside this increment.
