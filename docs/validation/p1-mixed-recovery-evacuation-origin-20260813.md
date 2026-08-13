# Mixed Recovery → Planned Host EVACUATE Origin Qualification

- Date: 2026-08-13
- Database: disposable PostgreSQL 17
- Migration range: 001–069, fresh apply and replay
- Scope: synthetic PostgreSQL/OVN/OVS authority qualification
- Real Host qualification: BLOCKED; no production workload was used
- New migration: none

## Result

One logical VM and OVN Port traversed two different orchestration origins while
sharing one resource-incarnation history:

```text
Initial:
A / VM materialization 1 / Port-Binding 1/1 / RUNNING

RECOVERY origin:
Failure Observation → Epoch → Confirmation → Fencing Proof → Storage Safety
→ Recovery Eligibility/Budget/Operation → A 1/1 retirement/quiescence
→ A→B Handoff → B materialization 2 / Port-Binding 2/2 / RUNNING
→ Recovery Verification VERIFIED → Recovery Terminal VERIFIED
→ Epoch RECOVERED / Budget RELEASED

PLANNED_EVACUATION origin:
B drain → snapshot of B/mat2/2/2 → typed SHUTOFF LOST/read-back
→ Planned Source Quiescence → planned Storage SAFE → B 2/2 retirement
→ B→C Handoff → Final Admission → generic relocation
→ C materialization 3 / Port-Binding 3/3 / RUNNING
→ Child VERIFIED → Parent VERIFIED → B DRAINED
```

Recovery authority and EVACUATE authority remain disjoint. Only the VM,
materialization, Port/Binding, MAC/IP, and historical cleanup lineage are
shared.

## Authority boundary and defects closed

Migration 069 already supports the mixed lineage; Migration 070 was not
required. The campaign found a terminal-origin confusion gap: the same textual
terminal ID could previously be accepted independently by Recovery and
EVACUATE terminal tables. Both terminal consumers now reject IDs already owned
by the other origin while retaining exact same-origin replay.

The planned source root read-back fixture now obtains a monotonically newer
observation generation from PostgreSQL. This is necessary when the B root
already has a Recovery destination attachment observation; it does not equate
Recovery attachment evidence with planned Storage SAFE.

## Cross-origin negative matrix

- Recovery Storage Safety Proof cannot release planned B placement
- Recovery A/1/1 retirement operation cannot retire planned B/2/2
- Recovery A→B Handoff/quiescence cannot authorize the C Admission
- Recovery Operation cannot authorize generic planned C materialization
- Recovery B power evidence cannot verify the C destination
- Recovery Verification cannot complete the EVACUATE child
- Recovery Terminal ID cannot finalize the EVACUATE parent
- EVACUATE Parent Terminal ID cannot enter the Recovery terminal consumer
- EVACUATE B source-authority loss becomes `RECOVERY_REQUIRED` /
  `SOURCE_UNREACHABLE` without changing the completed A→B Recovery
- terminal-time Port/Binding drift to 4/4 rejects the EVACUATE verification
- immutable Recovery epoch, proof, verification, terminal, retirement, and
  Handoff rows reject UPDATE

No Fencing Proof was treated as planned quiescence, and no planned evidence
was treated as a Recovery proof.

## Current and historical state

```text
VM UUID       = unchanged
Port ID       = unchanged
Network       = unchanged
Subnet        = unchanged
MAC/IP        = unchanged

final VM Host = C
materialization = 3
Port/Binding    = 3/3
VM power        = RUNNING / MATCHED
active dataplane count = 1
latest Handoff  = B→C

A→B Recovery Handoff = present and immutable
B→C EVACUATE Handoff = present and immutable
A 1/1 retirement     = present and immutable
B 2/2 retirement     = present and immutable
```

Delayed A cleanup resolves from the historical Recovery A→B evidence after
current has advanced to C. Delayed B cleanup resolves independently from the
planned B→C evidence. Neither cleanup is required for either terminal.

## Gate matrix

| Gate | Result |
|---|---|
| EVACUATION_MIXED_RECOVERY_ORIGIN | PASS |
| MIXED_ORIGIN_VM_LINEAGE | PASS |
| MIXED_ORIGIN_OVN_HANDOFF | PASS |
| MIXED_ORIGIN_HISTORICAL_REPLAY | PASS |
| MIXED_ORIGIN_ABA_FENCING | PASS |
| MIXED_ORIGIN_DELAYED_CLEANUP | PASS |
| EVACUATION_REPEATED_INCARNATION | PASS |
| EVACUATION_NETWORK_HANDOFF | PASS |
| EVACUATE_OVN_PORT | PASS |
| EVACUATE_ZERO_PORT | PASS |
| EVACUATE_LOCAL_LVM | BLOCKED |
| EVACUATE_PCI_SRIOV | BLOCKED |
| REAL_TWO_HOST_KVM_HOST_EVACUATION | BLOCKED |

The synthetic Local LVM roots qualify authority joins, not real mutable guest
data preservation across Hosts.

## Campaign metrics

```text
Recovery parents/operations = 1
EVACUATE parents            = 1
Failure Epochs              = 1
Fencing Proofs              = 1
EVACUATE drains             = 1
Port retirements            = 2
Port Handoffs               = 2
materializations            = 1 → 2 → 3
Recovery A→B terminal       = VERIFIED
EVACUATE B→C terminal       = VERIFIED
final Host                  = C
final Port/Binding          = 3/3
active dataplane count      = 1
```

## Exact successful campaign identities

```text
VM UUID = 71000000-0000-4000-8000-010508909000
Network = mixed-origin-network-1786610010508903000
Subnet  = mixed-origin-subnet-1786610010508903000
Port    = mixed-origin-port-1786610010508903000
MAC     = 02:00:00:71:00:01
IP      = 192.0.2.71
Host A  = mixed-origin-a-1786610010508903000
Host B  = mixed-origin-b-1786610010508903000
Host C  = mixed-origin-c-1786610010508903000
```

Recovery A→B:

```text
Failure Epoch       = mixed-origin-failure-epoch-1786610010508903000
Confirmation        = mixed-origin-confirmation-decision-1786610010508903000
Fencing Proof       = mixed-origin-fencing-proof-1786610010508903000
Fencing digest      = 1b996934085356ca4dd25881d3cd7565b3b219e06de27db9f1e7f0df6b978c9a
Storage Proof       = mixed-origin-storage-proof-1786610010508903000
Storage digest      = 2794cbfbf084c67bf5c0f718619220cdcac2e277495954e398850dfe3c09cea9
Recovery Operation  = mixed-origin-recovery-operation-1786610010508903000
source Admission    = admission:mixed-origin-source-1786610010508903000
destination Admission = admission:recovery-placement:mixed-origin-recovery-operation-1786610010508903000
source Plan         = mixed-origin-plan-a-1786610010508903000
destination Plan    = mixed-recovery-plan-b-1786610010508903000
materialization     = 1 → 2
Port/Binding        = 1/1 → 2/2
retirement evidence = mixed-origin-recovery-retirement-evidence-1786610010508903000
retirement digest   = b65ef6065d6f482c7e1e37d706596c2df738e79cff5fc2913351d756c073c24e
Handoff             = recovery-handoff:mixed-origin-recovery-operation-1786610010508903000:1
Handoff digest      = eca77ebc674fa67951d10f2bff01323d975dbb65a46848afa7e712d29583ecda
Verification        = mixed-recovery-verification-1786610010508903000
Verification digest = bc30b07901643d34028321600b8bab739af93af896f936f0178810d2586b1d6d
Terminal            = mixed-recovery-terminal-1786610010508903000
Terminal digest     = a01a2e63b3731798f53b77fd2d832e0fef1a1be581980906c0f7947cf8db7101
```

Planned EVACUATE B→C:

```text
Operation           = evacuation-repeated-mixed-e2-1786610010508903000
source Admission    = admission:recovery-placement:mixed-origin-recovery-operation-1786610010508903000
destination Admission = admission:evacuation-repeated-destination-mixed-e2-1786610010508903000
source Plan         = mixed-recovery-plan-b-1786610010508903000
destination Plan    = evacuation-repeated-destination-plan-mixed-e2-1786610010508903000
materialization     = 2 → 3
Port/Binding        = 2/2 → 3/3
retirement evidence = evacuation-repeated-retirement-evidence-mixed-e2-1786610010508903000
retirement digest   = bb2450d0b441fa67f3a259595d72252766860faddcc6adf0b153e32b3e7044db
Handoff             = evacuation-handoff:evacuation-repeated-destination-mixed-e2-1786610010508903000:1
Handoff digest      = 68823bcce45619c3938572b70ba5cf62312a9b8ba4ff96dc45f59752935b27e6
Child Verification  = evacuation-repeated-child-verification-mixed-e2-1786610010508903000
Verification digest = 6c2372223104a376310fbd28e9e6a997799fa5f04982ac71aa1d62396766d253
Child Terminal      = evacuation-repeated-child-terminal-mixed-e2-1786610010508903000
Child terminal dig. = 00d08c6c16124936a4f5a391a05bda2bbea1f5a3c7afe0e70d170d4243b01096
Parent Terminal     = evacuation-repeated-parent-terminal-mixed-e2-1786610010508903000
Parent terminal dig.= 26bf877568ba0d5951a104ac7a44657f1eddfa7a8041746fb7fcaf2ed414fe8e
```

## Safety assertions

```text
Recovery authority reused as EVACUATE authority = no
EVACUATE authority reused as Recovery authority = no
Fencing Proof treated as planned quiescence     = no
planned quiescence treated as Fencing Proof     = no
Recovery Budget used for EVACUATE               = no
EVACUATE slot used for Recovery                  = no
VM UUID / Port / MAC / IP changed                = no
active dataplane overlap                         = no
direct SSH backend mutation                      = none
production workload mutation                     = none
historical evidence rewritten/deleted            = none
```

## Validation commands

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'TestMigratePostgreSQLIntegration|TestHostEvacuation|TestMixedRecovery' -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -timeout 600s ./internal/persistence/postgres
go test ./...
go test -race ./...
make check
git diff --check
```
