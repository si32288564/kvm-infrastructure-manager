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
	// ErrStaleOutboxClaim means the caller no longer owns the current claim generation.
	ErrStaleOutboxClaim = errors.New("stale outbox claim")
	// ErrOutboxEvidenceConflict means an idempotency key was reused with different evidence.
	ErrOutboxEvidenceConflict = errors.New("outbox evidence digest conflict")
)

// OutboxClaimRequest bounds a durable publisher claim.
type OutboxClaimRequest struct {
	Owner string
	Limit int
	Lease time.Duration
}

// OutboxMessage is a message claimed by one publisher generation.
type OutboxMessage struct {
	MessageID       string
	AggregateType   string
	AggregateID     string
	EventType       string
	SchemaVersion   string
	PayloadDigest   string
	Payload         []byte
	ClaimGeneration int64
	ClaimExpiresAt  time.Time
}

// OutboxClaim identifies one durable publisher attempt.
type OutboxClaim struct {
	MessageID       string
	Owner           string
	ClaimGeneration int64
}

// ClaimOutboxTx claims eligible messages using database authority time.
// An expired prior claim is eligible for redelivery but is not evidence that
// its dispatch did not occur. The caller must commit or roll back tx as a unit.
func ClaimOutboxTx(ctx context.Context, tx pgx.Tx, request OutboxClaimRequest) ([]OutboxMessage, error) {
	if request.Owner == "" {
		return nil, errors.New("outbox claim owner is required")
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return nil, errors.New("outbox claim limit must be between 1 and 1000")
	}
	if request.Lease <= 0 {
		return nil, errors.New("outbox claim lease must be positive")
	}

	rows, err := tx.Query(ctx, `
		WITH candidates AS (
			SELECT message_id
			FROM kim.outbox_messages
			WHERE available_at <= statement_timestamp()
			  AND (
				state = 'PENDING'
				OR (state = 'CLAIMED' AND claim_expires_at <= statement_timestamp())
			  )
			ORDER BY available_at, created_at, message_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE kim.outbox_messages AS message
		SET state = 'CLAIMED',
			claim_owner = $2,
			claim_generation = message.last_claim_generation + 1,
			last_claim_generation = message.last_claim_generation + 1,
			claim_expires_at = statement_timestamp() + ($3 * interval '1 microsecond'),
			attempt_count = message.attempt_count + 1
		FROM candidates
		WHERE message.message_id = candidates.message_id
		RETURNING message.message_id, message.aggregate_type, message.aggregate_id,
			message.event_type, message.schema_version, message.payload_digest,
			message.payload, message.claim_generation, message.claim_expires_at
	`, request.Limit, request.Owner, request.Lease.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("claim outbox messages: %w", err)
	}
	defer rows.Close()

	var messages []OutboxMessage
	for rows.Next() {
		var message OutboxMessage
		if err := rows.Scan(
			&message.MessageID,
			&message.AggregateType,
			&message.AggregateID,
			&message.EventType,
			&message.SchemaVersion,
			&message.PayloadDigest,
			&message.Payload,
			&message.ClaimGeneration,
			&message.ClaimExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan outbox claim: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox claims: %w", err)
	}
	rows.Close()

	for _, message := range messages {
		claim := OutboxClaim{MessageID: message.MessageID, Owner: request.Owner, ClaimGeneration: message.ClaimGeneration}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.outbox_delivery_attempts (
				message_id, claim_generation, claim_owner, lease_expires_at
			) VALUES ($1, $2, $3, $4)
		`, claim.MessageID, claim.ClaimGeneration, claim.Owner, message.ClaimExpiresAt); err != nil {
			return nil, fmt.Errorf("record outbox delivery attempt: %w", err)
		}
		if err := appendOutboxDeliveryEventTx(ctx, tx, claim, "CLAIM_GRANTED", map[string]any{
			"claim_owner":      claim.Owner,
			"lease_expires_at": message.ClaimExpiresAt.UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// RecordOutboxDispatchStartedTx appends evidence before an external publish.
func RecordOutboxDispatchStartedTx(ctx context.Context, tx pgx.Tx, claim OutboxClaim, detail map[string]any) error {
	return appendOutboxDeliveryEventTx(ctx, tx, claim, "DISPATCH_STARTED", detail)
}

// RecordOutboxDispatchUnknownTx appends uncertainty evidence and intentionally
// leaves the claim active. Lease expiry may permit redelivery but never rewrites
// this evidence to failure or proves that the external side effect did not occur.
func RecordOutboxDispatchUnknownTx(ctx context.Context, tx pgx.Tx, claim OutboxClaim, detail map[string]any) error {
	return appendOutboxDeliveryEventTx(ctx, tx, claim, "DISPATCH_UNKNOWN", detail)
}

// MarkOutboxDeliveredTx completes only the current owner/generation and appends
// acknowledgement evidence. Any returned error requires transaction rollback.
func MarkOutboxDeliveredTx(ctx context.Context, tx pgx.Tx, claim OutboxClaim, acknowledgement map[string]any) error {
	tag, err := tx.Exec(ctx, `
		UPDATE kim.outbox_messages
		SET state = 'DELIVERED',
			claim_owner = NULL,
			claim_generation = NULL,
			claim_expires_at = NULL,
			delivered_at = statement_timestamp()
		WHERE message_id = $1
		  AND state = 'CLAIMED'
		  AND claim_owner = $2
		  AND claim_generation = $3
	`, claim.MessageID, claim.Owner, claim.ClaimGeneration)
	if err != nil {
		return fmt.Errorf("mark outbox delivered: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleOutboxClaim
	}
	return appendOutboxDeliveryEventTx(ctx, tx, claim, "DELIVERY_ACKNOWLEDGED", acknowledgement)
}

func appendOutboxDeliveryEventTx(ctx context.Context, tx pgx.Tx, claim OutboxClaim, eventType string, payload map[string]any) error {
	if claim.MessageID == "" || claim.Owner == "" || claim.ClaimGeneration < 1 {
		return errors.New("complete outbox claim identity is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode outbox delivery evidence: %w", err)
	}
	digestBytes := sha256.Sum256(encoded)
	digest := hex.EncodeToString(digestBytes[:])

	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.outbox_delivery_events (
			message_id, claim_generation, event_type, event_payload, event_payload_digest
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (message_id, claim_generation, event_type) DO NOTHING
	`, claim.MessageID, claim.ClaimGeneration, eventType, encoded, digest)
	if err != nil {
		return fmt.Errorf("append outbox delivery event %s: %w", eventType, err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	var acceptedDigest string
	if err := tx.QueryRow(ctx, `
		SELECT event_payload_digest
		FROM kim.outbox_delivery_events
		WHERE message_id = $1 AND claim_generation = $2 AND event_type = $3
	`, claim.MessageID, claim.ClaimGeneration, eventType).Scan(&acceptedDigest); err != nil {
		return fmt.Errorf("read existing outbox delivery event %s: %w", eventType, err)
	}
	if acceptedDigest != digest {
		return ErrOutboxEvidenceConflict
	}
	return nil
}
