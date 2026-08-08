# Data and Persistence Architecture

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的と原則

KIMのPostgreSQLはcacheではなく、resource ownership、desired state、allocation、attachment、execution、recovery等のauthorityです。本書は、authorityと履歴evidenceを混同せず、障害・upgrade・retention・DRを経ても意味を維持する永続化契約を定義します。

- authorityはPostgreSQL commitだけで進める。
- backend observationやMessage Bus deliveryをauthorityへ昇格しない。
- current decisionと、その判断に使ったimmutable evidenceを分離する。
- delete/retention/partition maintenanceをbackend mutationと分離する。
- schema rollout中もN/N-1 serviceが同じauthority semanticsを解釈できるようにする。
- PITR後は失われたcommitをbackend stateから推測再生成せず、read-only recovery modeで再証明する。

## 2. Persistent Data Classes

すべてのtable/resourceは次のclassをschema catalogへ宣言します。

| Class | 例 | 更新規則 | Retention/Restore規則 |
|---|---|---|---|
| `CURRENT_AUTHORITY` | desired resource、ownership、Allocation、Attachment、Lease、current binding/summary | generation/ETag、DB constraint、明示state transitionだけで更新 | active reference中は削除不可。PITR後の正本だがbackendとの差分を再証明 |
| `IMMUTABLE_DECISION` | Admission decision、Policy revision、Availability Binding、Campaign membership decision | append-only revision。過去行をcurrent resultに合わせて変更しない | retention期限とreference/legal holdを満たすまで保持 |
| `IMMUTABLE_EVIDENCE` | Attempt、Result、Observation、Compliance Result、fencing proof、audit reference | append-only、digest/provenance付き | archive可能だが未解決authorityの根拠を先にGCしない |
| `DELIVERY_JOURNAL` | Outbox、Inbox、Receipt、delivery attempt | at-least-onceを前提に状態遷移 | dedupe/replay windowと下流SLOより長く保持 |
| `DERIVED_PROJECTION` | aggregate capacity、検索index、current summary、metrics rollup | authorityから再構築可能 |破棄・再構築可能。authority transactionの成否条件にしない |

secret value、private key、backend credentialはこの分類の外に置き、Secret Providerを正本とします。DBにはsecret reference、version、scope、必要ならdigestだけを保持します。

### Current AuthorityとHistoryの接続

current rowは最新判断を指すpointer/generationを持てますが、履歴を上書きしません。

```text
Current Resource / Summary
├─ resource_id
├─ generation
├─ current_revision_id
├─ current_evidence_set_id
└─ lifecycle / ownership / authority state
          │
          ├──> Immutable Decision Revision
          └──> Immutable Evidence Records
```

summary再計算失敗でhistoryを変更せず、history欠損時にsummaryを推測してauthorityへ使用しません。

### Reference Classes

Current AuthorityからDecision/Evidence/Archiveへのreferenceは、実装都合で曖昧なID参照にせず次へ分類します。

| Class | 用途 | Enforcement |
|---|---|---|
| `HARD_DATABASE_REFERENCE` | 同じonline authority set内のownership、active claim、current revision、未完Operation | PostgreSQL FK/unique/check。deferred constraintを含め同transactionで検証 |
| `VERIFIED_LOGICAL_REFERENCE` | partitioned history、別retention class、cross-partition evidence | type+ID+digest+schema generationを保持し、write/GC時のIntegrity Verifierとperiodic scanで検証 |
| `ARCHIVE_REFERENCE` | detached/archive済みimmutable evidence | archive manifest ID、object key、record digest、schema/reader artifact、retention/holdを保持 |

authority/safetyに必要な参照を、FK実装が難しいという理由だけでunchecked logical referenceへ下げません。logical/archive referenceはcurrent pointer更新前に存在/digest/accessibilityを検証し、Verifierが欠損・不一致を検出したscopeは`REFERENCE_UNKNOWN`としてmutation/GCを停止します。archive detachはonline referenceを同transactional workflowでArchive Referenceへ切り替え、manifest verification前に元partitionをdropしません。

## 3. Schema and Row Conventions

authority-bearing schemaは最低限次を使用します。

- opaque stable ID。Tenant resourceは`tenant_id/project_id`を明示する。
- mutable authority rowは単調増加`generation`とAPI用ETagを持つ。
- immutable revision/evidenceは`revision_id`またはevent ID、schema version、payload digest、provenanceを持つ。
- `created_at/observed_at`は診断とbounded freshnessに使うが、単独でordering/fencing authorityにしない。
- source observed time、KIM received/verified time、Database Authority Time、process/Agent monotonic timeの意味は [Time and Clock Semantics Architecture](time-and-clock-semantics.md) に従う。
- state transitionは許可された遷移とpreconditionをDB/Application双方で検証する。
- ownership、active uniqueness、allocation、single-writer、Lease token等は可能な限りunique/check/foreign-key constraintで保護する。
- logical deleteはtombstone state/generationを発行し、active referenceと非同期backend cleanup完了を確認してからphysical GC候補にする。

resource type間で同じ意味のgeneration、revision、status名を別意味に再利用しません。schema catalogはdata class、owner service、tenant scope、retention class、partition key、PII/secret classification、restore criticalityを管理します。

## 4. Transaction Boundaries

一つのauthority decisionに必要なrowは一つのPostgreSQL transactionでcommit/rollbackします。例:

- resource desired state + Operation + idempotency receipt + Outbox Event
- Final Admission + quota usage + Allocation/Attachment/Domain Claim + Availability Binding
- Command Lease + Attempt creation
- accepted Result receipt + Attempt evidence
- Recovery Operation + Recovery Budget Consumption

transaction中にlibvirt、OVN、Ceph、Agent、外部IdPへ接続しません。外部side effectとDB rollbackを分散transactionとして偽装しません。commit outcomeが不明なclient/workerは、同じidempotency keyまたはstable identityでread-backし、新規mutationを推測作成しません。

lock取得が複数resourceへ及ぶ場合、各Domain Architectureが定義するcanonical順序を使用します。deadlock/serialization failureはtransaction全体をrollbackし、bounded retry前にcurrent generationから再評価します。

## 5. Transactional Outbox

domain mutationと外部通知/内部work intentは同じtransactionでOutboxへ書きます。

```text
OutboxRecord
├─ event_id / event_type / schema_version
├─ aggregate_type / aggregate_id / aggregate_generation
├─ tenant_id / project_id / authorization_projection
├─ payload / payload_digest / redaction_class
├─ correlation / causation / trace
├─ committed_at / available_at
└─ publish_state / attempt / lease / last_error_class
```

- publisherはOutbox rowを短いLeaseでclaimし、at-least-onceで配送する。
- publish ACKはdomain authorityを進めない。ACK喪失時は同じ`event_id`を再送する。
- orderingが必要なconsumerはaggregate generation/sequenceを検証し、global orderingを仮定しない。
- poison eventはbounded retry後に`BLOCKED`/dead-letter projectionへ移すが、元Outboxとdomain commitを削除しない。
- Event payloadは発行時schema versionとredaction policyを保持し、後続resource変更から再生成しない。

Message Busはdelivery transportであり、Outboxを迂回してmutation intentをauthority化しません。

## 6. Inbox, Receipt, and Replay

Agent、backend adapter、external remediation、webhook callback等から受けるat-least-once inputはInbox/Receiptで重複排除します。

```text
InboxRecord
├─ source_identity / source_authority_generation
├─ message_id / schema_version / payload_digest
├─ received_at / expiry / replay_scope
├─ processing_state / decision_id
└─ accepted_receipt / conflict_evidence
```

- dedupe scopeはsource identity/authority generation/message IDを含む。
- 同じkey+digestは同じReceiptを返し、同じkey+異なるdigestはconflictとして監査する。
- Inbox受理とdomain decision/Outbox作成は同じtransactionでcommitする。
- schema不明、期限切れ、失効source、stale generationはfail closedにする。
- Inbox retention前に送信側最大replay期間、DR RPO、監査要件を満たす。

## 7. Retention, Archival, and Garbage Collection

`DataRetentionPolicy`はdata class/resource typeごとにversion、minimum online period、archive period、tombstone period、dedupe/replay period、legal/security hold、approvalを定義します。TenantがCore safety evidenceを任意に短縮できません。

physical GCは次の段階を持ちます。

```text
Retention eligible
  -> reference/hold/unresolved check
  -> GC Candidate Snapshot
  -> GC Lease
  -> bounded batch delete or partition detach
  -> integrity verification
  -> GC Receipt / audit
```

- current/active authority、unresolved `UNKNOWN`、open Operation、active Lease/Claim、current summaryが参照するdecision/evidenceはGCしない。
- tombstoneはresource ID、tenant/project、final generation、delete decision、retention class、integrity digestを最低限保持する。
- archiveは暗号化、access control、checksum、schema/artifact reader compatibilityを持つ。
- DB GCはlibvirt/OVN/Ceph resource削除を開始しない。backend cleanupは別のauthorized typed Operationとverificationを必要とする。
- GC response lossやworker crashではreceipt/snapshotをread-backし、同じbatchを冪等に再開する。

## 8. Partitioning and Scale

partitioningは主にOutbox/Inbox delivery history、Observation、Attempt/Result、Compliance/Audit reference等のappend-heavy tableへ使用します。

- mutable Current Authorityやglobal uniqueness/transactional admissionを、運用都合だけで独立partition authorityへ分裂させない。
- partition key、boundary、retention classをschema catalogでversion管理する。
- future partitionを事前作成し、missing partitionをmutation data lossへ変換しない。緊急default partitionはAlarm対象とする。
- detach/drop前にretention eligibility、foreign/reference guard、archive manifest、replica/backup coverageを検証する。
- tenant/time partitioningでもProject authorizationとcross-partition uniquenessをparent registry/constraintで維持する。
- index creation/rebuild、vacuum、analyze、partition maintenanceはbounded lock/IO budgetとpause条件を持つ。

## 9. Schema Migration and Rolling Upgrade

schema変更は`expand -> migrate/backfill -> switch -> contract`で進めます。

本節はdatabase schema evolutionの正本です。Control Plane/Agent/API/Event/extension/backendを含む製品release順序、Release Manifest、mixed-version、Feature Gate、rollback decisionは [Upgrade and Compatibility Architecture](upgrade-and-compatibility-architecture.md) を正本とします。

1. **Expand**: nullable/new table/index等、N/N-1 reader/writerに後方互換なschemaを追加する。
2. **Migrate**: immutable migration artifact digestとschema generationを記録し、idempotent/checkpointed backfillを小さいbatchで実行する。
3. **Switch**: feature/read-write capability gateを、全required replicaとbackfill verification後に切り替える。
4. **Contract**: rollback/compatibility window終了と旧reader不在を証明後、明示承認で旧column/indexを除去する。

原則:

- service binaryはsupported schema min/max generationを宣言し、互換外ならreadinessを拒否する。
- migrationはsingle active Lease/fencing token、statement/lock timeout、bounded batch、pause/abortを持つ。
- backfillはcurrent row generationを条件にし、並行更新を上書きしない。
- enum meaning、authority key、generation semanticsを同名のまま変更しない。新revision/schemaで表す。
- DDL/migration失敗を成功扱いせず、適用履歴、digest、operator、開始/終了、verificationをappend-onlyで残す。
- destructive contract前にbackup/restore drillとrollback decisionを確認する。

## 10. Backup, PITR, and Restore Epoch

backup setはbase backup、連続WAL、schema/migration catalog、required artifact manifest、encryption key reference、checksum、start/end LSN/timeを一つのmanifestへbindします。別failure domainへ保管し、isolated restoreで定期検証します。

PITR後は新しい`restore_epoch`と`database_authority_generation`を発行します。restore前に発行された次のauthorityは、wall clock上未失効でも新規mutationへ使用できません。

- leader/migration/GC/Budget/Command Lease
- Agent session authorityとdispatch claim
- Outbox publisher/Inbox processor claim
- cached Placement/Compliance/Group/Policy snapshot

restore epochは復元cluster内のfencing tokenであり、旧Site/旧primaryの停止証明を代替しません。通常mutationを再開する前に、DR activation authorityが旧database writer、旧Control Plane dispatch path、旧service credential/endpointのfencing proofを記録します。旧Siteの停止を証明できなければ復元側も`RECOVERY_READ_ONLY`を維持します。

## 11. Recovery Mode and Authority Re-establishment

Restore後はControl Planeを`RECOVERY_READ_ONLY`で起動します。これはTenant/resource/backend mutationを禁止する状態であり、restore epoch、observation、classification、reconciliation evidence、operator decision等のrecovery-control writeだけを専用権限で許可します。

Recovery Control write pathは通常service principal/DB role/APIから分離します。専用workload identity、最小権限DB role、recovery-only API、DR activation generation、操作種別ごとのapproval、immutable auditを要求します。通常API GatewayやDomain workerがroleを昇格・代行できず、Recovery Control roleも通常resource/backend mutation tableやCommand dispatchを直接進められません。scope authority再開は承認済みtransitionを通常roleが新generationとして受け取る方式にします。

1. restored DB/schema/artifact/backup manifestのintegrityを検証する。
2. restore epochでpre-restore actor/Lease/sessionをfenceする。
3. Host/libvirt、OVN/OVS、Storageからfull observationを収集する。
4. resourceを`MATCHED`、`DB_ONLY`、`BACKEND_ONLY`、`CONFLICTING`、`UNKNOWN`へ分類する。
5. current authorityと証拠が一致するscopeだけを段階的にmutation可能へする。
6. backend-only resourceはquarantineし、identity/ownership/fencing/authorization付きexplicit Adoption Operationで新しいauthorityを作る。
7. DB-only/conflicting/UNKNOWNを推測削除・再作成せず、domain-specific verificationへ送る。

PITR pointより後に外部へ配送済みのEvent/CommandがDB上未配送へ戻る可能性があります。Outbox/Inbox/Commandは同じstable IDで再送し、consumer/Agent journalのReceiptを回収します。外部side effectのReceiptも失われている場合はOutcomeを`UNKNOWN`としてtyped read-backし、反対操作を自動実行しません。

authority再開は全体一括booleanではなく、resource/domain scopeとrecovery generationを持ちます。ただし依存scopeが`UNKNOWN`なら上位mutationをfail closedにします。

## 12. Integrity, Security, and Observability

- backup/archive/immutable evidenceへchecksum/digestと改ざん検知を適用する。
- DB roleをservice責務別に分離し、extensionへdirect SQLを許可しない。
- row/project scope、diagnostic export、archive restoreでTenant authorization/redactionを維持する。
- secret/credential、生backend error、不要なhardware identityをOutbox/Inbox/evidenceへ保存しない。
- replication lag、WAL/backup gap、transaction abort/deadlock、table/index/partition growth、Outbox/Inbox age、GC backlog、migration/backfill progress、restore reconciliationをmetrics/alarm化する。
- database integrity failure、missing partition、backup gap、schema compatibility failureはmutation readinessをfail closedにできる。

## 13. API and Operational Contract

公開APIはretention policyの許可された表示、archive/restore job、recovery mode/status、migration statusを提供できますが、Tenantへphysical table/partition/LSN/raw topologyを公開しません。

operator actionはauthorization、approval、idempotency、Operation、auditを要求します。manual SQLによるauthority修正は製品runbookに含めず、修復はversioned migrationまたはtyped repair Operationとして証拠付きで行います。

## 14. Verification Contract

- commit response lossとidempotency read-back
- domain mutationとOutbox insertのatomicity、duplicate publish
- Inbox duplicate/replay/digest conflict
- authority/history/projectionの再構築と改変検知
- legal hold/reference中のGC拒否、GC worker crash、partition detach guard
- N/N-1 rolling schema、concurrent write中backfill、DDL lock timeout、rollback window
- backup/WAL gap、corrupt manifest、PITR、restore epoch fencing
- restore point後に実行済みのCommand/Event再送とReceipt回収
- backend-only/DB-only/conflicting/UNKNOWN resourceのquarantine/adoption
- 100 Host/5,000 VM規模のpartition、vacuum、Outbox/Inbox、retention負荷
