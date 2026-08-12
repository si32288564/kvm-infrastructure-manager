# P1 Real Two-Host KVM Recovery Qualification

- Date: 2026-08-12
- Overall result: `REAL_TWO_HOST_KVM_RECOVERY_AUTHORITY = BLOCKED`
- Physical backend sub-gate: `REAL_TWO_HOST_KVM_BACKEND_SEQUENCE = PASS`
- Blocker: ordinary Result/Observation ingestion is now exercised, but the real source boot Volume remains configured as `vda`; the physical run's detached `vdb` safety disk is not the boot Volume bound to the Recovery Epoch. The current `SOURCE_DETACHED_NO_HOLDER` policy therefore cannot honestly issue the exact source Storage Safety Proof without a separately-authorized root/source cleanup operation.
- Repository baseline: `8260b70692f3618c65d19ba4f38bdf6960be1289`

The operator explicitly authorized g02 only for isolated, non-disruptive, fully removable artifacts. The run therefore used loop-backed VGs, one deterministic disposable VM, zero Ports, and no PCI. Existing production Domains, `vg0`, addresses, routes, OVS/OVN configuration, and services were outside the allow-list.

## Exact profile and guards

```text
action: RESTART_ON_OTHER_HOST
source: kvm-base-g01-n001-p.core.s01.si1230.com
destination: kvm-base-g02-n001-p.core.s01.si1230.com
VM UUID: f1c06a00-0000-4000-8000-202608120001
image: dedicated RAW, 268435456 bytes
image SHA-256: 7e423a1dc718d83c204e2def60b2fa63fcd4fa4814a9b2e40b29b5ae6a8502a1
flavor: 1 vCPU / 256 MiB
network: zero-Port
PCI/SR-IOV: excluded
storage: isolated loop-backed Local LVM only
```

The opt-in helper requires `KIM_REAL_KVM_RECOVERY_QUALIFICATION=1`, exact hostname/Host ID, `real-recovery-` command identity, a `kimrr_` VG, `/var/tmp/kim-real-recovery-` cache/state roots, bounded attempt/generation values, and one of the compile-time registered typed backends. It accepts no XML, path, VG/LV name, libvirt method, LVM argv, flag, or shell command from the request.

## Actual backend evidence

| Boundary | Actual result |
|---|---|
| source initial state | standard libvirt read-back `RUNNING` |
| source typed shutdown immediate result | `UNKNOWN / CONFLICTING`; this was not treated as non-execution |
| source later power read-back | `SHUTOFF / MATCHED`, digest `01b05033d3cc35a1e727c7bcf86e400c06decb6753cb392fa72cbee641178b4d` |
| source safety disk | typed detach `MATCHED`, device absent and holder closed, digest `e62f08d94db8fdbe7f9c07d99702d39fa3835e0395b357143c68673a6693719c` |
| destination before | exact VM UUID absent; isolated VG empty |
| destination LV | 512 MiB, LV UUID `1M2peE-K93k-1pCy-QOt7-TfjT-w6Hg-uSt8mf`, `MATCHED` |
| destination image | target-LV digest read-back exactly matched the declared RAW digest |
| destination define | inactive Domain with same UUID, materialization generation 2, fixed plan/root identity `MATCHED` |
| destination power | typed power state `RUNNING / MATCHED`, digest `f249701cff2ee03bae1880bf8ed4f0cddc760815903c5ff27e3bb98754e10071` |
| destination root | observation-only `vda` identity, source identity and LVM holder-open all true, digest `b229a814d79865da0c892580650e28655ab41d854656510901e9ea0b54ced330` |
| split-brain assertion | g01 `SHUTOFF`; g02 `RUNNING`; identical UUID |

The backend sequence used materialization generation 1 on g01 and generation 2 on g02. A second VM UUID was never generated.

## Defects exposed by the real run

The run found two authority/backend gaps that a fixture had hidden:

1. the fixed typed Domain XML did not expose ACPI or a serial console, so a minimal guest could not perform typed graceful shutdown. The fixed profile now always includes ACPI and a PTY serial console; callers still cannot supply XML or machine flags;
2. pre-power zero-Port readiness required `holder_open=true`, although QEMU opens the root LV only after start. Pre-power readiness now requires current immutable inactive-Domain root identity plus exact BOUND binding and current root claim. Post-power Recovery Verification and Terminal Decision still require current `ATTACHED`, device-present, identity-matched, holder-open evidence.

Migration 056 only widens the attachment observation target CHECK from secondary `vdb-vdz` to `vda-vdz`. The Agent permits slot zero only for observation of an already-defined `vda`; it rejects root attach/detach mutation. No unrelated CHECK or UNIQUE constraint is changed.

## Ordinary Result / Observation ingestion follow-up

The follow-up increment removed direct success-state SQL from the destination define, image, and root-attachment terminal fixture. Those steps now use the ordinary execution authority path:

```text
Execution Job / Command
→ random PostgreSQL Lease capability
→ Attempt
→ typed Agent Result + Observation
→ AcceptAgentCommandResult
→ durable Receipt / Result / Verification
→ Job SUCCEEDED
```

The ambiguous power step now uses an ordinary Lease expiry to reach `UNKNOWN`, followed by `AcceptAgentVerificationObservation` over a stable RESYNC envelope. The real helper accepts the exact granted Lease generation/capability on stdin, but returns capability-free `ResultEvidence`; the Control Plane can bind the capability back only when Command and Attempt exactly match. The raw Lease token is not emitted in helper JSON or persisted in the qualification artifact.

This closes the previously hidden direct-SQL execution-evidence shortcut. It does not by itself make the prior physical observations part of one committed real Recovery history.

## Why the overall gate remains BLOCKED

The real physical evidence above and the PostgreSQL Recovery authority regression remain two distinct qualification lanes. More importantly, the original physical profile did not prove the exact source boot-Volume safety condition consumed by Recovery authority:

```text
Recovery source boot Volume = libvirt vda (still configured on inactive source Domain)
physical detached safety disk = vdb (separate disposable Volume)
```

The current typed backend intentionally permits `vda` only for observation and continues to reject root attach/detach mutation. Treating the detached `vdb` as proof for the boot Volume would violate exact Attachment/Binding identity and falsely authorize Recovery. Standard power `SHUTOFF`, helper success, or a closed holder alone is not a substitute for the required `DETACHED + RELEASED + device absent + holder closed` source Storage Safety Proof.

Consequently this run does **not** claim:

```text
actual g01/g02 evidence
→ EvaluateRecoveryDangerousStep AUTHORIZED
→ AuthorizeRecoveryPowerOn
→ EvaluateRecoveryVerification VERIFIED
→ CommitRecoveryTerminalDecision
→ Operation VERIFIED / Epoch RECOVERED / Budget RELEASED
```

Fixture-backed `AUTHORIZED`, `VERIFIED`, `RECOVERED`, and `RELEASED` remain regression evidence only. Physical `RUNNING` and SQL state transition success are not substituted for the missing exact ingestion chain.

## Negative gates

- Source shutdown response ambiguity remained `UNKNOWN` until standard libvirt read-back.
- Slot-zero/root absence does not cause an attach; root detach remains rejected.
- A detached secondary `vdb` is never accepted as Storage Safety proof for the boot Volume bound as `vda`.
- Capability-free helper evidence cannot be accepted against a different Command or Attempt, and helper output never contains the raw Lease token.
- Pre-power readiness cannot use a holder-open claim fabricated before QEMU start.
- Post-power terminal verification still requires holder-open.
- Source and destination Host equality, hostname mismatch, foreign command identity, non-`kimrr_` VG, non-qualification state/cache roots, unsupported command type/schema, and missing opt-in fail closed.
- `EVACUATE` remains unsupported/blocked.

## Cleanup and non-disruption

After the final split-brain assertion, g02 was gracefully shut down. On both Hosts, cleanup revalidated the exact VM UUID, Domain name, VG UUID, and loop backing path before removing only the disposable Domain, `kimrr_*` VG/LVs, loop PV/device, helper, cache, journal, and build directory.

Post-cleanup Domain, address, route, and OVS fingerprints exactly matched the preflight baseline on both Hosts. No production Domain, `vg0`, network configuration, route, OVS/OVN state, or service was changed. Immutable repository/qualification evidence was not rewritten; backend cleanup is an operator procedure and is not claimed as KIM cleanup authority.

## Regression

| Check | Result |
|---|---|
| typed Local LVM attachment/root observation unit tests | PASS |
| ordinary Lease/Attempt/Result/Receipt/Verification Recovery terminal PostgreSQL 17 integration | PASS |
| ordinary UNKNOWN → RESYNC Verification Observation ingestion | PASS |
| capability-free helper Result binding and libvirt-tag build | PASS |
| fresh migration 001-056 | PASS |
| `go test -race ./...` | PASS |
| `make check` | PASS |
| documentation lint | PASS: 471 requirements, 715 test contracts, 234 links |

## Next gate

Define and qualify the minimum explicit source-root safety authority instead of weakening the existing policy. Viable follow-up work is a closed, separately-authorized source Domain/root cleanup operation with exact read-back, or a new typed policy whose positive proof is explicitly `source SHUTOFF + holder closed` and whose safety assumptions are independently qualified. Until one of those authorities exists, do not ingest the secondary `vdb` evidence as the boot Volume proof and do not issue a Terminal Decision. After that gate, rerun the isolated two-Host profile through ordinary Result/Observation ingestion and require one committed Terminal Decision with actual exact-volume observations. Non-empty OVN, PCI/SR-IOV, `EVACUATE`, and repeated Recovery soak remain later gates.
