# Q-094 Agent Transport Spike

- 状態: Closed
- 更新日: 2026-08-09
- Owner: Agent / Gateway
- Target gate: P1-A exit
- Decision authority: implementation ADR または Q-094 close record

## 1. Purpose

Agent transport implementation を、protocol semantics を変更せずに選定します。この spike は「HTTP/2 か gRPC か」を好みで決めるためのものではありません。候補 implementation が AGT-001〜011、INV-AGT-011〜017、AT-AGT-011〜014、FI-GATEWAY-003〜005 を満たせることを、同一 fixture と測定条件で比較します。

## 2. Fixed Contract

次は spike で変更しません。

- 1 Host Agent identity に一つの current multiplexed outbound mTLS session
- current `session_generation` は PostgreSQL authority
- typed logical stream と bounded message/queue/spool
- Control、Command/Lease、Result、Heartbeat の bulk stream に対する優先性
- scope ごとの generation、sequence、idempotency。global FIFO 非依存
- stale session fencing、bounded reconnect、journal/resync recovery
- typed module から socket、TLS credential、endpoint、reconnect loop を隠す境界

候補 implementation がこの contract を満たせない場合、contract を弱めず候補を棄却します。

## 3. Candidates

初回比較対象は次です。選定時点で version と artifact digest を記録します。

| Candidate | Wire/runtime | 評価対象 |
|---|---|---|
| C1 | gRPC bidirectional stream over HTTP/2 | code generation、flow control、proxy/LB behavior、Go operational maturity |
| C2 | typed HTTP/2 streaming with explicit envelope codec | protocol control、dependency surface、flow control/backpressure 実装量 |

WebSocket、QUIC、複数 long-poll endpoint 等は、上記候補が blocking requirement を満たせない証拠がある場合だけ追加します。artifact transfer の別 endpoint は Agent control session の代替候補に含めません。

## 4. Common Fixture

1 Gateway process と 1 Agent process の間に、同一 certificate、Host identity、session generation を使用します。Agent は少なくとも libvirt、Storage、Network、Clock、Compliance の五つの synthetic module を登録します。

fixture は次を生成します。

- small high-priority Control/Lease/Heartbeat/Result traffic
- configurable Inventory/Observation payload と Resync burst
- duplicate、reorder、response loss、half-open、Gateway restart
- old/new connection overlap と stale session message
- slow consumer、bounded disk spool、proxy idle timeout

module は candidate 固有 transport type を import しません。candidate adapter は同じ Session Manager port を実装します。

## 5. Blocking Assertions

| Assertion | Pass condition |
|---|---|
| Connection/certificate cardinality | module 数を増やしても current Agent session/certificate は一組 |
| Priority isolation | bulk saturation 中も Control/Lease/Result/Heartbeat が定義 SLO 内で進行 |
| Bounded resources | memory、queue、spool、message/chunk が設定上限を超えない |
| Stale fencing | old session の全 message が current authority を進めない |
| Durable result | transport loss後も Result/journalをsilent dropせずReceiptへ収束 |
| Resync | Gateway/Agent restart後にbounded checkpointから収束 |
| Ordering | stream間reorderでもscope generation/sequenceで正しく判定 |
| Module isolation | module source/build dependencyにcandidate transport APIが現れない |
| Proxy/LB | supported proxy pathでmTLS identity、stream、idle/reconnect semanticsを維持 |

一つでも満たさない候補は性能値に関係なく採用しません。

## 6. Measurements

- Host session 数 1、100、1,000、10,000 のGateway memory/CPU/file descriptor見積り
- message class別 p50/p95/p99 latency と throughput
- bulk saturation時のpriority latency、queue depth、coalesce/drop reason
- reconnect stormのconnection attempt rate、stabilization time、DB contention
- certificate renewal overlap時のstale reject/Receipt recovery数
- maximum supported message/chunk とoversize rejection behavior
- proxy/LB別idle timeout、stream reset、graceful drain behavior
- binary size、dependency/SBOM、security update surface

Developer Preview の具体 SLO と limit は測定前に fixture config として versioned に固定し、結果を見て成功条件を後から緩めません。

## 7. Decision Record

結果には次を保存します。

- candidate/library/proxy version と artifact digest
- fixture revision、Host/Gateway resource、network condition
- raw result と集計、failed assertion、known limitation
- selected candidate と rejected alternatives
- rollback/replaceability、module contract非依存の確認

Q-094 は blocking assertions、operational profile、dependency/security reviewを満たした候補を選び、Decision Recordを承認した時点でClosedにします。2026-08-09 に [ADR-0024](../adr/0024-initial-agent-transport-grpc.md) を Accepted とし、Developer Preview transport に gRPC bidirectional stream を採用しました。

## 8. Implementation Progress

初回 fixture foundation として次を実装済みです。

- transport 非依存の typed `Envelope` と logical `Stream`
- payload digest、bounded message、session generation validation
- bulk 用 capacity が priority traffic 用 reserve を消費しない bounded queue
- Control 優先と Command/Result/Heartbeat/Credential 間の round-robin
- module 登録数と connection open 回数を分離する `Session Manager` / `TransportAdapter` 境界
- stale session message を module routing 前に拒否する fixture

gRPC/typed HTTP/2 candidate adapter、real mTLS、basic disconnect detection、per-session persistent receive loop、1,000-session open/idle/echo/reconnect fixture、PriorityQueue を通す bulk saturation fixture を実装しました。caller 単位の `Receive` timeout は transport session を破棄せず、同一 session を継続利用できます。L4 TLS passthrough hard drain、Envoy L7 GOAWAY / rolling replacement、connection/stream idle timeout 分離、durable spool/Receipt replay/resync checkpoint まで完了しました。

初回の real mTLS adapter、functional contract、loopback benchmark は [Q-094 Loopback Smoke Result](results/q094-loopback-20260809.md) に記録しました。両候補が基本 contract を通過し、この限定条件では gRPC が低い round-trip latency を示しましたが、proxy/HOL/reconnect storm/spool 未評価のため Decision は `HOLD` です。

per-message receive goroutine を除去した後の 1,000-session 測定は [Q-094 1,000-session Scale Result](results/q094-scale-20260809.md) に記録しました。[Q-094 Session Density Curve](results/q094-session-density-curve-20260809.md) では 100〜10,000 session の線形 resource 傾向と generation reconnect を確認し、typed HTTP/2 を density leader としました。一方、[Q-094 Bulk Saturation Result](results/q094-bulk-saturation-20260809.md) では gRPC が slow-reader 下の priority latency と bulk completion で優位でした。[Q-094 Reconnect Storm Result](results/q094-reconnect-storm-20260809.md)、[Q-094 Real gRPC Authority Storm](results/q094-grpc-authority-storm-20260809.md)、[Q-094 TLS Passthrough Proxy Drain](results/q094-tls-passthrough-drain-20260809.md)、[Q-094 Envoy L7 Rolling Restart](results/q094-envoy-l7-rolling-restart-20260809.md)、[Q-094 Envoy Idle Timeout Profiles](results/q094-envoy-idle-timeouts-20260809.md) で authority/proxy lifecycleを検証しました。[Q-094 Durable Delivery and Resync](results/q094-durable-delivery-resync-20260809.md) で Receipt loss、Gateway/Agent restart、generation 2 replay、checkpoint convergence を完了しました。

## 9. Decision

Developer Preview は gRPC bidirectional stream over HTTP/2 を採用します。gRPC は operational/control-path behavior、maintained framing/flow control、proxy lifecycle の総合評価で初期約 100 Host profileに適します。typed HTTP/2 のdensity advantageは将来 profile候補として測定結果とadapter境界を保持しますが、Developer Previewのproduction pathは二重実装しません。
