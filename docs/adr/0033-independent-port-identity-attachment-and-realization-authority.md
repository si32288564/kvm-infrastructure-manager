# ADR-0033: Independent Port identity, attachment, and realization authority

- Status: Accepted
- Date: 2026-08-15
- Scope: Migration 079 internal authority; no public API or Terraform surface

## Context

Before Migration 079, `network_ports_current` was created inside Final Admission. Logical Port identity, MAC/IP claims, workload attachment, Host binding, and OVN convergence therefore appeared to be one lifecycle. That coupling cannot represent a persistent unattached Port and makes a Host move look like desired-resource drift.

## Decision

KIM keeps stable `port_id` and separates four authorities:

1. `port_resource_revision_evidence` records immutable Project/Network/Subnet-bound logical desired revisions. Host, chassis, backend UUID, BDF, socket and binding generation are excluded.
2. `port_mac_allocation_*` owns exact KIM MAC decisions. Migration 078 `subnet_ip_allocation_*` remains the only IPAM authority. Port creation commits the desired revision and both allocations atomically.
3. `port_attachment_intent_*` records workload attachment intent. A valid Port can be `UNATTACHED`; Placement changes it to `BOUND` by creating a separate `port_bindings_current` incarnation. Handoff advances attachment and binding generation without advancing `port_revision`.
4. `port_realization_*` is a typed claim/lease/attempt/observation/terminal pipeline. An unattached STANDARD Port is realized as an OVN Logical Switch Port on an already verified parent Logical Switch. The Port actuator may not create or adopt the parent Network.

Final Admission remains backward compatible with `LEGACY_ADMISSION`, but for `PORT_RESOURCE` it validates and consumes the existing exact Port revision, attachment intent, MAC allocation and Subnet IP allocation. It cannot choose replacement identities. Recovery and EVACUATE reuse the existing qualified handoff and source-retirement path; Migration 079 adds Port-resource attachment and realization history to that path.

Retirement is ordered: freeze new attachments, reject active bindings, retire the exact backend LSP, verify absence, record immutable identity release evidence, release MAC/IP, then project `DELETED`. Allocation release is therefore fenced against a delayed old incarnation.

## Consequences

- Port revision, identity allocation generation, attachment generation, binding generation, and realization generation are independent.
- `AUTO` identities are selected only by PostgreSQL authority. Explicit MAC is closed to valid unicast syntax; explicit IPv4 is checked by the Subnet pool.
- Response loss produces `READ_BACK_FIRST`; command exit status is never terminal evidence.
- Existing Admission-created Ports remain explicit `LEGACY_ADMISSION` rows and continue to use the qualified legacy flow.
- `/api/v1/ports`, Terraform `kim_port`, IPv6, Router, Security Policy, HIGH_PERFORMANCE, DIRECT_IO, Volume authority, and VM aggregate remain out of scope.

## Alternatives rejected

- Treating the Placement request as Port desired authority: this preserves dual ownership and cannot represent unattached Ports.
- Reallocating MAC/IP during Host moves: this breaks logical identity continuity.
- Releasing identities when a command reports success: a stale backend LSP could retain an already reused address.
- Allowing the Port worker to create the Logical Switch: parent Network ownership would become ambiguous.
