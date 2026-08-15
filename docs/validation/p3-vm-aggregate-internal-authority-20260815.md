# Phase 3 VM Aggregate Internal Authority Qualification

- Date: 2026-08-15
- Database: disposable PostgreSQL 17 (`postgres:17-alpine`)
- Migration: 082
- Scope: internal authority only; no Northbound API or Terraform VM resource

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_RESOURCE_AUTHORITY` | PASS |
| `VM_DEPENDENCY_SNAPSHOT_AUTHORITY` | PASS |
| `VM_PLACEMENT_ADMISSION_BINDING` | PASS |
| `VM_MATERIALIZATION_AGGREGATE_VERIFICATION` | PASS |
| `VM_POWER_DESIRED_OBSERVED_SEPARATION` | PASS |
| `NORTHBOUND_VM_RESOURCE` | BLOCKED |
| `TERRAFORM_VM_RESOURCE` | BLOCKED |
| `VM_RECOVERY_EVACUATE_NO_DESIRED_DRIFT` | NOT RUN |
| `VM_DELETE_QUIESCENCE_AND_ABSENCE` | NOT RUN |

## Qualified profile

```text
VM count              = 1
Port count            = 0
PCI requirement count = 0
boot Volume count     = 1
desired power         = RUNNING
observed power        = RUNNING / MATCHED
aggregate outcome     = VERIFIED
```

The boot Volume is a standalone backend-neutral Volume revision with exact capacity allocation, Local LVM backend binding and immutable materialization terminal. Volume materialization includes response loss, Lease succession and `READ_BACK_FIRST`; the aggregate consumes the resulting exact VERIFIED authority. This synthetic use does not add or change any production Local LVM qualification gate.

## Authority chain

```text
CreateVMAggregate
→ logical VM revision 1
→ exact dependency snapshot
→ runtime intent generation 1
→ lifecycle Operation PENDING
→ ClaimVMAggregateLifecycle
→ CompileVMAggregatePlacement
→ DryEvaluateAvailabilityPlacementScope
→ FinalAdmitAvailabilityPlacementScope
→ BindVMAggregateAdmission
→ PrepareVMAggregateMaterialization
→ generic PrepareVMMaterialization
→ typed DEFINE Command / Lease / Attempt / Result / Verification
→ VM definition MATCHED
→ typed Image materialization / bounded content MATCHED
→ zero-Port readiness derived READY
→ typed power-on Command / Lease / Attempt / Result / Verification
→ RUNNING observation MATCHED
→ EvaluateVMAggregateEvidence VERIFIED
→ CompleteVMAggregateLifecycle VERIFIED
→ logical VM ACTIVE / CONVERGED
```

## Exact authority boundaries

- logical VM revision is distinct from runtime intent generation;
- dependency snapshot binds exact Flavor revision/digest, Image revision/digest, Availability Policy revision/digest, Placement Scope generation/digest and root Volume revision/digest;
- caller supplies no Host, Admission, backend, VG, LV, Binding, READY or RUNNING authority;
- the compiler rechecks current exact logical and Volume materialization authority before producing the ordinary Placement request;
- Final Admission alone advances physical Volume attachment intent from immutable REQUESTED evidence to immutable ATTACHED evidence;
- aggregate Admission binding consumes the exact Availability binding and attachment lineage;
- generic materialization creates the physical runtime projection and exact plan;
- aggregate verification requires current exact READY plus immutable RUNNING read-back, not Command success;
- terminal-time joins fence plan/readiness/power evidence drift before current logical convergence is advanced.

## Negative and replay coverage

- stale root Volume revision is rejected before any VM authority is created;
- duplicate Create with the same request and authority identities returns the same dependency snapshot;
- zero Ports and zero PCI are derived by the compiler, not asserted as physical readiness by the caller;
- evaluation before RUNNING observation is rejected even after definition and image convergence;
- power mutation response loss converges through Lease expiry and read-back verification without a Result row;
- verification replay with the same identifier returns the same digest;
- readiness observation-generation drift between verification and terminal is rejected in a rollback branch;
- terminal replay with the same verification and terminal identifier is idempotent;
- dependency snapshot, runtime intent, verification and terminal evidence reject UPDATE;
- exact logical current and runtime binding retain the same VM UUID while physical fields remain in a separate rebuildable projection.

## PostgreSQL qualification

- fresh Migration 001–082 application: PASS;
- target `TestVMAggregateAuthorityPostgreSQLIntegration`: PASS;
- all persistence integration on PostgreSQL 17: PASS (`39.120s`);
- all persistence integration with race detector on an independent fresh PostgreSQL 17 database: PASS (`44.627s`);
- ordinary non-database package compilation/test before full regression: PASS.

Final standard/race and documentation checks are run as part of the delivery regression and are not evidence of Northbound/Terraform or real-Host qualification.

## Safety assertions

```text
caller-supplied Host authority        = none
caller-supplied Admission authority   = none
caller-supplied backend/LV authority  = none
caller-supplied READY authority       = none
caller-supplied RUNNING authority     = none
fake Recovery operation               = none
fake EVACUATE operation               = none
direct terminal row seed              = none
production workload mutation          = none
historical evidence rewritten         = none
```

## Remaining work

The initial producer accepts only desired `RUNNING`, one verified root Volume, zero Ports and no PCI. Metadata/power revision, delete/tombstone, one-Port compilation, multiple Volumes, Recovery/EVACUATE stable association, Northbound API and Terraform Provider require separate qualification before their gates can advance.
