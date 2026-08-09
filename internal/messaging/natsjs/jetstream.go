// Package natsjs is the internal NATS JetStream transport adapter. It carries
// durable messages but owns no KIM resource or mutation authority.
package natsjs

import (
	"context"
	"errors"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/delivery"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Publisher struct{ JetStream jetstream.Publisher }

func (publisher Publisher) Publish(ctx context.Context, subject, messageID string, payload []byte) (delivery.PublishAcknowledgement, error) {
	if publisher.JetStream == nil || subject == "" || messageID == "" || len(payload) == 0 {
		return delivery.PublishAcknowledgement{}, errors.New("complete JetStream publish identity is required")
	}
	ack, err := publisher.JetStream.Publish(ctx, subject, payload, jetstream.WithMsgID(messageID))
	if err != nil {
		return delivery.PublishAcknowledgement{}, err
	}
	return delivery.PublishAcknowledgement{Stream: ack.Stream, Sequence: ack.Sequence, Duplicate: ack.Duplicate}, nil
}

type Handler interface {
	Handle(context.Context, string, []byte) (delivery.ConsumeDisposition, error)
}

type Consumer struct {
	Consumer jetstream.Consumer
	Handler  Handler
	PollWait time.Duration
	NakDelay time.Duration
}

func (consumer Consumer) Run(ctx context.Context) error {
	if consumer.Consumer == nil || consumer.Handler == nil || consumer.PollWait <= 0 || consumer.NakDelay <= 0 {
		return errors.New("complete bounded JetStream consumer configuration is required")
	}
	for {
		if context.Cause(ctx) != nil {
			return nil
		}
		message, err := consumer.Consumer.Next(jetstream.FetchMaxWait(consumer.PollWait))
		if context.Cause(ctx) != nil {
			return nil
		}
		if errors.Is(err, jetstream.ErrNoMessages) || errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			continue
		}
		if err != nil {
			if transientConsumerError(err) && waitConsumerRetry(ctx, consumer.PollWait) {
				continue
			}
			return err
		}
		messageID := message.Headers().Get(nats.MsgIdHdr)
		disposition, _ := consumer.Handler.Handle(ctx, messageID, message.Data())
		switch disposition {
		case delivery.ConsumeAck:
			if err := message.DoubleAck(ctx); err != nil {
				if context.Cause(ctx) != nil {
					return nil
				}
				if transientConsumerError(err) && waitConsumerRetry(ctx, consumer.PollWait) {
					continue
				}
				return err
			}
		case delivery.ConsumeTerm:
			if err := message.Term(); err != nil {
				if transientConsumerError(err) && waitConsumerRetry(ctx, consumer.PollWait) {
					continue
				}
				return err
			}
		case delivery.ConsumeNak:
			if err := message.NakWithDelay(consumer.NakDelay); err != nil {
				if transientConsumerError(err) && waitConsumerRetry(ctx, consumer.PollWait) {
					continue
				}
				return err
			}
		default:
			return errors.New("unknown internal delivery disposition")
		}
	}
}

func transientConsumerError(err error) bool {
	return errors.Is(err, jetstream.ErrConsumerLeadershipChanged) ||
		errors.Is(err, jetstream.ErrNoStreamResponse) ||
		errors.Is(err, nats.ErrDisconnected) ||
		errors.Is(err, nats.ErrConnectionReconnecting) ||
		errors.Is(err, nats.ErrNoServers) ||
		errors.Is(err, nats.ErrTimeout)
}

func waitConsumerRetry(ctx context.Context, pollWait time.Duration) bool {
	delay := pollWait
	if delay < 25*time.Millisecond {
		delay = 25 * time.Millisecond
	}
	if delay > 250*time.Millisecond {
		delay = 250 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
