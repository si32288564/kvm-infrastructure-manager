# KIM Northbound / Terraform Phase 2 Resource Contract Review

> Completion addendum (2026-08-15): Migration 081 and the public/Provider vertical slice now implement and qualify all four resources through real HTTP, PostgreSQL 17, and Terraform 1.14.9. The review below is retained as the pre-implementation decision record. See [Phase 2 Northbound / Terraform Resource Contract Qualification](../validation/p2-northbound-terraform-resource-contract-20260815.md) and [ADR-0035](../adr/0035-phase2-northbound-logical-resource-contract.md).

- Review date: 2026-08-14
- Current implementation: Migrations 077–080; Volume decomposition baseline `9ebed2940a6bb000e6e038646de7d0a6bb940ecb`
- Candidates: Network, Subnet, Port, Volume
- Decision: **Network, Subnet, Port, and Volume internal contracts ready; no Phase 2 public resource exposed yet**

## Decision

The four candidates now have complete internal persistent logical lifecycle contracts. This review deliberately does not equate internal readiness with a public surface or add convenience CRUD over current tables. No `/networks`, `/subnets`, `/ports`, or `/volumes` endpoint and no corresponding Terraform resource is introduced.

| Resource | Stable identity/current authority | Realization/allocation | Missing Northbound authority | Classification |
|---|---|---|---|---|
| Network | Migration 077 immutable desired revisions/current projection with stable ID and Project ownership | KIM VNI/VLAN allocation/release plus standalone typed OVN Logical Switch Operation/read-back terminal | public RBAC/idempotency/audit/OpenAPI/list/import surface only | `INTERNAL_AUTHORITY_COMPLETE` / `CONTRACT_READY` |
| Subnet | Migration 078 stable Project-owned ID, immutable desired revision/current projection and closed IPv4 CIDR/gateway/DHCP/DNS | independent IPAM pool/allocation/release plus typed OVN DHCP realization/read-back/retirement; Final Admission consumes exact terminals | public RBAC/idempotency/audit/OpenAPI/list/import surface; IPv6/router are future resources | `INTERNAL_AUTHORITY_COMPLETE` / `CONTRACT_READY` |
| Port | Migration 079 immutable Project/Network-bound desired revisions/current projection; valid unattached state | KIM MAC allocation, Migration 078 IPAM consumption, attachment/binding incarnations, standalone typed OVN LSP realization/read-back/retirement; Final Admission and handoff consume exact identity | public RBAC/idempotency/audit/OpenAPI/list/import and Terraform surface only | `INTERNAL_AUTHORITY_COMPLETE` / `CONTRACT_READY` |
| Volume | Migration 080 stable Project-owned ID, immutable desired revision/current projection; valid unattached state | independent capacity decision/claim, typed Local LVM materialization/read-back/retirement, exact Final Admission consumer; Migrations 068–072 copy/cleanup remain authoritative | public RBAC/idempotency/audit/OpenAPI/list/import/Operation projection and Terraform surface; Ceph/resize are future capabilities | `INTERNAL_AUTHORITY_COMPLETE` / `CONTRACT_READY` |

## Authority inventory

### Network and Subnet

Migration 077 decomposes Network from Migration 013's combined foundation. `network_resource_revision_evidence` and expanded `networks_current` own desired identity; allocation decision/current/release evidence own segments; Network realization Operation/Attempt/Observation/Terminal/current tables own the standalone OVN Logical Switch lifecycle. The normal `kim-network-worker` claims this work and invokes only the closed `kim.network-intent.ovn-network/v1` adapter. Exact marker read-back—not apply response—derives `VERIFIED`; exact owned-object absence derives `ABSENT`. Segment release follows only that absence terminal. Backend UUID replacement creates a new realization incarnation without changing desired identity.

`UpsertNetworkFoundation` remains a legacy adapter and cannot overwrite either new Network or Subnet authority. Migration 078 separates immutable Subnet desired revisions, pool/allocation/release evidence, and typed DHCP realization. Placement requires exact current Network and Subnet terminals and records the IPAM decision in the same Final Admission transaction. Raw OVN UUID/DHCP syntax, Host identity, arbitrary CIDR family, and caller availability remain forbidden. Router/LRP and per-Port DHCP attachment are explicitly deferred to their own resource consumers.

### Port

Migration 079 preserves the stable logical Port property and removes Admission as the only producer. Port desired, MAC decision, Subnet IP allocation, attachment intent, Host binding, and OVN realization now have distinct immutable identities and generations. Port creation is valid without workload, Admission, Host, or binding; Final Admission consumes an explicit attachment request and exact allocations. Recovery and EVACUATE continue to retire the old binding and realize a new exact incarnation without advancing `port_revision`. Backend absence is required before MAC/IP release. Public CRUD/import and Terraform remain deferred.

### Volume

Migration 080 adds an admission-independent backend-neutral desired revision, exact KIM capacity allocation, standalone typed Local LVM materialization/read-back/retirement, and explicit internal attachment intent. Final Admission may consume an existing Volume only when its exact revision, allocation generation, verified materialization/binding, and workload intent are current. Its existing reservation is deducted only from incremental demand and remains in the ledger. Migration 014 rows retain `LEGACY_ADMISSION`; Migrations 068–072 continue to own copy/content/transport/cleanup history. Host/VG/LV/path remain physical and never enter desired drift.

## Required implementation sequence

1. Completed in Migration 077: Network immutable revision/current, KIM segment allocation/release, typed standalone OVN realization/read-back, dependency retirement, and consumer gating.
2. Completed in Migration 078: independent Subnet revision, Project/Network ownership, IPAM lifecycle, gateway/DHCP/DNS closed policy, typed backend verification and safe retirement.
3. Completed in Migration 079: admission-independent Port desired/allocation authority, explicit attachment intent, physical binding incarnations, typed standalone LSP convergence, release ordering, and existing handoff continuation.
4. Completed in Migration 080: backend-neutral Volume revision, capacity allocation, standalone typed Local LVM materialization/retirement, exact Final Admission consumer, and attachment separation.
5. Next: implement one consistent public RBAC/idempotency/audit/OpenAPI/list/import/Operation surface for all four Phase 2 resources, then add Provider resources. Public Attachment remains with VM Phase 3.

## Create recovery contract

The Phase 1 gap is independently closed without Migration 077. Existing KIM idempotency evidence already binds authenticated principal, scope, method, canonical path and key to canonical desired digest and exact resource revision. The Provider now requires:

- stable provider `client_id` (or `KIM_CLIENT_ID`);
- per-resource write-only `client_reference`;
- deterministic key derived from resource type + client ID + client reference.

The key intentionally excludes display name and desired payload. KIM separately binds the payload digest, so exact replay recovers the original logical ID while the same reference with changed intent returns `IDEMPOTENCY_CONFLICT`. A different client/reference derives a different identity and cannot adopt the original. `client_reference` is not stored in Terraform state and is not KIM authority.

## Gate decision

| Gate | Result |
|---|---|
| `NETWORK_RESOURCE_AUTHORITY` | PASS |
| `NETWORK_IMMUTABLE_REVISION` | PASS |
| `NETWORK_SEGMENT_ALLOCATION_AUTHORITY` | PASS |
| `NETWORK_SEGMENT_REPLAY` | PASS |
| `NETWORK_SEGMENT_ABA_FENCING` | PASS |
| `NETWORK_STANDALONE_OVN_REALIZATION` | PASS (synthetic typed adapter) |
| `NETWORK_OVN_READ_BACK` | PASS (synthetic typed adapter) |
| `NETWORK_RESPONSE_LOSS` | PASS |
| `NETWORK_DELETE_DEPENDENCY` | PASS |
| `NETWORK_BACKEND_RETIREMENT` | PASS |
| `NETWORK_SEGMENT_RELEASE_ORDERING` | PASS |
| `NETWORK_PLACEMENT_COMPATIBILITY` | PASS |
| `NETWORK_PORT_CONSUMER_COMPATIBILITY` | PASS |
| `NETWORK_NO_PHYSICAL_IDENTITY_LEAKAGE` | PASS |
| `NETWORK_TERRAFORM_DRIFT_INVARIANT` | PASS (internal contract; no Provider resource) |
| `NORTHBOUND_NETWORK_RESOURCE_READINESS` | CONTRACT_READY |
| `NORTHBOUND_NETWORK_RESOURCE` | BLOCKED (endpoint not implemented) |
| `NORTHBOUND_SUBNET_RESOURCE_READINESS` | CONTRACT_READY |
| `NORTHBOUND_SUBNET_RESOURCE` | BLOCKED (endpoint not implemented) |
| `NORTHBOUND_PORT_RESOURCE_READINESS` | CONTRACT_READY |
| `NORTHBOUND_PORT_RESOURCE` | BLOCKED (endpoint not implemented) |
| `NORTHBOUND_VOLUME_RESOURCE_READINESS` | CONTRACT_READY |
| `NORTHBOUND_VOLUME_RESOURCE` | BLOCKED |
| `NORTHBOUND_PHASE2_OPERATION_CONVERGENCE` | BLOCKED |
| `NORTHBOUND_PHASE2_NO_PHYSICAL_LEAKAGE` | PASS (no unsafe surface published) |
| `NORTHBOUND_CLIENT_CREATE_RECOVERY` | PASS for Phase 1 resources |
| `TERRAFORM_NETWORK_RESOURCE` | BLOCKED |
| `TERRAFORM_SUBNET_RESOURCE` | BLOCKED |
| `TERRAFORM_PORT_RESOURCE` | BLOCKED |
| `TERRAFORM_VOLUME_RESOURCE` | BLOCKED |
| `TERRAFORM_PROCESS_CRASH_CREATE_RECOVERY` | PASS for Project; Phase 2 application BLOCKED |
| `TERRAFORM_PHASE2_DRIFT_INVARIANTS` | Network/Subnet/Port/Volume internal contract PASS; Provider acceptance BLOCKED |
| `TERRAFORM_PHASE2_IMPORT` | BLOCKED |
| `TERRAFORM_PHASE2_ACCEPTANCE` | BLOCKED |

VM Phase 3 exit criteria are not met. Phase 1 resources remain experimentally ready; Network, Subnet, Port, and Volume are internal `CONTRACT_READY`, but none is yet a public API or Terraform resource.

The established 35-row infrastructure/backend denominator remains unchanged: Architecture `31.5/35 = 90.0%`, Functional `30/35 = 85.7%`, Production `17.5/35 = 50.0%`.
