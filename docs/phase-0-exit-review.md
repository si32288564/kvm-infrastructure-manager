# Phase 0 Architecture Baseline Exit Review

- 状態: Passed
- Initial audit commit: `45a94cc` (`docs: define pki trust lifecycle architecture`)
- Decision Gate finalization base: `8c1f9a9` (`docs: add phase 0 architecture exit review`)
- Review実施日: 2026-08-09
- 対象: Phase 0 Architecture Baseline
- Formal exit: Exited on 2026-08-09 after Decision Gate finalization

## 1. 判定

Architecture 内容の横断整合性は **PASS** とします。authority、failure、recovery、resource admission、upgrade、time、trust の主要 contract 間に、実装を直ちに停止すべき未解消の Critical/High contradiction は検出しませんでした。

当初の formal exit は governance gate のため HOLD でした。次の closure を個別 review と同一 change set で完了したため、正式な Phase 0 exit を **PASS / EXITED** とします。

1. ADR-0001〜0023 を Decision/Consequences と全 contract で個別照合し、23件を Accepted としました。
2. Phase 0 期限の Open Question 24件を、17件 Closed、7件 owner/理由/target gate 付き Deferred としました。
3. 標準 KVM neutrality、KIM Host Agent の Go 方針、writing convention を正本・Invariant・Test Contract へ追加しました。
4. 主要文書を `Baseline` へ昇格しました。

Decision の証跡は [ADR Decision Gate Review](adr-decision-gate-review.md)、未決事項の処理は [Open Questions](open-questions.md)、v1 の初回比較は [v1 Gap Analysis](v1-gap-analysis.md) を正本とします。

## 2. Evidence Snapshot

| Evidence | Snapshot | 判定 |
|---|---:|---|
| Requirements | 377件 | scope 確認済み |
| Must requirements | 301件 | traceability 対象 |
| Traceability entries | 202件、すべて`Planned` | Phase 0 contract として PASS、実装 evidence ではない |
| Architecture Invariants | 261件 | 検証 ID 付与済み |
| Acceptance / Performance tests | 305件 | contract 定義済み |
| Fault Injection tests | 187件 | contract定義済み |
| Extension Conformance tests | 61件 | contract定義済み |
| ADR | 23件、すべて`Accepted` | PASS |
| Phase 0 Open Questions | Closed 17件、Deferred 7件 | PASS |
| v1 Gap | 33件 | Initial baseline 完了 |
| duplicate/undefined ID、broken local link | 0件 | PASS |

数値は Decision Gate finalization change set の文書を機械検査した snapshot です。件数そのものを品質目標にはせず、正本間の矛盾と未追跡を gate にします。

## 3. Exit Criteria Audit

| Gate | 判定 | Evidence / Action |
|---|---|---|
| Requirement ownerと検証方法 | PASS | RequirementsとTraceability Matrix |
| 主要設計とfailure scenario | PASS | Architecture、Failure Model、Fault Injection Matrix |
| 全MustのArchitecture/ADR/Invariant/Test trace | PASS | 未追跡0件 |
| Phase 0 decision ADR の承認 | PASS | Accepted 23、Rejected 0、Superseded 0 |
| Phase 0 Open Question の解決 | PASS | Closed 17、owner/reason/target gate 付き Deferred 7 |
| Baseline 文書の状態昇格 | PASS | normative Phase 0 文書を Baseline へ昇格 |
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

KIM の製品 identity は標準 KVM/QEMU/libvirt であり、任意 hypervisor abstraction や KIM 専用 hypervisor distribution を目的にしません。core management function は upstream/standard component の patch、fork、proprietary modification を要求しません。KIM metadata は標準 interface からの manageability を失わせません。

一般的な Linux distribution の package、service manager、security module、firewall、tuning 差異は KIM Host Agent 内の OS Integration Adapter が吸収します。新しい distribution 対応のため Control Plane へ OS 名による分岐を追加しないため、標準 KVM neutrality と Host OS portability は両立しています。

## 10. Designed and Undecided

### Phase 0で設計済み

- responsibility、identity、trust、authority、failure semantics
- Placement/Final Admission、Execution/Lease/Attempt、Agent Gateway
- Host lifecycle/compliance/grouping、Availability/Recovery/resilience intent
- Compute/NFV dataplane、Storage/Attachment/Fencing、Network resource
- Persistence/DR、Upgrade/Compatibility、Time、PKI lifecycle
- Invariant、traceability、acceptance/fault/conformance test contract

### 後続 gate へ明示的 Deferred

- 初期 Validated OS combination
- PMD assignment の初期 certified mode
- Enrollment evidence と policy-auto enrollment の deployment profile
- HostGroup initial catalog と binding priority range
- public Failure Domain class と topology disclosure profile

これらは owner と Developer/Technical Preview gate を持ち、Phase 0 Architecture invariant を変更しない support/certification/tuning profile です。正式な component 名は KIM Host Agent として Closed 済みです。

## 11. Findings and Closure

| ID | Finding | Severity | Disposition |
|---|---|---|---|
| P0ER-001 | Phase 0 exitに実装済み2-distribution system testが混在 | Medium | Phase 1 exitへ移動済み |
| P0ER-002 | generation/Lease/UNKNOWNの共通scope規則がdomain文書へ分散 | Medium | Cross-domain Semantic Registryを追加済み |
| P0ER-003 | Document Governanceの採番重複とUpgrade/Time/PKI gate不足 | Low | 修正済み |
| P0ER-004 | 23 ADR がすべて Proposed | High / Gate blocker | 個別 review 後に23件 Accepted、解消済み |
| P0ER-005 | Phase 0 期限 Open Question が24件未処理 | High / Gate blocker | Closed 17、明示的 Deferred 7、解消済み |
| P0ER-006 | 主要文書が Draft | Medium / Gate blocker | Baseline 昇格、解消済み |
| P0ER-007 | 標準 KVM 非特殊化が上位 Invariant で未固定 | High | Product Vision、ADR-0004、HST-012〜014、INV-AGT-008/009 へ追加済み |
| P0ER-008 | KIM Host Agent の primary language/native boundary が曖昧 | Medium | ADR-0003、Architecture、INV-AGT-010、AT-AGT-010 へ追加済み |
| P0ER-009 | 日本語/ASCII spacing rule と安全な lint rollout が未定義 | Low | Writing Conventions、INV-DOC-003、AT-DOC-003 へ追加済み |

## 12. Formal Exit Completion

1. ADR-0001〜0023: completed、23 Accepted。
2. Phase 0 Open Questions: completed、17 Closed / 7 Deferred。
3. Requirement/Architecture/Invariant/Test、local link、duplicate/undefined ID check: passed。
4. Exit findings: all blockers closed。
5. Normative documents: Baseline promoted。

Phase 0 formal exit の残 blocker は **0件** です。Deferred profile は各 target gate の blocker であり、Phase 0 を再度 HOLD にしません。

## 13. v1 Gap Analysis Entry

Gap Analysis は [kvm-topology v1 to KIM Gap Analysis](v1-gap-analysis.md) として開始済みです。v1 commit `c481388` の code/schema/test/package を確認し、33件を分類しました。

- Existing: 3
- Reusable: 4
- Partial: 18
- Missing: 6
- Conflicting: 2

主要 reuse は Go Agent Core、durable spool/journal、Job/Command/Lease/Attempt、stale Result fencing、Host authority generation、typed libvirt adapter、PKI primitive、PostgreSQL transaction/outbox、fault fixture、Debian/systemd packaging です。

主要 conflict は Agent→Controller direct transport と legacy `executor_credential_*` authority です。前者は Agent Gateway へ再配置し、後者は active authority として discard して必要な history だけを archive evidence とします。
