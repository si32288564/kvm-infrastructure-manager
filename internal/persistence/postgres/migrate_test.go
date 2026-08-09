package postgres

import (
	"context"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/db/migrations"
	"github.com/pashagolub/pgxmock/v5"
)

func TestMigrateAppliesPendingMigrations(t *testing.T) {
	available, err := migrations.List()
	if err != nil {
		t.Fatal(err)
	}
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs(migrationAdvisoryLock).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS kim").
		WillReturnResult(pgxmock.NewResult("CREATE TABLE", 0))
	for _, migration := range available {
		mock.ExpectQuery("SELECT name, checksum FROM kim.schema_migrations").
			WithArgs(migration.Version).
			WillReturnError(pgx.ErrNoRows)
		mock.ExpectExec(regexp.QuoteMeta(migration.SQL)).
			WillReturnResult(pgxmock.NewResult("MIGRATION", 0))
		mock.ExpectExec("INSERT INTO kim.schema_migrations").
			WithArgs(migration.Version, migration.Name, migration.Checksum).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}
	mock.ExpectCommit()

	result, err := Migrate(context.Background(), mock)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if result.Applied != len(available) || result.CurrentVersion != available[len(available)-1].Version {
		t.Fatalf("unexpected result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateAcceptsExactReplay(t *testing.T) {
	available, err := migrations.List()
	if err != nil {
		t.Fatal(err)
	}
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs(migrationAdvisoryLock).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS kim").
		WillReturnResult(pgxmock.NewResult("CREATE TABLE", 0))
	for _, migration := range available {
		mock.ExpectQuery("SELECT name, checksum FROM kim.schema_migrations").
			WithArgs(migration.Version).
			WillReturnRows(pgxmock.NewRows([]string{"name", "checksum"}).AddRow(migration.Name, migration.Checksum))
	}
	mock.ExpectCommit()

	result, err := Migrate(context.Background(), mock)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if result.Applied != 0 || result.CurrentVersion != available[len(available)-1].Version {
		t.Fatalf("unexpected replay result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateRejectsChecksumMismatch(t *testing.T) {
	available, err := migrations.List()
	if err != nil {
		t.Fatal(err)
	}
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs(migrationAdvisoryLock).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS kim").
		WillReturnResult(pgxmock.NewResult("CREATE TABLE", 0))
	mock.ExpectQuery("SELECT name, checksum FROM kim.schema_migrations").
		WithArgs(available[0].Version).
		WillReturnRows(pgxmock.NewRows([]string{"name", "checksum"}).AddRow(available[0].Name, "changed"))
	mock.ExpectRollback()

	if _, err := Migrate(context.Background(), mock); err == nil {
		t.Fatal("Migrate accepted a checksum mismatch")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
