# HA / DR Architecture

- 状態: Draft
- 更新日: 2026-08-09

## 1. 原則

PostgreSQLは管理情報のキャッシュではなくauthorityです。High AvailabilityとDisaster Recoveryを別の障害モデル、RPO、手順として扱います。

DB以外を含む障害分類とcontainment/fencingは [System-wide Failure Model](failure-model.md) に従います。

本書のControl Plane HA/DRと、Host failure時のmanaged VM recoveryは別問題です。VM recovery責任と動作は [Availability Responsibility and Managed Recovery Architecture](availability-responsibility-architecture.md) に従い、Control Plane failoverだけでVM restart/evacuateを開始しません。

## 2. Authority Data

以下はcommitted stateを失うと自動再構築できない、または安全な所有判断ができない情報です。

- Tenant、Project、Membership、Role Binding、Quota
- desired state、resource ownership、generation
- placement allocation、reservation、PCI/Volume attachment authority
- PMD CPU、DPDK memory、Dataplane Port/RxQ、VM Dataplane Binding authority
- logical network intent とprovider binding
- Operation、Job、Command、Lease、Attempt、idempotency
- Host Operation Authority と監査outbox
- Hardware Identity Evidence、Enrollment Policy、Host Profile/Baseline、Evaluator Artifact/Assignment、Compliance evidence/summary、External Remediation Request/Claim
- HostGroup、Dimension、materialized Membership、Hierarchy、Policy Binding、Membership Snapshot、Placement Scope
- Availability Policy/Binding、Host Failure Epoch、Failure Campaign/Membership、Recovery Campaign Claim、Recovery Plan/Operation、Manual Recovery Decision
- Recovery Budget Policy、Queue Entry、Budget Lease/Consumption、canonical scope schema、Workload Resilience Group/Member/Constraint/Domain Claim

backend observationだけでこれらを無条件に復元しません。

## 3. High Availability

対象: 単一Site内のnode/process/network障害。

- committed PostgreSQL authority dataのRPO目標は0。
- synchronous/quorum replicationとautomatic failoverを前提とする。
- clientはtransaction outcome unknownを扱い、idempotency keyで再確認する。
- leader leaseにはfencing tokenを使用する。
- Message Bus再構築時もPostgreSQL authorityから未完workを再駆動できる。
- Control Plane単一node障害で既存VM/dataplaneとAPI提供を継続する。

## 4. Disaster Recovery

対象: Site喪失、database cluster全損、operator error、corruption。

- backup/PITR RPO目標は5分以内。
- Control Plane RTO目標は60分以内。
- backupは暗号化、完全性検証、別failure domain保管を行う。
- 定期restore drillでschema migrationとartifact versionを含めて検証する。

## 5. Restore and Reconciliation

restore時点より新しいbackend resourceが存在し得るため、通常reconciliationとは別のrecovery modeを使用します。

1. Control Planeをread-only recovery modeで起動する。
2. Host/OVN/Storageからobserved stateをfull resyncする。
3. DB authorityと一致するresourceだけをmanagedとして再確認する。
4. backendにだけ存在するresourceを`unresolved`/`quarantined`として隔離する。
5. identity、provenance、attachment/fencing、operator authorizationを確認して明示adoptする。
6. capacity/allocation ledgerを検証してからmutationを再開する。

未知VM/Port/Volumeを自動削除または自動adoptしません。結果不明Commandの反対操作も自動発行しません。

## 6. Verification

- PostgreSQL planned failover中のAPI/Operation継続試験
- commit response喪失時のidempotent recovery
- backupから隔離環境への定期restore
- restore point後に作られたVM/Port/Volumeのquarantine試験
- expired Lease、started Attempt、UNKNOWN outcomeの復旧試験
- OVN/Ceph unavailableを含む段階的reconciliation
- audit trailとrecovery decisionの保存
