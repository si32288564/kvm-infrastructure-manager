# ADR-0009: HAとDRのRPOを分離する

- 状態: Accepted
- 日付: 2026-08-09

## Context

PostgreSQLはdesired state、allocation、network intent、attachment、execution authorityのSystem of Recordです。HA failoverで5分のdata lossを許すと、backend resourceと所有・容量台帳の不整合を安全に解決できません。一方、Site全損に対するbackup/PITRには別の現実的RPOが必要です。

## Decision

- 同一Site HAのcommitted authority data RPO目標を0とする。
- HAはsynchronous/quorum replication、automatic failover、fencingを前提とする。
- Disaster Recoveryのbackup/PITR RPO目標を5分以内、RTOを60分以内とする。
- Restore後はread-only recovery mode、full observation、quarantine、explicit adoptionを経てmutationを再開する。
- backendにだけ存在するresourceを自動削除・自動adoptしない。

## Consequences

- PostgreSQL配置とlatencyに厳しい要件が加わります。
- restore drillとrecovery reconciliationが製品試験範囲になります。
- HAとDRのSLO、runbook、顧客向け表現を分ける必要があります。
