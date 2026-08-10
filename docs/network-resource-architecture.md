# Network Resource Architecture

- 状態: Baseline
- 更新日: 2026-08-09

## 1. 目的と責任境界

本書はKIMが提供するVirtual Network、Subnet/IPAM、Port/Binding、VLAN/VNI、OVN intent、DHCP、Router/Gateway、Floating IP/NAT、Security Policyのauthorityとfailure semanticsを定義します。

KIMが所有するもの:

- Project scopeのNetwork/Subnet/Port/Router/Floating IP等のdesired lifecycleとownership
- IP/MAC、VLAN/VNI、Port Binding、Gateway/NATのallocation/claim
- OVNへmaterializeするversioned intentと収束verification
- NFVI-PoP内のVM connectivity、anti-spoofing、Security Policy

KIMが所有しないもの:

- physical switch/routerのconfiguration/lifecycle
- WAN/transport path、inter-PoP connectivity、carrier routing
- external DHCP/DNS/IPAM authorityを連携する場合の外部resource lifecycle
- guest OS内のaddress configuration/application reachability

Provider network/physical mapping/WIM connectivityは参照・capability contractとして扱い、外部fabricを暗黙構成しません。

## 2. Layered Authority Model

```text
KIM Current Authority
  Network/Subnet/Port/IP/Segment/Binding/Policy Claim
          ↓ Outbox / Operation
KIM Network Intent Revision
          ↓ typed plan/apply
OVN Northbound desired object
          ↓ OVN control-plane realization
OVN Southbound binding / logical flow
          ↓ controller programming
Host OVS / NIC / dataplane observation
```

- PostgreSQLのKIM resource/claimだけがownershipとallocation authorityです。
- Network Intent RevisionはKIM authorityから導出したimmutable apply contractです。
- OVN NBはdownstream desired-state materializationであり、KIM resource ownershipの正本ではありません。
- OVN SB/chassis binding/logical flowとHost OVS/NIC/packet-pathはobserved realizationです。
- 上位layerの成功だけで下位dataplane convergenceを宣言しません。

## 3. Core Resource Model

```text
Network
├─ network_id / project_id / generation
├─ type: PROVIDER_VLAN | GENEVE_OVERLAY
├─ mtu_policy / administrative state
├─ segment_claim_id
└─ current_intent_revision

Subnet
├─ subnet_id / network_id / IP family / CIDR
├─ gateway / DHCP / DNS / route intent
├─ IPPool / exclusion / reserved ranges
└─ generation / current allocation summary

Port
├─ port_id / project_id / network_id
├─ MAC/IP identity claims
├─ security policy / anti-spoof profile
├─ binding profile / desired_host
├─ desired_generation / current_binding_id
└─ observed connectivity summary

PortBinding
├─ binding_id / port_id / binding_generation
├─ type: OVS | VHOST_USER | SRIOV_DIRECT
├─ Host/chassis/device claims
├─ segment/provider mapping revision
├─ state / authority generation
└─ intent/observation references
```

Router、Router Interface、Gateway Binding、Floating IP/NAT Binding、DHCP Profile、Security Policy/RuleもProject ownershipとgenerationを持つ第一級resourceです。

## 4. IPAM and Network Identity Claims

```text
IPPool
├─ subnet_id / range / exclusions
├─ allocation policy / generation
└─ reserved gateway/DHCP/infrastructure addresses

NetworkIdentityClaim
├─ network/subnet/port
├─ IP address / MAC address
├─ claim generation / state
├─ allocation source: AUTOMATIC | EXPLICIT | EXTERNAL
├─ external authority reference if any
└─ release/quarantine evidence
```

- IP addressはSubnet/IP family内、MAC addressはNetwork identity scope内でactive uniquenessをDB constraint/registryにより保証します。
- isolated Network間のoverlapping CIDRは許可できますが、同一routing/attachment scopeで曖昧になる構成は拒否します。
- gateway、DHCP、reserved/excluded addressを一般Portへ割り当てません。
- explicit IP/MAC要求もProject ownership、pool、conflict、policyを検証します。
- automatic allocation の dry Eligibility は候補の存在だけを read-only で評価し、具体的な identity を返したり Claim を作ったりしません。Final Admission は Subnet/Network scope を PostgreSQL transaction 内で直列化し、current pool、exclusion、`RESERVED | ACTIVE | RELEASE_PENDING | QUARANTINED` Claim を再読込して concrete IP/MAC を選びます。
- Port createとIP/MAC Claim、Quota、Operation/Outboxを同じtransactionでcommitします。
- external IPAMを使用する場合、外部reservation claimをInboxで受け、KIM Claimへbindします。外部応答だけでKIM PortをACTIVEにしません。

Port delete/unbind要求時にIP/MACを直ちに再利用しません。current Port Binding、OVN NB/SB、Host dataplane、NAT/DHCP/anti-spoof referenceのabsenceを検証し、identity claimを`RELEASE_PENDING -> QUARANTINED -> RELEASED`へ進めます。単一 observation または timeout は解放証明にせず、current authority generation に結び付く二つの独立した完全 absence observation を要求します。observation generation は単調増加とし、`UNKNOWN`、`CONFLICTING`、stale evidence 中は解放しません。`RELEASED` は過去または新しい遅延 evidence で `QUARANTINED` へ逆戻りさせません。

## 5. VLAN/VNI Segment Authority

```text
SegmentPool
├─ pool_id / type: VLAN | VNI
├─ physical_network or overlay_domain
├─ allowed ranges / exclusions
├─ allocation policy / generation
└─ external mapping/capability reference

SegmentClaim
├─ network_id / segment_id
├─ pool_id / generation / state
├─ provider mapping revision
└─ release / observation evidence
```

- VLAN IDはphysical network scope、VNIはoverlay domain scopeでactive uniquenessを保証します。
- provider VLANはKIMが許可pool内のclaimとOVN/Host mappingを所有しますが、physical switch trunk/access configを所有しません。
- Geneve VNIはKIM allocation ledgerで確定し、OVNが任意に割り当てた値を後から正本にしません。
- Segment ClaimはNetwork lifecycleと同じtransactionで確定し、unknown/stale external mappingやHost reachabilityをeligibleとみなしません。
- Network delete後もPort/Router/Gateway/NAT/DHCP referenceとOVN/dataplane absenceを確認するまでVLAN/VNIを再利用しません。

## 6. Port Binding and Placement

Port BindingはPort identity/allocationと実Host接続を分離します。

```text
UNBOUND
  -> RESERVED
  -> BINDING
  -> VERIFYING
  -> ACTIVE
  -> UNBIND_REQUESTED
  -> UNBINDING
  -> RELEASE_VERIFYING
  -> UNBOUND

Any non-terminal state
  -> UNKNOWN | BLOCKED | FENCE_REQUIRED
```

Final Admissionは次をlatest generationで評価し、VM Allocation、Network Identity/Segment/Device Claim、Port Binding、Quota等を不可分commitします。

- Network/Subnet/Port ownership/lifecycle
- IP/MAC/Segment Claim
- Host network mapping、physnet/VLAN reachability、overlay/chassis capability
- MTU、encapsulation overhead、gateway/path capability
- OVS/vhost/SR-IOV/DPDK binding capabilityとdevice ownership
- Security Policy、anti-spoof profile
- backend/intent/observation freshness

一般Portのactive Binding Claimは最大一つです。migration/recoveryは`PortBindingHandoff`でold/new generationを明示し、一時的なprotocol stateを二つの通常active binding authorityとして扱いません。

`ACTIVE`はcurrent DB Binding、OVN NB logical port intent、OVN SB chassis/datapath realization、Host OVS/NIC/device observationがbinding typeごとのcontractで一致した場合だけ宣言します。

### 6.1 Pre-boot Realization and Post-boot Dataplane

Network authority、pre-boot realization、post-boot dataplane convergence は別の状態です。

```text
Port / IP / MAC / Binding Claim
        ↓
pre-boot libvirt NIC / provider realization
        ↓
Boot Readiness = READY
        ↓
VM power state = RUNNING
        ↓
post-boot Host dataplane observation
```

OVS Port の post-boot `CONVERGED` は、current VM/Plan、Network、Segment、Host mapping、Port Binding、pre-boot evidence に加え、active libvirt XML の NIC target と Agent 管理 Segment-to-Bridge mapping、OVS Port の bridge/link state が一致する場合だけ宣言します。Command は bridge 名、raw XML、path、argv を受け取りません。

この状態は Host-side OVS Port が期待した bridge で利用可能であることだけを表し、OVN logical flow/chassis convergence、外部 Network の到達性、Guest readiness、application health を意味しません。これらは独立した observation/projection とします。

## 7. OVN Intent, Apply, and Observation

```text
NetworkIntentRevision
├─ intent_id / aggregate / generation
├─ schema / adapter contract version
├─ canonical desired object set / digest
├─ stable KIM external IDs
├─ preconditions / dependency generations
└─ apply / observation / verification state
```

Network Controller Adapterは`plan -> apply -> observe`のtyped contractを実装します。

- planはpureで、KIM resourceをOVN object proposalへ変換するだけです。
- applyはstable KIM IDs、intent generation、object digestをOVN external IDsへ記録し、一つのOVN transactionで可能な範囲を不可分適用します。
- apply response lossはsame intent ID/generation/digestでNB read-backし、反対delete/createを推測実行しません。
- observeはNB object、SB binding/logical flow、chassis/datapath、Host observationを別generation/freshnessで返します。
- adapterはCore DB/Allocationへwriteせず、任意OVN command/object/columnを受け取りません。

共有 object と Port object の ownership は分離します。Logical Switch は Network ID/generation の stable markerだけを持ち、Logical Switch Port は Port intent ID/generation/object-set digest を持ちます。同一 Network の二つの Port intent が同じ Logical Switchを参照しても、後から適用した Port が共有 Logical Switchを自分の Port intentとして上書きしてはなりません。

Production runtime は current `HostNetworkMapping` に bind された OVN Chassis nameをtyped planへ固定します。adapter設定は管理者管理の `unix:` またはcredential付き `ssl:` NB/SB endpoint、standard `ovn-nbctl`/`ovn-sbctl` path、bounded command timeoutだけを許可します。Port/API payloadからDB endpoint、CLI path、OVN table/column、argvを受け取りません。apply前にdeterministic object nameのownership markerをread-backし、foreign/conflicting objectを上書きしません。apply responseがtimeout/lostでも別objectまたは反対operationへ進まず、同じNetwork/LSP marker、object digest、SB Port Binding/datapath/chassisを再読込します。

Port intent、NB observation、SB observation はそれぞれ immutable evidence として保持し、current OVN projection は current Network/Port/Segment/Host mapping/Binding generation との一致から再構築します。apply response を失っても stable KIM ownership marker、intent generation、object digest を使って同じ NB object を read-back し、反対 operation を発行しません。NB/SB 収束は Host-side OVS dataplane、end-to-end reachability、Guest readiness を暗黙に進めません。

KIM所有markerのないobject、unknown generation、外部管理objectを自動adopt/deleteしません。intent driftはowned objectだけをtyped reconcileし、ownership conflictは`CONFLICTING/QUARANTINED`にします。

## 8. Connectivity Status and UNKNOWN Semantics

Port/Networkの表示状態はlayer別に保持します。

| Layer | 例 |
|---|---|
| `INTENT_COMMITTED` | KIM DB resource/claim/intent revisionがcommit済み |
| `NB_APPLIED` | matching OVN NB objectを観測 |
| `SB_REALIZED` | matching chassis binding/logical flow/datapathを観測 |
| `HOST_PROGRAMMED` | Host OVS/NIC/vhost/VF stateを観測 |
| `DATAPLANE_VERIFIED` | contractで許可されたprobe/telemetryがmatching generationを確認 |

`INTENT_COMMITTED`、`NB_APPLIED`、`SB_REALIZED` は独立状態です。`SB_REALIZED` は matching datapath/chassis を観測したことだけを意味し、`HOST_PROGRAMMED` または `DATAPLANE_VERIFIED` の代替ではありません。

SB realization の後段では、current Port datapath に属する individual/shared Logical Flow の required ingress/egress coverage と、expected Host chassis identity、許可された Encap type、endpoint registration を別々の immutable evidence として保持します。両方が current intent/SB generation に一致した状態を `CONTROL_PLANE_CONVERGED` としますが、これは cross-chassis tunnel traffic、Host OVS programming、end-to-end reachability を証明しません。単一 chassis の Encap registration と複数 chassis 間の tunnel datapath verification は別 qualification です。

cross-chassis Geneve packet path は、異なる current Host に bind された source/destination Port を方向付き pair として扱います。両端の current `CONTROL_PLANE_CONVERGED`、Host mapping generation、Chassis/Encap evidence、tunnel interface identity と bounded packet probe を PostgreSQL で再検証し、送信 packet が全て受信された immutable evidence だけを `VERIFIED` projection へ昇格します。これは tunnel transport の観測であり、tenant L3 reachability、Guest readiness、application health の証明ではありません。単一 Host 内の network namespace fixture は kernel Geneve packet-path verifier の検証にだけ用い、実 2 Host qualification の代替にはしません。

NB apply成功だけでPortをACTIVEにしません。SB/Host/dataplaneが未収束なら`PROVISIONING/DEGRADED/UNKNOWN`を区別します。timeout、controller reconnect、chassis row消失、heartbeat lossをunbind/release/fencingの証明にしません。

Network-side `UNKNOWN`では次を禁止します。

- IP/MAC/VLAN/VNI/Floating IPの再利用
- old Bindingを解放したとみなすnew Host binding
- unknown NAT/Gateway ruleの反対operation
- unknown/foreign OVN objectの削除
- Security Policyをdefault allowへ緩和するfallback

## 9. DHCP and Address Delivery

KIMはSubnetのDHCP enablement、options、DNS/route、Port IP/MAC bindingをdesired intentとして所有します。OVN DHCP configurationとHost/dataplane realizationを検証します。

guestが実際にleaseを取得・適用した状態はobservationであり、KIM IPAllocation authorityを変更しません。DHCP unavailable時に同じIPを別Portへ再割当しません。external DHCP integrationではserver lifecycleを所有せず、reservation/option contractとdelivery evidenceだけを扱います。

## 10. Router, Gateway, Floating IP, and NAT

Router InterfaceはSubnet/Router ownership、IP Claim、route overlapをtransactionalに検証します。North-south connectivityはversioned `GatewayBinding`へprovider mapping、eligible gateway group/chassis、external network reference、HA policy、health generationをbindします。

Floating IPはExternal IP Poolのunique ClaimとProject ownershipを持ち、fixed Port/IPとの`NATBinding`を第一級resourceとして管理します。Floating IP Claim、NAT Binding、Router/Gateway dependency、Operation/Outboxを不可分commitします。

Gateway/NATのACTIVEはKIM DB Claim、OVN NB intent、SB gateway/chassis realization、許可されたdataplane probeの一致を必要とします。Gateway failoverはold gateway authority、chassis/session、NAT generationをfenceし、HA policyに従う新Bindingを発行します。physical upstream/WAN reachabilityがUNKNOWNならKIM内intent成功とend-to-end到達性を区別します。

## 11. Security Policy and Anti-spoofing

Security Policy/RuleはProject scope、direction、ethertype、protocol/port、remote selector、stateful/stateless capability、priority semanticsをversion管理します。

- Portのallowed IP/MAC identity claimからanti-spoofing intentを生成する。
- default policyは明示profileとし、controller failure時にdefault allowへfallbackしない。
- rule/PortGroup membership updateをgeneration付きintentとして不可分に適用する。
- unknown/stale policy realizationでは新Port ACTIVEまたはexternal exposureを停止できる。
- raw OVN ACL expressionをTenant入力として受け付けない。

## 12. MTU and Path Capability

Network MTUはTenant要求、provider/overlay mapping、Geneve overhead、Host NIC/OVS/dataplane、gateway/external path capabilityからeffective valueを計算します。未知path MTUを最大値として楽観評価せず、required MTUを満たさないHost/segment/gatewayをPlacement不適格にします。

external pathのend-to-end MTUをKIMが証明できない場合はbounded advertised capabilityとして公開し、WIM/physical network contractを必要とします。silent fragmentation/tunnel type fallbackを行いません。

## 13. SR-IOV and NFV Dataplane Binding

SR-IOV PortはNetwork Identity/Segment ClaimとPCI VF/device claim、IOMMU/Host capability、physical network mappingを同じFinal Admissionに含めます。VF assignment、representor/OVS policy、anti-spoofing、link stateをbinding generation付きで観測します。

OVS-DPDK/vhost-user Portは [NFV Dataplane Resource Architecture](nfv-dataplane-resource-architecture.md) のPMD/RxQ/vhost/NUMA claimと不可分に扱います。binding type間をsilent fallbackせず、SR-IOV/DPDK不適格時に通常OVS Portへ自動変換しません。

## 14. Host Failure, Recovery, and Migration

Host failure時もNetwork identity/Segment Claimは維持し、old Port Binding generationをfenceしてからnew Bindingを作ります。

- Host heartbeat/SB chassis absenceだけでold binding解放を確定しない。
- Agent/Host authority、old Command/Result、device/VF/vhost ownershipをbinding typeごとにfenceする。
- destination Hostのphysnet/overlay/MTU/gateway/security/dataplane capabilityをcurrent Placementで再評価する。
- WORKLOAD_MANAGED/MANUAL/INFRASTRUCTURE_MANAGEDのresponsibilityを変更せず、各branchで同じnetwork safety gateを使用する。

MigrationはPortBindingHandoffへsource/destination generation、protocol mode、OVN chassis transition、Host/device observationを保持します。response loss時に両Host PortやOVN objectを推測削除しません。

## 15. Delete, Release, Reconciliation, and Adoption

Network/Subnet/Port/Router/Gateway/Floating IP deleteはactive/pending Binding、IP/MAC/Segment/NAT/DHCP/Security reference、Migration/Recovery/UNKNOWNを検証します。

- DB tombstone/GCとOVN/Host cleanupを分離する。
- typed deleteとNB/SB/Host absence verification後にidentity/segment claimをreleaseする。
- delete response lossはstable KIM marker/generationでread-backする。
- restore後は`MATCHED/DB_ONLY/BACKEND_ONLY/CONFLICTING/UNKNOWN`へ分類する。
- backend-only OVN objectやHost interfaceを自動adopt/deleteしない。

AdoptionにはProject ownership、stable KIM/external identity、no-conflict、IP/MAC/Segment claim、Security/Gateway dependency、operator authorizationを要求し、新しいresource/intent generationとしてcommitします。

## 16. Security and Adapter Boundary

- Network Controller Adapterをscoped service identity、allowed OVN endpoint/database、network policyで分離する。
- OVN credential、raw chassis/tunnel IP、physical mapping、Host interface detailをTenant API/Eventからredactする。
- provider mapping、Segment Pool、Gateway、external IP Pool、force unbind/delete/Adoptionを個別permission/approvalで保護する。
- adapterはCore DB/Busへ直接接続せず、versioned plan/apply/observe APIだけを使用する。
- Network intentとobservation/auditへdigest、actor、correlationを保持する。

## 17. API and Event Contract

公開resource:

- `/networks`、`/subnets`、`/ports`
- `/routers`、`/floating-ips`、`/security-policies`
- 許可されたprovider/external network、IP/segment pool capability projection

mutationはidempotency key、ETag/generation、Operationを要求します。Eventはresource/intent/binding generation、layer status、bounded reason、Operation/Fault/Recovery correlationを持ちます。raw OVN object/UUID、Host/chassis identity、physical topology、credentialを公開しません。

## 18. Failure Semantics

| Failure | State / Action | Prohibited |
|---|---|---|
| OVN NB response loss | intent UNKNOWN、NB read-back | opposite create/delete |
| SB/controller lag | PROVISIONING/DEGRADED、bounded re-observe | NB successだけでACTIVE |
| Host/chassis loss | binding FENCE_REQUIRED | identity/segment release、blind rebind |
| IPAM/Segment conflict | transaction rollback/reselect | duplicate claim、last-wins |
| DHCP failure | address delivery degraded | IP reallocation |
| Gateway/NAT unknown | external exposure UNKNOWN/BLOCKED | opposite NAT、Floating IP reuse |
| Security realization unknown | new exposure/ACTIVE block | default allow fallback |
| backend-only object | quarantine | auto adopt/delete |

## 19. Verification Contract

- concurrent IP/MAC/VLAN/VNI/Floating IP claim
- Port create/delete response lossとidentity reuse guard
- OVN NB commit response loss、SB lag、controller restart、stale chassis binding
- old/new Port Binding generationとHost recovery/migration handoff
- provider VLAN mapping欠損、Geneve VNI conflict、MTU mismatch
- DHCP option/lease delivery、Router Interface、Gateway HA、NAT/Floating IP
- Security Policy generation、anti-spoof、default-deny/fail-closed
- SR-IOV/DPDK bindingとPCI/PMD/RxQ transactional admission
- unknown/foreign OVN object、restore classification、explicit Adoption
- adapter secret/redaction、typed operation、UNKNOWN/read-back conformance
