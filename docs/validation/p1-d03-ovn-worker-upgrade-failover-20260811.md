# P1-D03 OVN Worker Upgrade / Failover Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-030`, `FI-UPG-020`
- Invariant: `INV-UPG-023`
- Runtime: real `kim-network-worker` processes、PostgreSQL 17 synchronous primary / standby、standard repository OVN fixture adapter

## Scope

次の二つを qualification した。

1. N/N-1 rolling upgrade で explicit compatibility edge を使用し、FeatureGate activation / rollback、edge / Manifest distrust、stale component binding fencing を実行する。
2. N-1 worker の OVN apply 後 read-back を hard drain し、同時期に synchronous PostgreSQL primary を hard stop して standby を promote する。N worker が promoted primary の durable authority から `READ_BACK_FIRST` recovery を行う。

## Fault Sequence

```text
N-1 claim generation 1
→ OVN apply
→ read-back block
→ DRAINING
→ v2 FeatureGate activation BLOCKED
→ second signal / hard drain
→ PostgreSQL primary hard stop
→ synchronous standby promote
→ N worker registration
→ claim expiry / DISPATCH_UNKNOWN
→ claim generation 2 / READ_BACK_FIRST
→ existing OVN object MATCHED
→ OBSERVED
→ N-1 compatibility edge distrust
→ old binding generation INCOMPATIBLE / FENCED
→ v2 activation evidence
→ v1 rollback evidence
```

## Result

| Assertion | Result |
|---|---|
| promoted primary が pre-failover committed LSN を保持 | PASS |
| `restore_epoch` / database authority generation 不変 | PASS |
| N-1 binding lifecycle `DRAINING` を同期複製 | PASS |
| Attempt 数 | 2 |
| `DISPATCH_UNKNOWN` | 1 |
| `READ_BACK_STARTED` | 1 |
| physical OVN apply | 1 |
| revoked edge の old binding generation claim | `INCOMPATIBLE` で拒否 |
| current N-1 binding | `INCOMPATIBLE / FENCED` |
| FeatureGate transition evidence | activation 1、rollback 1 |
| current target Manifest distrust | release authority `PAUSED`、N worker `INCOMPATIBLE / FENCED` |

別の N/N-1 process qualification では、edge distrust、N-1 Manifest distrust、current N Manifest distrust の 3 件を immutable evidence として保存した。再登録した N-1 process を `INCOMPATIBLE / FENCED` にし、current target Manifest distrust 後は release authority を `PAUSED`、N worker を `INCOMPATIBLE / FENCED` にした。v2 work と rollback 後 v1 work は N worker の current release binding generation だけが claim し、各 object の physical apply は 1 回だった。

## Commands

```bash
make test-p1d03-ovn-worker-rolling-upgrade
make test-p1d03-ovn-worker-upgrade-failover
```

## Boundary

この qualification は OVN runtime worker の release binding、schema FeatureGate、same-Site PostgreSQL HA を対象とする。product-wide Upgrade Campaign / Plan / Wave、artifact signing / SBOM、Agent / Gateway mixed-version、DR restore、長期間 mixed-version soak は別 gate である。
