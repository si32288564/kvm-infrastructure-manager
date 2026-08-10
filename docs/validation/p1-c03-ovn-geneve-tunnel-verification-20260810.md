# P1-C03 OVN Geneve Tunnel Verification

日付: 2026-08-10  
状態: Foundation PASS / 実 2 Host kernel Geneve qualification PASS / 隔離 OVN-generated Port path PASS / P1-C03 In Progress

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

## 実 2 物理 Host non-disruptive qualification

次の 2 Host 間で、既存 OVS/OVN、VM、route、service を変更せず、専用の一時 kernel Geneve interface だけを使用して cross-chassis packet path を検証しました。

- source: `kvm-base-g01-n001-p.core.s01.si1230.com` (`172.17.0.31`)
- destination: `kvm-base-g02-n001-p.core.s01.si1230.com` (`172.17.0.37`、本番 Host)
- VNI: `16777000`
- UDP destination port: `16082`
- temporary interface: `kimgv260810`
- overlay: `198.18.255.0/30`
- tunnel MTU: `1400`

安全条件:

- interface 名、overlay route/address、UDP port の衝突がないことを作成前に read-only で確認
- 本番 Host の OVS/OVN configuration、bridge、Port、VM、NIC、route、firewall、service は変更しない
- 両 SSH session に `EXIT/HUP/INT/TERM` cleanup trap を設定し、失敗時も一時 interface を削除
- 試験後に interface、overlay address/route、UDP listener が存在しないことを確認
- 試験前後で両 Host の OVS external IDs が不変であることを確認

結果:

| Direction | Probe | Result |
|---|---|---|
| g01 → g02 | ICMP 56-byte payload | 5 sent / 5 received / 0% loss |
| g02 → g01 | ICMP 56-byte payload | 5 sent / 5 received / 0% loss |
| g01 → g02 | DF、1372-byte payload、1400-byte inner packet | 3 sent / 3 received / 0% loss |
| g02 → g01 | DF、1372-byte payload、1400-byte inner packet | 3 sent / 3 received / 0% loss |

この試験により、2 物理 Host 間の management underlay 上で Linux kernel Geneve transport と MTU 1400 の双方向 packet path を確認しました。

ただし、両 Host の既存 `ovn-encap-ip` は `127.0.0.1` であり、今回の専用 interface は OVN controller が生成したものではありません。したがって、current KIM Port/Chassis authority に結び付く OVN-generated cross-chassis tunnel、production uplink profile、tenant end-to-end reachability は引き続き未証明です。

## 実 2 物理 Host の隔離 OVN-generated qualification

前節の kernel Geneve qualification に続き、同じ 2 Host の management underlay を使用し、production OVSDB / OVN DB / VM / NIC 設定から分離した一時 OVN/OVS stack で、OVN controller が生成した Port-bound Geneve tunnel の実 packet path を検証しました。

物理 transport:

- g01 management endpoint: `172.17.0.31/24` (`enp2s0`)
- g02 management endpoint: `172.17.0.37/24` (`eno1`)
- management endpoint 間を next hop とする試験専用 `/32` route だけを追加
- 隔離 namespace endpoint: `169.254.231.2` / `169.254.232.2`
- production address、default route、OVS external IDs、bridge、Port、OVN DB は変更しない

隔離 OVN/OVS topology:

```text
private NB/SB DB + ovn-northd
        ├─ Chassis kim-g01-260810
        │    └─ Port kimport31 (198.18.253.1/30)
        └─ Chassis kim-g02-260810
             └─ Port kimport37 (198.18.253.2/30)

private kernel OVS datapath
        └─ OVN-generated Geneve Interface
             local_ip / remote_ip = isolated endpoint pair
```

両 Port Binding は別 Chassis に bind され、`up=true` へ収束しました。両 Host の private OVSDB で、`ovn-controller` が次の Geneve Interface を生成したことを read-backしました。

| Host | local_ip | remote_ip | type |
|---|---|---|---|
| g01 | `169.254.231.2` | `169.254.232.2` | `geneve` |
| g02 | `169.254.232.2` | `169.254.231.2` | `geneve` |

packet-path result:

| Direction | Probe | Result |
|---|---|---|
| g01 → g02 | ICMP 56-byte payload | 5 sent / 5 received / 0% loss |
| g02 → g01 | ICMP 56-byte payload | 5 sent / 5 received / 0% loss |
| g01 → g02 | DF、1372-byte payload、1400-byte inner packet | 3 sent / 3 received / 0% loss |
| g02 → g01 | DF、1372-byte payload、1400-byte inner packet | 3 sent / 3 received / 0% loss |

両 production management Interface 上の `tcpdump udp port 6081` で、OVN-generated VNI `0x1`、inner source/destination Port IP、request/reply packet を確認しました。capture は両端とも kernel drop 0 でした。

この試験により、次を一続きで確認しました。

```text
isolated OVN Logical Port / Port Binding
→ separate Chassis
→ ovn-controller generated Geneve Interface
→ management underlay transport
→ remote Geneve decapsulation
→ destination Logical Port
```

この PASS は隔離 OVN profile の 2 物理 Host packet-path qualification です。production OVN DB の変更、production Port Binding、tenant L3 reachability、Guest readiness、application health の証明には昇格しません。

cleanup 後、両 Host で試験 namespace、veth、route、private DB/process/socket、test bridge/Port/Geneve Interface が存在しないことを確認しました。production `ovn-encap-ip=127.0.0.1`、`ovn-remote`、OVS external IDs、kernel datapath、management IP/route は試験前の状態を維持しています。

## Validation result

- `go test ./internal/network/ovnadapter ./internal/persistence/postgres`: PASS
- fresh PostgreSQL 17、migration 001〜026、全 persistence integration: PASS
- `kvm-base-g01-n001-p.core.s01.si1230.com`、Linux kernel 7.0.0-28-generic、isolated Geneve fixture: PASS
- network namespace / temporary fixture: test cleanup により削除
- 実 2 Host kernel Geneve cross-chassis qualification: PASS
- 実 2 Host の隔離 OVN-generated Port-bound Geneve / MTU 1400 qualification: PASS
- cleanup / OVS configuration non-interference verification: PASS
- production OVN runtime と current KIM Port authority を結合した qualification: 未実施。隔離 qualification の PASS へ暗黙昇格しない
