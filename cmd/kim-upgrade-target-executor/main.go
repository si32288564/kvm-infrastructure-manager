package main

import (
	"context"
	"crypto/sha256"
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
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/upgrade/targetexecutor"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("kim-upgrade-target-executor", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	campaignID := set.String("campaign-id", os.Getenv("KIM_UPGRADE_CAMPAIGN_ID"), "Upgrade Campaign identity")
	targetID := set.String("target-id", os.Getenv("KIM_UPGRADE_TARGET_ID"), "immutable Upgrade Target identity")
	executorID := set.String("executor-id", os.Getenv("KIM_UPGRADE_TARGET_EXECUTOR_ID"), "stable executor process identity")
	stateDirectory := set.String("state-directory", os.Getenv("KIM_UPGRADE_TARGET_STATE_DIRECTORY"), "administrator-configured KIM-owned state directory")
	backendType := set.String("backend", "state-marker", "closed backend type: state-marker or systemd-package")
	backendProfile := set.String("backend-profile", os.Getenv("KIM_UPGRADE_TARGET_BACKEND_PROFILE"), "administrator-owned backend profile")
	claimLease := set.Duration("claim-lease", 30*time.Second, "database Target claim lease")
	claimMaximumLifetime := set.Duration("claim-maximum-lifetime", 10*time.Minute, "maximum lifetime of one Target Attempt")
	claimRenewInterval := set.Duration("claim-renew-interval", 10*time.Second, "DB-time Target claim renewal interval")
	claimPollInterval := set.Duration("claim-poll-interval", time.Second, "unavailable claim retry interval")
	observationSettleWindow := set.Duration("observation-settle-window", 0, "bounded wait before post-apply read-back")
	databaseMaxConnections := set.Int("database-max-connections", 4, "bounded PostgreSQL pool size")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-upgrade-target-executor %s\n", componentmain.Version)
		return 0
	}
	if *databaseURL == "" || *campaignID == "" || *targetID == "" || *executorID == "" {
		fmt.Fprintln(stderr, "kim-upgrade-target-executor configuration error: database, Campaign, Target, and executor identities are required")
		return 2
	}
	if *claimLease <= 0 || *claimMaximumLifetime < *claimLease || *claimRenewInterval <= 0 ||
		*claimRenewInterval >= *claimLease || *claimPollInterval <= 0 || *observationSettleWindow < 0 ||
		*observationSettleWindow > *claimMaximumLifetime || *databaseMaxConnections < 2 || *databaseMaxConnections > 64 {
		fmt.Fprintln(stderr, "kim-upgrade-target-executor configuration error: bounded lease/renewal/poll/settle/database settings are required")
		return 2
	}
	var backend targetexecutor.Backend
	var err error
	switch *backendType {
	case "state-marker":
		backend, err = targetexecutor.NewStateMarkerBackend(*stateDirectory)
	case "systemd-package":
		backend, err = targetexecutor.NewSystemdPackageBackend(*backendProfile)
	default:
		err = fmt.Errorf("unsupported backend %q", *backendType)
	}
	if err != nil {
		fmt.Fprintf(stderr, "kim-upgrade-target-executor backend configuration error: %v\n", err)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := postgres.OpenWithMaxConnections(ctx, *databaseURL, int32(*databaseMaxConnections))
	if err != nil {
		fmt.Fprintf(stderr, "kim-upgrade-target-executor PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
	var claim postgres.UpgradeTargetClaim
	for {
		claim, err = postgres.ClaimUpgradeTarget(ctx, pool, postgres.UpgradeTargetClaimRequest{
			CampaignID: *campaignID, TargetID: *targetID, Owner: *executorID,
			Lease: *claimLease, MaximumLifetime: *claimMaximumLifetime,
		})
		if err == nil {
			break
		}
		if errors.Is(err, postgres.ErrUpgradeTargetAlreadyCompleted) {
			fmt.Fprintf(stdout, "kim-upgrade-target-executor target=%s already completed in PostgreSQL authority\n", *targetID)
			return 0
		}
		if !errors.Is(err, postgres.ErrUpgradeTargetClaimUnavailable) {
			fmt.Fprintf(stderr, "kim-upgrade-target-executor claim error; retrying: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return 1
		case <-time.After(*claimPollInterval):
		}
	}
	fmt.Fprintf(stdout, "kim-upgrade-target-executor claimed target=%s attempt=%d mode=%s coordinator_generation=%d\n",
		claim.TargetID, claim.AttemptGeneration, claim.AttemptMode, claim.CoordinatorClaimGeneration)
	opCtx, opCancel := context.WithCancel(ctx)
	defer opCancel()
	executionDone := make(chan error, 1)
	go func() {
		executionDone <- executeTarget(opCtx, pool, backend, claim, *observationSettleWindow, stdout)
	}()
	renewTicker := time.NewTicker(*claimRenewInterval)
	defer renewTicker.Stop()
	finishExecution := func(err error) int {
		opCancel()
		if err != nil {
			fmt.Fprintf(stderr, "kim-upgrade-target-executor outcome remains unknown: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "kim-upgrade-target-executor completed target=%s attempt=%d\n", claim.TargetID, claim.AttemptGeneration)
		return 0
	}
	for {
		select {
		case err := <-executionDone:
			return finishExecution(err)
		case <-renewTicker.C:
			if _, err := postgres.RenewUpgradeTargetClaim(ctx, pool, postgres.UpgradeTargetRenewRequest{
				TargetID: claim.TargetID, Owner: claim.Owner, AttemptGeneration: claim.AttemptGeneration, Extension: *claimLease,
			}); err != nil {
				if errors.Is(err, postgres.ErrStaleUpgradeTargetClaim) {
					select {
					case executionErr := <-executionDone:
						return finishExecution(executionErr)
					case <-time.After(100 * time.Millisecond):
					}
				}
				opCancel()
				fmt.Fprintf(stderr, "kim-upgrade-target-executor renewal failed; side-effect outcome remains unknown: %v\n", err)
				return 1
			}
		case <-ctx.Done():
			opCancel()
			fmt.Fprintln(stderr, "kim-upgrade-target-executor stopped; Target side-effect outcome remains database-authoritative")
			return 1
		}
	}
}

func executeTarget(ctx context.Context, pool postgres.TxBeginner, backend targetexecutor.Backend,
	claim postgres.UpgradeTargetClaim, settle time.Duration, stdout io.Writer) error {
	target := targetexecutor.Target{TargetID: claim.TargetID, ComponentType: claim.ComponentType,
		ComponentID: claim.ComponentID, TargetArtifactDigest: claim.TargetArtifactDigest, TargetDigest: claim.TargetDigest}
	observation, err := backend.Observe(ctx, target)
	if err != nil {
		return err
	}
	decision, err := postgres.ObserveUpgradeTarget(ctx, pool, postgres.UpgradeTargetObservationRequest{
		TargetID: claim.TargetID, Owner: claim.Owner, AttemptGeneration: claim.AttemptGeneration,
		ObservationState: observation.State, ObservedDigest: observation.Digest,
	})
	if err != nil {
		return err
	}
	if decision.Action == "APPLY_AUTHORIZED" {
		if err := backend.Apply(ctx, target); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "kim-upgrade-target-executor applied target=%s attempt=%d\n", claim.TargetID, claim.AttemptGeneration)
		if settle > 0 {
			timer := time.NewTimer(settle)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		observation, err = backend.Observe(ctx, target)
		if err != nil {
			return err
		}
		decision, err = postgres.ObserveUpgradeTarget(ctx, pool, postgres.UpgradeTargetObservationRequest{
			TargetID: claim.TargetID, Owner: claim.Owner, AttemptGeneration: claim.AttemptGeneration,
			ObservationState: observation.State, ObservedDigest: observation.Digest,
		})
		if err != nil {
			return err
		}
	}
	if decision.Action != "COMPLETE_MATCHED" {
		return fmt.Errorf("typed read-back is %s", observation.State)
	}
	resultInput := fmt.Sprintf("%s:%d:%s", claim.TargetID, claim.AttemptGeneration, observation.Digest)
	resultDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(resultInput)))
	return postgres.CompleteUpgradeTarget(ctx, pool, postgres.UpgradeTargetCompletionRequest{
		TargetID: claim.TargetID, Owner: claim.Owner, AttemptGeneration: claim.AttemptGeneration,
		Outcome: "SUCCEEDED", ResultDigest: resultDigest, ObservedDigest: observation.Digest,
	})
}
