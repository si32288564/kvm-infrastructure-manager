# P1-C05 OVS Dataplane Convergence Validation

- 日付: 2026-08-10
- 状態: PASS / P1-C05 In Progress
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`

## Scope

OVS Port の post-boot Host-side dataplane convergence を、pre-boot realization、VM power state、OVN/end-to-end convergence、Guest readiness から分離しました。

```text
Port authority + pre-boot REALIZED
        ↓
VM power read-back = RUNNING
        ↓
typed OVS dataplane observation
        ↓
active libvirt NIC target
+ Agent-managed Segment-to-Bridge mapping
+ OVS Port bridge/link read-back
        ↓
immutable evidence
        ↓
current dataplane projection = CONVERGED
```

## Persistence qualification

fresh PostgreSQL 17 へ migration 001〜023 を適用し、Placement integration fixture で次を確認しました。

- `RUNNING + pre-boot REALIZED` だけでは dataplane projection を作らない
- current VM/Plan、Port、Network、Segment、Host mapping、Binding generation を acceptance transaction で再検証する
- typed Command/Schema/Target と MATCHED Verification payload の identity/generation を再検証する
- active Domain、NIC target、bridge match、`link_state=up` が揃った場合だけ `CONVERGED` に進める
- immutable evidence の UPDATE を拒否する
- same evidence ID / different digest を拒否する
- idempotent replay は新しい Job、Command、evidence を作らない

```text
TestMigratePostgreSQLIntegration: PASS
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration: PASS
```

## Real KVM qualification

既存 VM を変更せず、test 内で disposable Domain `kim-ovs-qualification-20260810` を define しました。closed typed pre-boot backend で `br-int` へ virtio NIC を追加した後に Domain を起動し、active libvirt XML の target device、`ovs-vsctl port-to-br`、OVS Interface `link_state` を read-backしました。

```text
TestDisposableLibvirtOVSPrebootAndDataplaneRealization: PASS
disposable Domain cleanup: PASS
```

この qualification は Host-side OVS Port convergence のみを証明します。OVN logical flow/chassis convergence、外部 Network の end-to-end reachability、Guest readiness、application health は証明しません。

## Safety boundary

- Command payload は bridge 名、raw XML、path、argv、OVS flags を受け取らない
- Segment Claim ID から Agent administrator 設定の bridge mapping を解決する
- stale/UNKNOWN generation または不一致 evidence は `CONVERGED` に進めない
- pre-boot realization、VM RUNNING、process/transport liveness を dataplane authority にしない

## Remaining

- OVN NB/SB/chassis/logical-flow convergence projection
- end-to-end reachability と Gateway/WIM status
- drift、link-down、OVS restart 後の `DEGRADED / UNKNOWN` projection workflow
- SRIOV_DIRECT post-boot hardware qualification
