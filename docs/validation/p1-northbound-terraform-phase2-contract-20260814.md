# Northbound / Terraform Phase 2 Contract Qualification

- Date: 2026-08-14
- PostgreSQL: 17
- Scope: Phase 2 authority inventory and cross-process Create recovery
- Migration: none (001–076 unchanged)

## Outcome

Network, Subnet, Port and Volume are BLOCKED at their exact authority boundaries; no unsafe endpoint/provider resource was added. Project/Flavor/Availability/Image now use a client-owned stable Create identity that survives Provider/Terraform process loss before state persistence.

```text
Terraform configuration
  client_id + write-only client_reference
    -> deterministic Idempotency-Key
      -> kim-api authenticated principal/path scope
        -> immutable canonical desired digest binding
          -> original KIM logical resource ID/revision
```

The real Terraform acceptance removes the Project state mapping after the KIM transaction, starts another apply, recovers the exact original ID and observes one active current row plus one immutable idempotency decision. Reusing the same client reference with changed desired intent returns `IDEMPOTENCY_CONFLICT`; no display-name lookup is used.

## Safety assertions

- Terraform to PostgreSQL/Agent/OVN/OVS/libvirt/LVM/filesystem: none
- backend identity in state: none
- display name as recovery identity: no
- client reference as KIM resource authority: no
- different intent adopted by same reference: no
- Network/Subnet convenience CRUD over foundation tables: none
- Port Final Admission bypass: none
- Volume Local LVM binding exposed as universal desired model: none
- VM/Attachment Phase 3 behavior invented: none
- historical evidence rewritten: none

## Results

| Check | Result |
|---|---|
| Provider unit/schema contract | PASS |
| stable replay key across client recreation | PASS |
| different client/reference fencing | PASS |
| same reference/different desired digest | PASS (`IDEMPOTENCY_CONFLICT`) |
| real Terraform CLI 1.14.9 state-loss Project recovery | PASS (`9.68s`, real HTTP + PostgreSQL 17) |
| Project duplicate count | 0 |
| Phase 2 endpoint/provider resources | NOT IMPLEMENTED by authority decision |
| Network/Subnet/Port/Volume backend qualification | existing internal qualification unchanged |
| VM Phase 3 readiness | NO |

## Regression

| Suite | Result |
|---|---|
| fresh PostgreSQL 17 Migration 001–076 + replay | PASS |
| all persistence integration | PASS (`22.344s`; real-host opt-in tests skipped) |
| persistence integration `-race` | PASS (`31.475s`) |
| `go test ./...` / `go vet ./...` | PASS |
| `go test -race ./...` | PASS |
| Provider `go test ./...` / `go vet ./...` | PASS |
| Provider `go test -race ./...` | PASS |
| `make check` | PASS |
| documentation contracts | PASS (`521` requirements, `780` test contracts, `288` links) |
| `git diff --check` | PASS |

## Gates

`NORTHBOUND_CLIENT_CREATE_RECOVERY = PASS` and `TERRAFORM_PROCESS_CRASH_CREATE_RECOVERY = PASS` for Project/Phase 1. Every Phase 2 resource/operation/import/drift/acceptance gate remains BLOCKED. `NORTHBOUND_PHASE2_NO_PHYSICAL_LEAKAGE = PASS` because no incomplete public surface was introduced.

No production Host or workload was mutated. The 35-row completion metrics remain `90.0% / 85.7% / 50.0%`.
