# P1-B11 UNKNOWN Verification Delivery Validation

日付: 2026-08-09

## Scope

UNKNOWN Command の read-only Verification を、process-local dispatcher だけでなく durable Worker/Gateway runtime path へ接続した。

```text
Lease expiry
  ↓
Attempt / Command = UNKNOWN
  ↓
Worker bounded candidate scan
  ↓
Verification Outbox intent
  ↓
JetStream durable message
  ↓
Gateway Inbox + PostgreSQL authority revalidation
  ↓
current authorized Agent session
  ↓
typed Verification Request
```

Verification intent は Command ID、Attempt、Host、current session generation、Command/schema、target、canonical payload digest へ bind する。Host mutation authority を参照して rearm せず、新 Lease/Attempt を作らない。

## Results

| Case | Result |
|---|---|
| Lease expiry から UNKNOWN transition | PASS |
| bounded verification candidate enqueue | PASS |
| stable Outbox identity `command-verification/<command>/<attempt>/<session>` | PASS |
| canonical JSON payload digest verification | PASS |
| publisher から typed Verification envelope 生成 | PASS |
| Gateway Inbox dedupe/current session authorization | PASS |
| current Agent stream routing | PASS |
| Host mutation authority rearm/new Lease/new Attempt | 発生しない: PASS |
| fresh PostgreSQL 17 integration | PASS |

JSONB の text representation に含まれる空白差を raw byte digest で比較せず、semantic JSON を canonical marshal した digest と immutable Command payload digest を比較する。

## Remaining

指定 KVM Host 上の remote Agent、Mac 側 PostgreSQL/Worker/NATS/Gateway、実 libvirt mutation、Agent kill、session regeneration、Verification response、PostgreSQL Job convergence を一つの campaign に統合する。
