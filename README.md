# KVM Infrastructure Manager

KVM Infrastructure Manager（以下 KIM）は、QEMU/KVM を実行基盤とし、ETSI NFV における Virtualised Infrastructure Manager（VIM）相当の機能を提供するインフラストラクチャ管理製品です。

## プロダクトの目的

通信・エッジおよびオンプレミス環境において、複数の KVM ホストにまたがる計算、ネットワーク、ストレージ資源を一貫した API と運用モデルで管理します。NFVO/VNFM などの外部オーケストレーターから利用できる Northbound API と、運用者向けの管理機能を提供します。

## 初期ターゲット

- 単一 NFVI-PoP
- 最大 100 KVM ホスト
- 最大 5,000 VM
- マルチテナント
- VLAN および Geneve オーバーレイ
- KVM/libvirt を利用できる一般的な Linux ディストリビューション
- 初期 CPU アーキテクチャは x86_64
- オンライン／オフライン導入

## 設計原則

1. API を先に定義し、実装と UI は同じ公開 API を利用する。
2. すべての変更操作を冪等な非同期 Operation として扱う。
3. desired state と observed state を分離し、reconciliation により収束させる。
4. Control Plane からハイパーバイザーへ直接ログインせず、ホスト上の Agent を介して操作する。
5. テナント分離、最小権限、監査可能性を初期設計に含める。
6. バージョンアップ、バックアップ、障害解析を製品機能として扱う。
7. ハイパーバイザー OS 固有の差異はホスト側コンポーネントで吸収し、Control Plane へ持ち込まない。
8. Linux KVM、QEMU、libvirt の patch、fork、proprietary modification を要求せず、標準 interface を通じて管理する。

ホスト上で動作するコンポーネントの正式名称は「KIM Host Agent」（本文中では Host Agent または Agent）とします。

## ドキュメント

- [製品ビジョン](docs/product-vision.md)
- [要件定義](docs/requirements.md)
- [アーキテクチャ](docs/architecture.md)
- [ドメインモデル](docs/domain-model.md)
- [API 設計原則](docs/api-principles.md)
- [セキュリティ設計](docs/security.md)
- [責任境界](docs/responsibility-boundaries.md)
- [Placement Architecture](docs/placement-architecture.md)
- [Execution Architecture](docs/execution-architecture.md)
- [Agent Protocol Architecture](docs/agent-protocol.md)
- [HA / DR Architecture](docs/ha-dr.md)
- [Data and Persistence Architecture](docs/data-persistence-architecture.md)
- [Storage, Attachment, and Fencing Architecture](docs/storage-attachment-fencing-architecture.md)
- [Network Resource Architecture](docs/network-resource-architecture.md)
- [Upgrade and Compatibility Architecture](docs/upgrade-and-compatibility-architecture.md)
- [Time and Clock Semantics Architecture](docs/time-and-clock-semantics.md)
- [PKI and Trust Lifecycle Architecture](docs/pki-and-trust-lifecycle-architecture.md)
- [設計文書の正本と変更規則](docs/document-governance.md)
- [日本語ドキュメント表記規約](docs/writing-conventions.md)
- [Phase 0 Exit Review](docs/phase-0-exit-review.md)
- [ADR Decision Gate Review](docs/adr-decision-gate-review.md)
- [v1 Gap Analysis](docs/v1-gap-analysis.md)
- [Phase 1 Implementation Plan](docs/phase-1-implementation-plan.md)
- [Q-094 Agent Transport Spike](docs/spikes/q094-agent-transport.md)
- [System-wide Failure Model](docs/failure-model.md)
- [Extensibility Architecture](docs/extensibility-architecture.md)
- [Architecture Invariants](docs/architecture-invariants.md)
- [Architecture Traceability Matrix](docs/traceability-matrix.md)
- [Fault Injection Matrix](docs/fault-injection-matrix.md)
- [Extension Conformance Contract](docs/extension-conformance.md)
- [Acceptance Test Catalog](docs/acceptance-test-catalog.md)
- [NFV Dataplane Resource Architecture](docs/nfv-dataplane-resource-architecture.md)
- [Host Lifecycle and Compliance Architecture](docs/host-lifecycle-and-compliance-architecture.md)
- [Host Grouping Architecture](docs/host-grouping-architecture.md)
- [Availability Responsibility and Managed Recovery Architecture](docs/availability-responsibility-architecture.md)
- [Workload Resilience Intent Architecture](docs/workload-resilience-intent-architecture.md)
- [リリース計画](docs/release-plan.md)
- [未決事項](docs/open-questions.md)
- [Architecture Decision Records](docs/adr/README.md)

## 現在の状態

Phase 0 Architecture Baseline を完了し、Phase 1 Developer Preview の実装を開始しています。現在は repository/CI、fresh PostgreSQL authority schema、Agent transport spike を進めています。未確定の tuning 値、support matrix、実装技術は、対応する Decision Gate を通過するまで製品保証として扱いません。

## 開発時の基本コマンド

```sh
make check
make build
```

PostgreSQL migration は `KIM_DATABASE_URL` を設定して実行します。

```sh
go run ./cmd/kim-db-migrate
```

専用の空 PostgreSQL database に対する migration integration test は次で実行します。

```sh
make test-postgres-integration KIM_POSTGRES_TEST_URL='postgres://...'
```
