# ADR-0031: Separate Network desired, segment allocation, and backend realization authority

- Status: Accepted
- Date: 2026-08-14

## Context

Migration 013 and `UpsertNetworkFoundation` established a combined Placement catalog row for Network, Subnet, and a caller-selected Segment Claim. Port-scoped OVN intent could create the shared Logical Switch, but Network had no independent immutable revision, KIM segment allocator, deletion ordering, or standalone verified realization. Publishing CRUD over that aggregate would expose a fixture producer as resource authority.

## Decision

Migration 077 keeps `networks_current` as the single logical current projection and introduces three separately generated authorities:

1. immutable Network desired revisions with stable Network ID, Project, name, closed profile, MTU, segment policy, lifecycle, protection, and digest;
2. KIM-owned VNI/VLAN pool decisions with immutable allocation and release evidence;
3. standalone typed OVN Logical Switch Operations with bounded claims, attempts, observations, terminal evidence, and a current realization projection.

The OVN UUID and realization generation are computed backend incarnation, not desired state. `REALIZE` is terminal only after the deterministic Logical Switch and exact KIM/Network/allocation/revision/plan markers are read back. `RETIRE` is terminal only after exact owned-object absence. Response loss and expired/uncertain claims use read-back first. Segment reuse is forbidden in `RELEASE_PENDING` and becomes possible only after the matching absence terminal releases the current allocation. Delayed evidence is fenced by Network, allocation, operation, and realization generations.

`UpsertNetworkFoundation` remains an explicit compatibility adapter. It may add Subnet catalog state to an exact new-authority Network but cannot overwrite its Network or segment authority. Placement and Port intent require a current verified standalone realization for new-authority Networks; legacy rows retain their existing behavior until migrated.

## Consequences

- Network internal authority is contract-ready without publishing `/api/v1/networks` or `kim_network`.
- logical updates retain Network ID and allocation while producing a new desired revision and backend realization incarnation;
- backend UUID replacement does not create Terraform drift;
- active Subnet/Port dependencies and delete protection reject retirement;
- existing Port plans reuse the independently owned Logical Switch and cannot replace its Network ownership markers;
- Subnet remains a separate Phase 2 blocker requiring its own revision, IPAM/DHCP, delete, and realization authority.
