# P1-C03 OVN Worker Scale / Drain / Observability Validation

- 対象: worker scale up、graceful drain、scale down、bounded Prometheus metrics
- 状態: PASS / P1-C03 In Progress
- Test Contract: `AT-NET-044`, `AT-O11Y-002`, `FI-NET-030`
- Invariant: `INV-NET-032`, `INV-NET-033`, `INV-NET-035`, `INV-NET-038`

## 1. Runtime lifecycle

`Worker.RunWithDrain` を追加し、drain signal と hard cancellation を分離しました。drain signal は metrics state を `DRAINING` に変更し、次の claim loop へ入ることを止めます。既に取得済みの batch は同じ claim generation で renewal、typed apply/read-back、completion を継続します。

```text
ACTIVE
→ first SIGTERM / SIGINT
→ DRAINING
→ new claim stopped
→ current batch renewal + completion
→ STOPPED

drain timeout / second signal
→ hard context cancellation
→ outcome is not inferred
→ expiry / DISPATCH_UNKNOWN / READ_BACK_FIRST
```

production `kim-network-worker` は `drain-timeout >= claim-maximum-lifetime` を起動時に要求します。最初の termination signal は drain channel だけを close し、drain deadline または 2 回目の signal だけが hard context を cancel します。

## 2. Scale / drain qualification

fresh PostgreSQL 17 に 64 work を投入し、2 workers で開始した後に 4 workers を追加しました。全 4 scale-up workers の claim participation を確認してから、current batch と renewal を持つ initial worker 1 台を drain しました。

```text
items=64
initial_workers=2
scaled_workers=4
owners=6
attempts=64
DISPATCH_UNKNOWN=0
physical_apply=1 per object
renewals=439
drained_owner_claims=2 before/after drain
drain_duration=708.394 ms
```

drained worker は new claim を取得せず、current 2 claims を完了して `STOPPED` / in-flight 0 になりました。残り 5 workers は全 work を `OBSERVED` へ収束し、その後それぞれ graceful drain で停止しました。全 worker の metrics は `STOPPED`、in-flight 0、fatal errors 0 です。

## 3. Metrics

任意の管理者設定 `metrics-listen-address` で Prometheus text endpoint を公開します。公開 metric は bounded label だけを使用します。

- lifecycle: `STARTING / ACTIVE / DRAINING / STOPPED`
- claim runs/errors/total、in-flight、completed
- item-local / fatal errors
- claim latency、oldest in-flight age
- renewal total/errors/latency/headroom
- drain duration
- PostgreSQL pool acquired/idle/total、empty acquire、cumulative acquire wait
- authority backlog: `PENDING / CLAIMED / DISPATCH_UNKNOWN / OBSERVED`
- authority scrape error

Host ID、Port ID、Work ID、claim owner、secret、生 backend error は metric label/value に含めません。metrics scrape failure は `authority_scrape_error=1` として観測し、worker execution または DB authority decision を変更しません。

## 4. Fault found during qualification

race detector 付き scale/drain fixture で、worker function の return 後に metrics watcher が遅れて実行され、terminal `STOPPED` を `DRAINING` へ戻す競合を検出しました。watcher stop channel だけで終了を推測せず、watcher completion を待つ ordering fence を追加してから最後に `STOPPED` を確定するよう修正しました。

この fault は backend authority を変更しませんでしたが、operator が停止済み worker を draining と誤認するため、acceptance 対象外として修正しました。修正後の race detector 付き実 PostgreSQL fixture は PASS しています。

## 5. Verification

```text
make test-p1c03-ovn-worker-drain: PASS
go test -race ./cmd/kim-network-worker ./internal/network/ovnruntime: PASS
make check: PASS
```

一時 PostgreSQL container は fixture 終了時に cleanup します。外部 OVN、KVM Host、本番 resource は使用しません。

## 6. Remaining hardening

- hard drain deadline / second-signal の実 process fault campaign
- alert thresholds と runbook
- repeated failover、latency、scale churn を組み合わせた長時間 soak
- OpenTelemetry trace propagation

これらは今回の graceful scale/drain correctness gate を無効にしません。
