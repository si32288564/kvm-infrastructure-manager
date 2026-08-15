package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnruntime"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func main() {
	if os.Getenv("KIM_REAL_OVN_WORK_ONCE") != "1" {
		fmt.Fprintln(os.Stderr, "explicit real OVN one-shot opt-in is required")
		os.Exit(2)
	}
	databaseURL := flag.String("database-url", "", "disposable PostgreSQL authority URL")
	owner := flag.String("worker-id", "", "stable qualification worker identity")
	nb := flag.String("ovn-nb-db", "unix:/var/run/ovn/ovnnb_db.sock", "Host-local OVN NB endpoint")
	sb := flag.String("ovn-sb-db", "unix:/var/run/ovn/ovnsb_db.sock", "Host-local OVN SB endpoint")
	flag.Parse()
	if *databaseURL == "" || *owner == "" {
		fmt.Fprintln(os.Stderr, "database and worker identity are required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := postgres.OpenWithMaxConnections(ctx, *databaseURL, 4)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()
	worker := ovnruntime.Worker{
		Store: ovnruntime.PostgresWorkStore{DB: pool}, PortResourceStore: ovnruntime.PostgresPortResourceWorkStore{DB: pool},
		Adapter: ovnadapter.Runtime{Config: ovnadapter.RuntimeConfig{
			NBDatabase: *nb, SBDatabase: *sb,
			NBCTL: "/usr/bin/ovn-nbctl", SBCTL: "/usr/bin/ovn-sbctl", OVSCTL: "/usr/bin/ovs-vsctl",
			CommandTimeout: 20 * time.Second,
		}},
		Owner: *owner, BatchLimit: 1, ClaimLease: 30 * time.Second,
		ClaimMaximumLifetime: time.Minute, ClaimRenewInterval: 10 * time.Second,
		AdapterArtifactDigest: "b84dc18b20144960b80760aa93489e305225de747e809e769b1c7a5ae94f31cb",
	}
	completed, err := worker.RunOnce(ctx)
	if err != nil {
		var runErr *ovnruntime.RunOnceError
		if !errors.As(err, &runErr) || runErr.FatalError != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, runErr.ItemErrors)
		os.Exit(1)
	}
	if completed != 1 {
		fmt.Fprintf(os.Stderr, "completed work=%d, want 1\n", completed)
		os.Exit(1)
	}
}
