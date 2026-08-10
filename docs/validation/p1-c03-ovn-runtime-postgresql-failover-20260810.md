# P1-C03 OVN Runtime PostgreSQL Failover Validation

- 実施日: 2026-08-10
- 対象: synchronous PostgreSQL authority failover 中の OVN runtime work recovery
- Test Contract: `AT-NET-038`, `FI-DB-003`
- Invariant: `INV-NET-032`, `INV-HA-001`

## Outcome

PostgreSQL 17 primary/standby を streaming replication で構成し、standby が `sync`、primary の `synchronous_commit=remote_apply` であることを確認してから OVN intent、claim generation 1、Attempt、apply authorizationをcommitしました。OVN apply 後・observation commit前にprimaryを強制停止し、standbyをpromoteしました。

promoted primary はpre-failover commit LSN、`restore_epoch`、`database_authority.authority_generation`を保持しました。別 `kim-network-worker` processはclaim generation 2を`READ_BACK_FIRST`で取得し、既存 OVN objectをread-backしてduplicate applyなしで`OBSERVED`へ収束しました。

```text
PostgreSQL primary + synchronous standby
→ worker A claim generation 1
→ APPLY_AUTHORIZED
→ typed OVN apply
→ primary hard stop before observation commit
→ standby promotion
→ generation 1 DISPATCH_UNKNOWN
→ worker B claim generation 2 / READ_BACK_FIRST
→ existing OVN ownership MATCHED
→ one OBSERVED decision
```

## Authority Assertions

- standbyはauthority mutation前に`streaming / sync`だった。
- pre-failover committed LSNはpromoted primaryに存在した。
- HA failoverでは`restore_epoch`とdatabase authority generationを変更しなかった。
- old primary containerを停止したままstandbyをpromoteし、同時writerを許可しなかった。
- old primary connectionを持つworker Aのobservation completionはcommitされなかった。
- expired generation 1へimmutable `DISPATCH_UNKNOWN`を1件記録した。
- promoted primaryだけがgeneration 2 / `READ_BACK_FIRST`をgrantした。
- old owner/generationのapply authorizationを`ErrStaleOVNRuntimeClaim`で拒否した。
- Attempt evidenceは2件、`READ_BACK_STARTED`は1件、`APPLY_AUTHORIZED`は1件だった。
- physical apply countは1で、terminal work stateは`OBSERVED`だった。

## Fixture Boundary

- PostgreSQL primary/standby、streaming replication、synchronous commit、primary kill、`pg_ctl promote`は実 process/containerで実行した。
- worker A/B は別々の実 `cmd/kim-network-worker` processである。
- OVN CLI はclosed typed fixture executableを使用し、backend stateをprocess外 evidenceとして保持した。
- Patroni、HAProxy、production service discovery、quorum DCSのcertificationではない。
- production KVM/OVN Hostには接続・変更していない。

## Result

```text
=== RUN   TestOVNRuntimeWorkerPostgreSQLFailoverConvergence
--- PASS: TestOVNRuntimeWorkerPostgreSQLFailoverConvergence
PASS
```

## Remaining Qualification

- Patroni等のproduction HA managerとclient endpoint切替
- long-running applyのclaim renewal
- failoverとclaim renewalの境界競合
- mass backlog、retry storm、long-duration soak
- foreign-object quarantineの運用復旧

今回の結果はOVN work authority scopeのRPO 0とfailover recoveryを証明しますが、Control Plane全体のHA certificationには昇格させません。
