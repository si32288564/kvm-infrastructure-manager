# P1-B08 Real JetStream Failover Validation

日付: 2026-08-09

## 1. Scope

P1-B07 で実装した Worker → JetStream → Gateway delivery contract に対して、実 NATS Server の JetStream/Raft と durable consumer を使用した failover fixture を追加した。

```text
stable command-delivery message
  ↓ publish + duplicate publish
3 replica JetStream stream / consumer
  ↓ stream leader stop
surviving replica leader election
  ↓ durable consumer delivery
Gateway handler NAK
  ↓ consumer stop / restart
same durable consumer redelivery
  ↓ handler ACK
no pending message
```

NATS Server はテスト process 内で 3 つの独立 server instance、listener、file store として起動する。mock Bus ではなく実 JetStream API、Raft replication、stream leader election、durable consumer state、`Nats-Msg-Id` duplicate window を使用する。

## 2. Contract

- stream と durable consumer は 3 replica とし、fault injection 前に follower が current であることを確認する。
- 同一 message ID の再 publish は duplicate として同じ stream sequence を返し、stream message count を増やさない。
- stream leader の停止後、surviving replica が別 leader を選び、同一の 1 message を保持する。
- handler の `NAK` と Gateway consumer stop は terminal decision ではなく、同じ durable consumer への redelivery を要求する。
- consumer restart 後の `ACK` は Bus delivery の収束だけを示し、Agent Receipt、backend execution、mutation authority を証明しない。
- context cancel 中の consumer shutdown は正常停止とし、poll または `DoubleAck` の cancellation error を process failure に昇格させない。

## 3. Validation Results

使用 profile: `github.com/nats-io/nats-server/v2 v2.12.12`、3 node、file storage、stream replicas 3、consumer replicas 3、explicit ACK。

| Case | Result |
|---|---|
| cluster formation と stream/consumer replica current gate | PASS |
| same `Nats-Msg-Id` duplicate publish | same sequence、message count 1: PASS |
| current stream leader shutdown | surviving replica から別 leader を選出: PASS |
| leader failover 後の durable message | message count 1 を維持: PASS |
| first Gateway consumer | 1 delivery、`NAK`: PASS |
| consumer stop/restart | same durable consumer へ 1 redelivery: PASS |
| restarted consumer `DoubleAck` | pending 0、ack pending 0: PASS |
| targeted test repeated 3 times | PASS |
| `go test -race ./internal/messaging/natsjs` | PASS |
| `make check` | PASS |

## 4. Evidence Boundary

本 fixture は実 JetStream cluster/consumer fault semantics を検証する。P1-B07 の PostgreSQL integration は、Inbox dedupe、digest conflict quarantine、current Lease/Host/session authority revalidation、live Agent route 不在時の NAK、authority fence 後の terminal handling を別途検証済みである。

この 2 つを組み合わせても、NATS ACK を Agent Receipt または backend execution evidence とみなしてはならない。JetStream redelivery は PostgreSQL authority path を再実行する契機であり、新しい Lease/Attempt/domain decision を生成する authority ではない。

## 5. Remaining Qualification

- TLS を有効化した OS process 分離 3 node NATS cluster と `kim-worker` / `kim-agent-gateway` / `kim-host-agent` の同時 fault campaign
- Gateway kill between Agent stream write and NATS ACK
- Agent application Receipt response loss と Gateway/NATS restart を組み合わせた stable message convergence
- stale Host/session/Lease authority へ変化した後の実 cluster redelivery terminal fence
- NATS rolling restart、network partition、TLS credential rotation、backlog pressure、dead-letter operator workflow

本増分により、real JetStream/Raft leader failover と durable Gateway consumer restart は qualification 済みとなった。full multi-process Agent Receipt fault campaign は引き続き P1-B02 の hardening scope とする。
