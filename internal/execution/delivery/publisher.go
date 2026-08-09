package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/security/tokenprotect"
)

type PublishAcknowledgement struct {
	Stream    string
	Sequence  uint64
	Duplicate bool
}

type BusPublisher interface {
	Publish(context.Context, string, string, []byte) (PublishAcknowledgement, error)
}

type OutboxPublisher struct {
	DB              postgres.TxBeginner
	Protector       tokenprotect.Protector
	Bus             BusPublisher
	Owner           string
	BatchLimit      int
	ClaimLease      time.Duration
	MaxMessageBytes int
}

// PublishOnce publishes a bounded batch. A JetStream PubAck proves only
// durable bus acceptance; it is not Gateway routing, Agent receipt, or backend
// execution evidence.
func (publisher OutboxPublisher) PublishOnce(ctx context.Context) (int, error) {
	if publisher.DB == nil || publisher.Protector == nil || publisher.Bus == nil || publisher.Owner == "" || publisher.BatchLimit < 1 || publisher.BatchLimit > 1000 || publisher.ClaimLease <= 0 || publisher.MaxMessageBytes < 1 {
		return 0, errors.New("complete bounded Command delivery publisher configuration is required")
	}
	var messages []postgres.OutboxMessage
	if err := pgx.BeginTxFunc(ctx, publisher.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var err error
		messages, err = postgres.ClaimOutboxTx(ctx, tx, postgres.OutboxClaimRequest{Owner: publisher.Owner, Limit: publisher.BatchLimit, Lease: publisher.ClaimLease, EventTypes: []string{"COMMAND_LEASE_DELIVERY_REQUESTED"}})
		return err
	}); err != nil {
		return 0, err
	}
	var published int
	var batchErr error
	for _, outbox := range messages {
		accepted, err := publisher.publishClaim(ctx, outbox)
		if err != nil {
			batchErr = errors.Join(batchErr, err)
			continue
		}
		if accepted {
			published++
		}
	}
	return published, batchErr
}

func (publisher OutboxPublisher) publishClaim(ctx context.Context, outbox postgres.OutboxMessage) (bool, error) {
	claim := postgres.OutboxClaim{MessageID: outbox.MessageID, Owner: publisher.Owner, ClaimGeneration: outbox.ClaimGeneration}
	if outbox.SchemaVersion != "kim.internal.command-lease-delivery/v1" {
		return false, publisher.deadLetter(ctx, claim, "outbox_schema_conflict")
	}
	var intent postgres.CommandLeaseDeliveryIntent
	decoder := json.NewDecoder(bytes.NewReader(outbox.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return false, errors.Join(err, publisher.deadLetter(ctx, claim, "invalid_protected_delivery_intent"))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return false, publisher.deadLetter(ctx, claim, "trailing_protected_delivery_intent")
	}
	canonicalIntent, err := json.Marshal(intent)
	if err != nil || Digest(canonicalIntent) != outbox.PayloadDigest {
		return false, publisher.deadLetter(ctx, claim, "outbox_payload_digest_conflict")
	}
	lease, err := postgres.RecoverCommandDelivery(ctx, publisher.DB, intent, publisher.Protector)
	if errors.Is(err, postgres.ErrDeliveryKeyUnavailable) {
		return false, fmt.Errorf("protected Command delivery key revision unavailable: %w", err)
	}
	if errors.Is(err, postgres.ErrInternalDeliveryConflict) {
		return false, publisher.deadLetter(ctx, claim, "protected_capability_conflict")
	}
	if errors.Is(err, postgres.ErrInternalDeliveryStale) {
		return false, publisher.completeWithoutPublish(ctx, claim, "authority_stale_before_bus_publish")
	}
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(lease)
	if err != nil {
		return false, err
	}
	envelope := session.NewEnvelope(lease.HostID, uint64(lease.SessionGeneration), session.StreamCommand, fmt.Sprintf("command-lease/%s/%d", lease.CommandID, lease.LeaseGeneration), contract.CommandLeaseSchema, "command/"+lease.CommandID, uint64(lease.AttemptIndex), payload)
	envelope.CorrelationKey = lease.CommandID
	message := Message{SchemaVersion: MessageSchema, OutboxID: outbox.MessageID, Envelope: envelope}
	busPayload, err := message.Encode(publisher.MaxMessageBytes)
	if err != nil {
		return false, publisher.deadLetter(ctx, claim, "invalid_agent_envelope")
	}
	if err := publisher.recordDispatch(ctx, claim, "DISPATCH_STARTED", map[string]any{"subject": Subject, "bus_message_id": outbox.MessageID, "bus_payload_digest": Digest(busPayload)}); err != nil {
		return false, err
	}
	ack, err := publisher.Bus.Publish(ctx, Subject, outbox.MessageID, busPayload)
	if err != nil {
		recordErr := publisher.recordDispatch(ctx, claim, "DISPATCH_UNKNOWN", map[string]any{"subject": Subject, "reason": "publish_or_puback_failed"})
		return false, errors.Join(fmt.Errorf("publish internal Command delivery with unknown outcome: %w", err), recordErr)
	}
	err = pgx.BeginTxFunc(ctx, publisher.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return postgres.MarkOutboxDeliveredTx(ctx, tx, claim, map[string]any{"boundary": "jetstream_puback", "stream": ack.Stream, "stream_sequence": ack.Sequence, "duplicate": ack.Duplicate})
	})
	return err == nil, err
}

func (publisher OutboxPublisher) recordDispatch(ctx context.Context, claim postgres.OutboxClaim, event string, detail map[string]any) error {
	return pgx.BeginTxFunc(ctx, publisher.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if event == "DISPATCH_STARTED" {
			return postgres.RecordOutboxDispatchStartedTx(ctx, tx, claim, detail)
		}
		return postgres.RecordOutboxDispatchUnknownTx(ctx, tx, claim, detail)
	})
}

func (publisher OutboxPublisher) deadLetter(ctx context.Context, claim postgres.OutboxClaim, reason string) error {
	return pgx.BeginTxFunc(ctx, publisher.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return postgres.MarkOutboxDeadLetterTx(ctx, tx, claim, map[string]any{"reason": reason})
	})
}

func (publisher OutboxPublisher) completeWithoutPublish(ctx context.Context, claim postgres.OutboxClaim, reason string) error {
	return pgx.BeginTxFunc(ctx, publisher.DB, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return postgres.MarkOutboxDeliveredTx(ctx, tx, claim, map[string]any{"boundary": "authority_revalidation", "published": false, "reason": reason})
	})
}
