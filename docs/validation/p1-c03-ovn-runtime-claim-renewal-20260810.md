# P1-C03 OVN Runtime Claim Renewal Validation

- 実施日: 2026-08-10
- 対象: long-running typed OVN operation の bounded claim renewal
- Test Contract: `AT-NET-039`, `FI-DB-004`
- Invariant: `INV-NET-033`, `INV-NET-032`, `INV-HA-001`

## Outcome

OVN runtime claimにclaim開始時のmaximum expiryとrenewal generationを追加し、long-running adapter operation中だけcurrent workerがbounded intervalでrenewできるようにしました。renewalはPostgreSQL transaction内でcurrent owner/generation、DB authority time上の未失効、maximum lifetimeを再検証し、prior/new/maximum expiryをimmutable evidenceへ保存します。

実 PostgreSQL 17 synchronous failover fixtureでは、worker Aが800 msのtyped applyとpost-apply read-backを処理する間、500 ms claimを100 ms intervalで複数回renewしました。renewal evidenceが`remote_apply`された後にprimaryを強制停止し、standbyをpromoteしました。promoted primaryはrenewal evidenceを保持し、expiry後にworker Bへgeneration 2 / `READ_BACK_FIRST`をgrantし、physical apply 1回のまま`OBSERVED`へ収束しました。

```text
claim generation 1
→ long-running typed adapter operation
→ renewal generation 1..N
→ each renewal remote-applied to synchronous standby
→ primary hard stop
→ standby promotion
→ renewed claim expiry
→ generation 1 DISPATCH_UNKNOWN
→ generation 2 READ_BACK_FIRST
→ existing OVN object MATCHED
→ OBSERVED without duplicate apply
```

## Persistence Contract

- current claimは`claim_expires_at`と固定`claim_maximum_expires_at`を別々に持つ。
- immutable Attempt evidenceはinitial expiryとmaximum expiryを保持する。
- immutable Renewal evidenceはclaim/renewal generation、owner、prior/new/maximum expiryを保持する。
- renewal generationは同じclaim generation内で単調増加する。
- foreign owner、old generation、expired claimをrenewしない。
- new expiryがprior expiryを進めない場合、maximum lifetime到達として拒否する。
- terminal/superseded workはcurrent expiry/maximum expiryを解放するが、Attempt/Renewal evidenceを書き換えない。

## Runtime Contract

- `kim-network-worker` はclaim lease、maximum lifetime、renew intervalを別設定として持つ。
- renewalはtyped adapter callの実行中だけ行う。
- renewal failure時はoperation contextをcancelするが、side effectが無かったとは判断しない。
- renewal intervalはclaim lease未満、maximum lifetimeはclaim leaseより長く、hard upper bound以内でなければならない。
- renewal無効profileではinitial claim expiryがmaximum expiryとなり、暗黙延長しない。

## Result

```text
fresh PostgreSQL 17 persistence/race integration: PASS
worker renewal unit test: PASS
TestOVNRuntimeWorkerPostgreSQLFailoverConvergence: PASS
physical apply count: 1
```

## Remaining Qualification

- renewal commit後のresponseだけを決定的に失うfault injection
- production HA endpoint/connection pool切替中のrenewal
- maximum lifetime到達時のlong-running backend containment
- mass backlog、retry storm、multi-worker soak

renewal response-lossが未検証でも、expired claimのrevivalやmaximum超過を許可しません。outcomeが不明な場合は既存のexpiry/`DISPATCH_UNKNOWN`/`READ_BACK_FIRST`へ収束させます。
