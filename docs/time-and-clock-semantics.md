# Time and Clock Semantics Architecture

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

KIMではLease、evidence freshness、retention、rollout deadline、maintenance window、certificate/token validity、queue aging、failure correlation等が時間へ依存します。本書は分散したclockを単一の正確な時計と仮定せず、時間をauthority、safety、ordering、diagnosticへ安全に利用する共通契約を定義します。

最重要原則は次です。

> 期限切れは、そのauthorityを今後使用できないことを意味する。期限切れだけでは、期限前に実世界のside effectが開始・完了しなかったことを証明しない。

この原則をCommand Leaseだけでなく、Budget/worker/publisher/GC/Upgrade Lease、credential、evidence、idempotency、maintenance、correlation等へ一般化します。

## 2. Non-responsibility

KIMが所有します。

- Control PlaneとHostのclock quality observation、time-dependent policy、safe gate
- PostgreSQL authorityへbindしたLease、deadline、freshness、retention decision
- Agent protocolでのconservative local deadline変換
- clock anomaly時のcontainment、fencing、read-back、audit
- timestamp provenance、uncertainty、causal/generation ordering

KIMが所有しません。

- NTP/PTP/GNSS applianceやupstream time sourceの構築・運用
- Host OSの汎用time configuration management
- guest OS/application/NFのclock synchronization
- external IdP、PKI、WIM、NFVO/VNFMが持つclock authority

Host baselineはrequired synchronization/uncertaintyを宣言し、KIMは観測・Compliance・placement/dispatch gateを行います。必要な修復はclosed typed remediationまたはexternal remediation boundaryを通します。

## 3. Clock Classes

| Clock / timestamp | 用途 | Authorityとして許可する範囲 | 禁止用途 |
|---|---|---|---|
| Wall Clock UTC | operator表示、calendar、certificate/token validity、監査時刻 | provenance/uncertaintyとpolicy付きの有効期間評価 | ordering、fencing、side effect有無の単独証明 |
| Database Authority Time | Control PlaneのLease、deadline、freshness ingest、retention snapshot | current DB authority generation内の共通comparison point | generation/tokenを伴わない単独authority |
| Process Monotonic Clock | timeout、backoff、local elapsed time | 一process/boot内のbounded wait | process/Host/bootを跨ぐorderingや永続expiry |
| Agent-local Monotonic Deadline | 受信Commandを開始できる保守的なlocal期限 | current session/Lease envelopeとclock uncertainty内 | DB Lease延長、別session/bootへの持越し |
| Observed Source Timestamp | Host/backend/eventが「いつ観測したか」 | diagnostic、bounded correlationの一入力 | freshness、ordering、ownershipの単独authority |
| Received / Committed Timestamp | KIMがいつ受信・commitしたか | freshness上限、retention/correlationの安全な基準 | external event発生順の断定 |

UTC timestampはRFC 3339形式と十分な精度で保存しますが、文字列表現の精度をclock accuracyとみなしません。すべての重要timestampはsource、clock identity/boot ID、received/committed time、uncertainty/quality、generationを関連付けます。

## 4. Ordering and Causality

KIMはtimestampだけで次を順序付けません。

- resource mutation: resource generation/revision、DB transaction、ETag
- execution: Operation/Job/Command/Attempt index、Lease token、authority generation
- delivery: Outbox/Event ID、Inbox/Receipt、source sequence
- observation: source identity、observation generation、inventory generation
- HA/DR: database authority generation、restore epoch、leader/fencing token

timestampが新しくてもstale generationはcurrent authorityを進めません。timestampが古くてもdurably acceptedされた同一Result/Receiptの再送はidempotentに応答できます。

causal relationはrequest/operation/trace/correlation ID、parent event、generationから表し、同じ時刻または近い時刻だけで因果を作りません。

## 5. Time Authority and Clock Health

`ClockObservation`はscopeごとに最低限次を持ちます。

```text
ClockObservation
├─ subject / source / collector
├─ wall_time / received_at
├─ estimated_offset / uncertainty
├─ synchronization state / source diversity
├─ monotonic continuity / boot_id
├─ step/slew/leap indicators
├─ observed generation / freshness
└─ evidence digest / provenance
```

`ClockHealthDecision`はversioned policyとcurrent evidenceへbindし、次を区別します。

- `HEALTHY`: 対象operationの許容offset/uncertainty/continuityを満たす。
- `DEGRADED`: 診断/既存workload維持は可能だが一部時間依存operationを禁止する。
- `UNTRUSTED`: clock step、逆行、source conflict、閾値超過を確認した。
- `UNKNOWN`: current evidenceを取得・検証できない。

許容値は用途別です。一般VM維持、new Command、certificate validation、failure correlation、高精度NFV telemetryを同じthresholdにしません。

Database/Control Plane clockは自分自身のtimestampだけでは`HEALTHY`を証明できません。`ClockReferenceSet`として、独立したupstream time source、DB HostとControl Plane nodeの相互観測、platform health、source diversityをprovenance/uncertainty付きで比較します。外部比較を取得できない場合は、last-known-goodを無期限利用せず、policyにより`DEGRADED`または`UNKNOWN`とします。単一external sourceを新しいmutation authorityにはせず、DB authority generationとgeneration/token gateを維持します。

### NFV Precision Time Boundary

PTP/GNSS等の高精度時刻は`PrecisionTimeDomain`としてControl Plane authority clockから分離します。KIMはHost/NIC/PTP hardware/daemonのcapability、offset、grandmaster/domain、holdover、qualityをObservation/Compliance/Placement inputとして扱えますが、VNF telemetry timestampやPTP lockだけをLease、credential、ordering、fencing authorityにしません。高精度時刻の提供・grandmaster運用・guest/application同期は外部infrastructure/NFの責任です。

### Leap Second and Smear

Clock source/policyはtime scale、leap indicator、smear有無/algorithm/windowを宣言します。異なるleap/smear policyを同一reference setへ無条件に混在させず、offsetが予測可能なwindowでもuncertaintyへ反映します。policy不明またはsource conflict時はtime-sensitive decisionを`DEGRADED/UNKNOWN`とし、leap eventをLease延長、mass expiry、duplicate calendar executionへ変換しません。

## 6. Database Authority Time

Control Planeが発行するLease、retention snapshot、queue age、rollout deadline等は、application nodeのwall clockではなくcurrent PostgreSQL authority上で計算・比較します。

- `issued_at`、`not_before`、`expires_at`、Lease token、authority generationを同じtransactionでcommitする。
- applicationが計算したabsolute expiryを無検証でDBへ持ち込まない。
- transaction内で「now」の意味を固定し、長時間transactionで期限判定が古くならないようstatement/decision pointを明示する。
- DB failover/restore後はnew database authority generationでpre-failover/pre-restore Lease/session/claimをfenceする。
- DB clock healthがbackward/forward step、uncertainty、primary conflictを示した場合、新Lease/GC/finalization/retention decisionをpauseする。
- clock正常化だけで旧Leaseをreviveしない。必要ならauthority generationを進め、current stateから再発行する。

Database Timeは共通comparison pointですが、Lease token、generation、transaction constraintを代替しません。

## 7. Lease Semantics

すべてのLeaseは最低限次を持ちます。

```text
Lease
├─ lease_id / owner / purpose / scope
├─ token / attempt or claim identity
├─ authority_generation
├─ issued_at / not_before / expires_at
├─ maximum lifetime / renewal policy
└─ revoked/released decision / evidence
```

共通規則:

1. Lease取得前、`not_before`前、失効/取消後に新しいside effectを開始しない。
2. Lease expiryは新規開始・authority更新を禁止するが、既開始side effectの不在を証明しない。
3. mutation開始済みでResult不達ならAttempt/claimを`UNKNOWN`としてread-backする。
4. renewalはcurrent owner/token/generationと未失効条件をDB transactionで検証し、新しいexpiry decisionを記録する。
5. expired Leaseを同じtokenの時刻変更で復活させず、新Lease/revisionを発行する。
6. maximum lifetimeを超える継続は明示renewal/long-running operation contractを要求する。
7. wall-clock rollbackでLeaseが延長されたように見える場合、clock anomalyとしてissuance/renewalを停止しgenerationでfenceする。

Command、Recovery Budget、Outbox publisher、Inbox processor、GC、Migration、Upgrade、leader Leaseは同じ原則を共有しますが、それぞれのresource authorityやverificationを代替しません。

## 8. Agent-local Deadline Conversion

Control PlaneとAgentのmonotonic clockは共有できません。AgentはDBのabsolute expiryをlocal wall clockだけで解釈せず、Gateway exchangeから保守的なlocal monotonic deadlineを導出します。

protocol envelopeは最低限次を含みます。

- DB authority generation、Lease ID/token
- server sample timeとDB `not_before/expires_at`
- request/response binding、session generation
- maximum allowed uncertainty/round-trip budget
- Command start budgetとexecution/renewal policy

Agentはrequest送信前のlocal monotonic sampleとresponse受信sampleを保持し、server sampleがそのexchange内のどこに対応するかを含むuncertainty intervalを作ります。local deadlineは、最も早くexpiryし得る境界からtransport/processing/drift marginを差し引いて計算します。受信時刻へ単純TTLを足しません。

- uncertainty/RTTがCommand policy上限を超えればCommandを開始しない。
- local wall clock jumpはmonotonic deadlineを延長しない。
- monotonic continuity消失、process restart、Host reboot/boot ID変更で未開始Commandを開始しない。
- execution開始時にlocal deadlineを再確認し、journalへclock sample/deadline derivationを保存する。
- local deadline超過後も既開始mutationを推測rollbackせず、safe boundaryまで進めResult/observationをjournalする。
- reconnect/full resyncでcurrent DB Lease/Attempt authorityを確認するまでcached Commandを再開しない。

このlocal guardはDB Lease authorityを置換せず、遅延・skewしたHostが期限外side effectを開始する確率を安全側へ抑える追加条件です。

## 9. Observation and Evidence Freshness

各Observation/Evidenceは次を分離します。

- `source_observed_at`: source clockでの観測時刻。diagnostic用。
- `received_at`: KIM境界で受信したDatabase Authority Time。
- `verified_at`: normalization/integrity/evaluatorが完了したDatabase Authority Time。
- `source_generation`、`collector_artifact`、`clock_quality`、`uncertainty`。

freshnessは原則としてtrusted `received_at/verified_at`とcurrent Database Authority Timeの差、source generation、collection challenge/request bindingから評価します。Agentの未来timestampでevidence寿命を延長しません。

stale、UNTRUSTED clock、timestamp conflict、missing evidenceは用途別に`UNKNOWN`または`DEGRADED`とします。last-known-goodを無期限にcurrent化せず、Critical placement/arming/fencing/adoption decisionをfail closedにします。

## 10. Credentials, Tokens, and Enrollment Time

certificate、OIDC token、bootstrap token、nonce/challenge、External Remediation responseの`not_before/not_after/expiry`はidentity/trust contractとして検証します。

- Control Plane trust validation clockのquality/uncertaintyを考慮し、境界付近の曖昧なcredentialをprivileged mutationへ使用しない。
- clock `UNKNOWN/UNTRUSTED`時は新規authentication、credential issuance/rotation、privileged session/Commandをfail closedにする。
- credentialが時間上有効でもEnrollment、Role Binding、Host authority、Command Leaseを意味しない。
- expiryはcredentialの今後の使用を拒否するが、既に認証・実行されたmutationがなかった証明にはならない。
- replay防止はexpiryだけに依存せず、nonce/idempotency/session/authority generationを使用する。
- 既存VM/dataplaneをcredential/clock failureだけで停止しない。

PKI固有のissuer hierarchy、rotation、revocation、overlapはPKI / Trust Lifecycle Architectureで定義します。

## 11. Scheduling, Maintenance, and Calendar Time

operatorが入力するmaintenance window、rollout schedule、deprecation date等はhuman wall-clock policyです。

- timezone ID、localeに依存しない入力、DST overlap/gap resolution policyを保持する。
- 実行前にversioned policyからUTC intervalをmaterializeし、元timezone/policy revisionを監査用に保持する。
- ambiguous/nonexistent local timeを暗黙補正しない。
- calendar window開始だけでdrain、fencing、Command authorityを取得しない。
- window終了は進行中mutationの未実行/失敗を証明せず、安全な境界でpause/UNKNOWN/read-backする。
- clock jumpでmissed windowを検出しても破壊的stepをcatch-up実行しない。新decision/approvalを要求する。

## 12. Queue Aging, Rate, and Grace Periods

Recovery Queue aging、rate window、grace period、rollout deadline、completion deadlineはdurable policy revisionとDatabase Authority Timeへbindします。

- priority agingはsafety/eligibilityを上書きしない。
- clock rollback/forward jumpでcredit/tokenを二重補充、queueを即時expire、graceを短縮しない。
- rate/concurrency consumptionはdurable token/window/Consumptionで管理し、process monotonic timerだけにしない。
- deadline超過は`WAITING/VIOLATED/EXPIRED/action-required`等のpolicy decisionを作るが、resource削除、failure確定、責任変更を暗黙実行しない。
- clock anomaly時はaffected scheduling/expiry decisionをpauseし、current DB generationとpolicyから再評価する。

## 13. Retention, Idempotency, and Reuse

retention/GCはData Retention Policy、reference/hold、archive/backup coverage、current Database Authority Timeからimmutable Candidate Snapshotを作成して判定します。

- wall clock jump、timezone/DST、Host timestampだけで大量GCしない。
- minimum safety horizonと一batch上限を持ち、clock anomaly時にGC Lease/partition detachを停止する。
- idempotency/Inbox/Receiptを最大client replay、Event retry、DR RPO、offline intervalより前に削除しない。
- retention expiryはrowを削除可能にする条件の一つであり、backend resource delete、IP/VLAN/Volume identity再利用、Lease side effect不在を証明しない。
- Event decoder/artifactはそのschemaを参照するonline/archive payloadのretention/hold期間とRelease Manifest referenceが解消するまでGCしない。
- restore後はrestore epoch/generationで旧Lease/sessionをfenceし、過去のwall-clock expiryだけでauthorityを再構築しない。

## 14. Failure Correlation and Event Time

Failure Campaign correlationはevent timeだけで決定しません。

- source event time、KIM received time、clock uncertainty、topology identity、independent evidenceを分離する。
- correlation windowはuncertaintyを含むintervalとして評価する。
- 同時刻/近接時刻だけでrack/power/site failureをmergeしない。
- clock qualityが不十分でcampaign uniquenessへ影響する場合、新規recovery dispatchをpauseし`UNKNOWN`へ送る。
- late event/late mergeでも既存Epoch、Operation、Consumptionをtimestamp順に書き換えない。

## 15. HA, DR, and Clock Discontinuity

- Control Plane process restartではprocess monotonic timerを復元せず、DB deadline/Leaseから新local timerを導出する。
- DB primary failoverではauthority generationとclock continuity/uncertaintyを検証し、新Lease発行前にsafe gateを通す。
- Host rebootはboot ID/monotonic continuityを変更し、pre-reboot local deadline/cached Commandを無効化する。
- PITRはtime travelですが、restore epoch/database authority generationによりpre-restore Lease/session/worker claimをfenceする。
- snapshot/backup内で未失効に見えるtokenを復元後に再利用しない。
- DR Site clockがUNTRUSTEDなら通常mutation、credential validation/rotation、GC/finalizationを再開しない。

## 16. API and Data Contract

公開/管理APIのtimestampはUTCとoffsetを明示し、必要に応じて次を返します。

- source/received/verified timestampの種別
- status、freshness/expiry、bounded clock quality/uncertainty
- policy/generation、reason、server-evaluated remaining duration

client提供timestampをresource ordering、Lease、freshness authorityとして受け入れません。APIの`expires_at`はserver-side decisionであり、client countdown表示は参考です。secret、raw time source topology、Host management identityはredactします。

## 17. Failure Semantics

| Failure | Containment / recovery |
|---|---|
| Control Plane/DB clock backward step | new Lease/renewal/GC/switch停止、generation/clock evidence確認 |
| Control Plane/DB clock forward step | mass expiry/GC/finalization停止、safety horizonとcurrent resource再評価 |
| Host wall clock skew | local monotonic guard使用、time-sensitive Command/placement block |
| Agent monotonic reset/boot change | cached/unstarted Command破棄、full resync、new session/Lease |
| source timestamp future/past | received time基準、evidence clock quality低下、freshness延長禁止 |
| time source conflict/unavailable | affected scope `UNKNOWN/UNTRUSTED`、privileged new mutation fail closed |
| maintenance window jump/miss | destructive catch-up禁止、新decision/approval |
| expiry境界でresponse loss | outcome `UNKNOWN`、journal/backend/Receipt read-back |
| retention clock anomaly | GC/partition detach pause、Candidate Snapshot再作成 |
| correlation uncertainty | automatic merge/duplicate dispatch停止、evidence追加/escalation |

## 18. Observability

最低限、次を公開します。

- DB/Control Plane/Host clock health、offset、uncertainty、last sample、boot/authority generation
- Lease issue/renew/expire/revoke、remaining lifetime、expired-but-unresolved Attempt/claim
- evidence freshness ageとsource/received timestamp差
- clock anomalyによるplacement/dispatch/auth/GC/rollout block数
- queue age、deadline、grace、maintenance window materialization
- time-related UNKNOWN、correlation uncertainty、clock step/recovery event

high-cardinality raw source identityを通常metric labelへ入れません。

## 19. Verification Contract

最低限、次を自動試験します。

- Control Plane/DB/Host wall clockのforward/backward step、slew、source loss。
- DB clockと独立reference sourceの乖離、全external reference喪失、single-source spoof。
- PTP lock/holdover/grandmaster changeがKIM authority clockへ昇格しないこと。
- leap second/smear policy混在とcalendar/Lease/freshness continuity。
- process restart/Host reboot/DB failover/PITRとmonotonic/authority generation。
- delayed/reordered Command、expiry直前開始、expiry後Result、renewal conflict。
- Agent request/response RTTとuncertainty上限、local deadline derivation。
- future/stale source timestampがevidence freshnessを延長しないこと。
- certificate/token/bootstrap/remediation expiry境界とclock uncertainty。
- DST gap/overlap、timezone変更、missed maintenance window。
- queue aging/rate/grace/deadlineがclock jumpで二重credit/即時破壊操作を生まないこと。
- retention/GC/idempotency/Event decoder retentionとforward jump。
- failure correlation window、late event、clock quality conflict。
- clock anomaly中も既存VMを維持し、禁止scopeだけをfail closedにすること。

## 20. 禁止事項

- wall clock、timestamp freshness、時刻の大小だけでauthority、ordering、ownershipを決める。
- Lease expiry、credential expiry、deadline超過をside effect未実行の証明にする。
- Agent/sourceの未来timestampでevidence、credential、Lease、retentionを延長する。
- local monotonic deadlineを別process/boot/sessionへ持ち越す。
- clock rollbackでexpired Lease/tokenをreviveする。
- clock forward jumpで大量GC、force cleanup、mass timeout、destructive catch-upを実行する。
- calendar windowだけでmaintenance/fencing/backend mutation authorityを得る。
- 同時刻だけでFailure Epochをmergeする。
- clock復旧だけでHost/Agent/Lease/credential authorityを自動再armする。
