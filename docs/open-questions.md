# 未決事項

- 状態: Active
- 更新日: 2026-08-09

設計を止めずに進めるため、現時点の仮定と、確定が必要な判断を分離します。

| ID | 論点 | 現在の仮定 | 決定期限 |
|---|---|---|---|
| Q-001 | 最初の顧客セグメント | 通信・エッジ事業者 | Closed (Phase 0) |
| Q-002 | 最初の Validated Linux 組合せ | Ubuntu/Debian 系と RHEL-compatible 系を最低1つずつ検証 | Developer Preview (Deferred) |
| Q-003 | 導入形態 | containerized Control Plane、deb/rpm/self-contained Agent | Closed (Phase 0) |
| Q-004 | Control Plane orchestration | Kubernetes 必須にはしない | Closed (Phase 0) |
| Q-005 | Message Bus | NATS JetStream | Prototype 前 |
| Q-006 | Network MVP | VLAN と OVN Geneve | Closed (Phase 0) |
| Q-007 | DHCP/L3 実装 | OVN native services | Technical Preview 前 |
| Q-008 | Storage MVP | local LVM、Ceph RBD は次段階 | Developer Preview 前 |
| Q-009 | Database HA の提供範囲 | 外部 PostgreSQL をサポートし、bundled HA は別途判断 | Technical Preview 前 |
| Q-010 | ETSI API 適合レベル | IFA 005 情報モデル mapping、適合宣言範囲は別途定義 | Product Beta 前 |
| Q-011 | UI の初期範囲 | 運用者向け最小 UI、Tenant 操作は API/CLI 優先 | Developer Preview 前 |
| Q-012 | 商用ライセンス | 未決定 | Product Beta 前 |
| Q-013 | 製品名 KVM Infrastructure Manager（KIM）の商標・競合 | 調査が必要 | Product Beta 前 |
| Q-014 | ARM64 対応 | GA 初版では対象外 | Product Beta 前 |
| Q-015 | Multi-PoP | GA 初版では独立 Site として管理、横断配置は対象外 | Product Beta 前 |
| Q-016 | ホスト側コンポーネントの正式名称 | KIM Host Agent（本文では Host Agent または Agent） | Closed (Phase 0) |
| Q-017 | SUSE 系を最初の Validated set に含めるか | Compatible として設計し、認定時期は未決定 | Technical Preview 前 |
| Q-018 | Identity ownership | 外部IdP/Identity Platformがidentity/credentialを所有し、KIMはbindingのみ | Closed (Phase 0) |
| Q-019 | Host remediation boundary | discovery/preflightを必須、mutationは限定typed remediationのみ | Closed (Phase 0) |
| Q-020 | Agent transport | 内部Busを直接公開せずAgent Gateway経由を標準案とする | Closed (Phase 0) |
| Q-021 | Execution model | Operation / Job / Command / Lease / Attemptを独立domainとして採用 | Closed (Phase 0) |
| Q-022 | Placement model | Eligibility / Admission / Scoring / transactional Reservationを採用 | Closed (Phase 0) |
| Q-023 | Database HA/DR | HA RPO 0とbackup/DR RPO 5分を分離 | Closed (Phase 0) |
| Q-024 | Network boundary | virtual networkはKIM、WAN/inter-PoP/physical switchは外部authority | Closed (Phase 0) |
| Q-025 | Failure severity model | resource/scope/severityとAlarm mappingを定義 | Closed (Phase 0) |
| Q-026 | Control Plane adapter isolation | 副作用を持つ外部adapterはout-of-processを優先 | Closed (Phase 0) |
| Q-027 | Extension SDK/API surface | 最初は内部contractとconformance kit、第三者SDK公開時期は未決定 | Technical Preview 前 |
| Q-028 | Extension trust class assignment | 初期extensionをC0/C1/C2/C3へ正式割当 | Closed (Phase 0) |
| Q-029 | Traceability automation | Markdown ID検証から開始し、machine-readable manifestへの移行時期を決定 | Developer Preview 前 |
| Q-030 | OVS/DPDK first Validated versions | distribution/OVS/DPDK/NIC driverの組合せを決定 | Developer Preview 前 |
| Q-031 | PMD assignment policy | automatic/pinned/mixedの初期support範囲 | Developer Preview (Deferred) |
| Q-032 | DPDK HugePage ownership | Host-wide reserved poolとKIM allocation ledgerのauthority境界 | Closed (Phase 0) |
| Q-033 | Dataplane disruption policy | ovs-vswitchd restartのimpact/drain/maintenance workflow | Technical Preview 前 |
| Q-034 | Performance policy classes | queue/latency/throughput requirementをTenant APIへ公開する粒度 | Technical Preview 前 |
| Q-035 | Enrollment trust evidence profile | provenance/conflict modelは確定。初期Validated環境で必須にする独立source class、attestation方式、manual fallbackを決定 | Developer Preview (Deferred) |
| Q-036 | Policy-auto enrollment/arming | manual approvalなしで許可するSite/Host classとguardrail | Technical Preview (Deferred) |
| Q-037 | Initial Host Profiles/Baselines | general-compute、nfv-sriov、nfv-ovs-dpdkのcontrol set | Developer Preview 前 |
| Q-038 | Compliance evidence retention | Control Result/evidence/auditの保持期間と容量 | Technical Preview 前 |
| Q-039 | External remediation transport/profile | authority/evidence contractは確定。初期adapterのevent/API transport、IdP identity、evidence retention profileを決定 | Technical Preview 前 |
| Q-040 | Decommission authority | credential revoke、drain exception、physical wipe境界のoperator policy | Product Beta 前 |
| Q-041 | Initial HostGroup dimensions | placement pool、site/rack/power、owner、baseline ringの初期dimension/cardinality | Developer Preview (Deferred) |
| Q-042 | Selector source authority | 初期CMDB/asset source、freshness、manual fallback、external assertion identity | Developer Preview 前 |
| Q-043 | Public Placement Scope | Tenant/NFVOへ公開するAZ/pool model、stable name、Project access policy | Technical Preview 前 |
| Q-044 | Group binding precedence | initial Profile/Baseline binding priority rangeとdirect Host override運用 | Developer Preview (Deferred) |
| Q-045 | Maintenance failure-domain policy | dimension別concurrency、minimum ready、membership drift時のpause/continue条件 | Technical Preview 前 |
| Q-046 | Initial Availability classes | 公開するInfrastructure/Workload/Manual classとdefault禁止方針 | Closed (Phase 0) |
| Q-047 | Fencing evidence profile | BMC/storage/clusterごとのconfirmation source、timeout、FENCE_UNKNOWN runbook | Technical Preview 前 |
| Q-048 | Infrastructure recovery scope | local disk、SR-IOV/PCI、DPDK、shared Volume別のinitial restart-on-other-host support | Technical Preview 前 |
| Q-049 | NFVO/VNFM fault contract | WORKLOAD_MANAGED event mapping、delivery SLO、ack/replay/correlation profile | Technical Preview 前 |
| Q-050 | Availability Rebind policy | existing VM bulk rebindのcanary、approval、maintenance、rollback条件 | Product Beta 前 |
| Q-051 | Northbound resilience profile | 対象NFVO/VNFMのmember/role/separation modelとCore API mapping | Technical Preview 前 |
| Q-052 | Public Failure Domain classes | rack、chassis、power-feed等の公開class、min domain、情報秘匿profile | Technical Preview (Deferred) |
| Q-053 | Resilience member replacement | old VM/source UNKNOWN時のNFVO retry、slot fencing、operator escalation | Technical Preview 前 |
| Q-054 | Recovery budget defaults | Site/Pool/backend別concurrency、rate/burst、queue age、backoff初期値 | Technical Preview 前 |
| Q-055 | Recovery priority/fairness | 公開priority class、Project fair-share、aging、emergency override approval | Product Beta 前 |
| Q-056 | Correlated failure profile | FailureCampaign authorityは確定。初期Host/rack/power/site correlation rule、evidence source、time bound、late merge運用値を決定 | Technical Preview 前 |
| Q-057 | Recovery budget lock schema | canonical ordering契約は確定。初期scope dimension rank、normalized ID encoding、DB isolation/retry上限を決定 | Developer Preview 前 |
| Q-058 | Data retention profile | class別online/archive/tombstone/dedupe期間、legal hold、容量見積りを決定 | Technical Preview 前 |
| Q-059 | Schema compatibility profile | N/N-1 window、initial isolation level、DDL lock budget、backfill batch/checkpoint値を決定 | Developer Preview 前 |
| Q-060 | Backup/PITR profile | PostgreSQL distribution、base/WAL方式、object storage、encryption/key custody、restore drill頻度を決定 | Technical Preview 前 |
| Q-061 | Ceph RBD fencing profile | supported Ceph/libvirt/QEMU version、exclusive-lock/blocklist/watcher evidence、client identity、timeoutを決定 | Technical Preview 前 |
| Q-062 | Local LVM support profile | thin/thick、persistent/ephemeral、VG discovery、failure/loss表示、copy/replication非対応境界を決定 | Developer Preview 前 |
| Q-063 | Storage force operation policy | force detach、client fence、lock break、delete/adoptionのapproval、break-glass、runbookを決定 | Technical Preview 前 |
| Q-064 | Volume encryption lifecycle | key rotation、Secret version、snapshot/clone inheritance、restore/recovery availabilityを決定 | Technical Preview 前 |
| Q-065 | Storage degraded admission | Ceph HEALTH_WARN/ERR、thin data/metadata pressure、external usage時のcreate/attach gateを決定 | Technical Preview 前 |
| Q-066 | Storage durability classes | replication/durability expectation、failure domain、support claim、tenant-visible classを決定 | Product Beta 前 |
| Q-067 | Initial IPAM/Segment profile | IPv4/IPv6、allocation/reuse quarantine、VLAN/VNI ranges、external IPAM連携範囲を決定 | Developer Preview 前 |
| Q-068 | OVN realization profile | NB/SB/Host/dataplane verification、probe、timeout、DEGRADED/UNKNOWN thresholdを決定 | Technical Preview 前 |
| Q-069 | Gateway/NAT/MTU profile | gateway HA chassis、external network/WIM contract、SNAT/FIP、MTU capabilityを決定 | Technical Preview 前 |
| Q-070 | Security policy profile | default policy、stateful/stateless、remote selector、anti-spoof、rule scaleを決定 | Technical Preview 前 |
| Q-071 | Gateway failure-domain profile | Gateway chassisをrack/power-path/siteへ分散するhard constraint、minimum ready、failover budgetを決定 | Technical Preview 前 |
| Q-072 | External network/WIM status contract | `KIM_REALIZED`と`END_TO_END_REACHABLE`の分離、UNKNOWN propagation、WIM evidence/SLOを決定 | Technical Preview 前 |
| Q-073 | IPAM fragmentation and pressure | contiguous range、reserved/excluded range、reuse quarantineを含むcapacity/pressure thresholdとadmission policyを決定 | Product Beta 前 |
| Q-074 | Initial upgrade compatibility window | N/N-1 windowの期間、source/target edge、mixed-version deadline、N-2 handlingを決定 | Developer Preview 前 |
| Q-075 | Component upgrade order and budget | Control Plane/Gateway/worker/Agent/extensionのwave順序、max unavailable、canary/failure thresholdを決定 | Developer Preview 前 |
| Q-076 | Agent delivery and rollback profile | deb/rpm/self-contained Agentのstage/atomic activation/supervisor/local receipt/rollback方式を決定 | Technical Preview 前 |
| Q-077 | Release Manifest and support matrix format | manifest schema、signing/provenance source、artifact registry/offline bundle、compatibility evaluatorを決定 | Developer Preview 前 |
| Q-078 | Rollback retention/finalization | old artifact/decoder/schema保持期間、destructive contract approval、forward repair runbookを決定 | Technical Preview 前 |
| Q-079 | Initial clock source and health profile | DB/Control Plane/Hostのtime source、independent source数、offset/uncertainty/RTT threshold、leap handlingを決定 | Developer Preview 前 |
| Q-080 | Lease and deadline profile | Command/Budget/publisher/GC/Upgrade別maximum lifetime、renewal、transport margin、long-running behaviorを決定 | Developer Preview 前 |
| Q-081 | Agent local deadline protocol | Gateway exchange sample、uncertainty calculation、stream/long-poll時のresync、journal fieldを決定 | Developer Preview 前 |
| Q-082 | Calendar and DST policy | timezone入力、DST gap/overlap、missed maintenance window、approval/catch-up禁止のinitial policyを決定 | Technical Preview 前 |
| Q-083 | Time retention/correlation profile | idempotency/Event decoder horizon、offline interval、clock anomaly GC safety horizon、failure correlation uncertaintyを決定 | Technical Preview 前 |
| Q-084 | Precision time profile | PTP domain/grandmaster/holdover/offsetのHost capability、Compliance、Placement公開範囲を決定 | Technical Preview 前 |
| Q-085 | Leap/smear policy | supported time scale、leap/smear source混在、maintenance/Lease test profileを決定 | Technical Preview 前 |
| Q-086 | Initial CA provider/topology | customer CA、bundled subordinate、Root custody、Site/purpose Intermediate、HSM/KMS integrationを決定 | Developer Preview 前 |
| Q-087 | Certificate profile/lifetime | Agent/Control Plane/integration/backend別SAN/EKU/algorithm、lifetime、renewal lead、rekey/overlapを決定 | Developer Preview 前 |
| Q-088 | Revocation profile | CRL/OCSP/local deny/offline update、freshness、session revalidation、propagation SLOを決定 | Technical Preview 前 |
| Q-089 | Agent trust bootstrap | one-time material delivery、hardware key/TPM requirement、CSR transport、manual/offline recoveryを決定 | Developer Preview 前 |
| Q-090 | Emergency trust authority | CA compromise時のout-of-band authority、two-person approval、TRUST_RECOVERY runbookを決定 | Technical Preview 前 |
| Q-091 | Offline trust update | full/delta Bundle、sequence/previous digest、update cadence、maximum disconnected intervalを決定 | Technical Preview 前 |
| Q-092 | PKI DR/key custody | CA/Secret Provider key backup、new Site reissue、old Site/issuer fencing、revocation sequence recoveryを決定 | Technical Preview 前 |
| Q-093 | Cross-domain trust | NFVO/VNFM/WIM/backend/customer PKIのexplicit relationship、name constraint、trust exposureを決定 | Product Beta 前 |
| Q-094 | Initial Agent multiplexed transport | **Closed**。Developer Preview は gRPC bidirectional stream over HTTP/2 を採用する。typed HTTP/2 の density advantage は将来 profile 候補として保持し、Agent capability contract は `TransportAdapter` から独立させる | ADR-0024、2026-08-09 |

## Phase 0 Close / Defer Register

### Closed

| ID | Decision | Owner | 根拠 |
|---|---|---|---|
| Q-001 | 最初の顧客セグメントを通信・エッジ事業者とする | Product Management | Product Vision、README |
| Q-003 | Control Plane は containerized、KIM Host Agent は deb/rpm/self-contained artifact を提供する | Release Engineering | NFR-OPS-001/006、Release Plan |
| Q-004 | Kubernetes を Control Plane の必須基盤にしない | Architecture | Product Vision、ADR-0003 |
| Q-006 | Network MVP を VLAN、次段階の overlay を OVN Geneve とする | Network | NET-002/003、ADR-0020 |
| Q-016 | 正式名称を KIM Host Agent とする | Product Management | ADR-0001/0004、Agent Protocol Architecture |
| Q-018 | Principal identity/credential は外部 Identity Platform、KIM は Tenant/Project authorization を所有する | Security / IAM | ADR-0005、IAM-001〜006 |
| Q-019 | discovery/preflight と Host mutation を分離し、mutation は closed typed remediation に限定する | Host Lifecycle | ADR-0004/0013、INV-AGT-007 |
| Q-020 | Agent は Agent Gateway へ outbound mTLS 接続し、内部 Message Bus を公開しない | Agent / Security | ADR-0008、INV-AGT-002 |
| Q-021 | Operation / Job / Command / Lease / Attempt を独立 domain とする | Execution | ADR-0002/0007、INV-EXEC-001〜008 |
| Q-022 | dry Eligibility/Admission、Scoring/Selection、transactional Final Admission を採用する | Placement | ADR-0006、INV-PLC-001〜006 |
| Q-023 | Site HA RPO 0 と DR RPO 5分/RTO 60分を分離する | Data / SRE | ADR-0009、NFR-AVL-004/005 |
| Q-024 | KIM は virtual network/provider binding、WIM/外部 authority は WAN/inter-PoP/physical network を所有する | Network | ADR-0020、INV-NET-001/019 |
| Q-025 | failure を resource/scope/severity と Alarm mapping で表現する | Reliability | ADR-0010、Failure Model |
| Q-026 | side effect を持つ Control Plane adapter は out-of-process C2 boundary を優先する | Extensibility / Security | ADR-0011、INV-EXT-001〜006 |
| Q-028 | C0 Core、C1 Restricted Module、C2 Isolated Service、C3 External Integration を採用する | Extensibility | ADR-0011、Extensibility Architecture |
| Q-032 | DPDK/workload HugePage を同一 Host allocation ledger と Final Admission で競合管理する | Dataplane / Placement | ADR-0012、INV-DPL-002/004 |
| Q-046 | Infrastructure Managed、Workload Managed、Manual を availability class とし、implicit/default recovery を禁止する | Availability | ADR-0015、INV-AVR-001、INV-HGR-014 |

### Deferred

| ID | 理由 | Owner | 次の Target Gate |
|---|---|---|---|
| Q-002 | Architecture invariant ではなく実機 certification/support matrix。Ubuntu/Debian 系と RHEL-compatible 系を各1以上という検証目標は維持する | Release Engineering / Host Integration | Developer Preview |
| Q-031 | PMD resource semantics は確定済みで、automatic/pinned/mixed の Validated 範囲は性能 certification profile | Dataplane | Developer Preview |
| Q-035 | provenance/conflict contract は確定済みで、初期 source class、TPM、manual fallback は環境別 certification profile | Security / Host Lifecycle | Developer Preview |
| Q-036 | fail-closed invariant は確定済みで、manual approval なしに許可する Site/Host class は deployment security profile | Security / Host Lifecycle | Technical Preview |
| Q-041 | type/dimension/cardinality semantics は確定済みで、初期 catalog 値は deployment/operations profile | Placement / Operations | Developer Preview |
| Q-044 | unique highest priority と conflict=BLOCKED は確定済みで、数値 range と direct override は運用 profile | Host Lifecycle / Operations | Developer Preview |
| Q-052 | raw topology 非公開と hard constraint は確定済みで、公開 class/min-domain/秘匿範囲は Northbound certification profile | Northbound API / Placement | Technical Preview |

Deferred は Phase 0 Architecture invariant の未決定を意味しません。owner は target gate までに product profile と certification evidence を確定します。

## 後続 Gate で最初に確定する Product Profile

1. Developer Preview の Validated Linux combination、package、support tier
2. Agent Gateway transport と typed remediation の初期許可 operation
3. Image/Flavor、VLAN/IPAM、Local LVM の Developer Preview support boundary
4. PMD assignment、Enrollment evidence、HostGroup catalog/binding の初期 profile
5. Traceability automation、Release Manifest、schema/upgrade compatibility profile
6. Technical Preview の OVN、Ceph fencing、HA/PITR、policy-auto enrollment profile
7. Northbound resilience、Failure Domain、Recovery budget/campaign correlation profile
8. Storage encryption/degraded/durability と Network Gateway/WIM/Security profile
9. Time source/uncertainty/deadline/calendar/retention profile
10. CA topology、certificate/revocation、emergency/offline/DR trust profile

各項目は上表の既存 Q-ID、owner、target gate で追跡します。Phase 0 で Closed 済みの Architecture Decision を再度 Open Question として扱いません。
