package dispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type Sender interface {
	Send(context.Context, string, uint64, session.Envelope) error
}

type Dispatcher struct {
	DB               postgres.TxBeginner
	Sender           Sender
	LeaseDuration    time.Duration
	ExecutionTimeout time.Duration
}

func (dispatcher Dispatcher) Dispatch(ctx context.Context, commandID string) (postgres.CommandLeaseGrant, error) {
	if dispatcher.DB == nil || dispatcher.Sender == nil || commandID == "" || dispatcher.LeaseDuration <= 0 || dispatcher.ExecutionTimeout <= 0 || dispatcher.ExecutionTimeout >= dispatcher.LeaseDuration {
		return postgres.CommandLeaseGrant{}, errors.New("complete bounded dispatcher configuration is required")
	}
	candidate, err := postgres.LoadCommandDispatchCandidate(ctx, dispatcher.DB, commandID)
	if err != nil {
		return postgres.CommandLeaseGrant{}, err
	}
	grant, err := postgres.AcquireCommandLease(ctx, dispatcher.DB, postgres.CommandLeaseRequest{
		CommandID: commandID, HostAuthorityGeneration: candidate.HostAuthorityGeneration, Duration: dispatcher.LeaseDuration,
	})
	if err != nil {
		return postgres.CommandLeaseGrant{}, err
	}
	lease := contract.CommandLease{
		SchemaVersion: contract.CommandLeaseSchema, CommandID: commandID,
		LeaseGeneration: grant.LeaseGeneration, AttemptIndex: grant.AttemptIndex,
		HostID: grant.HostID, HostAuthorityGeneration: grant.HostAuthorityGeneration,
		SessionGeneration: grant.SessionGeneration, LeaseToken: grant.Token,
		CommandType: candidate.CommandType, CommandSchemaVersion: candidate.SchemaVersion,
		TargetResourceID: candidate.TargetResourceID, CommandPayload: candidate.Payload,
		CommandPayloadDigest: candidate.PayloadDigest, ExecutionTimeoutMillis: dispatcher.ExecutionTimeout.Milliseconds(),
	}
	payload, err := json.Marshal(lease)
	if err != nil {
		return grant, err
	}
	messageID := fmt.Sprintf("command-lease/%s/%d", commandID, grant.LeaseGeneration)
	envelope := session.NewEnvelope(grant.HostID, uint64(grant.SessionGeneration), session.StreamCommand, messageID, contract.CommandLeaseSchema, "command/"+commandID, uint64(grant.AttemptIndex), payload)
	envelope.CorrelationKey = commandID
	if err := dispatcher.Sender.Send(ctx, grant.HostID, uint64(grant.SessionGeneration), envelope); err != nil {
		// A send error cannot prove non-delivery. The durable Lease remains active
		// and will become UNKNOWN unless a valid Result is accepted.
		return grant, fmt.Errorf("dispatch Command Lease with uncertain delivery: %w", err)
	}
	return grant, nil
}

func (dispatcher Dispatcher) DispatchVerification(ctx context.Context, commandID string) error {
	if dispatcher.DB == nil || dispatcher.Sender == nil || commandID == "" {
		return errors.New("verification dispatcher dependencies are required")
	}
	candidate, err := postgres.LoadCommandVerificationCandidate(ctx, dispatcher.DB, commandID)
	if err != nil {
		return err
	}
	request := contract.VerificationRequest{SchemaVersion: contract.VerificationRequestSchema, CommandID: commandID, AttemptIndex: candidate.AttemptIndex, HostID: candidate.HostID, SessionGeneration: candidate.SessionGeneration, CommandType: candidate.CommandType, CommandSchemaVersion: candidate.SchemaVersion, TargetResourceID: candidate.TargetResourceID, CommandPayload: candidate.Payload, CommandPayloadDigest: candidate.PayloadDigest}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	envelope := session.NewEnvelope(candidate.HostID, uint64(candidate.SessionGeneration), session.StreamCommand, fmt.Sprintf("verification-request/%s/%d", commandID, candidate.AttemptIndex), contract.VerificationRequestSchema, "command/"+commandID, uint64(candidate.AttemptIndex), payload)
	envelope.CorrelationKey = commandID
	return dispatcher.Sender.Send(ctx, candidate.HostID, uint64(candidate.SessionGeneration), envelope)
}
