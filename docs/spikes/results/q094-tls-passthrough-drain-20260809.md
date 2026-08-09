# Q-094 TLS Passthrough Proxy Drain — 2026-08-09

## Scope

real gRPC / mTLS Agent transport と PostgreSQL-backed Session Grant の間へ、opaque TCP passthrough proxy fixture を追加しました。proxy は TLS を終端せず、Host identity、certificate verification、session generation、application admission、DB authority を一切判断しません。

```text
Agent
  ↓ outbound mTLS
TLS passthrough proxy
  ↓ opaque TCP forwarding
Gateway pre-auth limiter
  ↓ verified mTLS
application admission
  ↓
PostgreSQL Session Grant
```

この fixture は L4 passthrough LB の connection drain / rolling replacement 相当を検証します。Envoy/HAProxy の L7 HTTP/2 termination、GOAWAY、idle timeout、stream rebalance を証明するものではありません。

## Assertions

- TLS peer identity は proxy で終端せず Gateway まで到達する。
- generation 1 の current stream を proxy drain すると、全 Agent が transport loss を観測する。
- drain は authority decision ではなく、generation を進めない。
- Agent は bounded exponential backoff + full jitter で再接続する。
- generation 2 は application admission と PostgreSQL Grant commit の後だけ current になる。
- DB pool exhaustion や unbounded TLS handshake concurrency を発生させない。

## Fixture

- Agent: 1,000
- transport: gRPC bidirectional stream / TLS 1.3 mutual authentication
- proxy: repository-owned opaque TCP forwarder
- transition: generation 1 を全接続後、proxy が current 1,000 stream を drain
- reconnect: generation 2、base 25 ms / maximum 1 s full-jitter backoff
- pre-auth TLS handshake limit: 8
- application admission: 16
- PostgreSQL pool: 32
- Gateway / proxy / Agent clients / PostgreSQL client は同一 local Host
- run: single non-certifying run

## Result

| Metric | Result |
|---|---:|
| proxy-drained generation 1 streams | 1,000 |
| Agent-observed disconnects | 1,000 |
| generation 2 convergence | 2.827 s |
| generation 2 throughput | 354 sessions/s |
| mTLS + Grant p50 / p95 / p99 | 1.656 / 2.324 / 2.515 s |
| application admission rejects | 1,307 |
| pre-auth TLS rejects | 5,205 |
| TLS handshake peak | 8 |
| generation 2 physical connections | 7,512 |
| mean / maximum Agent attempts | 7.512 / 12 |
| current generation 2 rows | 1,000 |
| DB pool waits | 0 |
| final proxy / Gateway active connections | 1,000 / 1,000 |

全 generation 1 session は drain を transport loss として観測しました。全 Host は generation 2 の新しい physical connection、mTLS verification、application admission、PostgreSQL Grant を通過して再収束しました。transport connection loss だけで generation は進まず、proxy も typed `SessionRejected` や authority event を生成しません。

## Interpretation

L4 passthrough proxy を挟んでも、credential evidence と session authority の境界は維持できました。proxy drain は current resource authority の消失を意味せず、Agent/Gateway は既存の reconnect / `UNKNOWN` / read-back semantics へ移行できます。

この run の数値を production capacity や default tuning にしません。proxy は in-process fixture であり、kernel backlog、cross-node RTT、real load balancer process、multiple Gateway replica、PostgreSQL HA を含みません。

## Remaining Decision Gates

- version と configuration を固定した Envoy/HAProxy profile での HTTP/2 GOAWAY、graceful drain、idle timeout、rolling restart
- durable spool、Receipt loss、Gateway restart、bounded resync convergence
- production Credential Binding verifier と per-Host certificate evidence
- typed HTTP/2 authority path の同条件 comparison

Q-094 は `HOLD` のままです。L4 passthrough proxy における mTLS identity preservation と hard drain/reconnect assertion は `PASS` とします。
