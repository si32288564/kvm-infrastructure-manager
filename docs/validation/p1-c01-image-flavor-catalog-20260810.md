# P1-C01 Image / Flavor Catalog Validation

- 実施日: 2026-08-10
- 対象: P1-C01 Image metadata/checksum と Flavor resource
- 結果: PASS（catalog authority foundation）

## 1. Scope

今回の増分は backend side effect を持たない PostgreSQL authority foundation に限定しました。

```text
Image metadata / integrity evidence
  -> immutable Image revision
  -> verified current Image authority

Flavor request
  -> immutable canonical Flavor revision
  -> current Flavor authority
  -> lossless Placement shape
```

Image binary download/cache、signature provider integration、public API、Placement Final Admission、libvirt define は今回の scope 外です。

## 2. Authority Model

- `image_revision_evidence` は Image format、size、declared/observed SHA-256、signature state/digest、source、visibility、metadata を immutable revision として保持する。
- checksum mismatch または signature failure は append-only `REJECTED` evidence として保持するが、`images_current` の `ACTIVE` authority を進めない。
- `flavor_revision_evidence` は vCPU、memory、root disk、NUMA、HugePages、CPU allocation/pinning、extra specs を canonical digest に固定する。
- `images_current` と `flavors_current` は mutable current authority とし、immutable revision から分離する。
- catalog mutation は `database_authority.mode = ACTIVE` の transaction だけで許可し、`RECOVERY_READ_ONLY` では拒否する。

## 3. Validation

fresh PostgreSQL 17 に migration 001〜010 を適用し、次を確認しました。

1. verified QCOW2 metadata が immutable revision と current Image authority へ commit される。
2. 同じ Image の新 revision で observed checksum を変更すると `REJECTED` evidence は残るが、current revision は旧 verified revision のまま維持される。
3. immutable Image evidence の `UPDATE` は trigger で拒否される。
4. rejected Image revision を DB から current authority へ直接設定しても trigger で拒否される。
5. NUMA node count、1 GiB HugePages、dedicated CPU、CPU pinning、extra specs を含む Flavor が current Placement shape へ欠落なく復元される。
6. map insertion order が異なっても canonical Flavor digest は一致する。
7. 同一 Flavor ID/revision を異なる shape または owner で再利用すると conflict として拒否される。
8. `RECOVERY_READ_ONLY` では新しい Flavor revision が commit されない。

実行結果:

```text
go test ./internal/persistence/postgres \
  -run 'TestCatalogPostgreSQLIntegration|TestNormalizeFlavorPreservesPlacementRequirements' \
  -count=1

PASS
```

## 4. Contracts

- Requirements: IMG-001、IMG-002、IMG-003、FLV-001、FLV-002
- Invariants: INV-IMG-001、INV-FLV-001、INV-FLV-002、INV-PLC-003、INV-PLC-004
- Acceptance: AT-IMG-001、AT-IMG-002、AT-FLV-001
- Fault boundary: FI-DATA-015

## 5. Remaining Work

- Image binary cache と Host cache integrity evidence
- production signature/artifact trust provider
- Project access/visibility authorization API
- Resource API idempotency と Operation binding
- Flavor shape を使用する dry eligibility と transactional Final Admission
