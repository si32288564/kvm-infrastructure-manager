# ADR-0025: Planned Local LVM relocation は content identity を boot prerequisite にする

## 状態

Accepted — 2026-08-13

## 背景

Migration 068 の planned source Storage safety は、exact VM/plan/root vda/LV と SHUTOFF、holder absence を結合し、source LV を安全に読めることを証明した。しかし、同じ Storage class と size の destination LV が同じ guest data を持つことは証明しない。define、image realization、または destination RUNNING も source の mutable guest state 保存を証明しない。

## 決定

Migration 070 は Local LVM relocation copy を first-class authority とする。source point は LVM snapshot ではなく、planned quiescence、exact source Storage SAFE、holder absence、および copy 前後の whole-volume digest 一致で固定する。source mutation path が将来増える場合は、この direct frozen-point profileを使用せず snapshot consumerを追加する。

copy Command は `VIRTUAL_MACHINE_ROOT_VOLUME_COPY` / `kim.command.virtual-machine-root-volume-copy/v1` とする。payload は control plane が current Volume/Binding authorityから導出した Host、Volume、Binding generation、VG UUID、LV UUID、exact byte countだけを含む。path、shell、argv、device selectorをcallerから受け取らない。

copy responseはauthorityではない。Lease expiryまたはresponse loss後は同じ Command/Attemptのread-backを先に行う。sourceとdestinationについて同じ exact byte rangeをSHA-256で検証し、sizeとdigestが双方一致するときだけ `CONTENT_IDENTICAL` を導出する。raw guest blocksはDB、result log、validation reportへ保存しない。

incomplete copyを再試行する場合は、同じ destination Binding/LVを再確認し、offset zeroからexact byte rangeを上書きする。silent append、alternate destination、Lease expiryだけを理由とする再初期化は禁止する。別destinationを選ぶ場合は新copy generationと明示的replanを必要とする。

Local LVM relocationを消費する generic VM materializationは、copy terminal `VERIFIED` とcurrent destination Binding/LVの一致を必須とする。child verifierとterminal verifierも同じcopy terminal/current projectionを再検証する。

relocation destinationでは通常のbase-Image realizationを再実行しない。copy済みrootはmutable guest stateを含むため、base Image writeはdata lossになる。copy terminalから導出する `PRESERVED_ROOT` content originをImage/readiness evidenceとして記録し、そのexact evidenceだけをREADYとchild verificationが消費する。

## Verification cost

qualification profileはwhole-volume SHA-256を正本とする。large production volumeではI/O costが高いため、将来は同じcorrectnessを保つchunk/Merkle evidenceまたはstorage-layer verified clone identityを追加できる。ただし sampled digestだけへのdowngradeはしない。

## 非対象

source LV deletion、source capacity reclamation、Recovery proof、PCI、real two-Host mutationはこの決定に含めない。copy成功後もsource LVとcapacity claimを保持し、`GENERIC_LOCAL_LVM_SOURCE_CLEANUP` は別authorityとする。
