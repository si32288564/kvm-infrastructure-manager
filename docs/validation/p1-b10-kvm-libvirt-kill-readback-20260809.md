# P1-B10 KVM/libvirt Kill and Read-back Validation

日付: 2026-08-09

## 1. Scope

標準 libvirt APIを使用するclosed typed VM power-state backendを追加し、実KVM Host `kvm-base-g01-n001-p.core.s01.si1230.com` でAgent subprocess kill/read-backをqualificationした。

対象Host:

- Ubuntu kernel `7.0.0-28-generic`、x86_64
- `/dev/kvm` available
- libvirt `12.0.0`
- `qemu:///system`
- QEMU `/usr/bin/qemu-system-x86_64`

既存VMは変更しない。各runで一意な `kim-qualification-<id>` Domainを64 MiB、1 vCPU、disklessとしてdefineし、test cleanupでdestroy/undefineする。

## 2. Typed Backend Boundary

```text
Command type: VIRTUAL_MACHINE_POWER_STATE_ENSURE
Schema: kim.command.virtual-machine-power-state/v1
Target: vm:<lowercase UUID>
Desired state: RUNNING | SHUTOFF
Interface: standard libvirt API
```

backendはraw XML、arbitrary libvirt method/flags、socket path、shell、argvをCommand payloadとして受理しない。libvirt URIはAgent administrator configurationであり、Tenant/Command入力ではない。libvirt supportはexplicit `libvirt` build tagとminimal cgo bindingに限定する。

## 3. Fault Sequence

```text
KIM qualification Domain = SHUTOFF
  ↓
typed Command + write-before-execute journal
  ↓
standard libvirt Domain start
  ↓
libvirt read-back = RUNNING
  ↓
Result constructed; transport publisher blocked
  ↓
Agent subprocess SIGKILL
  ↓
KVM/QEMU Domain remains RUNNING
  ↓
journal reopen + new libvirt connection
  ↓
typed Verification for same Command/Attempt/target/digest
  ↓
Domain UUID/state read-back = MATCHED
```

Control PlaneがResultを受け取らないwindowはnon-execution proofではない。既存Execution contractではLease expiry/process lossによりAttemptを`UNKNOWN`として保持し、本fixtureが生成するjournalとlibvirt Observationを同じAttemptのVerification evidenceとして使用する。

## 4. Results

| Case | Result |
|---|---|
| standard libvirt test driver power-state round-trip | PASS |
| libvirt-enabled Host Agent build on Linux/amd64 | PASS |
| KIM専用 persistent KVM Domain define | PASS |
| typed RUNNING mutation through standard libvirt | PASS |
| write-before-execute journal後・Result transport前のsubprocess kill | PASS |
| Agent process kill後もQEMU/KVM Domain running | PASS |
| journal reopenとnew libvirt connection | PASS |
| same Attempt/UUID/payload digest read-back | `MATCHED`: PASS |
| arbitrary XML/path/method相当payload rejection | PASS |
| qualification Domain cleanup | PASS、残存Domainなし |

実行contract:

```text
KIM_LIBVIRT_SYSTEM_URI=qemu:///system \
go test -count=1 -timeout 120s -tags libvirt \
  -run 'TestKVMProcessKillUnknownReadBack|TestStandardLibvirtTestDriverPowerStateRoundTrip' \
  -v ./internal/agent/execution/libvirtdomain
```

## 5. Remaining Qualification

- PostgreSQL Attempt `UNKNOWN` transition、Gateway session regeneration、typed Verification delivery、Job convergenceを同じremote KVM campaignへ統合
- graceful shutdownのasynchronous state、libvirt daemon restart、connection recovery
- VM create/delete、machine type、disk/network bindingを含むP1-C lifecycle

本増分はVM power-state backendと実KVM side effectのcrash-safe evidence chainをqualificationする。VM create/delete lifecycleまたは全Control Plane process campaignの完了を意味しない。
