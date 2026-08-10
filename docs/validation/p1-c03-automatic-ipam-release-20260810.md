# P1-C03 Automatic IPAM / Safe Release Validation

- 実施日: 2026-08-10
- 対象: Developer Preview の bounded IPv4 Automatic IPAM、Network identity release/quarantine/reuse
- 状態: PASS

## 1. Scope

今回の増分は、次の authority path を検証します。

```text
read-only dry availability
  -> transactional Final Admission
  -> concrete IP/MAC Claim
  -> release request
  -> immutable absence observations
  -> QUARANTINED
  -> RELEASED
  -> safe identity reuse
```

External IPAM、IPv6 automatic allocation、policy-timed quarantine、OVN/Host cleanup の実 side effect は今回の対象外です。

## 2. Implemented Contract

- `AllocationSource=AUTOMATIC` は caller-selected IP/MAC を受け付けません。
- Dry Eligibility は bounded IPv4 pool に候補が存在することだけを確認し、Decision、Port、Identity Claim、Binding を作りません。
- Final Admission は Subnet/Network scope の advisory lock を取得し、current pool、excluded address、protected Claim を再読込して concrete IP/MAC を選びます。
- Port、IP、MAC、Binding、Compute/PCI/Storage Claim は同じ PostgreSQL transaction で commit または rollback されます。
- `RESERVED | ACTIVE | RELEASE_PENDING | QUARANTINED` の IP/MAC は automatic/explicit allocation の双方から再利用できません。
- Release request は Port、Binding、Identity Claim を `RELEASE_PENDING` に進めるだけで、absence を証明しません。
- `UNKNOWN` または `CONFLICTING` evidence と最初の完全 absence evidence は Claim を `QUARANTINED` に保ちます。
- 二つ目の独立した、より新しい generation の完全 absence evidence 後だけ Claim を `RELEASED` にします。
- 同一 Observation identity/digest の replay は冪等です。異なる digest、同一/旧 generation、新しい post-terminal evidence は current authority を変更しません。
- 全 IP/MAC Claim が `RELEASED` になった場合だけ Port/Binding を `RELEASED` に進めます。

## 3. PostgreSQL Evidence

Migration `027_network_ipam_release_authority.sql` は `network_identity_release_observation_evidence` を追加します。この table は claim/port/binding/observation generation、各 layer の absence、verifier artifact digest、canonical observation digest を保持する immutable evidence です。UPDATE は trigger で拒否されます。

既存の partial unique index により、protected state の同一 IP/MAC Claim は PostgreSQL 自体が拒否します。Automatic allocator の application-level scan は候補選択であり、unique constraint が最後の排他境界です。

## 4. Validation Cases

Fresh PostgreSQL 17 schema に対して次を確認しました。

1. automatic dry evaluation 後も authority table の mutation count が変化しない。
2. Final Admission が excluded address を避け、`allocation_source=AUTOMATIC` の concrete IPv4/MAC Claim を作る。
3. stable request replay が同じ Admission/identity に収束する。
4. release request 前の absence evidence は authority conflict として拒否される。
5. `UNKNOWN` evidence 後に identity が `QUARANTINED` となり、explicit reuse が ineligible になる。
6. 最初の clean absence evidence だけでは release されない。
7. 同一 observation generation の別 evidence は stale として拒否される。
8. 二つ目の clean absence evidence 後だけ IP/MAC、Port、Binding が `RELEASED` になる。
9. terminal observation の完全 replay は同じ `RELEASED` state を返す。
10. `RELEASED` 後の新しい UNKNOWN evidence は stale として拒否され、terminal state を逆戻りさせない。
11. release 後に同じ IP/MAC を explicit Final Admission で再利用できる。

## 5. Commands and Results

```text
KIM_POSTGRES_TEST_URL=... go test -count=1 \
  -run 'TestMigratePostgreSQLIntegration|TestDryAndFinalPlacementAdmissionPostgreSQLIntegration' \
  ./internal/persistence/postgres

PASS
```

```text
go test -race -count=1 ./internal/placement ./internal/persistence/postgres

PASS
```

```text
make check

PASS
documentation contracts valid: 441 requirements, 636 test contracts, 225 links
```

## 6. Remaining Work

- external IPAM Inbox/Claim binding
- IPv6 automatic allocation profile
- versioned reuse-delay / quarantine policy
- typed OVN/Host cleanup operation と absence collector の production wiring
- multi-worker pool pressure / fragmentation metrics
