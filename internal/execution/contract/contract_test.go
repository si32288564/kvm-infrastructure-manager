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

func TestVerificationContractsRoundTripAndRejectUnknownFields(t *testing.T) {
	request := VerificationRequest{SchemaVersion: VerificationRequestSchema, CommandID: "c", AttemptIndex: 1, HostID: "h", SessionGeneration: 2, CommandType: "TYPE", CommandSchemaVersion: "schema/v1", TargetResourceID: "target", CommandPayload: json.RawMessage(`{"value":"x"}`), CommandPayloadDigest: strings.Repeat("a", 64)}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decodedRequest, err := DecodeVerificationRequest(payload)
	if err != nil || decodedRequest.SessionGeneration != 2 {
		t.Fatalf("decoded Verification Request = %#v, error = %v", decodedRequest, err)
	}

	observation := VerificationObservation{SchemaVersion: VerificationObservationSchema, CommandID: "c", AttemptIndex: 1, TargetResourceID: "target", CommandPayloadDigest: strings.Repeat("a", 64), Observation: Observation{State: "MATCHED", Generation: 1, Digest: strings.Repeat("b", 64)}, VerifierDigest: strings.Repeat("c", 64), JournalDigest: strings.Repeat("d", 64)}
	payload, err = json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	decodedObservation, err := DecodeVerificationObservation(payload)
	if err != nil || decodedObservation.Observation.State != "MATCHED" {
		t.Fatalf("decoded Verification Observation = %#v, error = %v", decodedObservation, err)
	}

	withUnknown := append(payload[:len(payload)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodeVerificationObservation(withUnknown); err == nil {
		t.Fatal("unknown Verification Observation field accepted")
	}
}
