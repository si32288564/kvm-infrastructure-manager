# ADR-0035: Phase 2 Northbound Logical Resource Contract

- Status: Accepted
- Date: 2026-08-15

## Context

Migrations 077–080 made Network, Subnet, Port, and Volume independent internal authorities. Publishing their current tables directly would leak allocation and backend incarnations, omit authenticated Create replay, and let clients mistake command acceptance for convergence.

## Decision

The four resources share one public lifecycle contract: Project-scoped RBAC, immutable idempotent Create binding, UUID logical identity, revision/ETag mutation fencing, cursor list/import, dependency-protected asynchronous retirement, and a read-only Operation projection. Migration 081 adds only the public Create replay evidence. Migrations 077–080 remain the desired, allocation, realization, materialization, and terminal authorities.

Public desired state never includes segment allocations, assigned MAC/IP, Host, backend, VG/LV UUID, binding, materialization generation, command, attempt, or evidence IDs. Volume backend selection is a KIM compiler decision over the exact current Storage Class, backend generation, and capacity observation. Terraform polls KIM Operations and never invokes a worker or backend.

Update supports only fields whose internal contract permits stable-identity revision changes. Other fields are Terraform replacement fields. Public Attachment remains deferred to the VM aggregate.

## Consequences

- Network/Subnet/Port/Volume public CRUD, import, audit, and Operation polling are available.
- `UNKNOWN` remains nonterminal; `SUCCEEDED` is projected only from the internal verified terminal.
- Recovery, EVACUATE, and backend incarnation changes do not rewrite Terraform desired state.
- Router, Floating IP, Volume resize, Ceph, Port attachment, and VM remain later resources.
