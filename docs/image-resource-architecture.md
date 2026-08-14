# Image Resource Authority

## Current boundary

Migration 010の`image_revision_evidence`はlogical identity、declared digest、caller-provided observed digest、signature decision、source URIを一つのimmutable registration callで受け取ります。PlacementとVM materializationは`VERIFIED`なexact Image revisionを消費し、Host-local image realizationは別のtyped materialization evidenceです。

このproducerはinternal qualification fixtureには有効ですが、Northbound create authorityではありません。public callerが`observed_checksum`を渡せばbackend observationを自己申告できるためです。

```text
logical Image != immutable content revision
              != ingestion Operation
              != observed artifact verification
              != Host cache/materialization
              != local/backend path
```

## Required closure

Northbound Imageを公開する前に、expected digest/source metadataだけをcommitするlogical revision、closed ingestion Operation、constrained actuator、partial/response-loss read-back、immutable observed digest/signature evidence、exact verification decisionを分離します。same-content replayは同じOperation/evidenceへ収束し、different content、partial ingestion、digest mismatchをterminal failureにします。verified revisionだけがcurrent materialization authorityとなり、content変更はnew revisionで既存VMをretrofitしません。

現在repositoryにはtyped ingestion actuatorとcontroller-consumable read-back producerがありません。したがってMigration 075ではImage schema/APIを追加せず、`NORTHBOUND_IMAGE_RESOURCE`と`IMAGE_INGESTION_OPERATION`をBLOCKEDに維持します。Host cache/path/Host ID/LV UUIDは将来もTerraform desired/import projectionへ含めません。
