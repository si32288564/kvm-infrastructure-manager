# P1-C03 OVN Runtime Worker Process Kill Validation

- 実施日: 2026-08-10
- 対象: `kim-network-worker` の process loss、claim expiry、read-back-first recovery
- Test Contract: `AT-NET-037`, `FI-NET-027`
- Invariant: `INV-NET-032`

## Outcome

実 `kim-network-worker` process を OVN apply 後・post-apply read-back 中に `SIGKILL` し、claim expiry 後に起動した別 worker process が generation 2 を `READ_BACK_FIRST` で取得しました。別 worker は保存済み OVN ownership を read-backし、同じ intent を再 applyせず `OBSERVED` へ収束しました。

```text
worker A claim generation 1
→ APPLY_AUTHORIZED
→ typed OVN apply
→ post-apply read-back 中に SIGKILL
→ claim expiry
→ immutable DISPATCH_UNKNOWN
→ worker B claim generation 2 / READ_BACK_FIRST
→ current OVN ownership read-back MATCHED
→ observation commit
→ work OBSERVED
```

## Fixture

- PostgreSQL 17 に fresh migration を適用した。
- 実 `cmd/kim-network-worker` binary を二つの独立した OS process として起動した。
- `ovn-nbctl` / `ovn-sbctl` は closed typed adapter contract を実装する fixture executable とし、apply後の persistent state と physical apply count を process外の JSON evidence に保持した。
- worker A の post-apply read-back開始を evidence file で確認してから `SIGKILL` した。
- worker B は同じ PostgreSQL work authority と同じ external OVN stateを参照した。

## Assertions

- worker A は claim generation 1 / `APPLY_ALLOWED` を取得した。
- worker A の claim expiry 後、旧 owner/generationの apply authorization は `ErrStaleOVNRuntimeClaim` で拒否された。
- worker B は claim generation 2 / `READ_BACK_FIRST` を取得した。
- immutable attempt evidence は2件、`DISPATCH_UNKNOWN` は1件、`READ_BACK_STARTED` は1件だった。
- `APPLY_AUTHORIZED` event と physical apply はどちらも1件だけだった。
- worker B はmatching read-back後にduplicate applyせず、current workを `OBSERVED` へ進めた。

## JSONB Canonicalization Finding

実 process pathでは、immutable Port planを PostgreSQL `jsonb` から取得した際に、空白とobject key orderが保存前 wire bytesから変化することが判明しました。保存前 raw bytesの SHA-256 と、DBが再serialiseした raw bytesを直接比較すると、同じtyped planでも誤ってdigest conflictになります。

修正後は次の順序を強制します。

```text
stored jsonb
→ unknown fieldを拒否するtyped decode
→ canonical Go wire formへ再marshal
→ immutable pre-storage digestと照合
→ adapterへcanonical bytesを渡す
```

異なるtyped content、unknown field、不正 ownership marker、異なるimmutable digestは引き続き fail closed です。

## Result

```text
=== RUN   TestOVNRuntimeWorkerProcessKillReadBackConvergence
--- PASS: TestOVNRuntimeWorkerProcessKillReadBackConvergence
PASS
```

## Remaining Qualification

- PostgreSQL failover中のclaim/observation transaction
- long-running applyのclaim renewal
- DB/worker clock boundary
- mass backlog、retry storm、soak
- foreign-object quarantineの運用復旧

これらは今回のprocess-loss gateを無効にしませんが、P1-C03 production hardeningとして継続します。
