# P1-B02/B03/B04 Execution Authority Validation

日付: 2026-08-09

## 1. Scope

P1-A08 の current Host trust/authority を、`Job → Command → Lease → Attempt → Result → Observation/Verification` の execution authority path へ接続した。

今回の validation は次を対象とする。

- typed immutable Command と mutable current execution state
- Host authority generation / Agent session generation に bind された Command Lease
- append-only Lease / Attempt / Result / verification evidence
- Agent の write-before-execute durable journal
- Lease expiry、Host/session fence、stale Result、Result response loss の意味論

backend mutation adapter と production dispatcher loop は本増分の対象外であり、P1-B03/B04 の後続作業とする。

## 2. Authority Model

```text
Host Operation Authority ARMED
        + current Agent session generation
        ↓
typed immutable Command
        ↓
PostgreSQL Command Lease Grant
        ↓
Agent journal fsync / atomic rename
        ↓
Attempt execution and durable Result receipt
        ↓
typed Observation / Verification evidence
        ↓
Job convergence
```

Lease grant は `host_id`、`host_authority_generation`、`session_generation`、`command_id`、`lease_generation`、`attempt_index`、token digest、`not_before`、`expires_at` を immutable evidence として保持する。raw Lease token は DB、Event、Agent journal に保存しない。

## 3. PostgreSQL Validation

PostgreSQL 17 の fresh database に migration 001〜008 を適用し、次を統合試験で確認した。

| Case | Result |
|---|---|
| 同一 Command への 2 並行 Lease 要求 | 1 件だけ grant、他方は `ErrActiveCommandLease` |
| Lease binding | current Host authority generation と session generation を grant/current row の両方へ記録 |
| Agent journal replay | 同一 evidence は冪等成功、異なる evidence は conflict |
| Result replay | 同一 Result ID/digest/outcome は同一 receipt、異なる digest は conflict |
| successful Result | Job は `VERIFYING` を経由し、matching verification 後だけ `SUCCEEDED` |
| Lease expiry 後の初回 Result | Lease `EXPIRED`、Attempt/Command `UNKNOWN`、Result は stale reject |
| `NOT_APPLIED` read-back | 旧 Attempt は不変のまま Command を `REDISPATCHABLE` とし new Attempt を発行可能 |
| new Attempt 発行後の旧 Result | stale reject、新 Attempt/current authority は進行しない |
| active Lease 中の reconnect | Host authority と Lease を fence、Attempt `UNKNOWN`、旧token Result を拒否 |

同一 Host の advisory lock と Command current row lock の順序を固定し、Grant transaction 成功時だけ Lease/Attempt generation を消費する。

## 4. Agent Journal Validation

Agent journal は private directory と process lock を使用し、backend mutation 前に Command identity/digest/target/Attempt を temporary file へ write、`fsync`、atomic rename、directory `fsync` する。

- Lease token、certificate、credential は journal record に含めない。
- Agent restart 後も `STARTED` / `COMPLETED` evidence を読み戻せる。
- 同じ Command/Attempt と異なる digest/target の再利用は拒否する。
- corrupt entry quarantine、disk-full、real backend kill/read-back fixture は後続 hardening とする。

## 5. Commands

```text
go test ./internal/agent/executionjournal ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'TestExecutionAuthorityLeaseResultAndVerificationPostgreSQLIntegration|TestHostTrustSessionAuthorizationAndExplicitArmingPostgreSQLIntegration' ./internal/persistence/postgres
make check
```

## 6. Decision

P1-B02 は schema/authority foundation、P1-B03 は Agent journal/verification foundation、P1-B04 は Host/session generation-bound Lease fencing まで In Progress とする。

Host trust が current に戻っても旧 Lease は再 arm されない。新しい mutation authority には current trust/readiness の再検証、明示的 Host authority arming、new Lease/Attempt が必要である。
