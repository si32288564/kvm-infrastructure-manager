# P1 Real Recovery Lease-Bound Helper Qualification

- Date: 2026-08-12
- Sub-gate: `REAL_CP_LEASE_BOUND_HELPER = PASS`
- Overall Recovery gate after the subsequent campaign: `REAL_TWO_HOST_KVM_RECOVERY_AUTHORITY = PASS`
- Migration: none

This increment closed the reusable physical execution binding. The subsequent
[real two-Host campaign](p1-real-two-host-kvm-recovery-20260811.md) reused it and
qualified the complete Failure Epoch through Terminal Decision history.

```text
PostgreSQL typed Command
→ random exact Lease Grant
→ SSH stdin only
→ real g01 helper / isolated Local LVM
→ capability-free Result + read-back Observation
→ exact authority identity validation
→ ordinary AcceptAgentCommandResult
→ Result / Verification / Receipt / Job SUCCEEDED
```

The helper evidence binds Lease generation, Attempt, Host authority generation,
session generation, Command type/schema, target, and canonical payload digest.
The receiver also requires the outer Host/hostname/Command/type and the embedded
and top-level Observations to agree before reattaching the in-memory token.

## Physical evidence

- Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- isolated VG: `kimrr_authority058_g01`
- VG UUID: `s9vV9i-9B63-qBp6-xdrq-tL0F-mwm5-qn97fP`
- physical LV: `kim-a0c35dc1229bd0ca89e2b9a26d22a3c4`
- LV UUID: `yCRkPd-TEDX-Jn1a-hN2I-ZQTS-t8yn-2OdZ6C`
- helper SHA-256 on both Hosts: `29b48d9fd5bf2db5c0bc229eddbb023bbf5bfdb0cd29af95e7e0e6f613a2ab5e`
- Command payload digest: `e8e0af1b5b28793494baf616c465fe39bb0e4357515c4cf68cc5cc4435284f6a`
- token digest: `31536f82935b026362fa8fd4b8c415372ad24692857a4525db10f81b1f1c943a`
- Result/Observation digest: `5375dcc8491c519a67416f4ca0aa61005fc3a6abbeee397de4536a556e5686be`

The fresh `kim4` PostgreSQL 17 history contained one Lease Grant, one Attempt,
one Result, one Command Verification and one command message Receipt. The Job
was `SUCCEEDED`. The isolated VG contained exactly one 16 MiB LV. Journal search
found no `lease_token` or token material. PostgreSQL stored only `token_digest`
and protected-token metadata columns.

Two fail-closed defects were exposed before PASS: the helper initially lacked
root device-mapper access, and PostgreSQL `jsonb` byte reserialization differed
from the original canonical payload bytes. The final path uses fixed `sudo -n`
for the allow-listed helper and reconstructs canonical semantic JSON, requiring
its digest to equal the immutable Control-Plane payload digest.

## Subsequent closure

This artifact remains the qualification record for the reusable Lease-bound
ingestion primitive. Migration 058 and the later g01→g02 campaign added a
closed read-only source-root Lease and joined actual Fencing/Root Safety,
Eligibility, Operation, destination materialization, Recovery Verification and
Terminal Decision in one PostgreSQL history. That later artifact is the
authority for Operation `VERIFIED`, Epoch `RECOVERED`, Budget `RELEASED`, and
the overall PASS.
