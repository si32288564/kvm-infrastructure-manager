# ADR-0001: Control Plane と Host Agent を分離する

- 状態: Accepted
- 日付: 2026-08-08

## Context

KIM は複数 KVM Host を管理します。Control Plane が各 Host の remote libvirt へ直接接続する構成と、各 Host に Agent を置く構成が考えられます。製品には不安定な管理ネットワーク、段階的アップグレード、Host ごとの機能差、監査、最小権限への対応が必要です。

## Decision

各 Compute Host に KIM Host Agent を配置します。

- Agent はローカル libvirt Unix socket を使用する。
- Agent から Control Plane へ outbound mTLS channel を確立する。
- Control Plane は versioned command を送信する。
- Agent は inventory、heartbeat、observed state、result を報告する。
- command は idempotent とし、Host 上に実行 journal を保持する。
- 任意 shell と任意 libvirt XML の実行機能は提供しない。

## Consequences

利点:

- Host の libvirt port をネットワークへ公開する必要がない。
- Host 単位の権限、互換性、再試行を Agent で制御できる。
- 通信断中の局所的な状態取得と、復旧後の同期が容易になる。

コスト:

- Agent の配布、証明書、アップグレード互換性が必要になる。
- Control Plane と Agent のプロトコル互換性試験が必要になる。
- Control Plane の desired state と Host の observed state/journal を同期管理する必要がある。

## Alternatives

### Remote libvirt direct access

コンポーネント数は少ないものの、同期的 remote call、Host ごとの接続管理、libvirt endpoint の公開、権限分離が製品要件に適合しにくいため採用しません。

### SSH command execution

任意コマンド、credential 管理、監査、冪等性の境界が曖昧になるため採用しません。
