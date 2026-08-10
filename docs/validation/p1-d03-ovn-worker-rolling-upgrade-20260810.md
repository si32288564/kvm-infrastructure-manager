# P1-D03 OVN Worker Rolling Upgrade / Version Skew Validation

- 対象: Release Manifest、explicit N/N-1 edge、Compatibility Decision、worker binding、work-schema Feature Gate
- 状態: Foundation PASS / P1-D03 In Progress
- Test Contract: `AT-UPG-029`, `FI-UPG-019`
- Invariant: `INV-UPG-004`, `INV-UPG-005`, `INV-UPG-022`

## 1. PostgreSQL authority

migration `031_release_compatibility_foundation.sql` で次を追加しました。

- immutable `release_manifest_evidence`
- immutable component artifact/schema evidence
- immutable explicit `N_MINUS_ONE` compatibility edge
- current release authority / write work schema
- immutable `compatibility_decision_evidence`
- current component release binding / binding generation / lifecycle
- OVN work required schema
- claim Attempt の release binding generation

Release Manifest、component、edge、Decision は UPDATE trigger で immutable です。product version の文字列順序や process liveness は compatibility authority ではありません。

## 2. Claim-time compatibility

production `kim-network-worker` は起動時に以下を PostgreSQL へ提示します。

```text
worker identity
+ Release Manifest identity / revision
+ adapter artifact digest
+ supported work schema versions
+ compatibility evaluator artifact digest
        ↓
immutable Compatibility Decision
        ↓
current component release binding generation
```

claim transaction は current binding generation、`ACTIVE` lifecycle、`VALIDATED / COMPATIBLE` decision、required work schema を再検証します。release authority が存在する DB では binding generation のない claim を拒否します。

## 3. Real process rolling qualification

fresh PostgreSQL 17 と二つの実 `kim-network-worker` process を使用しました。

```text
current release = N
write schema = v1

N-1 Manifest: v1
N Manifest:   v1 + v2
explicit N-1 → N edge: v1 only
N-2 Manifest: v1, edge absent
```

実行結果:

```text
N-1 worker starts
→ Compatibility = COMPATIBLE
→ v1 work claim generation 1

N worker starts
→ Compatibility = VALIDATED

N-1 first signal
→ binding DRAINING
→ new claim blocked
→ current v1 work completion

activate v2 while N-1 DRAINING
→ BLOCKED

N-1 STOPPED
→ activate v2 PASS
→ new work required schema = v2
→ N binding generation で claim
→ OBSERVED

N-2 registration without edge
→ INCOMPATIBLE / FENCED
→ claim count 0
```

測定結果:

- immutable Compatibility Decisions: 3
- N-1: `COMPATIBLE / STOPPED`、Attempt 1
- N: `VALIDATED`、v2 Attempt 1
- N-2: `INCOMPATIBLE / FENCED`、Attempt 0
- v1 / v2 physical apply: 各 1
- duplicate apply / `DISPATCH_UNKNOWN`: 0

## 4. Failure semantics

`DRAINING` は new claim eligibility の停止であり、既取得 claim の revoke ではありません。N-1 は v1 current work を完了できます。N-1 が理解しない v2 semantics は、binding が `STOPPED` になるまで write authority へ昇格しません。

edge のない N-2、artifact mismatch、Manifest schema mismatch は version 文字列が近くても `INCOMPATIBLE` です。incompatible binding は `FENCED` となり、OVN work claim pool に入りません。

## 5. Verification

```text
make test-p1d03-ovn-worker-rolling-upgrade: PASS
make test-p1c03-ovn-worker-hard-drain: PASS
go test -race ./cmd/kim-network-worker ./internal/network/ovnruntime: PASS
make check: PASS
```

一時 PostgreSQL container は fixture 終了時に cleanup します。外部 OVN、KVM Host、本番 resource は使用しません。

## 6. Remaining P1-D03 work

- Upgrade Campaign / Plan / Wave / Target persistence と canary controller
- rollback boundary / old artifact retention / destructive finalization approval
- Control Plane、Gateway、Agent、Event decoder を含む product-wide compatibility graph
- repeated failover と rolling replacement の combined campaign
- mixed-version deadline と operator alert / runbook

これらは今回の OVN worker N/N-1 claim/schema gate を無効にしません。
