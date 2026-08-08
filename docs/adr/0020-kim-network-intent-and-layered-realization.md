# ADR-0020: KIM network intentとOVN/dataplane realizationを分離する

- 状態: Accepted
- 日付: 2026-08-09

## Context

OVN NB object、SB chassis binding、Host OVS PortはいずれもNetworkの一側面ですが、単独ではKIMのProject ownership、IP/VLAN/VNI allocation、Port Binding authorityを表しません。OVN transaction response loss、controller lag、Host failure時にbackend stateを正本とすると、identity重複、stale binding、誤削除、security policy未適用のACTIVE表示が起こり得ます。

## Decision

- PostgreSQLのNetwork/Subnet/Port/IP/MAC/Segment/Binding/Gateway/NAT/Security claimをKIM authorityとする。
- KIM authorityからimmutable Network Intent Revisionを生成し、typed `plan/apply/observe` adapterでOVNへmaterializeする。
- KIM authority、OVN NB desired、OVN SB realization、Host/dataplane observationを別generation/stateとして保持する。
- IP/MAC/VLAN/VNI/Floating IP claimをtransactionalに一意化し、binding/deleteがUNKNOWNの間は再利用しない。
- Port Bindingを第一級resourceとし、Final AdmissionでHost mapping/MTU/binding/device/security capabilityと不可分commitする。
- NB apply成功だけでACTIVEにせず、binding typeに必要なSB/Host/dataplane verificationを要求する。
- Router/Gateway/NAT/DHCP/Security Policyをversioned intentとして管理し、external/physical network authorityを暗黙取得しない。
- unknown/foreign OVN objectを自動adopt/deleteせず、explicit Adoption/repair Operationを要求する。
- Host recovery/migrationはold/new Port Binding generationとHandoffでstale authorityをfenceする。

## Consequences

- IPAM/Segment registry、Network Intent/Observation、Port Binding/Handoff、Gateway/NAT controllerが必要になります。
- OVN control-plane successと実dataplane connectivityを正確に区別できます。
- identity reuseやrecovery開始がverification待ちで遅れる場合がありますが、重複IP/VNIと誤接続より安全側へ倒せます。
- initial IPAM、provider mapping、Gateway HA、MTU、probe/security policy profileを別途決定する必要があります。
