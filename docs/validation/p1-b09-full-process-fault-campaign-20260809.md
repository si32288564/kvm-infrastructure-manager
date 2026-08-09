# P1-B09 Full Process Fault Campaign Validation

日付: 2026-08-09

## 1. Scope

P1-B08 の in-process JetStream fixture を、実 product executable と独立 OS process を使用する fault campaign へ拡張した。

```text
PostgreSQL 17 container
        ↑
kim-worker process
        ↓ TLS + JWT credentials
3 node NATS Server process cluster
        ↓ durable consumer
kim-agent-gateway process
        ↓ outbound Agent mTLS
kim-host-agent process
        ↓
state-marker backend + journal + spool
```

`natsnode` helper は qualification 専用であり、product component または deployment artifact ではない。NATS node ごとに独立 listener、file store、PID を持ち、実 process signal で停止する。

`faultproxy` helper も qualification 専用である。Agent と実 Gateway の間で opaque TLS record を転送し、arm 後に Command を含む最初の downstream record だけを転送して後続 downstream record を破棄する。TLS を終端せず、Host identity、session authority、message payload のいずれも解釈しない。

## 2. Fault Sequence

```text
TLS/JWT credential cluster formation
  ↓
Agent session generation 1 + explicit Host arming
  ↓
current JetStream leader process kill
  ↓
new leader election + Command 1 Result/Verification/Receipt
  ↓
Gateway process stop/restart
  ↓
Agent reconnect generation 2 + explicit Host rearm
  ↓
Command 2 Result/Verification/Receipt
  ↓
Agent process stop
  ↓
Command 3 route UNKNOWN + NAK
  ↓
Agent restart/session generation 3
  ↓
old authority/Lease fence
  ↓
stale Bus redelivery terminal convergence without backend side effect
  ↓
Agent restart/session generation 4 + explicit Host rearm
  ↓
faultproxy arm + Command delivery
  ↓
Result/Observation/Receipt PostgreSQL commit
  ↓
Receipt transport response loss + Agent spool retained
  ↓
Gateway/Agent restart/session generation 5
  ↓
same message identity/digest replay
  ↓
original generation 4 Receipt recovery + one-time spool delete
  ↓
session generation 5 + explicit Host rearm
  ↓
qualification advisory-lock barrier
  ↓
Gateway live Agent stream write
  ↓
Result/Observation/Receipt commit + Job success
  ↓
Gateway hard-kill before JetStream ACK
  ↓
Gateway restart + Agent session generation 6
  ↓
same Bus message redelivery
  ↓
terminal authority revalidation + no Agent re-route
```

## 3. Validation Results

| Case | Result |
|---|---|
| NATS Server 2.12.12、3 OS processes、TLS server identity、JWT user credentials | PASS |
| stream/consumer 3 replica provisioning | PASS |
| current stream leader OS process kill | new leader elected: PASS |
| leader change 後の protected Command delivery | Job `SUCCEEDED`: PASS |
| PostgreSQL-backed Agent application Receipt | durable spool empty: PASS |
| `kim-agent-gateway` OS process stop/restart | PASS |
| Agent reconnect | session generation 1 → 2: PASS |
| Gateway restartだけによる implicit rearm | 発生しない。explicit arm generation 2 が必要: PASS |
| generation 2 の protected Command | Job `SUCCEEDED`、spool empty: PASS |
| live Agent process absent | `ROUTE_UNKNOWN` + NAK/redelivery: PASS |
| Agent process restart | session generation 3: PASS |
| generation 2 Lease | Command `UNKNOWN`、Lease `FENCED`: PASS |
| stale Bus redelivery | consumer pending/ack-pending 0 へ terminal convergence: PASS |
| stale Command backend marker | absent: PASS |
| Receipt response-loss proxy activation | Command 到達後に downstream TLS record を遮断: PASS |
| response loss 前の domain decision | Job `SUCCEEDED`、Receipt exactly 1: PASS |
| Receipt 未受信の Agent spool | exactly 1 message retained: PASS |
| Gateway/Agent restart | session generation 4 → 5: PASS |
| stable replay | same message identity/digest で original accepted generation 4 Receipt を回収: PASS |
| replay 後の Receipt | exactly 1 row、accepted generation 4 のまま: PASS |
| replay 後の Agent spool | empty、一度だけ削除: PASS |
| pre-ACK qualification barrier | `ROUTE_ACCEPTED` transaction が advisory lock wait: PASS |
| barrier 中の Agent delivery | Job `SUCCEEDED`、Receipt exactly 1、spool empty: PASS |
| Gateway process hard-kill | live stream write 後、JetStream ACK 前: PASS |
| Gateway/Agent recovery | session generation 5 → 6、implicit Host rearm なし: PASS |
| durable Bus redelivery | terminal PostgreSQL authority で Agent へ再 route せず ACK: PASS |
| route evidence | `ROUTE_STARTED` exactly 1、killed `ROUTE_ACCEPTED` 0: PASS |
| duplicate execution | Lease/Attempt/Receipt は各 1、backend marker digest/mtime 不変: PASS |
| Bus convergence | consumer pending/ack-pending 0: PASS |
| transient JetStream consumer leadership change | bounded retry 後に同一 durable consumer で収束: PASS |
| extended campaign repeat | fresh PostgreSQL を再作成した 2 consecutive runs: PASS |
| normal test lane without dedicated PostgreSQL | explicit skip: PASS |

## 4. Authority Boundary

- NATS leader election は Lease、Attempt、Host authority generation を生成しない。
- Gateway listener の復旧は Agent session または Host mutation authority を arm しない。
- Agent reconnect は PostgreSQL `SessionAccepted` により new session generation を得るが、Host authority は explicit arm まで fenced のままである。
- NATS ACK は Agent application Receipt ではない。成功 Command では PostgreSQL-backed Receipt を Agent が受け取った後だけ spool entry が削除される。
- live Agent がない message は NAK される。再接続で元 Lease が stale になった場合は Agent へ渡さず terminal handling する。
- Receipt transport response loss は Result/Observation/Receipt commit を取り消さない。Agent は spool entry を保持し、new session generation から stable message identity/digest を replay する。
- replay は accepted session generation を current generation へ書き換えない。original generation 4 の immutable Receipt を回収した後だけ、Agent は spool entry を一度削除する。
- JetStream consumer leadership change または一時的な unavailable/timeout は ACK 成功または authority evidence と解釈せず、bounded retry する。
- qualification-only trigger は `ROUTE_ACCEPTED` transaction を advisory lock で停止するだけで、product runtime に fault flag を追加しない。実 Gateway の stream write 完了後、Handler が JetStream ACK disposition を返す前に process を hard-killする。
- ACK 前の hard-kill 後も redelivery は新しい Lease/Attempt を作らない。既に terminal となった Command を current PostgreSQL authority で拒否し、Agent または backend へ再配送しない。

## 5. Remaining Qualification

- PostgreSQL HA failover を同時に含む campaign
- NATS network partition、rolling restart、TLS/JWT credential rotation、large backlog pressure
- state-marker ではなく実 libvirt backend を使用する Host kill/read-back campaign

本増分により、NATS leader、Gateway、Agent の OS process fault、stale authority redelivery、Receipt commit 後の response loss、new generation replay、Gateway stream write/JetStream ACK 間の hard-kill は一つの distributed runtime campaign で qualification 済みとなった。delivery side の主要 ambiguity gate は閉じ、次の主要 gate は実 libvirt Host kill/UNKNOWN/read-back である。
