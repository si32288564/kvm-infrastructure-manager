# P1-D03 Debian / systemd Component Upgrade Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-034/035`, `FI-UPG-024/025`
- Invariant: `INV-UPG-027/028`
- Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- Runtime: Ubuntu、systemd 259、standard `dpkg` / `dpkg-query` / `systemctl`、real Target executor transient units

## Qualified Path

```text
verified v2 .deb digest
→ administrator-owned closed backend profile
→ Target Attempt 1 / APPLY_ALLOWED
→ real dpkg database lock contention
→ package remains v1 / v2 install count 0
→ Lease expiry / TARGET_UNKNOWN
→ Target Attempt 2 / READ_BACK_FIRST
→ current package ABSENT
→ dpkg v1 → v2
→ systemd daemon-reload / restart
→ v2 typed health
→ executor SIGKILL before Target Result
→ Lease expiry / TARGET_UNKNOWN
→ Target Attempt 3 / READ_BACK_FIRST
→ installed package version read-back
→ systemd ActiveState / SubState / MainPID read-back
→ /proc/MainPID/exe SHA-256 read-back
→ typed health version / ready / PID / boot ID / process start ticks read-back
→ Target SUCCEEDED
→ Campaign ROLLING / batch-1
```

## Result

| Assertion | Result |
|---|---|
| source package/service | `1.0.0 / active` |
| target package | `2.0.0` |
| target `.deb` SHA-256 | `390303c1e01a6df01733a3e176a59467e575ec8d5412ecf87f9917bf8ef0f654` |
| running v2 binary SHA-256 | `49aa1b6fa8353def85d6018bcf7c42bf03f1b78f8bde665b4008e789715e0cfb` |
| lock-contended Attempt package change | none (`1.0.0` / v2 install count `0`) |
| Target Attempts | 3 |
| `TARGET_UNKNOWN` | 2 |
| successor mode | Attempts 2/3 = `READ_BACK_FIRST` |
| v2 package configure count | 1 |
| accepted Target Result | 1 |
| final Campaign | `ROLLING / batch-1` |

The test detected and corrected an initial fixture hardening defect where `ProtectSystem=strict` made the health path read-only. The qualified unit uses a dedicated `RuntimeDirectory` and matching `ReadWritePaths`; it does not relax the rest of the filesystem sandbox.

## Isolation and Cleanup

- Package/service/unit names contained a unique test nonce.
- Existing KIM、OVN、libvirt、VM、network、storage resources were not modified.
- PostgreSQL access used a temporary SSH reverse tunnel bound to remote loopback.
- Test package was purged after qualification.
- transient executor/lock-holder units、package unit、`/tmp` staging、`/run` health、`/var/lib` install evidence were removed.
- Post-test read-back returned `package not found`、`unit not found`、`cleanup-ok`.

## Command

```bash
make test-p1d03-systemd-package-upgrade
```

## Boundary

This qualification proves the closed Debian/systemd Target backend、real package database lock recovery、and post-install Result-loss semantics with an isolated component. It does not yet certify interrupted unpack/configure recovery、production KIM package names、offline bundle installation、Agent self-upgrade、Campaign rollback、or distribution families other than the tested Ubuntu/Debian profile.
