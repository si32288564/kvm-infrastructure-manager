# Acceptance Test Catalog

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

Architecture Traceability Matrixが参照する通常Acceptance/Performance Testの最低契約を定義します。具体的なfixture、実行環境、test fileは実装時に同じIDへ関連付けます。

## 2. Identity / Authorization

| ID | Acceptance Contract |
|---|---|
| AT-AUTH-001 | 外部IdPの有効/無効tokenを検証し、KIMがUser/Credential rowを発行しない |
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
| AT-AGT-001 | shell/argv/unknown Command/arbitrary libvirt XML/path payloadをschema境界で拒否する |
| AT-AGT-002 | Agent artifact/configにBus credential/subject accessがなくGateway mTLSだけを使用する |
| AT-AGT-003 | identity/capability/armed authority/current Leaseの一つでも欠ければCommandを取得・実行できない |
| AT-AGT-006 | 2系統以上のLinuxで同じControl Plane contractへ正規化し、OS名分岐を要求しない |
| AT-AGT-007 | typed remediation allow-list外のpackage/service/config/kernel変更を拒否する |

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

## 6. API / Data / Operations

| ID | Acceptance Contract |
|---|---|
| AT-API-001 | mutation APIが202+Operationを返し、request処理中にHost/backendへ接続しない |
| AT-API-002 | 同一idempotency key+payloadの並行再送が単一Operation/resourceへ収束する |
| AT-API-003 | 同一idempotency key+異なるpayloadが409 conflictになる |
| AT-DATA-001 | desired/allocation/Job/Command/idempotencyの一要素失敗で全transactionがrollbackする |
| AT-DATA-002 | desired/observed generationを独立保持し、stale observationをcurrent表示しない |
| AT-OPS-001 | Operation状態、進捗、correlation、bounded failure reasonを照会できる |
| AT-OPS-005 | retry/cancelが許可状態とauthorityを検証し、unsafe actionを拒否する |
| AT-EVT-001 | event/webhookをdurable outboxから再送し、重複IDとredaction contractを維持する |

## 7. Execution

| ID | Acceptance Contract |
|---|---|
| AT-EXEC-001 | concurrent Lease要求で一つだけがactive Leaseを取得する |
| AT-EXEC-002 | LeaseにHost owner/token/attempt/expiry/authority generationがbindされる |
| AT-EXEC-003 | Agent journalがbackend mutation前にdurably記録され、同じCommandを再実行しない |
| AT-EXEC-004 | accepted Resultの同一再送は同じreceipt、異なるdigestはconflictになる |
| AT-EXEC-005 | UNKNOWN Attemptを改変せず、verification evidenceと後続eventを追記する |
| AT-EXEC-006 | successful Result後もJobはverifyingで、matching observation後だけsucceededになる |
| AT-EXEC-007 | terminal Job/Attempt/Event履歴がimmutableである |

## 8. Placement / Migration

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

## 9. Compute / Image / Network / Storage

| ID | Acceptance Contract |
|---|---|
| AT-CMP-001 | VM create/start/stop/reboot/deleteがdesired→execution→observationで収束する |
| AT-CMP-008 | console accessが短寿命、一回用途、Project scope、監査付きである |
| AT-IMG-001 | qcow2/raw image lifecycle、visibility、Project accessを検証する |
| AT-IMG-002 | checksum/signature不一致imageをcache/boot前に拒否する |
| AT-FLV-001 | FlavorのCPU/RAM/disk/NUMA/HugePages/pinning要求をplacementへ完全伝播する |
| AT-NET-001 | KIM virtual network操作がWAN/physical switch authorityを変更しない |
| AT-NET-002 | VLAN/Geneve/DHCP/security group/L3 intentがgeneration付きで収束する |
| AT-NET-006 | SR-IOV Port assignmentをPCI/device/network eligibilityと不可分に扱う |
| AT-STO-001 | Volume lifecycle/attach/detach/snapshotがtyped executionとverificationで収束する |
| AT-STO-002 | backend capability未対応時にsilent fallbackせずbounded errorを返す |

## 10. NFV Dataplane

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

## 11. Security / Audit / Documentation

| ID | Acceptance Contract |
|---|---|
| AT-SEC-001 | authentication/authorization/audit durabilityの一つでも失敗すればmutationをcommitしない |
| AT-SEC-002 | response/log/metric/diagnosticへsecret、生error、他Tenant identityを漏らさない |
| AT-SEC-003 | TLS/mTLS、certificate rotation/revocation、Tenant API/Network/Storage isolationを検証する |
| AT-AUD-001 | 認証・認可・mutationにactor/scope/resource/decision/result/correlation監査がある |
| AT-AUD-002 | 診断bundleが必要evidenceを含み、secretと非許可identityを除外する |
| AT-O11Y-001 | metrics/alarm/trace correlationを公開し、high-cardinality identityやsecretを含めない |
| AT-DOC-001 | 矛盾するRequirement/Accepted ADR/ArchitectureをCIが検出して失敗する |
| AT-DOC-002 | 重要ADR変更時にRequirement/Architecture/Invariant/Test trace未更新をCIが拒否する |

## 12. HA / Upgrade / Packaging

| ID | Acceptance Contract |
|---|---|
| AT-HA-001 | 単一Control Plane node lossでAPIを継続しcommitted authorityを失わない |
| AT-UPG-001 | N-1→N upgrade/rollback中にAPI/Agent contractと既存VMを維持する |
| AT-OFFLINE-001 | network非接続環境で署名済みbundleからinstall/upgradeできる |

## 13. Performance Tests

| ID | Performance Contract |
|---|---|
| PT-SCALE-001 | 100 Host、5,000 VM、10,000 Portでinventory/reconciliationを継続する |
| PT-API-001 | 通常負荷のread API p95が500 ms以下である |
| PT-OPS-001 | 50 concurrent mutation Operationでauthority/invariant違反なくdispatch p95目標を測定する |
