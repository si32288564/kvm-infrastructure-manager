# Host EVACUATE One-OVN-Port Positive E2E and Network Handoff Qualification

- Date: 2026-08-13
- Database: disposable PostgreSQL 17
- Migration range: 001–069, fresh apply and replay
- Profile: one managed VM, one OVS-bound OVN logical Port, zero PCI, one synthetic Local LVM boot root
- Scope: synthetic PostgreSQL/Network authority qualification; no production Host or SSH mutation

## Result

The same logical Port identity moved from source A to destination B through the
existing generic retirement, source-quiescence, Final Admission Handoff, OVN
runtime worker, pre-boot OVS realization, and post-boot dataplane authorities:

```text
Host Drain
-> PostgreSQL workload snapshot (1 VM / 1 Port)
-> bounded child claim (1/1)
-> typed source SHUTOFF / Lease / Attempt / lost response / read-back
-> Planned Source Quiescence
-> source Storage SAFE
-> generic OVN Port binding retirement
-> source Port quiescence read-back
-> exact PortBindingHandoff 1/1 -> 2/2
-> source Placement release / destination Final Admission
-> generic relocation materialization generation 2
-> destination define / image
-> ordinary destination OVN runtime reconcile and NB/SB read-back
-> ordinary pre-boot OVS realization / READY
-> typed destination power-on / RUNNING read-back
-> post-boot OVS dataplane CONVERGED
-> DB-derived Child Verification VERIFIED
-> Child Terminal VERIFIED
-> Parent Terminal VERIFIED
-> source Host DRAINED
```

The central invariant passed:

```text
source Port ID = destination Port ID
source MAC     = destination MAC
source IP      = destination IP

source Port/Binding generation      = 1 / 1
destination Port/Binding generation = 2 / 2
source active dataplane overlap     = none
```

Parent terminal facts:

```text
workload_count               = 1
verified_count               = 1
active_source_workload_count = 0
post_drain_admission_count   = 0
parent                       = VERIFIED
source drain                 = DRAINED
destination VM              = RUNNING / MATCHED
destination dataplane       = CONVERGED
cleanup operations           = 0
```

## Migration 069 scope

Migration 069 closes provenance gaps without adding a Network mutation
primitive:

- planned source quiescence authorizes one exact existing generic OVN binding-retirement operation
- the child verifier binds exact immutable retirement, Network quiescence,
  PortBindingHandoff, destination pre-boot realization, NB observation, SB
  observation, and OVS dataplane evidence
- terminal rechecks current Port/Binding/Handoff/NB/SB/preboot/dataplane
  projections against that immutable set

Logical Port, Network, MAC Claim, and IP Claim rows are preserved. No
Recovery Operation, Failure Epoch, Fencing Proof, Recovery-specific Handoff,
or EVACUATE-specific OVN/OVS command exists.

## Derived child result

```text
source storage      = SAFE
source network      = RETIRED
source PCI          = NOT_REQUIRED
destination storage = CURRENT
destination network = CURRENT
destination PCI     = NOT_REQUIRED
destination power   = RUNNING
source ownership    = RETIRED
verification        = VERIFIED
```

Every state is derived from PostgreSQL evidence; the caller supplies only
evidence identities.

## Negative and drift qualification

- Network power authority is unavailable before pre-boot Network realization
- VM RUNNING and correct OVN NB state without destination OVS dataplane cannot verify the child
- missing destination SB convergence cannot verify the child
- MAC identity drift cannot verify the child
- source retirement revival/staleness cannot verify the child
- before Handoff, verified source absence and no destination Handoff prove no active overlap
- terminal rejects Port generation, Binding generation, current Handoff,
  pre-boot realization generation/evidence, SB evidence, OVS dataplane
  generation/evidence, destination VM plan, and power generation drift
- immutable retirement, Network quiescence, Handoff, realization, dataplane,
  child Network binding, child verification, child terminal, and parent
  terminal evidence reject UPDATE
- Failure Epoch and Fencing Proof counts are unchanged

Historical Handoff evidence remains immutable and independently addressable;
the terminal currentness checks do not rewrite or invalidate earlier
incarnations after terminal.

## Gate matrix

| Gate | Result |
|---|---|
| EVACUATION_SOURCE_QUIESCENCE_AUTHORITY | PASS |
| EVACUATION_CHILD_EVIDENCE_VERIFICATION | PASS |
| EVACUATION_CHILD_TERMINAL_AUTHORITY | PASS |
| EVACUATION_DESTINATION_EVIDENCE_BINDING | PASS |
| EVACUATION_TERMINAL_DRIFT_FENCING | PASS |
| EVACUATION_NONEMPTY_PARENT_TERMINAL | PASS |
| EVACUATE_ZERO_PORT | PASS |
| EVACUATION_NETWORK_HANDOFF | PASS |
| EVACUATE_OVN_PORT | PASS |
| EVACUATION_NONEMPTY_OVN_PARENT_TERMINAL | PASS |
| EVACUATION_REPEATED_INCARNATION | NOT RUN |
| EVACUATE_LOCAL_LVM | BLOCKED |
| EVACUATE_PCI_SRIOV | BLOCKED |
| REAL_TWO_HOST_KVM_HOST_EVACUATION | BLOCKED |

The Local LVM root is synthetic. This campaign does not prove mutable guest
data preservation or real cross-Host Local LVM semantics. The real-Host gate
also remains blocked because no disposable production-like workload was used.

## Campaign metrics

```text
workloads                        = 1
Ports                            = 1
maximum concurrency              = 1
source Port generation           = 1
source Binding generation        = 1
destination Port generation      = 2
destination Binding generation   = 2
source retirement attempts       = 1
Handoff count                    = 1
destination OVN realization      = 1
source SHUTOFF observations      = 1
destination RUNNING observations = 1
child verifications              = 1
child terminals                  = 1
parent terminals                 = 1
parent outcome                   = VERIFIED
```

## Exact successful campaign identities

```text
source Host                 = evacuation-ovn-source-1786606418714506000
destination Host            = evacuation-ovn-destination-1786606418714506000
VM UUID                     = 69000000-0000-4000-8000-418714510000
VM generation               = 1
source Admission            = admission:evacuation-ovn-source-1786606418714506000
source Plan                 = evacuation-ovn-source-plan-1786606418714506000
source materialization gen  = 1
destination Admission       = admission:evacuation-ovn-destination-1786606418714506000
destination Plan            = evacuation-ovn-destination-plan-1786606418714506000
destination materialization = 2
Network ID                  = evacuation-ovn-network-1786606418714506000
Subnet ID                   = evacuation-ovn-subnet-1786606418714506000
Port ID                     = evacuation-ovn-port-1786606418714506000
MAC                         = 02:00:00:69:00:01
IP                          = 192.0.2.69
source Port/Binding gen     = 1 / 1
destination Port/Binding gen= 2 / 2
source retirement evidence  = evacuation-ovn-retirement-evidence-1786606418714506000
source quiescence evidence  = evacuation-ovn-port-quiescence-evidence-1786606418714506000
Handoff ID                  = evacuation-handoff:evacuation-ovn-destination-1786606418714506000:1
destination realization     = ovs-realize-evidence-destination-1786606418714506000
destination NB observation  = ovn-nb-destination-1786606418714506000
destination SB observation  = ovn-sb-destination-1786606418714506000
destination dataplane       = ovs-dataplane-evidence-destination-1786606418714506000
child verification          = evacuation-ovn-child-verification-1786606418714506000
child terminal              = evacuation-ovn-child-terminal-1786606418714506000
parent terminal             = evacuation-ovn-parent-terminal-1786606418714506000
```

## Safety assertions

```text
logical Port deleted                       = no
MAC released                               = no
IP released                                = no
destination identity recreated             = no
source/destination active dataplane overlap = no
caller-supplied Network RETIRED             = none
caller-supplied Network CURRENT             = none
fake Failure Epoch                         = none
fake Fencing Proof                         = none
fake Recovery Operation                    = none
direct SSH backend mutation                = none
production workload mutation               = none
historical evidence rewritten              = none
cleanup required for parent VERIFIED       = no
Host automatically undrained               = no
```

## Validation commands

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'TestMigratePostgreSQLIntegration|TestHostEvacuation' -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -timeout 600s ./internal/persistence/postgres
go test ./...
go test -race ./...
make check
git diff --check
```
