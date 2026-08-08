# ADR-0004: ハイパーバイザー OS の差異をホスト側で吸収する

- 状態: Proposed
- 日付: 2026-08-08

## Context

KIM は、特定ベンダーまたは特定 Linux ディストリビューションに固定されない KVM Infrastructure Manager を目指します。KVM/libvirt を提供する一般的な Linux であっても、package、service manager、filesystem layout、SELinux/AppArmor、network、storage、Host tuning には差があります。

これらの差を Control Plane に持ち込むと、Scheduler、Workflow、API が OS ごとの条件分岐を持ち、機能追加と検証の組合せが急増します。

## Decision

- OS 固有の差異はホスト側コンポーネント（仮称 Agent）内の OS Integration Adapter が吸収する。
- Control Plane と Agent protocol は OS 非依存の command、capability、desired/observed state のみを扱う。
- Scheduler は distribution 名ではなく、正規化された capability、trait、constraint で判断する。
- Virtualization、Network、Storage、OS Integration を Agent 内で別 adapter とする。
- Agent は登録前または有効化前に preflight を実行し、必須 capability と remediation hint を報告する。
- 新しい Linux 対応のために Control Plane へ OS 名の条件分岐を追加しない。
- Agent という名称は仮称であり、製品上の正式名称は別途決定する。

## Support Policy

技術的互換性と商用サポート範囲を区別します。

- Validated: リリース認定試験済みでサポート対象。
- Compatible: capability を満たし動作可能だが、その組合せとして未認定。
- Unsupported: 必須 capability の不足または既知の非互換がある。

リリースごとに OS、kernel、QEMU、libvirt、OVN/OVS、Ceph client の組合せをサポートマトリクスとして公開します。

## Consequences

利点:

- 顧客が既存の標準 Linux をハイパーバイザー OS として採用しやすい。
- Control Plane の複雑性と OS 依存テストを抑制できる。
- OS の差異を Agent の preflight、diagnostic、adapter test に局所化できる。
- 将来のディストリビューション追加を独立して進められる。

コスト:

- adapter contract とディストリビューション別 CI 環境が必要になる。
- kernel/libvirt/QEMU の組合せを継続的に検証する必要がある。
- Compatible 環境と Validated 環境の違いを、顧客へ明確に説明する必要がある。

## Guardrails

- adapter が任意 shell 文字列を組み立てる設計を避け、型付き操作を使用する。
- OS 検出結果だけで capability を推測せず、実機能を probe する。
- 未知の OS は黙って既知 OS として扱わず、明示的に Compatible/Unsupported を判定する。
- セキュリティ機構を無効化して互換性を得る実装を標準動作にしない。

