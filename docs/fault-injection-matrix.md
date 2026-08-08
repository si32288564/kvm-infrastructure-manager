# Fault Injection Matrix

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

System-wide Failure Modelの各failure classを、再現可能なinjection、期待する検出、封じ込め、永続証拠、禁止副作用、回復条件へ落とします。

## 2. Test Contract

各fault injection testは以下を出力・保存します。

- test ID、seed、build/version、topology
- injection開始/終了時刻とscope
- authority/desired/observed generation
- Operation、Job、Command、Lease、Attempt correlation
- expected/actual detection code
- containment/fencing evidence
- prohibited side-effect assertion
- recovery/verification evidence
- database invariantsとaudit record

test harnessが障害を解除しただけでは合格になりません。期待したauthority stateとbackend stateへの収束を検証します。

## 3. Matrix

| ID | Failure class / Injection | Expected detection | Expected containment / fencing | Persisted evidence | Prohibited side effect | Recovery criterion |
|---|---|---|---|---|---|---|
| FI-CLIENT-001 | mutation commit後、API responseをdropしてclient再送 | duplicate idempotency key | 同一Operationを返す | request digest、idempotency record、単一Operation | 二重Desired/VM/Command | 同一resource/Operation IDを返す |
| FI-CLIENT-002 | stale ETag/desired generationでupdate | precondition failed | mutation transactionを開始しない | request/audit conflict | current state上書き | clientが最新resourceを再取得 |
| FI-CP-001 | workerをbackend call前にkill | started work timeout/journalなし | Lease/Attempt authorityを再評価 | started/UNKNOWNまたは未開始evidence | success推測 | typed resolverまたはsafe retry |
| FI-CP-002 | backend call後、DB result commit前にworker kill | Attempt timeout | resource mutationをblock | Attempt UNKNOWN、correlation | 反対operation/duplicate mutation | observation/read-backで解決 |
| FI-DB-001 | primary failover中に同時mutation | primary/term change、transaction error | old primary fencing、bounded retry | commit/idempotency/term evidence | split-brain commit、data loss | RPO 0、単一committed result |
| FI-DB-002 | commit成功後response loss | transaction outcome unknown | idempotency照会 | committed rows、request digest | 二重commit | 元Operationを回収 |
| FI-DR-001 | backup restore後に新しいbackend VM/Port/Volumeを提示 | generation/provenance mismatch | recovery mode、quarantine | unresolved resource、observation evidence | 自動adopt/削除/attach | explicit authorized adoptionまたは外部所有確定 |
| FI-BUS-001 | internal messageをduplicate/reorder | delivery metadata、old generation | handler idempotency、DB authority確認 | work/event dedupe evidence | 二重Command/transition | 単一authority stateへ収束 |
| FI-BUS-002 | Bus停止後に復旧 | consumer/work age alarm | durable acceptance後のdispatch待機 | pending work age | DB authority loss、成功推測 | DBから未完work再駆動 |
| FI-GATEWAY-001 | Lease前にAgent Gateway partition | heartbeat/session loss | 新Lease停止、Host ineligible | gateway/Host alarm | Agent cached/autonomous mutation | session+capability再検証 |
| FI-GATEWAY-002 | Gateway再接続、Host authorityはdisarmed | session restored | authorityをdisarmedのまま維持 | authority generation/audit | 自動arm/Command配送 | operatorによる明示arm |
| FI-TRANSPORT-001 | ResultをLease expiry後まで遅延し、その間に新AttemptをLease | lease expiry、stale attempt | 旧Attempt UNKNOWN、新token | 2 Attempts、distinct token、stale conflict | 旧ResultによるJob進行 | current Attempt/evidenceだけで収束 |
| FI-TRANSPORT-002 | Resultをcommit後responseだけdrop | client retry | accepted digest完全一致のみ冪等receipt | 単一Result/Attempt completion | 新Attempt/異なるResult受理 | 同じreceipt返却 |
| FI-AGENT-001 | journal write直後、backend実行前にAgent kill | started journal record | 新Command実行停止、read-back | journal+UNKNOWN/未適用evidence | 無条件再実行 | 未適用証明後のnew Attempt |
| FI-AGENT-002 | backend実行後、journal完了前にAgent kill | started journal+backend state | capability unavailable | UNKNOWN、read-back evidence | rollback推測 | typed resolverで適用/未適用確定 |
| FI-HOST-001 | active VM Hostのpower/network loss | heartbeat/BMC/Agent loss | Host ineligible、source fencing要求 | Host failure、affected resources | shared diskの別Host二重attach | source fenced+resource eligibility再評価 |
| FI-HOST-002 | Host clockを閾値外へskew | clock health/lease anomaly | 新Lease停止 | clock alarm、Host state | wall clockのみでauthority判定 | clock正常化+capability/preflight |
| FI-LIBVIRT-001 | libvirt mutation後にtimeoutを返す | backend timeout | Attempt UNKNOWN、read-back | Command/Attempt/evidence | 即時反対mutation | Domain UUID/stateで解決 |
| FI-LIBVIRT-002 | libvirt daemon restart中にCommand | connection/event gap | Host capability一時停止 | Agent health、Attempt result | success推測 | reconnect+full resync+verification |
| FI-NET-001 | OVN transaction conflictと未知objectを注入 | conflict/drift | affected network新規binding停止 | intent generation、unknown object evidence | 未知object/物理network削除 | KIM所有intentのみ再適用しdataplane確認 |
| FI-NET-002 | ovn-controller lagでDB intentとdataplaneを乖離 | binding/dataplane lag | Portをprovisioning/degradedに維持 | intent+observed generations | Port ready誤表示 | chassis/dataplane verification |
| FI-DPDK-001 | active PortのPMD threadを停止/消失させる | PMD/runtime observation | affected Port/Hostへの新規dataplane placement停止 | runtime/Port alarm、generation | ready継続、silent fallback | PMD復旧+RxQ polling verification |
| FI-DPDK-002 | RxQをunpolledまたは不正PMD coreへdriftさせる | RxQ/PMD assignment mismatch | bindingをdegraded/blocked | desired/observed mapping evidence | compliant/ready誤表示 | policy準拠mappingをobservationで確認 |
| FI-DPDK-003 | ovs-vswitchd restart適用後にResult responseをdrop | Command timeout/runtime gap | Attempt UNKNOWN、新規disruptive op停止 | journal、runtime generation、Port evidence | blind restart/rollback | full runtime/PMD/Port/RxQ observation |
| FI-DPDK-004 | DPDK socket memory/HugePage不足でruntime起動失敗 | runtime init/hugepage shortage | Host dataplane ineligible | desired/observed memory、bounded reason | restart loop、workload pages横取り | capacity修正+明示maintenance operation |
| FI-DPDK-005 | PCI driver bind/rebind後にAgentを停止 | device ownership outcome unknown | device/VF/IOMMU group quarantine | journal、driver/IOMMU/OVS observation | VM/OVSへのblind再割当 | exclusive ownershipをread-backで証明 |
| FI-DPDK-006 | PMD/Portを異NUMAへ移動させる | locality drift | policyによりdegraded/non-compliant | NUMA mapping、performance alarm | automatic cross-NUMA受容 | policy準拠配置または明示例外 |
| FI-STORAGE-001 | Volume attach適用後response timeout | attachment timeout | attachment generation block | Attempt UNKNOWN、backend/Host evidence | detach/別Host attach | single-writerとattachment state確定 |
| FI-STORAGE-002 | Ceph unavailable中にVolume operation | backend health/error | 対象backend mutation停止 | backend alarm、Operation待機/失敗 | local/silent backend fallback | backend復旧+capability+read-back |
| FI-SPLIT-001 | old leader/authority generationからLease/Result送信 | generation/token mismatch | stale actor拒否 | conflict audit、current generation | Job/Desired進行 | current authorityから再同期 |
| FI-IDENTITY-001 | JWKS/certificate revocation state unavailable | trust validation unavailable | privileged mutation fail closed | bounded auth error、audit | stale/unknown trustで新mutation | trust generation復旧 |
| FI-AUDIT-001 | durable audit outbox writeを失敗させる | audit unavailable | 管理mutation transaction rollback | failure metric、request correlation | 監査なしmutation | audit durability復旧後に再受付 |

## 4. Coverage Mapping

| Failure Model class | Injection IDs |
|---|---|
| Client | FI-CLIENT-001..002 |
| API / Control Plane | FI-CP-001..002 |
| Database / DR | FI-DB-001..002, FI-DR-001 |
| Internal Message | FI-BUS-001..002 |
| Agent Gateway / Transport | FI-GATEWAY-001..002, FI-TRANSPORT-001..002 |
| Agent | FI-AGENT-001..002 |
| Host | FI-HOST-001..002 |
| libvirt / QEMU | FI-LIBVIRT-001..002 |
| Network / NFV Dataplane | FI-NET-001..002, FI-DPDK-001..006 |
| Storage | FI-STORAGE-001..002 |
| Split-brain / Stale Authority | FI-SPLIT-001 |
| Identity / Audit | FI-IDENTITY-001, FI-AUDIT-001 |

## 5. Release Gate

- Developer Preview: Client、Execution、Agent、DB failoverのcritical pathsをImplemented。
- Technical Preview: 全12classで最低1 testをImplementedし、multi-node環境で証拠保存。
- Product Beta: network/storage partition、Host fencing、DR restoreを含む全matrixを自動または承認済みrunbookで実行。
- GA: release candidateごとにcritical subset、定期chaos campaignでfull setを実行。
