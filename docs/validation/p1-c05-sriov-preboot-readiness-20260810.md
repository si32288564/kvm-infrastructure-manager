# P1-C05 SR-IOV Pre-boot Readiness Validation

- 日付: 2026-08-10
- 状態: PASS / hardware qualification pending

## Authority path

```text
Final Admission VF Claim
  + current PCI observation
  + current Qualification Binding
  + validated VF_ASSIGN
  + current allocation policy
  + SRIOV_DIRECT Port Binding
  -> NETWORK_PORT_SRIOV_REALIZE
  -> typed libvirt hostdev interface
  -> inactive Domain PCI/MAC identity read-back
  -> immutable Port realization evidence
  -> all required Ports REALIZED
  -> READY + typed power-on authority
```

Command は PCI address を Final Admission の VF Claim identity として受け取りますが、raw XML、path、argv、libvirt method/flags は受け取りません。

## Validation

- fresh PostgreSQL 17 migration 001〜022: PASS
- Placement → qualified VF Claim → SRIOV_DIRECT evidence → READY integration: PASS
- libvirt `test:///default` hostdev XML/read-back contract: PASS
- `make check`: PASS

## Hardware qualification boundary

この検証は relationship/parser、authority binding、libvirt pre-boot configuration contract を確認するものです。専用 VF の明示的な割当てがないため、実 Host の VF assignment、IOMMU isolation、driver detach/rebind、Domain start は認証していません。

したがって production support profile では、実 device/driver/kernel/libvirt/QEMU profile に結び付く Qualification Evidence がない VF は従来どおり `BLOCKED` のままです。
