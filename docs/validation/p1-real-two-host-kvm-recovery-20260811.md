# P1 Real Two-Host KVM Recovery Qualification

- Date: 2026-08-11
- Result: `REAL_TWO_HOST_KVM_QUALIFICATION = BLOCKED`
- Reason: the only available second KVM Host is an important production Host and does not satisfy the destructive lab-only guard
- Repository authority baseline: `147f4b94a6a85b32b617227adaa78a61b85ab6b7`

## Intended profile

```text
action: RESTART_ON_OTHER_HOST
source: kvm-base-g01-n001-p.core.s01.si1230.com
destination candidate: kvm-base-g02-n001-p.core.s01.si1230.com
storage: isolated Local LVM qualification VG
network: zero-Port
PCI/SR-IOV: excluded
identity: same disposable workload and VM UUID
```

No test VM UUID was allocated because the environment guard failed before any backend mutation.

## Read-only preflight evidence

Both Hosts accepted SSH and standard `qemu:///system` read-only queries.

| Check | Source g01 | Destination candidate g02 |
|---|---|---|
| Host role | Existing lab/test KVM used by prior qualification | Important production KVM Host |
| Existing libvirt domains | 4 | 15 |
| Dedicated disposable recovery VM | Not created | Absent |
| Dedicated isolated recovery VG/LV | Not created | Absent |
| Current two-Host KIM Agent/session profile | Not established | No current `kim-host-agent` process observed |
| Mutation performed by this qualification | None | None |

g01 had existing temporary `kim-host-agent` processes from earlier single-Host qualification paths, but they did not form a current two-Host Recovery control plane with g02. Existing production Domain names, VG/LV identities, routes, network configuration, and Host authority were not modified.

## Guard decision

The requested completion condition requires an explicit opt-in, two lab-approved Hosts, a dedicated disposable VM identity, isolated storage, current KIM sessions/authority, and an operator cleanup plan. The available topology has one lab Host plus one important production Host. The instruction explicitly prohibits using an important Host as the destructive Recovery target, so the guard failed closed before:

- Failure injection;
- Host authority fencing;
- source VM shutdown;
- Local LVM creation or attachment;
- destination libvirt define;
- typed destination power-on;
- Recovery Verification or Terminal Decision.

Consequently none of the following are claimed:

```text
source RUNNING → actual SHUTOFF
destination absent → actual define → actual RUNNING
split-brain negative assertion
real Recovery VERIFIED / RECOVERED / Budget RELEASED
real recovery latency
```

Fixture-backed PostgreSQL authority qualification from Migration 055 remains PASS, but it is not substituted for this real-backend gate.

## Required lab topology to unblock

Before rerunning, provide:

1. two Hosts explicitly classified and allow-listed as disposable recovery lab targets;
2. current KIM Agent sessions and ARMED destination authority on both Hosts;
3. a deterministic disposable VM UUID and known RAW image/flavor;
4. per-Host isolated loop-backed Local LVM VGs or equivalent disposable storage;
5. a zero-Port profile or an explicitly isolated test network;
6. explicit opt-in such as `KIM_REAL_KVM_RECOVERY_QUALIFICATION=1` plus exact source/destination Host and VM UUID allow-lists;
7. an operator rollback/cleanup procedure that removes only test backend artifacts while retaining immutable KIM evidence.

The guard must reject equal source/destination Hosts, non-lab Host classifications, missing exact allow-list entries, existing foreign VM UUIDs, non-empty production VGs, or missing current Agent/Host authority.

## Safety and cleanup

- No VM state change was issued.
- No libvirt Domain was defined, started, stopped, or undefined.
- No LVM PV/VG/LV or loop device was created.
- No network interface, route, OVS/OVN object, or firewall state was changed.
- No PostgreSQL state on either KVM Host was created or changed.
- No backend cleanup was necessary.
- Historical KIM evidence was not altered.

## Regression qualification

The blocked real-backend decision does not replace the existing authority qualification. A disposable local PostgreSQL 17 container was used only for repository regression and was removed after the checks.

| Check | Result |
|---|---|
| Fresh PostgreSQL 17 persistence integration | PASS |
| `go test -race ./...` | PASS |
| `make check` | PASS |
| Documentation contracts | PASS: 471 requirements, 715 test contracts, 234 links |
| `git diff --check` | PASS |

## Remaining gate order

1. real two-lab-Host zero-Port/Local-LVM Recovery;
2. non-empty Network/OVN Recovery;
3. PCI/SR-IOV Recovery;
4. source cleanup authority;
5. `EVACUATE` backend;
6. repeated Recovery soak/chaos.
