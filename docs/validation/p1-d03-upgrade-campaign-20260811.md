# P1-D03 Product Upgrade Campaign Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-031`, `FI-UPG-021`
- Invariant: `INV-UPG-024`
- Runtime: PostgreSQL 17、Go race detector

## Scope

worker単体の release bindingから product-wide rollout authorityへ拡張し、次を同じ PostgreSQL authority pathで検証した。

```text
Release Manifest
→ immutable Campaign Plan
→ acyclic Component Graph
→ verified Provenance / SBOM binding
→ ordered Wave / Target Snapshot
→ Coordinator Claim
→ Target Result
→ immutable Canary Decision
→ next Wave or PAUSED
```

Planのcomponent setは `API / AGENT_GATEWAY / CONTROL_WORKER / OVN_RUNTIME_WORKER / HOST_AGENT` を含む。各Target artifact digestはverified provenance snapshotへ存在することを要求した。

## Coordinator Recovery Fault

```text
Coordinator A claim generation 1
→ API Target SUCCEEDED commit
→ Result response lossを同一digest replay
→ canary HOLD（2 Target pending）
→ claim expiry
→ Coordinator B claim generation 2 / RECOVER_FROM_DB
→ Aからstale Result
→ rejected
→ Bがremaining Target Resultをcommit
→ canary CONTINUE
→ current Wave = batch-1
```

claim expiryはTarget side effect不在とせず、`COORDINATOR_UNKNOWN` evidenceを追記した。既 accepted Resultの同一digest replayはTarget Attempt/Eventを増やさず、異 owner / old generationをcurrent authorityへ進めなかった。

## Canary Decisions

| Campaign | Canary evidence | Decision | Current state |
|---|---:|---|---|
| pass | success 3、failure 0、unknown 0、pending 0 | `CONTINUE` | `ROLLING / batch-1` |
| pause | success 1、failure 1、threshold 0 | `PAUSE` | `PAUSED / canary` |

cycleを持つcomponent graphはPlan publish前に拒否した。immutable PlanへのUPDATEもDB triggerで拒否した。`PAUSED` Campaignでもcurrent Coordinator claimが有効な間はPlan revision 2への切替を拒否し、revision 2のPlan/Wave/Target evidence全体をtransaction rollbackした。

## Evidence Summary

- Plan: 2
- Wave: 4
- Target: 7
- Canary Decision: 3（`HOLD / CONTINUE / PAUSE`）
- pass Campaign `COORDINATOR_UNKNOWN`: 1
- pass Campaign accepted Target Result Event: 3
- Coordinator B mode / generation: `RECOVER_FROM_DB / 2`

## Command

```bash
make test-p1d03-upgrade-campaign
```

## Boundary

この増分はCampaign authority、coordinator fencing、canary Decision foundationを対象とする。実component artifact activation、Wave `max_unavailable` とserving budgetの統合、Campaign abort / rollback executor、multi-replica coordinator process、long soakは後続gateである。
