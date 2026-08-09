package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TxBeginner is implemented by pgxpool.Pool.
type TxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// SerializableOptions bounds whole-transaction retry behavior.
type SerializableOptions struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

// RunSerializable executes fn in a serializable transaction and retries only
// PostgreSQL serialization/deadlock failures. A retry never retains partial claims.
func RunSerializable(ctx context.Context, db TxBeginner, options SerializableOptions, fn func(context.Context, pgx.Tx) error) error {
	if options.MaxAttempts < 1 {
		return errors.New("serializable transaction MaxAttempts must be positive")
	}
	if options.BaseDelay < 0 {
		return errors.New("serializable transaction BaseDelay must not be negative")
	}

	var lastErr error
	for attempt := 1; attempt <= options.MaxAttempts; attempt++ {
		err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
			return fn(ctx, tx)
		})
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableTransactionError(err) || attempt == options.MaxAttempts {
			break
		}
		delay := options.BaseDelay * time.Duration(attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
	return fmt.Errorf("serializable transaction failed after at most %d attempts: %w", options.MaxAttempts, lastErr)
}

func isRetryableTransactionError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40001" || postgresError.Code == "40P01"
}
