# P1-C05 OVS Readiness and Power Authority Validation

- 日付: 2026-08-10
- 状態: PASS / P1-C05 In Progress
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`

## Authority path

```text
Final Admission Port/IP/MAC/Binding authority
  -> NETWORK_PORT_OVS_REALIZE
  -> Agent-managed Segment Claim to OVS bridge mapping
  -> standard OVS bridge observation
  -> inactive libvirt NIC configuration/read-back
  -> immutable pre-boot Port evidence
  -> every required Port current
  -> current Domain + Storage + Image + VM/Plan generations
  -> current ARMED Host authority + Session + Capability + Readiness generations
  -> same PostgreSQL transaction
  -> Boot Readiness READY + typed RUNNING Job/Command
```

Command は bridge 名、XML、path、argv、libvirt method を受け付けません。`READY` は power-on を安全に発行できる authority であり、起動後 dataplane の通信可能性を表しません。

## Persistence validation

fresh PostgreSQL 17 で migration 001〜020 と integration test を実行しました。

```text
TestMigratePostgreSQLIntegration: PASS
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration: PASS
```

確認事項:

- Port/Network/Segment/Host mapping/Binding/VM/Plan generation の transaction 再検証
- ARMED Host authority と current Session/Capability/Readiness generation の transaction 再検証
- MATCHED Verification と immutable Port evidence
- required Port 全件の canonical evidence set digest
- Network REALIZED、Boot READY、typed RUNNING Job/Command の不可分生成
- replay 時に Power Job/Command/Event を重複生成しない
- pre-boot evidence を post-boot dataplane convergence に昇格しない

## Real Host qualification

既存 VM を変更せず、test 内で disposable inactive Domain `kim-ovs-qualification-20260810` を define し、既存 OVS bridge `br-int` へ closed typed virtio NIC を追加しました。standard libvirt inactive XML と `ovs-vsctl br-exists` の read-backが一致し、test 終了時に Domain を undefineしました。

```text
TestDisposableLibvirtOVSPrebootRealization: PASS
cleanup-pass
```

## Remaining

- SRIOV_DIRECT typed realization と qualified VF/libvirt PCI identity evidence
- post-boot OVS/OVN/dataplane convergence projection
- READY 後の real power-on execution/convergence fault campaign
