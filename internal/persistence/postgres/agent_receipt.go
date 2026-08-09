package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

var ErrAgentMessageEvidenceConflict = errors.New("Agent message receipt evidence conflict")

// AcceptAgentMessage atomically creates or reads a durable idempotent receipt.
// An existing identical receipt wins even when replay arrives on a later
// session generation. A new stale-generation message cannot become ACCEPTED.
func AcceptAgentMessage(ctx context.Context, db TxBeginner, envelope session.Envelope, maxMessageBytes int) (receipt session.Receipt, returnedErr error) {
	if err := envelope.Validate(maxMessageBytes); err != nil {
		return session.Receipt{}, err
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var err error
		receipt, err = acceptAgentMessageTx(ctx, tx, envelope)
		return err
	})
	return receipt, err
}

func acceptAgentMessageTx(ctx context.Context, tx pgx.Tx, envelope session.Envelope) (session.Receipt, error) {
	existing, found, err := readAgentReceiptTx(ctx, tx, envelope.HostIdentity, envelope.MessageID)
	if err != nil {
		return session.Receipt{}, err
	}
	if found {
		if err := existing.ValidateFor(envelope); err != nil {
			return session.Receipt{}, ErrAgentMessageEvidenceConflict
		}
		return existing, nil
	}

	var currentGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT session_generation FROM kim.agent_transport_sessions_current
		WHERE host_id = $1 AND state = 'CURRENT' FOR SHARE
	`, envelope.HostIdentity).Scan(&currentGeneration); err != nil {
		return session.Receipt{}, fmt.Errorf("read current Agent session for message receipt: %w", err)
	}
	disposition := "ACCEPTED"
	if uint64(currentGeneration) != envelope.SessionGeneration {
		disposition = "STALE"
	}
	return recordAgentReceiptTx(ctx, tx, envelope, currentGeneration, disposition)
}

func recordAgentReceiptTx(ctx context.Context, tx pgx.Tx, envelope session.Envelope, currentGeneration int64, disposition string) (session.Receipt, error) {
	if disposition != "ACCEPTED" && disposition != "STALE" {
		return session.Receipt{}, errors.New("unsupported Agent receipt disposition")
	}
	payload, err := json.Marshal(map[string]any{"accepted_session_generation": currentGeneration, "transport_session_generation": envelope.SessionGeneration})
	if err != nil {
		return session.Receipt{}, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.agent_message_receipts (
			host_id, session_generation, logical_stream, message_id, sequence_scope,
			sequence_number, payload_digest, disposition, receipt_payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT DO NOTHING
	`, envelope.HostIdentity, currentGeneration, string(envelope.Stream), envelope.MessageID,
		envelope.SequenceScope, envelope.Sequence, envelope.PayloadDigest, disposition, payload)
	if err != nil {
		return session.Receipt{}, fmt.Errorf("commit Agent message receipt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// A concurrent identical delivery may have committed after our initial
		// lookup. Read and validate its durable evidence instead of surfacing a
		// uniqueness error. A sequence/message collision remains fail closed.
		concurrent, found, err := readAgentReceiptTx(ctx, tx, envelope.HostIdentity, envelope.MessageID)
		if err != nil {
			return session.Receipt{}, err
		}
		if !found || concurrent.ValidateFor(envelope) != nil {
			return session.Receipt{}, ErrAgentMessageEvidenceConflict
		}
		return concurrent, nil
	}
	return session.Receipt{
		HostIdentity: envelope.HostIdentity, AcceptedSessionGeneration: uint64(currentGeneration), Stream: envelope.Stream,
		MessageID: envelope.MessageID, SequenceScope: envelope.SequenceScope, Sequence: envelope.Sequence,
		PayloadDigest: envelope.PayloadDigest, Disposition: disposition,
	}, nil
}

func readAgentReceiptTx(ctx context.Context, tx pgx.Tx, hostID, messageID string) (session.Receipt, bool, error) {
	var receipt session.Receipt
	var stream string
	var acceptedGeneration int64
	err := tx.QueryRow(ctx, `
		SELECT host_id, session_generation, logical_stream, message_id, sequence_scope,
		       sequence_number, payload_digest, disposition
		FROM kim.agent_message_receipts WHERE host_id = $1 AND message_id = $2
	`, hostID, messageID).Scan(
		&receipt.HostIdentity, &acceptedGeneration, &stream, &receipt.MessageID,
		&receipt.SequenceScope, &receipt.Sequence, &receipt.PayloadDigest, &receipt.Disposition,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Receipt{}, false, nil
	}
	if err != nil {
		return session.Receipt{}, false, fmt.Errorf("read Agent message receipt: %w", err)
	}
	receipt.Stream = session.Stream(stream)
	receipt.AcceptedSessionGeneration = uint64(acceptedGeneration)
	return receipt, true, nil
}
