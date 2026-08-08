# Extension Conformance Contract

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

Extensionの種類と信頼レベルに応じた実行境界を定義し、Core invariantsへの準拠を共通test contractで検証します。すべてを同じplugin interfaceとして扱いません。

## 2. Extension Classes

### C0: Core Built-in

KIM releaseとしてbuild、署名、配布されるCore subsystemです。Core内部interfaceへアクセスできますが、documented transaction/authority boundaryを迂回できません。

例: Core scheduler、Operation service、Agent Gateway、built-in OVN/Ceph controller。

### C1: Certified Restricted Module

KIM release bundleへ静的登録される制限moduleです。in-processでもnarrow interfaceだけを受け取り、DB、Bus、credential、generic backend handleへアクセスしません。

例: Agent Operation Module、pure Placement Rule、OS discovery adapter。

OVS-DPDK Host discovery/operation moduleもC1に分類します。

### C2: Isolated Adapter Service

別process/serviceとして、scoped workload identityとversioned APIを使います。network/secret/backend side effectを持ち得るため、process、credential、rate limit、circuit breakerを分離します。

例: 外部Network/Storage adapter、Secret Provider integration、Identity verification adapter、Northbound protocol adapter。

### C3: Untrusted External Integration

公開Northbound API、Webhook/Event subscriptionなど、外部clientとしてのみ接続します。内部API、DB、Bus、Agent protocolへアクセスできません。

例: NFVO/VNFM client、OSS/BSS、external automation、generic webhook consumer。

## 3. Allowed Capability Matrix

| Capability | C0 | C1 | C2 | C3 |
|---|---|---|---|---|
| Core internal typed interface | Allowed | Narrow allow-list | Gateway API only | No |
| In-process execution | Allowed | Certified only | No | No |
| Direct Core DB access | Owning Core repository only | No | No | No |
| Internal Message Bus access | Owning Core service only | No | No | No |
| Scoped secret use | Least privilege | No by default | Explicit contract | Own external credential only |
| Backend side effect | Core workflow経由 | Agent typed Command内のみ | Operation/adapter contract内 | Public API requestのみ |
| Identity/Credential issuance | No。外部authority | No | External authorityとの検証連携のみ | No |
| Placement decision | Core final admission | pure rule outputのみ | No | request constraintのみ |
| Audit bypass | No | No | No | No |
| Runtime arbitrary code/plugin load | No | No | No | N/A |

Identity、Secret、Placementは高影響領域として扱います。

- Identity adapterはPrincipal verification/mappingだけで、credential authorityにならない。
- Secret adapterはopaque referenceとscoped retrieval/rotationだけを扱い、secret valueをCore DB/eventへ返さない。
- Placement extensionはpure eligibility/scoring outputだけを返し、reservation/final admission/DB writeを行わない。

## 4. Manifest

各C1/C2 extensionは署名対象manifestを持ちます。

```text
extension_id
extension_class
contract_version
implementation_version
capabilities
required_permissions
network_egress
secret_scopes
resource_limits
supported_core_versions
support_tier
artifact_digest
```

manifest外のpermission、egress、capabilityを実行時に獲得しません。

## 5. Common Conformance Tests

| ID | Contract |
|---|---|
| XCT-CONTRACT-001 | version negotiation、unknown field/version拒否、bounded payload |
| XCT-CONTRACT-002 | documented timeout、cancellation、resource limit |
| XCT-BOUNDARY-001 | Core DB/Bus/internal socketへ到達できない |
| XCT-BOUNDARY-002 | authorization、audit、Lease/fencingを迂回できない |
| XCT-BOUNDARY-003 | 独自Identity/Credential authorityを作れない |
| XCT-FAIL-001 | timeout/crash/response lossでUNKNOWNを保持し、成功/失敗へ丸めない |
| XCT-FAIL-002 | backend生error/secretをresponse、log、metricへ漏らさない |
| XCT-CAP-001 | capability add/remove/generation changeをfail closedで処理する |
| XCT-LIFE-001 | register、ready、drain、upgrade、rollback、removeを検証する |
| XCT-AUDIT-001 | 全mutationにactor、resource、result、correlation auditがある |
| XCT-UPGRADE-001 | Core N-1/N compatibilityとunsupported version拒否 |

## 6. Class-specific Tests

### C1

| ID | Contract |
|---|---|
| XCT-AGENT-001 | ModuleにController client、credential、Lease token、journal、generic libvirt handleが渡らない |
| XCT-AGENT-002 | closed Command variant以外をdecode/executeしない |
| XCT-PLC-001 | Placement Ruleがpureで、同一snapshotへ決定的結果を返す |
| XCT-PLC-002 | eligibility=falseをscoreで上書きできない |
| XCT-DPDK-001 | PMD CPU、DPDK memory、Port/RxQ capabilityをraw設定ではなくversioned schemaへ正規化する |
| XCT-DPDK-002 | arbitrary OVSDB/EAL/PCI/shell操作を受理しない |
| XCT-DPDK-003 | restart-required変更をonline operationとして報告・実行しない |
| XCT-DPDK-004 | side effect後のtimeoutをPMD/RxQ/Port/runtime read-backで解決し、不能ならUNKNOWNにする |
| XCT-HLC-001 | Baseline Control Evaluatorがpure/deterministicでDB/backend mutationを行わない |
| XCT-HLC-002 | unknown/stale/conflicting evidenceをCOMPLIANTへ丸めずUNKNOWNにする |
| XCT-HLC-003 | remediation moduleがclosed Control/Commandだけを受け、generic configurationを実行しない |
| XCT-HLC-004 | evaluator/module追加がControl version、evidence contract、support tierを宣言する |
| XCT-HLC-005 | Evaluator Artifactがimmutable digest、build provenance、schema/control/evidence compatibilityと再現可能fixture resultを持つ |
| XCT-HLC-006 | Evaluator revision更新が旧版とのshadow comparisonとcanary thresholdを通過するまでcurrent assignmentにならない |
| XCT-HLC-007 | External remediation adapterがCore DB/Agent credential/Lease/authorityを持たず、completion claimをCompliance resultへ直接変換しない |
| XCT-HGR-001 | HostGroup Selectorがpure/deterministicでcandidate membershipとprovenanceだけを返しDB writeしない |
| XCT-HGR-002 | external assertion adapterがsource identity、generation、freshness、integrity、bounded claimsを検証する |
| XCT-HGR-003 | stale/conflicting/unknown selector inputを任意membershipへ丸めずUNKNOWN/conflictとして返す |
| XCT-HGR-004 | selector/adapter upgradeがcontract version、input/output digest、compatibility、support tierを宣言する |
| XCT-AVR-001 | Recovery Eligibility Ruleがpure/deterministicでbounded eligibility/reasonだけを返す |
| XCT-AVR-002 | Rule/adapterがresponsibility、fencing state、Availability Binding、Recovery Operationを変更しない |
| XCT-AVR-003 | UNKNOWN fencing/storage/device/policy evidenceをeligible/safeへ丸めない |
| XCT-AVR-004 | rule version、supported Policy/evidence schema、fixture result、support tierを宣言する |

### C2

| ID | Contract |
|---|---|
| XCT-ISO-001 | process/workload identity/credential/network policyが分離される |
| XCT-ISO-002 | scoped API以外のCore endpointへ接続できない |
| XCT-SECRET-001 | secret valueを永続metadata、event、logへ含めない |
| XCT-IDENTITY-001 | issuer/subject検証だけを行い、KIM User DB/Credentialを作らない |
| XCT-BACKEND-001 | side effect後のtimeoutをtyped read-backで解決し、証明不能ならUNKNOWN |

### C3

| ID | Contract |
|---|---|
| XCT-PUBLIC-001 | 公開versioned APIと明示subscription以外へアクセスできない |
| XCT-PUBLIC-002 | Tenant scope、rate limit、idempotency、redactionを強制する |

## 7. Certification Evidence

Validated/Certified extension releaseには以下を保存します。

- manifestとartifact signature/digest
- conformance test reportとCore build ID
- dependency/SBOM/vulnerability report
- permission、egress、secret scope review
- fault injection result
- supported version matrix
- owner、support period、known limitations

conformance合格は製品サポート認定の必要条件ですが十分条件ではありません。実backend/OS/version組合せのrelease certificationを別途必要とします。

## 8. Revocation and Quarantine

重大な脆弱性、contract違反、signature不一致、capability虚偽を検出したextensionはquarantineします。

- 新規resource選択を停止する。
- 既存resourceを暗黙変更・削除しない。
- active Operation/LeaseをCore policyに従ってfenceする。
- operatorへ依存resourceとsafe actionsを提示する。
- 再認定までReadyへ戻さない。
