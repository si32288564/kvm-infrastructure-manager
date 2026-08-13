# ADR-0028: Local LVM transportを通常Host Agent sessionへbindする

## 状態

Accepted — 2026-08-13

## 背景

ADR-0026 / Migration 071はcross-Host Local LVM transport authorityと閉じたHTTP/2 data planeを定義したが、`SourceHandler`と`DestinationClient`はqualification fixtureからのみ起動され、通常の`kim-host-agent` session、typed execution registry、startup/shutdownへ接続されていなかった。またTLS 1.3はfixture/configurationに依存し、handler/clientのnegotiated-version確認がなかった。

## 決定

Local LVM transportを有効にした`kim-host-agent`は、通常Gateway sessionを開く前に専用east-west listener、LVM resolver、TLS identity、administrator-owned endpoint registryを検証し起動する。listener readinessに失敗した場合、Agentはtransport capabilityをadvertiseせずfail closedする。transportは既定で無効であり、以下を全て管理者設定する。

- `local-lvm-transport-listen`: 専用listen address/port
- `local-lvm-transport-endpoints`: source Host IDからHTTPS originへのmap
- 既存Agent CA/certificate/keyとcredential binding revision
- 既存Local LVM `VG UUID → VG name` map

source endpointのpathは`/v1/local-lvm/transport`に固定する。Command/DB/callerはURL、host:port、path、VG/LV name、device pathを渡せない。destination typed backendはMigration 071 authorityのsource Host IDをキーに管理者registryだけからendpointを解決する。block payloadはGateway、JetStream、PostgreSQL、Agent attempt journalを通らず、source Agentからdestination Agentへ直接流れる。

通常execution registryは次のclosed Commandを登録し、handshakeで`kim.host.local-lvm-cross-host-transport.v1`をadvertiseする。

- `LOCAL_LVM_TRANSPORT_SOURCE_AUTHORIZE`: exact source authorityをcurrent source Agent sessionにrouteし、Source readerのwhole-volume read-backを返す。
- `LOCAL_LVM_CROSS_HOST_TRANSPORT_START`: destination Agentがexact destination writer/flush/read-backとsource streamを実行する。

Control Planeの`PrepareLocalLVMTransportRuntimeCommands`だけが、immutable transport sessionから両Commandを生成する。Command payloadは両Host authority generation、credential revision、Agent session generation、Volume/Binding/VG/LV、copy generation、byte/chunk/policy/concurrency/expiry、certificate fingerprintを省略せず含む。endpointは含まない。

`PrepareLocalLVMTransportSession`はsource/destination双方のcurrent session attemptのimmutable handshake evidenceを再joinし、このcapabilityがadvertiseされていないHostをtransport authorityから除外する。runtime config変更はAgent reconnectと新しいsession generationを伴い、capability changeもそのgenerationへbindされる。したがってdisabled/misconfigured HostはLocal LVM EVACUATEのtransport consumerでBLOCKEDとなり、古いcapabilityをnew sessionへ持ち上げない。

source listener routeはtransport session/generation、authority digest、destination Host、mTLS peer fingerprintで選択する。Host Agentのnormal sessionがclose/reconnectした時は全routeを破棄する。old source authorityはnew sessionで自動復活せず、Control Planeのnew current authorityが必要である。process restartもmemory routeを失うため同じ性質を持つ。

TLS configurationは既存Agent credentialを再利用し、source/destinationの両方で`MinVersion = MaxVersion = TLS 1.3`、HTTP/2、client certificate必須とする。さらにsource handlerとdestination clientはnegotiated TLS versionが1.3でなければ拒否し、両certificate fingerprintをMigration 071 authorityへ照合する。

sourceとdestination roleは非対称のまま維持する。

```text
Source Agent      = Inspect + ReadAt
Destination Agent = Inspect + WriteAt + Flush + ReadAt
```

Agent runtimeのoperation contextとtransport session expiryは別である。Result response loss、disconnect、Lease/session expiryは未実行証明ではない。same copy generation / same destination Binding/LVをread-backし、first implementationはoffset zeroから再実行する。alternate destinationはreplan/new generationを必要とする。

## Network and operations

east-west TCP portはdeployment firewallでsource Agentへのdestination Agent接続だけを許可する。KIMは今回firewallを変更しない。listenerはgeneric HTTP serverではなく固定stream pathだけを扱う。graceful Agent shutdownはrouteを先にdeactivateし、active requestをHTTP server shutdownへ渡す。完了を推測しない。

transport metricsはproduct runtimeの共有`Metrics`へ記録する。Host/Volume/Binding/session identityをlabelにしない。Migration 071のbandwidth policyはevidenceへ記録されるがruntime rate enforcementはまだないため、production deploymentは外部帯域制御または将来のclosed limiterを必要とする。

## 結果

通常Host Agent runtime wiringとTLS 1.3 direct enforcementのP1は閉じる。これはreal g01/g02 block mutation、real EVACUATE、real source cleanupをPASSへ昇格しない。active Agent、disposable VM/LV、east-west firewall、real certificate profileを用いた別qualificationが必要である。
