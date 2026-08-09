# P1-B05 Product Process Runtime Validation

日付: 2026-08-09

## 1. Scope

component scaffold だった `kim-host-agent` と `kim-worker` を、既存 execution authority component の long-running process lifecycle へ接続した。

本増分は Worker と Agent Gateway 間の internal messaging、production bootstrap/CSR、typed libvirt mutation を完成扱いにしない。

## 2. kim-host-agent Runtime

`kim-host-agent` は次を一つの process owner として構成する。

```text
signal-aware process context
        ↓
gRPC outbound mTLS Adapter
        ↓
SessionHello / PostgreSQL SessionAccepted
        ↓
durable accepted-generation ledger
        ↓
one Session Manager + Session Runner
        ├─ typed execution module
        ├─ write-before-execute journal
        ├─ durable outbound spool
        └─ Receipt handling
```

TLS は TLS 1.3 以上、明示 trust bundle、Agent certificate/private key、Gateway server name を要求する。typed module は TLS、socket、endpoint、reconnect loop を受け取らない。

Session generation ledger は PostgreSQL Grant の代替ではない。`SessionAccepted` 後だけ accepted generation を fsync、atomic rename、directory fsync で記録する。connection failure または `SessionRejected` は generation を消費しない。

restart/reconnect 時は durable spool の stable message identity/digest を保持し、current generation へ transport copy だけを rebind する。

## 3. kim-worker Runtime

`kim-worker` は PostgreSQL connection pool と signal-aware maintenance loop を所有する。現在接続した production task は expired Command Lease maintenance である。

```text
bounded DB-time candidate scan
        ↓
per-Command Host-scoped transaction
        ↓
current active Lease revalidation
        ↓
Lease EXPIRED / Attempt UNKNOWN evidence
```

candidate scan は authority ではない。別 Worker または Result acceptance と競合して candidate が stale になった場合は current state を変更しない。expiry は non-execution proof に変換しない。

Worker から別 process の Agent Gateway への dispatch は、process-local `OutboundRegistry` を共有せず、後続の internal messaging contract で接続する。

## 4. Validation Contracts

| Case | Result |
|---|---|
| Agent process cancel/SIGTERM | PASS: Runner、connection、spool、journal、generation ledger を bounded close |
| failed/rejected open | PASS: accepted generation を消費しない |
| SessionAccepted | PASS: generation ledger を durable commit 後に runtime loop を公開 |
| process restart | PASS: proposal sequence `1(rejected) → 1(accepted) → 2(accepted)`、restart next generation `3` |
| pending durable message | PASS: stable identity/digest を維持して current session generation へ rebind |
| Worker startup | PASS: Lease maintenance を一度即時実行 |
| Worker steady state | PASS: bounded interval/batch で maintenance |
| concurrent/stale expiry candidate | PASS: current transaction revalidation で無視 |
| expired active Lease | PASS: existing UNKNOWN semantics へ一度だけ進行 |

fresh PostgreSQL 17 の全 internal integration、`make check`、変更対象 race detector、`kim-host-agent -version`、`kim-worker -version` は PASS した。

## 5. Security and Failure Boundaries

- local generation file、spool、journal は private directory/file と lock を使用する。
- local accepted generation は Host/session authority を付与しない。
- durable generation persist が DB Grant に追随できなければ、generation を推測せず fail closed とする。
- `kim-worker` は Gateway の in-memory registry を process boundary 越しに authority として扱わない。
- qualification state marker 以外の arbitrary path、shell、argv、libvirt method は追加していない。

## 6. Remaining Work

- Worker → Agent Gateway internal command delivery messaging と durable dispatch receipt
- production Enrollment bootstrap/CSR、Credential Binding renewal source
- session-generation DB/local divergence の explicit recovery operation
- systemd/container packaging profile と readiness/health endpoint
- typed libvirt backend、実 KVM Host kill/read-back qualification

本 validation は process lifecycle foundation の成立を示す。Developer Preview の complete service topology または libvirt resource lifecycle completion は意味しない。
