# terraform-provider-kim

Experimental Terraform Plugin Framework provider for the KIM Northbound Resource API.

The provider manages Project, Flavor, closed SYSTEM Availability Policy, Image, Network, Subnet, unattached Port, and backend-neutral Volume. It never connects to PostgreSQL, a Host Agent, libvirt, OVN/OVS, LVM, or another backend. VM, Port attachment, Security Policy, and runtime Recovery/EVACUATE resources are not implemented.

Create recovery is process-safe: a stable provider `client_id` plus each resource's required write-only `client_reference` reconstructs the same KIM Idempotency-Key after Terraform state-write loss. KIM binds that identity to the canonical desired digest and stable resource ID; display-name lookup is never used.

The local source address is `registry.terraform.io/kvm-infrastructure-manager/kim`. Registry publication is out of scope; qualification installs the local binary through a Terraform filesystem mirror.

See [provider usage](docs/index.md), the [Phase 1 example](examples/phase1/main.tf), the [Phase 2 example](examples/phase2/main.tf), the [VM resource](docs/resources/vm.md), and the [Phase 3 VM validation report](../docs/validation/p3-vm-northbound-terraform-resource-20260815.md).
