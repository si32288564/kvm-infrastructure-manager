# ADR-0026: Local LVM relocation transport は相互認証した exact block capability とする

## 状態

Accepted — 2026-08-13

## 背景

Migration 070 は exact source/destination Volume、Binding generation、LV UUID、whole-volume SHA-256 を結合し、同一contentだけを `PRESERVED_ROOT` としてboot可能にした。しかしsource Local LVとdestination Local LVが異なるHostにある場合、単一Agent backendは双方を直接openできない。Control Planeをguest block data pathにすることやremote shellでdeviceを指定することはauthority boundaryを失わせる。

## 決定

Migration 071 はMigration 070の同じ `copy_operation_id/copy_generation` に一つのcross-Host transport sessionを結合する。Control Planeはcurrent planned quiescence、Storage SAFE、SHUTOFF、holder absence、Host authority、Agent credential/session、source/destination Binding/LVをPostgreSQL DB timeで再結合し、exact byte count、chunk profile、expiry、bounded Host concurrencyを含むimmutable authorityを発行する。

Source Agentはsource identityだけを解決するread-only interfaceを持ち、Destination Agentはdestination identityだけを解決するwriter/verifier interfaceを持つ。data pathはTLS 1.3 mutual TLS上のHTTP/2 streamであり、双方のcurrent certificate fingerprintとshared authority digestを検証する。Control Plane/PostgreSQLはguest blocksを中継・保存しない。

各frameはsequence、exact offset、length、SHA-256 chunk digestを持つ。範囲外とout-of-orderを拒否し、同じoffset/bytesのduplicateだけを冪等に受け入れ、同じoffsetの異なるbytesをconflictとする。最初のresume policyは、same copy generationかつsame destination Binding/LVを再確認してoffset zeroから再実行する。alternate destinationはexplicit replan/new generationを必要とする。

Destinationは最後のwrite後にblock flushを行う。stream completion、TLS success、response、Lease/session expiryはcontent authorityではない。response lossまたはexpiry後もside effectを推測せず、両Agentがexact whole rangeを独立にSHA-256 read-backする。Control Planeはcurrent Host/credential/session/Binding/source incarnationをterminal時に再結合し、size、algorithm、copy identity、source/destination digestが一致した場合だけtransport terminalを `VERIFIED` にする。Migration 070 verificationはtransport sessionが存在するprofileでは、そのexact terminalを必須とする。

real Host adapterはadministrator-owned `VG UUID -> VG name` mapとKIM Volume IDからLV nameを導出し、fixed `lvs` queryが返すexact VG/LV UUIDと `/dev/mapper` pathだけをopenする。caller path、selector、executable、flag、shell、argvを受け取らない。

## Failure semantics

partial/disconnect/Lease expiryは `PARTIAL/UNKNOWN` progressでありterminal authorityではない。retryはsame destinationをread-back firstで扱う。credential revoke、Agent session fence、Host authority drift、Binding/LV drift、source VM incarnation driftはterminalをrejectする。completed terminal replayは全identifier/digestが同じ場合だけ冪等であり、異なるreplayはconflictになる。

## 非対象

Migration 071だけではproduction HostへのAgent deployment、disposable VM/VG authorization、source LV deletion、source capacity reclamation、PCI、またはreal Host EVACUATEを証明しない。physical source cleanupは引き続き別authorityである。
