# kim_vm

Manages a KIM logical VM aggregate. Placement Host, Admission, materialization plan, Port binding, storage backend/LV, Recovery, and EVACUATE incarnations are KIM-owned and absent from Terraform state.

The current qualified create profile supports zero to two STANDARD Ports, one ROOT Volume, at most one DATA Volume, no PCI, and initial `RUNNING`. Flavor/Image/Policy/Scope and Port/Volume set changes replace the resource. `name`, `delete_protection`, and `desired_power_state` are in-place logical mutations.

Destroy first converges the exact current VM to observed `SHUTOFF`, then requests verified delete. Migrations 087/089/090 and their composite qualification permit zero or one STANDARD Port with ROOT and optional DATA. Migration 091 permits two STANDARD Ports with ROOT only. Port delete preserves logical Port/MAC/IP identity; DATA delete preserves both logical Volumes, capacity allocations and verified materializations while retiring only VM attachments. Two-Port-plus-DATA delete remains fail closed until separately qualified.

Import uses:

```text
terraform import kim_vm.example vm/<uuid>
```

Import never adopts a libvirt Domain or another backend object.
