# P1 Local LVM planned relocation data-preservation qualification — 2026-08-13

## Outcome

Migration 070 closes the shape-only gap in planned Host EVACUATE. A synthetic two-Host, one-VM, zero-Port, one-Local-LVM-root campaign copied guest-like mutated content through a closed typed backend, lost the copy response, recovered by Lease-expiry read-back, derived equal whole-volume SHA-256 evidence, and allowed the exact destination Binding/LV to be consumed by the ordinary materialization and power chain.

The selected frozen-source profile is direct exact-LV copy while the VM is SHUTOFF, the exact source root vda/LV has no QEMU holder, Storage SAFE is current, and the source digest is unchanged before and after copy. No snapshot path is used in this profile.

## Authority chain

`VM RUNNING on A → Host drain → typed SHUTOFF → read-back → Planned Quiescence → source Storage SAFE → destination Final Admission/Volume Binding → VIRTUAL_MACHINE_ROOT_VOLUME_COPY → response LOST → Lease expiry → read-back → source/destination SHA-256 → copy Verification VERIFIED → copy Terminal VERIFIED → relocation authority → generic PrepareVMMaterialization → definition → PRESERVED_ROOT readiness (no base-Image overwrite) → READY → typed RUNNING → Child Verification VERIFIED → Child Terminal VERIFIED → Parent Terminal VERIFIED → Host DRAINED`

The relocation authority, generic materialization consumer, child verifier, and child terminal verifier each rejoin the exact copy terminal and current destination Binding generation/LV UUID. A size-only destination never authorizes boot.

## Recorded PostgreSQL 17 campaign identities

| Identity | Value |
|---|---|
| VM UUID | `68000000-0000-4000-8000-016778590000` |
| source Host | `evacuation-positive-source-1786613016778590000` |
| destination Host | `evacuation-positive-destination-1786613016778590000` |
| source Admission | `admission:evacuation-source-1786613016778590000` |
| source Plan / materialization | `evacuation-source-plan-1786613016778590000` / `1` |
| source Volume / Binding generation / LV UUID | `evacuation-source-root-1786613016778590000` / `1` / `lv-evacuation-source-1786613016778590000` |
| planned quiescence / Storage SAFE | `evacuation-quiescence-1786613016778590000` / `evacuation-storage-safety-1786613016778590000` |
| copy-source identity | exact source Volume/Binding/LV above; direct frozen point, no snapshot |
| destination Admission | `admission:evacuation-destination-1786613016778590000` |
| destination Plan / materialization | `evacuation-destination-plan-1786613016778590000` / `2` |
| destination Volume / Binding generation / LV UUID | `evacuation-destination-1786613016778590000:root` / `1` / `lv-evacuation-destination-1786613016778590000` |
| destination content origin | `PRESERVED_ROOT`; normal base-Image realization rejected |
| copy Operation / Command | `local-lvm-copy-positive-1786613016778590000` / `local-lvm-copy-command-positive-1786613016778590000` |
| copy Attempt / response | `1` / `LOST` |
| copy Verification / Terminal | `local-lvm-copy-verification-positive-1786613016778590000` / `local-lvm-copy-terminal-positive-1786613016778590000` |
| digest algorithm | `SHA-256`, exact whole-volume byte range |
| source digest | `2b5d99add845e30f303051e78c2c9895bb869e7bc7763699361252f2049a5dcd` |
| destination digest | `2b5d99add845e30f303051e78c2c9895bb869e7bc7763699361252f2049a5dcd` |
| child Verification | `evacuation-child-verification-1786613016778590000` |
| child Terminal | `evacuation-child-terminal-1786613016778590000` |
| parent Terminal | `evacuation-parent-terminal-1786613016778590000` |

The fixture content is a base region plus a unique guest mutation marker and a second marker near the end. The typed backend test copies the actual byte buffer and checks both markers by byte equality; PostgreSQL persists only digest, size, and identity.

## Mandatory negative and drift coverage

- VM still RUNNING or source holder open: copy preparation/backend rejects.
- wrong source LV, source Binding, destination LV, or destination Binding: exact identity lookup/rejoin rejects.
- destination smaller, partial copy at full size, or one-block destination corruption: whole-range size/digest verification rejects.
- source mutation during copy: pre/post source digest drift rejects.
- copy absent: shape-only relocation authorization rejects.
- normal base-Image realization after verified copy: rejected; it cannot overwrite the preserved root.
- wrong source Storage SAFE and stale prior-incarnation safety: rejects.
- response LOST and Lease expiry: same exact command read-back verifies without blind alternate destination or destructive initialization.
- identical copy replay: returns the same immutable verification/terminal.
- old relocation/copy lineage in repeated A→B→C and Recovery A→B → EVACUATE B→C: cannot authorize the new incarnation.
- destination Binding LV drift or copy-current projection drift after child verification: child terminal rejects.
- immutable copy operation, Attempt, content observation, verification, and terminal tables reject UPDATE.

## Metrics

The typed backend maintains low-cardinality counters corresponding to:

| Required metric | Backend counter |
|---|---|
| `local_lvm_copy_active` | `RelocationMetrics.Active` |
| `local_lvm_copy_bytes` | `RelocationMetrics.Bytes` |
| `local_lvm_copy_attempts` | `RelocationMetrics.Attempts` |
| `local_lvm_copy_unknown` | `RelocationMetrics.Unknown` |
| `local_lvm_copy_verification_failures` | `RelocationMetrics.VerificationFailures` |
| `local_lvm_copy_duration` | `RelocationMetrics.DurationNanoseconds` |

Volume, Binding, and LV identities are evidence fields, not metric labels.

## Gate matrix

| Gate | Result |
|---|---|
| `PLANNED_LOCAL_LVM_COPY_AUTHORITY` | PASS |
| `PLANNED_LOCAL_LVM_COPY_VERIFICATION` | PASS |
| `PLANNED_LOCAL_LVM_CONTENT_IDENTITY` | PASS |
| `PLANNED_LOCAL_LVM_DESTINATION_BINDING` | PASS |
| `EVACUATE_LOCAL_LVM_RESPONSE_LOSS` | PASS |
| `EVACUATE_LOCAL_LVM_REPLAY_IDEMPOTENCY` | PASS |
| `EVACUATE_LOCAL_LVM_ABA_FENCING` | PASS |
| `EVACUATE_LOCAL_LVM` | PASS — synthetic direct frozen-point whole-digest profile |
| `EVACUATE_ZERO_PORT` | PASS |
| `EVACUATE_OVN_PORT` | PASS |
| `EVACUATION_REPEATED_INCARNATION` | PASS |
| `EVACUATION_MIXED_RECOVERY_ORIGIN` | PASS |
| `GENERIC_LOCAL_LVM_SOURCE_CLEANUP` | BLOCKED |
| `EVACUATE_PCI_SRIOV` | BLOCKED |
| `REAL_TWO_HOST_KVM_HOST_EVACUATION` | BLOCKED |
| `REAL_TWO_HOST_LOCAL_LVM_DATA_PRESERVATION` | BLOCKED — no disposable real workload was authorized |

## Capacity and cleanup

Parent VERIFIED does not invoke cleanup. The old source LV remains `BOUND`, its Storage capacity claim is not `RELEASED`, cleanup operation count remains zero, and source capacity is not advertised FREE. Data preservation is not source deletion or capacity reclamation.

## Safety assertions

| Assertion | Result |
|---|---|
| caller-supplied content-identical authority | none |
| copy success inferred from exit code | no |
| destination boot before copy verify | denied |
| source/destination arbitrary path | rejected by closed schema |
| arbitrary shell/argv | none; unknown fields rejected |
| source LV deleted by relocation | no |
| source capacity reclaimed before cleanup | no |
| Recovery proof reused as planned copy proof | no |
| production workload mutated | none |
| historical evidence rewritten | none |

## Verification commands

Qualification uses fresh `postgres:17-alpine` and DB statement time for Lease/Attempt authority. The final run records:

```text
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -run 'TestMigratePostgreSQLIntegration|TestHostEvacuation|TestMixedRecovery' -v ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -count=1 -timeout 600s ./internal/persistence/postgres
KIM_POSTGRES_TEST_URL=postgres://... go test -race -count=1 -timeout 900s ./internal/persistence/postgres
go test ./...
go test -race ./...
make check
git diff --check
```

Real Host mutation, source deletion, and PCI qualification were not run.
