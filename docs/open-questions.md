# 未決事項

- 状態: Active
- 更新日: 2026-08-09

設計を止めずに進めるため、現時点の仮定と、確定が必要な判断を分離します。

| ID | 論点 | 現在の仮定 | 決定期限 |
|---|---|---|---|
| Q-001 | 最初の顧客セグメント | 通信・エッジ事業者 | Phase 0 |
| Q-002 | 最初の Validated Linux 組合せ | Ubuntu/Debian 系と RHEL-compatible 系を最低1つずつ検証 | Phase 0 |
| Q-003 | 導入形態 | containerized Control Plane、deb/rpm/self-contained Agent | Phase 0 |
| Q-004 | Control Plane orchestration | Kubernetes 必須にはしない | Phase 0 |
| Q-005 | Message Bus | NATS JetStream | Prototype 前 |
| Q-006 | Network MVP | VLAN と OVN Geneve | Phase 0 |
| Q-007 | DHCP/L3 実装 | OVN native services | Technical Preview 前 |
| Q-008 | Storage MVP | local LVM、Ceph RBD は次段階 | Developer Preview 前 |
| Q-009 | Database HA の提供範囲 | 外部 PostgreSQL をサポートし、bundled HA は別途判断 | Technical Preview 前 |
| Q-010 | ETSI API 適合レベル | IFA 005 情報モデル mapping、適合宣言範囲は別途定義 | Product Beta 前 |
| Q-011 | UI の初期範囲 | 運用者向け最小 UI、Tenant 操作は API/CLI 優先 | Developer Preview 前 |
| Q-012 | 商用ライセンス | 未決定 | Product Beta 前 |
| Q-013 | 製品名 KVM Infrastructure Manager（KIM）の商標・競合 | 調査が必要 | Product Beta 前 |
| Q-014 | ARM64 対応 | GA 初版では対象外 | Product Beta 前 |
| Q-015 | Multi-PoP | GA 初版では独立 Site として管理、横断配置は対象外 | Product Beta 前 |
| Q-016 | ホスト側コンポーネントの正式名称 | Agent はアーキテクチャ上の仮称 | Phase 0 |
| Q-017 | SUSE 系を最初の Validated set に含めるか | Compatible として設計し、認定時期は未決定 | Technical Preview 前 |
| Q-018 | Identity ownership | 外部IdP/Identity Platformがidentity/credentialを所有し、KIMはbindingのみ | Phase 0 |
| Q-019 | Host remediation boundary | discovery/preflightを必須、mutationは限定typed remediationのみ | Phase 0 |
| Q-020 | Agent transport | 内部Busを直接公開せずAgent Gateway経由を標準案とする | Phase 0 |
| Q-021 | Execution model | Operation / Job / Command / Lease / Attemptを独立domainとして採用 | Phase 0 |
| Q-022 | Placement model | Eligibility / Admission / Scoring / transactional Reservationを採用 | Phase 0 |
| Q-023 | Database HA/DR | HA RPO 0とbackup/DR RPO 5分を分離 | Phase 0 |
| Q-024 | Network boundary | virtual networkはKIM、WAN/inter-PoP/physical switchは外部authority | Phase 0 |
| Q-025 | Failure severity model | resource/scope/severityとAlarm mappingを定義 | Phase 0 |
| Q-026 | Control Plane adapter isolation | 副作用を持つ外部adapterはout-of-processを優先 | Phase 0 |
| Q-027 | Extension SDK/API surface | 最初は内部contractとconformance kit、第三者SDK公開時期は未決定 | Technical Preview 前 |
| Q-028 | Extension trust class assignment | 初期extensionをC0/C1/C2/C3へ正式割当 | Phase 0 |
| Q-029 | Traceability automation | Markdown ID検証から開始し、machine-readable manifestへの移行時期を決定 | Developer Preview 前 |
| Q-030 | OVS/DPDK first Validated versions | distribution/OVS/DPDK/NIC driverの組合せを決定 | Developer Preview 前 |
| Q-031 | PMD assignment policy | automatic/pinned/mixedの初期support範囲 | Phase 0 |
| Q-032 | DPDK HugePage ownership | Host-wide reserved poolとKIM allocation ledgerのauthority境界 | Phase 0 |
| Q-033 | Dataplane disruption policy | ovs-vswitchd restartのimpact/drain/maintenance workflow | Technical Preview 前 |
| Q-034 | Performance policy classes | queue/latency/throughput requirementをTenant APIへ公開する粒度 | Technical Preview 前 |
| Q-035 | Enrollment trust evidence profile | provenance/conflict modelは確定。初期Validated環境で必須にする独立source class、attestation方式、manual fallbackを決定 | Phase 0 |
| Q-036 | Policy-auto enrollment/arming | manual approvalなしで許可するSite/Host classとguardrail | Phase 0 |
| Q-037 | Initial Host Profiles/Baselines | general-compute、nfv-sriov、nfv-ovs-dpdkのcontrol set | Developer Preview 前 |
| Q-038 | Compliance evidence retention | Control Result/evidence/auditの保持期間と容量 | Technical Preview 前 |
| Q-039 | External remediation transport/profile | authority/evidence contractは確定。初期adapterのevent/API transport、IdP identity、evidence retention profileを決定 | Technical Preview 前 |
| Q-040 | Decommission authority | credential revoke、drain exception、physical wipe境界のoperator policy | Product Beta 前 |

## 最初に確定すべき判断

1. 対象顧客と最優先ユースケース
2. 最初の Validated Linux 組合せと support tier
3. Kubernetes 依存を許容するか
4. VLAN、overlay、SR-IOV のリリース優先順位
5. local storage と Ceph のサポート境界
6. ETSI API の適合目標と接続対象 NFVO
7. Agent Gatewayのtransport（long pollingまたはstream）
8. typed infrastructure remediationの初期許可操作
9. fault injection環境とfailure scenario owner
10. 初期releaseで正式に公開するextension points
11. release blocker invariantと手動検証owner
12. OVS-DPDKのinitial resource schemaとsupport matrix
13. Enrollment/Baseline/Complianceのinitial policyとControl catalog
