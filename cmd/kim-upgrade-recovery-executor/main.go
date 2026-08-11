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
	set := flag.NewFlagSet("kim-upgrade-recovery-executor", flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	databaseURL := set.String("database-url", os.Getenv("KIM_DATABASE_URL"), "PostgreSQL connection URL")
	targetID := set.String("target-id", os.Getenv("KIM_UPGRADE_TARGET_ID"), "quarantined Upgrade Target identity")
	executorID := set.String("executor-id", os.Getenv("KIM_UPGRADE_RECOVERY_EXECUTOR_ID"), "stable recovery executor identity")
	backendProfile := set.String("backend-profile", os.Getenv("KIM_UPGRADE_TARGET_BACKEND_PROFILE"), "administrator-owned backend profile")
	claimLease := set.Duration("claim-lease", 30*time.Second, "database recovery claim lease")
	claimMaximumLifetime := set.Duration("claim-maximum-lifetime", 10*time.Minute, "maximum lifetime of one recovery Attempt")
	claimRenewInterval := set.Duration("claim-renew-interval", 10*time.Second, "DB-time recovery claim renewal interval")
	claimPollInterval := set.Duration("claim-poll-interval", time.Second, "unavailable claim retry interval")
	observationSettleWindow := set.Duration("observation-settle-window", 0, "bounded wait before post-recovery read-back")
	databaseMaxConnections := set.Int("database-max-connections", 4, "bounded PostgreSQL pool size")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "kim-upgrade-recovery-executor %s\n", componentmain.Version)
		return 0
	}
	if *databaseURL == "" || *targetID == "" || *executorID == "" || *backendProfile == "" {
		fmt.Fprintln(stderr, "kim-upgrade-recovery-executor configuration error: database, Target, executor, and backend profile are required")
		return 2
	}
	if *claimLease <= 0 || *claimMaximumLifetime < *claimLease || *claimRenewInterval <= 0 ||
		*claimRenewInterval >= *claimLease || *claimPollInterval <= 0 || *observationSettleWindow < 0 ||
		*observationSettleWindow > *claimMaximumLifetime || *databaseMaxConnections < 2 || *databaseMaxConnections > 64 {
		fmt.Fprintln(stderr, "kim-upgrade-recovery-executor configuration error: bounded lease/renewal/poll/settle/database settings are required")
		return 2
	}
	backend, err := targetexecutor.NewSystemdPackageBackend(*backendProfile)
	if err != nil {
		fmt.Fprintf(stderr, "kim-upgrade-recovery-executor backend configuration error: %v\n", err)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := postgres.OpenWithMaxConnections(ctx, *databaseURL, int32(*databaseMaxConnections))
	if err != nil {
		fmt.Fprintf(stderr, "kim-upgrade-recovery-executor PostgreSQL error: %v\n", err)
		return 1
	}
	defer pool.Close()
	var claim postgres.UpgradeTargetRecoveryClaim
	for {
		claim, err = postgres.ClaimUpgradeTargetRecovery(ctx, pool, postgres.UpgradeTargetRecoveryClaimRequest{
			TargetID: *targetID, Owner: *executorID, Lease: *claimLease, MaximumLifetime: *claimMaximumLifetime,
		})
		if err == nil {
			break
		}
		if !errors.Is(err, postgres.ErrUpgradeTargetRecoveryUnavailable) {
			fmt.Fprintf(stderr, "kim-upgrade-recovery-executor claim error; retrying: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return 1
		case <-time.After(*claimPollInterval):
		}
	}
	fmt.Fprintf(stdout, "kim-upgrade-recovery-executor claimed target=%s recovery=%d attempt=%d strategy=%s\n",
		claim.TargetID, claim.RecoveryGeneration, claim.AttemptGeneration, claim.Strategy)
	opCtx, opCancel := context.WithCancel(ctx)
	defer opCancel()
	executionDone := make(chan error, 1)
	go func() {
		executionDone <- executeRecovery(opCtx, pool, backend, claim, *observationSettleWindow, stdout)
	}()
	renewTicker := time.NewTicker(*claimRenewInterval)
	defer renewTicker.Stop()
	for {
		select {
		case err := <-executionDone:
			opCancel()
			if err != nil {
				fmt.Fprintf(stderr, "kim-upgrade-recovery-executor outcome remains unknown: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "kim-upgrade-recovery-executor verified target=%s recovery=%d attempt=%d; explicit rearm remains required\n",
				claim.TargetID, claim.RecoveryGeneration, claim.AttemptGeneration)
			return 0
		case <-renewTicker.C:
			if _, err := postgres.RenewUpgradeTargetRecoveryClaim(ctx, pool, postgres.UpgradeTargetRecoveryRenewRequest{
				TargetID: claim.TargetID, Owner: claim.Owner, RecoveryGeneration: claim.RecoveryGeneration,
				AttemptGeneration: claim.AttemptGeneration, Extension: *claimLease,
			}); err != nil {
				opCancel()
				fmt.Fprintf(stderr, "kim-upgrade-recovery-executor renewal failed; side-effect outcome remains unknown: %v\n", err)
				return 1
			}
		case <-ctx.Done():
			opCancel()
			fmt.Fprintln(stderr, "kim-upgrade-recovery-executor stopped; recovery side-effect outcome remains database-authoritative")
			return 1
		}
	}
}

func executeRecovery(ctx context.Context, pool postgres.TxBeginner, backend targetexecutor.RecoveryBackend,
	claim postgres.UpgradeTargetRecoveryClaim, settle time.Duration, stdout io.Writer) error {
	target := targetexecutor.Target{TargetID: claim.TargetID, ComponentType: claim.ComponentType,
		ComponentID: claim.ComponentID, TargetArtifactDigest: claim.TargetArtifactDigest, TargetDigest: claim.TargetDigest}
	observation, err := backend.Observe(ctx, target)
	if err != nil && observation.State != "UNKNOWN" {
		return err
	}
	decision, err := postgres.ObserveUpgradeTargetRecovery(ctx, pool, postgres.UpgradeTargetRecoveryObservationRequest{
		TargetID: claim.TargetID, Owner: claim.Owner, RecoveryGeneration: claim.RecoveryGeneration,
		AttemptGeneration: claim.AttemptGeneration, ObservationState: observation.State,
		ObservedCondition: observation.Condition, ObservedDigest: observation.Digest,
	})
	if err != nil {
		return err
	}
	if decision.Action == "RECOVERY_APPLY_AUTHORIZED" {
		if err := backend.Recover(ctx, target, claim.Strategy); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "kim-upgrade-recovery-executor applied target=%s recovery=%d attempt=%d strategy=%s\n",
			claim.TargetID, claim.RecoveryGeneration, claim.AttemptGeneration, claim.Strategy)
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
		decision, err = postgres.ObserveUpgradeTargetRecovery(ctx, pool, postgres.UpgradeTargetRecoveryObservationRequest{
			TargetID: claim.TargetID, Owner: claim.Owner, RecoveryGeneration: claim.RecoveryGeneration,
			AttemptGeneration: claim.AttemptGeneration, ObservationState: observation.State,
			ObservedCondition: observation.Condition, ObservedDigest: observation.Digest,
		})
		if err != nil {
			return err
		}
	}
	if decision.Action != "COMPLETE_VERIFIED" {
		return fmt.Errorf("typed recovery read-back is %s/%s", observation.State, observation.Condition)
	}
	resultInput := fmt.Sprintf("%s:%d:%d:%s", claim.TargetID, claim.RecoveryGeneration,
		claim.AttemptGeneration, observation.Digest)
	resultDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(resultInput)))
	return postgres.CompleteUpgradeTargetRecovery(ctx, pool, postgres.UpgradeTargetRecoveryCompletionRequest{
		TargetID: claim.TargetID, Owner: claim.Owner, RecoveryGeneration: claim.RecoveryGeneration,
		AttemptGeneration: claim.AttemptGeneration, Outcome: "VERIFIED", ResultDigest: resultDigest,
		ObservedDigest: observation.Digest,
	})
}
