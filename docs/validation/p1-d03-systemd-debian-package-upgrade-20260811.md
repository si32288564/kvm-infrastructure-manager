# P1-D03 Debian / systemd Component Upgrade Validation

- Date: 2026-08-11
- Test Contract: `AT-UPG-034〜037`, `FI-UPG-024〜027`
- Invariant: `INV-UPG-027〜030`
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
→ immutable CONFIGURE_EXISTING Recovery Plan / authorization
→ Recovery Attempt 1 / READ_BACK_FIRST
→ PACKAGE_HALF_CONFIGURED read-back
→ closed dpkg --configure <fixed-package>
→ package / service / process / health MATCHED
→ Recovery VERIFIED
→ Target / execution remain FENCED / Campaign remains PAUSED
→ separate immutable rearm authorization
→ Target / execution = PENDING
→ Campaign remains PAUSED / normal claim rejected
```

## Result

| Assertion | Result |
|---|---|
| source package/service | `1.0.0 / active` |
| target package | `2.0.0` |
| target `.deb` SHA-256 | `b9852e24c276c9bac245cd1889aaae6fd0f7b33aa27268b1f519bb7df856f88c` |
| running v2 binary SHA-256 | `bb8e9251f4c5518f428d5e33b7fb838c12dc7d8192650370020db04ba5893306` |
| lock-contended Attempt package change | none (`1.0.0` / v2 install count `0`) |
| Target Attempts | 3 |
| `TARGET_UNKNOWN` | 2 |
| successor mode | Attempts 2/3 = `READ_BACK_FIRST` |
| v2 package configure count | 1 |
| accepted Target Result | 1 |
| final Campaign | `ROLLING / batch-1` |
| interrupted target `.deb` SHA-256 | `5529d038dc0ba06fea4125b744b904d4dca0856854d3560679a077f18695e68b` |
| interrupted dpkg status | `install ok half-configured 3.0.0` |
| interrupted Target Attempts | 2 (`APPLY_ALLOWED`、`READ_BACK_FIRST`) |
| interrupted Target result/configure count | `0 / 0` |
| conflict evidence | `CONFLICTING` observation + `CONFLICT_QUARANTINED` event |
| terminal authority | Target/execution `FENCED`、Campaign `PAUSED` |
| additional claim | rejected |
| Recovery strategy | `CONFIGURE_EXISTING` |
| Recovery Attempts / configure count | `1 / 1` |
| Recovery verification | `VERIFIED`; package/service/process/health `MATCHED` |
| implicit rearm before authorization | none; Target/execution remain `FENCED` |
| explicit rearm | immutable authorization evidence; Target/execution `PENDING` |
| implicit Campaign resume | none; Campaign remains `PAUSED` |
| response-loss replay | Plan / Recovery Result / rearm return the original evidence; no duplicate row |

The test detected and corrected an initial fixture hardening defect where `ProtectSystem=strict` made the health path read-only. The qualified unit uses a dedicated `RuntimeDirectory` and matching `ReadWritePaths`; it does not relax the rest of the filesystem sandbox.

## Isolation and Cleanup

- Package/service/unit names contained a unique test nonce.
- Existing KIM、OVN、libvirt、VM、network、storage resources were not modified.
- PostgreSQL access used a temporary SSH reverse tunnel bound to remote loopback.
- Test package was purged after qualification.
- transient Target/Recovery executor and lock-holder units、package unit、`/tmp` staging、`/run` health、`/var/lib` install evidence were removed.
- Post-test read-back returned `package not found`、`unit not found`、`cleanup-ok`.

## Command

```bash
make test-p1d03-systemd-package-upgrade
```

## Boundary

This qualification proves the closed Debian/systemd Target backend、real package database lock recovery、post-install Result-loss semantics、fail-closed quarantine of interrupted unpack/configure state、and an explicit `CONFIGURE_EXISTING` Recovery Plan / Attempt / Verification / rearm path with an isolated component. It does not authorize `dpkg --configure -a`、caller-supplied package/argv、reinstall、downgrade、rollback、or implicit Campaign resume. Production KIM package names、offline bundle installation、Agent self-upgrade、Campaign rollback、and distribution families other than the tested Ubuntu/Debian profile remain unqualified.
