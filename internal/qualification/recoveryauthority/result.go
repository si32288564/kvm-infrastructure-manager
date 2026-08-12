package recoveryauthority

import (
	"errors"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

// ResultEvidence is the lab qualification helper's capability-free result.
// The raw Lease token is supplied only by the Control Plane that granted the
// Lease; it is never returned by the remote helper or persisted in an artifact.
type ResultEvidence struct {
	SchemaVersion  string               `json:"schema_version"`
	CommandID      string               `json:"command_id"`
	AttemptIndex   int                  `json:"attempt_index"`
	JournalDigest  string               `json:"journal_digest"`
	ResultID       string               `json:"result_id"`
	Outcome        string               `json:"outcome"`
	Result         map[string]any       `json:"result"`
	Observation    contract.Observation `json:"observation"`
	VerifierDigest string               `json:"verifier_digest"`
}

func Redact(result contract.CommandResult) ResultEvidence {
	return ResultEvidence{
		SchemaVersion: result.SchemaVersion, CommandID: result.CommandID,
		AttemptIndex: result.AttemptIndex, JournalDigest: result.JournalDigest,
		ResultID: result.ResultID, Outcome: result.Outcome, Result: result.Result,
		Observation: result.Observation, VerifierDigest: result.VerifierDigest,
	}
}

// BindGrant reconstructs the ordinary typed Command Result in memory. The
// exact current Lease identity must match the remote evidence before the
// capability is attached for normal PostgreSQL acceptance.
func (e ResultEvidence) BindGrant(grant postgres.CommandLeaseGrant) (contract.CommandResult, error) {
	if e.CommandID == "" || e.CommandID != grant.CommandID || e.AttemptIndex != grant.AttemptIndex || grant.Token == "" {
		return contract.CommandResult{}, errors.New("qualification Result does not match exact Command Lease")
	}
	result := contract.CommandResult{
		SchemaVersion: e.SchemaVersion, CommandID: e.CommandID,
		AttemptIndex: e.AttemptIndex, LeaseToken: grant.Token,
		JournalDigest: e.JournalDigest, ResultID: e.ResultID,
		Outcome: e.Outcome, Result: e.Result, Observation: e.Observation,
		VerifierDigest: e.VerifierDigest,
	}
	return result, nil
}
