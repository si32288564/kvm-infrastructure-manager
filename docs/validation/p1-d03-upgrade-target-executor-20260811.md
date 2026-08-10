# P1-D03 Upgrade Target Executor Fault Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-033`, `FI-UPG-023`
- Invariant: `INV-UPG-026`
- Runtime: real `kim-upgrade-coordinator`、3 + 3 real `kim-upgrade-target-executor` processes、PostgreSQL 17、Go race detector

## Fault Sequence

```text
Coordinator generation 1 / CANARY
→ 3 Target executors claim Attempt 1 / APPLY_ALLOWED
→ DB-time Target claim renewal
→ closed typed marker side effect
→ all 3 executor processes SIGKILL before Result
→ Target Lease expiry
→ 3 successor executors claim Attempt 2 / READ_BACK_FIRST
→ existing marker read-back MATCHED
→ duplicate applyなし
→ immutable Observation / Result
→ 3 Target SUCCEEDED
→ canary CONTINUE
→ Campaign ROLLING / batch-1
```

## Result

| Assertion | Result |
|---|---|
| real Target executor processes | 6 |
| canary Target Attempt evidence | 6 |
| `TARGET_UNKNOWN` | 3 |
| successor `READ_BACK_FIRST` | 3 |
| Target renewal evidence | 3 以上 |
| accepted Target Result | 3 |
| physical marker creation | Target ごとに 1 |
| stale Attempt 1 completion | rejected |
| final Campaign | `ROLLING / batch-1` |

## Command

```bash
make test-p1d03-upgrade-target-executor
```

## Boundary

この qualification は Target execution authority、process kill、multiple `UNKNOWN`、typed read-back recovery、single apply を対象とします。state marker は KIM-owned qualification backend であり、実 Debian/RPM package replacement、systemd service handoff、Host Agent binary upgrade、Campaign abort / rollback の certification ではありません。
