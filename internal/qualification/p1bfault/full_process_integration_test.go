package p1bfault

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/statemarker"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/delivery"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/security/tokenprotect"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestFullProcessCommandDeliveryFaultCampaign(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	binaryDirectory := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binaryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	natsBinary := buildFixtureBinary(t, root, binaryDirectory, "./internal/qualification/p1bfault/natsnode")
	faultProxyBinary := buildFixtureBinary(t, root, binaryDirectory, "./internal/qualification/p1bfault/faultproxy")
	upstreamFaultProxyBinary := buildFixtureBinary(t, root, binaryDirectory, "./internal/qualification/p1bfault/upstreamfaultproxy")
	gatewayBinary := buildFixtureBinary(t, root, binaryDirectory, "./cmd/kim-agent-gateway")
	agentBinary := buildFixtureBinary(t, root, binaryDirectory, "./cmd/kim-host-agent")
	workerBinary := buildFixtureBinary(t, root, binaryDirectory, "./cmd/kim-worker")

	pki := generateFixturePKI(t, workspace)
	natsIdentity := generateFixtureNATSIdentity(t, workspace)
	routePorts, clientPorts := reserveFixturePorts(t, 3), reserveFixturePorts(t, 3)
	natsConfigs := writeNATSConfigs(t, workspace, routePorts, clientPorts, pki, natsIdentity)
	natsProcesses := make(map[string]*fixtureProcess, 3)
	for index, config := range natsConfigs {
		name := fmt.Sprintf("KIM-NATS-%d", index+1)
		natsProcesses[name] = startFixtureProcess(t, name, natsBinary, filepath.Join(workspace, name+".log"), "-config", config)
	}
	natsURLs := make([]string, len(clientPorts))
	for index, port := range clientPorts {
		natsURLs[index] = fmt.Sprintf("tls://127.0.0.1:%d", port)
	}
	natsURL := strings.Join(natsURLs, ",")
	var connection *nats.Conn
	eventually(t, 20*time.Second, func() bool {
		candidate, err := nats.Connect(natsURL, nats.Name("kim-p1b-full-process-fixture"), nats.UserCredentials(natsIdentity.credsPath), nats.RootCAs(pki.caPath), nats.MaxReconnects(-1), nats.ReconnectWait(50*time.Millisecond))
		if err != nil {
			return false
		}
		connection = candidate
		return true
	}, "TLS/credentials NATS cluster did not accept the fixture client")
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	var stream jetstream.Stream
	var provisionErr error
	eventually(t, 20*time.Second, func() bool {
		attemptContext, stopAttempt := context.WithTimeout(ctx, 500*time.Millisecond)
		defer stopAttempt()
		stream, provisionErr = js.CreateStream(attemptContext, jetstream.StreamConfig{Name: "KIM_AGENT_COMMAND", Subjects: []string{delivery.Subject}, Storage: jetstream.FileStorage, Replicas: 3, Duplicates: time.Minute})
		return provisionErr == nil
	}, "three-replica JetStream did not become provisionable")
	var consumer jetstream.Consumer
	var consumerErr error
	eventually(t, 20*time.Second, func() bool {
		attemptContext, stopAttempt := context.WithTimeout(ctx, 500*time.Millisecond)
		defer stopAttempt()
		consumer, consumerErr = stream.CreateConsumer(attemptContext, jetstream.ConsumerConfig{Name: "kim-agent-gateway-command-v1", Durable: "kim-agent-gateway-command-v1", AckPolicy: jetstream.AckExplicitPolicy, AckWait: 2 * time.Second, MaxDeliver: 32, Replicas: 3})
		return consumerErr == nil
	}, "three-replica durable consumer did not become provisionable")
	eventually(t, 20*time.Second, func() bool {
		streamInfo, streamErr := stream.Info(ctx)
		consumerInfo, consumerErr := consumer.Info(ctx)
		return streamErr == nil && consumerErr == nil && streamInfo.Cluster != nil && consumerInfo.Cluster != nil && len(streamInfo.Cluster.Replicas) == 2 && len(consumerInfo.Cluster.Replicas) == 2
	}, "JetStream stream/consumer replicas did not form")

	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode) VALUES ('p1b-full-process',1,'ACTIVE') ON CONFLICT (singleton) DO UPDATE SET restore_epoch=EXCLUDED.restore_epoch, authority_generation=EXCLUDED.authority_generation, mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	hostID := "host-p1b-process"
	if err := prepareHostTrust(ctx, pool, hostID, pki.agentFingerprint); err != nil {
		t.Fatal(err)
	}
	deliveryKey := []byte("0123456789abcdef0123456789abcdef")
	deliveryKeyPath := filepath.Join(workspace, "delivery.key")
	writeFixtureFile(t, deliveryKeyPath, deliveryKey, 0o600)
	gatewayPort := reserveFixturePorts(t, 1)[0]
	gatewayListenHost := "127.0.0.1"
	if os.Getenv("KIM_P1B_REMOTE_KVM_SSH") != "" {
		gatewayListenHost = "0.0.0.0"
	}
	gatewayListenAddress := fmt.Sprintf("%s:%d", gatewayListenHost, gatewayPort)
	gatewayAddress := fmt.Sprintf("127.0.0.1:%d", gatewayPort)
	startGateway := func(suffix string) *fixtureProcess {
		return startFixtureProcess(t, "kim-agent-gateway-"+suffix, gatewayBinary, filepath.Join(workspace, "gateway-"+suffix+".log"),
			"-database-url", databaseURL, "-listen", gatewayListenAddress,
			"-tls-client-ca", pki.caPath, "-tls-cert", pki.gatewayCert, "-tls-key", pki.gatewayKey,
			"-nats-url", natsURL, "-nats-credentials", natsIdentity.credsPath, "-nats-tls-ca", pki.caPath)
	}
	gatewayProcess := startGateway("one")
	if !waitTCP(ctx, gatewayAddress) {
		t.Fatal("Gateway did not listen")
	}
	agentState := filepath.Join(workspace, "agent-state")
	startAgent := func(suffix, target string) *fixtureProcess {
		return startFixtureProcess(t, "kim-host-agent-"+suffix, agentBinary, filepath.Join(workspace, "agent-"+suffix+".log"),
			"-gateway", target, "-host-id", hostID, "-tls-server-name", "kim-agent-gateway",
			"-tls-ca", pki.caPath, "-tls-cert", pki.agentCert, "-tls-key", pki.agentKey,
			"-artifact-digest", digest("agent-artifact"), "-verifier-digest", digest("state-marker-verifier"),
			"-credential-binding-revision", "1", "-state-root", agentState)
	}
	agentProcess := startAgent("one", gatewayAddress)
	waitSessionGeneration(t, ctx, pool, hostID, 1)
	if err := establishHostReadiness(ctx, pool, hostID, 1); err != nil {
		t.Fatal(err)
	}
	authority, err := postgres.ArmHostOperationAuthority(ctx, pool, postgres.HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "qualification", ReasonCode: "full_process"})
	if err != nil {
		t.Fatal(err)
	}
	workerProcess := startFixtureProcess(t, "kim-worker", workerBinary, filepath.Join(workspace, "worker.log"),
		"-database-url", databaseURL, "-nats-url", natsURL, "-nats-credentials", natsIdentity.credsPath,
		"-nats-tls-ca", pki.caPath, "-delivery-key-file", deliveryKeyPath, "-delivery-key-id", "fixture-key-v1",
		"-outbox-publish-interval", "25ms", "-lease-sweep-interval", "50ms")

	streamInfo, err := stream.Info(ctx)
	if err != nil || streamInfo.Cluster == nil {
		t.Fatal("stream leader is unavailable")
	}
	oldLeader := streamInfo.Cluster.Leader
	natsProcesses[oldLeader].stop(t)
	eventually(t, 20*time.Second, func() bool {
		attemptContext, stopAttempt := context.WithTimeout(ctx, 500*time.Millisecond)
		defer stopAttempt()
		info, err := stream.Info(attemptContext)
		return err == nil && info.Cluster != nil && info.Cluster.Leader != "" && info.Cluster.Leader != oldLeader
	}, "JetStream did not elect a new leader after OS process kill")

	firstCommand := createProtectedCommand(t, ctx, pool, hostID, "leader-failover", authority.AuthorityGeneration, deliveryKey)
	waitJobState(t, ctx, pool, firstCommand, "SUCCEEDED")
	waitAgentSpoolEmpty(t, agentState)
	workerProcess.requireRunning(t)
	gatewayProcess.requireRunning(t)
	agentProcess.requireRunning(t)

	gatewayProcess.stop(t)
	eventually(t, 10*time.Second, func() bool {
		return !waitTCPAttempt(gatewayAddress)
	}, "Gateway listener remained after process stop")
	gatewayProcess = startGateway("two")
	if !waitTCP(ctx, gatewayAddress) {
		t.Fatal("restarted Gateway did not listen")
	}
	waitSessionGeneration(t, ctx, pool, hostID, 2)
	authority, err = postgres.ArmHostOperationAuthority(ctx, pool, postgres.HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 2, ActorID: "qualification", ReasonCode: "post_gateway_restart"})
	if err != nil {
		t.Fatal(err)
	}
	secondCommand := createProtectedCommand(t, ctx, pool, hostID, "gateway-restart", authority.AuthorityGeneration, deliveryKey)
	waitJobState(t, ctx, pool, secondCommand, "SUCCEEDED")
	waitAgentSpoolEmpty(t, agentState)

	agentProcess.stop(t)
	staleCommand := createProtectedCommand(t, ctx, pool, hostID, "stale-redelivery", authority.AuthorityGeneration, deliveryKey)
	waitRouteUnknown(t, ctx, pool, staleCommand)
	agentProcess = startAgent("two", gatewayAddress)
	waitSessionGeneration(t, ctx, pool, hostID, 3)
	eventually(t, 15*time.Second, func() bool {
		var commandState, leaseState string
		err := pool.QueryRow(ctx, `SELECT current.command_state, lease.lease_state FROM kim.execution_commands_current current JOIN kim.command_leases_current lease USING(command_id) WHERE current.command_id=$1`, staleCommand).Scan(&commandState, &leaseState)
		return err == nil && commandState == "UNKNOWN" && leaseState == "FENCED"
	}, "stale Command Lease did not fence after Agent session generation change")
	eventually(t, 15*time.Second, func() bool {
		info, err := consumer.Info(ctx)
		return err == nil && info.NumAckPending == 0 && info.NumPending == 0
	}, "stale authority redelivery did not reach terminal Bus handling")
	eventually(t, 15*time.Second, func() bool {
		var state string
		err := pool.QueryRow(ctx, `SELECT verification_state FROM kim.command_verification_evidence WHERE command_id=$1 AND attempt_index=1`, staleCommand).Scan(&state)
		return err == nil && state == "UNKNOWN"
	}, "journal-absent stale Command Verification did not persist UNKNOWN evidence")
	waitAgentSpoolEmpty(t, agentState)
	markerPath := filepath.Join(agentState, "qualification-state", digest(staleCommand+"-target")+".json")
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("stale authority command produced a backend side effect: %v", err)
	}
	workerProcess.requireRunning(t)
	gatewayProcess.requireRunning(t)
	agentProcess.requireRunning(t)

	// Deterministically lose only the Gateway-to-Agent Receipt response. A
	// qualification-only PostgreSQL trigger holds the Receipt transaction after
	// Result/Observation processing. Only then is the opaque downstream path
	// armed, so the fixture never assumes TLS records are application messages.
	agentProcess.stop(t)
	proxyPort := reserveFixturePorts(t, 1)[0]
	proxyAddress := fmt.Sprintf("127.0.0.1:%d", proxyPort)
	armPath := filepath.Join(workspace, "drop-receipt.arm")
	activatedPath := filepath.Join(workspace, "drop-receipt.activated")
	proxyProcess := startFixtureProcess(t, "receipt-loss-proxy", faultProxyBinary, filepath.Join(workspace, "receipt-loss-proxy.log"),
		"-listen", proxyAddress, "-target", gatewayAddress, "-arm-file", armPath, "-activated-file", activatedPath)
	if !waitTCP(ctx, proxyAddress) {
		t.Fatal("Receipt loss proxy did not listen")
	}
	agentProcess = startAgent("receipt-loss", proxyAddress)
	waitSessionGeneration(t, ctx, pool, hostID, 4)
	authority, err = postgres.ArmHostOperationAuthority(ctx, pool, postgres.HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 4, ActorID: "qualification", ReasonCode: "receipt_loss"})
	if err != nil {
		t.Fatal(err)
	}
	installAgentReceiptBarrier(t, ctx, pool)
	receiptLossMessage := "command-result/p1b-receipt-loss/1"
	receiptBarrierConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var receiptBarrierKey int64
	if err := receiptBarrierConnection.QueryRow(ctx, `SELECT hashtextextended($1,0)`, receiptLossMessage).Scan(&receiptBarrierKey); err != nil {
		t.Fatal(err)
	}
	if _, err := receiptBarrierConnection.Exec(ctx, `SELECT pg_advisory_lock($1)`, receiptBarrierKey); err != nil {
		t.Fatal(err)
	}
	receiptLossCommand := createProtectedCommand(t, ctx, pool, hostID, "receipt-loss", authority.AuthorityGeneration, deliveryKey)
	waitAgentReceiptBarrier(t, ctx, pool)
	writeFixtureFile(t, armPath, []byte("drop committed Receipt response\n"), 0o600)
	if _, err := receiptBarrierConnection.Exec(ctx, `SELECT pg_advisory_unlock($1)`, receiptBarrierKey); err != nil {
		t.Fatal(err)
	}
	receiptBarrierConnection.Release()
	eventually(t, 10*time.Second, func() bool {
		_, err := os.Stat(activatedPath)
		return err == nil
	}, "Receipt loss proxy did not discard the committed Receipt response")
	waitJobState(t, ctx, pool, receiptLossCommand, "SUCCEEDED")
	waitAgentSpoolCount(t, agentState, 1)
	assertResultReceipt(t, ctx, pool, hostID, receiptLossCommand, 4)

	// Restart both ends of the lost response path. The Agent replays the same
	// message identity/digest on generation 5 and recovers the generation 4
	// PostgreSQL Receipt before deleting exactly one spool entry.
	agentProcess.stop(t)
	proxyProcess.stop(t)
	gatewayProcess.stop(t)
	gatewayProcess = startGateway("three")
	if !waitTCP(ctx, gatewayAddress) {
		t.Fatal("Gateway after Receipt loss did not listen")
	}
	agentProcess = startAgent("receipt-replay", gatewayAddress)
	waitSessionGeneration(t, ctx, pool, hostID, 5)
	waitAgentSpoolEmpty(t, agentState)
	assertResultReceipt(t, ctx, pool, hostID, receiptLossCommand, 4)

	// Hold ROUTE_ACCEPTED persistence after the live gRPC stream write but
	// before the Handler can return ConsumeAck to JetStream. The PostgreSQL
	// trigger is qualification-only and blocks on the target message advisory
	// lock; no product fault switch or authority shortcut is introduced.
	authority, err = postgres.ArmHostOperationAuthority(ctx, pool, postgres.HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 5, ActorID: "qualification", ReasonCode: "pre_bus_ack_kill"})
	if err != nil {
		t.Fatal(err)
	}
	installGatewayAckBarrier(t, ctx, pool)
	ackBarrierCommand := "p1b-gateway-pre-ack-kill"
	ackBarrierMessage := "command-lease-delivery/" + ackBarrierCommand + "/1"
	barrierConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var barrierKey int64
	if err := barrierConnection.QueryRow(ctx, `SELECT hashtextextended($1,0)`, ackBarrierMessage).Scan(&barrierKey); err != nil {
		t.Fatal(err)
	}
	if _, err := barrierConnection.Exec(ctx, `SELECT pg_advisory_lock($1)`, barrierKey); err != nil {
		t.Fatal(err)
	}
	created := createProtectedCommand(t, ctx, pool, hostID, "gateway-pre-ack-kill", authority.AuthorityGeneration, deliveryKey)
	if created != ackBarrierCommand {
		t.Fatalf("ack barrier Command identity = %s, want %s", created, ackBarrierCommand)
	}
	waitGatewayAckBarrier(t, ctx, pool)
	waitJobState(t, ctx, pool, ackBarrierCommand, "SUCCEEDED")
	waitAgentSpoolEmpty(t, agentState)
	assertResultReceipt(t, ctx, pool, hostID, ackBarrierCommand, 5)
	ackBarrierMarker := filepath.Join(agentState, "qualification-state", digest(ackBarrierCommand+"-target")+".json")
	markerBefore, err := os.Stat(ackBarrierMarker)
	if err != nil {
		t.Fatal(err)
	}
	markerDigestBefore := fileDigest(t, ackBarrierMarker)

	// Hard-kill the Gateway while its delivery Handler is blocked between the
	// stream write and JetStream ACK. Releasing the fixture lock cannot commit
	// the killed transaction; the durable Bus message must redeliver.
	gatewayProcess.kill(t)
	if _, err := barrierConnection.Exec(ctx, `SELECT pg_advisory_unlock($1)`, barrierKey); err != nil {
		t.Fatal(err)
	}
	barrierConnection.Release()
	gatewayProcess = startGateway("four")
	if !waitTCP(ctx, gatewayAddress) {
		t.Fatal("Gateway after pre-ACK kill did not listen")
	}
	waitSessionGeneration(t, ctx, pool, hostID, 6)
	eventually(t, 15*time.Second, func() bool {
		info, err := consumer.Info(ctx)
		return err == nil && info.NumAckPending == 0 && info.NumPending == 0
	}, "pre-ACK Gateway kill redelivery did not reach terminal Bus handling")
	assertGatewayPreAckConvergence(t, ctx, pool, ackBarrierCommand, ackBarrierMessage)
	assertResultReceipt(t, ctx, pool, hostID, ackBarrierCommand, 5)
	markerAfter, err := os.Stat(ackBarrierMarker)
	if err != nil {
		t.Fatal(err)
	}
	if markerDigestAfter := fileDigest(t, ackBarrierMarker); markerDigestAfter != markerDigestBefore || !markerAfter.ModTime().Equal(markerBefore.ModTime()) {
		t.Fatal("pre-ACK redelivery repeated the backend side effect")
	}
	workerProcess.requireRunning(t)
	gatewayProcess.requireRunning(t)
	agentProcess.requireRunning(t)

	if remote := os.Getenv("KIM_P1B_REMOTE_KVM_SSH"); remote != "" {
		runRemoteKVMConvergence(t, ctx, remoteKVMFixture{
			Remote: remote, GatewayHost: os.Getenv("KIM_P1B_REMOTE_GATEWAY_HOST"), GatewayPort: gatewayPort,
			Root: root, Workspace: workspace, UpstreamFaultProxyBinary: upstreamFaultProxyBinary, PKI: pki,
			Pool: pool, HostID: hostID, LocalAgent: agentProcess, LocalAgentState: agentState,
			DeliveryKey: deliveryKey,
		})
	}
}

type remoteKVMFixture struct {
	Remote, GatewayHost, Root, Workspace, UpstreamFaultProxyBinary, HostID, LocalAgentState string
	GatewayPort                                                                             int
	PKI                                                                                     fixturePKI
	Pool                                                                                    *pgxpool.Pool
	LocalAgent                                                                              *fixtureProcess
	DeliveryKey                                                                             []byte
}

func runRemoteKVMConvergence(t *testing.T, ctx context.Context, fixture remoteKVMFixture) {
	t.Helper()
	if fixture.GatewayHost == "" {
		t.Fatal("KIM_P1B_REMOTE_GATEWAY_HOST is required with KIM_P1B_REMOTE_KVM_SSH")
	}
	fixture.LocalAgent.stop(t)
	var previousGeneration int64
	if err := fixture.Pool.QueryRow(ctx, `SELECT session_generation FROM kim.agent_transport_sessions_current WHERE host_id=$1`, fixture.HostID).Scan(&previousGeneration); err != nil {
		t.Fatal(err)
	}
	remoteRoot := fmt.Sprintf("/tmp/kim-p1b-libvirt-%d", time.Now().UnixNano())
	remoteRun(t, ctx, fixture.Remote, "mkdir -p "+shellQuote(remoteRoot+"/source")+" "+shellQuote(remoteRoot+"/state/session-generation")+" "+shellQuote(remoteRoot+"/tls"))
	t.Cleanup(func() { _, _ = remoteOutput(context.Background(), fixture.Remote, "rm -rf "+shellQuote(remoteRoot)) })
	buildRemoteHostAgent(t, ctx, fixture.Root, fixture.Remote, remoteRoot)
	copyRemoteFile(t, ctx, fixture.Remote, fixture.PKI.caPath, remoteRoot+"/tls/ca.pem")
	copyRemoteFile(t, ctx, fixture.Remote, fixture.PKI.agentCert, remoteRoot+"/tls/agent.pem")
	copyRemoteFile(t, ctx, fixture.Remote, fixture.PKI.agentKey, remoteRoot+"/tls/agent-key.pem")
	copyRemoteFile(t, ctx, fixture.Remote, filepath.Join(fixture.LocalAgentState, "session-generation", "current.json"), remoteRoot+"/state/session-generation/current.json")

	domainUUID := qualificationUUID(t)
	domainName := "kim-full-process-" + strings.ReplaceAll(domainUUID[:8], "-", "")
	domainXML := fmt.Sprintf(`<domain type='kvm'><name>%s</name><uuid>%s</uuid><memory unit='MiB'>64</memory><currentMemory unit='MiB'>64</currentMemory><vcpu>1</vcpu><os><type arch='x86_64' machine='pc'>hvm</type><boot dev='hd'/></os><features><acpi/></features><clock offset='utc'/><on_poweroff>destroy</on_poweroff><on_reboot>restart</on_reboot><on_crash>destroy</on_crash><devices><emulator>/usr/bin/qemu-system-x86_64</emulator><controller type='pci' model='pci-root'/><memballoon model='none'/></devices></domain>`, domainName, domainUUID)
	remoteInput(t, ctx, fixture.Remote, "virsh -c qemu:///system define /dev/stdin", domainXML)
	t.Cleanup(func() {
		_, _ = remoteOutput(context.Background(), fixture.Remote, "virsh -c qemu:///system destroy "+shellQuote(domainUUID)+" >/dev/null 2>&1 || true; virsh -c qemu:///system undefine "+shellQuote(domainUUID)+" >/dev/null 2>&1 || true")
	})

	proxyPort := reserveFixturePorts(t, 1)[0]
	proxyAddress := fmt.Sprintf("0.0.0.0:%d", proxyPort)
	armPath := filepath.Join(fixture.Workspace, "drop-agent-result.arm")
	activatedPath := filepath.Join(fixture.Workspace, "drop-agent-result.activated")
	proxy := startFixtureProcess(t, "remote-agent-result-loss-proxy", fixture.UpstreamFaultProxyBinary, filepath.Join(fixture.Workspace, "remote-agent-result-loss-proxy.log"),
		"-listen", proxyAddress, "-target", fmt.Sprintf("127.0.0.1:%d", fixture.GatewayPort), "-arm-file", armPath, "-activated-file", activatedPath)
	if !waitTCP(ctx, fmt.Sprintf("127.0.0.1:%d", proxyPort)) {
		t.Fatal("remote Agent result-loss proxy did not listen")
	}
	remoteAgent := startRemoteAgent(t, fixture, remoteRoot, fmt.Sprintf("%s:%d", fixture.GatewayHost, proxyPort), "one")
	waitSessionGeneration(t, ctx, fixture.Pool, fixture.HostID, previousGeneration+1)
	authority, err := postgres.ArmHostOperationAuthority(ctx, fixture.Pool, postgres.HostAuthorityArmRequest{HostID: fixture.HostID, PolicyID: "manual", PolicyGeneration: previousGeneration + 1, ActorID: "qualification", ReasonCode: "remote_kvm_libvirt"})
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, armPath, []byte("drop Agent Result and Observation TLS records\n"), 0o600)
	commandID := createProtectedLibvirtCommand(t, ctx, fixture.Pool, fixture.HostID, domainUUID, authority.AuthorityGeneration, fixture.DeliveryKey)
	eventually(t, 20*time.Second, func() bool {
		state, err := remoteOutput(ctx, fixture.Remote, "virsh -c qemu:///system domstate "+shellQuote(domainUUID))
		return err == nil && strings.Contains(strings.ToLower(state), "running")
	}, "remote typed libvirt mutation did not start the KVM Domain")
	eventually(t, 10*time.Second, func() bool { _, err := os.Stat(activatedPath); return err == nil }, "Agent Result path was not deterministically blocked")
	killRemoteAgent(t, ctx, fixture.Remote, remoteRoot)
	remoteAgent.stop(t)
	eventually(t, 20*time.Second, func() bool {
		var state, attemptState string
		err := fixture.Pool.QueryRow(ctx, `SELECT current.command_state,COALESCE((SELECT event_type FROM kim.command_attempt_events WHERE command_id=current.command_id AND attempt_index=current.current_attempt_index AND event_type='UNKNOWN'),'') FROM kim.execution_commands_current current WHERE current.command_id=$1`, commandID).Scan(&state, &attemptState)
		return err == nil && state == "UNKNOWN" && attemptState == "UNKNOWN"
	}, "lost remote libvirt Result did not converge to UNKNOWN after Lease expiry")
	proxy.stop(t)
	remoteAgent = startRemoteAgent(t, fixture, remoteRoot, fmt.Sprintf("%s:%d", fixture.GatewayHost, fixture.GatewayPort), "two")
	waitSessionGeneration(t, ctx, fixture.Pool, fixture.HostID, previousGeneration+2)
	waitJobState(t, ctx, fixture.Pool, commandID, "SUCCEEDED")
	var attemptState, verificationState string
	var leases, attempts, verifications int
	if err := fixture.Pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT event_type FROM kim.command_attempt_events WHERE command_id=$1 AND attempt_index=1 AND event_type='UNKNOWN'),''),
		       (SELECT verification_state FROM kim.command_verification_evidence WHERE command_id=$1 ORDER BY recorded_at DESC LIMIT 1),
		       (SELECT count(*) FROM kim.command_lease_grants WHERE command_id=$1),
		       (SELECT count(*) FROM kim.command_attempts WHERE command_id=$1),
		       (SELECT count(*) FROM kim.command_verification_evidence WHERE command_id=$1)
		FROM kim.command_attempts attempt WHERE attempt.command_id=$1 AND attempt.attempt_index=1
	`, commandID).Scan(&attemptState, &verificationState, &leases, &attempts, &verifications); err != nil {
		t.Fatal(err)
	}
	if attemptState != "UNKNOWN" || verificationState != "MATCHED" || leases != 1 || attempts != 1 || verifications != 1 {
		t.Fatalf("remote libvirt convergence attempt/verification/leases/attempts/evidence = %s/%s/%d/%d/%d", attemptState, verificationState, leases, attempts, verifications)
	}
	remoteAgent.requireRunning(t)
}

func buildRemoteHostAgent(t *testing.T, ctx context.Context, root, remote, remoteRoot string) {
	t.Helper()
	archive := exec.CommandContext(ctx, "git", "archive", "--format=tar", "HEAD")
	archive.Dir = root
	pipe, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	remoteExtract := exec.CommandContext(ctx, "ssh", remote, "tar -xf - -C "+shellQuote(remoteRoot+"/source"))
	remoteExtract.Stdin = pipe
	var remoteLog strings.Builder
	remoteExtract.Stdout, remoteExtract.Stderr = &remoteLog, &remoteLog
	if err := remoteExtract.Start(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Start(); err != nil {
		t.Fatal(err)
	}
	archiveErr := archive.Wait()
	extractErr := remoteExtract.Wait()
	if archiveErr != nil || extractErr != nil {
		t.Fatalf("remote Host Agent source transfer: archive=%v extract=%v\n%s", archiveErr, extractErr, remoteLog.String())
	}
	// The qualification may exercise uncommitted candidate code. Overlay the
	// Agent execution boundary under test before compiling on Linux/cgo.
	copyRemoteFile(t, ctx, remote, filepath.Join(root, "internal/agent/execution/module.go"), remoteRoot+"/source/internal/agent/execution/module.go")
	copyRemoteFile(t, ctx, remote, filepath.Join(root, "internal/agent/spool/spool.go"), remoteRoot+"/source/internal/agent/spool/spool.go")
	buildOutput, buildErr := remoteOutput(ctx, remote, "cd "+shellQuote(remoteRoot+"/source")+" && go build -tags libvirt -o "+shellQuote(remoteRoot+"/kim-host-agent")+" ./cmd/kim-host-agent")
	if buildErr != nil {
		t.Fatalf("remote Host Agent build: %v\n%s", buildErr, buildOutput)
	}
}

func copyRemoteFile(t *testing.T, ctx context.Context, remote, source, destination string) {
	t.Helper()
	command := exec.CommandContext(ctx, "scp", source, remote+":"+destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("copy remote fixture %s: %v\n%s", filepath.Base(source), err, output)
	}
}

func startRemoteAgent(t *testing.T, fixture remoteKVMFixture, remoteRoot, gateway, suffix string) *fixtureProcess {
	t.Helper()
	pidPath := remoteRoot + "/agent.pid"
	command := "echo $$ > " + shellQuote(pidPath) + "; exec " + shellQuote(remoteRoot+"/kim-host-agent") +
		" -gateway " + shellQuote(gateway) + " -host-id " + shellQuote(fixture.HostID) + " -tls-server-name kim-agent-gateway" +
		" -tls-ca " + shellQuote(remoteRoot+"/tls/ca.pem") + " -tls-cert " + shellQuote(remoteRoot+"/tls/agent.pem") + " -tls-key " + shellQuote(remoteRoot+"/tls/agent-key.pem") +
		" -artifact-digest " + digest("agent-artifact") + " -verifier-digest " + digest("state-marker-verifier") +
		" -credential-binding-revision 1 -state-root " + shellQuote(remoteRoot+"/state") + " -libvirt-uri qemu:///system"
	return startFixtureProcess(t, "remote-kim-host-agent-"+suffix, "ssh", filepath.Join(fixture.Workspace, "remote-agent-"+suffix+".log"), fixture.Remote, command)
}

func killRemoteAgent(t *testing.T, ctx context.Context, remote, remoteRoot string) {
	t.Helper()
	remoteRun(t, ctx, remote, "kill -KILL $(cat "+shellQuote(remoteRoot+"/agent.pid")+")")
}

func createProtectedLibvirtCommand(t *testing.T, ctx context.Context, pool postgres.TxBeginner, hostID, uuid string, authorityGeneration int64, key []byte) string {
	t.Helper()
	commandID := "p1b-remote-libvirt"
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: commandID + "-job", CommandID: commandID, HostID: hostID, ResourceType: "VM", ResourceID: uuid, DesiredRevision: 1, CommandType: libvirtdomain.CommandType, SchemaVersion: libvirtdomain.SchemaVersion, TargetResourceID: "vm:" + uuid, Payload: map[string]any{"desired_state": libvirtdomain.StateRunning}}); err != nil {
		t.Fatal(err)
	}
	protector := tokenprotect.AESGCM{KeyID: "fixture-key-v1", Key: key}
	if _, err := postgres.AcquireCommandLease(ctx, pool, postgres.CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authorityGeneration, Duration: 5 * time.Second, ExecutionTimeout: 2 * time.Second, DeliveryProtector: protector}); err != nil {
		t.Fatal(err)
	}
	return commandID
}

func qualificationUUID(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	buffer[6], buffer[8] = (buffer[6]&0x0f)|0x40, (buffer[8]&0x3f)|0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

func remoteRun(t *testing.T, ctx context.Context, remote, command string) {
	t.Helper()
	if output, err := remoteOutput(ctx, remote, command); err != nil {
		t.Fatalf("remote command: %v\n%s", err, output)
	}
}

func remoteOutput(ctx context.Context, remote, command string) (string, error) {
	output, err := exec.CommandContext(ctx, "ssh", remote, command).CombinedOutput()
	return string(output), err
}

func remoteInput(t *testing.T, ctx context.Context, remote, command, input string) {
	t.Helper()
	process := exec.CommandContext(ctx, "ssh", remote, command)
	process.Stdin = strings.NewReader(input)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("remote input command: %v\n%s", err, output)
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func prepareHostTrust(ctx context.Context, pool postgres.TxBeginner, hostID, fingerprint string) error {
	if err := postgres.RegisterDiscoveredHost(ctx, pool, hostID); err != nil {
		return err
	}
	if err := postgres.RecordEnrollmentDecision(ctx, pool, postgres.EnrollmentDecision{DecisionID: hostID + "-enrollment", HostID: hostID, Revision: 1, PolicyID: "manual", PolicyGeneration: 1, HardwareEvidenceDigest: digest("hardware"), State: "APPROVED", ActorID: "qualification", ReasonCode: "full_process"}); err != nil {
		return err
	}
	now := time.Now().UTC()
	return postgres.RecordAgentCredentialBinding(ctx, pool, postgres.AgentCredentialBindingEvidence{HostID: hostID, Revision: 1, CertificateFingerprint: fingerprint, PublicKeyDigest: digest("public-key"), IssuerID: "fixture-ca", ProfileRevision: "host-agent/v1", TrustGeneration: 1, EnrollmentDecisionID: hostID + "-enrollment", EnrollmentRevision: 1, EvidenceDigest: digest("credential"), State: "ACTIVE", ValidNotBefore: now.Add(-time.Hour), ValidNotAfter: now.Add(time.Hour)})
}

func establishHostReadiness(ctx context.Context, pool postgres.TxBeginner, hostID string, sessionGeneration int64) error {
	snapshot := inventory.Snapshot{SchemaVersion: inventory.SnapshotSchemaV3, HostIdentity: hostID, ObservationGeneration: 1, CollectionStatus: "COMPLETE", Fragments: []inventory.Fragment{{Domain: inventory.DomainVirtualization, Source: inventory.Source{ModuleName: "qualification", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: digest("inventory-module")}, Capabilities: []inventory.Capability{{Name: "kim.host.kvm.v1", Version: "v1", State: inventory.AvailabilityAvailable}}, Virtualization: &inventory.Virtualization{KVMAvailable: true, LibvirtVersion: "fixture", QEMUVersion: "fixture"}}}}
	envelope, err := inventory.NewEnvelope(snapshot, uint64(sessionGeneration), hostID+"-inventory-1")
	if err != nil {
		return err
	}
	if _, err := postgres.AcceptHostInventory(ctx, pool, envelope, 1<<20); err != nil {
		return err
	}
	return postgres.UpdateHostReadinessGate(ctx, pool, postgres.HostReadinessGate{HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1, PreflightGeneration: 1, PreflightState: "PASSED", ComplianceGeneration: 1, ComplianceState: "COMPLIANT"})
}

func createProtectedCommand(t *testing.T, ctx context.Context, pool postgres.TxBeginner, hostID, suffix string, authorityGeneration int64, key []byte) string {
	t.Helper()
	commandID := "p1b-" + suffix
	if err := postgres.CreateExecutionCommand(ctx, pool, postgres.ExecutionCommandRequest{JobID: commandID + "-job", CommandID: commandID, HostID: hostID, ResourceType: "HOST_AGENT_STATE", ResourceID: commandID + "-resource", DesiredRevision: 1, CommandType: statemarker.CommandType, SchemaVersion: statemarker.SchemaVersion, TargetResourceID: commandID + "-target", Payload: map[string]any{"value": suffix}}); err != nil {
		t.Fatal(err)
	}
	protector := tokenprotect.AESGCM{KeyID: "fixture-key-v1", Key: key}
	if _, err := postgres.AcquireCommandLease(ctx, pool, postgres.CommandLeaseRequest{CommandID: commandID, HostAuthorityGeneration: authorityGeneration, Duration: 30 * time.Second, ExecutionTimeout: 10 * time.Second, DeliveryProtector: protector}); err != nil {
		t.Fatal(err)
	}
	return commandID
}

func waitSessionGeneration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hostID string, expected int64) {
	t.Helper()
	eventually(t, 20*time.Second, func() bool {
		var generation int64
		var state string
		err := pool.QueryRow(ctx, `SELECT session_generation, state FROM kim.agent_transport_sessions_current WHERE host_id=$1`, hostID).Scan(&generation, &state)
		return err == nil && generation == expected && state == "CURRENT"
	}, fmt.Sprintf("Agent session generation %d did not become current", expected))
}

func waitJobState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commandID, expected string) {
	t.Helper()
	eventually(t, 20*time.Second, func() bool {
		var state string
		err := pool.QueryRow(ctx, `SELECT job_state FROM kim.execution_jobs WHERE job_id=$1`, commandID+"-job").Scan(&state)
		return err == nil && state == expected
	}, fmt.Sprintf("Command %s did not reach Job state %s", commandID, expected))
}

func waitRouteUnknown(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commandID string) {
	t.Helper()
	messageID := "command-lease-delivery/" + commandID + "/1"
	eventually(t, 15*time.Second, func() bool {
		var count int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.gateway_command_delivery_events WHERE message_id=$1 AND event_type='ROUTE_UNKNOWN'`, messageID).Scan(&count)
		return err == nil && count > 0
	}, "Gateway did not persist ROUTE_UNKNOWN for absent Agent session")
}

func waitAgentSpoolEmpty(t *testing.T, stateRoot string) {
	t.Helper()
	queue := filepath.Join(stateRoot, "spool", "queue")
	eventually(t, 10*time.Second, func() bool {
		entries, err := os.ReadDir(queue)
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				return false
			}
		}
		return true
	}, "Agent durable spool did not receive its PostgreSQL-backed application Receipt")
}

func waitAgentSpoolCount(t *testing.T, stateRoot string, expected int) {
	t.Helper()
	queue := filepath.Join(stateRoot, "spool", "queue")
	eventually(t, 10*time.Second, func() bool {
		entries, err := os.ReadDir(queue)
		if err != nil {
			return false
		}
		count := 0
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				count++
			}
		}
		return count == expected
	}, fmt.Sprintf("Agent durable spool did not retain exactly %d unacknowledged message(s)", expected))
}

func assertResultReceipt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hostID, commandID string, acceptedGeneration int64) {
	t.Helper()
	messageID := "command-result/" + commandID + "/1"
	var count int
	var generation int64
	if err := pool.QueryRow(ctx, `SELECT count(*), max(session_generation) FROM kim.agent_message_receipts WHERE host_id=$1 AND message_id=$2 AND disposition='ACCEPTED'`, hostID, messageID).Scan(&count, &generation); err != nil {
		t.Fatal(err)
	}
	if count != 1 || generation != acceptedGeneration {
		t.Fatalf("durable Result Receipt count/generation = %d/%d, want 1/%d", count, generation, acceptedGeneration)
	}
}

func installGatewayAckBarrier(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION kim.qualification_gateway_ack_barrier() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.event_type = 'ROUTE_ACCEPTED' THEN
				PERFORM pg_advisory_xact_lock(hashtextextended(NEW.message_id,0));
			END IF;
			RETURN NEW;
		END
		$$
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS qualification_gateway_ack_barrier ON kim.gateway_command_delivery_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER qualification_gateway_ack_barrier
		BEFORE INSERT ON kim.gateway_command_delivery_events
		FOR EACH ROW EXECUTE FUNCTION kim.qualification_gateway_ack_barrier()
	`); err != nil {
		t.Fatal(err)
	}
}

func installAgentReceiptBarrier(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION kim.qualification_agent_receipt_barrier() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(hashtextextended(NEW.message_id,0));
			RETURN NEW;
		END
		$$
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS qualification_agent_receipt_barrier ON kim.agent_message_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER qualification_agent_receipt_barrier BEFORE INSERT ON kim.agent_message_receipts FOR EACH ROW EXECUTE FUNCTION kim.qualification_agent_receipt_barrier()`); err != nil {
		t.Fatal(err)
	}
}

func waitAgentReceiptBarrier(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	eventually(t, 15*time.Second, func() bool {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory' AND query LIKE '%agent_message_receipts%')`).Scan(&blocked)
		return err == nil && blocked
	}, "Agent Receipt transaction did not reach the qualification barrier")
}

func waitGatewayAckBarrier(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	eventually(t, 15*time.Second, func() bool {
		var blocked bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE datname=current_database()
				  AND wait_event_type='Lock' AND wait_event='advisory'
				  AND query LIKE '%gateway_command_delivery_events%'
			)
		`).Scan(&blocked)
		return err == nil && blocked
	}, "Gateway delivery Handler did not block between stream write and JetStream ACK")
}

func assertGatewayPreAckConvergence(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commandID, messageID string) {
	t.Helper()
	var started, accepted int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type='ROUTE_STARTED'),
		       count(*) FILTER (WHERE event_type='ROUTE_ACCEPTED')
		FROM kim.gateway_command_delivery_events WHERE message_id=$1
	`, messageID).Scan(&started, &accepted); err != nil {
		t.Fatal(err)
	}
	if started != 1 || accepted != 0 {
		t.Fatalf("pre-ACK route evidence started/accepted = %d/%d, want 1/0", started, accepted)
	}
	var leases, attempts int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM kim.command_lease_grants WHERE command_id=$1),
		       (SELECT count(*) FROM kim.command_attempts WHERE command_id=$1)
	`, commandID).Scan(&leases, &attempts); err != nil {
		t.Fatal(err)
	}
	if leases != 1 || attempts != 1 {
		t.Fatalf("pre-ACK Lease/Attempt count = %d/%d, want 1/1", leases, attempts)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest(string(payload))
}

func waitTCPAttempt(address string) bool {
	connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
