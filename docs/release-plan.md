# リリース計画

- 状態: Draft
- 更新日: 2026-08-08

日付ではなく、検証可能な exit criteria で段階を進めます。具体的な日程はチーム体制と対象 OS の決定後に設定します。

## Phase 0: Architecture Baseline

### 成果物

- 製品境界、用語、主要ユースケース
- API resource model と OpenAPI skeleton
- Control Plane / Agent protocol の ADR
- VM create の end-to-end sequence と failure model
- Threat model
- 対応予定 OS と component version 方針
- Agent の OS Integration Adapter 契約と support tier

### Exit criteria

- Must 要件に owner と検証方法がある。
- 主要な未決事項が ADR または open question として追跡される。
- VM create/delete、Host loss、Control Plane loss の設計レビューが完了する。
- 少なくとも2系統の Linux ディストリビューションで同じ Control Plane build を用いた preflight と VM lifecycle を検証する。

## Phase 1: Developer Preview

### Scope

- 単一 Control Plane
- Host Agent 登録と inventory
- Image、Flavor、VM lifecycle
- 基本 scheduler
- VLAN network
- local storage
- Operation API、監査、基本メトリクス

### Exit criteria

- 2 Host で API から VM を繰り返し作成・削除できる。
- API 再送と Agent 再起動で重複 VM が作られない。
- Host 切断時に新規配置されず、復旧後に状態が収束する。
- clean install と uninstall 手順が自動試験される。
- 最初の Validated OS 組合せと、Compatible/Unsupported の判定方法が公開される。

## Phase 2: Technical Preview

### Scope

- 3-node Control Plane
- OIDC、Tenant、RBAC、Quota
- OVN overlay、Subnet、Port、Security Group
- Ceph RBD、Volume、Snapshot
- NUMA、HugePages、CPU Pinning
- Backup/restore、診断バンドル

### Exit criteria

- 20 Host、500 VM の連続試験を完了する。
- Control Plane の単一ノード障害で API が継続する。
- Tenant isolation test を通過する。
- DB restore 後に backend state と収束できる。

## Phase 3: Product Beta

### Scope

- 100 Host 規模検証
- cold/live migration
- SR-IOV
- NFVO integration profile
- ローリングアップグレード
- offline bundle、SBOM、artifact signing
- 運用 UI とアラーム管理

### Exit criteria

- 100 Host、5,000 VM の性能・耐久試験を完了する。
- N-1 から N のアップグレードとロールバック演習を完了する。
- 外部セキュリティ評価の重大指摘が解消される。
- サポート診断と既知問題の運用フローが確立する。

## Phase 4: General Availability

### 必須成果物

- インストール、設定、運用、アップグレード、DR、Troubleshooting 文書
- サポートマトリクスと互換性ポリシー
- SLA/SLO、サポート期間、脆弱性対応ポリシー
- ライセンス、Third-party notice、SBOM
- release note と既知問題

### Exit criteria

- 全 Must 要件が traceable な acceptance test を通過する。
- 30日以上の soak test で release blocker がない。
- backup/restore、Control Plane failover、Host failure の演習が完了する。
- インストールとアップグレードを第三者が文書のみで実施できる。

## リリース品質ゲート

各リリース候補は以下を満たす必要があります。

- unit、integration、contract、system、upgrade test
- API backward compatibility check
- migration forward/backward test
- vulnerability、license、secret scan
- signed artifact と SBOM
- performance regression check
- documentation link check
- release note と support matrix 更新
