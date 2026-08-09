// Package workerruntime owns bounded PostgreSQL authority maintenance loops.
package workerruntime

import (
	"context"
	"errors"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

type LeaseMaintainer interface {
	ExpireDue(context.Context, int) (int, error)
}

type PostgresLeaseMaintainer struct{ DB postgres.TxBeginner }

func (maintainer PostgresLeaseMaintainer) ExpireDue(ctx context.Context, limit int) (int, error) {
	return postgres.ExpireDueCommandLeases(ctx, maintainer.DB, limit)
}

type Config struct {
	SweepInterval   time.Duration
	BatchLimit      int
	PublishInterval time.Duration
}

type DeliveryPublisher interface {
	PublishOnce(context.Context) (int, error)
}

// Run expires elapsed Command Leases. It does not infer non-execution and does
// not dispatch through the Gateway's process-local outbound registry.
func Run(ctx context.Context, config Config, maintainer LeaseMaintainer) error {
	return RunWithDelivery(ctx, config, maintainer, nil)
}

// RunWithDelivery owns independent bounded maintenance and Outbox publish
// cadences. JetStream acknowledgement never changes Command authority.
func RunWithDelivery(ctx context.Context, config Config, maintainer LeaseMaintainer, publisher DeliveryPublisher) error {
	if maintainer == nil || config.SweepInterval <= 0 || config.BatchLimit < 1 || config.BatchLimit > 1000 {
		return errors.New("complete bounded Worker runtime configuration is required")
	}
	if publisher != nil && config.PublishInterval <= 0 {
		return errors.New("delivery publisher requires a positive interval")
	}
	if _, err := maintainer.ExpireDue(ctx, config.BatchLimit); err != nil {
		return err
	}
	if publisher != nil {
		if _, err := publisher.PublishOnce(ctx); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(config.SweepInterval)
	defer ticker.Stop()
	var publishC <-chan time.Time
	var publishTicker *time.Ticker
	if publisher != nil {
		publishTicker = time.NewTicker(config.PublishInterval)
		defer publishTicker.Stop()
		publishC = publishTicker.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := maintainer.ExpireDue(ctx, config.BatchLimit); err != nil {
				return err
			}
		case <-publishC:
			if _, err := publisher.PublishOnce(ctx); err != nil {
				return err
			}
		}
	}
}
