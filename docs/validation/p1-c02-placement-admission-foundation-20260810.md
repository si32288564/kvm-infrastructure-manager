# P1-C02 Placement / Final Admission Foundation Validation

- 実施日: 2026-08-10
- 対象: dry Eligibility、Scoring、Selection、transactional Final Admission foundation
- 結果: PASS（Compute/Memory/HugePages/qualified PCI VF scope）

## 1. Implemented Path

```text
current Image revision
+ immutable Flavor shape
+ current Host capability snapshot
+ READY / Compliance generations
+ current Placement Pool membership/policy
+ existing compute claims
        ↓
side-effect-free dry Eligibility
        ↓ eligible candidates only
deterministic Scoring / Selection
        ↓
same-rule transactional Final Admission
        ├─ immutable Admission Decision
        ├─ Compute/Memory/HugePages Reservation
        └─ qualified PCI VF Claim
```

Final Admission transaction では libvirt、Agent、JetStream、Network、Storage backend を呼び出しません。

## 2. Authority and Failure Contracts

- dry evaluation は read-only repeatable-read transaction と pure evaluator だけを使用し、Decision/Claim/Outbox を書き込まない。
- readiness capability、Host capability、Pool、Pool policy、membership、Image、Flavor generation/digest を evaluation fingerprint に含める。
- ineligible Host は capacity が大きくても scoring/selection へ入れない。
- Final Admission は catalog/pool/Host authority を lock し、dry と同じ rule を current state へ再適用する。
- typed PCI requirement は device observation、PF/VF relationship、Qualification Binding、validated `VF_ASSIGN` operation、policy generation/profile、NUMA/IOMMU constraint、active claim 不在を dry/final の両方で評価する。
- PCI requirement と digest を immutable Admission Decision に固定し、同じ request identity を異なる VF/policy requirement で再利用できない。
- dry 後に membership generation が変化した場合は `STALE` として transaction 全体を rollback する。
- concurrent request が同じ残 capacity を要求した場合は Host-scoped authority lock により一方だけを commit し、他方を current capacity で再評価して拒否する。
- stable request identity の replay は新しい Decision/Claim を作らず、original admission を返す。
- `PRIVATE` Image と Flavor owner が Project scope に一致しない request は fail closed とする。`SHARED` access は ACL authority 実装まで自動許可しない。

## 3. Validation

fresh PostgreSQL 17 に migration 001〜011 を適用し、次を確認しました。

1. dry evaluation 前後で Admission Decision と Allocation Claim の件数が変化しない。
2. NUMA、1 GiB HugePages、dedicated/pinned CPU を Flavor から required claim へ伝播する。
3. blocking readiness の大容量 Host を selection から除外する。
4. membership generation 変更後の stale evaluation が全 rollback し、部分 claim を残さない。
5. Compute capacity は二件分存在するが同じ VF を要求する二つの並行 Final Admission は一件だけ commit し、他方の Compute/HugePages claim も rollback する。
6. accepted request replay は同一 Admission/Allocation ID へ収束する。
7. immutable Decision、Compute/Memory/HugePages Claim、qualified VF Claim が同じ PostgreSQL transaction で commit される。
8. request identity を異なる PCI policy generation で replay すると conflict として拒否する。

```text
go test -race ./internal/placement ./internal/persistence/postgres \
  -run 'TestEvaluate|TestDryAndFinalPlacementAdmissionPostgreSQLIntegration' \
  -count=1

PASS
```

## 4. Traceability

- Requirements: SCH-001〜007、FLV-001、FLV-002、DPL-016〜020
- Invariants: INV-PLC-001〜006、INV-FLV-001、INV-DPL-011〜014
- Acceptance: AT-PLC-001〜006、AT-PLC-008、AT-PLC-009、AT-FLV-001、AT-DPL-014〜017

## 5. Remaining P1-C02 Scope

- Network identity/Port、Storage capacity/Attachment、Quota claim の同一 Final Admission transaction 統合
- Availability Binding と Resilience Domain Claim
- idempotent Resource API、Operation、Desired State、Job/Command intent の atomic commit
- multi-Host re-selection loop と persisted explanation/rank history
- NUMA node-local CPU/Memory/HugePage claim ledger の詳細化
