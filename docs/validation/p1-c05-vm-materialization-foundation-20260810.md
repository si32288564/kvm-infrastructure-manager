# P1-C05 VM Materialization Foundation Validation

- 日付: 2026-08-10
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- 状態: PASS / P1-C05 In Progress

## 1. Authority path

```text
accepted Final Admission
+ current RESERVED Compute Claim
+ verified Image revision
+ immutable Flavor shape
+ current BOUND root Volume Binding
+ current RESERVED SINGLE_WRITER Attachment Claim
  -> immutable VM Materialization Plan
  -> VM current desired authority
  -> Job + closed typed VIRTUAL_MACHINE_DEFINE Command
  -> write-before-execute Execution contract
  -> standard libvirt DomainDefineXML
  -> inactive Domain XML read-back
```

Plan、VM authority、Job、Command は一つの PostgreSQL transaction で commit / rollback します。Plan は Image と Network を `PENDING` と記録し、Domain define 成功を Image materialization、Network realization、power-on、guest readiness へ昇格しません。

## 2. Closed typed boundary

Command は Domain UUID、materialization generation、bounded vCPU/memory、Image identity、root Volume/VG/LV identity のみを受理します。Agent は admin-configured VG UUID mapping と KIM-owned resource key から root device path、Domain name、`vda`、disk serial、standard Domain XML を導出します。

次を caller input として受理しません。

- raw Domain XML
- source path
- machine type / CPU model
- libvirt method / flag
- arbitrary device list
- Image/Network の実現済み自己申告

## 3. Real KVM evidence

専用 loop-backed VG/LV と Domain UUID `77777777-7777-4777-8777-777777777777` を使用しました。標準 `qemu:///system` に inactive Domain を define し、同じ Command の再実行が新しい Domain を作らず、UUID/name、plan digest、generation、vCPU/memory、root source/target/serial の read-back が `MATCHED` へ収束しました。

```text
TestDefineDomainFromTypedAdmissionPlanOnKVM
--- PASS (0.11s)
qualification-pass
```

専用 Domain、VG/LV、PV、loop device、source archive、test binary は終了後に削除し `cleanup-pass` を確認しました。既存 `vg0` と既存 Domain は変更していません。

## 4. Persistence validation

fresh PostgreSQL 17 で migration 001〜017 と Placement integration を実行しました。

```text
TestMigratePostgreSQLIntegration
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration
PASS (race detector)
```

検証内容:

- bootable Volume が正確に1件でない場合は fail closed
- BOUND Binding / RESERVED Attachment Claim がない場合は plan/Job/Command を作らない
- stable request replay は Compute Claim が後続 `ALLOCATED` へ進んだ後も同一 plan/Command へ収束
- plan evidence の UPDATE は拒否
- Image/Network state は `PENDING`

## 5. Remaining work

- verified Image binary cacheからroot Volumeへのtyped materialization
- OVS/SR-IOV Port realizationとDomain NIC
- Domain define Result-loss / Agent-kill / UNKNOWN read-back full-process campaign
- VM current projectionのVerification連携
- start/delete、resource release、quarantine/fencing
- public API、Quota、Operation/Event統合
