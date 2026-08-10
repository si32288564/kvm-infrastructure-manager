package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/persistence/postgres"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-upgrade-coordinator", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	campaignID := set.String("campaign-id", os.Getenv("KIM_UPGRADE_CAMPAIGN_ID"), "current Upgrade Campaign identity")
	coordinatorID := set.String("coordinator-id", os.Getenv("KIM_UPGRADE_COORDINATOR_ID"), "stable coordinator process identity")
	evaluatorDigest := set.String("canary-evaluator-artifact-digest", os.Getenv("KIM_UPGRADE_CANARY_EVALUATOR_ARTIFACT_DIGEST"), "SHA-256 digest of the canary evaluator artifact")
	claimLease := set.Duration("claim-lease", 30*time.Second, "database coordinator claim lease")
	claimMaximumLifetime := set.Duration("claim-maximum-lifetime", 5*time.Minute, "maximum lifetime of one coordinator claim generation")
	claimRenewInterval := set.Duration("claim-renew-interval", 10*time.Second, "DB-time coordinator claim renewal interval")
	pollInterval := set.Duration("poll-interval", time.Second, "canary evidence evaluation interval")
	databaseMaxConnections := set.Int("database-max-connections", 4, "bounded PostgreSQL pool size")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-upgrade-coordinator %s\n", componentmain.Version)
		return 0
	}
	if *databaseURL == "" || *campaignID == "" || *coordinatorID == "" || !validSHA256(*evaluatorDigest) {
		fmt.Fprintln(stderr, "kim-upgrade-coordinator configuration error: database, campaign/coordinator identity, and evaluator digest are required")
		return 2
	}
	if *claimLease <= 0 || *claimMaximumLifetime < *claimLease || *claimRenewInterval <= 0 ||
		*claimRenewInterval >= *claimLease || *pollInterval <= 0 || *databaseMaxConnections < 2 || *databaseMaxConnections > 64 {
		fmt.Fprintln(stderr, "kim-upgrade-coordinator configuration error: bounded lease/renewal/poll/database settings are required")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := postgres.OpenWithMaxConnections(ctx, *databaseURL, int32(*databaseMaxConnections))
	if err != nil {
		fmt.Fprintf(stderr, "kim-upgrade-coordinator PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
	var claim postgres.UpgradeCampaignClaim
	for {
		claim, err = postgres.ClaimUpgradeCampaign(ctx, pool, postgres.UpgradeCampaignClaimRequest{
			CampaignID: *campaignID, Owner: *coordinatorID, Lease: *claimLease, MaximumLifetime: *claimMaximumLifetime,
		})
		if err == nil {
			break
		}
		if !errors.Is(err, postgres.ErrUpgradeCampaignClaimUnavailable) {
			fmt.Fprintf(stderr, "kim-upgrade-coordinator claim error; retrying: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(*pollInterval):
		}
	}
	fmt.Fprintf(stdout, "kim-upgrade-coordinator claimed campaign=%s generation=%d mode=%s plan=%d wave=%s\n",
		claim.CampaignID, claim.ClaimGeneration, claim.ClaimMode, claim.PlanRevision, claim.WaveID)
	renewTicker := time.NewTicker(*claimRenewInterval)
	defer renewTicker.Stop()
	pollTicker := time.NewTicker(*pollInterval)
	defer pollTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "kim-upgrade-coordinator stopped; current target outcome remains database-authoritative")
			return 0
		case <-renewTicker.C:
			if _, err := postgres.RenewUpgradeCampaignClaim(ctx, pool, postgres.UpgradeCampaignRenewRequest{
				CampaignID: claim.CampaignID, Owner: claim.Owner, ClaimGeneration: claim.ClaimGeneration, Extension: *claimLease,
			}); err != nil {
				fmt.Fprintf(stderr, "kim-upgrade-coordinator renewal error; outcome remains unknown: %v\n", err)
				return 1
			}
		case <-pollTicker.C:
			decision, err := postgres.EvaluateUpgradeCanary(ctx, pool, postgres.UpgradeCanaryDecisionRequest{
				CampaignID: claim.CampaignID, Owner: claim.Owner, ClaimGeneration: claim.ClaimGeneration,
				EvaluatorArtifactDigest: *evaluatorDigest,
			})
			if err != nil {
				fmt.Fprintf(stderr, "kim-upgrade-coordinator evaluation error; outcome remains database-authoritative: %v\n", err)
				return 1
			}
			if decision.Decision == "HOLD" {
				continue
			}
			fmt.Fprintf(stdout, "kim-upgrade-coordinator canary decision=%s succeeded=%d failed=%d unknown=%d pending=%d\n",
				decision.Decision, decision.Succeeded, decision.Failed, decision.Unknown, decision.Pending)
			return 0
		}
	}
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
