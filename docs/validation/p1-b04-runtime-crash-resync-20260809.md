# P1-B04 Runtime Crash/Resync Validation

日付: 2026-08-09

## 1. Scope

P1-B execution plane に、long-running Agent Session Runner と、backend mutation 後・Result 発行前の crash から typed read-back で収束する経路を追加した。

本増分の backend は引き続き KIM-owned state marker であり、Linux KVM、QEMU、libvirt、VM、Host network/storage configuration は変更しない。

## 2. Runtime Path

```text
current Agent transport session
        ├─ inbound Command / Verification routing
        ├─ priority outbound Result / Observation flush
        └─ durable Message Receipt handling
```

`Session Runner` は上記 loop を一つの current transport session 上で並行実行する。typed module は connection、TLS、endpoint、reconnect loop を所有しない。いずれかの loop が停止しても、transport loss は backend side effect の absence または resource authority loss を証明しない。

## 3. Crash and Resync Path

```text
Command Lease generation 1
        ↓
write-before-execute journal + fsync
        ↓
backend mutation + journal completion
        ↓
Result publish failure / Agent stop
        ↓
Lease expiry → Attempt UNKNOWN
        ↓
session generation 2 PostgreSQL Grant
        ↓
Host mutation authority remains FENCED
        ↓
read-only Verification Request
        ↓
existing journal evidence lookup
        ↓
typed backend read-back MATCHED
        ↓
Verification + Message Receipt atomic commit
        ↓
Command / Job SUCCEEDED
```

Verification Request は original Command ID、Attempt index、payload digest、target resource、typed schema を保持する。Agent は既存 journal record と完全一致しない Request を拒否し、resync のために `STARTED` evidence を新規生成しない。

## 4. Authority Properties

- reconnect は old Host Operation Authority を rearm しない。
- read-only verification は current `AUTHORIZED` session を必要とするが、`ARMED` mutation authority を要求または付与しない。
- UNKNOWN Attempt 自体は書き換えず、Verification evidence を append する。
- current Command/Job projectionだけが matching observation によって収束する。
- Verification Observation と Agent Message Receipt は同じ PostgreSQL transaction で commit する。
- stale session、別 Attempt、異なる payload digest/target、missing/conflicting journal evidence は current decision を進めない。

## 5. Validation Results

| Case | Result |
|---|---|
| Session Runner inbound route | advertised typed moduleへ配送 |
| Session Runner outbound flush | priority queueから同一 connectionへ配送 |
| durable Receipt | Receipt handlerが accepted receiptを処理 |
| Runner shutdown | context cancelで bounded停止 |
| mutation-before-Result crash | marker mutationとcompleted journalを保持しResultを失う |
| Lease expiry | Command/Attemptを UNKNOWNへ進める |
| reconnect | session generation 2をGrant、Host authority generation 1はFENCEDを維持 |
| journal reopen | immutable Command/Attempt/digest/target evidenceを回収 |
| typed read-back | state markerをMATCHEDとして観測 |
| atomic convergence | Verification/Receipt commit後だけCommand/JobをSUCCEEDEDへ更新 |

fresh PostgreSQL 17 integration、unit tests、race detector、repository checksを実行する。個別結果と commit identity は本増分の handoff に記録する。

## 6. Remaining Work

- `kim-host-agent` / `kim-worker` executable lifecycle への Runner、dispatcher、reconnect supervisor wiring
- Agent journal の disk-full/fsync latency/corruption quarantine qualification
- multi-Command resync scheduling、rate limit、backpressure qualification
- closed typed libvirt backend と実 KVM Host kill/read-back fixture

本 validation は production process completion または libvirt mutation qualification を意味しない。current execution contract が process crash後も evidenceから収束できることを示す。
