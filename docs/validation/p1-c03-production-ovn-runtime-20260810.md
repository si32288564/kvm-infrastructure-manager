# P1-C03 Production-shape OVN Runtime Validation

- 実施日: 2026-08-10
- 対象: v2 OVN Port intent、共有 Logical Switch ownership、closed runtime apply/read-back
- 実 Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- 状態: PASS

## 1. Purpose

従来の `kim.network-intent.ovn-port/v1` は、一つの Port intent が Logical Switch と Logical Switch Port の共通 marker set を持っていました。同じ Network へ二つ目の Port を追加すると、共有 Logical Switch の intent marker が last-writer-wins で置き換わる可能性があります。

v2 contract は ownership を次のように分離します。

```text
Logical Switch
  -> Network ID / Network generation / Project / KIM owner

Logical Switch Port
  -> Port intent ID / intent generation / object-set digest
  -> Port / Segment / Host mapping / Binding generations
```

## 2. Runtime Boundary

Production-shape runtime は次だけを入力にします。

- PostgreSQL で current generation を再検証して commitした immutable v2 plan
- object-set digest
- current Host Network mapping に bindした OVN Chassis name
- 管理者設定の NB/SB endpoint、standard CLI path、timeout、必要な TLS material path

次を Port/API payload から受け取りません。

- OVN DB endpoint
- executable path
- table / column / raw transaction
- shell / argv
- Chassis override

Endpoint は `unix:` または certificate material を伴う `ssl:` のみです。Plain `tcp:`、非標準 executable、foreign ownership marker は backend mutation 前に拒否します。

## 3. Apply and Read-back Semantics

```text
current v2 plan
  -> deterministic LS/LSP identity
  -> ownership pre-read
  -> one typed ovn-nbctl transaction
  -> NB Network/LSP marker read-back
  -> SB Port Binding/datapath/chassis read-back
  -> immutable PostgreSQL observation
```

Apply command の timeout または response loss は非実行の証明にしません。同じ deterministic object name、Network/Port marker、object-set digestをread-backし、同じ intentへ収束します。

## 4. Real KVM Qualification

Test KVM の既存 OVN NB/SB unix socket と standard `ovn-nbctl` / `ovn-sbctl`、OVS `br-int` を使用しました。既存 Network/Port は変更せず、hash-derivedの一時 Logical Switch、二つの一時 Logical Switch Port、二つの一時 OVS internal Portだけを作成しました。

確認結果:

1. 同一 Network の二つの v2 Port plan が同一 Logical Switch identityを生成した。
2. 二つの Port plan が別の LSP identity、intent marker、object-set digestを生成した。
3. 両 Portとも NB ownership/digestが `MATCHED` へ収束した。
4. 両 Portとも SB Port Binding、datapath、expected Chassisが `MATCHED` へ収束した。
5. 二つ目の Port reconcile後も共有 Logical Switchには Network markerだけが残り、どちらの Port intent IDも記録されなかった。
6. 一時 LS/LSP/OVS Portとtest binaryを削除し、対象 objectのabsenceを確認した。

実行結果:

```text
KIM_OVN_RUNTIME_QUALIFY=1 \
  /tmp/kim-ovnadapter-runtime.test \
  -test.run TestProductionShapeOVNRuntimeReconcile -test.v

=== RUN   TestProductionShapeOVNRuntimeReconcile
--- PASS: TestProductionShapeOVNRuntimeReconcile
PASS
```

## 5. Automated Tests

- deterministic v2 plan / strict digest decode
- same-Network shared marker separation
- typed apply command and NB/SB/chassis read-back
- apply response loss followed by matching read-back
- foreign shared object rejection before apply
- plain TCP / missing SSL material / non-standard executable rejection
- Controller binding of runtime evidence to committed Port/Binding generation
- fresh PostgreSQL 17 migration and Placement/OVN integration

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
documentation contracts valid: 443 requirements, 639 test contracts, 225 links
```

## 6. Scope Boundary

この PASS は KIM の v2 Port intent と production-shape OVN runtime adapter が、実 test KVM の標準 OVN/OVS interface へ安全に接続できることを証明します。次は durable multi-worker work claim、retry/claim expiry evidence、Network/Router/DHCP/Security Policy の独立 aggregate intentを追加します。
