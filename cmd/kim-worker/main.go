package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/workerruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-worker", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	sweepInterval := set.Duration("lease-sweep-interval", time.Second, "expired Command Lease sweep interval")
	batchLimit := set.Int("lease-sweep-batch", 128, "maximum expired Command Leases per sweep")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-worker %s\n", componentmain.Version)
		return 0
	}
	if *databaseURL == "" {
		fmt.Fprintln(stderr, "kim-worker configuration error: PostgreSQL URL is required")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.Open(ctx, *databaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "kim-worker PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
	if err := workerruntime.Run(ctx, workerruntime.Config{SweepInterval: *sweepInterval, BatchLimit: *batchLimit}, workerruntime.PostgresLeaseMaintainer{DB: pool}); err != nil {
		fmt.Fprintf(stderr, "kim-worker stopped: %v\n", err)
		return 1
	}
	return 0
}
