package recoveryauthority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

const (
	SourceHost      = "kvm-base-g01-n001-p.core.s01.si1230.com"
	DestinationHost = "kvm-base-g02-n001-p.core.s01.si1230.com"
)

// HelperRequest is written only to the remote helper's stdin. LeaseToken is
// deliberately absent from argv, environment, stdout, and persisted evidence.
type HelperRequest struct {
	ExpectedHostname string          `json:"expected_hostname"`
	HostID           string          `json:"host_id"`
	CommandID        string          `json:"command_id"`
	CommandType      string          `json:"command_type"`
	SchemaVersion    string          `json:"schema_version"`
	TargetResourceID string          `json:"target_resource_id"`
	Payload          json.RawMessage `json:"payload"`
	PayloadDigest    string          `json:"payload_digest"`
	VGName           string          `json:"vg_name"`
	VGUUID           string          `json:"vg_uuid"`
	CacheRoot        string          `json:"cache_root"`
	StateRoot        string          `json:"state_root"`
	AttemptIndex     int             `json:"attempt_index"`
	AuthorityGen     int64           `json:"host_authority_generation"`
	SessionGen       int64           `json:"session_generation"`
	LeaseGeneration  int64           `json:"lease_generation"`
	LeaseToken       string          `json:"lease_token"`
}

type HelperResponse struct {
	HostID      string               `json:"host_id"`
	Hostname    string               `json:"hostname"`
	CommandID   string               `json:"command_id"`
	CommandType string               `json:"command_type"`
	Result      ResultEvidence       `json:"result"`
	Observation contract.Observation `json:"observation"`
}

type RemoteConfig struct {
	Host, HelperPath, VGName, VGUUID, CacheRoot, StateRoot string
}

type AcceptedExecution struct {
	CommandID, ResultID, VerificationID string
	LeaseGeneration                     int64
	AttemptIndex                        int
	Receipt                             session.Receipt
	Observation                         contract.Observation
}

// Execute is the single opt-in qualification entry point. It reads the
// immutable typed Command first, grants the random Lease in PostgreSQL, sends
// that exact capability to the helper, and accepts capability-free evidence
// through the ordinary Agent Result transaction.
func Execute(ctx context.Context, db postgres.TxBeginner, config RemoteConfig, commandID, messageID, verificationID string, duration time.Duration) (AcceptedExecution, error) {
	if os.Getenv("KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E") != "1" {
		return AcceptedExecution{}, errors.New("explicit real Recovery authority E2E opt-in is required")
	}
	if db == nil || commandID == "" || messageID == "" || verificationID == "" || duration <= 0 {
		return AcceptedExecution{}, errors.New("complete real Recovery execution request is required")
	}
	candidate, err := postgres.LoadCommandDispatchCandidate(ctx, db, commandID)
	if err != nil {
		return AcceptedExecution{}, err
	}
	grant, err := postgres.AcquireCommandLease(ctx, db, postgres.CommandLeaseRequest{CommandID: commandID,
		HostAuthorityGeneration: candidate.HostAuthorityGeneration, Duration: duration})
	if err != nil {
		return AcceptedExecution{}, err
	}
	response, err := InvokeRemote(ctx, config, grant, candidate)
	if err != nil {
		// A helper/transport error is deliberately not converted into proof that
		// the side effect did not happen. Lease expiry/UNKNOWN/read-back owns it.
		return AcceptedExecution{}, err
	}
	receipt, err := AcceptResponse(ctx, db, grant, candidate, response, messageID, verificationID)
	if err != nil {
		return AcceptedExecution{}, err
	}
	return AcceptedExecution{CommandID: commandID, ResultID: response.Result.ResultID,
		VerificationID: verificationID, LeaseGeneration: grant.LeaseGeneration,
		AttemptIndex: grant.AttemptIndex, Receipt: receipt, Observation: response.Observation}, nil
}

// InvokeRemote sends the bearer capability only over SSH stdin to a fixed,
// explicitly allow-listed Host and fixed helper executable.
func InvokeRemote(ctx context.Context, config RemoteConfig, grant postgres.CommandLeaseGrant, candidate postgres.CommandDispatchCandidate) (HelperResponse, error) {
	if config.Host != SourceHost && config.Host != DestinationHost {
		return HelperResponse{}, errors.New("real Recovery Host is not allow-listed")
	}
	if config.Host != grant.HostID || candidate.HostID != grant.HostID || candidate.CommandID != grant.CommandID ||
		candidate.HostAuthorityGeneration != grant.HostAuthorityGeneration || candidate.SessionGeneration != grant.SessionGeneration {
		return HelperResponse{}, errors.New("remote invocation does not match exact Control-Plane Lease")
	}
	if config.HelperPath == "" || !strings.HasPrefix(config.HelperPath, "/var/tmp/kim-real-recovery-") ||
		config.VGName == "" || !strings.HasPrefix(config.VGName, "kimrr_") || config.VGUUID == "" ||
		!strings.HasPrefix(config.CacheRoot, "/var/tmp/kim-real-recovery-") || !strings.HasPrefix(config.StateRoot, "/var/tmp/kim-real-recovery-") {
		return HelperResponse{}, errors.New("dedicated real Recovery artifacts are required")
	}
	request := HelperRequest{ExpectedHostname: config.Host, HostID: grant.HostID, CommandID: grant.CommandID,
		CommandType: candidate.CommandType, SchemaVersion: candidate.SchemaVersion,
		TargetResourceID: candidate.TargetResourceID, Payload: candidate.Payload, PayloadDigest: candidate.PayloadDigest,
		VGName: config.VGName, VGUUID: config.VGUUID, CacheRoot: config.CacheRoot, StateRoot: config.StateRoot,
		AttemptIndex: grant.AttemptIndex, AuthorityGen: grant.HostAuthorityGeneration,
		SessionGen: grant.SessionGeneration, LeaseGeneration: grant.LeaseGeneration, LeaseToken: grant.Token}
	encoded, err := json.Marshal(request)
	if err != nil {
		return HelperResponse{}, err
	}
	command := exec.CommandContext(ctx, "ssh", config.Host, "sudo", "-n", "env", "KIM_REAL_KVM_RECOVERY_QUALIFICATION=1", config.HelperPath)
	command.Stdin = bytes.NewReader(encoded)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return HelperResponse{}, fmt.Errorf("remote typed Recovery helper failed: %w: %s", err, bounded(stderr.String(), 2048))
	}
	var response HelperResponse
	decoder := json.NewDecoder(io.LimitReader(&stdout, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return HelperResponse{}, fmt.Errorf("decode capability-free helper response: %w", err)
	}
	verification := postgres.CommandVerificationCandidate{CommandID: candidate.CommandID, HostID: candidate.HostID,
		SessionGeneration: candidate.SessionGeneration, AttemptIndex: grant.AttemptIndex, CommandType: candidate.CommandType,
		SchemaVersion: candidate.SchemaVersion, TargetResourceID: candidate.TargetResourceID,
		Payload: candidate.Payload, PayloadDigest: candidate.PayloadDigest}
	if err := validateResponse(response, grant, verification); err != nil {
		return HelperResponse{}, err
	}
	return response, nil
}

// AcceptResponse uses the normal Agent Result transaction, including journal,
// Result, Verification, durable Receipt, and Job convergence.
func AcceptResponse(ctx context.Context, db postgres.TxBeginner, grant postgres.CommandLeaseGrant, candidate postgres.CommandDispatchCandidate, response HelperResponse, messageID, verificationID string) (session.Receipt, error) {
	verificationCandidate := postgres.CommandVerificationCandidate{CommandID: candidate.CommandID, HostID: candidate.HostID,
		SessionGeneration: candidate.SessionGeneration, AttemptIndex: grant.AttemptIndex, CommandType: candidate.CommandType,
		SchemaVersion: candidate.SchemaVersion, TargetResourceID: candidate.TargetResourceID,
		Payload: candidate.Payload, PayloadDigest: candidate.PayloadDigest}
	if err := validateResponse(response, grant, verificationCandidate); err != nil {
		return session.Receipt{}, err
	}
	result, err := response.Result.BindAuthority(grant, verificationCandidate)
	if err != nil {
		return session.Receipt{}, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return session.Receipt{}, err
	}
	envelope := session.NewEnvelope(grant.HostID, uint64(grant.SessionGeneration), session.StreamResult,
		messageID, contract.CommandResultSchema, grant.CommandID, uint64(grant.AttemptIndex), payload)
	envelope.CorrelationKey = grant.CommandID
	return postgres.AcceptAgentCommandResult(ctx, db, envelope, 1<<20, postgres.AgentCommandResultDecision{
		Start: postgres.CommandAttemptStart{CommandID: grant.CommandID, AttemptIndex: grant.AttemptIndex,
			LeaseToken: grant.Token, JournalEvidenceDigest: result.JournalDigest},
		Result: postgres.CommandResultSubmission{CommandID: grant.CommandID, AttemptIndex: grant.AttemptIndex,
			LeaseToken: grant.Token, ResultID: result.ResultID, Outcome: result.Outcome, Payload: result.Result},
		Verification: &postgres.CommandVerification{VerificationID: verificationID, CommandID: grant.CommandID,
			AttemptIndex: grant.AttemptIndex, ObservationGeneration: response.Observation.Generation,
			ObservationDigest: response.Observation.Digest, State: response.Observation.State,
			VerifierArtifactDigest: result.VerifierDigest,
			Evidence:               map[string]any{"journal_digest": result.JournalDigest, "read_back": response.Observation.Evidence}},
	})
}

func validateResponse(response HelperResponse, grant postgres.CommandLeaseGrant, candidate postgres.CommandVerificationCandidate) error {
	if response.HostID != grant.HostID || response.Hostname != grant.HostID || response.CommandID != grant.CommandID ||
		response.CommandType != candidate.CommandType || response.Observation.State != response.Result.Observation.State ||
		response.Observation.Digest != response.Result.Observation.Digest || response.Observation.Generation != response.Result.Observation.Generation {
		return errors.New("helper response identity conflicts with exact execution authority")
	}
	_, err := response.Result.BindAuthority(grant, candidate)
	return err
}

func bounded(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
