# ADR-0030: Separate Image logical revisions from artifact ingestion Operations

- Status: Accepted
- Date: 2026-08-14

## Context

Migration 010 combined declared and caller-supplied observed checksums in one internal registration call. It is safe as a fixture/catalog compatibility producer but cannot be exposed Northbound: a caller could self-assert backend observation, and partial/response-lost ingestion had no durable Operation or read-back authority.

## Decision

Migration 076 introduces an immutable expected-only logical Image revision and a distinct asynchronous ingestion Operation. Public callers select only an administrator-approved source ID. A closed Agent command resolves source/cache paths from local configuration, performs bounded staging/fsync/whole-artifact SHA-256/atomic publish, and supports read-back. The controller accepts a Command verification ID—not an observed digest—and derives immutable observation, verification, and terminal evidence. Only VERIFIED terminal evidence publishes the exact revision into Migration 010's existing materialization catalog.

Image create is synchronous `201`; ingestion is `202`. Operation UNKNOWN is non-terminal and read-back-first. Cancellation is unsupported (`cancellable=false`) until exact side-effect and staging cleanup semantics are qualified.

## Consequences

- existing Placement/materialization FKs and historical revisions remain unchanged;
- unverified Images cannot become materialization authority;
- content changes require a new revision/artifact generation and never retrofit existing VMs;
- Host cache incarnation is physical/computed and absent from Terraform desired/import;
- internal `RegisterImageRevision` remains compatibility-only and is not reachable through `kim-api`;
- whole-artifact SHA-256 is the correctness profile; chunk/Merkle verification may optimize large artifacts later without weakening identity.
