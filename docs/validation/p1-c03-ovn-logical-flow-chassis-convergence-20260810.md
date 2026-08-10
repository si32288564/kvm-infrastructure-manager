# P1-C03 OVN Logical-flow and Chassis Convergence Validation

- 日付: 2026-08-10
- 状態: PASS / P1-C03 In Progress
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`

## Scope

OVN `SB_REALIZED` の後段に、Logical Flow coverage と Chassis/Encap registration を独立 evidence として追加しました。

```text
current OVN SB Port Binding / datapath
        ├─ individual/shared Logical Flow read-back
        │      └─ required ingress/egress coverage
        └─ expected Chassis identity / Encap read-back
               └─ allowed Geneve profile + endpoint
                         ↓
immutable evidence
                         ↓
CONTROL_PLANE_CONVERGED
```

`CONTROL_PLANE_CONVERGED` は OVN control-plane の coverage/registration 状態です。Host OVS programming、cross-chassis tunnel traffic、end-to-end reachability、Guest readiness は主張しません。

## Persistence qualification

fresh PostgreSQL 17 へ migration 001〜025 を適用し、次を確認しました。

- Logical Flow evidence と Chassis/Encap evidence は immutable
- evidence は current intent、Port/Binding generation、SB observation に結び付く
- ingress/egress pipeline、current Port identity coverage、expected chassis/Encap のいずれかが不足すれば convergence へ進めない
- same evidence identity / different digest を拒否する
- stale Port generation の flow/chassis evidence を transaction 全体で拒否する
- current projection は evidence から再構築可能で、Host dataplane projectionとは分離する

```text
TestMigratePostgreSQLIntegration: PASS
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration: PASS
```

## Real OVN qualification

disposable Logical Switch、Logical Switch Port、OVS Interface を作成し、次を標準 OVN/OVS interface から read-back しました。

- Port Binding の current datapath identity
- `logical_datapath` と共有 `Logical_DP_Group` の両方に属する ingress/egress Logical Flow
- Host OVS `system-id` と SB Chassis identity の一致
- Chassis に関連付く Geneve Encap type、endpoint、options

```text
TestDisposableOVNNBSBChassisConvergence: PASS
disposable OVN/OVS resource cleanup: PASS
```

実 Host は単一 chassis 構成です。この結果は Encap registration を証明しますが、別 Host との Geneve tunnel packet path は証明しません。

## Remaining

- production OVN controller adapter executable/runtime
- controller restart、flow lag、chassis re-registration の long-running reconcile
- 2 Host 以上を使う cross-chassis Geneve tunnel qualification
- Gateway/WIM と end-to-end reachability の独立 probe/evidence
- Network/Router/DHCP/Security の multi-object realization
