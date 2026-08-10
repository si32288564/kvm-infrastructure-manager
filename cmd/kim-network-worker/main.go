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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.OpenWithMaxConnections(ctx, *databaseURL, int32(*databaseMaxConnections))
	if err != nil {
		fmt.Fprintf(stderr, "kim-network-worker PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
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
		ErrorHandler: func(err error) {
			fmt.Fprintf(stderr, "kim-network-worker reconcile error; retrying: %v\n", err)
		},
	}
	if err := worker.Run(ctx, *pollInterval); err != nil {
		fmt.Fprintf(stderr, "kim-network-worker stopped: %v\n", err)
		return 1
	}
	return 0
}
