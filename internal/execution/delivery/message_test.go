package delivery

import (
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

func TestMessageRoundTripPreservesStableEnvelope(t *testing.T) {
	envelope := session.NewEnvelope("host-1", 7, session.StreamCommand, "command-lease/cmd-1/2", "kim.execution.command-lease/v1", "command/cmd-1", 2, []byte(`{"schema_version":"kim.execution.command-lease/v1"}`))
	envelope.CorrelationKey = "cmd-1"
	message := Message{SchemaVersion: MessageSchema, OutboxID: "command-lease-delivery/cmd-1/2", Envelope: envelope}
	payload, err := message.Encode(4096)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(payload, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OutboxID != message.OutboxID || decoded.Envelope.MessageID != envelope.MessageID || decoded.Envelope.PayloadDigest != envelope.PayloadDigest || decoded.Envelope.SessionGeneration != 7 {
		t.Fatalf("decoded message changed stable evidence: %#v", decoded)
	}
	if Digest(payload) == "" {
		t.Fatal("message digest is empty")
	}
}

func TestDecodeRejectsUnknownFieldsAndDigestConflict(t *testing.T) {
	if _, err := Decode([]byte(`{"schema_version":"kim.internal.agent-command-delivery/v1","outbox_id":"x","unknown":true}`), 4096); err == nil {
		t.Fatal("unknown field accepted")
	}
	envelope := session.NewEnvelope("host-1", 1, session.StreamCommand, "message-1", "schema-1", "scope-1", 1, []byte(`{}`))
	envelope.PayloadDigest = "bad"
	if _, err := (Message{SchemaVersion: MessageSchema, OutboxID: "outbox-1", Envelope: envelope}).Encode(4096); err == nil {
		t.Fatal("payload digest conflict accepted")
	}
}
