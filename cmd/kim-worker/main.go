package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/delivery"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/execution/workerruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/messaging/natsjs"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/security/tokenprotect"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-worker", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	sweepInterval := set.Duration("lease-sweep-interval", time.Second, "expired Command Lease sweep interval")
	batchLimit := set.Int("lease-sweep-batch", 128, "maximum expired Command Leases per sweep")
	natsURL := set.String("nats-url", os.Getenv("KIM_NATS_URL"), "internal NATS URL; empty disables Command delivery publishing")
	natsCredentials := set.String("nats-credentials", os.Getenv("KIM_NATS_CREDENTIALS"), "NATS user credentials file")
	natsCA := set.String("nats-tls-ca", os.Getenv("KIM_NATS_TLS_CA"), "NATS server CA bundle")
	deliveryKeyPath := set.String("delivery-key-file", os.Getenv("KIM_DELIVERY_KEY_FILE"), "32-byte Command delivery AEAD key file")
	deliveryKeyID := set.String("delivery-key-id", os.Getenv("KIM_DELIVERY_KEY_ID"), "Command delivery AEAD key revision")
	publishInterval := set.Duration("outbox-publish-interval", 100*time.Millisecond, "Command delivery Outbox polling interval")
	publishBatch := set.Int("outbox-publish-batch", 64, "maximum Command delivery intents per claim")
	claimLease := set.Duration("outbox-claim-lease", 30*time.Second, "Outbox publisher claim lease")
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
	var publisher workerruntime.DeliveryPublisher
	var natsConnection *nats.Conn
	if *natsURL != "" || *deliveryKeyPath != "" || *deliveryKeyID != "" || *natsCredentials != "" || *natsCA != "" {
		if !strings.HasPrefix(*natsURL, "tls://") || *deliveryKeyPath == "" || *deliveryKeyID == "" || *natsCredentials == "" || *natsCA == "" {
			fmt.Fprintln(stderr, "kim-worker configuration error: tls:// NATS URL, credentials, CA, and delivery key file/ID are all required")
			return 2
		}
		key, err := os.ReadFile(*deliveryKeyPath)
		if err != nil || len(key) != 32 {
			fmt.Fprintln(stderr, "kim-worker configuration error: delivery key file must contain exactly 32 bytes")
			return 2
		}
		natsConnection, err = nats.Connect(*natsURL, nats.Name("kim-worker-command-delivery"), nats.UserCredentials(*natsCredentials), nats.RootCAs(*natsCA), nats.NoEcho(), nats.MaxReconnects(-1))
		if err != nil {
			fmt.Fprintf(stderr, "kim-worker NATS error: %v\n", err)
			return 1
		}
		defer natsConnection.Close()
		js, err := jetstream.New(natsConnection)
		if err != nil {
			fmt.Fprintf(stderr, "kim-worker JetStream error: %v\n", err)
			return 1
		}
		publisher = delivery.OutboxPublisher{DB: pool, Protector: tokenprotect.AESGCM{KeyID: strings.TrimSpace(*deliveryKeyID), Key: key}, Bus: natsjs.Publisher{JetStream: js}, Owner: fmt.Sprintf("kim-worker/%d", os.Getpid()), BatchLimit: *publishBatch, ClaimLease: *claimLease, MaxMessageBytes: 4 << 20}
	}
	if err := workerruntime.RunWithDelivery(ctx, workerruntime.Config{SweepInterval: *sweepInterval, BatchLimit: *batchLimit, PublishInterval: *publishInterval}, workerruntime.PostgresLeaseMaintainer{DB: pool}, publisher); err != nil {
		fmt.Fprintf(stderr, "kim-worker stopped: %v\n", err)
		return 1
	}
	return 0
}
