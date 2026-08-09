// Package postgres implements PostgreSQL authority primitives for KIM.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates and verifies a bounded PostgreSQL connection pool.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return OpenWithMaxConnections(ctx, databaseURL, 0)
}

// OpenWithMaxConnections creates a pool with an explicit connection bound.
// A zero bound preserves pgxpool's default.
func OpenWithMaxConnections(ctx context.Context, databaseURL string, maxConnections int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}
	if maxConnections < 0 {
		return nil, errors.New("PostgreSQL maximum connections must not be negative")
	}
	if maxConnections > 0 {
		config.MaxConns = maxConnections
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return pool, nil
}
