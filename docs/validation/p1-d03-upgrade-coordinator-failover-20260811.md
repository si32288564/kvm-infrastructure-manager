# P1-D03 Upgrade Coordinator Process / HA Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-032`, `FI-UPG-022`
- Invariant: `INV-UPG-025`
- Runtime: real `kim-upgrade-coordinator` processes、PostgreSQL 17 synchronous primary / standby、Go race detector

## Fault Sequence

```text
Coordinator A process
→ Campaign claim generation 1 / EXECUTE
→ DB-time claim renewal
→ API Target SUCCEEDED
→ immutable canary HOLD（success 1 / pending 2）
→ Coordinator A SIGKILL
→ PostgreSQL primary hard stop
→ synchronous standby promote
→ renewed expiry retained
→ Coordinator B waits for expiry
→ Campaign claim generation 2 / RECOVER_FROM_DB
→ Coordinator A stale Result rejected
→ remaining canary Target Results committed
→ canary CONTINUE
→ Campaign ROLLING / batch-1
```

## Result

| Assertion | Result |
|---|---|
| pre-failover committed LSN replay | PASS |
| `restore_epoch` / database authority generation | unchanged |
| Coordinator Attempt | 2 |
| successor claim mode | `RECOVER_FROM_DB` |
| `COORDINATOR_UNKNOWN` | 1 |
| accepted Target Result Event | 3 |
| renewal evidence | 1 以上 |
| stale generation 1 Result | rejected |
| duplicate canary Decision for identical counts | 0 |
| final Campaign | `ROLLING / batch-1` |

`HOLD` polling は Target counts と evaluator evidence が不変な間、同じ immutable Decision を返し、Campaign generation や Event を増幅しなかった。Coordinator process / PostgreSQL role の liveness は upgrade authority として使用していない。

## Command

```bash
make test-p1d03-upgrade-coordinator-failover
```

## Boundary

この qualification は Coordinator process lifecycle、DB-time renewal、same-Site PostgreSQL HA、canary Decision recovery を対象とする。Target component executor、canary Decision commit 後 response-loss の決定的 proxy、Wave hard drain、Campaign abort / rollback、long soak は後続 gate である。
