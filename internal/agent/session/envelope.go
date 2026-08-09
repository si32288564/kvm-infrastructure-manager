// Package session defines transport-neutral Agent Session Manager contracts.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Stream identifies an application-level logical stream. It is not a wire
// transport stream identifier and remains stable across transport adapters.
type Stream string

const (
	StreamControl    Stream = "CONTROL"
	StreamCommand    Stream = "COMMAND_LEASE"
	StreamResult     Stream = "RESULT_RECEIPT"
	StreamHeartbeat  Stream = "HEARTBEAT_HEALTH"
	StreamCredential Stream = "CREDENTIAL_LIFECYCLE"
	StreamInventory  Stream = "INVENTORY_OBSERVATION"
	StreamResync     Stream = "RESYNC"
)

var knownStreams = map[Stream]struct{}{
	StreamControl:    {},
	StreamCommand:    {},
	StreamResult:     {},
	StreamHeartbeat:  {},
	StreamCredential: {},
	StreamInventory:  {},
	StreamResync:     {},
}

// Envelope carries one typed logical message independently of HTTP/2/gRPC.
type Envelope struct {
	HostIdentity       string
	SessionGeneration  uint64
	Stream             Stream
	MessageID          string
	SchemaVersion      string
	SequenceScope      string
	Sequence           uint64
	ResourceGeneration uint64
	PayloadDigest      string
	CorrelationKey     string
	Payload            []byte
}

// Receipt is durable Gateway acceptance evidence. A receipt can acknowledge a
// message replayed over a later transport generation without changing the
// original accepted generation.
type Receipt struct {
	HostIdentity              string
	AcceptedSessionGeneration uint64
	Stream                    Stream
	MessageID                 string
	SequenceScope             string
	Sequence                  uint64
	PayloadDigest             string
	Disposition               string
}

// ValidateFor requires a receipt to identify the exact durable message.
func (receipt Receipt) ValidateFor(envelope Envelope) error {
	if receipt.HostIdentity != envelope.HostIdentity || receipt.Stream != envelope.Stream || receipt.MessageID != envelope.MessageID || receipt.SequenceScope != envelope.SequenceScope || receipt.Sequence != envelope.Sequence || receipt.PayloadDigest != envelope.PayloadDigest {
		return errors.New("message receipt does not match durable envelope")
	}
	if receipt.AcceptedSessionGeneration == 0 {
		return errors.New("message receipt accepted generation must be positive")
	}
	switch receipt.Disposition {
	case "ACCEPTED", "STALE", "REJECTED", "QUARANTINED":
		return nil
	default:
		return fmt.Errorf("unknown message receipt disposition %q", receipt.Disposition)
	}
}

// BindSession returns a transport copy for the current session. Message
// identity, sequence, payload, and digest remain stable across reconnect.
func (envelope Envelope) BindSession(generation uint64) Envelope {
	bound := envelope
	bound.SessionGeneration = generation
	bound.Payload = append([]byte(nil), envelope.Payload...)
	return bound
}

// Validate rejects incomplete, oversized, or digest-conflicting envelopes.
func (envelope Envelope) Validate(maxMessageBytes int) error {
	if envelope.HostIdentity == "" || envelope.MessageID == "" || envelope.SchemaVersion == "" || envelope.SequenceScope == "" {
		return errors.New("agent envelope identity, message, schema, and sequence scope are required")
	}
	if envelope.SessionGeneration == 0 {
		return errors.New("agent envelope session generation must be positive")
	}
	if _, ok := knownStreams[envelope.Stream]; !ok {
		return fmt.Errorf("unknown logical stream %q", envelope.Stream)
	}
	if maxMessageBytes < 1 || len(envelope.Payload) > maxMessageBytes {
		return fmt.Errorf("agent envelope payload is %d bytes, limit is %d", len(envelope.Payload), maxMessageBytes)
	}
	digest := sha256.Sum256(envelope.Payload)
	if envelope.PayloadDigest != hex.EncodeToString(digest[:]) {
		return errors.New("agent envelope payload digest mismatch")
	}
	return nil
}

// NewEnvelope calculates the payload digest but does not grant authority.
func NewEnvelope(host string, generation uint64, stream Stream, messageID, schema, sequenceScope string, sequence uint64, payload []byte) Envelope {
	digest := sha256.Sum256(payload)
	return Envelope{
		HostIdentity:      host,
		SessionGeneration: generation,
		Stream:            stream,
		MessageID:         messageID,
		SchemaVersion:     schema,
		SequenceScope:     sequenceScope,
		Sequence:          sequence,
		PayloadDigest:     hex.EncodeToString(digest[:]),
		Payload:           payload,
	}
}
