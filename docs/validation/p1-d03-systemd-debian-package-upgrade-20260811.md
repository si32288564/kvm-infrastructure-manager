# P1-D03 Debian / systemd Component Upgrade Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-034`, `FI-UPG-024`
- Invariant: `INV-UPG-027`
- Host: `kvm-base-g01-n001-p.core.s01.si1230.com`
- Runtime: Ubuntu、systemd 259、standard `dpkg` / `dpkg-query` / `systemctl`、real Target executor transient units

## Qualified Path

```text
verified v2 .deb digest
→ administrator-owned closed backend profile
→ Target Attempt 1 / APPLY_ALLOWED
→ dpkg v1 → v2
→ systemd daemon-reload / restart
→ v2 typed health
→ executor SIGKILL before Target Result
→ Lease expiry / TARGET_UNKNOWN
→ Target Attempt 2 / READ_BACK_FIRST
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
| target `.deb` SHA-256 | `0e0d279a28f9e18242ec10e0feaf21836b68a6b926dc9db3a4ee397f50ddcb1a` |
| running v2 binary SHA-256 | `c4ce033e36df00201195b881b81986cb36ff801be1c418dbad07927da029132f` |
| Target Attempts | 2 |
| `TARGET_UNKNOWN` | 1 |
| successor mode | `READ_BACK_FIRST` |
| v2 package configure count | 1 |
| accepted Target Result | 1 |
| final Campaign | `ROLLING / batch-1` |

The test detected and corrected an initial fixture hardening defect where `ProtectSystem=strict` made the health path read-only. The qualified unit uses a dedicated `RuntimeDirectory` and matching `ReadWritePaths`; it does not relax the rest of the filesystem sandbox.

## Isolation and Cleanup

- Package/service/unit names contained a unique test nonce.
- Existing KIM、OVN、libvirt、VM、network、storage resources were not modified.
- PostgreSQL access used a temporary SSH reverse tunnel bound to remote loopback.
- Test package was purged after qualification.
- transient executor units、package unit、`/tmp` staging、`/run` health、`/var/lib` install evidence were removed.
- Post-test read-back returned `package not found`、`unit not found`、`cleanup-ok`.

## Command

```bash
make test-p1d03-systemd-package-upgrade
```

## Boundary

This qualification proves the closed Debian/systemd Target backend and failure semantics with an isolated component. It does not yet certify production KIM package names、offline bundle installation、Agent self-upgrade、Campaign rollback、or distribution families other than the tested Ubuntu/Debian profile.
