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
	SweepInterval time.Duration
	BatchLimit    int
}

// Run expires elapsed Command Leases. It does not infer non-execution and does
// not dispatch through the Gateway's process-local outbound registry.
func Run(ctx context.Context, config Config, maintainer LeaseMaintainer) error {
	if maintainer == nil || config.SweepInterval <= 0 || config.BatchLimit < 1 || config.BatchLimit > 1000 {
		return errors.New("complete bounded Worker runtime configuration is required")
	}
	if _, err := maintainer.ExpireDue(ctx, config.BatchLimit); err != nil {
		return err
	}
	ticker := time.NewTicker(config.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := maintainer.ExpireDue(ctx, config.BatchLimit); err != nil {
				return err
			}
		}
	}
}
