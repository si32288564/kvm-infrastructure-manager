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
	SchemaVersion           string               `json:"schema_version"`
	CommandID               string               `json:"command_id"`
	AttemptIndex            int                  `json:"attempt_index"`
	LeaseGeneration         int64                `json:"lease_generation"`
	HostID                  string               `json:"host_id"`
	HostAuthorityGeneration int64                `json:"host_authority_generation"`
	SessionGeneration       int64                `json:"session_generation"`
	CommandType             string               `json:"command_type"`
	CommandSchemaVersion    string               `json:"command_schema_version"`
	TargetResourceID        string               `json:"target_resource_id"`
	CommandPayloadDigest    string               `json:"command_payload_digest"`
	JournalDigest           string               `json:"journal_digest"`
	ResultID                string               `json:"result_id"`
	Outcome                 string               `json:"outcome"`
	Result                  map[string]any       `json:"result"`
	Observation             contract.Observation `json:"observation"`
	VerifierDigest          string               `json:"verifier_digest"`
}

// ExecutionIdentity is capability-free metadata copied from the exact
// Control-Plane grant and immutable Command. It lets the receiver reject a
// plausible Result produced for a different Lease, session, or typed target
// before reattaching the bearer capability held only in memory.
type ExecutionIdentity struct {
	LeaseGeneration         int64
	HostID                  string
	HostAuthorityGeneration int64
	SessionGeneration       int64
	CommandType             string
	CommandSchemaVersion    string
	TargetResourceID        string
	CommandPayloadDigest    string
}

func Redact(result contract.CommandResult, identity ...ExecutionIdentity) ResultEvidence {
	var execution ExecutionIdentity
	if len(identity) == 1 {
		execution = identity[0]
	}
	return ResultEvidence{
		SchemaVersion: result.SchemaVersion, CommandID: result.CommandID,
		AttemptIndex: result.AttemptIndex, JournalDigest: result.JournalDigest,
		ResultID: result.ResultID, Outcome: result.Outcome, Result: result.Result,
		Observation: result.Observation, VerifierDigest: result.VerifierDigest,
		LeaseGeneration: execution.LeaseGeneration, HostID: execution.HostID,
		HostAuthorityGeneration: execution.HostAuthorityGeneration,
		SessionGeneration:       execution.SessionGeneration, CommandType: execution.CommandType,
		CommandSchemaVersion: execution.CommandSchemaVersion,
		TargetResourceID:     execution.TargetResourceID,
		CommandPayloadDigest: execution.CommandPayloadDigest,
	}
}

// BindGrant reconstructs the ordinary typed Command Result in memory. The
// exact current Lease identity must match the remote evidence before the
// capability is attached for normal PostgreSQL acceptance.
func (e ResultEvidence) BindGrant(grant postgres.CommandLeaseGrant) (contract.CommandResult, error) {
	if e.CommandID == "" || e.CommandID != grant.CommandID || e.AttemptIndex != grant.AttemptIndex || grant.Token == "" ||
		(e.LeaseGeneration != 0 && e.LeaseGeneration != grant.LeaseGeneration) ||
		(e.HostID != "" && e.HostID != grant.HostID) ||
		(e.HostAuthorityGeneration != 0 && e.HostAuthorityGeneration != grant.HostAuthorityGeneration) ||
		(e.SessionGeneration != 0 && e.SessionGeneration != grant.SessionGeneration) {
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

// BindAuthority requires the complete capability-free helper identity to
// match both the exact Lease grant and immutable typed Command.
func (e ResultEvidence) BindAuthority(grant postgres.CommandLeaseGrant, candidate postgres.CommandVerificationCandidate) (contract.CommandResult, error) {
	if e.LeaseGeneration < 1 || e.HostID == "" || e.HostAuthorityGeneration < 1 || e.SessionGeneration < 1 ||
		e.CommandType == "" || e.CommandSchemaVersion == "" || e.TargetResourceID == "" || len(e.CommandPayloadDigest) != 64 ||
		candidate.CommandID != grant.CommandID || candidate.HostID != grant.HostID || candidate.AttemptIndex != grant.AttemptIndex ||
		candidate.SessionGeneration != grant.SessionGeneration || e.CommandType != candidate.CommandType ||
		e.CommandSchemaVersion != candidate.SchemaVersion || e.TargetResourceID != candidate.TargetResourceID ||
		e.CommandPayloadDigest != candidate.PayloadDigest {
		return contract.CommandResult{}, errors.New("qualification Result does not match exact typed Command authority")
	}
	return e.BindGrant(grant)
}
