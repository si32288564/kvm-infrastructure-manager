# ADR-0022: 分散clockを区別し時間切れを未実行証明にしない

- 状態: Accepted
- 日付: 2026-08-09

## Context

KIMはCommand/Recovery/worker/GC/Upgrade Lease、evidence freshness、credential expiry、retention、queue aging、maintenance window、failure correlationへ時間を使用します。Control Plane、PostgreSQL、Agent、Host、backend、external systemの時計は一致せず、step、slew、reboot、failover、PITR、network delayが発生します。wall clockだけで期限・順序・freshnessを判断すると、stale authorityの復活、期限外Command、証拠の誤認、大量GC、二重recoveryが起こり得ます。

## Decision

- Wall Clock UTC、Database Authority Time、Process Monotonic Clock、Agent-local Monotonic Deadline、Observed Source Timestamp、Received/Committed Timestampを区別する。
- resource ordering/fencingはgeneration、revision、token、sequence、restore epochを用い、timestampだけをauthorityにしない。
- Control Plane Lease/deadline/freshness/retention decisionをcurrent PostgreSQL authority timeとgenerationへbindする。
- Lease/credential/deadline expiryは今後の利用を終了するが、期限前のside effect不在を証明しない共通原則とする。
- AgentはGateway exchangeとuncertaintyから保守的なlocal monotonic deadlineを導出し、wall clockや受信時刻+TTLだけでCommandを開始しない。
- source observed timeとKIM received/verified timeを分離し、未来timestampでevidence freshnessを延長しない。
- clock healthをHEALTHY/DEGRADED/UNTRUSTED/UNKNOWNへ分類し、用途別policyでplacement、dispatch、auth、GC等をfail closedにする。
- clock forward/backward step時にLease復活、大量expiry/GC、destructive catch-upを行わない。
- maintenance/calendar timeはtimezone/DST policyからversioned UTC intervalへmaterializeし、windowだけをmutation authorityにしない。
- failure correlationはtime interval/uncertaintyに加えtopology/independent evidenceを要求する。
- DB failover、Host reboot、PITRではauthority/boot generationでpre-event timer/Lease/sessionをfenceする。

## Consequences

- Clock Observation/Health Policy、uncertainty-aware Agent protocol、time fault injectionが必要になります。
- clock qualityが不十分なHost/scopeでは安全のため新規operationが停止する場合があります。
- timestamp列だけの単純実装より複雑ですが、time jumpやpartition時にもauthority semanticsを維持できます。
- PKI/Trust Lifecycleは本ADRのclock qualityとexpiry semanticsを前提に設計できます。
- initial clock source、offset/uncertainty threshold、Lease lifetime、DST/retention/correlation profileを別途決定する必要があります。
