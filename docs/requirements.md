# 要件定義

- 状態: Draft
- 更新日: 2026-08-09
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

### 2.1 Tenancy、Authorization、Quota

| ID | 要件 | 優先度 |
|---|---|---|
| IAM-001 | 外部 OIDC Provider が認証した Principal を検証できる | Must |
| IAM-002 | system、tenant、project のスコープで membership と RBAC を評価できる | Must |
| IAM-003 | vCPU、メモリ、VM、Volume、Storage、Port ごとにクォータを設定できる | Must |
| IAM-004 | 外部 Identity Platform が発行した Service Identity を Project と Role Binding に関連付けられる | Should |
| IAM-005 | 複数 IdP を同時に利用できる | Could |
| IAM-006 | User lifecycle、password、MFA、Identity federation、Credential 発行を KIM の責務に含めない | Must |

### 2.2 Host、Inventory、Capacity

| ID | 要件 | 優先度 |
|---|---|---|
| HST-001 | Agent の登録、承認、無効化、削除ができる | Must |
| HST-002 | CPU、NUMA、メモリ、HugePages、ストレージ、NIC、libvirt 機能を収集できる | Must |
| HST-003 | Host の enabled、disabled、maintenance、failed 状態を管理できる | Must |
| HST-004 | 仮想資源と物理 Host の対応を照会できる | Must |
| HST-005 | 第一級HostGroup、AZ相当のPlacement Scope、ラベル、traitを管理できる | Should |
| HST-006 | Capacity の予約量、使用量、実測量を区別できる | Must |
| HST-007 | OS、kernel、QEMU、libvirt、service manager、security module の差異を Agent adapter で吸収できる | Must |
| HST-008 | Agent が Host capability と制約を正規化して Control Plane へ報告できる | Must |
| HST-009 | 未対応または不完全な Host 環境を安全に拒否し、不足条件を診断できる | Must |
| HST-010 | discovery、preflight、validation と Host mutation を明確に分離する | Must |
| HST-011 | Host mutation は versioned typed infrastructure remediation に限定し、任意 package/service/config 操作を許可しない | Must |

### 2.3 Host Lifecycle、Baseline、Compliance

| ID | 要件 | 優先度 |
|---|---|---|
| HLC-001 | Hostをdiscovery、identity bootstrap、enrollment、baseline、ready、maintenance、decommissionのlifecycleで管理する | Must |
| HLC-002 | authenticated HostをEnrollment approvalまたは信頼済みPolicy Match前にtrusted/armedへ昇格させない | Must |
| HLC-003 | Enrollment Policyをversioned ruleとして管理し、identity evidenceとapproved factsで評価する | Must |
| HLC-004 | Host Profileとimmutable versioned Host Baselineを管理する | Must |
| HLC-005 | Baseline Controlをrequirement、applicability、severity、placement impact、remediation mode、evidence contractで表現する | Must |
| HLC-006 | Control statusをCOMPLIANT、NON_COMPLIANT、DEGRADED、UNKNOWN、NOT_APPLICABLEで評価する | Must |
| HLC-007 | Compliance ResultとevidenceをInventory/Baseline/evaluator generation付きappend-only履歴として保持する | Must |
| HLC-008 | Critical NON_COMPLIANT/UNKNOWNをHost-wideまたはcapability-scoped Placement Eligibilityへ反映する | Must |
| HLC-009 | remediation modeをobserve-only、auto-remediate-safe、maintenance-required、external-remediationに分類する | Must |
| HLC-010 | auto remediationにもEnrollment/Baseline/Authority generation、Lease、journal、verificationを要求する | Must |
| HLC-011 | Inventory/evidence更新と期限切れによりContinuous Complianceとdrift detectionを実行する | Must |
| HLC-012 | Baseline rolloutをcanary/batch/pause/abort/verification gateで管理する | Should |
| HLC-013 | disruptive remediationをdrain、impact approval、maintenance authority後に実行する | Must |
| HLC-014 | external-remediationではKIMがHostを変更せずrequirement/evidence/maintenance境界だけを管理する | Must |
| HLC-015 | decommissionでplacement停止、authority/Lease fencing、resource drain、credential失効、evidence保持を行う | Must |
| HLC-016 | duplicate Host identity/hardware conflictをquarantineし、自動mergeしない | Must |
| HLC-017 | credential renewal、Agent reconnect、Gateway recovery、Baseline assignmentだけでHost authorityをarmしない | Must |
| HLC-018 | Hostが自身のapproval、Profile、Baseline、Control policyを変更できない | Must |
| HLC-019 | Hardware identityを複数sourceのprovenance/freshness/conflict付きevidenceとして評価し、単一の可変identifierだけでpolicy-auto enrollmentしない | Must |
| HLC-020 | Compliance Evaluatorをimmutable artifact digest、contract/control/evidence compatibility、build/certification provenanceでversion管理する | Must |
| HLC-021 | Evaluator更新をCI comparison、shadow、canary、batch、failure threshold付きrolloutで進め、過去Resultを改変しない | Must |
| HLC-022 | External remediationの要求/応答を認証・generation・expiry・idempotency付きcontractで管理し、外部完了claimだけでCOMPLIANT/READY/armedへ遷移しない | Must |

### 2.4 Host Grouping

| ID | 要件 | 優先度 |
|---|---|---|
| HGR-001 | HostGroupとmembershipをSystem scopeの第一級versioned resourceとして作成、照会、更新、drain、retireできる | Must |
| HGR-002 | Placement Pool、Failure Domain、Operational Cohortを型分離し、型に許可された効果だけを適用する | Must |
| HGR-003 | Group dimension+levelごとにEXACTLY_ONE、ZERO_OR_ONE、MANYのmembership cardinalityを定義・検証する | Must |
| HGR-004 | explicit、versioned selector、authenticated external assertionによるmembership sourceとprovenanceを保持する | Must |
| HGR-005 | selector evaluationを副作用なく実行し、PostgreSQLへgeneration付きでmaterializeしたmembershipだけをauthorityにする | Must |
| HGR-006 | 同一type/dimension内のversioned hierarchyをcycle/partial graphなしに不可分更新する | Must |
| HGR-007 | stale、conflicting、required-missing membership/hierarchyをUNKNOWNまたは不適格としてfail closedに扱う | Must |
| HGR-008 | Placement dry/final admissionでGroup membership、policy、hierarchy generationを再検証する | Must |
| HGR-009 | Group membership/weightがHost lifecycle、Compliance、capability、resource eligibilityを上書きしない | Must |
| HGR-010 | Group capacityをHost inventory/allocationから導出し、独立したreservation authorityにしない | Must |
| HGR-011 | Group Profile/Baseline bindingをversion/priority付きで解決し、同priority conflictをlast-winsにせずBLOCKEDにする | Must |
| HGR-012 | Baseline rolloutを開始時のimmutable Group Membership Snapshotへbindし、加入/離脱で実行中scopeを暗黙変更しない | Must |
| HGR-013 | Maintenance waveをGroup snapshotとfailure-domain concurrency/capacity policyへbindする | Must |
| HGR-014 | Tenantへraw infrastructure Groupではなくexposure policy付きPlacement Scopeだけを公開する | Must |
| HGR-015 | active membership/reference/rollout/maintenance/policy bindingを持つGroupを削除しない | Must |
| HGR-016 | Group変更だけで既存workloadを暗黙移動、停止、再構成せずdrift/action-requiredとして扱う | Must |
| HGR-017 | READY/placement可能なHostが全active Placement Pool membershipsから一つのeffective Availability Policyを解決できることを必須とする | Must |

### 2.5 Availability Responsibility and Managed Recovery

| ID | 要件 | 優先度 |
|---|---|---|
| AVR-001 | immutable versioned AvailabilityPolicyとresponsibility、Host failure action、fencing/storage/recovery/failure-domain条件を管理する | Must |
| AVR-002 | responsibilityをINFRASTRUCTURE_MANAGED、WORKLOAD_MANAGED、MANUALに分類する | Must |
| AVR-003 | Host failure actionをRESTART_ON_OTHER_HOST、EVACUATE、NO_AUTOMATIC_ACTIONに分類し、responsibilityとの不正な組合せを拒否する | Must |
| AVR-004 | AvailabilityPolicyをPLACEMENT_POOLだけからversioned GroupPolicyBindingで参照する | Must |
| AVR-005 | binding欠損、stale、同priority conflictでHost Effective Availability Policyが一意に解決できないHostをREADY/Placement不適格にする | Must |
| AVR-006 | Final AdmissionでPolicy/Pool/membership generationをVM/Allocationのimmutable AvailabilityBindingへ保存する | Must |
| AVR-007 | Group/Policy変更だけで既存VMのAvailabilityBindingを変更せず、明示Rebind Operationと新revisionを要求する | Must |
| AVR-008 | Host failureをfailure epochとして検出、確認、fence、policy decision、recover、verifyの証拠付きstateで管理する | Must |
| AVR-009 | WORKLOAD_MANAGEDではFault/Eventを通知するがKIMから自動restart、evacuate、replacementを開始しない | Must |
| AVR-010 | MANUALではauthorized Manual Recovery DecisionまでKIMから自動VM mutationを開始しない | Must |
| AVR-011 | INFRASTRUCTURE_MANAGED recoveryでsource fencing、storage single-writer、VM/resource eligibility、failure-domain、transactional admissionを必須とする | Must |
| AVR-012 | fencing、attachment、resource ownership、Availability BindingのいずれかがUNKNOWNならautomatic recoveryを開始しない | Must |
| AVR-013 | Recovery Operationをcanonical Failure Campaign、VM、Availability Binding revision、actionで冪等化し、stale Campaign/epoch/resultをfenceする | Must |
| AVR-014 | EVACUATEをHost-scoped planからVM単位Operationへ分解し、部分成功、capacity不足、個別BLOCKEDを表現する | Must |
| AVR-015 | recovery destinationでcurrent Placement Pool/Policy compatibility、Compliance、capacity、Failure Domainを再評価しsilent fallbackしない | Must |
| AVR-016 | Host failure/recovery Eventをresponsibilityにかかわらずdurableに通知し、delivery failureでresponsibilityを変更しない | Must |

### 2.6 Workload Resilience Intent

| ID | 要件 | 優先度 |
|---|---|---|
| WRI-001 | Project scopeのversioned WorkloadResilienceGroup、Member Slot、Failure Domain Constraintを管理する | Must |
| WRI-002 | NFVO/VNFMのactive/standby等のroleをopaque metadataとして保持し、VNF lifecycle/application health authorityに使用しない | Must |
| WRI-003 | Northbound APIで公開Failure Domain classのdimension/levelを指定し、raw HostGroup/topologyを公開しない | Must |
| WRI-004 | rack、power-path等の複数dimensionへ独立したhard separation/max-members/min-domains constraintを指定できる | Must |
| WRI-005 | stable Member Slotへ同時に複数active VMをbindせず、Project ownershipとgenerationを検証する | Must |
| WRI-006 | Placement SnapshotへResilience Group/member/constraint/domain claim/hierarchy generationを含める | Must |
| WRI-007 | ResilienceDomainClaimをVM Allocation/Availability Binding/resource claimsと同じFinal Admission transactionでcommitする | Must |
| WRI-008 | concurrent member Placementでも同一Failure Domainへのhard constraint違反を一方だけcommit可能にする | Must |
| WRI-009 | distinct domain不足またはdomain evidence UNKNOWN時にconstraintをsilent relaxせずPlacementを拒否する | Must |
| WRI-010 | replacement時にold VM/source ownershipがUNKNOWNならMember Slot/Domain Claimを再利用しない | Must |
| WRI-011 | hierarchy/domain driftで既存VMを暗黙migrationせずVIOLATED/UNKNOWNとFault/Eventを記録する | Must |
| WRI-012 | Resilience IntentがAvailability responsibilityを上書きせず、全responsibility branchのPlacement/Recoveryでconstraintを再利用する | Must |
| WRI-013 | Northbound mappingがCore authorization、Project scope、idempotency、transactional admissionを迂回しない | Must |
| WRI-014 | active Member/Domain Claimを持つResilience Groupを削除しない | Must |
| WRI-015 | required member未充足をPENDINGとして表現し、増分max-members constraintとcomplete時min-distinct評価を区別する | Must |

### 2.7 Recovery Storm Control

| ID | 要件 | 優先度 |
|---|---|---|
| RCV-001 | immutable versioned RecoveryBudgetPolicyをAvailabilityPolicyから参照する | Must |
| RCV-002 | RecoveryQueueEntryとPostgreSQL transactionで発行するRecoveryBudgetLeaseをdurable authorityとして管理する | Must |
| RCV-003 | PLANNING/DISPATCH phaseごとにSite/Pool/Failure Domain/backend/Project等の該当全budget scopeを不可分取得する | Must |
| RCV-004 | Budget Leaseをdispatch許可に限定し、fencing、Placement、capacity claim、Command Lease、verificationを代替させない | Must |
| RCV-005 | Budget Lease expiry/worker lossから未実行を推測せず、Recovery Operation/Command/read-backで重複dispatchを防ぐ | Must |
| RCV-006 | max concurrency、start rate/window/burst、bounded backoff/jitterをscope別に強制する | Must |
| RCV-007 | bounded priority class、aging、fair-share、per-Project/Resilience Group capでstarvationを防ぐ | Must |
| RCV-008 | backend health gate/circuit breakerで該当recoveryをpauseし、復旧後に全safety generationを再検証する | Must |
| RCV-009 | duplicate failure signalをfailure epochへdeduplicateし、複数epochをevidence付きversioned FailureCampaignへ相関付ける | Must |
| RCV-010 | queue age、budget saturation、waiting/blocked/unknown/escalated stateを監査・Alarm/Eventへ公開する | Must |
| RCV-011 | Budget Policy変更だけでdispatch/started Recovery Operationを暗黙cancel/reclassifyしない | Must |
| RCV-012 | Control Plane/worker failover後もbudget/queue/lease authorityとfair orderingをPostgreSQLから復元する | Must |
| RCV-013 | Recovery dispatch時にBudget LeaseをOperationと不可分なdurable Budget Consumptionへ変換しterminal verificationまで並行数へ計上する | Must |
| RCV-014 | applicable budget scopeをversioned canonical key順でlockし、deadlock/serialization failure時は全取得をrollbackして再評価する | Must |
| RCV-015 | FailureCampaignごとにVM/actionのunique Recovery Campaign Claimを保持し、late correlation/mergeでも重複Queue、dispatch、Budget Consumptionを防ぐ | Must |

### 2.8 Data and Persistence

| ID | 要件 | 優先度 |
|---|---|---|
| DAT-001 | persistent dataをCurrent Authority、Immutable Decision/Evidence、Delivery Journal、Derived Projectionとして分類しschema catalogで管理する | Must |
| DAT-002 | ownership、desired state、allocation、attachment、execution/recovery authorityをPostgreSQL commitだけで進める | Must |
| DAT-003 | current pointer/summary更新時も過去Decision、Attempt、Result、Observation、Compliance、fencing evidenceを改変しない | Must |
| DAT-004 | Derived Projectionをauthorityから再構築可能にし、projection failureをdomain mutationの成功根拠にしない | Must |
| DAT-005 | domain mutation、Operation/idempotency、Outbox Eventを同一transactionでcommit/rollbackする | Must |
| DAT-006 | Inbox inputをsource identity/generation/message ID/digestでdeduplicateし、domain decisionとReceipt/Outboxを不可分commitする | Must |
| DAT-007 | data class別のversioned Retention Policy、legal/security hold、archive、tombstone期間を管理する | Must |
| DAT-008 | active reference、UNKNOWN、open Operation、Lease/Claim、legal holdを持つdataをGCせず、GCをLease/Receipt付きで冪等化する | Must |
| DAT-009 | DB retention/GC/partition削除からlibvirt、OVN、Ceph等のbackend side effectを開始しない | Must |
| DAT-010 | append-heavy historyをpartition可能にし、authority uniqueness、transactional admission、Tenant isolationをpartition境界で失わない | Must |
| DAT-011 | schema変更をexpand、migrate/backfill、switch、contractで実施し、N/N-1 reader/writer compatibilityを維持する | Must |
| DAT-012 | migration/backfillをartifact digest、schema generation、single Lease、checkpoint、bounded batch/lock、verification付きで管理する | Must |
| DAT-013 | backfillが並行更新のcurrent generationを上書きせず、失敗/再開時にも冪等に収束する | Must |
| DAT-014 | base backup、continuous WAL、schema/migration catalog、artifact manifest、checksumを一つのBackup Manifestへbindする | Must |
| DAT-015 | PITR後に新しいrestore epoch/database authority generationを発行し、pre-restore Lease、session、worker/publisher claimをfenceする | Must |
| DAT-016 | restore後はread-only recovery modeでfull observationし、MATCHED、DB_ONLY、BACKEND_ONLY、CONFLICTING、UNKNOWNへ分類する | Must |
| DAT-017 | backend-only resourceを自動adopt/deleteせず、ownership/fencing/authorization付きAdoption Operationを要求する | Must |
| DAT-018 | PITR後のOutbox/Inbox/Command再送をstable ID/Receiptでdeduplicateし、外部side effect不明をUNKNOWN/read-backで解決する | Must |
| DAT-019 | DR restore epochを旧Site/primary fencingの代替にせず、旧database writer、Control Plane dispatch、credential/endpointのfencing proofまで通常mutationを再開しない | Must |

### 2.9 Image、Flavor

| ID | 要件 | 優先度 |
|---|---|---|
| IMG-001 | qcow2/raw イメージを登録、検証、削除できる | Must |
| IMG-002 | checksum、署名、取得元、可視性を保持できる | Must |
| IMG-003 | Host へのイメージキャッシュと整合性確認ができる | Must |
| FLV-001 | vCPU、RAM、root disk、追加仕様を Flavor として管理できる | Must |
| FLV-002 | NUMA、HugePages、CPU Pinning を Flavor で要求できる | Should |

### 2.10 Compute

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
| CMP-009 | VM ごとに cold、live、restart-on-other-host、none の migration capability と不適格理由を評価できる | Should |

### 2.11 Scheduler

| ID | 要件 | 優先度 |
|---|---|---|
| SCH-001 | eligibility/admission を scoring から分離し、pure evaluation として候補適格性を判定する | Must |
| SCH-002 | 選択候補に対して transactional final admission と容量予約を競合なく実行する | Must |
| SCH-003 | 適格性、除外理由、score、選択理由、final admission 結果を保存する | Must |
| SCH-004 | NUMA topology と CPU Pinning を考慮する | Should |
| SCH-005 | カスタム重み付けポリシーを追加できる | Could |
| SCH-006 | final admission の競合失敗時に同じ request snapshot の残候補を再選択できる | Must |
| SCH-007 | dry admission は状態を変更せず、capacity を予約しない | Must |

### 2.12 Network

| ID | 要件 | 優先度 |
|---|---|---|
| NET-001 | Network、Subnet、Port を作成、照会、更新、削除できる | Must |
| NET-002 | VLAN provider network を利用できる | Must |
| NET-003 | OVN/OVS による Geneve tenant network を利用できる | Must |
| NET-004 | DHCP、security group、L2/L3 connectivity を提供できる | Should |
| NET-005 | Floating IP と north-south gateway を管理できる | Should |
| NET-006 | SR-IOV Port を VM に接続できる | Should |
| NET-007 | Network state と実データプレーンの不整合を検出できる | Must |

### 2.13 NFV Dataplane

| ID | 要件 | 優先度 |
|---|---|---|
| DPL-001 | OVS/DPDK version、runtime readiness、datapath modeをHost capabilityとして収集する | Must |
| DPL-002 | pCPUをhousekeeping、workload shared/dedicated、emulator、PMD、service lcoreのroleで排他的に管理する | Must |
| DPL-003 | NUMA/page sizeごとのHugePage poolをworkload、DPDK、platform reserveのpurpose別ledgerで管理する | Must |
| DPL-004 | DPDK socket memoryをNUMAごとのdesired/reserved/observed resourceとして管理する | Must |
| DPL-005 | DPDK Port、PF/VF/representor、vhost、Rx Queue、queue capabilityをstable identityで管理する | Must |
| DPL-006 | PMD core setとRxQ assignmentをdesired/observed generationとして管理する | Must |
| DPL-007 | PMD、Port、DPDK memory、VM memory、PCIのNUMA localityをeligibilityで評価する | Must |
| DPL-008 | PMD CPU、DPDK memory、Port/RxQ claimを他resourceと同じfinal admissionで不可分commitする | Must |
| DPL-009 | vhost multiqueue/queue pair要求をVM Dataplane Bindingとして表現する | Should |
| DPL-010 | OVS-DPDK変更をclosed typed Commandで実行し、任意OVSDB/EAL/PCI操作を許可しない | Must |
| DPL-011 | restart-required変更をdisruptive operationとして通常VM作成から分離する | Must |
| DPL-012 | PMD/RxQ/Port/runtime observationでdataplane complianceを検証する | Must |
| DPL-013 | PMD utilization、cycles、dropsをauthorityではなくtelemetryとして扱う | Must |
| DPL-014 | OVS/DPDK非対応・degraded時にkernel datapath等へsilent fallbackしない | Must |
| DPL-015 | OVS/DPDK version組合せとDataplane capabilityをsupport matrixで公開する | Should |

### 2.14 Storage

| ID | 要件 | 優先度 |
|---|---|---|
| STO-001 | Volume の作成、照会、拡張、削除ができる | Must |
| STO-002 | Volume を VM に attach/detach できる | Must |
| STO-003 | local LVM backend を利用できる | Must |
| STO-004 | Ceph RBD backend を利用できる | Should |
| STO-005 | snapshot と clone を利用できる | Should |
| STO-006 | backend 能力差を capability として公開できる | Must |

### 2.15 Operation、Event、Notification

| ID | 要件 | 優先度 |
|---|---|---|
| OPS-001 | 変更 API は Operation ID を返し、非同期に完了できる | Must |
| OPS-002 | Operation の状態、進捗、失敗理由、相関 ID を照会できる | Must |
| OPS-003 | 一時障害を分類し、上限付きで安全に再試行できる | Must |
| OPS-004 | Webhook または Event Stream で状態変更を通知できる | Should |
| OPS-005 | Operator が安全な Operation を再実行または中止できる | Should |
| OPS-006 | Operation と実行配送を分離し、Job、Command、Lease、Attempt を永続化する | Must |
| OPS-007 | Command Lease は期限、owner、token、attempt index、authority generation を持つ | Must |
| OPS-008 | Agent は Command を実行する前に durable journal へ記録する | Must |
| OPS-009 | Execution Outcome の UNKNOWN を FAILED と区別し、stale result を fencing できる | Must |
| OPS-010 | 成功 Result だけで Operation を成功にせず、後続 observation で desired state を検証する | Must |

### 2.16 Fault、Performance、Audit

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
| NFR-AVL-004 | 同一 Site の HA failover は committed authority data の RPO 0 を目標とする |
| NFR-AVL-005 | Disaster Recovery は backup RPO 5分以内、Control Plane RTO 60分以内を GA 目標とする |
| NFR-AVL-006 | Restore 後に backend observation、quarantine、adoption を用いて authority を安全に再構築できる |

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
| NFR-SEC-005 | Agent は内部 Message Bus credential を保持せず、専用 Agent Gateway と mTLS session で通信することを標準案とする |

### Operability and Compatibility

| ID | 目標 |
|---|---|
| NFR-OPS-001 | オフライン環境へインストールできる |
| NFR-OPS-002 | N-1 から N へのアップグレードをサポートする |
| NFR-OPS-003 | API の破壊的変更には新しい major version を使用する |
| NFR-OPS-004 | 対応 OS、KVM/libvirt、OVN、Ceph の組合せをリリースごとに公開する |
| NFR-OPS-005 | 新しい Linux ディストリビューション対応に Control Plane の OS 条件分岐を必要としない |
| NFR-OPS-006 | deb、rpm、および検証用の自己完結型配布方式を用意する |

### Robustness and Failure Semantics

| ID | 目標 |
|---|---|
| NFR-ROB-001 | 全failure classについてDetect、Contain、Fence、Observe、Recover、Reconcile、Escalateを定義する |
| NFR-ROB-002 | timeout、Lease expiry、通信断をbackend mutation失敗または未実行の証明として扱わない |
| NFR-ROB-003 | UNKNOWN outcomeの履歴を上書きせず、verification evidenceと後続decisionを追記する |
| NFR-ROB-004 | stale identity、generation、Lease、Result、observationがcurrent authorityを進めない |
| NFR-ROB-005 | recovery不能時はresourceをblocked/quarantinedに保ち、推測ベースの破壊操作を行わない |
| NFR-ROB-006 | commit応答喪失、partition、process crash、Host loss、backend timeout、stale authorityをfault injectionで検証する |

### Extensibility

| ID | 目標 |
|---|---|
| NFR-EXT-001 | extension contractはversion、capability、limits、timeout、error、side-effect、verificationを定義する |
| NFR-EXT-002 | extensionはCore DB、内部Message Bus、authorization、audit、Lease authorityを迂回できない |
| NFR-EXT-003 | Agent operation moduleは閉じたtyped Commandとnarrow backend interfaceだけを受け取る |
| NFR-EXT-004 | capabilityをversion、constraints、generation、health、support tierとして公開する |
| NFR-EXT-005 | extensionのregister、ready、drain、upgrade、remove lifecycleを安全に実行できる |
| NFR-EXT-006 | extensionごとに共通conformance testとrelease certificationを実行する |

## 4. 受入れの考え方

各要件は、実装 Issue への分割時に以下を必須とします。

- 正常系、再送、タイムアウト、部分障害の受入れ条件
- Tenant 境界と認可条件
- 監査イベント
- メトリクスとアラート
- アップグレード時の互換性
- 自動テストのレベル
- Architecture Invariant IDとAcceptance/Fault/Conformance Test ID

Must requirementに未追跡行がある場合、実装Phaseへ進めません。追跡状態は [Architecture Traceability Matrix](traceability-matrix.md) を正本とします。
