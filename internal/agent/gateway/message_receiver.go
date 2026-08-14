package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

// PostgresMessageReceiver commits receipt authority before transport response.
type PostgresMessageReceiver struct {
	DB              postgres.TxBeginner
	MaxMessageBytes int
}

func (receiver PostgresMessageReceiver) Receive(ctx context.Context, envelope session.Envelope) (session.Receipt, error) {
	if receiver.DB == nil || receiver.MaxMessageBytes < 1 {
		return session.Receipt{}, errors.New("Agent message receiver database and positive message limit are required")
	}
	if envelope.Stream == session.StreamInventory {
		return postgres.AcceptHostInventory(ctx, receiver.DB, envelope, receiver.MaxMessageBytes)
	}
	if envelope.Stream == session.StreamResult {
		if envelope.SchemaVersion != contract.CommandResultSchema {
			return session.Receipt{}, errors.New("unsupported typed Command Result schema")
		}
		result, err := contract.DecodeCommandResult(envelope.Payload)
		if err != nil {
			return session.Receipt{}, err
		}
		if envelope.CorrelationKey != result.CommandID || envelope.Sequence != uint64(result.AttemptIndex) {
			return session.Receipt{}, errors.New("Command Result envelope identity mismatch")
		}
		start := postgres.CommandAttemptStart{
			CommandID: result.CommandID, AttemptIndex: result.AttemptIndex,
			LeaseToken: result.LeaseToken, JournalEvidenceDigest: result.JournalDigest,
		}
		resultDecision := postgres.CommandResultSubmission{
			CommandID: result.CommandID, AttemptIndex: result.AttemptIndex,
			LeaseToken: result.LeaseToken, ResultID: result.ResultID,
			Outcome: result.Outcome, Payload: result.Result,
		}
		var verification *postgres.CommandVerification
		if result.Outcome != "FAILED" {
			verificationState := "UNKNOWN"
			switch result.Observation.State {
			case "MATCHED":
				verificationState = "MATCHED"
			case "NOT_APPLIED":
				verificationState = "NOT_APPLIED"
			case "CONFLICTING":
				verificationState = "CONFLICTING"
			}
			verification = &postgres.CommandVerification{
				VerificationID: fmt.Sprintf("%s-verification-%d", result.CommandID, result.AttemptIndex),
				CommandID:      result.CommandID, AttemptIndex: result.AttemptIndex,
				ObservationGeneration: result.Observation.Generation, ObservationDigest: result.Observation.Digest,
				State: verificationState, VerifierArtifactDigest: result.VerifierDigest,
				Evidence: result.Observation.Evidence,
			}
		}
		receipt, err := postgres.AcceptAgentCommandResult(ctx, receiver.DB, envelope, receiver.MaxMessageBytes, postgres.AgentCommandResultDecision{Start: start, Result: resultDecision, Verification: verification})
		if err != nil || verification == nil {
			return receipt, err
		}
		if err := postgres.ConvergeImageIngestionCommand(ctx, receiver.DB, result.CommandID, verification.VerificationID); err != nil {
			return session.Receipt{}, err
		}
		return receipt, nil
	}
	if envelope.Stream == session.StreamResync && envelope.SchemaVersion == contract.VerificationObservationSchema {
		observation, err := contract.DecodeVerificationObservation(envelope.Payload)
		if err != nil {
			return session.Receipt{}, err
		}
		if envelope.CorrelationKey != observation.CommandID || envelope.Sequence != uint64(observation.AttemptIndex) {
			return session.Receipt{}, errors.New("Verification Observation envelope identity mismatch")
		}
		state := "UNKNOWN"
		switch observation.Observation.State {
		case "MATCHED":
			state = "MATCHED"
		case "NOT_APPLIED":
			state = "NOT_APPLIED"
		case "CONFLICTING":
			state = "CONFLICTING"
		}
		verificationID := fmt.Sprintf("%s-resync-verification-%d", observation.CommandID, observation.AttemptIndex)
		receipt, err := postgres.AcceptAgentVerificationObservation(ctx, receiver.DB, envelope, receiver.MaxMessageBytes, postgres.AgentVerificationObservationDecision{TargetResourceID: observation.TargetResourceID, CommandPayloadDigest: observation.CommandPayloadDigest, Verification: postgres.CommandVerification{VerificationID: verificationID, CommandID: observation.CommandID, AttemptIndex: observation.AttemptIndex, ObservationGeneration: observation.Observation.Generation, ObservationDigest: observation.Observation.Digest, State: state, VerifierArtifactDigest: observation.VerifierDigest, Evidence: map[string]any{"journal_digest": observation.JournalDigest, "read_back": observation.Observation.Evidence}}})
		if err != nil {
			return receipt, err
		}
		if err := postgres.ConvergeImageIngestionCommand(ctx, receiver.DB, observation.CommandID, verificationID); err != nil {
			return session.Receipt{}, err
		}
		return receipt, nil
	}
	return postgres.AcceptAgentMessage(ctx, receiver.DB, envelope, receiver.MaxMessageBytes)
}

var _ MessageReceiver = PostgresMessageReceiver{}
