# P1-C04 Libvirt Attachment Kill / Read-back Validation

- 日付: 2026-08-10
- 対象: closed typed Local LVM disk attach/cold-detach、Agent process kill、libvirt device/LVM holder read-back、Attachment authority
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- 状態: PASS / P1-C04 In Progress

## 1. Validated authority path

```text
current BOUND Local LVM Binding
+ SINGLE_WRITER Attachment Claim
  -> LOCAL_LVM_VOLUME_ATTACHMENT_ENSURE/v1
  -> write-before-execute journal
  -> standard libvirt AttachDeviceFlags
  -> Domain XML device + lvs lv_device_open read-back MATCHED
  -> Result construction
  -> Agent subprocess SIGKILL
  -> typed Verification: ATTACHED
  -> maintenance shutoff
  -> standard libvirt config detach
  -> Domain device absence + holder release MATCHED
  -> Result construction
  -> Agent subprocess SIGKILL
  -> typed Verification: DETACHED
```

Command caller は raw XML、source path、target device name、libvirt method/flag、VG/LV name、argv を指定できません。Command は Domain UUID、Volume/Binding identity、LV UUID、bounded disk slot、desired state、`SINGLE_WRITER` access mode に閉じます。Agent は admin-configured VG UUID mapping と KIM-owned LV key から source path と `vd[b-z]` target を導出します。

## 2. Real Host isolation and evidence

既存 `vg0` と既存 Domain は使用・変更していません。qualification 専用に loop-backed VG/LV と Domain を作成しました。

```text
Domain UUID: 66666666-6666-4666-8666-666666666666
VG UUID:     k0Vch4-gN8d-6UFT-xtLd-Vmyw-aJuB-c5RHGK
LV UUID:     2t0iNv-jg0c-4jDr-iGa8-z6Hz-pRyY-3YjaQz
LV key:      kim-7b68695a280a61e77443676935fe2814
LV size:     67108864 bytes
target:      vdb
```

Attach verification は Domain XML の source/target/serial identity と `lv_device_open=open` を要求しました。Detach verification は maintenance shutoff 後の Domain device absence と non-open holder を要求しました。終了時は Domain `shut off`、block device なし、`lv_device_open` non-open でした。

専用 Domain、VG/LV、loop device、image、remote source/test binary は終了後に削除し、`cleanup-pass` を確認しました。

## 3. Fault and authority assertions

1. attach/detach の両方で journal `Prepare` を side effect 前に fsync / atomic rename する。
2. libvirt response だけを `ATTACHED` / `DETACHED` authority にしない。
3. attach は exact Domain UUID、target、serial、derived source identity、non-read-only、LVM holder open が一致した場合だけ `MATCHED` にする。
4. foreign disk が同じ target を使用している場合は置換せず `CONFLICTING` にする。
5. detach は device absence と LVM holder release が両方一致した場合だけ `MATCHED` にする。
6. running guest の live detach が未収束なら response success を `DETACHED` にせず `UNKNOWN` のまま read-back する。今回の initial qualification は明示 maintenance shutoff 後の cold detach を使用した。
7. Agent process kill は side effect absence の証明にせず、new process が original Command/Attempt/journal identity で read-back する。
8. PostgreSQL は既存 typed `command_verification_evidence` と current BOUND Binding / Attachment generation / Claim を再検証する。
9. accepted attach evidence は current state `ATTACHED` と Claim `ACTIVE`、accepted detach evidence は `DETACHED` と Claim `RELEASED` を同一 transaction で確定する。
10. immutable evidence update、different device/LV identity、holder-open detach、same evidence identity / different digest を拒否する。
11. より新しい detach observation を受理した後の旧 attach observation replay を拒否し、current `DETACHED` / `RELEASED` authority を巻き戻さない。

## 4. Validation results

```text
go test ./internal/agent/execution/locallvm \
  ./internal/agent/execution/libvirtvolume
PASS

go test -tags libvirt -c \
  ./internal/agent/execution/libvirtvolume
PASS (remote build, standard libvirt 12.0.0)

TestLibvirtVolumeAttachDetachProcessKillReadBack
  attach mutation reached Result and Agent subprocess was killed
  attach device and holder read-back matched
  Domain entered maintenance shutoff before cold detach
  detach mutation reached Result and Agent subprocess was killed
  detach absence and holder release read-back matched
PASS (1.01s)

KIM_POSTGRES_TEST_URL=postgres://.../kimtest?sslmode=disable
go test -race ./internal/persistence/postgres \
  -run 'TestMigratePostgreSQLIntegration|TestDryAndFinalPlacementAdmissionPostgreSQLIntegration' \
  -count=1
PASS
```

## 5. Remaining P1-C04 work

- guest-coordinated live detach/hot-unplug qualification
- full distributed Worker/Gateway/Agent attach/detach Result-loss campaign
- Host loss時の compute/storage/attachment fencing proof
- capacity release、quarantine/reuse、typed LV delete/absence verification
- disk-full、libvirt/LVM timeout、corrupt journal、thin pool pressure
- Volume/Attachment public API、Quota、Operation/Job/Outbox atomic commit
