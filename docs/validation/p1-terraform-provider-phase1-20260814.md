# Terraform Provider Phase 1 Validation

- Date: 2026-08-14 JST
- Baseline: `d742155afa84e2cf357bde6ef2e8a94ce9077af`
- Schema: Migration 001–076; no new migration
- Provider: `terraform-provider-kim/`, pre-1.0 experimental
- Source address: `registry.terraform.io/kvm-infrastructure-manager/kim` (local filesystem mirror; Registry publication not performed)
- Terraform CLI: 1.14.9 darwin/arm64, HashiCorp archive SHA-256 `5bc0b11b7a63c8984a41d82523356df46f7833c2e9651a39a7f8919422de5cde`
- Terraform Plugin Framework: v1.19.0
- PostgreSQL: 17 (`postgres:17-alpine`, disposable)

## Delivered contract

The provider is a Northbound-only lifecycle client:

```text
Terraform CLI
→ terraform-provider-kim
→ /api/v1 KIM HTTP handler
→ PostgreSQL logical resource authority
```

It has no PostgreSQL driver and no Host Agent, Agent Gateway, libvirt, OVN/OVS, FRR, LVM, filesystem, or internal persistence endpoint. Authentication uses an externally issued Bearer automation token. Endpoint/token/CA can come from `KIM_ENDPOINT`, `KIM_TOKEN`, and `KIM_CA_CERTIFICATE`; token/CA provider attributes are sensitive and absent from resource state.

Implemented resources:

| Resource | Lifecycle |
|---|---|
| `kim_project` | CRUD, refresh, drift, import, protection/dependency diagnostics |
| `kim_flavor` | project-owned CRUD; desired update creates a new KIM revision with stable logical ID and no VM retrofit |
| `kim_availability_policy` | closed `MANUAL`/`WORKLOAD_MANAGED`; revision update; runtime Recovery fields absent |
| `kim_image` | metadata commit, separate ingestion Operation, verified terminal wait, content revision re-ingestion |

Network, Subnet, Port, Volume, VM, module authoring, UI, Security Policy, OVS-DPDK, FRR, and Registry publication remain out of scope.

## Authority semantics

- Read captures logical revision/ETag. Update/Delete send exact `If-Match`; `STALE_REVISION` is a diagnostic and is never silently retried.
- Create uses a fresh invocation-scoped Idempotency-Key; bounded response-loss retries inside the invocation retain the same key and JSON payload. A later intentional recreate receives a new key and does not replay a tombstone.
- Problem Details diagnostics preserve the stable KIM code, status, retryability, and request ID without token disclosure.
- Only `RESOURCE_NOT_FOUND` removes state. Forbidden, unauthenticated, unavailable, conflict, stale, and internal errors do not masquerade as deletion.
- Imports require `project|flavor|availability-policy|image/<uuid>` and reconstruct only authorized logical state.
- Image Create/Update completes after `GET /operations/{id}` returns verified `SUCCEEDED`. `UNKNOWN` remains non-terminal; `FAILED`/`CANCELLED` fail apply. Operation and attempt IDs are not state.
- Image content/source/format/architecture changes create the next immutable logical revision, reset the projection to `PENDING`, and require re-ingestion. Metadata-only revision preserves exact verified artifact identity. Unverified content is not published to existing consumers.

## Real acceptance topology and decisions

The campaign built the provider binary, installed it through a Terraform filesystem mirror, started a real KIM HTTP handler backed by PostgreSQL 17, and authenticated an `AUTOMATION` principal. Image ingestion was completed through immutable Command verification, artifact observation, content verification, and terminal evidence; Terraform observed only the public Operation.

| Qualification | Result |
|---|---|
| four-resource initial apply | PASS |
| second apply / plan no-op | PASS |
| Flavor vCPU new revision, stable ID | PASS |
| Availability max-attempts new revision, stable ID | PASS |
| Image content/source new revision, PENDING → Operation → VERIFIED, stable ID | PASS |
| unsupported Availability mode rejected by provider schema | PASS |
| remote Project desired change detected as drift | PASS |
| stale Flavor revision with `-refresh=false` rejected as `STALE_REVISION` | PASS |
| refresh then intended update succeeds | PASS |
| all four contract imports followed by no-op plan | PASS |
| destroy through KIM Delete/If-Match | PASS |
| response-loss retry retains same Create key | PASS (client fault test plus KIM idempotency integration) |
| `UNKNOWN` Operation remains non-terminal | PASS |
| no Host/backend/Attempt/Recovery/EVACUATE fields in schema or state JSON | PASS |
| Image cache/physical realization absent from drift surface | PASS by schema/state contract |

## Gates

| Gate | Result |
|---|---|
| `TERRAFORM_PROVIDER_RUNTIME` | PASS |
| `TERRAFORM_PROVIDER_AUTHENTICATION` | PASS |
| `TERRAFORM_PROVIDER_PROBLEM_DETAILS` | PASS |
| `TERRAFORM_PROVIDER_ETAG_CONCURRENCY` | PASS |
| `TERRAFORM_PROVIDER_IDEMPOTENCY` | PASS |
| `TERRAFORM_PROVIDER_IMPORT` | PASS |
| `TERRAFORM_PROVIDER_DRIFT` | PASS |
| `TERRAFORM_PROVIDER_OPERATION_POLLING` | PASS |
| `TERRAFORM_PROJECT_RESOURCE` | PASS — experimental |
| `TERRAFORM_FLAVOR_RESOURCE` | PASS — experimental |
| `TERRAFORM_AVAILABILITY_POLICY_RESOURCE` | PASS — experimental closed profiles |
| `TERRAFORM_IMAGE_RESOURCE` | PASS — experimental verified ingestion |
| `TERRAFORM_PHASE1_ACCEPTANCE` | PASS |
| `TERRAFORM_PHASE1_REAL_HTTP_POSTGRES` | PASS |
| `TERRAFORM_PHASE1_NO_PHYSICAL_STATE_LEAKAGE` | PASS |
| Network / Volume / VM Provider resources | NOT READY / NOT RUN |

## Completion impact

Project、Flavor、closed Availability Policy、Image advance from `TERRAFORM_READY_EXPERIMENTAL` contract candidates to `TERRAFORM_PROVIDER_ACCEPTANCE_PASS_EXPERIMENTAL`. This does not imply production-complete Provider support or raise any Network/Storage/VM backend gate.

The established 35-row infrastructure/backend denominator remains unchanged: Architecture `31.5/35 = 90.0%`、Functional `30/35 = 85.7%`、Production `17.5/35 = 50.0%`. Provider Phase 1 is a cross-cutting delivery surface, not a new backend capability row.

## Regression record

| Check | Result |
|---|---|
| fresh PostgreSQL 17 Migration 001–076 plus exact replay | PASS |
| all persistence integration on isolated fresh DB | PASS (`22.475s`) |
| real Terraform CLI/HTTP/PostgreSQL acceptance | PASS (`8.701s`) |
| same real acceptance under Go race detector on independent fresh DB | PASS (`10.630s`) |
| `go test -count=1 ./...` | PASS |
| `go test -race -count=1 ./...` | PASS |
| provider `go test ./...` | PASS |
| provider `go test -race ./...` | PASS |
| root/provider `go vet ./...` | PASS |
| `make check` including nested provider and documentation lint | PASS |
| documentation lint | PASS (`520` requirements, `779` test contracts, `284` links) |
| `git diff --check` | PASS |

One combined invocation that pointed every integration package at the same database was deliberately not accepted as evidence: unrelated fixture suites raced over global release/aggregate authority. Persistence and Terraform acceptance were rerun on independent fresh databases and passed, matching the repository's isolation convention. No production workload, Host backend, or external infrastructure resource was mutated.
