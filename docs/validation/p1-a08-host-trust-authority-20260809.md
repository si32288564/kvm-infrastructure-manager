# P1-A08 Host Trust and Authority Validation

## 1. Scope

P1-A07 の Host capability evidence を、次の trust/authority chain へ接続した。

```text
Host Identity
  → immutable Enrollment Decision / current Enrollment Binding
  → immutable Credential Binding Evidence / current Credential Binding
  → PostgreSQL current Session Generation
  → current Capability Projection
  → Session Authorization
  → Readiness Gates
  → explicit Host Operation Authority arming
```

`mTLS valid`、`Credential Binding current`、`Session current`、`Capability current` は Session Authorization の入力であり、mutation authority の代替ではない。

## 2. Persistence Separation

migration 007 で以下を追加した。

- `host_enrollment_decisions`: immutable manual/policy decision evidence
- `host_enrollment_bindings_current`: current Enrollment state
- `agent_credential_binding_evidence`: certificate/public key/issuer/profile/trust/Enrollment binding evidence
- `agent_credential_bindings_current`: current credential revision
- `host_session_authorizations_current`: Enrollment/Credential/session/capability binding decision
- `host_readiness_gates_current`: Baseline/preflight/Compliance/capability gate projection
- `host_operation_authorities_current`: explicit mutation authority generation
- `host_operation_authority_events`: append-only arm/fence evidence

Gateway は `SessionHello` の Host/revision と、verified peer certificate fingerprint、current Enrollment/Credential Binding を同じ PostgreSQL admission transaction で検証する。証明書が有効でも Binding 不一致なら session を grant しない。

## 3. Non-Rearm Integration Fixture

Fresh PostgreSQL 17 integration で次を確認した。

```text
DISCOVERED + valid-looking session                 → rejected
Enrollment APPROVED + Credential current           → session granted
Capability absent                                  → PENDING_CAPABILITY
Capability current                                 → AUTHORIZED, authority absent
Readiness READY                                    → authority still absent
explicit arm                                       → generation 1 ARMED
credential renewal                                 → session STALE / authority FENCED generation 1
new credential session                             → AUTHORIZED / still FENCED
explicit arm                                       → generation 2 ARMED
same-credential reconnect                          → authority FENCED generation 2
capability generation update                       → AUTHORIZED new capability / authority remains FENCED
Compliance NON_COMPLIANT                           → authority FENCED, arm rejected
Enrollment quarantine/reapproval + new credential  → session recoverable / authority remains FENCED
```

Host authority generation は explicit arming のときだけ増加する。fence、renewal、reconnect、Inventory、Compliance、Enrollment transition は既存 generation を維持する。

## 4. SR-IOV Regression

P1-A08 の認証・Session Authorization・Host arming を追加しても、PCI/SR-IOV の Qualification authority は独立している。実 hardware Qualification Evidence がない VF は、Host session/authority が current でも `BLOCKED` のままとする。既存 PCI integration fixture で Observed-only VF の claim 拒否を継続検証する。

## 5. Remaining Work

- one-time bootstrap、CSR proof-of-possession、issuance response-loss read-back
- production TrustBundle/Certificate Profile/Trust Generation verifier
- Baseline Assignment と immutable Compliance Result の正式 resource model
- Host Authority と P1-B Command/Lease dispatch の不可分 authorization
- revocation propagation、certificate overlap drain、offline bootstrap qualification
