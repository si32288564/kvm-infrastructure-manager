# Q-094 Session Density Curve — 2026-08-09

- 状態: Preliminary / Non-certifying
- Host: Apple M2 / darwin arm64、24 GB memory、8 core
- Go: 1.26.5
- FD soft limit: 1,048,575
- 対象: gRPC bidirectional stream、typed protobuf stream over HTTP/2

## Purpose

100〜10,000 の long-lived mTLS session に対する heap、goroutine、FD、open/reconnect time の傾向を比較します。KIM の初期想定である約 100 Host と、将来の multiple Site / SaaS 集約を同じ fixture で観測します。

## Method and Limits

候補ごとに別 process を起動し、100 / 1,000 / 2,500 / 5,000 / 10,000 session を測定しました。各 session は五つの synthetic capability を一つの handshake に集約します。

各 run は generation 1 の全 session を確立して echo を検証し、GC 後の Go heap、goroutine、process FD を記録します。その後、全 connection が 0 へ drain したことを確認してから generation 2 を再確立し、全 session で再度 echo を検証します。

fixture は Agent client と echo Gateway server を同じ process に含みます。したがって resource 値は両端の合計であり、Gateway 単体 capacity ではありません。CPU utilization、RSS、DB session registry、admission/backoff、jitter、proxy/LB、network impairment は含みません。

## Resource Curve

| Sessions | gRPC goroutine | HTTP/2 goroutine | gRPC heap alloc | HTTP/2 heap alloc | Process FD |
|---:|---:|---:|---:|---:|---:|
| 100 | 1,102 | 602 | 13.0 MB | 8.0 MB | 210 |
| 1,000 | 11,002 | 6,002 | 123.3 MB | 75.8 MB | 2,010 |
| 2,500 | 27,502 | 15,002 | 305.9 MB | 187.2 MB | 5,010 |
| 5,000 | 55,002 | 30,002 | 614.2 MB | 373.7 MB | 10,010 |
| 10,000 | 110,002 | 60,002 | 1,231.0 MB | 747.3 MB | 20,010 |

両候補とも session 数に対してほぼ線形です。この両端 fixture では、1 session あたり概算で gRPC は約 11 goroutine / 122 KiB heap、typed HTTP/2 は約 6 goroutine / 74 KiB heap を使用します。FD は両候補とも約 2 FD/session です。

## Open and Reconnect

| Sessions | gRPC open | HTTP/2 open | gRPC drain + reconnect | HTTP/2 drain + reconnect |
|---:|---:|---:|---:|---:|
| 100 | 30 ms | 27 ms | 42 ms | 37 ms |
| 1,000 | 258 ms | 221 ms | 310 ms | 232 ms |
| 2,500 | 609 ms | 1,494 ms | 731 ms | 827 ms |
| 5,000 | 1,762 ms | 2,072 ms | 2,049 ms | 1,256 ms |
| 10,000 | 4,052 ms | 4,459 ms | 5,198 ms | 3,678 ms |

全 run で次を確認しました。

- generation 1 の connection は generation 2 open 前に 0 へ drain
- cumulative accepted connection は `2 × session count`
- generation 2 後の active connection は session count と一致
- open、echo、close、reconnect に error なし

open/reconnect time と echo latency は負荷点ごとに優位候補が入れ替わり、単一 run の scheduler/GC 影響を含みます。resource curve と異なり、安定した candidate 差とは扱いません。

## Interpretation

typed HTTP/2 は 10,000 session で gRPC より約 50,000 goroutine、約 484 MB の Go heap を削減し、明確な **density leader** です。

一方、gRPC も 10,000 session の generation handoff を resource exhaustion や connection leak なしで完了しました。特に初期想定の 100 Host では、この両端 fixture の差は約 500 goroutine / 5 MB heap です。現時点の証拠では、gRPC の resource overhead は採用を棄却する水準ではありません。ただし CPU/RSS と実 Gateway handler/DB を含む qualification は別途必要です。

bulk saturation では gRPC が priority tail latency と completion で優位なため、現時点の位置付けは次です。

- gRPC: operational/control-path leader
- typed HTTP/2: density leader
- Decision: `HOLD`

## Remaining Decision Gates

- Envoy/HAProxy の GOAWAY、drain、idle timeout、rolling restart
- Gateway admission、exponential backoff、jitter、DB session registry contention を含む reconnect storm
- 10,000 session の CPU/RSS/GC profile と Gateway-only resource 分離
- credential renewal overlap、durable spool、resync convergence
