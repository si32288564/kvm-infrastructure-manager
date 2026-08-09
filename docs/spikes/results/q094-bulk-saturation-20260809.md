# Q-094 Bulk Saturation Result — 2026-08-09

- 状態: Preliminary / Non-certifying
- Host: Apple M2 / darwin arm64
- Go: 1.26.5
- 対象: gRPC bidirectional stream、typed protobuf stream over HTTP/2
- run 数: 各候補 3

## Purpose

Inventory/Resync が application queue と transport flow-control path を飽和させている間に、Control/Result traffic が bounded latency で進むかを比較します。単なる transport throughput ではなく、KIM の実 `PriorityQueue` が選んだ次 message が secure transport と slow Gateway reader を越えて返るまでを測定します。

## Fixture

- 一つの long-lived mTLS session
- Inventory と Resync を各 256、合計 512 message
- 各 bulk payload は 256 KiB、初期 backlog は 128 MiB
- Control と Result を交互に 100 message
- priority publish interval は 5 ms
- `MaxConsecutivePriority = 8` とし、bulk にも bounded service を保証
- echo Gateway は各 frame read 前に 1 ms wait する slow-reader
- latency は priority message の enqueue 直前から echo receipt まで

次で候補ごとに独立 process を 3 回実行しました。

```sh
go run ./cmd/kim-agent-transport-scale -mode hol -candidate grpc
go run ./cmd/kim-agent-transport-scale -mode hol -candidate http2
```

## Result

| Metric range across 3 runs | gRPC bidi | typed HTTP/2 |
|---|---:|---:|
| priority p50 | 4.000–5.976 ms | 8.237–8.845 ms |
| priority p95 | 4.659–7.107 ms | 9.454–15.402 ms |
| priority p99 | 4.824–10.091 ms | 9.701–77.432 ms |
| priority maximum | 4.932–15.815 ms | 9.925–88.401 ms |
| all-message completion | 693.6–727.9 ms | 989.1–1,040.5 ms |
| queue peak | 512 / 128 MiB | 512 / 128 MiB |

typed HTTP/2 の 1 run では p99 77.4 ms の outlier がありました。他 2 run の p99 も 9.7–11.4 ms で、gRPC の 4.8–10.1 ms より高い値です。fixture、Host scheduler、Go runtime を分離した原因分析はまだ行っていないため、outlier を candidate 固有 defect と断定しません。

## Interpretation

1,000-session steady-state fixture では typed HTTP/2 が goroutine、heap、concurrent echo latency で優位でした。一方、この single-session slow-reader fixture では gRPC が priority latency と total completion の双方で優位でした。

したがって現時点では次の二軸評価とします。

- gRPC: operational/control-path leader。今回の flow-control/HOL fixture で優位。
- typed HTTP/2: density leader。slow-reader 下の tail latency は追加調査が必要。

この結果は proxy/LB、network loss、Gateway DB 処理、durable spool、multiple concurrent stream workload を含まないため、Decision は `HOLD` のままです。

## Next Blocking Work

- Envoy/HAProxy の idle timeout、GOAWAY、drain、rolling restart
- Gateway admission/backoff と jitter を含む reconnect storm
- credential renewal の old/new session overlap
- slow consumer と response loss を durable spool/resync へ接続
- 10,000-session resource model と CPU/RSS/GC
