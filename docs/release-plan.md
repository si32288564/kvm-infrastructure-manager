# リリース計画

- 状態: Draft
- 更新日: 2026-08-09

日付ではなく、検証可能な exit criteria で段階を進めます。具体的な日程はチーム体制と対象 OS の決定後に設定します。

## Phase 0: Architecture Baseline

### 成果物

- 製品境界、用語、主要ユースケース
- API resource model と OpenAPI skeleton
- Control Plane / Agent protocol の ADR
- VM create の end-to-end sequence と failure model
- Threat model
- 対応予定 OS と component version 方針
- Agent の OS Integration Adapter 契約と support tier
- Responsibility Boundary
- Placement Architecture と transactional final admission
- Operation / Job / Command / Lease / Attempt のExecution model
- Agent GatewayとAgent transport境界
- PostgreSQL authorityのHA/DR model
- System-wide Failure Modelとfault injection matrix
- Extensibility Architecture、Core invariant、extension conformance contract
- Architecture InvariantsとRequirement-to-Test Traceability Matrix
- 13 failure classのFault Injection Matrix
- NFV Dataplane Resource ModelとOVS-DPDK support boundary
- Host Lifecycle、Enrollment、Baseline、Continuous Compliance、Decommission model
- Hardware Identity Evidence policy、Evaluator Artifact rollout、External Remediation trust contract
- HostGroup、Failure Domain、Placement Scope、rollout/maintenance snapshot model
- Availability Responsibility、VM Binding、Host Failure Epoch、Managed Recovery model
- Workload Resilience Intent/Domain Claimとdurable Recovery Budget/Queue model
- Data classification、Outbox/Inbox、retention/GC、schema migration、partition、PITR restore model
- Storage Backend/Class、Volume Binding、Attachment Claim/Observation、single-writer/fencing/handoff model
- IPAM/Segment Claim、Port Binding/Handoff、OVN layered realization、Gateway/NAT/Security model
- Release Manifest、Compatibility Decision、N/N-1 mixed-version、Upgrade Campaign/Wave/Feature Gate、rollback boundary model

### Exit criteria

- Must 要件に owner と検証方法がある。
- 主要な未決事項が ADR または open question として追跡される。
- VM create/delete、Host loss、Control Plane loss の設計レビューが完了する。
- 少なくとも2系統の Linux ディストリビューションで同じ Control Plane build を用いた preflight と VM lifecycle を検証する。
- Identity、Host configuration、Network、NFVO/VNFM/WIMのauthority境界がADRで承認される。
- placement競合、lease expiry、stale result、outcome unknown、DB failover/restoreのfailure scenarioがテスト設計に落ちる。
- 各failure classにDetect/Contain/Fence/Observe/Recover/Reconcile/Escalateと禁止操作が割り当てられる。
- 初期extension pointがCore authorityを迂回しないことをcontract testで検証できる。
- PMD CPU、DPDK memory、Port/RxQを含むdataplane admissionが既存placement invariantsへtraceされる。
- identityからauthorityまでのEnrollment/Baseline/Compliance gateがfailure/test matrixへtraceされる。
- HostGroup membership generationがPlacement final admissionとrollout/maintenance snapshotへtraceされる。
- Workload/Infrastructure/Manual responsibilityがfencing、Placement、Execution、Fault/Event testへtraceされる。
- NF側HA domain separationとRecovery storm budget/fairnessがtransaction/failover testへtraceされる。
- authority/history分離、Outbox/Inbox atomicity、schema migration、GC、PITR restore epochがfailure/test matrixへtraceされる。
- attach/detach UNKNOWN、stale watcher/lock、Host recovery、Local LVM locality、Ceph client fencingがtest matrixへtraceされる。
- IP/MAC/VLAN/VNI conflict、OVN response loss/SB lag、Port recovery、Gateway/NAT/Security UNKNOWNがtest matrixへtraceされる。
- Manifest/artifact mismatch、mixed writer semantics、canary pause、Agent update UNKNOWN、schema finalization/rollback boundaryがtest matrixへtraceされる。
- 全Must requirementがArchitecture、ADR、Invariant、Testへtraceされる。
- 全InvariantにAT/FI/XCTの検証IDがあり、未追跡をCIで検出できる。

## Phase 1: Developer Preview

### Scope

- 単一 Control Plane
- Host Agent 登録と inventory
- manual Enrollment approval、Host Profile/Baseline Assignment、read-only Compliance
- provenance付きidentity evidence収集とEvaluator artifact/input digest付きResult
- explicit HostGroup、Placement Pool、materialized membership generation
- Availability Policy/Pool bindingとVM Availability Bindingのread-only表示
- explicit Workload Resilience Group/memberとread-only Recovery Queue model
- Image、Flavor、VM lifecycle
- 基本 scheduler
- VLAN network
- transactional IPAM/VLAN Claim、Port Binding、OVN NB/SB/Host layer status
- OVS-DPDK capability discoveryとread-only dataplane observation
- local storage
- Local LVM Volume/Attachment generationとsingle-writer Claim
- Operation API、監査、基本メトリクス
- transactional Outbox/Inboxとschema generation/readiness
- Release Manifest検証、Compatibility Decision表示、schema expandと単一target canaryのread-only campaign model

### Exit criteria

- 2 Host で API から VM を繰り返し作成・削除できる。
- API 再送と Agent 再起動で重複 VM が作られない。
- Host 切断時に新規配置されず、復旧後に状態が収束する。
- clean install と uninstall 手順が自動試験される。
- 最初の Validated OS 組合せと、Compatible/Unsupported の判定方法が公開される。
- dry admissionとtransactional final admissionの競合再選択を自動試験する。
- write-before-execute journal、lease expiry、stale Result fencingを自動試験する。
- Developer Preview対象Invariantのtest contractがImplemented状態になる。
- DPDK claimのdry/final admissionとworkload resource競合をfixtureで検証する。
- authenticatedだけのHostがREADY/armedにならず、Critical driftがplacementをblockすることを検証する。
- dry/final間のHostGroup membership変更を検出し、stale Hostへ予約しないことを検証する。
- Availability Policy欠損/競合HostをPlacementせず、WORKLOAD_MANAGED障害で自動restartしないことを検証する。
- concurrent resilience memberを同一hard domainへcommitせず、Budget Lease二重取得を防ぐことを検証する。
- domain mutation/OutboxとInbox/domain decisionの不可分性、N/N-1 expand migrationを検証する。
- attach/detach response lossでClaimを誤解放せず、Local LVMを別Host recoveryしないことを検証する。
- duplicate IP/VLANをcommitせず、OVN NB成功だけでPort ACTIVEにしないことを検証する。
- N-1/N reader/writer fixture、artifact digest mismatch、Feature Gate前のnew-only write拒否を検証する。

## Phase 2: Technical Preview

### Scope

- 3-node Control Plane
- OIDC、Tenant、RBAC、Quota
- policy-based Enrollment、safe typed convergence、continuous drift detection
- selector-based HostGroup、Failure Domain placement、immutable rollout snapshot
- Host Failure Epoch、WORKLOAD_MANAGED event、MANUAL decision workflow
- NFVO Resilience Intent mapping、multi-dimension Domain Claim、durable Recovery Queue/fair budget
- OVN overlay、Subnet、Port、Security Group
- Router/Gateway、Floating IP/NAT、DHCP、PortBindingHandoff、network-side UNKNOWN resolver
- Ceph RBD、Volume、Snapshot
- Ceph RBD client/watcher/lock observation、typed fencing、Attachment Handoff
- NUMA、HugePages、CPU Pinning
- OVS-DPDK PMD/RxQ allocation、vhost multiqueue、typed online operation
- Backup/restore、診断バンドル
- partitioned history、retention/GC、checkpointed backfill、PITR recovery mode

### Exit criteria

- 20 Host、500 VM の連続試験を完了する。
- Control Plane の単一ノード障害で API が継続する。
- Tenant isolation test を通過する。
- DB restore 後に backend state と収束できる。
- restore epochが旧Lease/session/claimをfenceし、backend-only resourceをquarantineする。
- Ceph shared Volume recoveryでcompute/storage/attachment fencingをすべて検証する。
- OVN controller/gateway failoverでidentity/segmentを再利用せずlayered realizationへ収束する。

## Phase 3: Product Beta

### Scope

- 100 Host 規模検証
- cold/live migration
- SR-IOV
- disruptive dataplane maintenance operationとOVS/DPDK version certification
- Baseline rollout、maintenance-required/external remediation、decommission workflow
- Evaluator shadow/canary rolloutとExternal Remediation request/claim + KIM再観測
- Group-based maintenance wave、failure-domain concurrency、Placement Scope exposure
- INFRASTRUCTURE_MANAGED restart/evacuate、fencing/storage eligibility、Availability Rebind rollout
- correlated failure storm、backend circuit breaker、priority/fair-share recovery campaign
- NFVO integration profile
- ローリングアップグレード
- durable Upgrade Campaign、canary/batch、Agent drain/update、API/Event/extension/backend compatibility matrix
- offline bundle、SBOM、artifact signing
- 運用 UI とアラーム管理

### Exit criteria

- 100 Host、5,000 VM の性能・耐久試験を完了する。
- N-1 から N のアップグレードとロールバック演習を完了する。
- coordinator failover、canary threshold、Agent response loss、rollback不能点のfault injectionを完了する。
- 外部セキュリティ評価の重大指摘が解消される。
- サポート診断と既知問題の運用フローが確立する。

## Phase 4: General Availability

### 必須成果物

- インストール、設定、運用、アップグレード、DR、Troubleshooting 文書
- サポートマトリクスと互換性ポリシー
- SLA/SLO、サポート期間、脆弱性対応ポリシー
- ライセンス、Third-party notice、SBOM
- release note と既知問題

### Exit criteria

- 全 Must 要件が traceable な acceptance test を通過する。
- 30日以上の soak test で release blocker がない。
- backup/restore、Control Plane failover、Host failure の演習が完了する。
- インストールとアップグレードを第三者が文書のみで実施できる。

## リリース品質ゲート

各リリース候補は以下を満たす必要があります。

- unit、integration、contract、system、upgrade test
- API backward compatibility check
- migration forward/backward test
- Release Manifest/compatibility graph、mixed-version writer、Feature Gate、rollback boundary test
- vulnerability、license、secret scan
- Architecture invariant/traceability coverage check
- critical Fault Injection subsetとExtension Conformance suite
- signed artifact と SBOM
- performance regression check
- documentation link check
- release note と support matrix 更新
