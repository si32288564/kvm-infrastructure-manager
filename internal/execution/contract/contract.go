package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	CommandLeaseSchema            = "kim.execution.command-lease/v1"
	CommandResultSchema           = "kim.execution.command-result/v1"
	VerificationRequestSchema     = "kim.execution.verification-request/v1"
	VerificationObservationSchema = "kim.execution.verification-observation/v1"
)

type VerificationRequest struct {
	SchemaVersion        string          `json:"schema_version"`
	CommandID            string          `json:"command_id"`
	AttemptIndex         int             `json:"attempt_index"`
	HostID               string          `json:"host_id"`
	SessionGeneration    int64           `json:"session_generation"`
	CommandType          string          `json:"command_type"`
	CommandSchemaVersion string          `json:"command_schema_version"`
	TargetResourceID     string          `json:"target_resource_id"`
	CommandPayload       json.RawMessage `json:"command_payload"`
	CommandPayloadDigest string          `json:"command_payload_digest"`
}

type VerificationObservation struct {
	SchemaVersion        string      `json:"schema_version"`
	CommandID            string      `json:"command_id"`
	AttemptIndex         int         `json:"attempt_index"`
	TargetResourceID     string      `json:"target_resource_id"`
	CommandPayloadDigest string      `json:"command_payload_digest"`
	Observation          Observation `json:"observation"`
	VerifierDigest       string      `json:"verifier_digest"`
	JournalDigest        string      `json:"journal_digest"`
}

type CommandLease struct {
	SchemaVersion           string          `json:"schema_version"`
	CommandID               string          `json:"command_id"`
	LeaseGeneration         int64           `json:"lease_generation"`
	AttemptIndex            int             `json:"attempt_index"`
	HostID                  string          `json:"host_id"`
	HostAuthorityGeneration int64           `json:"host_authority_generation"`
	SessionGeneration       int64           `json:"session_generation"`
	LeaseToken              string          `json:"lease_token"`
	CommandType             string          `json:"command_type"`
	CommandSchemaVersion    string          `json:"command_schema_version"`
	TargetResourceID        string          `json:"target_resource_id"`
	CommandPayload          json.RawMessage `json:"command_payload"`
	CommandPayloadDigest    string          `json:"command_payload_digest"`
	ExecutionTimeoutMillis  int64           `json:"execution_timeout_millis"`
}

type Observation struct {
	State      string         `json:"state"`
	Digest     string         `json:"digest"`
	Generation int64          `json:"generation"`
	Evidence   map[string]any `json:"evidence"`
}

type CommandResult struct {
	SchemaVersion  string         `json:"schema_version"`
	CommandID      string         `json:"command_id"`
	AttemptIndex   int            `json:"attempt_index"`
	LeaseToken     string         `json:"lease_token"`
	JournalDigest  string         `json:"journal_digest"`
	ResultID       string         `json:"result_id"`
	Outcome        string         `json:"outcome"`
	Result         map[string]any `json:"result"`
	Observation    Observation    `json:"observation"`
	VerifierDigest string         `json:"verifier_digest"`
}

func DecodeCommandLease(payload []byte) (CommandLease, error) {
	var lease CommandLease
	if err := decodeStrict(payload, &lease); err != nil {
		return CommandLease{}, err
	}
	if lease.SchemaVersion != CommandLeaseSchema || lease.CommandID == "" || lease.LeaseGeneration < 1 || lease.AttemptIndex < 1 || lease.HostID == "" || lease.HostAuthorityGeneration < 1 || lease.SessionGeneration < 1 || lease.LeaseToken == "" || lease.CommandType == "" || lease.CommandSchemaVersion == "" || lease.TargetResourceID == "" || len(lease.CommandPayload) == 0 || len(lease.CommandPayloadDigest) != 64 || lease.ExecutionTimeoutMillis < 1 || lease.ExecutionTimeoutMillis > 3_600_000 {
		return CommandLease{}, errors.New("incomplete typed Command Lease")
	}
	return lease, nil
}

func DecodeCommandResult(payload []byte) (CommandResult, error) {
	var result CommandResult
	if err := decodeStrict(payload, &result); err != nil {
		return CommandResult{}, err
	}
	if result.SchemaVersion != CommandResultSchema || result.CommandID == "" || result.AttemptIndex < 1 || result.LeaseToken == "" || len(result.JournalDigest) != 64 || result.ResultID == "" || len(result.Observation.Digest) != 64 || result.Observation.Generation < 1 || len(result.VerifierDigest) != 64 {
		return CommandResult{}, errors.New("incomplete typed Command Result")
	}
	if result.Outcome != "SUCCEEDED" && result.Outcome != "FAILED" && result.Outcome != "UNKNOWN" {
		return CommandResult{}, fmt.Errorf("unsupported Command outcome %q", result.Outcome)
	}
	return result, nil
}

func DecodeVerificationRequest(payload []byte) (VerificationRequest, error) {
	var request VerificationRequest
	if err := decodeStrict(payload, &request); err != nil {
		return VerificationRequest{}, err
	}
	if request.SchemaVersion != VerificationRequestSchema || request.CommandID == "" || request.AttemptIndex < 1 || request.HostID == "" || request.SessionGeneration < 1 || request.CommandType == "" || request.CommandSchemaVersion == "" || request.TargetResourceID == "" || len(request.CommandPayload) == 0 || len(request.CommandPayloadDigest) != 64 {
		return VerificationRequest{}, errors.New("incomplete typed Verification Request")
	}
	return request, nil
}

func DecodeVerificationObservation(payload []byte) (VerificationObservation, error) {
	var observation VerificationObservation
	if err := decodeStrict(payload, &observation); err != nil {
		return VerificationObservation{}, err
	}
	if observation.SchemaVersion != VerificationObservationSchema || observation.CommandID == "" || observation.AttemptIndex < 1 || observation.TargetResourceID == "" || len(observation.CommandPayloadDigest) != 64 || observation.Observation.Generation < 1 || len(observation.Observation.Digest) != 64 || len(observation.VerifierDigest) != 64 || len(observation.JournalDigest) != 64 {
		return VerificationObservation{}, errors.New("incomplete typed Verification Observation")
	}
	return observation, nil
}

func decodeStrict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON values are not allowed")
	}
	return nil
}
