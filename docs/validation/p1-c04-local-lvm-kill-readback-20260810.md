# P1-C04 Local LVM Kill / Read-back Validation

- 日付: 2026-08-10
- 対象: closed typed Local LVM create、Agent process kill、標準 LVM read-back、immutable Binding evidence
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- 状態: PASS / P1-C04 In Progress

## 1. Validated authority path

```text
Final Admission Binding Intent
  -> LOCAL_LVM_VOLUME_ENSURE/v1
  -> write-before-execute journal
  -> standard lvcreate
  -> standard lvs read-back
  -> Result construction
  -> Agent subprocess SIGKILL
  -> journal reopen / new Agent process
  -> typed Verification
  -> same Host + VG UUID + LV UUID + size MATCHED
  -> immutable Binding Evidence
  -> current Backend Binding = BOUND
```

Command caller は VG name、LV name、device path、binary、argv、LVM flag を指定できません。Agent 管理設定が allowed VG UUID を VG name へ対応付け、LV key は `kim-` + `SHA-256(volume_id)[0:16]` から KIM が導出します。

## 2. Real Host isolation

既存 `vg0` は使用・変更していません。qualification 専用に 512 MiB の loop-backed VG `kimq_c04_20260810` を作成し、テスト終了後に専用 VG、PV、loop device、image、test binary を削除しました。

Observed evidence:

```text
VG name:  kimq_c04_20260810
VG UUID:  GCQCc5-kEa1-961Y-VFRY-xclL-Oxvq-KyZq4Y
LV name:  kim-c214ea2fde00225c4c87250bd0fd5444
LV UUID:  LTel3S-RRZ9-pTg8-6uNm-dGpl-nr0z-ZUOgGa
LV size:  67108864 bytes
```

Cleanup verification: `cleanup-pass`。

## 3. Fault assertions

1. LV create 前に journal `Prepare` を fsync / atomic rename する。
2. LV create と read-back 後、Result transport 前に Agent subprocess を `SIGKILL` する。
3. process kill は LV create 未実行の証明にせず、LV は Host 上に存続する。
4. new process は caller input や予約名から LV UUID を推測せず、`lvs` から再取得する。
5. original Command ID / Attempt / payload digest / target と journal evidence が一致する Verification だけを実行する。
6. read-back は同じ VG UUID、KIM-owned LV key、64 MiB size、non-empty LV UUID の場合だけ `MATCHED` にする。
7. absent は `NOT_APPLIED`、size/identity mismatch は `CONFLICTING`、tool/permission error は `UNKNOWN` とし、absence へ丸めない。
8. PostgreSQL は既存 `command_verification_evidence` の Command / Attempt / generation / digest / verifier / typed payload と一致する `MATCHED` evidence だけを immutable Binding row として保存し、current Binding を `BOUND` にする。
9. 同じ evidence identity / digest は冪等、同じ identity / different digest は conflict とする。同じ Binding generation を別 LV UUID へ切り替えない。
10. immutable evidence の `UPDATE` は DB trigger が拒否する。

## 4. Validation results

```text
go test ./internal/agent/execution/locallvm
PASS

KIM_LOCAL_LVM_VG_UUID=<sandbox-vg-uuid>
KIM_LOCAL_LVM_VG_NAME=kimq_c04_20260810
/var/tmp/kim-locallvm-qualification.test \
  -test.run=^TestLocalLVMProcessKillUnknownReadBack$ -test.v
PASS (0.21s)

KIM_POSTGRES_TEST_URL=postgres://.../kimtest?sslmode=disable
go test -race ./internal/persistence/postgres \
  -run 'TestMigratePostgreSQLIntegration|TestDryAndFinalPlacementAdmissionPostgreSQLIntegration' \
  -count=1
PASS
```

## 5. Remaining P1-C04 work

- typed libvirt disk attach/detach と device/holder observation
- create Result loss を含む full distributed Worker/Gateway/Agent campaign
- verified detach/fencing、capacity release、quarantine/reuse workflow
- disk-full、LVM command timeout、corrupt journal、thin pool metadata pressure
- READ_ONLY_MANY、encryption / Secret Provider、Ceph profile
- Volume/Attachment public API、Quota、Operation/Job/Outbox atomic commit
