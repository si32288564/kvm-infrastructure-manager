# P1-C05 Image Materialization Validation

- 日付: 2026-08-10
- 状態: PASS / P1-C05 In Progress
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`

## 1. Authority path

```text
verified RAW Image revision
  + current VM / immutable Plan
  + current BOUND root Volume Binding
  + RESERVED single-writer Attachment Claim
  -> closed typed LOCAL_IMAGE_TO_LVM_ENSURE
  -> admin-configured digest-addressed cache
  -> identity-verified Local LVM target
  -> bounded copy + fsync
  -> target content SHA-256 read-back
  -> immutable VM Image Realization Evidence
  -> Image = REALIZED
  -> Network = PENDING
  -> Boot Readiness = BLOCKED
```

Command payload は Image ID/revision/checksum/size、VM/Volume/VG/LV identity、KIM-derived backend resource key のみを保持します。URI、source/target path、argv、flag は受け付けません。Agent は admin-configured cache root の `sha256/<digest>` を `os.OpenRoot` で開き、root Volume は standard `lvs` observation から解決します。

Developer Preview の Local LVM direct-copy profile は RAW revision のみに限定します。現在の libvirt root block device は `raw` driver で定義されるため、QCOW2 file bytes を LV へ copy して `REALIZED` とすることは禁止します。QCOW2 は typed conversion と virtual content identity verification を別途実装・認証するまで fail closed です。

## 2. Persistence

- migration `019_vm_image_materialization`
- `vm_image_realization_evidence`: immutable destination read-back evidence
- `vm_materialization_readiness_current.image_observation_generation`: current Image evidence fencing
- Image observation acceptance は current VM/Plan/Image revision/Binding generation/Attachment Claim/typed Command/MATCHED Verification を同一 PostgreSQL transaction で再検証
- `REALIZED` は target LV の先頭 `image_size_bytes` の digest を意味し、trailing Volume capacity 全体の digest とはみなしません

## 3. PostgreSQL validation

fresh PostgreSQL 17 で migration 001〜019 と Placement/VM materialization integration を実行しました。

```text
TestMigratePostgreSQLIntegration
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration
PASS
```

確認事項:

- same request/evidence replay は冪等
- same evidence identity/different digest は拒否
- immutable Image realization evidence の UPDATE は拒否
- Command payload に URI/path/argv/flag が存在しない
- Image `REALIZED` 後も Network `PENDING`、Boot `BLOCKED`

## 4. Real Host qualification

既存 `vg0` とは別に disposable loop device、VG `kimq_img_20260810`、64 MiB LV を作成し、8 MiB の deterministic RAW artifact を materialize しました。

```text
TestRealLocalLVMImageMaterialization: PASS
image_sha256=47616c7755ec9a121ebfa0b181474b5647d0815815a2ecf817006a4d03ef58bf
vg_uuid=YRQwzE-ZesV-3owD-uGV7-ALWU-Az50-deB4UV
lv_uuid=JXxfG2-8rl0-0aD2-3gEV-EpZv-LiXw-druEEO
cleanup-pass
```

target LV read-back digest、VG/LV UUID、holder absence が一致しました。qualification 後に専用 LV/VG/PV/loop/cache/binary を削除し、既存 `vg0` UUID `nV0vku-f6vA-yC9P-BZXl-uNRq-s1AF-DQDFWs` が不変であることを確認しました。

## 5. Remaining work

- Image retrieval provider、signature provider、cache distribution/eviction
- QCOW2 typed conversion と virtual content identity verification
- disk-full、fsync latency、corrupt/short artifact fault qualification
- OVS/SR-IOV Network realization evidence
- all-component READY transition と typed power-on gate
