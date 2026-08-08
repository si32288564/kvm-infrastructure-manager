# Upgrade and Compatibility Architecture

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

KIMのupgradeを単なるbinary置換ではなく、Control Plane、database schema、Agent、protocol、extension、backend client、Host capabilityを一つの互換性契約の下で安全に進化させるproduct lifecycleとして定義します。

本書は次を保証します。

- mixed-version期間にも一つのauthority semanticsを維持する。
- compatibilityをversion文字列から推測せず、署名済みRelease Manifestと実測capabilityで判定する。
- upgrade coordinationとresource/backend mutation authorityを分離する。
- rollback可能点と不可逆なcontract pointを事前に固定する。
- failed/UNKNOWNなupgradeを既存VMの推測rollbackやHost authorityの自動再armingへ波及させない。
- online/offlineのどちらでも同じartifact、provenance、verification contractを使用する。

Database schemaの詳細な`expand -> migrate -> switch -> contract`、backfill、partition、PITR契約は [Data and Persistence Architecture](data-persistence-architecture.md) を正本とします。本書は製品全体の順序、compatibility、campaign、rollback gateを定義します。

## 2. 責任境界

KIMが所有します。

- KIM product artifactとRelease Manifestの検証、compatibility判定、rollout state
- Control Plane、Agent Gateway、worker、Agent、KIM extension/adapterのtarget revision
- schema migration、feature activation、API/protocol/event compatibility gateのcoordination
- canary/batch、max unavailable、pause/abort、verification、rollback decisionの記録
- Host maintenance、dispatch eligibility、placement eligibilityとの連携
- release evidence、SBOM/provenance、support matrix、audit

KIMが所有しません。

- Host OS、kernel、firmware、BMC、physical switch、Ceph cluster、OVN cluster等の汎用upgrade lifecycle
- distribution package repositoryやexternal orchestratorの一般的な構成管理authority
- external IdP、WIM、NFVO/VNFMのupgrade
- application/NF内部のupgrade、schema、traffic migration

外部systemがこれらをupgradeする場合、KIMはmaintenance boundary、required capability、observed version、compatibility/compliance、再arming条件だけを管理します。外部の完了claimだけでready/compatibleにしません。

## 3. Version Dimensions

単一の`product_version`だけでは互換性を表しません。最低限、次を独立して管理します。

| Dimension | Authority / purpose |
|---|---|
| Product release | customer-visible release identity |
| Artifact digest | 実行binary/image/moduleのimmutable identity |
| Database schema generation | reader/writer semanticsとmigration state |
| Public API major/minor | client-visible request/response contract |
| Agent protocol version | Gateway handshake、message envelope、session contract |
| Command/Result schema | typed operationごとのpayloadとoutcome contract |
| Event schema | immutable Outbox payloadとconsumer compatibility |
| Extension contract | adapter/module/evaluatorのinput/output capability |
| Backend compatibility profile | PostgreSQL、NATS、libvirt/QEMU、OVN/OVS/DPDK、Ceph等の組合せ |
| Host baseline/control version | Host上のrequired capabilityとcompliance semantics |

versionは順序比較だけで互換とみなしません。各consumerは`read_min/read_max`、`write_min/write_max`、supported feature/command/control setを宣言し、Release Manifestの許可graphとruntime observationの両方を満たす必要があります。

compatibility statusは次の意味を持ちます。

- `VALIDATED`: release certification済みの明示的組合せ。
- `COMPATIBLE`: contract/capabilityを満たすが組合せとして未認定。
- `INCOMPATIBLE`: contract、required capability、known deny ruleのいずれかに違反。
- `UNKNOWN`: version、digest、provenance、capability、freshnessを十分に検証できない。

`UNKNOWN`を`COMPATIBLE`へ丸めません。`Compatible`は製品サポートを暗黙に意味しません。

## 4. Release Manifest

各releaseはimmutableな`ReleaseManifest`を持ちます。

```text
ReleaseManifest
├─ release_id / product_version / channel
├─ artifact identities and digests
├─ signature / build provenance / SBOM references
├─ component dependency graph
├─ supported source releases and upgrade paths
├─ API / protocol / command / event schema ranges
├─ database schema min / target / max
├─ backend and Host support matrix
├─ migration and verification artifact digests
├─ feature activation requirements
├─ Feature Gate dependency graph / rollback closure
├─ retained event/evidence decoder dependencies
├─ rollback target and last reversible phase
├─ required approvals / maintenance policy
└─ certification evidence / known deny rules
```

Manifestはrelease後に書き換えません。修正は新しいmanifest revision/release identityとして発行します。Control Plane、Agent、extensionは自己申告versionだけでなく実artifact digestとattested/deployment provenanceを報告し、manifestと一致しないartifactを`UNKNOWN`または`INCOMPATIBLE`として隔離します。

offline bundleも同じManifest、artifact digest、SBOM、migration、support matrix、verification fixtureを含みます。offlineだからsignature/provenance/compatibility gateを省略しません。

## 5. Compatibility Authority

`CompatibilityDecision`は次へbindするimmutable decisionです。

```text
subject identity + observed artifact digest
+ source/target Release Manifest revisions
+ relevant schema/protocol/backend/Host generations
+ evaluator artifact digest
+ decision + bounded reasons + evidence digest
```

判定は対象scopeごとに行います。

- Control Plane replicaがcurrent DB schemaをread/writeできるか。
- GatewayとAgentが共通protocol envelopeをnegotiationできるか。
- Command producerとAgent moduleが同じtyped Command/Result schemaを理解するか。
- Outbox producerとconsumerがevent schemaをdecodeできるか。
- adapter/extension/evaluatorがcurrent Core contractとcertificationを満たすか。
- Host/backendのobserved version/capabilityがtarget support matrixを満たすか。
- target featureが全required replica/Host/backendで利用可能か。

cached decisionは入力generation/freshnessが変わればauthorityを失います。Final dispatch、schema switch、feature activation、Host rearmingではcurrent decisionを再評価します。

## 6. Upgrade Domain Model

```text
UpgradeCampaign
├─ source / target Release Manifest
├─ scope / strategy / approval
├─ UpgradePlan revision
├─ Compatibility Snapshot
├─ UpgradeWave
│  └─ UpgradeTarget
├─ Migration / Feature Gate references
├─ Rollback Boundary
└─ Verification Summary
```

- `UpgradeCampaign`: 一つのsource/target pathとscopeを持つauthority resource。
- `UpgradePlan`: dependency order、wave、budget、pause/abort、verification、rollbackを固定したimmutable revision。
- `CompatibilitySnapshot`: plan時点のcomponent/schema/backend/Host evidence。実行authorityではなく、各gateでcurrent stateを再検証します。
- `UpgradeWave`: canary/batch/control-plane/agent等の対象集合と`max_unavailable`、failure threshold。
- `UpgradeTarget`: component/Hostごとのdesired artifact、observed artifact、state、Attempt/evidence。
- `FeatureGate`: target binaryが存在しても、全required participantが対応するまで新semanticsを有効化しないswitch。
- `RollbackBoundary`: reversible phase、required retained artifact/schema/decoder、不可逆条件を固定します。

すべてのtransition、decision、approval、Attempt、Result、Observationはappend-only evidenceを残します。summaryは更新できますが過去の失敗/UNKNOWNを改変しません。

複数Feature Gateはversioned DAGとして`requires/conflicts_with/rollback_requires`を持ちます。publish時にcycleを拒否し、activationはtopological order、rollbackは依存closureの逆順で行います。Gate単体の互換性だけで、依存先が未active/rollback不能な状態を許可しません。

## 7. Upgrade State Machine

```text
DRAFT
  -> PREFLIGHT
  -> PREPARED
  -> CANARY
  -> ROLLING
  -> VERIFYING
  -> FINALIZING
  -> COMPLETED
```

任意の実行phaseから`PAUSED`、`ABORTING`、`ROLLING_BACK`、`BLOCKED`へ遷移できます。外部side effectやartifact適用結果が不明なら`UNKNOWN`をtarget/step単位で保持し、read-back前に再適用や逆操作を開始しません。

### Preflight

- source state、current release/schema、active migration、restore mode、quorum/replica healthを確認する。
- target Manifest、artifact、signature/provenance、upgrade path、rollback artifact、容量を検証する。
- API/protocol/event/extension/backend/Host compatibilityを評価する。
- open `UNKNOWN`、active recovery storm、maintenance conflict、unsupported componentをscope別にblockする。
- backup/restore evidenceとrollback boundaryを確認する。

### Prepare

- backward-compatible schema expandとdecoder/reader追加を先行する。
- target artifactをstageするが、feature activationやCommand dispatch authorityを暗黙に変更しない。
- immutable target snapshotとwave assignmentを確定する。

### Canary and Rolling

- canaryを先行し、readiness、schema/API/protocol、error rate、reconciliation、resource invariantを検証する。
- failure threshold超過、compatibility drift、quorum/availability budget違反で自動pauseする。
- wave membershipは開始時snapshotへbindし、途中のselector/group driftで対象を暗黙追加しない。
- 各target適用直前にcurrent compatibility、maintenance、Lease、authority generationを再検証する。

### Verify and Finalize

- 全required target、schema/backfill、feature gate、API/Agent/backend smoke、existing workload continuityを検証する。
- rollback window中は旧artifact、旧reader/decoder、旧schemaを保持する。
- destructive schema contract、old protocol/event decoder除去、old artifact GCは別の明示approvalを持つfinalization stepとする。

## 8. Mixed-Version Semantics

初期product policyは一つのcampaign内で`N`と`N-1`だけを許可します。これは暗黙の互換性ではなく、該当Release Manifest間に明示edgeがある場合だけ成立します。

- mixed-version中のwrite formatは全active writerが解釈できる範囲へ制限する。
- new-only field/enum/authority semanticsはFeature Gateが開くまでwriteしない。
- old replicaが意味を誤解する値をunknown field toleranceだけで送らない。
- readinessはprocess aliveではなく、current schema/protocol/feature/write contractへの適合を含む。
- N-2、unmanaged build、digest mismatchはserving/dispatch poolへ参加できない。
- mixed-version windowにはdeadlineを持たせ、無期限の互換modeを通常状態にしない。

同じresourceを異version workerが処理しても、PostgreSQL generation、Lease、idempotency、Result fencingは共通です。upgrade coordinatorはこれらのauthorityを代替しません。

## 9. Control Plane and Database Order

Control Plane rolling upgradeはHA/readiness budgetを守り、一度にrequired quorum/serving capacityを失いません。

1. compatible schema expand/readerを導入する。
2. old/new replicaが同じauthority semanticsで動作することを検証する。
3. replicaをcanary/batchで置換する。
4. required replicaのtarget capabilityを確認する。
5. checkpointed migration/backfillを進める。
6. feature/read-write gateをswitchする。
7. rollback window終了後にschema contractを別承認で実施する。

leader/worker failoverでUpgrade Leaseが失われた場合、new ownerはDB state、artifact observation、migration receiptをread-backします。process内のstep memoryから継続しません。schema contract後のbinary rollback、PITRを通常rollbackとして実行することは禁止します。

## 10. Agent, Gateway, and Command Compatibility

Agent sessionは次へbindします。

- Host identity、session/authority generation
- Agent artifact digestとproduct/component version
- protocol envelope range
- supported Command/Result schema set
- operation module/capability generation
- current Host Baseline/Compliance evidence

Gatewayは共通versionを明示negotiationし、unsupported/unknown versionを接続成功として扱いません。接続できても、対象Command schema/capabilityが互換でなければdispatchしません。Control Planeがnew Commandをold Agentへdown-convertしたり、別operationへsilent fallbackしたりしません。

Agent binaryの配布・切替をKIMが提供する場合は、署名済みartifact、atomic activation、local durable receipt、health/read-back、bounded rollbackを持つclosed product operationに限定します。Host OS/kernel/libvirt/QEMU/packageの一般upgradeはexternal remediation boundaryを通します。

Agent upgrade中はHostをdispatch drainし、新規mutationを停止します。既存VMは維持します。再接続、binary置換、version一致だけでHost authorityをarmせず、current enrollment、Baseline、preflight、Compliance、session generationを再検証します。

## 11. API and Event Compatibility

- public APIのcompatible変更は同一majorで行い、削除/意味変更は新majorとdeprecation windowを必要とする。
- serverはsupported API version/capabilityを公開し、client requested versionを推測で別semanticsへ変換しない。
- idempotency scope、resource ID、ETag/generation、Operation/Event correlationはcompatible upgradeで安定させる。
- Event payloadは発行時schema/digestのimmutable recordであり、upgrade後のresourceから再生成しない。
- Outbox再送とarchive replayに必要なdecoder/schema catalogをretention期間中保持する。
- Release Manifestは各Event/evidence schemaを参照するonline/archive Retention Policy、legal hold、decoder artifactへbindし、payload referenceが残る間はdecoderをfinalize/GCしない。
- consumer incompatibilityはdelivery failureとして隔離し、domain authorityやAvailability responsibilityを変更しない。

## 12. Extension, Evaluator, and Adapter Upgrade

extension lifecycleは [Extensibility Architecture](extensibility-architecture.md) と [Extension Conformance Contract](extension-conformance.md) を正本とします。Upgrade Campaignは次を追加gateとして要求します。

- immutable digest、contract range、Core release range、support tier、certification evidence
- drain、in-flight Operation/Lease、state/evidence decoder compatibility
- shadow/canary比較とbounded failure threshold
- old/new adapterが同じexternal objectを同時mutationしないownership fencing
- rollback後もnew schema/evidenceを安全に読めること

extensionがincompatible/UNKNOWNなら影響scopeの新規mutationを停止します。Core DB直接write、独自migration、独自retry/Lease、arbitrary plugin loadでupgradeを迂回できません。

## 13. Backend and Host Compatibility

release support matrixは少なくとも次を組合せとして管理します。

- PostgreSQL、NATS、external IdP/JWKS behavior
- Host OS/kernel、QEMU、libvirt、machine type、CPU model
- OVN、OVS、DPDK、NIC/driver/firmware
- Ceph client/cluster feature、local LVM/tooling
- Agent adapter/module/evaluator artifacts

backend/Host versionはinventory/typed probeからprovenance付きで観測します。名前やversion prefixだけでcapabilityを推測しません。

- target releaseで既存VMを維持できても、新規create/migration/recoveryには追加capabilityが必要な場合を区別する。
- 既存VMのmachine type、CPU model、firmware、device ABIをimmutable Runtime Compatibility Bindingとして保持し、QEMU/libvirt upgradeや新規default変更だけで書き換えない。
- 新規VMのdefault machine type/CPU modelと既存VMのruntime compatibilityを別判定し、既存binding変更はexplicit compatibility-checked migration/rebuild operationを要求する。
- incompatible destinationはPlacement/Recovery eligibilityから除外する。
- current workloadが将来非対応になる場合、upgrade前にimpact inventoryとexplicit remediation/exceptionを要求する。
- backend upgrade中のhealth/feature UNKNOWNをhealthyへ丸めず、対象side effectを停止する。
- support matrix変更だけで既存VM、Port、Volumeを暗黙移動・停止・再構成しない。

## 14. Rollback and Abort

rollbackは「過去の状態に見えるよう履歴を書き換える」操作ではありません。新しいUpgrade Plan revisionとAttemptとして実行します。

rollback可能条件:

- target Manifestがsourceへの明示rollback edgeを持つ。
- destructive schema contract/irreversible data transform前である。
- source artifact、reader/decoder、configuration、migration receiptが保持されている。
- source componentがcurrent schema/event/evidenceを安全に解釈できる。
- backend side effectとHost authorityがcurrent observationで確定している。

条件を満たさない場合は`BLOCKED`としてforward repairを行います。自動PITR、database restore、Host/backend推測rollbackを行いません。rollback失敗/timeoutも`UNKNOWN`であり、blind retryしません。

abortは未開始waveを止め、active targetを安全な境界まで収束させます。部分適用済みtargetを無条件にsourceへ戻しません。

## 15. Failure Semantics

| Failure | Containment / recovery |
|---|---|
| Manifest/artifact signature・digest不一致 | artifact quarantine、campaign開始禁止 |
| compatibility evidence missing/stale/conflict | affected target/scopeを`UNKNOWN`、switch/dispatch停止 |
| Control Plane target readiness失敗 | wave pause、serving old replica維持、quorum budget保護 |
| schema migration/lock/backfill失敗 | migration pause/rollback transaction、switch/contract禁止 |
| Agent update response loss | Host drain維持、artifact/service/journal read-back、再arming禁止 |
| protocol/Command schema mismatch | session/Command拒否、down-convert/silent fallback禁止 |
| extension/backend incompatibility | affected capability scopeをineligible、Core authority維持 |
| verification threshold超過 | later wave停止、current evidenceに基づきrollback/forward repair判断 |
| coordinator failover | durable Campaign/Lease/Receiptから再開、in-memory progress不使用 |
| rollback outcome unknown | target隔離、read-back、反対方向の再適用禁止 |

既存VMの稼働継続はControl Plane upgrade成功の証拠ではありません。一方、Control Plane/Agent upgrade failureを理由に既存VMを停止、再作成、別Hostへ移動しません。

## 16. Security and Authorization

- release publish、campaign approve/start、schema switch/contract、feature activation、rollback、force bypassを個別permissionにする。
- two-person approvalを設定可能にし、不可逆stepとsupport overrideへ必須化できる。
- artifact取得・検証・stage・activation identityを分離し、通常domain workerへrelease signing権限を与えない。
- secret/credentialをManifest、log、Event、diagnosticへ含めない。
- artifact、SBOM、provenance、vulnerability exception、approval、compatibility decisionを監査可能に関連付ける。
- break-glassでもschema/Lease/fencing/tenant isolation invariantを迂回できない。

PKI、signing key、workload identityのrotation/revocation詳細は将来のPKI / Trust Lifecycle Architectureで定義します。本書ではverified identity/digestをupgrade gateの入力として要求します。

## 17. Observability and Operator Contract

最低限、次を公開します。

- Campaign/Plan/Manifest revision、phase、wave、target status
- source/target/observed artifactとcompatibility decision/reason
- mixed-version population、deadline、oldest active version
- schema migration/backfill/checkpoint/lock budget、Feature Gate
- unavailable/drained/blocked/UNKNOWN targetとfailure threshold
- rollback eligibility、last reversible phase、retained artifact/decoder status
- API/protocol/event/backend/Host compatibility drift

Tenantには内部artifact、Host、backend topologyを公開せず、service availability、Operation impact、deprecation/capabilityだけをbounded statusとして公開します。

## 18. Verification Contract

最低限、次を自動試験またはrelease evidenceとして保存します。

- N-1/N Control Plane混在でread/write/idempotency/Lease/fencing semanticsが一致する。
- schema expand、concurrent backfill、switch、contract、rollback boundary。
- old/new Gateway-Agent negotiationとCommand/Result schema matrix。
- API backward compatibility、Event replay、old/new consumer matrix。
- Event/evidence Retention Policyとdecoder artifact reference、legal hold中decoder GC拒否。
- canary threshold、wave pause、coordinator crash/failover、resume。
- Agent update response loss、local activation failure、read-back、Host rearming gate。
- extension/adapter/evaluator shadow/canary、ownership fencing、rollback。
- backend/Host version drift、unsupported recovery destination、existing workload continuity。
- QEMU/libvirt upgrade後の既存machine type/CPU model/device ABI維持と新規default分離。
- Feature Gate DAGのcycle拒否、topological activation、dependency-aware rollback。
- offline bundleのdigest/signature/SBOM/migration completeness。
- destructive finalization後のrollback拒否とforward repair。

## 19. 禁止事項

- version文字列、process alive、deployment successだけでcompatibility/readinessを確定する。
- N-2、unmanaged build、digest不明componentを通常serving/dispatch poolへ参加させる。
- mixed-version中にold writerが理解できないauthority semanticsを有効化する。
- schema contract、old decoder/artifact GCをrollback window終了前に行う。
- upgrade coordinatorがCommand Lease、Placement、Attachment、Network Binding等のauthorityを代替する。
- Agent reconnectやupgrade完了claimだけでHost authorityをarmする。
- failed/UNKNOWN upgradeをPITR、blind reinstall、backend cleanupで自動的に巻き戻す。
- support matrix変更だけで既存resourceを暗黙mutationする。
- QEMU/libvirt upgradeやdefault変更だけで既存VMのmachine type/CPU model/device ABIを更新する。
- payload retention中にrequired Event/evidence decoderを削除する。
- dependency未充足またはcycleを持つFeature Gateをactivateする。
- offline/緊急upgradeを理由にartifact verification、authorization、auditを省略する。
