# Phase 0 Architecture Baseline Exit Review

- 状態: Conditional Hold
- 対象commit: `45a94cc` (`docs: define pki trust lifecycle architecture`)
- Review実施日: 2026-08-09
- 対象: Phase 0 Architecture Baseline

## 1. 判定

Architecture内容の横断整合性は **PASS** とします。authority、failure、recovery、resource admission、upgrade、time、trustの主要contract間に、実装を直ちに停止すべき未解消のCritical/High contradictionは検出しませんでした。

正式なPhase 0 exitは **HOLD** とします。これはArchitecture内容の不足ではなく、次のgovernance gateが未完了だからです。

1. 23件のADRがすべて`Proposed`であり、Accepted ADRが0件である。
2. Phase 0期限のOpen Questionが24件あり、Closedまたは明示的Deferredになっていない。
3. 正本となる主要文書が`Draft`であり、ADR承認後のBaseline昇格が未実施である。

Exit Reviewは上記を自動承認・自動クローズしません。承認者が判断した後、同一change setでADR、Open Question、関連Architectureの状態を更新します。

## 2. Evidence Snapshot

| Evidence | Snapshot | 判定 |
|---|---:|---|
| Requirements | 372件 | scope確認済み |
| Must requirements | 298件 | traceability対象 |
| Traceability entries | 200件、すべて`Planned` | Phase 0 contractとしてPASS、実装evidenceではない |
| Architecture Invariants | 257件 | 検証ID付与済み |
| Acceptance / Performance tests | 299件 | contract定義済み |
| Fault Injection tests | 187件 | contract定義済み |
| Extension Conformance tests | 61件 | contract定義済み |
| ADR | 23件、すべて`Proposed` | BLOCKER |
| Phase 0 Open Questions | 24件 | BLOCKER |
| duplicate/undefined ID、broken local link | 0件 | PASS |

数値は対象commitの文書を機械検査したsnapshotです。件数そのものを品質目標にはせず、正本間の矛盾と未追跡をgateにします。

## 3. Exit Criteria Audit

| Gate | 判定 | Evidence / Action |
|---|---|---|
| Requirement ownerと検証方法 | PASS | RequirementsとTraceability Matrix |
| 主要設計とfailure scenario | PASS | Architecture、Failure Model、Fault Injection Matrix |
| 全MustのArchitecture/ADR/Invariant/Test trace | PASS | 未追跡0件 |
| Phase 0 decision ADRの承認 | BLOCKED | 23件をレビューしAccepted/Rejected/Supersededへ遷移する |
| Phase 0 Open Questionの解決 | BLOCKED | 24件をClosedまたはowner/target gate付きDeferredへ遷移する |
| Baseline文書の状態昇格 | BLOCKED | ADR/Open Question処理後にDraftからBaselineへ昇格する |
| 実装済みsystem test | NOT APPLICABLE | Phase 1以降のexit evidenceへ分離した |

## 4. Authority Owner Audit

| Subject | Authority owner | Observation / delegated system | 判定 |
|---|---|---|---|
| Principal credential、User lifecycle | External Identity Platform | KIMはsubject/claimを検証 | 一意 |
| Tenant、Project、Membership、Role、Quota | KIM / PostgreSQL | IdP identityへbinding | 一意 |
| Host enrollment、Baseline、Compliance、arming | KIM / PostgreSQL | Agent、Evaluator、external remediationはevidence/claimを提供 | 一意 |
| 汎用Host OS構成管理 | External Configuration Management / Operator | KIMはclosed typed remediationのみ所有 | 境界明確 |
| VM desired state、Placement Allocation | KIM / PostgreSQL | libvirt/QEMUはruntime observation | 一意 |
| Network identity、segment、binding、intent | KIM / PostgreSQL | OVN/OVSはmaterializationとobservation | 一意 |
| Storage binding、attachment、fencing decision | KIM / PostgreSQL | Ceph/LVM/libvirtはphysical evidence | 一意 |
| Execution、Lease、Attempt、Receipt | KIM / PostgreSQL | Agent Gatewayは配送境界 | 一意 |
| Key custody、CA signing、secret value | Customer/External CA、HSM/KMS/Secret Provider | KIMはTrust Profile/Binding/session enforcementを所有 | 境界明確 |
| WAN、inter-PoP、physical network service | WIM / external network authority | KIMはprovider mappingとlocal realizationを公開 | 境界明確 |

Internal Message Bus、cache、search index、backend observed state、projectionはSystem of Recordではありません。

## 5. Generation、Lease、UNKNOWN Audit

`generation`はnamed authority scope内のincarnation、`revision`はimmutable content版、`attempt`はexecution試行として区別されています。Host authority、Agent session、database restore、Trust Bundle等のgenerationは別scopeであり、相互比較できません。

すべてのLeaseはtyped permissionです。Command Lease、Recovery Budget Lease、publisher claim等は相互に代替できず、expiryはfuture authorityを止めるだけで、side effectが無かった証拠にはなりません。

`UNKNOWN`はdomain-specific evidence不足を表します。timeoutをSUCCESS/FAILEDへ丸めず、Observation/Verification evidenceを追記して解決し、過去Attemptやdecisionを改変しません。これらの共通定義を[システムアーキテクチャ](architecture.md)のCross-domain Semantic Registryへ追加しました。

## 6. Final Admission Transaction Boundary Audit

```text
Dry Eligibility / Admission       no side effect
            ↓
Scoring and Selection             no authoritative claim
            ↓
PostgreSQL Final Admission        one transaction
  ├─ compute / NUMA / HugePages / PCI
  ├─ DPDK PMD / memory / Port / RxQ
  ├─ IP / MAC / segment / Port Binding
  ├─ Volume / Attachment / capacity
  ├─ Quota / Failure Domain Claim
  └─ Availability Binding + input generations
            ↓ commit
Execution / Backend realization   external side effects
```

Network、Storage、Dataplane、Availabilityは独立したbackend transactionをFinal Admissionと呼びません。Final Admission内ではPostgreSQL authorityだけをcommitし、libvirt、Agent、OVN、Cephへside effectを発生させません。競合またはinput generation変化時はtransaction全体をrollbackし、再選択します。

## 7. Recovery Constraint Reuse Audit

Infrastructure Managed Recoveryは通常配置の抜け道ではありません。source compute/storage/attachment authorityをfenceした後、次をcurrent evidenceで再評価します。

- Placement eligibility、capacity、quota、Host compliance
- Availability Bindingとfailure-domain constraints
- Storage locality、single-writer、Attachment generation、client fencing
- Network identity/segment、Port Binding/Handoff、Gateway dependency
- PCI/SR-IOV/IOMMU、NUMA、HugePages、OVS-DPDK PMD/RxQ
- transactional Final Admission、Execution Lease、post-execution verification

Workload Managedはcontainment/fencingとFault/Eventまでに留まり、通知失敗を理由にInfrastructure Managedへ昇格しません。Manual Recoveryも安全gateを省略しません。

## 8. Host Authority Rearming Negative Audit

| Event | 暗黙rearm | 必要な再検証 |
|---|---|---|
| Agent Gateway再接続 | 禁止 | Host identity、capability、current authority generation、Command Lease |
| Agent binary upgrade/reconnect | 禁止 | Enrollment、Baseline、Preflight、Compliance、session generation |
| clock health回復 | 禁止 | domain-specific evidenceとcurrent policy |
| certificate renewal/rekey | 禁止 | Credential Binding、Trust Decision、logical Host authority、new session generation |
| revocation解除/CA rollover | 禁止 | new trusted chain、binding、session fence、Host lifecycle gate |
| PITR/DR restore | 禁止 | new restore epoch、external old-site fencing proof、reconciliation/adoption |
| external remediation completion claim | 禁止 | fresh inventory/evidenceとassigned Evaluator再評価 |

credential、transport connectivity、clock validity、backend observationの単独成立はmutation authorityではありません。

## 9. Extension and Host OS Portability Audit

ExtensionはCore DBへの直接write、内部Bus credential、独自Lease/identity、arbitrary shell/XML/SQL、監査迂回を許されません。C0-C3 trust class、out-of-process boundary、versioned contract、conformance testによりCore invariantを維持します。

KIMの製品identityはKVM/libvirtであり、任意hypervisor抽象化を目的にしません。一方、一般的なLinux distributionのpackage、service manager、security module、firewall、tuning差異はHost Agent内のOS Integration Adapterが吸収します。新しいdistribution対応のためControl PlaneへOS名による分岐を追加しないため、「KVMを特殊化しない」ことと「Host OSを固定しない」ことは両立しています。

## 10. Designed and Undecided

### Phase 0で設計済み

- responsibility、identity、trust、authority、failure semantics
- Placement/Final Admission、Execution/Lease/Attempt、Agent Gateway
- Host lifecycle/compliance/grouping、Availability/Recovery/resilience intent
- Compute/NFV dataplane、Storage/Attachment/Fencing、Network resource
- Persistence/DR、Upgrade/Compatibility、Time、PKI lifecycle
- Invariant、traceability、acceptance/fault/conformance test contract

### Phase 0 exit前に決定または明示的延期が必要

- 23件のProposed ADRの採否
- Phase 0期限の24 Open Questions
- 初期Validated OS、deployment/package profile、正式Agent component名
- 初期network/dataplane/group/availability/enrollment policy profile
- Architecture文書のBaseline昇格とreview approver記録

Technical Preview以降のcapacity値、運用default、certification matrix、SLO tuningは、Architecture invariantを変えない限り後続gateで決定できます。

## 11. Findings and Closure

| ID | Finding | Severity | Disposition |
|---|---|---|---|
| P0ER-001 | Phase 0 exitに実装済み2-distribution system testが混在 | Medium | Phase 1 exitへ移動済み |
| P0ER-002 | generation/Lease/UNKNOWNの共通scope規則がdomain文書へ分散 | Medium | Cross-domain Semantic Registryを追加済み |
| P0ER-003 | Document Governanceの採番重複とUpgrade/Time/PKI gate不足 | Low | 修正済み |
| P0ER-004 | 23 ADRがすべてProposed | High / Gate blocker | 承認reviewが必要 |
| P0ER-005 | Phase 0期限Open Questionが24件未処理 | High / Gate blocker | closeまたは明示的deferが必要 |
| P0ER-006 | 主要文書がDraft | Medium / Gate blocker | P0ER-004/005後にBaseline昇格 |

## 12. Formal Exit Procedure

1. ADR-0001からADR-0023をDecision Gate順にレビューし、Accepted/Rejected/Supersededを記録する。
2. Phase 0期限のOpen QuestionをClosed、またはowner、理由、次のtarget gateを持つDeferredへ更新する。
3. Accepted ADRとArchitecture/Requirement間の最終diffを機械検査する。
4. 本Reviewのblockerを閉じ、対象commitを更新して承認者と承認日を記録する。
5. 主要文書をDraftからBaselineへ昇格し、Phase 0をExitedへ変更する。

## 13. v1 Gap Analysis Entry

Gap Analysisはこのcandidate baselineを比較軸として開始できます。ただしProposed ADRを不可逆な実装authorityとして扱いません。比較は少なくとも次の軸で行います。

- public API/resource model、tenant/authz/quota
- PostgreSQL schema/authority/evidence/outbox/inbox
- Operation/Job/Command/Lease/AttemptとAgent protocol
- Placement、Host lifecycle/grouping/compliance
- Compute、NFV dataplane、Network、Storage
- Availability、fencing、recovery、DR
- Upgrade、Time、PKI、extension/security boundary
- observability、audit、test coverage、packaging/supportability

各Gapは`Existing / Partial / Missing / Conflicting / Reusable`、target phase、dependency、migration/discard decision、関連Requirement/Invariant/Test IDを持たせます。
