# P1-C03 OVN Runtime Work Claim Validation

- 実施日: 2026-08-10
- 対象: durable multi-worker claim、claim expiry、read-back-first recovery、stale worker fencing
- 状態: PASS

## 1. Authority Boundary

OVN runtime work は process-local queue ではなく PostgreSQL current authority として保持します。

```text
Committed OVN Port Intent
  -> PENDING Work
  -> bounded PostgreSQL Claim
  -> typed read-back / apply
  -> immutable NB/SB Observation
  -> terminal current Work state
```

Claim は worker identity、claim generation、DB authority time による expiry を持ちます。同じ work を複数 worker が取得しようとしても、`FOR UPDATE SKIP LOCKED` により一つの current claim だけを発行します。

## 2. Expiry and Recovery

Claim expiry は apply が実行されなかった証明ではありません。

```text
worker A claim
  -> apply outcome unknown
  -> claim expiry
  -> immutable DISPATCH_UNKNOWN evidence
  -> worker B claim generation + 1 / READ_BACK_FIRST
  -> typed NB/SB ownership read-back
     -> MATCHED: no duplicate apply, observation convergence
     -> not matched: current claim revalidation, same intent apply
```

`READ_BACK_FIRST` claim は read-back event が PostgreSQL に記録されるまで apply authorization を取得できません。旧 worker A の apply authorization と completion は current owner/generation 不一致で拒否されます。

## 3. Persistence Model

- `ovn_runtime_work_current`: mutable current work/claim authority
- `ovn_runtime_work_attempt_evidence`: immutable claim owner/generation/mode/expiry
- `ovn_runtime_work_event_evidence`: append-only claim、read-back、apply、UNKNOWN、observation evidence

新しい Port intent generation は同じ Port の旧 non-terminal work を `SUPERSEDED` にします。exact intent replay は terminal work を `PENDING` へ戻さず、claim generation や attempt を増やしません。

## 4. Verified Contracts

- fresh PostgreSQL 17 migration と全 persistence integration
- 同一 work の competing claim が 0 件になること
- expiry 後の claim generation 1 から 2 への移行
- generation 1 への immutable `DISPATCH_UNKNOWN` evidence
- generation 2 が `READ_BACK_FIRST` になること
- read-back evidence 前の apply authorization 拒否
- stale owner/generation の apply authorization 拒否
- current observation acceptance と work terminalization の同一 transaction
- observe-only adapter が OVN mutation command を発行しないこと
- matching read-back 後に duplicate apply を行わないこと

```text
go test -race -count=1 \
  ./internal/network/ovnadapter \
  ./internal/network/ovnruntime \
  ./internal/persistence/postgres

PASS
```

```text
make check

PASS
```

## 5. Scope Boundary

この validation は OVN Port runtime work の durable ownership と retry semantics を証明します。Router、DHCP、Security Policy の multi-object work graph、cross-aggregate dependency ordering、production metrics/alert は後続です。
