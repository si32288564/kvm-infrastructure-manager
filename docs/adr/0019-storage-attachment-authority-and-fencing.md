# ADR-0019: Volume Attachment authorityと実世界fencingを分離する

- 状態: Proposed
- 日付: 2026-08-09

## Context

PostgreSQLのAttachment ClaimはKIM内のsingle-writer競合を防げますが、旧Host/QEMU/storage clientが実際にI/Oを停止した証明にはなりません。逆にCeph watcher/lockやlibvirt deviceの観測だけでは、Project ownershipやcurrent generationを決められません。timeoutやHost loss時に一方だけをauthorityにすると、二重writer、data corruption、誤detach/deleteが起こり得ます。

## Decision

- Desired Attachment、PostgreSQL Attachment Claim/Generation、backend/libvirt Observationを分離する。
- `SINGLE_WRITER`のactive ClaimをVolumeごとにDB constraintで一つへ限定し、`READ_ONLY_MANY`は明示capability時だけ許可する。`SHARED_WRITER`はinitial support外とする。
- attach/detachをOperation/Command/Attempt/Observationで実行し、ResultだけでATTACHED/DETACHEDを確定しない。
- detach verification前にClaimをreleaseせず、UNKNOWN状態で反対操作または別Host attachを開始しない。
- compute source fencing、storage client fencing、attachment authority fencingを別の証明として要求する。
- Ceph watcher/lock/blocklist、LVM holder/device stateをevidenceとして扱い、単独でownership authorityにしない。
- Host recoveryとmigrationはAttachment generation/handoffを使用し、old generation/Resultをfenceする。
- Local LVMはsource Host localityへ固定し、certified replication/exportなしに別Host recoveryを許可しない。
- backend-only resource、unknown watcher/lockを自動adopt/deleteせず、explicit Adoption/repair Operationを要求する。
- KIM capacity ledgerとbackend observed/external usageを分離し、Final Admissionでclaimしbackend absence確認前にcapacityを再利用しない。

## Consequences

- Storage Backend/Adapter capability、Attachment Claim/Observation、Fencing Proof、Handoffの実装が必要になります。
- CephとLVMで異なるevidenceを共通のfencing contractへ正規化できます。
- Recovery開始がfencing verification待ちで遅れる場合がありますが、二重writerより安全側へ倒せます。
- initial Ceph feature/client fencing profile、LVM support scope、force operation approvalを別途決定する必要があります。
