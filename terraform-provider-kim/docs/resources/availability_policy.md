# kim_availability_policy

Closed SYSTEM Availability Policy resource.

```hcl
resource "kim_availability_policy" "managed" {
  client_reference  = "kim_availability_policy.managed"
  name              = "managed"
  availability_mode = "WORKLOAD_MANAGED"
  max_attempts       = 3
}
```

`client_reference` is the required write-only crash-recovery identity; it is not policy authority.

`availability_mode` accepts only `MANUAL` and `WORKLOAD_MANAGED`; `max_attempts` is 1 through 100. Runtime Failure Epoch, fencing, Recovery, EVACUATE, destination, and budget state are not Terraform fields. Updates create a new policy revision without retrofitting existing exact workload bindings. Import uses `availability-policy/<uuid>`.
