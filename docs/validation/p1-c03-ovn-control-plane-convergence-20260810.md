# P1-C03 OVN Control-plane Convergence Validation

- 日付: 2026-08-10
- 状態: PASS / P1-C03 In Progress
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`

## Scope

OVN Port intent、Northbound materialization、Southbound datapath/chassis realization を、KIM Network authority と Host-side OVS dataplane convergence から分離しました。

```text
current Network / Port / Segment / Host Mapping / Binding
        ↓
immutable OVN Port Intent
        ↓
typed NB apply / read-back
        ↓
immutable NB Observation
        ↓
typed SB datapath / chassis read-back
        ↓
immutable SB Observation
        ↓
current OVN projection = SB_REALIZED
```

`SB_REALIZED` は matching datapath/chassis を観測したことだけを意味します。Host OVS dataplane、end-to-end reachability、Guest readiness、application health は主張しません。

## Persistence qualification

fresh PostgreSQL 17 へ migration 001〜024 を適用し、Placement integration fixture で次を確認しました。

- Port intent は current Network、Port、Segment、Host mapping、Binding generation からのみ commit する
- intent、NB observation、SB observation は immutable evidence として保持する
- apply response を失っても stable ownership marker、intent generation、object digest の read-back で同じ intent へ収束する
- NB/SB observation acceptance transaction で current generation を再検証する
- same evidence identity / different digest を拒否する
- stale Port generation の NB/SB evidence を current projection に昇格しない
- `INTENT_COMMITTED`、`NB_APPLIED`、`SB_REALIZED` を別状態として保持する

```text
TestMigratePostgreSQLIntegration: PASS
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration: PASS
```

## Typed adapter contract

`internal/network/ovnadapter` は deterministic な closed typed Port plan と NB/SB observation contract を提供します。

- caller supplied の raw OVN column、command、argv を受け取らない
- stable KIM external IDs と canonical intent/object digest を生成する
- NB ownership marker または digest の不一致を `CONFLICTING` とする
- SB datapath/chassis 未収束を `UNKNOWN` とし、Host convergence へ昇格しない

```text
go test ./internal/network/ovnadapter: PASS
```

## Real OVN qualification

実 Host の標準 `ovn-nbctl`、`ovn-sbctl`、`ovs-vsctl` を使い、disposable Logical Switch、Logical Switch Port、OVS Interface を作成しました。KIM ownership marker と intent digest を NB から read-backし、SB `Port_Binding` の datapath/chassis realization を観測しました。

```text
TestDisposableOVNNBSBChassisConvergence: PASS
disposable OVN/OVS resource cleanup: PASS
```

この qualification は標準 OVN interface 上の Port 単位 NB/SB/chassis convergence を証明します。production controller service/runtime、Network/Router/DHCP/Security の multi-object transaction、Host OVS dataplane、外部到達性は証明しません。

## Failure semantics

- NB transaction response lossをnon-application proofにしない
- apply response lossから反対create/deleteを発行しない
- unknown/foreign ownership markerを自動adopt/deleteしない
- stale NB/SB evidenceでcurrent projectionを巻き戻さない
- controller/SB lagをHost dataplane failureまたはPort release proofにしない

## Remaining

- production OVN controller adapter executable/runtime wiring
- Network/Router/DHCP/Security intentのmulti-object transaction
- controller restart、SB lag、chassis rebindingのlong-running reconcile
- Port ACTIVE lifecycle、release quarantine、typed cleanup
- OVN logical flow、Gateway/WIM、end-to-end reachabilityの独立 projection
