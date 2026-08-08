# ADR-0016: NF側HAの分離意図をtransactional Failure Domain claimへ変換する

- 状態: Accepted
- 日付: 2026-08-09

## Context

WORKLOAD_MANAGEDなNF/VNFではactive/standby等をrack、power-feed等の相関障害domainへ分離する必要があります。単純なanti-affinity labelやscoreでは、並行Placementの競合、複数failure dimension、NFVO member correlation、driftをauthority付きで扱えません。一方、KIMがactive/standby role semanticsを解釈するとNFVO/VNFMの責任を侵食します。

## Decision

- Project scopeのversioned `WorkloadResilienceGroup`、stable `ResilienceMemberSlot`、`FailureDomainConstraint`を導入する。
- NFVO/VNFM roleはopaque metadataとし、KIMはVNF lifecycle/application healthを解釈しない。
- public dimension/level classから内部Failure Domain HostGroupへmapし、raw topologyを公開しない。
- rack、power-path等のdimensionを独立したhard constraintとして評価する。
- Final Admissionで`ResilienceDomainClaim`をHost/resource claimsと同じtransactionへcommitする。
- concurrent member placementでも同一domain制約を一方だけcommit可能にする。
- required member未充足はPENDINGとし、max-membersは増分強制、min-distinctはrequired set完成時に評価する。
- insufficient/UNKNOWN domain evidenceをsilent relaxせずineligibleにする。
- intent/domain driftで既存VMを暗黙migrationせず、VIOLATED/UNKNOWNとFault/Eventを記録する。
- Availability responsibilityを上書きせず、全responsibility branchのPlacement/Recoveryでconstraintを再利用する。

## Consequences

- NFVO/VNFMはVNF role authorityを保ったままKIMのFailure Domain admissionを利用できます。
- Resilience Group/member/constraint/domain claimとNorthbound mappingが必要になります。
- hard separationを満たせない場合はavailabilityより作成成功を優先せず、bounded failureになります。
- replacement時のmember slot/old resource ownershipを厳密に扱う必要があります。
