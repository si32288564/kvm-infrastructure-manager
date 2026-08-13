# ADR 0027: Generic Local LVM source cleanup and capacity reclamation

## Decision

Local LVM source deletion is a closed consumer of the Migration 064/065 generic Backend Cleanup aggregate. A planned materialization terminal is a producer only after PostgreSQL re-derives the exact source VM/materialization/plan, Storage SAFE evidence, Copy Terminal, source Volume/Binding/backend/VG/LV UUID, inactive attachment, destination terminal, and current non-source VM incarnation.

The Agent accepts `LOCAL_LVM_VOLUME_DELETE` and `LOCAL_LVM_VOLUME_DELETE_READ_BACK` only. Their payload contains stable identities and the expected LV UUID; it contains no path, VG/LV name, shell, argv, or flags. The Agent derives the LV name from the Volume ID and an administrator mapping from VG UUID to VG name. An open exact LV is fenced. A same-name LV with another UUID is foreign and is never deleted; it also proves that the obsolete exact UUID is absent.

Command success, response loss, and Lease expiry do not prove deletion. `READ_BACK_FIRST` must observe either the exact inactive LV as `PRESENT` before apply, or the exact old UUID as `ABSENT` before the generic cleanup terminal becomes `VERIFIED`.

Capacity is a separate terminal consumer. The source claim moves to `RELEASE_PENDING` when cleanup authority is committed and remains unavailable to Placement. Only immutable exact-absence terminal evidence can create `local_lvm_capacity_reclamation_evidence`, release the claim, revoke the old Binding, and mark the obsolete Volume deleted. Replay is idempotent and backend-generation drift fails closed.

## Consequences

EVACUATE and Recovery terminal decisions and the current destination VM are independent of cleanup outcome. Cleanup can run after later incarnations, but each operation remains bound to its historical source Host/materialization/Volume/Binding/LV UUID. Real production mutation remains a separate opt-in qualification requiring an isolated disposable workload and deployed Agent authority.
