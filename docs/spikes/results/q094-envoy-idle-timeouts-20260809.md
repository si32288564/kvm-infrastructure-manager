# Q-094 Envoy Idle Timeout Profiles — 2026-08-09

## Scope

Envoy `v1.38.0` の HTTP/2 connection idle timeout と stream idle timeout を別 run で検証しました。両 run は同じ pinned Envoy image、TLS 1.3、Agent downstream mTLS、proxy→Gateway mTLS、sanitized JSON XFCC、PostgreSQL Session Grant を使用します。

- image: `envoyproxy/envoy:v1.38.0`
- digest: `sha256:8146b97ee61a42cd216514709e4e3198af75f014974e3d9f310aef9c901fcbdf`
- Agent: 100
- environment: single local Host、non-certifying run

## Profile A: Connection Idle Timeout

- connection idle timeout: 1 s
- stream idle timeout: disabled
- generation 1 を 2 s 無通信で保持
- 2 s 後に同じ 100 stream で typed Heartbeat probe を送信

| Metric | Result |
|---|---:|
| hold duration | 2 s |
| live stream probes | 100 / 100 |
| Envoy downstream connection idle timeout | 0 |
| Envoy downstream stream idle timeout | 0 |
| generation 2 convergence | 259 ms |
| generation 2 open p50 / p95 / p99 | 169 / 249 / 255 ms |
| current generation 2 rows | 100 |
| DB pool waits | 0 |

active gRPC stream があるため、connection idle timeout は発火しませんでした。timeout 値を超えたことを Host session expiry と解釈せず、generation 1 stream は 100/100 で message delivery を継続できました。

## Profile B: Stream Idle Timeout

- connection idle timeout: disabled
- stream idle timeout: 1 s
- generation 1 stream 上で application message を送信せず passive reset を待機

| Metric | Result |
|---|---:|
| Agent-observed stream resets | 100 / 100 |
| Envoy `downstream_rq_idle_timeout` | 100 |
| Envoy `downstream_cx_idle_timeout` | 0 |
| generation 2 convergence | 423 ms |
| generation 2 open p50 / p95 / p99 | 213 / 308 / 392 ms |
| mean / maximum reconnect attempts | 3.07 / 5 |
| current generation 2 rows | 100 |
| DB pool waits | 0 |

stream idle reset は transport loss として観測されました。resource authority や generation を proxy が変更せず、Agent は bounded reconnect を開始し、generation 2 の PostgreSQL Grant commit 後だけ current session を公開しました。

## Product Profile Decision

Developer Preview の Envoy Agent route は stream idle timeout を無効化します。Agent liveness は typed application Heartbeat、session health、PostgreSQL current generation で管理します。

stream idle timeout を有効化する将来 profile は、maximum Heartbeat interval、jitter、proxy/clock uncertainty より十分大きな値を certification しなければなりません。HTTP/2 PING や connection 生存だけを Agent application liveness または authority の証拠にしません。

## Q-094 Status

connection idle / stream idle semantics は `PASS` です。Q-094 全体は durable spool、Receipt loss、Gateway restart、bounded resync convergence、production Credential Binding、typed HTTP/2 authority comparison が残るため `HOLD` を維持します。
