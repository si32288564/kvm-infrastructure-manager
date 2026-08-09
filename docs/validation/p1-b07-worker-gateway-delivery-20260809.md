# P1-B07 Worker / Gateway Durable Delivery Validation

日付: 2026-08-09

## 1. Scope

protected Command Lease Outbox から internal NATS JetStream、Gateway Inbox、current Agent stream までの delivery authority path を実装した。

```text
Lease Grant + protected Outbox intent
  ↓ bounded kim-worker claim
JetStream publish with stable Nats-Msg-Id
  ↓ PubAck
durable Gateway consumer
  ↓ PostgreSQL Inbox dedupe
current Lease / Host authority / session revalidation
  ↓ Outbound Registry generation check
current Agent gRPC stream
```

NATS は delivery transport に限定し、resource authority を所有しない。PubAck は Bus durable acceptance、consumer ACK は Gateway terminal decision または live-stream route acceptance までの evidence であり、Agent Receipt または backend execution evidence ではない。

## 2. Persistence and Runtime

- `inbox_messages` は immutable acceptance decision とし、同一 ID の異 digest は `inbox_message_conflicts` へ quarantine evidence を追記する。
- `gateway_command_delivery_events` は `ROUTE_STARTED / ROUTE_ACCEPTED / ROUTE_UNKNOWN` を append-only に保持する。
- route attempt 採番は consumer/message scope の PostgreSQL advisory lock で直列化する。
- Worker は対象 event type だけを bounded claim し、PubAck response loss を `DISPATCH_UNKNOWN` として保持する。
- Gateway は duplicate Inbox hit でも current authority を再検証し、stable envelope を再 route できる。
- `kim-worker` と `kim-agent-gateway` は authenticated TLS NATS profile、bounded poll/claim/NAK、PostgreSQL authority path へ結線した。

## 3. Validation Results

| Case | Result |
|---|---|
| migration 001〜009 on fresh PostgreSQL 17 | PASS |
| protected Outbox → Bus payload → Inbox → Outbound Registry | PASS |
| identical Inbox duplicate | same message ID/digest/envelope で再 route: PASS |
| live session absent | `NAK` + `ROUTE_UNKNOWN`: PASS |
| current session recovery | identical redelivery route: PASS |
| Host readiness/authority fence | terminal handling、Agent route なし: PASS |
| same message ID / different digest | quarantine + `TERM`: PASS |
| PubAck response loss | `DISPATCH_UNKNOWN`、claim expiry 後に identical payload 再 publish: PASS |
| `go test ./...` | PASS |

## 4. Remaining Qualification

- production Secret Provider による delivery key rotation/revocation と old key overlap
- real NATS JetStream cluster restart、leader change、duplicate window、consumer failover fixture
- Gateway process kill between live-stream write and NATS ACK
- delivery backlog pressure、dead-letter operator workflow、metrics/alarms
- Agent application Receipt まで含む multi-process end-to-end qualification

本増分で Worker → internal Bus → Gateway → current Agent stream の runtime topology は実装上閉じた。Agent Receipt と backend execution は引き続き別 authority/evidence boundary である。
