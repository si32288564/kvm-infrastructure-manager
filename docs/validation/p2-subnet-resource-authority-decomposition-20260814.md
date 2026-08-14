# Phase 2 Subnet Resource Authority Decomposition Qualification

- Date: 2026-08-14
- Baseline: `5f9aa336d7c7a1ad0d5e9b5ff1640c8bba4e07b8`
- Schema: fresh Migration 001–078 plus replay
- Database: PostgreSQL 17
- Backend scope: synthetic typed OVN adapter/runtime; no real OVN or production mutation

## Authority result

Migration 078 keeps `network_subnets_current` as the compatibility/current projection and adds immutable desired revisions, independent IPAM decision/current/release evidence, pool lifecycle evidence, and standalone realization Operation/Attempt/Observation/Terminal/current evidence. Stable Subnet ID, desired revision, pool and allocation generation, OVN DHCP UUID, parent Network revision, and realization generation remain distinct.

The closed logical profile is IPv4 `/16..30`, one bounded RANGE pool, `NONE|AUTO|EXPLICIT` gateway, boolean DHCP policy, up to eight IPv4 DNS service addresses, explicit reservations, and delete protection. CIDRs are canonicalized, gateway/reservations are excluded, and overlap is rejected in the same live Network. IPv6, multiple pools, raw OVN options, Router/LRP, NTP profile, and external IPAM are fail-closed future extensions.

## PostgreSQL 17 and synthetic OVN campaign

The campaign created and verified an independent parent Network, then created one non-canonical-input Subnet and observed canonical `10.88.0.0/24`, AUTO gateway `10.88.0.1`, exact range/reservations/DNS, revision 1, pool generation 1, and PENDING realization. Exact create replay returned the same operation; overlapping create and allocation before verification failed.

Typed claim generation 1 applied the deterministic DHCP plan. A `LOST` response still converged only after exact DHCP marker/options/UUID and parent Logical Switch read-back produced immutable `VERIFIED`. Revision 2 changed name/DNS, advanced pool and realization generations, and remained unusable until verified. A mismatched observation did not terminate; successor generation 2 was `READ_BACK_FIRST`, rejected apply before read-back evidence, and then converged from observation alone.

AUTO and EXPLICIT allocation decisions are serialized by Subnet scope, exclude gateway/reservations/protected claims, and bind exact Subnet/pool generations and Port owner. Replay returned the same address; explicit collision, stale revision and allocation on pending/retiring Subnet failed. Concurrent AUTO requests committed different addresses. Final Admission integration used an exact independently verified Network and Subnet, created immutable IPAM evidence atomically with Port/IP/MAC/binding, replayed admission, and reused the address only after the existing two-observation absence protocol produced immutable Subnet allocation release evidence. Delayed/post-terminal observations remained fenced.

A second verified Subnet entered `RETIRE_PENDING`; immutable pool-freeze evidence preceded the current freeze. Allocation failed while frozen. A `LOST` retirement response with DHCP still present did not retire the pool. The successor read back exact absence, emitted `ABSENT`, then and only then moved logical Subnet to `DELETED` and pool to `RETIRED`. Historical desired, allocation, observation and terminal tables reject UPDATE.

## Gates

| Gate | Result |
|---|---|
| `SUBNET_RESOURCE_AUTHORITY` | PASS |
| `SUBNET_IMMUTABLE_REVISION` | PASS |
| `SUBNET_NETWORK_DEPENDENCY` | PASS |
| `SUBNET_CIDR_AUTHORITY` | PASS (closed IPv4 profile) |
| `SUBNET_GATEWAY_AUTHORITY` | PASS |
| `SUBNET_IPAM_POOL_AUTHORITY` | PASS |
| `SUBNET_IP_ALLOCATION` | PASS |
| `SUBNET_IP_ALLOCATION_REPLAY` | PASS |
| `SUBNET_IP_ALLOCATION_ABA_FENCING` | PASS |
| `SUBNET_DHCP_LOGICAL_MODEL` | PASS |
| `SUBNET_STANDALONE_OVN_REALIZATION` | PASS (synthetic) |
| `SUBNET_OVN_READ_BACK` | PASS (synthetic) |
| `SUBNET_RESPONSE_LOSS` | PASS |
| `SUBNET_RETIREMENT_ALLOCATION_FREEZE` | PASS |
| `SUBNET_DELETE_DEPENDENCY` | PASS |
| `SUBNET_BACKEND_RETIREMENT` | PASS (synthetic) |
| `SUBNET_FINAL_ADMISSION_COMPATIBILITY` | PASS |
| `SUBNET_PORT_CONSUMER_COMPATIBILITY` | PASS |
| `SUBNET_NO_PHYSICAL_IDENTITY_LEAKAGE` | PASS |
| `SUBNET_TERRAFORM_DRIFT_INVARIANT` | PASS (internal contract) |
| `NORTHBOUND_SUBNET_RESOURCE_READINESS` | CONTRACT_READY |
| `NORTHBOUND_SUBNET_RESOURCE` | BLOCKED: endpoint/RBAC/idempotency/audit not implemented |
| `TERRAFORM_SUBNET_RESOURCE` | BLOCKED: provider resource not implemented |
| real OVN/production Subnet qualification | NOT RUN |

Phase 2 classification is `Network=CONTRACT_READY`, `Subnet=CONTRACT_READY`, `Port=BLOCKED (next target)`, `Volume=BLOCKED`. VM Phase 3 readiness remains `NO`. Existing Architecture `31.5/35`, Functional `30/35`, and Production `17.5/35` scores are unchanged because this is an internal synthetic qualification.

## Completion metrics

| Metric | Campaign value |
|---|---:|
| primary Subnet creates / exact replays | 1 / 1 |
| desired updates | 1 |
| primary immutable desired revisions | 2 |
| response-lost observations | 2 |
| read-back-first successors | 2 |
| verified terminals | 3 |
| absence terminals | 1 |
| standalone/concurrent allocation decisions | 3+ |
| Final Admission IPAM decisions | 2 |
| immutable allocation release evidence | 1 |
| overlap/collision/stale/freeze rejects | qualified |
| public endpoints / Terraform resources added | 0 / 0 |

## Safety assertions

```text
caller-supplied OVN transaction / external_ids = none
caller-supplied backend UUID / Host / Chassis   = none
caller-supplied availability decision           = none
command response treated as convergence         = no
allocation before exact verification            = no
allocation reuse before immutable release       = no
pool retired before backend absence              = no
legacy producer overwrote Subnet authority       = no
historical evidence rewritten                    = none
production resource mutation                     = none
```
