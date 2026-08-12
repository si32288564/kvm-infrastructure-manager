# Real Two-Host KVM Recovery with non-empty OVN Port

Date: 2026-08-12

## Result

```text
REAL_TWO_HOST_KVM_RECOVERY_AUTHORITY           = PASS (existing zero-Port campaign)
PORT_BINDING_HANDOFF_AUTHORITY_FOUNDATION      = PASS
REAL_OVN_PORT_HANDOFF                          = BLOCKED
REAL_DESTINATION_OVN_BINDING                   = BLOCKED
REAL_OVS_DATAPLANE                             = BLOCKED
REAL_NONEMPTY_NETWORK_RECOVERY_VERIFICATION    = BLOCKED
REAL_TWO_HOST_KVM_OVN_RECOVERY_AUTHORITY       = BLOCKED
```

`BLOCKED` は backend failure や qualification PASS ではありません。指定された production Hostへ危険な代替 mutationを行わず、既存 authority boundary の不足を fail closed で検出した結果です。

## Implemented foundation

Migration 059 は、以前 ADR/Requirement にだけ存在していた generic Network authority を persistenceへ追加しました。

```text
immutable source Port quiescence evidence
→ immutable PortBindingHandoff
→ rebuildable current Handoff projection
→ Final Admissionでlogical Port/MAC/IPを保持
→ Port/Binding generationだけをatomic advance
```

また既存 closed typed OVS backendを次の範囲で強化しました。

- libvirt OVS virtualportへexact logical `interfaceid`を設定
- active OVS Interfaceの`external_ids:iface-id`をstandard `ovs-vsctl` read-backで検証
- FENCED Hostのsource quiescenceを既存read-only Command Lease、Result、Observation、Verificationへ接続
- Recovery pre-power readinessをpower authorityと分離
- non-empty Network evidence setをPort/IP/MAC/Handoff/NB/SB/OVS exact generations/digestsから構成
- Dangerous-step、Recovery Verification、Terminalでcurrent evidence setを再検証

EligibilityはNetwork mutationを行いません。Handoffはdestination Final Admission transactionだけがcommitします。historical Admission、identity Claim、source evidenceはrewriteしていません。

## Exact real-environment blocker

read-only preflightで両Hostのproduction `br-int`、OVS system-id、local OVN endpointを確認しました。

| Host | OVS system-id | `ovn-remote` | `ovn-encap-ip` |
|---|---|---|---|
| g01 | `dba0d9ce-859d-4422-a486-17161ebc1b31` | `unix:/var/run/ovn/ovnsb_db.sock` | `127.0.0.1` |
| g02 | `aefea4f0-27ed-4a2a-ae77-8e9ce09303ff` | `unix:/var/run/ovn/ovnsb_db.sock` | `127.0.0.1` |

現在の generic OVN runtime workerはPort intentの`ObservePort/ReconcilePort`だけを実装し、source bindingをtypedにunbound/retiredへ進めるoperation authorityを持ちません。production profileはHostごとのlocal SB endpointで、同一shared SB上のrequested-chassis generation transitionとしても実行できません。

したがってsource LSP/SB bindingを残したままdestinationをpositive convergenceへ進めると、次の禁止状態になります。

```text
source SB binding still current
+ destination SB binding current
= dual-active/conflicting Network binding
```

qualification harnessから直接`ssh ovn-nbctl/ovs-vsctl`でsource objectを削除・unbindする方法は、指示されたauthority bypassです。また今回scope外のgeneric source cleanup authorityをRecovery専用shortcutとして追加することも禁止されています。このためreal mutation開始前に停止しました。

## Non-destructive evidence

今回のHost accessはread-only preflightだけです。VM、VG/LV、OVN object、OVS Port/Interface、route、NAT、production Networkを作成・変更・削除していません。cleanup対象のremote artifactはありません。

## Qualification executed

- `go test ./...`: PASS
- `go test -race ./...`: PASS
- fresh PostgreSQL 17 migration 001–059: PASS
- full persistence integration on fresh PostgreSQL 17: PASS
- OVS typed backend exact iface-id/source-quiescence unit qualification: PASS
- g01 Linux/libvirt-tag helper build and targeted OVS/recoveryauthority tests: PASS
- `make check` / documentation lint / `git diff --check`: PASS
- real g01/g02 read-only OVS/OVN preflight: PASS
- real non-empty Network Recovery mutation: not started

## Unblock condition

Recoveryとは独立した generic Network work packageとして、次を先に実装・qualificationする必要があります。

```text
typed OVN Port unbind/retirement intent
→ PostgreSQL work claim/Lease/UNKNOWN/read-back
→ stable ownership marker validation
→ NB/SB source absence or unbound evidence
→ source Handoff quiescence acceptance
```

そのauthorityを通常Network consumerとして成立させた後、このcampaignはsame PostgreSQL history、same Port/MAC/IP、g01→g02 binding generationで再実行できます。
