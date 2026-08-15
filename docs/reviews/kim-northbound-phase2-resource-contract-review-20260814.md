# KIM Northbound / Terraform Phase 2 Resource Contract Review

- Review date: 2026-08-14
- Current implementation: Migrations 077–079; Port decomposition baseline `c9646a1dbb1ecb72cc27b5a74cb6ba1c948862ce`
- Candidates: Network, Subnet, Port, Volume
- Decision: **Network, Subnet, and Port internal contracts ready; no Phase 2 public resource exposed yet**

## Decision

The four candidates have valuable internal authority, but none has the complete persistent logical lifecycle required by the Northbound Resource Contract. This review deliberately does not add convenience CRUD over current tables. No `/networks`, `/subnets`, `/ports`, or `/volumes` endpoint and no corresponding Terraform resource is introduced.

| Resource | Stable identity/current authority | Realization/allocation | Missing Northbound authority | Classification |
|---|---|---|---|---|
| Network | Migration 077 immutable desired revisions/current projection with stable ID and Project ownership | KIM VNI/VLAN allocation/release plus standalone typed OVN Logical Switch Operation/read-back terminal | public RBAC/idempotency/audit/OpenAPI/list/import surface only | `INTERNAL_AUTHORITY_COMPLETE` / `CONTRACT_READY` |
| Subnet | Migration 078 stable Project-owned ID, immutable desired revision/current projection and closed IPv4 CIDR/gateway/DHCP/DNS | independent IPAM pool/allocation/release plus typed OVN DHCP realization/read-back/retirement; Final Admission consumes exact terminals | public RBAC/idempotency/audit/OpenAPI/list/import surface; IPv6/router are future resources | `INTERNAL_AUTHORITY_COMPLETE` / `CONTRACT_READY` |
| Port | Migration 079 immutable Project/Network-bound desired revisions/current projection; valid unattached state | KIM MAC allocation, Migration 078 IPAM consumption, attachment/binding incarnations, standalone typed OVN LSP realization/read-back/retirement; Final Admission and handoff consume exact identity | public RBAC/idempotency/audit/OpenAPI/list/import and Terraform surface only | `INTERNAL_AUTHORITY_COMPLETE` / `CONTRACT_READY` |
| Volume | `volumes_current.volume_id`, desired generation; relocation history preserves identity semantics | Final Admission creates capacity claim, Local LVM binding and attachment; copy/content verification/cleanup are qualified | Admission-independent logical Volume lifecycle, backend-neutral storage-class request compiler, standalone allocation/materialization/delete/tombstone, attachment boundary before VM Phase 3, public revision/RBAC/read projection | `RESOURCE_MODEL_GAP` / `BLOCKED` |

## Authority inventory

### Network and Subnet

Migration 077 decomposes Network from Migration 013's combined foundation. `network_resource_revision_evidence` and expanded `networks_current` own desired identity; allocation decision/current/release evidence own segments; Network realization Operation/Attempt/Observation/Terminal/current tables own the standalone OVN Logical Switch lifecycle. The normal `kim-network-worker` claims this work and invokes only the closed `kim.network-intent.ovn-network/v1` adapter. Exact marker read-back—not apply response—derives `VERIFIED`; exact owned-object absence derives `ABSENT`. Segment release follows only that absence terminal. Backend UUID replacement creates a new realization incarnation without changing desired identity.

`UpsertNetworkFoundation` remains a legacy adapter and cannot overwrite either new Network or Subnet authority. Migration 078 separates immutable Subnet desired revisions, pool/allocation/release evidence, and typed DHCP realization. Placement requires exact current Network and Subnet terminals and records the IPAM decision in the same Final Admission transaction. Raw OVN UUID/DHCP syntax, Host identity, arbitrary CIDR family, and caller availability remain forbidden. Router/LRP and per-Port DHCP attachment are explicitly deferred to their own resource consumers.

### Port

Migration 079 preserves the stable logical Port property and removes Admission as the only producer. Port desired, MAC decision, Subnet IP allocation, attachment intent, Host binding, and OVN realization now have distinct immutable identities and generations. Port creation is valid without workload, Admission, Host, or binding; Final Admission consumes an explicit attachment request and exact allocations. Recovery and EVACUATE continue to retire the old binding and realize a new exact incarnation without advancing `port_revision`. Backend absence is required before MAC/IP release. Public CRUD/import and Terraform remain deferred.

### Volume

Migration 014 ties Volume creation to Placement Admission, capacity observation/claim, a Local LVM backend/VG/Host binding and a workload attachment. Migrations 068–072 provide strong relocation, content identity and cleanup evidence, but they are consumers of an existing workload materialization, not a standalone backend-neutral Volume create lifecycle. Exposing the Local LVM binding as the universal desired model would leak Host/VG/LV/path and make A→B relocation drift. Attachment remains deferred to the VM aggregate.

## Required implementation sequence

1. Completed in Migration 077: Network immutable revision/current, KIM segment allocation/release, typed standalone OVN realization/read-back, dependency retirement, and consumer gating.
2. Completed in Migration 078: independent Subnet revision, Project/Network ownership, IPAM lifecycle, gateway/DHCP/DNS closed policy, typed backend verification and safe retirement.
3. Completed in Migration 079: admission-independent Port desired/allocation authority, explicit attachment intent, physical binding incarnations, typed standalone LSP convergence, release ordering, and existing handoff continuation.
4. Define backend-neutral Volume revision and storage-class request compiler, then standalone allocation/materialization Operation; keep Attachment with the VM aggregate.
5. Only after each producer/consumer/delete/RBAC/verification contract exists, add OpenAPI endpoints and Terraform resources.

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
| `NORTHBOUND_PORT_RESOURCE` | BLOCKED |
| `NORTHBOUND_VOLUME_RESOURCE` | BLOCKED |
| `NORTHBOUND_PHASE2_OPERATION_CONVERGENCE` | BLOCKED |
| `NORTHBOUND_PHASE2_NO_PHYSICAL_LEAKAGE` | PASS (no unsafe surface published) |
| `NORTHBOUND_CLIENT_CREATE_RECOVERY` | PASS for Phase 1 resources |
| `TERRAFORM_NETWORK_RESOURCE` | BLOCKED |
| `TERRAFORM_SUBNET_RESOURCE` | BLOCKED |
| `TERRAFORM_PORT_RESOURCE` | BLOCKED |
| `TERRAFORM_VOLUME_RESOURCE` | BLOCKED |
| `TERRAFORM_PROCESS_CRASH_CREATE_RECOVERY` | PASS for Project; Phase 2 application BLOCKED |
| `TERRAFORM_PHASE2_DRIFT_INVARIANTS` | Network/ Subnet internal contract PASS; Provider acceptance BLOCKED |
| `TERRAFORM_PHASE2_IMPORT` | BLOCKED |
| `TERRAFORM_PHASE2_ACCEPTANCE` | BLOCKED |

VM Phase 3 exit criteria are not met. Phase 1 resources remain experimentally ready; Network and Subnet are internal `CONTRACT_READY`, while Port and Volume remain authority-blocked. None is yet a Terraform resource.

The established 35-row infrastructure/backend denominator remains unchanged: Architecture `31.5/35 = 90.0%`, Functional `30/35 = 85.7%`, Production `17.5/35 = 50.0%`.
