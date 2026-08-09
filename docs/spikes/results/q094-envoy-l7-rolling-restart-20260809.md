# Q-094 Envoy L7 Rolling Restart — 2026-08-09

## Scope

Envoy `v1.38.0` を gRPC / HTTP/2 L7 proxy とし、Agent mTLS termination、sanitized XFCC、proxy→Gateway mTLS、HTTP/2 GOAWAY、graceful drain、process replacement、generation 2 reconnect を一つの fixture で検証しました。

- image: `envoyproxy/envoy:v1.38.0`
- image digest: `sha256:8146b97ee61a42cd216514709e4e3198af75f014974e3d9f310aef9c901fcbdf`
- configuration: TLS 1.3 / HTTP/2、`SANITIZE_SET`、JSON XFCC、1 s drain time
- run: 100 Host、single local non-certifying run

公式 Envoy documentation では、HTTP/2 drain は初回 GOAWAY と final GOAWAY を使用し、active request/stream の完了と connection termination を分離します。このため fixture は GOAWAY 送信だけで active Agent stream が消えるとは仮定せず、rolling process replacement まで実行します。

## Trust Path

```text
Agent certificate
  ↓ Envoy downstream mTLS verification
SANITIZE_SET JSON XFCC certificate hash
  ↓
Envoy workload certificate
  ↓ Gateway upstream mTLS + leaf SHA-256 pin
Gateway trusted-proxy resolver
  ↓
SessionHello / PostgreSQL Grant
```

Gateway は XFCC header 単独を受理しません。verified upstream mTLS peer の leaf certificate SHA-256 が許可済み proxy digest と一致し、XFCC が exactly one JSON element / valid SHA-256 の場合だけ downstream certificate evidence を Session Attempt へ保存します。production の Host identity ↔ Credential Binding verifier は引き続き別 blocker です。

## Transition

1. generation 1 の 100 Agent session を Envoy 経由で PostgreSQL Grant する。
2. Envoy admin drain で HTTP/2 GOAWAY / graceful drain を開始する。
3. active long-lived stream が 100 のままであることを観測する。GOAWAY 単独を session termination と解釈しない。
4. old Envoy process を 3 s grace で停止し、全 100 Agent が transport loss を観測する。
5. 同一 endpoint / certificate profile で replacement Envoy を起動する。
6. Agent は bounded backoff で generation 2 を開始し、new PostgreSQL Grant へ収束する。

## Result

| Metric | Result |
|---|---:|
| generation 1 active streams before replacement | 100 |
| Agent-observed disconnects | 100 |
| generation 2 convergence | 478 ms |
| generation 2 throughput | 209 sessions/s |
| generation 2 open p50 / p95 / p99 | 266 / 344 / 457 ms |
| mean / maximum attempts | 3.15 / 5 |
| application admission rejects | 215 |
| Gateway upstream TLS handshake peak | 8 |
| Gateway upstream physical connections total / active | 16 / 8 |
| current generation 2 rows | 100 |
| DB pool waits | 0 |

Session Attempt evidence には `via_trusted_proxy=true`、Agent downstream certificate hash、Envoy upstream leaf certificate hash が永続化されました。Envoy upstream HTTP/2 connection pool は Host session 数と一致せず、100 Host に対して replacement 後の Gateway-side active physical connection は 8 でした。この pool 数を Host session authority に使用していません。

## Assertions

- pinned proxy mTLS + sanitized downstream certificate evidence: `PASS`
- GOAWAY を session failure/authority transition と誤認しない: `PASS`
- rolling replacement の全 Agent disconnect detection: `PASS`
- generation 2 の PostgreSQL Grant 再取得: `PASS`
- DB pool bounded: `PASS`
- Envoy idle timeout profile: `NOT RUN`
- durable spool / Receipt loss / resync: `NOT RUN`

## Decision

Envoy L7 rolling restart profile は限定条件で `PASS` です。Q-094 全体は idle timeout、durable spool/resync、production Credential Binding、typed HTTP/2 authority comparison が残るため `HOLD` を維持します。
