# P1-D03 Debian / systemd Component Upgrade Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-034〜036`, `FI-UPG-024〜026`
- Invariant: `INV-UPG-027〜029`
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

verified v3 .deb digest
→ Target Attempt 1 / APPLY_ALLOWED
→ dpkg unpack / blocking postinst started
→ executor control-group SIGKILL
→ dpkg status = install ok half-configured 3.0.0
→ Lease expiry / TARGET_UNKNOWN
→ Target Attempt 2 / READ_BACK_FIRST
→ typed package status = CONFLICTING
→ immutable CONFLICT_QUARANTINED evidence
→ Target / execution = FENCED
→ Campaign = PAUSED
→ additional claim rejected
```

## Result

| Assertion | Result |
|---|---|
| source package/service | `1.0.0 / active` |
| target package | `2.0.0` |
| target `.deb` SHA-256 | `04b912fa01c43ffeb287191a7296eb6d7053a4fc30819b10caaa5e41341f3c3b` |
| running v2 binary SHA-256 | `f8d62131842e1a2e9c95a5ac218404c2a875484e3f727de250d0596aef41c011` |
| lock-contended Attempt package change | none (`1.0.0` / v2 install count `0`) |
| Target Attempts | 3 |
| `TARGET_UNKNOWN` | 2 |
| successor mode | Attempts 2/3 = `READ_BACK_FIRST` |
| v2 package configure count | 1 |
| accepted Target Result | 1 |
| final Campaign | `ROLLING / batch-1` |
| interrupted target `.deb` SHA-256 | `3831caac49bcad11166115ce813305f6ad2289b353dbcbcb2a988d2db88ec8c0` |
| interrupted dpkg status | `install ok half-configured 3.0.0` |
| interrupted Target Attempts | 2 (`APPLY_ALLOWED`、`READ_BACK_FIRST`) |
| interrupted Target result/configure count | `0 / 0` |
| conflict evidence | `CONFLICTING` observation + `CONFLICT_QUARANTINED` event |
| terminal authority | Target/execution `FENCED`、Campaign `PAUSED` |
| additional claim | rejected |

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

This qualification proves the closed Debian/systemd Target backend、real package database lock recovery、post-install Result-loss semantics、and fail-closed quarantine of interrupted unpack/configure state with an isolated component. It does not authorize automatic `dpkg --configure -a`、reinstall、rollback、or rearm. Production KIM package names、explicit recovery Plan、offline bundle installation、Agent self-upgrade、Campaign rollback、and distribution families other than the tested Ubuntu/Debian profile remain unqualified.
