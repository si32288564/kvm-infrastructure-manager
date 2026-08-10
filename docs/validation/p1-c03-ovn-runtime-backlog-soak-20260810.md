# P1-C03 OVN Runtime Backlog / Retry Storm Validation

- 実施日: 2026-08-10
- 対象: PostgreSQL-backed OVN runtime work の backlog、retry storm、multi-worker soak
- Test Contract: `AT-NET-041`, `FI-NET-028`
- Invariant: `INV-NET-032`, `INV-NET-033`, `INV-NET-034`, `INV-NET-035`

## Outcome

fresh PostgreSQL 17 に 512 個の独立した OVN runtime work を投入し、16 worker を同時起動しました。work の 10% は pre-apply failure、10% は side effect 後の response-loss、10% は initial Lease より長い operation とし、残りを通常 reconcile としました。

最初の fixture では claim batch を直列処理していたため、前半の long-running work が後半 claim の Lease を処理開始前に消費し、512 work に対して attempt 2987、`DISPATCH_UNKNOWN` 2475、最大 attempt 20 まで増幅しました。この結果は acceptance にせず、claimed batch を `BatchLimit` 内で並行処理し、各 item が authority check と renewal loop を直ちに開始するよう runtime を修正しました。

次に aggregate in-flight claim 128、DB pool 32 の過剰 profile を実行すると、並行処理後も attempt 1030、最大 attempt 9 となりました。安全性は維持され全 work は収束しましたが、downstream capacity を超える admission が expiry/retry amplification を生むことを確認しました。この profile は certification 対象外です。

500 ms Lease / DB pool 48 の profile は開発 clone で3回PASSしましたが、canonical exact-code runではauthority pathのscheduling jitterにより1件の追加expiryが発生しました。安全性は維持されましたが、attempt 617、最大 attempt 3となったためcertification profileには採用しません。

最終 profile は aggregate in-flight claim 32、DB pool 64 とし、Lease 1 s、renewal interval 200 ms、long operation 1.5 s、maximum lifetime 5 s としました。renewal 必須条件を維持しながら、通常の authority path jitter を expiry へ変換しない headroom を持たせ、開発 clone 3 回と canonical exact-code 1 回の計 4 回連続 PASS しました。最後の run は item-local error / DB authority error の分類を追加した final code です。

```text
512 work backlog
→ 16 workers × batch 2 = 32 in-flight claims
→ DB pool 64
→ normal / pre-apply failure / post-apply response-loss / long renewal
→ expected 104 DISPATCH_UNKNOWN
→ generation 2 READ_BACK_FIRST
→ all 512 OBSERVED
→ one physical apply per object
```

## Measured Results

| Metric | Run 1 | Run 2 | Canonical Run | Final Canonical Run |
|---|---:|---:|---:|---:|
| Convergence | 6.786 s | 6.767 s | 6.696 s | 6.838 s |
| Attempts | 616 | 616 | 616 | 616 |
| Renewal evidence | 357 | 357 | 357 | 357 |
| `DISPATCH_UNKNOWN` | 104 | 104 | 104 | 104 |
| Maximum attempt per work | 2 | 2 | 2 | 2 |
| Participating workers | 16 / 16 | 16 / 16 | 16 / 16 | 16 / 16 |
| RunOnce p50 | 3.800 ms | 3.636 ms | 4.572 ms | 3.133 ms |
| RunOnce p95 | 30.770 ms | 29.133 ms | 35.644 ms | 36.479 ms |
| RunOnce p99 | 1.524 s | 1.523 s | 1.524 s | 1.523 s |
| DB empty acquire count | 31 | 32 | 32 | 32 |
| DB cumulative acquire wait | 508.526 ms | 895.691 ms | 386.869 ms | 594.628 ms |
| Physical apply per object | 1 | 1 | 1 | 1 |

`RunOnce` latency は claim transaction だけではなく、batch 内の typed adapter operation と observation commit を含みます。p99 は 5 s の maximum claim lifetime 未満でなければなりません。試験終了後は worker goroutine と PostgreSQL pool goroutine が bounded baseline へ戻ることを要求します。

## Fixed Contract

- `BatchLimit` は claim fetch size だけでなく process-local operation concurrency bound である。
- claim 済み item を未更新の local serial queue に保持しない。
- 各 concurrent item は独立した current claim check と renewal loop を持つ。
- item-local adapter error は error handler へ通知して bounded poll を継続するが、DB claim/renewal authority error は process を停止する。
- aggregate in-flight claim は DB/backend capacity より小さい certified deployment profile とする。
- pre-apply failure は side effect を作らず、post-apply response-loss は read-back から収束する。
- retry attempt 数は initial work 数と意図的 uncertain failure 数の和を超えない。
- worker/process liveness、timeout、expiry を side effect 不在の証明にしない。

## Remaining Qualification

- repeated PostgreSQL failover と renewal overlap の長時間 campaign
- sustained OVN endpoint latency / saturation
- production observability、alert threshold、capacity profile certification
