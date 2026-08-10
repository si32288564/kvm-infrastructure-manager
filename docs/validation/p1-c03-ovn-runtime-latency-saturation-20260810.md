# P1-C03 OVN Runtime Latency / Pool Saturation Validation

- 対象: sustained OVN endpoint latency、partial timeout、PostgreSQL connection pool saturation
- 状態: PASS / P1-C03 In Progress
- Test Contract: `AT-NET-043`, `FI-NET-029`
- Invariant: `INV-NET-032`, `INV-NET-033`, `INV-NET-035`, `INV-NET-037`

## 1. Result

production `kim-network-worker` に explicit `database-max-connections` を追加し、`database-max-connections >= 2 × BatchLimit` を起動時に fail closed で検証するようにしました。PostgreSQL pool は `OpenWithMaxConnections` で実際に bounded とし、CLI configuration と runtime pool capacity を一致させます。

実 PostgreSQL 17 fixture では、96 work、8 worker、batch 2、pool 16 のうち 8 connections を他 domain traffic の模擬として継続占有しました。全 OVN observe/apply を 2 s 遅延させ、各 8 work のうち 1 件を pre-apply timeout、1 件を post-apply response-loss にしました。claim Lease 1.5 s、renewal interval 300 ms、maximum lifetime 8 s なので、全 operation は initial Lease を超え、current claim renewal が必須です。

```text
96 work
→ 8 workers × batch 2 = aggregate in-flight 16
→ PostgreSQL pool 16 / reserved 8
→ every OVN operation latency 2 s
→ pre-apply timeout 12 + post-apply response-loss 12
→ periodic DB-time renewal
→ expiry / DISPATCH_UNKNOWN / generation 2 READ_BACK_FIRST
→ 96 OBSERVED / attempts 120 / physical apply 1 per object
```

## 2. Rejected insufficient profile

最初の profile は 128 work、in-flight 16、effective DB connections 8、Lease 500 ms、renewal 100 ms、endpoint delay 650 ms でした。physical apply は object ごとに 1 回を維持しましたが、expected attempts 160 に対して 190、maximum attempt 4、`DISPATCH_UNKNOWN` 62 まで増幅しました。

この profile は certification していません。pool wait と authority-path jitter に対する Lease headroom が不足しており、意図した partial timeout 以外を claim expiry へ変換したためです。単に timeout 値を default として大きくするのではなく、measured pool wait / endpoint latency / renewal schedule の関係を deployment qualification 対象にします。

## 3. Certified profile

同じ effective DB capacity と aggregate in-flight pressureを維持し、Lease 1.5 s、renewal 300 ms、endpoint delay 2 s、maximum lifetime 8 s としました。開発 clone の 3 回連続 run と正規 checkout の最終 run は全て PASS しました。

| Metric | Run 1 | Run 2 | Run 3 | Canonical |
|---|---:|---:|---:|---:|
| convergence | 22.582 s | 23.696 s | 21.642 s | 21.705 s |
| attempts | 120 | 120 | 120 | 120 |
| renewals | 798 | 800 | 792 | 792 |
| `DISPATCH_UNKNOWN` | 24 | 24 | 24 | 24 |
| maximum attempt | 2 | 2 | 2 | 2 |
| workers participating | 8 | 8 | 8 | 8 |
| p99 RunOnce | 2.101 s | 2.043 s | 2.021 s | 2.027 s |
| empty pool acquires | 342 | 246 | 181 | 146 |
| cumulative pool wait | 14.203 s | 15.763 s | 1.731 s | 1.149 s |

各 run で 96/96 work が `OBSERVED`、physical apply は object ごとに 1 回でした。attempts 120 は initial 96 + intentional uncertain outcomes 24 と完全に一致し、pool wait による追加 attempt はありません。

## 4. Authority semantics

- slow OVN endpoint を dead endpoint と判断しない。
- pre-apply timeout でも side effect 不在を timeout だけから確定しない。
- post-apply response-loss を rollback と判断しない。
- PostgreSQL pool wait を claim expiry または未実行 evidence にしない。
- expired/uncertain attempt は immutable `DISPATCH_UNKNOWN` を残し、successor を `READ_BACK_FIRST` に限定する。
- pool capacity、BatchLimit、Lease、renewal interval、maximum lifetime は独立 tuning 値ではなく、一つの certified deployment profile として扱う。

## 5. Verification

```text
make test-p1c03-ovn-worker-latency-saturation: PASS
go test -race ./cmd/kim-network-worker ./internal/qualification/p1c03ovnwork: PASS
make check: PASS
```

fixture 終了後、一時 PostgreSQL container は cleanup します。外部 OVN、KVM Host、本番 resource は使用しません。

## 6. Remaining hardening

- worker scale up / drain / down
- production metrics、alert、diagnostic bundle
- repeated failover と latency/saturation を組み合わせた長時間 soak
- external IPAM、Router/DHCP/Security multi-object realization

これらは今回の latency/saturation correctness gate を無効にしません。
