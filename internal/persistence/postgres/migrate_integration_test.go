package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/db/migrations"
)

func TestMigratePostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	available, err := migrations.List()
	if err != nil {
		t.Fatal(err)
	}
	first, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if first.CurrentVersion != available[len(available)-1].Version {
		t.Fatalf("first CurrentVersion = %d", first.CurrentVersion)
	}
	second, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("replay Migrate: %v", err)
	}
	if second.Applied != 0 || second.CurrentVersion != first.CurrentVersion {
		t.Fatalf("replay result = %#v, first = %#v", second, first)
	}

	var migrationCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM kim.schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != len(available) {
		t.Fatalf("migration ledger count = %d, want %d", migrationCount, len(available))
	}

	requiredTables := []string{
		"agent_message_receipts",
		"agent_resync_checkpoints",
		"agent_transport_sessions",
		"database_authority",
		"host_identities",
		"inbox_messages",
		"outbox_messages",
	}
	for _, table := range requiredTables {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'kim' AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("required table kim.%s does not exist", table)
		}
	}
}
