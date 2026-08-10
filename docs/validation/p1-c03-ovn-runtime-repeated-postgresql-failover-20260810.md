# P1-C03 OVN Runtime Repeated PostgreSQL Failover Validation

- 対象: long-running OVN work renewal と同一 Site PostgreSQL HA の繰り返し切替
- 状態: PASS / P1-C03 In Progress
- Test Contract: `AT-NET-042`, `FI-DB-006`
- Invariant: `INV-NET-032`, `INV-NET-033`, `INV-NET-036`

## 1. Result

一時 Docker 上の PostgreSQL 17 primary A / synchronous standby B で、long-running typed OVN apply が claim を renewal した後に A を hard stop し B を promote しました。generation 1 の worker は DB authority error で停止し、B 上の generation 2 worker は replicated renewed expiry 後に `READ_BACK_FIRST` を取得し、既存 object の matching read-back から duplicate apply なしで `OBSERVED` へ収束しました。

次に旧 A の data volume を削除し、B の current authority から `pg_basebackup` で再構築して synchronous standby として参加させました。別の long-running work を B 上で renewal した後に B を hard stop し、再参加した A を promote して同じ recovery contract を逆方向に検証しました。削除対象は qualification 用の一時 Docker volume だけであり、外部 PostgreSQL、OVN、KVM Host は使用していません。

```text
cycle 1
primary A → renewal remote_apply → A hard stop → B promote
→ old worker stop → generation 2 READ_BACK_FIRST → OBSERVED

rejoin
B current authority → fresh pg_basebackup → A synchronous standby

cycle 2
primary B → renewal remote_apply → B hard stop → A promote
→ old worker stop → generation 2 READ_BACK_FIRST → OBSERVED
```

## 2. Accepted evidence

| Evidence | Cycle 1 | Cycle 2 |
|---|---:|---:|
| pre-failover committed LSN | `0/343E908` | `0/5007388` |
| renewal evidence | 14 | 8 |
| attempts | 2 | 2 |
| `DISPATCH_UNKNOWN` | 1 | 1 |
| `READ_BACK_STARTED` | 1 | 1 |
| `APPLY_AUTHORIZED` | 1 | 1 |
| physical apply | 1 | 1 |
| recovery mode | `READ_BACK_FIRST` | `READ_BACK_FIRST` |

両 cycle で `restore_epoch=ovn-repeated-failover-1786368476092232000`、`database_authority_generation=1` を維持しました。LSN と restore epoch の文字列はこの qualification run の evidence であり、製品 default や固定 identity ではありません。

## 3. Additional hardening

この campaign で qualification 共通 process helper の `stop()` が二重 cleanup 時に完了済み channel を再待機する問題を検出しました。cleanup を `sync.Once` で冪等化し、明示 stop と `defer` cleanup が重なっても fixture が停止しないよう修正しました。

また、authority seed helper が既存 `database_authority` を各 work の suffix で上書きしていたため、fresh DB でのみ singleton を初期化し、既存 authority は `ON CONFLICT DO NOTHING` で維持するよう修正しました。これにより qualification data の追加が restore epoch や authority generation を暗黙再発行しません。

## 4. Verification

```text
make test-p1c03-ovn-worker-repeated-db-failover: PASS
go test ./internal/qualification/p1c03ovnwork: PASS
```

正規 checkout の実 fault run は 89.78 s で完了しました。fixture 終了後、一時 PostgreSQL container、network、data volume は cleanup します。

## 5. Remaining hardening

- sustained OVN endpoint latency / partial timeout
- PostgreSQL pool saturation boundary と overload admission
- worker scale up / drain / down
- repeated failover を含む長時間 soak と resource trend
- production metrics、alarm、diagnostic bundle

これらは今回の repeated failover correctness gate を無効にしません。
