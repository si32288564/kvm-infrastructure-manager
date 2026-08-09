package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrDatabaseAuthorityNotActive = errors.New("database authority is not active")
	ErrHostNotApproved            = errors.New("Host is not approved for Agent session admission")
	ErrSessionAttemptConflict     = errors.New("Agent session attempt evidence conflict")
)

type AgentSessionAdmission struct {
	SessionAttemptID          string
	HostID                    string
	ConnectionInstanceID      string
	TransportProfile          string
	ProtocolVersion           string
	AgentArtifactDigest       string
	CredentialBindingRevision int64
	HandshakeEvidence         map[string]any
}

type AgentSessionGrant struct {
	HostID            string
	SessionAttemptID  string
	SessionGeneration int64
	DatabaseTime      time.Time
}

// AdmitAgentSession records immutable attempt evidence and atomically advances
// the Host's single current transport session authority.
func AdmitAgentSession(ctx context.Context, db TxBeginner, request AgentSessionAdmission) (grant AgentSessionGrant, returnedErr error) {
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var err error
		grant, err = AdmitAgentSessionTx(ctx, tx, request)
		return err
	})
	return grant, err
}

func AdmitAgentSessionTx(ctx context.Context, tx pgx.Tx, request AgentSessionAdmission) (AgentSessionGrant, error) {
	if request.SessionAttemptID == "" || request.HostID == "" || request.ConnectionInstanceID == "" || request.TransportProfile == "" || request.ProtocolVersion == "" || request.CredentialBindingRevision < 1 {
		return AgentSessionGrant{}, errors.New("complete Agent session admission identity is required")
	}
	if len(request.AgentArtifactDigest) != 64 {
		return AgentSessionGrant{}, errors.New("Agent artifact digest must contain 64 hexadecimal characters")
	}
	evidence, err := json.Marshal(request.HandshakeEvidence)
	if err != nil {
		return AgentSessionGrant{}, fmt.Errorf("encode Agent handshake evidence: %w", err)
	}
	evidenceDigest := digestBytes(evidence)

	var authorityMode string
	if err := tx.QueryRow(ctx, `SELECT mode FROM kim.database_authority WHERE singleton FOR SHARE`).Scan(&authorityMode); err != nil {
		return AgentSessionGrant{}, fmt.Errorf("read database authority: %w", err)
	}
	if authorityMode != "ACTIVE" {
		return AgentSessionGrant{}, ErrDatabaseAuthorityNotActive
	}
	var enrollmentState string
	if err := tx.QueryRow(ctx, `SELECT enrollment_state FROM kim.host_identities WHERE host_id = $1 FOR SHARE`, request.HostID).Scan(&enrollmentState); err != nil {
		return AgentSessionGrant{}, fmt.Errorf("read Host enrollment: %w", err)
	}
	if enrollmentState != "APPROVED" {
		return AgentSessionGrant{}, ErrHostNotApproved
	}
	// The lock also serializes the first session for a Host, where no current
	// row exists yet. Different Hosts retain independent transaction progress.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, request.HostID); err != nil {
		return AgentSessionGrant{}, fmt.Errorf("lock Host Agent session authority: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.agent_transport_session_attempts (
			session_attempt_id, host_id, connection_instance_id, transport_profile,
			protocol_version, agent_artifact_digest, credential_binding_revision,
			handshake_evidence, handshake_evidence_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT DO NOTHING
	`, request.SessionAttemptID, request.HostID, request.ConnectionInstanceID, request.TransportProfile,
		request.ProtocolVersion, request.AgentArtifactDigest, request.CredentialBindingRevision, evidence, evidenceDigest)
	if err != nil {
		return AgentSessionGrant{}, fmt.Errorf("record Agent session attempt: %w", err)
	}
	createdAttempt := tag.RowsAffected() == 1
	if !createdAttempt {
		var acceptedHost, acceptedConnection, acceptedTransport, acceptedProtocol, acceptedArtifact, acceptedDigest string
		var acceptedCredentialRevision int64
		if err := tx.QueryRow(ctx, `
			SELECT host_id, connection_instance_id, transport_profile, protocol_version,
			       agent_artifact_digest, credential_binding_revision, handshake_evidence_digest
			FROM kim.agent_transport_session_attempts WHERE session_attempt_id = $1
		`, request.SessionAttemptID).Scan(
			&acceptedHost, &acceptedConnection, &acceptedTransport, &acceptedProtocol,
			&acceptedArtifact, &acceptedCredentialRevision, &acceptedDigest,
		); err != nil {
			return AgentSessionGrant{}, ErrSessionAttemptConflict
		}
		if acceptedHost != request.HostID || acceptedConnection != request.ConnectionInstanceID ||
			acceptedTransport != request.TransportProfile || acceptedProtocol != request.ProtocolVersion ||
			acceptedArtifact != request.AgentArtifactDigest || acceptedCredentialRevision != request.CredentialBindingRevision ||
			acceptedDigest != evidenceDigest {
			return AgentSessionGrant{}, ErrSessionAttemptConflict
		}
	}

	var oldGeneration int64
	var oldAttemptID *string
	err = tx.QueryRow(ctx, `
		SELECT session_generation, current_session_attempt_id
		FROM kim.agent_transport_sessions_current WHERE host_id = $1 FOR UPDATE
	`, request.HostID).Scan(&oldGeneration, &oldAttemptID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AgentSessionGrant{}, fmt.Errorf("lock current Agent session: %w", err)
	}
	newGeneration := oldGeneration + 1
	if oldAttemptID != nil && *oldAttemptID == request.SessionAttemptID {
		return AgentSessionGrant{HostID: request.HostID, SessionAttemptID: request.SessionAttemptID, SessionGeneration: oldGeneration}, nil
	}
	if !createdAttempt {
		return AgentSessionGrant{}, ErrSessionAttemptConflict
	}
	if oldAttemptID != nil {
		if err := appendSessionEventTx(ctx, tx, *oldAttemptID, "FENCED", oldGeneration, map[string]any{"superseded_by": request.SessionAttemptID}); err != nil {
			return AgentSessionGrant{}, err
		}
	}
	if err := appendSessionEventTx(ctx, tx, request.SessionAttemptID, "OPENED", 0, map[string]any{"connection_instance_id": request.ConnectionInstanceID}); err != nil {
		return AgentSessionGrant{}, err
	}
	if err := appendSessionEventTx(ctx, tx, request.SessionAttemptID, "CURRENT_GRANTED", newGeneration, map[string]any{"session_generation": newGeneration}); err != nil {
		return AgentSessionGrant{}, err
	}
	grant := AgentSessionGrant{}
	if err := tx.QueryRow(ctx, `
		INSERT INTO kim.agent_transport_sessions_current (
			host_id, session_generation, state, protocol_version, agent_artifact_digest,
			credential_binding_revision, current_session_attempt_id
		) VALUES ($1,$2,'CURRENT',$3,$4,$5,$6)
		ON CONFLICT (host_id) DO UPDATE SET
			session_generation = EXCLUDED.session_generation,
			state = 'CURRENT', protocol_version = EXCLUDED.protocol_version,
			agent_artifact_digest = EXCLUDED.agent_artifact_digest,
			credential_binding_revision = EXCLUDED.credential_binding_revision,
			current_session_attempt_id = EXCLUDED.current_session_attempt_id,
			connected_at = statement_timestamp(), updated_at = statement_timestamp()
		RETURNING statement_timestamp()
	`, request.HostID, newGeneration, request.ProtocolVersion, request.AgentArtifactDigest,
		request.CredentialBindingRevision, request.SessionAttemptID).Scan(&grant.DatabaseTime); err != nil {
		return AgentSessionGrant{}, fmt.Errorf("grant current Agent session: %w", err)
	}
	grant.HostID = request.HostID
	grant.SessionAttemptID = request.SessionAttemptID
	grant.SessionGeneration = newGeneration
	return grant, nil
}

func appendSessionEventTx(ctx context.Context, tx pgx.Tx, attemptID, eventType string, generation int64, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var generationValue any
	if generation > 0 {
		generationValue = generation
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.agent_transport_session_events (
			session_attempt_id, event_sequence, event_type, session_generation,
			event_payload, event_payload_digest
		) SELECT $1, COALESCE(max(event_sequence),0)+1, $2, $3, $4, $5
		FROM kim.agent_transport_session_events WHERE session_attempt_id = $1
	`, attemptID, eventType, generationValue, encoded, digestBytes(encoded))
	if err != nil {
		return fmt.Errorf("append Agent session event %s: %w", eventType, err)
	}
	return nil
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
