package wire

import (
	"bytes"
	"testing"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

func TestFrameRoundTrip(t *testing.T) {
	envelope := session.NewEnvelope("host-1", 3, session.StreamResult, "result-1", "v1", "attempt-1", 2, []byte("result"))
	frame := &agentprotocolv1.Frame{Body: &agentprotocolv1.Frame_Envelope{Envelope: EnvelopeToProto(envelope)}}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, frame, 1024); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadFrame(&buffer, 1024)
	if err != nil {
		t.Fatal(err)
	}
	converted, err := EnvelopeFromProto(decoded.GetEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	if converted.MessageID != envelope.MessageID || converted.PayloadDigest != envelope.PayloadDigest || !bytes.Equal(converted.Payload, envelope.Payload) {
		t.Fatalf("round trip = %#v, want %#v", converted, envelope)
	}
}

func TestReadFrameRejectsOversizeBeforeAllocation(t *testing.T) {
	buffer := bytes.NewBuffer([]byte{0, 0, 16, 0})
	if _, err := ReadFrame(buffer, 128); err == nil {
		t.Fatal("ReadFrame accepted oversized length")
	}
}
