# Generic OVN Port Binding Retirement Authority

- Date: 2026-08-12
- Migration: 060
- Result: `GENERIC_OVN_PORT_UNBIND_AUTHORITY = PASS`
- Requirement: `NET-055`
- Invariant: `INV-NET-041`
- Acceptance / fault contract: `AT-NET-047`, `FI-NET-033`

## Authority chain

```text
exact current Port/Binding/source Host
→ immutable typed retirement intent
→ existing PostgreSQL OVN work claim
→ closed UNBIND adapter
→ NB/SB/source OVS read-back
→ immutable retirement evidence
→ VERIFIED current retirement projection
→ source-quiescence / PortBindingHandoff consumer
```

The operation removes only `options:requested-chassis` from the exact KIM-owned Logical Switch Port. It does not delete the Logical Switch, Logical Switch Port, Port authority, IP Claim, or MAC Claim. The adapter accepts no caller-provided OVN/OVS command, table, column, UUID, database path, bridge, or argv.

Positive evidence requires all of the following:

- expected Logical Switch and Logical Switch Port still exist;
- stable Network and Port KIM ownership markers and the source object-set digest match;
- requested chassis is absent;
- the SB Port Binding is not active on the exact source chassis;
- no source Host OVS Interface carries the exact logical `iface-id`.

An unreadable NB, SB, or OVS layer cannot produce positive evidence. Foreign ownership is quarantined before mutation. A stale Port generation, Binding generation, source Host, or ordinary source intent is rejected.

## Ambiguous result and replay

The PostgreSQL 17 qualification injected a mutation response loss even though the same adapter turn could observe an unbound backend. Attempt generation 1 was recorded as `DISPATCH_UNKNOWN`; it was not promoted to `VERIFIED`. Generation 2 was granted only as `READ_BACK_FIRST`, observed the existing unbound state without a second mutation, and produced the terminal evidence.

Stable request replay returned the same operation/work identity. The logical Port row and both active identity Claims remained present. Publishing a later ordinary intent for the same Port/Binding generation fenced the old retirement projection as `STALE`, preventing source-binding ABA from reusing the old Unbound Proof.

## Qualification

| Check | Result |
|---|---|
| typed adapter exact mutation / no LSP delete | PASS |
| response loss and read-back | PASS |
| wrong ownership marker | PASS |
| stale binding generation | PASS |
| fresh PostgreSQL 17 migration 001–060 | PASS |
| full PostgreSQL persistence integration | PASS |
| ordinary Network worker regression | PASS |
| `go test ./...` | PASS |

The separate real g01→g02 non-empty OVN Recovery campaign remains a distinct gate. This document does not claim physical source unbind, destination binding, dataplane convergence, or Recovery Terminal success.
