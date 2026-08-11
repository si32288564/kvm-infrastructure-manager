# P1-B06 External Assertion Verifier Authority Validation

- Date: 2026-08-11
- Scope: purpose-limited issuer trust, closed assertion verification, and complete Membership Set materialization
- Status: PASS for the Phase 1 Ed25519 + SYSTEM scope profile; P1-B06 remains In Progress

## Authority boundary

The implemented chain is:

    external complete-set claim
      -> current purpose-limited issuer trust
      -> immutable verification/member/conflict evidence
      -> explicit materialization request
      -> current issuer/Group/Cardinality/Hierarchy/expected Set revalidation
      -> existing atomic complete Membership Set publisher
      -> current HostGroup membership authority

External receipt, a valid signature, trusted issuer state, and a VERIFIED decision do not modify current membership. Only an accepted complete Membership Set generation does so. The verifier and materializer are Control Plane persistence operations; no Agent assertion authority was added.

## Closed assertion and issuer profile

The only accepted assertion profile is:

- schema: kim.host-group.external-assertion/v1;
- subject: HOST_GROUP_COMPLETE_MEMBERSHIP;
- audience: kim-control-plane;
- algorithm: Ed25519;
- semantic payload: assertion/issuer identity, exact HostGroup generation, sorted complete Host IDs, issued/expiry time, audience, and nonce;
- scope: SYSTEM/system issuer authority with an explicit allow-list of exact HostGroup generations.

Issuer revision evidence stores only the public verification key and its digest. Private key custody remains outside KIM. The issuer authority is dedicated to HostGroup membership assertions and is not a general PKI, Credential Binding, Project, Tenant, or federation authority.

## Immutable evidence and results

Migration 047 adds immutable issuer revision/scope evidence, issuer current state, assertion/member evidence, issuer-nonce evidence, replay-conflict evidence, and exact assertion provenance on new EXTERNAL_ASSERTION Membership Sets. Pre-047 Sets retain NULL assertion provenance as immutable compatibility history.

The persisted result vocabulary is:

- VERIFIED;
- INVALID_SIGNATURE;
- UNTRUSTED_ISSUER;
- EXPIRED;
- REPLAY_CONFLICT;
- UNSUPPORTED_SCHEMA;
- AUDIENCE_MISMATCH;
- PAYLOAD_DIGEST_MISMATCH;
- STALE_HOST_GROUP;
- UNKNOWN_HOST;
- UNKNOWN.

Assertion evidence fixes issuer generation, schema/subject, exact HostGroup generation, times, audience, nonce, declared and canonical payload digests, signature digest, canonical member-set digest, verifier version/digest, hierarchy generation when present, and the verification digest.

## Replay, expiry, and distrust

Exact assertion ID + nonce + semantic payload + signature replay returns the original evidence. Reusing an assertion ID or issuer nonce with different semantics records REPLAY_CONFLICT and never applies last-write-wins.

Expiry means that an assertion cannot authorize a new materialization. Issuer rotation or RETIRED/REVOKED current state similarly blocks new materialization. Neither event rewrites immutable assertion evidence or an already accepted historical/current Membership Set. A materialization response-loss replay with the same publish request returns the committed Set even after later issuer distrust; it does not create or re-authorize a Set.

## Shared Membership Set authority

External materialization calls the same complete-set transaction used by EXPLICIT and SELECTOR sources. It verifies exact assertion members and provenance, then applies the current:

- HostGroup generation and ACTIVE lifecycle;
- cardinality policy and sibling-set serialization;
- hierarchy ID/generation when that class has hierarchy authority;
- expected current Membership Set generation;
- known Host identities and complete canonical member-set digest.

EXPLICIT, SELECTOR, and EXTERNAL_ASSERTION remain distinct provenance paths. Source switching requires a new complete Set generation. A competing SELECTOR/EXTERNAL_ASSERTION publish at the same expected generation permits one commit only; no mixed-source member rows are published.

## PostgreSQL 17 qualification

The integration suite covered:

- valid A/B complete-set verification followed by explicit materialization;
- proof that VERIFIED evidence alone creates no current Set;
- exact verification replay and materialization response-loss replay without generation amplification;
- invalid signature, unknown issuer, expiry, audience mismatch, unsupported schema, unknown Host, assertion-ID conflict, and nonce conflict;
- parallel identical verifiers converging to one evidence row;
- issuer key rotation, old-key rejection, current-key verification, issuer revocation, and a verifier-versus-rotation transaction race producing complete generation 1 or generation 2 semantics only;
- revocation blocking new materialization while preserving accepted Set evidence;
- EXTERNAL_ASSERTION -> EXPLICIT -> EXTERNAL_ASSERTION source switching with historical evidence retained;
- hierarchy drift fencing and re-verification against the new hierarchy;
- exclusive sibling cardinality conflict preserving the old accepted state;
- HostGroup generation drift producing STALE_HOST_GROUP;
- SELECTOR versus EXTERNAL_ASSERTION concurrent publication with one generation-one winner.

Commands:

    KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestMigratePostgreSQLIntegration ./internal/persistence/postgres
    KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run TestHostGroupExternalAssertion ./internal/persistence/postgres
    KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 ./internal/persistence/postgres
    KIM_POSTGRES_TEST_URL=postgres://... go test -race -count=1 ./internal/persistence/postgres
    make check

The PostgreSQL 17 container used for qualification is temporary and is removed after all suites complete.

## Explicit limits and remaining P1-B06 gates

- Site/managed-Host population authority and population-complete EXACTLY_ONE;
- AvailabilityPolicy persistence/consumers;
- failure-domain Maintenance scheduling and minimum-ready;
- first-class Project/Tenant generation authority;
- broad federation/JWS negotiation, arbitrary algorithms, delta/event-stream membership, and external policy languages;
- public topology/API/UI and Agent-side HostGroup logic.
