package p1bfault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
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
	consumerContext, stopConsumer := context.WithTimeout(ctx, 10*time.Second)
	defer stopConsumer()
	consumer, err := stream.CreateConsumer(consumerContext, jetstream.ConsumerConfig{Name: "kim-agent-gateway-command-v1", Durable: "kim-agent-gateway-command-v1", AckPolicy: jetstream.AckExplicitPolicy, AckWait: 2 * time.Second, MaxDeliver: 32, Replicas: 3})
	if err != nil {
		t.Fatal(err)
	}
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
	gatewayAddress := fmt.Sprintf("127.0.0.1:%d", gatewayPort)
	startGateway := func(suffix string) *fixtureProcess {
		return startFixtureProcess(t, "kim-agent-gateway-"+suffix, gatewayBinary, filepath.Join(workspace, "gateway-"+suffix+".log"),
			"-database-url", databaseURL, "-listen", gatewayAddress,
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
	markerPath := filepath.Join(agentState, "qualification-state", digest(staleCommand+"-target")+".json")
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("stale authority command produced a backend side effect: %v", err)
	}
	workerProcess.requireRunning(t)
	gatewayProcess.requireRunning(t)
	agentProcess.requireRunning(t)

	// Deterministically lose only the Gateway-to-Agent Receipt response. The
	// opaque proxy forwards the next complete TLS record (the Command), then
	// discards later downstream records while preserving Agent Result upload.
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
	writeFixtureFile(t, armPath, []byte("drop next Gateway response after Command\n"), 0o600)
	receiptLossCommand := createProtectedCommand(t, ctx, pool, hostID, "receipt-loss", authority.AuthorityGeneration, deliveryKey)
	eventually(t, 10*time.Second, func() bool {
		_, err := os.Stat(activatedPath)
		return err == nil
	}, "Receipt loss proxy did not activate after forwarding the Command TLS record")
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
	workerProcess.requireRunning(t)
	gatewayProcess.requireRunning(t)
	agentProcess.requireRunning(t)
}

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
