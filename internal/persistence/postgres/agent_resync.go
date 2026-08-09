package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrAgentResyncSessionStale = errors.New("Agent resync session is not current")

// CommitAgentResyncCheckpoint records a verified journal convergence point.
// It is accepted only for the Host's current session generation and never
// infers backend state from transport liveness or an empty local queue alone.
func CommitAgentResyncCheckpoint(ctx context.Context, db TxBeginner, hostID string, sessionGeneration uint64, journalDigest string, checkpoint map[string]any) error {
	if hostID == "" || sessionGeneration == 0 || len(journalDigest) != 64 {
		return errors.New("complete Agent resync checkpoint identity is required")
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("encode Agent resync checkpoint: %w", err)
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var currentGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT session_generation FROM kim.agent_transport_sessions_current
			WHERE host_id = $1 AND state = 'CURRENT' FOR SHARE
		`, hostID).Scan(&currentGeneration); err != nil {
			return fmt.Errorf("read current Agent session for resync: %w", err)
		}
		if uint64(currentGeneration) != sessionGeneration {
			return ErrAgentResyncSessionStale
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.agent_resync_checkpoints (
				host_id, session_generation, journal_digest, checkpoint_payload
			) VALUES ($1,$2,$3,$4)
			ON CONFLICT (host_id) DO UPDATE SET
				session_generation = EXCLUDED.session_generation,
				journal_digest = EXCLUDED.journal_digest,
				checkpoint_payload = EXCLUDED.checkpoint_payload,
				verified_at = statement_timestamp()
		`, hostID, currentGeneration, journalDigest, payload)
		if err != nil {
			return fmt.Errorf("commit Agent resync checkpoint: %w", err)
		}
		return nil
	})
}
