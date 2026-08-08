# ADR-0006: Placementはdry evaluationとtransactional final admissionを分離する

- 状態: Proposed
- 日付: 2026-08-09

## Context

NUMA、HugePages、PCI/SR-IOV、network、storage、quotaを含むplacementでは、simulation時点と予約時点の間にcapacity競合が発生します。単純なfilter/weigh/claimでは、部分予約やstale選択を防ぐ契約が不足します。

## Decision

- Eligibility/AdmissionをScoringから分離する。
- dry admissionはpure evaluationであり、予約もDesired変更も行わない。
- eligible setだけをscoringし候補を選択する。
- 選択後、PostgreSQL transaction内でlatest authority stateに対するfinal admissionを再実行する。
- 全resource claim、Quota、Reservation、Desired、Jobを不可分にcommitする。
- 競合失敗は通常動作として残候補をreselectする。

## Consequences

- scoreが不適格性を上書きしません。
- dryとfinalで同じrule implementationを共有する必要があります。
- explanation、generation、再選択履歴を永続化する必要があります。
