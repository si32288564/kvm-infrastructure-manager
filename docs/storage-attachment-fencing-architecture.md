# Storage, Attachment, and Fencing Architecture

- 状態: Baseline
- 更新日: 2026-08-09

## 1. 目的と責任境界

本書はVolume lifecycle、VM attachment、single-writer authority、backend observation、Host failure/migration時のstorage fencingを定義します。

KIMが所有するもの:

- Volume metadata、Project ownership、desired lifecycle、quota/capacity claim
- Storage Class/Backend capabilityとVolume Backend Binding
- Attachment Intent/Claim/Generation、Operation、verification evidence
- KIM管理VMに対するattach/detach/snapshot/clone/delete orchestration
- Host recovery/migration時のstorage eligibilityとfencing decision

KIMが所有しないもの:

- Ceph cluster、OSD/MON/MGR、LVM physical disk/VGそのものの構築・upgrade・repair
- SAN/NAS fabric、external storage array、backup productのlifecycle
- guest filesystem/application consistency、VNF内部replication/cluster lock manager
- backendにだけ存在するimage/LVの無条件adoption/delete

backend adapterが存在しても、外部Storage Platformのcluster authorityをKIMへ移しません。

## 2. Backend and Storage Class Model

```text
StorageBackend
├─ backend_id / type / endpoint_reference
├─ failure_domain / access_scope
├─ capability_generation / health_generation
├─ supported_features / limits
├─ credential_reference
└─ support_tier / status

StorageClass
├─ class_id / version
├─ allowed_backends / placement constraints
├─ durability / locality / performance profile
├─ access_modes
├─ snapshot / clone / expansion policy
├─ fencing requirements
└─ encryption / deletion policy

StorageCapacityPool
├─ backend_id / capacity_scope / generation
├─ total / hard_reserve / allocated / reserved
├─ thin_provisioning / allocation_ratio / policy revision
├─ observed_used / external_or_unknown_usage
├─ data/metadata health
└─ observed_at / freshness / eligibility
```

初期backend:

| Backend | Resource locality | 主用途 | Recovery特性 |
|---|---|---|---|
| Local LVM | 一つのHost/VG | local/ephemeralまたは明示local persistent Volume | Host外restart不可。replication/exportを別capabilityとして証明しない限り`restart-on-other-host=none` |
| Ceph RBD | 許可されたCeph cluster/pool/namespaceへ到達するHost群 | shared persistent block Volume | client/source fencing、image identity、lock/watcher、destination accessを証明後に別Host recovery可能 |

capabilityはbackend type名だけで推測せず、versioned factsとして公開します。最低限、create/delete/expand、snapshot/clone、online expansion、access mode、shared/local、exclusive lock/client fencing、discard、encryption、migration handoff、consistency levelを表します。未対応機能へ別backendや別access modeでsilent fallbackしません。

KIMが管理するreserved/allocated容量はPostgreSQL ledgerをauthorityとし、backend total/used/free、thin pool data/metadata、外部/未知使用量はgeneration付きobservationとして安全余裕とeligibilityへ反映します。stale/UNKNOWN health/capacityを楽観的な空き容量へ丸めません。Final AdmissionはStorage Capacity Claimを他resource/Quotaと不可分commitします。Volume deleteではbackend absence verificationまでphysical capacity claimを再利用せず、論理Quota release policyとbackend capacity releaseを分離します。

## 3. Core Resource Model

```text
Volume
├─ volume_id / project_id
├─ storage_class_revision
├─ desired_generation / lifecycle
├─ size / access_mode / bootable
├─ current_backend_binding_id
└─ current_attachment_summary_id

VolumeBackendBinding
├─ binding_id / generation
├─ backend_id / backend_resource_stable_id
├─ pool / namespace / host / VG scope
├─ capability_snapshot / locality
├─ encryption_secret_reference
└─ created/deleted evidence

VolumeAttachment
├─ attachment_id / volume_id / vm_id
├─ desired_host_id / attachment_generation
├─ access_mode / device contract
├─ current_claim_id / handoff_id
├─ desired_state / observed_summary
└─ Operation / evidence references

AttachmentClaim
├─ claim_id / volume_id / attachment_generation
├─ VM / Host / access authority
├─ state / authority generation
├─ fencing policy revision
└─ acquired/released decision

AttachmentObservation
├─ observer / source identity / observed_at
├─ backend image/LV identity
├─ libvirt/QEMU device identity
├─ watcher/lock/client evidence
├─ attachment generation / freshness
└─ observation outcome / digest
```

Desired Attachment、PostgreSQL Claim、backend/libvirt observationを分離します。watcher、lock、device presence、I/O telemetryはobservationであり、単独でAttachment Claim authorityを作成・譲渡・解放しません。

## 4. Access Modes and Single-writer Invariant

初期access mode:

- `SINGLE_WRITER`: active write Attachment ClaimはVolumeごとに最大一つ。
- `READ_ONLY_MANY`: backend、QEMU/device contract、Storage Classが明示対応する場合だけ複数read-only claimを許可。initial profileでは同時writerと共存させず、全active Claimがread-onlyであることを要求。
- `SHARED_WRITER`: 初期support外。将来、certified backend fencingとguest/application cluster contractを別profileで定義するまで拒否。

`SINGLE_WRITER`はPostgreSQLのunique active claimとattachment generationで確定します。しかしDB Claimだけでは旧HostのI/O停止を証明しません。別Hostへのwrite authorityには、DB authorityと実世界fencing evidenceの両方を必要とします。

Project ownership、Volume lifecycle、access mode、VM/Host、backend capability、active Claim、Attachment/Backend generationはFinal Admissionでlock/revalidateし、Allocation/Quota/Availability Binding等と不可分に予約します。transaction中にCeph/LVM/libvirtへ接続しません。

## 5. Attachment State Model

```text
REQUESTED
  -> RESERVED
  -> PREPARING
  -> ATTACHING
  -> VERIFYING
  -> ATTACHED
  -> DETACH_REQUESTED
  -> DETACHING
  -> RELEASE_VERIFYING
  -> DETACHED

Any non-terminal state
  -> UNKNOWN | BLOCKED | FENCE_REQUIRED
```

- `RESERVED`: DB Attachment Claim/desired stateだけが確定し、backend side effectは未開始。
- `ATTACHED`: current DB Claim、libvirt/QEMU device、backend client/watcher/lock evidenceがgeneration付きで一致。
- `DETACHED`: libvirt/QEMU device absence、backend client/watcher/lock release、必要なI/O quiescenceが確認され、Claim release decisionがcommit済み。
- `UNKNOWN`: side effect実行有無、client I/O、device/lock状態のいずれかを証明できない。
- `FENCE_REQUIRED`: 新write authority前にsource compute/storage fencingが必要。

timeout、Lease expiry、Agent/Gateway/DB failoverを`DETACHED`の証明にしません。

## 6. Attach Workflow

1. APIがVolume/VM Project ownership、desired generation、idempotencyを検証する。
2. Placement/Final AdmissionがHost/backend reachability、locality、capacity/quota、access mode、active Claimを評価する。
3. DB transactionでAttachment Intent/Claim、VM/storage claim、Operation/Job/Command intent、Outboxをcommitする。
4. Executionがtyped backend prepareとHost/libvirt attachを実行する。
5. Agent/backend adapterがstable Volume/backend identityとattachment generationをread-backする。
6. DB Claim、libvirt device、backend watcher/lock/client evidenceが一致した場合だけ`ATTACHED`へ進める。

attach side effect後にResultを失った場合は`UNKNOWN`としてread-backします。同じAttachment generation/CommandはAgent journal/backend idempotencyへ収束し、反対detachや別Host attachを開始しません。

## 7. Detach Workflow

1. active VM/device impactとguest/application quiesce requirementを評価する。
2. authorized typed detachでQEMU/libvirt I/O pathを停止・除去する。
3. backend client、watcher、lock、mappingをtyped resolverで確認・解放する。
4. sourceにwrite I/Oが残らないことをpolicy要求のevidence setで検証する。
5. DB transactionでAttachment Claimをreleasedへ進め、Attachment generationを終了する。

Claimはdetach Command発行時ではなく、verified release後にだけ解放します。libvirt device absenceだけ、Ceph watcher absenceだけ、Host heartbeat lossだけではreleaseしません。force detachは通常detachの別名ではなく、source/storage fencing、impact approval、bounded target、post-verificationを持つ明示Operationです。

## 8. Fencing Model

別Hostへwrite authorityを移す際は、次を区別します。

| Fence | 証明対象 |
|---|---|
| Compute source fencing | 旧QEMU/HostがCPU実行と新I/Oを開始できない |
| Storage client fencing | 旧Ceph client/session、mapping、lock holder等がbackendへwriteできない |
| Attachment authority fencing | 旧Attachment generation/Claim/Command/Resultがcurrent DB authorityを進められない |

Storage Fencing Proofはpolicy revision、Volume/backend identity、source Host/client identity、old/new attachment generation、fence mechanism、issuer/provenance、実行結果、fresh verificationを保持します。

Cephではexclusive-lock、watcher/client identity、blocklist等をadapter capabilityとして扱います。watcher/lockの存在または不在はevidenceの一部であり、stale monitor viewやsession raceを考慮せずsingle-writer証明へ昇格しません。LVMではLV/VG identity、device-mapper/open holder、source Host fencingを組み合わせます。

fencing outcomeが`UNKNOWN`ならVolumeを`FENCE_REQUIRED/BLOCKED`に保ち、別Host Claimをactiveにしません。operator overrideでもfencing gate自体を省略できません。

## 9. Host Failure and Managed Recovery

Host failure時、Availability responsibilityに関係なくaffected Attachmentを特定し、source I/O ownershipを`CONFIRMED/FENCED/UNKNOWN`として評価します。

- `WORKLOAD_MANAGED`: KIMはAttachmentを保護しFault/Eventを通知するが、自動replacement attachを開始しない。
- `MANUAL`: authorized Manual Recovery Decision後も同じstorage fencing/admission gateを通る。
- `INFRASTRUCTURE_MANAGED`: compute fencing、storage client fencing、single-writer Claim、backend/destination capability、current Placementを満たして初めてRecovery Attachment generationを作る。

Ceph healthが回復しただけ、Host heartbeatが途絶えただけ、watcherが消えただけで新Claimを発行しません。Recovery Campaign/Operation、Volume、old/new Attachment generation、Fencing Proofを相関付けます。

Local LVM Volumeを持つVMは、source storageがdestinationから同一identityで安全に利用可能と証明されない限り別Host recovery不適格です。Host failure時はVolumeを`UNAVAILABLE/UNKNOWN`として保持し、空Volumeや同名LVを作ってreplacementとみなしません。

## 10. Migration and Attachment Handoff

Migration capabilityはVolumeごとにshared reachability、access mode、backend feature、source/destination client fencing、active operation、snapshot/clone dependencyを評価します。

```text
AttachmentHandoff
├─ handoff_id / volume_id
├─ source/destination Host
├─ source_attachment_generation
├─ target_attachment_generation
├─ mode: COLD | LIVE_PROTOCOL
├─ preparation / switchover / fencing evidence
└─ state / Lease / verification
```

Cold migrationはsource verified detach後にdestination Claimをactive化します。Live migrationでbackend/libvirt protocol上の一時的dual accessが必要な場合も、一つのlogical write authorityとbounded `LIVE_PROTOCOL` handoffとして扱い、一般的な二つのactive writer Claimへ変換しません。response loss時はsource/destination QEMU、backend client/lock、libvirt migration stateをread-backし、両側を推測cleanupしません。

Local LVMは別途certified copy/replication workflowがない限りlive/restart-on-other-hostを提供しません。

## 11. Ceph RBD Contract

- backend resourceはcluster FSID、pool/namespace、RBD image stable IDでbindし、display nameだけで照合しない。
- CephX secret valueはSecret Providerが所有し、KIMはscoped reference/versionだけを保持する。
- image feature、object size、data pool、exclusive-lock、journaling等をcapability/binding snapshotへ記録する。
- attach/detach verificationはQEMU/libvirt deviceとRBD client/watcher/lock evidenceを相関する。
- snapshot/clone dependency、protected snapshot、child image、trash/deferred deletionを明示stateとして扱う。
- Ceph health/timeout後にunknown image、watcher、lock、trash entryを自動削除しない。

## 12. Local LVM Contract

- backend resourceはHost identity、VG UUID、LV UUIDでbindし、device path/nameだけをauthorityにしない。
- capacity reservationとLV create resultを分離し、timeout後はLV UUID/tag/read-backで解決する。
- source Host/VGへlocalityを固定し、Placementで別Host候補をineligibleにする。
- thin pool data/metadata health、VG/LV availability、open holder、device-mapper stateをcapability/observationとして扱う。
- Host喪失時に別Hostへ同名LVを作って元Volumeとしてadoptしない。

## 13. Snapshot, Clone, Expand, and Delete

- snapshot consistencyを`CRASH_CONSISTENT`、明示guest coordination付き`APPLICATION_QUIESCED`等で区別し、未証明のapplication consistencyを表示しない。
- Snapshot/Cloneはsource Volume/backend binding、parent/child dependency、backend generationを保持する。
- online expandはbackendとguest/device capabilityを別に評価し、backend拡張成功だけでguest-visible size収束を宣言しない。
- Volume deleteはactive/pending Attachment、Snapshot/Clone child、Recovery/Migration/UNKNOWN、legal holdを検証する。
- DB tombstone/metadata GCとbackend image/LV cleanupを分離し、typed deleteとabsence verification後にだけbackend deletionを完了とする。

## 14. Observation, Reconciliation, and Adoption

Storage observationはsource identity、backend/Host generation、stable resource ID、freshness、digestを持ちます。desired/claimと一致しない状態を次へ分類します。

- `MATCHED`
- `DB_ONLY`
- `BACKEND_ONLY`
- `CONFLICTING`
- `UNKNOWN`

backend-only image/LV、unknown watcher/lock、unmatched libvirt deviceを自動adopt/delete/detachしません。AdoptionにはProject ownership、stable identity/provenance、no-conflict、capacity/quota、encryption secret、attachment/fencing、operator authorizationを要求し、新しいBinding/Generationとしてcommitします。

## 15. Security and Secret Boundary

- Storage adapter/service identityをbackend/pool/namespace/operationごとに最小権限化する。
- Tenant/APIへCephX key、secret value、monitor raw detail、Host device path、VG/LV internalsを公開しない。
- force detach、client blocklist、lock break、backend delete、Adoptionを別permission/approvalで保護する。
- command schemaはstable resource identity、generation、bounded operationだけを受け、任意RBD/LVM command/argument/pathを許可しない。
- attachment/fencing evidenceとauditを改ざん検知可能に保持する。

## 16. API and Event Contract

公開resource:

- `/storage-classes`、`/volumes`、`/volume-snapshots`
- `/volume-attachments`、`/operations`
- 許可されたbackend capability/health projection

mutationはidempotency key、ETag/generation、Operationを要求します。EventはVolume/Attachment ID、old/new generation、state、bounded reason、Operation/Fault/Recovery correlationを持ち、secret/raw backend identityをredactします。

## 17. Failure Semantics

| Failure | State / Action | Prohibited |
|---|---|---|
| attach/detach timeout | Attempt/Attachment `UNKNOWN`、typed read-back | 反対操作、Claim解放、別Host attach |
| Ceph/LVM unavailable | affected backend/Volume mutation pause | silent backend fallback |
| stale watcher/lock | evidence stale/UNKNOWN、fresh resolver | stale表示だけでlock break/Claim譲渡 |
| Host loss | source compute/storage fencing required | heartbeat lossだけでreplacement attach |
| DB commit response loss | idempotency/Attachment generation read-back | duplicate Claim/Operation |
| migration response loss | source/destination/backend observation | 両側detach、二active Claim |
| delete response loss | stable backend identity read-back | 同名resource削除、tombstone GC |

## 18. Verification Contract

- concurrent single-writer Attachment Claim
- attach/detach side effect後のresponse lossとUNKNOWN/read-back
- stale/delayed libvirt、Ceph watcher/lock、LVM holder observation
- source compute fence成功・storage client fence失敗、および逆の組合せ
- Host recoveryでold Claim/Resultをfenceしnew generationだけをactive化
- local LVM Volumeの別Host recovery拒否
- Ceph shared Volumeのdestination access/fencing/final admission
- live/cold handoff crashと二重writer禁止
- snapshot/clone dependency、expand partial convergence、delete guard
- backend-only/adoption、PITR後のAttachment classification
- concurrent capacity claim、thin pool data/metadata pressure、stale/unknown external usage、delete前capacity reuse禁止
- secret redaction、force operation permission、adapter conformance
