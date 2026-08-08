# ADR-0002: 非同期 Operation と reconciliation を採用する

- 状態: Proposed
- 日付: 2026-08-08

## Context

VM、Network、Volume の操作は複数 backend にまたがり、秒から分単位の時間を要します。API timeout、message 重複、Agent 再起動、backend で結果不明となる障害を扱う必要があります。

## Decision

- 書き込み API は desired state と Operation を永続化して `202 Accepted` を返す。
- Workflow が Operation を step に分解する。
- Reconciler が desired/observed 差分を継続的に収束させる。
- message delivery は at-least-once を前提にする。
- idempotency key、resource generation、command journal で重複を制御する。
- 結果不明の破壊的操作は自動で逆操作せず、状態照会または operator action を要求する。

## Consequences

利点:

- client は timeout 後も Operation を追跡できる。
- 一時的な backend 障害から自動回復できる。
- 障害解析に必要な step と相関関係を保持できる。

コスト:

- 状態機械、retry policy、compensation の設計が必要になる。
- 即時一貫性ではなく eventual consistency を UI と API 利用者へ明示する必要がある。
- Operation data の保持・削除ポリシーが必要になる。

