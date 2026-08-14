# Northbound Phase 1 Authority Closure Validation

- Date: 2026-08-14
- Database: PostgreSQL 17
- Schema: fresh/replayed Migrations 001–075
- Implemented: SYSTEM Availability Policy closed profiles
- Remains blocked: Image Resource / Image Ingestion Operation

## Authority decisions

Availability Policy reuses Migration 048's immutable policy revisions/current projection, generic Group Policy catalog, exact VM Availability Binding, explicit Rebind, and Recovery consumers. Migration 075 adds only immutable public metadata, exact revision idempotency, protection/timestamps, and retirement replay. Public profiles are `MANUAL` and `WORKLOAD_MANAGED`, both derived to `NO_AUTOMATIC_ACTION`; callers cannot provide Failure/Fencing/Recovery evidence or backend identity.

PATCH appends a new immutable Policy revision. Existing workload bindings continue referencing their exact old revision and digest. Delete appends `RETIRED` only when unprotected and without active Group Policy or historical workload dependencies. It never falls back to another policy.

Image remains blocked. Migration 010's registration producer accepts declared and observed checksum/signature/source in one call. No typed ingestion actuator, response-loss read-back producer, or immutable observation-to-verification operation exists. Adding metadata CRUD would not close this authority gap, so no `/api/v1/images` route or Migration 075 Image table was added.

## Endpoints

```text
POST   /api/v1/availability-policies
GET    /api/v1/availability-policies
GET    /api/v1/availability-policies/{policy_id}
PATCH  /api/v1/availability-policies/{policy_id}
DELETE /api/v1/availability-policies/{policy_id}
```

All are SYSTEM-scoped and use HUMAN/AUTOMATION plus SYSTEM READER/WRITER/ADMIN, ETag/If-Match, Idempotency-Key, cursor pagination, Problem Details, request ID, bounded JSON, and immutable audit evidence.

## Gates

| Gate | Result |
|---|---|
| `IMAGE_RESOURCE_AUTHORITY` | BLOCKED — logical/observed evidence producer conflated |
| `IMAGE_CONTENT_IDENTITY` | BLOCKED for Northbound |
| `IMAGE_INGESTION_OPERATION` | BLOCKED — actuator/read-back absent |
| `IMAGE_DIGEST_VERIFICATION` | BLOCKED for caller-safe ingestion |
| `IMAGE_NO_RETROFIT` | PASS for existing exact revision consumers |
| `NORTHBOUND_IMAGE_RESOURCE` | BLOCKED — endpoint intentionally absent |
| `AVAILABILITY_POLICY_RESOURCE_AUTHORITY` | PASS — closed SYSTEM profiles |
| `AVAILABILITY_POLICY_REVISION` | PASS |
| `AVAILABILITY_POLICY_DEPENDENCY` | PASS |
| `AVAILABILITY_POLICY_OPERATION_SEPARATION` | PASS |
| `NORTHBOUND_AVAILABILITY_POLICY_RESOURCE` | PASS |
| `NORTHBOUND_PHASE1_LOGICAL_RESOURCE_COMPLETION` | BLOCKED — Image remains |
| `TERRAFORM_PHASE1_LOGICAL_RESOURCE_CONTRACT` | PASS for Project/Flavor/closed Availability only |

## Safety

```text
caller-supplied Image observed digest authority = none
Image metadata-only shortcut endpoint           = none
Availability runtime evidence desired fields    = none
Policy CRUD creates Failure Epoch/Recovery       = no
Policy revision retrofits existing workload     = no
referenced policy silent fallback                = no
physical incarnation desired fields              = none
Agent/Gateway/backend changes                    = none
historical evidence rewritten                    = none
```

## Terraform readiness

| Surface | Classification |
|---|---|
| Provider scaffold | Conditional |
| Project | `TERRAFORM_READY` experimental |
| Flavor | `TERRAFORM_READY` experimental |
| Availability Policy | `TERRAFORM_READY` experimental for SYSTEM MANUAL/WORKLOAD_MANAGED only |
| Image | `BLOCKED` |
| Provider Phase 1 overall | No — Image exit condition not met |

The 35-row infrastructure/backend inventory remains Architecture `90.0%`, Functional `85.7%`, Production `50.0%`.

## Validation

- fresh PostgreSQL 17 migrations 001–075 and replay: PASS
- Project/Flavor/Availability real HTTP integration: PASS
- all persistence integration: PASS
- auth/authz/concurrency/idempotency/OpenAPI: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- persistence integration `-race`: PASS
- `go vet ./...`: PASS
- `make check`: PASS
- documentation lint/link check: PASS
- `git diff --check`: PASS
