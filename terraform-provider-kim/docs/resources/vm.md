# kim_vm

Manages a KIM logical VM aggregate. Placement Host, Admission, materialization plan, Port binding, storage backend/LV, Recovery, and EVACUATE incarnations are KIM-owned and absent from Terraform state.

The current qualified create profile supports zero to two STANDARD Ports, one ROOT Volume, at most one DATA Volume, no PCI, and initial `RUNNING`. Flavor/Image/Policy/Scope and Port/Volume set changes replace the resource. `name`, `delete_protection`, and `desired_power_state` are in-place logical mutations.

Destroy first converges the exact current VM to observed `SHUTOFF`, then requests verified delete. Migration 087 currently permits delete only for zero-Port, one-ROOT VMs. Port-attached and DATA-Volume profiles fail closed until separately qualified.

Import uses:

```text
terraform import kim_vm.example vm/<uuid>
```

Import never adopts a libvirt Domain or another backend object.
