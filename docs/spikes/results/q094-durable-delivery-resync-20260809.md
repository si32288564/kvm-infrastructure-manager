# Q-094 Durable Delivery and Resync — 2026-08-09

## Scope

Developer Preview gRPC path で、PostgreSQL acceptance commit 後の Receipt response loss、Gateway restart、Agent spool owner restart、new session generation replay を一つの fixture へ接続しました。

## Implemented Contract

- bounded private spool、exclusive process lock、per-message size/entry/byte limit
- atomic temporary write、file fsync、rename、directory fsync
- stable `message_id + sequence_scope + sequence + payload_digest`
- transport-independent Envelope と current `session_generation` rebinding
- PostgreSQL `(host_id, message_id)` idempotency Receipt
- same message / different evidence conflict rejection
- current generation で新規 stale message を `STALE` とする fencing
- original `ACCEPTED` Receipt の generation-preserving replay
- matching Receipt 後だけ spool file を削除し directory fsync
- current session generation だけが commit できる Resync Checkpoint

## Fault Sequence

```text
spool write + fsync
  → generation 1 send
  → PostgreSQL Receipt commit
  → Receipt response を未適用のまま connection close
  → Gateway stop
  → Agent spool close/reopen
  → Gateway restart
  → PostgreSQL Grant generation 2
  → same message identity を generation 2 へ bindして replay
  → original generation 1 ACCEPTED Receiptを回収
  → spool remove + fsync
  → generation 2 Resync Checkpoint commit
```

## Result

`TestDurableDeliveryConvergesAfterReceiptLossAndGatewayRestart` は `PASS` しました。

| Assertion | Result |
|---|---|
| Receipt commit 後の response loss で spool entry を失わない | PASS |
| Agent/Gateway restart 後に pending message を再発見する | PASS |
| generation 2 replay が duplicate Receipt row を作らない | PASS（Receipt row 1） |
| replay に合わせて original accepted generation を書き換えない | PASS（accepted generation 1） |
| matching `ACCEPTED` Receipt 前に spool を解放しない | PASS |
| convergence 後に spool が空になる | PASS |
| stale generation から Resync Checkpoint を commitできない | PASS |
| current generation 2 で empty journal digestをcheckpointする | PASS |

PostgreSQL integration では、同一 message replay、digest conflict、新規 stale-generation message、stale/current Resync Checkpoint を個別にも検証しました。spool unit test では capacity limit、exclusive lock、restart recovery、conflict、stable digest を検証しました。

## Boundary

この結果は functional fault path の証拠です。multi-message resync throughput、disk-full/fsync latency、corrupt journal quarantine、production Credential Binding は P1-A qualificationで継続します。Receipt expiryを未実行証明にせず、backend read-back/UNKNOWNを置換しません。

## Decision

Q-094 の durable result と bounded resync blocking assertions は `PASS` です。これまでの direct/L4/L7、storm、density、bulk saturation、idle timeout evidenceと合わせ、ADR-0024でDeveloper Preview transportにgRPC bidirectional streamを採用し、Q-094を`Closed`とします。
