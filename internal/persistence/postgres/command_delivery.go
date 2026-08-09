package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/security/tokenprotect"
)

var (
	ErrInternalDeliveryConflict = errors.New("internal delivery message evidence conflict")
	ErrInternalDeliveryStale    = errors.New("internal delivery authority is stale")
	ErrDeliveryKeyUnavailable   = errors.New("internal delivery key revision is unavailable")
)

// RecoverCommandDelivery decrypts a protected capability and revalidates the
// exact current Lease and Host authority before an internal bus publish.
func RecoverCommandDelivery(ctx context.Context, db TxBeginner, intent CommandLeaseDeliveryIntent, protector tokenprotect.Protector) (contract.CommandLease, error) {
	if protector == nil || intent.SchemaVersion != "kim.internal.command-lease-delivery/v1" || intent.CommandID == "" || intent.LeaseGeneration < 1 || intent.AttemptIndex < 1 || intent.HostID == "" || intent.HostAuthorityGeneration < 1 || intent.SessionGeneration < 1 || len(intent.TokenDigest) != 64 || intent.ExecutionTimeoutMillis < 1 {
		return contract.CommandLease{}, ErrInternalDeliveryConflict
	}
	plaintext, err := protector.Unprotect(ctx, intent.ProtectedToken, []byte(intent.CommandID))
	if errors.Is(err, tokenprotect.ErrKeyUnavailable) {
		return contract.CommandLease{}, ErrDeliveryKeyUnavailable
	}
	if err != nil || tokenSHA256(string(plaintext)) != intent.TokenDigest {
		return contract.CommandLease{}, ErrInternalDeliveryConflict
	}
	lease := contract.CommandLease{SchemaVersion: contract.CommandLeaseSchema, CommandID: intent.CommandID, LeaseGeneration: intent.LeaseGeneration, AttemptIndex: intent.AttemptIndex, HostID: intent.HostID, HostAuthorityGeneration: intent.HostAuthorityGeneration, SessionGeneration: intent.SessionGeneration, LeaseToken: string(plaintext), ExecutionTimeoutMillis: intent.ExecutionTimeoutMillis}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return loadCurrentCommandDeliveryTx(ctx, tx, &lease)
	})
	return lease, err
}

func loadCurrentCommandDeliveryTx(ctx context.Context, tx pgx.Tx, lease *contract.CommandLease) error {
	if err := lockHostAuthorityTx(ctx, tx, lease.HostID); err != nil {
		return err
	}
	var leaseState, commandState, acceptedTokenDigest string
	if err := tx.QueryRow(ctx, `
		SELECT command.command_type, command.schema_version, command.target_resource_id,
		       command.payload, command.payload_digest, current.command_state,
		       active.token_digest, active.lease_state
		FROM kim.execution_commands command
		JOIN kim.execution_commands_current current USING(command_id)
		JOIN kim.command_leases_current active USING(command_id)
		WHERE command.command_id=$1 AND command.host_id=$2
		  AND active.lease_generation=$3 AND active.attempt_index=$4
		  AND active.host_authority_generation=$5 AND active.session_generation=$6
		  AND active.not_before <= statement_timestamp() AND active.expires_at > statement_timestamp()
	`, lease.CommandID, lease.HostID, lease.LeaseGeneration, lease.AttemptIndex, lease.HostAuthorityGeneration, lease.SessionGeneration).Scan(
		&lease.CommandType, &lease.CommandSchemaVersion, &lease.TargetResourceID,
		&lease.CommandPayload, &lease.CommandPayloadDigest, &commandState,
		&acceptedTokenDigest, &leaseState,
	); err != nil {
		return ErrInternalDeliveryStale
	}
	if leaseState != "ACTIVE" || (commandState != "LEASED" && commandState != "EXECUTING") || acceptedTokenDigest != tokenSHA256(lease.LeaseToken) {
		return ErrInternalDeliveryStale
	}
	authority, err := readHostMutationAuthorityTx(ctx, tx, lease.HostID, lease.HostAuthorityGeneration)
	if err != nil || authority.SessionGeneration != lease.SessionGeneration {
		return ErrInternalDeliveryStale
	}
	return nil
}

type InternalDeliveryInboxDecision struct {
	State     string
	Duplicate bool
	Lease     contract.CommandLease
}

// AcceptInternalCommandDelivery records Inbox acceptance and revalidates DB
// authority. NATS contents and a prior Inbox ACCEPTED row are not authority.
func AcceptInternalCommandDelivery(ctx context.Context, db TxBeginner, consumer, messageID, payloadDigest string, envelope session.Envelope, maxMessageBytes int) (decision InternalDeliveryInboxDecision, returnedErr error) {
	if consumer == "" || messageID == "" || len(payloadDigest) != 64 || envelope.MessageID == "" {
		return decision, ErrInternalDeliveryConflict
	}
	if err := envelope.Validate(maxMessageBytes); err != nil {
		return decision, err
	}
	lease, err := contract.DecodeCommandLease(envelope.Payload)
	if err != nil || lease.HostID != envelope.HostIdentity || lease.SessionGeneration != int64(envelope.SessionGeneration) || lease.CommandID != envelope.CorrelationKey || envelope.Sequence != uint64(lease.AttemptIndex) {
		return decision, ErrInternalDeliveryConflict
	}
	decision.Lease = lease
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var acceptedDigest, acceptedState string
		err := tx.QueryRow(ctx, `SELECT payload_digest, decision_state FROM kim.inbox_messages WHERE consumer=$1 AND message_id=$2`, consumer, messageID).Scan(&acceptedDigest, &acceptedState)
		switch {
		case err == nil && acceptedDigest != payloadDigest:
			if _, insertErr := tx.Exec(ctx, `
				INSERT INTO kim.inbox_message_conflicts (
					consumer,message_id,accepted_payload_digest,conflicting_payload_digest,conflict_reason
				) VALUES ($1,$2,$3,$4,'message_id_digest_conflict') ON CONFLICT DO NOTHING
			`, consumer, messageID, acceptedDigest, payloadDigest); insertErr != nil {
				return insertErr
			}
			returnedErr = ErrInternalDeliveryConflict
			return nil
		case err == nil:
			decision.Duplicate = true
			decision.State = acceptedState
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		}

		validationErr := loadCurrentCommandDeliveryTx(ctx, tx, &lease)
		if validationErr == nil {
			decision.State = "ACCEPTED"
		} else if errors.Is(validationErr, ErrInternalDeliveryStale) {
			decision.State = "REJECTED"
		} else {
			return validationErr
		}
		if !decision.Duplicate {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.inbox_messages (consumer,message_id,payload_digest,decision_state) VALUES ($1,$2,$3,$4)`, consumer, messageID, payloadDigest, decision.State); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return InternalDeliveryInboxDecision{}, fmt.Errorf("accept internal Command delivery: %w", err)
	}
	return decision, returnedErr
}

func RecordGatewayCommandRoute(ctx context.Context, db TxBeginner, consumer, messageID, eventType, hostID string, sessionGeneration int64, routeAttempt int64, detail map[string]any) error {
	if consumer == "" || messageID == "" || hostID == "" || sessionGeneration < 1 || routeAttempt < 1 || (eventType != "ROUTE_STARTED" && eventType != "ROUTE_ACCEPTED" && eventType != "ROUTE_UNKNOWN") {
		return errors.New("complete Gateway route evidence is required")
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.gateway_command_delivery_events (
				consumer,message_id,route_attempt,event_type,host_id,session_generation,event_payload,event_payload_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING
		`, consumer, messageID, routeAttempt, eventType, hostID, sessionGeneration, payload, digestBytes(payload))
		return err
	})
}

func StartGatewayCommandRoute(ctx context.Context, db TxBeginner, consumer, messageID, hostID string, sessionGeneration int64) (int64, error) {
	if consumer == "" || messageID == "" || hostID == "" || sessionGeneration < 1 {
		return 0, errors.New("complete Gateway route identity is required")
	}
	var attempt int64
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, consumer+"\n"+messageID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(route_attempt),0)+1 FROM kim.gateway_command_delivery_events WHERE consumer=$1 AND message_id=$2`, consumer, messageID).Scan(&attempt); err != nil {
			return err
		}
		payload := []byte(`{"boundary":"gateway_route_attempt"}`)
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.gateway_command_delivery_events (
				consumer,message_id,route_attempt,event_type,host_id,session_generation,event_payload,event_payload_digest
			) VALUES ($1,$2,$3,'ROUTE_STARTED',$4,$5,$6,$7)
		`, consumer, messageID, attempt, hostID, sessionGeneration, payload, digestBytes(payload))
		return err
	})
	return attempt, err
}
