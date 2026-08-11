# Architecture Invariants

- 状態: Baseline
- 更新日: 2026-08-09

## 1. 目的

実装、レビュー、テストが絶対に破ってはいけない条件をID化します。Invariantに違反する実装は、機能的に動作していても受け入れません。

## 2. 運用規則

- Invariant IDは再利用しない。
- 内容を弱める変更はADRを必要とする。
- Requirement、Architecture、ADR、TestはInvariant IDを参照する。
- 自動検証できないInvariantには、review gateまたはoperational evidenceを割り当てる。
- `Proposed` ADRに依存するInvariantはPhase 0 gateでADR Accepted後に実装authorityとなる。

## 3. Authority and Identity

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-AUTH-001 | User/Northbound Service Principal credential authorityは外部Identity Platformであり、KIMはそのcredentialを発行しない | AT-AUTH-001 |
| INV-AUTH-002 | KIMはissuer+subjectでPrincipalを識別し、Tenant/Project MembershipとRole Bindingを所有する | AT-AUTH-002 |
| INV-AUTH-003 | Host credentialはidentityを証明するが、Host mutation authorityそのものではない | AT-AGT-003 |
| INV-AUTH-004 | stale authority generationはCommand LeaseとResultを進められない | FI-SPLIT-001 |
| INV-AUTH-005 | backend observationだけでresourceをmanagedへ自動adoptしない | FI-DR-001 |

## 4. API and Data

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-API-001 | Host/backend mutationを同期API request内で実行しない | AT-API-001 |
| INV-API-002 | 同じidempotency scope/keyと同一payloadは同じOperation/結果へ収束する | AT-API-002 |
| INV-API-003 | 同じidempotency keyの異なるpayloadはconflictになる | AT-API-003 |
| INV-DATA-001 | desired state、allocation、attachment、execution authorityはPostgreSQL commitでのみ確定する | AT-DATA-001 |
| INV-DATA-002 | desired stateとobserved stateを別resource/generationとして保持する | AT-DATA-002 |
| INV-DATA-003 | terminal Job/Attempt/audit historyを結果に合わせて書き換えない | AT-EXEC-007 |
| INV-DATA-004 | Derived ProjectionとMessage delivery状態をresource/ownership/execution authorityにしない | AT-DATA-003 |
| INV-DATA-005 | domain mutation、Operation/idempotency、Outbox Eventを一つのtransactionでcommit/rollbackする | FI-DATA-001 |
| INV-DATA-006 | Inboxの同一source/generation/message ID+digestは同じReceiptへ収束し、異なるdigestはconflictにする | FI-DATA-003 |
| INV-DATA-007 | current authorityが参照するDecision/Evidence、UNKNOWN、open Operation、active Lease/Claim、legal holdをGCしない | FI-DATA-004 |
| INV-DATA-008 | DB GC/partition detach/archiveはbackend resource mutationを開始しない | AT-DATA-008 |
| INV-DATA-009 | tombstoneはresource identity、scope、final generation、delete decision、integrity digestを保持する | AT-DATA-007 |
| INV-DATA-010 | partitioningはauthority uniqueness、transactional admission、Tenant isolationを分裂させない | AT-DATA-009 |
| INV-DATA-011 | schema switch前にN/N-1 reader/writer compatibilityとrequired replica capabilityを検証する | FI-DATA-007 |
| INV-DATA-012 | migration/backfillはsingle Lease、artifact digest、checkpoint、bounded lock/batch、verificationを持つ | AT-DATA-011 |
| INV-DATA-013 | backfillはcurrent generationの並行更新を上書きせず、retryで同じ結果へ収束する | FI-DATA-006 |
| INV-DATA-014 | restore可能なbackupはbase/WAL/schema/migration/artifact/checksumを一つのmanifestへbindする | FI-DATA-008 |
| INV-DATA-015 | PITR後のrestore epochはpre-restore Lease、session、worker/publisher claimをcurrent authorityからfenceする | FI-DATA-009 |
| INV-DATA-016 | restore後はread-only classification/reconciliation前にmutation authorityを再開しない | AT-DATA-015 |
| INV-DATA-017 | BACKEND_ONLY/CONFLICTING/UNKNOWN resourceを自動adopt/deleteしない | FI-DATA-011 |
| INV-DATA-018 | PITR後の再送はstable ID/Receiptでdeduplicateし、外部side effect不明をUNKNOWNとしてread-backする | FI-DATA-010 |
| INV-DATA-019 | restore epochだけで旧Site/primaryをfencedとみなさず、外部DR fencing proofなしに通常mutationを再開しない | FI-DATA-013 |
| INV-DATA-020 | authority/history/archive referenceをhard DB、verified logical、archive referenceへ分類し、欠損/不一致scopeのmutation/GCを停止する | FI-DATA-014 |
| INV-DATA-021 | Recovery Control writeは専用identity/DB role/API/DR generation/approval/auditを要求し、通常resource/backend mutation権限を持たない | FI-DATA-015 |

## 5. Placement

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-PLC-001 | eligibility=falseのHostはscoreに関係なく選択されない | AT-PLC-001 |
| INV-PLC-002 | dry evaluationはDB/backend/Agent/Busへ副作用を起こさない | AT-PLC-002 |
| INV-PLC-003 | final admissionはlatest authority stateへ同じadmission ruleを再適用する | AT-PLC-003 |
| INV-PLC-004 | compute/NUMA/HugePages/PCI/network/storage/quota claimは一つのtransactionで不可分にcommitする | AT-PLC-004 |
| INV-PLC-005 | final admission競合時は部分予約を残さず、残候補の再選択または再評価へ戻る | AT-PLC-005 |
| INV-PLC-006 | final admission transaction中にbackend side effectを実行しない | AT-PLC-006 |
| INV-PLC-007 | migration capabilityはVM/resource bindingとsource/destinationの組合せで評価する | AT-PLC-007 |

### Image / Flavor

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-IMG-001 | Image revision の metadata、checksum、signature evidence は immutable とし、checksum または signature verification 不一致の revision を current boot authority へ昇格しない | AT-IMG-001、AT-IMG-002 |
| INV-IMG-002 | Agent は Image URI/path を Command から受け取らず、admin-configured digest-addressed cache と current BOUND LV identity のみを使用する。copy response ではなく target LV の `image_size_bytes` 範囲の SHA-256 read-back が current RAW revision と一致した場合だけ Image を `REALIZED` に進める。format conversion を byte-copy として扱わない | AT-IMG-003 |
| INV-FLV-001 | Flavor revision は immutable canonical shape とし、vCPU、memory、root disk、NUMA、HugePages、CPU allocation/pinning、extra specs を欠落なく Placement Request へ伝播する | AT-FLV-001 |
| INV-FLV-002 | Image/Flavor catalog mutation は `ACTIVE` database authority でのみ行い、`RECOVERY_READ_ONLY` では fail closed とする | FI-DATA-015 |

### VM Materialization

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-CMP-001 | VM define plan は accepted Final Admission と current resource authority からのみ生成し、Image/Network 未実現を READY/RUNNING へ昇格せず、caller supplied XML/path/libvirt method/flagを受け入れない | AT-CMP-009 |
| INV-CMP-002 | Domain `DEFINED` はboot readinessではなく、current generationのDomain/Image/Network/Storage evidenceがすべて収束するまでpower-on authorityを発行せず、stale component evidenceでreadinessを進めない | AT-CMP-010 |
| INV-CMP-003 | Image `REALIZED` は Network realization または boot authority を暗黙に進めず、Network `PENDING` 中は boot readiness を `BLOCKED` に保つ | AT-CMP-011 |
| INV-CMP-004 | READY 判定は current Domain/Storage/Image と required Port 全件の current evidence を再検証する transaction とし、その transaction からだけ typed RUNNING Command authority を発行する | AT-CMP-012 |
| INV-CMP-005 | power Command の Result、process liveness、transport ACK だけで VM runtime state を確定せず、current VM generation と READY authority に結び付く standard libvirt read-back evidence だけを current power projection へ昇格する | AT-CMP-013, FI-LIBVIRT-004 |

## 6. Execution

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-EXEC-001 | Commandごとにactive Leaseは最大一つ | AT-EXEC-001 |
| INV-EXEC-002 | Agentはbackend mutation前にCommandをdurable journalへwrite-before-executeする | FI-AGENT-001 |
| INV-EXEC-003 | Lease失効後に初めて届いた旧Attempt Resultはauthorityを変更できない | FI-TRANSPORT-001 |
| INV-EXEC-004 | durably accepted済みの同一Result再送だけが同じreceiptを得られる | AT-EXEC-004 |
| INV-EXEC-005 | UNKNOWNをFAILED/SUCCEEDEDへ書き換えず、verification evidenceを追記する | AT-EXEC-005 |
| INV-EXEC-006 | Agent Resultの成功だけではJobを成功にせず、後続observationを必要とする | AT-EXEC-006 |
| INV-EXEC-007 | Attemptはappend-onlyで、stale Resultは新Attemptを進めない | FI-TRANSPORT-001 |
| INV-EXEC-008 | UNKNOWN状態で反対mutationを推測実行しない | FI-STORAGE-001 |
| INV-EXEC-009 | active Command Lease は発行時の Host authority generation と Agent session generation の両方が current の間だけ使用できる | AT-EXEC-008、FI-TRANSPORT-003 |
| INV-EXEC-010 | Lease expiry、Host authority fence、session generation 変更後に旧 Lease/Attempt が再び current authority へ戻らない | AT-EXEC-009、FI-TRANSPORT-003 |
| INV-EXEC-011 | Gateway の live outbound registry は routing projection に限定し、PostgreSQL Session Grant と一致しない Host/session generation へ Command を配送しない | AT-EXEC-010、FI-GATEWAY-003 |
| INV-EXEC-012 | Agent は compile-time registered typed backend だけを実行し、journal 完了前の Result または read-back 未確認の success を authority へ進めない | AT-EXEC-010、FI-AGENT-001/002 |
| INV-EXEC-013 | UNKNOWN Command の resync は既存 write-before-execute journal evidence を新規生成または改変せず、current authorized session と immutable Command/Attempt/digest/target identity が一致する場合だけ read-back observation を受理する | AT-EXEC-012、FI-AGENT-005 |
| INV-EXEC-014 | read-only verification は fenced Host mutation authority を暗黙 rearm せず、matching observation を append して current Command/Job decision だけを収束させる | AT-EXEC-012、FI-TRANSPORT-004 |
| INV-EXEC-015 | Agent session runtime は inbound routing、outbound multiplexing、durable Receipt 処理を一つの current transport session で駆動し、transport loop termination を backend side effect の absence と解釈しない | AT-EXEC-013、FI-TRANSPORT-004 |
| INV-EXEC-016 | local session generation ledger は SessionAccepted 後だけ fsync/atomic rename で進み、rejected/failed attempt、reconnect timer、process start だけでは generation を消費または authority として確定しない | AT-EXEC-014、FI-AGENT-006 |
| INV-EXEC-017 | Worker の Lease expiry scan は discovery に限定し、各 Lease の current state/DB time/Host authority scope を transaction で再検証してから既存 UNKNOWN semantics を適用する | AT-EXEC-015、FI-TRANSPORT-001 |
| INV-EXEC-018 | Command Lease delivery Outbox は plaintext Lease token を永続化せず、AEAD key ID/algorithm/nonce/ciphertext と token digest だけを保持し、Lease Grant と intent の一方だけを commit しない | AT-EXEC-016、FI-BUS-003 |
| INV-EXEC-019 | NATS/JetStream message、PubAck、consumer ACK は mutation authority、Agent receipt、backend execution evidence のいずれにもならず、PostgreSQL current authority の再検証を迂回しない | AT-EXEC-017、FI-BUS-004 |
| INV-EXEC-020 | Gateway は Inbox acceptance 済みの duplicate でも current Lease/Host/session authority を毎回再検証し、stale authority または generation mismatch を Outbound Registry へ渡さない | AT-EXEC-018、FI-BUS-005 |
| INV-EXEC-021 | Gateway live-stream route が失敗または不明な Bus message を ACK せず、同一 message identity/digest/envelope で redelivery する。digest conflict は quarantine し自動 merge しない | AT-EXEC-019、FI-BUS-006 |
| INV-EXEC-022 | JetStream stream/consumer failover は stable Bus message identity/digest と durable consumer state を維持し、redelivery を新しい domain decision、Lease、Attempt、Agent receipt、backend execution evidence へ昇格させない | AT-EXEC-020、FI-BUS-007 |
| INV-EXEC-023 | process restart または Bus leader change は Host/session authority を暗黙 rearm せず、Agent 不在中の delivery は NAK/redelivery、new session generation により stale となった Lease は terminal fence とし、NATS ACK だけで Agent spool を削除しない | AT-EXEC-021、FI-BUS-008 |
| INV-EXEC-024 | Result/Observation と application Receipt の PostgreSQL commit 後に Receipt transport response が失われても、Agent は spool を保持し、new session generation から同一 message identity/digest を replay して original accepted generation の Receipt を回収した後だけ spool entry を一度削除する | AT-EXEC-022、FI-GATEWAY-008 |
| INV-EXEC-025 | Agent stream write と JetStream ACK の間で Gateway が停止しても、redelivery は PostgreSQL current Lease/Command authority を再検証し、terminal Command を Agent へ再配送せず、新しい Lease/Attempt または重複 backend side effect を生成しない | AT-EXEC-023、FI-BUS-009 |
| INV-EXEC-026 | VM power-state backend は compile-time registered Command/schema、`vm:<UUID>` target、RUNNING/SHUTOFF desired state と標準 libvirt API だけを受理し、Agent process kill 後も QEMU/KVM Domain stateを変更せず journal付きread-backでUNKNOWNを解決する | AT-EXEC-024、FI-LIBVIRT-003 |
| INV-EXEC-027 | UNKNOWN Verification delivery は current authorized session、immutable Command/Attempt/target/payload digest を PostgreSQL で再検証し、durable Outbox/Inbox を通るが、Host mutation authority または新 Lease/Attempt を生成しない | AT-EXEC-025、FI-TRANSPORT-004 |
| INV-EXEC-028 | expired/fenced Attempt の初回旧 Result replay は authority を変更せず append-only stale-result evidence と durable `STALE` Receipt を生成する。non-`ACCEPTED` Receipt は spool evidence を解放しないが、current multiplexed session または後続 Verification を失敗させない | AT-EXEC-026、FI-LIBVIRT-004 |

## 7. Agent and Host

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-AGT-001 | Control Plane/Agentはarbitrary shell、argv、libvirt method/XML/pathを受理しない | AT-AGT-001 |
| INV-AGT-002 | Agentは内部Message Bus credentialを持たず、Gateway経由で通信する | AT-AGT-002 |
| INV-AGT-003 | Command実行にはHost identity、capability、armed authority、current Leaseがすべて必要 | AT-AGT-003 |
| INV-AGT-004 | Gateway障害中、Agentは新規/cached mutationまたは自律rollbackを開始しない | FI-GATEWAY-001 |
| INV-AGT-005 | Gateway復旧またはAgent再起動だけでHost authorityをarmしない | FI-GATEWAY-002 |
| INV-AGT-006 | OS差分はAgent adapterで正規化し、Control Planeをdistribution名で分岐させない | AT-AGT-006 |
| INV-AGT-007 | Host mutationは閉じたtyped remediationに限定し、汎用Configuration Managementを提供しない | AT-AGT-007 |
| INV-AGT-008 | KIM core functionはLinux KVM、QEMU、libvirtのpatch、fork、proprietary modificationを要求しない | AT-AGT-008 |
| INV-AGT-009 | KIM metadataの有無によってunderlying resourceを標準libvirt/QEMU/KVM interfaceから扱えなくしない | AT-AGT-009 |
| INV-AGT-010 | KIM Host AgentはGoをprimary implementation languageとし、cgo/native helperをnarrow audited boundaryに限定する | AT-AGT-010 |
| INV-AGT-011 | Agent module/capability 数を Host identity あたりの mTLS connection/certificate 数へ連動させない | AT-AGT-011 |
| INV-AGT-012 | 一つの Host Agent identity に PostgreSQL current transport session generation は最大一つで、live socket を authority にせず stale session の全 message は current authority を進めない | FI-GATEWAY-003 |
| INV-AGT-013 | transport loss を module/resource authority loss または operation 失敗の証明にせず、UNKNOWN/journal/read-back で解決する | FI-GATEWAY-004 |
| INV-AGT-014 | transport implementation、connection、certificate を Agent capability/module authorization の代替にしない | AT-AGT-012 |
| INV-AGT-015 | logical stream は bounded message/queue と priority-aware backpressure を持ち、bulk stream が Control/Lease/Heartbeat/Result を無期限 starve させない | FI-GATEWAY-005 |
| INV-AGT-016 | transport arrival 順を global resource ordering にせず、ordering scope ごとの sequence/generation/idempotency contract を使用する | AT-AGT-013 |
| INV-AGT-017 | 別 endpoint/connection は明示的な別要件と contract/approval なしに追加しない | AT-AGT-014 |
| INV-AGT-018 | L7 forwarded Agent identity は pinned proxy workload identity と sanitized downstream certificate evidence が同時に成立する場合だけ受理し、header 単独を identity authority にしない | AT-AGT-015 |
| INV-AGT-019 | GOAWAY、proxy drain、rolling restart、upstream connection pool の生存を Host session authority transition にせず、new current session は PostgreSQL Grant commit を必須とする | FI-GATEWAY-006 |
| INV-AGT-020 | connection idle と stream idle を混同せず、active Agent stream の liveness/authority を proxy timer だけで確定しない | FI-GATEWAY-007 |
| INV-AGT-021 | Agent durable message は write-before-send とし、transport send/Receipt delivery を PostgreSQL acceptance commit と同一視せず、matching durable `ACCEPTED` Receipt だけが spool entry を解放できる | FI-GATEWAY-008 |
| INV-AGT-022 | session generation 変更後の同一 message replay は original Receipt へ冪等収束し、stale/new session、response loss、restart のいずれも duplicate decision または evidence rewrite を起こさない | AT-AGT-016 |
| INV-AGT-023 | Inventory module は descriptor で宣言した closed typed domain/schema/capability の外へ evidence を出せず、一つでも module collection が失敗した snapshot を current capability projection にしない | FI-AGENT-003 |
| INV-AGT-024 | Host capability projection は immutable normalized Inventory evidence からだけ導出し、同一 generation の異なる digest を拒否し、古い generation で current projection を巻き戻さない | AT-HST-005 |
| INV-AGT-025 | OS Integration Adapter は raw source の read/parse outcome を typed evidence state と reason code へ変換し、Normalizer は provenance を失わず Snapshot/Projection へ伝播する | AT-HST-006 |
| INV-AGT-026 | AVAILABLE、UNAVAILABLE、UNKNOWN、UNSUPPORTED は相互に置換せず、UNKNOWN を含む snapshot は DEGRADED とし、既知の UNAVAILABLE/UNSUPPORTED だけを理由に observation 全体を UNKNOWN にしない | FI-AGENT-004 |

## 8. Network and Storage

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-NET-001 | KIMはvirtual network/provider bindingを所有し、WAN/inter-PoP/physical switch authorityを暗黙取得しない | AT-NET-001 |
| INV-NET-002 | 未知または外部所有network objectを自動削除しない | FI-NET-001 |
| INV-NET-003 | active IP/MAC Claimは定義scopeで一意で、Port/Identity allocationと不可分commitする | FI-NET-003 |
| INV-NET-004 | Port/NAT/DHCP/binding/dataplane absenceとquarantine完了前にIP/MACを再利用しない | FI-NET-004 |
| INV-NET-005 | VLAN/VNI Claimをscope内で一意にし、reference/dataplane absence前に再利用しない | FI-NET-005 |
| INV-NET-006 | KIM authority、Intent Revision、OVN NB、OVN SB、Host/dataplane observationを別generation/stateで保持する | AT-NET-007 |
| INV-NET-007 | Port ACTIVEはcurrent DB Bindingとbinding-type別NB/SB/Host/dataplane verification後だけ確定する | FI-NET-007 |
| INV-NET-008 | 一般Portのactive Binding Claimは最大一つで、handoffは二つの通常active authorityを作らない | FI-NET-008 |
| INV-NET-009 | Network adapterはCore DB/claimへwriteせずtyped plan/apply/observeだけを実行する | XCT-NET-001 |
| INV-NET-010 | network-side UNKNOWNでidentity/segment再利用、反対操作、blind rebind、security緩和を行わない | FI-NET-006 |
| INV-NET-011 | DHCP lease/runtime observationをIP Allocation authorityにしない | AT-NET-012 |
| INV-NET-012 | Floating IP/NAT/Gateway Claimとdependencyをtransactionalに確定し、UNKNOWN中に再利用しない | FI-NET-011 |
| INV-NET-013 | Security Policy realization不明時にdefault allowへfallbackしない | FI-NET-013 |
| INV-NET-014 | required MTUを満たさない、またはpath capability UNKNOWNのHost/segment/gatewayをeligibleにしない | FI-NET-014 |
| INV-NET-015 | SR-IOV/DPDK Port claimをPCI/PMD/RxQ/NUMAと不可分commitしbinding typeをsilent fallbackしない | AT-NET-020 |
| INV-NET-016 | Host recovery/migrationはold Binding/Host/device authorityをfenceしnew generationを検証する | FI-NET-009 |
| INV-NET-017 | active dependency/UNKNOWN中のNetwork resourceを削除せず、DB GCとOVN/Host cleanupを分離する | FI-NET-015 |
| INV-NET-018 | backend-only/foreign OVN object、unknown interface/chassisを自動adopt/delete/unbindしない | FI-NET-016 |
| INV-NET-019 | Provider mappingはphysical/WIM capability referenceでありswitch/WAN authorityをKIMへ移さない | AT-NET-015 |
| INV-NET-020 | provider pool/gateway/force operation/Adoptionは個別permission/approval/auditを要求しraw topology/credentialをredactする | FI-NET-017 |
| INV-NET-021 | Network Authority、pre-boot realization、post-boot dataplane convergence を相互に昇格させず、Boot READY は通信可能性ではなく power-on 発行可能性だけを意味する | AT-NET-026 |
| INV-NET-022 | OVS Command は stable KIM identity/generation のみを含み、Agent 管理 provider mapping と standard OVS/libvirt read-backが一致するまでPortをpre-boot REALIZEDにしない | AT-NET-026 |
| INV-NET-023 | SRIOV_DIRECT Port は observed VF、current Qualification、`VF_ASSIGN`、allocation policy、exclusive VF Claim の一つでも stale/UNKNOWN/不一致なら REALIZED/READY へ進めない | AT-NET-027, FI-NET-019 |
| INV-NET-024 | VM RUNNING または pre-boot REALIZED だけで OVS dataplane を `CONVERGED` にせず、current generation と active libvirt NIC target、Agent 管理 Segment mapping、OVS Port bridge/link read-back が一致する immutable evidence を要求する | AT-NET-028, FI-NET-020 |
| INV-NET-025 | OVN apply response、NB object、SB datapath/chassis、Host OVS dataplane を同一 authority にせず、current Port intent の stable ownership marker、generation、digest に一致する immutable NB/SB evidence だけを current OVN projection へ昇格する | AT-NET-029, FI-NET-021 |
| INV-NET-026 | current SB Port Binding/datapath に属する required ingress/egress pipeline と Port identity の logical-flow coverage、expected Host chassis identity・許可 Encap profile の immutable evidence が揃うまで OVN control plane を `CONVERGED` にせず、cross-chassis tunnel または reachability を暗黙に証明しない | AT-NET-030, FI-NET-022 |
| INV-NET-027 | directed Geneve tunnel を `VERIFIED` にするには、異なる current Host に bind された source/destination Port、両端の `CONTROL_PLANE_CONVERGED`、current mapping/chassis evidence、tunnel interface identity、送受信 packet evidence を同一 generation scope で再検証しなければならない。単一 Host fixture または Encap registration を実 2 Host qualification に昇格させてはならない | AT-NET-031, FI-NET-023 |
| INV-NET-028 | Automatic IPAM の dry evaluation は Claim を作らず、Final Admission だけが Subnet/Network scope を直列化して protected identity state を除外し、Port/IP/MAC/Binding と不可分に concrete identity を確保する | AT-NET-032, FI-NET-024 |
| INV-NET-029 | `RELEASE_PENDING`、`QUARANTINED`、`UNKNOWN` の Network identity を再利用せず、current generation に対する二つの独立した完全 absence observation が単調増加するまで `RELEASED` にしない。`RELEASED` は stale/new evidence で逆戻りさせない | AT-NET-033, FI-NET-024 |
| INV-NET-030 | 共有 OVN Logical Switch は Network generation の stable ownership marker だけを持ち、Port 固有 intent/digest marker は Logical Switch Port に限定する。同一 Network の別 Port が共有 object ownership を変更してはならない | AT-NET-034, FI-NET-025 |
| INV-NET-031 | OVN runtime は caller supplied command/column/DB endpoint を受け取らず、current typed plan と管理者設定の secure endpointだけを標準 OVN CLI へ変換する。apply timeout/response lossを非実行証明にせず、stable object/marker read-backで解決する | AT-NET-035, FI-NET-025 |
| INV-NET-032 | OVN runtime worker は PostgreSQL の current work claim owner/generation/expiry を持たずに apply または observation acceptance を行わない。expired/uncertain claim の再取得は `READ_BACK_FIRST` とし、旧 worker の遅延結果を current authority へ進めない | AT-NET-036, AT-NET-037, AT-NET-038, FI-NET-026, FI-NET-027, FI-DB-003 |
| INV-NET-033 | OVN runtime claim renewalはcurrent owner/generation、DB authority time上の未失効、固定maximum lifetimeを不可分に検証し、immutable renewal generationを残す。renewal failure/response loss/DB failoverからexpired claimをreviveせず、曖昧なside effectをread-backなしに再実行しない | AT-NET-039, FI-DB-004 |
| INV-NET-034 | renewal commit 後の response loss を非 commit または side effect 不在の証明にしない。worker は local operation を停止し、DB authority の renewed expiry 前に別 claim を発行せず、expiry 後の successor を新 generation の `READ_BACK_FIRST` に限定して旧 worker の継続・completion を fence する | AT-NET-040, FI-DB-005 |
| INV-NET-035 | claimed batch を claim lifetime を消費する未更新の local serial queue にせず、`BatchLimit` 以下で各 item を並行処理して独立 renewal を行う。item-local adapter error は bounded reconciliation を継続するが、DB claim/renewal authority loss を同一 process 内で推測 retry しない。aggregate in-flight claim を downstream authority capacity より大きくする profile を certification せず、overload 時も duplicate apply、unbounded attempt、starvation を許可しない | AT-NET-041, FI-NET-028 |
| INV-NET-036 | 同一 Site HA の repeated failover では、各切替前に current work/renewal evidence が synchronous standby へ `remote_apply` され、old-primary worker が authority error で停止しなければならない。promotion や旧 primary の standby 再参加を restore と扱わず、`restore_epoch` / database authority generation を維持し、各 uncertain attempt を一つの successor `READ_BACK_FIRST` と single physical apply へ収束させる | AT-NET-042, FI-DB-006 |
| INV-NET-037 | OVN endpoint latency、partial timeout、PostgreSQL pool wait を backend failure、side effect 不在、または claim expiry の証明にしない。DB pool を process admission に対して bounded headroom 付きで構成し、Lease を measured authority-path uncertainty より長く、renewal interval を Lease より短く保つ。saturation 中も意図的 uncertain outcome 以外の attempt amplification、duplicate apply、worker starvation を許可しない | AT-NET-043, FI-NET-029 |
| INV-NET-038 | worker drain は process liveness または claim authority の revoke ではない。`DRAINING` を観測した worker は新規 claim を取得せず、current batch の renewal と completion を続け、`STOPPED` を terminal operational projection とする。drain metrics watcher の遅延 event で `STOPPED` を `DRAINING` へ巻き戻さず、hard cancellation 後の claim outcome を推測しない | AT-NET-044, FI-NET-030 |
| INV-NET-039 | drain deadline 超過または 2 回目の termination signal は current adapter operation を hard cancel して process を非 0 で終了させるが、backend side effect 不在または claim revoke を意味しない。current DB expiry 前の successor claim を許可せず、expiry 後に旧 attempt を `DISPATCH_UNKNOWN`、新 generation を `READ_BACK_FIRST` として single physical apply へ収束させる | AT-NET-045, FI-NET-031 |
| INV-STO-001 | attachment outcomeまたはsingle-writer fencingが不明なVolumeを別Hostへattachしない | FI-STORAGE-001 |
| INV-STO-002 | Volume backend capability差を明示し、未対応機能へsilent fallbackしない | AT-STO-002 |
| INV-STO-003 | Volume desired state、Backend Binding、Attachment Intent/Claim、backend/libvirt Observationを別generationで保持する | AT-STO-003 |
| INV-STO-004 | SINGLE_WRITER Volumeのactive Attachment ClaimはPostgreSQL constraintで最大一つ | FI-STORAGE-003 |
| INV-STO-005 | READ_ONLY_MANYは明示capabilityと全active Claim read-onlyを要求し、未certified SHARED_WRITERを拒否する | AT-STO-005 |
| INV-STO-006 | ATTACHEDはcurrent DB Claim、libvirt device、backend client/lock evidenceの一致後だけ確定する | AT-STO-006 |
| INV-STO-007 | DETACHED/Claim releaseはsource I/O pathとbackend client/lock releaseのverification後だけ確定する | FI-STORAGE-004 |
| INV-STO-008 | Attachment outcome/I/O ownershipがUNKNOWNなら反対操作、Claim release、別Host write attachを開始しない | FI-STORAGE-005 |
| INV-STO-009 | 別Host write authorityはcompute source、storage client、attachment authority fencingをすべて必要とする | FI-STORAGE-006 |
| INV-STO-010 | watcher/lock/blocklist/device/holder observation単独ではAttachment authorityを作成・譲渡・解放しない | FI-STORAGE-005 |
| INV-STO-011 | Ceph Bindingはstable image identityとscoped secret referenceを持ち、name/secret valueをauthority metadataにしない | AT-STO-011 |
| INV-STO-012 | Local LVM VolumeはHost/VG/LV identityへbindし、certified replication/exportなしに別Host recoveryしない | FI-STORAGE-009 |
| INV-STO-013 | Host recoveryはold/new Attachment generationと全storage fencing/eligibilityを再検証する | FI-STORAGE-008 |
| INV-STO-014 | migration handoffは一つのlogical write authorityを維持し、一般的な二active writer Claimを作らない | FI-STORAGE-011 |
| INV-STO-015 | Snapshot/Clone dependencyとconsistency evidenceを保持し、未証明application consistencyを表示しない | AT-STO-015 |
| INV-STO-016 | active/pending Attachment、child、Recovery/Migration/UNKNOWN/hold中のVolumeを削除せず、DB GCとbackend cleanupを分離する | FI-STORAGE-012 |
| INV-STO-017 | backend-only image/LV、unknown watcher/lock、unmatched deviceを自動adopt/delete/detachしない | FI-STORAGE-014 |
| INV-STO-018 | force detach/client fence/lock break/backend delete/Adoptionは個別permission/approval/auditを要求する | FI-STORAGE-015 |
| INV-STO-019 | Storage capacityはtransactional ledgerでclaimし、stale/UNKNOWN backend usageを空きへ丸めずbackend absence前に再利用しない | FI-STORAGE-017 |
| INV-STO-020 | Local LVM の予約名を LV identity とみなさず、typed read-back の Host/VG/LV UUID、size、Binding generation が一致した immutable evidence だけを current BOUND authority へ昇格する | FI-STORAGE-018 |
| INV-STO-021 | libvirt attach/detach response 単独では Attachment authority を変更せず、current generation の Binding/Claim と device identity/LVM holder の typed read-back が一致してから ATTACHED/DETACHED と ACTIVE/RELEASED を不可分に確定し、stale observation は current authority を変更できない | FI-STORAGE-019 |

### NFV Dataplane

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-DPL-001 | 一つのexclusive pCPUをworkload/emulator/PMD/service roleへ二重claimしない | AT-DPL-002 |
| INV-DPL-002 | workload HugePagesとDPDK socket memoryを同じ物理poolのpurpose別ledgerで予約する | AT-DPL-003 |
| INV-DPL-003 | PMD、Port、DPDK memory、VM memory、PCIのNUMA localityをeligibilityで評価する | AT-DPL-005 |
| INV-DPL-004 | PMD CPU、DPDK memory、Port/RxQ claimを他resourceと同じtransactionで不可分commitする | AT-DPL-006 |
| INV-DPL-005 | PMD utilization/cycles/drop telemetryをallocation authorityとして使用しない | AT-DPL-011 |
| INV-DPL-006 | dataplane desired allocationとobserved OVS/DPDK bindingを別generationで保持する | AT-DPL-010 |
| INV-DPL-007 | restart-required dataplane変更を通常VM作成から暗黙実行しない | AT-DPL-009 |
| INV-DPL-008 | arbitrary OVSDB/EAL/PCI/shell操作をAPI/Command/Extensionで受理しない | AT-DPL-008 |
| INV-DPL-009 | OVS-DPDK不適格時にkernel datapath等へsilent fallbackしない | AT-DPL-012 |
| INV-DPL-010 | PCI/PMD/OVS mutation結果不明時はresourceをquarantineしblind replay/rebindしない | FI-DPDK-005 |
| INV-DPL-011 | Observed/Normalized PCI capability は Qualification または Allocation authority ではなく、fixture parser pass を hardware qualification として使用しない | AT-DPL-014 |
| INV-DPL-012 | Qualification Evidence は immutable とし、binding 対象の observation/stack/evaluator/artifact/operation set が変化した場合は CURRENT を継承しない | FI-PCI-002 |
| INV-DPL-013 | Qualification Binding が STALE、UNKNOWN、REVOKED、または欠損なら allocation state を BLOCKED とし、Observed AVAILABLE から自動昇格しない | FI-PCI-001 |
| INV-DPL-014 | VF claim は current Host/device/qualification/policy/NUMA/IOMMU generation と active claim 不在を一 transaction で再検証し、device ごとに一つの active/release-pending claim だけを許可する | AT-DPL-017 |

## 9. Host Lifecycle and Compliance

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-HLC-001 | Host authentication/credential発行だけではenrollment、READY、mutation authorityのいずれも成立しない | AT-HLC-002 |
| INV-HLC-002 | Host ProfileとHost Baseline versionはimmutableで、assignmentと適用generationを明示する | AT-HLC-004 |
| INV-HLC-003 | Compliance Resultとevidenceはappend-onlyで、UNKNOWNを推測して別statusへ丸めない | AT-HLC-006 |
| INV-HLC-004 | blocking controlのNON_COMPLIANT/DEGRADED/UNKNOWNは定義されたHostまたはcapability scopeをplacement不適格にする | AT-HLC-007 |
| INV-HLC-005 | auto-remediate-safeもenrollment、current assignment、armed authority、Command Lease、Agent journalを迂回しない | FI-HLC-005 |
| INV-HLC-006 | Host preflightとcompliance evaluationは副作用を起こさない | AT-HLC-005 |
| INV-HLC-007 | Host/Agentは自身のapproval、Profile、Baseline、Control policyを変更できない | AT-HLC-015 |
| INV-HLC-008 | reconnect、credential renewal、Gateway recovery、Baseline assignmentだけでHost authorityをarmしない | FI-HLC-008 |
| INV-HLC-009 | external-remediation modeはKIMからHost mutationを開始しない | AT-HLC-013 |
| INV-HLC-010 | decommissionはauthority/Leaseをfenceし、managed resourceをdrainし、credentialを失効するまで完了しない | AT-HLC-014 |
| INV-HLC-011 | duplicate Host identity/hardware fingerprint conflictを自動mergeせずquarantineする | FI-HLC-002 |
| INV-HLC-012 | Baseline rolloutは旧version/resultを改変せず、rollbackを自動的なHost state復元とみなさない | FI-HLC-006 |
| INV-HLC-013 | 単一の可変hardware identifierまたはAgent自己申告だけでpolicy-auto enrollmentしない | AT-HLC-018 |
| INV-HLC-014 | Enrollment decisionはsource/issuer/provenance/freshness/conflictを持つidentity evidence setとpolicy generationへbindする | AT-HLC-017 |
| INV-HLC-015 | Compliance Resultはimmutable Evaluator Artifact digestとinput evidence digestへbindする | AT-HLC-019 |
| INV-HLC-016 | Evaluator更新は旧Resultを改変せず、比較/canary/failure thresholdを通じて新Assignment generationとして適用する | FI-HLC-010 |
| INV-HLC-017 | 外部remediationの完了claimだけではCOMPLIANT、READY、authority armed、maintenance exitへ遷移しない | FI-HLC-012 |
| INV-HLC-018 | External remediation integrationはCore DB、Agent credential、Command Lease、Host Operation Authorityを取得しない | AT-HLC-021 |
| INV-HLC-019 | Credential Binding revision と authenticated certificate fingerprint が current Enrollment binding に一致しない Agent session を grant しない | AT-HLC-023 |
| INV-HLC-020 | Session Authorization は Enrollment、Credential Binding、session、capability generation を全て保持し、transport liveness や証明書 validity だけで AUTHORIZED にしない | AT-HLC-024 |
| INV-HLC-021 | Session Authorization が AUTHORIZED でも explicit Host Operation Authority arming 前に mutation を許可しない | AT-HLC-025 |
| INV-HLC-022 | reconnect、credential renewal/rekey、Enrollment、capability、Baseline/preflight/Compliance の変更は既存 Host authority を fence できるが、同一または新 generation を暗黙 arm しない | FI-HLC-013 |
| INV-HLC-023 | Host Operation Authority は全 current dependency generation と policy/actor を一 transaction で固定し、mutation authorization 時にも同じ binding を再検証する | AT-HLC-026 |
| INV-HLC-024 | Upgrade/Maintenanceの一方がcurrent disruptive Host claimを持つ間、他domainはside effectを開始せず、Lease expiryだけでclaimを解放しない | AT-HLC-027, FI-HLC-014 |

## 10. Host Grouping

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-HGR-001 | PostgreSQLへgeneration付きでmaterializeされたHostGroup membershipだけをauthorityとして使用する | AT-HGR-004 |
| INV-HGR-002 | Placement Pool、Failure Domain、Operational Cohortの効果を相互に暗黙継承しない | AT-HGR-002 |
| INV-HGR-003 | required/exclusive dimensionの欠損・多重所属を任意Group選択で解決せずfail closedにする | FI-HGR-002 |
| INV-HGR-004 | selectorはpure proposalであり、Agent自己申告やexternal claimからmembershipを直接writeしない | XCT-HGR-001 |
| INV-HGR-005 | hierarchy graphをcycle/partial stateなしに一generationでcommitする | FI-HGR-003 |
| INV-HGR-006 | Group membership、weight、policyはHost lifecycle/Compliance/capability/resource eligibilityを上書きしない | AT-HGR-006 |
| INV-HGR-007 | Final Admissionはcurrent membership/policy/hierarchy generationを再検証する | FI-HGR-004 |
| INV-HGR-008 | Group capacityはHost authorityからの導出値で、独立allocation/reservation ledgerを持たない | AT-HGR-008 |
| INV-HGR-009 | 同priorityの非互換Group policy bindingをlast-winsで解決せずASSIGNMENT_CONFLICTにする | FI-HGR-006 |
| INV-HGR-010 | rollout/maintenance targetは開始時のimmutable Group Membership Snapshotへbindする | FI-HGR-005 |
| INV-HGR-011 | Tenantへraw infrastructure Group/failure topologyを公開せず許可されたPlacement Scopeだけを公開する | AT-HGR-012 |
| INV-HGR-012 | Group変更だけで既存workloadを暗黙移動、停止、再構成しない | AT-HGR-014 |
| INV-HGR-013 | active membership/reference/snapshot/policy bindingを持つGroupを削除しない | FI-HGR-007 |
| INV-HGR-014 | READY/placement可能なHostは全active Placement Poolsから一つのeffective Availability Policyを解決できなければならない | AT-AVR-005 |
| INV-HGR-015 | 個別membership row、selector proposal、partial bulk writeはaccepted membership set authorityではなく、完全なset evidenceとcurrent pointerのatomic commitだけがauthorityを進める | AT-HGR-016, FI-HGR-009 |
| INV-HGR-016 | snapshot、Placement dry/finalはcurrent accepted membership set generation/digestとそのset内のmember evidenceへbindし、live row寄せ集めやstale setからauthorityを進めない | AT-HGR-017, FI-HGR-010 |
| INV-HGR-017 | EXACTLY_ONE/ZERO_OR_ONE/MANYは単一Group内member数ではなく同一type/dimension/level/scopeのACTIVE sibling sets全体に対するHost別制約として、shared scope lock下で検証し、policy generation不一致setをauthorityへ進めない | AT-HGR-018, FI-HGR-011 |
| INV-HGR-018 | hierarchyは同一type/dimension/scopeのcomplete accepted graphとしてimmutable evidenceを作成後にcurrent pointerをatomic switchし、current graphの全node generationとlevelがcurrent HostGroup authorityに一致しない限りmembership set、snapshot、Placement authorityを進めない | AT-HGR-019, FI-HGR-012 |
| INV-HGR-019 | selector match/evaluationはmembership authorityではなく、UNKNOWNをNOT_MATCHEDへ縮退させず、current selector/input/cardinality/hierarchy/HostGroup generationへ再bindしたaccepted complete Membership Setだけがcurrent membershipを進める | AT-HGR-020, FI-HGR-013 |
| INV-HGR-020 | HostGroup-targeted Upgrade Planはcurrent SetやSelectorを直接評価せず、exact immutable UPGRADE Snapshotとmember evidenceからTargetを一度だけ生成する。live membership drift、Coordinator recovery、PAUSE/RESUMEは既存Plan/Snapshot/Targetを変更しない | AT-HGR-021, FI-HGR-014 |
| INV-HGR-021 | Maintenanceはexact immutable MAINTENANCE Snapshotから独立Plan/Wave/Targetを一度だけatomic publishし、UPGRADE Snapshotを受理せず、live drift/recovery/pause/resumeでTarget identityを変更しない | AT-HGR-022, FI-HGR-015 |
| INV-HGR-022 | HostGroup membership/hierarchy/cardinalityとGroup Policy Bindingを別authorityとし、exact Group/Policy revisionを解決する。same-priority非互換はASSIGNMENT_CONFLICTでconsumer BLOCKED、stale highest assignmentはlower-priority fallbackを行わず、live Binding driftでhistorical consumer evidenceを書き換えない | AT-HGR-023, FI-HGR-016 |
| INV-HGR-023 | HostGroup membership、Placement Scope visibility、Eligibility、Final Admissionを別authorityとし、Hierarchy/Selector/Group Policyからexposureを暗黙生成しない。Final Admissionはexact Scope/Group/Set/member provenanceを再検証し、stale時にre-placementやpartial claimを行わない | AT-HGR-024, FI-HGR-017 |
| INV-HGR-024 | external assertion receipt/verificationはmembership authorityではない。closed issuer scope、signature、audience、freshness、nonce、payload、HostGroup/Hierarchy/Cardinality generationを検証し、explicit complete-set materialization transactionだけがcurrent membershipを進める。issuer distrust/expiryはnew materializationを止めるがhistorical evidence/Setを書き換えない | AT-HGR-025, FI-HGR-018 |

## 11. Availability Responsibility and Recovery

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-AVR-001 | AvailabilityPolicy versionはimmutableで、responsibilityとHost failure actionの不正な組合せをpublishしない | AT-AVR-001 |
| INV-AVR-002 | Final Admissionはeffective Policy/Pool/membership generationをVM Availability Bindingとresource claimへ不可分にcommitする | AT-AVR-006 |
| INV-AVR-003 | Group/Policy変更だけで既存VM Availability Bindingを変更せず、明示Rebindで新revisionを作る | FI-AVR-002 |
| INV-AVR-004 | WORKLOAD_MANAGED VMへKIMが自動restart、evacuate、replacement mutationを開始しない | FI-AVR-004 |
| INV-AVR-005 | MANUAL VMへauthorized Manual Recovery DecisionなしにKIMがrecovery mutationを開始しない | FI-AVR-005 |
| INV-AVR-006 | INFRASTRUCTURE_MANAGED recoveryはPolicy要求を満たすsource fencing proofなしに開始しない | FI-AVR-003 |
| INV-AVR-007 | fencing、single-writer attachment、resource ownership、Availability BindingのUNKNOWNを推測で安全扱いしない | FI-AVR-007 |
| INV-AVR-008 | recovery destinationはcurrent Placement/Compliance/capacity/failure-domainとbound Policy compatibilityを再評価する | FI-AVR-008 |
| INV-AVR-009 | Recovery Operationはcanonical Failure Campaign、VM、Availability Binding revision、actionへ冪等にbindし、元epoch群をevidenceとして保持する | AT-AVR-013 |
| INV-AVR-010 | stale failure epoch、Binding、fencing proof、Lease、Resultはcurrent recovery authorityを進めない | FI-AVR-006 |
| INV-AVR-011 | EVACUATEはVM単位Operationへ分解し、一VMの失敗/UNKNOWNを他VMの推測rollbackへ波及させない | FI-AVR-009 |
| INV-AVR-012 | Fault/Event delivery failureを理由にAvailability responsibility/actionを変更しない | FI-AVR-010 |
| INV-AVR-013 | heartbeat/Agent lossだけでHost source fencing完了を確定しない | FI-AVR-003 |
| INV-AVR-014 | Policy/HostGroup/Group Policy Binding driftまたはoperator intentだけでVM Availability Bindingを変更しない。accepted explicit Rebind Decisionだけがexact source/targetを再検証し、next revisionとcurrent pointerを一transactionで一度だけ進める | AT-AVR-017, FI-AVR-011 |
| INV-AVR-015 | Rebind response loss/replay、stale source、concurrent intentをnext revisionへ再解釈せず、historical Bindingをimmutableに保ち、Compute/PCI/Network/Storage/runtime mutationやRecovery authorizationを発行しない | AT-AVR-017, FI-AVR-011 |
| INV-AVR-016 | Failure Epoch openはcurrent VM Availability Binding revision/digestとそのexact Policy、Admission、allocation、source Host provenanceを一transactionで固定し、後続Rebind/Policy/HostGroup driftでhistorical responsibilityを書き換えない | AT-AVR-018, FI-AVR-012 |
| INV-AVR-017 | failure signal、heartbeat/Agent loss、UNKNOWN observationをconfirmed failure、fencing proof、Recovery Eligibility/Operationへ昇格させず、typed confirmation-policy consumerがない間はSUSPECTEDだけを発行する | AT-AVR-018, FI-AVR-012 |
| INV-AVR-018 | Epoch/Evidenceのsame-identity replayは同じimmutable evidenceへ収束し、late/stale evidenceはappendしても過去transitionを書き換えず、explicit incident keyのparallel openは一Epochへ収束する | AT-AVR-018, FI-AVR-012 |
| INV-AVR-019 | confirmation Evaluationはexact Epoch generation、historical Availability Binding、typed Policy revision/digest、Evidence identities/generations/digestsを固定するpure evidenceであり、SATISFIEDだけではEpoch stateを変更しない | AT-AVR-019, FI-AVR-013 |
| INV-AVR-020 | UNKNOWN、STALE、CONFLICTING、typed Policy欠損をpositive confirmationへ縮退させず、Decision時のPolicy/Evidence/Epoch driftをsilent re-evaluationまたはcurrent revisionへのupliftで解決しない | AT-AVR-019, FI-AVR-013 |
| INV-AVR-021 | accepted Confirmation DecisionだけがSUSPECTED→CONFIRMED transition/currentをatomic commitし、same-ID replayやparallel Decisionでgenerationを増幅しない。CONFIRMEDはHost authority、fencing proof、Recovery Eligibility/Operation、resource/runtime mutationを生成しない | AT-AVR-019, FI-AVR-013 |

## 12. Workload Resilience Intent

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-WRI-001 | NFVO/VNFMのopaque roleをVNF lifecycle、application health、authorization authorityに使用しない | AT-WRI-002 |
| INV-WRI-002 | Tenant/NFVOへraw HostGroup/failure topologyを公開せずpublic dimension/level classだけを受け付ける | AT-WRI-003 |
| INV-WRI-003 | HARD Failure Domain constraintをscore/soft ruleへ降格またはsilent relaxしない | FI-WRI-003 |
| INV-WRI-004 | rack、power-path等のFailure Domain dimensionを独立に評価する | AT-WRI-004 |
| INV-WRI-005 | ResilienceDomainClaimをVM Allocation/Availability Binding/resource claimsと同じFinal Admission transactionでcommitする | AT-WRI-007 |
| INV-WRI-006 | concurrent member Placementでhard same-domain claimは一方だけがcommitできる | FI-WRI-001 |
| INV-WRI-007 | missing/stale/UNKNOWN domain evidenceをdistinct domainとして数えない | FI-WRI-002 |
| INV-WRI-008 | old VM/source ownershipがUNKNOWNのMember Slot/Domain Claimをreplacementへ再利用しない | FI-WRI-004 |
| INV-WRI-009 | domain/hierarchy driftだけで既存VMを暗黙migration/restartしない | FI-WRI-005 |
| INV-WRI-010 | Resilience IntentはVM Availability responsibilityを変更しない | AT-WRI-012 |
| INV-WRI-011 | Northbound mapperはCore DB/Domain Claim/Allocationへ直接writeしない | XCT-WRI-001 |
| INV-WRI-012 | active Member/Domain Claimを持つResilience Groupを削除しない | FI-WRI-007 |
| INV-WRI-013 | required members未充足をmin-distinct違反にせずPENDINGとし、max-members-per-domainは各admissionで強制する | AT-WRI-015 |

## 13. Recovery Storm Control

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-RCV-001 | Recovery Queue/Budget Lease authorityをPostgreSQL transactionで確定し、in-memory/Bus状態をauthorityにしない | FI-RCV-002 |
| INV-RCV-002 | Queue Entryは各PLANNING/DISPATCH phaseの該当全budget scopeを不可分取得するまでそのphaseへ進まない | AT-RCV-003 |
| INV-RCV-003 | Budget Leaseはfencing、Placement/capacity claim、Command Lease、verificationを代替しない | AT-RCV-004 |
| INV-RCV-004 | Budget Lease expiry/worker lossをRecovery未実行の証明にしない | FI-RCV-003 |
| INV-RCV-005 | max concurrencyとstart rate/window/burstを全workerで共有するdurable generationから強制する | FI-RCV-001 |
| INV-RCV-006 | recovery priorityはsafety/eligibilityを上書きしない | AT-RCV-007 |
| INV-RCV-007 | aging/fair-share/per-scope capで一Project/Planによるstarvationを防ぐ | FI-RCV-005 |
| INV-RCV-008 | circuit breaker復旧だけでdispatchせずfencing/evidence/Placement generationを再検証する | FI-RCV-006 |
| INV-RCV-009 | duplicate/correlated failure signalを重複Recovery Queue Entryへ無制限展開しない | FI-RCV-007 |
| INV-RCV-010 | queue delay/saturationをsuccess/failureへ丸めずWAITING/BLOCKED/ESCALATED evidenceとして保持する | AT-RCV-010 |
| INV-RCV-011 | Budget Policy変更だけでdispatch/started Recovery Operationを暗黙cancel/reclassifyしない | FI-RCV-008 |
| INV-RCV-012 | Control Plane/worker failover後もBudget/Queue/Lease authorityとorderingをPostgreSQLから復元する | FI-RCV-009 |
| INV-RCV-013 | dispatchはRecovery OperationとBudget Consumptionを不可分commitし、terminal verificationまでactive concurrencyへ計上する | FI-RCV-011 |
| INV-RCV-014 | 全budget acquisition pathは同じversioned canonical scope順でlockし、deadlock/serialization failureで部分Leaseを残さない | FI-RCV-012 |
| INV-RCV-015 | 同一Failure Campaign/VM/Binding/actionは一つのRecovery Campaign Claimへ収束し、late mergeでも追加dispatchを許可しない | FI-RCV-013 |

## 14. Security, Audit, and Failure

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-SEC-001 | mutationはauthentication、authorization、audit durabilityを満たさなければfail closed | AT-SEC-001 |
| INV-SEC-002 | secret、生backend error、他Tenant resource identityを公開response/metricsへ出さない | AT-SEC-002 |
| INV-FAIL-001 | timeout/partition/Lease expiryをmutation失敗または未実行の証明にしない | FI-TRANSPORT-001 |
| INV-FAIL-002 | stale identity/generation/token/observationはcurrent authorityを進めない | FI-SPLIT-001 |
| INV-FAIL-003 | recovery不能resourceはblocked/quarantinedを維持し、推測cleanupしない | FI-DR-001 |
| INV-HA-001 | 同一Site HA failoverはcommitted authority RPO 0を目標とする | AT-NET-038, FI-DB-001, FI-DB-003 |
| INV-DR-001 | restore後の未知resourceはquarantineし、明示adoption前にmutationしない | FI-DR-001 |

## 15. Extensions

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-EXT-001 | ExtensionはCore DBへ直接writeできない | XCT-BOUNDARY-001 |
| INV-EXT-002 | Extensionはauthorization、audit、Lease/fencingを迂回できない | XCT-BOUNDARY-002 |
| INV-EXT-003 | Extensionは独自Identity/Credential authorityを暗黙追加できない | XCT-BOUNDARY-003 |
| INV-EXT-004 | Agent moduleはclosed Commandとnarrow backend interfaceだけを受け取る | XCT-AGENT-001 |
| INV-EXT-005 | UNKNOWNをFAILED/SUCCEEDEDへ丸めるadapterを受け入れない | XCT-FAIL-001 |
| INV-EXT-006 | capability消失時は新規利用を停止し、既存resourceを暗黙変更しない | XCT-CAP-001 |

## 16. Documentation

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-DOC-001 | Requirements、Accepted ADR、Architectureの矛盾を暗黙解釈せず、実装を停止して解消する | AT-DOC-001 |
| INV-DOC-002 | 重要判断の変更はADR、Requirements、Architecture、test traceを同じchange setで更新する | AT-DOC-002 |
| INV-DOC-003 | 日本語spacing lintはcode、URL、identifier、API path、約物を除外し、未reviewのrepository-wide自動修正を行わない | AT-DOC-003 |

## 17. Upgrade and Compatibility

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-UPG-001 | Release Manifestはimmutable artifact digest、contract/support range、migration、rollback boundaryへbindする | AT-UPG-002 |
| INV-UPG-002 | version文字列やprocess aliveだけでcompatibility/readinessを確定せずcurrent evidenceへbindしたDecisionを要求する | FI-UPG-001 |
| INV-UPG-003 | Upgrade Campaign/Plan/Wave/Target/Feature Gate authorityをPostgreSQLへ永続化しin-memory progressをauthorityにしない | FI-UPG-010 |
| INV-UPG-004 | mixed-versionは明示edgeを持つN/N-1だけとしN-2/unmanaged/digest不明componentをserving/dispatchへ参加させない | FI-UPG-002 |
| INV-UPG-005 | 全active writer/consumerが理解するsemanticsだけをFeature Gate前にwriteする | FI-UPG-003 |
| INV-UPG-006 | schema contract/old decoder/artifact GCをrollback window終了とrequired participant absenceの証明前に行わない | FI-UPG-004 |
| INV-UPG-007 | 各waveはimmutable target snapshot、current compatibility、availability budget、failure thresholdを満たす | AT-UPG-007 |
| INV-UPG-008 | upgrade coordinatorはdomain mutation、Placement、Command、Attachment、Network Binding等のauthorityを代替しない | AT-UPG-009 |
| INV-UPG-009 | unsupported protocol/Command/Result schemaをdispatch/down-convert/silent fallbackしない | FI-UPG-006 |
| INV-UPG-010 | Agent upgrade/reconnect/version一致だけでHost authorityを再armしない | FI-UPG-007 |
| INV-UPG-011 | Event payloadをupgrade後resourceから再生成せず発行時schema/digestとretention decoderを保持する | AT-UPG-014 |
| INV-UPG-012 | extension/adapter upgradeはdrainとownership fencingなしにold/new writerを同時activeにしない | FI-UPG-008 |
| INV-UPG-013 | support matrix変更だけで既存VM/Port/Volumeを暗黙mutationしない | AT-UPG-018 |
| INV-UPG-014 | incompatible/UNKNOWN Host/backendを新規Placement/Recovery/dispatchに使用しない | FI-UPG-009 |
| INV-UPG-015 | rollbackを新Plan/Attemptとして記録し過去Target/Attempt/evidenceを改変しない | AT-UPG-020 |
| INV-UPG-016 | destructive contract後またはoutcome UNKNOWN時にblind rollback/PITR/逆操作を開始しない | FI-UPG-011 |
| INV-UPG-017 | offline/緊急upgradeでもartifact verification、authorization、audit、compatibility gateを省略しない | FI-UPG-013 |
| INV-UPG-018 | release publish/start/switch/contract/activation/rollback/overrideを分離した権限と監査で保護する | FI-UPG-014 |
| INV-UPG-019 | QEMU/libvirt/default変更だけで既存VMのmachine type/CPU model/firmware/device ABI bindingを変更しない | FI-UPG-016 |
| INV-UPG-020 | Event/evidence payload referenceまたはlegal hold中にrequired decoder artifactをfinalize/GCしない | FI-UPG-017 |
| INV-UPG-021 | Feature Gate dependency graphのcycle/未充足/conflictを拒否しdependency-aware orderを迂回しない | FI-UPG-018 |
| INV-UPG-022 | OVN runtime work claim は current `ReleaseManifest`、明示 N/N-1 edge、artifact digest、supported work schema、immutable `CompatibilityDecision` と current component binding generation を要求する。`DRAINING` は new claim を停止して current claim を revoke せず、active/draining participant が理解できない schema の FeatureGate activation と、edge のない N-2 component の claim を拒否する | AT-UPG-029, FI-UPG-019 |
| INV-UPG-023 | Manifest または compatibility edge の distrust は immutable evidence と new `CompatibilityDecision` を追記して current binding generation を `INCOMPATIBLE/FENCED` へ進める。過去の Manifest、edge、Decision、Attempt を改変せず、既 grant claim の非実行を推測しない。FeatureGate rollback も current participant compatibility を再検証し immutable schema transition evidence を残す。同一 Site PostgreSQL HA failover はこの authority generation と evidence を RPO 0 で維持する | AT-UPG-030, FI-UPG-020 |
| INV-UPG-024 | product-wide Upgrade Campaign は immutable Plan revision、acyclic component graph、verified artifact provenance / SBOM、ordered Wave、Target snapshot、canary threshold を PostgreSQL authority として保持する。Coordinator claim expiry / restart は target side effect 不在を意味せず、successor は同じ Plan と accepted target evidence から `RECOVER_FROM_DB` する。stale coordinator は Result / canary decisionを進めず、threshold 超過は `PAUSED`、全 canary 成功だけが次 Wave へ進む | AT-UPG-031, FI-UPG-021 |
| INV-UPG-025 | `kim-upgrade-coordinator` は DB-time claim / bounded renewal generation で一つの Campaign authority だけを持つ。process kill、renewal 停止、同一 Site PostgreSQL failover 後も expiry を Target side effect 不在とせず、promoted primary の Plan / Target Result / canary Decision から successor generation が `RECOVER_FROM_DB` する。同一 inputs の `HOLD` を重複 Decision へ増幅せず、old owner/generation を fence する | AT-UPG-032, FI-UPG-022 |
| INV-UPG-026 | Upgrade Target executor は current Campaign / Wave、current Coordinator claim generation、Target Attempt generation、DB-time Lease に bind された authority だけで typed side effect を開始する。process kill / Lease expiry は side effect 不在を意味せず、successor Attempt は `READ_BACK_FIRST` とする。current read-back `MATCHED` だけが immutable Result と Target `SUCCEEDED` を進め、stale executor completion、任意 path / command、観測なしの成功を拒否する | AT-UPG-033, FI-UPG-023 |
| INV-UPG-027 | Debian/systemd component Target は Release Manifest の component identity / package digest と administrator-owned profile の package/service/binary/health identity を bind する。Target payload から path、package 名、service 名、argv を受け取らず、`dpkg` response や restart response だけで成功にしない。installed version、systemd `active/running`、configured executable と一致する `MainPID`、executable digest、typed health schema/version/PID/boot ID/process start ticks の current read-back がすべて一致した場合だけ `MATCHED` とする | AT-UPG-034, FI-UPG-024 |
| INV-UPG-028 | `dpkg` lock contention、process interruption、または response loss は package side effect 不在を意味しない。current Attempt は失敗応答から反対操作や別 artifact install を開始せず、Lease expiry 後の successor だけが `READ_BACK_FIRST` で status/version/process/health を観測する。`ABSENT` に限り current claim から apply でき、不完全 package status は `CONFLICTING`、観測不能は `UNKNOWN` として fail closed にする | AT-UPG-035, FI-UPG-025 |
| INV-UPG-029 | current Target claim から受理した `CONFLICTING` observation は immutable evidence / `CONFLICT_QUARANTINED` event を追記し、Target と execution を同一 transaction で `FENCED` にする。FENCED Target を failure threshold へ算入して Campaign を fail closed にし、process restart、Lease expiry、同じ Plan の successor executor だけでは rearm または再 claim できない | AT-UPG-036, FI-UPG-026 |
| INV-UPG-030 | package recovery は通常 Target Attempt と別の immutable Recovery Plan / generation / Attempt / Lease / Result authority を持つ。`CONFIGURE_EXISTING` は `PACKAGE_HALF_CONFIGURED` の current read-back と明示 authorization が揃う場合だけ固定 package identity へ発行し、Recovery `VERIFIED` だけでは Target または Campaign を rearm/resume しない。別の immutable rearm authorization transaction だけが Target を `PENDING` へ戻し、Campaign は `PAUSED` のまま維持する | AT-UPG-037, FI-UPG-027 |
| INV-UPG-031 | HostGroup Snapshot binding、Wave、derived Target、current Campaign switchを一transactionでpublishしsemantic replayは同じevidenceへ収束する。Snapshot member Hostのcurrent mutation authorityが失効してもTarget identityを消さずTarget executionをFENCEDにする | AT-UPG-038, FI-UPG-028 |

## 18. Time and Clock Semantics

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-TIM-001 | wall clock、DB time、monotonic clock、source/received timestampを同じauthorityとして扱わない | AT-TIM-001 |
| INV-TIM-002 | timestampだけでresource/execution/delivery/observation orderingやfencingを決めない | FI-TIME-001 |
| INV-TIM-003 | Clock Health Decisionをcurrent evidence、uncertainty、policy generationへbindしUNKNOWNをHEALTHYへ丸めない | AT-TIM-004 |
| INV-TIM-004 | Control Plane Lease/deadline/retention decisionをapplication node clockではなくcurrent DB authority time/generationで行う | AT-TIM-005 |
| INV-TIM-005 | DB clock step/failover/restoreでexpiredまたはold-generation Lease/session/claimをreviveしない | FI-TIME-002 |
| INV-TIM-006 | Lease expiryを期限前side effectの未実行/失敗証明にしない | FI-TIME-003 |
| INV-TIM-007 | expired/revoked Leaseを同じtokenの時刻変更でrenew/reviveしない | AT-TIM-008 |
| INV-TIM-008 | Agentは受信時刻+TTLやlocal wall clockだけでCommand start deadlineを決めない | FI-TIME-004 |
| INV-TIM-009 | Agent clock uncertainty/RTT/monotonic continuityがpolicy外なら新Commandを開始しない | FI-TIME-005 |
| INV-TIM-010 | process/Host reboot後にpre-reboot monotonic deadline/cached Commandを使用しない | FI-TIME-006 |
| INV-TIM-011 | sourceの未来timestampでObservation/Evidenceのfreshnessを延長しない | FI-TIME-007 |
| INV-TIM-012 | credentialが時間上有効なことをEnrollment/Role/Host/Command authorityとして使用しない | AT-TIM-014 |
| INV-TIM-013 | clock UNKNOWN/UNTRUSTED時に新規privileged authentication/credential/Commandをfail openしない | FI-TIME-008 |
| INV-TIM-014 | calendar window開始/終了だけでdrain/fencing/mutation/catch-up authorityを得ない | FI-TIME-009 |
| INV-TIM-015 | clock jumpでqueue/rate creditを二重付与し、grace/deadlineから破壊操作を即時実行しない | FI-TIME-010 |
| INV-TIM-016 | clock anomalyまたはreference/hold/backup guard未確認時にretention GC/partition detachを実行しない | FI-TIME-011 |
| INV-TIM-017 | replay/DR/archive reference期間より前にidempotency/Receipt/decoder evidenceを削除しない | AT-TIM-019 |
| INV-TIM-018 | event timestamp近接だけでFailure Epochを同一Campaignへmergeしない | FI-TIME-012 |
| INV-TIM-019 | clock anomalyだけで既存VM/dataplaneを停止・移動・再構成しない | AT-TIM-022 |
| INV-TIM-020 | clock復旧だけでHost/Agent/Lease/credential authorityを自動再armしない | FI-TIME-013 |
| INV-TIM-021 | DB/Control Plane clockは自身のtimestampまたは単一external sourceだけでHEALTHYを自己証明しない | FI-TIME-017 |
| INV-TIM-022 | PTP/GNSS lock、grandmaster、VNF telemetry timestampをKIM Lease/credential/ordering/fencing authorityにしない | FI-TIME-018 |
| INV-TIM-023 | leap/smear policy不明・競合を無視してLease延長、mass expiry、calendar二重実行を行わない | FI-TIME-019 |

## 19. PKI and Trust Lifecycle

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-PKI-001 | Control Plane、Agent、integration/backend、artifact signing、data encryptionのtrust/key domainを無制限共有しない | AT-PKI-001 |
| INV-PKI-002 | workload certificateを外部Identity PlatformのUser/Service authorization authorityとして使用しない | AT-PKI-002 |
| INV-PKI-003 | Root private keyを通常Control Plane/Agent/DBへ配置して日常issuanceに使用しない | FI-PKI-001 |
| INV-PKI-004 | unknown/wildcard/CN-only/constraint外SAN・EKU・algorithm certificateをtrusted identityにしない | FI-PKI-002 |
| INV-PKI-005 | private key/secret valueをKIM DB、Event、Command、log、diagnostic、通常backupへ保存しない | FI-PKI-003 |
| INV-PKI-006 | TrustBundle/Profile/Relationshipをimmutable revisionとmonotonic trust generationで更新しold Bundleを上書き・rollbackしない | FI-PKI-004 |
| INV-PKI-007 | valid certificateだけでRole、Enrollment、Host authority、Command Lease、backend mutationを成立させない | AT-PKI-007 |
| INV-PKI-008 | Trust Decisionをcurrent Bundle/profile/revocation/clock/Binding/session generationへbindしUNKNOWNをtrustedへ丸めない | FI-PKI-005 |
| INV-PKI-009 | bootstrap token/credentialだけでEnrollment、READY、Host authorityを成立させない | FI-PKI-006 |
| INV-PKI-010 | proof-of-possessionとcurrent Enrollment/identity evidenceなしにAgent certificateを発行しない | AT-PKI-010 |
| INV-PKI-011 | issuance/renewal response lossで別key/identity certificateをblind発行しない | FI-PKI-007 |
| INV-PKI-012 | renewal/rekeyは新Credential Binding revisionとし過去certificate/Binding historyを書き換えない | AT-PKI-012 |
| INV-PKI-013 | overlap中のold/new certificateから二つのHost/workload mutation authorityを作らない | FI-PKI-008 |
| INV-PKI-014 | TrustBundle/revocation/Binding/authority generation変更後のstale sessionをmutationへ使用しない | FI-PKI-009 |
| INV-PKI-015 | certificate expiry/revocation/session closeをpeer process/Host/backend side effect停止の証明にしない | FI-PKI-010 |
| INV-PKI-016 | revocation intentをdistribution/propagation完了へ丸めずsequence/freshness/receiptを要求する | FI-PKI-011 |
| INV-PKI-017 | revocation stateがstale/UNKNOWNなprofile scopeでnew privileged sessionをfail openしない | FI-PKI-012 |
| INV-PKI-018 | distrusted issuer/profile/namespaceを別chain、cached session、old Bundleへsilent fallbackしない | FI-PKI-013 |
| INV-PKI-019 | Host credential revoke/Gateway disconnectだけでcompute/storage/network fencing完了を確定しない | FI-PKI-014 |
| INV-PKI-020 | Control Plane certificate rotationだけでDB/Bus/backend credentialやLease/authorityをfencedとみなさない | FI-PKI-015 |
| INV-PKI-021 | compromised issuer自身の署名だけでemergency Root/anchor rolloverを承認しない | FI-PKI-016 |
| INV-PKI-022 | offline Bundleのsequence/previous digest/trust generation rollbackまたはTOFU/default shared secretを許可しない | FI-PKI-017 |
| INV-PKI-023 | PITR/DRで時間上有効なold certificate/session/Lease authorityを復活・cloneしない | FI-PKI-018 |
| INV-PKI-024 | Secret Provider completion claimだけでcredential active/revoked/rotatedを確定しない | FI-PKI-019 |
| INV-PKI-025 | Root/issuer distrust、emergency anchor、CA key restore、force issuanceを通常resource operator単独で実行しない | FI-PKI-020 |
