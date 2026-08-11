# ADR-0017: Recovery stormをdurable budgetとqueueで制御する

- 状態: Accepted
- 日付: 2026-08-09

## Context

Infrastructure Managed HAでHost、rack、power、site障害が起きると、多数VMのRecovery Planが同時に作られます。worker数やin-memory semaphoreだけで抑制すると、Control Plane failoverやworker再起動で上限を失い、storage/network/libvirtへ再起動stormを起こし得ます。一方、rate limitをcapacity/fencing authorityとして扱うと安全条件を迂回します。

## Decision

- immutable versioned `RecoveryBudgetPolicy`をAvailability Policyから参照する。
- durable `RecoveryQueueEntry`とPostgreSQL transactionで発行する`RecoveryBudgetLease`を導入する。
- Site/Pool/Failure Domain/backend/Project等のapplicable budgetをすべて取得してからplanning/dispatchする。
- PLANNINGとDISPATCH phaseを分離し、各phaseのapplicable budget scopeを一transactionで取得する。
- 全scope row/tokenを`phase rank + scope dimension rank + normalized scope ID + policy ID + generation`のcanonical順でlockし、全取得経路をCore Budget Acquirerへ限定する。serialization/deadlock時は全rollbackし、bounded retry前にscope setを再評価する。
- dispatch transactionでRecovery Operationとdurable `RecoveryBudgetConsumption`を不可分commitし、verified terminalまでconcurrencyへ計上する。
- Budget Leaseをdispatch許可だけに限定し、fencing、Placement admission、capacity claim、Command Lease、verificationを代替させない。
- priority class、aging、fair-share、per-scope concurrency、rate/burst、backend health circuit breakerをversioned policyで評価する。
- same failure signalをfailure epochへdeduplicateし、複数Epochをevidence付きversioned `FailureCampaign`へ相関付ける。
- `RecoveryCampaignClaim(campaign, VM, Binding revision, action)`をunique authorityとしてQueue/Operation/Consumptionをbindし、後着Campaign mergeでも追加dispatchと二重Budget計上をfenceする。
- Budget Lease expiryやworker lossからRecovery未実行を推測せず、Operation/Command/read-backで解決する。
- queue delay、saturation、blocked/escalated stateをdurable evidence/eventとして公開する。

## Consequences

- Control Plane failover後もrecovery concurrency/rate authorityを維持できます。
- Budget/Queue/Lease/Consumption、fair scheduling、circuit breaker、queue observabilityが必要になります。
- canonical scope schema、Campaign correlation rule、late merge reconciliationをversioning/test対象として管理する必要があります。
- Recovery開始遅延を許容する代わりにbackend overloadと重複dispatchを抑えます。
- Budget tuning、priority/fairness class、failure campaignによる検証が必要です。

Migration 055ではpower/Result ambiguity中のBudgetを`CONSUMED`に維持する。exact Recovery Verificationをaccepted Terminal Decisionがcommitした時だけ`RELEASED`へgenerationを進める。terminal response lossのreplayは同じimmutable Decisionへ収束し、active-countを一時的に過小計上またはtransition generationを増幅しない。
