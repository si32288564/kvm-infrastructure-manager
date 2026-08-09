package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/spool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/contracttest"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/transport/grpcstream"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestDurableDeliveryConvergesAfterReceiptLossAndGatewayRestart(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := postgres.OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('delivery-resync-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode = 'ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}
	hostID := fmt.Sprintf("delivery-resync-%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities (host_id, enrollment_state) VALUES ($1, 'APPROVED')`, hostID); err != nil {
		t.Fatal(err)
	}

	spoolDirectory := t.TempDir()
	if err := os.Chmod(spoolDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	spoolConfig := spool.Config{Directory: spoolDirectory, HostIdentity: hostID, MaxEntries: 16, MaxBytes: 1 << 20, MaxMessageBytes: 64 << 10}
	journal, err := spool.Open(spoolConfig)
	if err != nil {
		t.Fatal(err)
	}
	original := session.NewEnvelope(hostID, 1, session.StreamResult, hostID+"-result-1", "v1", "command-result", 1, []byte(`{"outcome":"SUCCEEDED"}`))
	if err := journal.Enqueue(original); err != nil {
		t.Fatal(err)
	}

	serverTLS, clientTLS, err := contracttest.TLSConfigs()
	if err != nil {
		t.Fatal(err)
	}
	startGateway := func() (*grpc.Server, string) {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		limiter, err := NewAdmissionLimiter(8)
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS.Clone())))
		agentprotocolv1.RegisterAgentTransportServer(server, GRPCServer{
			Authorizer:      PostgresSessionAuthorizer{DB: pool, Admission: limiter, RetryAfter: 25 * time.Millisecond},
			MessageReceiver: PostgresMessageReceiver{DB: pool, MaxMessageBytes: 64 << 10},
		})
		go func() { _ = server.Serve(listener) }()
		return server, listener.Addr().String()
	}
	handshake := func(generation uint64) session.Handshake {
		return session.Handshake{
			HostIdentity: hostID, SessionGeneration: generation, ProtocolVersion: "v1",
			SessionAttemptID:     fmt.Sprintf("%s-attempt-%d", hostID, generation),
			ConnectionInstanceID: fmt.Sprintf("%s-connection-%d", hostID, generation),
			AgentArtifactDigest:  deliveryDigest([]byte("agent-artifact")), CredentialBindingRevision: int64(generation),
		}
	}

	firstServer, firstTarget := startGateway()
	firstConnection, err := (&grpcstream.Adapter{Target: firstTarget, TLSConfig: clientTLS, MaxMessageBytes: 64 << 10}).Open(ctx, handshake(1))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := journal.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("first pending = %d, error = %v", len(pending), err)
	}
	if err := firstConnection.Send(ctx, pending[0].BindSession(1)); err != nil {
		t.Fatal(err)
	}
	// Wait until PostgreSQL has committed the receipt, then deliberately discard
	// the transport response and restart both Gateway and Agent journal owner.
	waitForReceipt(t, ctx, pool, hostID, original.MessageID)
	_ = firstConnection.Close()
	firstServer.Stop()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err = spool.Open(spoolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()

	secondServer, secondTarget := startGateway()
	defer secondServer.Stop()
	secondConnection, err := (&grpcstream.Adapter{Target: secondTarget, TLSConfig: clientTLS, MaxMessageBytes: 64 << 10}).Open(ctx, handshake(2))
	if err != nil {
		t.Fatal(err)
	}
	defer secondConnection.Close()
	receiptConnection, ok := secondConnection.(session.ReceiptConnection)
	if !ok {
		t.Fatal("gRPC connection does not expose durable receipts")
	}
	pending, err = journal.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("reopened pending = %d, error = %v", len(pending), err)
	}
	if err := secondConnection.Send(ctx, pending[0].BindSession(2)); err != nil {
		t.Fatal(err)
	}
	receiptContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	receipt, err := receiptConnection.ReceiveReceipt(receiptContext)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.AcceptedSessionGeneration != 1 || receipt.Disposition != "ACCEPTED" {
		t.Fatalf("replayed receipt = %#v", receipt)
	}
	if err := journal.Acknowledge(receipt); err != nil {
		t.Fatal(err)
	}
	digest, err := journal.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.CommitAgentResyncCheckpoint(ctx, pool, hostID, 2, digest, map[string]any{
		"queued_entries": 0, "replayed_messages": 1, "receipt_loss_recovered": true,
	}); err != nil {
		t.Fatal(err)
	}
	stats := journal.Stats()
	var receiptCount, checkpointGeneration int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_message_receipts WHERE host_id = $1`, hostID).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT session_generation FROM kim.agent_resync_checkpoints WHERE host_id = $1`, hostID).Scan(&checkpointGeneration); err != nil {
		t.Fatal(err)
	}
	if stats.QueuedEntries != 0 || receiptCount != 1 || checkpointGeneration != 2 {
		t.Fatalf("convergence queue/receipts/checkpoint = %d/%d/%d", stats.QueuedEntries, receiptCount, checkpointGeneration)
	}

	// The same transport/receipt path now carries a normalized typed Inventory
	// snapshot into immutable evidence and a rebuildable capability projection.
	inventorySnapshot := agentinventory.Snapshot{
		SchemaVersion: agentinventory.SnapshotSchemaV1, HostIdentity: hostID,
		ObservationGeneration: 1, CollectionStatus: "COMPLETE",
		Fragments: []agentinventory.Fragment{{
			Domain:         agentinventory.DomainVirtualization,
			Source:         agentinventory.Source{ModuleName: "libvirt", ModuleVersion: "v1", SchemaVersion: "v1", ArtifactDigest: deliveryDigest([]byte("libvirt-module"))},
			Capabilities:   []agentinventory.Capability{{Name: "kim.host.kvm.v1", Version: "v1", Available: true}},
			Virtualization: &agentinventory.Virtualization{KVMAvailable: true, LibvirtVersion: "fixture", QEMUVersion: "fixture", MachineTypes: []string{"pc-q35"}},
		}},
	}
	inventoryEnvelope, err := agentinventory.NewEnvelope(inventorySnapshot, 2, hostID+"-inventory-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Enqueue(inventoryEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := secondConnection.Send(ctx, inventoryEnvelope); err != nil {
		t.Fatal(err)
	}
	inventoryReceiptContext, inventoryCancel := context.WithTimeout(ctx, 5*time.Second)
	defer inventoryCancel()
	inventoryReceipt, err := receiptConnection.ReceiveReceipt(inventoryReceiptContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Acknowledge(inventoryReceipt); err != nil {
		t.Fatal(err)
	}
	var projectedGeneration int
	var projectionState, capabilityDigest string
	if err := pool.QueryRow(ctx, `
		SELECT observation_generation, projection_state, capability_digest
		FROM kim.host_capability_projections WHERE host_id = $1
	`, hostID).Scan(&projectedGeneration, &projectionState, &capabilityDigest); err != nil {
		t.Fatal(err)
	}
	if projectedGeneration != 1 || projectionState != "CURRENT" || len(capabilityDigest) != 64 || journal.Stats().QueuedEntries != 0 {
		t.Fatalf("Inventory projection generation/state/digest/queue = %d/%s/%s/%d", projectedGeneration, projectionState, capabilityDigest, journal.Stats().QueuedEntries)
	}
}

func waitForReceipt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hostID, messageID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_message_receipts WHERE host_id = $1 AND message_id = $2`, hostID, messageID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("durable Agent receipt was not committed")
}

func deliveryDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
