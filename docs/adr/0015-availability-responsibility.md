# ADR-0015: Availability責任をPlacement Pool Policyとして固定する

- 状態: Proposed
- 日付: 2026-08-09

## Context

HostGroupはPlacement Pool、Failure Domain、Operational Cohortを型分離していますが、Host障害時にKIMとNF/VNF/operatorの誰が復旧責任を持つかが第一級Policyではありません。default recoveryやheartbeat lossから責任を推測すると、NF側HA workloadをKIMが二重起動したり、Infrastructure Managed workloadを放置したりする危険があります。

## Decision

- immutable versioned `AvailabilityPolicy`を導入し、`PLACEMENT_POOL`から`AVAILABILITY_POLICY` GroupPolicyBindingで参照する。
- responsibilityを`INFRASTRUCTURE_MANAGED`、`WORKLOAD_MANAGED`、`MANUAL`へ分類する。
- Host failure actionを`RESTART_ON_OTHER_HOST`、`EVACUATE`、`NO_AUTOMATIC_ACTION`へ分類し、責任種別との合法な組合せを固定する。
- placement可能なHost/request contextは一つのeffective Availability Policyを解決できなければならない。
- Final Admission時にPolicy/Pool/membership generationをVMのimmutable Availability Bindingへ保存する。
- Group/Policy変更だけで既存VM Bindingを変更せず、明示Rebind Operationを必要とする。
- `WORKLOAD_MANAGED`と`MANUAL`ではKIMから自動restart/evacuateを開始しない。
- `INFRASTRUCTURE_MANAGED` recoveryもsource fencing、storage single-writer、VM/resource eligibility、Failure Domain、transactional admission、verificationを必須とする。
- Control Plane HA/DRとmanaged workload recoveryを別Architectureとして扱う。

## Consequences

- NF側HAとKIM managed HAを同じHost基盤上で安全に共存できます。
- Availability Policy/Binding、Host Failure Epoch、Recovery Plan/Operation、Rebind workflowが必要になります。
- Policy変更の即時性より、既存VM責任の再現性と二重起動防止を優先します。
- Northbound Fault/EventとAvailability class mappingを定義する必要があります。
