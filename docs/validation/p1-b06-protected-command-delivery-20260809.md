# P1-B06 Protected Command Delivery Validation

日付: 2026-08-09

## 1. Problem

Worker と Agent Gateway は別 process であるため、Command Lease Grant 後の配送 intent は durable に再駆動できなければならない。一方、raw Lease token は bearer capability であり、PostgreSQL、Outbox、Agent journal へ plaintext 保存してはならない。

単純な `Lease Grant → plaintext gRPC envelope in Outbox` は既存 security invariant に違反する。

## 2. Implemented Boundary

```text
random Lease token in Worker memory
        ↓
token SHA-256 for Lease validation
        ↓
AEAD protect(token, Command ID as AAD)
        ↓
PostgreSQL transaction
        ├─ Lease Grant / current Lease / Attempt
        └─ protected delivery Outbox intent
```

protected value は `key_id / algorithm / nonce / ciphertext` を持つ。initial implementation は AES-256-GCM を使用し、key material 自体は payload/DB に含めない。

Outbox intent は Command、Lease generation、Attempt、Host authority generation、session generation、token digest、expiry と protected token を保持する。plaintext token は保持しない。

## 3. Failure Semantics

- protection failure は PostgreSQL transaction 開始前に fail closed する。
- Outbox conflict/insert failure は Lease Grant、Attempt、current Command/Job state と一緒に rollback する。
- wrong key ID、wrong AAD、nonce/ciphertext 改変は AEAD authentication failure となり、token を返さない。
- Outbox claim expiry は未配送を証明せず、同じ protected intent を再処理可能にする。
- Secret Provider key revision が利用不能な intent を別 key で推測復号しない。

## 4. Validation Results

| Case | Result |
|---|---|
| AES-256-GCM protect/unprotect | PASS |
| different Command AAD | authentication failure: PASS |
| Lease Grant + Outbox atomic commit | PASS on fresh PostgreSQL 17 |
| Outbox plaintext token scan | token absent: PASS |
| protected token recovery | original token + stored token digest match: PASS |
| pre-existing conflicting Outbox ID | `ErrCommandEvidenceConflict`: PASS |
| conflict transaction state | Command `PENDING`、Job `DISPATCHABLE`、Attempt 0 を維持: PASS |

## 5. Remaining Work

- production Secret Provider からの delivery key revision/rotation/revocation
- bounded Outbox publisher と internal NATS subject/schema
- Gateway Inbox dedupe、current session revalidation、OutboundRegistry routing
- publish/ACK response loss、Gateway restart、old session generation の end-to-end fixture
- delivered/dead-letter/quarantine operator workflow

本増分は durable delivery authority の最初の半分を完成させる。NATS adapter と Gateway consumer が入るまでは Worker → Gateway runtime topology complete を意味しない。
