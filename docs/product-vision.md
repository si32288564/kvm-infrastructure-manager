# 製品ビジョン

- 状態: Draft
- 更新日: 2026-08-09

## 1. ビジョン

通信・エッジおよびオンプレミスの運用者が、OpenStack 全体を導入せずに、KVM ベースの仮想インフラを安全かつ予測可能に運用できる VIM を提供します。

KIM は単なる libvirt フロントエンドではありません。ホスト群のインベントリ、配置判断、ネットワーク、ストレージ、テナント境界、クォータ、非同期処理、障害収束、監査を一つの製品境界として扱います。

## 2. 想定利用者

### Infrastructure Operator

ホスト、ネットワーク、ストレージ、容量、障害およびアップグレードを管理します。

### Tenant Administrator

外部 Identity Provider で認証された Principal に対する Tenant/Project membership、Role Binding、クォータ、イメージおよび利用状況を管理します。ユーザーアカウント自体は管理しません。

### Workload Operator

VM、ポート、ボリュームを API または管理 UI から操作します。

### External Orchestrator

NFVO、VNFM、OSS/BSS、CI/CD などから Northbound API を呼び出します。

### Support Engineer

監査ログ、Operation 履歴、診断バンドルを用いて障害を解析します。

## 3. 提供価値

- KVM ホスト群を単一のリソースプールとして管理できる。
- 通信ワークロード向けの NUMA、HugePages、CPU Pinning、SR-IOV を段階的に利用できる。
- API の冪等性と収束制御により、部分障害時にも安全に再試行できる。
- ETSI NFV の概念と対応する外部モデルを提供できる。
- 小規模なエッジから 100 ホスト規模まで、同一の運用モデルを適用できる。
- KVM/libvirt を利用できる一般的な Linux ディストリビューションを、Control Plane を変更せずに採用できる。
- オフライン環境にインストールし、アップグレードとロールバックを管理できる。

## 4. 製品境界

KIM が所有する責務:

- 仮想化された compute、network、storage 資源の管理
- 物理ホストと仮想資源の対応関係および容量の管理
- VM イメージ、フレーバー、配置ポリシーの管理
- テナント、クォータ、認可および監査
- 外部 Principal と Tenant/Project の membership および Role Binding
- Fault、Performance、Capacity 情報の公開
- 外部オーケストレーター向け API

KIM が初期リリースで所有しない責務:

- Network Service や VNF のライフサイクル管理
- VNF 内部の設定管理
- 物理スイッチや Ceph クラスターそのものの構築・更新
- WAN 全体のパス制御
- 課金および請求
- ベアメタル OS プロビジョニング
- User lifecycle、password、MFA、Identity federation、Credential authority
- 汎用 package installation、任意設定ファイル変更、OS patch management

Identity、Tenancy、Authorization は別の責務です。外部 Identity Platform が Principal を認証し、KIM はその Principal とリソース所有境界を結び付け、KIM resource に対する認可を評価します。

## 5. ハイパーバイザー OS の柔軟性

KIM は特定の Linux ディストリビューションを製品アーキテクチャの前提にしません。Ubuntu、Debian、RHEL-compatible、SUSE 系など、KVM/libvirt を実用的に提供できる一般的な Linux を採用可能にします。

ディストリビューション間の以下の差異は、ホスト側コンポーネント（仮称 Agent）が検出・正規化します。

- package prerequisite、service、filesystem layout
- SELinux、AppArmor、firewall
- libvirt/QEMU の機能および設定差
- NIC、bridge、OVS、SR-IOV の検出と設定
- LVM、Ceph client、HugePages、CPU/NUMA tuning の状態と capability
- ログ、監査、診断情報の収集方法

「採用可能」と「製品サポート済み」は区別します。アーキテクチャは広く受け入れ、リリースごとに自動試験済みの OS/version/component 組合せをサポートマトリクスとして公開します。

KIM は discovery と preflight/validation を所有します。OS変更を行う場合は、KIM resource を成立させるために定義された限定的な typed infrastructure remediation に限ります。任意 package、service、kernel parameter、設定ファイルを操作する汎用 Configuration Management は提供しません。

## 6. 初期市場仮説

- 通信事業者またはサービスプロバイダーのエッジ拠点
- OpenStack より小さい運用面積を求めるオンプレミス環境
- API 駆動で VM 基盤を組み込みたいアプライアンスベンダー

## 7. 成功指標

- 3 ノードの Control Plane と 100 Compute Host の構成を再現可能に導入できる。
- 5,000 VM のインベントリを保持し、主要一覧 API が定義済み SLO を満たす。
- Agent、Control Plane、ネットワークの一時障害後に手動 DB 修復なしで収束する。
- N-1 から N へのアップグレードでテナント VM を停止しない。
- すべての管理操作について actor、対象、時刻、結果を監査できる。
- 公開 API の後方互換性ポリシーと廃止手順が運用される。
- 新しい Linux ディストリビューション対応を、Control Plane の分岐追加なしで実装・検証できる。

## 8. 参照仕様

- [ETSI GS NFV 006: Architectural Framework](https://www.etsi.org/deliver/etsi_gs/NFV/001_099/006/05.02.01_60/gs_NFV006v050201p.pdf)
- [ETSI GS NFV-IFA 005: Or-Vi Interface and Information Model](https://www.etsi.org/deliver/etsi_gs/NFV-IFA/001_099/005/05.02.01_60/gs_NFV-IFA005v050201p.pdf)
- [ETSI GS NFV-IFA 010: Functional Requirements](https://www.etsi.org/deliver/etsi_gs/NFV-IFA/001_099/010/05.02.01_60/gs_NFV-IFA010v050201p.pdf)
