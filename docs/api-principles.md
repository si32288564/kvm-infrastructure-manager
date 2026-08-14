# API 設計原則

- 状態: Baseline
- 更新日: 2026-08-14

## 1. API の役割

製品 UI、CLI、NFVO/VNFM、外部自動化は同じ versioned Northbound API を利用します。内部サービス API と公開 API は分離します。

## 2. 基本規約

- JSON over HTTPS
- OpenAPI 3.1 を唯一の機械可読契約とする
- base path は `/api/v1`
- resource name は複数形 kebab-case
- ID は URL path、filter は query、変更内容は request body に置く
- 日時は UTC の RFC 3339
- byte、MHz、count など単位を属性名または schema で明示する
- 未知フィールドを無条件に受理しない

## 3. リソース例

```text
/api/v1/sites
/api/v1/hosts
/api/v1/images
/api/v1/flavors
/api/v1/vms
/api/v1/networks
/api/v1/subnets
/api/v1/ports
/api/v1/routers
/api/v1/floating-ips
/api/v1/security-policies
/api/v1/volumes
/api/v1/volume-attachments
/api/v1/volume-snapshots
/api/v1/storage-classes
/api/v1/operations
/api/v1/alarms
```

ETSI IFA 005 対応 API は、内部 API と独立した adapter/profile として提供します。

## 4. 変更の完了境界

PostgreSQL resource authority の commit だけで desired mutation が完了し、Host/backend realization を伴わない resource は同期応答を許可します。Create は `201 Created`、Update は `200 OK` または `204 No Content`、同期 delete は `204 No Content` とします。Project はこの分類です。

Host/backend realization または時間を持つ convergence を伴う変更は `202 Accepted` と Operation resource を返します。

```http
POST /api/v1/vms
Idempotency-Key: 018f...
Content-Type: application/json
```

```json
{
  "operationId": "019c...",
  "resourceId": "019d...",
  "status": "QUEUED",
  "links": {
    "operation": "/api/v1/operations/019c...",
    "resource": "/api/v1/vms/019d..."
  }
}
```

## 5. 冪等性

- すべての create と action request で `Idempotency-Key` を受け付ける。
- key の scope は principal、project、method、canonical path とする。
- 同一 key、同一 payload は元の結果を返す。
- 同一 key、異なる payload は `409 Conflict` を返す。
- key の最低保持期間を API 契約で公開する。
- Agent command は Operation ID、step ID、generation で重複を除去する。

## 6. 並行更新

- 更新可能 resource は `ETag` を返す。
- 更新と削除は `If-Match` を要求する。
- stale generation は `412 Precondition Failed` とする。
- allocation claim などサーバー内部競合は再評価可能な `409 Conflict` とする。

## 7. エラー形式

RFC 9457 Problem Details を基礎に、安定した製品 error code を追加します。

```json
{
  "type": "https://docs.example.invalid/problems/quota-exceeded",
  "title": "Quota exceeded",
  "status": 409,
  "code": "KIM-QUOTA-001",
  "detail": "Requested vCPU exceeds the project limit.",
  "requestId": "019e...",
  "retryable": false
}
```

内部例外、秘密情報、backend credential は返しません。

## 8. 一覧と検索

- cursor-based pagination を使用する。
- stable sort key を必須とする。
- filter と field selection は OpenAPI で列挙する。
- Tenant ユーザーに存在を開示できない resource は `404` として扱う。

## 9. 互換性

- 同一 major version 内では既存フィールドを削除・再定義しない。
- enum 追加を想定し、client は未知値を安全に扱う。
- 廃止予定は response header、OpenAPI、release note に記載する。
- 最低2回の minor release または12か月の長い方を廃止猶予の初期案とする。
- API contract test と保存済み互換性 fixture を CI で実行する。

API versionはRelease Manifestのread/write rangeとFeature Gateへbindし、mixed-version中にold server/clientが意味を誤解するfield/enum/authority semanticsを有効化しません。Event/Agent/extensionを含む製品全体のcompatibilityとdeprecation/rollout gateは [Upgrade and Compatibility Architecture](upgrade-and-compatibility-architecture.md) に従います。

timestampはUTC/offsetと意味（source/received/verified/expiry）を区別し、必要なscopeではbounded clock quality/uncertaintyとserver-evaluated remaining durationを返します。client timestamp/countdownをLease、ordering、freshness authorityにしません。詳細は [Time and Clock Semantics Architecture](time-and-clock-semantics.md) に従います。

## 10. 認証・認可

- OAuth 2.0/OIDC access token を使用する。
- User/Northbound Service Principal credentialは外部Identity Platformが発行し、KIMはそのcredential authorityにならない。内部workload/Host transport certificateはPKI trust lifecycleの別境界とする。
- authorization は action、resource、scope、ownership、attribute で評価する。
- 認可失敗は actor、policy、resource ID、request ID とともに監査する。
- Console URL、image upload URL などは短寿命かつ一回用途を基本とする。

## 11. IaC、Provider、UI の共通契約

Terraform Provider、管理 UI、CLI、外部 automation は独自の resource lifecycle semantics を持ちません。OpenAPI に identity、revision、mutability、replacement、computed field、Operation、import、drift、delete protection 等の machine-readable metadata を組み合わせた KIM Resource Contract を共通入力とします。

Terraform state を KIM authority とせず、Placement、Materialization、Recovery、EVACUATE が変更する Host-local physical incarnation は desired configuration から分離します。詳細と current/proposed gap は [Infrastructure Lifecycle and IaC Architecture](infrastructure-lifecycle-iac-architecture.md) を参照します。

Migration 073–075 の Project、Flavor、SYSTEM Availability Policyは、同期的なPostgreSQL-only mutation、OIDC/RBAC、revision、idempotency、audit、OpenAPIを複数resourceで成立させたreference implementationです。Availabilityのpublic profileは`MANUAL`/`WORKLOAD_MANAGED`に閉じ、Policy revision更新は既存VMのexact Bindingをretrofitせず、CRUDはRecovery runtime authorityを生成しません。Imageはartifact ingestion/read-back producerがないためNorthbound endpointを持たずBLOCKEDです。実査結果は [KIM Northbound API / Terraform Readiness Review](reviews/kim-terraform-api-readiness-review-20260814.md) を参照します。
