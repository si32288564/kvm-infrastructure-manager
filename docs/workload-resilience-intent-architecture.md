# Workload Resilience Intent Architecture

- 状態: Baseline
- 更新日: 2026-08-09

## 1. 目的

NFVO/VNFM/Tenantが要求するactive/standby等の分離意図を、KIMのversioned Failure Domain constraintとtransactional Placement claimへ変換します。KIMはVNF role/lifecycleを所有せず、指定されたmember集合のinfrastructure placement invariantsだけを所有します。

```text
NFVO/VNFM Resilience Intent
  -> Northbound Adapter / Public API
  -> WorkloadResilienceGroup + Member Slots
  -> Failure Domain Constraints
  -> Placement Snapshot
  -> Transactional Domain Occupancy Claim
  -> VM Allocation
```

## 2. 基本原則

1. VNF active/standby semanticsをKIM内部lifecycleへ取り込まない。
2. NFVO/VNFM role名はopaque metadataで、KIMはmember/constraintだけを評価する。
3. raw rack/power Group IDではなく公開Failure Domain classのdimension/levelをAPIで指定する。
4. hard constraintをscoreへ降格せずEligibility/Final Admissionで強制する。
5. rackとpower-path等の異なるdimensionを独立に評価する。
6. concurrent member placementはPostgreSQLのDomain Occupancy Claimで競合制御する。
7. insufficient/UNKNOWN domain evidenceを独立domainとして楽観評価しない。
8. intent/membership変更だけで既存VMを暗黙migration/restartしない。
9. WORKLOAD_MANAGEDでもinitial/replacement placement constraintsはKIMが守る。
10. Northbound adapterはCore authorization、Project ownership、idempotency、admissionを迂回しない。

## 3. Resource Model

```text
WorkloadResilienceGroup
├─ resilience_group_id / project_id
├─ name / external_correlation
├─ generation / policy_digest
├─ lifecycle_state
├─ member_slot_policy
├─ required_member_count / completion_policy
├─ constraints[]
├─ exposure / owner
└─ created_by / audit

ResilienceMemberSlot
├─ member_key
├─ opaque_role
├─ vm_id / allocation_id
├─ membership_generation
├─ state
└─ external_reference / audit

FailureDomainConstraint
├─ constraint_id / version
├─ dimension / level
├─ member_selector
├─ enforcement
├─ max_members_per_domain
├─ min_distinct_domains
├─ applicability
└─ reason/explanation policy

ResilienceDomainClaim
├─ resilience_group_id / constraint_id
├─ member_key / vm_id / allocation_id
├─ failure_domain_group_id / hierarchy_generation
├─ placement_decision_id
└─ claim_generation / state
```

`opaque_role`は`active`、`standby`等を保持できますが、KIMはrole transitionやapplication healthを判断しません。

## 4. Constraint Semantics

初期constraint typeは`DISTINCT_FAILURE_DOMAIN`です。

例:

```text
constraint rack-separation:
  dimension = physical-location
  level = rack
  members = [vnf-a-active, vnf-a-standby]
  enforcement = HARD
  max_members_per_domain = 1
  min_distinct_domains = 2

constraint power-separation:
  dimension = power-path
  level = feed
  members = [vnf-a-active, vnf-a-standby]
  enforcement = HARD
  max_members_per_domain = 1
  min_distinct_domains = 2
```

`HARD`はeligibilityを決定し、silent relaxationしません。`SOFT`を将来提供する場合も別constraintとして明示し、hard failure時のfallbackには使用しません。

member selectorはmember key/opaque role集合を参照できます。role名の意味はcallerが所有します。KIMは選択されたmember集合に同じ数学的constraintを適用するだけです。

Group complianceは`PENDING / COMPLIANT / VIOLATED / UNKNOWN`を持ちます。required member slotsが揃う前は`PENDING`であり、最初のmemberが一domainしか占有していないことを`min_distinct_domains`違反とはしません。一方、`max_members_per_domain`は各admissionで増分強制し、将来のhard separationを破るclaimをcommitしません。required slotsが揃った時点で`min_distinct_domains`を最終評価します。

completion deadline/policyを設定した場合、期限超過したincomplete Groupを`VIOLATED`またはaction-requiredとして通知しますが、既存memberを暗黙削除しません。複数VMのall-or-nothing作成を必要とする場合は別のPlacement Set transaction contractを必要とし、本Group resourceだけでは分散rollbackを仮定しません。

## 5. Northbound Contract

NFVO/VNFMは次の順で使用します。

1. Project scopeでWorkloadResilienceGroupとconstraintを作る。
2. stable member key/opaque roleを予約する。
3. VM create/replacement requestからmember keyを参照する。
4. KIMがProject ownership、slot uniqueness、constraint generationを検証する。
5. Operation/Eventでbindingとviolationを追跡する。

Northbound APIは公開Failure Domain class（例:`rack`、`power-feed`）を受け、内部HostGroup ID/topologyを返しません。Northbound adapterはETSI/NFVO固有modelをCore resourceへmapしますが、DB/Placementへ直接writeしません。

同じexternal correlation/idempotency keyと同じpayloadは同じGroup/member/Operationへ収束し、異なるpayloadはconflictです。

## 6. Placement and Transactional Claims

Placement Request Snapshotは次を含みます。

- Resilience Group/member/constraint generation
- existing member VM/AllocationとDomain Claims
- candidate HostのFailure Domain path/hierarchy generation
- requested Placement Scope/Availability Binding context

Dry Eligibilityはcandidateごとに各constraintを評価します。

```text
candidate rack claim
  + committed/pending group rack claims
  -> max_members_per_domain / min_distinct_domains

candidate power-feed claim
  + committed/pending group feed claims
  -> independent evaluation
```

Final AdmissionはHost/resource claimsと同じPostgreSQL transactionで次を行います。

1. Resilience Group/member/constraint generationをlock/再検証する。
2. relevant Domain Claim rowsとFailure Domain hierarchy generationをlock/検証する。
3. hard constraintをlatest stateへ再適用する。
4. ResilienceDomainClaim、VM Allocation、Availability Binding、全resource claimsを不可分commitする。

active/standbyを並行作成しても同じrack/feedへのclaimは一方だけがcommitできます。競合側は残候補reselectionへ戻り、constraintを満たす候補がなければbounded failureになります。

Group capacityと同様、Domain Claimは物理capacityを所有しません。配置関係の占有事実であり、CPU/memory/device reservationは既存Host resource authorityが所有します。

## 7. Member Lifecycle and Replacement

```text
RESERVED -> BINDING -> BOUND -> RELEASING -> RELEASED
                         \-> VIOLATED
                         \-> UNKNOWN
```

- member slotへ同時に複数active VMをbindしない。
- VM delete完了とAllocation/attachment解放を確認後にDomain Claimをreleaseする。
- WORKLOAD_MANAGED replacementはNFVO/VNFMの明示requestで新VMを同じmember slotへrebindする。
- old VM/source ownershipがUNKNOWNならslot/claimを再利用しない。
- replacementもcurrent Failure Domain/Placement/Availability constraintsを再評価する。

## 8. Drift and Reconciliation

HostGroup failure-domain membership/hierarchy変更で既存VMがconstraint違反になった場合:

- current claim/historyを書き換えない。
- Resilience Group/Memberを`VIOLATED`またはevidence不明なら`UNKNOWN`にする。
- Fault/Eventとaffected member/constraintのbounded explanationを通知する。
- WORKLOAD_MANAGEDではKIMから自動migration/replacementしない。
- INFRASTRUCTURE_MANAGED recoveryでもconstraintをsilent relaxしない。

operator/NFVOは明示migration、member replacement、constraint revisionを選択します。Constraint revisionは新generationで、過去Placement decisionを改変しません。

## 9. Availability Responsibility Integration

`WorkloadResilienceGroup`はAvailability responsibilityを上書きしません。

- `WORKLOAD_MANAGED`: KIMはinitial/replacement placementとdrift検出を所有し、service failover/replacement intentはNFVO/VNFMが所有。
- `INFRASTRUCTURE_MANAGED`: KIM Recovery Placementも同じResilience Domain Claimsを守る。候補不足ならBLOCKED。
- `MANUAL`: operator Recovery Decision後も同じconstraintを守る。

Availability Binding、member slot、Resilience constraintが不整合/UNKNOWNならrecovery/placementを停止します。

## 10. Security and Tenancy

- WorkloadResilienceGroupはProject-owned resourceで、member VMも同じProjectに限定する。
- NFVO/VNFM Service Principalへscoped create/bind/read permissionを付与する。
- Tenantへ内部HostGroup/failure topologyを公開せず、公開dimension classとcompliance resultだけを返す。
- 他Project member/domain occupancyをexplanationやmetricsへ漏らさない。
- opaque role/external referenceをauthority、authorization、application healthとして使用しない。

## 11. Failure Semantics

- concurrent same-domain claim:一方だけcommit、他方はreselection。
- stale constraint/member/hierarchy generation:Final Admissionを拒否して再評価。
- insufficient distinct domains:hard failure、constraint relaxationなし。
- domain evidence UNKNOWN:candidate ineligible、独立domainとして数えない。
- member replacement source UNKNOWN:slot/claim reuse停止。
- adapter response loss:idempotencyで同じGroup/member/Operationを回収。
- hierarchy drift:existing workload維持、VIOLATED/UNKNOWN event、暗黙migrationなし。
- Group delete with members/claims:delete拒否、DRAINING維持。

## 12. API Resources

```text
/api/v1/workload-resilience-groups
/api/v1/workload-resilience-groups/{id}/members
/api/v1/workload-resilience-groups/{id}/constraints
/api/v1/workload-resilience-groups/{id}/domain-claims
/api/v1/failure-domain-classes
```

すべてのmutationはETag/If-Match、Idempotency-Key、Operation、Project Authorization、Audit contractに従います。
