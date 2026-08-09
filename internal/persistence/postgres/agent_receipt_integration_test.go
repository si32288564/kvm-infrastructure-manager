package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

func TestAgentReceiptReplayAndResyncPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('agent-receipt-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode = 'ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}
	hostID := fmt.Sprintf("agent-receipt-%d", time.Now().UnixNano())
	fingerprint := digestBytes([]byte("agent-receipt-certificate"))
	prepareSessionIdentityFixture(t, ctx, pool, hostID, 1, fingerprint)
	grantSession := func(attempt string, expected int64) {
		t.Helper()
		if expected > 1 {
			prepareSessionIdentityFixture(t, ctx, pool, hostID, expected, fingerprint)
		}
		grant, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{
			SessionAttemptID: attempt, HostID: hostID, ConnectionInstanceID: attempt + "-connection",
			TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")),
			CredentialBindingRevision: expected, PeerCertificateFingerprint: fingerprint,
			ExpectedSessionGeneration: expected,
		})
		if err != nil || grant.SessionGeneration != expected {
			t.Fatalf("grant %s = %#v, error = %v", attempt, grant, err)
		}
	}
	grantSession(hostID+"-attempt-1", 1)
	original := session.NewEnvelope(hostID, 1, session.StreamResult, hostID+"-message-1", "v1", "result", 1, []byte("result"))
	receipt, err := AcceptAgentMessage(ctx, pool, original, 64*1024)
	if err != nil || receipt.Disposition != "ACCEPTED" || receipt.AcceptedSessionGeneration != 1 {
		t.Fatalf("first receipt = %#v, error = %v", receipt, err)
	}

	grantSession(hostID+"-attempt-2", 2)
	replayed, err := AcceptAgentMessage(ctx, pool, original.BindSession(2), 64*1024)
	if err != nil || replayed != receipt {
		t.Fatalf("replayed receipt = %#v, want %#v, error = %v", replayed, receipt, err)
	}
	conflict := original.BindSession(2)
	conflict.Payload = []byte("different")
	conflict.PayloadDigest = digestBytes(conflict.Payload)
	if _, err := AcceptAgentMessage(ctx, pool, conflict, 64*1024); !errors.Is(err, ErrAgentMessageEvidenceConflict) {
		t.Fatalf("conflicting replay error = %v", err)
	}
	stale := session.NewEnvelope(hostID, 1, session.StreamResult, hostID+"-message-2", "v1", "result", 2, []byte("stale"))
	staleReceipt, err := AcceptAgentMessage(ctx, pool, stale, 64*1024)
	if err != nil || staleReceipt.Disposition != "STALE" || staleReceipt.AcceptedSessionGeneration != 2 {
		t.Fatalf("stale receipt = %#v, error = %v", staleReceipt, err)
	}
	concurrent := session.NewEnvelope(hostID, 2, session.StreamInventory, hostID+"-message-3", "v1", "inventory", 1, []byte("snapshot"))
	var wait sync.WaitGroup
	concurrentErrors := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			receipt, err := AcceptAgentMessage(ctx, pool, concurrent, 64*1024)
			if err == nil && (receipt.Disposition != "ACCEPTED" || receipt.MessageID != concurrent.MessageID) {
				err = fmt.Errorf("unexpected concurrent receipt %#v", receipt)
			}
			concurrentErrors <- err
		}()
	}
	wait.Wait()
	close(concurrentErrors)
	for err := range concurrentErrors {
		if err != nil {
			t.Fatal(err)
		}
	}

	const emptyJournalDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if err := CommitAgentResyncCheckpoint(ctx, pool, hostID, 1, emptyJournalDigest, map[string]any{"queued": 0}); !errors.Is(err, ErrAgentResyncSessionStale) {
		t.Fatalf("stale checkpoint error = %v", err)
	}
	if err := CommitAgentResyncCheckpoint(ctx, pool, hostID, 2, emptyJournalDigest, map[string]any{"queued": 0}); err != nil {
		t.Fatal(err)
	}
	var receiptCount, checkpointGeneration int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_message_receipts WHERE host_id = $1`, hostID).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT session_generation FROM kim.agent_resync_checkpoints WHERE host_id = $1`, hostID).Scan(&checkpointGeneration); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 3 || checkpointGeneration != 2 {
		t.Fatalf("receipt count/checkpoint generation = %d/%d", receiptCount, checkpointGeneration)
	}
}
