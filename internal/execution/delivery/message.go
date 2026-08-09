// Package delivery moves protected Command Lease intents from the durable
// PostgreSQL Outbox through an internal bus to the current Agent stream.
// The bus is never an authority source.
package delivery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

const (
	Subject       = "kim.internal.agent.command.v1"
	MessageSchema = "kim.internal.agent-command-delivery/v1"
)

// Message is the stable internal bus representation of an Agent envelope.
// Session generation remains evidence to be revalidated by the Gateway.
type Message struct {
	SchemaVersion string           `json:"schema_version"`
	OutboxID      string           `json:"outbox_id"`
	Envelope      session.Envelope `json:"envelope"`
}

func (message Message) Encode(maxBytes int) ([]byte, error) {
	if message.SchemaVersion != MessageSchema || message.OutboxID == "" {
		return nil, errors.New("incomplete internal delivery message")
	}
	if err := message.Envelope.Validate(maxBytes); err != nil {
		return nil, err
	}
	return json.Marshal(message)
}

func Decode(payload []byte, maxBytes int) (Message, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var message Message
	if err := decoder.Decode(&message); err != nil {
		return Message{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Message{}, errors.New("trailing internal delivery values are not allowed")
	}
	if message.SchemaVersion != MessageSchema || message.OutboxID == "" {
		return Message{}, errors.New("unsupported or incomplete internal delivery message")
	}
	if err := message.Envelope.Validate(maxBytes); err != nil {
		return Message{}, err
	}
	return message, nil
}

func Digest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
