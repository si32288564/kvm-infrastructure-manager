# Typed Fencing Proof / Storage Safety Authority Validation

Date: 2026-08-11

## Scope

Migration 052 adds two independent safety authorities for a `CONFIRMED` Failure Epoch. It does not add Recovery Eligibility, Recovery Operation, restart, evacuation, replacement placement, or a generic fencing backend.

```text
CONFIRMED Failure Epoch
├─ exact FencingPolicy / pure Evaluation / explicit positive Proof
└─ exact StorageSafetyPolicy / pure Evaluation / explicit positive Proof
```

## Closed profiles

- Fencing: `KIM_AUTHORITY_FENCED_AND_LIBVIRT_SHUTOFF`
  - exact Host Operation Authority `FENCED` event
  - exact standard libvirt VM power `SHUTOFF / MATCHED` evidence
  - bounded KIM execution fence only; not BMC/physical power or storage fencing
- Storage: `LOCAL_LVM / SOURCE_DETACHED_NO_HOLDER`
  - Attachment `DETACHED`
  - single-writer Claim `RELEASED`
  - Binding `BOUND`
  - typed read-back `MATCHED`
  - device absent and holder closed

The AvailabilityPolicy text slots are not parsed. New policy revisions use separate immutable exact ID/revision/digest associations. Pre-052 AvailabilityPolicy/Epoch evidence is not rewritten and evaluates as `NO_FENCING_POLICY` / `NO_STORAGE_SAFETY_POLICY`.

## PostgreSQL qualification

Fresh PostgreSQL 17 migration and `TestAvailabilityPolicyPlacementConsumerPostgreSQLIntegration` passed. The scenario verifies:

- `CONFIRMED != FENCED` and Evaluation alone does not change the Epoch.
- connectivity/Agent loss without positive source evidence remains `UNKNOWN`; positive Proof is blocked.
- Local LVM `UNKNOWN` evidence cannot become `SAFE`.
- Fencing and Storage Safety Proofs are independent.
- a post-Epoch explicit Availability Rebind does not replace the Epoch's historical Binding revision.
- storage Claim state changes are generation fenced, including ABA change-and-restore.
- later fencing evidence makes an old Evaluation stale.
- FencingPolicy and StorageSafetyPolicy rev1 Evaluations do not uplift to newly-current rev2.
- parallel same-ID Proof calls converge to one immutable Proof; Fencing emits one `FENCED` transition.
- Evaluation and Proof replay returns the original immutable digest without generation amplification.
- no Proof creates Recovery authority, new Job/Command/Lease, compute/PCI/network/storage allocation claim, VM power request, or Host authority mutation.

## Result states

Fencing Evaluation: `PROVEN`, `NOT_PROVEN`, `UNKNOWN`, `CONFLICTING_INPUT`, `STALE_POLICY`, `STALE_EPOCH`, `NO_FENCING_POLICY`.

Storage Evaluation: `SAFE`, `NOT_SAFE`, `UNKNOWN`, `CONFLICTING_INPUT`, `STALE_POLICY`, `STALE_EPOCH`, `NO_STORAGE_SAFETY_POLICY`.

## Remaining gate

Recovery Eligibility remains a separate future authority that must bind the exact current usability of both proofs plus destination placement, failure-domain, recovery-budget, and current resource safety inputs. Neither proof alone nor both proofs together authorize mutation.
