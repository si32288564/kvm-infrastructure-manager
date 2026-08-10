# Architecture Traceability Matrix

- 状態: Baseline
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
| HST-005 | host-grouping-architecture, placement-architecture | ADR-0006, ADR-0014 | INV-HGR-001, INV-HGR-007 | AT-HST-003, AT-HGR-001, AT-HGR-007 | Planned |
| HST-007, HST-008, HST-009, HST-010, HST-011 | architecture, extensibility-architecture | ADR-0004, ADR-0011 | INV-AGT-006, INV-AGT-007, INV-EXT-004 | AT-AGT-006, AT-AGT-007, XCT-AGENT-001 | Planned |
| HST-012, HST-013, HST-014 | product-vision, architecture | ADR-0003, ADR-0004 | INV-AGT-008, INV-AGT-009, INV-AGT-010 | AT-AGT-008, AT-AGT-009, AT-AGT-010 | Planned |
| HST-015, HST-016, HST-017 | architecture, domain-model, agent-protocol, phase-1-implementation-plan | ADR-0001, ADR-0004, ADR-0024 | INV-AGT-023, INV-AGT-024 | AT-HST-004, AT-HST-005, FI-AGENT-003 | Implemented |
| HST-018, HST-019 | architecture, agent-protocol, phase-1-implementation-plan | ADR-0001, ADR-0004 | INV-AGT-025, INV-AGT-026 | AT-HST-006, AT-HST-007, FI-AGENT-004 | Implemented (CPU/NUMA/Memory/HugePages) |

## 4.1 Agent Transport Multiplexing

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| AGT-001, AGT-002, AGT-003, AGT-004, AGT-005 | agent-protocol, phase-1-implementation-plan | ADR-0008 | INV-AGT-011, INV-AGT-014 | AT-AGT-011, AT-AGT-012 | Planned |
| AGT-006, AGT-011 | agent-protocol, pki-and-trust-lifecycle-architecture, phase-1-implementation-plan | ADR-0008, ADR-0023 | INV-AGT-012 | FI-GATEWAY-003 | Planned |
| AGT-007 | agent-protocol, execution-architecture, failure-model | ADR-0007, ADR-0008, ADR-0010 | INV-AGT-013 | FI-GATEWAY-004 | Planned |
| AGT-008 | agent-protocol, security, extensibility-architecture | ADR-0008, ADR-0011 | INV-AGT-017 | AT-AGT-014 | Planned |
| AGT-009, AGT-010 | agent-protocol, phase-1-implementation-plan | ADR-0008 | INV-AGT-015, INV-AGT-016 | AT-AGT-012, AT-AGT-013, FI-GATEWAY-005 | Planned |
| AGT-012 | agent-protocol, pki-and-trust-lifecycle-architecture, security | ADR-0008, ADR-0023 | INV-AGT-018 | AT-AGT-015 | In Progress |
| AGT-013 | agent-protocol, execution-architecture, time-and-clock-semantics | ADR-0007, ADR-0008, ADR-0022 | INV-AGT-019 | FI-GATEWAY-006 | In Progress |
| AGT-014 | agent-protocol, time-and-clock-semantics, phase-1-implementation-plan | ADR-0008, ADR-0022 | INV-AGT-020 | FI-GATEWAY-007 | In Progress |
| AGT-015, AGT-016, AGT-017 | agent-protocol, execution-architecture, phase-1-implementation-plan | ADR-0007, ADR-0010, ADR-0024 | INV-AGT-021, INV-AGT-022 | AT-AGT-016, FI-GATEWAY-008 | Implemented |

## 5. Host Lifecycle / Baseline / Compliance

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| HLC-001, HLC-002, HLC-017 | host-lifecycle-and-compliance-architecture, agent-protocol | ADR-0013 | INV-HLC-001, INV-HLC-008 | AT-HLC-001, AT-HLC-002, FI-HLC-008 | Planned |
| HLC-003 | host-lifecycle-and-compliance-architecture, security | ADR-0013 | INV-HLC-001, INV-HLC-007 | AT-HLC-003 | Planned |
| HLC-004, HLC-005 | host-lifecycle-and-compliance-architecture, domain-model, extensibility-architecture | ADR-0011, ADR-0013 | INV-HLC-002 | AT-HLC-004, XCT-HLC-001, XCT-HLC-004 | Planned |
| HLC-006, HLC-007 | host-lifecycle-and-compliance-architecture | ADR-0013 | INV-HLC-003, INV-HLC-006 | AT-HLC-005, AT-HLC-006, FI-HLC-004, XCT-HLC-002 | Planned |
| HLC-008 | host-lifecycle-and-compliance-architecture, placement-architecture | ADR-0006, ADR-0013 | INV-HLC-004 | AT-HLC-007 | Planned |
| HLC-009, HLC-010 | host-lifecycle-and-compliance-architecture, execution-architecture, agent-protocol, extensibility-architecture | ADR-0007, ADR-0011, ADR-0013 | INV-HLC-005, INV-HLC-009 | AT-HLC-008, AT-HLC-009, AT-HLC-013, FI-HLC-005, XCT-HLC-003 | Planned |
| HLC-011 | host-lifecycle-and-compliance-architecture, failure-model | ADR-0010, ADR-0013 | INV-HLC-003, INV-HLC-004 | AT-HLC-010, FI-HLC-004 | Planned |
| HLC-012 | host-lifecycle-and-compliance-architecture | ADR-0013 | INV-HLC-002, INV-HLC-012 | AT-HLC-011, FI-HLC-003, FI-HLC-006 | Planned |
| HLC-013 | host-lifecycle-and-compliance-architecture, execution-architecture | ADR-0007, ADR-0013 | INV-HLC-005 | AT-HLC-012, FI-HLC-007 | Planned |
| HLC-014 | host-lifecycle-and-compliance-architecture, responsibility-boundaries | ADR-0013 | INV-HLC-009 | AT-HLC-013 | Planned |
| HLC-015 | host-lifecycle-and-compliance-architecture | ADR-0013 | INV-HLC-010 | AT-HLC-014, FI-HLC-007 | Planned |
| HLC-016 | host-lifecycle-and-compliance-architecture, security | ADR-0013 | INV-HLC-011 | AT-HLC-016, FI-HLC-001, FI-HLC-002 | Planned |
| HLC-018 | host-lifecycle-and-compliance-architecture, security | ADR-0013 | INV-HLC-007 | AT-HLC-015 | Planned |
| HLC-019 | host-lifecycle-and-compliance-architecture, security | ADR-0013 | INV-HLC-013, INV-HLC-014 | AT-HLC-017, AT-HLC-018, FI-HLC-009 | Planned |
| HLC-020, HLC-021 | host-lifecycle-and-compliance-architecture, extensibility-architecture | ADR-0011, ADR-0013 | INV-HLC-015, INV-HLC-016 | AT-HLC-019, AT-HLC-020, FI-HLC-010, XCT-HLC-005, XCT-HLC-006 | Planned |
| HLC-022 | host-lifecycle-and-compliance-architecture, responsibility-boundaries, security | ADR-0011, ADR-0013 | INV-HLC-017, INV-HLC-018 | AT-HLC-021, AT-HLC-022, FI-HLC-011, FI-HLC-012, XCT-HLC-007 | Planned |
| HLC-023, HLC-024, HLC-025 | host-lifecycle-and-compliance-architecture, pki-and-trust-lifecycle-architecture, agent-protocol | ADR-0013, ADR-0023, ADR-0024 | INV-HLC-019, INV-HLC-020 | AT-HLC-023, AT-HLC-024, FI-HLC-013 | Implemented |
| HLC-026, HLC-027 | host-lifecycle-and-compliance-architecture, agent-protocol, execution-architecture | ADR-0007, ADR-0013, ADR-0023 | INV-HLC-021, INV-HLC-022, INV-HLC-023 | AT-HLC-025, AT-HLC-026, FI-HLC-013 | Implemented (Command Lease coupling remains P1-B) |

## 6. Host Grouping

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| HGR-001, HGR-015 | host-grouping-architecture, domain-model | ADR-0014 | INV-HGR-001, INV-HGR-013 | AT-HGR-001, AT-HGR-013, AT-HGR-015, FI-HGR-007 | Planned |
| HGR-002 | host-grouping-architecture | ADR-0014 | INV-HGR-002 | AT-HGR-002 | Planned |
| HGR-003, HGR-007 | host-grouping-architecture, failure-model | ADR-0010, ADR-0014 | INV-HGR-003 | AT-HGR-003, FI-HGR-001, FI-HGR-002 | Planned |
| HGR-004, HGR-005 | host-grouping-architecture, extensibility-architecture | ADR-0011, ADR-0014 | INV-HGR-001, INV-HGR-004 | AT-HGR-004, AT-HGR-005, XCT-HGR-001, XCT-HGR-002, XCT-HGR-003, XCT-HGR-004 | Planned |
| HGR-006 | host-grouping-architecture | ADR-0014 | INV-HGR-005 | FI-HGR-003 | Planned |
| HGR-008, HGR-009 | host-grouping-architecture, placement-architecture | ADR-0006, ADR-0014 | INV-HGR-006, INV-HGR-007 | AT-HGR-006, AT-HGR-007, FI-HGR-004 | Planned |
| HGR-010 | host-grouping-architecture, placement-architecture | ADR-0006, ADR-0014 | INV-HGR-008 | AT-HGR-008 | Planned |
| HGR-011 | host-grouping-architecture, host-lifecycle-and-compliance-architecture | ADR-0013, ADR-0014 | INV-HGR-009 | AT-HGR-009, FI-HGR-006 | Planned |
| HGR-012 | host-grouping-architecture, host-lifecycle-and-compliance-architecture | ADR-0013, ADR-0014 | INV-HGR-010 | AT-HGR-010, FI-HGR-005 | Planned |
| HGR-013 | host-grouping-architecture, host-lifecycle-and-compliance-architecture | ADR-0013, ADR-0014 | INV-HGR-010 | AT-HGR-011, FI-HGR-005, FI-HGR-008 | Planned |
| HGR-014 | host-grouping-architecture, security | ADR-0014 | INV-HGR-011 | AT-HGR-012 | Planned |
| HGR-016 | host-grouping-architecture, failure-model | ADR-0010, ADR-0014 | INV-HGR-012 | AT-HGR-014 | Planned |
| HGR-017 | host-grouping-architecture, availability-responsibility-architecture, placement-architecture | ADR-0014, ADR-0015 | INV-HGR-014 | AT-AVR-005, FI-AVR-001 | Planned |

## 7. Availability Responsibility / Managed Recovery

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| AVR-001, AVR-002, AVR-003, AVR-004 | availability-responsibility-architecture, host-grouping-architecture | ADR-0014, ADR-0015 | INV-AVR-001 | AT-AVR-001, AT-AVR-002, AT-AVR-003, AT-AVR-004 | Planned |
| AVR-005 | availability-responsibility-architecture, placement-architecture | ADR-0006, ADR-0015 | INV-HGR-014 | AT-AVR-005, FI-AVR-001 | Planned |
| AVR-006 | availability-responsibility-architecture, placement-architecture, domain-model | ADR-0006, ADR-0015 | INV-AVR-002 | AT-AVR-006 | Planned |
| AVR-007 | availability-responsibility-architecture | ADR-0015 | INV-AVR-003 | AT-AVR-007, FI-AVR-002 | Planned |
| AVR-008 | availability-responsibility-architecture, failure-model | ADR-0010, ADR-0015 | INV-AVR-006, INV-AVR-013 | AT-AVR-008, FI-AVR-003 | Planned |
| AVR-009 | availability-responsibility-architecture, responsibility-boundaries | ADR-0015 | INV-AVR-004, INV-AVR-012 | AT-AVR-009, FI-AVR-004, FI-AVR-010 | Planned |
| AVR-010 | availability-responsibility-architecture, security | ADR-0015 | INV-AVR-005 | AT-AVR-010, FI-AVR-005 | Planned |
| AVR-011, AVR-012 | availability-responsibility-architecture, failure-model, placement-architecture | ADR-0006, ADR-0010, ADR-0015 | INV-AVR-006, INV-AVR-007, INV-AVR-008, INV-AVR-013 | AT-AVR-011, AT-AVR-012, FI-AVR-003, FI-AVR-007, FI-AVR-008, XCT-AVR-001, XCT-AVR-002, XCT-AVR-003, XCT-AVR-004 | Planned |
| AVR-013 | availability-responsibility-architecture, execution-architecture | ADR-0007, ADR-0015, ADR-0017 | INV-AVR-009, INV-AVR-010 | AT-AVR-013, FI-AVR-006, FI-RCV-013 | Planned |
| AVR-014 | availability-responsibility-architecture, execution-architecture | ADR-0007, ADR-0015 | INV-AVR-011 | AT-AVR-014, FI-AVR-009 | Planned |
| AVR-015 | availability-responsibility-architecture, placement-architecture | ADR-0006, ADR-0015 | INV-AVR-008 | AT-AVR-015, FI-AVR-008 | Planned |
| AVR-016 | availability-responsibility-architecture, responsibility-boundaries | ADR-0015 | INV-AVR-012 | AT-AVR-016, FI-AVR-010 | Planned |

## 8. Workload Resilience Intent

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| WRI-001, WRI-005, WRI-014 | workload-resilience-intent-architecture, domain-model | ADR-0016 | INV-WRI-008, INV-WRI-012 | AT-WRI-001, AT-WRI-005, AT-WRI-014, FI-WRI-004, FI-WRI-007 | Planned |
| WRI-002 | workload-resilience-intent-architecture, responsibility-boundaries | ADR-0016 | INV-WRI-001 | AT-WRI-002 | Planned |
| WRI-003 | workload-resilience-intent-architecture, security | ADR-0016 | INV-WRI-002 | AT-WRI-003 | Planned |
| WRI-004, WRI-009 | workload-resilience-intent-architecture, host-grouping-architecture, placement-architecture | ADR-0006, ADR-0014, ADR-0016 | INV-WRI-003, INV-WRI-004, INV-WRI-007 | AT-WRI-004, AT-WRI-009, FI-WRI-002, FI-WRI-003 | Planned |
| WRI-006, WRI-007, WRI-008 | workload-resilience-intent-architecture, placement-architecture | ADR-0006, ADR-0016 | INV-WRI-005, INV-WRI-006 | AT-WRI-006, AT-WRI-007, AT-WRI-008, FI-WRI-001 | Planned |
| WRI-010 | workload-resilience-intent-architecture, failure-model | ADR-0010, ADR-0016 | INV-WRI-008 | AT-WRI-010, FI-WRI-004 | Planned |
| WRI-011 | workload-resilience-intent-architecture, failure-model | ADR-0010, ADR-0016 | INV-WRI-009 | AT-WRI-011, FI-WRI-005 | Planned |
| WRI-012 | workload-resilience-intent-architecture, availability-responsibility-architecture | ADR-0015, ADR-0016 | INV-WRI-010 | AT-WRI-012 | Planned |
| WRI-013 | workload-resilience-intent-architecture, extensibility-architecture | ADR-0011, ADR-0016 | INV-WRI-011 | AT-WRI-013, FI-WRI-006, FI-WRI-008, XCT-WRI-001, XCT-WRI-002, XCT-WRI-003, XCT-WRI-004 | Planned |
| WRI-015 | workload-resilience-intent-architecture | ADR-0016 | INV-WRI-013 | AT-WRI-015, FI-WRI-009 | Planned |

## 9. Recovery Storm Control

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| RCV-001 | availability-responsibility-architecture | ADR-0015, ADR-0017 | - | AT-RCV-001 | Planned |
| RCV-002, RCV-012 | availability-responsibility-architecture, execution-architecture, ha-dr | ADR-0007, ADR-0009, ADR-0017 | INV-RCV-001, INV-RCV-012 | AT-RCV-002, AT-RCV-012, FI-RCV-002, FI-RCV-009 | Planned |
| RCV-003 | availability-responsibility-architecture | ADR-0017 | INV-RCV-002 | AT-RCV-003, FI-RCV-004 | Planned |
| RCV-004 | availability-responsibility-architecture, placement-architecture, execution-architecture | ADR-0006, ADR-0007, ADR-0017 | INV-RCV-003 | AT-RCV-004 | Planned |
| RCV-005 | availability-responsibility-architecture, execution-architecture, failure-model | ADR-0007, ADR-0010, ADR-0017 | INV-RCV-004 | AT-RCV-005, FI-RCV-003 | Planned |
| RCV-006 | availability-responsibility-architecture | ADR-0017 | INV-RCV-005 | AT-RCV-006, FI-RCV-001 | Planned |
| RCV-007 | availability-responsibility-architecture, security, extensibility-architecture | ADR-0011, ADR-0017 | INV-RCV-006, INV-RCV-007 | AT-RCV-007, FI-RCV-005, XCT-RCV-001, XCT-RCV-002, XCT-RCV-003, XCT-RCV-004 | Planned |
| RCV-008 | availability-responsibility-architecture, failure-model | ADR-0010, ADR-0017 | INV-RCV-008 | AT-RCV-008, FI-RCV-006 | Planned |
| RCV-009 | availability-responsibility-architecture, failure-model | ADR-0010, ADR-0017 | INV-RCV-009 | AT-RCV-009, FI-RCV-007 | Planned |
| RCV-010 | availability-responsibility-architecture | ADR-0017 | INV-RCV-010 | AT-RCV-010, FI-RCV-010 | Planned |
| RCV-011 | availability-responsibility-architecture | ADR-0017 | INV-RCV-011 | AT-RCV-011, FI-RCV-008 | Planned |
| RCV-013 | availability-responsibility-architecture, execution-architecture | ADR-0007, ADR-0017 | INV-RCV-013 | AT-RCV-013, FI-RCV-003, FI-RCV-011 | Planned |
| RCV-014 | availability-responsibility-architecture, execution-architecture | ADR-0007, ADR-0017 | INV-RCV-014 | AT-RCV-014, FI-RCV-012 | Planned |
| RCV-015 | availability-responsibility-architecture, execution-architecture, failure-model | ADR-0007, ADR-0010, ADR-0017 | INV-RCV-015 | AT-RCV-015, FI-RCV-007, FI-RCV-013 | Planned |

## 10. Data and Persistence

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| DAT-001, DAT-004 | data-persistence-architecture, domain-model | ADR-0018 | INV-DATA-004 | AT-DATA-003, AT-DATA-004, FI-DATA-012 | Planned |
| DAT-002, DAT-003 | data-persistence-architecture, architecture, execution-architecture | ADR-0007, ADR-0018 | INV-DATA-001, INV-DATA-003 | AT-DATA-001, AT-EXEC-007 | Planned |
| DAT-005 | data-persistence-architecture, architecture | ADR-0018 | INV-DATA-005 | AT-DATA-005, FI-DATA-001, FI-DATA-002 | Planned |
| DAT-006 | data-persistence-architecture | ADR-0018 | INV-DATA-006 | AT-DATA-006, FI-DATA-003 | Planned |
| DAT-007, DAT-008 | data-persistence-architecture, security | ADR-0018 | INV-DATA-007, INV-DATA-009 | AT-DATA-007, AT-DATA-008, FI-DATA-004, FI-DATA-005 | Planned |
| DAT-009 | data-persistence-architecture, responsibility-boundaries | ADR-0018 | INV-DATA-008 | AT-DATA-008, FI-DATA-005 | Planned |
| DAT-010 | data-persistence-architecture | ADR-0018 | INV-DATA-010 | AT-DATA-009, FI-DATA-005 | Planned |
| DAT-011 | data-persistence-architecture | ADR-0018 | INV-DATA-011 | AT-DATA-010, FI-DATA-007 | Planned |
| DAT-012, DAT-013 | data-persistence-architecture | ADR-0018 | INV-DATA-012, INV-DATA-013 | AT-DATA-011, AT-DATA-012, FI-DATA-006, FI-DATA-007 | Planned |
| DAT-014 | data-persistence-architecture, ha-dr | ADR-0009, ADR-0018 | INV-DATA-014 | AT-DATA-013, AT-DATA-018, FI-DATA-008 | Planned |
| DAT-015 | data-persistence-architecture, ha-dr, failure-model | ADR-0009, ADR-0010, ADR-0018 | INV-DATA-015 | AT-DATA-014, FI-DATA-009 | Planned |
| DAT-016, DAT-017 | data-persistence-architecture, ha-dr, failure-model | ADR-0009, ADR-0010, ADR-0018 | INV-DATA-016, INV-DATA-017 | AT-DATA-015, AT-DATA-016, FI-DR-001, FI-DATA-011 | Planned |
| DAT-018 | data-persistence-architecture, execution-architecture, ha-dr | ADR-0007, ADR-0009, ADR-0018 | INV-DATA-018 | AT-DATA-017, FI-DATA-010 | Planned |
| DAT-019 | data-persistence-architecture, ha-dr, failure-model | ADR-0009, ADR-0010, ADR-0018 | INV-DATA-019 | AT-DATA-019, FI-DATA-009, FI-DATA-013 | Planned |
| DAT-020 | data-persistence-architecture | ADR-0018 | INV-DATA-020 | AT-DATA-020, FI-DATA-014 | Planned |
| DAT-021 | data-persistence-architecture, security | ADR-0009, ADR-0018 | INV-DATA-021 | AT-DATA-021, FI-DATA-015 | Planned |

## 11. Image / Flavor / Compute

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| IMG-001, IMG-002, IMG-003, IMG-004 | architecture, security, storage-attachment-fencing-architecture | ADR-0011, ADR-0019 | INV-SEC-002, INV-IMG-001, INV-IMG-002 | AT-IMG-001, AT-IMG-002, AT-IMG-003 | Partial (immutable metadata/integrity authority and digest-addressed Local LVM materialization/read-back implemented; retrieval provider and cache distribution pending) |
| FLV-001, FLV-002 | domain-model, placement-architecture | ADR-0006 | INV-PLC-003, INV-PLC-004, INV-FLV-001, INV-FLV-002 | AT-FLV-001, FI-DATA-015 | Implemented (catalog persistence and lossless Placement shape) |
| CMP-001, CMP-002, CMP-003, CMP-011, CMP-012, CMP-013, CMP-014, CMP-015 | architecture, execution-architecture, placement-architecture, storage-attachment-fencing-architecture, network-resource-architecture | ADR-0002, ADR-0006, ADR-0007, ADR-0019, ADR-0020 | INV-API-001, INV-API-002, INV-API-003, INV-DATA-002, INV-EXEC-006, INV-CMP-001, INV-CMP-002, INV-CMP-003, INV-CMP-004, INV-CMP-005 | AT-CMP-001, AT-CMP-009, AT-CMP-010, AT-CMP-011, AT-CMP-012, AT-CMP-013, AT-API-002, FI-LIBVIRT-004 | Partial (Domain/Storage/Image/OVS pre-boot→READY→typed power-on→libvirt read-back/runtime projection implemented; SR-IOV、post-boot dataplane、delete/public API pending) |
| CMP-010 | execution-architecture, agent-protocol | ADR-0001, ADR-0007 | INV-EXEC-002, INV-EXEC-005, INV-EXEC-012, INV-EXEC-013, INV-EXEC-026, INV-EXEC-028, INV-AGT-001, INV-AGT-008, INV-AGT-009 | AT-EXEC-012, AT-EXEC-024, AT-EXEC-026, AT-AGT-001, AT-AGT-008, AT-AGT-009, FI-LIBVIRT-001, FI-LIBVIRT-003, FI-LIBVIRT-004 | Implemented (Phase 1 typed power-state backend and remote KVM full-process qualification) |
| CMP-004, CMP-007 | placement-architecture | ADR-0006 | INV-PLC-001, INV-PLC-004 | AT-PLC-008 | Planned |
| CMP-005, CMP-006, CMP-009 | placement-architecture | ADR-0006 | INV-PLC-007 | AT-PLC-007 | Planned |
| CMP-008 | api-principles, security | ADR-0005 | INV-SEC-001, INV-SEC-002 | AT-CMP-008 | Planned |

## 12. Placement

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| SCH-001, SCH-007 | placement-architecture | ADR-0006 | INV-PLC-001, INV-PLC-002 | AT-PLC-001, AT-PLC-002 | Implemented (pure eligibility and eligible-only selection foundation) |
| SCH-002, SCH-004 | placement-architecture | ADR-0006 | INV-PLC-003, INV-PLC-004, INV-PLC-006 | AT-PLC-003, AT-PLC-004, AT-PLC-006 | Partial (Compute/Memory/HugePages/qualified PCI VF atomic admission; remaining domain claims pending) |
| SCH-003, SCH-005 | placement-architecture | ADR-0006 | INV-PLC-001 | AT-PLC-009 | Partial (bounded reason/score and deterministic rank implemented) |
| SCH-006 | placement-architecture | ADR-0006 | INV-PLC-005 | AT-PLC-005 | Implemented (Host-scoped serialization, full rollback, concurrent re-evaluation) |

## 13. Network / Storage

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| NET-001, NET-008, NET-019, NET-020, NET-040, NET-041, NET-042, NET-045, NET-046, NET-047, NET-048, NET-049, NET-050, NET-051, NET-052, NET-053, NET-054 | network-resource-architecture, architecture, execution-architecture | ADR-0007, ADR-0011, ADR-0020 | INV-NET-006, INV-NET-009, INV-NET-025, INV-NET-026, INV-NET-027, INV-NET-030, INV-NET-031, INV-NET-032, INV-NET-033, INV-NET-034, INV-NET-035, INV-NET-036, INV-NET-037, INV-NET-038, INV-NET-039 | AT-NET-002, AT-NET-007, AT-NET-011, AT-NET-029, AT-NET-030, AT-NET-031, AT-NET-034, AT-NET-035, AT-NET-036, AT-NET-037, AT-NET-038, AT-NET-039, AT-NET-040, AT-NET-041, AT-NET-042, AT-NET-043, AT-NET-044, AT-NET-045, FI-NET-006, FI-NET-007, FI-NET-021, FI-NET-022, FI-NET-023, FI-NET-025, FI-NET-026, FI-NET-027, FI-NET-028, FI-NET-029, FI-NET-030, FI-NET-031, FI-DB-003, FI-DB-004, FI-DB-005, FI-DB-006, XCT-NET-001, XCT-NET-002, XCT-NET-003, XCT-NET-004 | Partial (v2 Network/Port ownership separation、closed production-shape adapter、durable PostgreSQL multi-worker claim/renewal、graceful/hard drain、実 process kill・synchronous PostgreSQL repeated failover・renewal response-loss・512-work soak・sustained endpoint latency/pool saturation からの bounded read-back-first recovery、immutable OVN Port/NB/SB/chassis/logical-flow/Chassis/Encap/directed Geneve evidence implemented and real test KVM runtime qualified; Network/Router/DHCP/Security multi-object realization pending) |
| NET-002, NET-003, NET-013, NET-014 | network-resource-architecture | ADR-0020 | INV-NET-005 | AT-NET-008, FI-NET-005 | Partial (VLAN Segment Claim authority/current generation implemented; VNI and lifecycle cleanup pending) |
| NET-004, NET-022, NET-023, NET-027 | network-resource-architecture, security | ADR-0010, ADR-0020 | INV-NET-011, INV-NET-013 | AT-NET-012, AT-NET-013, AT-NET-017, FI-NET-010, FI-NET-013 | Planned |
| NET-005, NET-024, NET-025, NET-026 | network-resource-architecture, failure-model | ADR-0010, ADR-0020 | INV-NET-012 | AT-NET-014, AT-NET-016, FI-NET-011, FI-NET-012 | Planned |
| NET-006, NET-029, NET-030 | network-resource-architecture, placement-architecture, nfv-dataplane-resource-architecture | ADR-0006, ADR-0012, ADR-0020 | INV-NET-015, INV-PLC-004, INV-PLC-007 | AT-NET-006, AT-NET-020, FI-NET-018 | Partial (qualified VF and SRIOV_DIRECT Port claims atomically admitted and pre-boot realized; real VF hardware certification pending) |
| NET-007 | network-resource-architecture, failure-model | ADR-0010, ADR-0020 | INV-NET-002, INV-NET-007, INV-NET-010, INV-NET-018, INV-FAIL-003 | AT-NET-010, AT-NET-022, FI-NET-001, FI-NET-002, XCT-NET-004, XCT-NET-005 | Planned |
| NET-009, NET-010, NET-011, NET-012, NET-043, NET-044 | network-resource-architecture, data-persistence-architecture, placement-architecture | ADR-0006, ADR-0010, ADR-0018, ADR-0020 | INV-NET-003, INV-NET-004, INV-NET-028, INV-NET-029 | AT-NET-003, AT-NET-004, AT-NET-005, AT-NET-032, AT-NET-033, FI-NET-003, FI-NET-004, FI-NET-024 | Partial (explicit/automatic IP/MAC uniqueness、transactional Port Claim、immutable absence evidence、release quarantine/reuse workflow implemented; external allocation and policy-timed quarantine profile pending) |
| NET-015 | network-resource-architecture, responsibility-boundaries | ADR-0020 | INV-NET-001, INV-NET-019 | AT-NET-001, AT-NET-015 | Planned |
| NET-036, NET-037, NET-038, NET-039 | network-resource-architecture, agent-protocol, execution-architecture | ADR-0001, ADR-0007, ADR-0020 | INV-NET-021, INV-NET-022, INV-NET-023, INV-NET-024, INV-CMP-004 | AT-NET-026, AT-NET-027, AT-NET-028, AT-CMP-012, FI-NET-019, FI-NET-020 | Partial (OVS/SRIOV_DIRECT pre-boot realization、READY authority、OVS Host-side post-boot dataplane projection implemented; real VF hardware certification、OVN/E2E convergence pending) |
| NET-016, NET-017, NET-018 | network-resource-architecture, placement-architecture | ADR-0006, ADR-0020 | INV-NET-007, INV-NET-008 | AT-NET-009, AT-NET-010, FI-NET-007, FI-NET-008 | Partial (single current RESERVED Port Binding authority implemented; realization, ACTIVE verification, and handoff pending) |
| NET-021 | network-resource-architecture, execution-architecture, failure-model | ADR-0007, ADR-0010, ADR-0020 | INV-NET-010 | FI-NET-004, FI-NET-006, FI-NET-011, FI-NET-013 | Planned |
| NET-028 | network-resource-architecture, placement-architecture | ADR-0006, ADR-0020 | INV-NET-014 | AT-NET-018, FI-NET-014 | Planned |
| NET-031 | network-resource-architecture, availability-responsibility-architecture, execution-architecture | ADR-0007, ADR-0015, ADR-0020 | INV-NET-016 | AT-NET-019, FI-NET-008, FI-NET-009 | Planned |
| NET-032 | network-resource-architecture, data-persistence-architecture | ADR-0018, ADR-0020 | INV-NET-017 | AT-NET-021, FI-NET-015 | Planned |
| NET-033 | network-resource-architecture, failure-model | ADR-0010, ADR-0020 | INV-NET-018 | AT-NET-022, FI-NET-016, XCT-NET-005 | Planned |
| NET-034, NET-035 | network-resource-architecture, security, extensibility-architecture | ADR-0011, ADR-0020 | INV-NET-020, INV-SEC-002 | AT-NET-023, AT-NET-024, AT-NET-025, FI-NET-017, XCT-NET-006 | Planned |
| STO-001, STO-002, STO-008, STO-011, STO-012, STO-029 | storage-attachment-fencing-architecture, execution-architecture, placement-architecture | ADR-0006, ADR-0007, ADR-0019 | INV-STO-003, INV-STO-006, INV-STO-007, INV-STO-008, INV-STO-021 | AT-STO-001, AT-STO-003, AT-STO-006, AT-STO-007, AT-STO-008, AT-STO-016, AT-STO-017, AT-STO-025, FI-STORAGE-001, FI-STORAGE-004, FI-STORAGE-012, FI-STORAGE-013, FI-STORAGE-019 | Partial (Local LVM typed attach/cold-detach、device/holder Verification、ATTACHED/DETACHED Claim transition を実 Host で検証。live detach、fencing/reuse workflow は pending) |
| STO-003, STO-017, STO-028 | storage-attachment-fencing-architecture, placement-architecture, execution-architecture | ADR-0006, ADR-0007, ADR-0019 | INV-STO-012, INV-STO-020 | AT-STO-012, AT-STO-024, FI-STORAGE-009, FI-STORAGE-018 | Partial (Host/VG UUID intent、closed typed LV create/read-back、immutable LV identity evidence、current BOUND Binding を実 Host で検証。attach/detach と release/fencing は pending) |
| STO-004, STO-016 | storage-attachment-fencing-architecture, security | ADR-0011, ADR-0019 | INV-STO-011 | AT-STO-011, FI-STORAGE-002, FI-STORAGE-006, FI-STORAGE-010, XCT-STO-001 | Planned |
| STO-005, STO-020 | storage-attachment-fencing-architecture | ADR-0019 | INV-STO-015 | AT-STO-015, FI-STORAGE-012 | Planned |
| STO-006, STO-007 | storage-attachment-fencing-architecture, extensibility-architecture | ADR-0011, ADR-0019 | INV-STO-002 | AT-STO-002, AT-STO-021, AT-STO-022, XCT-CAP-001, XCT-STO-001, XCT-STO-006 | Partial (Local LVM Backend/Class/capability generation implemented; adapter conformance and additional profiles pending) |
| STO-009, STO-010 | storage-attachment-fencing-architecture | ADR-0019 | INV-STO-004, INV-STO-005 | AT-STO-004, AT-STO-005, FI-STORAGE-003 | Partial (PostgreSQL SINGLE_WRITER exclusion implemented; READ_ONLY_MANY pending) |
| STO-013, STO-014, STO-015 | storage-attachment-fencing-architecture, execution-architecture, failure-model | ADR-0007, ADR-0010, ADR-0019 | INV-STO-001, INV-STO-008, INV-STO-009, INV-STO-010 | AT-STO-008, AT-STO-009, AT-STO-010, FI-STORAGE-005, FI-STORAGE-006, FI-STORAGE-007 | Planned |
| STO-018 | storage-attachment-fencing-architecture, availability-responsibility-architecture | ADR-0010, ADR-0015, ADR-0019 | INV-STO-013 | AT-STO-013, FI-STORAGE-008, FI-STORAGE-010 | Planned |
| STO-019 | storage-attachment-fencing-architecture, placement-architecture, execution-architecture | ADR-0006, ADR-0007, ADR-0019 | INV-STO-014 | AT-STO-014, FI-STORAGE-011 | Planned |
| STO-021 | storage-attachment-fencing-architecture, execution-architecture | ADR-0007, ADR-0019 | INV-STO-003 | AT-STO-016, FI-STORAGE-013 | Planned |
| STO-022, STO-023 | storage-attachment-fencing-architecture, data-persistence-architecture | ADR-0018, ADR-0019 | INV-STO-016 | AT-STO-017, FI-STORAGE-012 | Planned |
| STO-024 | storage-attachment-fencing-architecture, security, extensibility-architecture | ADR-0011, ADR-0019 | INV-STO-011, INV-SEC-002 | AT-STO-011, AT-STO-020, XCT-STO-005 | Planned |
| STO-025 | storage-attachment-fencing-architecture, failure-model | ADR-0010, ADR-0019 | INV-STO-017 | AT-STO-018, FI-STORAGE-014, XCT-STO-002, XCT-STO-004 | Planned |
| STO-026 | storage-attachment-fencing-architecture, security | ADR-0019 | INV-STO-018 | AT-STO-019, FI-STORAGE-015, FI-STORAGE-016, XCT-STO-003 | Planned |
| STO-027 | storage-attachment-fencing-architecture, placement-architecture | ADR-0006, ADR-0019 | INV-STO-019, INV-PLC-004 | AT-STO-023, FI-STORAGE-017 | Partial (reserved ledger and observed/external capacity admission implemented; verified release/reuse pending) |

### NFV Dataplane

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| DPL-001, DPL-005, DPL-006, DPL-012 | nfv-dataplane-resource-architecture | ADR-0012 | INV-DPL-006 | AT-DPL-001, AT-DPL-004, AT-DPL-010, XCT-DPDK-001 | Planned |
| DPL-002, DPL-003, DPL-004 | nfv-dataplane-resource-architecture, placement-architecture | ADR-0012 | INV-DPL-001, INV-DPL-002 | AT-DPL-002, AT-DPL-003 | Planned |
| DPL-007, DPL-008, DPL-009 | nfv-dataplane-resource-architecture, placement-architecture | ADR-0006, ADR-0012 | INV-DPL-003, INV-DPL-004 | AT-DPL-005, AT-DPL-006, AT-DPL-007 | Planned |
| DPL-010, DPL-011 | nfv-dataplane-resource-architecture, execution-architecture | ADR-0007, ADR-0012 | INV-DPL-007, INV-DPL-008, INV-DPL-010 | AT-DPL-008, AT-DPL-009, FI-DPDK-003, FI-DPDK-005, XCT-DPDK-002, XCT-DPDK-003, XCT-DPDK-004 | Planned |
| DPL-013, DPL-014, DPL-015 | nfv-dataplane-resource-architecture, failure-model | ADR-0010, ADR-0012 | INV-DPL-005, INV-DPL-009 | AT-DPL-011, AT-DPL-012, AT-DPL-013, FI-DPDK-001 | Planned |
| DPL-016, DPL-017, DPL-018, DPL-019, DPL-020 | nfv-dataplane-resource-architecture, placement-architecture, data-persistence-architecture | ADR-0006, ADR-0010, ADR-0012, ADR-0018 | INV-DPL-011, INV-DPL-012, INV-DPL-013, INV-DPL-014 | AT-DPL-014, AT-DPL-015, AT-DPL-016, AT-DPL-017, FI-PCI-001, FI-PCI-002, FI-PCI-003, FI-PCI-004 | Implemented (qualified VF integrated with Placement Final Admission; hardware qualification pending) |

## 14. Operations / Observability / Audit

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| OPS-001, OPS-002 | api-principles, execution-architecture | ADR-0002 | INV-API-001 | AT-API-001, AT-OPS-001 | Planned |
| OPS-003, OPS-005 | failure-model, execution-architecture | ADR-0010 | INV-FAIL-001, INV-EXEC-008 | FI-TRANSPORT-001, AT-OPS-005 | Planned |
| OPS-004 | api-principles, extensibility-architecture | ADR-0011 | INV-SEC-002 | AT-EVT-001 | Planned |
| OPS-006, OPS-007, OPS-008, OPS-009, OPS-010, OPS-011, OPS-012, OPS-013, OPS-014, OPS-015, OPS-016, OPS-017, OPS-018, OPS-019, OPS-020, OPS-021, OPS-022, OPS-023, OPS-024, OPS-025, OPS-026, OPS-027 | execution-architecture, agent-protocol | ADR-0007, ADR-0024 | INV-EXEC-001, INV-EXEC-002, INV-EXEC-003, INV-EXEC-004, INV-EXEC-005, INV-EXEC-006, INV-EXEC-007, INV-EXEC-008, INV-EXEC-009, INV-EXEC-010, INV-EXEC-011, INV-EXEC-012, INV-EXEC-013, INV-EXEC-014, INV-EXEC-015, INV-EXEC-016, INV-EXEC-017, INV-EXEC-018, INV-EXEC-019, INV-EXEC-020, INV-EXEC-021, INV-EXEC-022, INV-EXEC-023, INV-EXEC-024, INV-EXEC-025, INV-EXEC-027, INV-EXEC-028 | AT-EXEC-001, AT-EXEC-002, AT-EXEC-003, AT-EXEC-004, AT-EXEC-005, AT-EXEC-006, AT-EXEC-007, AT-EXEC-008, AT-EXEC-009, AT-EXEC-010, AT-EXEC-011, AT-EXEC-012, AT-EXEC-013, AT-EXEC-014, AT-EXEC-015, AT-EXEC-016, AT-EXEC-017, AT-EXEC-018, AT-EXEC-019, AT-EXEC-020, AT-EXEC-021, AT-EXEC-022, AT-EXEC-023, AT-EXEC-025, AT-EXEC-026, FI-AGENT-001, FI-AGENT-002, FI-AGENT-005, FI-AGENT-006, FI-BUS-003, FI-BUS-004, FI-BUS-005, FI-BUS-006, FI-BUS-007, FI-BUS-008, FI-BUS-009, FI-GATEWAY-003, FI-GATEWAY-008, FI-LIBVIRT-004, FI-TRANSPORT-001, FI-TRANSPORT-002, FI-TRANSPORT-003, FI-TRANSPORT-004 | In Progress |
| O11Y-001, O11Y-002, O11Y-003, O11Y-004 | security, failure-model, network-resource-architecture | ADR-0010, ADR-0020 | INV-SEC-002, INV-NET-038 | AT-O11Y-001, AT-O11Y-002, AT-NET-044 | Partial (OVN worker bounded Prometheus lifecycle/execution/renewal/pool/backlog metrics implemented; product-wide alarms/traces pending) |
| AUD-001, AUD-002 | security, responsibility-boundaries | ADR-0005, ADR-0010 | INV-SEC-001, INV-SEC-002 | AT-AUD-001, AT-AUD-002 | Planned |

## 15. Non-functional Requirements

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| NFR-AVL-001, NFR-AVL-002, NFR-AVL-003, NFR-AVL-004 | architecture, ha-dr | ADR-0009 | INV-HA-001 | FI-DB-001, FI-DB-003, AT-HA-001, AT-NET-038 | Partial (synchronous PostgreSQL OVN work authority RPO 0、primary kill、standby promotion、read-back recoveryをqualification。full API/Control Plane HA profileは継続) |
| NFR-AVL-005, NFR-AVL-006 | ha-dr | ADR-0009 | INV-DR-001, INV-AUTH-005 | FI-DR-001 | Planned |
| NFR-PERF-001, NFR-PERF-002, NFR-PERF-003, NFR-PERF-004 | architecture, release-plan | ADR-0003 | - | PT-SCALE-001, PT-API-001, PT-OPS-001 | Planned |
| NFR-SEC-001, NFR-SEC-002, NFR-SEC-003, NFR-SEC-004, NFR-SEC-005 | security, agent-protocol | ADR-0005, ADR-0008 | INV-SEC-001, INV-SEC-002, INV-AGT-002 | AT-SEC-001, AT-SEC-002, AT-SEC-003, AT-AGT-002 | Planned |
| NFR-SEC-006, NFR-SEC-007, NFR-SEC-008, NFR-SEC-009, NFR-SEC-010 | pki-and-trust-lifecycle-architecture, security, agent-protocol | ADR-0005, ADR-0008, ADR-0023 | INV-PKI-001, INV-PKI-005, INV-PKI-006, INV-PKI-016, INV-PKI-021, INV-PKI-022, INV-PKI-023 | AT-PKI-001, AT-PKI-005, AT-PKI-006, AT-PKI-015, AT-PKI-020, AT-PKI-022, AT-PKI-023, FI-PKI-003, FI-PKI-016, FI-PKI-017, FI-PKI-018 | Planned |
| NFR-OPS-001, NFR-OPS-003, NFR-OPS-004, NFR-OPS-005, NFR-OPS-006 | architecture, release-plan, extensibility-architecture | ADR-0003, ADR-0004, ADR-0011 | INV-AGT-006, INV-EXT-006 | AT-OFFLINE-001, XCT-CAP-001 | Planned |
| NFR-OPS-013, NFR-OPS-014 | product-vision, architecture, release-plan | ADR-0003 | — | AT-DEPLOY-001, AT-DEPLOY-002 | Planned |
| NFR-OPS-002, NFR-OPS-007, NFR-OPS-008, NFR-OPS-009, NFR-OPS-010, NFR-OPS-011, NFR-OPS-012 | upgrade-and-compatibility-architecture, data-persistence-architecture, release-plan | ADR-0018, ADR-0021 | INV-UPG-001, INV-UPG-004, INV-UPG-005, INV-UPG-006, INV-UPG-007, INV-UPG-016, INV-UPG-017 | AT-UPG-001, AT-UPG-008, AT-UPG-010, AT-UPG-019, AT-UPG-023, FI-UPG-005, FI-UPG-010, FI-UPG-011, FI-UPG-013, FI-UPG-015 | Planned |
| NFR-ROB-001, NFR-ROB-002, NFR-ROB-003, NFR-ROB-004, NFR-ROB-005, NFR-ROB-006 | failure-model | ADR-0010 | INV-FAIL-001, INV-FAIL-002, INV-FAIL-003 | FI-CLIENT-001, FI-CP-001, FI-DB-001, FI-BUS-001, FI-GATEWAY-001, FI-AGENT-001, FI-HOST-001, FI-LIBVIRT-001, FI-NET-001, FI-STORAGE-001, FI-SPLIT-001, FI-IDENTITY-001 | Planned |
| NFR-ROB-007, NFR-ROB-008 | time-and-clock-semantics, failure-model | ADR-0010, ADR-0022 | INV-TIM-002, INV-TIM-005, INV-TIM-006, INV-TIM-019 | FI-TIME-001, FI-TIME-002, FI-TIME-003, FI-TIME-006, FI-TIME-015, AT-TIM-022 | Planned |
| NFR-EXT-001, NFR-EXT-002, NFR-EXT-003, NFR-EXT-004, NFR-EXT-005, NFR-EXT-006 | extensibility-architecture | ADR-0011 | INV-EXT-001, INV-EXT-002, INV-EXT-003, INV-EXT-004, INV-EXT-005, INV-EXT-006 | XCT-CONTRACT-001, XCT-BOUNDARY-001, XCT-BOUNDARY-002, XCT-BOUNDARY-003, XCT-FAIL-001, XCT-CAP-001, XCT-LIFE-001 | Planned |

## 16. Upgrade and Compatibility

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| UPG-001, UPG-002, UPG-003 | upgrade-and-compatibility-architecture | ADR-0021 | INV-UPG-001, INV-UPG-002, INV-UPG-022 | AT-UPG-002, AT-UPG-003, AT-UPG-004, AT-UPG-029, FI-UPG-001, FI-UPG-019 | Partial (immutable ReleaseManifest/component/explicit edge、CompatibilityDecision、current worker binding foundation implemented and OVN worker N/N-1 process qualification PASS; product-wide component graph/provenance/SBOM pending) |
| UPG-004, UPG-005 | upgrade-and-compatibility-architecture, data-persistence-architecture | ADR-0018, ADR-0021 | INV-UPG-003 | AT-UPG-005, AT-UPG-006, FI-UPG-010 | Planned |
| UPG-006, UPG-007 | upgrade-and-compatibility-architecture, host-grouping-architecture | ADR-0014, ADR-0021 | INV-UPG-007 | AT-UPG-007, FI-UPG-005, FI-UPG-015 | Planned |
| UPG-008, UPG-009 | upgrade-and-compatibility-architecture | ADR-0021 | INV-UPG-004, INV-UPG-005, INV-UPG-022 | AT-UPG-008, AT-UPG-029, FI-UPG-002, FI-UPG-003, FI-UPG-019 | Partial (OVN worker explicit N/N-1 edge、drain-aware claim fencing、all-participant work-schema FeatureGate implemented; other Control Plane/Agent/Event writers pending) |
| UPG-010 | upgrade-and-compatibility-architecture, data-persistence-architecture | ADR-0018, ADR-0021 | INV-DATA-011, INV-UPG-006 | AT-UPG-010, FI-DATA-007, FI-UPG-004 | Planned |
| UPG-011 | upgrade-and-compatibility-architecture, ha-dr | ADR-0009, ADR-0021 | INV-HA-001, INV-UPG-007 | AT-UPG-001, FI-UPG-015 | Planned |
| UPG-012, UPG-013, UPG-014 | upgrade-and-compatibility-architecture, agent-protocol, host-lifecycle-and-compliance-architecture | ADR-0004, ADR-0008, ADR-0013, ADR-0021 | INV-UPG-009, INV-UPG-010 | AT-UPG-011, AT-UPG-012, FI-UPG-006, FI-UPG-007 | Planned |
| UPG-015, UPG-016 | upgrade-and-compatibility-architecture, api-principles, data-persistence-architecture | ADR-0002, ADR-0018, ADR-0021 | INV-UPG-011 | AT-UPG-013, AT-UPG-014, FI-UPG-012 | Planned |
| UPG-017 | upgrade-and-compatibility-architecture, extensibility-architecture | ADR-0011, ADR-0021 | INV-UPG-012 | AT-UPG-015, FI-UPG-008, XCT-LIFE-001, XCT-UPGRADE-001 | Planned |
| UPG-018, UPG-019 | upgrade-and-compatibility-architecture, placement-architecture | ADR-0004, ADR-0006, ADR-0021 | INV-UPG-013, INV-UPG-014 | AT-UPG-016, AT-UPG-017, AT-UPG-018, FI-UPG-009 | Planned |
| UPG-020, UPG-021 | upgrade-and-compatibility-architecture, data-persistence-architecture | ADR-0018, ADR-0021 | INV-UPG-015, INV-UPG-016 | AT-UPG-019, AT-UPG-020, AT-UPG-021, FI-UPG-011 | Planned |
| UPG-022 | upgrade-and-compatibility-architecture, execution-architecture | ADR-0007, ADR-0021 | INV-UPG-003 | AT-UPG-022, FI-UPG-010 | Planned |
| UPG-023 | upgrade-and-compatibility-architecture, release-plan | ADR-0003, ADR-0021 | INV-UPG-017 | AT-UPG-023, FI-UPG-013 | Planned |
| UPG-024 | upgrade-and-compatibility-architecture, security | ADR-0005, ADR-0021 | INV-UPG-018 | AT-UPG-024, FI-UPG-014 | Planned |
| UPG-025 | upgrade-and-compatibility-architecture | ADR-0021 | INV-UPG-019 | AT-UPG-026, FI-UPG-016 | Planned |
| UPG-026 | upgrade-and-compatibility-architecture, data-persistence-architecture | ADR-0018, ADR-0021 | INV-UPG-020 | AT-UPG-027, FI-UPG-017 | Planned |
| UPG-027 | upgrade-and-compatibility-architecture | ADR-0021 | INV-UPG-021 | AT-UPG-028, FI-UPG-018 | Planned |

## 17. Time and Clock Semantics

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| TIM-001, TIM-002, TIM-003 | time-and-clock-semantics, data-persistence-architecture | ADR-0010, ADR-0022 | INV-TIM-001, INV-TIM-002 | AT-TIM-001, AT-TIM-002, AT-TIM-003, FI-TIME-001 | Planned |
| TIM-004, TIM-005, TIM-006 | time-and-clock-semantics, host-lifecycle-and-compliance-architecture | ADR-0013, ADR-0022 | INV-TIM-003 | AT-TIM-004, FI-TIME-005, FI-TIME-016 | Planned |
| TIM-007, TIM-008 | time-and-clock-semantics, data-persistence-architecture, ha-dr | ADR-0009, ADR-0018, ADR-0022 | INV-TIM-004, INV-TIM-005 | AT-TIM-005, AT-TIM-006, FI-TIME-002 | Planned |
| TIM-009, TIM-010, TIM-011 | time-and-clock-semantics, execution-architecture | ADR-0007, ADR-0010, ADR-0022 | INV-TIM-006, INV-TIM-007, INV-NET-033, INV-NET-034 | AT-TIM-007, AT-TIM-008, AT-TIM-009, AT-NET-039, AT-NET-040, FI-TIME-003, FI-DB-004, FI-DB-005 | Partial (OVN runtime claimでDB authority time、owner/generation、maximum lifetime、immutable renewal evidenceとcommit後response-loss recoveryを実装。全Lease classへの展開は継続) |
| TIM-012, TIM-013, TIM-014 | time-and-clock-semantics, agent-protocol | ADR-0008, ADR-0022 | INV-TIM-008, INV-TIM-009, INV-TIM-010 | AT-TIM-010, AT-TIM-011, FI-TIME-004, FI-TIME-005, FI-TIME-006 | Planned |
| TIM-015, TIM-016 | time-and-clock-semantics, host-lifecycle-and-compliance-architecture | ADR-0013, ADR-0022 | INV-TIM-011 | AT-TIM-012, AT-TIM-013, FI-TIME-007 | Planned |
| TIM-017, TIM-018 | time-and-clock-semantics, security, agent-protocol | ADR-0005, ADR-0008, ADR-0022 | INV-TIM-012, INV-TIM-013 | AT-TIM-014, FI-TIME-008, FI-TIME-014 | Planned |
| TIM-019, TIM-020 | time-and-clock-semantics, host-grouping-architecture | ADR-0014, ADR-0022 | INV-TIM-014 | AT-TIM-015, AT-TIM-016, FI-TIME-009 | Planned |
| TIM-021 | time-and-clock-semantics, availability-responsibility-architecture | ADR-0017, ADR-0022 | INV-TIM-015 | AT-TIM-017, FI-TIME-010 | Planned |
| TIM-022, TIM-023 | time-and-clock-semantics, data-persistence-architecture, upgrade-and-compatibility-architecture | ADR-0018, ADR-0021, ADR-0022 | INV-TIM-016, INV-TIM-017, INV-UPG-020 | AT-TIM-018, AT-TIM-019, FI-TIME-011, FI-UPG-017 | Planned |
| TIM-024 | time-and-clock-semantics, availability-responsibility-architecture | ADR-0017, ADR-0022 | INV-TIM-018 | AT-TIM-020, FI-TIME-012 | Planned |
| TIM-025 | time-and-clock-semantics, ha-dr, data-persistence-architecture | ADR-0009, ADR-0018, ADR-0022 | INV-TIM-005, INV-TIM-010 | AT-TIM-021, FI-TIME-006, FI-TIME-015 | Planned |
| TIM-026 | time-and-clock-semantics, api-principles | ADR-0002, ADR-0022 | INV-TIM-001 | AT-TIM-023 | Planned |
| TIM-027 | time-and-clock-semantics, failure-model | ADR-0010, ADR-0022 | INV-TIM-019, INV-TIM-020 | AT-TIM-022, AT-TIM-025, FI-TIME-013, FI-TIME-016 | Planned |
| TIM-028 | time-and-clock-semantics, fault-injection-matrix | ADR-0022 | INV-TIM-002, INV-TIM-005, INV-TIM-006, INV-TIM-011, INV-TIM-014, INV-TIM-016, INV-TIM-018 | FI-TIME-001, FI-TIME-002, FI-TIME-003, FI-TIME-007, FI-TIME-009, FI-TIME-011, FI-TIME-012, FI-TIME-015 | Planned |
| TIM-029 | time-and-clock-semantics | ADR-0022 | INV-TIM-021 | AT-TIM-026, FI-TIME-017 | Planned |
| TIM-030 | time-and-clock-semantics, host-lifecycle-and-compliance-architecture, placement-architecture | ADR-0013, ADR-0022 | INV-TIM-022 | AT-TIM-027, FI-TIME-018 | Planned |
| TIM-031 | time-and-clock-semantics | ADR-0022 | INV-TIM-023 | AT-TIM-028, FI-TIME-019 | Planned |

## 18. PKI and Trust Lifecycle

| Requirements | Architecture | ADR | Invariants | Tests | 状態 |
|---|---|---|---|---|---|
| PKI-001, PKI-002 | pki-and-trust-lifecycle-architecture, responsibility-boundaries, security | ADR-0005, ADR-0023 | INV-PKI-001, INV-PKI-002 | AT-PKI-001, AT-PKI-002 | Planned |
| PKI-003, PKI-004, PKI-005 | pki-and-trust-lifecycle-architecture, security | ADR-0023 | INV-PKI-003, INV-PKI-004 | AT-PKI-003, AT-PKI-004, FI-PKI-001, FI-PKI-002 | Planned |
| PKI-006 | pki-and-trust-lifecycle-architecture, extensibility-architecture, security | ADR-0011, ADR-0023 | INV-PKI-005 | AT-PKI-005, FI-PKI-003 | Planned |
| PKI-007, PKI-008, PKI-009, PKI-010, PKI-011 | pki-and-trust-lifecycle-architecture, agent-protocol | ADR-0005, ADR-0008, ADR-0023 | INV-PKI-006, INV-PKI-007, INV-PKI-008 | AT-PKI-006, AT-PKI-007, AT-PKI-008, FI-PKI-004, FI-PKI-005 | Planned |
| PKI-012, PKI-013, PKI-014 | pki-and-trust-lifecycle-architecture, host-lifecycle-and-compliance-architecture, agent-protocol | ADR-0008, ADR-0013, ADR-0023 | INV-PKI-009, INV-PKI-010 | AT-PKI-009, AT-PKI-010, FI-PKI-006 | Planned |
| PKI-015, PKI-016 | pki-and-trust-lifecycle-architecture, execution-architecture | ADR-0007, ADR-0023 | INV-PKI-011, INV-PKI-012 | AT-PKI-011, AT-PKI-012, FI-PKI-007 | Planned |
| PKI-017, PKI-018, PKI-019 | pki-and-trust-lifecycle-architecture, agent-protocol | ADR-0008, ADR-0023 | INV-PKI-013, INV-PKI-014, INV-PKI-015 | AT-PKI-013, AT-PKI-014, FI-PKI-008, FI-PKI-009, FI-PKI-010 | Planned |
| PKI-020, PKI-021, PKI-022 | pki-and-trust-lifecycle-architecture, security | ADR-0023 | INV-PKI-016, INV-PKI-017, INV-PKI-018 | AT-PKI-015, AT-PKI-016, FI-PKI-011, FI-PKI-012, FI-PKI-013 | Planned |
| PKI-023, PKI-024 | pki-and-trust-lifecycle-architecture, availability-responsibility-architecture, storage-attachment-fencing-architecture | ADR-0010, ADR-0015, ADR-0019, ADR-0023 | INV-PKI-019 | AT-PKI-017, FI-PKI-014 | Planned |
| PKI-025 | pki-and-trust-lifecycle-architecture, ha-dr, execution-architecture | ADR-0007, ADR-0009, ADR-0023 | INV-PKI-020 | AT-PKI-018, FI-PKI-015 | Planned |
| PKI-026, PKI-027 | pki-and-trust-lifecycle-architecture, upgrade-and-compatibility-architecture | ADR-0021, ADR-0023 | INV-PKI-021 | AT-PKI-019, AT-PKI-020, FI-PKI-016 | Planned |
| PKI-028 | pki-and-trust-lifecycle-architecture, extensibility-architecture | ADR-0011, ADR-0023 | INV-PKI-024 | AT-PKI-021, FI-PKI-019 | Planned |
| PKI-029 | pki-and-trust-lifecycle-architecture, upgrade-and-compatibility-architecture | ADR-0021, ADR-0023 | INV-PKI-022 | AT-PKI-022, FI-PKI-017 | Planned |
| PKI-030 | pki-and-trust-lifecycle-architecture, ha-dr, data-persistence-architecture | ADR-0009, ADR-0018, ADR-0023 | INV-PKI-023 | AT-PKI-023, FI-PKI-018 | Planned |
| PKI-031 | pki-and-trust-lifecycle-architecture, security | ADR-0005, ADR-0023 | INV-PKI-025 | AT-PKI-024, FI-PKI-020 | Planned |
| PKI-032 | pki-and-trust-lifecycle-architecture, failure-model, fault-injection-matrix | ADR-0010, ADR-0023 | INV-PKI-008, INV-PKI-015, INV-PKI-016 | AT-PKI-025, FI-PKI-005, FI-PKI-009, FI-PKI-011, FI-PKI-018 | Planned |

## 19. Coverage Gate

Phase 0完了条件:

- 全Must requirementがArchitectureとInvariantまたはAcceptance Testへtraceされる。
- 重要ADRがAcceptedで、対応するtest contractがPlanned以上になる。
- `INV-*`に少なくとも一つの検証IDがある。
- 未trace、矛盾、廃止test IDをCIが検出する。

Developer Preview開始条件:

- 対象sliceのtestがImplemented。
- release blocker invariantがCIで常時実行される。
- 手動検証にはowner、手順、保存evidence、期限がある。
