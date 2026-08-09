# P1-B03 Typed Mutation Round-trip Validation

日付: 2026-08-09

## 1. Scope

`168e087` で成立した execution authority foundation を、current Gateway session routing、Agent typed execution、Result receipt、read-back verification へ接続した。

最初の backend operation は `HOST_AGENT_STATE_MARKER_ENSURE` とする。これは KIM-owned private state directory だけを変更する execution-plane qualification operation であり、Linux KVM、QEMU、libvirt、VM、Host network/storage configuration を変更しない。

## 2. Round-trip

```text
PostgreSQL Host Authority + current Session
        ↓
Dispatcher / immutable Command Lease Grant
        ↓
Gateway current outbound session registry
        ↓
Agent Session Manager schema routing
        ↓
closed typed execution backend
        ↓
Agent journal fsync / atomic rename
        ↓
KIM-owned state mutation + immediate read-back
        ↓
typed Command Result / Observation
        ↓
PostgreSQL Result receipt + Verification
        ↓
Command / Job SUCCEEDED
```

## 3. Implemented Contracts

- Gateway `OutboundRegistry` は Host ごとに current live generation だけを route する。これは liveness projection であり PostgreSQL Session Grant の代替ではない。
- old connection の conditional release は new generation registration を削除できない。
- Dispatcher は dispatch candidate を読み、`AcquireCommandLease` transaction 成功後だけ typed Command Lease envelope を生成する。
- transport send error は non-delivery proof とせず、Lease を active のまま保持して既存 expiry/UNKNOWN semantics に委ねる。
- Session Manager は transport implementation を module へ渡さず、closed advertised schema で inbound Command を route する。
- Agent execution module は compile-time registered `CommandType + SchemaVersion` だけを実行する。
- backend mutation 前に Command identity/digest/target/Attempt を durable journal へ記録する。
- successful backend returnだけでは Job を成功にせず、同じ Result に含まれる typed read-back Observation が `MATCHED` の場合だけ Verification を進める。
- Result replay 時は既存 Result/Verification/Agent message receipt を冪等に回収する。
- Agent journal evidence、Result、Verification、message receipt は一つの outer PostgreSQL transaction 内で commit し、receipt digest conflict 時に domain decision を部分 commit しない。

## 4. Validation Results

fresh PostgreSQL 17 上で次を確認した。

| Case | Result |
|---|---|
| Host trust/readiness/explicit arming | authority generation 1 を grant |
| Dispatcher Lease | authority generation 1 / session generation 1 へ bind |
| Gateway outbound routing | generation 1 sink だけへ配送 |
| stale registry generation | reject |
| typed Agent backend | unknown schema/field、unregistered backend を reject |
| write-before-execute | Agent journal persistence 後に state marker mutation |
| read-back | KIM-owned marker を読み戻して typed digest/evidence を生成 |
| Result/Verification | Result `SUCCEEDED` → `VERIFYING` → Observation `MATCHED` → Job `SUCCEEDED` |
| Gateway send failure | envelope を queue へ戻し、未実行へ誤確定しない |
| message receipt digest conflict | Result/Verification を rollback し、Command は `LEASED` のまま維持 |

## 5. Security Boundary

State marker backend の target path は caller input を path として使用せず、`target_resource_id` の SHA-256 から KIM-owned directory 内の固定 filename を導出する。payload は closed `value` field のみで、unknown field、arbitrary path、shell、argv、libvirt XML/method を受理しない。

Lease token は bounded Lease capability として Command/Result transport 内でのみ使用する。DB には digest だけを保持し、Agent execution journal と marker evidence には保存しない。

## 6. Remaining Work

- `kim-worker` / `kim-host-agent` process runtime への long-running dispatcher/receive/receipt loop wiring
- Result durable spool の Lease capability protection と expiry cleanup
- Agent crash、disk-full、corrupt journal、backend kill 後の resync/read-back fixture
- typed libvirt backend operation と実 KVM Host qualification

本 validation は production runtime completion を意味しない。execution plane の transport-neutral component round-trip が一つ成立したことを示す。
