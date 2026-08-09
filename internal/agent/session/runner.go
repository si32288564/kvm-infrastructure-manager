package session

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/errgroup"
)

type ReceiptHandler interface {
	HandleReceipt(context.Context, Receipt) error
}

type Runner struct {
	Manager        *Manager
	ReceiptHandler ReceiptHandler
	FlushInterval  time.Duration
}

// Run drives inbound routing, outbound multiplexing, and durable receipt
// handling for one already-open current Agent session. Any loop failure tears
// down transport ownership but does not change resource authority.
func (runner Runner) Run(ctx context.Context) error {
	if runner.Manager == nil || runner.FlushInterval <= 0 {
		return errors.New("Agent session runner requires Manager and positive flush interval")
	}
	runner.Manager.mu.Lock()
	connection := runner.Manager.connection
	runner.Manager.mu.Unlock()
	if connection == nil {
		return ErrSessionNotOpen
	}
	receiptConnection, receiptCapable := connection.(ReceiptConnection)
	if receiptCapable && runner.ReceiptHandler == nil {
		return errors.New("receipt-capable transport requires durable receipt handler")
	}
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		for {
			if err := runner.Manager.ReceiveAndRoute(groupContext); err != nil {
				return err
			}
		}
	})
	group.Go(func() error {
		ticker := time.NewTicker(runner.FlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-groupContext.Done():
				return context.Cause(groupContext)
			case <-ticker.C:
				for {
					sent, err := runner.Manager.FlushOne(groupContext)
					if err != nil {
						return err
					}
					if !sent {
						break
					}
				}
			}
		}
	})
	if receiptCapable {
		group.Go(func() error {
			for {
				receipt, err := receiptConnection.ReceiveReceipt(groupContext)
				if err != nil {
					return err
				}
				if err := runner.ReceiptHandler.HandleReceipt(groupContext, receipt); err != nil {
					return err
				}
			}
		})
	}
	err := group.Wait()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}
