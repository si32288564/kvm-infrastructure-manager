# P1-C03 OVN Worker Hard Drain Process Validation

- 対象: graceful drain、2 回目の termination signal、drain deadline、read-back-first recovery
- 状態: PASS / P1-C03 In Progress
- Test Contract: `AT-NET-045`, `FI-NET-031`
- Invariant: `INV-NET-038`, `INV-NET-039`

## 1. 終了境界

production `kim-network-worker` の最初の `SIGTERM` / `SIGINT` は drain だけを開始します。新規 claim を止め、既に取得した current work の apply、read-back、renewal、completion を継続します。current work が完了した場合は終了 code 0 です。

2 回目の signal または bounded drain deadline は hard context を cancel します。hard cancellation は process を終了 code 非 0 にし、operator へ outcome が read-back まで不明であることを報告します。これは backend side effect 不在、claim revoke、即時再利用の証明ではありません。

```text
first signal
→ DRAINING
→ new claim stopped

current work completes
→ OBSERVED
→ exit 0

second signal / deadline
→ hard cancel
→ exit non-zero
→ current claim expiry
→ old attempt DISPATCH_UNKNOWN
→ successor generation READ_BACK_FIRST
→ matching object read-back
→ OBSERVED
```

## 2. 実 process Fault Injection

fresh PostgreSQL 17、実 `kim-network-worker` binary、標準 OVN CLI contract を実装する test executable を使用しました。fixture は apply 後の typed NB read-back を停止でき、物理 apply count を永続化します。

### 2.1 Graceful drain

- apply 中に最初の signal を送信
- metrics: `DRAINING`、in-flight 1
- current work を最後まで完了
- process exit code 0
- attempts 1
- `DISPATCH_UNKNOWN` 0
- physical apply 1
- final work state `OBSERVED`

### 2.2 Second signal

- apply 完了後の read-back を停止
- first signal で `DRAINING`
- second signal で hard cancel
- process exit code 非 0
- claim expiry 前の takeover なし
- successor claim generation 2 / `READ_BACK_FIRST`
- attempts 2
- `DISPATCH_UNKNOWN` 1
- `READ_BACK_STARTED` 1
- physical apply 1
- final work state `OBSERVED`

### 2.3 Drain deadline

- apply 完了後の read-back を停止
- first signal で `DRAINING`
- 2 回目の signal を送らず deadline を発火
- process exit code 非 0
- successor claim generation 2 / `READ_BACK_FIRST`
- attempts 2
- `DISPATCH_UNKNOWN` 1
- `READ_BACK_STARTED` 1
- physical apply 1
- final work state `OBSERVED`

## 3. Observability contract

hard cancellation 前の Prometheus projection で `DRAINING`、in-flight 1、`CLAIMED` backlog 1 を確認しました。metric output に Port ID または Work ID は含まれません。

hard-drained process の metrics endpoint 消失は backend outcome を意味しません。current outcome は PostgreSQL の attempt/event evidence と successor read-back から確定します。

## 4. Verification

```text
make test-p1c03-ovn-worker-hard-drain: PASS
go test -race ./cmd/kim-network-worker ./internal/network/ovnruntime: PASS
make check: PASS
```

一時 PostgreSQL container は fixture 終了時に cleanup します。外部 OVN、KVM Host、本番 resource は使用しません。

## 5. Remaining hardening

- alert threshold と operator runbook
- repeated failover、endpoint latency、scale churn を組み合わせた長時間 soak
- rolling upgrade / version skew
- OpenTelemetry trace propagation

これらは今回の real-process drain boundary と read-back-first recovery gate を無効にしません。
