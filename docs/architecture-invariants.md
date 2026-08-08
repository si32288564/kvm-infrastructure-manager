# Architecture Invariants

- 状態: Draft
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
| INV-AUTH-001 | User/Service credential authorityは外部Identity Platformであり、KIMはcredentialを発行しない | AT-AUTH-001 |
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

## 8. Network and Storage

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-NET-001 | KIMはvirtual network/provider bindingを所有し、WAN/inter-PoP/physical switch authorityを暗黙取得しない | AT-NET-001 |
| INV-NET-002 | 未知または外部所有network objectを自動削除しない | FI-NET-001 |
| INV-STO-001 | attachment outcomeまたはsingle-writer fencingが不明なVolumeを別Hostへattachしない | FI-STORAGE-001 |
| INV-STO-002 | Volume backend capability差を明示し、未対応機能へsilent fallbackしない | AT-STO-002 |

## 9. Security, Audit, and Failure

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-SEC-001 | mutationはauthentication、authorization、audit durabilityを満たさなければfail closed | AT-SEC-001 |
| INV-SEC-002 | secret、生backend error、他Tenant resource identityを公開response/metricsへ出さない | AT-SEC-002 |
| INV-FAIL-001 | timeout/partition/Lease expiryをmutation失敗または未実行の証明にしない | FI-TRANSPORT-001 |
| INV-FAIL-002 | stale identity/generation/token/observationはcurrent authorityを進めない | FI-SPLIT-001 |
| INV-FAIL-003 | recovery不能resourceはblocked/quarantinedを維持し、推測cleanupしない | FI-DR-001 |
| INV-HA-001 | 同一Site HA failoverはcommitted authority RPO 0を目標とする | FI-DB-001 |
| INV-DR-001 | restore後の未知resourceはquarantineし、明示adoption前にmutationしない | FI-DR-001 |

## 10. Extensions

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-EXT-001 | ExtensionはCore DBへ直接writeできない | XCT-BOUNDARY-001 |
| INV-EXT-002 | Extensionはauthorization、audit、Lease/fencingを迂回できない | XCT-BOUNDARY-002 |
| INV-EXT-003 | Extensionは独自Identity/Credential authorityを暗黙追加できない | XCT-BOUNDARY-003 |
| INV-EXT-004 | Agent moduleはclosed Commandとnarrow backend interfaceだけを受け取る | XCT-AGENT-001 |
| INV-EXT-005 | UNKNOWNをFAILED/SUCCEEDEDへ丸めるadapterを受け入れない | XCT-FAIL-001 |
| INV-EXT-006 | capability消失時は新規利用を停止し、既存resourceを暗黙変更しない | XCT-CAP-001 |

## 11. Documentation

| ID | Invariant | 主な検証 |
|---|---|---|
| INV-DOC-001 | Requirements、Accepted ADR、Architectureの矛盾を暗黙解釈せず、実装を停止して解消する | AT-DOC-001 |
| INV-DOC-002 | 重要判断の変更はADR、Requirements、Architecture、test traceを同じchange setで更新する | AT-DOC-002 |

