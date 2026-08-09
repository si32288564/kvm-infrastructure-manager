# ADR-0024: Developer Preview の Agent Transport に gRPC Bidirectional Stream を採用する

- 状態: Accepted
- 決定日: 2026-08-09
- 対象: Phase 1 / Developer Preview

## Context

KIM Host Agent は、一つの Host identity と current session generation に対して一つの long-lived outbound mTLS session を確立し、Control、Command、Result、Heartbeat、Inventory、Observation、Credential、Resync を multiplex する必要があります。transport は module/capability contract から独立し、stale session fencing、bounded backpressure、Receipt replay、proxy drain、restart convergence を満たさなければなりません。

Q-094 では gRPC bidirectional stream と typed protobuf stream over HTTP/2 を同じ `TransportAdapter`、protobuf Envelope、mTLS、session generation、priority queue の条件で比較しました。

## Decision

Developer Preview の initial Agent transport に、HTTP/2 上の gRPC bidirectional stream を採用します。

- 1 Host Agent identity / 1 current gRPC stream / 1 Host certificate を通常形とする。
- `SessionHello → PostgreSQL Grant → SessionAccepted` を `Open()` 成功条件とする。
- durable outbound message は送信前に spool へ fsync し、PostgreSQL commit 済み `MessageReceipt` の検証後だけ削除する。
- reconnect では stable message identity/digest を保持し、current session generation だけを再 bind する。
- direct mTLS、L4 passthrough、または明示的に trusted な Envoy L7 termination profile を許可する。
- Developer Preview の L7 route は stream idle timeout を無効化し、typed Heartbeat と session generation で liveness/authority を管理する。
- transport/library type を typed module interface、capability advertisement、Command/Lease authority へ露出しない。

typed HTTP/2 adapter は rejected architecture ではありません。10,000-session fixture で確認した density advantage を保持し、将来の high-density profile が別 Decision Gate と同じ blocking assertions を通過した場合に選択可能とします。ただし Developer Preview の production path を二重実装しません。

## Rationale

- gRPC は slow-reader/bulk saturation fixture で priority traffic の tail latency と total completion が優位でした。
- protobuf、bidirectional stream、flow control、status/cancellation、Go tooling を一つの maintained runtime contract で利用できます。
- direct/L4/L7、GOAWAY、rolling restart、idle timeout、1,000 Host reconnect storm の authority semantics を実測できました。
- 初期約 100 Host profile では、typed HTTP/2 と比べた session 当たりの goroutine/heap 差は採用を妨げません。
- Receipt response loss、Gateway restart、Agent spool reopen、generation 2 replay が単一 durable Receipt と Resync Checkpoint へ収束しました。

## Consequences

- Developer Preview の Gateway/Agent packaging、proxy certification、observability は gRPC path を正本にします。
- gRPC dependency、HTTP/2 proxy configuration、message size/flow control/security updateをRelease ManifestとSBOMで管理します。
- large-scale SaaS profile では gRPC のsession density costをcapacity planningへ含めます。
- typed HTTP/2 の adapter/test evidence はreplaceability regression用に保持しますが、同等production supportを宣言しません。
- production Credential Binding、certificate lifecycle、multi-message resync scale、resource SLO tuning は P1-A qualificationで継続し、このADRのtransport選定とは分離します。

## Evidence

- [Q-094 Agent Transport Spike](../spikes/q094-agent-transport.md)
- [Session Density Curve](../spikes/results/q094-session-density-curve-20260809.md)
- [Bulk Saturation](../spikes/results/q094-bulk-saturation-20260809.md)
- [Real gRPC Authority Storm](../spikes/results/q094-grpc-authority-storm-20260809.md)
- [Envoy L7 Rolling Restart](../spikes/results/q094-envoy-l7-rolling-restart-20260809.md)
- [Envoy Idle Timeout Profiles](../spikes/results/q094-envoy-idle-timeouts-20260809.md)
- [Durable Delivery and Resync](../spikes/results/q094-durable-delivery-resync-20260809.md)
