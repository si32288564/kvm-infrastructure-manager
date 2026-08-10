package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-network-worker", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	owner := set.String("worker-id", os.Getenv("KIM_OVN_WORKER_ID"), "stable OVN runtime worker identity")
	adapterDigest := set.String("adapter-artifact-digest", os.Getenv("KIM_OVN_ADAPTER_ARTIFACT_DIGEST"), "SHA-256 digest of the qualified OVN adapter artifact")
	nbDatabase := set.String("ovn-nb-db", os.Getenv("KIM_OVN_NB_DB"), "OVN NB unix: or ssl: endpoint")
	sbDatabase := set.String("ovn-sb-db", os.Getenv("KIM_OVN_SB_DB"), "OVN SB unix: or ssl: endpoint")
	nbctl := set.String("ovn-nbctl", "/usr/bin/ovn-nbctl", "absolute standard ovn-nbctl path")
	sbctl := set.String("ovn-sbctl", "/usr/bin/ovn-sbctl", "absolute standard ovn-sbctl path")
	privateKey := set.String("ovn-private-key", os.Getenv("KIM_OVN_PRIVATE_KEY"), "OVN SSL private key path")
	certificate := set.String("ovn-certificate", os.Getenv("KIM_OVN_CERTIFICATE"), "OVN SSL certificate path")
	caCert := set.String("ovn-ca-cert", os.Getenv("KIM_OVN_CA_CERT"), "OVN SSL CA certificate path")
	pollInterval := set.Duration("poll-interval", 250*time.Millisecond, "bounded work polling interval")
	batchLimit := set.Int("batch-limit", 16, "maximum work claims per poll")
	databaseMaxConnections := set.Int("database-max-connections", 32, "bounded PostgreSQL pool size; must be at least twice batch-limit")
	claimLease := set.Duration("claim-lease", 30*time.Second, "database work claim lease")
	claimMaximumLifetime := set.Duration("claim-maximum-lifetime", 2*time.Minute, "maximum lifetime of one claim generation")
	claimRenewInterval := set.Duration("claim-renew-interval", 10*time.Second, "renewal interval during a long-running typed adapter operation; zero disables renewal")
	commandTimeout := set.Duration("command-timeout", 15*time.Second, "bounded OVN CLI timeout")
	drainTimeout := set.Duration("drain-timeout", 2*time.Minute, "maximum graceful drain duration before hard cancellation")
	metricsListenAddress := set.String("metrics-listen-address", "", "optional Prometheus metrics listen address")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-network-worker %s\n", componentmain.Version)
		return 0
	}
	if *databaseURL == "" || *owner == "" || *adapterDigest == "" || *nbDatabase == "" || *sbDatabase == "" {
		fmt.Fprintln(stderr, "kim-network-worker configuration error: database, worker identity, adapter digest, and NB/SB endpoints are required")
		return 2
	}
	if *databaseMaxConnections < 1 || *databaseMaxConnections > 1024 || *batchLimit < 1 || *batchLimit > 100 || *databaseMaxConnections < 2*(*batchLimit) {
		fmt.Fprintln(stderr, "kim-network-worker configuration error: database-max-connections must be bounded and at least twice batch-limit")
		return 2
	}
	if *drainTimeout < *claimMaximumLifetime {
		fmt.Fprintln(stderr, "kim-network-worker configuration error: drain-timeout must be at least claim-maximum-lifetime")
		return 2
	}
	ctx, hardCancel := context.WithCancel(context.Background())
	defer hardCancel()
	pool, err := postgres.OpenWithMaxConnections(ctx, *databaseURL, int32(*databaseMaxConnections))
	if err != nil {
		fmt.Fprintf(stderr, "kim-network-worker PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
	metrics := ovnruntime.NewMetrics()
	metricsServer, metricsListener, err := startMetricsServer(*metricsListenAddress, metrics, pool)
	if err != nil {
		fmt.Fprintf(stderr, "kim-network-worker metrics error: %v\n", err)
		return 1
	}
	if metricsServer != nil {
		defer func() {
			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metricsServer.Shutdown(shutdownContext)
			_ = metricsListener.Close()
		}()
	}
	drain := make(chan struct{})
	var hardDrain atomic.Bool
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			close(drain)
		case <-ctx.Done():
			return
		}
		timer := time.NewTimer(*drainTimeout)
		defer timer.Stop()
		select {
		case <-signals:
			hardDrain.Store(true)
			hardCancel()
		case <-timer.C:
			hardDrain.Store(true)
			hardCancel()
		case <-ctx.Done():
		}
	}()
	worker := ovnruntime.Worker{
		Store: ovnruntime.PostgresWorkStore{DB: pool},
		Adapter: ovnadapter.Runtime{Config: ovnadapter.RuntimeConfig{
			NBDatabase: *nbDatabase, SBDatabase: *sbDatabase, NBCTL: *nbctl, SBCTL: *sbctl,
			PrivateKeyPath: *privateKey, CertificatePath: *certificate, CACertPath: *caCert,
			CommandTimeout: *commandTimeout,
		}},
		Owner: *owner, BatchLimit: *batchLimit, ClaimLease: *claimLease,
		ClaimMaximumLifetime: *claimMaximumLifetime, ClaimRenewInterval: *claimRenewInterval,
		AdapterArtifactDigest: *adapterDigest,
		Metrics:               metrics,
		ErrorHandler: func(err error) {
			fmt.Fprintf(stderr, "kim-network-worker reconcile error; retrying: %v\n", err)
		},
	}
	if err := worker.RunWithDrain(ctx, drain, *pollInterval); err != nil {
		fmt.Fprintf(stderr, "kim-network-worker stopped: %v\n", err)
		return 1
	}
	if hardDrain.Load() {
		fmt.Fprintln(stderr, "kim-network-worker hard drain interrupted current work; outcome remains unknown until read-back")
		return 1
	}
	return 0
}

func startMetricsServer(address string, metrics *ovnruntime.Metrics, pool *pgxpool.Pool) (*http.Server, net.Listener, error) {
	if address == "" {
		return nil, nil, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{Handler: newMetricsHandler(metrics, pool), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return server, listener, nil
}
