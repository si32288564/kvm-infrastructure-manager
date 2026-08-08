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
