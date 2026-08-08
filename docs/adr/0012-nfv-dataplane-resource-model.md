# ADR-0012: OVS-DPDK資源を第一級Placement Resourceとして扱う

- 状態: Accepted
- 日付: 2026-08-09

## Context

KIMはCPU Pinning、NUMA、HugePages、PCI/SR-IOVを既にresource modelへ含めています。しかしOVS-DPDKでは、PMD CPU、NUMAごとのDPDK memory、Port/Rx Queue、vhost queue bindingが相互依存し、workload resourceと競合します。OVS設定をHost-local detailとしてだけ扱うと、capacity二重使用とNUMA不整合をfinal admissionで防げません。

## Decision

- PMD/service CPU、DPDK socket memory、Dataplane Port、Rx Queue、VM Dataplane Bindingを第一級resource/claimとする。
- workload CPU/HugePagesと同じHost allocation ledgerで競合を防ぐ。
- PMD/Port/RxQ/VM memoryのNUMA localityをeligibility ruleとする。
- dataplane claimをcompute/network/storage/quotaと同じtransactional final admissionで不可分commitする。
- desired allocation、observed OVS/DPDK binding、telemetryを分離する。
- restart-required設定をdisruptive typed operationとして通常VM createから分離する。
- arbitrary OVSDB/EAL/PCI commandを受け付けず、closed Agent Dataplane Moduleを使用する。

## Consequences

- NFV workload placementとdataplane capacityを一貫して説明・予約できます。
- Host inventory、allocation schema、scheduler、Agent、OVS observation、fault injectionが拡張されます。
- OVS/DPDK version compatibilityと実機performance certificationが製品support範囲に加わります。
- PMD load rebalanceを短期telemetryだけで自動authority変更しないため、proposal/policy/operation設計が必要になります。

