# 設計文書の正本と変更規則

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

Requirements、Architecture、ADRを二重の正本にしません。それぞれのauthorityと更新規則を定義します。

## 2. 文書の役割

| 文書 | Authority |
|---|---|
| Product Vision / Responsibility Boundaries | 製品scope、所有責任、非責任 |
| Requirements | 検証可能な製品behaviorと非機能要件 |
| Accepted ADR | 重要な設計判断、その理由、却下案、結果 |
| Architecture文書 | Accepted ADRとRequirementsを統合した現在の構造、contract、failure model |
| Open Questions / Proposed ADR | 未確定事項。実装authorityではない |
| Release Plan | exit criteriaと品質gate |
| Release Manifest / Compatibility Matrix | 出荷artifact、upgrade path、contract range、support/rollback boundaryのrelease正本 |
| Architecture Invariants / Traceability | 実装禁止条件とRequirement-to-Test coverage |
| Fault Injection / Conformance Contract | 検証可能なfailure/extension test authority |

## 3. 正本規則

- 重要な設計判断の正本はAccepted ADRです。
- Architectureは現在採用されている全体像の正本ですが、判断理由を複製せずADRを参照します。
- Requirementsは外部から検証可能な契約の正本です。
- Proposed ADRを根拠にirreversibleな実装を確定しません。
- Requirements、Accepted ADR、Architectureが矛盾した場合は文書不備として実装を停止し、ADRまたはRequirementの明示変更で解消します。暗黙の優先順位で読み替えません。

## 4. 変更規則

重要判断を変更するpull requestは、同じchange setで以下を更新します。

1. 新規ADRまたは既存ADRのSuperseded状態
2. 関連Requirements
3. 統合Architectureと図
4. Threat model / failure scenario
5. Open QuestionsとRelease gate
6. Contract/acceptance testへのtraceability
7. Upgrade path、mixed-version、rollback boundary、support matrixへの影響
7. Architecture InvariantとFault/Conformance test ID

ADR本文に詳細なAPI schemaや運用手順を複製せず、Architectureまたは専用contract文書へリンクします。

## 5. Phase 0 Decision Gate

以下はAccepted ADRなしに実装を確定しません。

- Identity authority
- Host mutation / Configuration Management境界
- Placement final admission
- Operation / Execution model
- Agent transport / Trust Boundary
- PostgreSQL HA / DR authority
- Network / WIM boundary
- System-wide failure semantics
- Extension contractとCore invariant境界
- NFV dataplane resource/admission/disruption boundary
- Host enrollment、Baseline/Compliance authority、remediation/placement/decommission boundary
- HostGroup membership、Placement Scope、Failure Domain、rollout/maintenance snapshot boundary
- Availability responsibility、Host failure fencing、managed workload recovery boundary
- Workload resilience intent/Domain ClaimとRecovery Budget/Queue authority boundary
- persistent data classification、retention/GC、schema evolution、Outbox/Inbox、PITR restore authority boundary
- Storage Backend/Class、Volume Attachment Claim/Generation、single-writer、compute/storage fencing boundary
- Network/IPAM/Segment/Port Binding、OVN layered realization、Gateway/NAT/Security authority boundary
