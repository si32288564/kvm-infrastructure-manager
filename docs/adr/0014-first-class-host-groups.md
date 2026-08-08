# ADR-0014: Host Groupを第一級resourceとして扱う

- 状態: Accepted
- 日付: 2026-08-09

## Context

KIMにはHost Aggregate/AZ/traitの要求と、Placement、Baseline rollout、Maintenance、Failure Domainの個別概念があります。しかしGroup membershipのauthority、世代、型、snapshot、policy conflictを定義しないままtag/selectorで実装すると、同じHost集合が異なる意味で解釈され、実行中rolloutへのHost混入、stale placement、failure-domain誤認、暗黙のBaseline競合が起こり得ます。

## Decision

- System scopeの第一級`HostGroup`とversioned `HostGroupMembership`を導入する。
- `PLACEMENT_POOL`、`FAILURE_DOMAIN`、`OPERATIONAL_COHORT`を別typeとして効果を制限する。
- dimension/level/cardinality、explicit/selector/external membership source、同一dimension内hierarchyを明示する。
- selector resultはpure evaluation後にPostgreSQLへmaterializeして初めてauthorityとする。
- Placement dry/final admissionでmembership/policy/hierarchy generationを検証する。
- Baseline rolloutとMaintenance waveをimmutable Group Membership Snapshotへbindする。
- Profile/Baseline binding conflictを決定的に検出し、last-winsにしない。
- Groupはcapacity、Host capability、Compliance、Enrollment、Host Operation Authorityを所有・上書きしない。
- Tenantへはexposure policy付きPlacement Scopeだけを公開し、raw infrastructure groupingを隠す。

## Consequences

- Placement、rollout、maintenance、failure-domain分析を同じmembership authorityへ揃えられます。
- Group type/dimension、selector materialization、snapshot、binding resolver、lifecycleの実装が必要です。
- dynamic selectorの即時性より、再現可能なgenerationとfailure containmentを優先します。
- 既存Host Aggregate/AZ表現はHostGroup/Placement Scopeへmigrationする必要があります。
