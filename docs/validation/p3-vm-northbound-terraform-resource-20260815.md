# Phase 3 VM Northbound / Terraform Resource Qualification — 2026-08-15

- Date: 2026-08-15
- Database: PostgreSQL 17 (`postgres:17-alpine`, disposable container)
- Terraform: 1.14.9 darwin_arm64
- Migration: 088
- Scope: bounded logical VM public contract; no production Host mutation

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_RESOURCE_AUTHORITY` | PASS (Migration 082 regression) |
| `VM_LOGICAL_UPDATE_DELETE_AUTHORITY` | PASS (Migration 087 regression) |
| `NORTHBOUND_VM_RESOURCE` | PASS for bounded profile |
| `NORTHBOUND_VM_CREATE_REPLAY` | PASS |
| `NORTHBOUND_VM_ETAG_STALE_FENCING` | PASS |
| `TERRAFORM_VM_RESOURCE` | PASS for bounded profile |
| `TERRAFORM_VM_IMPORT` | PASS |
| `TERRAFORM_VM_POWER_OPERATION` | PASS |
| `TERRAFORM_VM_PHYSICAL_INCARNATION_NO_DRIFT` | PASS by schema/state exclusion |
| `VM_DELETE_ZERO_PORT_ONE_ROOT` | PASS (Migration 087 regression) |
| `VM_DELETE_ONE_STANDARD_PORT_ONE_ROOT` | PASS (Migration 089 qualification) |
| `VM_DELETE_ROOT_PLUS_DATA` | PASS (Migration 090 qualification) |
| `VM_DELETE_ONE_STANDARD_PORT_ROOT_PLUS_DATA` | PASS (Migration 089/090 composite qualification) |
| `VM_DELETE_STANDARD_PORT_PROFILE` | NOT RUN / API rejects |
| `VM_DELETE_MULTI_VOLUME_PROFILE` | NOT RUN / API rejects |
| `VM_MULTI_PORT_MOBILITY` | NOT RUN |
| `VM_MULTI_VOLUME_MOBILITY` | NOT RUN |
| production VM qualification | BLOCKED |

## Public profile

Create accepts exact immutable dependency revisions for Flavor, verified Image, Availability Policy, Placement Scope, one ROOT Volume, zero through two STANDARD Ports, and at most one DATA Volume. Initial desired power is `RUNNING`; PCI is not exposed. Port and DATA sets are canonicalized.

Metadata/delete protection is a synchronous logical revision. Desired `RUNNING`/`SHUTOFF` is a separate Operation and cannot be mixed with metadata in one PATCH. Dependency changes are Terraform replacement boundaries. Public delete remains narrower than create: Migrations 087/089 require at most one STANDARD Port, one ROOT Volume, no PCI, delete protection disabled, and observed `SHUTOFF`.

## Evidence exercised

Fresh Migration 001–088 and replay passed. PostgreSQL integration exercised authenticated RBAC, Create binding, same-digest replay, different-digest conflict, Get/List, metadata revision with unchanged runtime generation, stale revision rejection, RUNNING delete rejection, and immutable Northbound replay evidence. The create fixture used a separate ROOT Volume produced through ordinary allocation, typed Local LVM mutation, LOST response, READ_BACK_FIRST, observation, and VERIFIED materialization before VM dependency snapshot.

Terraform 1.14.9 acceptance executed the compiled provider over HTTP for apply, Operation polling, second-plan no-op, desired power update, refresh, state inspection, `vm/<uuid>` import, post-import no-op, and destroy. The state was checked for absence of Host, Admission, binding, LV, materialization, Recovery, and EVACUATE identities. This Provider lifecycle acceptance uses a deterministic HTTP authority fixture; the PostgreSQL authority adapter is qualified separately above rather than claiming a production combined campaign.

## Safety assertions

```text
virtual_machines_current serialized as public desired = no
caller-supplied Host/Admission/materialization authority = none
caller-supplied Port binding/backend/LV authority = none
command success treated as convergence = no
metadata revision changes runtime generation = no
mobility physical incarnation becomes Terraform drift = no
stale If-Match silently retried = no
delete protection bypassed by Provider = no
Port/DATA delete opened without qualification = no
historical evidence rewritten = none
production workload mutated = none
```

## Commands

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestMigratePostgreSQLIntegration -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestNorthboundVMContractPostgreSQLIntegration -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -timeout 900s ./internal/persistence/postgres
KIM_TERRAFORM_CLI=/private/tmp/terraform-1.14.9/terraform go test -count=1 -run TestTerraformVMApplyNoopPowerImportDestroy -v ./terraform-provider-kim/internal/provider
go test ./...
```

Production scores and real two-Host gates are unchanged. Migration 088 adds only public Create replay binding and does not grant physical lifecycle authority.
