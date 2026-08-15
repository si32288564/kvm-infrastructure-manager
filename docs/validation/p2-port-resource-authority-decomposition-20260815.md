# Phase 2 Port Resource Authority Decomposition Validation — 2026-08-15

## Scope and environment

- Baseline: `c9646a1dbb1ecb72cc27b5a74cb6ba1c948862ce`
- Schema: fresh Migration 001–079 plus migration replay
- Database: PostgreSQL 17 disposable integration instance
- Profile: STANDARD OVN, IPv4 Subnet IPAM, zero PCI, synthetic backend observations
- Public `/api/v1/ports` and Terraform `kim_port`: not implemented or exercised

## Authority result

Migration 079 retains `network_ports_current` as the compatibility/current projection and adds immutable desired revision, KIM MAC allocation, logical attachment intent, standalone typed OVN realization, and backend-absence-fenced identity release evidence. Port create commits desired + MAC + optional Migration 078 IP allocation in one serializable transaction. Backend realization is a separate Operation.

The qualified Port was created unattached, received one KIM AUTO MAC and one KIM AUTO IPv4 allocation, converged after a simulated `LOST` response, advanced to a second immutable metadata revision without changing either identity, and retired only after an `ABSENT` read-back. Final Admission consumed a second independently created Port and produced binding/realization generation 1/2 while Port revision and MAC/IP remained unchanged.

## Identity and generation invariants

| Identity | Qualified invariant |
|---|---|
| `port_id` | stable logical identity |
| `port_revision` | desired change only; not advanced by Placement/handoff |
| MAC allocation | exact immutable allocation ID/generation; no Admission randomness |
| IP allocation | exact Migration 078 allocation ID/generation |
| attachment generation | advances independently for request/bind/handoff |
| binding generation | Host/chassis incarnation only |
| realization generation | unattached, attached, handoff, or retire backend convergence |
| OVN UUID | observed backend evidence, never logical identity |

## Negative and fault coverage

- malformed/multicast explicit MAC and caller-supplied AUTO identity reject closed
- stale Network/Subnet/Port revisions and wrong attachment intent reject
- duplicate unreleased MAC/IP are unique and ABA-fenced by allocation ID/generation
- wrong MAC/IP/network/binding/ownership/digest cannot produce `VERIFIED`
- simulated response loss yields `DISPATCH_UNKNOWN`, successor `READ_BACK_FIRST`, and apply-before-read-back rejection
- Port retirement with an active binding or delete protection rejects
- identity release occurs only in the same transaction as exact backend `ABSENT` terminal
- immutable revision/allocation/attempt/observation/terminal/release evidence rejects UPDATE
- legacy Final Admission, automatic IPAM, Recovery, EVACUATE, repeated A→B→C, PCI/SR-IOV, OVN/OVS, Storage, Cleanup, and Terraform-provider suites remain regression scope

## Gate matrix

| Gate | Result | Evidence |
|---|---|---|
| `PORT_RESOURCE_AUTHORITY` | PASS | persistent unattached logical resource producer/current projection |
| `PORT_IMMUTABLE_REVISION` | PASS | desired revision 1→2 and UPDATE rejection |
| `PORT_NETWORK_DEPENDENCY` | PASS | exact ACTIVE/VERIFIED Network revision |
| `PORT_SUBNET_DEPENDENCY` | PASS | exact ACTIVE/VERIFIED Subnet/pool revision |
| `PORT_MAC_REPLAY` | PASS | same create returns exact allocation |
| `PORT_MAC_ABA_FENCING` | PASS | allocation ID/generation/revision fenced release |
| `PORT_IPAM_CONSUMER` | PASS | Migration 078 AUTO/EXPLICIT/NONE contract |
| `PORT_IDENTITY_ATOMIC_COMMIT` | PASS | one serializable create transaction |
| `PORT_BINDING_INCARCATION_SEPARATION` | PASS | desired revision excludes physical incarnation |
| `PORT_FINAL_ADMISSION_COMPATIBILITY` | PASS | new consumer plus legacy producer regression |
| `PORT_RECOVERY_IDENTITY_CONTINUITY` | PASS | existing exact handoff plus resource continuation |
| `PORT_EVACUATE_IDENTITY_CONTINUITY` | PASS | existing exact handoff plus resource continuation |
| `PORT_STANDALONE_OVN_REALIZATION` | PASS | closed LSP operation; parent create forbidden |
| `PORT_OVN_READ_BACK` | PASS | exact marker/network/MAC/IP/binding observation |
| `PORT_RESPONSE_LOSS` | PASS | LOST → READ_BACK_FIRST → terminal |
| `PORT_RETIREMENT_IDENTITY_RELEASE_ORDERING` | PASS | backend ABSENT precedes release |
| `PORT_DELAYED_CLEANUP_ABA_FENCING` | PASS | exact allocation/Port revision and terminal references |
| `PORT_NO_PHYSICAL_IDENTITY_LEAKAGE` | PASS | Host/chassis/UUID/BDF/socket absent from desired |
| `PORT_TERRAFORM_DRIFT_INVARIANT` | PASS | binding/backend generations do not advance desired revision |
| `PORT_RESOURCE_SCHEMA` | PASS | Migration 079 fresh/replay |
| `PORT_DESIRED_REVISION` | PASS | immutable revision 1→2; Host binding excluded |
| `PORT_MAC_ALLOCATION_AUTHORITY` | PASS | exact AUTO/EXPLICIT decision/current/release |
| `PORT_SUBNET_IPAM_CONSUMPTION` | PASS | Migration 078 allocation consumed atomically |
| `PORT_ATTACHMENT_INTENT` | PASS | unattached → requested → bound separation |
| `PORT_BINDING_INCARNATION` | PASS | Placement binding generation independent of revision |
| `PORT_STANDALONE_REALIZATION` | PASS | typed LSP plan; no parent LS create |
| `PORT_READ_BACK_TERMINAL` | PASS | LOST → READ_BACK_FIRST → exact terminal |
| `PORT_RECOVERY_HANDOFF` | PASS | qualified handoff consumer plus Port-resource continuation branch |
| `PORT_EVACUATE_HANDOFF` | PASS | qualified handoff consumer plus Port-resource continuation branch |
| `PORT_IDENTITY_CONTINUITY` | PASS | same Port/MAC/IP across binding incarnation logic |
| `PORT_RETIREMENT_RELEASE_ORDERING` | PASS | ABSENT terminal precedes atomic identity release |
| `PORT_LEGACY_COMPATIBILITY` | PASS | `LEGACY_ADMISSION` defaults and regression |
| `PORT_INTERNAL_AUTHORITY_COMPLETE` | PASS | desired/allocation/attachment/realization/retire chain |
| `PORT_CONTRACT_READY` | PASS | internal contract only |
| `PORT_PUBLIC_API` | NOT RUN | explicitly out of scope |
| `TERRAFORM_KIM_PORT` | NOT RUN | explicitly out of scope |
| `VOLUME_RESOURCE_AUTHORITY` | BLOCKED | next Phase 2 authority gap |
| `VM_PHASE3_READINESS` | NO | Volume and public resource surfaces incomplete |

## Safety assertions

- caller-supplied AUTO MAC/IP authority = none
- Final Admission identity generation = none for `PORT_RESOURCE`
- logical Port desired Host/chassis/BDF/socket = none
- raw OVN/OVSDB payload exposed to caller = none
- command success treated as convergence = no
- identity reused before backend absence = no
- delayed old allocation release can release a new owner = no
- historical evidence rewritten = none
- production workload or real backend mutated = none

## Out of scope

Public CRUD/RBAC/idempotency/OpenAPI/list/import, Terraform `kim_port`, IPv6, Router/LRP, Security Policy compiler, HIGH_PERFORMANCE and DIRECT_IO public profiles, Volume authority, VM aggregate, and real two-Host backend qualification remain future work.
