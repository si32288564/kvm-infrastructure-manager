# P1-A07 Linux CPU/NUMA/Memory/HugePages Validation

## 1. Scope

P1-A07 の最初の collector 適合として、CPU topology、CPU model、CPU isolation、Memory、NUMA、HugePages を次の evidence chain へ接続した。

```text
sysfs / procfs
  → Raw Evidence
  → Linux OS Integration Adapter
  → Normalizer
  → typed Compute / Memory Fragment
  → Capability Mapping
  → canonical Snapshot / Capability Projection
```

raw byte は OS Integration Adapter 境界に留める。normalized fragment は各 field の source path、`AVAILABLE / UNAVAILABLE / UNKNOWN / UNSUPPORTED`、reason code を `EvidenceRef` として保持する。

availability state と field evidence は wire/schema 変更であるため、snapshot schema は `kim.inventory.snapshot/v2` とした。旧 v1 の boolean `available` と暗黙互換にはしない。

## 2. Fixture Validation

`internal/agent/inventory/linuxhost` の contract test で次を確認した。

| Case | Expected result |
|---|---|
| CPU/NUMA/Memory/HugePages の正常 fixture | typed topology と source evidence が canonical `COMPLETE` snapshot へ収束 |
| NUMA interface なし | `kim.host.numa.v1 = UNSUPPORTED` |
| HugePage interface は存在するが page 数 0 | `kim.host.hugepages.v1 = UNAVAILABLE` |
| `isolated` read が permission denied | CPU isolation は `UNKNOWN`、snapshot は `DEGRADED` |
| mandatory CPU topology file 欠損 | CPU topology は `UNKNOWN`、partial topology を `COMPLETE` にしない |
| HugePage pool field 欠損 | HugePages は `UNKNOWN`、既知の 0 と区別 |
| state の zero value | schema validation で拒否 |

## 3. Real Linux Validation

2026-08-09 に Linux container runtime の実 `/proc` と `/sys` を read-only で収集した。

```text
runtime: Docker Engine 29.5.2
validator: statically built linux/arm64 kim-host-inventory-validate
filesystem: container runtime の実 /proc と /sys
network: disabled
root filesystem: read-only
```

Observed summary:

```text
collection_status  COMPLETE
cpu_threads        8
total_memory       8,321,994,752 bytes
hugepage_pools     4
cpu-topology       AVAILABLE
cpu-isolation      AVAILABLE
cpu-model          UNAVAILABLE (cpu_model_not_reported)
memory             AVAILABLE
numa               UNSUPPORTED (interface_not_present)
hugepages          UNAVAILABLE (no_pages_configured)
```

この環境では NUMA interface と configured HugePages が存在しないため、NUMA/HugePages の available capacity を実機認証したものではない。重要な確認結果は、実 Linux source に対して known absence を `0` や `UNKNOWN` に潰さず、CPU/Memory の usable observation と同一 snapshot で安全に表現できたことである。NUMA 複数 node と configured HugePages の hardware qualification は P1-A07 の後続 validation lane に残す。

## 4. Commands

```bash
go test ./internal/agent/inventory/...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /private/tmp/kim-host-inventory-validate ./cmd/kim-host-inventory-validate
docker run --rm --read-only --network none -v /private/tmp/kim-host-inventory-validate:/usr/local/bin/kim-host-inventory-validate:ro debian:bookworm-slim /usr/local/bin/kim-host-inventory-validate
```

## 5. Remaining Qualification

- 2 node 以上の NUMA Host
- configured 2 MiB / 1 GiB HugePage pool と node-local pool
- cgroup/systemd isolation profile 差異
- Ubuntu/Debian 系と RHEL-compatible 系の各 validated combination
- PCI/IOMMU/SR-IOV、OVS/LVM/libvirt module の同一 evidence contract への適合
