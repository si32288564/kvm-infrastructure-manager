# kvm-topology v1 to KIM Gap Analysis

- 状態: Initial Baseline
- 更新日: 2026-08-09
- v1 source: `/Volumes/Data/Nextcloud/30 プロジェクト/10 進行中/kvm-topology`
- v1 commit: `c481388` (`Freeze v1 release boundary`)
- KIM comparison baseline: Phase 0 candidate baseline and Accepted ADR-0001〜0023

## 1. Scope and Method

v1 の自己申告文書だけでなく、`internal/*`、command、PostgreSQL migration、versioned JSON schema、Go test、E2E script、systemd/Debian packaging を確認しました。

- Go source: 579 files
- Go test: 262 files
- PostgreSQL migration: 47
- versioned JSON schema: 70

分類の意味は次のとおりです。

| Classification | Meaning |
|---|---|
| Existing | candidate baseline の主要 semantics が v1 に存在する。無変更再利用を意味しない |
| Reusable | KIM contract への adaptation を前提に、実装資産を明確に再利用できる |
| Partial | 一部の semantics/implementation はあるが、authority または product scope が不足する |
| Missing | 対応する第一級 resource/authority/flow が存在しない |
| Conflicting | v1 の authority/boundary を KIM へそのまま継承してはいけない |

Proposed ADR は調査開始時の比較基準としてのみ使用し、irreversible implementation authority としては扱っていません。本分析を確定した時点で ADR-0001〜0023 は個別 review 後に Accepted されています。

## 2. Initial Result

| Classification | Count |
|---|---:|
| Existing | 3 |
| Reusable | 4 |
| Partial | 18 |
| Missing | 6 |
| Conflicting | 2 |
| Total | 33 |

## 3. Gap Register

| ID | Class | v1 evidence / Gap | Target phase | Dependency | Migration / Reuse / Discard decision | Trace |
|---|---|---|---|---|---|---|
| GAP-V1-001 | Existing | official Go libvirt binding、sysfs/procfs/netlink/OVSDB/LVM、typed XML mutation。KVM/QEMU/libvirt patch/fork なし | Phase 0 onward | KVM neutrality contract | adapter 境界へ移し原則と実装を保持 | HST-012〜014、INV-AGT-008/009、AT-AGT-008/009 |
| GAP-V1-002 | Reusable | long-lived Go daemon、outbound mTLS、watch loop、durable journal、single binary/systemd | Phase 1 | Agent Protocol、OS Adapter | `cmd/kvm-topology-agent` と collector/publisher/hostcommand/watch を rename/refactor して再利用 | HST-001/002/008、INV-AGT-003/010、INV-EXEC-002、AT-HST-001/002、AT-AGT-010 |
| GAP-V1-003 | Partial | Linux standard interface 中心だが Ubuntu 26.04 amd64、Debian package、systemd、固定 path/group 前提 | Phase 1 | GAP-V1-002、support matrix | Ubuntu adapter を初期実装として再利用し、OS/package profile を分離 | HST-007〜011、INV-AGT-006/007、AT-AGT-006/007 |
| GAP-V1-004 | Partial | CPU/NUMA/HugePages/PCI/IOMMU/SR-IOV/network/OVS/block/LVM/libvirt inventory と generation snapshot | Phase 1 | KIM domain schema | collector/normalizer/relation builder を再利用。v1 snapshot を直接 KIM authority にしない | HST-002/004/006/008、INV-DATA-002、AT-HST-002 |
| GAP-V1-005 | Reusable | fsync/atomic rename generation、durable spool、at-least-once、digest ACK、retry/backpressure/quarantine | Phase 1 | Agent Gateway、Inbox/Outbox | spool/envelope/idempotency primitive を Gateway protocol/Inbox Receipt へ適合 | OPS-003/006/009/010、INV-DATA-006、AT-DATA-006 |
| GAP-V1-006 | Partial | machine-id を Host ID authority とし、SMBIOS fields は収集。TPM/BMC/CMDB/confidence/conflict model なし | Phase 1 | Enrollment Policy | collector を evidence source として再利用し、machine-id は candidate evidence へ降格 | HLC-016/019、INV-HLC-011/013/014、AT-HLC-016〜018 |
| GAP-V1-007 | Conflicting | Agent が Controller Inventory/Command endpoint へ直接 mTLS。独立 Agent Gateway/arming boundary なし | Phase 1 | Gateway service、PKI/Profile | client semantics を再利用し Gateway へ接続。direct endpoint は期限付き compatibility bridge 後に廃止 | NFR-SEC-001〜005、INV-AGT-002、AT-AGT-002 |
| GAP-V1-008 | Partial | Job/Command/Attempt/Lease/Result、transaction、`SKIP LOCKED`、verification は実装済み。第一級 Operation/DAG/evidence が不足 | Phase 1 | KIM Operation schema、GAP-V1-005/007 | locking/state/idempotency を再利用し、v1 Job table を公開 authority にせず explicit import | OPS-001〜010、INV-EXEC-001〜008、AT-EXEC-001〜007 |
| GAP-V1-009 | Existing | expiry 後 old Attempt は `outcome_unknown`、stale first Result 拒否、accepted Result 再送 receipt、Inventory verification | Phase 1 | GAP-V1-008 | semantics と E2E fixture を再利用し KIM Test ID へ map | OPS-009/010、INV-EXEC-004/005/007、AT-EXEC-004/005/007 |
| GAP-V1-010 | Existing | explicit arm/disarm generation。reconnect/certificate renewal だけで arm せず old authority を fence | Phase 1 | Enrollment/Baseline gate | generation model を HostAuthority へ拡張し trust/compliance generation を追加 | HLC-002/010/017、INV-HLC-001/005/008、AT-HLC-002/008/012 |
| GAP-V1-011 | Partial | one-time bootstrap、CSR/issuance/approval、renewal/revocation/CA rotation は存在。TrustBundle/Profile/Binding/compromise/DR 不足 | Phase 1; Phase 3 full | GAP-V1-006/010、Secret Provider | crypto/enrollment client と test を再利用し固定 CA/SAN を KIM profile へ移行 | PKI-001〜032、INV-PKI-001〜025、AT/FI-PKI catalog |
| GAP-V1-012 | Partial | Host CPU Policy/placement readiness はあるが Profile/Baseline/Control/Evaluator/rollout/drift/remediation なし | Phase 1; Phase 2 | GAP-V1-004/006/010 | CPU isolation checks を Baseline Control へ再利用。VM Compliance table は別 context のまま保持 | HLC-004〜015/020〜022、INV-HLC-002〜018、AT/FI-HLC catalog |
| GAP-V1-013 | Partial | OIDC UI session、operator delegation、mTLS grant、Host action policy は存在。Tenant/Project/RBAC/Quota ledger なし | Phase 2 | external Identity、domain schema | OIDC/audit primitive を再利用。独自 operator authority は正本にしない | IAM-001〜006、INV-AUTH-001/002、AT-AUTH-001〜004、AT-QUOTA-001 |
| GAP-V1-014 | Partial | 70 schema、idempotency、optimistic revision、typed error は有用。Host-centric API で KIM project resource/Operation API と非互換 | Phase 1 | IAM、KIM API | validation/error/idempotency utility を再利用。v1 API は期限付き bridge 後に廃止 | CMP-001〜003/008、INV-API-001〜003、AT-API-001/002 |
| GAP-V1-015 | Missing | Image catalog/cache/checksum/signature、Flavor なし | Phase 1 | Storage、Artifact Trust | 新規実装。qcow2 E2E は disk adapter fixture に限定再利用 | IMG-001〜003、FLV-001/002、AT-IMG-001/002、AT-FLV-001 |
| GAP-V1-016 | Missing | existing Domain の observe/adopt/start/reconfigure のみ。VM create/delete/boot-from-image なし | Phase 1 | GAP-V1-014/015/017/021/022 | existing VM adoption は import path に再利用。create/delete pipeline は新規 | CMP-001〜003、INV-API-001〜003、INV-EXEC-006、AT-CMP-001 |
| GAP-V1-017 | Partial | single Host 内 CPU/HugePage/storage/MAC claim transaction は存在。fleet eligibility/scoring/all-domain Final Admission なし | Phase 1 | HostGroup、Network、Storage | SQL claim/check を domain admission へ分解して再利用。simulation は authority にしない | SCH-001〜007、INV-PLC-001〜007、AT-PLC-001〜009 |
| GAP-V1-018 | Missing | typed HostGroup、materialized membership、hierarchy/snapshot/policy resolution なし | Phase 1; Phase 2 selector | IAM、Placement、Host Lifecycle | 新規実装。v1 Host ID/trait は membership input へ移行 | HGR-001〜017、INV-HGR-001〜014、AT/FI-HGR catalog |
| GAP-V1-019 | Missing | AvailabilityPolicy/Binding/FailureEpoch/fencing/recovery なし。live migration/Host HA を明示除外 | Phase 1 model; Phase 2 flow; Phase 3 recovery | HostGroup、Placement、Storage/Network fencing | 新規実装。Execution/fault fixture だけ再利用 | AVR-001〜016、INV-AVR-001〜013、AT/FI-AVR catalog |
| GAP-V1-020 | Missing | NFVO intent、DomainClaim、FailureCampaign、RecoveryQueue/BudgetLease/Consumption なし | Phase 1 model; Phase 2 constraints; Phase 3 policy | IAM、HostGroup、Availability | 新規実装 | WRI-001〜015、RCV-001〜015、INV-WRI/RCV、AT/FI-WRI/RCV |
| GAP-V1-021 | Partial | Linux/OVS/SR-IOV inventory と stopped-VM vNIC attach/MAC reservation。IPAM/VLAN/VNI/OVN/L3/Security/BindingHandoff なし | Phase 1 VLAN/IPAM; Phase 2 OVN/L3 | Placement、IAM、Gateway | collector/libvirt NIC adapter を再利用。reservations は KIM Claim へ explicit migration | NET-001〜035、INV-NET-001〜020、AT/FI/XCT-NET catalog |
| GAP-V1-022 | Partial | file/block attach、Host-local reservation、LVM topology。Volume lifecycle/Ceph fencing/Attachment generation/handoff なし | Phase 1 LVM; Phase 2 Ceph | Placement、Secret、Availability | disk/LVM collector を再利用。reservation は import input であり single-writer authority にしない | STO-001〜027、INV-STO-001〜019、AT/FI/XCT-STO catalog |
| GAP-V1-023 | Partial | PF/VF/IOMMU/hostdev inventory と fixture は存在。VF lifecycle/PCI allocation/final admission と実機認定なし | Phase 3 | Dataplane、Placement、hardware | collector/fixture を再利用。allocation/typed operation は新規 | CMP-007、NET-006/029/030、INV-NET-015、AT-NET-006/020 |
| GAP-V1-024 | Missing | PMD/service lcore、DPDK memory、Port/RxQ、vhost queue authority/claim/compliance なし | Phase 1 discovery; Phase 2 admission; Phase 3 operation | CPU/NUMA/HugePage、Network | OVS reader/CPU/PCI collector だけ再利用し resource module は新規 | DPL-001〜015、INV-DPL-001〜010、AT/FI/XCT-DPL catalog |
| GAP-V1-025 | Partial | current rows、immutable Job event/Attempt、Inventory/Audit outbox、retention worker。generic Inbox/data class/partition/archive/PITR なし | Phase 1; Phase 2 PITR | all domain schema | transaction/outbox pattern を再利用。fresh KIM schema + explicit v1 import tool を採用 | DAT-001〜021、INV-DATA-001〜021、AT/FI-DATA catalog |
| GAP-V1-026 | Partial | PostgreSQL failover/process crash E2E はあるが production HA fencing/backup/restore は未検証 | Phase 2 | GAP-V1-025、external fencing | failover fixture/locking test を再利用。DR authority は新規 | NFR-AVL-001〜006、INV-HA-001、INV-DR-001、FI-DB-001/FI-DR-001 |
| GAP-V1-027 | Partial | signed config rollout、Kubernetes cutover、Agent package rollback、schema replay。ReleaseManifest/FeatureGate/DAG なし | Phase 1; Phase 3 | Data migration、HostGroup、Artifact Signing | rollout/journal/canary primitive を UpgradeCampaign へ移行 | UPG-001〜027、INV-UPG-001〜021、AT/FI-UPG catalog |
| GAP-V1-028 | Partial | Lease/deadline/retry は実装するが clock taxonomy/health/uncertainty/monotonic conversion/boot fencing なし | Phase 1; Phase 3 faults | Gateway、Execution、PKI、DB | Lease code を DB authority time へ統一し wall-clock deadline を monotonic conversion へ変更 | TIM-001〜031、INV-TIM-001〜023、AT/FI-TIM catalog |
| GAP-V1-029 | Partial | compile-time Collector/Operation Registry、closed typed payload、narrow backend。C0〜C3/lifecycle/certification なし | Phase 1 | Release Manifest、Security | Agent module を再利用し descriptor/trust class/conformance metadata を追加 | NFR-EXT-001〜006、INV-EXT-001〜006、XCT catalog |
| GAP-V1-030 | Partial | Prometheus/alert/runbook、redacted health、API audit outbox。OpenTelemetry/product alarm/tamper evidence/diagnostic bundle 不足 | Phase 1 onward | IAM、Outbox、Event schema | metrics/redaction/audit delivery を再利用し全 domain へ拡張 | O11Y-001〜003、AUD-001/002、INV-SEC-002、AT-O11Y/AUD |
| GAP-V1-031 | Reusable | Debian package、systemd hardening、reproducible build、offline APT/OpenPGP、upgrade/rollback test。Ubuntu amd64 のみ | Phase 1; Phase 4 qualify | OS Adapter、ReleaseManifest、SBOM | Debian/Ubuntu lane を再利用し他 distribution と KIM manifest を追加 | NFR-OPS-001〜012、INV-UPG-001/016/017、AT-OFFLINE-001 |
| GAP-V1-032 | Conflicting | migration 017〜021 の `executor_credential_*` authority は現 v1 single Agent identity と KIM credential≠authority に矛盾 | Phase 1 import | GAP-V1-007/010/011、Retention | active authority として破棄。必要な履歴だけ immutable archive evidence として import | HLC-017、PKI-010/017〜019、INV-HLC-008、INV-PKI-006/016 |
| GAP-V1-033 | Reusable | 262 Go test、PostgreSQL E2E、crash/partition/result-loss/expiry/concurrency/failover/PKI/real-KVM script | Phase 1 onward | all gaps、CI traceability | fixture を移植し KIM AT/FI/XCT ID へ map。v1 pass を certification evidence として自動継承しない | OPS/DAT/PKI/UPG Requirements、関連 INV/AT/FI/XCT |

## 4. Reuse Priorities

1. Go Agent Core: collector、publisher、hostcommand、watch
2. durable snapshot spool、digest ACK、idempotency primitives
3. PostgreSQL Job/Command/Lease/Attempt、stale Result fencing、observation verification
4. explicit Host authority generation
5. typed libvirt CPU/NUMA/HugePage/disk/NIC adapter
6. enrollment、renewal、revocation、CA rotation primitives
7. transaction、outbox、locking pattern
8. crash/partition/result-loss/concurrency/PostgreSQL failover/real-KVM fixture
9. Debian/systemd hardening、reproducible package pipeline

## 5. Migration Policy

- `Existing` は semantic match であり、namespace、ID、protocol、schema を無変更で採用する意味ではありません。
- 47本の v1 migration を KIM core schema へ連続適用しません。fresh KIM baseline schema と explicit import tool を使用します。
- import tool は v1 stable identity、digest、generation、history を入力 evidence として検証し、KIM authority row を新しい transaction で作成します。
- backend/Host observation または legacy table を理由に自動 adopt しません。
- `executor_credential_*` は current authority へ merge せず、必要な history だけを archive evidence として保持します。
- repository 内 compiled artifact は再利用せず、source から Release Manifest 付きで rebuild します。

## 6. Recommended Implementation Order

1. Phase 1-A: Agent Gateway、Inventory、OS Adapter
2. Phase 1-B: Operation/Execution、Host Authority
3. Phase 1-C: API、Image、Flavor、VM create/delete
4. Phase 1-D: Placement、HostGroup、VLAN/IPAM、Local LVM
5. Phase 2: Tenancy、OVN、Ceph、HA/PITR、policy-based Host Lifecycle
6. Phase 3: SR-IOV、OVS-DPDK、managed recovery、rolling upgrade

## 7. Evidence Limitation

v1 source/test assets は inspected ですが、この analysis では `make release-candidate-check` を実行していません。`v1.0.0-rc1` annotated tag も未作成です。したがって v1 test pass を KIM certification evidence として扱わず、移植後に KIM Test ID、Release Manifest、target environment へ bind して再実行します。
