package delivery

import (
	"context"
	"errors"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/gateway"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type ConsumeDisposition string

const (
	ConsumeAck  ConsumeDisposition = "ACK"
	ConsumeNak  ConsumeDisposition = "NAK"
	ConsumeTerm ConsumeDisposition = "TERM"
)

type GatewayHandler struct {
	DB              postgres.TxBeginner
	Registry        *gateway.OutboundRegistry
	Consumer        string
	MaxMessageBytes int
}

// Handle persists Inbox evidence and revalidates PostgreSQL authority before
// exposing a message to the process-local live session registry.
func (handler GatewayHandler) Handle(ctx context.Context, busMessageID string, payload []byte) (ConsumeDisposition, error) {
	if handler.DB == nil || handler.Registry == nil || handler.Consumer == "" || busMessageID == "" || handler.MaxMessageBytes < 1 {
		return ConsumeNak, errors.New("complete Gateway delivery handler configuration is required")
	}
	message, err := Decode(payload, handler.MaxMessageBytes)
	if err != nil || message.OutboxID != busMessageID {
		return ConsumeTerm, ErrMalformedBusMessage
	}
	decision, err := postgres.AcceptInternalCommandDelivery(ctx, handler.DB, handler.Consumer, busMessageID, Digest(payload), message.Envelope, handler.MaxMessageBytes)
	if errors.Is(err, postgres.ErrInternalDeliveryConflict) {
		return ConsumeTerm, err
	}
	if err != nil {
		return ConsumeNak, err
	}
	if decision.State == "REJECTED" {
		return ConsumeAck, nil
	}
	attempt, err := postgres.StartGatewayCommandRoute(ctx, handler.DB, handler.Consumer, busMessageID, decision.Lease.HostID, decision.Lease.SessionGeneration)
	if err != nil {
		return ConsumeNak, err
	}
	if err := handler.Registry.Send(ctx, decision.Lease.HostID, uint64(decision.Lease.SessionGeneration), message.Envelope); err != nil {
		recordErr := postgres.RecordGatewayCommandRoute(ctx, handler.DB, handler.Consumer, busMessageID, "ROUTE_UNKNOWN", decision.Lease.HostID, decision.Lease.SessionGeneration, attempt, map[string]any{"reason": "outbound_registry_send_failed"})
		return ConsumeNak, errors.Join(err, recordErr)
	}
	if err := postgres.RecordGatewayCommandRoute(ctx, handler.DB, handler.Consumer, busMessageID, "ROUTE_ACCEPTED", decision.Lease.HostID, decision.Lease.SessionGeneration, attempt, map[string]any{"boundary": "gateway_live_stream_write"}); err != nil {
		return ConsumeNak, err
	}
	return ConsumeAck, nil
}

var ErrMalformedBusMessage = errors.New("malformed internal bus message")
