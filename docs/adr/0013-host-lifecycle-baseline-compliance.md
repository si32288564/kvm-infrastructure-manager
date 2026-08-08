# ADR-0013: ZTPとContinuous ComplianceをHost Lifecycleへ統合する

- 状態: Proposed
- 日付: 2026-08-09

## Context

KIMにはHost Inventory、Capability、Preflight、typed remediationがありますが、Enrollment、Baseline、Compliance、Drift、Maintenance、Decommissionが一つのauthority modelとして未定義です。ZTPを認証直後の自動構成として実装すると、credentialをmutation authorityとして誤用し、汎用Configuration Managementへ責任が拡大する危険があります。

## Decision

- ZTPをZero Touch Enrollment + Continuous ComplianceとしてHost Lifecycleへ統合する。
- Host Profile、immutable versioned Host Baseline、Baseline Control、Baseline Assignment、Compliance Resultを第一級resourceとする。
- Lifecycle stateとCompliance statusを分離する。
- identity、Enrollment approval/Policy Match、Baseline Assignment、current preflightをauthority armingの前提とする。
- remediationをobserve-only、auto-remediate-safe、maintenance-required、external-remediationへ分類する。
- Critical違反/UNKNOWNをHostまたはcapability-scoped Placement Eligibilityへ反映する。
- Compliance history/evidenceをappend-onlyに保持し、typed verificationで収束する。
- 汎用package/config/kernel/reboot managementをKIMへ持ち込まない。

## Consequences

- Host参加から退役までauthority、evidence、placementの一貫したモデルを持てます。
- Enrollment Policy、Baseline schema/evaluator、rollout、maintenance、decommissionの実装・試験が必要になります。
- 外部Configuration Managementとの責任境界とevent/maintenance contractが必要になります。
- 自動化速度よりtrust、evidence、failure containmentを優先する場面があります。

