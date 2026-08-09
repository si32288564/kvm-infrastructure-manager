// Package wire maps transport-neutral session contracts to the versioned wire schema.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	agentprotocolv1 "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/api/agentprotocol/v1"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"google.golang.org/protobuf/proto"
)

// EnvelopeToProto converts the stable internal contract to v1 wire schema.
func EnvelopeToProto(envelope session.Envelope) *agentprotocolv1.Envelope {
	return &agentprotocolv1.Envelope{
		HostIdentity:       envelope.HostIdentity,
		SessionGeneration:  envelope.SessionGeneration,
		LogicalStream:      string(envelope.Stream),
		MessageId:          envelope.MessageID,
		SchemaVersion:      envelope.SchemaVersion,
		SequenceScope:      envelope.SequenceScope,
		Sequence:           envelope.Sequence,
		ResourceGeneration: envelope.ResourceGeneration,
		PayloadDigest:      envelope.PayloadDigest,
		CorrelationKey:     envelope.CorrelationKey,
		Payload:            append([]byte(nil), envelope.Payload...),
	}
}

// EnvelopeFromProto converts v1 wire schema to the stable internal contract.
func EnvelopeFromProto(envelope *agentprotocolv1.Envelope) (session.Envelope, error) {
	if envelope == nil {
		return session.Envelope{}, errors.New("wire envelope is required")
	}
	converted := session.Envelope{
		HostIdentity:       envelope.GetHostIdentity(),
		SessionGeneration:  envelope.GetSessionGeneration(),
		Stream:             session.Stream(envelope.GetLogicalStream()),
		MessageID:          envelope.GetMessageId(),
		SchemaVersion:      envelope.GetSchemaVersion(),
		SequenceScope:      envelope.GetSequenceScope(),
		Sequence:           envelope.GetSequence(),
		ResourceGeneration: envelope.GetResourceGeneration(),
		PayloadDigest:      envelope.GetPayloadDigest(),
		CorrelationKey:     envelope.GetCorrelationKey(),
		Payload:            append([]byte(nil), envelope.GetPayload()...),
	}
	return converted, nil
}

// ReceiptToProto converts durable Gateway acceptance evidence.
func ReceiptToProto(receipt session.Receipt) *agentprotocolv1.MessageReceipt {
	return &agentprotocolv1.MessageReceipt{
		HostIdentity: receipt.HostIdentity, AcceptedSessionGeneration: receipt.AcceptedSessionGeneration,
		LogicalStream: string(receipt.Stream), MessageId: receipt.MessageID, SequenceScope: receipt.SequenceScope,
		Sequence: receipt.Sequence, PayloadDigest: receipt.PayloadDigest, Disposition: receipt.Disposition,
	}
}

// ReceiptFromProto converts durable Gateway acceptance evidence.
func ReceiptFromProto(receipt *agentprotocolv1.MessageReceipt) (session.Receipt, error) {
	if receipt == nil {
		return session.Receipt{}, errors.New("wire message receipt is required")
	}
	return session.Receipt{
		HostIdentity: receipt.GetHostIdentity(), AcceptedSessionGeneration: receipt.GetAcceptedSessionGeneration(),
		Stream: session.Stream(receipt.GetLogicalStream()), MessageID: receipt.GetMessageId(), SequenceScope: receipt.GetSequenceScope(),
		Sequence: receipt.GetSequence(), PayloadDigest: receipt.GetPayloadDigest(), Disposition: receipt.GetDisposition(),
	}, nil
}

// HelloToProto converts the transport-neutral handshake.
func HelloToProto(handshake session.Handshake) *agentprotocolv1.SessionHello {
	return &agentprotocolv1.SessionHello{
		HostIdentity:              handshake.HostIdentity,
		SessionGeneration:         handshake.SessionGeneration,
		ProtocolVersion:           handshake.ProtocolVersion,
		Capabilities:              append([]string(nil), handshake.Capabilities...),
		SessionAttemptId:          handshake.SessionAttemptID,
		ConnectionInstanceId:      handshake.ConnectionInstanceID,
		AgentArtifactDigest:       handshake.AgentArtifactDigest,
		CredentialBindingRevision: handshake.CredentialBindingRevision,
	}
}

// ValidateSessionDecision requires an explicit Gateway decision before a live
// transport becomes a usable current Agent session.
func ValidateSessionDecision(frame *agentprotocolv1.Frame, handshake session.Handshake) error {
	if frame == nil {
		return errors.New("Gateway session decision is required")
	}
	if rejected := frame.GetRejected(); rejected != nil {
		return &session.AdmissionRejectedError{
			Code: rejected.GetCode(), RetryAfter: time.Duration(rejected.GetRetryAfterMillis()) * time.Millisecond,
			Retryable: rejected.GetRetryable(),
		}
	}
	accepted := frame.GetAccepted()
	if accepted == nil {
		return errors.New("Gateway first response must be a session decision")
	}
	if accepted.GetHostIdentity() != handshake.HostIdentity || accepted.GetSessionGeneration() != handshake.SessionGeneration {
		return errors.New("Gateway session grant identity/generation mismatch")
	}
	if handshake.SessionAttemptID != "" && accepted.GetSessionAttemptId() != handshake.SessionAttemptID {
		return errors.New("Gateway session grant Attempt mismatch")
	}
	return nil
}

// WriteFrame writes one bounded length-prefixed protobuf Frame.
func WriteFrame(writer io.Writer, frame *agentprotocolv1.Frame, maxBytes int) error {
	if frame == nil || maxBytes < 1 {
		return errors.New("wire frame and positive maximum are required")
	}
	encoded, err := proto.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal wire frame: %w", err)
	}
	if len(encoded) > maxBytes {
		return fmt.Errorf("wire frame is %d bytes, limit is %d", len(encoded), maxBytes)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(encoded)))
	if _, err := writer.Write(header[:]); err != nil {
		return fmt.Errorf("write wire frame length: %w", err)
	}
	if _, err := writer.Write(encoded); err != nil {
		return fmt.Errorf("write wire frame body: %w", err)
	}
	return nil
}

// ReadFrame reads one bounded length-prefixed protobuf Frame.
func ReadFrame(reader io.Reader, maxBytes int) (*agentprotocolv1.Frame, error) {
	if maxBytes < 1 {
		return nil, errors.New("positive wire frame maximum is required")
	}
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || uint64(length) > uint64(maxBytes) {
		return nil, fmt.Errorf("wire frame length %d exceeds limit %d", length, maxBytes)
	}
	body := make([]byte, int(length))
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("read wire frame body: %w", err)
	}
	frame := new(agentprotocolv1.Frame)
	if err := proto.Unmarshal(body, frame); err != nil {
		return nil, fmt.Errorf("unmarshal wire frame: %w", err)
	}
	return frame, nil
}
