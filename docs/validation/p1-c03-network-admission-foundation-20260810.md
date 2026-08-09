# P1-C03 Network Admission Foundation Validation

- 日付: 2026-08-10
- 対象: VLAN Segment、explicit IP/MAC Claim、Port identity、basic Port Binding の transactional Final Admission 統合
- 状態: Foundation implemented / P1-C03 In Progress

## 1. Authority boundary

```text
current Network / Subnet / Segment Claim / Host mapping
  -> side-effect-free dry Eligibility
  -> same-rule transactional Final Admission
       -> immutable Admission Decision
       -> Compute / Memory / HugePages Claim
       -> qualified PCI VF Claim when required
       -> Port identity
       -> IP Claim
       -> MAC Claim
       -> RESERVED Port Binding
```

Final Admission transaction は PostgreSQL authority だけを変更します。OVS、OVN、libvirt、Agent、physical switch、external IPAM への接続や side effect は開始しません。`RESERVED` Binding は desired authority であり、dataplane realization evidence ではありません。

## 2. Implemented contracts

- Network、Subnet、VLAN/VNI Segment Claim、Host Network mapping を generation/state 付き current authority として保持する。
- explicit IP が Subnet CIDR/allocation range 内で、excluded address ではないことを dry/final の両方で確認する。
- active IP は Subnet scope、active MAC は Network scope で PostgreSQL unique index により一意にする。
- `RELEASE_PENDING` / `QUARANTINED` の identity と Segment を再利用可能とみなさない。
- Host mapping generation、supported binding type、Network/Host MTU を Final Admission で再検証する。
- `SRIOV_DIRECT` Port は同じ Placement Request 内の qualified PCI device requirement を必須とし、silent OVS fallback を行わない。
- canonical Network requirements/digest を immutable Admission Decision へ保存し、同じ request identity の semantic drift を conflict にする。
- Port、IP、MAC、Binding、Compute/HugePages/VF Claim のいずれかが失敗した場合は transaction 全体を rollback する。

## 3. Concurrency and fault assertions

1. dry evaluation 前後で Admission / Compute / VF / Port / Identity / Binding row 数が変化しない。
2. dry 後に membership generation が変化すると `ErrPlacementStale` になり、部分 Claim を残さない。
3. Compute capacity が二件分存在しても、同じ qualified VF と Network identity を要求する二つの Admission は一件だけ commit する。
4. distinct Port、PCI requirement なし、同じ IP/MAC を要求する二つの Admission も一件だけ commit する。
5. Network conflict の敗者は Compute / Memory / HugePages / Port / IP / MAC / Binding を一切残さない。
6. stable request replay は元 Admission へ収束し、Host mapping generation 等の Network requirement 変更は conflict になる。

## 4. Validation commands

```bash
KIM_POSTGRES_TEST_URL=postgres://postgres:kimtest@127.0.0.1:55443/kimtest?sslmode=disable \
go test -race ./internal/placement ./internal/persistence/postgres \
  -run 'TestEvaluate|TestDryAndFinalPlacementAdmissionPostgreSQLIntegration|TestPCIQualificationAndFinalAdmissionPostgreSQLIntegration' \
  -count=1

make check
```

## 5. Remaining P1-C03 work

- automatic IP selection、IPv6/profile、external IPAM contract
- identity/segment release verification、quarantine timer、reuse workflow
- Network/Port public API、Quota、Operation/Job/Outbox atomic commit
- typed OVS/OVN/libvirt realization、NB/SB/Host/dataplane observation、`ACTIVE` verification
- PortBindingHandoff、migration/recovery generation fencing
- Gateway/DHCP/NAT/Security Policy domain
