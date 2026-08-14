# Image Resource Authority

## Authority model (Migration 076)

Migration 010の`image_revision_evidence`は引き続きPlacement/materializationが消費するverified publicationです。Northbound producerはこのlegacy registration APIを公開しません。Migration 076は`northbound_image_revision_evidence`にlogical intentとcaller由来のexpected SHA-256だけを記録し、`image_ingestion_*`、`image_artifact_observation_evidence`、`image_artifact_verification_evidence`を別authorityにしました。independent verificationが`VERIFIED`になった時だけcontrollerがMigration 010 catalogへexact Image revisionをpublishします。

`RegisterImageRevision`は既存internal fixtureとの互換producerであり、Northbound HTTPから到達不能です。public Image schemaには`observedDigest`、path、URL、Host、cache identityが存在しません。

```text
logical Image != immutable content revision
              != ingestion Operation
              != observed artifact verification
              != Host cache/materialization
              != local/backend path
```

## Ingestion and read-back

`IMAGE_ARTIFACT_INGEST/kim.command.image-artifact-ingest/v1`はsource registry ID、Image/revision、artifact generation、expected digest、policy maximumだけを受けます。source fileとcache rootはadministrator-owned Agent configurationです。actuatorはstagingへbounded write、fsync、whole-artifact SHA-256、digest-addressed atomic renameを行い、Observerはpublished bytesを再読します。arbitrary URL/path/shell/argvはcommand schemaにありません。

Operationは`PENDING/RUNNING/VERIFYING/SUCCEEDED/FAILED/UNKNOWN/CANCELLED`を公開します。`UNKNOWN`はterminalではなく、same command/artifact generationのREAD_BACK_FIRST対象です。DB consumerが受け取るのはCommand verification IDであり、observed digest/size/read-back stateはimmutable `command_verification_evidence`から導出されます。Result欠落時もMATCHED read-backでSUCCEEDEDに収束できます。

## Lifecycle and consumers

createはlogical metadataを201でcommitし、ingestionは202です。未検証revisionはMigration 010 catalogへpublishされないためPlacement/materializationは利用できません。content changeはnew revision/new artifact generationであり、既存Admission、VM、Volume、materializationはhistorical exact revisionを維持します。DEPRECATEDはnew placement publicationを`DELETING`へ移し、参照中revisionのdeleteはdependency conflictです。logical deleteとHost cache cleanupは独立です。

Cache消失、別Hostでのcache realization、cache generation変更はTerraform driftではありません。同名別contentはdigest-addressed final identityとwhole-content read-backで拒否します。large artifactでwhole-volume hashingが高コストになる点は将来のchunk/Merkle verification最適化対象ですが、correctness条件は緩和しません。
