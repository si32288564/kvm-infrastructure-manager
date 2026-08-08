# PKI and Trust Lifecycle Architecture

- 状態: Draft
- 更新日: 2026-08-09

## 1. 目的

KIMのAgent、Agent Gateway、Control Plane workload、adapter、external integrationは、TLS/mTLS、artifact verification、credential bindingを通じて相互に信頼を判断します。本書はbootstrap、issuance、renewal、overlap、revocation、distrust、compromise、emergency CA rollover、DR restore、offline environmentを含むtrust lifecycleを定義します。

本書の中心原則は次です。

> credentialはidentityと通信相手の証明材料であり、resource mutation authorityそのものではない。

有効な証明書を持つだけではRole Binding、Enrollment、Host Operation Authority、Command Lease、Placement、backend mutationは成立しません。

## 2. Responsibility Boundary

KIMが所有します。

- KIM trust domain、certificate profile、Trust Bundle、trust generation、distrust policy
- workload/Host identityとcertificate/key referenceのbinding
- Agent bootstrap、CSR proof-of-possession、issuance/renewal/rekey workflow
- Gateway/Control Plane session trust evaluationとsession fencing
- revocation intent、distribution state、freshness、propagation evidence
- compromise containment、credential quarantine、emergency rollover campaign
- offline trust bootstrap/update、restore後のcredential/session re-establishment
- PKI lifecycleのauthorization、audit、observability、test contract

KIMが所有しません。

- 人間User、Service Principal、password、MFA、OIDC federationのauthority
- customer enterprise Root CAやexternal Identity Platform/CAの一般運用
- HSM/KMS/Secret Providerの物理key custody、availability、backupそのもの
- Host OS、network appliance、Ceph、OVN、WIM等の汎用PKI全体
- artifact signing、Volume encryption、backup encryptionをTLS CAと同じkey hierarchyで所有すること

外部Identity PlatformはPrincipal/Credential authorityを持ち、KIMはissuer+subjectをMembership/Role Bindingへ関連付けます。workload certificateをexternal platform/CAが発行する場合も、KIMはaccepted issuer/profile、Credential Binding、trust generation、resource authorizationを所有します。

## 3. Trust Domains and Key Separation

用途とblast radiusが異なるcredentialを一つのCA/keyへ集約しません。初期trust domainは最低限次を分離します。

| Trust domain | Subject / purpose | Boundary |
|---|---|---|
| Control Plane Workload | API、controller、worker、Gateway、internal service mTLS | service identity/role別profile |
| Host Agent | enrolled Host AgentとGatewayのmTLS | Host identity、Site、Enrollment binding |
| External Integration | IdP、WIM、NFVO/VNFM、external remediation、webhook | explicit peer/issuer/profileごとのtrust |
| Backend Adapter | OVN、Ceph、Secret Provider等へのclient identity | backend/scopeごとのleast privilege |
| Artifact Verification | release/image/module署名検証 | TLS issuing CAとkey/rootを再利用しない |
| Data Protection | Volume/backup/archive encryption key | certificate issuance/trust generationと分離 |

cross-domain trustはexplicit `TrustRelationship`でsource/target、allowed profile、name constraint、purpose、expiry、approvalを宣言します。CA chainが技術的に検証できても、明示relationがなければtransitive trustを許可しません。

## 4. Root and Intermediate CA Model

- Root CAはofflineまたはcustomer/external trust serviceで保護し、日常issuanceへ直接使用しない。
- online issuanceはpurpose/Site/environmentごとに分離したIntermediate CAを基本とする。
- AgentとControl Plane workloadでissuing intermediateを分離し、一方のcompromiseを他方の無制限issuanceへ波及させない。
- path length、name constraints、SAN namespace、EKU/key usage、algorithm/size、maximum lifetimeをCertificate Profileで制限する。
- wildcard identity、任意SAN、CN-only identity、unbounded subordinate CAを許可しない。
- Root/Intermediate private keyはHSM/KMS/Secret Provider等のisolated custodyへ置き、KIM PostgreSQLへprivate key valueを保存しない。
- external CA利用時もissuer nameだけで信頼せず、anchor/intermediate fingerprint、profile、policy generationを固定する。

KIM bundled intermediateを提供する場合もcustomer Rootまたはoffline product bootstrap rootのsubordinateとして扱い、Root keyを通常Control Plane replicaへ配置しません。

## 5. Core Resources

```text
TrustDomain
├─ TrustBundle revision
├─ CertificateProfile
├─ IssuerBinding
├─ TrustRelationship
├─ RevocationSet
└─ TrustGeneration

CredentialBinding
├─ principal / workload / Host identity
├─ certificate fingerprint / issuer / serial
├─ public key or key reference / provenance
├─ profile / SAN / EKU
├─ not_before / not_after
├─ issued / renewed / revoked / distrusted state
├─ binding generation / trust generation
└─ session / enrollment references
```

### TrustBundle

immutable revisionとして次を保持します。

- accepted Root/Intermediate fingerprintsとchain constraints
- accepted certificate profiles、SAN namespaces、EKU
- issuer status、not-before/activation、retirement/distrust
- revocation source/sequence/freshness requirement
- trust domain/relation、minimum algorithm policy
- artifact digest、author/approver、audit

修正は新revisionで行い、過去Bundleを上書きしません。`trust_generation`はmonotonicに進め、session/credential evaluationへbindします。

### CertificateProfile

subject typeごとに、identity namespace、SAN形式、EKU、key provenance、algorithm、maximum lifetime、renewal/rekey、overlap、revocation freshness、attestation requirementを定義します。

certificateのCN、display name、source IPをidentity authorityにしません。Host Agentはstable Host identity/Enrollment、Control Plane workloadはservice/workload identityとdeployment scopeへbindします。

### CredentialBinding

certificateは外部/内部issuerが発行しても、KIM identityとのbinding decisionを別resourceとして保持します。Bindingはissuer/profile、fingerprint/public key、subject identity、Enrollment/Principal、trust generation、evidence digestへbindし、証明書再発行で過去Bindingを改変しません。

## 6. Trust Evaluation

TLS/application sessionを受理する前に最低限次を検証します。

1. chain/signature、accepted anchor/intermediate、current TrustBundle/generation
2. time validityと [Time and Clock Semantics Architecture](time-and-clock-semantics.md) のclock quality/uncertainty
3. Certificate Profile、EKU/key usage、SAN namespace/name constraint
4. Credential Bindingのsubject/Host/workload identity一致
5. revocation/distrust stateとrequired freshness/sequence
6. proof-of-possession、channel/session binding、protocol version
7. Enrollment/Principal/Role/Host state、authority generation等のapplication authorization

評価結果は`TrustDecision`としてsession、peer fingerprint、Bundle/profile/revocation generation、clock decision、bounded reason、evidence digestへbindします。

- certificate validation successはapplication authorization successではない。
- cached TrustDecisionはTrustBundle/revocation/clock/Binding generation変更でcurrent authorityを失う。
- unknown issuer/profile/revocation freshness/clockを`trusted`へ丸めない。
- trust validation不能時も既存VM/dataplaneを停止せず、新規session/privileged mutationをscope別にfail closedにする。

## 7. Agent Bootstrap and Issuance

Agent bootstrapは`UNTRUSTED_DISCOVERED -> BOOTSTRAP_AUTHENTICATED -> ENROLLMENT_PENDING -> ENROLLED -> CREDENTIAL_ACTIVE`を分離します。

### Bootstrap Material

bootstrap materialは一回用途、短寿命、Site/expected Host/policy scope、nonce/challenge、maximum usesへbindします。次を禁止します。

- shared factory password/API keyの無期限利用
- bootstrap tokenだけでEnrollment/READY/armedへ進むこと
- TOFUだけでRoot/Agent identityを確定すること
- Agent自己申告Host ID/MAC/hostnameだけへのissuance

### CSR and Proof of Possession

- Agent/workload側でkey pairを生成し、private keyをControl Plane/DBへ送らない。
- CSRはbootstrap/enrollment request、Host hardware identity evidence、challenge、profile、key provenanceへbindする。
- issuerはproof-of-possession、Enrollment decision、current policy/generation、SAN/EKU constraintsを検証する。
- TPM/HSM-backed keyはprofileで要求可能だが、software keyと同じprovenanceに偽装しない。
- issuance response lossでは同じrequest/key/CSR digestをread-backし、別identity/certificateをblind発行しない。

証明書発行後もHostはcurrent Baseline、Preflight、Compliance、Host Operation Authorityを満たすまでCommandを取得できません。

## 8. Renewal, Rekey, and Overlap

renewalは既存certificateの期限延長ではなく、新しいCredential Binding revisionです。

- current identity、Enrollment/Principal status、profile、trust generation、proof-of-possessionを再検証する。
- renewalとkey rotationを区別し、policyにより定期rekeyまたはkey compromise時のmandatory rekeyを要求する。
- old/new certificateのoverlap windowを明示し、transport continuityを確保する。
- overlap中に二つのHost/worker authorityを作らず、両certificateを同じlogical identityと一つのcurrent session/Host authority generationへmapする。
- new certificateで新sessionを確立し、current TrustDecision/Bindingを確認後にold sessionをdrainする。
- renewal response lossはissued certificate/Bindingをrequest digestでread-backし、duplicate issuanceをboundedに処理する。
- expiry/renewal failureだけで既存VMを停止しない。Hostはunarmed/disconnectedとなり、新規mutationを停止する。

certificate overlapはrevocation/distrustを遅らせる理由になりません。compromise時はold/new両Bindingとactive sessionをscope付きでfenceします。

## 9. Session Lifecycle and Fencing

`AuthenticatedSession`は最低限次へbindします。

- local/peer workload or Host identity
- certificate fingerprint/Credential Binding revision
- TrustBundle/revocation/trust generation
- protocol/capability generation
- established/maximum lifetime/last revalidation
- Host/Control Plane authority generation

long-lived TLS connectionをcertificate expiry/revocation確認の迂回路にしません。

- session maximum lifetimeとperiodic trust revalidationをprofileで定義する。
- TrustBundle/revocation/Binding/Host authority generation変更時にsessionをdrain/terminateする。
- renewed certificateはexisting TLS sessionのpeer identityを遡及変更せず、新sessionを要求する。
- stale sessionからのCommand取得、Result、Inventory、internal service mutationをgenerationで拒否する。
- session closeはpeer process停止、Host fencing、backend I/O停止の証明ではない。

## 10. Revocation and Distrust

revocationを単一CRL/OCSP応答ではなく、intent、distribution、enforcement、verificationへ分離します。

```text
REVOKE_REQUESTED
  -> LOCAL_ENFORCED
  -> DISTRIBUTING
  -> PROPAGATION_VERIFIED
  -> REVOKED
```

- KIMはCredential Bindingをcurrent trust generationでdenyし、Gateway/API/internal endpointのnew sessionを即時拒否する。
- active sessionとrelated Host/service authority generationをfenceする。
- issuer側CRL/OCSP/deny registry updateをtyped integrationで要求し、sequence/freshness/receiptを観測する。
- distribution未確認を`REVOKED`完了へ丸めない。
- offline node向けsigned revocation updateはsequence、previous digest、expiry、TrustBundle generationを持つ。
- short-lived certificateはrisk windowを減らすがrevocationを代替しない。
- revocation stateがstale/UNKNOWNならprofileに応じて新規privileged sessionをfail closedにする。

`distrust`はcertificate単体でなくissuer、intermediate、algorithm、profile、namespace全体を拒否できます。distrust対象を別chainやcached sessionへsilent fallbackしません。

## 11. Compromised Host Identity

Host key/certificate/Agent compromiseが疑われる場合、次を分離して実行します。

1. Host Operation Authorityをdisarmし、新Command/Leaseを停止する。
2. Credential Bindingをrevoke/quarantineし、Gateway session/trust generationをfenceする。
3. Inventory/Result/Hardware Evidenceをuntrusted provenanceとして隔離する。
4. affected resource/Attachment/Port/PCI/storage ownershipを`UNKNOWN`を含めて観測する。
5. Availability Policyに従い、必要なcompute/storage/network fencing後だけmanaged recoveryを判断する。
6. hardware identityを再証明し、new key/credentialで明示re-enrollmentする。

certificate revocation、Gateway disconnect、heartbeat lossだけでHostやstorage clientがfencedされたとみなしません。WORKLOAD_MANAGED workloadへKIMが自動replacementを作ることもありません。

同じhardware identityへnew credentialを発行しても、old Host authority/Lease/Result/Attachment generationを復活させません。

## 12. Compromised Control Plane Identity

Control Plane workload compromiseではcertificate失効だけでは不十分です。次を一つの`TrustIncident`/containment planとして扱います。

- workload certificate/Credential Bindingとactive sessionのrevocation/fencing
- service discovery/load balancer/endpointからの除外
- database role/session、Message Bus、Secret Provider/backend credentialの個別rotation/revocation
- leader/worker/publisher/Upgrade/GC/Command Leaseとauthority generationのfencing
- issued Command/Outbox/Inbox/Attempt/Auditのscope付きreviewとUNKNOWN classification
- clean artifact/provenanceからのworkload再構築、new identity、canary rejoin

一つのcredential rotationを他credential、DB authority、published Commandのfencing proofとして代用しません。既存VMは維持し、影響scopeのnew mutationを停止します。

## 13. CA Compromise and Emergency Rollover

通常rotationは次のtwo-phase trust transitionを用います。

```text
Prepare new anchor/intermediate
  -> publish dual TrustBundle
  -> verify distribution receipts
  -> issue/rekey canary
  -> batch reissue and new sessions
  -> switch issuance/current generation
  -> distrust old issuer
  -> remove old anchor after absence proof
```

`TrustRolloverCampaign`はsource/target Bundle、issuer、scope、wave、max unavailable、minimum ready、deadline、revocation/rollback policy、receiptsを永続化します。

issuer/Root compromiseが疑われる場合、compromised chainで署名されたrollover instruction自体を信頼しません。

- independent out-of-band recovery authorityとtwo-person approvalでnew anchorをauthorizeする。
- old issuerをimmediate distrustし、old chainによるnew certificate/Bundleを拒否する。
- affected scopeを`TRUST_RECOVERY`へ移し、新規privileged mutationを停止する。
- identityをhardware/workload deployment evidenceから再証明しnew chainへreissueする。
- old/new trustの重複期間を通常rotationより優先せず、compromise policyに従い縮小/禁止する。

Root removalやdistrust後の自動rollbackは行いません。old anchorを再信頼するには新しいindependent approval/TrustBundle generationを要求します。

## 14. Secret Provider and Key Custody

Secret Providerはprivate key、CA key、backend credential等の値とcryptographic operation/custodyを提供できます。KIM DBはopaque reference、provider identity、key version/public fingerprint、purpose、status、generationだけを保持します。

- application log/Event/Command/diagnostic/backupへprivate key/secret valueを保存しない。
- Secret Provider credentialをAgent、extension、Tenantへ渡さない。
- key export可能/不可、HSM-backed、backup/recovery、rotation capabilityをprofileへ明示する。
- Secret Provider completion claimだけでcertificate active/revoked/rotatedを確定せず、public certificate/TrustBundle/sessionを検証する。
- Secret Provider outage時はnew issuance/renewal/signingを停止するが、既存VMを停止しない。

Volume/backup encryption key lifecycleとPKI private key lifecycleは同じproviderを利用しても別policy、permission、audit、rotation campaignを持ちます。

## 15. Offline and Air-gapped Bootstrap

offline環境でもTOFUや固定shared secretへ退化しません。

- installation bundleへTrust Bootstrap Manifest、Root/Intermediate public certificate/fingerprint、profile、artifact signature、sequence、expiryを含める。
- bootstrap trust anchorはindependent physical/organizational channelで照合可能にする。
- one-time bootstrap materialをSite/Host/policyへbindし、使用済みnonce/receiptをdurableに保持する。
- trust/revocation updateはsigned full/delta bundle、monotonic sequence、previous digest、validity、approvalを持つ。
- old/replayed Bundleでtrust generationをrollbackしない。
- offline intervalがrevocation/certificate/profile freshness上限を超える場合、new enrollment/privileged mutationをfail closedにする。
- emergency recovery bundleは通常bundleと別permission/two-person approval/auditを要求する。

## 16. HA, Backup, DR, and Restore

CA/Secret Provider key backupは通常KIM PostgreSQL backupと分離します。DB backupへprivate keyを含めません。

PITR/DR restore後:

- new restore epoch/database authority generationでpre-restore session、Lease、worker claimをfenceする。
- restored certificate rowが時間上有効でもsession/mutation authorityを復活させない。
- old SiteのCA、Gateway、Control Plane endpoint、service/backend credentialをexternal fencingする。
- new Site workloadはnew Credential Binding/sessionを取得し、old Site identityをcloneしない。
- Agent certificateを継続受理する場合もnew Gateway session、current TrustBundle/revocation、restore/Host authority generationを再検証する。
- revocation/TrustBundle sequenceがbackup pointより進んでいた可能性を`UNKNOWN`として外部issuer/registryから再取得する。
- old Site/issuer fencingまたはcurrent revocation stateを証明できなければ`RECOVERY_READ_ONLY/TRUST_RECOVERY`を維持する。

restore epochはcertificate revocationやold Site停止の証明を代替しません。

## 17. API and Authorization

operator APIは最低限次を扱います。

- Trust Domain/Bundle/Profile/Issuer/Relationshipの照会とversioned publish
- Credential Binding/Certificate status/expiry/renewal/revocation
- Trust Rollover Campaign、distribution receipt、blocked/UNKNOWN target
- Trust Incident、quarantine、emergency recovery decision

権限を分離します。

- trust bundle/profile publish
- issuance/renewal operator override
- credential/issuer revoke/distrust
- Root/intermediate rollover
- emergency trust recovery/break-glass
- Secret Provider/key reference administration

Root/issuer distrust、emergency anchor追加、CA key restore、force credential issuanceへtwo-person approvalを必須化できます。Tenant/Agent/adapterはtrust policy、issuer、Binding、revocation statusを変更できません。

Tenant APIへ内部CA topology、serial/fingerprint、Host/service identity、revocation detailを公開せず、必要なservice/Host trust statusとbounded reasonだけを返します。

## 18. Failure Semantics

| Failure | Containment / recovery |
|---|---|
| issuer/Secret Provider unavailable | new issuance/renewal停止、existing workload維持 |
| issuance response loss | CSR/request digestでBinding/certificate read-back、blind reissue禁止 |
| revocation registry stale/UNKNOWN | profile scopeのnew privileged session fail closed |
| TrustBundle distribution partial | campaign pause、old/new accepted範囲をtarget別に保持、remove禁止 |
| certificate expired | new session/Command停止、既存side effect不在とはみなさない |
| active session after revoke | trust/session generation fence、endpoint terminate、scope audit |
| Host key compromise | Host disarm、credential/session revoke、resource ownership observe/fence |
| Control Plane key compromise | identity/DB/Bus/backend/Leaseを別々にfence、clean rejoin |
| Intermediate/Root compromise | independent emergency authority、distrust、TRUST_RECOVERY、reissue |
| offline Bundle replay | sequence/previous digest/generationで拒否 |
| DR restore with old credential | restore/trust generationでsession fence、external trust state再取得 |

timeout、certificate expiry、session close、revocation requestはpeer process/Host/backend side effectの停止証明ではありません。

## 19. Observability and Audit

最低限、次を公開します。

- Trust Domain/Bundle/current generation、issuer/profile health
- active/expiring/renewing/revoking/quarantined Credential Binding数
- certificate lifetime/renewal lead time、session age/revalidation
- revocation sequence/freshness/distribution lag/receipt
- Trust Rollover Campaign wave、max unavailable、blocked/UNKNOWN
- unknown issuer/profile、SAN/EKU mismatch、clock/revocation validation failure
- compromised identity containment、stale session/Lease rejection
- offline Bundle sequence/age、DR trust recovery status

metric labelや一般logへfull certificate、serial、fingerprint、SAN、subject、secret referenceを不要に露出しません。security auditはactor、action、scope、Bundle/Binding generation、certificate/CSR digest、approval、decision/resultを改ざん検知可能に保持します。

## 20. Verification Contract

最低限、次を自動試験またはrelease evidenceとして保存します。

- Root/Intermediate/profile/name constraint/EKU/SAN/algorithm validation。
- Agent one-time bootstrap、hardware evidence/Enrollment binding、CSR proof-of-possession。
- issuance/renewal response loss、duplicate request、rekey、overlap、新session/old session drain。
- expired/not-yet-valid certificate、clock uncertainty、revocation freshness。
- CRL/OCSP/deny registry update loss/rollback、offline signed update replay。
- active session/Command/Result after credential revoke/trust generation change。
- compromised Host identityとcompute/storage/network fencingの分離。
- compromised Control Plane identityとDB/Bus/backend/Lease containment。
- normal Root/Intermediate rotation、partial distribution、canary/batch、old anchor removal guard。
- CA compromise時のindependent emergency rolloverとold chain distrust。
- Secret Provider outage/key reference conflict、private key non-export/non-disclosure。
- PITR/DR後のold Site/session/credential fencing、revocation sequence再取得。
- offline bootstrap、trust anchor verification、Bundle expiry/sequence/delta chain。
- user/service Identity PlatformとKIM workload PKI authority境界。

## 21. 禁止事項

- certificate validityだけでRole、Enrollment、Host authority、Command Leaseを成立させる。
- Root private keyを通常Control Plane/Agent/DBへ配置する。
- Agent、Control Plane、artifact signing、Volume encryptionで同一CA/key hierarchyを無制限共有する。
- CN、source IP、hostname、self-reported Host IDだけをcertificate identity authorityにする。
- bootstrap tokenを通常credentialまたはmutation authorityとして再利用する。
- private keyをCSR/API/Event/Command/log/diagnostic/通常DB backupへ含める。
- revocation requestをpeer/Host/storage/client fencing完了とみなす。
- short-lived certificateを理由にrevocation/distrust/session revalidationを省略する。
- trust/revocationがUNKNOWNなとき別issuer、cached chain、old Bundleへsilent fallbackする。
- compromised issuer自身の署名だけでemergency rolloverを承認する。
- offline/DRを理由にTOFU、shared default secret、trust generation rollbackを許可する。
- certificate rotation/clock復旧/Gateway reconnectだけでHost authorityを自動rearmする。
