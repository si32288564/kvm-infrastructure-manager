# ADR-0029: Northbound resource mutation の完了境界を authority で分ける

- 状態: Accepted
- 日付: 2026-08-14

## Context

ADR-0002 と従来の `AT-API-001` は、時間のかかる backend mutation を request 内で実行せず `202 Accepted` と Operation で収束させます。一方、Project の desired state は PostgreSQL authority transaction の commit だけで完了し、Agent や backend realization を持ちません。全 mutation に形式上の Operation を作ると、完了していない実作業があるという誤った lifecycle を API、Terraform、UI に伝えます。

## Decision

Northbound mutation を次の二種類に分けます。

1. PostgreSQL resource authority commit だけで完了する mutation は同期 resource response を返します。Create は `201`、Update は `200` または `204`、Delete は resource contract に従う `200`/`204` です。
2. Host/backend realization または時間を持つ convergence を伴う mutation は `202` と stable Operation を返し、terminal evidence を別に確認します。

Project Phase 0 vertical slice は第1分類です。Project の resource revision、idempotency record、current projection、immutable revision evidence、audit は同じ transaction boundary で確定します。HTTP request 内では Host/backend に接続しません。

## Consequences

- Terraform は Project の成功応答を確定した remote desired state として扱えます。
- VM、Network、Volume 等は PostgreSQL desired row を作っただけで同期成功を返せません。
- resource ごとの OpenAPI lifecycle metadata は mutation が synchronous か Operation-backed かを宣言します。
- Operation を監査用 wrapper や HTTP request log の代用にしません。
- ADR-0002 の read-back-first、UNKNOWN、reconciliation の原則は backend-convergent mutation に引き続き適用します。

## Alternatives

- 全 mutation を `202` にする案は、Project に存在しない convergence と terminal authority を捏造するため採用しません。
- backend mutation を HTTP request 内で待つ案は、response loss と UNKNOWN を安全に扱えないため採用しません。

## Qualification

Migration 073 の Project reference implementation と `AT-IAC-012` が同期分類を検証します。将来の最初の backend-convergent Northbound resource は、別途 `202` + Operation contract を qualification しなければなりません。
