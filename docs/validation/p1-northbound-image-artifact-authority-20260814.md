# Northbound Image Artifact Authority Qualification — 2026-08-14

## Scope and profile

- Baseline: `ef8739b6541ce5d1f193333ecb1eb0b3fe7863e9`, Migration 001–075
- Delivered: Migration 076, Image logical resource, artifact ingestion Operation, typed Agent backend, immutable read-back verification, HTTP/OpenAPI contract
- Database: PostgreSQL 17 fresh Migration 001–076 plus exact replay
- Source profile: administrator-owned `approved.fixture`; public request contains no URL/path/credential
- Artifact profile: RAW/X86_64, non-zero guest-like mutable markers, whole-artifact SHA-256

## Authority chain

```text
POST Image (expected digest only)
→ immutable logical revision / PENDING
→ POST ingestion / 202 Operation
→ DB selects an ARMED Host with image-ingest capability
→ IMAGE_ARTIFACT_INGEST typed Command
→ bounded staging write + fsync + whole SHA-256 + atomic digest publish
→ Result received or LOST
→ Command Verification read-back
→ immutable artifact observation (derived from verification ID)
→ expected == independently observed
→ immutable Image verification/terminal
→ Operation SUCCEEDED
→ exact Migration 010 verified revision publication
→ existing Placement/materialization consumers enabled
```

The qualification response-loss branch records no Command Result (`response_state=LOST`) but consumes a MATCHED read-back verification and converges to Image/Operation VERIFIED/SUCCEEDED. `verifiedDigest` is computed and cannot be submitted through the public schema.

## Gates

| Gate | Result |
|---|---|
| `IMAGE_RESOURCE_AUTHORITY` | PASS |
| `IMAGE_ARTIFACT_IDENTITY` | PASS |
| `IMAGE_EXPECTED_OBSERVED_DIGEST_SEPARATION` | PASS |
| `IMAGE_INGESTION_OPERATION` | PASS |
| `IMAGE_INGESTION_TYPED_EXECUTION` | PASS |
| `IMAGE_INGESTION_RESPONSE_LOSS` | PASS |
| `IMAGE_ARTIFACT_READ_BACK` | PASS |
| `IMAGE_DIGEST_VERIFICATION` | PASS |
| `IMAGE_CACHE_ABA_FENCING` | PASS (digest-addressed atomic final + whole read-back) |
| `IMAGE_MATERIALIZATION_VERIFIED_ONLY` | PASS |
| `IMAGE_NO_EXISTING_VM_RETROFIT` | PASS |
| `NORTHBOUND_IMAGE_RESOURCE` | PASS |
| `NORTHBOUND_OPERATION_RESOURCE` | PASS |
| `TERRAFORM_IMAGE_RESOURCE_CONTRACT` | PASS |
| `NORTHBOUND_PHASE1_LOGICAL_RESOURCE_COMPLETION` | PASS |

## Negative and safety evidence

- digest mismatch and partial content: typed backend returns `CONFLICTING`; nothing is published current
- unknown/arbitrary `source_path`: strict typed payload decoding rejects it
- same operation/idempotency identity: replay; different desired create payload: conflict
- immutable artifact observation UPDATE: rejected by trigger
- Image delete while Admission/materialization references exist: dependency conflict
- cache path, Host ID, backend identity, Agent address, credentials: absent from Image desired/OpenAPI
- `RegisterImageRevision` caller-observed producer: internal compatibility only and unreachable from Northbound API

## Public endpoints

- `POST/GET /api/v1/images`
- `GET/PATCH/DELETE /api/v1/images/{image_id}`
- `POST /api/v1/images/{image_id}/ingestions`
- `GET /api/v1/operations/{operation_id}`

Operation cancellation is deliberately `cancellable=false`. UNKNOWN is non-terminal and uses same-authority read-back first.

## Terraform and inventory decision

Project, Flavor, closed SYSTEM Availability Policy, and Image are `TERRAFORM_READY_EXPERIMENTAL`. Provider Phase 1 implementation start: **Yes, limited to those four resources**. Network, Volume, and VM are not implied ready.

The existing 35-row infrastructure/backend inventory denominator is unchanged. Architecture `31.5/35 = 90.0%`, Functional `30/35 = 85.7%`, Production `17.5/35 = 50.0%`. Image Phase 1 is a cross-cutting Northbound delivery gate, not a new backend row.

## Validation

| Check | Result |
|---|---|
| fresh PostgreSQL 17 Migration 001–076 | PASS |
| migration exact replay | PASS |
| Image PostgreSQL + real HTTP integration | PASS |
| typed process/backend whole-content test | PASS |
| response-loss/read-back | PASS |
| OpenAPI 3.1 JSON and lifecycle metadata | PASS |
| full persistence integration | PASS |
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |
| persistence integration `-race` | PASS |
| `go vet ./...` | PASS |
| `make check` | PASS |
| documentation lint/link | PASS |
| `git diff --check` | PASS |

Raw artifact content, source paths, credentials, and cache incarnation identities are not recorded in this report or public evidence.
