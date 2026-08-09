package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestAgentSessionAdmissionPostgreSQLIntegration(t *testing.T) {
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
		VALUES ('session-registry-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode = 'ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}
	hostID := fmt.Sprintf("session-registry-%d", time.Now().UnixNano())
	fingerprint := digestBytes([]byte("session-registry-certificate"))
	prepareSessionIdentityFixture(t, ctx, pool, hostID, 1, fingerprint)
	first := AgentSessionAdmission{
		SessionAttemptID: hostID + "-attempt-1", HostID: hostID, ConnectionInstanceID: "connection-1",
		TransportProfile: "integration", ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent-1")),
		CredentialBindingRevision: 1, HandshakeEvidence: map[string]any{"wave": 1},
		PeerCertificateFingerprint: fingerprint,
	}
	firstGrant, err := AdmitAgentSession(ctx, pool, first)
	if err != nil || firstGrant.SessionGeneration != 1 {
		t.Fatalf("first grant = %#v, error = %v", firstGrant, err)
	}
	idempotentGrant, err := AdmitAgentSession(ctx, pool, first)
	if err != nil || idempotentGrant.SessionGeneration != 1 {
		t.Fatalf("idempotent grant = %#v, error = %v", idempotentGrant, err)
	}
	conflicting := first
	conflicting.AgentArtifactDigest = digestBytes([]byte("conflicting-agent"))
	if _, err := AdmitAgentSession(ctx, pool, conflicting); !errors.Is(err, ErrSessionAttemptConflict) {
		t.Fatalf("digest-conflicting attempt replay error = %v", err)
	}
	second := first
	second.SessionAttemptID = hostID + "-attempt-2"
	second.ConnectionInstanceID = "connection-2"
	second.CredentialBindingRevision = 2
	prepareSessionIdentityFixture(t, ctx, pool, hostID, 2, fingerprint)
	second.HandshakeEvidence = map[string]any{"wave": 2}
	secondGrant, err := AdmitAgentSession(ctx, pool, second)
	if err != nil || secondGrant.SessionGeneration != 2 {
		t.Fatalf("second grant = %#v, error = %v", secondGrant, err)
	}
	if _, err := AdmitAgentSession(ctx, pool, first); !errors.Is(err, ErrCredentialBindingNotCurrent) {
		t.Fatalf("stale attempt replay error = %v", err)
	}

	var currentAttempt string
	var generation, attemptCount, eventCount int
	if err := pool.QueryRow(ctx, `SELECT session_generation, current_session_attempt_id FROM kim.agent_transport_sessions_current WHERE host_id = $1`, hostID).Scan(&generation, &currentAttempt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_transport_session_attempts WHERE host_id = $1`, hostID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.agent_transport_session_events AS event JOIN kim.agent_transport_session_attempts AS attempt USING (session_attempt_id) WHERE attempt.host_id = $1`, hostID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || currentAttempt != second.SessionAttemptID || attemptCount != 2 || eventCount != 5 {
		t.Fatalf("current generation/attempt/counts = %d/%s/%d/%d", generation, currentAttempt, attemptCount, eventCount)
	}
}
