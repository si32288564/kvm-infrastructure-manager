# Phase 3 VM Aggregate Recovery No-Drift Qualification

- Date: 2026-08-15
- Database: disposable PostgreSQL 17 (`postgres:17-alpine`)
- Migration: 084 (qualification follow-on; no new migration)
- Scope: aggregate-origin Recovery association and cross-mobility incarnation continuity

## Result

| Gate | Result |
|---|---|
| `VM_AGGREGATE_RECOVERY_NO_DESIRED_DRIFT` | PASS |
| `VM_AGGREGATE_RECOVERY_TERMINAL_BINDING` | PASS |
| `VM_AGGREGATE_RECOVERY_NETWORK_PROVENANCE` | PASS |
| `VM_AGGREGATE_CROSS_MOBILITY_CAS` | PASS |
| `VM_AGGREGATE_EVACUATE_NO_DESIRED_DRIFT` | PASS (regression) |
| `VM_AGGREGATE_MULTI_PORT_PROFILE` | NOT RUN |
| `VM_AGGREGATE_DATA_VOLUME_PROFILE` | NOT RUN |
| `NORTHBOUND_VM_RESOURCE` | BLOCKED |
| `TERRAFORM_VM_RESOURCE` | BLOCKED |

## Qualified chain

```text
CreateVMAggregate
→ exact one STANDARD Port + one boot Volume dependency snapshot
→ Final Admission / materialization / READY / RUNNING on Host A
→ aggregate terminal VERIFIED
→ typed SHUTOFF observation
→ Failure Epoch CONFIRMED
→ source execution fencing PROVEN
→ source root and Storage safety proofs
→ source OVN retirement / OVS quiescence
→ source materialization RETIRED
→ Recovery eligibility and budget claim
→ Recovery Final Admission on Host B
→ destination logical Port realization VERIFIED
→ Recovery materialization generation 2
→ definition / image / OVN / OVS / dataplane / root attachment
→ destination RUNNING
→ Recovery verification VERIFIED
→ Recovery terminal VERIFIED
→ aggregate association generation 1 (RECOVERY)
→ planned EVACUATE B→C
→ aggregate association generation 2 (HOST_EVACUATION)
```

## Authority separation

Recovery's network evidence-set digest covers exact NB, SB, OVS, dataplane, source-quiescence and handoff evidence. EVACUATE child verification uses the destination preboot OVS evidence-set digest. These are intentionally different evidence algorithms. The aggregate consumer:

1. binds the Recovery terminal digest to current destination readiness;
2. independently proves the exact logical Port revision/digest;
3. independently proves the exact destination binding and preboot realization;
4. advances only the runtime binding after all current checks pass.

No caller translates either digest into a boolean or substitutes one evidence set for the other.

## No-drift and CAS assertions

```text
logical VM revision             = 1 → 1 → 1
runtime intent generation       = 1 → 1 → 1
dependency snapshot/digest      = unchanged
VM desired digest               = unchanged
logical Port revision/digest    = unchanged
runtime Host                    = A → B → C
materialization generation      = 1 → 2 → 3
mobility association generation = 0 → 1 → 2
```

- post-Recovery destination plan drift before association is rejected in a rollback branch;
- replay with the same Recovery association identity is idempotent;
- the same association ID cannot be rebound as Host EVACUATION;
- the second association requires the exact B incarnation produced by the first;
- the old Recovery terminal under a new association ID cannot advance the post-EVACUATE C incarnation;
- immutable mobility association evidence rejects UPDATE;
- existing legacy Recovery→EVACUATE mixed-origin qualification remains PASS.

## Safety assertions

```text
fake Recovery Operation                    = none
fake Failure Epoch                         = none
caller-supplied destination Host authority = none
caller-supplied READY/RUNNING authority    = none
logical VM/Port mutation for mobility      = none
historical evidence rewritten              = none
production workload mutation               = none
```

Multi-Port, data Volume, logical update/delete, Northbound `/api/v1/vms`, Terraform `kim_vm` and production Host qualification remain separate gates.

## Regression evidence

- fresh PostgreSQL 17 Migration 001–084 and replay: PASS;
- aggregate-origin Recovery A→B→EVACUATE B→C no-drift E2E: PASS;
- legacy mixed Recovery→EVACUATE integration: PASS;
- all persistence integration on isolated PostgreSQL 17: PASS (`45.942s`);
- all persistence race integration on an independent PostgreSQL 17 database: PASS (`52.790s`);
- `go test ./...`: PASS;
- `go test -race ./...`: PASS;
- `make check`, Go vet, Provider tests, documentation lint and `git diff --check`: PASS.
