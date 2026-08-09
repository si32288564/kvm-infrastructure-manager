package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandLeaseRejectsUnknownFields(t *testing.T) {
	payload := `{"schema_version":"` + CommandLeaseSchema + `","command_id":"c","lease_generation":1,"attempt_index":1,"host_id":"h","host_authority_generation":1,"session_generation":1,"lease_token":"token","command_type":"TYPE","command_schema_version":"schema/v1","target_resource_id":"target","command_payload":{"value":"x"},"command_payload_digest":"` + strings.Repeat("a", 64) + `","execution_timeout_millis":1000,"unexpected":true}`
	if _, err := DecodeCommandLease([]byte(payload)); err == nil {
		t.Fatal("unknown Command Lease field accepted")
	}
}

func TestCommandResultRoundTrip(t *testing.T) {
	original := CommandResult{SchemaVersion: CommandResultSchema, CommandID: "c", AttemptIndex: 1, LeaseToken: "token", JournalDigest: strings.Repeat("a", 64), ResultID: "r", Outcome: "SUCCEEDED", Result: map[string]any{"state": "APPLIED"}, Observation: Observation{State: "MATCHED", Digest: strings.Repeat("b", 64), Generation: 1, Evidence: map[string]any{"state": "present"}}, VerifierDigest: strings.Repeat("c", 64)}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCommandResult(payload)
	if err != nil || decoded.CommandID != original.CommandID {
		t.Fatalf("decoded Result = %#v, error = %v", decoded, err)
	}
}
