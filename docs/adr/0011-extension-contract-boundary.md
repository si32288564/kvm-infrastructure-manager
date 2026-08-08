# ADR-0011: ExtensionはCore authorityを迂回しない

- 状態: Proposed
- 日付: 2026-08-09

## Context

OS、Network、Storage、Identity、Northbound integrationを増やすにはextension pointsが必要です。一方、自由なpluginがDB、Message Bus、credential、shell、backendへ直接アクセスすると、KIMのauthority、failure semantics、audit、security boundaryが崩れます。

## Decision

- extension pointsを列挙し、versioned typed contractだけで拡張する。
- extensionはCore DB、内部Message Bus、authorization、audit、Lease authorityへ直接アクセスしない。
- Agent moduleは静的登録されたclosed Commandとnarrow backend interfaceを使用する。
- capabilityはversion、constraints、generation、health、support tierを持つ。
- side effectを持つControl Plane adapterはout-of-process isolationを優先する。
- conformance testとrelease certificationをValidated supportの条件とする。
- extensionをC0 Core Built-in、C1 Certified Restricted Module、C2 Isolated Adapter Service、C3 Untrusted External Integrationに分類する。
- Identity/Secret連携はC2隔離、Placement Ruleはpure C1を基本とし、同一plugin interfaceへ統合しない。

## Consequences

- arbitrary pluginより追加速度は遅くなりますが、Core invariantsを維持できます。
- contract versioning、test kit、drain/upgrade lifecycleが必要になります。
- third-party SDKの公開範囲は別途決定が必要です。
