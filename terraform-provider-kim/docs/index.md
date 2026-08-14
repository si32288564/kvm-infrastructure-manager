# KIM Provider

Status: experimental, pre-1.0. Terraform state is a client mapping/cache and is not KIM authority.

## Configuration

```hcl
terraform {
  required_providers {
    kim = {
      source = "kvm-infrastructure-manager/kim"
    }
  }
}

provider "kim" {
  endpoint = "https://kim.example/api/v1"
  client_id = "terraform-production-workspace"
}
```

Set the externally issued short-lived automation credential in `KIM_TOKEN`. Set a stable, non-secret automation identity with `client_id` or `KIM_CLIENT_ID`. `KIM_ENDPOINT` and `KIM_CA_CERTIFICATE` are also supported. Provider attributes `token` and `ca_certificate` are sensitive; no resource state contains them. `insecure_skip_verify` exists only for disposable development endpoints. Per-request timeout defaults to 30 seconds; Image Operation polling interval defaults to one second.

The provider sends `Authorization: Bearer`, decodes KIM Problem Details by stable `code`, and includes the KIM request ID in diagnostics. It does not acquire OIDC tokens or accept Agent/backend credentials.

## Concurrency and retry

Read stores the KIM logical revision derived from the response ETag. Update and Delete send that revision in `If-Match`. `STALE_REVISION` fails visibly; the provider does not fetch a newer revision and overwrite a concurrent client.

Every resource requires a write-only `client_reference`, normally its stable Terraform address. The provider derives `Idempotency-Key` from resource type, provider `client_id`, and `client_reference`; KIM separately binds that key to the canonical desired digest. The same configuration therefore recovers the original KIM logical ID after provider process loss before state persistence. A different client/reference cannot adopt it, and the same reference with different desired intent is rejected by KIM's immutable idempotency authority. Display names are never recovery identity. `client_reference` is not stored in state and is not KIM resource authority.

After an intentional destroy, a later logically new resource incarnation must use a new `client_reference`; retaining the old reference requests replay of the old lifecycle rather than permission to create a duplicate.

## Refresh, drift, and deletion

Refresh reads only the authorized current Northbound projection. Only `RESOURCE_NOT_FOUND` removes a resource from state; forbidden, unauthenticated, stale, unavailable, and internal failures remain diagnostics. Remote desired changes are Terraform drift. Host cache, placement, materialization, Agent attempt, Recovery, and EVACUATE changes are absent from these schemas and therefore are not drift.

Destroy sends KIM Delete with `If-Match`. The provider does not disable delete protection, cascade dependents, or retry stale mutations. Resolve `DELETE_PROTECTED` or `DEPENDENCY_CONFLICT` explicitly.

## Image completion boundary

`kim_image` first commits persistent metadata, then creates a separate ingestion Operation and polls `GET /operations/{id}` until KIM reports `SUCCEEDED`. `UNKNOWN` is non-terminal; `FAILED` and `CANCELLED` fail apply. A content/source revision returns to `PENDING` and is re-ingested. Terraform never interprets Agent attempts, command success, cache paths, or caller-supplied observed digests.

Image polling is bounded by `ingestion_timeout_seconds` (default 7200). The field controls provider waiting only and is not a KIM desired field.

## Imports

All identifiers follow the OpenAPI Resource Contract:

```text
terraform import kim_project.example project/<uuid>
terraform import kim_flavor.example flavor/<uuid>
terraform import kim_availability_policy.example availability-policy/<uuid>
terraform import kim_image.example image/<uuid>
```

Import performs authorized logical Read. It never adopts Host-local/backend objects. Configuration must still contain all required desired fields; a matching import produces a no-op plan.
