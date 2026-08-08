# 要件定義

- 状態: Draft
- 更新日: 2026-08-08
- 対象: Product Beta までの要求を含む

## 1. 前提

- 単一 NFVI-PoP を最初の管理単位とする。
- Control Plane は 3 ノード HA 構成を標準とする。
- Compute Host は KVM/libvirt を利用できる一般的な Linux ディストリビューションを使用する。
- OS 固有の差異はホスト側コンポーネント（仮称 Agent）の adapter で吸収する。
- 初期検証規模は 100 ホスト、5,000 VM とする。
- 外部 IdP との OIDC 連携を標準認証方式とする。

優先度は Must、Should、Could、Won't for now で示します。

## 2. 機能要件

### 2.1 Identity、Tenant、Quota

| ID | 要件 | 優先度 |
|---|---|---|
| IAM-001 | 外部 OIDC Provider でユーザーを認証できる | Must |
| IAM-002 | system、tenant、project のスコープで RBAC を評価できる | Must |
| IAM-003 | vCPU、メモリ、VM、Volume、Storage、Port ごとにクォータを設定できる | Must |
| IAM-004 | Service Account と短寿命 Credential を発行できる | Should |
| IAM-005 | 複数 IdP を同時に利用できる | Could |

### 2.2 Host、Inventory、Capacity

| ID | 要件 | 優先度 |
|---|---|---|
| HST-001 | Agent の登録、承認、無効化、削除ができる | Must |
| HST-002 | CPU、NUMA、メモリ、HugePages、ストレージ、NIC、libvirt 機能を収集できる | Must |
| HST-003 | Host の enabled、disabled、maintenance、failed 状態を管理できる | Must |
| HST-004 | 仮想資源と物理 Host の対応を照会できる | Must |
| HST-005 | Host Aggregate、AZ、ラベル、trait を管理できる | Should |
| HST-006 | Capacity の予約量、使用量、実測量を区別できる | Must |
| HST-007 | OS、kernel、QEMU、libvirt、service manager、security module の差異を Agent adapter で吸収できる | Must |
| HST-008 | Agent が Host capability と制約を正規化して Control Plane へ報告できる | Must |
| HST-009 | 未対応または不完全な Host 環境を安全に拒否し、不足条件を診断できる | Must |

### 2.3 Image、Flavor

| ID | 要件 | 優先度 |
|---|---|---|
| IMG-001 | qcow2/raw イメージを登録、検証、削除できる | Must |
| IMG-002 | checksum、署名、取得元、可視性を保持できる | Must |
| IMG-003 | Host へのイメージキャッシュと整合性確認ができる | Must |
| FLV-001 | vCPU、RAM、root disk、追加仕様を Flavor として管理できる | Must |
| FLV-002 | NUMA、HugePages、CPU Pinning を Flavor で要求できる | Should |

### 2.4 Compute

| ID | 要件 | 優先度 |
|---|---|---|
| CMP-001 | VM を作成、照会、起動、停止、再起動、削除できる | Must |
| CMP-002 | API の再送で VM が重複作成されない | Must |
| CMP-003 | VM の desired state と observed state を別々に保持する | Must |
| CMP-004 | hard/soft affinity、anti-affinity を指定できる | Should |
| CMP-005 | cold migration を実行できる | Should |
| CMP-006 | 共有ストレージ利用時に live migration を実行できる | Should |
| CMP-007 | SR-IOV VF と PCI passthrough を割り当てられる | Should |
| CMP-008 | VM コンソールへ期限付きでアクセスできる | Should |

### 2.5 Scheduler

| ID | 要件 | 優先度 |
|---|---|---|
| SCH-001 | capacity、Host 状態、trait、placement constraint で候補を絞り込む | Must |
| SCH-002 | 配置決定と容量確保を競合なく実行する | Must |
| SCH-003 | 配置理由と除外理由を説明可能な形で保存する | Must |
| SCH-004 | NUMA topology と CPU Pinning を考慮する | Should |
| SCH-005 | カスタム重み付けポリシーを追加できる | Could |

### 2.6 Network

| ID | 要件 | 優先度 |
|---|---|---|
| NET-001 | Network、Subnet、Port を作成、照会、更新、削除できる | Must |
| NET-002 | VLAN provider network を利用できる | Must |
| NET-003 | OVN/OVS による Geneve tenant network を利用できる | Must |
| NET-004 | DHCP、security group、L2/L3 connectivity を提供できる | Should |
| NET-005 | Floating IP と north-south gateway を管理できる | Should |
| NET-006 | SR-IOV Port を VM に接続できる | Should |
| NET-007 | Network state と実データプレーンの不整合を検出できる | Must |

### 2.7 Storage

| ID | 要件 | 優先度 |
|---|---|---|
| STO-001 | Volume の作成、照会、拡張、削除ができる | Must |
| STO-002 | Volume を VM に attach/detach できる | Must |
| STO-003 | local LVM backend を利用できる | Must |
| STO-004 | Ceph RBD backend を利用できる | Should |
| STO-005 | snapshot と clone を利用できる | Should |
| STO-006 | backend 能力差を capability として公開できる | Must |

### 2.8 Operation、Event、Notification

| ID | 要件 | 優先度 |
|---|---|---|
| OPS-001 | 変更 API は Operation ID を返し、非同期に完了できる | Must |
| OPS-002 | Operation の状態、進捗、失敗理由、相関 ID を照会できる | Must |
| OPS-003 | 一時障害を分類し、上限付きで安全に再試行できる | Must |
| OPS-004 | Webhook または Event Stream で状態変更を通知できる | Should |
| OPS-005 | Operator が安全な Operation を再実行または中止できる | Should |

### 2.9 Fault、Performance、Audit

| ID | 要件 | 優先度 |
|---|---|---|
| O11Y-001 | Prometheus 形式で製品メトリクスを公開できる | Must |
| O11Y-002 | Host、VM、Control Plane のアラームを管理できる | Must |
| O11Y-003 | OpenTelemetry trace context をサービス間で伝播する | Should |
| AUD-001 | すべての認証、認可、変更操作を改ざん検知可能な監査ログへ記録する | Must |
| AUD-002 | 秘密情報を除外した診断バンドルを生成できる | Must |

## 3. 非機能要件

### Availability and Recovery

| ID | 目標 |
|---|---|
| NFR-AVL-001 | Control Plane API の月間可用性目標を 99.95% とする |
| NFR-AVL-002 | 単一 Control Plane ノード障害で API 提供を継続する |
| NFR-AVL-003 | Control Plane と Agent の通信断中も既存 VM は稼働を継続する |
| NFR-AVL-004 | Control Plane RPO 5分以内、RTO 60分以内を GA 目標とする |

### Performance and Scale

| ID | 目標 |
|---|---|
| NFR-PERF-001 | 100 Host、5,000 VM、10,000 Port を単一 PoP で管理する |
| NFR-PERF-002 | 読み取り API の p95 を通常負荷で 500 ms 以下とする |
| NFR-PERF-003 | API 受付から VM create dispatch までの p95 を 2秒以下とする |
| NFR-PERF-004 | 50件の同時変更 Operation を処理できる |

### Security

| ID | 目標 |
|---|---|
| NFR-SEC-001 | 外部通信とサービス間通信を TLS 1.3 で保護する |
| NFR-SEC-002 | Agent はノード固有の短寿命 Identity を使用する |
| NFR-SEC-003 | Tenant 間の API、Network、Storage 分離を試験する |
| NFR-SEC-004 | リリースごとに SBOM、署名、脆弱性レポートを生成する |

### Operability and Compatibility

| ID | 目標 |
|---|---|
| NFR-OPS-001 | オフライン環境へインストールできる |
| NFR-OPS-002 | N-1 から N へのアップグレードをサポートする |
| NFR-OPS-003 | API の破壊的変更には新しい major version を使用する |
| NFR-OPS-004 | 対応 OS、KVM/libvirt、OVN、Ceph の組合せをリリースごとに公開する |
| NFR-OPS-005 | 新しい Linux ディストリビューション対応に Control Plane の OS 条件分岐を必要としない |
| NFR-OPS-006 | deb、rpm、および検証用の自己完結型配布方式を用意する |

## 4. 受入れの考え方

各要件は、実装 Issue への分割時に以下を必須とします。

- 正常系、再送、タイムアウト、部分障害の受入れ条件
- Tenant 境界と認可条件
- 監査イベント
- メトリクスとアラート
- アップグレード時の互換性
- 自動テストのレベル
