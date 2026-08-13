# P1 real two-Host Local LVM transport authority qualification — 2026-08-13

## Outcome

Migration 071 connects Migration 070 content preservation to a closed cross-Host data-plane authority. The simulated transport campaign used two separately-owned Agent backends, real TLS 1.3 mutual authentication and HTTP/2 framing; the source fixture cannot access destination storage and the destination fixture cannot access source storage. A production adapter resolves only administrator-mapped VG UUIDs and KIM-derived LV names through a fixed `lvs` read-back before opening an internally observed `/dev/mapper` device.

All 16 MiB were transferred and flushed. The destination response was recorded `LOST`, the transport Lease expired, a successor performed read-back, and independent source/destination SHA-256 observations matched. Only that immutable transport terminal was accepted by Migration 070 copy verification; the exact copy terminal then supplied `PRESERVED_ROOT`, destination READY/RUNNING, child VERIFIED, parent VERIFIED, and Host DRAINED.

This is a synthetic two-Agent transport qualification, not a claim that blocks traversed g01→g02. The read-only real Host preflight found no authorized disposable profile and therefore performed no physical mutation. `REAL_TWO_HOST_LOCAL_LVM_DATA_PRESERVATION` remains `BLOCKED`.

## Authority chain

`Migration 070 copy Operation → Migration 071 shared transport Session → source current quiescence/Storage SAFE/SHUTOFF/holder fence → source exact LV read authority → mTLS HTTP/2 exact-offset frames → destination exact LV write authority → flush → response LOST → Lease expiry → successor READ_BACK → independent whole-volume source/destination SHA-256 → transport Terminal VERIFIED → Migration 070 Copy Verification/Terminal VERIFIED → PRESERVED_ROOT → destination READY/RUNNING → Child/Parent VERIFIED`

Source and destination identities, byte count, chunk policy, credential/session generations, Host authority generations, expiry, and certificate fingerprints are included in the same authority digest. Guest blocks never enter PostgreSQL or evidence JSON.

## Recorded PostgreSQL 17.10 campaign identities

| Identity | Value |
|---|---|
| copy Operation | `local-lvm-copy-positive-1786620644113109000` |
| transport Session | `local-lvm-transport-positive-1786620644113109000`, generation `1` |
| source Host / authority generation | `evacuation-positive-source-1786620644113109000` / `1` |
| destination Host / authority generation | `evacuation-positive-destination-1786620644113109000` / `1` |
| source Volume | `evacuation-source-root-1786620644113109000` |
| source Binding / generation | `storage-binding:evacuation-source-1786620644113109000:evacuation-source-root-1786620644113109000` / `1` |
| source VG UUID / LV UUID | `evacuation-positive-vg-1-1786620644113109000` / `lv-evacuation-source-1786620644113109000` |
| destination Volume | `evacuation-destination-1786620644113109000:root` |
| destination Binding / generation | `storage-binding:evacuation-destination-1786620644113109000:evacuation-destination-1786620644113109000:root` / `1` |
| destination VG UUID / LV UUID | `evacuation-positive-vg-2-1786620644113109000` / `lv-evacuation-destination-1786620644113109000` |
| exact/transferred bytes | `16777216` / `16777216` |
| transport Attempt / response | `2` / `LOST` |
| source digest | `5164dcf43bda838e4e50fa97a9658bd5d0a9013ea3d671249fef12c9824dd25a` |
| destination digest | `5164dcf43bda838e4e50fa97a9658bd5d0a9013ea3d671249fef12c9824dd25a` |
| transport Terminal | `local-lvm-transport-terminal-positive-1786620644113109000` |
| copy Command / Attempt / Lease | `local-lvm-copy-command-positive-1786620644113109000` / `1` / `1` |
| copy Verification / Terminal | `local-lvm-copy-verification-positive-1786620644113109000` / `local-lvm-copy-terminal-positive-1786620644113109000` |
| PRESERVED_ROOT evidence | `preserved-root-evidence-destination-1786620644113109000` |
| child Verification / Terminal | `evacuation-child-verification-1786620644113109000` / `evacuation-child-terminal-1786620644113109000` |
| parent Terminal | `evacuation-parent-terminal-1786620644113109000`, `VERIFIED` |

The payload contained a base region, a unique guest-like mutation marker, and a second marker near the end. The destination byte buffer was compared in the isolated Agent test; only digest, size, and identities were persisted.

## Mandatory qualification coverage

- successful cross-Host stream: separate source reader and destination writer over real TLS 1.3 mTLS/HTTP2; destination flush required;
- wrong source/destination Host or peer certificate: rejected;
- wrong source/destination LV and stale Binding generation: rejected by peer resolver and PostgreSQL terminal rejoin;
- open source/destination holder: rejected;
- partial stream: remains `UNKNOWN`, never terminal;
- destination corruption and source pre/post digest drift: digest verification rejects;
- response loss and Lease expiry: immutable `DISCONNECTED`, `LEASE_EXPIRED`, and successor `READ_BACK` events precede terminal;
- retry from offset zero: same destination converges; alternate destination is not accepted;
- exact duplicate chunk: idempotent; conflicting duplicate and out-of-order/range frame: rejected;
- concurrent session: maximum per-Host value `1`; a second session for the same copy/destination is rejected under Host advisory locks/current count;
- stale Host authority, revoked credential, fenced Agent session, destination Binding drift, and old source VM incarnation: terminal rejects in rollback branches;
- replay: exact terminal identifiers/digests are idempotent; conflicting digest replay rejects;
- transport session/event/peer/terminal evidence rejects UPDATE.

## Real g01/g02 read-only preflight

| Check | g01 | g02 |
|---|---|---|
| Host | `kvm-base-g01-n001-p.core.s01.si1230.com` | `kvm-base-g02-n001-p.core.s01.si1230.com` |
| existing Domains | 4 | 15 production Domains |
| `lvs` binary | `/usr/sbin/lvs` | `/usr/sbin/lvs` |
| LVM inventory with current login | blocked by `/run/lock/lvm/P_global` permission | blocked by `/run/lock/lvm/P_global` permission |
| KIM Agent service | `inactive` | `inactive` |
| device-mapper control access | not readable | not readable |
| disposable VM/VG/LV authorization | none found | none found |

The inspection used read-only SSH commands only. It did not create a VG/LV, start an Agent, alter a service, change a Domain, or touch production data. Because the required deployed mTLS Agent endpoints, device access, and disposable workload were absent, no g01→g02 stream or EVACUATE mutation was attempted.

## Gate matrix

| Gate | Result |
|---|---|
| `CROSS_HOST_LOCAL_LVM_TRANSPORT_AUTHORITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_SOURCE_READ_AUTHORITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_DESTINATION_WRITE_AUTHORITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_TRANSPORT_INTEGRITY` | PASS |
| `CROSS_HOST_LOCAL_LVM_RESPONSE_LOSS` | PASS |
| `CROSS_HOST_LOCAL_LVM_REPLAY_IDEMPOTENCY` | PASS |
| `CROSS_HOST_LOCAL_LVM_ABA_FENCING` | PASS |
| `PLANNED_LOCAL_LVM_COPY_AUTHORITY` | PASS |
| `PLANNED_LOCAL_LVM_COPY_VERIFICATION` | PASS |
| `PLANNED_LOCAL_LVM_CONTENT_IDENTITY` | PASS |
| `EVACUATE_LOCAL_LVM` | PASS — synthetic data-preserving profile |
| `REAL_TWO_HOST_LOCAL_LVM_DATA_PRESERVATION` | BLOCKED — no deployed Agent/device access or authorized disposable workload |
| `REAL_TWO_HOST_KVM_HOST_EVACUATION` | BLOCKED |
| `GENERIC_LOCAL_LVM_SOURCE_CLEANUP` | BLOCKED |
| `EVACUATE_PCI_SRIOV` | BLOCKED |

## Metrics

The transport records low-cardinality counters for active sessions, bytes, sessions, retries, unknown outcomes, integrity failures, and duration. Volume/Binding/LV/session identities are not labels.

## Security and safety assertions

| Assertion | Result |
|---|---|
| SSH used as block transport | no; SSH was read-only preflight only |
| arbitrary shell/argv in transport | none |
| caller path/selector/device accepted | no |
| source Agent arbitrary LV read | no; exact signed identity plus admin VG map/KIM LV derivation |
| destination Agent arbitrary LV write | no; exact signed identity plus admin VG map/KIM LV derivation |
| unauthenticated transport peer | no; both certificate fingerprints and TLS 1.3 mTLS required |
| guest raw blocks stored in DB | no |
| session expiry treated as no side effect | no; read-back first |
| stream success treated as content proof | no |
| destination boot before copy VERIFIED | denied |
| base Image overwrite after preserved copy | denied |
| source LV deleted / capacity reclaimed | no / no |
| old session uplifted to new incarnation | no |
| historical evidence rewritten | none; immutable UPDATE tests reject |
| production workload mutated | none |

## Verification profile

- fresh PostgreSQL `17.10` migrations `001–071`: PASS;
- migration replay `070 → 071` and replay at latest: PASS;
- transport-linked one-VM/zero-Port/one-Local-LVM EVACUATE parent terminal: PASS;
- real g01/g02 read-only preflight: completed, mutation blocked by the conditions above.

The complete repository regression results are recorded with the implementing commit.
