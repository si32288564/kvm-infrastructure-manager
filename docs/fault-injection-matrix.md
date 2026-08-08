# Fault Injection Matrix

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

System-wide Failure Modelの各failure classを、再現可能なinjection、期待する検出、封じ込め、永続証拠、禁止副作用、回復条件へ落とします。

## 2. Test Contract

各fault injection testは以下を出力・保存します。

- test ID、seed、build/version、topology
- injection開始/終了時刻とscope
- authority/desired/observed generation
- Operation、Job、Command、Lease、Attempt correlation
- expected/actual detection code
- containment/fencing evidence
- prohibited side-effect assertion
- recovery/verification evidence
- database invariantsとaudit record

test harnessが障害を解除しただけでは合格になりません。期待したauthority stateとbackend stateへの収束を検証します。

## 3. Matrix

| ID | Failure class / Injection | Expected detection | Expected containment / fencing | Persisted evidence | Prohibited side effect | Recovery criterion |
|---|---|---|---|---|---|---|
| FI-CLIENT-001 | mutation commit後、API responseをdropしてclient再送 | duplicate idempotency key | 同一Operationを返す | request digest、idempotency record、単一Operation | 二重Desired/VM/Command | 同一resource/Operation IDを返す |
| FI-CLIENT-002 | stale ETag/desired generationでupdate | precondition failed | mutation transactionを開始しない | request/audit conflict | current state上書き | clientが最新resourceを再取得 |
| FI-CP-001 | workerをbackend call前にkill | started work timeout/journalなし | Lease/Attempt authorityを再評価 | started/UNKNOWNまたは未開始evidence | success推測 | typed resolverまたはsafe retry |
| FI-CP-002 | backend call後、DB result commit前にworker kill | Attempt timeout | resource mutationをblock | Attempt UNKNOWN、correlation | 反対operation/duplicate mutation | observation/read-backで解決 |
| FI-DB-001 | primary failover中に同時mutation | primary/term change、transaction error | old primary fencing、bounded retry | commit/idempotency/term evidence | split-brain commit、data loss | RPO 0、単一committed result |
| FI-DB-002 | commit成功後response loss | transaction outcome unknown | idempotency照会 | committed rows、request digest | 二重commit | 元Operationを回収 |
| FI-DR-001 | backup restore後に新しいbackend VM/Port/Volumeを提示 | generation/provenance mismatch | recovery mode、quarantine | unresolved resource、observation evidence | 自動adopt/削除/attach | explicit authorized adoptionまたは外部所有確定 |
| FI-DATA-001 | domain mutationまたはOutbox insertの片方をcommit直前に失敗 | transaction failure | 全domain/Operation/Outbox row rollback | transaction/error/idempotency evidence | eventなしauthority commit、phantom event | 同一request再送で一つのatomic commit |
| FI-DATA-002 | Outbox publish成功後ACKをdropしpublisherを再起動 | ACK loss/expired publish Lease | 同じevent IDを再送 | Outbox attempts/receipt correlation | 新event生成、domain mutation再実行 | consumer dedupeとOutbox published収束 |
| FI-DATA-003 | 同一Inbox keyへ同一/異なるpayload digestを並行送信 | dedupe/unique conflict | 同一digestは同じReceipt、異digestは拒否 | source/generation/key/digests/audit | domain decision二重commit、last-wins | original decision/Receipt回収 |
| FI-DATA-004 | active authority reference、UNKNOWN、open Operation、legal hold中のevidenceをGC対象化 | eligibility/reference/hold guard | GC Candidate/Lease発行拒否 | references/hold/reason/policy generation | safety evidence/tombstone削除 | reference解消と新snapshot再評価 |
| FI-DATA-005 | partition detachまたはbatch GC途中でworker/DB failover | Lease/transaction interruption | committed batchだけ保持し残り停止 | GC snapshot/Lease/Receipt/partition manifest | orphan reference、backend cleanup、二重delete | current Leaseでcheckpointから再開しintegrity確認 |
| FI-DATA-006 | backfill対象rowをAPIが同時更新しworkerを再起動 | generation/precondition conflict | stale backfill write拒否、checkpoint retry | old/current generation、migration/checkpoint | current value上書き、二重変換 | current generationからidempotent再計算 |
| FI-DATA-007 | N/N-1混在中にcontract DDL、長時間row/index lock、未知schema switchを注入 | compatibility/readiness/lock timeout | switch/contract停止、serving replica維持 | binary/schema ranges、DDL error、migration state | 互換外write、無期限API停止 | compatible expandまたは承認rollback後に再開 |
| FI-DATA-008 | backup manifestからWAL segment/artifact/checksumを欠損・破損させる | manifest/integrity/restore verification failure | production recovery開始拒否 | backup IDs、LSN range、digest/error | 不完全DBでmutation開始 | 完全なverified backup setでisolated restore成功 |
| FI-DATA-009 | PITR後にpre-restore Lease/session/publisher claimからmutation/resultを送信 | restore epoch/authority generation mismatch | stale actor/result拒否 | old/current epoch、token、audit | stale dispatch/Result/Outbox claim受理 | current epochでre-enroll/re-lease/reconcile |
| FI-DATA-010 | PITR point後に既に配送/実行済みのEvent/CommandをDBから再送 | consumer Receipt/Agent journal hit |同じReceipt/resultを回収 | stable ID、digest、attempt/journal evidence | webhook/Host side effect二重実行 | Outbox/Commandがoriginal outcomeへ収束 |
| FI-DATA-011 | restore後にDB_ONLY/BACKEND_ONLY/CONFLICTING/UNKNOWN resourceを混在提示 | full observation/classification mismatch | scope mutation停止、backend-only quarantine | DB/observed identity/generation/ownership evidence | 自動adopt/delete、反対mutation | matchedまたはexplicit Adoption/domain resolution |
| FI-DATA-012 | Derived Projection/current summary/検索indexを全削除 | projection health/missing generation | authority write継続可否をpolicy通り制御しprojection再構築 | source authority snapshot、rebuild generation | projectionを正本に逆同期、authority消失 | authorityから同じprojectionを再生成 |
| FI-DATA-013 | DR restore中に旧primary/Control Planeを隔離後再接続し、旧credentialからmutationを送信 | DR fencing/restore epoch/endpoint conflict | 復元側はREAD_ONLY維持または旧actor拒否、単一writerだけを有効化 | DR activation、old/current DB/credential generation、fencing proof | 両Site mutation、restore epochだけで安全宣言 | 旧writer/dispatch/credential fencingを外部証明しcurrent側だけでmutation成功 |
| FI-DATA-014 | current authorityが参照するhistory partitionをarchive manifest不完全のままdetach、またはlogical reference先を欠損させる | FK/Integrity Verifier/reference digest failure | affected scope mutation/GC停止、REFERENCE_UNKNOWN | reference class、target/digest、manifest、verifier result | dangling pointer、evidence推測、元partition drop | valid target/manifestへrepairしfull integrity scan成功 |
| FI-DATA-015 | 通常Service PrincipalからRecovery Control API/DB roleを使用、またはRecovery roleからCommand dispatchを試行 | identity/role/API/DR generation policy violation | request拒否、mutationなし、security alert | actor/role/scope/approval/audit | privilege escalation、通常resource/backend mutation | authorized recovery identity+approvalでrecovery evidenceだけcommit |
| FI-BUS-001 | internal messageをduplicate/reorder | delivery metadata、old generation | handler idempotency、DB authority確認 | work/event dedupe evidence | 二重Command/transition | 単一authority stateへ収束 |
| FI-BUS-002 | Bus停止後に復旧 | consumer/work age alarm | durable acceptance後のdispatch待機 | pending work age | DB authority loss、成功推測 | DBから未完work再駆動 |
| FI-GATEWAY-001 | Lease前にAgent Gateway partition | heartbeat/session loss | 新Lease停止、Host ineligible | gateway/Host alarm | Agent cached/autonomous mutation | session+capability再検証 |
| FI-GATEWAY-002 | Gateway再接続、Host authorityはdisarmed | session restored | authorityをdisarmedのまま維持 | authority generation/audit | 自動arm/Command配送 | operatorによる明示arm |
| FI-TRANSPORT-001 | ResultをLease expiry後まで遅延し、その間に新AttemptをLease | lease expiry、stale attempt | 旧Attempt UNKNOWN、新token | 2 Attempts、distinct token、stale conflict | 旧ResultによるJob進行 | current Attempt/evidenceだけで収束 |
| FI-TRANSPORT-002 | Resultをcommit後responseだけdrop | client retry | accepted digest完全一致のみ冪等receipt | 単一Result/Attempt completion | 新Attempt/異なるResult受理 | 同じreceipt返却 |
| FI-AGENT-001 | journal write直後、backend実行前にAgent kill | started journal record | 新Command実行停止、read-back | journal+UNKNOWN/未適用evidence | 無条件再実行 | 未適用証明後のnew Attempt |
| FI-AGENT-002 | backend実行後、journal完了前にAgent kill | started journal+backend state | capability unavailable | UNKNOWN、read-back evidence | rollback推測 | typed resolverで適用/未適用確定 |
| FI-HOST-001 | active VM Hostのpower/network loss | heartbeat/BMC/Agent loss | Host ineligible、source fencing要求 | Host failure、affected resources | shared diskの別Host二重attach | source fenced+resource eligibility再評価 |
| FI-HOST-002 | Host clockを閾値外へskew | clock health/lease anomaly | 新Lease停止 | clock alarm、Host state | wall clockのみでauthority判定 | clock正常化+capability/preflight |
| FI-HLC-001 | bootstrap responseをdropし、同一Hostがidentity bootstrapを再送 | bootstrap retry/identity correlation | 単一pending Host identityへ収束 | credential request digest、fingerprint、audit | credential/Host row二重発行、auto enrollment | 同一identityを回収しapproval待ちを維持 |
| FI-HLC-002 | 同一identity+異なるhardware、または同一hardware+異なるidentityを別Agentから提示 | identity/fingerprint conflict | 両sessionのmutation停止、conflict quarantine | conflicting evidence、session、audit | 自動merge、既存Host authority継承 | 明示的な管理者解決とcredential rotation |
| FI-HLC-003 | Baseline assignment更新とpreflight/final admissionを競合 | assignment generation mismatch | stale evaluation/claimを拒否 | old/current generation、decision evidence | 旧BaselineでREADY/claim commit | current generationで再評価 |
| FI-HLC-004 | evaluatorを停止またはevidenceを期限切れにする | evaluation failure/evidence expiry | status UNKNOWN、定義scopeの新規placement停止 | last good result、新failure/expiry evidence | UNKNOWNをCOMPLIANT/NON_COMPLIANTへ推測変換 | fresh evidenceでcurrent generationを再評価 |
| FI-HLC-005 | safe remediation適用後、Result responseをdrop | Command timeout/observation mismatch | Attempt UNKNOWN、反対/重複remediation停止 | Lease、journal、Attempt、read-back evidence | blind retry、authority bypass | typed observationで適用有無を確定 |
| FI-HLC-006 | canaryでfailure thresholdを超過させる | rollout health threshold breach | rollout pause、未対象Hostのassignment不変 | batch/result/threshold/decision audit | 全Host継続、旧result書換え、自動state rollback | authorized resume/abortと新assignment decision |
| FI-HLC-007 | maintenance/decommission中にControl Plane-Agent通信をpartition | session/Lease loss | authority disarm、進行中stepをUNKNOWN/blocked | lifecycle step、Lease、resource/evidence snapshot | drain未確認で次step、credential先行再利用 | reconnect後のcurrent authority/resource再検証 |
| FI-HLC-008 | disarmed Hostでreconnect、Gateway recovery、credential renewal、Baseline再assignmentを連続実行 | session/trust/assignment change | disarmed stateを維持、Command配送停止 | authority generation、events、audit | implicit arm/READY復帰 | 明示arm条件とcurrent preflight/complianceを満たす |
| FI-HLC-009 | MAC/hostname/SMBIOS等の一部identity evidenceを複製・変更し、他sourceと矛盾させる | evidence conflict/confidence policy failure | policy-auto enrollment停止、candidate quarantine | source provenance、digest、conflict、decision generation | 単一一致値で既存Hostへmerge/authority継承 | independent evidenceと明示approvalで解決 |
| FI-HLC-010 | 新Evaluatorが同じevidence corpusへ旧版と異なる判定を返しcanary thresholdを超過 | shadow/canary result delta | Evaluator rollout pause、未対象Assignment不変 | artifact/input digest、old/new result、threshold decision | 全Host切替、旧Result書換え | authorized fix/new revisionまたは明示rollout decision |
| FI-HLC-011 | External remediation callbackを偽装・replay・expiry後に送信 | identity/binding/nonce/expiry failure | claim拒否、current request不変 | rejected digest、source、reason、audit | Compliance/maintenance/authority進行 | current authenticated requestに対するfresh response |
| FI-HLC-012 | 外部systemが成功claimを返すがHost observationは未変更または取得不能 | claim/observation mismatch | Compliance NON_COMPLIANT/UNKNOWN、placement block維持 | external claim、fresh/failed observation、Evaluator result | COMPLIANT/READY/arm/maintenance exit | KIM-trusted observationとcurrent Evaluatorが一致 |
| FI-HGR-001 | selector/CMDB sourceを停止しmembership evidenceを期限切れにする | source health/freshness expiry | affected dynamic membership UNKNOWN、新規scope利用停止 | last materialization、source/evidence generation、alarm | last resultの無期限current化、推測remove/add | trusted source復旧とnew materialization |
| FI-HGR-002 | EXACTLY_ONE dimensionを欠損、またはexclusive Group二重所属にする | cardinality conflict | affected Host/scopeをineligible/BLOCKED | conflicting memberships、dimension policy、audit | 任意Group選択、scoreでの救済 | 一意なauthorized membership generation |
| FI-HGR-003 | hierarchy更新へcycleを入れる、またはgraph write途中でtransactionを失敗させる | cycle/transaction failure | new graph全体をrollback、old committed graph維持 | proposed digest、validation/error、old generation | partial hierarchy公開 | valid graphの一generation commit |
| FI-HGR-004 | dry selection後、final admission前にHostをPlacement Poolから除外する | membership generation mismatch | final admission rollback、残候補reselection | dry/current generation、conflict reason | stale Host claim、部分reservation | current snapshotで再評価・commit |
| FI-HGR-005 | rollout/maintenance開始後にGroupへHostを加入・離脱させる | live membershipとsnapshot digest差 | active scope不変、policyによりpause/skip/action-required | snapshot、new membership、decision audit | 加入Hostへの自動適用/maintenance、離脱履歴削除 | new snapshot/new generationまたは元scope完了 |
| FI-HGR-006 | 同priorityで異なるBaseline bindingを二Groupから適用する | assignment conflict | effective assignment未発行、Host BLOCKED | bindings、priority、group generations、reason | last-wins、任意Baseline適用 | authorized priority/binding conflict解消 |
| FI-HGR-007 | active Placement/rollout/snapshot/policy referenceを持つGroupをretire/deleteする | active reference guard | DRAINING/RETIRED維持、delete拒否 | reference set、audit、lifecycle state | reference orphan、workload移動/削除 | reference終了/移行後のauthorized delete |
| FI-HGR-008 | maintenance wave中にfailure-domain membershipを変更し同domain concurrency上限を超える | domain generation/concurrency mismatch | new maintenance authority停止、wave pause | snapshot/current domain、active maintenance、capacity | 追加Host drain/reboot | domain再評価とauthorized new wave/snapshot |
| FI-AVR-001 | 同priorityのPlacement Poolが異なるAvailability PolicyをHostへ適用 | policy resolution conflict | Hostをplacement ineligible | pool/membership/binding generations、reason | score/DB順でPolicy選択 | binding/priorityを一意に解消 |
| FI-AVR-002 | VM admission後にPool Availability PolicyをWORKLOADからINFRAへ変更 | current Pool/VM Binding revision差 | 既存VMは旧Binding維持、新規だけ新Policy | old/new Policy、Binding、audit | 既存VMの自動責任変更/restart | authorized Availability Rebind新revision |
| FI-AVR-003 | Host heartbeat/Agentを失わせBMC/storage fencing evidenceを不明にする | failure suspected/confirmed、fence timeout | FENCE_UNKNOWN/BLOCKED、自動restart停止 | failure epoch、required/missing proof | heartbeatだけでFENCED、別Host起動 | trusted fencing proofまたはoperator escalation |
| FI-AVR-004 | WORKLOAD_MANAGED VMのHost failureをconfirm/fence | bound responsibility decision | Fault/Event、VM unavailable/unknown | Binding、failure epoch、event outbox | Recovery Job/Command、replacement作成 | workload orchestratorの明示requestまたはservice recovery |
| FI-AVR-005 | MANUAL VMのHost failure後、Decisionなしでworkerを再駆動 | ACTION_REQUIRED/no decision | mutation dispatch停止 | Binding、failure epoch、decision absence | automatic restart/evacuate | authorized Manual Recovery Decision |
| FI-AVR-006 | INFRA recoveryのdestination起動後にResult responseをdropしold failure epoch Resultを遅延 | Attempt timeout/stale epoch | current outcome UNKNOWN、old Result拒否 | Recovery Operation、Lease/Attempt、observations | duplicate destination VM/反対cleanup | typed read-backで一意なruntime/attachmentを確認 |
| FI-AVR-007 | source Volume attachment/single-writer fencingをUNKNOWNにする | attachment ownership unknown | recovery placement/attach停止 | backend/source/destination evidence | 別Hostattach、source detach推測 | single-writer ownership/fencing証明 |
| FI-AVR-008 | recovery候補Hostを異responsibility Policyまたはfailure-domain違反Poolへ変更 | policy/domain incompatibility | candidate ineligible、silent fallback禁止 | bound/current Policy、domain path、reason | 責任変更、constraint無視 | compatible current candidateへfinal admission |
| FI-AVR-009 | EVACUATE中に一VMをcapacity不足、一VMをUNKNOWN、一VMを成功させる | per-VM outcomes | plan partial/blocked、成功VM維持 | VM Operations/Attempts/reasons | 全体rollback、失敗VM成功扱い | 各VMがverified/blocked/escalated terminalへ収束 |
| FI-AVR-010 | WORKLOAD_MANAGED Fault/Event sinkを停止する | outbox age/delivery failure | durable retry、responsibility維持 | event/outbox/Policy correlation | INFRA recovery fallback、event loss | sink復旧後同一eventを再送 |
| FI-WRI-001 | active/standby memberを同時に同rack/feed候補へfinal admission | Domain Claim row conflict | 一方commit、他方rollback/reselection | constraint/member/claim generations | 両方same-domain commit | distinct candidateまたはbounded insufficient-domain failure |
| FI-WRI-002 | candidate Hostのrackまたはpower evidenceを欠損/staleにする | domain evidence UNKNOWN | candidate ineligible、distinct countへ含めない | hierarchy/evidence generation、reason | unknownを新domain扱い | trusted current domain evidence |
| FI-WRI-003 | distinct domain不足時にsoft score候補だけを提示 | hard constraint unsatisfied | Placement failure、claimなし | required/available domain summary | hard-to-soft fallback、same-domain commit | domain capacity追加またはexplicit constraint revision |
| FI-WRI-004 | old member VM/Volume ownershipをUNKNOWNのまま同slotへreplacement bind | slot/source ownership conflict | replacement admission停止 | member/VM/attachment evidence | slot再利用、二重VM/attach | old ownership terminal/fenced証明 |
| FI-WRI-005 | Hostのrack/power membershipを変更し既存member separationを破る | current domain vs claim mismatch | VIOLATED/UNKNOWN event、既存VM維持 | old claim/current hierarchy/generation | 暗黙migration/restart/claim rewrite | explicit migration/replacement/constraint revision |
| FI-WRI-006 | Northbound create commit後responseをdropし同correlationを再送 | idempotency hit | 同じGroup/member/Operation返却 | request digest、Project、audit | duplicate Group/member slot | original resource回収 |
| FI-WRI-007 | active member/domain claimを持つResilience Groupをdelete | active reference guard | DRAINING/delete拒否 | member/claim/reference set | orphan claim/VM mutation | members/claims解放後authorized delete |
| FI-WRI-008 | mapperをstale constraint schemaで応答させる | contract/version mismatch | request拒否、Core state不変 | adapter/input/output version、audit | unknown field受理、partial group | supported contractで再送 |
| FI-WRI-009 | required memberの一部を未作成のままcompletion deadlineを超過 | member completeness/deadline | PENDINGからVIOLATED/action-required、既存member維持 | required/bound slots、deadline、event | 最初のmember placement拒否、既存member削除 | missing member bindまたはexplicit intent revision |
| FI-RCV-001 | 100 Recovery Entryを複数workerで同時取得 | budget concurrency/rate contention | configured slots/rateだけlease | budget generation/tokens/timestamps | limit超過dispatch | lease release/window refill |
| FI-RCV-002 | 同じbudget slotを二workerがtransaction競合取得 | row/token conflict | 一workerだけBudget Lease commit | owner/token/generation/audit | double consumption | loserが別eligible Entryを選択 |
| FI-RCV-003 | Budget Lease取得後dispatch commit前後でworkerをkill | lease/Operation outcome uncertainty | DB stateで未dispatchを再queue、dispatch済みはread-back | queue/lease/Operation correlation | duplicate Recovery Operation | idempotent original recoveryへ収束 |
| FI-RCV-004 | planning完了後、applicable Pool budgetは空き、destination backend dispatch budgetは飽和 | dispatch multi-scope budget unavailable | Entry WAITING、planning結果保持、部分dispatch leaseなし | phase/all scopes/usage/reason | planning budgetでdispatch、一部budgetだけ保持 | 全dispatch scopeを一transactionで取得 |
| FI-RCV-005 | 一Projectが大量high-volume Entryを投入し別Projectを待機 | fair-share/aging evaluation | per-project capとagingでbounded progress | queue rank/share/wait age | starvation、任意priority abuse | 全eligible Projectがpolicy内progress |
| FI-RCV-006 | storage backendをdegraded後に復旧 | health gate/circuit state |該当Entry pause、他backend継続 | health generation/circuit/event | busy retry、復旧だけで即dispatch | fencing/evidence/Placement full revalidation |
| FI-RCV-007 | 同一Host failure signalをduplicateしrack correlationを後着させる | epoch dedupe/correlation update | VMごと単一Entry、shared domain budget適用 | signal digest/epoch/correlation generation | duplicate Entries、budget bypass |一つのcurrent correlated planへ収束 |
| FI-RCV-008 | Budget Policyを低limitへ変更中にOperation実行 | policy generation change | started維持、new leaseはnew policy | old/new policy、active Operations | running cancel/reclassify、旧policy新lease | started terminal化とnew generation scheduling |
| FI-RCV-009 | active leases/queue中にDB failoverとworker restart | DB term/worker loss | old owner fence、committed lease/queue復元 | DB term、tokens、queue rank | slot loss/二重消費/ordering reset | current DB authorityでbounded dispatch再開 |
| FI-RCV-010 | queue age thresholdを超過させcapacityを不足のまま維持 | age/escalation threshold | ESCALATED/action-required、成功失敗は未確定 | age/capacity attempts/events | queue drop、FAILED/RECOVERED推測 | capacity/policy/operator decisionで再評価 |
| FI-RCV-011 | dispatch commit直後にBudget Leaseをexpireしworkerを停止 | lease expired + active Operation/Consumption | Consumptionをactive計上、slot再利用停止 | Operation/Consumption/Lease/Attempt correlation | concurrency slot二重利用、duplicate dispatch | Operationがverified terminal後にConsumption release |
| FI-RCV-012 | 二workerへ同じ複数scopeを逆入力順で与え、row lock競合/deadlock/serialization failureを注入 | DB lock/serialization error | canonical順取得または全rollback後bounded retry | scope key/order、transaction ID、SQL state、retry audit | 部分Lease保持、別順retry、limit bypass | current scope/generationから全scope再取得 |
| FI-RCV-013 | Host epochごとにQueue作成後、rack/powerの同一事故evidenceを遅延到着させCampaignをmerge | Campaign generation/claim uniqueness conflict | 未dispatchEntryをcanonical claimへ統合し、started Operationは追加dispatchをfenceしてreconcile | member epochs、evidence、old/current Campaign、Claims/Operations/Consumptions | VM重複restart、二重Budget計上、履歴改変 | 一つのcurrent Campaign decisionとverified Operationへ収束 |
| FI-LIBVIRT-001 | libvirt mutation後にtimeoutを返す | backend timeout | Attempt UNKNOWN、read-back | Command/Attempt/evidence | 即時反対mutation | Domain UUID/stateで解決 |
| FI-LIBVIRT-002 | libvirt daemon restart中にCommand | connection/event gap | Host capability一時停止 | Agent health、Attempt result | success推測 | reconnect+full resync+verification |
| FI-NET-001 | OVN transaction conflictと未知objectを注入 | conflict/drift | affected network新規binding停止 | intent generation、unknown object evidence | 未知object/物理network削除 | KIM所有intentのみ再適用しdataplane確認 |
| FI-NET-002 | ovn-controller lagでDB intentとdataplaneを乖離 | binding/dataplane lag | Portをprovisioning/degradedに維持 | intent+observed generations | Port ready誤表示 | chassis/dataplane verification |
| FI-NET-003 | 同一Subnet/Networkへ同じIP/MACを複数workerが同時claim | unique/allocation conflict | 一Claimだけcommit、他方rollback | pool/identity/Port generations、transactions | duplicate IP/MAC、partial Port | winning Portまたはrelease後に再評価 |
| FI-NET-004 | Port/OVN delete適用後responseをdropしstale SB/Host bindingを残す | deletion/binding outcome unknown | Identity Claim RELEASE_PENDING/QUARANTINED維持 | Port/intent/binding/absence observations | IP/MAC即時再利用、反対create | 全layer absence+reuse policy完了 |
| FI-NET-005 | 同一physnet VLANまたはoverlay VNIを並行allocateし、delete中segmentを再要求 | Segment Claim conflict/reference guard | 一Claimだけcommit、delete中ID再利用停止 | pool/network/segment generations/references | duplicate VLAN/VNI、early reuse | old reference/dataplane absence後にfree |
| FI-NET-006 | OVN NB transaction commit後responseをdrop | apply outcome unknown | same intent ID/generation/digest read-back | KIM intent、NB external IDs/digest | opposite create/delete、duplicate objects | matching NB object setと後続realization確認 |
| FI-NET-007 | NB object適用後にSB/controller/Host programmingを停止 | layered convergence gap | Port PROVISIONING/DEGRADED/UNKNOWN、ACTIVE禁止 | NB/SB/chassis/Host generations | NB successだけでACTIVE | required SB/Host/dataplane verification |
| FI-NET-008 | migrationでsource/destination Bindingを並行active化しworkerをkill | Binding Claim/handoff conflict | 一logical binding authority、両側read-back | Handoff/Lease、old/new Binding、Host evidence | 二通常active Binding、両側cleanup | verified source/destination handoff state |
| FI-NET-009 | recovery先Binding後にold Host/Agent/OVN Resultを遅延 | old binding/authority generation | stale result拒否、new Bindingだけcurrent | Recovery/Port/Handoff correlation、old/new generations | old Binding復活、identity release | destination binding/dataplane verification |
| FI-NET-010 | DHCP intent適用中にservice/controllerを停止しguest leaseを欠損 | DHCP/runtime observation gap | IP Claim維持、delivery DEGRADED | DHCP options、IP claim、lease observation | IP再割当、ACTIVE誤表示 | desired configとdelivery observation収束 |
| FI-NET-011 | Floating IP/NAT apply後responseをdropしGateway healthをUNKNOWN化 | NAT/Gateway outcome unknown | Claim/Binding active-unknown、再利用/反対NAT停止 | FIP/NAT/Gateway/OVN generations | duplicate NAT/FIP、blind delete | NB/SB/gateway/dataplane read-back一致 |
| FI-NET-012 | Gateway failover中にold chassis/sessionを復帰させstale NAT Resultを送信 | gateway authority generation conflict | old authority/result拒否、一current Binding | old/new gateway/NAT generations、fencing evidence | 二active gateway authority | new gateway/chassis/dataplane verification |
| FI-NET-013 | Security Policy NB apply成功後SB/Host ACL programmingを失敗 | policy realization gap | new exposure/Port ACTIVE停止、default deny維持 | policy/PortGroup/NB/SB/Host generations | default allow、policy ready誤表示 | matching ACL/dataplane observation |
| FI-NET-014 | Host/provider/gateway MTUを要求未満またはUNKNOWNに変更 | path capability generation mismatch | candidate/binding ineligible、existing DEGRADED | requested/effective MTU、path observations | silent fragmentation/tunnel fallback | current path capabilityがrequirementを満たす |
| FI-NET-015 | active Port/Router/NAT/Security/UNKNOWN referenceを持つNetwork/Subnetをdelete | dependency/reference guard | delete拒否、OVN/claims維持 | dependency graph、states、audit | orphan Port/identity/segment、backend cleanup | dependencies解消+typed absence verification |
| FI-NET-016 | backend-only/foreign OVN object、unknown chassis/interfaceを提示 | ownership/marker/generation mismatch | quarantine、affected scope mutation停止 | raw/normalized IDs、provenance、observations | auto adopt/delete/unbind | explicit Adoption/repairまたはexternal ownership確定 |
| FI-NET-017 | unauthorized actorがSegment Pool/Gateway/force unbind/delete/Adoptionを要求しadapterがcredential/raw topologyをerrorへ返す | permission/redaction/conformance failure | request拒否、adapter quarantine/security alert | actor/action/payload digest/audit | backend mutation、credential/topology leak | authorized actionまたはpatched certified adapter |
| FI-NET-018 | dry/final間にprovider mapping/physnet/SR-IOV/DPDK capability generationを変更 | final admission generation conflict | transaction rollback/reselection | snapshot/current mapping/device generations | unreachable Port、binding type fallback、partial claims | current compatible Host/mappingで全claim commit |
| FI-DPDK-001 | active PortのPMD threadを停止/消失させる | PMD/runtime observation | affected Port/Hostへの新規dataplane placement停止 | runtime/Port alarm、generation | ready継続、silent fallback | PMD復旧+RxQ polling verification |
| FI-DPDK-002 | RxQをunpolledまたは不正PMD coreへdriftさせる | RxQ/PMD assignment mismatch | bindingをdegraded/blocked | desired/observed mapping evidence | compliant/ready誤表示 | policy準拠mappingをobservationで確認 |
| FI-DPDK-003 | ovs-vswitchd restart適用後にResult responseをdrop | Command timeout/runtime gap | Attempt UNKNOWN、新規disruptive op停止 | journal、runtime generation、Port evidence | blind restart/rollback | full runtime/PMD/Port/RxQ observation |
| FI-DPDK-004 | DPDK socket memory/HugePage不足でruntime起動失敗 | runtime init/hugepage shortage | Host dataplane ineligible | desired/observed memory、bounded reason | restart loop、workload pages横取り | capacity修正+明示maintenance operation |
| FI-DPDK-005 | PCI driver bind/rebind後にAgentを停止 | device ownership outcome unknown | device/VF/IOMMU group quarantine | journal、driver/IOMMU/OVS observation | VM/OVSへのblind再割当 | exclusive ownershipをread-backで証明 |
| FI-DPDK-006 | PMD/Portを異NUMAへ移動させる | locality drift | policyによりdegraded/non-compliant | NUMA mapping、performance alarm | automatic cross-NUMA受容 | policy準拠配置または明示例外 |
| FI-STORAGE-001 | Volume attach適用後response timeout | attachment timeout | attachment generation block | Attempt UNKNOWN、backend/Host evidence | detach/別Host attach | single-writerとattachment state確定 |
| FI-STORAGE-002 | Ceph unavailable中にVolume operation | backend health/error | 対象backend mutation停止 | backend alarm、Operation待機/失敗 | local/silent backend fallback | backend復旧+capability+read-back |
| FI-STORAGE-003 | 同じSINGLE_WRITER Volumeへ二VM/workerから同時attach | active Claim unique conflict | 一Claimだけcommit、他方rollback/BLOCKED | Volume/Attachment generation、claim transaction | 二active writer Claim、部分quota | winning Claim verificationまたはrelease後に再評価 |
| FI-STORAGE-004 | libvirt detach適用後にresponseをdropしCeph watcher/lockを遅延表示 | detach outcome uncertainty | Claim active/UNKNOWN維持、別Host attach停止 | Command/Attempt、device/client/lock observations | Claim早期release、force cleanup | source I/O/device/client releaseをfresh evidenceで確認 |
| FI-STORAGE-005 | watcher/lock absenceのstale snapshotとsource device presenceを競合提示 | evidence generation/freshness conflict | FENCE_REQUIRED/UNKNOWN、fresh resolver | source/digests/generations/conflict | absenceだけでownership譲渡 | current device/client/lock evidenceが一致 |
| FI-STORAGE-006 | Host compute fence成功、Ceph client fencing失敗/UNKNOWN | storage fence incomplete | replacement Claim/attach停止 | compute proof、client fence error、old generation | 別Host write attach | storage client fencingとabsence verification成功 |
| FI-STORAGE-007 | Ceph client fence成功、Host/BMC fencing失敗/UNKNOWN | compute fence incomplete | replacement Claim/attach停止 | client proof、compute fence state | heartbeat lossだけでrestart/attach | source compute fencing proof成功 |
| FI-STORAGE-008 | recovery先attach後にold Attachment Result/observationを遅延 | old generation/token | stale evidence拒否、new Claimのみcurrent | old/new generation、Recovery Operation、proofs | old Claim復活、二重writer | destination DB/device/backend evidence一致 |
| FI-STORAGE-009 | Local LVM VolumeのHostをlossし同名VG/LV候補を別Hostに提示 | Host/VG/LV UUID/locality mismatch | restart-on-other-host ineligible、Volume UNKNOWN/UNAVAILABLE | original/candidate identities、Host failure | 空/同名LV作成・自動adopt | source復旧またはexplicit certified copy/restore workflow |
| FI-STORAGE-010 | Ceph shared Volume recovery時にdestination pool access/capabilityを欠損 | destination eligibility failure | Recovery BLOCKED、Claim未active | backend/class/binding/capability generations |別backend/local fallback、partial attach | current access/capability+全fencing+final admission |
| FI-STORAGE-011 | live handoffのswitchover前後でworker/libvirt connectionをkill | handoff/operation outcome unknown | 一logical writer authority維持、両側read-back | handoff Lease/state、source/destination QEMU/client evidence | 二active Claim、両側推測detach | verified source/destination stateへhandoff収束 |
| FI-STORAGE-012 | active clone child/Attachment/UNKNOWN Operationを持つVolume/Snapshotをdelete | dependency/reference guard | delete拒否、backend untouched | dependency graph、state、audit | parent/image削除、DB tombstone先行 | dependency解消とtyped absence verification |
| FI-STORAGE-013 | backend expand成功後にguest/device resizeを失敗/timeout | desired/backend/guest generation mismatch | Volume DEGRADED/UNKNOWN、縮小rollback禁止 | sizes/generations/Attempts/observations | guest-ready誤表示、自動shrink | device/guest verificationまたはaction-required |
| FI-STORAGE-014 | backend-only RBD image/LV、unknown watcher、unmatched libvirt deviceを検出 | identity/ownership mismatch | quarantine、affected scope mutation停止 | stable IDs、provenance、observations | auto adopt/delete/detach | explicit authorized Adoption/repairまたは外部所有確定 |
| FI-STORAGE-015 | unauthorized actorがforce detach/client fence/lock break/deleteを要求 | permission/approval failure | request拒否、no Command、security audit | actor/action/target/decision | destructive backend side effect | authorized scoped approvalとpost-verification |
| FI-STORAGE-016 | Storage adapterがsecret/raw device pathをerror/Eventへ返し、またはside effect後timeoutをFAILED化 | conformance/redaction/UNKNOWN violation | adapter quarantine、affected new mutations停止 | payload digest、manifest/version、test evidence | secret leak、blind retry/silent fallback | patched certified adapterとread-back reconciliation |
| FI-STORAGE-017 | concurrent Volume create中にCeph/LVM observed freeをstale化しthin metadata圧迫/外部使用量を注入 | ledger conflict、health/freshness threshold | claim上限内だけcommit、stale/pressure scope ineligible | capacity generations、claims、backend observations | over-allocation、UNKNOWN freeの楽観利用、delete中capacity再利用 | fresh healthy capacityとbackend absence後にclaim/release再評価 |
| FI-UPG-001 | Manifest versionと実artifact digest/provenanceを不一致にする | artifact/manifest mismatch | target quarantine、campaign開始/継続停止 | manifest/artifact/evaluator digests、decision | version文字列だけでready | verified artifactへ置換または新Manifest |
| FI-UPG-002 | N/N-1混在中にN-2またはunmanaged replica/Agentを接続する | unsupported compatibility edge | serving/dispatch poolから除外 | component/session/schema ranges、bounded reason | N-2 writer/Command処理 | supported revisionへ更新し再評価 |
| FI-UPG-003 | old writerが理解できないenum/authority fieldをFeature Gate前にnew writerから送る | mixed writer contract violation | write/feature activation拒否、campaign pause | writer/schema/feature generations、payload digest | old readerの誤解釈、部分authority進行 | 全required writer対応後にgate switch |
| FI-UPG-004 | rollback window中にschema contractまたはold event decoder/artifact GCを要求する | retained participant/reference guard | finalization拒否 | active versions、decoder/archive refs、approval | rollback path喪失、event replay不能 | deadline+old participant不在+明示承認 |
| FI-UPG-005 | canaryでreadiness/error/invariant thresholdを超過させる | canary/failure threshold | later wave停止、serving old replica維持 | target Attempts、health、threshold decision | batch継続、quorum喪失 | approved rollbackまたはforward fix再canary |
| FI-UPG-006 | old Agentへ未知Command schemaをdispatchしGateway responseをdropする | negotiation/dispatch incompatibility | Command未lease、session/target block | protocol/command ranges、Lease absence、audit | down-convert、blind retry、別operation fallback | compatible Agent/moduleとfresh session |
| FI-UPG-007 | Agent artifact activation後response loss/reconnectを注入する | update outcome UNKNOWN | Host drained/unarmed、artifact/service/journal read-back | old/new digest、local receipt、session generation | reconnectだけでarm、再install | current artifact+preflight+Compliance確認 |
| FI-UPG-008 | old/new adapterを同じexternal object writerとして同時activeにする | ownership/Lease conflict | one writerだけactive、他方drain/quarantine | adapter digests、ownership token、Operation | duplicate backend mutation | old writer absence+new ownership verification |
| FI-UPG-009 | rollout中にHost/backend capability/versionを非互換またはstaleへ変更 | compatibility drift | scope別Placement/Recovery/dispatch停止 | observed generations、support decision | incompatible destination使用、既存resource暗黙mutation | fresh compatible evidenceまたは明示exception |
| FI-UPG-010 | wave/target適用中にupgrade coordinatorをkill/failover | coordinator Lease loss | new ownerがDB/receipt/artifact read-back | Campaign/Lease/Attempt/observation | in-memory stepから二重適用 | current stateへidempotent resume |
| FI-UPG-011 | rollback artifact activation後response loss、またはdestructive contract後rollback要求 | rollback outcome/boundary violation | target UNKNOWN/BLOCKED、blind reverse/PITR禁止 | boundary、Attempts、artifact/schema observations | repeated reinstall、DB restore、履歴改変 | read-back後forward repairまたは許可済みrollback |
| FI-UPG-012 | Event replayへ旧decoder未対応payloadまたは削除済みschemaを注入 | consumer/schema incompatibility | delivery隔離、domain authority不変 | event schema/digest、consumer range、Receipt | payload再生成、Event drop、責任変更 | compatible decoder/consumerで再送 |
| FI-UPG-013 | offline bundleからartifact/SBOM/migration/manifestの一部を欠損・改ざんする | bundle completeness/integrity failure | install/upgrade開始拒否 | bundle manifest/checksum/signature/audit | bypass install、部分stage | complete verified bundleを再投入 |
| FI-UPG-014 | unauthorized actorがschema contract/feature activation/rollback/overrideを要求 | permission/approval failure | transition拒否、campaign state不変 | actor/action/approval/decision audit | destructive step、権限昇格 | authorized scoped approval |
| FI-UPG-015 | Control Plane wave中にreplica loss/DB failoverとmax unavailable超過を注入 | HA/readiness budget violation | rollout pause、remaining serving replica維持 | replica/version/schema/quorum/wave evidence | quorum喪失、全replica同時停止 | HA回復+current compatibility再評価 |
| FI-UPG-016 | QEMU/libvirt upgrade後に既存VMのmachine type/CPU modelをnew defaultへ書換える候補を提示 | runtime binding/default conflict | implicit update拒否、existing VM維持 | old/current binding、support decision、audit | VM ABI変更、再起動時boot failure | explicit compatibility-checked migration/rebuild |
| FI-UPG-017 | archive/legal hold中Eventのdecoder artifactをfinalization/GC対象化 | retention/reference guard | decoder GC拒否、campaign finalization pause | payload/schema/decoder/retention refs | replay不能、payload再生成 | retention/hold/reference解消後のnew snapshot |
| FI-UPG-018 | Feature Gate graphへcycle/conflictまたは未active dependencyを注入 | DAG publish/activation validation | publish/activation拒否、current gates不変 | graph revision、cycle/path、decision | out-of-order semantics、partial rollback | acyclic compatible graphをnew revisionでpublish |
| FI-TIME-001 | timestamp順とresource/Attempt generation順を逆転させる | causal/generation mismatch | current generationだけauthorityへ反映 | timestamps、generations、tokens、audit | newest timestamp採用、stale result進行 | current sequence/generationで収束 |
| FI-TIME-002 | DB primary clockをbackward/forward stepさせfailoverを重ねる | clock continuity/authority generation anomaly | new Lease/renewal/GC/finalization停止、old Lease fence | DB term、clock samples、Lease generations | Lease延長/復活、mass expiry/GC | healthy clock+new generation+current re-evaluation |
| FI-TIME-003 | Command side effect開始後にLeaseをexpireさせResultを遅延 | expiry + execution uncertainty | Attempt UNKNOWN、opposite/retry停止 | journal、Lease/token、Attempt、backend observation | 未実行扱い、blind retry/rollback | typed read-backまたはaccepted receipt |
| FI-TIME-004 | Gateway responseをLease remaining time以上遅延してAgentへ配送 | transport/deadline uncertainty | Agent start拒否、journal evidence | request send/receive monotonic、server sample、margin | receive time+TTLで期限外実行 | fresh exchange/new Lease |
| FI-TIME-005 | Agent-Gateway RTT/uncertaintyをpolicy上限超過へ揺らす | time uncertainty threshold | time-sensitive Command/Host scope block | RTT samples、uncertainty、Clock Health | unsafe local deadline、silent margin縮小 | bounded exchangeとcurrent Clock Health |
| FI-TIME-006 | cached Command受信後にAgent process/Hostをrebootしwall clockを元へ戻す | boot ID/monotonic discontinuity | cached/unstarted Command破棄、full resync | old/new boot/session/Lease、journal | pre-reboot deadline再利用 | new session/current Lease/Command |
| FI-TIME-007 | Agent/backend observed_atを未来または大幅過去へ改ざん | source/received timestamp conflict | freshness延長禁止、evidence DEGRADED/UNKNOWN | source/received/verified、clock quality、digest | future evidenceでCOMPLIANT/eligible | trusted fresh observation |
| FI-TIME-008 | Control Plane clockをcertificate/token境界外またはUNKNOWNへする | trust time uncertainty | new privileged auth/rotation/Command fail closed | clock/trust generation、token interval、audit | expiry bypass、clock-only authority | verified clock+current trust/role/Lease |
| FI-TIME-009 | DST gap/overlap、timezone変更、maintenance window飛越しを注入 | calendar materialization ambiguity | ambiguous schedule拒否、destructive catch-up禁止 | timezone/policy/UTC interval、decision | 二重実行、missed step即時実行 | explicit policy/new approved window |
| FI-TIME-010 | Recovery queue/rate window中にDB clockをforward/backward step | durable window/token anomaly | scheduling pause、credit/age再評価 | policy/window/Consumption/clock generation | token二重補充、全Entry即時expire | current DB time/generationからwindow再構築 |
| FI-TIME-011 | retention horizon直前にDB clockをforward stepし大量partitionをeligible化 | GC clock/safety guard | Candidate/GC Lease拒否、mass delete停止 | clock health、snapshot、refs/holds/backups | evidence/decoder/tombstone大量削除 | stable horizonでbounded new snapshot |
| FI-TIME-012 | 異clock sourceのHost failure eventを同じtimestampへ揃える | correlation uncertainty/topology mismatch | automatic campaign merge/dispatch停止 | source/received intervals、uncertainty、topology | timestamp-only merge、double/incorrect recovery | independent evidence+current campaign decision |
| FI-TIME-013 | Host clock正常化だけを通知し旧session/Lease/credentialを再送 | stale authority generation | rearm/renew/result拒否 | clock/Host/session/Lease generations | clock recoveryだけでarm | enrollment/preflight/Compliance+new authority |
| FI-TIME-014 | credential expiry境界で認証responseをdropし同tokenをreplay | expiry/replay/receipt uncertainty | nonce/session/idempotencyでduplicate拒否/同Receipt | token interval、nonce、session、audit | expiryだけに依存した再受付 | current credential/new request binding |
| FI-TIME-015 | PITRで未失効に見えるLease/token/maintenance scheduleを復元 | restore epoch/time travel | pre-restore timer/session/Lease fence | restore/DB/Lease/schedule generations | restored token再利用、catch-up mutation | DR clock healthy+current epoch reissue |
| FI-TIME-016 | DB/Control Plane/Host time sourceを同時喪失させclock evidenceをstale化 | source unavailable/freshness expiry | affected auth/dispatch/GC block、existing VM維持 | scope health、last evidence、alarms | fail-open mutation、既存VM停止 | independent source recovery+new Clock Decision |
| FI-TIME-017 | DB clockと独立sourceを乖離させ、単一sourceだけを正常値としてspoofする | ClockReferenceSet diversity/conflict | DB clock UNTRUSTED/UNKNOWN、新Lease/GC/finalization停止 | source identities、offset/uncertainty、provenance | self/single-source HEALTHY、authority継続 | independent quorum/reference一致+new decision |
| FI-TIME-018 | PTP grandmaster change/holdover lossと高精度telemetry timestamp jumpを注入 | Precision Time Domain health drift | affected NFV capability/placement block、KIM Lease維持 | PTP domain/GM/offset/holdover、DB clock evidence | PTPをauthority clock化、VM停止 | precision domain recovery+Compliance再評価 |
| FI-TIME-019 | leap secondと異なるsmear policy sourceを混在させる | time scale/policy conflict | uncertainty拡大、time-sensitive decision pause | source policies、leap/smear window、decisions | Lease延長、mass expiry、calendar二重実行 | compatible policy/reference setへ収束 |
| FI-PKI-001 | Root private keyまたはunconstrained Intermediateを通常Control Planeへ配置する構成を投入 | custody/profile policy violation | deployment/readiness拒否、security alert | key reference/profile/manifest digest | online Rootの日常利用、unbounded issuance | offline Root+constrained issuerへ是正 |
| FI-PKI-002 | wildcard/CN-only、wrong SAN/EKU、unknown issuer/algorithm certificateで接続 | profile/name/chain mismatch | TLS/application session拒否 | peer digest、profile、bounded reason | identity推測、別profile fallback | compliant certificateでnew session |
| FI-PKI-003 | adapter/Agentがprivate key/Secret Provider valueをDB/Event/Command/logへ返す | secret scanning/schema violation | write/redaction拒否、component quarantine | redacted digest、source、audit | secret persistence/disclosure | credential rotation+patched certified component |
| FI-PKI-004 | old/replayed TrustBundle revisionをcurrent generationへ適用 | sequence/generation rollback | publish/apply拒否、current trust維持 | old/current sequence/digest/approval | old anchor/profile再信頼 | higher valid revisionを適用 |
| FI-PKI-005 | TrustDecision後にrevocation/clock/Binding generationを変更しcached sessionを使用 | decision dependency stale | session/privileged mutation拒否 | old/current generations、peer/session | cached trust継続 | current revalidation/new session |
| FI-PKI-006 | used/expired/shared bootstrap tokenで別Host CSR/Enrollmentを要求 | nonce/use/scope/identity mismatch | bootstrap/issuance拒否、quarantine | token digest、Host evidence、use receipt | duplicate Host credential、auto-enroll | new scoped bootstrap+current approval |
| FI-PKI-007 | issuer commit後のissuance/renewal responseをdropしてretry | issuance outcome unknown/idempotency | same CSR/requestへsame Binding/receipt | request/CSR/key/cert digests | blind duplicate key/identity issuance | issuer/Binding read-backで収束 |
| FI-PKI-008 | overlap中old/new Agent certificateから二sessionでCommandを同時取得 | logical identity/authority conflict | one current Host/session generationだけlease | Binding/session/Host/Lease generations | dual Host authority、duplicate execution | new session verified+old drain/fence |
| FI-PKI-009 | revoke/TrustBundle change後にlong-lived TLS sessionを維持してResult/Command送信 | stale trust/session generation | stale traffic拒否、session terminate | peer fingerprint、trust/session generations | revocation bypass | current certificate/new session/resync |
| FI-PKI-010 | certificate expiry/session close直後にHost/backend side effect不在としてrecovery | trust loss + resource uncertainty | ownership UNKNOWN、typed observation/fencing | credential/session/Host/resource evidence | restart/attach without fencing | compute/storage/network proof後にpolicy decision |
| FI-PKI-011 | local deny後にCRL/OCSP/offline revocation distributionをdrop | propagation incomplete | LOCAL_ENFORCED/DISTRIBUTING維持、remove禁止 | sequence、receipts、target status | REVOKED完了誤表示、session許可 | required target propagation verification |
| FI-PKI-012 | revocation sourceをstale/UNKNOWNにしてnew privileged sessionを要求 | freshness unavailable | profile scope fail closed、existing VM維持 | source sequence/age、policy、decision | last-good無期限trust | fresh current revocation state |
| FI-PKI-013 | distrusted issuer certificateをalternate chain/old Bundle/cached sessionで提示 | distrust scope match | 全fallback拒否、scope quarantine | chains、Bundle generations、distrust reason | silent alternate trust | approved new issuer/profile |
| FI-PKI-014 | Host certificate compromise後revokeだけ成功しBMC/storage fencingを失敗 | trust contained/resource fence incomplete | recovery/reattach停止、ownership UNKNOWN | revoke/session/compute/storage evidence | revoke=Host fencedと推測 | required fencing+new enrollment |
| FI-PKI-015 | Control Plane certificateだけrotateしold DB/Bus/backend credential/Leaseを使用 | multi-credential containment gap | old workload/credential/Lease拒否、scope pause | identities、roles、tokens、authority generations | rotated TLSでincident完了 | individual fence/rotation+clean canary rejoin |
| FI-PKI-016 | compromised Intermediate自身が署名したnew Root/Bundleでemergency rollover | recovery authority not independent | rollover拒否、TRUST_RECOVERY維持 | old/new chains、approval sources、audit | attacker-controlled anchor adoption | independent out-of-band approval/new anchor |
| FI-PKI-017 | offline trust/revocation deltaをskip/reorder/replayしdefault secretでbootstrap | chain/sequence/bootstrap violation | bundle/bootstrap拒否 | sequence/previous digest/expiry/use receipt | trust rollback、TOFU/shared credential | valid full/delta chain+one-time material |
| FI-PKI-018 | PITR後に時間上有効なold Site certificate/session/Leaseを再送 | restore/trust generation mismatch | old identity/session/Lease拒否、recovery mode維持 | restore/trust/session generations、revocation age | old Site clone、stale mutation | external fencing+current trust reissue/session |
| FI-PKI-019 | Secret Providerがrotation成功claimを返すがpublic certificate/sessionは旧key | claim/observation mismatch | credential state UNKNOWN、switch/old revoke停止 | provider claim、public key/Binding/session evidence | unverified ACTIVE、old key早期revoke | public trust/session verification |
| FI-PKI-020 | unauthorized single operatorがRoot distrust/emergency anchor/force issuanceを要求 | permission/approval failure | transition拒否、TrustBundle不変 | actor/action/approvals/digest/audit | unilateral trust takeover | scoped two-person authorization |
| FI-SPLIT-001 | old leader/authority generationからLease/Result送信 | generation/token mismatch | stale actor拒否 | conflict audit、current generation | Job/Desired進行 | current authorityから再同期 |
| FI-IDENTITY-001 | JWKS/certificate revocation state unavailable | trust validation unavailable | privileged mutation fail closed | bounded auth error、audit | stale/unknown trustで新mutation | trust generation復旧 |
| FI-AUDIT-001 | durable audit outbox writeを失敗させる | audit unavailable | 管理mutation transaction rollback | failure metric、request correlation | 監査なしmutation | audit durability復旧後に再受付 |

## 4. Coverage Mapping

| Failure Model class | Injection IDs |
|---|---|
| Client | FI-CLIENT-001..002 |
| API / Control Plane | FI-CP-001..002 |
| Database / DR / Persistence | FI-DB-001..002, FI-DR-001, FI-DATA-001..015 |
| Internal Message | FI-BUS-001..002 |
| Agent Gateway / Transport | FI-GATEWAY-001..002, FI-TRANSPORT-001..002 |
| Agent | FI-AGENT-001..002 |
| Host / Lifecycle / Compliance | FI-HOST-001..002, FI-HLC-001..012 |
| Host Grouping / Failure Domain | FI-HGR-001..008 |
| Availability Responsibility / Managed Recovery | FI-AVR-001..010 |
| Workload Resilience Intent | FI-WRI-001..009 |
| Recovery Storm Control | FI-RCV-001..013 |
| libvirt / QEMU | FI-LIBVIRT-001..002 |
| Network / NFV Dataplane | FI-NET-001..018, FI-DPDK-001..006 |
| Storage | FI-STORAGE-001..017 |
| Upgrade / Compatibility | FI-UPG-001..018 |
| Time / Clock Semantics | FI-TIME-001..019 |
| PKI / Trust Lifecycle | FI-PKI-001..020 |
| Split-brain / Stale Authority | FI-SPLIT-001 |
| Identity / Audit | FI-IDENTITY-001, FI-AUDIT-001 |

## 5. Release Gate

- Developer Preview: Client、Execution、Agent、DB failoverのcritical pathsをImplemented。
- Technical Preview: 全15classで最低1 testをImplementedし、multi-node環境で証拠保存。
- Product Beta: network/storage partition、Host fencing、DR restoreを含む全matrixを自動または承認済みrunbookで実行。
- GA: release candidateごとにcritical subset、定期chaos campaignでfull setを実行。
