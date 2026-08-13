# P1 PCI / SR-IOV Recovery Authority Validation

- Date: 2026-08-13
- Migration: 063
- Generic authority: PASS
- Real two-Host physical campaign: BLOCKED (safe preflight)

## Gates

```text
GENERIC_PCI_VF_RETIREMENT_AUTHORITY          = PASS
GENERIC_PCI_VF_HANDOFF_AUTHORITY             = PASS (persistence/consumer foundation)
DESTINATION_VF_ALLOCATION_AUTHORITY          = PASS (ordinary Final Admission)
DESTINATION_LIBVIRT_HOSTDEV_AUTHORITY        = PASS (typed backend/integration)
RECOVERY_PCI_DANGEROUS_STEP_CONSUMER         = PASS
RECOVERY_PCI_VERIFICATION_CONSUMER           = PASS
REAL_SOURCE_VF_RETIREMENT                    = BLOCKED
REAL_PCI_VF_HANDOFF                          = BLOCKED
REAL_DESTINATION_VF_ALLOCATION               = BLOCKED
REAL_DESTINATION_HOSTDEV                     = BLOCKED
REAL_PCI_RECOVERY_VERIFICATION               = BLOCKED
REAL_TWO_HOST_KVM_PCI_RECOVERY_AUTHORITY     = BLOCKED
```

## Generic qualification

Migration 063 adds exact-incarnation retirement work/attempt/evidence/current/latest projections and generic VF handoff evidence/current projections. `PCI_VF_RETIRE` is a closed Agent command; caller input cannot supply XML, shell, argv, libvirt method, sysfs path, or arbitrary BDF outside accepted PCI authority. The backend constructs typed libvirt detach configuration and reads inactive Domain hostdev plus fixed `/sys/bus/pci/devices/<validated BDF>` driver/IOMMU state.

PostgreSQL 17 integration exercised mutation-response ambiguity: attempt 1 committed `UNKNOWN`, the work became `DISPATCH_UNKNOWN`, attempt 2 was `READ_BACK_FIRST`, and one exact MATCHED Command Verification produced immutable `VERIFIED` retirement with the claim moved only to `RELEASE_PENDING`. Final Admission binds destination claims to exact SR-IOV Port/Binding incarnations. Recovery planning consumes verified source retirement; Final Admission commits the generic handoff; destination SR-IOV realization advances its current projection; dangerous-step and Recovery Verification require the current complete PCI evidence set.

## Real g01/g02 safe preflight

Read-only SSH preflight inspected `lspci -Dnnk`, libvirt Domain lists, and `/sys/bus/pci/devices/*/physfn` on:

- `kvm-base-g01-n001-p.core.s01.si1230.com`
- `kvm-base-g02-n001-p.core.s01.si1230.com`

Neither Host exposed a VF (`physfn` result set was empty). g01 has I211 NICs without enabled SR-IOV VFs; g02 has 82576-class NICs but also no current VF. g02 is production and all listed Domains remained running. Therefore no VF enablement, driver rebinding, libvirt hostdev mutation, VM power mutation, or Recovery campaign was attempted.

The after-check returned the same four running g01 Domains and the same fifteen running g02 Domains, with an empty VF set on both Hosts. Production Host state is unchanged.

This is a safe `BLOCKED`, not a PASS or failed mutation. Unblock requires a disposable qualified VF on each Host, explicit KIM inventory/qualification/policy authority, an isolated VM/Port, and proof that the VF is not a production management/uplink dependency.

## Validation

- fresh PostgreSQL 17 migration 001–063: PASS
- full persistence integration: PASS
- typed SR-IOV/VF retirement backend tests: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- libvirt-tagged SR-IOV/VM/Volume/Domain backend regression: PASS
- `make check`: PASS
- documentation lint: PASS (`480 requirements`, `734 test contracts`, `234 links`)
- `git diff --check`: PASS

No direct SSH mutation and no fixture evidence were used for a real authority claim. The dedicated PostgreSQL container is removed after validation; immutable repository test evidence remains.
