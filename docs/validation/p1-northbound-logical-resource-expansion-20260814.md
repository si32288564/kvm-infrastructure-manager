# Northbound Logical Resource Expansion Validation

- Date: 2026-08-14
- Database: PostgreSQL 17
- Schema: fresh/replayed Migrations 001–074
- Implemented resources: Project、Flavor
- Reviewed and blocked: Image、Availability Policy

## Authority inventory decision

| Candidate | Existing authority | Northbound decision |
|---|---|---|
| Flavor | Migration 010 immutable shape revision、current projection、Project owner、Placement/Admission exact-revision consumer | IMPLEMENTED |
| Image | immutable registration evidence requires declared+observed checksum、signature decision、source URI; materialization consumes verified revision | BLOCKED until artifact ingestion/read-back is separate from caller metadata CRUD |
| Availability Policy | immutable revision、SYSTEM group-policy catalog、typed confirmation/fencing/storage/budget bindings、PLACEMENT_POOL resolution and Recovery consumers | BLOCKED until public scope/name/typed child-policy/dependency contract exists |

Image was not exposed with caller-supplied `observedChecksum` or fake verification. Availability CRUD was not allowed to create Failure Epoch、Fencing Proof、Recovery Operation、Host selection or budget claims.

## Flavor lifecycle

```text
POST /api/v1/flavors
→ authenticate HUMAN/AUTOMATION
→ authorize ADMIN in SYSTEM or target Project
→ validate logical shape (no physical identity / no arbitrary extraSpecs)
→ immutable flavor_revision_evidence revision 1
→ flavors_current ACTIVE
→ exact Flavor idempotency evidence FK
→ immutable audit
→ 201 + ETag "1"
```

PATCH creates a new immutable Flavor revision under required If-Match. Existing Placement Admission and VM materialization evidence continue to reference their exact historical revision and are never retrofitted. Delete is synchronous only when unprotected and unreferenced; it creates a final historical revision and tombstone. Same delete retry converges through `deleted_from_revision`.

Public desired fields are Project ID、name、vCPU count、memory MiB、root disk GiB、logical NUMA policy/count、logical HugePage size、CPU allocation policy、pinning requirement and delete protection. Exact Host、pCPU IDs、Host NUMA node、HugePage allocation、PMD/RxQ、VF BDF and backend generation are absent.

## Common framework evidence

Project and Flavor share:

- `resource.Principal` HUMAN/AUTOMATION identity and common stable errors/UUID generation;
- OIDC/JWKS authentication middleware and SYSTEM/PROJECT READER/WRITER/ADMIN authorization function;
- request ID、timeout、bounded JSON、panic recovery;
- ETag formatter、If-Match parser、cursor parser、Problem Details mapping;
- immutable audit table and lifecycle metadata conventions.

They do not share a generic lifecycle handler. Project and Flavor retain separate services、persistence adapters、revision writers、dependency checks and delete semantics. Project idempotency retains its exact Project FK; Flavor uses a separate exact Flavor revision FK rather than weakening referential integrity with a polymorphic record.

## Qualification gates

| Gate | Result |
|---|---|
| `NORTHBOUND_FLAVOR_RESOURCE` | PASS |
| `NORTHBOUND_IMAGE_RESOURCE` | BLOCKED — artifact observation/ingestion boundary |
| `NORTHBOUND_AVAILABILITY_POLICY_RESOURCE` | BLOCKED — public scope/typed dependency model |
| `NORTHBOUND_COMMON_RESOURCE_CONTRACT` | PASS — Project+Flavor |
| `NORTHBOUND_MULTI_RESOURCE_AUTHORIZATION` | PASS |
| `NORTHBOUND_MULTI_RESOURCE_IDEMPOTENCY` | PASS |
| `NORTHBOUND_MULTI_RESOURCE_ETAG` | PASS |
| `NORTHBOUND_MULTI_RESOURCE_PAGINATION` | PASS |
| `NORTHBOUND_LIFECYCLE_METADATA` | PASS |
| `TERRAFORM_PHASE1_LOGICAL_RESOURCE_CONTRACT` | PASS for Project+Flavor only |

## Integration evidence

- concurrent same-key Flavor create converges to one stable ID/current row/idempotency row;
- same key with different canonical shape returns `IDEMPOTENCY_CONFLICT`;
- HUMAN and AUTOMATION paths use the real HTTP listener;
- Project READER reads but cannot write; WRITER updates but cannot delete; ADMIN performs destructive actions;
- cross-Project read/list is denied or filtered;
- competing If-Match update produces exactly one 200 and one 412;
- immutable `projectId` and physical/unknown fields are rejected;
- delete protection and response-loss delete replay are verified;
- immutable Flavor idempotency evidence rejects UPDATE;
- OpenAPI route/schema/lifecycle metadata and forbidden physical/runtime field checks pass.

Flavor dependency fencing checks immutable Placement Admission and VM materialization references. Existing placement/materialization regression tests preserve those exact historical Flavor foreign keys; no delete path mutates an existing VM.

## Validation

- fresh PostgreSQL 17 migrations 001–074 and replay: PASS
- Project + Flavor real HTTP PostgreSQL integration: PASS
- all persistence integration, serialized for existing global release fixtures: PASS
- auth/authz/concurrency/idempotency/OpenAPI: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- persistence integration `-race`: PASS
- `go vet ./...`: PASS
- `make check`: PASS
- documentation lint/link check: PASS
- `git diff --check`: PASS

## Terraform readiness

| Surface | Decision |
|---|---|
| Provider scaffold | Conditional / may start |
| Project experimental resource | `TERRAFORM_READY` for implementation; release acceptance pending |
| Flavor experimental resource | `TERRAFORM_READY` for implementation; release acceptance pending |
| Image | BLOCKED |
| Availability Policy | BLOCKED |
| Network/Subnet/Port/Volume/VM | unchanged; not made ready by this campaign |

The existing 35-row infrastructure/backend inventory remains `90.0%` Architecture、`85.7%` Functional、`50.0%` Production. Northbound expansion does not change that denominator.

## Safety assertions

```text
caller supplied Image observed checksum authority = none
caller supplied Availability runtime authority    = none
physical Flavor incarnation desired fields        = none
existing VM automatic Flavor retrofit              = no
silent dependent cascade                           = no
generic polymorphic FK weakening                   = no
Agent/backend credential exposure                  = none
Host/backend mutation                              = none
Terraform Provider implemented                     = no
historical evidence rewritten                      = none
```
