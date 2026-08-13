# Architecture Decision Records

重要な技術判断を、背景、選択肢、結果とともに保存します。

## 状態

- Proposed: 議論中
- Accepted: 採用済み
- Rejected: 不採用
- Superseded: 後続 ADR に置換済み

## 命名

`NNNN-short-title.md`

## 一覧

Phase 0 Decision Gate review の結果、2026-08-09 に ADR-0001〜0023 を個別照合済みの `Accepted` としました。詳細は [ADR Decision Gate Review](../adr-decision-gate-review.md) を参照します。Phase 1 の implementation decision は ADR-0024 以降へ記録します。

- [ADR-0001: Control Plane と Host Agent を分離する](0001-control-plane-host-agent.md)
- [ADR-0002: 非同期 Operation と reconciliation を採用する](0002-operation-and-reconciliation.md)
- [ADR-0003: 初期技術スタック](0003-initial-technology-stack.md)
- [ADR-0004: ハイパーバイザー OS の差異をホスト側で吸収する](0004-host-os-portability.md)
- [ADR-0005: Identityは外部Platformが所有する](0005-external-identity-ownership.md)
- [ADR-0006: Placementはdry evaluationとtransactional final admissionを分離する](0006-placement-admission.md)
- [ADR-0007: OperationとExecutionを分離する](0007-execution-model.md)
- [ADR-0008: Agentを内部Message Busから分離する](0008-agent-gateway-boundary.md)
- [ADR-0009: HAとDRのRPOを分離する](0009-database-ha-dr.md)
- [ADR-0010: 不確実な障害状態を推測で確定しない](0010-system-wide-failure-semantics.md)
- [ADR-0011: ExtensionはCore authorityを迂回しない](0011-extension-contract-boundary.md)
- [ADR-0012: OVS-DPDK資源を第一級Placement Resourceとして扱う](0012-nfv-dataplane-resource-model.md)
- [ADR-0013: ZTPとContinuous ComplianceをHost Lifecycleへ統合する](0013-host-lifecycle-baseline-compliance.md)
- [ADR-0014: Host Groupを第一級resourceとして扱う](0014-first-class-host-groups.md)
- [ADR-0015: Availability責任をPlacement Pool Policyとして固定する](0015-availability-responsibility.md)
- [ADR-0016: NF側HAの分離意図をtransactional Failure Domain claimへ変換する](0016-workload-resilience-intent.md)
- [ADR-0017: Recovery stormをdurable budgetとqueueで制御する](0017-bounded-recovery-storm-control.md)
- [ADR-0018: 永続データをclassifyし安全なschema evolutionとrestoreを行う](0018-classified-persistence-and-safe-evolution.md)
- [ADR-0019: Volume Attachment authorityと実世界fencingを分離する](0019-storage-attachment-authority-and-fencing.md)
- [ADR-0020: KIM network intentとOVN/dataplane realizationを分離する](0020-kim-network-intent-and-layered-realization.md)
- [ADR-0021: Manifest駆動の互換性gateで製品upgradeを行う](0021-manifest-driven-compatible-upgrades.md)
- [ADR-0022: 分散clockを区別し時間切れを未実行証明にしない](0022-explicit-distributed-time-semantics.md)
- [ADR-0023: Trust Domainを分離しcredentialをgenerationでfenceする](0023-separated-trust-domains-and-generation-fenced-credentials.md)
- [ADR-0024: Developer Preview の Agent transport に gRPC bidirectional stream を採用する](0024-initial-agent-transport-grpc.md)
- [ADR-0025: Planned Local LVM relocation は content identity を boot prerequisite にする](0025-planned-local-lvm-relocation-content-authority.md)
- [ADR-0026: Local LVM relocation transport は相互認証した exact block capability とする](0026-cross-host-local-lvm-transport-authority.md)
- [ADR-0027: Generic CleanupからLocal LVM source capacityを安全にreclaimする](0027-generic-local-lvm-source-cleanup-authority.md)
- [ADR-0028: Local LVM transportを通常Host Agent sessionへbindする](0028-local-lvm-transport-agent-runtime.md)
- [ADR-0029: Northbound resource mutation の完了境界を authority で分ける](0029-northbound-resource-mutation-completion-boundary.md)
