# kim_project

Persistent KIM Project with stable logical ID and immutable revisions.

```hcl
resource "kim_project" "example" {
  client_reference  = "kim_project.example"
  name              = "tenant-a"
  delete_protection = true
}
```

`client_reference` is a required write-only client identity used only to recover the exact Create result after Terraform process/state-write loss. It is not KIM resource authority and is never stored in Terraform state.

`name` is required desired state. `delete_protection` is optional and defaults to false. `id`, `revision`, `created_at`, and `updated_at` are computed. Import uses `project/<uuid>`. Destroy does not bypass protection or dependent-resource fencing.
