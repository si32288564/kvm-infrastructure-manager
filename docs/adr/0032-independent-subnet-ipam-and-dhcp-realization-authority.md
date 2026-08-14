# ADR-0032: Separate Subnet desired, IPAM, and DHCP realization authority

- Status: Accepted
- Date: 2026-08-14

## Context

Migration 013 stored a Subnet as part of `UpsertNetworkFoundation`. Final Admission could select an IPv4 address from that row, but there was no Project-owned immutable Subnet revision, independent pool lifecycle, allocation decision history, gateway/DHCP/DNS contract, standalone backend convergence, or safe retirement order. Publishing that projection would have made a Placement fixture and an OVN response into resource authority.

## Decision

Migration 078 retains stable `subnet_id` and separates three identities and lifecycles:

1. immutable logical IPv4 Subnet revisions and a latest current projection, bound to the exact current `ACTIVE` and `VERIFIED` parent Network revision;
2. a KIM-owned IPAM pool plus immutable AUTO/EXPLICIT allocation and release evidence, with allocation generation and retirement freeze;
3. a standalone typed `kim.network-intent.ovn-subnet/v1` realization Operation for the deterministic OVN DHCP Options incarnation and exact parent Logical Switch identity.

The current closed profile accepts canonical IPv4 prefixes `/16` through `/30`. Overlap is forbidden among live Subnets in the same Network; separate Networks are separate overlap scopes. Gateway policy is `NONE`, deterministic `AUTO` first-usable, or validated `EXPLICIT`. The gateway and explicit reservations are excluded from allocation. DNS is a bounded list of IPv4 service addresses. Raw OVN syntax, backend UUID, path, Host, Chassis, Router Port, and DHCP row identity are never desired input. IPv6, multiple pool sets, Router/LRP and raw DHCP option maps fail closed until separate contracts exist.

Final Admission remains the Port mutation authority. For a `SUBNET_RESOURCE` row it requires the exact parent Network and Subnet realization terminals and atomically records the IPAM allocation decision/current row with Port/IP/MAC/binding claims. `LEGACY_FOUNDATION` rows retain their explicit compatibility branch. Release uses the existing two clean absence observations; the exact IPAM allocation transitions through `RELEASE_PENDING` and immutable release evidence to `RELEASED`.

`RETIRE_PENDING` first freezes the pool and records immutable lifecycle evidence. Active/release-pending allocations or Ports reject retirement. Only exact owned DHCP absence plus exact current parent Network verification produces the Subnet `ABSENT` terminal, logical `DELETED`, and pool `RETIRED`. A response loss or expired uncertain claim always reads back first. Subnet, pool, allocation, Operation, parent Network, and realization generations fence delayed evidence and reuse.

## Consequences

- internal Subnet authority is contract-ready without `/api/v1/subnets` or `kim_subnet`;
- a backend DHCP UUID replacement is a physical incarnation, not desired drift;
- DHCP-disabled Subnets converge to `VERIFIED` from exact parent association plus absence (`NOT_REQUIRED` semantics), not a caller state string;
- a DHCP Options object is verified independently; future Port realization owns per-Port DHCP option attachment and Router/LRP remains out of scope;
- Port is the next Phase 2 authority gap; Volume remains blocked;
- synthetic adapter/PostgreSQL qualification does not raise production scores or claim real OVN qualification.
