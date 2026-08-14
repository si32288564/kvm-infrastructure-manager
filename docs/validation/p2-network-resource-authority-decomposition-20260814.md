# Phase 2 Network Resource Authority Decomposition Qualification

- Date: 2026-08-14
- Baseline: `5e5e67f4cb7759973ebb5a2fd05c97157b496a07`
- Schema: Migration 001–077
- Database: fresh PostgreSQL 17 plus migration replay
- Backend scope: synthetic closed OVN adapter/runtime; no production OVN, Host, Port, or workload mutation

## Authority result

Migration 077 retains `networks_current` as the single current projection and separates three authorities:

1. `network_resource_revision_evidence`: stable logical Network ID, Project, name, closed profile, MTU, segment policy, delete protection, lifecycle, desired digest, and immutable revision;
2. segment pool/allocation decision/current/release evidence: KIM chooses AUTO VNI or validates closed EXPLICIT VLAN/VNI and owns collision/reuse decisions;
3. standalone realization Operation/Attempt/Observation/Terminal/current evidence: `kim-network-worker` executes the closed `kim.network-intent.ovn-network/v1` Logical Switch plan and PostgreSQL derives terminals only from typed read-back.

Logical Network ID, allocation ID/generation, segment value, realization generation, and observed OVN UUID remain distinct. A name/MTU update keeps the Network ID and allocation, advances the desired and realization generations, and accepts a replacement backend UUID only after matching read-back. This is desired drift for name/MTU, but backend replacement alone is not desired drift.

## Synthetic PostgreSQL 17 campaign

The campaign created one Project-owned `STANDARD_OVERLAY/AUTO` Network, replayed create to the same identity/allocation, rejected a same-segment explicit collision, and verified that PENDING realization is not consumer-ready. Claim generation 1 applied the standalone plan; its response was `LOST`, but exact Logical Switch marker and UUID read-back produced immutable `VERIFIED` terminal evidence.

Revision 2 changed name and MTU while retaining Network/allocation identity. A second exact realization recorded a different backend UUID and a new terminal without rewriting revision 1 or realization history. The legacy foundation adapter then added a Subnet projection while preserving `authority_source=NETWORK_RESOURCE` and the original allocation; this demonstrates there is no silent dual Network/segment authority.

Retirement first failed with the active Subnet dependency. After the fixture disabled that dependency, the allocation entered `RELEASE_PENDING`; reuse still failed. A `LOST` retirement response with the object still present did not release the segment. Successor claim generation 2 was `READ_BACK_FIRST`; exact absence produced `ABSENT`, immutable terminal/release evidence, logical `DELETED`, and only then removed the current allocation. A second Network acquired the same numeric segment. Replaying the old A claim/evidence was rejected and did not alter B. A protected Network rejected retirement. Two concurrent AUTO creates converged through serializable retries to different segment values.

Typed adapter tests additionally reject incomplete/open plan fields and foreign ownership, accept an owned Network revision update, preserve the deterministic Logical Switch identity, read back after an apply response loss, and retire only the exact owned Network. Existing Port plans use the same deterministic switch name and a subset of the standalone Network markers; they do not create a second switch or overwrite allocation/realization markers. Placement and Port-intent persistence queries require exact current `VERIFIED` realization for `NETWORK_RESOURCE` rows while retaining the explicit `LEGACY_FOUNDATION` compatibility branch.

## Gates

| Gate | Result |
|---|---|
| `NETWORK_RESOURCE_AUTHORITY` | PASS |
| `NETWORK_IMMUTABLE_REVISION` | PASS |
| `NETWORK_SEGMENT_ALLOCATION_AUTHORITY` | PASS |
| `NETWORK_SEGMENT_REPLAY` | PASS |
| `NETWORK_SEGMENT_ABA_FENCING` | PASS |
| `NETWORK_STANDALONE_OVN_REALIZATION` | PASS (synthetic) |
| `NETWORK_OVN_READ_BACK` | PASS (synthetic) |
| `NETWORK_RESPONSE_LOSS` | PASS |
| `NETWORK_DELETE_DEPENDENCY` | PASS |
| `NETWORK_BACKEND_RETIREMENT` | PASS |
| `NETWORK_SEGMENT_RELEASE_ORDERING` | PASS |
| `NETWORK_PLACEMENT_COMPATIBILITY` | PASS |
| `NETWORK_PORT_CONSUMER_COMPATIBILITY` | PASS |
| `NETWORK_NO_PHYSICAL_IDENTITY_LEAKAGE` | PASS |
| `NETWORK_TERRAFORM_DRIFT_INVARIANT` | PASS (internal model) |
| `NORTHBOUND_NETWORK_RESOURCE_READINESS` | CONTRACT_READY |
| `NORTHBOUND_NETWORK_RESOURCE` | BLOCKED: endpoint/RBAC/idempotency/audit not implemented |
| `TERRAFORM_NETWORK_RESOURCE` | BLOCKED: provider resource not implemented |
| `NORTHBOUND_SUBNET_RESOURCE` | BLOCKED: independent Subnet authority remains next |
| `NORTHBOUND_PORT_RESOURCE` | BLOCKED |
| `NORTHBOUND_VOLUME_RESOURCE` | BLOCKED |
| real OVN/production Network qualification | NOT RUN |

## Completion metrics

| Metric | Campaign value |
|---|---:|
| successful logical Network creates | 5 |
| exact create replays | 1 |
| concurrent AUTO allocations | 2 |
| collision rejects | 2 (allocated and release-pending) |
| desired updates | 1 |
| immutable desired revisions for primary Network | 3 |
| primary Network realization claims | 4 |
| primary Network read-back-first claims | 1 |
| response-lost observations | 2 |
| primary Network verified realization terminals | 2 |
| primary Network absence terminals | 1 |
| segment release evidence | 1 |
| post-release ABA reuse | 1 |
| stale completion rejects | 1 |
| dependency rejects | 1 |
| delete-protection rejects | 1 |
| backend UUID replacements | 1 |
| arbitrary OVN command/external ID/path inputs | 0 |
| public API endpoints added | 0 |
| Terraform resources added | 0 |

## Validation

- fresh PostgreSQL 17 Migration 001–077 and replay: PASS;
- Network authority persistence integration including concurrency, response loss, read-back, deletion, release, ABA, protection, and immutability: PASS;
- all persistence integration: PASS;
- typed OVN Network adapter and worker unit tests: PASS;
- existing Placement/Port and repository regression: PASS;
- `go test ./...`, `go test -race ./...`, persistence integration `-race`, `go vet ./...`, `make check`, documentation lint/link, and `git diff --check`: PASS.

The real/synthetic boundary is strict: no real OVN NBDB or production Host was changed, so Production qualification and the established 35-row scores remain unchanged. Network internal authority is contract-ready; public Northbound and Terraform delivery are separate future gates.

## Safety assertions

```text
caller-supplied OVN command / OVSDB transaction = none
caller-supplied external_ids                    = none
caller-supplied Host / Chassis / backend UUID  = none
arbitrary VNI outside closed pool policy       = rejected
command response treated as VERIFIED           = no
segment released before absence terminal       = no
old cleanup allowed to affect reused segment   = no
legacy producer allowed to overwrite authority = no
historical evidence rewritten                  = none
production resource mutation                   = none
```
