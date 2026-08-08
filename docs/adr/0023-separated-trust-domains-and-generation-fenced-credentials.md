# ADR-0023: Trust Domainを分離しcredentialをgenerationでfenceする

- 状態: Proposed
- 日付: 2026-08-09

## Context

KIMはControl Plane workload、Host Agent、backend adapter、external integrationへcredentialを必要とします。certificateを単なるTLS設定として扱うと、valid certificateをmutation authorityと誤認し、一つのCA compromiseが全domainへ波及し、renewal/revocation/DR restore後もold sessionがauthorityを保持する危険があります。offline環境ではTOFUやshared bootstrap secretへ退化しやすい問題もあります。

## Decision

- external Identity PlatformのUser/Service Principal authorityと、KIM workload/transport PKI trust lifecycleを分離する。
- Control Plane、Host Agent、External Integration、Backend Adapter、Artifact Verification、Data Protectionのtrust/key domainを用途別に分離する。
- Rootはoffline/external custodyを基本とし、purpose/Site別Intermediateとstrict Certificate Profileでissuanceを限定する。
- immutable TrustBundle、CertificateProfile、IssuerBinding、CredentialBinding、RevocationSet、monotonic trust generationを管理する。
- certificate validationとapplication authorizationを分離し、Enrollment/Role/Host authority/Command Leaseを別gateにする。
- Agent bootstrapをone-time material、hardware/Enrollment evidence、CSR proof-of-possessionへbindし、TOFU/shared credentialを禁止する。
- renewal/rekeyを新Credential Binding revisionとし、overlap中も一つのlogical identity/authority generationへmapする。
- revocationをintent/distribution/enforcement/verificationへ分け、session/authority generationを即時fenceする。
- Host/Control Plane/CA compromiseを別containment flowで扱い、certificate revokeだけをcompute/storage/DB/backend fencing proofにしない。
- normal CA rotationをdual trust/canary/batch/absence proofで行い、CA compromise時はindependent emergency authorityでold chainをdistrustする。
- private key valueをKIM DBへ保存せずSecret Provider/HSM/KMS custodyとopaque referenceを使用する。
- offline trust updateへsigned manifest、sequence、previous digest、expiryを要求しTOFU/trust rollbackを禁止する。
- PITR/DR後はrestore/trust generationでold session/Leaseをfenceし、old Site/issuer/credentialを外部再検証する。

## Consequences

- Trust Bundle/Profile/Binding/Revocation registry、issuance integration、session revalidation、Rollover Campaign controllerが必要になります。
- purpose/Site別intermediateとshort-lived credentialによりcertificate数と運用workflowが増えます。
- trust/revocation stateがUNKNOWNなscopeでは安全のためnew privileged operationが停止しますが、既存VMは維持されます。
- external CA、bundled subordinate CA、offline environmentを同じKIM trust contractへ統合できます。
- initial CA provider、certificate lifetime、revocation mechanism、offline update cadence、emergency recovery authorityを別途決定する必要があります。
