package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/executionjournal"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/contract"
)

type verificationBackend struct{ observed bool }

func (*verificationBackend) CommandType() string   { return "VERIFY_TEST" }
func (*verificationBackend) SchemaVersion() string { return "kim.command.verify-test/v1" }
func (*verificationBackend) Execute(context.Context, contract.CommandLease) (BackendResult, error) {
	return BackendResult{}, nil
}
func (backend *verificationBackend) Observe(context.Context, contract.VerificationRequest) (contract.Observation, error) {
	backend.observed = true
	return contract.Observation{State: "MATCHED", Generation: 1, Digest: testDigest("matched")}, nil
}

type verificationPublisher struct{ envelope session.Envelope }

func (publisher *verificationPublisher) Publish(envelope session.Envelope) error {
	publisher.envelope = envelope
	return nil
}

func TestMissingJournalVerificationPublishesUnknownWithoutEndingSession(t *testing.T) {
	journal, err := executionjournal.Open(filepath.Join(t.TempDir(), "journal"), "host-1")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	publisher := &verificationPublisher{}
	module, err := NewModule("host-1", journal, publisher, testDigest("verifier"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &verificationBackend{}
	if err := module.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
	commandPayload := json.RawMessage(`{"value":"desired"}`)
	request := contract.VerificationRequest{SchemaVersion: contract.VerificationRequestSchema, CommandID: "command-1", AttemptIndex: 1, HostID: "host-1", SessionGeneration: 2, CommandType: backend.CommandType(), CommandSchemaVersion: backend.SchemaVersion(), TargetResourceID: "resource-1", CommandPayload: commandPayload, CommandPayloadDigest: testDigest(string(commandPayload))}
	payload, _ := json.Marshal(request)
	envelope := session.NewEnvelope("host-1", 2, session.StreamCommand, "verification-request/command-1/1", contract.VerificationRequestSchema, "command/command-1", 1, payload)
	envelope.CorrelationKey = request.CommandID
	if err := module.Handle(t.Context(), envelope); err != nil {
		t.Fatalf("missing journal Verification ended the session: %v", err)
	}
	if backend.observed {
		t.Fatal("backend read-back ran without local write-before-execute evidence")
	}
	var observation contract.VerificationObservation
	if err := json.Unmarshal(publisher.envelope.Payload, &observation); err != nil {
		t.Fatal(err)
	}
	if observation.Observation.State != "UNKNOWN" || observation.Observation.Evidence["journal_state"] != "ABSENT" || len(observation.JournalDigest) != 64 {
		t.Fatalf("missing journal observation = %#v", observation)
	}
}

func testDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
