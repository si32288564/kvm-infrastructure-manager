# Architecture Invariants

- 状態: Baseline
- 更新日: 2026-08-09

## 1. 目的

実装、レビュー、テストが絶対に破ってはいけない条件をID化します。Invariantに違反する実装は、機能的に動作していても受け入れません。

## 2. 運用規則

- Invariant IDは再利用しない。
- 内容を弱める変更はADRを必要とする。
- Requirement、Architecture、ADR、TestはInvariant IDを参照する。
- 自動検証できないInvariantには、review gateまたはoperational evidenceを割り当てる。
- `Proposed` ADRに依存するInvariantはPhase 0 gateでADR Accepted後に実装authorityとなる。

## 3. Authority and Identity

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-AUTH-001 | User/Northbound Service Principal credential authorityは外部Identity Platformであり、KIMはそのcredentialを発行しない | AT-AUTH-001 |
| INV-AUTH-002 | KIMはissuer+subjectでPrincipalを識別し、Tenant/Project MembershipとRole Bindingを所有する | AT-AUTH-002 |
| INV-AUTH-003 | Host credentialはidentityを証明するが、Host mutation authorityそのものではない | AT-AGT-003 |
| INV-AUTH-004 | stale authority generationはCommand LeaseとResultを進められない | FI-SPLIT-001 |
| INV-AUTH-005 | backend observationだけでresourceをmanagedへ自動adoptしない | FI-DR-001 |

## 4. API and Data

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-API-001 | Host/backend mutationを同期API request内で実行しない | AT-API-001 |
| INV-API-002 | 同じidempotency scope/keyと同一payloadは同じOperation/結果へ収束する | AT-API-002 |
| INV-API-003 | 同じidempotency keyの異なるpayloadはconflictになる | AT-API-003 |
| INV-DATA-001 | desired state、allocation、attachment、execution authorityはPostgreSQL commitでのみ確定する | AT-DATA-001 |
| INV-DATA-002 | desired stateとobserved stateを別resource/generationとして保持する | AT-DATA-002 |
| INV-DATA-003 | terminal Job/Attempt/audit historyを結果に合わせて書き換えない | AT-EXEC-007 |
| INV-DATA-004 | Derived ProjectionとMessage delivery状態をresource/ownership/execution authorityにしない | AT-DATA-003 |
| INV-DATA-005 | domain mutation、Operation/idempotency、Outbox Eventを一つのtransactionでcommit/rollbackする | FI-DATA-001 |
| INV-DATA-006 | Inboxの同一source/generation/message ID+digestは同じReceiptへ収束し、異なるdigestはconflictにする | FI-DATA-003 |
| INV-DATA-007 | current authorityが参照するDecision/Evidence、UNKNOWN、open Operation、active Lease/Claim、legal holdをGCしない | FI-DATA-004 |
| INV-DATA-008 | DB GC/partition detach/archiveはbackend resource mutationを開始しない | AT-DATA-008 |
| INV-DATA-009 | tombstoneはresource identity、scope、final generation、delete decision、integrity digestを保持する | AT-DATA-007 |
| INV-DATA-010 | partitioningはauthority uniqueness、transactional admission、Tenant isolationを分裂させない | AT-DATA-009 |
| INV-DATA-011 | schema switch前にN/N-1 reader/writer compatibilityとrequired replica capabilityを検証する | FI-DATA-007 |
| INV-DATA-012 | migration/backfillはsingle Lease、artifact digest、checkpoint、bounded lock/batch、verificationを持つ | AT-DATA-011 |
| INV-DATA-013 | backfillはcurrent generationの並行更新を上書きせず、retryで同じ結果へ収束する | FI-DATA-006 |
| INV-DATA-014 | restore可能なbackupはbase/WAL/schema/migration/artifact/checksumを一つのmanifestへbindする | FI-DATA-008 |
| INV-DATA-015 | PITR後のrestore epochはpre-restore Lease、session、worker/publisher claimをcurrent authorityからfenceする | FI-DATA-009 |
| INV-DATA-016 | restore後はread-only classification/reconciliation前にmutation authorityを再開しない | AT-DATA-015 |
| INV-DATA-017 | BACKEND_ONLY/CONFLICTING/UNKNOWN resourceを自動adopt/deleteしない | FI-DATA-011 |
| INV-DATA-018 | PITR後の再送はstable ID/Receiptでdeduplicateし、外部side effect不明をUNKNOWNとしてread-backする | FI-DATA-010 |
| INV-DATA-019 | restore epochだけで旧Site/primaryをfencedとみなさず、外部DR fencing proofなしに通常mutationを再開しない | FI-DATA-013 |
| INV-DATA-020 | authority/history/archive referenceをhard DB、verified logical、archive referenceへ分類し、欠損/不一致scopeのmutation/GCを停止する | FI-DATA-014 |
| INV-DATA-021 | Recovery Control writeは専用identity/DB role/API/DR generation/approval/auditを要求し、通常resource/backend mutation権限を持たない | FI-DATA-015 |

## 5. Placement

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-PLC-001 | eligibility=falseのHostはscoreに関係なく選択されない | AT-PLC-001 |
| INV-PLC-002 | dry evaluationはDB/backend/Agent/Busへ副作用を起こさない | AT-PLC-002 |
| INV-PLC-003 | final admissionはlatest authority stateへ同じadmission ruleを再適用する | AT-PLC-003 |
| INV-PLC-004 | compute/NUMA/HugePages/PCI/network/storage/quota claimは一つのtransactionで不可分にcommitする | AT-PLC-004 |
| INV-PLC-005 | final admission競合時は部分予約を残さず、残候補の再選択または再評価へ戻る | AT-PLC-005 |
| INV-PLC-006 | final admission transaction中にbackend side effectを実行しない | AT-PLC-006 |
| INV-PLC-007 | migration capabilityはVM/resource bindingとsource/destinationの組合せで評価する | AT-PLC-007 |

## 6. Execution

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-EXEC-001 | Commandごとにactive Leaseは最大一つ | AT-EXEC-001 |
| INV-EXEC-002 | Agentはbackend mutation前にCommandをdurable journalへwrite-before-executeする | FI-AGENT-001 |
| INV-EXEC-003 | Lease失効後に初めて届いた旧Attempt Resultはauthorityを変更できない | FI-TRANSPORT-001 |
| INV-EXEC-004 | durably accepted済みの同一Result再送だけが同じreceiptを得られる | AT-EXEC-004 |
| INV-EXEC-005 | UNKNOWNをFAILED/SUCCEEDEDへ書き換えず、verification evidenceを追記する | AT-EXEC-005 |
| INV-EXEC-006 | Agent Resultの成功だけではJobを成功にせず、後続observationを必要とする | AT-EXEC-006 |
| INV-EXEC-007 | Attemptはappend-onlyで、stale Resultは新Attemptを進めない | FI-TRANSPORT-001 |
| INV-EXEC-008 | UNKNOWN状態で反対mutationを推測実行しない | FI-STORAGE-001 |
| INV-EXEC-009 | active Command Lease は発行時の Host authority generation と Agent session generation の両方が current の間だけ使用できる | AT-EXEC-008、FI-TRANSPORT-003 |
| INV-EXEC-010 | Lease expiry、Host authority fence、session generation 変更後に旧 Lease/Attempt が再び current authority へ戻らない | AT-EXEC-009、FI-TRANSPORT-003 |
| INV-EXEC-011 | Gateway の live outbound registry は routing projection に限定し、PostgreSQL Session Grant と一致しない Host/session generation へ Command を配送しない | AT-EXEC-010、FI-GATEWAY-003 |
| INV-EXEC-012 | Agent は compile-time registered typed backend だけを実行し、journal 完了前の Result または read-back 未確認の success を authority へ進めない | AT-EXEC-010、FI-AGENT-001/002 |
| INV-EXEC-013 | UNKNOWN Command の resync は既存 write-before-execute journal evidence を新規生成または改変せず、current authorized session と immutable Command/Attempt/digest/target identity が一致する場合だけ read-back observation を受理する | AT-EXEC-012、FI-AGENT-005 |
| INV-EXEC-014 | read-only verification は fenced Host mutation authority を暗黙 rearm せず、matching observation を append して current Command/Job decision だけを収束させる | AT-EXEC-012、FI-TRANSPORT-004 |
| INV-EXEC-015 | Agent session runtime は inbound routing、outbound multiplexing、durable Receipt 処理を一つの current transport session で駆動し、transport loop termination を backend side effect の absence と解釈しない | AT-EXEC-013、FI-TRANSPORT-004 |
| INV-EXEC-016 | local session generation ledger は SessionAccepted 後だけ fsync/atomic rename で進み、rejected/failed attempt、reconnect timer、process start だけでは generation を消費または authority として確定しない | AT-EXEC-014、FI-AGENT-006 |
| INV-EXEC-017 | Worker の Lease expiry scan は discovery に限定し、各 Lease の current state/DB time/Host authority scope を transaction で再検証してから既存 UNKNOWN semantics を適用する | AT-EXEC-015、FI-TRANSPORT-001 |

## 7. Agent and Host

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-AGT-001 | Control Plane/Agentはarbitrary shell、argv、libvirt method/XML/pathを受理しない | AT-AGT-001 |
| INV-AGT-002 | Agentは内部Message Bus credentialを持たず、Gateway経由で通信する | AT-AGT-002 |
| INV-AGT-003 | Command実行にはHost identity、capability、armed authority、current Leaseがすべて必要 | AT-AGT-003 |
| INV-AGT-004 | Gateway障害中、Agentは新規/cached mutationまたは自律rollbackを開始しない | FI-GATEWAY-001 |
| INV-AGT-005 | Gateway復旧またはAgent再起動だけでHost authorityをarmしない | FI-GATEWAY-002 |
| INV-AGT-006 | OS差分はAgent adapterで正規化し、Control Planeをdistribution名で分岐させない | AT-AGT-006 |
| INV-AGT-007 | Host mutationは閉じたtyped remediationに限定し、汎用Configuration Managementを提供しない | AT-AGT-007 |
| INV-AGT-008 | KIM core functionはLinux KVM、QEMU、libvirtのpatch、fork、proprietary modificationを要求しない | AT-AGT-008 |
| INV-AGT-009 | KIM metadataの有無によってunderlying resourceを標準libvirt/QEMU/KVM interfaceから扱えなくしない | AT-AGT-009 |
| INV-AGT-010 | KIM Host AgentはGoをprimary implementation languageとし、cgo/native helperをnarrow audited boundaryに限定する | AT-AGT-010 |
| INV-AGT-011 | Agent module/capability 数を Host identity あたりの mTLS connection/certificate 数へ連動させない | AT-AGT-011 |
| INV-AGT-012 | 一つの Host Agent identity に PostgreSQL current transport session generation は最大一つで、live socket を authority にせず stale session の全 message は current authority を進めない | FI-GATEWAY-003 |
| INV-AGT-013 | transport loss を module/resource authority loss または operation 失敗の証明にせず、UNKNOWN/journal/read-back で解決する | FI-GATEWAY-004 |
| INV-AGT-014 | transport implementation、connection、certificate を Agent capability/module authorization の代替にしない | AT-AGT-012 |
| INV-AGT-015 | logical stream は bounded message/queue と priority-aware backpressure を持ち、bulk stream が Control/Lease/Heartbeat/Result を無期限 starve させない | FI-GATEWAY-005 |
| INV-AGT-016 | transport arrival 順を global resource ordering にせず、ordering scope ごとの sequence/generation/idempotency contract を使用する | AT-AGT-013 |
| INV-AGT-017 | 別 endpoint/connection は明示的な別要件と contract/approval なしに追加しない | AT-AGT-014 |
| INV-AGT-018 | L7 forwarded Agent identity は pinned proxy workload identity と sanitized downstream certificate evidence が同時に成立する場合だけ受理し、header 単独を identity authority にしない | AT-AGT-015 |
| INV-AGT-019 | GOAWAY、proxy drain、rolling restart、upstream connection pool の生存を Host session authority transition にせず、new current session は PostgreSQL Grant commit を必須とする | FI-GATEWAY-006 |
| INV-AGT-020 | connection idle と stream idle を混同せず、active Agent stream の liveness/authority を proxy timer だけで確定しない | FI-GATEWAY-007 |
| INV-AGT-021 | Agent durable message は write-before-send とし、transport send/Receipt delivery を PostgreSQL acceptance commit と同一視せず、matching durable `ACCEPTED` Receipt だけが spool entry を解放できる | FI-GATEWAY-008 |
| INV-AGT-022 | session generation 変更後の同一 message replay は original Receipt へ冪等収束し、stale/new session、response loss、restart のいずれも duplicate decision または evidence rewrite を起こさない | AT-AGT-016 |
| INV-AGT-023 | Inventory module は descriptor で宣言した closed typed domain/schema/capability の外へ evidence を出せず、一つでも module collection が失敗した snapshot を current capability projection にしない | FI-AGENT-003 |
| INV-AGT-024 | Host capability projection は immutable normalized Inventory evidence からだけ導出し、同一 generation の異なる digest を拒否し、古い generation で current projection を巻き戻さない | AT-HST-005 |
| INV-AGT-025 | OS Integration Adapter は raw source の read/parse outcome を typed evidence state と reason code へ変換し、Normalizer は provenance を失わず Snapshot/Projection へ伝播する | AT-HST-006 |
| INV-AGT-026 | AVAILABLE、UNAVAILABLE、UNKNOWN、UNSUPPORTED は相互に置換せず、UNKNOWN を含む snapshot は DEGRADED とし、既知の UNAVAILABLE/UNSUPPORTED だけを理由に observation 全体を UNKNOWN にしない | FI-AGENT-004 |

## 8. Network and Storage

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-NET-001 | KIMはvirtual network/provider bindingを所有し、WAN/inter-PoP/physical switch authorityを暗黙取得しない | AT-NET-001 |
| INV-NET-002 | 未知または外部所有network objectを自動削除しない | FI-NET-001 |
| INV-NET-003 | active IP/MAC Claimは定義scopeで一意で、Port/Identity allocationと不可分commitする | FI-NET-003 |
| INV-NET-004 | Port/NAT/DHCP/binding/dataplane absenceとquarantine完了前にIP/MACを再利用しない | FI-NET-004 |
| INV-NET-005 | VLAN/VNI Claimをscope内で一意にし、reference/dataplane absence前に再利用しない | FI-NET-005 |
| INV-NET-006 | KIM authority、Intent Revision、OVN NB、OVN SB、Host/dataplane observationを別generation/stateで保持する | AT-NET-007 |
| INV-NET-007 | Port ACTIVEはcurrent DB Bindingとbinding-type別NB/SB/Host/dataplane verification後だけ確定する | FI-NET-007 |
| INV-NET-008 | 一般Portのactive Binding Claimは最大一つで、handoffは二つの通常active authorityを作らない | FI-NET-008 |
| INV-NET-009 | Network adapterはCore DB/claimへwriteせずtyped plan/apply/observeだけを実行する | XCT-NET-001 |
| INV-NET-010 | network-side UNKNOWNでidentity/segment再利用、反対操作、blind rebind、security緩和を行わない | FI-NET-006 |
| INV-NET-011 | DHCP lease/runtime observationをIP Allocation authorityにしない | AT-NET-012 |
| INV-NET-012 | Floating IP/NAT/Gateway Claimとdependencyをtransactionalに確定し、UNKNOWN中に再利用しない | FI-NET-011 |
| INV-NET-013 | Security Policy realization不明時にdefault allowへfallbackしない | FI-NET-013 |
| INV-NET-014 | required MTUを満たさない、またはpath capability UNKNOWNのHost/segment/gatewayをeligibleにしない | FI-NET-014 |
| INV-NET-015 | SR-IOV/DPDK Port claimをPCI/PMD/RxQ/NUMAと不可分commitしbinding typeをsilent fallbackしない | AT-NET-020 |
| INV-NET-016 | Host recovery/migrationはold Binding/Host/device authorityをfenceしnew generationを検証する | FI-NET-009 |
| INV-NET-017 | active dependency/UNKNOWN中のNetwork resourceを削除せず、DB GCとOVN/Host cleanupを分離する | FI-NET-015 |
| INV-NET-018 | backend-only/foreign OVN object、unknown interface/chassisを自動adopt/delete/unbindしない | FI-NET-016 |
| INV-NET-019 | Provider mappingはphysical/WIM capability referenceでありswitch/WAN authorityをKIMへ移さない | AT-NET-015 |
| INV-NET-020 | provider pool/gateway/force operation/Adoptionは個別permission/approval/auditを要求しraw topology/credentialをredactする | FI-NET-017 |
| INV-STO-001 | attachment outcomeまたはsingle-writer fencingが不明なVolumeを別Hostへattachしない | FI-STORAGE-001 |
| INV-STO-002 | Volume backend capability差を明示し、未対応機能へsilent fallbackしない | AT-STO-002 |
| INV-STO-003 | Volume desired state、Backend Binding、Attachment Intent/Claim、backend/libvirt Observationを別generationで保持する | AT-STO-003 |
| INV-STO-004 | SINGLE_WRITER Volumeのactive Attachment ClaimはPostgreSQL constraintで最大一つ | FI-STORAGE-003 |
| INV-STO-005 | READ_ONLY_MANYは明示capabilityと全active Claim read-onlyを要求し、未certified SHARED_WRITERを拒否する | AT-STO-005 |
| INV-STO-006 | ATTACHEDはcurrent DB Claim、libvirt device、backend client/lock evidenceの一致後だけ確定する | AT-STO-006 |
| INV-STO-007 | DETACHED/Claim releaseはsource I/O pathとbackend client/lock releaseのverification後だけ確定する | FI-STORAGE-004 |
| INV-STO-008 | Attachment outcome/I/O ownershipがUNKNOWNなら反対操作、Claim release、別Host write attachを開始しない | FI-STORAGE-005 |
| INV-STO-009 | 別Host write authorityはcompute source、storage client、attachment authority fencingをすべて必要とする | FI-STORAGE-006 |
| INV-STO-010 | watcher/lock/blocklist/device/holder observation単独ではAttachment authorityを作成・譲渡・解放しない | FI-STORAGE-005 |
| INV-STO-011 | Ceph Bindingはstable image identityとscoped secret referenceを持ち、name/secret valueをauthority metadataにしない | AT-STO-011 |
| INV-STO-012 | Local LVM VolumeはHost/VG/LV identityへbindし、certified replication/exportなしに別Host recoveryしない | FI-STORAGE-009 |
| INV-STO-013 | Host recoveryはold/new Attachment generationと全storage fencing/eligibilityを再検証する | FI-STORAGE-008 |
| INV-STO-014 | migration handoffは一つのlogical write authorityを維持し、一般的な二active writer Claimを作らない | FI-STORAGE-011 |
| INV-STO-015 | Snapshot/Clone dependencyとconsistency evidenceを保持し、未証明application consistencyを表示しない | AT-STO-015 |
| INV-STO-016 | active/pending Attachment、child、Recovery/Migration/UNKNOWN/hold中のVolumeを削除せず、DB GCとbackend cleanupを分離する | FI-STORAGE-012 |
| INV-STO-017 | backend-only image/LV、unknown watcher/lock、unmatched deviceを自動adopt/delete/detachしない | FI-STORAGE-014 |
| INV-STO-018 | force detach/client fence/lock break/backend delete/Adoptionは個別permission/approval/auditを要求する | FI-STORAGE-015 |
| INV-STO-019 | Storage capacityはtransactional ledgerでclaimし、stale/UNKNOWN backend usageを空きへ丸めずbackend absence前に再利用しない | FI-STORAGE-017 |

### NFV Dataplane

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-DPL-001 | 一つのexclusive pCPUをworkload/emulator/PMD/service roleへ二重claimしない | AT-DPL-002 |
| INV-DPL-002 | workload HugePagesとDPDK socket memoryを同じ物理poolのpurpose別ledgerで予約する | AT-DPL-003 |
| INV-DPL-003 | PMD、Port、DPDK memory、VM memory、PCIのNUMA localityをeligibilityで評価する | AT-DPL-005 |
| INV-DPL-004 | PMD CPU、DPDK memory、Port/RxQ claimを他resourceと同じtransactionで不可分commitする | AT-DPL-006 |
| INV-DPL-005 | PMD utilization/cycles/drop telemetryをallocation authorityとして使用しない | AT-DPL-011 |
| INV-DPL-006 | dataplane desired allocationとobserved OVS/DPDK bindingを別generationで保持する | AT-DPL-010 |
| INV-DPL-007 | restart-required dataplane変更を通常VM作成から暗黙実行しない | AT-DPL-009 |
| INV-DPL-008 | arbitrary OVSDB/EAL/PCI/shell操作をAPI/Command/Extensionで受理しない | AT-DPL-008 |
| INV-DPL-009 | OVS-DPDK不適格時にkernel datapath等へsilent fallbackしない | AT-DPL-012 |
| INV-DPL-010 | PCI/PMD/OVS mutation結果不明時はresourceをquarantineしblind replay/rebindしない | FI-DPDK-005 |
| INV-DPL-011 | Observed/Normalized PCI capability は Qualification または Allocation authority ではなく、fixture parser pass を hardware qualification として使用しない | AT-DPL-014 |
| INV-DPL-012 | Qualification Evidence は immutable とし、binding 対象の observation/stack/evaluator/artifact/operation set が変化した場合は CURRENT を継承しない | FI-PCI-002 |
| INV-DPL-013 | Qualification Binding が STALE、UNKNOWN、REVOKED、または欠損なら allocation state を BLOCKED とし、Observed AVAILABLE から自動昇格しない | FI-PCI-001 |
| INV-DPL-014 | VF claim は current Host/device/qualification/policy/NUMA/IOMMU generation と active claim 不在を一 transaction で再検証し、device ごとに一つの active/release-pending claim だけを許可する | AT-DPL-017 |

## 9. Host Lifecycle and Compliance

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-HLC-001 | Host authentication/credential発行だけではenrollment、READY、mutation authorityのいずれも成立しない | AT-HLC-002 |
| INV-HLC-002 | Host ProfileとHost Baseline versionはimmutableで、assignmentと適用generationを明示する | AT-HLC-004 |
| INV-HLC-003 | Compliance Resultとevidenceはappend-onlyで、UNKNOWNを推測して別statusへ丸めない | AT-HLC-006 |
| INV-HLC-004 | blocking controlのNON_COMPLIANT/DEGRADED/UNKNOWNは定義されたHostまたはcapability scopeをplacement不適格にする | AT-HLC-007 |
| INV-HLC-005 | auto-remediate-safeもenrollment、current assignment、armed authority、Command Lease、Agent journalを迂回しない | FI-HLC-005 |
| INV-HLC-006 | Host preflightとcompliance evaluationは副作用を起こさない | AT-HLC-005 |
| INV-HLC-007 | Host/Agentは自身のapproval、Profile、Baseline、Control policyを変更できない | AT-HLC-015 |
| INV-HLC-008 | reconnect、credential renewal、Gateway recovery、Baseline assignmentだけでHost authorityをarmしない | FI-HLC-008 |
| INV-HLC-009 | external-remediation modeはKIMからHost mutationを開始しない | AT-HLC-013 |
| INV-HLC-010 | decommissionはauthority/Leaseをfenceし、managed resourceをdrainし、credentialを失効するまで完了しない | AT-HLC-014 |
| INV-HLC-011 | duplicate Host identity/hardware fingerprint conflictを自動mergeせずquarantineする | FI-HLC-002 |
| INV-HLC-012 | Baseline rolloutは旧version/resultを改変せず、rollbackを自動的なHost state復元とみなさない | FI-HLC-006 |
| INV-HLC-013 | 単一の可変hardware identifierまたはAgent自己申告だけでpolicy-auto enrollmentしない | AT-HLC-018 |
| INV-HLC-014 | Enrollment decisionはsource/issuer/provenance/freshness/conflictを持つidentity evidence setとpolicy generationへbindする | AT-HLC-017 |
| INV-HLC-015 | Compliance Resultはimmutable Evaluator Artifact digestとinput evidence digestへbindする | AT-HLC-019 |
| INV-HLC-016 | Evaluator更新は旧Resultを改変せず、比較/canary/failure thresholdを通じて新Assignment generationとして適用する | FI-HLC-010 |
| INV-HLC-017 | 外部remediationの完了claimだけではCOMPLIANT、READY、authority armed、maintenance exitへ遷移しない | FI-HLC-012 |
| INV-HLC-018 | External remediation integrationはCore DB、Agent credential、Command Lease、Host Operation Authorityを取得しない | AT-HLC-021 |
| INV-HLC-019 | Credential Binding revision と authenticated certificate fingerprint が current Enrollment binding に一致しない Agent session を grant しない | AT-HLC-023 |
| INV-HLC-020 | Session Authorization は Enrollment、Credential Binding、session、capability generation を全て保持し、transport liveness や証明書 validity だけで AUTHORIZED にしない | AT-HLC-024 |
| INV-HLC-021 | Session Authorization が AUTHORIZED でも explicit Host Operation Authority arming 前に mutation を許可しない | AT-HLC-025 |
| INV-HLC-022 | reconnect、credential renewal/rekey、Enrollment、capability、Baseline/preflight/Compliance の変更は既存 Host authority を fence できるが、同一または新 generation を暗黙 arm しない | FI-HLC-013 |
| INV-HLC-023 | Host Operation Authority は全 current dependency generation と policy/actor を一 transaction で固定し、mutation authorization 時にも同じ binding を再検証する | AT-HLC-026 |

## 10. Host Grouping

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-HGR-001 | PostgreSQLへgeneration付きでmaterializeされたHostGroup membershipだけをauthorityとして使用する | AT-HGR-004 |
| INV-HGR-002 | Placement Pool、Failure Domain、Operational Cohortの効果を相互に暗黙継承しない | AT-HGR-002 |
| INV-HGR-003 | required/exclusive dimensionの欠損・多重所属を任意Group選択で解決せずfail closedにする | FI-HGR-002 |
| INV-HGR-004 | selectorはpure proposalであり、Agent自己申告やexternal claimからmembershipを直接writeしない | XCT-HGR-001 |
| INV-HGR-005 | hierarchy graphをcycle/partial stateなしに一generationでcommitする | FI-HGR-003 |
| INV-HGR-006 | Group membership、weight、policyはHost lifecycle/Compliance/capability/resource eligibilityを上書きしない | AT-HGR-006 |
| INV-HGR-007 | Final Admissionはcurrent membership/policy/hierarchy generationを再検証する | FI-HGR-004 |
| INV-HGR-008 | Group capacityはHost authorityからの導出値で、独立allocation/reservation ledgerを持たない | AT-HGR-008 |
| INV-HGR-009 | 同priorityの非互換Group policy bindingをlast-winsで解決せずASSIGNMENT_CONFLICTにする | FI-HGR-006 |
| INV-HGR-010 | rollout/maintenance targetは開始時のimmutable Group Membership Snapshotへbindする | FI-HGR-005 |
| INV-HGR-011 | Tenantへraw infrastructure Group/failure topologyを公開せず許可されたPlacement Scopeだけを公開する | AT-HGR-012 |
| INV-HGR-012 | Group変更だけで既存workloadを暗黙移動、停止、再構成しない | AT-HGR-014 |
| INV-HGR-013 | active membership/reference/snapshot/policy bindingを持つGroupを削除しない | FI-HGR-007 |
| INV-HGR-014 | READY/placement可能なHostは全active Placement Poolsから一つのeffective Availability Policyを解決できなければならない | AT-AVR-005 |

## 11. Availability Responsibility and Recovery

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-AVR-001 | AvailabilityPolicy versionはimmutableで、responsibilityとHost failure actionの不正な組合せをpublishしない | AT-AVR-001 |
| INV-AVR-002 | Final Admissionはeffective Policy/Pool/membership generationをVM Availability Bindingとresource claimへ不可分にcommitする | AT-AVR-006 |
| INV-AVR-003 | Group/Policy変更だけで既存VM Availability Bindingを変更せず、明示Rebindで新revisionを作る | FI-AVR-002 |
| INV-AVR-004 | WORKLOAD_MANAGED VMへKIMが自動restart、evacuate、replacement mutationを開始しない | FI-AVR-004 |
| INV-AVR-005 | MANUAL VMへauthorized Manual Recovery DecisionなしにKIMがrecovery mutationを開始しない | FI-AVR-005 |
| INV-AVR-006 | INFRASTRUCTURE_MANAGED recoveryはPolicy要求を満たすsource fencing proofなしに開始しない | FI-AVR-003 |
| INV-AVR-007 | fencing、single-writer attachment、resource ownership、Availability BindingのUNKNOWNを推測で安全扱いしない | FI-AVR-007 |
| INV-AVR-008 | recovery destinationはcurrent Placement/Compliance/capacity/failure-domainとbound Policy compatibilityを再評価する | FI-AVR-008 |
| INV-AVR-009 | Recovery Operationはcanonical Failure Campaign、VM、Availability Binding revision、actionへ冪等にbindし、元epoch群をevidenceとして保持する | AT-AVR-013 |
| INV-AVR-010 | stale failure epoch、Binding、fencing proof、Lease、Resultはcurrent recovery authorityを進めない | FI-AVR-006 |
| INV-AVR-011 | EVACUATEはVM単位Operationへ分解し、一VMの失敗/UNKNOWNを他VMの推測rollbackへ波及させない | FI-AVR-009 |
| INV-AVR-012 | Fault/Event delivery failureを理由にAvailability responsibility/actionを変更しない | FI-AVR-010 |
| INV-AVR-013 | heartbeat/Agent lossだけでHost source fencing完了を確定しない | FI-AVR-003 |

## 12. Workload Resilience Intent

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-WRI-001 | NFVO/VNFMのopaque roleをVNF lifecycle、application health、authorization authorityに使用しない | AT-WRI-002 |
| INV-WRI-002 | Tenant/NFVOへraw HostGroup/failure topologyを公開せずpublic dimension/level classだけを受け付ける | AT-WRI-003 |
| INV-WRI-003 | HARD Failure Domain constraintをscore/soft ruleへ降格またはsilent relaxしない | FI-WRI-003 |
| INV-WRI-004 | rack、power-path等のFailure Domain dimensionを独立に評価する | AT-WRI-004 |
| INV-WRI-005 | ResilienceDomainClaimをVM Allocation/Availability Binding/resource claimsと同じFinal Admission transactionでcommitする | AT-WRI-007 |
| INV-WRI-006 | concurrent member Placementでhard same-domain claimは一方だけがcommitできる | FI-WRI-001 |
| INV-WRI-007 | missing/stale/UNKNOWN domain evidenceをdistinct domainとして数えない | FI-WRI-002 |
| INV-WRI-008 | old VM/source ownershipがUNKNOWNのMember Slot/Domain Claimをreplacementへ再利用しない | FI-WRI-004 |
| INV-WRI-009 | domain/hierarchy driftだけで既存VMを暗黙migration/restartしない | FI-WRI-005 |
| INV-WRI-010 | Resilience IntentはVM Availability responsibilityを変更しない | AT-WRI-012 |
| INV-WRI-011 | Northbound mapperはCore DB/Domain Claim/Allocationへ直接writeしない | XCT-WRI-001 |
| INV-WRI-012 | active Member/Domain Claimを持つResilience Groupを削除しない | FI-WRI-007 |
| INV-WRI-013 | required members未充足をmin-distinct違反にせずPENDINGとし、max-members-per-domainは各admissionで強制する | AT-WRI-015 |

## 13. Recovery Storm Control

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-RCV-001 | Recovery Queue/Budget Lease authorityをPostgreSQL transactionで確定し、in-memory/Bus状態をauthorityにしない | FI-RCV-002 |
| INV-RCV-002 | Queue Entryは各PLANNING/DISPATCH phaseの該当全budget scopeを不可分取得するまでそのphaseへ進まない | AT-RCV-003 |
| INV-RCV-003 | Budget Leaseはfencing、Placement/capacity claim、Command Lease、verificationを代替しない | AT-RCV-004 |
| INV-RCV-004 | Budget Lease expiry/worker lossをRecovery未実行の証明にしない | FI-RCV-003 |
| INV-RCV-005 | max concurrencyとstart rate/window/burstを全workerで共有するdurable generationから強制する | FI-RCV-001 |
| INV-RCV-006 | recovery priorityはsafety/eligibilityを上書きしない | AT-RCV-007 |
| INV-RCV-007 | aging/fair-share/per-scope capで一Project/Planによるstarvationを防ぐ | FI-RCV-005 |
| INV-RCV-008 | circuit breaker復旧だけでdispatchせずfencing/evidence/Placement generationを再検証する | FI-RCV-006 |
| INV-RCV-009 | duplicate/correlated failure signalを重複Recovery Queue Entryへ無制限展開しない | FI-RCV-007 |
| INV-RCV-010 | queue delay/saturationをsuccess/failureへ丸めずWAITING/BLOCKED/ESCALATED evidenceとして保持する | AT-RCV-010 |
| INV-RCV-011 | Budget Policy変更だけでdispatch/started Recovery Operationを暗黙cancel/reclassifyしない | FI-RCV-008 |
| INV-RCV-012 | Control Plane/worker failover後もBudget/Queue/Lease authorityとorderingをPostgreSQLから復元する | FI-RCV-009 |
| INV-RCV-013 | dispatchはRecovery OperationとBudget Consumptionを不可分commitし、terminal verificationまでactive concurrencyへ計上する | FI-RCV-011 |
| INV-RCV-014 | 全budget acquisition pathは同じversioned canonical scope順でlockし、deadlock/serialization failureで部分Leaseを残さない | FI-RCV-012 |
| INV-RCV-015 | 同一Failure Campaign/VM/Binding/actionは一つのRecovery Campaign Claimへ収束し、late mergeでも追加dispatchを許可しない | FI-RCV-013 |

## 14. Security, Audit, and Failure

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-SEC-001 | mutationはauthentication、authorization、audit durabilityを満たさなければfail closed | AT-SEC-001 |
| INV-SEC-002 | secret、生backend error、他Tenant resource identityを公開response/metricsへ出さない | AT-SEC-002 |
| INV-FAIL-001 | timeout/partition/Lease expiryをmutation失敗または未実行の証明にしない | FI-TRANSPORT-001 |
| INV-FAIL-002 | stale identity/generation/token/observationはcurrent authorityを進めない | FI-SPLIT-001 |
| INV-FAIL-003 | recovery不能resourceはblocked/quarantinedを維持し、推測cleanupしない | FI-DR-001 |
| INV-HA-001 | 同一Site HA failoverはcommitted authority RPO 0を目標とする | FI-DB-001 |
| INV-DR-001 | restore後の未知resourceはquarantineし、明示adoption前にmutationしない | FI-DR-001 |

## 15. Extensions

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-EXT-001 | ExtensionはCore DBへ直接writeできない | XCT-BOUNDARY-001 |
| INV-EXT-002 | Extensionはauthorization、audit、Lease/fencingを迂回できない | XCT-BOUNDARY-002 |
| INV-EXT-003 | Extensionは独自Identity/Credential authorityを暗黙追加できない | XCT-BOUNDARY-003 |
| INV-EXT-004 | Agent moduleはclosed Commandとnarrow backend interfaceだけを受け取る | XCT-AGENT-001 |
| INV-EXT-005 | UNKNOWNをFAILED/SUCCEEDEDへ丸めるadapterを受け入れない | XCT-FAIL-001 |
| INV-EXT-006 | capability消失時は新規利用を停止し、既存resourceを暗黙変更しない | XCT-CAP-001 |

## 16. Documentation

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-DOC-001 | Requirements、Accepted ADR、Architectureの矛盾を暗黙解釈せず、実装を停止して解消する | AT-DOC-001 |
| INV-DOC-002 | 重要判断の変更はADR、Requirements、Architecture、test traceを同じchange setで更新する | AT-DOC-002 |
| INV-DOC-003 | 日本語spacing lintはcode、URL、identifier、API path、約物を除外し、未reviewのrepository-wide自動修正を行わない | AT-DOC-003 |

## 17. Upgrade and Compatibility

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-UPG-001 | Release Manifestはimmutable artifact digest、contract/support range、migration、rollback boundaryへbindする | AT-UPG-002 |
| INV-UPG-002 | version文字列やprocess aliveだけでcompatibility/readinessを確定せずcurrent evidenceへbindしたDecisionを要求する | FI-UPG-001 |
| INV-UPG-003 | Upgrade Campaign/Plan/Wave/Target/Feature Gate authorityをPostgreSQLへ永続化しin-memory progressをauthorityにしない | FI-UPG-010 |
| INV-UPG-004 | mixed-versionは明示edgeを持つN/N-1だけとしN-2/unmanaged/digest不明componentをserving/dispatchへ参加させない | FI-UPG-002 |
| INV-UPG-005 | 全active writer/consumerが理解するsemanticsだけをFeature Gate前にwriteする | FI-UPG-003 |
| INV-UPG-006 | schema contract/old decoder/artifact GCをrollback window終了とrequired participant absenceの証明前に行わない | FI-UPG-004 |
| INV-UPG-007 | 各waveはimmutable target snapshot、current compatibility、availability budget、failure thresholdを満たす | AT-UPG-007 |
| INV-UPG-008 | upgrade coordinatorはdomain mutation、Placement、Command、Attachment、Network Binding等のauthorityを代替しない | AT-UPG-009 |
| INV-UPG-009 | unsupported protocol/Command/Result schemaをdispatch/down-convert/silent fallbackしない | FI-UPG-006 |
| INV-UPG-010 | Agent upgrade/reconnect/version一致だけでHost authorityを再armしない | FI-UPG-007 |
| INV-UPG-011 | Event payloadをupgrade後resourceから再生成せず発行時schema/digestとretention decoderを保持する | AT-UPG-014 |
| INV-UPG-012 | extension/adapter upgradeはdrainとownership fencingなしにold/new writerを同時activeにしない | FI-UPG-008 |
| INV-UPG-013 | support matrix変更だけで既存VM/Port/Volumeを暗黙mutationしない | AT-UPG-018 |
| INV-UPG-014 | incompatible/UNKNOWN Host/backendを新規Placement/Recovery/dispatchに使用しない | FI-UPG-009 |
| INV-UPG-015 | rollbackを新Plan/Attemptとして記録し過去Target/Attempt/evidenceを改変しない | AT-UPG-020 |
| INV-UPG-016 | destructive contract後またはoutcome UNKNOWN時にblind rollback/PITR/逆操作を開始しない | FI-UPG-011 |
| INV-UPG-017 | offline/緊急upgradeでもartifact verification、authorization、audit、compatibility gateを省略しない | FI-UPG-013 |
| INV-UPG-018 | release publish/start/switch/contract/activation/rollback/overrideを分離した権限と監査で保護する | FI-UPG-014 |
| INV-UPG-019 | QEMU/libvirt/default変更だけで既存VMのmachine type/CPU model/firmware/device ABI bindingを変更しない | FI-UPG-016 |
| INV-UPG-020 | Event/evidence payload referenceまたはlegal hold中にrequired decoder artifactをfinalize/GCしない | FI-UPG-017 |
| INV-UPG-021 | Feature Gate dependency graphのcycle/未充足/conflictを拒否しdependency-aware orderを迂回しない | FI-UPG-018 |

## 18. Time and Clock Semantics

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-TIM-001 | wall clock、DB time、monotonic clock、source/received timestampを同じauthorityとして扱わない | AT-TIM-001 |
| INV-TIM-002 | timestampだけでresource/execution/delivery/observation orderingやfencingを決めない | FI-TIME-001 |
| INV-TIM-003 | Clock Health Decisionをcurrent evidence、uncertainty、policy generationへbindしUNKNOWNをHEALTHYへ丸めない | AT-TIM-004 |
| INV-TIM-004 | Control Plane Lease/deadline/retention decisionをapplication node clockではなくcurrent DB authority time/generationで行う | AT-TIM-005 |
| INV-TIM-005 | DB clock step/failover/restoreでexpiredまたはold-generation Lease/session/claimをreviveしない | FI-TIME-002 |
| INV-TIM-006 | Lease expiryを期限前side effectの未実行/失敗証明にしない | FI-TIME-003 |
| INV-TIM-007 | expired/revoked Leaseを同じtokenの時刻変更でrenew/reviveしない | AT-TIM-008 |
| INV-TIM-008 | Agentは受信時刻+TTLやlocal wall clockだけでCommand start deadlineを決めない | FI-TIME-004 |
| INV-TIM-009 | Agent clock uncertainty/RTT/monotonic continuityがpolicy外なら新Commandを開始しない | FI-TIME-005 |
| INV-TIM-010 | process/Host reboot後にpre-reboot monotonic deadline/cached Commandを使用しない | FI-TIME-006 |
| INV-TIM-011 | sourceの未来timestampでObservation/Evidenceのfreshnessを延長しない | FI-TIME-007 |
| INV-TIM-012 | credentialが時間上有効なことをEnrollment/Role/Host/Command authorityとして使用しない | AT-TIM-014 |
| INV-TIM-013 | clock UNKNOWN/UNTRUSTED時に新規privileged authentication/credential/Commandをfail openしない | FI-TIME-008 |
| INV-TIM-014 | calendar window開始/終了だけでdrain/fencing/mutation/catch-up authorityを得ない | FI-TIME-009 |
| INV-TIM-015 | clock jumpでqueue/rate creditを二重付与し、grace/deadlineから破壊操作を即時実行しない | FI-TIME-010 |
| INV-TIM-016 | clock anomalyまたはreference/hold/backup guard未確認時にretention GC/partition detachを実行しない | FI-TIME-011 |
| INV-TIM-017 | replay/DR/archive reference期間より前にidempotency/Receipt/decoder evidenceを削除しない | AT-TIM-019 |
| INV-TIM-018 | event timestamp近接だけでFailure Epochを同一Campaignへmergeしない | FI-TIME-012 |
| INV-TIM-019 | clock anomalyだけで既存VM/dataplaneを停止・移動・再構成しない | AT-TIM-022 |
| INV-TIM-020 | clock復旧だけでHost/Agent/Lease/credential authorityを自動再armしない | FI-TIME-013 |
| INV-TIM-021 | DB/Control Plane clockは自身のtimestampまたは単一external sourceだけでHEALTHYを自己証明しない | FI-TIME-017 |
| INV-TIM-022 | PTP/GNSS lock、grandmaster、VNF telemetry timestampをKIM Lease/credential/ordering/fencing authorityにしない | FI-TIME-018 |
| INV-TIM-023 | leap/smear policy不明・競合を無視してLease延長、mass expiry、calendar二重実行を行わない | FI-TIME-019 |

## 19. PKI and Trust Lifecycle

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-PKI-001 | Control Plane、Agent、integration/backend、artifact signing、data encryptionのtrust/key domainを無制限共有しない | AT-PKI-001 |
| INV-PKI-002 | workload certificateを外部Identity PlatformのUser/Service authorization authorityとして使用しない | AT-PKI-002 |
| INV-PKI-003 | Root private keyを通常Control Plane/Agent/DBへ配置して日常issuanceに使用しない | FI-PKI-001 |
| INV-PKI-004 | unknown/wildcard/CN-only/constraint外SAN・EKU・algorithm certificateをtrusted identityにしない | FI-PKI-002 |
| INV-PKI-005 | private key/secret valueをKIM DB、Event、Command、log、diagnostic、通常backupへ保存しない | FI-PKI-003 |
| INV-PKI-006 | TrustBundle/Profile/Relationshipをimmutable revisionとmonotonic trust generationで更新しold Bundleを上書き・rollbackしない | FI-PKI-004 |
| INV-PKI-007 | valid certificateだけでRole、Enrollment、Host authority、Command Lease、backend mutationを成立させない | AT-PKI-007 |
| INV-PKI-008 | Trust Decisionをcurrent Bundle/profile/revocation/clock/Binding/session generationへbindしUNKNOWNをtrustedへ丸めない | FI-PKI-005 |
| INV-PKI-009 | bootstrap token/credentialだけでEnrollment、READY、Host authorityを成立させない | FI-PKI-006 |
| INV-PKI-010 | proof-of-possessionとcurrent Enrollment/identity evidenceなしにAgent certificateを発行しない | AT-PKI-010 |
| INV-PKI-011 | issuance/renewal response lossで別key/identity certificateをblind発行しない | FI-PKI-007 |
| INV-PKI-012 | renewal/rekeyは新Credential Binding revisionとし過去certificate/Binding historyを書き換えない | AT-PKI-012 |
| INV-PKI-013 | overlap中のold/new certificateから二つのHost/workload mutation authorityを作らない | FI-PKI-008 |
| INV-PKI-014 | TrustBundle/revocation/Binding/authority generation変更後のstale sessionをmutationへ使用しない | FI-PKI-009 |
| INV-PKI-015 | certificate expiry/revocation/session closeをpeer process/Host/backend side effect停止の証明にしない | FI-PKI-010 |
| INV-PKI-016 | revocation intentをdistribution/propagation完了へ丸めずsequence/freshness/receiptを要求する | FI-PKI-011 |
| INV-PKI-017 | revocation stateがstale/UNKNOWNなprofile scopeでnew privileged sessionをfail openしない | FI-PKI-012 |
| INV-PKI-018 | distrusted issuer/profile/namespaceを別chain、cached session、old Bundleへsilent fallbackしない | FI-PKI-013 |
| INV-PKI-019 | Host credential revoke/Gateway disconnectだけでcompute/storage/network fencing完了を確定しない | FI-PKI-014 |
| INV-PKI-020 | Control Plane certificate rotationだけでDB/Bus/backend credentialやLease/authorityをfencedとみなさない | FI-PKI-015 |
| INV-PKI-021 | compromised issuer自身の署名だけでemergency Root/anchor rolloverを承認しない | FI-PKI-016 |
| INV-PKI-022 | offline Bundleのsequence/previous digest/trust generation rollbackまたはTOFU/default shared secretを許可しない | FI-PKI-017 |
| INV-PKI-023 | PITR/DRで時間上有効なold certificate/session/Lease authorityを復活・cloneしない | FI-PKI-018 |
| INV-PKI-024 | Secret Provider completion claimだけでcredential active/revoked/rotatedを確定しない | FI-PKI-019 |
| INV-PKI-025 | Root/issuer distrust、emergency anchor、CA key restore、force issuanceを通常resource operator単独で実行しない | FI-PKI-020 |
