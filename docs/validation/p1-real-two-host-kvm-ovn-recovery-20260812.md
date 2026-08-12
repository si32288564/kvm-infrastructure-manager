# Real Two-Host KVM Recovery with non-empty OVN Port

Date: 2026-08-13 (JST)

## Result

```text
REAL_TWO_HOST_KVM_RECOVERY_AUTHORITY           = PASS
PORT_BINDING_HANDOFF_AUTHORITY_FOUNDATION      = PASS
GENERIC_OVN_PORT_UNBIND_AUTHORITY               = PASS
REAL_SOURCE_OVN_UNBIND                          = PASS
REAL_OVN_PORT_HANDOFF                           = PASS
REAL_DESTINATION_OVN_BINDING                    = PASS
REAL_OVS_DATAPLANE                              = PASS
REAL_NONEMPTY_NETWORK_RECOVERY_VERIFICATION     = PASS
REAL_TWO_HOST_KVM_OVN_RECOVERY_AUTHORITY        = PASS
```

The final opt-in campaign completed in `13.56 s`. It used one disposable Local LVM root volume, one non-empty OVS Port, no PCI/SR-IOV, and one PostgreSQL authority history from Failure Epoch through Terminal Decision.

## Exact profile

| Identity | Value |
|---|---|
| source | `kvm-base-g01-n001-p.core.s01.si1230.com` |
| destination | `kvm-base-g02-n001-p.core.s01.si1230.com` |
| VM UUID | `f1c06a00-0000-4000-8000-202608120058` |
| Network | `real-recovery-network060` |
| Port | `f1c06a00-0000-4000-8000-202608120060` |
| MAC | `02:00:00:00:60:01` |
| IP | `192.0.2.60` |
| source Port/Binding | generation `1/1` |
| destination Port/Binding | generation `2/2` |

Both Hosts used their production-shaped Host-local NB/SB endpoints and `br-int`; the campaign did not assume a shared SB database. Source and destination Network mutations were performed by the ordinary PostgreSQL-backed OVN runtime worker and closed typed adapter. The qualification harness did not issue SSH `ovn-nbctl`, `ovn-sbctl`, or `ovs-vsctl` mutation commands.

## Source retirement and Handoff

The exact source `UNBIND` work completed with one attempt and one immutable terminal retirement evidence. Its positive proof recorded:

- stable KIM ownership and logical Port preservation;
- `requested-chassis` absent in NB;
- exact source chassis inactive in SB;
- exact source `iface-id` absent from Host OVS;
- distinct NB/SB/OVS observation generations and digests.

The immutable Handoff preserved Port, MAC, and IP identity while advancing only the Port and Binding incarnations from `1/1` to `2/2`. Historical source binding/evidence was not rewritten. The current Handoff reached `VERIFIED` only after destination pre-boot realization and post-power dataplane evidence.

## Destination and split-brain assertion

The destination ordinary `RECONCILE` work first established NB intent before power-on and then recovered the same stable work identity with `READ_BACK_FIRST` after power-on to observe SB convergence. The typed libvirt NIC used the exact UUID Port as its OVS `interfaceid`.

At the PASS point:

```text
g01 Domain                    = SHUTOFF
g01 NB logical Port ownership = PRESERVED
g01 SB source chassis         = INACTIVE
g01 OVS iface-id              = ABSENT

g02 Domain                    = RUNNING
g02 SB destination chassis    = ACTIVE
g02 OVS Interface             = tap0
g02 OVS iface-id              = exact Port ID
g02 OVS link_state            = up
```

This proves the required Network split-brain negative for the dedicated logical Port. It does not claim tenant L3 reachability, guest readiness, PCI/SR-IOV recovery, or a shared production OVN topology.

## Recovery Verification and Terminal

The non-empty Network evidence set used destination observation generation `2` and digest `24feb36a6332db19e21f35ae239949cc15f7d3684e85913e577e3513b5af25fe`. The same PostgreSQL history converged to:

```text
Recovery Verification = VERIFIED
Recovery Operation    = VERIFIED (state generation 4)
Failure Epoch         = RECOVERED (transition generation 4)
Recovery Budget       = RELEASED (state generation 3)
Terminal Decision     = VERIFIED
```

The terminal transaction remained the only authority that committed Operation `VERIFIED`, Epoch `RECOVERED`, and Budget `RELEASED`. A successful Network work, libvirt power result, or VM RUNNING observation was not used as a substitute terminal cause.

## Incarnation-history and read-only authority fixes

Real qualification exposed two generic lifecycle defects and fixed them without rewriting historical evidence:

1. Migration 061 makes pre-boot and dataplane evidence unique per exact `(VM, Port generation, Binding generation, observation generation)` incarnation. Current projections advance lexicographically by VM/Port/Binding/observation generation, so a Recovery handoff can retain generation `1/1` history and publish generation `2/2` even when both backends begin their local observation counter at `1`.
2. closed `READ_ONLY_VERIFICATION` Leases now accept a current AUTHORIZED session while Host mutation authority is either `ARMED` or `FENCED`; they do not rearm, consume, or mutate Host authority. This permits post-mutation OVS read-back in the same current session while retaining the FENCED-host recovery contract.

## Cleanup and production safety

After evidence collection, exact UUID/name/ownership/SHA guards were applied before cleanup. The disposable Domains, LVs, VGs, loop devices, backing files, KIM-owned Logical Switch/Ports, OVS Interface, journals, and helper binaries were removed from both Hosts. Immutable PostgreSQL authority history was not modified.

Before/after production checks retained the same VM lists and OVS external IDs. In particular, g01/g02 kept their original system IDs, bridge mappings, Host-local `ovn-remote`, and `ovn-encap-ip=127.0.0.1`. No production VM, route, bridge, Port, or OVN/OVS object was modified.

## Qualification

- real opt-in g01→g02 non-empty OVN Recovery campaign: PASS (`13.56 s`)
- source generic `UNBIND` and exact NB/SB/OVS proof: PASS
- immutable PortBindingHandoff and identity preservation: PASS
- destination ordinary Network reconcile and OVS dataplane: PASS
- non-empty Recovery Verification and atomic Terminal: PASS
- full dedicated artifact cleanup: PASS
- production before/after identity: PASS
- direct SSH Network mutation during campaign: none
- fixture evidence substitution: none
- fresh PostgreSQL 17 migrations `001–061`: PASS
- full PostgreSQL persistence integration: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- libvirt-tagged Domain/VM/Volume backend tests: PASS
- `make check`: PASS
- documentation lint: PASS (`475` requirements, `725` test contracts, `238` links)
- `git diff --check`: PASS

Historical authority evidence remained immutable throughout backend cleanup.
