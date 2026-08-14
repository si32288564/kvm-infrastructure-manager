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
| HLC-023 | Enrollment Decision を immutable evidence と current binding に分離し、manual approval、quarantine、decommission の変更を Host identity と session authorization へ generation 付きで反映する | Must |
| HLC-024 | Agent Credential Binding を Host、certificate fingerprint、public key、issuer/profile、trust generation、Enrollment Decision、validity interval へ bind し、renewal/rekey を新 revision として保持する | Must |
| HLC-025 | Session Authorization を current Enrollment、Credential Binding、transport session generation、Host capability generation の全てへ bind し、不足または不一致時は PENDING、STALE、FENCED とする | Must |
| HLC-026 | mTLS authentication、current Credential Binding、current Session Authorization、current capability だけでは Host mutation authority を発行しない | Must |
| HLC-027 | Host Operation Authority は Enrollment、Credential、session、capability、Baseline Assignment、preflight、Compliance、policy を一 transaction で検証した明示 arming だけで generation を進め、依存 generation 変更は fence のみ行う | Must |
| HLC-028 | UpgradeとMaintenanceのtyped disruptive Host operationを共通Host-scoped claimで排他し、Lease expiryやprocess lossをside effect不在または別domain mutation許可の証明にしない | Must |

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
| HGR-018 | HostGroup本体のgenerationとmembership set generationを分離し、完全なmember set evidenceの検証完了後だけcurrent setとcurrent member projectionを同一PostgreSQL transactionで不可分に切り替える | Must |
| HGR-019 | Group Membership Snapshotをlive membership rowではなくaccepted membership set generationとcanonical digestへbindし、後続set変更から不変に保つ | Must |
| HGR-020 | cardinality policyをgroup type、dimension、level、scopeのversioned authorityとして管理し、complete set publishを同scopeのsibling HostGroups全体でtransactionally直列化・検証する | Must |
| HGR-021 | closed typed Selectorをversioned authorityとして管理し、current normalized Host evidenceからimmutable proposal evidenceを作成後、current Selector/input/Cardinality/Hierarchy/HostGroup generationを再検証したcomplete Membership Setだけをatomic materializeする | Must |
| HGR-022 | HostGroup対象Upgradeをpurpose=UPGRADEのimmutable Membership Snapshotへbindし、Plan/Wave/TargetをSnapshot member evidenceから生成してlive membership変更・Coordinator recovery・Campaign resumeでtarget setを再生成しない | Must |
| HGR-023 | HostGroup対象Maintenanceをpurpose=MAINTENANCEのimmutable Membership Snapshotへbindし、独立Plan/Wave/typed Targetをatomic publishしてlive membership変更・Coordinator recovery・pause/resumeでtarget setを再生成しない | Must |
| HGR-024 | HostGroup membershipとpolicy associationを分離し、exact Group/Policy revision付きtyped Group Policy Bindingをpriorityで解決する。同priority非互換はASSIGNMENT_CONFLICT、stale highest assignmentはlower priorityへfallbackせずconsumerをBLOCKEDにする | Must |
| HGR-025 | Placement Requestをfirst-class versioned Placement Scopeへbindし、exact PLACEMENT_POOL HostGroup generationからcurrent accepted Membership Setを解決する。visibility、eligibility、Final Admissionを分離し、Scope/Set drift時は全claimをrollbackする | Must |
| HGR-026 | closed complete-set External Assertionをpurpose-limited issuer trust、Ed25519 signature、audience、freshness、nonce、payload/HostGroup/Host evidenceで検証し、VERIFIED evidenceとexplicit materializationを分離する。current issuer/Group/Cardinality/Hierarchy/Set generationを再検証したaccepted complete Membership Setだけをauthorityにする | Must |

### 2.5 Availability Responsibility and Managed Recovery

| ID | 要件 | 優先度 |
|---|---|---|
| AVR-001 | immutable versioned AvailabilityPolicyとresponsibility、Host failure action、fencing/storage/recovery/failure-domain条件を管理する | Must |
| AVR-002 | responsibilityをINFRASTRUCTURE_MANAGED、WORKLOAD_MANAGED、MANUALに分類する | Must |
| AVR-003 | Host failure actionをRESTART_ON_OTHER_HOST、EVACUATE、NO_AUTOMATIC_ACTIONに分類し、responsibilityとの不正な組合せを拒否する | Must |
| AVR-004 | AvailabilityPolicyをPLACEMENT_POOLだけからversioned GroupPolicyBindingで参照する | Must |
| AVR-005 | binding欠損、stale、同priority conflictでHost Effective Availability Policyが一意に解決できないHostをREADY/Placement不適格にする | Must |
| AVR-006 | Final AdmissionでPolicy/Pool/membership generationをVM/Allocationのimmutable AvailabilityBindingへ保存する | Must |
| AVR-007 | Group/Policy変更だけで既存VMのAvailabilityBindingを変更せず、exact source Bindingとexact target Policy revision、actor/approval/reasonへbindした明示Rebind Decisionだけが新Binding revisionとcurrent pointerを不可分に進める | Must |
| AVR-008 | Host/VM failure observationをclosed typed append-only evidenceとして保持し、Failure Epochをexact VM Availability Binding/Policy/Admission/allocation/source Host provenanceへbindする。signalとconfirmation/fencing/recoveryを分離しUNKNOWNをconfirmedへ縮退させない | Must |
| AVR-009 | WORKLOAD_MANAGEDではFault/Eventを通知するがKIMから自動restart、evacuate、replacementを開始しない | Must |
| AVR-010 | MANUALではauthorized Manual Recovery DecisionまでKIMから自動VM mutationを開始しない | Must |
| AVR-011 | INFRASTRUCTURE_MANAGED recoveryでsource fencing、storage single-writer、VM/resource eligibility、failure-domain、transactional admissionを必須とする | Must |
| AVR-012 | fencing、attachment、resource ownership、Availability BindingのいずれかがUNKNOWNならautomatic recoveryを開始しない | Must |
| AVR-013 | Recovery Operationをcanonical Failure Campaign、VM、Availability Binding revision、actionで冪等化し、stale Campaign/epoch/resultをfenceする | Must |
| AVR-014 | EVACUATEをHost-scoped planからVM単位Operationへ分解し、部分成功、capacity不足、個別BLOCKEDを表現する | Must |
| AVR-015 | recovery destinationでcurrent Placement Pool/Policy compatibility、Compliance、capacity、Failure Domainを再評価しsilent fallbackしない | Must |
| AVR-016 | Host failure/recovery Eventをresponsibilityにかかわらずdurableに通知し、delivery failureでresponsibilityを変更しない | Must |
| AVR-017 | exact AvailabilityPolicy revisionからclosed typed FailureConfirmationPolicy revisionを参照し、exact Epoch/Policy/Evidence snapshotのimmutable Evaluationとexplicit Decisionを分離する。UNKNOWN/STALE/CONFLICTINGはCONFIRMEDへ進めず、accepted DecisionだけがSUSPECTED→CONFIRMED transitionを不可分にcommitし、fencing/recovery authorityを生成しない | Must |
| AVR-018 | CONFIRMED Failure Epochからexact typed FencingPolicyとStorageSafetyPolicyを独立に評価し、positive Fencing ProofとLocal LVM Storage Safety Proofだけをimmutableにmaterializeする。heartbeat/Agent loss、UNKNOWN、policy/evidence driftをpositive proofへ昇格させず、両proofが揃ってもRecovery Eligibility/Operationまたはruntime/resource mutationを生成しない | Must |
| AVR-019 | FENCED Failure Epochのexact historical Availability Binding/Policy、current-usable Fencing/Storage Proof、closed typed RecoveryBudgetPolicy、read-only destination snapshotをimmutable Recovery Eligibility Evaluationへ固定する。Evaluationとexplicit Decisionを分離し、positive DecisionとGLOBAL/PLANNING Budget Claimだけを一transactionでcommitする。ELIGIBLE/ClaimはRecovery Operation、Placement Admission、Job/Command/Lease、resource/runtime mutationではない | Must |
| AVR-020 | exact Eligibility Decision/Budget Claimからexplicit Recovery Operation Requestとimmutable one-destination Planを作成し、start時にEpoch/responsibility/Fencing/Storage/Budget/destinationを再検証する。RESTART_ON_OTHER_HOST startはsource compute accounting release、ordinary destination Final Admission、Budget RESERVED→CONSUMED、closed typed preparation Commandをatomic commitし、RUNNING/Command successをRecovery VERIFIEDへ昇格させない | Must |
| AVR-021 | `RESTART_ON_OTHER_HOST` はexact destination Admissionと既存VM/Image/Local LVM/Attachment/Network/libvirt Power authorityを再利用してsame workload identityの新materialization incarnationを作る。power直前にFencing/Storage/Budget/destinationを再検証し、UNKNOWNはblind restartせずBudgetをCONSUMEDに保持する。exact multi-domain read-backのimmutable Recovery Verificationをexplicit Terminal Decisionがacceptした時だけ、Operation VERIFIED、Failure Epoch RECOVERED、Budget RELEASEDをone transactionでcommitする | Must |
| AVR-022 | Source boot-root Recovery safetyはFailure Epochからexact source Admission/materialization、`vda`、Volume、Binding、LV、Attachmentを導出し、actual `SHUTOFF/MATCHED`、configured root identity、current `BOUND`、holder closedをimmutable Evaluation/Proofへ固定する。secondary proofをrootへ代用せずroot mutationを許可しない。FencingとRoot Safetyだけがexact source incarnationのlogical retirementを許し、composite Storage Safety、Eligibility、Operation start、dangerous-stepはpower/holder/Binding/materialization driftを再検証する | Must |
| AVR-023 | real Recovery helperはPostgreSQL Control Planeが事前に発行したexact Command/Lease/Attempt capabilityのみをstdinでconsumeし、capability-free Result/ObservationのHost、Lease/session/authority generation、Command type/schema/target/payload digestが全て一致する場合だけordinary Result/Verification/Receipt transactionへ受理する。raw tokenをstdout/log/journal/reportへ保存しない | Must |
| AVR-024 | real two-Host Recovery qualificationは一つのPostgreSQL historyでactual source failure、typed read-only source-root observation、Fencing/Storage Proof、Eligibility、Operation、destination materialization、Verification、Terminal Decisionを結ぶ。FENCED Hostのread-only Leaseはclosed source-root schemaだけに限定しrearm/mutationを許さず、accepted Terminal DecisionだけがOperation VERIFIED、Epoch RECOVERED、Budget RELEASEDをatomic commitする | Must |
| AVR-025 | planned Host EVACUATEをFailure Recoveryと分離し、Failure Epoch、Confirmation、Fencing Proof、Recovery Budgetを作成・偽装・再利用しないfirst-class Host Evacuation Operationとして管理する | Must |
| AVR-026 | EVACUATE開始時にsource Hostをnew Placementからatomicにdrainし、current PostgreSQL authorityから全managed VMのgeneration、Admission、materialization plan、Availability Binding、Network/Storage/PCI requirementをimmutable workload setへsnapshotする | Must |
| AVR-027 | Host Evacuation parentをorchestration authority、workload childをmutation authorityとして分離し、parentがbackendを直接操作せず、DB-backed slot claimで同時quiescence/relocation数をhard limit以下に保つ | Must |
| AVR-028 | planned source quiescenceはexact VM/source Host/materializationへbindしたtyped shutdownとSHUTOFF read-backを要求し、Storage safety、Network retirement、PCI retirementを独立に再検証する。quiescence evidenceをFencing Proofへ昇格させない | Must |
| AVR-029 | child destinationはordinary current Placement/Final Admissionからsource Host以外を選択し、plan drift時はexplicit generation付きreplanを要求する。source activeまたはsource component retirement未完了のままdestination powerを許可しない | Must |
| AVR-030 | per-child outcome、partial success、cancel-before-quiescence、restart/resume、same-request replayをPostgreSQL authorityから再構築し、一child BLOCKEDで既VERIFIED childをrollbackしない | Must |
| AVR-031 | planned evacuation中にsource Host authorityが失効した場合、remaining childをRECOVERY_REQUIREDとして停止し、silent Recovery conversionを行わない。通常のFailure Observation→Epoch→Confirmation→Fencing→Recoveryだけが後続を所有する | Must |
| AVR-032 | parent VERIFIEDは全snapshot workload VERIFIED、source active workload 0、post-drain Admission 0、drain継続を要求する。generic cleanup failure/未実施とobsolete artifact残存はterminalをrollbackせず、undrainは別のexplicit authorityとする | Must |
| AVR-033 | evacuation/workload/slot/duration/replan/UNKNOWN/recovery-escalationをbounded aggregate metricsとimmutable audit evidenceで観測し、raw VM/Host identityをunbounded metric labelへ使用しない | Must |

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
| IMG-004 | supported format の verified Image revision を identity-verified root Volume へ closed typed operation で materialize し、destination の bounded content identity read-back が current revision、Volume、Binding generation と一致した場合だけ Image realization authority を進める。Developer Preview の Local LVM direct-copy profile は RAW のみに限定し、QCOW2 は certified typed conversion/verification が入るまで fail closed とする | Must |
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
| CMP-010 | VM power-state mutation を closed typed Command と標準 libvirt API に限定し、Agent process lossで Result が不明な場合は Domain UUID/state の read-back evidence で解決する | Must |
| CMP-011 | VM materialization は accepted Final Admission と current Compute/Volume Binding/Attachment Claim から immutable plan と Job/Command を不可分生成し、closed typed libvirt define と inactive Domain read-back で収束する | Must |
| CMP-012 | VM materialization readiness は Domain、Image、Network、Storage を独立状態として評価し、全required componentのcurrent typed evidenceが揃うまでboot/power-on authorityをfail closedにする | Must |
| CMP-013 | Image materialization 成功だけでは VM を boot ready にせず、current Network realization evidence が未確定なら `BLOCKED` を維持する | Must |
| CMP-014 | current VM/Plan と全 required component/Port evidence を同一 PostgreSQL transaction で再検証し、Boot Readiness `READY` と typed power-on Job/Command authority を不可分に生成する | Must |
| CMP-015 | typed power-on の成功は標準 libvirt read-back の immutable evidence と current VM/runtime generation が一致した場合だけ current power projection を `MATCHED` へ進め、`RUNNING` を dataplane/guest readiness と同一視しない | Must |
| CMP-016 | backend cleanupをRecovery Terminalから分離したgeneric exact-incarnation authorityとし、Recovery由来ではaccepted Terminal、RECOVERED Epoch、VERIFIED Operation、logical source retirementを全て要求する | Must |
| CMP-017 | destructive Domain cleanupはexact source Host/VM/plan/materialization generationへbindしたclosed typed `VIRTUAL_MACHINE_UNDEFINE/v1`だけを許し、SHUTOFFとKIM metadata identityを確認後に標準libvirt APIで実行する | Must |
| CMP-018 | cleanup timeout、response loss、Agent loss、Lease expiryをabsenceへ縮退させず、`DISPATCH_UNKNOWN`からsuccessor `READ_BACK_FIRST`をobservation-only typed Commandへ限定する。exact physical absenceならVERIFIED、exact inactive presentなら同じClaimのimmutable read-back後にだけ別apply authorityを発行する | Must |
| CMP-019 | immutable cleanup eligibility/Attempt/Observation/Terminal evidenceとper-artifact current projectionを分離し、cleanup BLOCKED/UNKNOWN/CONFLICTINGでRecovery VERIFIED/Epoch RECOVEREDを巻き戻さない | Must |
| CMP-020 | Local LVM capacityはexact LV physical absenceとdata-independence policyが証明されるまで再利用せず、logical Port/MAC/IPおよびhistorical PCI Claim/retirement evidenceをsource cleanupで削除しない | Must |
| CMP-021 | Network source retirementのexact immutable NB/SB/source-OVS absence evidenceは、後続HandoffでPort-wide current projectionが進んだ後も追加mutationなしの`ALREADY_ABSENT` cleanupとして利用できるが、logical LSPまたはdestination dataplaneの削除authorityへ昇格させない | Must |
| CMP-022 | generic cleanupはorigin-specific current projectionを直接hard-codeせず、各Recovery/Materialization/Delete producerが自身のclosed authorityを検証してimmutable origin eligibility adapterを発行する。未実装producerはadapterを発行せずfail closedにする | Must |

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
| NET-036 | Network Authority、pre-boot realization、post-boot dataplane convergence を独立状態として保持し、Port RESERVED や NIC realization を dataplane convergence とみなさない | Must |
| NET-037 | OVS pre-boot realization は Agent 管理の Segment-to-Bridge mapping、standard OVS bridge observation、inactive libvirt NIC identity の typed read-back で確定し、Command から bridge 名/XML/path/argv を受け取らない | Must |
| NET-038 | SRIOV_DIRECT pre-boot realization は current exclusive VF Claim、PCI observation、Qualification Binding、validated `VF_ASSIGN` operation、allocation policy と Binding generation を再検証し、typed libvirt PCI identity read-back だけを Port realization evidence へ昇格する | Must |
| NET-039 | OVS post-boot dataplane convergence は current RUNNING VM、pre-boot Port evidence、Network/Segment/Mapping/Binding generation と、active libvirt NIC target・OVS bridge・link state の typed read-back が一致した場合だけ `CONVERGED` に進め、end-to-end reachability、OVN convergence、Guest readiness を主張しない | Must |
| NET-040 | OVN Port intent、NB materialization、SB datapath/chassis realization を immutable evidence と独立 current state で管理し、apply response loss は stable KIM ownership marker、intent generation、object digest の read-back で解決する。SB realization を Host dataplane、end-to-end reachability、Guest readiness へ暗黙昇格しない | Must |
| NET-041 | OVN SB logical-flow pipeline と current Port identity coverage、Chassis/Encap registration を SB Port Binding、Host mapping、intent generation に結び付く独立 immutable evidence として評価する。logical-flow/Encap readiness だけで Host programming、cross-chassis tunnel traffic、end-to-end reachability を主張しない | Must |
| NET-042 | cross-chassis Geneve packet path は、異なる current Host/Port/chassis authority、current mapping generation、両端 tunnel interface identity、方向付き packet probe の immutable evidence から評価する。Encap registration または control-plane convergence だけで tunnel traffic を証明せず、tunnel verification から tenant L3 reachability、Guest readiness、application health を主張しない | Must |
| NET-043 | Automatic IPAM は dry Eligibility で候補の存在だけを無副作用に評価し、Final Admission の同一 PostgreSQL transaction 内で current Subnet generation、除外範囲、競合 Claim を再検証して具体的な IP/MAC Claim を確定する | Must |
| NET-044 | Network identity の release request、timeout、単一 observation を absence の証明にせず、current Port/Binding/OVN NB/SB/Host absence の独立した連続 evidence が揃うまで `RELEASE_PENDING` または `QUARANTINED` として再利用を禁止する | Must |
| NET-045 | OVN Logical Switch の ownership を Network ID/generation、Logical Switch Port の ownership を Port intent ID/generation/digest に分離し、同一 Network の複数 Port realization が共有 Logical Switch marker を上書きしない | Must |
| NET-046 | Production OVN adapter は current Host mapping の OVN Chassis reference と immutable typed plan だけを受け取り、標準 `ovn-nbctl`/`ovn-sbctl`、`unix:` または authenticated `ssl:` DB endpoint、bounded timeout、ownership pre-read、apply後 read-back を強制する | Must |
| NET-047 | OVN runtime work は PostgreSQL-backed bounded claim で multi-worker 間を排他し、claim expiry を非実行証明にせず immutable `DISPATCH_UNKNOWN` evidence を残す。再取得 worker は apply 前に同一 intent の typed read-back を行い、current owner/claim generation だけが observation と current projection を進める | Must |
| NET-048 | long-running OVN adapter operation の claim renewal は current owner/generation、未失効、DB authority time、maximum lifetime を一つの PostgreSQL transaction で検証し、immutable renewal evidenceを残す。expired claimをrenew/reviveせず、renewal outcomeが不明ならexpiry後の`READ_BACK_FIRST`で解決する | Must |
| NET-049 | claim renewal の commit 後に response を失った worker は adapter operation を停止し、renewal の成功・失敗または side effect 不在を推測しない。PostgreSQL に commit 済みの renewed expiry までは別 worker の takeover を禁止し、expiry 後だけ新 claim generation の `READ_BACK_FIRST` で解決する | Must |
| NET-050 | OVN runtime worker は一括取得した claim を未更新の local serial queue に滞留させず、`BatchLimit` を process 内の同時実行上限として各 work の authority check と renewal loop を直ちに開始する。item-local adapter error は観測可能に報告して bounded poll を継続するが、DB claim/renewal authority error は process を停止する。deployment profile は全 replica の aggregate in-flight claim 数を PostgreSQL/backend capacity 以下に制限し、意図的 failure 数を超える retry amplification を qualification で拒否する | Must |
| NET-051 | 同一 Site の PostgreSQL HA は primary/standby の役割を繰り返し切り替えても `restore_epoch` と database authority generation を変更せず、各 failover 前に synchronous `remote_apply` された work/renewal evidence を保持する。各 old-primary worker を停止し、renewed expiry 後の successor を新 claim generation の `READ_BACK_FIRST` に限定して duplicate backend mutation を許可しない | Must |
| NET-052 | OVN runtime worker は PostgreSQL connection pool を明示的に bounded とし、process-local `database-max-connections` を少なくとも `2 × BatchLimit` として claim/renewal/completion 用 headroom を確保する。deployment profile は measured pool wait と OVN endpoint uncertainty に対して claim Lease/renewal interval/maximum lifetime を qualification し、slow endpoint、partial timeout、pool wait を side effect 不在または claim expiry の証明へ昇格させない | Must |
| NET-053 | OVN runtime worker の scale down は graceful drain を使用し、`DRAINING` 後の新規 claim を停止する一方、current batch は bounded drain deadline 内で renewal、typed apply/read-back、completion を継続する。drain deadline 超過または 2 回目の termination signal だけを hard cancellation とし、残る曖昧な claim は expiry 後の `READ_BACK_FIRST` へ送る | Must |
| NET-054 | hard drain は process の非正常終了として観測可能にし、current claim の side effect 有無を終了コードから推測しない。deadline 超過または 2 回目の signal 後も current claim を即時再利用せず、DB expiry、immutable `DISPATCH_UNKNOWN`、successor generation の `READ_BACK_FIRST` を経て収束する | Must |
| NET-055 | logical Port/MAC/IP ownershipを保持したHost binding retirementをclosed typed `UNBIND` workとして実行し、exact Port/Binding/Host generationとKIM ownership markerを再検証する。NB LSP ownership維持、source SB binding inactive、source OVS iface-id absentのimmutable evidenceが揃うまで`VERIFIED`にせず、response lossは`DISPATCH_UNKNOWN → READ_BACK_FIRST`で解決する | Must |
| NET-056 | 同一logical PortはHandoffごとのexact `(Port generation, Binding generation)` retirement projectionを独立して保持し、strictly-later incarnationの`UNBIND`を許可する。historical operation replayは元のwork/evidenceへ収束し、旧retirement/quiescenceを新incarnationへ流用せず、latest projectionをhistorical authorityの代用にしない | Must |

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
| DPL-016 | PCI/IOMMU/SR-IOV の Observed Evidence、Normalized Device Projection、Qualification Evidence、Current Qualification Binding、Allocation State を別 authority として管理する | Must |
| DPL-017 | sysfs で device/PF/VF を観測できても、current immutable Qualification Evidence がない device を allocation candidate に昇格しない | Must |
| DPL-018 | Qualification Evidence を observation generation/digest、device/firmware/driver/kernel/IOMMU/libvirt/QEMU profile、evaluator/test artifact digest、validated operation set に binding する | Must |
| DPL-019 | current binding を CURRENT、STALE、UNKNOWN、REVOKED で評価し、CURRENT 以外の allocation state を BLOCKED とする | Must |
| DPL-020 | VF Final Admission は Host capability generation、device observation、PF/VF relationship、Qualification Binding、policy、NUMA/IOMMU constraint、既存 claim を同一 PostgreSQL transaction で再検証する | Must |
| DPL-021 | VF release は exact allocation/Port/Binding/VM incarnation に対する closed typed `PCI_VF_RETIRE/v1` と libvirt/sysfs read-backを使用し、Command successまたはLease expiryだけで再利用可能にしない | Must |
| DPL-022 | source VF retirement は workload/Port identityを削除せず、inactive hostdev absence、driver release、holder absence、IOMMU identityをimmutable evidenceとして確定する | Must |
| DPL-023 | VF handoff は verified source retirementとordinary destination Final Admission claimを結合し、source/destination BDFが同一であることを要求しない | Must |
| DPL-024 | Recovery dangerous-step/Verificationはcurrent VF retirement/handoff/destination SR-IOV realization evidence setを再検証し、stale/UNKNOWN/ABA時はfail closedにする | Must |

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
| STO-028 | Local LVM create は closed typed Command と KIM-owned LV key のみを使用し、実 LVM read-back の VG/LV UUID evidence が一致した場合だけ current Backend Binding を BOUND にする | Must |
| STO-029 | Local LVM attach/detach は closed typed libvirt Command を使用し、current BOUND Binding、SINGLE_WRITER Claim、Domain device identity、LVM open-holder evidence が一致した場合だけ ATTACHED/DETACHED と Claim transition を確定する | Must |

### 2.15 Operation、Event、Notification

| ID | 要件 | 優先度 |
|---|---|---|
| OPS-001 | Host/backend realizationまたは時間を持つconvergenceを伴う変更 API は Operation ID を返し、非同期に完了できる | Must |
| OPS-002 | Operation の状態、進捗、失敗理由、相関 ID を照会できる | Must |
| OPS-003 | 一時障害を分類し、上限付きで安全に再試行できる | Must |
| OPS-004 | Webhook または Event Stream で状態変更を通知できる | Should |
| OPS-005 | Operator が安全な Operation を再実行または中止できる | Should |
| OPS-006 | Operation と実行配送を分離し、Job、Command、Lease、Attempt を永続化する | Must |
| OPS-007 | Command Lease は期限、owner、token、attempt index、authority generation を持つ | Must |
| OPS-008 | Agent は Command を実行する前に durable journal へ記録する | Must |
| OPS-009 | Execution Outcome の UNKNOWN を FAILED と区別し、stale result を fencing できる | Must |
| OPS-010 | 成功 Result だけで Operation を成功にせず、後続 observation で desired state を検証する | Must |
| OPS-011 | Command Lease は発行時の Host authority generation と current Agent session generation の両方へ bind し、いずれかの失効後に旧 Lease を再利用できない | Must |
| OPS-012 | 同一 Result の再送は durable receipt で冪等化し、異なる digest または stale Attempt の Result は current authority を変更しない | Must |
| OPS-013 | Dispatcher は PostgreSQL で grant された current Host/session generation の Lease だけを current Agent stream へ配送し、transport send failure を未配送または未実行の証明にしない | Must |
| OPS-014 | Agent execution module は closed typed backend registry、write-before-execute journal、typed read-back を使用し、arbitrary command/path/backend method を受理しない | Must |
| OPS-015 | mutation 後に Result が不明となった Command は、current authorized Agent session へ immutable Command/Attempt identity を持つ read-only Verification Request を配送し、既存 journal evidence と backend read-back によって収束できる | Must |
| OPS-016 | long-running Agent session runtime は inbound Command、outbound Result/Observation、durable Receipt を同一 current transport 上で並行処理し、一つの loop 障害を resource authority loss または未実行の証明として扱わない | Must |
| OPS-017 | Host Agent process は PostgreSQL で SessionAccepted された generation だけを local durable ledger へ記録し、rejected/failed attempt では generation を消費せず、process restart 後は最後の accepted generation の次だけを提案する | Must |
| OPS-018 | Worker process は elapsed Command Lease を bounded batch で検出し、各 Command を Host-scoped PostgreSQL transaction で再検証して UNKNOWN へ進め、expiry を non-execution proof として扱わない | Must |
| OPS-019 | production Command Lease Grant は raw Lease token を DB/Outbox に保存せず、Secret Provider key revision と grant identity に bind した protected capability を含む durable delivery intent を Lease authority と同一 PostgreSQL transaction で commit する | Must |
| OPS-020 | Worker は protected Command delivery Outbox を bounded claim し、stable message ID を持つ internal NATS JetStream message として publish する。JetStream PubAck は Bus の durable acceptance だけを証明し、Gateway delivery、Agent receipt、backend execution を証明しない | Must |
| OPS-021 | Agent Gateway は internal Bus message を PostgreSQL Inbox で dedupe し、current Lease、Host authority generation、session generation、credential/readiness/capability binding を再検証してからだけ current Outbound Registry へ route する | Must |
| OPS-022 | 同一 message ID/digest の Bus redelivery は current authority を再検証した上で stable Agent envelope を再 route できる。異なる digest は quarantine し、live session 不在または route outcome 不明は ACK せず redelivery へ戻す | Must |
| OPS-023 | internal JetStream profile は replicated stream と durable consumer を使用し、stream leader または Gateway consumer の停止後も stable message ID/digest を維持して redelivery する。leader election、Bus ACK、consumer restart は新しい Lease、Attempt、mutation authority を生成しない | Must |
| OPS-024 | TLS/credential 付き internal Bus、Worker、Gateway、Host Agent、PostgreSQL を process 分離した fault campaign で、Bus leader/Gateway/Agent の停止後も current generation へ明示的に再収束し、stale Lease を Agent へ渡さず backend side effect を発生させない | Must |
| OPS-025 | Gateway が current Agent stream へ Command を write した後、internal Bus ACK 前に停止しても、redelivery を新しい Lease/Attempt/backend side effect へ昇格させず、PostgreSQL current authority と既存 Result/Receipt evidence から terminal convergence する | Must |
| OPS-026 | UNKNOWN Command の read-only Verification を current authorized session generation へ bind した durable Outbox/Inbox message として配送し、Host mutation authority の rearm を要求または発生させない | Must |
| OPS-027 | Lease expiry 後に replay された旧 Result は current authority を変更せず、PostgreSQL-backed `STALE` Receipt を返す。Agent は該当 spool evidence を解放せず、Receipt disposition を transport failure に昇格させず current session 上の read-only Verification を継続する | Must |

### 2.15.1 Northbound Resource Lifecycle and IaC

| ID | 要件 | 優先度 |
|---|---|---|
| IAC-001 | Northbound persistent resource は stable resource type/ID、scope、resource revision、desired/computed projection を持ち、display name や physical incarnation を identity にしない | Must |
| IAC-002 | OpenAPI 3.1 の HTTP schema と versioned lifecycle semantic metadata を共通 KIM Resource Contract とし、API、Terraform Provider、UI が mutability、replacement、computed、sensitive、import、async/delete semantics を独自に発明しない | Must |
| IAC-003 | create mutation は principal/project/method/canonical path scope の Idempotency-Key と canonical desired digest を Operation/resource identity へ不可分に bind し、response loss 後に read-back できる | Must |
| IAC-004 | update/delete は current resource revision の `ETag` と `If-Match` 相当を要求し、stale client mutation を fail closed に拒否する | Must |
| IAC-005 | public field を REQUIRED_DESIRED、OPTIONAL_DESIRED、COMPUTED、IMMUTABLE、SENSITIVE、OPERATION_ONLY、INTERNAL_ONLY へ machine-readable に分類し、absent、null、zero value を schema どおり区別する | Must |
| IAC-006 | current Host、exact CPU/NUMA/HugePage/PMD/RxQ、socket/interface/backend UUID、PCI BDF/IOMMU、LV UUID/path、Materialization/Recovery/EVACUATE generation を desired configuration または replacement trigger にしない | Must |
| IAC-007 | Recovery、EVACUATE、Drain、Retry、Cancel、Read-back、Cleanup、Reconciliation、diagnostic/qualification を persistent resource CRUD から分離した Operation として公開する | Must |
| IAC-008 | Operation は stable ID、target resource/revision、requester/authorization、type、accepted time、phase、terminal state、stable error/retryability、cancel semantics、immutable history、polling、retention を持つ | Must |
| IAC-009 | delete は protection、dependent conflict、cascade policy、asynchronous terminal/tombstone、response-loss read-back を resource ごとに定義し、backend deletion や request acceptance だけで state を消さない | Must |
| IAC-010 | import は KIM stable logical ID と authorized Read projectionからだけ構成し、backend-only objectや Host-local incarnationを暗黙 adoptしない | Must |
| IAC-011 | Recovery/EVACUATE/reconciliation により physical incarnation が変わっても logical desired fields が同じなら Terraform refresh は replacement または desired drift を生成しない | Must |
| IAC-012 | API error は validation、authorization、not found/tombstone、stale revision、dependency/allocation conflict、operation in progress、transient unavailable、backend UNKNOWN、terminal failed と retryability を stable machine-readable code で区別する | Must |
| IAC-013 | Northbound automation principal は外部 IdP の machine identity を使用し、Project/Site/resource/action scope、read/write/admin separation、credential rotation、audit actor、destructive protectionを評価する。backend/Host Agent credentialを取得しない | Must |
| IAC-014 | backend-independent Security Policy desired model を selector、direction、protocol/service/port、statefulness、action、priority、logging policy として定義し、raw OVN ACL syntaxを通常の public desired fieldにしない | Must |
| IAC-015 | PostgreSQL resource authority commitだけで完了するmutationは201/200/204の同期応答を許容し、backend realizationを伴うmutationは202+Operationとする。形式上だけのOperationを作らない | Must |
| IAC-016 | 複数Northbound resourceはprincipal、error、request ID、ETag/If-Match、cursor、audit、lifecycle metadata規約を共有しつつ、resource固有revision、dependency、delete、consumer semanticsをgeneric handlerへ隠さない | Must |
| IAC-017 | SYSTEM Availability Policyのclosed non-automatic intentをstable ID、immutable revision、ETag/idempotency、exact workload dependency、retirementでNorthbound管理し、runtime Recovery authorityをdesiredへ混入しない | Must |
| IAC-018 | Image metadataとartifact observation/ingestionを分離し、caller supplied observed digestをImage authorityへ昇格しない | Must |

### 2.16 Fault、Performance、Audit

| ID | 要件 | 優先度 |
|---|---|---|
| O11Y-001 | Prometheus 形式で製品メトリクスを公開できる | Must |
| O11Y-002 | Host、VM、Control Plane のアラームを管理できる | Must |
| O11Y-003 | OpenTelemetry trace context をサービス間で伝播する | Should |
| O11Y-004 | OVN runtime worker は bounded Prometheus metrics として lifecycle state、claim/in-flight/completion、claim age、renewal latency/headroom/error、drain duration、PostgreSQL pool pressure、`PENDING/CLAIMED/DISPATCH_UNKNOWN/OBSERVED` backlog を公開し、Host/Port/Work ID、secret、生 backend error を label または value に含めない | Must |
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
| UPG-028 | KIM-owned component package replacement は Target component identity、verified package digest、administrator-owned closed backend profile に bind し、package version、service state、configured executable path、running binary digest、typed health schema/process identity の current read-back が一致した場合だけ Target を成功へ進める | Must |
| UPG-029 | package database lock contention、package manager interruption、または package operation response loss を「未適用」の証明にせず、prior Attempt を `UNKNOWN`、successor を `READ_BACK_FIRST` として current package status/evidence から再評価する | Must |
| UPG-030 | unpacked / half-configured / triggers-pending 等の不完全 package status を `CONFLICTING` evidence として Target authority を terminal `FENCED` にし、明示 recovery plan なしの再 install、configure、restart、rollback、再 claim を禁止する | Must |
| UPG-031 | quarantined package の recovery を通常 Upgrade Attempt と分離した immutable Plan、明示 authorization、closed strategy、Recovery Attempt/Lease、typed read-back、Verification、明示 rearm で実行する。`CONFIGURE_EXISTING` は固定 package identity だけを configure し、汎用 `dpkg --configure -a`、任意 package/argv、reinstall、downgrade、rollback を含めない | Must |
| UPG-032 | HostGroup-targeted Plan revisionをexact immutable UPGRADE Snapshot identity/digestへbindし、TargetからWave/Plan/Snapshot/Membership Set/member evidenceまで監査可能にする。Host eligibility driftはTarget evidenceを削除せずexecutionをfail closedにする | Must |

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

| ID | Image Phase 1 Must requirement |
|---|---|
| FR-IMG-001 | logical Image revisionはexpected digestだけをcallerから受け、observed digestを受けない |
| FR-IMG-002 | ingestionはapproved source registryからclosed typed commandでstaging、fsync、whole-artifact read-backを行う |
| FR-IMG-003 | expected/observed digest一致のimmutable verificationだけがImage revisionをmaterialization catalogへpublishする |
| FR-IMG-004 | response lossはUNKNOWN/read-back firstで収束し、same artifact generationを別contentで上書きしない |
| FR-IMG-005 | Image deletion/deprecation、Host cache cleanup、既存VM exact revisionは独立authorityとする |
