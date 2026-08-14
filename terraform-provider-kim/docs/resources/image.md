# kim_image

Persistent Image intent plus a separately authoritative ingestion Operation.

```hcl
resource "kim_image" "base" {
  client_reference = "kim_image.base"
  project_id      = kim_project.example.id
  name            = "base.raw"
  architecture    = "X86_64"
  format          = "RAW"
  expected_digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  source_id       = "approved.registry.object"
  visibility      = "PRIVATE"
}
```

`client_reference` is the required write-only crash-recovery identity; it is not Image desired state.

Architecture is `X86_64` or `AARCH64`; format is `RAW` or `QCOW2`; visibility is `PRIVATE`, `SHARED`, or `PUBLIC`. Apply completes only after KIM's Operation reaches verified `SUCCEEDED`. `verified_digest`, `verified_size_bytes`, and `verification_state` are computed from KIM evidence. Host cache, local filename/path, attempt, observation, verification, and Operation IDs are intentionally absent from state.

Changing name/lifecycle/visibility creates a logical metadata revision. Changing architecture, format, expected digest, or source creates a content revision, clears the current verified projection, and requires another ingestion terminal. Existing VM consumers are not retrofitted. Changing `project_id` replaces the resource. Import uses `image/<uuid>`.
