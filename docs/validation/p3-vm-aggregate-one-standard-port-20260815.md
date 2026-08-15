# Phase 3 VM Aggregate One STANDARD Port Qualification

- Date: 2026-08-15
- Database: disposable PostgreSQL 17 (`postgres:17-alpine`)
- Migration: 083
- Scope: internal aggregate producer; no Northbound API or Terraform VM resource

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_STANDARD_PORT_PROFILE` | PASS |
| `VM_LOGICAL_PORT_DEPENDENCY_SNAPSHOT` | PASS |
| `VM_PORT_FINAL_ADMISSION_BINDING` | PASS |
| `VM_OVS_PREBOOT_EVIDENCE_BINDING` | PASS |
| `VM_AGGREGATE_NETWORK_TERMINAL_DRIFT_FENCING` | PASS |
| `VM_AGGREGATE_ZERO_PORT_PROFILE` | PASS (regression) |
| `VM_AGGREGATE_MULTI_PORT_PROFILE` | NOT RUN |
| `VM_AGGREGATE_DATA_VOLUME_PROFILE` | NOT RUN |
| `VM_RECOVERY_EVACUATE_NO_DESIRED_DRIFT` | NOT RUN |
| `NORTHBOUND_VM_RESOURCE` | BLOCKED |
| `TERRAFORM_VM_RESOURCE` | BLOCKED |

## Qualified profile and authority chain

```text
VM count              = 1
STANDARD Port count   = 1
PCI requirement count = 0
boot Volume count     = 1

standalone Network/Subnet/Port revisions VERIFIED
→ CreateVMAggregate
→ exact logical Port revision/digest + REQUESTED attachment snapshot
→ CompileVMAggregatePlacement
→ Network/Subnet/segment/IP/MAC requirement derived by KIM
→ Availability-aware Dry Evaluation
→ Final Admission
→ exact Port Host binding + BOUND attachment evidence
→ attached OVN Port realization VERIFIED
→ generic VM materialization
→ typed definition and image observations MATCHED
→ PrepareOVSPortRealization
→ Command / Lease / Attempt / Verification
→ exact OVS preboot observation REALIZED
→ readiness READY
→ typed power-on response LOST
→ RUNNING read-back MATCHED
→ aggregate verification VERIFIED
→ aggregate terminal VERIFIED
→ logical VM ACTIVE / CONVERGED
```

The dependency snapshot contains no Host, binding generation, OVN chassis, backend UUID or OVS interface incarnation. Final Admission and materialization evidence own those physical identities. A later mobility consumer may replace them without revising the logical VM or logical Port solely because physical realization changed.

## Negative, replay and drift coverage

- stale Port revision is rejected before VM authority is created;
- missing OVS realization is rejected even after definition and image evidence exists;
- Command success is not treated as network readiness;
- readiness digest is recomputed from the exact immutable OVS realization evidence;
- Port binding generation drift between aggregate verification and terminal is rejected in a rollback branch;
- immutable aggregate Port binding and network verification evidence reject UPDATE;
- the existing zero-Port aggregate profile and its DB-derived empty evidence-set digest remain qualified.

## Safety assertions

```text
caller-supplied Host/binding authority       = none
caller-supplied OVN/OVS physical identity    = none
caller-supplied network READY authority      = none
caller-supplied RUNNING authority            = none
logical Port replaced during materialization = no
fake Recovery/EVACUATE authority             = none
production workload mutation                 = none
historical evidence rewritten                = none
```

## Regression evidence

- fresh PostgreSQL 17 Migration 001–083 and migration replay: PASS;
- targeted zero-Port and one STANDARD Port aggregate E2E: PASS;
- all persistence integration on PostgreSQL 17: PASS (`36.187s`);
- all persistence race integration on an independent fresh PostgreSQL 17 database: PASS (`42.324s`);
- `go test ./...`: PASS;
- `go test -race ./...` with database campaigns isolated from package-parallel global authorities: PASS;
- `make check`, Go vet, Provider tests, documentation lint and `git diff --check`: PASS.

Database-backed race and ordinary package race were deliberately separated. Passing the same database URL to all concurrently built qualification packages creates unrelated shared global component-release authority collisions and is not a valid isolation topology.

## Remaining work

Multi-Port ordering/cardinality, data Volume aggregation, desired power/update/delete contracts, Recovery and EVACUATE logical no-drift association, Northbound/OpenAPI and Terraform `kim_vm` remain separate qualification gates. Production OVN/OVS behavior is not promoted by this synthetic PostgreSQL campaign.
