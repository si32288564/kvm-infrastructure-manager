# KIM Northbound API / Terraform Readiness Review

- Review date: 2026-08-14
- Repository baseline: Migration 001–074
- Baseline commit: Phase 1 Project+Flavor logical-resource delivery commit
- Primary SSOT: [Infrastructure Lifecycle and IaC Architecture](../infrastructure-lifecycle-iac-architecture.md)
- Scope: repository-based re-review after the executable Project reference vertical slice
- Decision: **Conditional — Provider scaffold and experimental Project/Flavor resources may begin; Image/Availability and all backend resources remain blocked**

## 1. Executive Summary

KIM の内部 authority model は Terraform と相性のよい要素を多く持っています。stable logical ID、immutable revision/evidence、current projection、idempotent replay、generation fencing、asynchronous subsystem Operation、desired/observed separation、Recovery/EVACUATE 後の logical identity continuity は PostgreSQL persistence と integration tests で確認できました。

Migration 074 は同じcontractをFlavorへ適用し、Project固有だったprincipal/error/IDとHTTP request/ETag/cursor/Problem Details規約を共通化しました。Flavorは既存Migration 010のimmutable shape/current authorityとPlacement consumerを再利用し、更新は新revisionを作るだけで既存VMのexact revision bindingを変更しません。

したがって、内部 persistence function や table が存在する resource も `TERRAFORM_READY` ではありません。Provider を先行実装すると、次の P0 authority violation を誘発します。

- Provider が PostgreSQL/internal Go function/backend を直接 client interface として扱う。
- `virtual_machines_current.host_id`、Volume Binding の VG/LV UUID、Port Binding の Host/BDF/generation を desired state として保存する。
- subsystem-specific Recovery/EVACUATE/Cleanup state machine を Terraform CRUD として再実装する。
- response loss、delete、stale update を Provider 側の推測や recreate で解決する。

Provider scaffoldとexperimental Project/Flavor resourceの開始条件は成立しました。Imageはartifact ingestion/observed digest authority未分離、Availability Policyはpublic scope/typed dependency contract不足のためBLOCKEDです。unified Operation、Provider import conformance、VM no-driftも未実装です。

## 2. Review Methodology

次を current implementation の根拠として実査しました。

1. accepted ADR-0001–0029 と architecture/API/security/resource documents
2. Migration 001–074 の current/evidence/operation schema
3. `internal/persistence/postgres` の producer、consumer、read helper、integration tests
4. `cmd/kim-api`、HTTP/gRPC listener、OpenAPI/Swagger artifact、OIDC/RBAC/ETag/idempotency implementation の repository search
5. current architecture qualification inventory と validation documents

`IMPLEMENTED API` の判定には、schema、persistence producer、public read projection、public mutation path、authorization enforcement、tests、current validation の全てを要求しました。DB table または internal function だけでは API endpoint と数えていません。

### 2.1 Evidence summary

| Layer | Repository evidence | Finding |
|---|---|---|
| Public API process | configurable listener、TLS fail-closed、timeout、bounded body、request ID、panic recovery、health/readiness、graceful shutdown | implemented |
| HTTP resource handlers | Project/Flavor POST/GET/list/PATCH/DELETE under `/api/v1` | implemented for Project and Flavor |
| OpenAPI | `api/openapi/kim-v1.json` + executable contract test | implemented multi-resource contract |
| Northbound auth | RS256 OIDC/JWKS verification for HUMAN/AUTOMATION; system/Project READER/WRITER/ADMIN enforcement | implemented minimal closed model |
| Persistence | resource/authority producers and projections are extensive | implemented internally |
| Operations | execution, Recovery, EVACUATE, cleanup 等の typed state machines | implemented per subsystem; no unified public projection |
| Qualification | real HTTP listener、PostgreSQL 17 concurrency/replay/authz、JWT、OpenAPI、race tests | Project/Flavor Northbound qualification PASS |

## 3. Current Northbound API Inventory

Project と Flavor が Create/Read/List/Update/Delete endpoint を持ちます。他行の `Internal authority` は API 実装とは別の repository maturity を示します。

| Resource/capability | Stable identity / revision | Internal authority and qualification | Public lifecycle gaps | Readiness |
|---|---|---|---|---|
| Project | UUIDv4 stable ID + immutable resource revision | Migration 073 current/evidence、owner binding、idempotency、audit、dependency/protection guards、HTTP integration | quota/policy/tenant hierarchy、formal Provider import test | `TERRAFORM_READY` for experimental implementation; release blocked |
| Site | dedicated Site resource なし。Failure Domain HostGroup level の文字列としてのみ表現可能 | HostGroup hierarchy synthetic tests | Site identity/ownership/lifecycle/API がない | `RESOURCE_MODEL_GAP` P2 |
| Host | stable `host_id`; identity/enrollment/authority/session generations | trust/inventory/qualification は実装・real tested | public inventory/status projection、admin mutations、ETag、scope/redaction がない。Terraform-managed resourceにするかも未決定 | `API_SEMANTIC_GAP` P1 |
| Host Group | `host_group_id` + generation、immutable revision digest | Migrations 038–047、publish/snapshot/selectors/hierarchy synthetic pass | public CRUD/list、expected revision、delete guard、authorization、field contract がない | `API_SEMANTIC_GAP` P1 |
| Placement Scope / Pool | stable scope/group ID + generation/digest | Scope publication/final admission、Host drain fencing synthetic pass | tenant-safe projectionは未公開。Placement/Admissionは CRUD resource ではなく internal authority | Scope: `API_SEMANTIC_GAP` P1; Admission: `OPERATION_ONLY` |
| Image | `image_id` + immutable verified revision; current authority generation | checksum/signature validation、replay conflict、materialization integration | caller supplied observed checksumを許さないingestion Operation/source redactionがない | `BLOCKED` (artifact authority boundary) |
| Flavor | UUIDv4 + immutable shape revision; Project ownership | public CRUD/list、scope auth、idempotency、ETag、dependency/protection/tombstone、Placement exact revision | formal Provider import/acceptance | `TERRAFORM_READY` for experimental implementation; release blocked |
| Virtual Machine | UUID + `vm_generation`; logical workload ID | Placement→materialization→readiness→power、Recovery/EVACUATE qualified | public create aggregate、desired/status DTO、update/delete/tombstone、Operation、auth absent。current row contains physical Host/plan | `API_SEMANTIC_GAP` P0/P1 |
| Volume | `volume_id` + desired generation | Local LVM allocation/binding/attachment/copy/cleanup qualified | Volume currently Admission-created and physical binding-heavy。standalone CRUD、expand/delete/import/protection public contract absent | `API_SEMANTIC_GAP` P0/P1 |
| Volume Attachment | `attachment_id` + generation | single-writer claim and typed read-back qualified | logical desired Attachment と current physical Host/holder projection の public separationなし | `RESOURCE_MODEL_GAP` P0/P1 |
| Network | `network_id` + generation | `UpsertNetworkFoundation`、OVN/OVS realization qualified | Network/Subnet/Segmentを一 internal callでupsert。immutable revision evidence、separate CRUD/delete/public authなし | `API_SEMANTIC_GAP` P1 |
| Subnet | `subnet_id` + generation | IP range/claim validation and placement use | independent lifecycle、gateway/DHCP/update rules、dependency/delete guard、public projectionなし | `API_SEMANTIC_GAP` P1 |
| Port | stable `port_id`; generation advances across incarnation | Final Admission-created Port/MAC/IP/Binding、Recovery/EVACUATE handoff qualified | independent logical Port CRUD/import/update/deleteなし。Admission and physical Binding coupled | `RESOURCE_MODEL_GAP` P0/P1 |
| Router | none | architecture/requirements only | complete resource/realization/API absent | `RESOURCE_MODEL_GAP` P2 |
| Floating IP / public IP | no first-class resource | IP/MAC claims only; generic floating/NAT producer absent | identity、allocation、association、release、router dependency absent | `RESOURCE_MODEL_GAP` P2 |
| Availability Policy | `policy_id` + immutable revision/digest | policy publication/binding/rebind/Recovery consumption | SYSTEM/PROJECT scope、name、typed child-policy authoring、PLACEMENT_POOL dependency/delete contract未確定 | `BLOCKED` (`RESOURCE_MODEL_GAP` public intent) |
| Resilience Policy/Group | requirements/ADR model; active schema producer not found | no current resource implementation | complete model/API absent | `RESOURCE_MODEL_GAP` P2 |
| PCI/SR-IOV logical requirement | Placement request requirement; physical claim/BDF generation | generic allocation/retirement/Recovery synthetic pass | no persistent portable requirement/profile resource; internal request exposes physical BDF | `RESOURCE_MODEL_GAP` P0/P2 |
| Datapath Profile | none | STANDARD/DPDK/Direct-I/O target architecture; kernel OVS realization exists | profile schema、capability contract、CRUD absent | `RESOURCE_MODEL_GAP` P2 |
| Security Policy/ACL | none | requirements/ADR target only; no policy table/compiler | logical schema、revision、selector、OVN compiler/read-back/API absent | `RESOURCE_MODEL_GAP` P1 |
| Router protocol/Network Service | none | FRR/BGP/OSPF/IS-IS/BFD/VRF target only | resource model and backend absent | `OUT_OF_SCOPE` for Provider MVP |
| Recovery | operation ID and subsystem generations | Migrations 050–058; typed evidence/terminal; real qualification | no unified Northbound operation/action/poll/cancel/auth/retention projection | `OPERATION_ONLY` P1 API gap |
| Host EVACUATE / Drain | evacuation/drain/child IDs and generations | Migrations 066–072; synthetic end-to-end | same; must not become Terraform resource | `OPERATION_ONLY` P1 API gap |
| Cleanup / read-back / retry | typed operation/command IDs | subsystem-specific implementation and replay tests | public administrative operation contract absent | `OPERATION_ONLY` P1 API gap |

### 3.1 Qualification interpretation

Internal authority qualification remains valid. It proves that a future Northbound API can call safe primitives; it does not prove endpoint schema, authorization, pagination, concurrency, error mapping, or Provider behavior. Project and Flavor alone now meet the experimental public resource contract; all other resources retain their documented gaps.

## 4. Terraform Resource Lifecycle Contract Assessment

### 4.1 Identity

Image、Flavor、VM、Volume、Network、Subnet、Port、HostGroup、Policy は stable ID candidates を持ちます。display name と ID は Flavor 等で分離されています。一方 Project、Site、Router、Security Policy、Datapath Profile は first-class identity がありません。

Public create が server-generated ID と client-supplied ID のどちらを使うか、import identifier grammar、ID retention/tombstone、rename behavior は未定義です。Provider は internal request/operation/evidence ID を resource ID にしてはいけません。

### 4.2 Revision and concurrency

Project と Flavor は public desired revision、ETag、required If-Matchを実装し、stale mutationを412で拒否します。他resourceの internal `expected generation` や revision conflict はまだ HTTP `If-Match` contract ではありません。

### 4.3 Create

Project と Flavor は Northbound `Idempotency-Key` を principal/parent/method/canonical pathとcanonical desired digestへbindし、resource transaction内で確定します。それぞれの idempotency evidence は exact resource revision FK を持ちます。Image/HostGroup/Policy 等は internal replay authorityのみで、Northbound contractは未実装です。

VM create は複数 internal callsを Provider が順番に実行してはなりません。logical desired aggregateとOperationを一 transactionで受け、KIMだけが Final Admission/Materialization chainを進める public create contract が必要です。

### 4.4 Update

Project と Flavor はtyped partial PATCHを採用し、absent/null/value、mutable/immutable分類、revision preconditionをOpenAPIとhandlerで検証します。Flavor 更新は immutable revision を追加し、existing VM の exact revision binding を retrofit しません。他resourceにはpublic PATCH/replace/action distinctionがありません。

Image/Availability Policy の新 revision semantics は authority gap の解消時に定義します。Flavor は新しい immutable revision を current にしますが、既存VMのexact Flavor revisionを変更しません。VM Network/Volume/Availability変更は async reconcile、replace、明示 operation のどれかを resource-specific に決める必要があります。Providerが `ForceNew` を推測してはいけません。

### 4.5 Delete

Project と Flavor はdependency conflict、delete protection、tombstone、If-Match、同一削除retryを定義します。Flavor は Placement Admission/materialization 参照を silent cascade せず拒否します。他resourceの `DELETING`、`DELETE_PENDING`、`DELETED` 等には一貫したpublic producer/API/tombstone/retentionがありません。

特に VM/Volume/Network delete を current rowの削除やbackend absenceへ直接mappingすると unsafeです。Cleanup terminalとcapacity reclamationはworkload deletionと独立したままにします。

### 4.6 Read and refresh

Project と Flavor には物理realizationを含まないpublic DTOがあります。backend resourceのTerraform Readは、PostgreSQL desired/current/immutable evidenceから次の三層を分離して返す必要があります。

```text
spec    = persistent desired fields managed by client
status  = computed convergence/health/operation summary
links   = authorized references to observations/history/operations
```

`virtual_machines_current.host_id/current_plan_id`、Port/Volume Binding、runtime generationを `spec` へflattenしてはいけません。statusがtemporarily `UNKNOWN`/`BLOCKED`でも desired configurationの差分にしません。

### 4.7 Import

Project/Flavor は stable ID で authorized public Read と desired state 復元が可能です。tombstone は 404 とし、physical realization は DTO に含めません。その他の stable logical ID candidates は authorized public Read がないため import 不可です。import は KIM-managed logical resourceだけを対象にし、libvirt Domain、OVN object、OVS interface、LV、PCI VFをbackendからdiscoverして暗黙adoptしません。

### 4.8 Error contract

Go sentinel errors と PostgreSQL constraint errors はありますが、public stable error taxonomy へのmappingはありません。message text、SQLSTATE、backend stderrをProviderが解釈する設計は禁止です。

## 5. Resource vs Operation Assessment

### 5.1 Persistent resources

Project、Image、Flavor、VM、Volume、Network、Subnet、Port、logical Policy/Profile は persistent resource候補です。Placement Admission、materialization plan、binding incarnation、Command/Lease/Attempt、Recovery/EVACUATE generation はresource status/historyであってTerraform resourceではありません。

### 5.2 Operation contract gap

Execution Job、Recovery、EVACUATE、Cleanup、Upgrade/Maintenance は個別に高品質な state/evidenceを持ちます。しかし共通 public Operation table/projection/API はありません。以下を正規化する contract が必要です。

| Field | Required semantics |
|---|---|
| `operationId` | stable、type namespace付き、再利用禁止 |
| target | resource type/ID と requested resource revision |
| request context | principal、Project/Site scope、authorization decision、Idempotency-Key/request digest |
| type | closed enum; resource mutationとadministrative actionを区別 |
| time | accepted/updated/terminal DB authority time |
| phase | bounded type-specific phase + common queued/running/waiting/read-back/operator-action |
| terminal | succeeded/failed/cancelled/blocked/unknownを混同しない |
| error | stable code、retryable、safe next action、request ID |
| cancellation | cancel requestでありside effect absence/rollback proofではない |
| history | immutable ordered event references |
| polling | ETag/long poll/event link、retry hint、eventual consistency |
| retention | resource/tombstone/idempotency/audit retentionより短くしてよいかを明示 |

Terraform Provider は CRUD に付随する Operation をpollできます。Recovery、EVACUATE、Drain、Retry、Cancel、Read-back、Cleanup、Reconciliation、diagnostic/qualificationを standalone managed resourceにしません。

## 6. Desired / Computed / Immutable Field Review

### 6.1 Common classification

| Class | Meaning | Examples |
|---|---|---|
| `REQUIRED_DESIRED` | create/update intentに必須 | Project reference、VM name/Flavor、Network CIDR |
| `OPTIONAL_DESIRED` | explicit default/null semanticsを持つintent | metadata、policy/profile reference、delete protection |
| `COMPUTED` | KIMが返すがconfig ownership外 | convergence、current power observation、current Host summary |
| `IMMUTABLE` | logical identity期間中に変更不可。replacementかnew revisionはcontract定義 | resource ID、Port logical identity、some scope/parent bindings |
| `SENSITIVE` | read/state/log redactionまたはwrite-only/reference | secret reference、short-lived console/upload token |
| `OPERATION_ONLY` | action request/status/historyだけに存在 | drain reason、retry/cancel request、Recovery generation |
| `INTERNAL_ONLY` | Northbound schemaへ公開しない | Lease token、Agent session、backend path、exact claim/evidence internals |

### 6.2 Physical realization rule

| Field | Classification | Terraform behavior |
|---|---|---|
| current Host | `COMPUTED` authorized summary | refresh-only; no diff/replacement |
| pCPU/NUMA/HugePage physical allocation | `COMPUTED` or `INTERNAL_ONLY` | logical policy only desired |
| PMD/RxQ/vhost-user socket/interface | `INTERNAL_ONLY` by default | profile/capability only desired |
| OVS UUID/OVN binding generation | `INTERNAL_ONLY` | evidence/status links only |
| PCI BDF/IOMMU group | `INTERNAL_ONLY` or redacted `COMPUTED` admin view | portable PCI requirement only desired |
| VG/LV UUID/device path | `INTERNAL_ONLY` | Volume/Storage Class only desired |
| Materialization/Recovery/EVACUATE generation | `OPERATION_ONLY`/`INTERNAL_ONLY` | Operation/history only |

現行 internal Placement DTO は `VGUUID`、PCI/network `DeviceAddress`、`HostMappingGeneration` 等を受け取ります。これは Final Admission がDB-derived immutable requirementを扱う internal interfaceとして妥当ですが、public Terraform requestへ直接公開できません。logical request compiler/API aggregateが必要です。

### 6.3 Normative no-drift scenario

```text
Given desired VM logical ID V:
  network = X
  datapath_profile = HIGH_PERFORMANCE
  availability_policy = HA
And runtime materialization is Host A
When KIM confirms failure and completes Recovery to Host B
Then Read(V).spec is semantically unchanged
And Host/materialization/binding generations change only in status/history
And terraform refresh/plan proposes neither update nor replacement
```

This is now requirements `IAC-006`/`IAC-011`, invariants `INV-API-005`/`INV-API-006`, and acceptance contracts `AT-IAC-002`/`AT-IAC-003`.

## 7. Import / Drift / Replacement Semantics

- Import identifier should initially be `<resource-type>/<stable-kim-id>` or a type-specific documented stable ID. Project-qualified composite IDs are allowed only when global uniqueness is not guaranteed; physical IDs are forbidden.
- Read after import reconstructs desired fields from KIM resource revision, not from current backend realization.
- remote desired mutation is drift and must be shown after refresh; computed status change is not drift.
- `ETag`/revision conflict must stop apply rather than auto-merge unknown remote desired changes.
- replacement triggers are Resource Contract metadata. Provider code does not infer them from generation changes.
- resource replacement cannot silently perform Recovery/EVACUATE. Capacity overlap, create-before-destroy, dependency and delete protection must be explicit.

## 8. Async Operation Contract

ADR-0002/ADR-0029 distinguish completion boundaries. PostgreSQL-only Project mutations complete synchronously; backend-convergent mutations require `202 Accepted` + Operation. The remaining gap is executable unified Northbound Operation projection and cross-subsystem normalization.

Create/update/delete response must atomically identify both logical resource and Operation. On HTTP timeout the client retries the same Idempotency-Key or reads the resource/Operation; it does not issue delete/recreate. Provider wait timeout leaves KIM work running unless a separately authorized cancel request is accepted. Cancel acceptance never proves backend rollback.

Terminal resource convergence is type-specific: VM `RUNNING` requires exact materialization readiness and power observation; Network requires required OVN/OVS evidence; Volume requires binding/attachment or absence terminal as applicable. HTTP 202、Command `SUCCEEDED`、backend response alone are non-terminal.

## 9. Error / Retry Contract

Required stable categories are:

| Category | Retry rule |
|---|---|
| validation/unsupported | non-retryable until configuration changes |
| unauthenticated/forbidden | credential/scope correction; existence hiding preserved |
| not found/tombstone | resource contract decides import/delete/external deletion behavior |
| stale revision | refresh and present conflict; never silent overwrite |
| dependency/protection | non-retryable until referenced state/approval changes |
| allocation/quota/capability | may be retryable only with server reason/hint; no forced Host |
| operation in progress | poll same Operation; do not duplicate mutation |
| backend `UNKNOWN` | read-back-first inside KIM; Provider polls |
| terminal failed/blocked | return stable diagnostic and retained resource/Operation state |
| transient unavailable/rate limit | bounded exponential backoff + jitter/`Retry-After` |

RFC 9457 body must include stable `code`、`requestId`、`retryable`、resource/Operation reference and optional safe retry hint. Raw SQL/backend error text is not public contract.

## 10. Security / Authorization

### 10.1 Current

External Identity ownership、OIDC、system/tenant/project RBAC、Service Identity は accepted requirements/designです。Host Agent mTLS enrollment/session/operation authorization は実装されていますが、Northbound user/service authorizationの代替ではありません。Northbound bearer token verification、membership/role tables/enforcement、field redaction、API audit middleware は見つかりませんでした。

### 10.2 Required machine-principal boundary

- external IdP issues OAuth/OIDC machine credential; KIM stores no client secret/private key authority
- audience、issuer、subject/client identity、expiry、scopeを検証
- Project/Site/resource/action ownershipとread/write/admin separation
- destructive actionはadditional permission、delete protection、optional approval/break-glass audit
- token rotation/revocation時もstable audit actorを保持
- request ID、Idempotency-Key、resource revision、Operation IDをaudit相関
- sensitive/computed physical detailsをroleとtenant boundaryでredact
- Provider trust boundaryはNorthbound HTTPS APIまで

Provider は PostgreSQL、internal Bus、Agent endpoint、OVN/OVS/FRR、libvirt、LVM credentialを取得しません。

## 11. Machine-readable Resource Contract

### 11.1 Responsibility split

```text
OpenAPI 3.1
  = HTTP paths, methods, request/response shapes, validation, auth schemes,
    status codes, pagination and Problem Details schemas

KIM lifecycle semantic metadata
  = resource type/identity, desired/computed/immutable/sensitive fields,
    replacement triggers, revision, import grammar, async operation,
    delete protection and terminal states
```

Lifecycle metadata may later use reviewed OpenAPI extensions or a versioned companion schema. This review does not select or implement an arbitrary extension format. Whichever representation is chosen must be one versioned contract package with compatibility tests and reject contradictions between HTTP schema and lifecycle metadata.

### 11.2 Minimum resource descriptor

```yaml
resourceType: <stable-type>
identityField: <logical-id>
revisionField: <etag-source>
scope: <system|site|project>
fields:
  <name>:
    class: <required_desired|optional_desired|computed|immutable|sensitive|operation_only|internal_only>
    replacementTrigger: <true|false>
create:
  idempotent: true
  asynchronous: true
  terminalSuccess: []
  terminalFailure: []
import:
  identifierFormat: <stable-logical-format>
delete:
  protection: <supported|required|none>
  dependencyPolicy: <conflict|explicit-cascade>
  asynchronous: true
```

This is an illustrative shape, not an adopted file format.

## 12. Security Policy / ACL Gap

Current schema has no first-class Security Policy/Rule、Port Group、Address Set authority or compiler. ADR-0020/requirements describe security intent, but no producer/read projection/consumer/test current chain exists.

Proposed public desired model requires stable policy/rule identity and revision plus:

- source/destination logical selector
- direction
- protocol and named service/port range
- statefulness
- allow/deny/reject action
- priority/order conflict semantics
- logging policy
- attachment/membership target and Project scope

Initial realization may compile to OVN ACL、Port Group、Address Set with compiler revision、input digest、generated identity、apply/read-back evidence. Raw OVN match/action syntax、OVN UUID、logical flow expression are backend details and not normal Terraform/UI desired fields. Hardware offload/external service remain proposed and cannot silently weaken semantics.

## 13. Terraform Readiness Matrix

| Capability | Classification | Severity | Provider disposition |
|---|---|---:|---|
| Northbound API runtime | `IMPLEMENTED_PROJECT_SLICE` | P0 closed for Project | reusable runtime; production deployment profile remains |
| OpenAPI + lifecycle metadata | `IMPLEMENTED_MULTI_RESOURCE` | P1 closed for Project/Flavor | extend per resource; keep contract tests |
| OIDC machine principal/RBAC | `IMPLEMENTED_MINIMAL_MODEL` | P0 closed for Project | extend tenant/site/policy model before broader resources |
| public idempotency + Operation create | `PARTIAL` | Project/Flavor idempotency PASS; Operation gap P1 | synchronous logical create allowed; backend resources blocked |
| ETag/If-Match | `IMPLEMENTED_MULTI_RESOURCE` | P1 closed for Project/Flavor | extend per mutable resource |
| desired/computed DTO separation | `API_SEMANTIC_GAP` | P0 | block VM/Volume/Port schema |
| common error/retry contract | `IMPLEMENTED_MULTI_RESOURCE` | P1 closed for Project/Flavor | backend UNKNOWN/Operation errors remain |
| Project/Site scope | Project `IMPLEMENTED`; Site `RESOURCE_MODEL_GAP` | Project P1 closed | Project experimental candidate; Site later |
| Flavor | `TERRAFORM_READY_EXPERIMENTAL` | Phase 1 contract PASS | Provider implementation may begin; release acceptance pending |
| Image | `BLOCKED` | artifact authority gap | separate ingestion/read-back authority first |
| Availability Policy | `BLOCKED` | public resource-model gap | scope and typed child-policy/dependency contract first |
| HostGroup/Placement Scope | `API_SEMANTIC_GAP` | P1/P2 | admin phase after auth/redaction |
| Network/Subnet | `API_SEMANTIC_GAP` | P1 | separate public resources before Provider |
| Port | `RESOURCE_MODEL_GAP` | P0/P1 | decouple logical Port from Admission/Binding |
| VM | `API_SEMANTIC_GAP` | P0/P1 | aggregate API + no-drift contract required |
| Volume/Attachment | `RESOURCE_MODEL_GAP` | P0/P1 | logical lifecycle vs physical Binding split |
| Availability Policy | `API_SEMANTIC_GAP` | P1 | typed public schema required |
| Security/Datapath/Router/FRR | `RESOURCE_MODEL_GAP` | P1/P2 | not MVP |
| Recovery/EVACUATE/Cleanup | `OPERATION_ONLY` | P1 public gap | polling/status only; no managed resource |

Project and Flavor are qualified experimental Provider candidates, not production-ready Provider releases. Image、Availability Policy and all backend-bearing resources remain blocked or semantic gaps.

## 14. P0 / P1 / P2 / P3 Findings

### P0 — authority/destructive ambiguity

1. Northbound runtime exists for Project and Flavor only; a Provider for any other resource would still bypass or invent an unimplemented contract.
2. Project excludes physical fields; no public desired/computed schema yet prevents Host/BDF/LV/binding/generation leakage for backend resources.
3. VM create/delete has no aggregate public Operation contract; client-side orchestration would steal Placement/Materialization/Cleanup authority.
4. Minimal Project/system RBAC exists; tenant/site/attribute policy and wider resource enforcement remain absent.
5. Port and Attachment current models are tightly bound to Admission/physical incarnation and cannot be exposed directly as portable desired resources.

### P1 — Provider blockers

1. OpenAPI/lifecycle metadata、revision、ETag/If-MatchはProject/Flavorで実装。
2. public Idempotency-KeyはProject/Flavorで実装、common Operation APIは未実装。
3. Project/Flavor Read/List/cursor/tombstone/dependency/delete protectionは実装、formal Provider import acceptanceは未実装。
4. Problem Detailsはcommon HTTPで実装、backend UNKNOWN taxonomyはOperation実装待ち。
5. Project first-class authorityは実装、Site/tenant hierarchyは未実装。
6. Image/Network/VM/Volume/Availability public resource-specific lifecycle contracts absent; Flavor is implemented.
7. Security Policy logical model/compiler absent if included beyond MVP.

### P2 — quality/usability blockers

1. Site、Router、Floating IP、Resilience、Datapath Profile/FRR resource models absent.
2. operation history/audit chain lacks one public correlated view.
3. eventual consistency freshness、event/long-poll、Provider timeout defaults未定義.
4. canonical unit/null/set ordering and UI/Terraform export equivalence need contract fixtures.

### P3 — documentation/naming debt

1. revision/generation/incarnation/attempt naming remains subsystem-specific.
2. public logical DTO vs internal Placement DTO package boundary is not named/implemented.
3. resource capability-to-schema/producer/consumer/API/test inventory is manual.

## 15. Terraform Provider MVP Recommendation

### 15.1 MVP rule

MVP must follow public API maturity, not internal schema breadth. Project+Flavor are the qualified logical-resource contracts; Provider work must remain limited to scaffold/conformance and these experimental resources.

### Phase 0 — Project reference contract (complete)

- Project scope/auth model and first-class authority
- common Resource Contract/OpenAPI/error/idempotency/revision semantics
- Project CRUD/list vertical slice and machine-principal authentication/audit
- executable HTTP/PostgreSQL/OpenAPI contract tests
- Operation semantics deliberately deferred because Project has no backend convergence

### Phase 1 — catalog and low-coupling logical resources

Candidate order after contract implementation:

1. Project experimental Provider resource and import conformance
2. Flavor experimental Provider resource and import conformance
3. Image only after artifact ingestion/read-back separation
4. Availability Policy only after typed public policy DTO、scope and dependency references

These have stable revision/evidence patterns and fewer physical incarnation fields. Image binary upload may remain a separate Operation.

### Phase 2 — logical network/storage resources

1. Network
2. Subnet
3. logical Port
4. Volume

This phase requires Network/Subnet public separation, Port/Binding separation, standalone logical Volume lifecycle, delete/dependency contracts, and IP allocation semantics. Router/Floating IP are added only after implementation.

### Phase 3 — VM aggregate and advanced profiles

1. Virtual Machine aggregate using Project/Image/Flavor/Network/Port/Volume/Availability references
2. HostGroup/Placement Scope administrative resources
3. Datapath Profile、Security Policy、Router/Network Service after their authority implementations

VM is not Phase 1 merely because its internal lifecycle is mature. It has the highest risk of leaking physical incarnation and client-side orchestration.

Recovery/EVACUATE/Drain/Cleanup remain administrative Operation APIs in all phases. OVS-DPDK、FRR、Security Policy、SR-IOV profile management are not current MVP resources.

## 16. Required Work Before Provider Implementation

Minimum blocker set, in order:

1. **P0 boundary (Project complete):** ADR-0029 and the public service/persistence boundary forbid Provider DB/internal/Agent/backend access.
2. **P1 schema SSOT (Project complete):** OpenAPI 3.1 + lifecycle semantic metadata classify the Project contract.
3. **P1 security (minimal Project complete):** OIDC HUMAN/AUTOMATION、Project/system RBAC and immutable audit are enforced; broader tenant/site policy remains.
4. **P1 common lifecycle (Project partial):** Idempotency-Key、revision/ETag、Problem Details、cursor list、tombstone/dependency/delete protection are complete; Operation read/poll is not applicable to Project and remains required for backend resources.
5. **P1 vertical slice (Project complete):** schema + producer + public Read/List/Create/Update/Delete + authorization + contract tests are qualified.
6. **P0 no-drift slice:** before VM Provider resource, implement public `spec/status/links` projection and Recovery A→B no-drift contract test.

The Project/Flavor-applicable portions of items 1–5 are complete, so Provider scaffold and experimental Project/Flavor resources may begin. Image/Availability and no Phase 2 resource are authorized. VM waits for unified Operation plus aggregate async create/delete and no-drift contracts.

## 17. Proposed Implementation Order

```text
Resource inventory and field classification
→ API/Resource Contract ADR decision
→ OpenAPI + lifecycle metadata SSOT
→ OIDC machine principal / RBAC / audit
→ common HTTP idempotency, revision, error, Operation, list contracts
→ read-only projections
→ Flavor or Project vertical slice
→ Provider conformance harness
→ catalog/policy resources
→ Network/Subnet/Port/Volume logical separation
→ VM aggregate and Recovery no-drift
→ advanced Security/Datapath/Router resources
```

## 18. Completion / Roadmap Impact

The current architecture qualification inventory denominator remains unchanged. Its 35 in-scope rows measure infrastructure authority/backend capabilities; this review evaluates the cross-cutting Northbound delivery surface and does not add or qualify a backend capability row. No Functional or Production score is raised.

This re-review adds `IAC-015`/`INV-API-010`/`AT-IAC-012` and qualifies twelve Northbound Phase 0 gates for Project. The existing 35-row inventory measures infrastructure authority/backend capabilities, so its denominator and scores remain `31.5/35 = 90.0%` architecture、`30/35 = 85.7%` functional、`17.5/35 = 50.0%` production. Project is a cross-cutting delivery surface and does not silently qualify any backend row.

## 19. Final Decision

**May implementation of `terraform-provider-kim` begin? Conditional for scaffold + Project + Flavor only.**

Allowed now:

- Provider architecture/repository/scaffold
- experimental Project and Flavor resources against the committed OpenAPI contract
- Project/Flavor import、refresh、response-loss conformance work

Blocked now:

- shipping Project as production-ready before Provider acceptance/import tests
- implementing Provider-managed KIM resources other than Project/Flavor
- direct PostgreSQL/internal function/Agent/backend integration from Provider
- exposing current physical projection tables as Terraform desired schema
- modeling Recovery/EVACUATE/Cleanup as Terraform resources

次の re-review condition: experimental Project ProviderのCRUD/import/refresh/response-loss acceptance、または最初のbackend-convergent resourceのunified Operation vertical slice。

## 20. Related Evidence

- [Infrastructure Lifecycle and IaC Architecture](../infrastructure-lifecycle-iac-architecture.md)
- [API Design Principles](../api-principles.md)
- [System Architecture](../architecture.md)
- [Domain Model](../domain-model.md)
- [Responsibility Boundaries](../responsibility-boundaries.md)
- [Network and Dataplane Target Architecture](../network-dataplane-target-architecture.md)
- [Network Resource Architecture](../network-resource-architecture.md)
- [Security Architecture](../security.md)
- [Architecture and Qualification Inventory Review](kim-architecture-qualification-inventory-20260813.md)
- [Requirements](../requirements.md)
- [Architecture Invariants](../architecture-invariants.md)
- [Acceptance Test Catalog](../acceptance-test-catalog.md)
- [Traceability Matrix](../traceability-matrix.md)
