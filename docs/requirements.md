# 要件定義

- 状態: Baseline
- 更新日: 2026-08-09
- 対象: Product Beta までの要求を含む

## 1. 前提

- 単一 NFVI-PoP を最初の管理単位とする。
- Control Plane は 3 ノード HA 構成を標準とする。
- Compute Host は KVM/libvirt を利用できる一般的な Linux ディストリビューションを使用する。
- OS 固有の差異は KIM Host Agent の adapter で吸収する。
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
| IAM-006 | User/Northbound Service Principal lifecycle、password、MFA、Identity federation、Credential発行をKIMの責務に含めない | Must |

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
| HST-012 | KIM の core management function は Linux KVM、QEMU、libvirt の patch、fork、proprietary modification を要求しない | Must |
| HST-013 | KIM Host Agent は標準 interface を使用し、KIM metadata がなくても underlying resource を通常の標準 interface から扱える状態を維持する | Must |
| HST-014 | KIM を hypervisor distribution または KIM 専用 KVM/QEMU/libvirt build の提供主体にしない | Must |
| HST-015 | Inventory module は versioned descriptor、artifact digest、closed domain、schema version、capability allow-list を宣言し、未宣言 domain/capability または任意 opaque payload を報告できない | Must |
| HST-016 | normalized Host Inventory は Host identity、observation generation、collection status、module provenance、typed resource fragment、capability/constraint を canonical schema で保持する | Must |
| HST-017 | durable Inventory Receipt、immutable snapshot evidence、current capability projection を一つの PostgreSQL transaction で処理し、古い observation generation で current projection を巻き戻さない | Must |
| HST-018 | Linux Host inventory は Raw Source → Raw Evidence → OS Integration Adapter → Normalizer → typed Fragment → Capability Mapping → Snapshot/Projection の evidence chain を保持し、各 normalized field の source path、observation state、reason を追跡できる | Must |
| HST-019 | Host capability の状態は AVAILABLE、UNAVAILABLE、UNKNOWN、UNSUPPORTED を区別し、既知の未設定、観測不能、interface 非対応を 0、false、空配列へ縮退させない | Must |

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
| DAT-020 | current authorityからhistory/archiveへのreferenceをhard DB、verified logical、archive manifest referenceに分類し、欠損/不一致scopeをfail closedにする | Must |
| DAT-021 | RECOVERY_READ_ONLY writeを専用identity、DB role、API、DR generation、approval、auditで通常service/resource mutationから分離する | Must |

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
| NET-008 | KIM network authority、Network Intent Revision、OVN NB desired、OVN SB realization、Host/dataplane observationを別generation/stateで管理する | Must |
| NET-009 | IP/MACをNetwork/Subnet scopeのtransactional Network Identity Claimとして一意に確保する | Must |
| NET-010 | isolated Network間のoverlapping CIDRを許可し、同一routing/attachment scopeの曖昧なoverlapを拒否する | Must |
| NET-011 | IP Poolのgateway/DHCP/infrastructure reservation、exclusion、explicit/automatic allocation policyを管理する | Must |
| NET-012 | Port/NAT/DHCP/binding/dataplane absence確認とquarantine期間完了までIP/MAC Claimを再利用しない | Must |
| NET-013 | VLAN/VNI Segment PoolとClaimをphysical network/overlay domain scopeで一意に管理する | Must |
| NET-014 | Network referenceとOVN/Host dataplane absence確認までVLAN/VNI Segment Claimを再利用しない | Must |
| NET-015 | Provider network mappingを外部physical network capability/referenceとして扱い、switch/fabric authorityを暗黙取得しない | Must |
| NET-016 | Port BindingをHost/chassis/device、binding type、segment mapping、generation付き第一級resourceとして管理する | Must |
| NET-017 | 一般Portのactive Binding Claimを最大一つにし、migration/recoveryの一時状態をPortBindingHandoffで表現する | Must |
| NET-018 | Port ACTIVEをDB Binding、OVN NB、OVN SB、Host/device/dataplaneのbinding-type別verification後だけ確定する | Must |
| NET-019 | KIM authorityからimmutable Network Intent Revisionを生成し、typed plan/apply/observe contractでOVNへ適用する | Must |
| NET-020 | OVN apply response lossをstable KIM ID、intent generation、digestのread-backで解決する | Must |
| NET-021 | network binding/NAT/gateway/security outcomeがUNKNOWNならidentity/segment再利用、反対操作、blind rebind、policy緩和を行わない | Must |
| NET-022 | DHCP desired options/IP bindingとguest lease/runtime observationを分離し、delivery failureでIPを再割当しない | Must |
| NET-023 | Router InterfaceをSubnet/Router ownership、IP Claim、route overlapと不可分に管理する | Must |
| NET-024 | Gateway Bindingをprovider mapping、gateway group/chassis、HA policy、health generation付きで管理する | Must |
| NET-025 | Floating IP Claimとfixed Port/IP NAT Binding、Router/Gateway dependencyを不可分commitする | Must |
| NET-026 | Gateway failoverでold gateway/chassis/NAT generationをfenceし、physical/WAN reachability UNKNOWNを区別する | Must |
| NET-027 | Security Policy/Rule、Port membership、anti-spoofingをversioned intentとして管理しUNKNOWN時にdefault allowへfallbackしない | Must |
| NET-028 | effective MTUをprovider/overlay overhead、Host/dataplane/gateway/path capabilityから評価し不足・UNKNOWN候補を拒否する | Must |
| NET-029 | SR-IOV PortのNetwork Identity/Segment ClaimとPCI VF/device/physical mappingをFinal Admissionで不可分commitする | Must |
| NET-030 | OVS-DPDK/vhost bindingをPMD/RxQ/NUMA claimと不可分にし、binding typeをsilent fallbackしない | Must |
| NET-031 | Host recovery/migrationでold/new Port Binding generation、Host/device authority、destination reachability/securityを再評価する | Must |
| NET-032 | active/pending Binding、IP/MAC/Segment/NAT/DHCP/Security、Recovery/Migration/UNKNOWN中のNetwork resource deleteを拒否する | Must |
| NET-033 | backend-only/foreign OVN object、unknown Host interface/chassis bindingを自動adopt/delete/unbindしない | Must |
| NET-034 | Network adapter credential、raw topologyを秘匿し、provider pool/gateway/force operation/Adoptionを個別permission/approvalで保護する | Must |
| NET-035 | Network Event/APIでintent/binding/layer generationとbounded reasonを公開しraw OVN/Host/physical identityをredactする | Must |

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
| STO-007 | Storage Backend/Classをstable identity、locality、access scope、capability/health generation、fencing/secret policy付きで管理する | Must |
| STO-008 | Volume desired state、Backend Binding、Attachment Intent/Claim、backend/libvirt Observationを別resource/generationとして管理する | Must |
| STO-009 | SINGLE_WRITER Volumeのactive Attachment ClaimをPostgreSQL transactionで最大一つに制限する | Must |
| STO-010 | READ_ONLY_MANYを明示backend/device capabilityかつ全active Claim read-only時だけ許可し、未certified SHARED_WRITERを拒否する | Must |
| STO-011 | attachをFinal Admission、typed execution、DB/libvirt/backend observation verificationで収束させる | Must |
| STO-012 | detachでlibvirt I/O pathとbackend client/lock releaseを検証するまでAttachment Claimを解放しない | Must |
| STO-013 | attach/detach outcome、client I/O、watcher/lock/holderのいずれかがUNKNOWNなら反対操作または別Host write attachを開始しない | Must |
| STO-014 | compute source fencing、storage client fencing、attachment authority fencingを別の証明として管理する | Must |
| STO-015 | watcher、lock、blocklist、device/holder stateをgeneration/freshness/provenance付きevidenceとして扱い、単独でownership authorityにしない | Must |
| STO-016 | Ceph RBDをcluster/pool/namespace/image stable ID、feature、client/lock、secret reference付きBindingとして管理する | Must |
| STO-017 | Local LVMをHost/VG/LV UUIDとlocalityへbindし、certified replication/exportなしに別Host recoveryを許可しない | Must |
| STO-018 | Host failure recoveryでcurrent Availability responsibility、old/new Attachment generation、compute/storage fencing、destination accessを再評価する | Must |
| STO-019 | cold/live migrationをAttachment Handoffとして管理し、一時dual accessを一般的な二active writer Claimへ変換しない | Must |
| STO-020 | Snapshot/Cloneのparent-child dependencyとconsistency levelを保持し、未証明のapplication consistencyを表示しない | Must |
| STO-021 | backend expandとguest-visible device/filesystem convergenceを分離して検証する | Should |
| STO-022 | active/pending Attachment、Snapshot/Clone child、Recovery/Migration/UNKNOWN、legal hold中のVolume deleteを拒否する | Must |
| STO-023 | DB tombstone/GCとbackend image/LV cleanupを分離し、typed deleteとabsence verificationを要求する | Must |
| STO-024 | Storage credential値をSecret Providerへ置き、KIM DB/Event/Commandにはscoped reference/versionだけを保持する | Must |
| STO-025 | backend-only image/LV、unknown watcher/lock、unmatched deviceを自動adopt/delete/detachせず明示Operationを要求する | Must |
| STO-026 | force detach、client fencing、lock break、backend delete、Adoptionを個別permission/approval/auditで保護する | Must |
| STO-027 | Storage capacityをPostgreSQL reserved/allocated ledgerとbackend observed/external usageへ分離し、Final Admissionで不可分claimしbackend delete確認まで再利用しない | Must |

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

### 2.17 Upgrade and Compatibility

| ID | 要件 | 優先度 |
|---|---|---|
| UPG-001 | artifact digest、provenance/SBOM、dependency、contract range、support matrix、migration、rollback boundaryをimmutable Release Manifestで管理する | Must |
| UPG-002 | source/target release、schema/protocol/backend/Host evidenceをbindしたCompatibility Decisionをgeneration/digest付きで保持する | Must |
| UPG-003 | compatibilityをversion文字列だけで推測せずVALIDATED、COMPATIBLE、INCOMPATIBLE、UNKNOWNへ明示判定する | Must |
| UPG-004 | Upgrade Campaign、Plan、Wave、Target、Feature Gate、Rollback BoundaryをPostgreSQL authorityとして永続化する | Must |
| UPG-005 | upgrade preflightでManifest/artifact、upgrade path、quorum、schema、API/protocol/event、extension、backend/Host、rollback readinessを検証する | Must |
| UPG-006 | canary/batch、max unavailable、failure threshold、pause/abort条件を持つwaveでrolloutする | Must |
| UPG-007 | wave開始時のimmutable target snapshotへ対象をbindし、途中のselector/group driftで暗黙追加しない | Must |
| UPG-008 | mixed-versionを明示compatibility edgeを持つN/N-1に限定し、N-2/unmanaged/digest不明componentをserving/dispatchへ参加させない | Must |
| UPG-009 | 全active writer/consumerが解釈できるschema/field/enum/authority semanticsだけをFeature Gate前にwriteする | Must |
| UPG-010 | schema変更をexpand/migrate/switch/contractへ従わせ、destructive contractをrollback window後の別承認にする | Must |
| UPG-011 | Control Plane rolling upgrade中もHA quorum、serving capacity、committed authority、既存VM稼働を維持する | Must |
| UPG-012 | Gateway/Agentがprotocol envelopeとCommand/Result schemaをnegotiationし、互換外Commandをdispatchしない | Must |
| UPG-013 | Agent upgrade中はHost dispatchをdrainし、再接続/version一致だけでHost authorityを再armしない | Must |
| UPG-014 | KIM所有Agent artifact更新とHost OS/kernel/libvirt/QEMU等のexternal remediationを責任分離する | Must |
| UPG-015 | public API compatible変更、major version/deprecation、idempotency/ETag/Operation identityの安定性を検証する | Must |
| UPG-016 | Eventを発行時schema/digestのimmutable payloadとして保持し、retention期間のdecode/replay compatibilityを維持する | Must |
| UPG-017 | extension/adapter/evaluator upgradeにcontract range、certification、drain、shadow/canary、ownership fencingを要求する | Must |
| UPG-018 | Host/backend support matrixをobserved version/capability/provenanceで評価し、互換外scopeのPlacement/Recovery/dispatchを拒否する | Must |
| UPG-019 | support matrix変更だけで既存VM/Port/Volumeを暗黙停止、移動、再構成しない | Must |
| UPG-020 | rollbackを新しいPlan/Attemptとして記録し、明示rollback edge、schema/decoder/artifact保持、current observationを要求する | Must |
| UPG-021 | destructive contract後、rollback outcome UNKNOWN、互換外sourceへのrollbackを拒否しforward repairへ送る | Must |
| UPG-022 | coordinator failover後にdurable Campaign/Lease/Receiptとartifact observationから再開しin-memory progressをauthorityにしない | Must |
| UPG-023 | online/offline bundleへ同じManifest、artifact verification、SBOM、migration、support matrix、verification evidenceを要求する | Must |
| UPG-024 | publish/start/switch/contract/feature activation/rollback/overrideを分離したpermission、approval、auditで保護する | Must |
| UPG-025 | QEMU/libvirt upgrade時に既存VMのmachine type/CPU model/firmware/device ABI bindingを維持し新規VM defaultと分離する | Must |
| UPG-026 | Event/evidence decoder artifactを参照payloadのRetention Policy、archive、legal holdとbindし参照中に削除しない | Must |
| UPG-027 | 複数Feature Gateをrequires/conflicts/rollback dependencyのacyclic graphとして管理し順序付きactivation/rollbackを行う | Must |

### 2.18 Time and Clock Semantics

| ID | 要件 | 優先度 |
|---|---|---|
| TIM-001 | Wall Clock、Database Authority Time、Process Monotonic、Agent-local Deadline、Observed、Received/Committed timestampを区別する | Must |
| TIM-002 | 重要timestampへsource、clock/boot identity、received/committed time、uncertainty/quality、generationを関連付ける | Must |
| TIM-003 | resource/execution/delivery/observation/HAのorderingとfencingをtimestampだけで決めずgeneration/token/sequence/epochを使用する | Must |
| TIM-004 | DB、Control Plane、HostのClock Observationをoffset、uncertainty、sync、monotonic continuity、step/leap、provenance付きで収集する | Must |
| TIM-005 | Clock HealthをHEALTHY、DEGRADED、UNTRUSTED、UNKNOWNへ分類し用途別policyへbindする | Must |
| TIM-006 | 一般VM、Command、credential、correlation、NFV telemetryで異なるclock quality thresholdを適用できる | Should |
| TIM-007 | Control Plane Lease、deadline、freshness ingest、retention decisionをcurrent PostgreSQL authority time/generationで計算・比較する | Must |
| TIM-008 | DB clock step/conflict時にnew Lease、renewal、GC、finalizationをpauseしclock復旧だけで旧Leaseをreviveしない | Must |
| TIM-009 | Leaseへowner/purpose/scope、token、authority generation、not-before/expiry、maximum lifetime、renew/revoke decisionを保持する | Must |
| TIM-010 | Lease/credential/deadline expiryを今後のauthority終了として扱い、期限前side effectの未実行証明にしない | Must |
| TIM-011 | Lease renewalをcurrent owner/token/generation/未失効条件のDB transactionとしexpired tokenの時刻変更で復活させない | Must |
| TIM-012 | AgentがGateway exchangeとDB expiry/uncertaintyから保守的なlocal monotonic start deadlineを導出する | Must |
| TIM-013 | Agent protocolのRTT/uncertaintyがCommand policy上限を超える場合にCommandを開始しない | Must |
| TIM-014 | Agent process/Host reboot/boot IDまたはmonotonic continuity変更後にcached/unstarted Commandを開始しない | Must |
| TIM-015 | source_observed_atとKIM received_at/verified_atを分離しfreshnessをtrusted ingest time、generation、challenge bindingで評価する | Must |
| TIM-016 | Agent/backendの未来timestampやclock rollbackでevidence freshnessを延長しない | Must |
| TIM-017 | certificate/token/bootstrap/remediation expiryをControl Plane clock quality/uncertaintyとnonce/session/generationで検証する | Must |
| TIM-018 | 時間上有効なcredentialだけではEnrollment、Role Binding、Host authority、Command Leaseを成立させない | Must |
| TIM-019 | maintenance/rollout calendarへtimezone ID、DST ambiguity policy、versioned UTC interval materializationを要求する | Should |
| TIM-020 | calendar window開始/終了やmissed windowだけでdrain/fencing/mutation/destructive catch-upを実行しない | Must |
| TIM-021 | queue aging、rate window、grace/deadlineをdurable policyとDB timeへbindしclock jumpで二重creditや即時破壊操作を生まない | Must |
| TIM-022 | retention/GCをDB time、minimum safety horizon、Candidate Snapshot、reference/hold/backup guardで判定しclock anomaly時に停止する | Must |
| TIM-023 | idempotency/Inbox/Receipt/decoder retentionを最大replay、Event retry、DR RPO、offline interval、legal holdへbindする | Must |
| TIM-024 | failure correlationをsource/received time、uncertainty interval、topology、independent evidenceで評価し同時刻だけでmergeしない | Must |
| TIM-025 | DB failover、Host reboot、PITRでauthority/boot/restore generationによりpre-event timer/Lease/sessionをfenceする | Must |
| TIM-026 | APIでtimestamp種別、UTC/offset、freshness/expiry、bounded clock quality/uncertainty、server-evaluated remaining durationを表現する | Should |
| TIM-027 | clock anomaly時も既存VM/dataplaneを維持し影響するnew auth/placement/dispatch/GC/finalizationだけをfail closedにする | Must |
| TIM-028 | clock step/slew/source loss、delay/reorder、reboot/failover/PITR、DST、retention、correlationをfault injectionする | Must |
| TIM-029 | DB/Control Plane Clock Healthを独立upstream source、node相互観測、source diversity、provenance/uncertaintyから評価する | Must |
| TIM-030 | PTP/GNSS等のPrecision Time DomainをKIM authority clockから分離しcapability/Compliance/Placement observationとして扱う | Must |
| TIM-031 | time scale、leap second/smear policy/windowをClock Policyへ宣言し不明・競合sourceを安全に扱う | Should |

### 2.19 PKI and Trust Lifecycle

| ID | 要件 | 優先度 |
|---|---|---|
| PKI-001 | Control Plane、Host Agent、External Integration、Backend Adapter、Artifact Verification、Data Protectionのtrust/key domainを分離する | Must |
| PKI-002 | 外部Identity PlatformのUser/Service Principal authorityとKIM workload/transport PKIのTrust Bindingを分離する | Must |
| PKI-003 | Root CAをoffline/external custodyとし通常Control Plane/Agent/DBで日常issuanceに使用しない | Must |
| PKI-004 | purpose/Site/environment別Intermediate CAでAgentとControl Plane workloadのissuance blast radiusを分離する | Must |
| PKI-005 | Certificate ProfileでSAN namespace、EKU/key usage、name/path constraint、algorithm、lifetime、key provenanceを制限する | Must |
| PKI-006 | CA/workload private key valueをKIM DBへ保存せずSecret Provider/HSM/KMSのopaque reference/version/public fingerprintだけを保持する | Must |
| PKI-007 | Root/Intermediate、Profile、Relationship、Revocation sourceをimmutable TrustBundle revisionとmonotonic trust generationで管理する | Must |
| PKI-008 | certificateをissuer/profile/fingerprint/public key、Principal/Host/workload identity、Enrollment、trust generationへCredential Bindingする | Must |
| PKI-009 | Trust Decisionでchain、time、profile、SAN/EKU、Binding、revocation freshness、proof-of-possession、sessionを検証する | Must |
| PKI-010 | certificate validation成功をRole Binding、Enrollment、Host authority、Command Lease、backend mutation成功とみなさない | Must |
| PKI-011 | TrustBundle、revocation、clock、Credential Binding generation変更時にcached Trust Decisionを失効する | Must |
| PKI-012 | Agent bootstrap materialをone-time、short-lived、Site/Host/policy、nonce/challenge、maximum useへbindする | Must |
| PKI-013 | Agent CSRをhardware identity/Enrollment evidence、challenge、Certificate Profile、proof-of-possessionへbindする | Must |
| PKI-014 | private keyをAgent/workload側で生成しCSR/API/Event/Command/log/diagnostic/通常backupへ送信・保存しない | Must |
| PKI-015 | issuance response lossをrequest/CSR/key digestとCredential Bindingのread-backで解決しblind duplicate issuanceを行わない | Must |
| PKI-016 | renewal/rekeyをcurrent identity/policy/trust/proofを再評価した新Credential Binding revisionとして管理する | Must |
| PKI-017 | certificate overlap中もold/new credentialを一つのlogical identityとcurrent session/Host authority generationへmapする | Must |
| PKI-018 | Authenticated Sessionをpeer fingerprint、Binding、TrustBundle/revocation/trust/protocol/authority generationとmaximum lifetimeへbindする | Must |
| PKI-019 | renewal、revocation、distrust、trust generation変更時にactive sessionをrevalidate/drain/fenceする | Must |
| PKI-020 | revocationをintent、local enforcement、distribution、propagation verificationへ分離しsequence/freshness/receiptを保持する | Must |
| PKI-021 | revocation stateがstale/UNKNOWNなprofile scopeでnew privileged sessionをfail closedにする | Must |
| PKI-022 | certificate単体に加えissuer/intermediate/algorithm/profile/namespaceをdistrustできsilent fallbackしない | Must |
| PKI-023 | Host identity compromise時にHost authority/session/credentialをfenceしInventory/Result/evidenceをquarantineする | Must |
| PKI-024 | credential revocation/Gateway disconnectをHost、storage client、Attachment/Port ownershipのfencing proofにしない | Must |
| PKI-025 | Control Plane identity compromise時にcertificate、endpoint、DB/Bus/Secret/backend credential、Lease/authorityを個別にcontainする | Must |
| PKI-026 | CA compromise時にcompromised chainと独立したrecovery authority/approvalでnew anchorをauthorizeしold issuerをdistrustする | Must |
| PKI-027 | normal CA rotationをdual TrustBundle、distribution receipt、canary/batch reissue、issuance switch、old anchor absence proofで行う | Must |
| PKI-028 | Secret Providerのcompletion claimだけでcredential active/revoked/rotatedを確定せずpublic trust/session stateを検証する | Must |
| PKI-029 | offline trust/bootstrap/revocation bundleへsignature、sequence、previous digest、expiry、approvalを要求しTOFU/rollbackを拒否する | Must |
| PKI-030 | PITR/DR後にrestore/trust generationでold session/Leaseをfenceしold Site/issuer/revocation stateを外部再検証する | Must |
| PKI-031 | Trust publish、issuance override、revoke/distrust、CA rollover、emergency recovery、Secret administrationを個別permission/approval/auditで保護する | Must |
| PKI-032 | trust/credential/session/revocation/rollover/offline/DR状態をsecret/raw identityを漏らさず観測・fault injectionできる | Must |

### 2.20 Agent Transport Multiplexing

| ID | 要件 | 優先度 |
|---|---|---|
| AGT-001 | 原則として 1 つの KIM Host Agent identity につき 1 つの current long-lived outbound mTLS session を Agent Gateway へ確立する | Must |
| AGT-002 | Command、Result、Inventory、Heartbeat、Observation、Control、Resync、credential renewal を同一 secure transport 上の typed logical message/stream として multiplex する | Must |
| AGT-003 | libvirt、Storage、OVS、SR-IOV、DPDK、PCI、Clock、Compliance 等の Agent module ごとに独立 mTLS connection または独立 Host certificate を要求しない | Must |
| AGT-004 | capability 分離を connection/certificate ではなく typed message、schema version、capability advertisement、authorization、Command/Lease authority で実施する | Must |
| AGT-005 | Agent module 追加だけを理由に Agent Gateway connection 数を増加させない | Must |
| AGT-006 | session generation を Host Agent transport session 全体の current authority とし、stale session の Result、Inventory、Observation、Command acknowledgment 等で current authority を進めない | Must |
| AGT-007 | transport connection loss を各 Agent module の resource authority loss とみなさず、既開始 operation を journal、UNKNOWN、read-back semantics で解決する | Must |
| AGT-008 | trust domain、security isolation、traffic/QoS、artifact transfer 等の独立要件がある場合だけ、明示 contract と approval 付きで別 endpoint/connection を許可する | Must |
| AGT-009 | transport は multiplexing、reconnect、bounded backoff/queue/message size、backpressure、session generation、idempotency、logical ordering contract を提供する | Must |
| AGT-010 | HTTP/2、gRPC 等の transport implementation detail を Agent capability/module contract から分離する | Must |
| AGT-011 | reconnect または credential rotation 中の bounded old/new session overlap でも current session generation を一つに保ち、old session を drain/fence する | Must |
| AGT-012 | L7 proxy が Agent mTLS を終端する profile では、Gateway は pinned proxy workload certificate と proxy が sanitize/rebuild した downstream certificate evidence の両方を検証し、untrusted peer の forwarded identity header を拒否する | Must |
| AGT-013 | HTTP/2 GOAWAY、proxy drain、rolling restart を transport signal として扱い、同一 generation の暗黙 rearm または PostgreSQL Session Grant を迂回した stream 再開を許可しない | Must |
| AGT-014 | L7 proxy の connection idle timeout と stream idle timeout を別 policy として扱い、connection idle を active Agent stream expiry に使用せず、stream idle reset を resource/session authority loss の証明にしない | Must |
| AGT-015 | Result、Observation、Inventory 等の再送対象 message を送信前に bounded durable spool へ記録し、同一 message identity/digest の durable Receipt を得るまで削除しない | Must |
| AGT-016 | Receipt response loss、Agent/Gateway restart、session generation 変更後も stable message identity で replay し、既 commit の同一 Receipt を回収して bounded resync checkpoint へ収束する | Must |
| AGT-017 | Receipt の session generation は最初に durable acceptance した generation の evidence として保持し、後続 session での replay に合わせて履歴を書き換えない | Must |

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
| NFR-SEC-006 | workload/Host PKIをpurpose/Site別trust domainとleast-privilege certificate profileで分離する |
| NFR-SEC-007 | credential issuance、renewal、revocation、distrust、CA rolloverを監査可能なlifecycleとして提供する |
| NFR-SEC-008 | credential/CA compromise時にaffected trust scopeをcontainし既存VMを不用意に停止しない |
| NFR-SEC-009 | offline/DR環境でもTOFU、default shared secret、trust generation rollbackを許可しない |
| NFR-SEC-010 | private key/secret valueをControl Plane DB、Event、Command、log、diagnosticへ保存・公開しない |

### Operability and Compatibility

| ID | 目標 |
|---|---|
| NFR-OPS-001 | オフライン環境へインストールできる |
| NFR-OPS-002 | N-1 から N へのアップグレードをサポートする |
| NFR-OPS-003 | API の破壊的変更には新しい major version を使用する |
| NFR-OPS-004 | 対応 OS、KVM/libvirt、OVN、Ceph の組合せをリリースごとに公開する |
| NFR-OPS-005 | 新しい Linux ディストリビューション対応に Control Plane の OS 条件分岐を必要としない |
| NFR-OPS-006 | deb、rpm、および検証用の自己完結型配布方式を用意する |
| NFR-OPS-007 | mixed-version期間を明示N/N-1 compatibility windowへ限定する |
| NFR-OPS-008 | upgrade中に既存VMを停止せず、Control Planeのserving capacityとauthority semanticsを維持する |
| NFR-OPS-009 | rollback可能点と不可逆なschema/artifact finalizationをreleaseごとに公開する |
| NFR-OPS-010 | API、Agent protocol、Command/Event、extension、backend/Hostのcompatibility matrixをreleaseごとに検証する |
| NFR-OPS-011 | canary failure、coordinator crash、response loss、rollback failureをfault injectionする |
| NFR-OPS-012 | offline upgradeでもartifact provenance、SBOM、signature、互換性検証を弱めない |
| NFR-OPS-013 | Control Plane を containerized artifact として導入できる |
| NFR-OPS-014 | Kubernetes を Control Plane の必須実行基盤とせず、小規模・offline 環境向けの非 Kubernetes deployment を許可する |

### Robustness and Failure Semantics

| ID | 目標 |
|---|---|
| NFR-ROB-001 | 全failure classについてDetect、Contain、Fence、Observe、Recover、Reconcile、Escalateを定義する |
| NFR-ROB-002 | timeout、Lease expiry、通信断をbackend mutation失敗または未実行の証明として扱わない |
| NFR-ROB-003 | UNKNOWN outcomeの履歴を上書きせず、verification evidenceと後続decisionを追記する |
| NFR-ROB-004 | stale identity、generation、Lease、Result、observationがcurrent authorityを進めない |
| NFR-ROB-005 | recovery不能時はresourceをblocked/quarantinedに保ち、推測ベースの破壊操作を行わない |
| NFR-ROB-006 | commit応答喪失、partition、process crash、Host loss、backend timeout、stale authorityをfault injectionで検証する |
| NFR-ROB-007 | clock skew、step、slew、uncertainty、reboot/failover/PITRをauthority failureとして安全に処理する |
| NFR-ROB-008 | timestampや期限だけでordering、fencing、side effect不在を推測しない |

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
