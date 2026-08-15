# Phase 2 Northbound / Terraform Resource Contract Qualification

- Date: 2026-08-15
- Migration: 081 (`northbound_phase2_resource_contract`)
- Database: disposable PostgreSQL 17 (`postgres:17-alpine`)
- Terraform: 1.14.9 darwin_arm64, archive checksum verified
- Scope: logical Network, Subnet, unattached Port, backend-neutral Volume

## Authority chain

```text
Terraform desired + stable client_reference
→ authenticated Northbound Create
→ immutable idempotency binding
→ Migration 077/078/079/080 producer
→ typed realization/materialization Operation
→ worker Claim
→ typed apply/Command authority
→ response LOST where applicable
→ READ_BACK_FIRST
→ immutable verified terminal
→ public Operation SUCCEEDED
→ Terraform refresh/no-op/import
```

Volume Create selected an eligible current backend inside KIM. The caller supplied no Host, backend, VG, LV, capacity generation, binding, or materialization identity. The Local LVM acceptance used Command/Lease/Attempt, deliberately lost the mutation response, performed read-back, recorded exact verification, and only then completed materialization. Destroy used the corresponding absence verification before capacity/identity release.

## Results

| Gate | Result |
|---|---|
| `NORTHBOUND_NETWORK_RESOURCE` | PASS |
| `NORTHBOUND_SUBNET_RESOURCE` | PASS |
| `NORTHBOUND_PORT_RESOURCE` | PASS |
| `NORTHBOUND_VOLUME_RESOURCE` | PASS |
| `NORTHBOUND_PHASE2_RBAC_IDEMPOTENCY_AUDIT` | PASS |
| `NORTHBOUND_PHASE2_OPERATION_CONVERGENCE` | PASS |
| `NORTHBOUND_PHASE2_NO_PHYSICAL_LEAKAGE` | PASS |
| `TERRAFORM_NETWORK_RESOURCE` | PASS |
| `TERRAFORM_SUBNET_RESOURCE` | PASS |
| `TERRAFORM_PORT_RESOURCE` | PASS |
| `TERRAFORM_VOLUME_RESOURCE` | PASS |
| `TERRAFORM_PHASE2_IMPORT` | PASS |
| `TERRAFORM_PHASE2_ACCEPTANCE` | PASS |
| `TERRAFORM_PHASE2_DRIFT_INVARIANTS` | PASS |
| `VM_PHASE3_READINESS` | NO — VM aggregate contract remains separate |

## Acceptance coverage

- four-resource apply through real HTTP and PostgreSQL;
- exact Operation polling, including Volume response loss/read-back;
- second plan `No changes`;
- state removal and contract-prefixed import for every resource;
- post-import no-op plan;
- dependency-ordered asynchronous destroy;
- stale ETag and idempotency conflict rejection;
- immutable Migration 081 evidence UPDATE rejection;
- no physical identity keys in Terraform state;
- fresh Migration 001–081 apply and replay;
- all persistence integration, including Recovery, EVACUATE, Local LVM, OVN, PCI, and Cleanup.

## Qualification runs

All database-backed runs used independent fresh databases in the disposable PostgreSQL 17 container.

| Run | Result |
|---|---|
| Migration 001–081 apply and replay | PASS (2.155 s) |
| complete PostgreSQL persistence integration | PASS (33.112 s) |
| complete PostgreSQL persistence integration with `-race` | PASS (39.856 s) |
| Terraform 1.14.9 four-resource acceptance | PASS (14.015 s) |
| repository and provider `go test -race ./...` | PASS |
| `make check` | PASS |
| `git diff --check` | PASS |

`make check` included formatting, `go vet`, repository tests, provider tests, and documentation contract lint. The documentation contract result was 552 requirements, 812 test contracts, and 305 links.

## Safety assertions

```text
Provider direct DB/backend/Agent access       = none
caller-supplied physical incarnation          = none
command success treated as convergence        = no
UNKNOWN treated as terminal                   = no
existing reservation returned as free space   = no
Port attachment exposed before VM aggregate   = no
Recovery/EVACUATE history rewritten           = none
production workload/backend mutated           = none
```
