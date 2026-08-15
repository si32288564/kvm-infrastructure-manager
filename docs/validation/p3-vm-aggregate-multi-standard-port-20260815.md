# Phase 3 VM Aggregate Multi STANDARD Port Qualification

Date: 2026-08-15  
Database: PostgreSQL 17  
Migration: 085  
Profile: one VM, two logical STANDARD Ports, one verified ROOT Volume, no PCI

## Result

```text
VM_AGGREGATE_MULTI_PORT_PROFILE          = PASS
VM_AGGREGATE_PORT_SET_CANONICALIZATION   = PASS
VM_AGGREGATE_PORT_CARDINALITY_FENCING    = PASS
VM_AGGREGATE_MULTI_PORT_TERMINAL_FENCING = PASS

VM_AGGREGATE_DATA_VOLUME_PROFILE         = NOT RUN
NORTHBOUND_VM_RESOURCE                   = BLOCKED
TERRAFORM_VM_RESOURCE                    = BLOCKED
```

## Authority chain

```text
two independently VERIFIED logical Port revisions
→ caller order reversed
→ CreateVMAggregate canonicalizes by logical Port ID
→ immutable dependency snapshot (port_count=2, dense ordinals 0/1)
→ compiler re-derives two exact ordinary Network requirements
→ Final Admission binds both Ports to one selected Host
→ two immutable aggregate Port-binding evidence rows
→ destination materialization
→ typed OVS preboot Command/Attempt/observation for each exact Port binding
→ DB-derived readiness network evidence-set digest
→ RUNNING power read-back
→ two immutable aggregate Port-verification rows
→ aggregate verification VERIFIED
→ terminal-time all-Port current-authority fencing
→ aggregate terminal VERIFIED
```

Logical Port ordinals are not caller-selected interface identities. The producer sorts the exact logical Port ID/revision set before creating desired evidence. Host, binding generation, OVS interface identity and observation identity remain runtime-incarnation evidence.

## Exact campaign identities

```text
VM UUID             = 83000001-0000-4000-8000-e2930733d180
Operation           = vm-port-create-operation-e2930733d180
Dependency snapshot = vm-dependencies:83000001-0000-4000-8000-e2930733d180:1
Admission           = admission:vm-placement:vm-port-create-operation-e2930733d180:1
Host                = vm-port-host-e2930733d180
Plan                = vm-plan:vm-port-create-operation-e2930733d180:1
Verification        = vm-port-verification-e2930733d180
Terminal            = vm-port-terminal-e2930733d180

ordinal 0 Port      = vm-port-resource-0-e2930733d180 revision 1
binding evidence    = vm-port-binding:vm-port-create-operation-e2930733d180:0 generation 1
OVS evidence        = ovs-realize-evidence-aggregate-0-e2930733d180 generation 1

ordinal 1 Port      = vm-port-resource-1-e2930733d180 revision 1
binding evidence    = vm-port-binding:vm-port-create-operation-e2930733d180:1 generation 1
OVS evidence        = ovs-realize-evidence-aggregate-1-e2930733d180 generation 1
```

## Negative and replay coverage

- duplicate logical Port identity in one create request: rejected before mutation;
- stale revision in either requested Port: rejected;
- reversed caller order: canonical ordinals and digest remain Port-ID ordered;
- missing all OVS observations: aggregate verification rejected;
- one of two OVS current observations absent while READY/RUNNING evidence remains: aggregate verification rejected in a rollback branch;
- one exact Port binding generation drifted after verification: terminal rejected in a rollback branch;
- dependency Port, aggregate Port-binding and aggregate Port-verification immutable rows: UPDATE rejected;
- one STANDARD Port legacy request shape and its Recovery→EVACUATE mobility chain: PASS unchanged.

No caller supplies network readiness, Port binding success or aggregate verification state. Migration 085 widens only the immutable mobility history cardinality constraint from `0..1` to the producer-qualified `0..2`; it does not introduce a physical identity into VM desired state. A larger set remains fail-closed until separately qualified.

## Regression evidence

The final commit was qualified with fresh PostgreSQL 17 migrations 001–085, migration replay, all persistence integration, race integration, `go test ./...`, `go test -race ./...`, `make check`, documentation lint and `git diff --check`.
