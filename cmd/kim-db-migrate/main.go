package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func main() {
	var databaseURL string
	flag.StringVar(&databaseURL, "database-url", "", "PostgreSQL connection URL; defaults to KIM_DATABASE_URL")
	flag.Parse()

	if databaseURL == "" {
		databaseURL = os.Getenv("KIM_DATABASE_URL")
	}
	if databaseURL == "" {
		log.Fatal("database URL is required through -database-url or KIM_DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		log.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()

	result, err := postgres.Migrate(ctx, pool)
	if err != nil {
		log.Fatalf("migrate PostgreSQL: %v", err)
	}
	fmt.Printf("KIM PostgreSQL schema is current at version %d (%d applied)\n", result.CurrentVersion, result.Applied)
}
