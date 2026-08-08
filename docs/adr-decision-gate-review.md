# ADR-0001〜0023 Decision Gate Review

- 状態: Complete
- Review 日: 2026-08-09
- 比較基準: Phase 0 candidate baseline at `8c1f9a9` と本 change set の整合修正
- 結果: Accepted 23、Rejected 0、Superseded 0

## 1. Review Method

各 ADR の Context、Decision、Consequences を、Requirements、統合 Architecture、Architecture Invariants、Acceptance/Fault/Extension Conformance Test Contract と個別に照合しました。タイトルや方向性だけによる一括承認は行っていません。

次を rejection または修正条件として確認しました。

- authority owner の重複または迂回
- `generation`、Lease、`UNKNOWN`、Observation の意味変更
- Final Admission transaction 内の backend side effect
- Recovery による Placement/Storage/Network/Dataplane constraint の迂回
- reconnect、upgrade、restore、certificate rotation による暗黙 rearming
- Extension または OS adapter による Core invariant の迂回
- 標準 KVM neutrality または Host OS portability への違反
- Test Contract で検証できない normative Decision

## 2. Decision Results

| ADR | Decision Gate Result | Cross-domain consistency |
|---|---|---|
| [ADR-0001](adr/0001-control-plane-host-agent.md) | Accepted | outbound mTLS、local libvirt、closed Command、journal は Host/Agent/Data contract と一致 |
| [ADR-0002](adr/0002-operation-and-reconciliation.md) | Accepted | asynchronous Operation、at-least-once、reconciliation、`UNKNOWN` は API/Execution contract と一致 |
| [ADR-0003](adr/0003-initial-technology-stack.md) | Accepted after correction | 「初期候補」と prototype 前提を除去し、採用 stack と Phase 1 validation を分離。Agent の primary Go 方針を固定 |
| [ADR-0004](adr/0004-host-os-portability.md) | Accepted after correction | OS Adapter/typed remediation に標準 KVM 非特殊化、metadata 非 lock-in、KIM Host Agent 正式名称を追加 |
| [ADR-0005](adr/0005-external-identity-ownership.md) | Accepted | external Identity authority と KIM Tenant/Project authorization ownership が一意 |
| [ADR-0006](adr/0006-placement-admission.md) | Accepted | dry evaluation と PostgreSQL transactional Final Admission が全 resource domain で一貫 |
| [ADR-0007](adr/0007-execution-model.md) | Accepted | Job/Command/Lease/Attempt、stale Result、verification が Execution invariant と一致 |
| [ADR-0008](adr/0008-agent-gateway-boundary.md) | Accepted | Agent Gateway と内部 Message Bus の Trust Boundary が Security/PKI/Time と一致 |
| [ADR-0009](adr/0009-database-ha-dr.md) | Accepted | Site HA RPO 0 と DR RPO/RTO、restore reconciliation が分離済み |
| [ADR-0010](adr/0010-system-wide-failure-semantics.md) | Accepted | timeout/expiry 非証明、append-only `UNKNOWN`、typed verification が全 domain と一致 |
| [ADR-0011](adr/0011-extension-contract-boundary.md) | Accepted | C0〜C3 と Core DB/Bus/Auth/Audit/Lease 迂回禁止が Conformance Contract と一致 |
| [ADR-0012](adr/0012-nfv-dataplane-resource-model.md) | Accepted | PMD/DPDK memory/Port/RxQ/Binding の first-class claim と Final Admission 統合が一致 |
| [ADR-0013](adr/0013-host-lifecycle-baseline-compliance.md) | Accepted | Enrollment、Baseline、Evaluator、Compliance、arming の authority chain が一致 |
| [ADR-0014](adr/0014-first-class-host-groups.md) | Accepted | typed HostGroup、materialized membership、snapshot、generation が一致 |
| [ADR-0015](adr/0015-availability-responsibility.md) | Accepted | responsibility、immutable VM Binding、Recovery safety gate が一致 |
| [ADR-0016](adr/0016-workload-resilience-intent.md) | Accepted | opaque NF role、hard domain constraint、transactional Domain Claim が一致 |
| [ADR-0017](adr/0017-bounded-recovery-storm-control.md) | Accepted | durable queue/budget/campaign、canonical lock、non-authority budget semantics が一致 |
| [ADR-0018](adr/0018-classified-persistence-and-safe-evolution.md) | Accepted | data classification、Outbox/Inbox、GC、migration、PITR fencing が一致 |
| [ADR-0019](adr/0019-storage-attachment-authority-and-fencing.md) | Accepted | logical Attachment Claim、physical fencing、single-writer、handoff が一致 |
| [ADR-0020](adr/0020-kim-network-intent-and-layered-realization.md) | Accepted | PostgreSQL network authority、OVN layered realization、Port Binding が一致 |
| [ADR-0021](adr/0021-manifest-driven-compatible-upgrades.md) | Accepted | Manifest graph、N/N-1 edge、Feature Gate、rollback/finalization が一致 |
| [ADR-0022](adr/0022-explicit-distributed-time-semantics.md) | Accepted | DB/monotonic/wall/source time、expiry semantics、clock fail-closed が一致 |
| [ADR-0023](adr/0023-separated-trust-domains-and-generation-fenced-credentials.md) | Accepted | Trust Domain、credential/application authority、revocation/rollover/DR fencing が一致 |

## 3. Corrections Made Before Acceptance

### ADR-0003

- Technology selection を「初期候補」から Phase 0 baseline の採用判断へ変更しました。
- prototype を ADR acceptance の前提から外し、Phase 1 support/readiness validation へ移しました。
- KIM Host Agent の primary implementation language を Go としました。
- cgo/native API は minimal narrow wrapper、native helper は不可欠な低レイヤ処理だけに限定し、Agent 全体の C++ 化を禁止しました。

### ADR-0004

- KIM Host Agent を正式名称にしました。
- Linux KVM、QEMU、libvirt の patch/fork/proprietary modification を core requirement にしない原則を追加しました。
- KIM metadata が標準 interface からの manageability を失わせないことを追加しました。
- KIM が hypervisor distribution または専用 KVM/QEMU/libvirt build の提供主体にならないことを追加しました。

## 4. Decision

修正後の ADR-0001〜0023 は candidate baseline と整合し、各 Decision と Consequences は実装・運用・試験上の責務を正しく表現しています。23件を個別照合済みの `Accepted` とします。

Rejected/Superseded はありません。将来 Decision を弱める、または異なる authority model へ変更する場合は、新しい ADR により明示的に supersede します。
