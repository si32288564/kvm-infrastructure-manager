# API 設計原則

- 状態: Draft
- 更新日: 2026-08-09

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
/api/v1/volumes
/api/v1/operations
/api/v1/alarms
```

ETSI IFA 005 対応 API は、内部 API と独立した adapter/profile として提供します。

## 4. 非同期変更

時間のかかる変更は `202 Accepted` と Operation resource を返します。

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

## 10. 認証・認可

- OAuth 2.0/OIDC access token を使用する。
- User/Service credentialは外部Identity Platformが発行し、KIMはcredential authorityにならない。
- authorization は action、resource、scope、ownership、attribute で評価する。
- 認可失敗は actor、policy、resource ID、request ID とともに監査する。
- Console URL、image upload URL などは短寿命かつ一回用途を基本とする。
