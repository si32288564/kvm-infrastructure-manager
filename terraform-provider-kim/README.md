# terraform-provider-kim

Experimental Terraform Plugin Framework provider for the KIM Northbound Resource API.

The Phase 1 provider manages only four persistent logical resources: Project, Flavor, closed SYSTEM Availability Policy, and Image. It never connects to PostgreSQL, a Host Agent, libvirt, OVN/OVS, LVM, or another backend. Network, Subnet, Port, Volume, VM, Security Policy, and runtime Recovery/EVACUATE resources are not implemented.

The local source address is `registry.terraform.io/kvm-infrastructure-manager/kim`. Registry publication is out of scope; qualification installs the local binary through a Terraform filesystem mirror.

See [provider usage](docs/index.md), [examples](examples/phase1/main.tf), and the [Phase 1 validation report](../docs/validation/p1-terraform-provider-phase1-20260814.md).
