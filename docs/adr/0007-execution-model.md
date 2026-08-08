# ADR-0007: OperationとExecutionを分離する

- 状態: Accepted
- 日付: 2026-08-09

## Context

API利用者が追跡する長時間Operationと、Hostへのat-least-once配送は異なる状態とauthorityを持ちます。抽象的なStep/Retryだけではlease expiry、duplicate execution、stale result、outcome unknownを正確に表せません。

## Decision

- Operationの下にJobを置き、Host配送はimmutable Commandで表す。
- Command execution authorityを期限付きLeaseとしてPostgreSQLから発行する。
- 配送ごとにappend-only Attemptを作る。
- Attempt outcomeをSUCCEEDED、FAILED、UNKNOWNとする。
- Agentはwrite-before-execute journalでCommand IDを冪等化する。
- stale resultをHost identity、lease token、attempt index、authority generationでfencingする。
- 成功ResultはJobをverifyingへ進め、後続observationでのみsucceededとする。

## Consequences

- Execution failureの診断と安全な再配送が明確になります。
- Job/Command/Lease/Attemptのretention、API redaction、migrationが必要になります。
- transportを変更してもExecution semanticsを維持できます。
