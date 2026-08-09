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
		return postgres.AcceptAgentCommandResult(ctx, receiver.DB, envelope, receiver.MaxMessageBytes, postgres.AgentCommandResultDecision{Start: start, Result: resultDecision, Verification: verification})
	}
	return postgres.AcceptAgentMessage(ctx, receiver.DB, envelope, receiver.MaxMessageBytes)
}

var _ MessageReceiver = PostgresMessageReceiver{}
