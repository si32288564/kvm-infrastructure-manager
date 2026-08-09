package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestExecutionAuthorityLeaseResultAndVerificationPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('execution-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}

	hostID := fmt.Sprintf("host-exec-%d", time.Now().UnixNano())
	fingerprint := digestBytes([]byte(hostID + "-certificate"))
	if err := RegisterDiscoveredHost(ctx, pool, hostID); err != nil {
		t.Fatal(err)
	}
	recordEnrollment(t, ctx, pool, hostID, 1, "APPROVED")
	recordCredential(t, ctx, pool, hostID, 1, 1, fingerprint)
	if _, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 1, 1, fingerprint)); err != nil {
		t.Fatal(err)
	}
	acceptTrustInventory(t, ctx, pool, hostID, 1, 1)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{
		HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1,
		PreflightGeneration: 1, PreflightState: "PASSED",
		ComplianceGeneration: 1, ComplianceState: "COMPLIANT",
	}); err != nil {
		t.Fatal(err)
	}
	authority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "execution_fixture"})
	if err != nil {
		t.Fatal(err)
	}

	command := commandFixture(hostID, "success")
	if err := CreateExecutionCommand(ctx, pool, command); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(2)
	grants := make(chan CommandLeaseGrant, 2)
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			defer wait.Done()
			grant, err := AcquireCommandLease(ctx, pool, CommandLeaseRequest{CommandID: command.CommandID, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: time.Minute})
			if err != nil {
				errorsFound <- err
				return
			}
			grants <- grant
		}()
	}
	wait.Wait()
	close(grants)
	close(errorsFound)
	if len(grants) != 1 || len(errorsFound) != 1 {
		t.Fatalf("concurrent Lease results: grants=%d errors=%d", len(grants), len(errorsFound))
	}
	grant := <-grants
	leaseErr := <-errorsFound
	if !errors.Is(leaseErr, ErrActiveCommandLease) || grant.HostAuthorityGeneration != authority.AuthorityGeneration || grant.SessionGeneration != 1 || grant.AttemptIndex != 1 {
		t.Fatalf("Lease grant/error = %#v/%v", grant, leaseErr)
	}
	journalDigest := digestBytes([]byte("durable-agent-journal"))
	start := CommandAttemptStart{CommandID: command.CommandID, AttemptIndex: grant.AttemptIndex, LeaseToken: grant.Token, JournalEvidenceDigest: journalDigest}
	if err := MarkCommandAttemptJournaled(ctx, pool, start); err != nil {
		t.Fatal(err)
	}
	if err := MarkCommandAttemptJournaled(ctx, pool, start); err != nil {
		t.Fatalf("identical journal replay failed: %v", err)
	}
	result := CommandResultSubmission{CommandID: command.CommandID, AttemptIndex: 1, LeaseToken: grant.Token, ResultID: command.CommandID + "-result", Outcome: "SUCCEEDED", Payload: map[string]any{"backend": "applied"}}
	receipt, err := AcceptCommandResult(ctx, pool, result)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := AcceptCommandResult(ctx, pool, result)
	if err != nil || replayed.ReceiptID != receipt.ReceiptID {
		t.Fatalf("Result replay = %#v, error = %v", replayed, err)
	}
	conflict := result
	conflict.Payload = map[string]any{"backend": "different"}
	if _, err := AcceptCommandResult(ctx, pool, conflict); !errors.Is(err, ErrCommandResultConflict) {
		t.Fatalf("conflicting Result accepted: %v", err)
	}
	if err := RecordCommandVerification(ctx, pool, CommandVerification{
		VerificationID: command.CommandID + "-verify", CommandID: command.CommandID, AttemptIndex: 1,
		ObservationGeneration: 2, ObservationDigest: digestBytes([]byte("observation-applied")),
		State: "MATCHED", VerifierArtifactDigest: digestBytes([]byte("verifier-v1")), Evidence: map[string]any{"read_back": "matched"},
	}); err != nil {
		t.Fatal(err)
	}
	assertExecutionState(t, ctx, pool, command.CommandID, "SUCCEEDED", "SUCCEEDED", 1)

	expiring := commandFixture(hostID, "expiry")
	if err := CreateExecutionCommand(ctx, pool, expiring); err != nil {
		t.Fatal(err)
	}
	expiredGrant, err := AcquireCommandLease(ctx, pool, CommandLeaseRequest{CommandID: expiring.CommandID, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := AcceptCommandResult(ctx, pool, CommandResultSubmission{CommandID: expiring.CommandID, AttemptIndex: 1, LeaseToken: expiredGrant.Token, ResultID: expiring.CommandID + "-late", Outcome: "SUCCEEDED", Payload: map[string]any{"late": true}}); !errors.Is(err, ErrStaleCommandResult) {
		t.Fatalf("expired Result error = %v", err)
	}
	assertExecutionState(t, ctx, pool, expiring.CommandID, "UNKNOWN", "ACTION_REQUIRED", 1)
	if err := RecordCommandVerification(ctx, pool, CommandVerification{
		VerificationID: expiring.CommandID + "-not-applied", CommandID: expiring.CommandID, AttemptIndex: 1,
		ObservationGeneration: 3, ObservationDigest: digestBytes([]byte("observation-absent")),
		State: "NOT_APPLIED", VerifierArtifactDigest: digestBytes([]byte("verifier-v1")), Evidence: map[string]any{"read_back": "absent"},
	}); err != nil {
		t.Fatal(err)
	}
	secondAttempt, err := AcquireCommandLease(ctx, pool, CommandLeaseRequest{CommandID: expiring.CommandID, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: time.Minute})
	if err != nil || secondAttempt.AttemptIndex != 2 {
		t.Fatalf("second Attempt = %#v, error = %v", secondAttempt, err)
	}
	if _, err := AcceptCommandResult(ctx, pool, CommandResultSubmission{CommandID: expiring.CommandID, AttemptIndex: 1, LeaseToken: expiredGrant.Token, ResultID: expiring.CommandID + "-late-again", Outcome: "SUCCEEDED", Payload: map[string]any{"late": "again"}}); !errors.Is(err, ErrStaleCommandResult) {
		t.Fatalf("old Attempt revived: %v", err)
	}

	fenced := commandFixture(hostID, "fenced")
	if err := CreateExecutionCommand(ctx, pool, fenced); err != nil {
		t.Fatal(err)
	}
	fencedGrant, err := AcquireCommandLease(ctx, pool, CommandLeaseRequest{CommandID: fenced.CommandID, HostAuthorityGeneration: authority.AuthorityGeneration, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 2, 1, fingerprint)); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptCommandResult(ctx, pool, CommandResultSubmission{CommandID: fenced.CommandID, AttemptIndex: 1, LeaseToken: fencedGrant.Token, ResultID: fenced.CommandID + "-stale", Outcome: "SUCCEEDED", Payload: map[string]any{"stale": true}}); !errors.Is(err, ErrStaleCommandResult) {
		t.Fatalf("fenced Result error = %v", err)
	}
	assertExecutionState(t, ctx, pool, fenced.CommandID, "UNKNOWN", "ACTION_REQUIRED", 1)
}

func commandFixture(hostID, suffix string) ExecutionCommandRequest {
	id := fmt.Sprintf("%s-%s-%d", hostID, suffix, time.Now().UnixNano())
	return ExecutionCommandRequest{
		JobID: id + "-job", CommandID: id + "-command", HostID: hostID,
		ResourceType: "VM", ResourceID: id + "-vm", DesiredRevision: 1,
		CommandType: "VM_ENSURE_STATE", SchemaVersion: "kim.command.vm-ensure-state/v1",
		TargetResourceID: id + "-vm", Payload: map[string]any{"state": "RUNNING"},
	}
}

func assertExecutionState(t *testing.T, ctx context.Context, db TxBeginner, commandID, wantCommand, wantJob string, wantAttempt int) {
	t.Helper()
	var commandState, jobState string
	var attempt int
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current.command_state, job.job_state, current.current_attempt_index
			FROM kim.execution_commands_current current
			JOIN kim.execution_commands command USING(command_id)
			JOIN kim.execution_jobs job USING(job_id)
			WHERE current.command_id=$1
		`, commandID).Scan(&commandState, &jobState, &attempt)
	})
	if err != nil {
		t.Fatal(err)
	}
	if commandState != wantCommand || jobState != wantJob || attempt != wantAttempt {
		t.Fatalf("execution state = %s/%s/%d, want %s/%s/%d", commandState, jobState, attempt, wantCommand, wantJob, wantAttempt)
	}
}
