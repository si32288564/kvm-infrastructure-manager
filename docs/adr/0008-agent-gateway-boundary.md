# ADR-0008: Agentを内部Message Busから分離する

- 状態: Accepted
- 日付: 2026-08-09

## Context

AgentがNATS JetStreamへ直接接続すると、Bus credential、subject authorization、consumer ownership、publish範囲、replayがHost Trust Boundaryの一部になります。一方、Agentはoutbound mTLS connectionを使う方針です。

## Decision

- NATS JetStreamはControl Plane内部のdurable messagingに限定する。
- AgentはAgent Gateway/Command Serviceへoutbound mTLS sessionを確立する。
- GatewayがBus wake-upとPostgreSQL Lease authorityをAgent protocolへ変換する。
- AgentはBus credentialを持たず、他Host/任意subjectへpublishできない。
- HTTPS long polling/stream等のtransportとJob/Command/Lease/Attempt semanticsを分離する。

## Consequences

- compromised Agentのblast radiusとcredential種類を抑えられます。
- GatewayのHA、connection scale、backpressureを設計・試験する必要があります。
- AgentのBus直接接続を将来採る場合は、このADRを置換する独立security decisionが必要です。
