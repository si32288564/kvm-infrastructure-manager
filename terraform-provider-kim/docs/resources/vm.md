# kim_vm

Manages a KIM logical VM aggregate. Placement Host, Admission, materialization plan, Port binding, storage backend/LV, Recovery, and EVACUATE incarnations are KIM-owned and absent from Terraform state.

The current qualified create profile supports zero to two STANDARD Ports, one ROOT Volume, at most one DATA Volume, no PCI, and initial `RUNNING`. Flavor/Image/Policy/Scope and Port/Volume set changes replace the resource. `name`, `delete_protection`, and `desired_power_state` are in-place logical mutations.

Destroy first converges the exact current VM to observed `SHUTOFF`, then requests verified delete. The qualified delete matrix matches create: zero through two STANDARD Ports, ROOT and optional DATA. Port deletion preserves logical Port/MAC/IP identity; Volume deletion preserves logical Volumes, capacity allocations and verified materializations while retiring only VM attachments. The maximum profile consumes a complete two-Port absence set and exact ROOT/DATA absence in one terminal.

Import uses:

```text
terraform import kim_vm.example vm/<uuid>
```

Import never adopts a libvirt Domain or another backend object.
