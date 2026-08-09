# Q-094 Real gRPC Authority Storm — 2026-08-09

- 状態: Preliminary / Non-certifying
- Host: Apple M2 / darwin arm64、24 GB memory、8 core
- PostgreSQL: 17-alpine、dedicated local container
- Storm: 1,000 Host Agent
- Transport: gRPC bidirectional stream、TLS 1.3 mutual authentication
- Gateway admission: 16
- PostgreSQL pool: 32

## Purpose

real mTLS handshake、gRPC adapter、explicit session decision、Agent backoff、Gateway admission、PostgreSQL session Grant を一つの direct path として接続します。transport liveness と authority を分離し、`Open()` は TLS/HTTP2 connection が成立しただけでは成功しません。

```text
Agent TCP/TLS/gRPC Connect
    ↓ SessionHello
Gateway verified peer certificate
    ↓ bounded admission
PostgreSQL Host-scoped Grant transaction
    ├─ rejected → SessionRejected → connection close → Agent backoff
    └─ committed → SessionAccepted → adapter Open succeeds
```

## Protocol Hardening

versioned `Frame` に `SessionAccepted` と `SessionRejected` を追加しました。

- `SessionAccepted` は Host identity、session generation、SessionAttempt ID を返す。
- adapter は Hello と Accepted の identity/generation/Attempt が一致しなければ session を公開しない。
- `SessionRejected` は typed code、retryable、retry-after hint を返す。
- admission reject は generation を消費しない。
- proposed generation が PostgreSQL の次 generation と一致しなければ Grant transaction を rollback する。
- superseded Attempt、artifact/credential/protocol/evidence conflict は再利用しない。

fixture certificate は全 Agent が共有する test certificate です。verified mTLS peer evidence は取得していますが、Host identity と Certificate Binding の production-grade 一致検証を certification するものではありません。

## Aggressive Backoff Result

最初は DB-only fixture と同じ 2 ms base / 100 ms max、Gateway retry hint 2 ms を使用しました。

| Metric | Result |
|---|---:|
| convergence | 5.814 s |
| throughput | 172 session/s |
| mTLS + Grant open p50 | 4.048 s |
| p95 | 5.665 s |
| p99 | 5.746 s |
| storm admission rejects | 15,988 |
| storm mean / max attempts | 16.99 / 38 |
| warm + storm physical connections | 39,709 |
| DB pool empty acquire | 0 |

DB pool は飽和していません。短い retry が mTLS handshake と connection を増幅し、transport/Gateway CPU path が支配的になりました。

## Transport-aware Backoff Result

base 25 ms / max 1 s、Gateway retry hint 25 ms へ変更して再測定しました。これは tuning candidate であり、production default の決定ではありません。

| Metric | Result | Change from aggressive |
|---|---:|---:|
| convergence | 3.291 s | -43% |
| throughput | 304 session/s | +77% |
| mTLS + Grant open p50 | 1.913 s | -53% |
| p95 | 2.581 s | -54% |
| p99 | 2.903 s | -49% |
| storm admission rejects | 5,050 | -68% |
| storm mean / max attempts | 6.05 / 10 | -64% mean |
| warm + storm physical connections | 12,605 | -68% |
| DB pool empty acquire | 0 | unchanged |

両 profile で 1,000 Host 全てが `CURRENT / session_generation = 2` へ収束し、warm generation の connection は storm 前に 0 へ drain しました。storm 完了後の active physical connection は 1,000 です。

## Interpretation

DB-only admission profile と real mTLS transport profile は backoff tuning を共有できません。rejected connection も TLS handshake cost を支払うため、短い retry は Gateway admission の外側で connection amplification を起こします。

固定すべき contract は具体的な 25 ms / 1 s ではなく、次です。

- Agent reconnect は bounded exponential backoff、jitter、Gateway retry hint の最大値を使用する。
- transport profile は handshake/connection cost を含めて tuning/certification する。
- Gateway は application admission に加え、pre-auth connection/TLS handshake の bounded resource protection を持つ。
- pre-auth limit は session authority、Host identity、credential validity の代替判定をしない。
- Agent adapter は explicit PostgreSQL-backed Accepted を受けるまで current session を公開しない。

## Remaining Decision Gates

- Envoy/HAProxy の GOAWAY、drain、idle timeout、rolling restart
- pre-auth connection/TLS handshake limiter と SYN/backlog/DoS profile
- production Credential Binding verifier と per-Host certificate evidence
- multiple Gateway replica と PostgreSQL HA path
- durable spool、response loss、resync convergence
- typed HTTP/2 authority path の同条件 comparison

Q-094 は `HOLD` のままです。ただし Developer Preview の gRPC candidate は、direct authority path まで成立しました。
