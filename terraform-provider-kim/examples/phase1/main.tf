terraform {
  required_providers {
    kim = {
      source = "kvm-infrastructure-manager/kim"
    }
  }
}

provider "kim" {
  endpoint = "https://kim.example/api/v1"
  client_id = "terraform-production-workspace"
}

resource "kim_project" "example" {
  client_reference = "kim_project.example"
  name = "example"
}

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

resource "kim_availability_policy" "managed" {
  client_reference  = "kim_availability_policy.managed"
  name              = "managed"
  availability_mode = "WORKLOAD_MANAGED"
  max_attempts       = 3
}

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
