# KIM Northbound / Terraform Phase 2 Resource Contract Review

- Review date: 2026-08-14
- Baseline: `88749e1a72ecbedf4b9af05c5852f96b9c6054a5`, Migration 001–076
- Candidates: Network, Subnet, Port, Volume
- Decision: **no Phase 2 candidate is safe to expose yet**

## Decision

The four candidates have valuable internal authority, but none has the complete persistent logical lifecycle required by the Northbound Resource Contract. This review deliberately does not add convenience CRUD over current tables. No `/networks`, `/subnets`, `/ports`, or `/volumes` endpoint and no corresponding Terraform resource is introduced.

| Resource | Stable identity/current authority | Realization/allocation | Missing Northbound authority | Classification |
|---|---|---|---|---|
| Network | `networks_current.network_id`, project, generation, lifecycle, MTU | OVN Logical Switch is produced only inside exact Port intent; segment and Host mapping are separate placement authorities | immutable logical revisions, independent producer, name/profile contract, segment allocation lifecycle, delete/tombstone/dependencies, standalone typed OVN observation/Operation, public ownership/RBAC projection | `API_SEMANTIC_GAP` / `BLOCKED` |
| Subnet | `network_subnets_current.subnet_id`, generation, CIDR and allocation range | PostgreSQL IPAM claims are allocated only by Final Admission; no standalone DHCP/router realization producer | Project ownership projection, immutable revisions, independent create/update/delete, gateway/DHCP/DNS semantics, overlap policy across lifecycle, backend verification/Operation | `API_SEMANTIC_GAP` / `BLOCKED` |
| Port | `network_ports_current.port_id`, generation; logical ID survives qualified EVACUATE | Final Admission atomically creates Port, MAC/IP claims and exact Host binding; OVN/OVS realization/read-back and A→B handoff are qualified | Admission-independent desired Port, attachment intent without VM aggregate, revision/delete/import authority, allocation lifecycle before placement; current row requires workload/admission and physical binding | `RESOURCE_MODEL_GAP` / `BLOCKED` |
| Volume | `volumes_current.volume_id`, desired generation; relocation history preserves identity semantics | Final Admission creates capacity claim, Local LVM binding and attachment; copy/content verification/cleanup are qualified | Admission-independent logical Volume lifecycle, backend-neutral storage-class request compiler, standalone allocation/materialization/delete/tombstone, attachment boundary before VM Phase 3, public revision/RBAC/read projection | `RESOURCE_MODEL_GAP` / `BLOCKED` |

## Authority inventory

### Network and Subnet

Migration 013 and `UpsertNetworkFoundation` establish Network, Subnet and Segment Claim in one internal transaction. The function is a catalog/placement producer and explicitly does not contact IPAM, OVS or OVN. It accepts caller-selected segment identity and cannot serve as public allocation authority. The typed OVN adapter's smallest current unit is a Port plan: it requires exact Port, Host, Chassis, segment, mapping and binding generations and creates/checks the Logical Switch as part of that aggregate. There is no standalone Network/Subnet work item or verified Operation terminal.

Consequently Network/Subnet create cannot truthfully claim either synchronous metadata completion (because the authority is incomplete) or asynchronous backend convergence (because no producer/consumer exists). Raw OVN UUID/DHCP syntax and caller-selected VNI/VLAN remain forbidden.

### Port

The strongest Port property is already correct: logical Port ID differs from Host binding incarnation. Recovery and EVACUATE retire the old binding and realize a new exact binding without changing the logical Port. However creation is inside Placement Final Admission and requires a workload, Admission, destination Host and segment mapping. A standalone Terraform Port would either fabricate those values or bypass the allocation transaction. VM is Phase 3, so attachment intent cannot yet be modeled honestly.

### Volume

Migration 014 ties Volume creation to Placement Admission, capacity observation/claim, a Local LVM backend/VG/Host binding and a workload attachment. Migrations 068–072 provide strong relocation, content identity and cleanup evidence, but they are consumers of an existing workload materialization, not a standalone backend-neutral Volume create lifecycle. Exposing the Local LVM binding as the universal desired model would leak Host/VG/LV/path and make A→B relocation drift. Attachment remains deferred to the VM aggregate.

## Required implementation sequence

1. Define Network immutable revision/current/tombstone and KIM-owned segment allocation; add typed standalone OVN Network realization/read-back and unified Operation binding.
2. Define independent Subnet revision, Project ownership, IPAM lifecycle, gateway/DHCP closed policy and typed backend verification.
3. Refactor Port into admission-independent logical desired/allocation authority, with Placement creating only physical binding incarnations; retain existing handoff history.
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
| `NORTHBOUND_NETWORK_RESOURCE` | BLOCKED |
| `NORTHBOUND_SUBNET_RESOURCE` | BLOCKED |
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
| `TERRAFORM_PHASE2_DRIFT_INVARIANTS` | BLOCKED (internal A→B invariants remain qualified) |
| `TERRAFORM_PHASE2_IMPORT` | BLOCKED |
| `TERRAFORM_PHASE2_ACCEPTANCE` | BLOCKED |

VM Phase 3 exit criteria are not met. Phase 1 resources remain experimentally ready; Network/Subnet/Port/Volume must not be represented as Terraform resources until the gaps above close.

The established 35-row infrastructure/backend denominator remains unchanged: Architecture `31.5/35 = 90.0%`, Functional `30/35 = 85.7%`, Production `17.5/35 = 50.0%`.
