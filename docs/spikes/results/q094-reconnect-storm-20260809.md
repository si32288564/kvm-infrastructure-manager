# Q-094 Reconnect Storm Result — 2026-08-09

- 状態: Preliminary / Non-certifying
- Host: Apple M2 / darwin arm64、24 GB memory、8 core
- PostgreSQL: 17-alpine、dedicated local container
- Storm: 1,000 Host Agent
- Backoff: 2 ms base、100 ms maximum、deterministic full jitter

## Purpose

transport connection の open/close だけでなく、Gateway admission、Agent reconnect backoff/jitter、PostgreSQL current session authority と immutable evidence を含む reconnect storm を測定します。

この fixture は transport-independent です。gRPC/typed HTTP/2 adapter の性能を再測定するものではなく、両候補が共通利用する Gateway/DB authority path の bottleneck を特定します。

## Implemented Authority Path

```text
Agent reconnect attempt
    ↓ full-jitter exponential backoff
Gateway non-blocking admission limiter
    ↓ admitted only
PostgreSQL transaction
    ├─ database authority ACTIVE verification
    ├─ Host enrollment APPROVED verification
    ├─ immutable SessionAttempt insert
    ├─ Host-scoped transaction advisory lock
    ├─ current session row lock / generation increment
    ├─ old Attempt FENCED event
    ├─ new OPENED / CURRENT_GRANTED events
    └─ current session authority update
```

credential、live transport、DB time だけでは session authority を付与しません。Grant transaction が commit した generation だけが current です。同じ current Attempt の replay は idempotent response とし、既に supersede された Attempt の replay は `ErrSessionAttemptConflict` で拒否します。

最初の Serializable 実装では、異なる Host の空 Event 集合に対する predicate read/insert が `40001` を発生させました。Host 単位の generation 競合だけを直列化する transaction advisory lock を導入し、異なる Host は Read Committed で並行させました。同一 Host の first session も current row の不存在を迂回できません。

## Result

各 profile は warm wave で generation 1 を作成した後、1,000 Agent を同時 release して generation 2 へ reconnect させた単一 run です。

| Metric | admission 64 / DB 32 | admission 32 / DB 32 | admission 16 / DB 32 |
|---|---:|---:|---:|
| total convergence | 994.7 ms | 809.7 ms | 783.7 ms |
| throughput | 1,005 session/s | 1,235 session/s | 1,276 session/s |
| reconnect p50 | 391.1 ms | 373.3 ms | 367.1 ms |
| reconnect p95 | 957.2 ms | 729.7 ms | 695.9 ms |
| reconnect p99 | 976.6 ms | 764.8 ms | 747.5 ms |
| DB transaction p50 | 54.2 ms | 20.6 ms | 10.7 ms |
| DB transaction p95 | 117.8 ms | 45.3 ms | 15.6 ms |
| DB transaction p99 | 131.1 ms | 59.6 ms | 21.7 ms |
| admission rejects/retries | 12,277 | 11,546 | 11,974 |
| maximum Agent attempts | 31 | 27 | 27 |
| DB pool empty acquires | 925 | 0 | 0 |
| cumulative DB pool wait | 28.8 s | 0.27 ms | 0.23 ms |

全 profile で次を確認しました。

- 1,000 Host 全てが `CURRENT / session_generation = 2` へ収束
- current authority は Host ごとに一行
- immutable SessionAttempt は 2,000 行
- append-only lifecycle Event は 5,000 行
- stale generation/Attempt の暗黙 rearm なし
- Gateway admission peak は設定値を超過しない

## Interpretation

admission 64 / DB pool 32 は、Gateway 内へ入れた request の半数を DB pool 待ちにし、transaction tail と全体収束を悪化させました。少なくともこの fixture では、Gateway authority admission limit を DB pool limit より大きくしても throughput は上がりません。

admission 16 が単一 run では最良でしたが、この値を production default として固定しません。DB CPU、network latency、Gateway replica 数、他 domain transaction と競合すれば optimum は変わります。Architecture contract は次です。

- admission は bounded で、DB pool/capacity と整合する。
- admission rejection は authority failure ではなく retryable transport control である。
- Agent は bounded exponential backoff と jitter を使用する。
- rejected attempt は session generation を消費しない。
- backoff/admission は PostgreSQL Grant、Host fencing、Command Lease の代替 authority ではない。

## Remaining Decision Gates

- real mTLS accept/handshake と selected transport adapter を同じ storm へ接続
- Envoy/HAProxy の GOAWAY、drain、idle timeout、rolling restart
- multiple Gateway replica と PostgreSQL primary/HA path
- credential renewal overlap、durable spool、resync convergence
- CPU/RSS/GC、DB WAL/lock/statement profile

この結果は Gateway/DB path の成立を示しますが、Q-094 の transport Decision を Close しません。
