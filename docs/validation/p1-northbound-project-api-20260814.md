# Northbound Project API Phase 0 Validation

- Date: 2026-08-14
- Database: PostgreSQL 17 (`postgres:17-alpine`)
- Schema: fresh and replayed Migrations 001–073
- Resource: Project
- Mutation completion: synchronous PostgreSQL authority commit (`201`/`200`/`204`)
- Backend mutation: none

## Outcome

Migration 073 and the `/api/v1/projects` reference vertical slice establish the common Northbound persistent-resource pattern. The product path is:

```text
HTTP client
→ versioned kim-api runtime
→ RS256 OIDC/JWKS authentication
→ system/Project RBAC authorization
→ bounded typed request validation
→ Project application service
→ PostgreSQL current + immutable revision authority
→ authorized public projection
→ immutable audit / stable Problem Details
```

Project has no Host/backend convergence. ADR-0029 therefore permits a synchronous authority commit and forbids a synthetic Operation. VM、Network、Volume and other backend-convergent resources still require `202 Accepted` plus a qualified Operation contract.

## Public contract

| Method | Path | Success | Concurrency/replay |
|---|---|---:|---|
| POST | `/api/v1/projects` | 201 | required `Idempotency-Key` |
| GET | `/api/v1/projects/{project_id}` | 200 | returns desired revision `ETag` |
| GET | `/api/v1/projects` | 200 | stable ID cursor, bounded `limit <= 100`, scope filtered |
| PATCH | `/api/v1/projects/{project_id}` | 200 | required `If-Match`; stale is 412 |
| DELETE | `/api/v1/projects/{project_id}` | 204 | required `If-Match`; dependency/protection conflict; same delete replay converges |

The OpenAPI 3.1 SSOT is `api/openapi/kim-v1.json`. `x-kim-resource` declares the Project identity/revision/synchronous mutation mode. `x-kim-field-class` marks required/optional desired and computed/immutable fields. Host、CPU/NUMA/PMD/RxQ、OVS/OVN identity、PCI BDF、LV UUID and materialization/recovery/evacuation generations are absent from desired schema.

PATCH is a closed typed partial update. Absent leaves a field unchanged, explicit `null` is rejected, boolean `false` remains a value, and immutable/unknown fields are rejected.

## Authority and security evidence

- Stable identity: server-generated UUIDv4; display name is mutable and not identity.
- Revision: immutable `project_revision_evidence` plus `projects_current`; ETag binds only to persistent desired revision.
- Idempotency: principal issuer/subject、SYSTEM parent scope、POST、canonical path、key and canonical desired digest are committed with Project creation.
- Same key/same digest returns the same Project; same key/different digest returns `IDEMPOTENCY_CONFLICT`.
- Authentication: external issuer RS256 token, exact issuer/audience/expiry/not-before, HUMAN or AUTOMATION principal; file-backed JWKS is reloaded for key rotation. Authentication cannot be disabled in production configuration.
- Authorization: closed SYSTEM/PROJECT scope and READER/WRITER/ADMIN roles. Project creator receives Project ADMIN. Authentication is not authorization.
- Audit: request ID、principal/type、action、resource/scope/revision、result、reason、timestamp and idempotency digest reference; Authorization headers/tokens are not persisted.
- Error surface: RFC 9457-compatible Problem Details with stable code、request ID and retryability; SQL/backend/panic detail is not returned.
- Delete: delete protection and dependent rows reject with stable conflict; no cascade. Tombstone preserves history and supports response-loss replay.

## PostgreSQL concurrency campaign

The real-listener integration used one disposable PostgreSQL 17 database and unique HUMAN/AUTOMATION subjects.

- 12 concurrent POST requests with one Idempotency-Key and one desired payload converged on one Project and one idempotency row.
- Reusing that key with another payload returned 409 `IDEMPOTENCY_CONFLICT`.
- Two concurrent PATCH requests with `If-Match: "1"` produced exactly one 200 and one 412.
- HUMAN and AUTOMATION principals created resources through the same HTTP path.
- Revoked system scope retained only the machine principal's creator Project membership; cross-scope read was denied.
- Project READER could read but not patch; WRITER could patch but not delete; ADMIN could delete.
- dependency conflict、delete protection、delete response-loss replay and tombstone read were verified.
- Project revision、idempotency and audit evidence rejected UPDATE.

## Gate matrix

| Gate | Result | Evidence |
|---|---|---|
| `NORTHBOUND_API_RUNTIME` | PASS | real binary/listener readiness and graceful SIGTERM |
| `NORTHBOUND_PROJECT_RESOURCE` | PASS | CRUD/list over real HTTP + PostgreSQL authority |
| `NORTHBOUND_AUTHENTICATION` | PASS | missing/malformed/expired/audience tests; HUMAN/AUTOMATION positive |
| `NORTHBOUND_AUTHORIZATION` | PASS | system/Project scope and READER/WRITER/ADMIN tests |
| `NORTHBOUND_RESOURCE_REVISION` | PASS | immutable revisions and concurrent stale fencing |
| `NORTHBOUND_ETAG_IF_MATCH` | PASS | quoted ETag, required update/delete precondition, 412 stale |
| `NORTHBOUND_PUBLIC_IDEMPOTENCY` | PASS | concurrent replay、response-loss retry、digest conflict |
| `NORTHBOUND_PROBLEM_DETAILS` | PASS | stable codes/correlation/no internal leak/panic recovery |
| `NORTHBOUND_OPENAPI_CONTRACT` | PASS | valid OpenAPI 3.1 JSON and executable route/schema/metadata check |
| `NORTHBOUND_PAGINATION` | PASS | deterministic UUID ordering、bounded cursor、scope isolation |
| `NORTHBOUND_AUDIT` | PASS | immutable success/denial evidence and secret exclusion |
| `TERRAFORM_PHASE0_RESOURCE_CONTRACT` | PASS | Project reference contract only |

`terraform-provider-kim` is not globally ready. Provider scaffold and an experimental Project resource are **Conditional**; production Project Provider release, all other resources, and unified backend Operation remain blocked on their own acceptance evidence.

## Validation commands

- fresh PostgreSQL 17 `TestMigratePostgreSQLIntegration`, including same-database replay: PASS
- all persistence integration on a fresh shared PostgreSQL database (`-parallel=1` to isolate existing global release-authority fixtures): PASS
- real HTTP `TestNorthboundProjectHTTPPostgreSQLIntegration`, including `-race`: PASS
- actual `kim-api` binary startup, `/readyz`, SIGTERM graceful exit: PASS
- targeted Northbound/auth/OpenAPI/persistence tests: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- `go vet ./...`: PASS
- `make check`: PASS
- documentation lint/link contract: PASS
- `git diff --check`: PASS

## Completion metrics

The established infrastructure/backend inventory denominator remains 35. Project API is a cross-cutting Northbound delivery surface and does not qualify any backend capability row.

```text
Architecture completion:   31.5 / 35 = 90.0%
Functional completion:     30.0 / 35 = 85.7%
Production qualification:  17.5 / 35 = 50.0%
```

Provider、UI、VM/Network/Volume/Flavor/Image API、unified Operation、Security Policy compiler、OVS-DPDK and FRR remain unimplemented or separately proposed.

## Safety assertions

```text
Northbound client direct PostgreSQL access = none
Northbound client Agent/backend credential  = none
Host/backend mutation in request path       = none
physical incarnation desired fields         = none
synthetic Project Operation                  = none
authenticated implies authorized            = no
stale revision overwrite                     = no
dependency cascade delete                    = no
raw token/Authorization audit                = none
internal SQL/error/panic detail disclosed    = none
historical evidence rewritten                = none
Terraform Provider declared globally READY  = no
```
