# kim_vm

Manages a KIM logical VM aggregate. Placement Host, Admission, materialization plan, Port binding, storage backend/LV, Recovery, and EVACUATE incarnations are KIM-owned and absent from Terraform state.

The current qualified create profile supports zero to two STANDARD Ports, one ROOT Volume, at most one DATA Volume, no PCI, and initial `RUNNING`. Flavor/Image/Policy/Scope and Port/Volume set changes replace the resource. `name`, `delete_protection`, and `desired_power_state` are in-place logical mutations.

Destroy first converges the exact current VM to observed `SHUTOFF`, then requests verified delete. Migrations 087/089 permit delete for zero Port or one STANDARD Port with exactly one ROOT Volume. For the one-Port profile, the logical Port/MAC/IP survives while the exact Host binding is retired from OVN/OVS and returned to `UNATTACHED`. Two-Port and DATA-Volume delete profiles fail closed until separately qualified.

Import uses:

```text
terraform import kim_vm.example vm/<uuid>
```

Import never adopts a libvirt Domain or another backend object.
