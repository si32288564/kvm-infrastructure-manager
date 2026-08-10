# P1-C05 Power Runtime Convergence Validation

- 日付: 2026-08-10
- 状態: PASS / P1-C05 In Progress
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`

## Authority path

```text
Boot Readiness READY
  -> typed VIRTUAL_MACHINE_POWER_STATE_ENSURE
  -> standard libvirt Domain start
  -> Result loss / Agent process loss
  -> Attempt UNKNOWN
  -> standard libvirt Domain state read-back
  -> immutable Verification evidence
  -> immutable VM power observation evidence
  -> current VM power projection MATCHED / RUNNING
```

`RUNNING` は Domain power state の current projection であり、post-boot dataplane convergence、guest readiness、application health を意味しません。

## Persistence validation

fresh PostgreSQL 17 の migration 001〜021 と Placement integration で、READY transaction が生成した power Command に対する MATCHED Verification のみが `vm_power_observation_evidence` と `vm_power_state_current` を生成することを確認しました。

```text
TestMigratePostgreSQLIntegration: PASS
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration: PASS
```

確認事項:

- VM generation、Host、READY、desired power state、typed Command schema の transaction 再検証
- direct Result observation と resync `read_back` evidence の同一 projection contract
- immutable power evidence と stale observation generation fencing
- power evidence の UPDATE 拒否

## Real KVM qualification

`FI-LIBVIRT-004` の full-process campaign で、標準 `qemu:///system` Domain に対する start、Result path loss、Agent `SIGKILL`、Lease expiry、Attempt `UNKNOWN`、再接続、libvirt read-back `MATCHED`、同一 Job convergence を確認済みです。この増分では、その verification evidence を VM runtime power projection authority へ接続しました。

## Remaining

- READY 発行から runtime projection までを単一の fresh full-lifecycle fixture で再実行する release qualification
- post-boot OVS/OVN/dataplane convergence projection
- SRIOV_DIRECT pre-boot realization
- guest readiness
