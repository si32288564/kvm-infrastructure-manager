# ADR-0005: Identityは外部Platformが所有する

- 状態: Accepted
- 日付: 2026-08-09

## Context

KIMにはTenant/Project単位のresource ownershipとauthorizationが必要ですが、OIDCを前提としながらUser、password、MFA、Northbound Service Principal Credentialまで所有するとIdentity Providerの責務を重複して持つことになります。Control Plane workload/Host Agentのtransport certificateはPrincipal credentialではなく、別のPKI trust lifecycleとして扱います。

## Decision

- User/Northbound Service Principal identity、credential lifecycle、password、MFA、federationは外部Identity Platformが所有する。
- KIMはissuerとsubjectでPrincipalを参照し、Tenant/Project Membership、Role Binding、Policy、Quotaを所有する。
- authenticationとKIM authorization decisionを分離する。
- KIMはUser/Northbound Service Principal Credentialを発行せず、外部Service IdentityをProject/Roleへbindする。
- 内部workload/Host transport certificateはPrincipal/Role authorityにせず、PKI Trust Domain/Profile/Bindingの別契約へ従う。

## Consequences

- NFV Platform全体で共通Identityを使用できます。
- KIM固有のUser databaseとcredential recovery機能は不要になります。
- 外部IdP障害、claim mapping、issuer migrationの契約が必要になります。
