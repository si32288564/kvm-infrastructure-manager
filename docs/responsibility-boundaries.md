# 責任境界

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

KIM が製品として所有する authority と、外部 Platform、Identity Provider、Configuration Management、WIM、NFVO/VNFM が所有する責務を分離します。連携できることは、KIM がそのライフサイクルを所有することを意味しません。

## 2. Identity、Tenancy、Authorization

```mermaid
flowchart LR
    IdP["External Identity Platform"] -->|"authenticate / issue identity"| Principal["Principal"]
    Principal -->|"subject / claims"| Authz["KIM Authorization"]
    Authz --> Membership["Tenant / Project Membership"]
    Authz --> Binding["Role Binding / Policy"]
    Authz --> Resource["KIM Resource Authorization"]
```

| 領域 | Authority |
|---|---|
| User lifecycle、password、MFA、federation | External Identity Platform |
| Human/Service credential 発行・失効 | External Identity Platform |
| Tenant、Project | KIM |
| Membership、Role Binding、KIM Policy | KIM |
| Resource ownership、Quota | KIM |
| KIM action authorization decision | KIM |

KIM は外部 Principal の安定した subject と issuer を参照します。email、display name、group claim は補助属性であり、単独では永続 identity にしません。

## 3. Host Configuration

```text
Discovery
  -> Preflight / Validation
      -> Eligibility and diagnostics
          -> optional Typed Infrastructure Remediation
```

KIM が所有するもの:

- Host capability と現状の discovery
- workload 要件に対する preflight/admission
- bounded reason code と remediation hint
- KIM resource を成立させるために仕様化された typed operation
- Enrollment、Host Profile/Baseline、Compliance evidence、Placement block、maintenance authority
- HostGroup、materialized membership、Placement Scope、rollout/maintenance snapshotとKIM内policy binding

外部 Configuration Management が所有するもの:

- 汎用 package installation と OS patching
- 任意 service、設定ファイル、kernel argument の管理
- reboot orchestration と全社 OS baseline
- KIM 外のアプリケーションおよび Host 構成
- PXE/OS installation、汎用ZTP、firmware lifecycle、Host wipe

KIM の typed remediation は schema、precondition、対象 resource、rollback/verification、authority generation を持つ閉じた操作です。任意 package 名、shell、argv、file path、設定内容を受け取りません。

外部Configuration Managementとの連携では、KIMはControl requirement、対象Host、Baseline/Assignment generation、maintenance/fencing条件、必要evidenceを持つscoped requestを所有します。外部systemは実際の汎用Host変更を所有します。外部systemの完了通知はclaimであり、KIMがfresh observationを取得してCompliance Evaluatorで再判定するまでKIMのCompliance/READY/authorityを変更しません。

CMDB/asset systemはHostGroup selector/assertionのsourceになれますが、KIM membership authorityを直接所有しません。KIMがsource identity、generation、freshnessを検証しPostgreSQLへmaterializeして初めてmembershipになります。

## 4. Network

| KIM | External Network / WIM / Physical Infrastructure Manager |
|---|---|
| Virtual Network、Subnet、Port | WAN path と transport network |
| Tenant overlay と virtual router | Inter-PoP connectivity |
| Provider network binding | Physical switch lifecycle/configuration |
| DHCP、Security Group、Floating IP | Carrier/WAN resource orchestration |
| VM connectivity と NFVI-PoP gateway attachment | 外部ネットワーク容量 authority |
| OVS-DPDK PMD/DPDK memory/Port/RxQのHost内allocation | NIC firmware、physical fabric、外部DPDK application lifecycle |

KIM は provider network、gateway、external connectivity の参照とbindingを保持できますが、外部物理ネットワークを暗黙に構成しません。外部資源変更は別authorityとの明示的な契約を必要とします。

## 5. NFV MANO

| KIM | NFVO/VNFM |
|---|---|
| NFVI virtual resource lifecycle | NS/VNF lifecycle |
| Image、Flavor、VM、Network、Volume | VNFD/NSD interpretation |
| Placement、Quota、Capacity | VNF scaling intent |
| Infrastructure Fault/Performance | VNF configuration/behavior |
| Virtual-to-physical mapping | Service-level orchestration |

ETSI model は Northbound adapter で対応づけ、内部 authority model へ直接焼き込みません。

## 6. データベース Authority

PostgreSQL は desired state、allocation、network intent、attachment、operation/execution authority の System of Record です。libvirt、OVN、Ceph の observed state は復旧証拠ですが、未知 resource を自動的に KIM 所有へ昇格させません。復旧時の adoption は identity、provenance、generation と operator authorization を必要とします。

## 7. 明示的な非責任

KIMは以下を暗黙にも代行しません。

- User/Service credentialの発行、password/MFA/federation
- 汎用OS構成管理、任意package/service/configuration、patching、reboot orchestration
- PXE/OS provisioning、firmware update、secure erase
- 物理switch/router、WAN transport、inter-PoP pathのライフサイクル
- Ceph cluster、OVN cluster、外部IdPそのものの構築・アップグレード
- NS/VNF lifecycle、VNFD/NSD interpretation、VNF内部設定
- backendにだけ存在するresourceの自動adoptionまたは自動削除
- 結果不明なmutationに対する推測ベースの逆操作

外部systemとのadapterが存在しても、このauthority境界は移動しません。境界変更にはRequirementsとAccepted ADRの同時更新が必要です。
