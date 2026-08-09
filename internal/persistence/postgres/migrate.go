package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/db/migrations"
)

const migrationAdvisoryLock int64 = 0x4b494d4d494752

// Beginner is implemented by pgxpool.Pool and permits transaction-scoped testing.
type Beginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// MigrationResult describes the durable migration outcome.
type MigrationResult struct {
	Applied        int
	CurrentVersion int64
}

// Migrate applies all pending migrations atomically under a PostgreSQL advisory lock.
// An already recorded version with a different name or checksum is rejected.
func Migrate(ctx context.Context, db Beginner) (result MigrationResult, returnedErr error) {
	available, err := migrations.List()
	if err != nil {
		return result, err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("begin KIM migration: %w", err)
	}
	defer func() {
		if returnedErr != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationAdvisoryLock); err != nil {
		return result, fmt.Errorf("lock KIM migration: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS kim;
		CREATE TABLE IF NOT EXISTS kim.schema_migrations (
			version bigint PRIMARY KEY CHECK (version > 0),
			name text NOT NULL,
			checksum char(64) NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT statement_timestamp()
		)
	`); err != nil {
		return result, fmt.Errorf("ensure KIM migration ledger: %w", err)
	}

	for _, migration := range available {
		var name, checksum string
		err := tx.QueryRow(ctx,
			"SELECT name, checksum FROM kim.schema_migrations WHERE version = $1",
			migration.Version,
		).Scan(&name, &checksum)
		switch {
		case err == nil:
			if name != migration.Name || checksum != migration.Checksum {
				return result, fmt.Errorf("migration %d integrity mismatch: database has %s/%s, binary has %s/%s", migration.Version, name, checksum, migration.Name, migration.Checksum)
			}
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, migration.SQL); err != nil {
				return result, fmt.Errorf("apply KIM migration %03d_%s: %w", migration.Version, migration.Name, err)
			}
			if _, err := tx.Exec(ctx,
				"INSERT INTO kim.schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
				migration.Version, migration.Name, migration.Checksum,
			); err != nil {
				return result, fmt.Errorf("record KIM migration %03d_%s: %w", migration.Version, migration.Name, err)
			}
			result.Applied++
		default:
			return result, fmt.Errorf("read KIM migration %d: %w", migration.Version, err)
		}
		result.CurrentVersion = migration.Version
	}

	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit KIM migration: %w", err)
	}
	return result, nil
}
