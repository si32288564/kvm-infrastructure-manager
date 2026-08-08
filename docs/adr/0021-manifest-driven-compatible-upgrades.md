# ADR-0021: Manifest駆動の互換性gateで製品upgradeを行う

- 状態: Proposed
- 日付: 2026-08-09

## Context

KIMはControl Plane、PostgreSQL schema、Agent/Gateway protocol、typed Command、Event、extension、Host/backend support matrixを同時に進化させます。binaryのversion順序やdeployment成功だけをupgrade authorityにすると、mixed-version中に異なるauthority semanticsを書き込み、old Agentへ未知Commandを配送し、rollback不能なschema contract後に旧binaryへ戻す事故が起こり得ます。

## Decision

- immutable Release Manifestへartifact digest、provenance/SBOM、component dependency、schema/API/protocol/event/extension range、support matrix、migration、rollback boundaryを固定する。
- compatibilityをversion文字列から推測せず、Manifest graphとcurrent observed artifact/capabilityからimmutable Compatibility Decisionとして評価する。
- Upgrade Campaign、Plan、Wave、Target、Feature Gate、Rollback BoundaryをPostgreSQLへ永続化する。
- mixed-versionは明示edgeを持つN/N-1だけを許可し、全writer/consumerが理解できるsemanticsへwriteを制限する。
- schema evolutionの正本はData and Persistence Architectureとし、Upgradeは製品全体のphase/gateをcoordinationする。
- Agent/GatewayはprotocolとCommand/Result schemaを明示negotiationし、down-convertやsilent fallbackを行わない。
- canary/batch、availability budget、failure threshold、current compatibility再検証を全waveに要求する。
- destructive schema contract、old decoder/artifact GCを別のfinalization approvalとし、それ以前/以後のrollback可否を明示する。
- failed/UNKNOWN upgradeをblind retry、PITR、backend cleanup、既存workload mutationで自動rollbackしない。
- offline bundleにもonline releaseと同一のManifest、artifact verification、SBOM、migration、compatibility gateを要求する。
- QEMU/libvirt upgradeやdefault変更だけで既存VMのmachine type/CPU model/device ABI bindingを変更しない。
- Event/evidence decoderをpayload Retention Policy/holdへbindし、参照中にfinalize/GCしない。
- Feature Gate dependencyをacyclic graphとして検証し、dependency orderでactivation/rollbackする。

## Consequences

- release tooling、Manifest registry、compatibility evaluator、durable Upgrade Campaign controller、mixed-version CIが必要になります。
- target binaryを配布済みでもFeature Gateが開くまで新semanticsを利用できない場合があります。
- rollback window中は旧artifact、schema、decoderを保持するため運用容量が増えます。
- Control Plane、Agent、extension、backend/Host compatibilityを一つの監査可能なrelease decisionへ統合できます。
- initial N/N-1期間、component順序、availability budget、rollback retention、support matrix profileを別途決定する必要があります。
