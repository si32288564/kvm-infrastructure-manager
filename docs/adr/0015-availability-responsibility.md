# ADR-0015: Availability責任をPlacement Pool Policyとして固定する

- 状態: Accepted
- 日付: 2026-08-09

## Context

HostGroupはPlacement Pool、Failure Domain、Operational Cohortを型分離していますが、Host障害時にKIMとNF/VNF/operatorの誰が復旧責任を持つかが第一級Policyではありません。default recoveryやheartbeat lossから責任を推測すると、NF側HA workloadをKIMが二重起動したり、Infrastructure Managed workloadを放置したりする危険があります。

## Decision

- immutable versioned `AvailabilityPolicy`を導入し、`PLACEMENT_POOL`から`AVAILABILITY_POLICY` GroupPolicyBindingで参照する。
- 既存VMのresponsibility変更はexact source Bindingとexact active target Policy、actor/authorization/reasonを持つexplicit Rebind Request/Decisionだけに限定し、accepted Decision、next Binding revision、current pointerを同一transactionでcommitする。Policy/Group driftやRebind intentだけでは変更せず、Rebindをfailure/recovery/resource mutation authorityにしない。
- failure signalをclosed typed append-only evidenceとして保持し、Failure Epochをopen時点のexact VM Availability Binding/Policy/Admission/allocationへbindする。typed confirmation consumerがない間は`SUSPECTED`だけを発行し、heartbeat/Agent lossまたは`UNKNOWN`をconfirmed failure、fencing proof、Recovery authorityへ昇格させない。
- `failure_confirmation_policy` text slotをruntime ruleとして解釈しない。exact AvailabilityPolicy revisionはclosed typed FailureConfirmationPolicy revisionを明示参照し、exact Epoch/Policy/Evidence snapshotのpure Evaluationとpositive Confirmation Decisionを分離する。accepted Decisionだけが`SUSPECTED`から`CONFIRMED`へ進め、`CONFIRMED`をfencing proofまたはRecovery authorityとして扱わない。
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
