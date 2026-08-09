# P1-B12 Remote KVM Full-process Convergence Validation

日付: 2026-08-09

## Scope

Mac 側 Control Plane process と指定 remote KVM Host を、実 TLS、PostgreSQL、JetStream、gRPC、SSH process boundary、標準 libvirt `qemu:///system` で接続した。

- KVM Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- Host Agent: remote Linux 上で `-tags libvirt` build
- Control Plane: PostgreSQL 17、`kim-worker`、3-node TLS/JWT JetStream、`kim-agent-gateway`
- Backend target: 64 MiB / 1 vCPU の KIM 専用一時 KVM Domain
- 既存 VM: 変更なし

```text
PostgreSQL Lease / Attempt
  ↓ protected Outbox
Worker → 3-node JetStream → Gateway
  ↓ current gRPC session generation 7
remote Host Agent
  ↓ write-before-execute journal
standard libvirt Domain start
  ↓ Result path loss + Agent SIGKILL
Lease expiry → Attempt UNKNOWN
  ↓ Agent restart / generation 8
old Result replay → durable STALE fence
  ↓ same current session
durable Verification delivery
  ↓ libvirt UUID/state read-back
MATCHED Verification → same Job SUCCEEDED
```

## Fault boundaries

Agent-to-Gateway Result path は TLS を終端・解釈しない qualification-only proxy で遮断した。Domain が `running` になり Result path loss evidence が記録された後、remote Agent PID を `SIGKILL` した。

Receipt response-loss fixture は TLS record と application message の境界を同一視しない。qualification-only PostgreSQL advisory-lock trigger で Result/Observation/Receipt transaction を停止し、その後に opaque downstream loss を arm して commit response だけを遮断する。

## Results

| Case | Result |
|---|---|
| Mac → remote KVM mTLS/gRPC session | PASS |
| remote standard libvirt backend build/connect | PASS |
| typed `vm:<UUID>` RUNNING Command | PASS |
| write-before-execute journal before Domain start | PASS |
| Domain survives Agent `SIGKILL` | PASS |
| Result absence + Lease expiry → UNKNOWN | PASS |
| Attempt UNKNOWN history remains append-only | PASS |
| restart proposes next accepted session generation | PASS |
| old Result cannot change authority | PASS |
| durable `STALE` Receipt does not release spool evidence | PASS |
| `STALE` Receipt does not terminate current session | PASS |
| durable Verification Outbox/Inbox/current-session route | PASS |
| new libvirt connection UUID/state read-back | PASS |
| single `MATCHED` Verification | PASS |
| single Lease / single Attempt | PASS |
| same Job reaches `SUCCEEDED` without rearm or redispatch | PASS |
| temporary Domain/source/state cleanup | PASS |
| pre-existing KVM VMs unchanged | PASS |

実行結果:

```text
--- PASS: TestFullProcessCommandDeliveryFaultCampaign (29.21s)
PASS
```

## Additional defects found and contained

1. journal のない valid Verification Request が module error となり session reconnect loop を起こしていた。STARTED evidence を生成せず typed `UNKNOWN / journal_state=ABSENT` observation を返すよう修正した。
2. expired Attempt の旧 Result replay が durable Receipt を返さず stream error になっていた。append-only stale-result evidence と `STALE` Receipt を commit するよう修正した。
3. non-`ACCEPTED` Receipt が Spool handler error となり current session を終了していた。spool evidence は保持しつつ session と後続 Verification を継続するよう修正した。
4. JetStream stream 作成直後の durable consumer provisioning race を bounded retry へ変更した。

## Remaining

- disk-full、fsync latency、corrupt journal quarantine の qualification
- retained STALE spool evidence の operator-visible quarantine/retention workflow
- production Secret Provider rotation
