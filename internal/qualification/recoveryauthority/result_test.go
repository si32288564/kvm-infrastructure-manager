package recoveryauthority

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestResultEvidenceRequiresExactLeaseIdentity(t *testing.T) {
	original := contract.CommandResult{SchemaVersion: contract.CommandResultSchema, CommandID: "command-1", AttemptIndex: 2, LeaseToken: "secret-capability", JournalDigest: strings.Repeat("a", 64), ResultID: "result-1", Outcome: "SUCCEEDED", Result: map[string]any{"state": "APPLIED"}, Observation: contract.Observation{State: "MATCHED", Digest: strings.Repeat("b", 64), Generation: 1}, VerifierDigest: strings.Repeat("c", 64)}
	identity := ExecutionIdentity{LeaseGeneration: 3, HostID: "host-1", HostAuthorityGeneration: 4,
		SessionGeneration: 5, CommandType: "TYPE", CommandSchemaVersion: "type/v1",
		TargetResourceID: "target-1", CommandPayloadDigest: strings.Repeat("d", 64)}
	evidence := Redact(original, identity)
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), original.LeaseToken) || strings.Contains(string(encoded), "lease_token") {
		t.Fatalf("capability-free evidence exposed the Lease token: %s", encoded)
	}
	grant := postgres.CommandLeaseGrant{CommandID: original.CommandID, AttemptIndex: original.AttemptIndex,
		LeaseGeneration: 3, HostID: "host-1", HostAuthorityGeneration: 4, SessionGeneration: 5, Token: original.LeaseToken}
	candidate := postgres.CommandVerificationCandidate{CommandID: original.CommandID, HostID: "host-1", AttemptIndex: original.AttemptIndex,
		SessionGeneration: 5, CommandType: "TYPE", SchemaVersion: "type/v1", TargetResourceID: "target-1",
		PayloadDigest: strings.Repeat("d", 64)}
	bound, err := evidence.BindAuthority(grant, candidate)
	if err != nil || bound.LeaseToken != original.LeaseToken || bound.CommandID != original.CommandID || bound.AttemptIndex != original.AttemptIndex {
		t.Fatalf("bound result=%+v err=%v", bound, err)
	}
	grant.AttemptIndex++
	if _, err := evidence.BindAuthority(grant, candidate); err == nil {
		t.Fatal("mismatched Attempt was accepted")
	}
	for label, mutate := range map[string]func(*ResultEvidence){
		"lease generation": func(value *ResultEvidence) { value.LeaseGeneration++ },
		"host":             func(value *ResultEvidence) { value.HostID = "other" },
		"session":          func(value *ResultEvidence) { value.SessionGeneration++ },
		"command type":     func(value *ResultEvidence) { value.CommandType = "OTHER" },
		"schema":           func(value *ResultEvidence) { value.CommandSchemaVersion = "other/v1" },
		"target":           func(value *ResultEvidence) { value.TargetResourceID = "other" },
		"payload digest":   func(value *ResultEvidence) { value.CommandPayloadDigest = strings.Repeat("e", 64) },
	} {
		t.Run(label, func(t *testing.T) {
			changed := evidence
			mutate(&changed)
			grant.AttemptIndex = original.AttemptIndex
			if _, err := changed.BindAuthority(grant, candidate); err == nil {
				t.Fatal("mismatched authority was accepted")
			}
		})
	}
}

func TestRealAuthorityExecuteRequiresExplicitOptIn(t *testing.T) {
	previous, present := os.LookupEnv("KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E")
	if err := os.Unsetenv("KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv("KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E", previous)
		} else {
			_ = os.Unsetenv("KIM_REAL_KVM_RECOVERY_AUTHORITY_E2E")
		}
	})
	if _, err := Execute(context.Background(), nil, RemoteConfig{}, "command", "message", "verification", time.Minute); err == nil || !strings.Contains(err.Error(), "opt-in") {
		t.Fatalf("missing opt-in was not fenced: %v", err)
	}
}

func TestHelperResponseRequiresExactOuterIdentityAndObservation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	grant := postgres.CommandLeaseGrant{CommandID: "command", HostID: SourceHost, LeaseGeneration: 1,
		AttemptIndex: 1, HostAuthorityGeneration: 1, SessionGeneration: 1, Token: "secret"}
	candidate := postgres.CommandVerificationCandidate{CommandID: "command", HostID: SourceHost,
		SessionGeneration: 1, AttemptIndex: 1, CommandType: "TYPE", SchemaVersion: "type/v1",
		TargetResourceID: "target", PayloadDigest: digest}
	observation := contract.Observation{State: "MATCHED", Digest: digest, Generation: 1}
	result := contract.CommandResult{SchemaVersion: contract.CommandResultSchema, CommandID: "command", AttemptIndex: 1,
		JournalDigest: digest, ResultID: "result", Outcome: "SUCCEEDED", Observation: observation, VerifierDigest: digest}
	response := HelperResponse{HostID: SourceHost, Hostname: SourceHost, CommandID: "command", CommandType: "TYPE",
		Observation: observation, Result: Redact(result, ExecutionIdentity{LeaseGeneration: 1, HostID: SourceHost,
			HostAuthorityGeneration: 1, SessionGeneration: 1, CommandType: "TYPE", CommandSchemaVersion: "type/v1",
			TargetResourceID: "target", CommandPayloadDigest: digest})}
	if err := validateResponse(response, grant, candidate); err != nil {
		t.Fatal(err)
	}
	for label, mutate := range map[string]func(*HelperResponse){
		"outer host":         func(value *HelperResponse) { value.HostID = DestinationHost },
		"hostname":           func(value *HelperResponse) { value.Hostname = DestinationHost },
		"outer command":      func(value *HelperResponse) { value.CommandID = "other" },
		"outer command type": func(value *HelperResponse) { value.CommandType = "OTHER" },
		"observation digest": func(value *HelperResponse) { value.Observation.Digest = strings.Repeat("b", 64) },
	} {
		t.Run(label, func(t *testing.T) {
			changed := response
			mutate(&changed)
			if err := validateResponse(changed, grant, candidate); err == nil {
				t.Fatal("conflicting helper response was accepted")
			}
		})
	}
}
