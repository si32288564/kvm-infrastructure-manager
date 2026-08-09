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
