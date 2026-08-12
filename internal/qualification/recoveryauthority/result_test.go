package recoveryauthority

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func TestResultEvidenceRequiresExactLeaseIdentity(t *testing.T) {
	original := contract.CommandResult{SchemaVersion: contract.CommandResultSchema, CommandID: "command-1", AttemptIndex: 2, LeaseToken: "secret-capability", JournalDigest: strings.Repeat("a", 64), ResultID: "result-1", Outcome: "SUCCEEDED", Result: map[string]any{"state": "APPLIED"}, Observation: contract.Observation{State: "MATCHED", Digest: strings.Repeat("b", 64), Generation: 1}, VerifierDigest: strings.Repeat("c", 64)}
	evidence := Redact(original)
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), original.LeaseToken) || strings.Contains(string(encoded), "lease_token") {
		t.Fatalf("capability-free evidence exposed the Lease token: %s", encoded)
	}
	grant := postgres.CommandLeaseGrant{CommandID: original.CommandID, AttemptIndex: original.AttemptIndex, Token: original.LeaseToken}
	bound, err := evidence.BindGrant(grant)
	if err != nil || bound.LeaseToken != original.LeaseToken || bound.CommandID != original.CommandID || bound.AttemptIndex != original.AttemptIndex {
		t.Fatalf("bound result=%+v err=%v", bound, err)
	}
	grant.AttemptIndex++
	if _, err := evidence.BindGrant(grant); err == nil {
		t.Fatal("mismatched Attempt was accepted")
	}
}
