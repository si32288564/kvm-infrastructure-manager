# Repeated OVN Port Retirement / Handoff Incarnation Authority

Date: 2026-08-13 (JST)

## Result

```text
REPEATED_OVN_PORT_RETIREMENT_AUTHORITY = PASS
REPEATED_PORT_BINDING_HANDOFF_AUTHORITY = PASS
HISTORICAL_RETIREMENT_REPLAY            = PASS
EXACT_INCARNATION_ABA_FENCING           = PASS
REAL_REPEATED_TWO_HOST_RECOVERY          = NOT RUN
```

- Requirement: `NET-056`
- Invariant: `INV-NET-042`
- Acceptance / fault contract: `AT-NET-048`, `FI-NET-034`
- Migration: `062_repeated_ovn_port_retirement_incarnations.sql`

## Authority model

Migration 062 replaces the Port-wide retirement primary key with exact
`(port_id, port_generation, binding_generation)` identity. Immutable terminal
evidence remains unchanged. A separate latest projection points to the highest
known retirement incarnation but is not accepted in place of an exact join.

The same PostgreSQL 17 qualification preserved one logical Port and its two
active MAC/IP Claims while exercising:

```text
Port/Binding 1/1
→ retirement R1 VERIFIED
→ ordinary immutable PortBindingHandoff
→ Port/Binding 2/2
→ ordinary OVN intent/work/read-back
→ retirement R2 VERIFIED
→ ordinary immutable PortBindingHandoff
→ Port/Binding 3/3
```

No test directly updated the Port or Binding generation projection. Both
advances were committed by `claimNetworkPortHandoffTx`, the ordinary Final
Admission PortBindingHandoff authority.

## Replay, isolation, and ABA

- R1 retained both immutable observations from its response-loss/read-back-first recovery.
- Replaying the exact R1 request after the current Port reached generation 2 returned the original R1 work identity and did not create a new mutation.
- Reusing the R1 quiescence evidence as authority for the 2/2 → 3/3 Handoff was rejected stale.
- R2 produced a distinct exact retirement current row and immutable evidence.
- A generation-2 ordinary binding revival staled only the 2/2 retirement and the latest projection inside a rollback qualification transaction; generation-1 history remained `VERIFIED`.
- The committed second Handoff used R2 quiescence, advanced only to 3/3, preserved Port/MAC/IP identity, and replaced only the latest Handoff projection.

## Qualification

| Check | Result |
|---|---|
| fresh PostgreSQL 17 migrations 001–062 | PASS |
| repeated retirement 1/1 and 2/2 | PASS |
| ordinary Handoff 1/1 → 2/2 → 3/3 | PASS |
| R1 immutable evidence retained | PASS |
| historical committed replay after generation advance | PASS |
| old Proof cannot authorize new incarnation | PASS |
| exact-incarnation ABA fencing | PASS |
| Port/MAC/IP identity preserved | PASS |
| full PostgreSQL persistence integration | PASS |
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |
| libvirt-tagged backend regression | PASS |
| `make check` | PASS (`476` requirements, `727` test contracts, `239` links) |
| `git diff --check` | PASS |

This is the authority/lifecycle gate requested before PCI/SR-IOV. It does not
claim that a second physical g02→g01/g03 KVM Recovery campaign was executed.
The previously qualified single real g01→g02 campaign remains valid and
unchanged.
