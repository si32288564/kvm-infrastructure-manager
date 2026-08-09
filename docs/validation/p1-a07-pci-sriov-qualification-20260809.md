# P1-A07 PCI/IOMMU/SR-IOV Qualification Validation

## 1. Scope

PCI/IOMMU/SR-IOV を次の独立した authority chain へ接続した。

```text
Raw sysfs Evidence
  → Normalized PCI Device Projection
  → Immutable Qualification Evidence
  → Current Qualification Binding
  → Allocation State
  → PostgreSQL VF Claim
```

Inventory schema は PF/VF relationship と PCI evidence semantics を追加した `kim.inventory.snapshot/v3` とする。v2 decoder へ未知 field を暗黙投入しない。

## 2. Observed and Normalized Fixture

Linux OS Integration Adapter の fixture で次を確認した。

| Case | Expected result |
|---|---|
| PF/VF、NUMA、IOMMU、driver、vendor/device | canonical PCI Fragment と source evidence へ正規化 |
| reciprocal `virtfn` / `physfn` | VF の PF address/index と relationship `AVAILABLE` |
| `physfn` conflict | SR-IOV observation/relationship `UNKNOWN`、snapshot `DEGRADED` |
| IOMMU source permission denied | IOMMU observation `UNKNOWN`、snapshot `DEGRADED` |
| PF が存在しない | SR-IOV observation `UNAVAILABLE`、snapshot `COMPLETE` |

fixture pass は parser/relationship contract の証拠に限定し、hardware Qualification Evidence を生成しない。

## 3. Qualification and Allocation Authority

migration 006 で以下を分離した。

- `host_pci_device_projections`: rebuildable normalized observation
- `pci_qualification_evidence`: immutable qualification/profile/artifact/evaluator/operation evidence
- `pci_qualification_bindings_current`: `CURRENT / STALE / UNKNOWN / REVOKED`
- `pci_allocation_policy_bindings`: current policy authority skeleton
- `pci_vf_allocation_claims`: exclusive `ACTIVE / RELEASE_PENDING / RELEASED` claim

Qualification は observation generation/digest と device/firmware/driver/kernel/IOMMU/libvirt/QEMU fingerprintへ binding する。`VF_ASSIGN` が validated operation set にない evidence は VF Final Admission に使用できない。

Fresh PostgreSQL 17 integration で次を確認した。

```text
Observed AVAILABLE + Qualification missing → BLOCKED
Qualification Evidence + matching Binding → CURRENT
CURRENT + policy allowed                   → AVAILABLE
two concurrent VF claims                   → one ACTIVE / one conflict
active claim                               → CLAIMED
driver + observation generation change     → STALE / BLOCKED
revocation evidence revision               → REVOKED
```

Inventory update、Qualification record/binding、Final Admission は同じ Host/device advisory-lock key を使用する。Final Admission は DB authority mode、Host/device generation、observation/relationship、Qualification Binding/profile/operation set、policy generation、NUMA/IOMMU constraint、active claim 不在を一つの transaction で再読込する。

## 4. Real Linux Observation

2026-08-09 に read-only、network disabled の Linux container runtimeで実 `/proc` と `/sys` を収集した。

```text
collection_status       DEGRADED
pci_devices             14
pci-observation         AVAILABLE
pci-numa-locality       UNKNOWN (pci_numa_incomplete)
iommu-observation       UNAVAILABLE (no_iommu_group_observed)
sriov-observation       UNAVAILABLE (no_sriov_pf_observed)
sriov_pfs               0
sriov_vfs               0
```

この結果は実 sysfs に対する PCI discovery の validation であり、IOMMU/SR-IOV hardware qualification ではない。PF/VF が存在せず Qualification Evidence もないため、VF Allocation を `AVAILABLE` にする根拠は生成されない。

## 5. Remaining Hardware Qualification

- IOMMU enabled、複数 NUMA node の実 Host
- supported NIC PF/VF と firmware/driver profile
- `VF_ENABLE / VF_DISABLE`
- libvirt/QEMU による `VF_ASSIGN / VF_DETACH / VF_REASSIGN`
- assignment 後の guest/Host/libvirt read-back
- Host reboot、driver reload、firmware change 後の binding invalidation
- production evaluator identity/authorization と certification artifact verification

これらを通過するまで、fixture または discovery だけで Allocation State を `AVAILABLE` にしない。
