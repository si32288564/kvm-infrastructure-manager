# ADR-0003: 初期技術スタック

- 状態: Proposed
- 日付: 2026-08-08

## Context

Control Plane と Host Agent は、長期保守、静的配布、並行処理、API 契約、障害解析を重視します。また、100 Host、5,000 VM の初期目標を満たしながら、小規模環境にも導入できる必要があります。

## Decision

初期候補を以下とします。

| 領域 | 選択 |
|---|---|
| Control Plane / Agent | Go |
| Public API | REST、JSON、OpenAPI 3.1 |
| Database | PostgreSQL |
| Internal durable messaging | NATS JetStream |
| Hypervisor | QEMU/KVM、libvirt |
| Virtual network | OVN、Open vSwitch |
| Local storage | LVM |
| Shared storage | Ceph RBD |
| Identity | OIDC |
| Metrics / tracing | Prometheus、OpenTelemetry |

この ADR の Accepted 前に、小規模 prototype で以下を測定します。

- Message redelivery と consumer recovery
- 100 Agent の heartbeat と inventory update
- libvirt event stream の再接続と full resync
- OVN transaction の競合・再試行
- PostgreSQL failover 後の Operation 継続
- offline bundle のサイズと更新手順

## Consequences

- Go により Control Plane と Agent の共通型、静的 binary、並行処理モデルを共有できます。
- PostgreSQL を一貫性が必要な metadata と allocation の System of Record とします。
- NATS JetStream の運用、version compatibility、quorum 設計が製品サポート範囲に加わります。
- NATS JetStreamはControl Plane内部用途とし、Agent transportはAgent Gatewayで分離する標準案です。
- OVN と Ceph は強力ですが、製品全体のサポートマトリクスと障害解析範囲を広げます。

## Alternatives to validate

- Workflow engine を内製せず Temporal などを使用する案
- Message Bus を RabbitMQ または PostgreSQL queue で代替する案
- Kubernetes を Control Plane の必須基盤とする案
- Developer Preview では overlay を外し VLAN のみに限定する案
