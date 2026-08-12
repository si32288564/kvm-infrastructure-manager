# P1 Real Two-Host KVM Recovery Authority E2E Qualification

- Date: 2026-08-12
- Overall result: `REAL_TWO_HOST_KVM_RECOVERY_AUTHORITY = PASS`
- `REAL_TWO_HOST_KVM_BACKEND_SEQUENCE = PASS`
- `REAL_SOURCE_ROOT_VDA_SAFETY_BACKEND = PASS`
- `REAL_CP_LEASE_BOUND_HELPER = PASS`
- `REAL_FAILURE_TO_STORAGE_AUTHORITY = PASS`
- `REAL_RECOVERY_OPERATION_EXECUTION = PASS`
- `REAL_RECOVERY_TERMINAL_AUTHORITY = PASS`
- Migration: 058

This campaign joined the actual g01/g02 libvirt and Local LVM observations to one ordinary PostgreSQL authority history. No fixture Result, direct success-state SQL, or physical-observation substitution was used between Failure Epoch open and Terminal Decision.

```text
real source RUNNING
→ real guarded failure injection
→ ordinary source SHUTOFF read-back
→ read-only source-root Command Lease
→ real exact vda/no-holder observation
→ Confirmation / Fencing / Root Safety / Retirement / Storage Proof
→ Eligibility / Decision / Budget Claim
→ Recovery Operation / destination Final Admission
→ ordinary destination materialization Commands and Leases
→ real destination RUNNING and exact vda/holder-open observation
→ Recovery Verification
→ Terminal Decision
→ Operation VERIFIED / Epoch RECOVERED / Budget RELEASED
```

## Exact isolated profile

| Item | Value |
|---|---|
| source | `kvm-base-g01-n001-p.core.s01.si1230.com` |
| destination | `kvm-base-g02-n001-p.core.s01.si1230.com` |
| action | `RESTART_ON_OTHER_HOST` |
| VM UUID | `f1c06a00-0000-4000-8000-202608120058` |
| flavor | 1 vCPU / 256 MiB |
| Network | zero Port |
| PCI/SR-IOV | excluded |
| source VG | `kimrr_campaign058_g01`, UUID `vfGTho-4dc7-b8bV-L3fV-4BrJ-HaE3-dWnw7Q`, `/dev/loop0` |
| destination VG | `kimrr_campaign058_g02`, UUID `ZZiDJk-J904-46bg-ssxp-rnuH-ocwt-RJEHqQ`, `/dev/loop0` |
| image | 1 MiB zero RAW, SHA-256 `30e14955ebf1352266dc2ff8067e68104607e750abb9d3b36582b8af909fcb58` |
| helper/cache root | `/var/tmp/kim-real-recovery-campaign058` |
| PostgreSQL | disposable PostgreSQL 17, one database history |

The operator authorized g02 only for non-disruptive, fully removable artifacts. Exact hostname, VM UUID, VG UUID, loop device, helper root, command identity, and opt-in guards were checked before every physical mutation. Existing Domains, `vg0`, addresses, routes, OVS/OVN configuration, and services remained outside the allow-list.

## Authority evidence

| Boundary | Accepted evidence |
|---|---|
| source Admission | `admission:real-recovery-source-admission` on g01 |
| source materialization | generation 1 |
| source power | `RUNNING` generation 1, then real `SHUTOFF/MATCHED` generation 2 |
| Failure Epoch | `real-recovery-failure-epoch`, opened `2026-08-12 09:54:14.557839 UTC` |
| Confirmation | `real-recovery-confirmation-decision`, `CONFIRMED` |
| source root | exact `vda`, LV UUID `ac4z0G-zAfa-apGn-7b2d-6Oej-rZal-B1neVz`, holder closed |
| Fencing Proof | `real-recovery-fencing-proof`, `PROVEN`, digest `96dc...5c01b` |
| Root Safety Proof | `real-recovery-source-root-proof`, `SAFE`, digest `fb73...15c3` |
| source retirement | `real-recovery-source-retirement`, materialization generation 1, `RETIRED` |
| Storage Proof | `LOCAL_LVM_SOURCE_ROOT_QUIESCED_DATA_DETACHED`, `SAFE`, digest `2bb3...6c542` |
| Eligibility | `real-recovery-eligibility-evaluation`, `ELIGIBLE`, one destination |
| Decision / Budget | `real-recovery-eligibility-decision`, `ACCEPTED`; claim later `RELEASED` generation 3 |
| Operation | `real-recovery-operation`, final `VERIFIED` generation 4 |
| destination Admission | `admission:recovery-placement:real-recovery-operation` on g02 |
| destination materialization | generation 2 |
| destination power/root | real `RUNNING/MATCHED`; exact `vda` LV UUID `YWB689-nizd-yqmy-gm4E-Sf3c-i0eA-mipTb3`, holder open |
| Verification | `real-recovery-terminal-verification`, `VERIFIED` |
| Terminal Decision | `real-recovery-terminal-decision`, `VERIFIED`, digest `496a...0178` |
| terminal transition | Epoch `RECOVERED` generation 4 and Budget `RELEASED` committed with Operation `VERIFIED` |

The final physical assertion was g01 `SHUTOFF`, g02 `RUNNING`, and the same VM UUID. Materialization identity advanced from 1 to 2; VM/workload identity did not change.

## Lease and ingestion evidence

Migration 058 adds a closed `authority_scope` to Command Lease authority:

- `MUTATION` remains the default and requires current Host mutation authority;
- `READ_ONLY_VERIFICATION` is allowed only for `SOURCE_ROOT_SAFETY_READ_BACK/v1`, a current `AUTHORIZED` session, and the exact `FENCED` Host generation;
- read-only Verification never arms or rearms the Host and cannot carry a mutation Command.

The source-root read-only Lease, destination power mutation Lease, and final source read-only Lease were all generation 1 / Attempt 1 and bound to the exact Command, Host, session, authority generation, target, schema, and payload digest. The remote helper consumed the random capability only on stdin and returned capability-free evidence. The ordinary Result/Verification/Receipt acceptance path committed all domain decisions.

The database contained 13 Jobs, 13 Commands, 13 Lease Grants, 13 Attempts, 13 Results, 13 Verifications, and 13 Receipts. Raw token columns were absent, and searches of repository artifacts, runner output, and journal evidence found no raw Lease capability. Helper SHA-256 was `5d9fbbf6a766dc86df47bc67c458bed1e2f2be8c9a13f385356794500e57980e` on both Hosts.

## Failure semantics and atomic terminal result

- source physical failure was not inferred from transport loss;
- `SHUTOFF` and root holder closure came from standard libvirt/LVM read-back;
- Lease expiry was not used as proof of non-execution;
- helper success, Command success, or destination `RUNNING` alone did not emit `RECOVERED`;
- one accepted Recovery Verification fed one Terminal Decision;
- Terminal Decision count, `VERIFIED` Operation transition, `RECOVERED` Epoch transition, and `RELEASED` Budget transition were each exactly one;
- terminal state changes committed atomically;
- root `vda` mutation Command count remained zero.

Measured latency was 4.591218 seconds from Epoch open to `RECOVERED`, 2.642123 seconds from Fencing Proof to destination `RUNNING`, and 3.727349 seconds from Fencing Proof to `RECOVERED`.

## Defects exposed and fixed

1. source-local LVM backend identity was incorrectly carried into multi-Host dry Placement; Eligibility now treats it as source safety provenance and derives the exact destination backend only during Recovery planning/Final Admission;
2. generated Recovery Volume/Attachment identities contained characters rejected by the closed Local LVM grammar; they now use bounded digest identities;
3. VM generation and materialization incarnation had been conflated; VM generation remains stable while immutable plan payloads advance materialization generation 1→2;
4. source retirement usability used the rebuildable current VM projection after it moved to g02; it now revalidates the exact historical source Host fencing/power evidence;
5. libvirt power observations needed an explicit generation so source `RUNNING` generation 1 could advance to `SHUTOFF` generation 2;
6. Migration 057-era immutable plans without the new payload field are read compatibly through their historical VM generation and are not backfilled or rewritten.

## Cleanup and non-disruption

After evidence capture, both exact Domains were absent, both `kimrr_campaign058_*` VGs were absent, `/dev/loop0` was detached, and the dedicated helper/cache roots were absent. OVS external IDs were identical to preflight: g01 retained `phys0:brphys0`; g02 retained `phys0:brphys0,phys1:brphys1`; both retained `ovn-encap-ip=127.0.0.1`. No production Domain, `vg0`, address, route, OVS/OVN state, or service was changed.

Backend cleanup is an operator procedure and is not claimed as KIM source cleanup authority.

## Regression

| Check | Result |
|---|---|
| real same-history two-Host campaign | PASS, 9.68 seconds |
| fresh migration 001-058 / full PostgreSQL 17 persistence integration | PASS |
| libvirt typed backend tests/build | PASS |
| `go test -race ./...` | PASS |
| `make check` | PASS |
| documentation lint / link check | PASS |

## Remaining gates

This PASS is deliberately limited to `RESTART_ON_OTHER_HOST`, Local LVM, zero Port, and no PCI/SR-IOV. Non-empty Network/OVN recovery, PCI/SR-IOV recovery, generic source cleanup authority, `EVACUATE`, and repeated Recovery soak/chaos remain separate gates.
