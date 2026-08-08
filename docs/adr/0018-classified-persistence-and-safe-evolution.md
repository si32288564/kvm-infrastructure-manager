# ADR-0018: 永続データをclassifyし安全なschema evolutionとrestoreを行う

- 状態: Accepted
- 日付: 2026-08-09

## Context

KIMではPostgreSQLがdesired state、ownership、allocation、attachment、execution/recovery authorityを持つ一方、Attempt、Observation、Compliance、Audit、delivery history等の大量の履歴も保持します。全行を同じ更新・retention規則で扱うと、authorityの誤削除、無制限な肥大化、rolling upgrade中の意味不一致、PITR後のstale authority再利用が起こり得ます。

## Decision

- persistent dataを`CURRENT_AUTHORITY`、`IMMUTABLE_DECISION`、`IMMUTABLE_EVIDENCE`、`DELIVERY_JOURNAL`、`DERIVED_PROJECTION`へ分類する。
- current authorityと履歴/evidenceを分離し、current pointer更新で過去decision/resultを書き換えない。
- referenceをhard database、verified logical、archive manifest referenceへ分類し、logical/archive integrity不明scopeをfail closedにする。
- domain mutationとOutbox、Inbox受理とdomain decisionをそれぞれ一つのPostgreSQL transactionでcommitする。
- retention/GCをversioned policy、reference/legal hold、tombstone、GC Lease/Receiptで制御し、DB GCからbackend side effectを起こさない。
- append-heavy historyだけを主なpartition対象とし、authority uniqueness/transactionをpartition都合で分裂させない。
- schema変更をexpand/migrate/switch/contractで実施し、N/N-1 compatibility、idempotent backfill、migration Lease、bounded lockを要求する。
- backup/WAL/schema/artifactをmanifestへbindし、PITR後に新しいrestore epoch/database authority generationでpre-restore Lease/session/claimをfenceする。
- restore epochだけをsplit-brain fencingとみなさず、旧database writer/Control Plane/credentialの外部fencing proofまで通常mutationを再開しない。
- restore後はread-only recovery mode、full observation、classification、quarantine、explicit adoptionを経てscope別にmutation authorityを再開する。
- Recovery Control writeを通常principal/DB role/APIから分離し、専用identity/role、approval、DR generation、immutable auditを要求する。

## Consequences

- schema catalog、retention catalog、migration/GC controller、Outbox/Inbox、restore coordinatorが必要になります。
- historical evidenceとcurrent query pathを個別にscale/partition/archiveできます。
- rolling upgradeとPITR後のfailure semanticsをテスト可能な契約として維持できます。
- retention容量、partition粒度、initial compatibility window、backup方式の製品profileを別途決定する必要があります。
