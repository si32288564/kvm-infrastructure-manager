# ADR-0003: 初期技術スタック

- 状態: Accepted
- 日付: 2026-08-08

## Context

Control Plane と Host Agent は、長期保守、静的配布、並行処理、API 契約、障害解析を重視します。また、100 Host、5,000 VM の初期目標を満たしながら、小規模環境にも導入できる必要があります。

## Decision

Phase 0 baseline の初期採用 stack を以下とします。

| 領域 | 選択 |
|---|---|
| Control Plane | Go |
| KIM Host Agent | Go（primary implementation language） |
| Public API | REST、JSON、OpenAPI 3.1 |
| Database | PostgreSQL |
| Internal durable messaging | NATS JetStream |
| Hypervisor | QEMU/KVM、libvirt |
| Virtual network | OVN、Open vSwitch |
| Local storage | LVM |
| Shared storage | Ceph RBD |
| Identity | OIDC |
| Metrics / tracing | Prometheus、OpenTelemetry |

以下は Decision の前提条件ではなく、Phase 1 の support/readiness validation として小規模 prototype で測定します。

- Message redelivery と consumer recovery
- 100 Agent の heartbeat と inventory update
- libvirt event stream の再接続と full resync
- OVN transaction の競合・再試行
- PostgreSQL failover 後の Operation 継続
- offline bundle のサイズと更新手順

KIM Host Agent は long-lived daemon、outbound mTLS session、inventory/observation loop、durable journal、Command/Lease/deadline processing、structured concurrency、single-binary packaging を Go で実装します。libvirt 等の native API が必要な箇所は minimal cgo または narrow wrapper に限定します。低レイヤ処理で不可欠な場合だけ小さな native helper を分離でき、Agent 全体を C++ で実装しません。

## Consequences

- Go により Control Plane と Agent の共通型、静的 binary、並行処理モデルを共有できます。
- cgo/native helper の境界には独立した build、ABI、security、fault/conformance test が必要です。
- PostgreSQL を一貫性が必要な metadata と allocation の System of Record とします。
- NATS JetStream の運用、version compatibility、quorum 設計が製品サポート範囲に加わります。
- NATS JetStreamはControl Plane内部用途とし、Agent transportはAgent Gatewayで分離する標準案です。
- OVN と Ceph は強力ですが、製品全体のサポートマトリクスと障害解析範囲を広げます。

## Alternatives to validate

- Workflow engine を内製せず Temporal などを使用する案
- Message Bus を RabbitMQ または PostgreSQL queue で代替する案
- Kubernetes を Control Plane の必須基盤とする案
- Developer Preview では overlay を外し VLAN のみに限定する案
