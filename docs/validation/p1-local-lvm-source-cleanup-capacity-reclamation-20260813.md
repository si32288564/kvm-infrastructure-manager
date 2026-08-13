# P1 Local LVM source cleanup and capacity reclamation validation — 2026-08-13

## Outcome

Migration 072 closes physical obsolete source-LV cleanup and capacity reuse as a post-terminal authority. The synthetic PostgreSQL 17 campaign passed for a one-VM Local LVM A→B EVACUATE, delayed A/B cleanup after A→B→C, and planned B cleanup after Recovery A→B → EVACUATE B→C. Destination VM and parent terminal authority remained unchanged.

`Child Terminal VERIFIED → exact MATERIALIZATION producer → capacity RELEASE_PENDING → generic cleanup claim APPLY_ALLOWED → typed delete response LOST → successor READ_BACK_FIRST → PRESENT → explicit apply → response LOST → successor READ_BACK_FIRST → exact old LV UUID ABSENT → Cleanup Terminal VERIFIED → immutable reclamation → capacity RELEASED`

## Exact positive campaign identities

| Identity | Value |
|---|---|
| VM UUID | `68000000-0000-4000-8000-460921995000` |
| source Host | `evacuation-positive-source-1786622460921995000` |
| destination Admission | `admission:evacuation-destination-1786622460921995000` |
| destination Plan | `evacuation-destination-plan-1786622460921995000` |
| source Volume | `evacuation-source-root-1786622460921995000` |
| source Binding / generation | `storage-binding:evacuation-source-1786622460921995000:evacuation-source-root-1786622460921995000` / `1` |
| source backend / generation | `evacuation-positive-backend-1-1786622460921995000` / `1` |
| source VG UUID | `evacuation-positive-vg-1-1786622460921995000` |
| source LV UUID | `lv-evacuation-source-1786622460921995000` |
| Child Terminal | `evacuation-child-terminal-1786622460921995000` |
| Copy Terminal | `local-lvm-copy-terminal-positive-1786622460921995000` |
| Cleanup Operation | `local-lvm-source-cleanup-1786622460921995000` |
| Cleanup Terminal | `cleanup-terminal-local-lvm-delete-absence-1786622460921995000` |
| capacity claim | `storage-capacity:evacuation-source-1786622460921995000:evacuation-source-root-1786622460921995000` |
| reclamation evidence | `local-lvm-capacity-reclamation-1786622460921995000` |
| released bytes | `16777216` |
| reclamation digest | `2ce40486d05e87fed96891a43fc9d429031eb9fbbd9ece4fa181862d9c70f15b` |

## Campaign metrics

| Metric | Value |
|---|---:|
| cleanup operations | 1 |
| cleanup claims | 3 |
| APPLY_ALLOWED claims | 1 |
| READ_BACK_FIRST successors | 2 |
| delete apply attempts | 2 |
| LOST responses | 2 |
| PRESENT read-backs | 1 |
| ABSENT read-backs | 1 |
| cleanup terminals | 1 |
| reclamation terminals | 1 |
| capacity before terminal | `RELEASE_PENDING` |
| capacity after terminal | `RELEASED` |

`LoadLocalLVMCleanupMetrics` exposes `local_lvm_cleanup_active`, `local_lvm_cleanup_attempts`, `local_lvm_cleanup_unknown`, `local_lvm_cleanup_present`, `local_lvm_cleanup_absent`, `local_lvm_cleanup_capacity_release_pending`, `local_lvm_cleanup_released_bytes`, and `local_lvm_cleanup_duration`. They are aggregate counters with no Host, Volume, Binding, or LV identity labels.

The delayed repeated campaign reclaimed exact A/mat1 and B/mat2 roots only after the VM was current on C/mat3. The mixed-origin campaign consumed the planned EVACUATE B→C Child Terminal; no Recovery terminal, Failure Epoch, or fencing proof was reused as planned cleanup proof.

## Negative and fencing coverage

- VM still current on the source, holder open, unknown terminal, missing Copy Terminal, backend generation drift, binding/LV drift, and premature capacity reclamation fail closed.
- `READ_BACK_FIRST` rejects blind delete. `PRESENT` is non-terminal and authorizes only the same exact operation/claim apply.
- open exact LV blocks Agent deletion; unknown fields, arbitrary paths, and caller LV names are rejected by strict decoding.
- same derived name with another LV UUID is treated as a foreign replacement: it is never deleted, while the obsolete exact UUID may converge as absent.
- response loss and Lease expiry retain uncertainty; successor read-back, not an exit code, creates absence authority.
- reclamation replay returns the same immutable decision; the same capacity claim cannot be released twice.
- cleanup failure/unknown state does not change Child/Parent VERIFIED, Host DRAINED, destination Admission/Plan, or destination RUNNING.

## Gate matrix

| Gate | Result |
|---|---|
| `GENERIC_LOCAL_LVM_SOURCE_CLEANUP` | PASS |
| `LOCAL_LVM_CLEANUP_PHYSICAL_ABSENCE` | PASS |
| `LOCAL_LVM_CLEANUP_CAPACITY_RECLAMATION` | PASS |
| `LOCAL_LVM_CLEANUP_UNKNOWN_READ_BACK` | PASS |
| `LOCAL_LVM_CLEANUP_REPLAY_IDEMPOTENCY` | PASS |
| `LOCAL_LVM_CLEANUP_ABA_FENCING` | PASS |
| `LOCAL_LVM_CLEANUP_DELAYED_INCARCATION` | PASS |
| `LOCAL_LVM_CLEANUP_MIXED_ORIGIN` | PASS |
| `EVACUATE_LOCAL_LVM` | PASS |
| `EVACUATE_ZERO_PORT` | PASS |
| `EVACUATE_OVN_PORT` | PASS |
| `EVACUATION_REPEATED_INCARNATION` | PASS |
| `EVACUATION_MIXED_RECOVERY_ORIGIN` | PASS |
| `EVACUATE_PCI_SRIOV` | BLOCKED |
| `REAL_TWO_HOST_LOCAL_LVM_SOURCE_CLEANUP` | BLOCKED |
| `REAL_TWO_HOST_LOCAL_LVM_CAPACITY_RECLAMATION` | BLOCKED |

## Real g01/g02 read-only preflight

Both canonical Hosts expose fixed LVM tools, including `/usr/sbin/lvremove`, but `kim-agent` is inactive. g01 listed four running Domains and g02 fifteen; every listed system/image LV was open. No isolated disposable VM/LV or deployed Agent authority was available. Only `hostname`, tool lookup, `systemctl is-active`, `lvs`, and `virsh list --name` were read. No LV, Domain, service, capacity, or production workload was mutated.

## Safety assertions

| Assertion | Result |
|---|---|
| caller-supplied physical absence | none |
| deletion inferred from exit code | no |
| capacity released before absence terminal | no |
| arbitrary path / VG name / LV name | rejected |
| arbitrary shell / argv | none |
| foreign replacement deleted | no |
| destination changed by cleanup | no |
| parent terminal depends on cleanup | no |
| Recovery proof reused by planned cleanup | no |
| production workload mutated | none |
| historical evidence rewritten | none |

## Verification

Qualification used fresh/replayed migrations `001–072` on `postgres:17-alpine`, PostgreSQL statement time for Lease/claim expiry, all persistence integration, standard/race Go tests, backend-tag suites, `make check`, documentation lint, and `git diff --check`. Exact final command results are recorded in the delivery commit.
