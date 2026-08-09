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
| campaign repeat | 2 consecutive runs: PASS |
| normal test lane without dedicated PostgreSQL | explicit skip: PASS |

## 4. Authority Boundary

- NATS leader election は Lease、Attempt、Host authority generation を生成しない。
- Gateway listener の復旧は Agent session または Host mutation authority を arm しない。
- Agent reconnect は PostgreSQL `SessionAccepted` により new session generation を得るが、Host authority は explicit arm まで fenced のままである。
- NATS ACK は Agent application Receipt ではない。成功 Command では PostgreSQL-backed Receipt を Agent が受け取った後だけ spool entry が削除される。
- live Agent がない message は NAK される。再接続で元 Lease が stale になった場合は Agent へ渡さず terminal handling する。

## 5. Remaining Qualification

- Agent Result/Observation の PostgreSQL commit 後、Receipt transport response だけを失わせる full-process fault injection
- Gateway kill between live Agent stream write and NATS ACK を deterministic barrier で発生させる process fixture
- PostgreSQL HA failover を同時に含む campaign
- NATS network partition、rolling restart、TLS/JWT credential rotation、large backlog pressure
- state-marker ではなく実 libvirt backend を使用する Host kill/read-back campaign

本増分により、NATS leader、Gateway、Agent の OS process fault と stale authority redelivery は一つの distributed runtime campaign で qualification 済みとなった。Receipt response-loss と実 libvirt は独立した次の hardening gate とする。
