resource "kim_network" "tenant" {
  client_reference = "tenant-network"
  project_id       = var.project_id
  name             = "tenant-overlay"
  profile          = "STANDARD_OVERLAY"
  mtu              = 1450
  segment_policy   = "AUTO"
}

resource "kim_subnet" "tenant" {
  client_reference  = "tenant-subnet"
  project_id        = var.project_id
  network_id        = kim_network.tenant.id
  name              = "tenant-v4"
  ip_family         = "IPV4"
  cidr              = "10.42.0.0/24"
  gateway_policy    = "AUTO"
  gateway_address   = "10.42.0.1"
  allocation_policy = "RANGE"
  allocation_start  = "10.42.0.10"
  allocation_end    = "10.42.0.200"
  dhcp_enabled      = true
}

resource "kim_port" "unattached" {
  client_reference   = "tenant-port"
  project_id         = var.project_id
  network_id         = kim_network.tenant.id
  subnet_id          = kim_subnet.tenant.id
  name               = "unattached"
  mac_policy         = "AUTO"
  ip_allocation_mode = "AUTO"
  attachment_policy  = "ON_DEMAND"
  datapath_profile    = "STANDARD"
}

resource "kim_volume" "root" {
  client_reference       = "tenant-root"
  project_id             = var.project_id
  name                   = "blank-root"
  size_bytes             = 17179869184
  storage_class_id       = var.storage_class_id
  storage_class_revision = 1
  bootable               = true
  source_type            = "BLANK"
}

variable "project_id" { type = string }
variable "storage_class_id" { type = string }
