# Phase 3 VM Aggregate Planned EVACUATE No-Drift Qualification

- Date: 2026-08-15
- Database: disposable PostgreSQL 17 (`postgres:17-alpine`)
- Migration: 084
- Scope: internal aggregate association; no Northbound API or Terraform VM resource

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_MOBILITY_ASSOCIATION_AUTHORITY` | PASS |
| `VM_AGGREGATE_EVACUATE_NO_DESIRED_DRIFT` | PASS |
| `VM_AGGREGATE_EVACUATE_STANDARD_PORT_HANDOFF` | PASS |
| `VM_AGGREGATE_MOBILITY_REPLAY_IDEMPOTENCY` | PASS |
| `VM_AGGREGATE_MOBILITY_IMMUTABILITY` | PASS |
| `VM_AGGREGATE_RECOVERY_NO_DESIRED_DRIFT` | NOT RUN |
| `VM_AGGREGATE_MULTI_PORT_PROFILE` | NOT RUN |
| `VM_AGGREGATE_DATA_VOLUME_PROFILE` | NOT RUN |
| `NORTHBOUND_VM_RESOURCE` | BLOCKED |
| `TERRAFORM_VM_RESOURCE` | BLOCKED |

## Qualified chain

```text
logical VM revision 1 / runtime intent 1
+ exact one STANDARD Port revision 1
+ one VERIFIED boot Volume
→ aggregate Final Admission on Host A
→ definition / image / OVN / OVS / READY / RUNNING
→ aggregate verification and terminal VERIFIED
→ StartHostEvacuation(A)
→ immutable workload snapshot 1
→ source shutdown LOST / SHUTOFF read-back MATCHED
→ planned quiescence and source Storage SAFE
→ source OVN retirement and OVS quiescence
→ source Placement release
→ destination Final Admission on Host B
→ destination logical Port realization Operation VERIFIED
→ Local LVM copy/content identity VERIFIED
→ relocation authorization
→ destination materialization generation 2
→ destination OVN/OVS/readiness/RUNNING
→ child verification and terminal VERIFIED
→ parent terminal VERIFIED / Host A DRAINED
→ AssociateVMAggregateMobility
→ runtime binding A→B
→ same logical VM revision/runtime intent/dependency/desired
→ same logical Port revision/digest
```

The association consumer accepts only an already-terminal mobility proof whose exact source Admission, Host, VM generation, plan and materialization generation equal the current aggregate runtime binding. It then revalidates the exact destination Admission, Host, plan digest, readiness evidence-set digest, power observation and one-Port realization. Only rebuildable runtime pointers advance. It does not infer success from an evacuation row existing.

## Negative and replay coverage

- the same association ID and terminal replay returns the same immutable association;
- rebinding the same association ID as `RECOVERY` is rejected;
- a non-EVACUATE terminal identifier is rejected;
- destination current-plan drift after parent terminal and before association is rejected in a rollback branch;
- immutable association evidence rejects UPDATE;
- destination Port resource realization must become VERIFIED before ordinary OVN runtime intent is accepted;
- source logical Port retirement does not delete or revise logical Port desired authority;
- terminal-time source/destination exactness remains enforced by the existing EVACUATE child and parent authorities.

## Exact preservation assertions

```text
VM revision                       = 1 before / 1 after
runtime intent generation         = 1 before / 1 after
dependency snapshot digest        = unchanged
VM desired digest                 = unchanged
logical Port revision             = 1 before / 1 after
logical Port desired digest       = unchanged
Host / Admission / plan           = A incarnation → B incarnation
mobility association generation   = 0 → 1
caller-supplied destination READY = none
caller-supplied destination RUNNING = none
fake Recovery authority           = none
historical evidence rewritten     = none
production workload mutation      = none
```

## Scope boundary

Migration 084 contains both closed discriminator branches (`RECOVERY`, `HOST_EVACUATION`) so a later aggregate-origin Recovery terminal can use the same association evidence model. This campaign executed only planned EVACUATE. Therefore `VM_AGGREGATE_RECOVERY_NO_DESIRED_DRIFT` remains `NOT RUN`; no Recovery PASS is inferred from schema or query existence.

Production OVN/OVS, Local LVM transport and real-Host status are unchanged. Multi-Port, data Volume, desired update/delete, Northbound `/api/v1/vms` and Terraform `kim_vm` remain later gates.

## Regression evidence

- fresh PostgreSQL 17 Migration 001–084 and replay: PASS;
- planned one-Port EVACUATE aggregate no-drift E2E: PASS;
- all persistence integration on isolated PostgreSQL 17: PASS (`47.530s`);
- all persistence race integration on an independent PostgreSQL 17 database: PASS (`59.302s`);
- `go test ./...`: PASS;
- `go test -race ./...`: PASS;
- `make check`, Go vet, Provider tests, documentation lint and `git diff --check`: PASS.
