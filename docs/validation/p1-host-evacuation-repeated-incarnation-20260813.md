# Repeated Planned Host EVACUATE A→B→C Qualification

- Date: 2026-08-13
- Database: disposable PostgreSQL 17
- Migration range: 001–069, fresh apply and replay
- Scope: synthetic PostgreSQL/OVN/OVS authority qualification
- Real Host qualification: BLOCKED; no production workload was used
- Mixed Recovery→EVACUATE origin: NOT RUN
- New migration: none

## Result

Two independent planned Host Evacuation parents moved the same VM and logical
Port through three physical incarnations:

```text
E1: A / VM materialization 1 / Port-Binding 1/1
 -> source SHUTOFF / Storage SAFE / OVN retirement / Network quiescence
 -> exact PortBindingHandoff
 -> B / VM materialization 2 / Port-Binding 2/2
 -> destination NB/SB/OVS convergence / RUNNING
 -> child VERIFIED / parent VERIFIED / A DRAINED

E2: B / VM materialization 2 / Port-Binding 2/2
 -> source SHUTOFF / Storage SAFE / OVN retirement / Network quiescence
 -> exact PortBindingHandoff
 -> C / VM materialization 3 / Port-Binding 3/3
 -> destination NB/SB/OVS convergence / RUNNING
 -> child VERIFIED / parent VERIFIED / B DRAINED
```

The logical identity remained unchanged before E1, after E1, and after E2:

```text
VM UUID = unchanged
Port ID = unchanged
Network = unchanged
Subnet  = unchanged
MAC     = unchanged
IP      = unchanged
```

Final current authority:

```text
VM Host                    = C
VM materialization         = 3
VM power                   = RUNNING / MATCHED
Port generation            = 3
Binding generation         = 3
latest Handoff             = B→C
A drain                    = DRAINED
B drain                    = DRAINED
active destination dataplane count = 1
```

## Schema and authority audit

Migration 068/069 already use child-scoped keys. They preserve two independent
relocation authorities and two independent Network evidence bindings for the
same VM and Port. No Migration 070 was needed.

The campaign did expose one consumer replay defect: an E1 parent terminal ID
could be passed to `FinalizeHostEvacuation` for E2 because the existing row was
not checked against the requested parent. The consumer now requires exact
terminal ID, evacuation operation, evacuation generation, current terminal
projection, and DRAINED projection. Same-operation response-loss replay remains
idempotent; cross-parent uplift is rejected.

Historical Port retirements and Handoffs remain exact-incarnation facts. The
Port-wide current projections point to 3/3 without modifying 1/1→2/2 or
2/2→3/3 history.

## ABA and negative qualification

- E1 shutdown authority/Command cannot authorize the B source incarnation
- E1 Storage SAFE cannot release or relocate the B/mat2 source
- E1 Port retirement authority for 1/1 cannot become the E2 2/2 retirement
- E1 Network quiescence/Handoff generations cannot create the C Admission
- E1 relocation authority cannot create the C/mat3 plan
- E1 B destination power evidence cannot verify the C destination
- E1 child verification cannot terminalize the E2 child
- E1 parent terminal ID cannot terminalize the E2 parent
- an E2 rollback-only blocked branch leaves E1 VERIFIED and VM current on B
- E2 verification cannot terminalize after current Port/Binding drift to 4/4
- wrong source/destination Host, Admission, plan, materialization and
  Port/Binding generations fail through the same exact joins
- Failure Epoch and Fencing Proof counts are unchanged

The old E1 parent terminal remains replayable for E1 after E2. This is
historical replay, not authority uplift.

## Historical reconstruction

After E2, all of the following remain present and immutable:

```text
A shutdown / planned quiescence / Storage SAFE
A 1/1 retirement / Network quiescence / A→B Handoff
B destination realization / dataplane
E1 child verification / child terminal / parent terminal

B shutdown / planned quiescence / Storage SAFE
B 2/2 retirement / Network quiescence / B→C Handoff
C destination realization / dataplane
E2 child verification / child terminal / parent terminal
```

The historical cleanup resolver returns an exact one-of-one evidence set for:

```text
delayed A cleanup after current advanced to C = resolvable
delayed B cleanup after B→C                  = resolvable
```

No cleanup operation is required for either parent to remain VERIFIED.

## Gate matrix

| Gate | Result |
|---|---|
| EVACUATION_REPEATED_INCARNATION | PASS |
| EVACUATION_REPEATED_VM_RELOCATION | PASS |
| EVACUATION_REPEATED_OVN_HANDOFF | PASS |
| EVACUATION_HISTORICAL_INCARNATION_REPLAY | PASS |
| EVACUATION_CROSS_GENERATION_ABA_FENCING | PASS |
| EVACUATION_NETWORK_HANDOFF | PASS |
| EVACUATE_OVN_PORT | PASS |
| EVACUATE_ZERO_PORT | PASS |
| EVACUATION_NONEMPTY_PARENT_TERMINAL | PASS |
| EVACUATION_MIXED_RECOVERY_ORIGIN | NOT RUN |
| EVACUATE_LOCAL_LVM | BLOCKED |
| EVACUATE_PCI_SRIOV | BLOCKED |
| REAL_TWO_HOST_KVM_HOST_EVACUATION | BLOCKED |

The three synthetic Local LVM roots prove incarnation authority and exact
Storage safety joins, not mutable guest-data preservation across Hosts.

## Campaign metrics

```text
evacuation parents        = 2
workloads per parent      = 1
child claims              = 2
source shutdown attempts  = 2
source shutdown LOST      = 2
Port retirements          = 2
Port Handoffs             = 2
materializations          = 1 -> 2 -> 3
source/destination         = A→B, B→C
child verifications       = 2
child terminals           = 2
parent terminals          = 2
final Host                = C
final VM RUNNING          = yes
final Port generation     = 3
final Binding generation  = 3
```

## Exact successful campaign identities

```text
VM UUID   = 70000000-0000-4000-8000-001357321000
VM gen    = 1
Network   = evacuation-repeated-network-1786608001357305000
Subnet    = evacuation-repeated-subnet-1786608001357305000
Port      = evacuation-repeated-port-1786608001357305000
MAC       = 02:00:00:70:00:01
IP        = 192.0.2.70
Host A    = evacuation-repeated-a-1786608001357305000
Host B    = evacuation-repeated-b-1786608001357305000
Host C    = evacuation-repeated-c-1786608001357305000
```

E1:

```text
source Admission       = admission:evacuation-repeated-source-1786608001357305000
destination Admission  = admission:evacuation-repeated-destination-e1-1786608001357305000
source Plan            = evacuation-repeated-plan-a-1786608001357305000
destination Plan       = evacuation-repeated-destination-plan-e1-1786608001357305000
materialization        = 1 -> 2
Port/Binding           = 1/1 -> 2/2
shutdown authority     = evacuation-repeated-shutdown-authority-e1-1786608001357305000
SHUTOFF read-back      = vm-power/evacuation-repeated-shutdown-command-e1-1786608001357305000/1
SHUTOFF obs. digest    = 334b0705e03f145ac35ed2fcfd7458cfc211c07f02475e5a18fefb240fead3ea
planned quiescence     = evacuation-repeated-planned-quiescence-e1-1786608001357305000
quiescence digest      = 4d9fe25e745f1dbea957b6b887e032a3bb6b153c462dbcecee3e3d68fc8c8fc9
Storage safety         = evacuation-repeated-storage-safety-e1-1786608001357305000
Storage safety digest  = 0a64a614a5eb3d020d3d70549895d40e6609af949754f91748f09264fa6478d8
retirement evidence    = evacuation-repeated-retirement-evidence-e1-1786608001357305000
retirement digest      = c008666c50dfb272a8416b1c550486e2f3d1daac31c27d5035c5a21790db319b
Network quiescence     = evacuation-repeated-network-quiescence-evidence-e1-1786608001357305000
Network quiescence dig.= ad5bc004bf14b54ea8fbd35bf0c4e143ecba0a50120a8206730a58cb2e4b3492
Handoff                = evacuation-handoff:evacuation-repeated-destination-e1-1786608001357305000:1
Handoff digest         = 00bf57717e8bd06d38aafefe62c5beb39f667180ce6fee22c3ba5a3cdfee0ff7
relocation authority   = evacuation-repeated-relocation-e1-1786608001357305000
relocation digest      = e0e6390e76093a61802812df3f268716b2109fc07ce07b319cce1747c5d60b10
destination realization= ovs-realize-evidence-repeated-destination-e1-1786608001357305000
realization obs. digest= 4e489fc0c087e6f06487286ae8a2bc5a237c3c2749b4a6cef471c719f04108da
destination dataplane  = ovs-dataplane-evidence-repeated-destination-e1-1786608001357305000
dataplane obs. digest  = cb3731a2892066e36ee6c303d7b0e5455ca709264b05891abfcb9bbbfcc6af21
child verification     = evacuation-repeated-child-verification-e1-1786608001357305000
verification digest    = adbc3d4d328ed6e152d4740f02910964c560d836c2a7e71676251fa7c36670cf
child terminal         = evacuation-repeated-child-terminal-e1-1786608001357305000
child terminal digest  = c1b713b812afb3191bb2d87dd27e36428baa92edfbfba38624fe81c8ca48d132
parent terminal        = evacuation-repeated-parent-terminal-e1-1786608001357305000
parent terminal digest = cdca6accc3886b99637b35321881c3abcc500a4f5e72ca335d2acc59ccd44be9
```

E2:

```text
source Admission       = admission:evacuation-repeated-destination-e1-1786608001357305000
destination Admission  = admission:evacuation-repeated-destination-e2-1786608001357305000
source Plan            = evacuation-repeated-destination-plan-e1-1786608001357305000
destination Plan       = evacuation-repeated-destination-plan-e2-1786608001357305000
materialization        = 2 -> 3
Port/Binding           = 2/2 -> 3/3
shutdown authority     = evacuation-repeated-shutdown-authority-e2-1786608001357305000
SHUTOFF read-back      = vm-power/evacuation-repeated-shutdown-command-e2-1786608001357305000/1
SHUTOFF obs. digest    = badb2266a4aceb426333721b0a66cc8e5070081704a91e56ed67cb8bffe80358
planned quiescence     = evacuation-repeated-planned-quiescence-e2-1786608001357305000
quiescence digest      = fa21dca1fe651e8020927a997ba38a9b29069d731d71239f6249f9a679ab78c2
Storage safety         = evacuation-repeated-storage-safety-e2-1786608001357305000
Storage safety digest  = 14c2b1ee8c771d80e836c255a618f34dfb12224a8d9e1964fa5836238f464b7a
retirement evidence    = evacuation-repeated-retirement-evidence-e2-1786608001357305000
retirement digest      = da4ae3674b8bf3d0d1289564e6578b6e9150ff834c66ce492614040a2851f14f
Network quiescence     = evacuation-repeated-network-quiescence-evidence-e2-1786608001357305000
Network quiescence dig.= 3c3460b634f25beaa8599976dbfc2852c8d29f7994c7ec0e1f33ae77ef010faf
Handoff                = evacuation-handoff:evacuation-repeated-destination-e2-1786608001357305000:1
Handoff digest         = a5f62206afba8bbd6b559c2dae9c0b7c79a6720da8390ee6ba7a63504ba488a8
relocation authority   = evacuation-repeated-relocation-e2-1786608001357305000
relocation digest      = 57f343c7890339f008fd21123764b3237b3454ff939043420c40b11b0eb8061e
destination realization= ovs-realize-evidence-repeated-destination-e2-1786608001357305000
realization obs. digest= bd7e595c21e3ae6af70e8719052170a9825f0df06bb4fe5f677f5326e9999b1b
destination dataplane  = ovs-dataplane-evidence-repeated-destination-e2-1786608001357305000
dataplane obs. digest  = 0ccfa37e2c4d5b07688d5c4a9615634c1ae722b05d714b93c2546f8cb7705dc8
child verification     = evacuation-repeated-child-verification-e2-1786608001357305000
verification digest    = 208ebf64329f8847344538536361d82d03759010c44ff8e7e381b44ed8775257
child terminal         = evacuation-repeated-child-terminal-e2-1786608001357305000
child terminal digest  = c79331d38bd79bb191717d586514b136f923920e7bc5b05b06f73459fd1fc9fd
parent terminal        = evacuation-repeated-parent-terminal-e2-1786608001357305000
parent terminal digest = b1bc7ba8826166ae823db5b704d9f87e5cd3d34489a35daa1ce602402331001e
```

## Safety assertions

```text
VM UUID changed                           = no
Port ID changed                           = no
MAC changed                               = no
IP changed                                = no
old authority uplifted to new generation  = no
historical evidence rewritten             = no
historical evidence deleted               = no
source/destination active overlap          = no
fake Failure Epoch                        = none
fake Fencing Proof                        = none
fake Recovery Operation                   = none
direct SSH backend mutation               = none
production workload mutation              = none
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
