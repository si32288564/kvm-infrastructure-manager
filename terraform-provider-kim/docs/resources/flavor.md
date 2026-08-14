# kim_flavor

Project-owned logical compute shape. Updating desired shape creates a new KIM revision while retaining the Terraform/KIM logical ID; existing VMs keep their exact historical revision.

```hcl
resource "kim_flavor" "small" {
  client_reference = "kim_flavor.small"
  project_id     = kim_project.example.id
  name           = "small"
  vcpus          = 2
  memory_mib     = 2048
  root_disk_gib  = 20
  numa_policy    = "NONE"
  cpu_allocation = "SHARED"
  cpu_pinning    = false
}
```

`client_reference` is the required write-only crash-recovery identity; it is not desired Flavor state.

`numa_policy` is `NONE` or `REQUIRED`; `cpu_allocation` is `SHARED` or `DEDICATED`. Optional `numa_nodes` and `huge_page_size_kib` are logical requirements, not physical allocations. No Host, pCPU, NUMA realization, HugePage allocation, PMD, or backend identity is state. Changing `project_id` replaces the resource. Import uses `flavor/<uuid>`.
