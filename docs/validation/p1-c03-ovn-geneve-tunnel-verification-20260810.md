# P1-C03 OVN Geneve Tunnel Verification

日付: 2026-08-10  
状態: Foundation PASS / 実 2 Host qualification pending / P1-C03 In Progress

## Scope

OVN control-plane convergence の後段に、異なる current Port/chassis authority 間の directed Geneve packet-path evidence を追加しました。

```text
source/destination Port authority
+ current Host mapping
+ CONTROL_PLANE_CONVERGED at both endpoints
+ current Chassis/Encap evidence
+ tunnel interface identity
+ directed packet probe
        ↓
immutable tunnel evidence
        ↓
VERIFIED / DEGRADED / CONFLICTING / UNKNOWN
```

`VERIFIED` は Geneve transport 上の方向付き packet path を意味します。tenant L3 reachability、Guest readiness、application health は主張しません。

## PostgreSQL authority validation

- source/destination Port は別 Host に bind されていなければ受理しない
- 両 endpoint の `CONTROL_PLANE_CONVERGED` と current Host mapping generation を再検証
- current projection が参照する両端 Chassis/Encap immutable evidence を再検証
- tunnel interface digest、probe protocol、sent/received counters、verifier artifact digest を immutable evidence に固定
- 同一 evidence の replay は冪等、同一 ID / different digest は conflict
- stale mapping generation は reject
- immutable evidence の UPDATE は trigger で reject

## Kernel Geneve packet-path fixture

`kvm-base-g01-n001-p.core.s01.si1230.com` 上で、二つの Linux network namespace、独立 underlay endpoint、VNI 4242 の Geneve interface を作り、3 packet の ICMP probe を通す隔離 fixture を実行しました。`TestIsolatedGenevePacketPath` は 2.11 秒で PASS し、3 packet 全件の通過を確認しました。

この fixture が証明する範囲:

- Linux kernel Geneve interface が両 endpoint で存在する
- 隔離 endpoint 間で encapsulated packet が実際に通過する
- verifier が packet evidence を `VERIFIED` に分類する

この fixture が証明しない範囲:

- 二つの物理 KVM Host 間の tunnel traffic
- OVN controller が生成した tunnel/interface
- production uplink、MTU、firewall、routing profile
- tenant end-to-end reachability、Guest readiness、application health

したがって、実 2 Host cross-chassis qualification は P1-C03 の残 gate として維持します。

## Validation result

- `go test ./internal/network/ovnadapter ./internal/persistence/postgres`: PASS
- fresh PostgreSQL 17、migration 001〜026、全 persistence integration: PASS
- `kvm-base-g01-n001-p.core.s01.si1230.com`、Linux kernel 7.0.0-28-generic、isolated Geneve fixture: PASS
- network namespace / temporary fixture: test cleanup により削除
- 実 2 Host cross-chassis qualification: 未実施、残 gate
