# P1-C04 Local LVM Admission Foundation Validation

- 日付: 2026-08-10
- 対象: Local LVM Backend/Class/capacity、Volume、Backend Binding Intent、single-writer Attachment Claim の transactional Final Admission 統合
- 状態: Foundation implemented / P1-C04 In Progress

## 1. Authority boundary

```text
current Host capability
+ Local LVM Backend (Host + VG UUID)
+ immutable Storage Class revision
+ immutable capacity observation / current projection
  -> side-effect-free dry Eligibility
  -> same-rule transactional Final Admission
       -> immutable Admission Decision
       -> Compute / HugePages / PCI / Network Claims
       -> Volume desired authority
       -> Storage Capacity Claim
       -> Local LVM Backend Binding Intent
       -> Volume Attachment desired authority
       -> SINGLE_WRITER Attachment Claim
```

Final Admission は LVM command、device path、libvirt、Agent、backend cleanup を実行しません。`RESERVED` Binding Intent は `Host + VG UUID + KIM-owned backend resource key` を固定しますが、まだ存在しない LV UUID を生成済み evidence として記録しません。実 `LV UUID` は typed create/read-back 後にだけ確定します。

## 2. Implemented contracts

- Backend type は initial profile の `LOCAL_LVM` に閉じ、Host identity と VG UUID を locality authority にする。
- Storage Class revision は immutable evidence とし、`HOST_LOCAL` / `SINGLE_WRITER` / fencing policy revision を固定する。
- `EXPERIMENTAL` backend、thin provisioning、encryption-required class は initial allocator で fail closed にする。
- KIM reserved/allocated capacity ledger と backend observed free/external-or-unknown usage を分離する。
- current Host capability/backend/class/capacity generation と `HEALTHY` state を dry/final の両方で再検証する。
- canonical Storage requirements/digest を immutable Admission Decision に保存し、同じ request identity の semantic drift を conflict にする。
- active `SINGLE_WRITER` Claim は Volume ごとの PostgreSQL partial unique index で最大一つにする。
- `UNKNOWN` / `FENCE_REQUIRED` / `RELEASE_PENDING` Attachment Claim は新しい writer から利用可能とみなさない。
- Capacity/Volume/Binding/Attachment のいずれかが失敗した場合、Compute/PCI/Networkを含む Final Admission transaction 全体を rollback する。

## 3. Concurrency and fault assertions

1. dry evaluation は Storage/Placement authority row を追加・更新しない。
2. Local LVM Backend の Host/VG UUID、capability generation、Class revision、capacity generation が stale/不一致なら ineligible にする。
3. distinct Network/Volume identity を持つ二つの 12 GiB request が、20 GiB の current remaining capacityを並行要求した場合、一件だけ commit する。
4. capacity conflict の敗者は Compute / HugePages / Port / IP / MAC / Volume / Capacity / Binding / Attachment Claim を残さない。
5. accepted request replay は元 Admission へ収束し、Storage capacity generation 等の requirement 変更は conflict になる。
6. committed Binding Intent は `observed_lv_uuid IS NULL` のままで、reservation を LV realization evidence へ誤昇格しない。
7. 同じ Volume へ二つ目の active `SINGLE_WRITER` Attachment Claim を直接競合させても、PostgreSQL constraint が拒否し、同一 transaction の追加 Attachment も rollback する。

## 4. Validation commands

```bash
KIM_POSTGRES_TEST_URL=postgres://postgres:kimtest@127.0.0.1:55444/kimtest?sslmode=disable \
go test -race ./internal/placement ./internal/persistence/postgres \
  -run 'TestEvaluate|TestDryAndFinalPlacementAdmissionPostgreSQLIntegration|TestPCIQualificationAndFinalAdmissionPostgreSQLIntegration' \
  -count=1

make check
```

## 5. Remaining P1-C04 work

- typed Local LVM delete と create Result loss の full distributed qualification
- guest-coordinated live detach と full distributed Result-loss qualification
- disk-full/LVM response-loss/Agent kill に対する `UNKNOWN` / read-back convergence
- verified detach/fencing、capacity release、quarantine/reuse workflow
- READ_ONLY_MANY、thin pool metadata policy、encryption/Secret Provider integration
- Volume/Attachment public API、Quota、Operation/Job/Outbox atomic commit

typed create/read-back、Agent kill、immutable LV identity evidence、current `BOUND` Binding は [P1-C04 Local LVM Kill / Read-back Validation](p1-c04-local-lvm-kill-readback-20260810.md) で検証済みです。

typed libvirt attach/cold-detach、Agent kill、device/LVM holder read-back、`ATTACHED` / `DETACHED` Claim transition は [P1-C04 Libvirt Attachment Kill / Read-back Validation](p1-c04-libvirt-attachment-kill-readback-20260810.md) で検証済みです。
