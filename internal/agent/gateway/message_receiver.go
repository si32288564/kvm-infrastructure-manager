package gateway

import (
	"context"
	"errors"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

// PostgresMessageReceiver commits receipt authority before transport response.
type PostgresMessageReceiver struct {
	DB              postgres.TxBeginner
	MaxMessageBytes int
}

func (receiver PostgresMessageReceiver) Receive(ctx context.Context, envelope session.Envelope) (session.Receipt, error) {
	if receiver.DB == nil || receiver.MaxMessageBytes < 1 {
		return session.Receipt{}, errors.New("Agent message receiver database and positive message limit are required")
	}
	return postgres.AcceptAgentMessage(ctx, receiver.DB, envelope, receiver.MaxMessageBytes)
}

var _ MessageReceiver = PostgresMessageReceiver{}
