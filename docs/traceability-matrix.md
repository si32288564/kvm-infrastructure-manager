# Architecture Traceability Matrix

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

RequirementからArchitecture、ADR、Invariant、Acceptance/Fault/Conformance Testまでを追跡します。実装開始時にIssue/Code/Test列を追加し、未追跡のMust requirementをPhase gateで拒否します。

## 2. Traceability Rules

- 一つの行は一つの検証可能な責務群を表す。
- `AT-*`は通常acceptance test、`FI-*`はfault injection、`XCT-*`はextension conformance test。
- `AT-*`/`PT-*`の契約は [Acceptance Test Catalog](acceptance-test-catalog.md)、`FI-*`は [Fault Injection Matrix](fault-injection-matrix.md)、`XCT-*`は [Extension Conformance Contract](extension-conformance.md) を正本とする。
- Architecture Invariantを持たない機能要件もacceptance testへ直接traceする。
- ADRがProposedの行はPhase 0でAcceptedになるまで実装確定しない。
- `Planned` testはtest contract作成済み、`Implemented`は実行可能、`Certified`はrelease環境で証拠保存済みを意味する。

## 3. Identity / Tenancy

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| IAM-001, IAM-004, IAM-006 | responsibility-boundaries, security | ADR-0005 | INV-AUTH-001, INV-AUTH-002 | AT-AUTH-001, AT-AUTH-002 | Planned |
| IAM-002, IAM-003 | domain-model, api-principles | ADR-0005 | INV-AUTH-002, INV-SEC-001 | AT-AUTH-003, AT-QUOTA-001 | Planned |
| IAM-005 | responsibility-boundaries | ADR-0005 | INV-AUTH-002 | AT-AUTH-004 | Planned |

## 4. Host / OS Portability

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| HST-001, HST-003 | architecture, agent-protocol | ADR-0001 | INV-AUTH-003, INV-AGT-003 | AT-HST-001 | Planned |
| HST-002, HST-004, HST-006 | architecture, domain-model | ADR-0001 | INV-DATA-002 | AT-HST-002 | Planned |
| HST-005 | placement-architecture | ADR-0006 | INV-PLC-001 | AT-HST-003 | Planned |
| HST-007, HST-008, HST-009, HST-010, HST-011 | architecture, extensibility-architecture | ADR-0004, ADR-0011 | INV-AGT-006, INV-AGT-007, INV-EXT-004 | AT-AGT-006, AT-AGT-007, XCT-AGENT-001 | Planned |

## 5. Image / Flavor / Compute

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| IMG-001, IMG-002, IMG-003 | architecture, security | ADR-0011 | INV-SEC-002 | AT-IMG-001, AT-IMG-002 | Planned |
| FLV-001, FLV-002 | domain-model, placement-architecture | ADR-0006 | INV-PLC-003, INV-PLC-004 | AT-FLV-001 | Planned |
| CMP-001, CMP-002, CMP-003 | architecture, execution-architecture | ADR-0002, ADR-0007 | INV-API-001, INV-API-002, INV-API-003, INV-DATA-002, INV-EXEC-006 | AT-CMP-001, AT-API-002 | Planned |
| CMP-004, CMP-007 | placement-architecture | ADR-0006 | INV-PLC-001, INV-PLC-004 | AT-PLC-008 | Planned |
| CMP-005, CMP-006, CMP-009 | placement-architecture | ADR-0006 | INV-PLC-007 | AT-PLC-007 | Planned |
| CMP-008 | api-principles, security | ADR-0005 | INV-SEC-001, INV-SEC-002 | AT-CMP-008 | Planned |

## 6. Placement

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| SCH-001, SCH-007 | placement-architecture | ADR-0006 | INV-PLC-001, INV-PLC-002 | AT-PLC-001, AT-PLC-002 | Planned |
| SCH-002, SCH-004 | placement-architecture | ADR-0006 | INV-PLC-003, INV-PLC-004, INV-PLC-006 | AT-PLC-003, AT-PLC-004, AT-PLC-006 | Planned |
| SCH-003, SCH-005 | placement-architecture | ADR-0006 | INV-PLC-001 | AT-PLC-009 | Planned |
| SCH-006 | placement-architecture | ADR-0006 | INV-PLC-005 | AT-PLC-005 | Planned |

## 7. Network / Storage

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| NET-001, NET-002, NET-003, NET-004, NET-005 | architecture, responsibility-boundaries | ADR-0011 | INV-NET-001 | AT-NET-001, AT-NET-002 | Planned |
| NET-006 | placement-architecture | ADR-0006 | INV-PLC-004, INV-PLC-007 | AT-NET-006 | Planned |
| NET-007 | failure-model | ADR-0010 | INV-NET-002, INV-FAIL-003 | FI-NET-001 | Planned |
| STO-001, STO-002, STO-003, STO-004, STO-005 | architecture, execution-architecture | ADR-0007, ADR-0011 | INV-STO-001, INV-EXEC-008 | AT-STO-001, FI-STORAGE-001 | Planned |
| STO-006 | extensibility-architecture | ADR-0011 | INV-STO-002, INV-EXT-006 | AT-STO-002, XCT-CAP-001 | Planned |

### NFV Dataplane

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| DPL-001, DPL-005, DPL-006, DPL-012 | nfv-dataplane-resource-architecture | ADR-0012 | INV-DPL-006 | AT-DPL-001, AT-DPL-004, AT-DPL-010, XCT-DPDK-001 | Planned |
| DPL-002, DPL-003, DPL-004 | nfv-dataplane-resource-architecture, placement-architecture | ADR-0012 | INV-DPL-001, INV-DPL-002 | AT-DPL-002, AT-DPL-003 | Planned |
| DPL-007, DPL-008, DPL-009 | nfv-dataplane-resource-architecture, placement-architecture | ADR-0006, ADR-0012 | INV-DPL-003, INV-DPL-004 | AT-DPL-005, AT-DPL-006, AT-DPL-007 | Planned |
| DPL-010, DPL-011 | nfv-dataplane-resource-architecture, execution-architecture | ADR-0007, ADR-0012 | INV-DPL-007, INV-DPL-008, INV-DPL-010 | AT-DPL-008, AT-DPL-009, FI-DPDK-003, FI-DPDK-005, XCT-DPDK-002, XCT-DPDK-003, XCT-DPDK-004 | Planned |
| DPL-013, DPL-014, DPL-015 | nfv-dataplane-resource-architecture, failure-model | ADR-0010, ADR-0012 | INV-DPL-005, INV-DPL-009 | AT-DPL-011, AT-DPL-012, AT-DPL-013, FI-DPDK-001 | Planned |

## 8. Operations / Observability / Audit

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| OPS-001, OPS-002 | api-principles, execution-architecture | ADR-0002 | INV-API-001 | AT-API-001, AT-OPS-001 | Planned |
| OPS-003, OPS-005 | failure-model, execution-architecture | ADR-0010 | INV-FAIL-001, INV-EXEC-008 | FI-TRANSPORT-001, AT-OPS-005 | Planned |
| OPS-004 | api-principles, extensibility-architecture | ADR-0011 | INV-SEC-002 | AT-EVT-001 | Planned |
| OPS-006, OPS-007, OPS-008, OPS-009, OPS-010 | execution-architecture | ADR-0007 | INV-EXEC-001, INV-EXEC-002, INV-EXEC-003, INV-EXEC-004, INV-EXEC-005, INV-EXEC-006, INV-EXEC-007, INV-EXEC-008 | AT-EXEC-001, AT-EXEC-002, AT-EXEC-003, AT-EXEC-004, AT-EXEC-005, AT-EXEC-006, AT-EXEC-007, FI-AGENT-001, FI-TRANSPORT-001 | Planned |
| O11Y-001, O11Y-002, O11Y-003 | security, failure-model | ADR-0010 | INV-SEC-002 | AT-O11Y-001 | Planned |
| AUD-001, AUD-002 | security, responsibility-boundaries | ADR-0005, ADR-0010 | INV-SEC-001, INV-SEC-002 | AT-AUD-001, AT-AUD-002 | Planned |

## 9. Non-functional Requirements

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| NFR-AVL-001, NFR-AVL-002, NFR-AVL-003, NFR-AVL-004 | architecture, ha-dr | ADR-0009 | INV-HA-001 | FI-DB-001, AT-HA-001 | Planned |
| NFR-AVL-005, NFR-AVL-006 | ha-dr | ADR-0009 | INV-DR-001, INV-AUTH-005 | FI-DR-001 | Planned |
| NFR-PERF-001, NFR-PERF-002, NFR-PERF-003, NFR-PERF-004 | architecture, release-plan | ADR-0003 | - | PT-SCALE-001, PT-API-001, PT-OPS-001 | Planned |
| NFR-SEC-001, NFR-SEC-002, NFR-SEC-003, NFR-SEC-004, NFR-SEC-005 | security, agent-protocol | ADR-0005, ADR-0008 | INV-SEC-001, INV-SEC-002, INV-AGT-002 | AT-SEC-001, AT-SEC-002, AT-SEC-003, AT-AGT-002 | Planned |
| NFR-OPS-001, NFR-OPS-002, NFR-OPS-003, NFR-OPS-004, NFR-OPS-005, NFR-OPS-006 | architecture, release-plan, extensibility-architecture | ADR-0003, ADR-0004, ADR-0011 | INV-AGT-006, INV-EXT-006 | AT-UPG-001, AT-OFFLINE-001, XCT-CAP-001 | Planned |
| NFR-ROB-001, NFR-ROB-002, NFR-ROB-003, NFR-ROB-004, NFR-ROB-005, NFR-ROB-006 | failure-model | ADR-0010 | INV-FAIL-001, INV-FAIL-002, INV-FAIL-003 | FI-CLIENT-001, FI-CP-001, FI-DB-001, FI-BUS-001, FI-GATEWAY-001, FI-AGENT-001, FI-HOST-001, FI-LIBVIRT-001, FI-NET-001, FI-STORAGE-001, FI-SPLIT-001, FI-IDENTITY-001 | Planned |
| NFR-EXT-001, NFR-EXT-002, NFR-EXT-003, NFR-EXT-004, NFR-EXT-005, NFR-EXT-006 | extensibility-architecture | ADR-0011 | INV-EXT-001, INV-EXT-002, INV-EXT-003, INV-EXT-004, INV-EXT-005, INV-EXT-006 | XCT-CONTRACT-001, XCT-BOUNDARY-001, XCT-BOUNDARY-002, XCT-BOUNDARY-003, XCT-FAIL-001, XCT-CAP-001, XCT-LIFE-001 | Planned |

## 10. Coverage Gate

Phase 0完了条件:

- 全Must requirementがArchitectureとInvariantまたはAcceptance Testへtraceされる。
- 重要ADRがAcceptedで、対応するtest contractがPlanned以上になる。
- `INV-*`に少なくとも一つの検証IDがある。
- 未trace、矛盾、廃止test IDをCIが検出する。

Developer Preview開始条件:

- 対象sliceのtestがImplemented。
- release blocker invariantがCIで常時実行される。
- 手動検証にはowner、手順、保存evidence、期限がある。
