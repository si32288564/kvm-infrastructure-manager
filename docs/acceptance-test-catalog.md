# Acceptance Test Catalog

- 状態: Baseline
- 更新日: 2026-08-09

## 1. 目的

Architecture Traceability Matrixが参照する通常Acceptance/Performance Testの最低契約を定義します。具体的なfixture、実行環境、test fileは実装時に同じIDへ関連付けます。

## 2. Identity / Authorization

| ID | Acceptance Contract |
|---|---|
| AT-AUTH-001 | 外部IdPの有効/無効tokenを検証し、KIMがUser/Northbound Service Principal credential rowを発行しない |
| AT-AUTH-002 | issuer+subjectを安定PrincipalとしてMembership/Role Bindingへ関連付ける |
| AT-AUTH-003 | system/tenant/project action matrixがdeny-by-defaultで評価される |
| AT-AUTH-004 | 複数issuerの同一subjectを別Principalとして安全に扱う |
| AT-QUOTA-001 | concurrent allocationでもQuota超過をtransactionで一件だけ拒否し、部分usageを残さない |

## 3. Host / Agent

| ID | Acceptance Contract |
|---|---|
| AT-HST-001 | Agent register/approve/disable/deleteとHost state transitionを認可・監査付きで行う |
| AT-HST-002 | CPU/NUMA/memory/HugePages/NIC/storage/libvirt inventoryとgenerationを正規化する |
| AT-HST-003 | HostGroup/Placement Scope/trait変更がplacement snapshotへversion付きで反映される |
| AT-HST-004 | CPU/Memory/PCI/Network/Storage/Virtualization module を同じ registry で収集し、descriptor provenance、canonical ordering、top-level capability と typed fragment の一致を検証する |
| AT-HST-005 | gRPC Inventory Envelope の Receipt、immutable snapshot、current capability projection を不可分に commitし、duplicate replayを冪等化し、older generationでprojectionを巻き戻さない |
| AT-HST-006 | CPU、NUMA、Memory、HugePages の sysfs/procfs raw source を Linux OS Integration Adapter で読み、source path/state/reason を持つ evidence から canonical typed Fragment と Capability Projection を再現する |
| AT-HST-007 | Linux 実環境で CPU topology、Memory、NUMA interface、HugePage pool を読み、fixture で NUMA 非対応、HugePages 未設定、sysfs 欠損、permission denied、kernel interface 差異を検証する |
| AT-AGT-001 | shell/argv/unknown Command/arbitrary libvirt XML/path payloadをschema境界で拒否する |
| AT-AGT-002 | Agent artifact/configにBus credential/subject accessがなくGateway mTLSだけを使用する |
| AT-AGT-003 | identity/capability/armed authority/current Leaseの一つでも欠ければCommandを取得・実行できない |
| AT-AGT-006 | 2系統以上のLinuxで同じControl Plane contractへ正規化し、OS名分岐を要求しない |
| AT-AGT-007 | typed remediation allow-list外のpackage/service/config/kernel変更を拒否する |
| AT-AGT-008 | upstream/標準Linux KVM、QEMU、libvirt packageだけでKIM core lifecycleを実行し、KIM専用patch/forkを依存関係に含めない |
| AT-AGT-009 | KIMが作成・管理するVM/deviceを標準libvirt/QEMU/KVM interfaceでinspectionでき、KIM metadataがunderlying standard operationを封鎖しない |
| AT-AGT-010 | Agent artifact/build reviewでGoがprimary daemonであり、cgo/wrapper/native helperが列挙済みnarrow interfaceと独立testへ限定される |
| AT-AGT-011 | libvirt/Storage/OVS/SR-IOV/DPDK/PCI/Clock/Compliance module を同時有効化しても Host identity の current mTLS session/certificate が一組のまま全 logical stream を処理する |
| AT-AGT-012 | HTTP/2/gRPC 等の transport 実装を差し替えても同じ typed module、capability advertisement、Command/Lease authorization contract を再利用できる |
| AT-AGT-013 | logical stream/resource scope ごとの sequence/generation で duplicate/reorder を処理し、transport arrival 順から global ordering を推測しない |
| AT-AGT-014 | 別 endpoint/connection 追加を trust/security/traffic/QoS/artifact 要件、threat analysis、approval、lifecycle test なしに受け入れない |
| AT-AGT-015 | L7 proxy termination で pinned proxy certificate と sanitize/rebuild 済み downstream certificate hash の両方を検証し、unpinned proxy、欠落/複数/malformed XFCC、Agent 注入 header を拒否する |
| AT-AGT-016 | generation 1 で commit 済みだが未受領の Receipt を generation 2 replay で回収し、Receipt row を増やさず durable spool を解放して current Resync Checkpoint へ収束する |

## 4. Host Lifecycle / Baseline / Compliance

| ID | Acceptance Contract |
|---|---|
| AT-HLC-001 | Host lifecycleが許可されたtransitionだけを実行し、transition actor/reason/generationを監査する |
| AT-HLC-002 | 有効なHost credentialとsessionだけではenrollment、READY、authority armedにならない |
| AT-HLC-003 | manual approvalまたはversioned Enrollment Policy matchだけがHostをENROLLEDへ進める |
| AT-HLC-004 | Host Profile/Baseline versionをimmutableに保持し、assignment generationとrollout provenanceを追跡する |
| AT-HLC-005 | preflight/compliance evaluation前後でHost、backend、DB authorityに副作用がない |
| AT-HLC-006 | controlごとにstatus、severity、observed/required state、evidence、evaluated generationをappend-onlyで保存する |
| AT-HLC-007 | blocking controlがHost-wideまたはcapability-scoped eligibilityだけを定義通り拒否し、final admissionで再検証される |
| AT-HLC-008 | remediation modeごとにobserve-only、safe auto、maintenance、externalの許可境界を強制する |
| AT-HLC-009 | safe auto remediationもcurrent assignment、armed authority、typed Command、Lease、journal、verificationを必要とする |
| AT-HLC-010 | observation change、evidence expiry、Agent/version changeで継続再評価し、drift actionをpolicy通り実行する |
| AT-HLC-011 | Baseline rolloutをcanary/batchで進め、failure thresholdで停止し旧version/resultを改変しない |
| AT-HLC-012 | disruptive remediationがmaintenance authority、impact set、drain condition、post-verificationを要求する |
| AT-HLC-013 | external-remediation controlはinstruction/evidence requestだけを生成し、KIMからHost mutationを発行しない |
| AT-HLC-014 | decommissionがdisarm、Lease fencing、resource drain、credential revocation、最終evidence保存後だけ完了する |
| AT-HLC-015 | Host/Agentからのapproval/Profile/Baseline/Control policy変更要求を認可境界で拒否・監査する |
| AT-HLC-016 | duplicate identity/hardware fingerprintを自動mergeせず、conflictをquarantineして明示解決を要求する |
| AT-HLC-017 | Hardware Identity Evidenceをsource/issuer/collector/provenance/freshness/request binding/conflict付きで正規化し、policy decisionへbindする |
| AT-HLC-018 | MAC/hostname/IP/単一serialまたはAgent自己申告だけではpolicy-auto enrollmentできない |
| AT-HLC-019 | Control ResultがControl version、Evaluator Artifact digest、input evidence digest、Inventory/policy generationを保持する |
| AT-HLC-020 | Evaluator更新をfixture CI、shadow comparison、canary、batchで進め、判定差とthreshold超過を可視化・停止する |
| AT-HLC-021 | External Remediation Request/responseがservice identity、contract/generation、expiry、idempotency、integrity、correlationを検証する |
| AT-HLC-022 | 外部COMPLETION_CLAIM後もfresh observationとassigned Evaluator再評価が一致するまでCompliance/READY/authorityを進めない |
| AT-HLC-023 | current Enrollment Decision と certificate fingerprint を含む Credential Binding revision が一致する場合だけ Agent transport session を grant する |
| AT-HLC-024 | session authorization が current Enrollment、Credential Binding、session generation、capability generation を保持し、capability 欠損時は PENDING_CAPABILITY になる |
| AT-HLC-025 | authenticated/authorized session と READY gate が揃っても explicit arming 前は Host mutation authority row/generation が存在しない |
| AT-HLC-026 | explicit arming transaction が Enrollment、Credential、session、capability、Baseline、preflight、Compliance、policy を固定し、current mutation authorization が全 binding を再検証する |
| AT-HLC-027 | 同一Hostのactive Maintenance HOST_DRAIN claimとUpgrade Target claimを競合させ、一方だけがtyped mutation authorityを取得し他方をside effect前に拒否する |

## 5. Host Grouping

| ID | Acceptance Contract |
|---|---|
| AT-HGR-001 | HostGroup/Dimension/Membership/Hierarchy/Policy Binding/Snapshot/Placement Scopeをgeneration・認可・監査付きresourceとして管理する |
| AT-HGR-002 | Group typeごとに許可効果を制限し、同じHost集合でも異type semanticsを暗黙共有しない |
| AT-HGR-003 | EXACTLY_ONE/ZERO_OR_ONE/MANY cardinalityとrequired membershipをdimension+levelごとに検証する |
| AT-HGR-004 | explicit/selector/external sourceのprovenanceを保持し、materialized membershipだけをcurrent authorityにする |
| AT-HGR-005 | selectorがpure/deterministicで、input/version/result digestから同じproposalを再現する |
| AT-HGR-006 | Group所属/weightがineligible Hostをeligibleにせず、Host固有reasonを保持する |
| AT-HGR-007 | Placement snapshotとfinal admissionがmembership/policy/hierarchy generationを固定・再検証する |
| AT-HGR-008 | Group capacity表示がHost inventory/allocation合計と一致し、Group独自claim/usage rowを作らない |
| AT-HGR-009 | Profile/Baseline bindingをpriority/versionで決定し、同priority conflictをBLOCKEDにする |
| AT-HGR-010 | rolloutが開始時membership snapshotだけを対象とし、retry/resumeでも同じdigestを使う |
| AT-HGR-011 | maintenance waveがsnapshot、failure-domain concurrency、minimum-ready、drain条件を強制する |
| AT-HGR-012 | Tenant APIが許可Placement Scopeだけを返し、Host membership/rack/power/owner情報を秘匿する |
| AT-HGR-013 | DRAFT/ACTIVE/DRAINING/RETIRED/DELETED lifecycleとactive reference delete guardを検証する |
| AT-HGR-014 | membership/hierarchy変更後も既存workloadを維持し、違反をdrift/action-requiredとして記録する |
| AT-HGR-015 | membership bulk updateを一generationで不可分commitし、response loss再送を同じdigestへ収束させる |
| AT-HGR-016 | complete membership set A/B/Cをgen1、A/C/Dをgen2としてatomic publishし、stable replayはgenerationを増幅せず、同request identityのsemantic mismatchとparallel stale publisherを拒否する |
| AT-HGR-017 | Placement dry/finalとimmutable snapshotがaccepted membership set generation/digest/member evidenceを固定し、member除外・Group generation変更・後続set publishを越えてstale authorityを進めない |
| AT-HGR-018 | SYSTEM scopeのcardinality policy evidence/currentをgeneration付きで管理し、parallel sibling publishで同一Hostをexclusive Groupへ割り当てる競合は一件だけcommitし、policy更新後はset再publishまでsnapshot/Placementをfail closedにする |
| AT-HGR-019 | 同一type/dimensionのcomplete hierarchyをordered level、generation-bound node、single-parent relationとしてatomic publishし、cycle/level inversion/multi-parent/parallel stale publisherを拒否する。membership set、snapshot、Placement dry/finalはcurrent accepted hierarchy generationへbindする |
| AT-HGR-020 | versioned closed typed Selectorをcurrent normalized Host evidenceへpure評価し、MATCHED/NOT_MATCHED/UNKNOWN/UNSUPPORTEDをimmutable evidence化する。materializationはinput/Selector/Cardinality/Hierarchy/HostGroup generationを再検証してcomplete Setをatomic publishし、semantic replayは一authorityへ収束する |
| AT-HGR-021 | A/B/Cのaccepted Membership SetからUPGRADE Snapshotを作成しPlan/Wave/Targetへbindする。live SetをA/C/Dへ変更後もactive TargetをA/B/Cに保ち、Snapshot/Set raceはcomplete一世代だけを記録する |
| AT-HGR-022 | A/B/Cのaccepted SetからMAINTENANCE Snapshotと独立Plan/Wave/Targetsをatomic publishし、A/C/D drift、recovery、pause/resume後もA/B/Cとdigestを維持する。purpose mismatch、concurrency、Host fenceもfail closedにする |
| AT-HGR-023 | exact HostGroup/Policy revisionにbindしたMAINTENANCE Group Policy Bindingをmany-to-many membershipからhigher-priorityで決定する。same-priority exact-equivalentは収束、非互換はASSIGNMENT_CONFLICTでPlan publicationをBLOCKED、stale highestはfallbackせず、replay/concurrency/drift後もimmutable resolution/Plan provenanceを維持する |
| AT-HGR-024 | closed VM_PLACEMENT Scopeへexact PLACEMENT_POOL generationsをatomic publishし、current accepted Setsからvisible populationをdeduplicateする。NO_SCOPE/BLOCKED/NO_VISIBLE/VISIBLE_BUT_INELIGIBLEを分離し、Scope/Set drift、widening、resource競合でFinal Admissionをstale rejectして全claimをrollbackする |
| AT-HGR-025 | purpose-limited Ed25519 issuer trustでclosed complete-set External Assertionを検証し、VERIFIED evidenceだけではmembershipを変更しない。explicit materializationはexact issuer/assertion/HostGroup/Hierarchy/Cardinality/expected Set generationを再検証し、stable replayは元evidence/Setへ収束、invalid/stale/revoked/conflicting inputとparallel source raceはold accepted Setを維持する |

## 6. Availability Responsibility and Recovery

| ID | Acceptance Contract |
|---|---|
| AT-AVR-001 | immutable AvailabilityPolicyをversion/digest/approval付きで管理し、responsibility/action compatibilityを検証する |
| AT-AVR-002 | INFRASTRUCTURE_MANAGED、WORKLOAD_MANAGED、MANUALの各responsibilityと公開classを認可付きで管理する |
| AT-AVR-003 | RESTART_ON_OTHER_HOST、EVACUATE、NO_AUTOMATIC_ACTIONの許可組合せとbounded semanticsを検証する |
| AT-AVR-004 | PLACEMENT_POOLだけがAVAILABILITY_POLICY bindingを持ち、他Group typeからのbindingを拒否する |
| AT-AVR-005 | 全active Pool membershipsからHost Effective Policyを一意解決し、missing/stale/conflict HostをREADY/placement ineligibleにする |
| AT-AVR-006 | Final AdmissionがAvailability Bindingと全resource claimを同transactionでcommit/rollbackする |
| AT-AVR-007 | Pool/Policy変更後も既存VM Bindingを維持し、Rebind Operationだけが新revisionを発行する |
| AT-AVR-008 | Host Failure Epochがdetection/confirmation/fencing/policy/recovery/verification evidenceをappend-onlyで保持する |
| AT-AVR-009 | WORKLOAD_MANAGED failureでFault/Eventを生成し、Recovery Job/Commandを自動作成しない |
| AT-AVR-010 | MANUAL failureでACTION_REQUIREDを維持し、authorized Decision後だけ同じrecovery gateを通す |
| AT-AVR-011 | INFRASTRUCTURE_MANAGED recoveryがfencing/storage/device/network/failure-domain/Placement gateをすべて評価する |
| AT-AVR-012 | recovery前提のUNKNOWNをBLOCKEDとして保持し、VM/Volume/Allocationを推測cleanupしない |
| AT-AVR-013 | 同一canonical Failure Campaign+VM+Binding+actionの並行要求を単一Recovery Campaign Claim/Operationへ収束する |
| AT-AVR-014 | EVACUATE planがVM別status/attempt/capacity failureを保持し、bounded concurrencyで実行する |
| AT-AVR-015 | destinationのPool Policy compatibilityを含むdry/final admissionを再実行しsilent fallbackしない |
| AT-AVR-016 | Fault/Recovery Eventがdurable outbox、correlation、policy version、redaction、再送contractを持つ |
| AT-AVR-017 | explicit Rebind Request/Decisionがexact source Bindingとexact active target Policyを固定し、accepted Decision、next immutable Binding revision、current pointerをatomic commitする。same-ID replayは同じDecision/Bindingへ収束し、stale/concurrent intentはrevisionを増幅せずresource/runtime side effectを起こさない |
| AT-AVR-018 | typed Failure observationとexplicit incident keyからSUSPECTED Failure Epochをopenし、exact current VM Availability Binding/Policy/Admission/allocation/source Host authorityをimmutableに固定する。UNKNOWN/late evidence、response-loss replay、duplicate incident、10回のRebind raceでもconfirmation/fencing/recovery/mutationを発行せずcomplete一世代だけを保持する |
| AT-AVR-019 | closed `ALL_REQUIRED_EVIDENCE` Policyをexact AvailabilityPolicy revisionへbindし、Failure Epochのexact Policy/Evidence snapshotをimmutable Evaluationへ保存する。UNKNOWN/STALE/CONFLICTING/NO_CONFIRMATION_POLICYを区別し、SATISFIED Evaluation単体はSUSPECTEDを維持する。explicit DecisionだけがCONFIRMED transitionをatomic commitし、replay、parallel Decision、evidence/Policy drift、Rebind raceでもhistorical provenanceを維持してfencing/recovery/runtime authorityを発行しない |
| AT-AVR-020 | closed typed Fencing/Local LVM Storage Safety Policyをexact AvailabilityPolicyへbindする。CONFIRMED Epochでheartbeat lossだけのFencingとUNKNOWN attachmentをpositive proofにせず、exact Host authority FENCED event+libvirt SHUTOFF read-backおよびDETACHED/RELEASED/BOUND/no-holder evidenceだけをexplicit Proofへmaterializeする。Evaluationはstateを変更せず、parallel Proofとresponse-loss replayは一proof/一FENCED transitionへ収束し、Policy/evidence driftと後続RebindをfenceしてRecovery/Job/Command/resource mutationを発行しない |
| AT-AVR-021 | typed GLOBAL/PLANNING RecoveryBudgetPolicyをexact AvailabilityPolicy revisionへbindし、FENCED Epochのhistorical responsibility、current-usable Fencing/Storage Proof、source除外済みread-only Placement candidate snapshotをRecovery Eligibility Evaluationへ固定する。Evaluation単体はpermission/Claimを作らず、explicit DecisionだけがBudget Claimとatomic commitする。max=1並行Decisionはone accepted/one exhausted、same-ID replayは一Decision/Claim、proof ABA/destination driftはstale、WORKLOAD_MANAGED/MANUAL/no-budget/no-destinationはfail closed、Recovery Operation/Placement/Job/Command/resource mutationはゼロとする |
| AT-AVR-022 | exact Eligibility Decision/Budget ClaimからRequestとone immutable destination Planを作り、Request/Plan時のside effectをゼロにする。start時のFencing/Storage/Budget/destination driftは全rollbackし、current authorityではsource compute release、ordinary Final Admission、Budget CONSUMED、one typed preparation Job/Command、RUNNINGをatomic commitする。response replayはsame Operation/Plan/Admission/Commandへ収束し、UNKNOWN→read-back MATCHEDはVERIFYINGに留まり、dangerous-step gateはproof ABAをblockする |
| AT-AVR-023 | `RESTART_ON_OTHER_HOST` がexact destination Admissionから既存Local LVM binding/image/attachment、VM define、network readiness、typed power Commandをmaterializeする。power直前のFencing/Storage/Budget/destination driftはCommand 0、power UNKNOWNはBudget CONSUMEDのままread-back-first、RUNNING後もpure VerificationまではVERIFYINGとする。power/attachment/network generation driftはold Verificationのterminal commitを拒否し、explicit Terminal DecisionだけがOperation VERIFIED、Epoch RECOVERED、Budget RELEASEDをatomicに一回だけcommit/replayする。Failure Recovery Operation内の`EVACUATE` backendは引き続きBLOCKEDとする |
| AT-AVR-024 | Failure Epochのexact source planからroot `vda`/Volume/Binding/LV/Attachmentを導出し、ordinary Lease/Attempt/Result/Verification由来の`SHUTOFF/MATCHED`、configured-root identity、holder closedだけでSource Root Safety Proofを発行する。wrong `vdb`、holder open、RUNNING、UNKNOWNではproofを発行せず、root/data compositeを個別provenanceでSAFEにする。root mutation countは0のままlogical retirementし、power/holder/Binding/current-plan ABAはEligibility、Operation start、dangerous-stepをstale/blockする |
| AT-AVR-025 | explicit opt-inとexact g01/g02 allow-listの下でPostgreSQL Commandを先に作成し、random Leaseをremote helper stdinだけに渡す。capability-free responseの全authority identityを照合し、ordinary `AcceptAgentCommandResult` でLease/Attempt/Result/Verification/Receipt/Job SUCCEEDEDを一回だけcommitする。Command/Attempt/Lease/Host/session/type/schema/target/payload mismatchはrejectし、raw tokenはartifactに残さない |
| AT-AVR-026 | isolated g01→g02 `RESTART_ON_OTHER_HOST` campaignをone PostgreSQL 17 historyで実行し、actual source RUNNING→SHUTOFF、read-only Lease-bound exact `vda` holder-closed、Confirmation/Fencing/Root/Storage authority、Eligibility/Decision/Budget、destination Final Admission/materialization generation 2、actual RUNNING/holder-open、Verification/Terminalへ収束させる。same VM UUID、physical split-brain negative、13件のordinary execution histories、terminal transition各1件、root mutation 0、raw token 0、完全cleanupを要求する |
| AT-AVR-027 | Migration 066 planned Host Evacuation schema/APIがFailure Epoch/Fencing/Recovery Budget rowを増加させず、same request replayは同じOperation/workload-setへ収束しdifferent policy replayをconflictにする |
| AT-AVR-028 | source Host drainとimmutable workload snapshotをone transactionでcommitし、Final AdmissionとのHost lock競合後にnew source Placementを拒否する。snapshotはVM/Admission/plan/Binding/Network/Storage/PCI provenanceを保持する |
| AT-AVR-029 | PostgreSQL campaignで3 child、maximum concurrency 2とし、2 claim中のthird wait、one release後のthird claim、restart後もmaximum observed in-flight 2を検証する |
| AT-AVR-030 | exact planned quiescenceがtyped shutdown response RECEIVED/LOSTの双方でSHUTOFF identity read-backを要求し、RUNNING/wrong VM/plan/Host authority driftを拒否する |
| AT-AVR-031 | zero-Port/OVN profileだけをeligibleとし、Local LVM data independence不明とreal PCI VF profileを明示BLOCKEDにする。source/destination Host同一、stale Admission/plan/generationを拒否する |
| AT-AVR-032 | source Host authority失効時にremaining childをRECOVERY_REQUIRED、parentをSOURCE_UNREACHABLEへ進め、Failure Epoch/Fencing Proofを作らないことを検証する |
| AT-AVR-033 | 2 VERIFIED/1 BLOCKEDをPARTIALに保ち、BLOCKED解消後だけ3/3 VERIFIEDへ進める。pre-quiescence cancelを許可しpost-quiescence cancelを拒否する |
| AT-AVR-034 | parent terminalが全snapshot VERIFIED、source active 0、post-drain Admission 0を要求し、verified後もsource HostをDRAINEDに維持する |
| AT-AVR-035 | child terminal後のgeneric cleanupはseparate producer adapterだけが発行し、cleanup BLOCKED/UNKNOWN/未実施でchild/parent VERIFIEDをrollbackせず、explicit undrainだけがPlacementを再許可する |

## 7. Workload Resilience Intent

| ID | Acceptance Contract |
|---|---|
| AT-WRI-001 | Project scopeのResilience Group/member/constraint/domain claim lifecycleを認可・generation・監査付きで管理する |
| AT-WRI-002 | active/standby等のopaque roleを保存するがVNF lifecycle/application health判断に使用しない |
| AT-WRI-003 | public Failure Domain classを内部dimension/levelへmapしraw topologyをresponse/eventへ出さない |
| AT-WRI-004 | rackとpower-feed等の複数dimensionへhard max-members/min-domainsを独立適用する |
| AT-WRI-005 | Member Slotへ同時に一VMだけをbindしProject ownership/generationを検証する |
| AT-WRI-006 | Placement SnapshotへGroup/member/constraint/claim/hierarchy generationを完全に含める |
| AT-WRI-007 | Domain ClaimとVM Allocation/Availability Binding/resource claimsを一transactionでcommit/rollbackする |
| AT-WRI-008 | parallel member admissionがsame-domain conflictを一件だけcommitし残候補をreselectする |
| AT-WRI-009 | insufficient/UNKNOWN domainでhard constraintをrelaxせずbounded ineligible reasonを返す |
| AT-WRI-010 | old member ownership UNKNOWN時にreplacement slot/claim bindを拒否する |
| AT-WRI-011 | domain driftをVIOLATED/UNKNOWNと通知し既存VM/claim/historyを暗黙変更しない |
| AT-WRI-012 | WORKLOAD/INFRASTRUCTURE/MANUALの各Placement/Recoveryで同じconstraintを守りresponsibilityを変更しない |
| AT-WRI-013 | Northbound create/bind retryをidempotentにしCore authz/admission/auditを通す |
| AT-WRI-014 | active member/claim/referenceがあるGroup deleteを拒否する |
| AT-WRI-015 | incomplete member setをPENDINGにし、各admissionのmax-membersとcompletion時min-distinctを正しく評価する |

## 8. Recovery Storm Control

| ID | Acceptance Contract |
|---|---|
| AT-RCV-001 | immutable RecoveryBudgetPolicyをAvailabilityPolicy reference、scope、rate/concurrency/fairness/health contract付きで管理する |
| AT-RCV-002 | Recovery Queue/Entry/Budget LeaseをPostgreSQL authority、generation、token、expiry付きで管理する |
| AT-RCV-003 | PLANNING/DISPATCH phase別にSite/Pool/domain/backend/Projectのapplicable budgetを全て不可分取得する |
| AT-RCV-004 | Budget Lease取得後もfencing、final admission、capacity、Command Lease、verificationを個別に要求する |
| AT-RCV-005 | lease expiry/worker restart後にdispatch済みOperationをduplicateせずread-backへ収束する |
| AT-RCV-006 | max concurrency、rate/window/burst、backoff/jitterをmulti-workerで上限内に保つ |
| AT-RCV-007 | bounded priority/aging/fair-share/per-scope capでsafetyとstarvation防止を両立する |
| AT-RCV-008 | backend health circuit breakerが該当Entryだけをpauseし復旧後にfull revalidationする |
| AT-RCV-009 | duplicate Host signalを同failure epochへ収束しcorrelated domain failureをshared budgetへ集約する |
| AT-RCV-010 | queue age/saturation/waiting/blocked/unknown/escalatedをmetrics/event/auditで説明する |
| AT-RCV-011 | Budget Policy変更後もstarted Operationを維持しnew leaseだけをnew generationへ従わせる |
| AT-RCV-012 | DB/worker failover後にqueue authority、active budget consumption、公平なorderingを復元する |
| AT-RCV-013 | dispatch transactionがRecovery Operationと全scope Budget Consumptionを不可分commitしterminal verification後だけreleaseする |
| AT-RCV-014 | 全workerがversioned canonical BudgetScopeKey順でscope row/tokenをlockし、deadlock/serialization retryで部分Leaseを残さない |
| AT-RCV-015 | FailureCampaign、Membership、Recovery Campaign Claimがduplicate/late correlationを一つのcurrent recovery decisionへ収束する |

## 9. API / Data / Operations

| ID | Acceptance Contract |
|---|---|
| AT-API-001 | Host/backend realizationを伴うmutation APIが202+Operationを返し、request処理中にHost/backendへ接続しない |
| AT-API-002 | 同一idempotency key+payloadの並行再送が単一Operation/resourceへ収束する |
| AT-API-003 | 同一idempotency key+異なるpayloadが409 conflictになる |
| AT-IAC-001 | Terraform/UI/backendにKIM PostgreSQLと異なるcurrent authorityを与えず、refresh/readがauthorized KIM projectionだけを使用する |
| AT-IAC-002 | VMのlogical ID、Network、Datapath Profile、Availability desiredを維持したRecovery A→B後のrefreshがno-op planになり、Host/binding/generationをreplacement triggerにしない |
| AT-IAC-003 | generated API/Provider/UI schemaがHost ID、pCPU/NUMA/PMD/RxQ/socket/interface/OVS UUID/PCI BDF/IOMMU/LV UUID/path/Materialization・Recovery・EVACUATE generationをdesired/export fieldに含めない |
| AT-IAC-004 | Recovery/EVACUATE/Drain/Retry/Cancel/Read-back/Cleanup/Reconciliation/diagnosticがOperationとして照会され、Terraform persistent resource CRUDへ擬装されない |
| AT-IAC-005 | OpenAPI+lifecycle metadataから生成したAPI/Provider/UI field分類、mutability、replacement、computed、sensitive、import、async/delete semanticsが一致する |
| AT-IAC-006 | stale `If-Match` update/deleteを412で拒否し、refresh後もremote desired changeをsilent overwriteしない |
| AT-IAC-007 | create response lossを同じIdempotency-Keyで再送し、単一resource/Operationへ収束してduplicateを作らない |
| AT-IAC-008 | delete protection、dependent conflict、async delete/tombstone、response lossをstable error/Operation contractで区別し、verified terminal前にclient stateを除去しない |
| AT-IAC-009 | stable logical ID importがdesired/computedを分離して再構成し、backend-only objectとphysical incarnationをadoptしない |
| AT-IAC-010 | machine principalのProject/Site/resource/action scope、read/write/admin、rotation、audit、destructive protectionを検証し、backend/Agent credentialを開示しない |
| AT-IAC-011 | logical Security PolicyをOVN ACL/Port Group/Address Setへcompile/read-backし、public desired/API/stateへraw OVN syntaxを要求しない |
| AT-IAC-012 | PostgreSQL authority commitだけで完了するProject create/update/deleteが201/200/204で確定し、形式上だけのOperationやHost/backend接続を生成しない |
| AT-IAC-013 | ProjectとFlavorが共通Northbound transport/security contractでCRUD/list/replay/concurrencyを通り、Flavor update後も既存Admission/materializationのexact historical revisionを改変しない |
| AT-IAC-014 | Imageはartifact ingestion/実測digest authority分離前にcaller assertion型CRUDとして公開されない。Availability PolicyはMigration 075のclosed SYSTEM profileだけを公開する |
| AT-IAC-015 | SYSTEM Availability Policyのconcurrent create、RBAC、ETag race、immutable revision、no-retrofit、dependency/protection retirement、runtime authority non-generationを実HTTP/PostgreSQLで検証する |
| AT-IAC-016 | Image Northbound endpointがexpected-only intentをcommitし、typed ingestion Operation、immutable whole-artifact observation、digest verification terminal後だけverified projectionを公開する。content変更は同じlogical IDの新revisionをPENDINGへ戻し、再verification前に既存VMへretrofitしない |
| AT-IAC-017 | 実Terraform CLIがlocal provider binaryからProject/Flavor/closed Availability/Imageを実HTTP/PostgreSQLへapply/update/refresh/import/destroyし、Image OperationをVERIFIEDまでpollする。no-op、remote drift、stale If-Match、response-loss idempotency、closed enum、logical ID continuity、physical-state非漏洩を検証する |
| AT-IAC-018 | 実Terraform CLIでKIM Create commit後のstate mapping lossを再現し、同じclient identity/reference/configurationの再applyがoriginal logical IDと単一immutable idempotency decisionをrecoverする。同referenceの異intentは409となり、display name lookupを使用しない |
| AT-DATA-001 | desired/allocation/Job/Command/idempotencyの一要素失敗で全transactionがrollbackする |
| AT-DATA-002 | desired/observed generationを独立保持し、stale observationをcurrent表示しない |
| AT-DATA-003 | schema catalogがCurrent Authority、Immutable Decision/Evidence、Delivery Journal、Derived Projectionとowner/scope/retentionを宣言する |
| AT-DATA-004 | current summary/pointer再計算が過去Decision/Evidenceを改変せず、projectionをauthorityから再構築できる |
| AT-DATA-005 | domain mutation、Operation/idempotency、Outboxの一要素失敗で全transactionをrollbackする |
| AT-DATA-006 | Inbox受理、domain decision、Receipt/Outboxを不可分commitし、duplicate/replayへ同じReceiptを返す |
| AT-DATA-007 | versioned retention、archive、tombstone、legal/security holdをresource/data class別に強制する |
| AT-DATA-008 | GC Candidate/Lease/Receiptがreference-safeかつ冪等で、DB GCからbackend side effectを発行しない |
| AT-DATA-009 | append-heavy partitionの作成/detachでglobal uniqueness、FK/reference、Tenant scope、backup coverageを維持する |
| AT-DATA-010 | expand/migrate/switch/contract中にN/N-1 replicaが互換schema範囲だけでread/writeする |
| AT-DATA-011 | migration/backfillがartifact digest、schema generation、Lease、checkpoint、batch/lock limit、verificationを保持する |
| AT-DATA-012 | concurrent updateとbackfill競合時にcurrent generationを上書きせずretry後も同じ結果へ収束する |
| AT-DATA-013 | Backup Manifestがbase backup、WAL range、schema/migration、artifact、key reference、checksumを検証する |
| AT-DATA-014 | PITR起動時にrestore epoch/database authority generationを発行しpre-restore authorityを拒否する |
| AT-DATA-015 | recovery modeがresourceをMATCHED/DB_ONLY/BACKEND_ONLY/CONFLICTING/UNKNOWNに分類しscope別にauthorityを再開する |
| AT-DATA-016 | backend-only adoptionがidentity/ownership/fencing/authorization付きOperationを要求し自動adopt/deleteしない |
| AT-DATA-017 | PITR point後に配送/実行済みのEvent/Commandをstable IDで再送しReceipt/journalへ収束する |
| AT-DATA-018 | replication/WAL/backup gap、partition/GC backlog、migration、Outbox/Inbox age、restore reconciliationをmetrics/alarmで公開する |
| AT-DATA-019 | RECOVERY_READ_ONLYがrecovery-control writeだけを許可し、旧writer/Control Plane/credentialのfencing proof後にscope別mutationを再開する |
| AT-DATA-020 | hard DB、verified logical、archive referenceのwrite/GC/archive切替とIntegrity Verifierを検証する |
| AT-DATA-021 | Recovery Control専用identity/role/APIが通常Service Principalと相互に権限昇格せずapproval/auditを強制する |
| AT-OPS-001 | Operation状態、進捗、correlation、bounded failure reasonを照会できる |
| AT-OPS-005 | retry/cancelが許可状態とauthorityを検証し、unsafe actionを拒否する |
| AT-EVT-001 | event/webhookをdurable outboxから再送し、重複IDとredaction contractを維持する |

## 10. Execution

| ID | Acceptance Contract |
|---|---|
| AT-EXEC-001 | concurrent Lease要求で一つだけがactive Leaseを取得する |
| AT-EXEC-002 | LeaseにHost owner/token/attempt/expiry/authority generationがbindされる |
| AT-EXEC-003 | Agent journalがbackend mutation前にdurably記録され、同じCommandを再実行しない |
| AT-EXEC-004 | accepted Resultの同一再送は同じreceipt、異なるdigestはconflictになる |
| AT-EXEC-005 | UNKNOWN Attemptを改変せず、verification evidenceと後続eventを追記する |
| AT-EXEC-006 | successful Result後もJobはverifyingで、matching observation後だけsucceededになる |
| AT-EXEC-007 | terminal Job/Attempt/Event履歴がimmutableである |
| AT-EXEC-008 | Lease grant が current Host authority generation と Agent session generation を保持し、並行要求でも active Lease が一つだけになる |
| AT-EXEC-009 | Lease expiry または session/Host authority fence 後の Result は stale として拒否され、明示的な再 arming 後も旧 Lease は復活しない |
| AT-EXEC-010 | PostgreSQL Lease Grant から current Gateway session、Agent typed backend、Result receipt、matching read-back verification までの round-trip が同じ authority/session/Attempt identity で収束する |
| AT-EXEC-011 | Gateway send failure で Lease/Attempt を未実行扱いせず、stable envelope を再送待ちまたは UNKNOWN/read-back path に保持する |
| AT-EXEC-012 | mutation 完了後・Result 発行前に Agent が crash しても、Lease expiry で Attempt を UNKNOWN とし、restart 後の既存 journal evidence と typed read-back が MATCHED の場合だけ同じ Attempt の Verification を追記して Job を SUCCEEDED へ収束させる |
| AT-EXEC-013 | long-running Session Runner が一つの current transport 上で inbound Command routing、priority outbound flush、durable Receipt handling を並行実行し、cancel または connection loss で bounded に停止する |
| AT-EXEC-014 | rejected/failed session attempt が local generation を消費せず、SessionAccepted 後の durable ledger を process restart で回収して次 generation だけを提案する |
| AT-EXEC-015 | kim-worker が expired active Lease を bounded batch で検出し、競合時は stale candidate を無視しながら各 current Lease/Attempt を UNKNOWN へ一度だけ進める |
| AT-EXEC-016 | protected delivery を要求した Lease Grant が同じ transaction で一つの Outbox intent を作り、payload に plaintext token を含まず、correct key/AAD だけで original token を復元でき、intent conflict 時は Lease/Attempt/current state を全 rollback する |
| AT-EXEC-017 | bounded Worker publisher が protected Outbox intent を current authority で再検証し、stable NATS message ID で publish し、JetStream PubAck 後だけ Outbox を delivered にする |
| AT-EXEC-018 | Gateway が Bus payload を Inbox へ受理し、current Lease/Host/session generation を再検証してから current Outbound Registry へ route し、同一 duplicate を同じ Agent envelope で安全に再 route する |
| AT-EXEC-019 | live session 不在時は NAK/redelivery、復旧後は route、authority fence 後は Agent へ渡さず terminal ACK、同一 message ID の異 digest は quarantine となる |
| AT-EXEC-020 | 3 replica JetStream stream/consumer の leader 停止後に新 leader が同一の 1 message を保持し、Gateway consumer の NAK/停止/再起動後に同じ durable consumer が 1 回 redelivery して ACK へ収束する |
| AT-EXEC-021 | TLS/JWT credential 付き 3 node NATS、`kim-worker`、`kim-agent-gateway`、`kim-host-agent`、PostgreSQL を別 process で起動し、NATS leader kill 後の command、Gateway restart 後の explicit rearm、Agent 不在時 NAK、new session による stale Lease fence、Agent Receipt 後の spool delete が一つの campaign で成立する |
| AT-EXEC-022 | 実 Gateway と Agent の間で Command 到達後の Receipt transport response だけを決定的に遮断し、Result/Observation/Receipt commit 後も spool が残り、Gateway/Agent restart と new session generation 後の同一 message replay が original accepted generation の単一 Receipt を回収して spool を一度だけ削除する |
| AT-EXEC-023 | qualification-only PostgreSQL advisory-lock barrier で実 Gateway を Agent stream write 後かつ JetStream ACK 前に停止し、Gateway/Agent restart 後の durable redelivery が terminal Command を再 route せず、同一 Result/Receipt、単一 route Attempt、変更されない backend marker へ収束する |
| AT-EXEC-024 | 標準 libvirt `qemu:///system` 上の KIM 専用 KVM Domain を closed typed power-state Command で起動し、write-before-execute journal 完了後・Result transport 前に Agent subprocess を killしても Domain が稼働継続し、journal再openと Domain UUID/state read-backが同じ Attemptへ`MATCHED` Verificationを生成する |
| AT-EXEC-025 | Lease expiry で UNKNOWN となった Command を Worker が current authorized session generation 付き Verification Outbox へ一度 enqueue し、publisher/Gateway が immutable Command/Attempt/target/canonical payload digest を再検証して current Agent stream へ typed Verification を配送する |
| AT-EXEC-026 | Mac 側 PostgreSQL/Worker/3-node JetStream/Gateway と remote KVM Host Agent を process 分離し、typed libvirt start 後の Result path 遮断、Agent SIGKILL、Lease expiry、UNKNOWN、new session generation、旧 Result の durable `STALE` fence、標準 libvirt read-back、同一 Job `SUCCEEDED` convergence を一つの campaign で成立させる |

## 11. Placement / Migration

| ID | Acceptance Contract |
|---|---|
| AT-PLC-001 | ineligible Hostは任意の高scoreでもselected setへ入らない |
| AT-PLC-002 | dry evaluation前後でDB/backend/Bus/Agent stateが完全に不変である |
| AT-PLC-003 | stale dry resultをfinal admissionで再評価して拒否する |
| AT-PLC-004 | CPU/NUMA/HugePages/PCI/network/storage/quota claimを不可分commitする |
| AT-PLC-005 | concurrent claim conflictで部分予約を残さず残候補を再選択する |
| AT-PLC-006 | final admission transaction中にbackend call/message dispatchがない |
| AT-PLC-007 | VM binding差によりcold/live/restart/noneが候補Hostごとに変わる |
| AT-PLC-008 | affinity/anti-affinity/PCI/SR-IOV/NUMA constraintをeligibilityで評価する |
| AT-PLC-009 | eligibility reason、score、rank、final conflict、reselectionを説明できる |

## 12. Compute / Image / Network / Storage

| ID | Acceptance Contract |
|---|---|
| AT-CMP-001 | VM create/start/stop/reboot/deleteがdesired→execution→observationで収束する |
| AT-CMP-008 | console accessが短寿命、一回用途、Project scope、監査付きである |
| AT-CMP-009 | accepted Final Admission と current BOUND root Volume/RESERVED Attachment Claim から immutable VM plan と typed define Job/Command を不可分生成し、標準 libvirt inactive Domain XML read-backで同一plan/compute/root identityへ収束する |
| AT-CMP-010 | typed Domain Verificationをimmutable evidenceとして受理してもImage/NetworkがPENDINGならDomain=DEFINED、Storage=BOUND、boot readiness=BLOCKEDを維持し、power-on authorityへ昇格しない |
| AT-CMP-011 | current VM/Plan/Image/BOUND root Binding/RESERVED Attachment Claim と MATCHED Image Verification を同一 transaction で再検証し、Image を REALIZED に進めても Network PENDING なら boot readiness を BLOCKED に維持する |
| AT-CMP-012 | required Port 全件の current pre-boot evidenceを集約し、Domain/Storage/Image/Host/Binding generationと同一transactionで再検証した場合だけREADYとtyped RUNNING Job/Commandを不可分生成する |
| AT-CMP-013 | READY transaction が発行した typed power Command の MATCHED libvirt read-backを immutable evidence として保存し、current VM generation に対する runtime power projectionだけをRUNNINGへ進める |
| AT-CMP-014 | accepted Recovery Terminalとexact logical source retirementからgeneric cleanup eligibilityを作成し、Terminal前、wrong Host/plan/materialization、current destination artifact、database authority inactiveを全てrejectする |
| AT-CMP-015 | typed inactive Domain undefineのmutation response lossをAttempt 1 `DISPATCH_UNKNOWN`へ固定し、Attempt 2 `READ_BACK_FIRST`がabsenceを観測してduplicate undefineなしでone immutable cleanup Terminalへ収束する |
| AT-CMP-016 | source Local LVMはdata-independence proof/policy欠損時にphysical deleteとcapacity reclaimをBLOCKEDとし、exact LV absence前にavailable capacityを戻さない |
| AT-CMP-017 | generic OVN retirementのNB ownership preserved/SB source binding absent/source OVS iface absentを追加mutationなしでNetwork cleanup satisfiedとし、Port/MAC/IPとdestination dataplaneを維持する。PCI physical driver profileはreal VF qualificationまでBLOCKEDに保つ |
| AT-CMP-018 | PostgreSQL上でcleanup Attempt 1のmutation outcomeをUNKNOWNへ固定し、Attempt 2 `READ_BACK_FIRST`からblind undefineを拒否する。observation-only typed read-backがABSENTならone Terminalへ収束し、exact inactive PRESENTならそのimmutable evidence後だけ別undefine Commandを発行する |
| AT-CMP-019 | 同一PortのA→B Handoff/retirement後にB→Cへcurrent projectionを進め、遅延したA cleanupがimmutable A→B Handoff/quiescence/NB/SB/OVS evidenceを1/1で解決し、current Port/MAC/IPを保持する |
| AT-IMG-001 | qcow2/raw image lifecycle、visibility、Project accessを検証する |
| AT-IMG-002 | checksum/signature不一致imageをcache/boot前に拒否する |
| AT-IMG-003 | caller supplied URI/path/argv を拒否し、digest-addressed verified RAW cache artifact を identity-verified Local LVM root Volume へ bounded copy/fsync した後、target から再計算した SHA-256、VG/LV UUID、holder absence が一致する場合だけ immutable realization evidence を受理する。QCOW2 direct-copy は拒否する |
| AT-FLV-001 | FlavorのCPU/RAM/disk/NUMA/HugePages/pinning要求をplacementへ完全伝播する |
| AT-NET-001 | KIM virtual network操作がWAN/physical switch authorityを変更しない |
| AT-NET-002 | VLAN/Geneve/DHCP/security group/L3 intentがgeneration付きで収束する |
| AT-NET-003 | IP/MAC Claimをscope内で一意に確保しreserved/excluded/explicit/automatic policyを強制する |
| AT-NET-004 | overlapping CIDRをisolated Networkで許可し同一routing/attachment scope conflictを拒否する |
| AT-NET-005 | Port delete/unbind後も全reference/dataplane absenceとquarantine完了までIP/MACを再利用しない |
| AT-NET-006 | SR-IOV Port assignmentをPCI/device/network eligibilityと不可分に扱う |
| AT-NET-007 | KIM authority、Intent Revision、OVN NB/SB、Host/dataplaneを別generation/layer statusで照会する |
| AT-NET-008 | VLAN/VNI Segment Pool/Claimをscope内で一意に確保しprovider/overlay mappingを検証する |
| AT-NET-009 | Port Bindingをtype/Host/chassis/device/segment/generation付きresourceとして管理する |
| AT-NET-010 | ACTIVEをbinding-type別NB/SB/Host/dataplane verification後だけ表示する |
| AT-NET-011 | typed Network plan/apply/observeがstable KIM ID/generation/digestで冪等に収束する |
| AT-NET-012 | DHCP desired option/IP bindingとguest lease/runtime observationを分離する |
| AT-NET-013 | Router Interfaceがownership/IP Claim/route overlapをtransactionalに検証する |
| AT-NET-014 | Gateway Binding/Floating IP/NATがprovider/chassis/HA/dependency generationを保持する |
| AT-NET-015 | Provider mappingがexternal physical/WIM capabilityだけを参照しfabric configurationを行わない |
| AT-NET-016 | Gateway failoverがold authority/NAT generationをfenceしnew binding/dataplaneを検証する |
| AT-NET-017 | Security Policy/Port membership/anti-spoofingをgeneration付きで適用しfail closedにする |
| AT-NET-018 | effective MTUをoverlay overhead/Host/gateway/path capabilityから計算してPlacementへ反映する |
| AT-NET-019 | Host recovery/migrationがPortBindingHandoffとold/new generationでsingle binding authorityを維持する |
| AT-NET-020 | SR-IOV/DPDK/vhost BindingをPCI/PMD/RxQ/NUMA/identity/segment claimと不可分commitする |
| AT-NET-021 | delete guard、typed OVN/Host cleanup、absence verification、identity/segment release、DB GC分離を強制する |
| AT-NET-022 | backend-only/foreign OVN object/interfaceをquarantineしexplicit Adoption/repairを要求する |
| AT-NET-023 | provider pool/gateway/force operation/Adoptionを個別permission/approval/auditで保護する |
| AT-NET-024 | Network Event/APIがlayer/binding/intent generationを保持しraw OVN/Host/physical identityをredactする |
| AT-NET-025 | Network adapterがtyped intent、UNKNOWN/read-back、ownership、secret/redaction contractへ適合する |
| AT-NET-026 | OVS Port Commandがbridge/XML/path/argvを受け付けず、Agent管理mappingのstandard OVS bridgeとinactive libvirt NIC identityをread-backしてimmutable pre-boot evidenceへ収束し、dataplane convergenceを主張しない |
| AT-NET-027 | Final Admission の qualified VF Claim を SRIOV_DIRECT typed Commandへ固定し、current PCI/Qualification/policy/Binding と libvirt hostdev PCI identity read-backが一致した場合だけPortをREALIZEDへ進める |
| AT-NET-028 | OVS Port を持つ RUNNING VM で、active libvirt NIC target と Agent 管理 Segment-to-Bridge mapping、OVS Port bridge/link state を typed read-backし、current Network/Segment/Mapping/Binding/Port generation と一致する場合だけ独立した dataplane projection を `CONVERGED` にする |
| AT-NET-029 | current Network/Port/Segment/Host mapping/Binding から immutable OVN Port intent を生成し、apply response loss 後も stable KIM ownership marker、intent generation、object digest の NB read-back と、matching datapath/chassis の SB read-back で同一 intent へ収束する。SB realization だけで Host dataplane または end-to-end convergence を主張しない |
| AT-NET-030 | current SB Port datapath の individual/shared Logical Flow から required ingress/egress pipeline と current Port identity coverage を評価し、expected Host chassis identity と許可された Geneve Encap registration/endpoint を read-backする。両 evidence が current intent/SB generation に一致する場合だけ OVN control-plane projection を `CONTROL_PLANE_CONVERGED` にする |
| AT-NET-031 | 異なる current Host の source/destination Port、両端 control-plane/chassis/mapping authority、Geneve interface identity を再検証し、方向付き probe の送信 packet が全て受信された immutable evidence だけを directed tunnel projection の `VERIFIED` へ昇格する。単一 Host 上の隔離 2 endpoint fixture は parser/kernel packet-path validation に限定し、実 2 Host qualification として記録しない |
| AT-NET-032 | Automatic IPAM の dry evaluation が authority row を変更せず、Final Admission が bounded IPv4 pool から excluded/protected identity を避けた concrete IP/MAC を Port/Binding/他 resource Claim と同一 transaction で確保する。stable request replay は同じ Admission/identity に収束する |
| AT-NET-033 | Port release 後の UNKNOWN、同一/旧 observation generation、最初の clean absence evidence では identity を `QUARANTINED` に維持し、二つ目の独立した current clean evidence 後だけ `RELEASED` にする。quarantine 中の explicit reuse は拒否し、release 後の reuse は一意に成功する |
| AT-NET-034 | 同一 Network generation の二つの Port intent が同じ deterministic Logical Switch identity/Network marker と、別々の Logical Switch Port identity/Port intent markerを生成し、順次 reconcile 後も共有 Logical Switch に Port intent markerを残さない |
| AT-NET-035 | production-shape OVN runtime が current Host mapping の Chassis reference、v2 typed plan、object-set digest を検証し、standard OVN CLI の単一 NB transactionで applyした後に NB/SB/chassisをread-backする。apply response lossでも同じ objectsへ収束し、foreign marker、plain TCP、非標準 executableをmutation前に拒否する |
| AT-NET-036 | 複数 OVN runtime worker が同じ current intent を claim しても PostgreSQL `SKIP LOCKED` により一つだけが取得する。claim expiry 後は旧 attempt に `DISPATCH_UNKNOWN` を追記して新 generation を `READ_BACK_FIRST` で取得し、matching read-back なら再 apply せず収束し、不一致時だけ current claim の apply authorization 後に同一 intent を再 reconcile する |
| AT-NET-037 | 実 `kim-network-worker` process を OVN apply 後・observation commit 前に `SIGKILL` し、claim expiry 後の別 process が generation 2 を `READ_BACK_FIRST` で取得する。保存済み OVN ownership を read-backして同一 intent へ収束し、物理 apply、`APPLY_AUTHORIZED`、terminal observation をそれぞれ一度だけ成立させる |
| AT-NET-038 | synchronous PostgreSQL standby が `remote_apply` した OVN claim/attempt/intentを保持した状態でprimaryを強制停止しstandbyをpromoteする。旧primary接続workerのcompletionをauthorityへ進めず、promoted primary上のgeneration 2 workerが`READ_BACK_FIRST`で収束し、committed LSN/authority generationと単一physical applyを維持する |
| AT-NET-039 | long-running typed OVN operation中にcurrent workerが同一claim generationを複数回renewし、各renewalがprior/new/maximum expiryとrenewal generationのimmutable evidenceを持つ。foreign/expired generationを拒否し、maximum lifetimeを超えず、synchronous DB failover後もrenewal evidenceを保持してexpiry後のgeneration 2 read-backへ収束する |
| AT-NET-040 | typed OVN apply の side effect 発生後、最初の renewal を PostgreSQL へ commit して response だけを失う。worker が operation を停止し、renewal evidence と extended expiry を保持し、successor が renewed expiry 前には claim できず、expiry 後の generation 2 `READ_BACK_FIRST` から同じ object を観測して physical apply 1 回のまま `OBSERVED` へ収束する |
| AT-NET-041 | fresh PostgreSQL に 512 work を backlog として投入し、16 worker、process batch 2、DB pool 64 で normal、long-running renewal、pre-apply failure、post-apply response-loss を混在させる。item-local adapter error を報告して long-lived reconciliation を継続し、DB authority error は process stop として分離する。全 work が bounded time 内に `OBSERVED` となり、全 worker が work を取得し、physical apply は object ごとに 1 回、attempt は initial 512 + 意図的 failure 104、最大 attempt 2、p99 batch completion は maximum claim lifetime 未満、worker/DB goroutine は bounded baseline へ戻る |
| AT-NET-042 | PostgreSQL primary A から synchronous standby B への failover 後、旧 A を B の新しい base backup から synchronous standby として再参加させ、B から A へ再度 failover する。両方向で long-running OVN apply の claim renewal を `remote_apply` 後に primary を hard stop し、old worker stop、committed LSN、同一 restore epoch/database authority generation、generation 2 `READ_BACK_FIRST`、attempt 2、physical apply 1 回を検証する |
| AT-NET-043 | fresh PostgreSQL に 96 work を投入し、8 workers / batch 2 / pool 16 のうち 8 connections を継続占有する。全 OVN observe/apply に 2 s latency、各 8 work 中 1 件ずつ pre-apply timeout と post-apply response-loss を注入し、Lease 1.5 s / renewal 300 ms / maximum 8 s で処理する。pool acquire wait が発生しても 96 work が 60 s 以内に `OBSERVED`、attempts 120、maximum attempt 2、全 worker 参加、physical apply 1 回、p99 が maximum lifetime 未満となることを 3 回連続で検証する |
| AT-NET-044 | 64 work を 2 workers で開始し、current claim 処理中に 4 workers を追加する。全 scale-up worker の claim participation 後、current batch を持つ 1 worker を `DRAINING` にし、new claim count を固定したまま renewal/completion 後に `STOPPED` へ進める。残り workers で 64/64 `OBSERVED`、owners 6、attempts 64、`DISPATCH_UNKNOWN` 0、physical apply 1 回へ収束し、全 metrics の in-flight が 0 になる |
| AT-NET-045 | 実 `kim-network-worker` process で、current apply 中の最初の signal が新規 claim を止めて current work を完了し終了 code 0 になることを確認する。post-apply read-back 中の 2 回目の signal と drain deadline 超過は終了 code 非 0 と ambiguous outcome を報告し、expiry 後の successor generation が `READ_BACK_FIRST` で収束する。各 fault で attempts 2、`DISPATCH_UNKNOWN` 1、physical apply 1 回を維持する |
| AT-STO-001 | Volume lifecycle/attach/detach/snapshotがtyped executionとverificationで収束する |
| AT-STO-002 | backend capability未対応時にsilent fallbackせずbounded errorを返す |
| AT-STO-003 | Volume、Backend Binding、Attachment Intent/Claim/Observationが独立generationとcurrent referenceを持つ |
| AT-STO-004 | concurrent SINGLE_WRITER attach requestが一つのactive Claimだけをcommitする |
| AT-STO-005 | READ_ONLY_MANYをcapability/policy一致かつ全active Claim read-only時だけ許可しSHARED_WRITERを拒否する |
| AT-STO-006 | attachがFinal Admission、typed backend/libvirt execution、DB/device/client verification後だけATTACHEDになる |
| AT-STO-007 | detachがsource I/O/device/backend client/lock releaseを検証してからClaimをreleaseする |
| AT-STO-008 | UNKNOWN AttachmentをBLOCKED/FENCE_REQUIREDに保ち反対操作と別Host writeを拒否する |
| AT-STO-009 | Storage Fencing Proofがcompute/storage/attachment fenceを別state/provenance/generationで保持する |
| AT-STO-010 | stale watcher/lock/device/holder observationをownership decisionへ昇格せずfresh resolverを要求する |
| AT-STO-011 | Ceph BindingをFSID/pool/namespace/image ID/features/secret referenceで管理しraw secretを永続化しない |
| AT-STO-012 | Local LVM BindingをHost/VG/LV UUID/localityで管理し別Host候補をineligibleにする |
| AT-STO-013 | Availability responsibility別のHost recoveryが同じAttachment/fencing gateを通る |
| AT-STO-014 | cold/live Attachment Handoffがsingle logical writerとsource/destination verificationを維持する |
| AT-STO-015 | Snapshot/Clone parent-child/consistency/deletion dependencyを管理する |
| AT-STO-016 | backend expandとguest-visible sizeを別generationで検証しpartial convergenceを表現する |
| AT-STO-017 | Volume delete guard、typed backend cleanup、absence verification、DB tombstone/GC分離を強制する |
| AT-STO-018 | backend-only/unknown storage objectをquarantineしexplicit Adoption/repair Operationを要求する |
| AT-STO-019 | force detach/client fence/lock break/delete/Adoptionを個別permission/approval/auditで保護する |
| AT-STO-020 | Storage Event/APIがgeneration/correlationを保持しsecret/raw backend/Host detailをredactする |
| AT-STO-021 | Ceph health/capability change時にaffected Volumeだけをpauseしsilent backend/access-mode fallbackしない |
| AT-STO-022 | Storage adapterがstable identity、typed side effect、UNKNOWN/read-back、secret/fencing evidence contractへ適合する |
| AT-STO-023 | Storage Capacity Poolがreserved/allocated/observed/external usageとthin data/metadata healthを分離しtransactional claim/releaseする |
| AT-STO-024 | closed typed Local LVM create を実 Host で実行し、Agent kill 後も同じ LV UUID を標準 LVM read-back で確認して immutable evidence と current BOUND Binding へ収束する |
| AT-STO-025 | current BOUND Local LVM Volume を実 Domain へ typed attach/detach し、各 Agent kill 後に libvirt device と LVM holder の一致/absence を確認して ATTACHED/DETACHED Claim authority へ収束し、遅延した旧 observation が current state を巻き戻さない |

## 13. NFV Dataplane

| ID | Acceptance Contract |
|---|---|
| AT-DPL-001 | OVS/DPDK version、datapath mode、runtime readinessをbounded capabilityへ正規化する |
| AT-DPL-002 | concurrent workload/emulator/PMD/service CPU claimが同一pCPUを二重確保しない |
| AT-DPL-003 | workload/DPDK/platform reserveをNUMA/page size別HugePage ledgerで競合判定する |
| AT-DPL-004 | PF/VF/representor/vhost/RxQをstable identityとcapabilityでinventory化する |
| AT-DPL-005 | PMD/Port/DPDK memory/VM memory/PCIのNUMA policy違反をeligibilityで拒否する |
| AT-DPL-006 | PMD CPU/DPDK memory/Port/RxQとVM resource claimを一transactionでcommit/rollbackする |
| AT-DPL-007 | vhost multiqueue/queue pair要求がVM Dataplane Bindingとadmissionへ伝播する |
| AT-DPL-008 | unknown/arbitrary OVSDB/EAL/PCI/shell operationをschema/Agent moduleが拒否する |
| AT-DPL-009 | restart-required変更がdisruptive Operation、impact set、maintenance authorityを要求する |
| AT-DPL-010 | successful Result後もPMD/RxQ/Port/runtime observationまでcompliantにならない |
| AT-DPL-011 | PMD cycles/utilization/drop変動だけでdurable allocationを変更しない |
| AT-DPL-012 | OVS-DPDK不適格時にkernel datapath等へsilent fallbackしない |
| AT-DPL-013 | Validated OVS/DPDK/distribution/NIC driver組合せだけをsupport matrix対象として公開する |
| AT-DPL-014 | PCI vendor/device、driver、NUMA node、IOMMU group、PF/VF reciprocal relationship を source evidence から canonical typed Fragment へ正規化する |
| AT-DPL-015 | qualification revision/profile、artifact/evaluator digest、observation/stack binding、validated operation set を immutable evidence として保存する |
| AT-DPL-016 | Observed AVAILABLE でも Qualification 欠損/STALE/UNKNOWN/REVOKED の device を allocation BLOCKED と判定する |
| AT-DPL-017 | current qualification/policy/NUMA/IOMMU を満たす VF claim だけを commit し、同じ VF への concurrent/second claim を一意制約と transaction で拒否する |
| AT-DPL-018 | exact VF retirement attempt 1 の response-lossを`DISPATCH_UNKNOWN`に残し、attempt 2 `READ_BACK_FIRST`のsame typed observationだけで一つの VERIFIED evidence/RELEASE_PENDINGへ収束する |
| AT-DPL-019 | wrong ownership/BDF/allocation generation、running source Domain、hostdev/driver/holder残存、IOMMU mismatchのいずれでもpositive retirementを拒否する |
| AT-DPL-020 | verified source retirement、destination Final Admission claim、VF handoff、SR-IOV libvirt hostdev realizationを同一Recovery evidence setへbindし、terminal前driftを拒否する |

## 14. Security / Audit / Documentation

| ID | Acceptance Contract |
|---|---|
| AT-SEC-001 | authentication/authorization/audit durabilityの一つでも失敗すればmutationをcommitしない |
| AT-SEC-002 | response/log/metric/diagnosticへsecret、生error、他Tenant identityを漏らさない |
| AT-SEC-003 | TLS/mTLS、certificate rotation/revocation、Tenant API/Network/Storage isolationを検証する |
| AT-AUD-001 | 認証・認可・mutationにactor/scope/resource/decision/result/correlation監査がある |
| AT-AUD-002 | 診断bundleが必要evidenceを含み、secretと非許可identityを除外する |
| AT-O11Y-001 | metrics/alarm/trace correlationを公開し、high-cardinality identityやsecretを含めない |
| AT-O11Y-002 | OVN worker の Prometheus endpoint が bounded lifecycle/execution/renewal/pool/backlog metrics を公開し、Host/Port/Work identity を含めず、metrics scrape failure を authority decision または worker failureへ昇格させない |
| AT-DOC-001 | 矛盾するRequirement/Accepted ADR/ArchitectureをCIが検出して失敗する |
| AT-DOC-002 | 重要ADR変更時にRequirement/Architecture/Invariant/Test trace未更新をCIが拒否する |
| AT-DOC-003 | 日本語spacing lintがproseの違反候補を検出し、code fence/inline code/URL/link destination/identifier/API path/約物を変更対象にしない |

## 15. HA / Upgrade / Packaging

| ID | Acceptance Contract |
|---|---|
| AT-HA-001 | 単一Control Plane node lossでAPIを継続しcommitted authorityを失わない |
| AT-UPG-001 | N-1→N rolling upgrade中にControl Plane quorum/serving capacity、committed authority、既存VMを維持する |
| AT-UPG-002 | Release Manifestがartifact digest、provenance/SBOM、dependency、contract/support range、migration、rollback boundaryをimmutableに保持する |
| AT-UPG-003 | Compatibility Decisionがsource/target Manifestとcurrent schema/protocol/backend/Host evidence generationへbindする |
| AT-UPG-004 | VALIDATED/COMPATIBLE/INCOMPATIBLE/UNKNOWNを区別しversion prefixやprocess aliveだけで昇格しない |
| AT-UPG-005 | preflightがupgrade path、artifact、quorum、schema、API/protocol/event、extension、backend/Host、rollback readinessを検証する |
| AT-UPG-006 | Upgrade Campaign/Plan/Wave/Target/Feature Gate/Rollback Boundaryのtransitionとapprovalを永続化する |
| AT-UPG-007 | canary/batchがimmutable target snapshot、max unavailable、failure threshold、pause/abort条件を強制する |
| AT-UPG-008 | N/N-1 mixed-version中のread/write/idempotency/generation/Lease semanticsとdeadlineを検証する |
| AT-UPG-009 | upgrade coordinatorだけではdomain Operation、Command Lease、Placement/Attachment/Binding authorityを取得できない |
| AT-UPG-010 | schema expand/migrate/switch/contractとrollback windowがData Architectureのmigration evidenceへ一致する |
| AT-UPG-011 | Gateway/AgentがprotocolとCommand/Result schemaをnegotiationし互換Commandだけをdispatchする |
| AT-UPG-012 | Agent updateがsigned artifact、drain、atomic activation、local receipt/read-back、preflight/Compliance再arming gateを通る |
| AT-UPG-013 | public API minor/major/deprecationとidempotency/ETag/resource/Operation identityを保存済みfixtureで検証する |
| AT-UPG-014 | old/new Event consumerがimmutable payloadをdecode/replayしretention中のschema catalogを維持する |
| AT-UPG-015 | extension/adapter/evaluator upgradeがcontract/certification、drain、shadow/canary、ownership fencingを満たす |
| AT-UPG-016 | Host/backend support matrixを実version/capability/provenanceで評価しunsupported targetをscope別にblockする |
| AT-UPG-017 | target releaseでexisting-workload continuityとnew create/migration/recovery capabilityを別判定する |
| AT-UPG-018 | support matrix/compatibility変更だけでは既存VM/Port/Volumeを停止、移動、再構成しない |
| AT-UPG-019 | rollback eligibilityがexplicit edge、last reversible phase、retained artifact/schema/decoder、current observationを要求する |
| AT-UPG-020 | rollback/abortを新Plan/Attemptとして記録し過去のTarget/Attempt/UNKNOWN evidenceを保持する |
| AT-UPG-021 | destructive finalization後のrollbackを拒否しforward repairへ遷移する |
| AT-UPG-022 | coordinator failover後にCampaign/Lease/Receipt/artifact observationから同じstepへ収束する |
| AT-UPG-023 | offline bundleがonline releaseと同じManifest/artifact/SBOM/migration/support/verification setを持つ |
| AT-UPG-024 | publish/start/switch/contract/feature activation/rollback/overrideを別権限、approval、auditで実行する |
| AT-UPG-025 | mixed-version期限、oldest component、schema/feature gate、rollback eligibility、blocked/UNKNOWN targetを観測できる |
| AT-UPG-026 | QEMU/libvirt upgrade後も既存VM machine type/CPU model/firmware/device ABI bindingを維持し新規defaultを別判定する |
| AT-UPG-027 | Event/evidence payload retention/holdとdecoder artifact referenceをManifestで追跡し参照中GCを拒否する |
| AT-UPG-028 | Feature Gate DAGのcycle/conflictを拒否しtopological activationとdependency closure逆順rollbackを行う |
| AT-UPG-029 | 実 `kim-network-worker` の N-1/N mixed-version を明示 compatibility edge と immutable artifact/schema evidence で許可する。N-1 drain 中は v2 work schema activation を拒否し、N-1 `STOPPED` 後だけ gate を開く。N worker だけが v2 work を release binding generation 付きで claim し、edge のない N-2 を `INCOMPATIBLE/FENCED` として claim pool から除外する |
| AT-UPG-030 | N-1 hard drain と同期 PostgreSQL primary kill を rolling upgrade 中に重ね、promoted primary 上の N worker が expired Attempt を `DISPATCH_UNKNOWN`、successor claim を `READ_BACK_FIRST` として single physical apply に収束する。edge/Manifest distrust 後は old binding generation を fence し、v2 activation と v1 rollback の両方を immutable schema transition evidence と current participant check 付きで実行する |
| AT-UPG-031 | product-wide Campaign / immutable Plan / ordered Wave / Target snapshotを component DAG、verified provenance、SBOM digestへ bindする。Coordinator claim generation 1 の expiry後、generation 2 が `RECOVER_FROM_DB` で accepted target evidenceを継承し、stale Resultを拒否する。canary全成功は次 Waveへ`ROLLING`、failure threshold超過は`PAUSED`とする |
| AT-UPG-032 | 実 `kim-upgrade-coordinator` process が DB-time claim を renew 中に SIGKILL され、synchronous PostgreSQL primary を hard stop して standby を promote する。successor process は replicated expiry 後に generation 2 / `RECOVER_FROM_DB` を取得し、pre-failover accepted Target と HOLD Decision を再利用する。stale generation Result を拒否し、remaining canary 成功後に次 Wave へ進む |
| AT-UPG-033 | 3 個の実 `kim-upgrade-target-executor` process が current Coordinator generation に bind された Target Attempt 1 を claim / renew し、closed typed backend side effect 後に SIGKILL される。各 successor は Attempt 2 / `READ_BACK_FIRST` で既存状態を read-back し、duplicate apply なしで 3 Target を `SUCCEEDED`、Campaign を次 Wave へ収束させる |
| AT-UPG-034 | 実 Ubuntu KVM Host 上の一意な Debian test package/systemd service を v1 から verified v2 package へ置換する。service restart と typed health 成立後、Result 前に実 Target executor を SIGKILL し、successor Attempt 2 / `READ_BACK_FIRST` が Target component identity、installed package version、systemd state/MainPID、configured executable path、running binary digest、health schema/version/PID/boot ID/start ticks を read-back して再 install なしで `SUCCEEDED` へ収束する |
| AT-UPG-035 | 実 Ubuntu KVM Host で dpkg database lock を別 process が保持中に Target Attempt 1 を実行し、package/version/install evidence が不変であることを確認する。Lease expiry 後の Attempt 2 `READ_BACK_FIRST` だけが `ABSENT` を確認して verified package を 1 回 install し、Result loss 後の Attempt 3 も read-back だけで `SUCCEEDED` へ収束する |
| AT-UPG-036 | 実 Ubuntu KVM Host の isolated package v3 `postinst` 開始後に executor control group を SIGKILL し、dpkg status が `half-configured` となることを確認する。Lease expiry 後の Attempt 2 は `READ_BACK_FIRST → CONFLICTING` として Target/execution を `FENCED`、Campaign を `PAUSED` にし、Result、configure 完了、automatic retry、Attempt 3 を生成しない |
| AT-UPG-037 | `FENCED` package Target に immutable `CONFIGURE_EXISTING` Recovery Plan と authorization を作成し、Recovery Attempt 1 の `READ_BACK_FIRST` が同じ package の `PACKAGE_HALF_CONFIGURED` を確認した場合だけ closed `dpkg --configure <fixed-package>` を実行する。package/service/process/health read-back の `MATCHED` 後に Recovery を `VERIFIED` とするが Target/execution は `FENCED` のまま維持し、別 rearm authorization 後だけ `PENDING` へ戻す。Plan、Result、rearm response-loss replay は同じ evidence へ収束し、Campaign は明示 resume まで `PAUSED` を維持する |
| AT-UPG-038 | HostGroup UPGRADE Snapshot-bound PlanをpublishしTargetからWave/Plan/Snapshot/Set/member provenanceを復元する。Coordinator recoveryとPAUSE/resume中のlive membership driftで同じTargetsを維持し、異Snapshot Plan raceは一方だけcomplete authorityとなり、FENCED Host executionを拒否する |
| AT-OFFLINE-001 | network非接続環境で署名済みbundleからinstall/upgradeできる |
| AT-DEPLOY-001 | Control Plane の署名済み containerized artifact を manifest と support profile に従って導入できる |
| AT-DEPLOY-002 | Kubernetes を使用しない supported deployment でも同一 Control Plane binary、schema、authority semantics を維持する |

## 16. Time and Clock Semantics

| ID | Acceptance Contract |
|---|---|
| AT-TIM-001 | Wall/DB/Process Monotonic/Agent Deadline/Observed/Received timeを型・用途・禁止用途付きで区別する |
| AT-TIM-002 | timestamp evidenceがsource、clock/boot ID、received/committed time、uncertainty/quality、generationを持つ |
| AT-TIM-003 | resource/Attempt/Event/Observation/restore orderingがgeneration/token/sequence/epochで安定する |
| AT-TIM-004 | Clock Observation/HealthがHEALTHY/DEGRADED/UNTRUSTED/UNKNOWNを用途別policyで評価する |
| AT-TIM-005 | Lease/not-before/expiry/retention snapshotをcurrent DB authority time/generation内でcommitする |
| AT-TIM-006 | DB clock anomaly時にnew Lease/renewal/GC/finalizationをpauseしnew generationから安全に再開する |
| AT-TIM-007 | Leaseがowner/purpose/scope、token/generation、not-before/expiry、maximum lifetime、renew/revoke evidenceを持つ |
| AT-TIM-008 | renewalがcurrent owner/token/generation/未失効条件を不可分検証しexpired tokenを復活させない |
| AT-TIM-009 | expiry後Resultをstale/duplicate accepted/UNKNOWNへ既存Execution契約どおり分類する |
| AT-TIM-010 | Gateway exchangeのserver sample、request send/receive monotonic、uncertainty marginからconservative Agent deadlineを導出する |
| AT-TIM-011 | Agentがdeadline derivationとboot/session/Lease generationをjournalしstart直前に再検証する |
| AT-TIM-012 | source observed、KIM received、verified timeとclock qualityを分離してfreshnessを評価する |
| AT-TIM-013 | stale/future/conflicting timestampをUNKNOWN/DEGRADEDへ分類しcritical decisionをfail closedにする |
| AT-TIM-014 | certificate/tokenの時間有効性とEnrollment/Role/Host/Command authorityを別gateとして評価する |
| AT-TIM-015 | timezone/DST policyからmaintenance/rollout UTC intervalをversion/generation付きでmaterializeする |
| AT-TIM-016 | missed/ended calendar windowで進行中operationを推測rollbackせずsafe pause/read-backへ送る |
| AT-TIM-017 | queue aging/rate/grace/deadlineをdurable DB window/token/Consumptionから再構築する |
| AT-TIM-018 | retention Candidate SnapshotがDB time、safety horizon、reference/hold/archive/backup guardを固定する |
| AT-TIM-019 | idempotency/Inbox/Receipt/decoder retentionがclient/Event replay、DR RPO、offline interval、legal holdを満たす |
| AT-TIM-020 | failure correlationがsource/received interval、uncertainty、topology、independent evidenceを要求する |
| AT-TIM-021 | process restart、DB failover、Host reboot、PITR後にold timer/Lease/sessionをgenerationでfenceする |
| AT-TIM-022 | clock anomaly中も既存VM/dataplaneを維持し影響するnew mutationだけをscope別にblockする |
| AT-TIM-023 | APIがUTC/offset、timestamp種別、freshness/expiry、bounded clock quality、server-side remaining durationを返す |
| AT-TIM-024 | metrics/alarmがoffset/uncertainty、clock step、Lease expiry unresolved、freshness、blocked scopeを公開する |
| AT-TIM-025 | clock正常化後もcurrent enrollment/preflight/Lease/credential generationなしにauthorityを再armしない |
| AT-TIM-026 | DB/Control Plane ClockReferenceSetが独立source、node相互観測、diversity、provenance/uncertaintyを評価する |
| AT-TIM-027 | Precision Time DomainのPTP/GNSS/holdover qualityをHost capability/Complianceとして扱いauthority clockと分離する |
| AT-TIM-028 | leap second/smear policy/windowを宣言し異policy混在時のuncertainty/DEGRADED/UNKNOWNを評価する |

## 17. PKI and Trust Lifecycle

| ID | Acceptance Contract |
|---|---|
| AT-PKI-001 | Control Plane/Agent/integration/backend/artifact/data protectionのtrust/key domainとexplicit cross-trustを分離する |
| AT-PKI-002 | external Identity Platform Principalとworkload certificate identityを別authority/bindingとして評価する |
| AT-PKI-003 | Rootをoffline/external custody、Agent/Control Planeをpurpose/Site別Intermediateでissuanceする |
| AT-PKI-004 | Certificate ProfileがSAN namespace、EKU/key usage、path/name constraint、algorithm/lifetime/provenanceを制限する |
| AT-PKI-005 | KIM DBがprivate key valueを持たずopaque Secret Provider reference/version/public fingerprintだけを保持する |
| AT-PKI-006 | TrustBundle/Profile/Issuer/Relationship/Revocation sourceをimmutable revision/trust generationでpublishする |
| AT-PKI-007 | certificate validation後にもRole/Enrollment/Host authority/Command Lease/backend authorizationを個別評価する |
| AT-PKI-008 | Trust Decisionがpeer fingerprint、Bundle/profile/revocation/clock/Binding/session generationとevidenceへbindする |
| AT-PKI-009 | one-time bootstrap materialがSite/Host/policy/nonce/max-useへbindしreplay/共有credentialを拒否する |
| AT-PKI-010 | Agent CSRがhardware/Enrollment evidence、challenge、profile、key provenance、proof-of-possessionを満たす |
| AT-PKI-011 | issuance response lossでrequest/CSR/key digestから同じBinding/certificate receiptへ収束する |
| AT-PKI-012 | renewal/rekeyがnew Credential Binding revision、current identity/policy/trust/proofを保持する |
| AT-PKI-013 | overlap中にnew sessionを検証してoldをdrainし一logical identity/authority generationを維持する |
| AT-PKI-014 | Authenticated Sessionがmaximum lifetime、periodic revalidation、trust/authority generation fencingを持つ |
| AT-PKI-015 | revocation lifecycleがrequest/local enforce/distribute/propagation verifiedをsequence/receipt付きで区別する |
| AT-PKI-016 | certificate、issuer、algorithm、profile、namespaceのdistrustをscope別に評価する |
| AT-PKI-017 | Host compromise flowがdisarm/revoke/session fence/evidence quarantine/resource observation/re-enrollmentを実行する |
| AT-PKI-018 | Control Plane compromise flowがcertificate/endpoint/DB/Bus/Secret/backend/Leaseを個別containする |
| AT-PKI-019 | normal CA rolloverがdual Bundle、receipt、canary/batch、issuance switch、old anchor absence proofを通る |
| AT-PKI-020 | CA compromise時にindependent recovery authority/two-person approvalでnew anchorをauthorizeしold issuerをdistrustする |
| AT-PKI-021 | Secret Provider outage/rotation/claimとpublic certificate/Bundle/session verificationを分離する |
| AT-PKI-022 | offline bootstrap/updateがsigned manifest、anchor fingerprint、sequence、previous digest、expiry、approvalを検証する |
| AT-PKI-023 | PITR/DR後にrestore/trust generationでold session/Leaseをfenceしrevocation/issuer stateを再取得する |
| AT-PKI-024 | Trust publish/issuance override/revoke/distrust/rollover/emergency/Secret administrationの権限とapprovalを分離する |
| AT-PKI-025 | metrics/auditがtrust/credential/session/revocation/rolloverを追跡しsecret/raw identityをredactする |
| AT-IMG-001 | Image create/replay/ETag/RBAC/public schemaがexpected digestだけを受理しobserved digest/path/URL/Host/credentialを拒否する |
| AT-IMG-002 | typed ingestionがbounded staging、fsync、whole SHA-256、atomic publishを行いpartial/mismatch/arbitrary pathを拒否する |
| AT-IMG-003 | immutable Command verification IDからobserved digestを導出し、Result LOSTでもMATCHED read-backからOperation/ImageをVERIFIEDへpublishする |
| AT-IMG-004 | cache消失・別Host realizationがlogical desired revisionを変更せず、同名別content ABAをdigest read-backで拒否する |
| AT-IMG-005 | verified revisionを参照するVM/materializationをImage patch/deleteがretrofitまたはsilent fallbackしない |
| AT-NET-046 | non-empty Network Recovery が同一Port/MAC/IP、source quiescence、PortBindingHandoff、destination NB/SB/OVS evidenceを同一PostgreSQL historyへ固定し、TerminalだけがRecovery成功を確定する |
| AT-NET-047 | exact Port generation/Binding generation/source Hostからgeneric OVN `UNBIND` workを発行し、typed adapterがlogical PortとKIM ownership markerを保持したままrequested chassisだけを解除する。mutation response lossをgeneration 1 `DISPATCH_UNKNOWN`へ固定し、generation 2 `READ_BACK_FIRST`がNB/SB/source OVSを観測して一つの`VERIFIED` retirementへ収束する |
| AT-NET-048 | 同一Port/MAC/IPを保った通常`PortBindingHandoff`で`1/1 → 2/2 → 3/3`を進め、`1/1`と`2/2`のretirement evidence/current projectionを独立保持する。R1 replayは元workへ収束し、R1 quiescenceはR2 handoffをauthorizeせず、latest retirement/Handoff projectionだけが後続generationを指す |
| AT-NET-049 | logical Networkを作成/replay/updateし、stable ID、新immutable revision、Project ownership、MTU/name変更、backend UUID非desired性、evidence UPDATE拒否を検証する |
| AT-NET-050 | AUTO VNIのserializable concurrent allocation、EXPLICIT collision、release pending中のreuse拒否、absence terminal後のABA reuse、old release replay fencingを検証する |
| AT-NET-051 | standalone typed Logical Switch create/update/retireでexact ownership marker、response-loss後read-back、partial/foreign object拒否、backend incarnation replacement、absence terminalを検証する |
| AT-NET-052 | new-authority NetworkがPENDING中はPlacement eligibilityを持たず、exact VERIFIED terminal後だけconsumer条件を満たす |
| AT-NET-053 | legacy foundation adapterがexact new NetworkへSubnet projectionを追加してもNetwork/segment sourceを変更せず、Port planが同じLogical Switch identity/Network markerを再利用する |
| AT-NET-054 | Subnet create/replay/updateがstable ID、immutable revision、Project/exact Network binding、canonical IPv4 CIDR、gateway/reservation/DNS、same-Network overlap rejectionを検証する |
| AT-NET-055 | typed standalone DHCP realize/update/retireがexact parent Logical Switch/ownership/optionsをread backし、foreign/wrong association/response loss/backend UUID replacementをfenceする |
| AT-NET-056 | AUTO/EXPLICIT IPAMのreplay/collision/concurrency/stale revision/release/ABAをimmutable decision/current/release evidenceで検証する |
| AT-NET-057 | active allocation/Port/delete protectionがSubnet retirementを拒否し、pool freeze後のallocationを拒否し、exact DHCP absence後だけDELETED/RETIREDへ進む |
| AT-NET-058 | PENDING SubnetはPlacement eligibilityを持たず、exact Network/Subnet VERIFIED後のFinal AdmissionだけがIPAM/Port/IP/MAC/Bindingを不可分にcommitする |
| AT-NET-059 | standalone Port create/replay/updateがstable ID、immutable desired revision、verified Network/Subnet dependency、valid unattached state、evidence UPDATE拒否を維持する |
| AT-NET-060 | AUTO/EXPLICIT MACとMigration 078 IPAMをPort desiredと不可分にcommitし、duplicate/stale/caller AUTO/partial authorityを拒否する |
| AT-NET-061 | explicit attachment request後のFinal Admissionがexisting exact Port/MAC/IPをconsumeし、Port revisionを変えずHost bindingとattached realizationだけを作る |
| AT-NET-062 | Recovery A→BとEVACUATE B→Cがsame Port revision/MAC/IPを維持し、attachment/binding/realization generationsとimmutable old historyだけを進める |
| AT-NET-063 | unattached Port retirementがLSP exact absenceまでMAC/IPをRELEASE_PENDING相当に保持し、一terminal transactionだけがidentity releaseとPort DELETEDを確定する |
| FI-NET-041 | concurrent MAC/IP collision、stale Subnet/Port revision、wrong attachment intent、old allocation release ABAがcurrent Portまたはnew ownerを進めない |
| FI-NET-042 | standalone Port apply response loss後、successorがREAD_BACK_FIRSTでwrong MAC/IP/Network/Chassis/marker/digestを拒否しexact stateだけへ収束する |
| FI-NET-043 | active binding、delete protection、backend LSP present、wrong absence generationのPort retirementがidentity release/reuseを発生させない |

## 18. Performance Tests

| ID | Performance Contract |
|---|---|
| PT-SCALE-001 | 100 Host、5,000 VM、10,000 Portでinventory/reconciliationを継続する |
| PT-API-001 | 通常負荷のread API p95が500 ms以下である |
| PT-OPS-001 | 50 concurrent mutation Operationでauthority/invariant違反なくdispatch p95目標を測定する |
