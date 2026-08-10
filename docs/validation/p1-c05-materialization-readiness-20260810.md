# P1-C05 Materialization Readiness Validation

- 日付: 2026-08-10
- 状態: PASS / P1-C05 In Progress

## 1. Readiness authority

```text
immutable VM Materialization Plan
  -> typed VIRTUAL_MACHINE_DEFINE
  -> standard libvirt inactive Domain read-back
  -> immutable VM Definition Observation
  -> current component projection

Domain  = DEFINED
Storage = BOUND
Image   = PENDING
Network = PENDING
  -> boot readiness = BLOCKED
```

Domain define responseだけではprojectionを変更しません。PostgreSQL transactionはcurrent VM/Plan generation、Host、BOUND root Binding、RESERVED SINGLE_WRITER Attachment Claim、closed typed Command、`MATCHED` Verification、plan/observation/verifier digest、device/compute identity evidenceを再検証します。

## 2. Persistence model

- `vm_definition_observation_evidence`: immutable standard-libvirt read-back evidence
- `vm_materialization_readiness_current`: Domain/Image/Network/Storageとboot readinessのrebuildable current projection

accepted Domain evidenceはVM lifecycleを`DEFINED`へ進めますが、Image/Networkを暗黙に`REALIZED`へ変更しません。blocking reasonは`image_pending`と`network_pending`を保持します。

## 3. Validation

fresh PostgreSQL 17でmigration 001〜018とrace integrationを実行しました。

```text
TestMigratePostgreSQLIntegration
TestDryAndFinalPlacementAdmissionPostgreSQLIntegration
PASS
```

確認事項:

- current Plan/Binding/Claimと一致しないVerificationを拒否
- raw define responseだけではreadinessを進めない
- same evidence replayは冪等
- immutable evidence UPDATEを拒否
- Domainが`DEFINED`でもImage/Network `PENDING`を維持
- boot readinessは`BLOCKED`

## 4. Remaining work

- verified Image binary materialization evidence
- OVS/SR-IOV Network realization evidence
- 全 component generation を同一 transaction で再検証する READY transition
- READYだけを許可するtyped power-on dispatch gate
- stale Image/Network/Domain evidence fault injection
