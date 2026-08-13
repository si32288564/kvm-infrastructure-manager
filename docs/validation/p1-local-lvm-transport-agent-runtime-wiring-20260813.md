# P1 Local LVM transport normal Host Agent runtime wiring — 2026-08-13

## Outcome

Migration 071のimmutable cross-Host authorityを、通常`kim-host-agent` startup/session lifecycleとtyped execution registryへ接続した。Migration追加はない。source/destinationは独立runtime/resolverで、real loopback TCP、HTTP/2、TLS 1.3 mutual TLSを通して16 KiBのguest-like marker contentを転送し、destination flushと両whole-volume SHA-256 read-backが`MATCHED`になった。

```text
PrepareLocalLVMTransportSession (PostgreSQL 071 authority)
→ PrepareLocalLVMTransportRuntimeCommands
→ normal Gateway/Agent Command delivery schemas
→ Source Agent LOCAL_LVM_TRANSPORT_SOURCE_AUTHORIZE
→ current-session-only source route
→ Destination Agent LOCAL_LVM_CROSS_HOST_TRANSPORT_START
→ administrator endpoint registry (source Host ID only)
→ dedicated TLS 1.3 mTLS HTTP/2 listener
→ independent source ReadAt / destination WriteAt+Flush+ReadAt
→ normal typed Result / Observation
→ Result LOST qualification
→ same Attempt journal + Verification Request
→ destination read-back MATCHED
→ existing Migration 071 peer observations/Transport Terminal
→ Migration 070 Copy Verification / PRESERVED_ROOT
→ EVACUATE child/parent / Migration 072 cleanup and capacity reclaim regression
```

## Product runtime components

| Role | Product component | Bounded authority |
|---|---|---|
| source | `locallvmtransport.Runtime` fixed-path `SourceRouter` + `SourceAuthorizeBackend` | source Host authority/session/credential/certificate and exact source Volume/Binding/VG/LV |
| destination | `DestinationBackend` registered by `kim-host-agent` | destination authority/session/credential/certificate and exact destination Volume/Binding/VG/LV; source endpoint resolved only from admin Host map |
| lifecycle | `hostruntime.RuntimeComponent` | listener starts before Agent session; activate binds generation; deactivate/restart discards routes; graceful shutdown closes listener |
| Control Plane | `PrepareLocalLVMTransportRuntimeCommands` | immutable 071 session creates exact asymmetric Commands; no endpoint/path in payload |

`kim-host-agent` enables the capability only when Local LVM VG UUID/name, listen address, non-empty endpoint registry, existing Agent CA/certificate/key, and credential revision are all configured. Missing or invalid configuration fails startup. The same certificate system is used; no parallel credential authority exists.

The normal execution module advertises `kim.host.local-lvm-cross-host-transport.v1` in immutable session handshake evidence. `PrepareLocalLVMTransportSession` re-joins that evidence for both exact current session generations, so a disabled or failed runtime cannot receive a new transport authority. A capability configuration change requires reconnect/new Agent session generation.

## Exact synthetic runtime profile

| Identity | Value |
|---|---|
| source Agent / session / credential | `host-a` / `17` / `5` |
| destination Agent / session / credential | `host-b` / `19` / `7` |
| source Volume / Binding generation / LV | `volume-a` / `binding-a:3` / `lv-a` |
| destination Volume / Binding generation / LV | `volume-b` / `binding-b:7` / `lv-b` |
| transport session / generation | `normal-session` / `1` |
| copy operation / generation | `normal-copy` / `1` |
| listener | dedicated loopback TCP, administrator bind `127.0.0.1:0` in fixture, fixed path `/v1/local-lvm/transport` |
| TLS | exact source/destination certificates; TLS 1.3 only; mutual certificate verification; HTTP/2 |
| bytes | `16384` |
| chunk profile | 4096-byte exact-offset SHA-256 frames |
| response | `RECEIVED` positive, then normal Agent Result `LOST` after repeated exact side effect |
| runtime read-back | destination `MATCHED`, same content digest as positive observation |
| source/destination storage | distinct backing byte stores and asymmetric interfaces; no shared resolver |

PostgreSQL integration creates the source and destination execution Commands directly from `LocalLVMTransportSession.AgentAuthority()`, checks exact Host/command type, and asserts Command JSON contains neither `/dev/` nor `https://`. Existing EVACUATE Local LVM integration then continues through Transport Terminal, Copy Terminal, `PRESERVED_ROOT`, child/parent terminal, source cleanup, and capacity reclamation.

## Mandatory negative and fault coverage

- TLS 1.2-only client fails protocol negotiation; both runtime TLS configs and handler/client negotiated-version checks require TLS 1.3.
- client certificate omission fails the TLS handshake; wrong certificate/fingerprint remains rejected by existing transport tests.
- unknown/unregistered source Host endpoint, non-HTTPS origin, arbitrary endpoint path, wrong destination Host, and generic URL injection are rejected.
- a Host session that does not advertise the transport capability is excluded when the Control Plane prepares transport authority.
- old Agent session route is deleted on deactivate/reconnect; authority for session 17 cannot authorize session 18.
- wrong credential revision, Host authority generation, Host ID, Binding generation/LV identity, expired session, holder-open, partial stream, out-of-order/conflicting duplicate, corruption, source drift, destination corruption, and flush failure cannot verify.
- a normal typed destination execution completes block transfer but loses its published Result. The write-before-execute journal remains, and a normal Verification Request produces `MATCHED` read-back without alternate destination or blind success.
- source/destination process restart creates a new empty registry/new session; an old route is not reconstructed from local state.

## Gate matrix

| Gate | Result |
|---|---|
| `LOCAL_LVM_TRANSPORT_AGENT_RUNTIME_WIRING` | PASS |
| `LOCAL_LVM_TRANSPORT_SOURCE_RUNTIME` | PASS |
| `LOCAL_LVM_TRANSPORT_DESTINATION_RUNTIME` | PASS |
| `LOCAL_LVM_TRANSPORT_NORMAL_SESSION_AUTHORITY` | PASS |
| `LOCAL_LVM_TRANSPORT_TLS13_ENFORCEMENT` | PASS |
| `LOCAL_LVM_TRANSPORT_RUNTIME_READ_BACK` | PASS |
| `CROSS_HOST_LOCAL_LVM_TRANSPORT_AUTHORITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_SOURCE_READ_AUTHORITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_DESTINATION_WRITE_AUTHORITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_TRANSPORT_INTEGRITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_RESPONSE_LOSS` | PASS |
| `CROSS_HOST_LOCAL_LVM_REPLAY_IDEMPOTENCY` | PASS |
| `CROSS_HOST_LOCAL_LVM_ABA_FENCING` | PASS |
| `EVACUATE_LOCAL_LVM` | PASS — synthetic product runtime/control-plane composition |
| `GENERIC_LOCAL_LVM_SOURCE_CLEANUP` | PASS — synthetic |
| `REAL_TWO_HOST_LOCAL_LVM_DATA_PRESERVATION` | BLOCKED — no active deployed Agent/disposable real VM/LV |
| `REAL_TWO_HOST_KVM_HOST_EVACUATION` | BLOCKED |

## Runtime safety assertions

| Assertion | Result |
|---|---|
| test-only transport path used as product path | no; `cmd/kim-host-agent` registers the runtime/backends |
| shared in-memory source/destination resolver | no |
| TLS below 1.3 / no client cert / wrong cert accepted | no / no / no |
| caller arbitrary URL/path/VG/LV accepted | no / no / no / no |
| guest blocks through PostgreSQL/Gateway | no / no |
| guest blocks persisted in journal/log | no |
| old Agent session/credential/Host authority uplifted | no / no / no |
| arbitrary HTTP route or arbitrary shell/argv | none |
| automatic alternate destination | none |

The east-west listen port is administrator/deployment configuration and must be allowed by the external firewall only between authorized Agents. KIM performs no firewall mutation. Metrics remain low-cardinality counters. Migration 071 records `bandwidth_limit_bytes_per_second`, but runtime rate enforcement is not implemented; this policy is recorded, not enforced.

## Inventory baseline update

The prior inventory's P1 items **normal Host Agent wiring** and **TLS 1.3 direct enforcement** are closed. The same 35-row denominator and weights are retained:

```text
Architecture completion:      31.5 / 35 = 90.0%  (was 88.6%)
Functional completion:        30.0 / 35 = 85.7%  (unchanged; row was already synthetic PASS)
Production qualification:     17.5 / 35 = 50.0%  (unchanged; no real Host mutation)
```

Remaining P1 blockers are real disposable EVACUATE/Local-LVM deployment, real PCI/VF and EVACUATE consumer, operator stuck-state workflow, evidence retention/archive, and accepted-but-unimplemented Ceph/DPDK capabilities.

## Regression execution

- fresh PostgreSQL 17 migrations 001–072, migration replay, and all persistence integration: PASS
- all persistence integration with `-race` on a separate fresh PostgreSQL 17: PASS
- Agent runtime/transport, Local LVM copy/transport/cleanup, EVACUATE zero-Port/OVN/repeated/mixed-origin, Recovery, Network, Storage, PCI, and Cleanup packages: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS
- `make check` including `go vet` and documentation lint: PASS
- `git diff --check`: PASS

Database-backed campaigns were run in dedicated PostgreSQL instances. Injecting the same database URL into every package concurrently is not a supported isolation profile because independent qualification packages intentionally use shared global release/queue authorities.
