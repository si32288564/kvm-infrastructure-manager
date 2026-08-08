# ADR-0010: 不確実な障害状態を推測で確定しない

- 状態: Accepted
- 日付: 2026-08-09

## Context

分散したControl Plane、Agent、libvirt、Network、Storageでは、timeoutやpartition後にmutation結果を即座に証明できない場合があります。componentごとに異なるfallbackを実装すると、二重実行、二重attachment、誤削除、監査履歴の改変につながります。

## Decision

- 全componentがSystem-wide Failure Modelを使用する。
- timeout、Lease expiry、通信断を失敗または未実行の証明にしない。
- UNKNOWNを第一級outcomeとしてappend-onlyに保持する。
- stale identity/generation/Lease/Result/observationをfencingする。
- recoveryはtyped observation/read-back/verificationに限定する。
- 証明不能時はblocked/quarantinedを維持し、推測ベースの逆操作を行わない。

## Consequences

- 一時的にoperator actionを必要とするresourceが残る可能性があります。
- failure record、verification evidence、fault injection、runbookが必要になります。
- 可用性よりauthority safetyを優先するmutation境界が明確になります。

