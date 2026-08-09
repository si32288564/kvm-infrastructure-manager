package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"
)

var (
	ErrCommandEvidenceConflict = errors.New("Command evidence conflict")
	ErrCommandNotDispatchable  = errors.New("Command is not dispatchable")
	ErrActiveCommandLease      = errors.New("Command already has an active Lease")
	ErrStaleCommandLease       = errors.New("Command Lease is stale or fenced")
	ErrCommandResultConflict   = errors.New("Command Result evidence conflict")
	ErrStaleCommandResult      = errors.New("stale Command Result cannot change authority")
	ErrVerificationConflict    = errors.New("Command verification evidence conflict")
)

type ExecutionCommandRequest struct {
	JobID            string
	CommandID        string
	HostID           string
	ResourceType     string
	ResourceID       string
	DesiredRevision  int64
	CommandType      string
	SchemaVersion    string
	TargetResourceID string
	Payload          map[string]any
}

type CommandLeaseGrant struct {
	CommandID               string
	HostID                  string
	LeaseGeneration         int64
	AttemptIndex            int
	HostAuthorityGeneration int64
	SessionGeneration       int64
	Token                   string
	NotBefore               time.Time
	ExpiresAt               time.Time
}

type CommandLeaseRequest struct {
	CommandID               string
	HostAuthorityGeneration int64
	Duration                time.Duration
}

type CommandAttemptStart struct {
	CommandID             string
	AttemptIndex          int
	LeaseToken            string
	JournalEvidenceDigest string
}

type CommandResultSubmission struct {
	CommandID    string
	AttemptIndex int
	LeaseToken   string
	ResultID     string
	Outcome      string
	Payload      map[string]any
}

type CommandResultReceipt struct {
	ReceiptID    string
	CommandID    string
	AttemptIndex int
	ResultDigest string
	Outcome      string
}

type CommandVerification struct {
	VerificationID         string
	CommandID              string
	AttemptIndex           int
	ObservationGeneration  int64
	ObservationDigest      string
	State                  string
	VerifierArtifactDigest string
	Evidence               map[string]any
}

type AgentCommandResultDecision struct {
	Start        CommandAttemptStart
	Result       CommandResultSubmission
	Verification *CommandVerification
}

type nestedTxBeginner struct{ tx pgx.Tx }

func (beginner nestedTxBeginner) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return beginner.tx.Begin(ctx)
}

// AcceptAgentCommandResult atomically commits Agent journal evidence, Result,
// optional read-back Verification, and the application-level message receipt.
// Nested savepoints reuse the standalone decision functions without exposing a
// partial domain decision if receipt commit fails.
func AcceptAgentCommandResult(ctx context.Context, db TxBeginner, envelope session.Envelope, maxMessageBytes int, decision AgentCommandResultDecision) (receipt session.Receipt, returnedErr error) {
	if err := envelope.Validate(maxMessageBytes); err != nil {
		return session.Receipt{}, err
	}
	if decision.Start.CommandID == "" || decision.Start.CommandID != decision.Result.CommandID || envelope.CorrelationKey != decision.Result.CommandID || envelope.Sequence != uint64(decision.Result.AttemptIndex) {
		return session.Receipt{}, ErrCommandEvidenceConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		existing, found, err := readAgentReceiptTx(ctx, tx, envelope.HostIdentity, envelope.MessageID)
		if err != nil {
			return err
		}
		if found {
			if err := existing.ValidateFor(envelope); err != nil {
				return ErrAgentMessageEvidenceConflict
			}
			receipt = existing
			return nil
		}
		var commandHost, sessionState string
		var currentSessionGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT command.host_id, session.session_generation, session.state
			FROM kim.execution_commands command
			JOIN kim.agent_transport_sessions_current session ON session.host_id=command.host_id
			WHERE command.command_id=$1
		`, decision.Result.CommandID).Scan(&commandHost, &currentSessionGeneration, &sessionState); err != nil {
			return ErrCommandEvidenceConflict
		}
		if commandHost != envelope.HostIdentity || currentSessionGeneration != int64(envelope.SessionGeneration) || sessionState != "CURRENT" {
			return ErrStaleCommandResult
		}
		beginner := nestedTxBeginner{tx: tx}
		if err := MarkCommandAttemptJournaled(ctx, beginner, decision.Start); err != nil {
			return err
		}
		if _, err := AcceptCommandResult(ctx, beginner, decision.Result); err != nil {
			return err
		}
		if decision.Verification != nil {
			if err := RecordCommandVerification(ctx, beginner, *decision.Verification); err != nil {
				return err
			}
		}
		receipt, err = acceptAgentMessageTx(ctx, tx, envelope)
		return err
	})
	return receipt, err
}

type AgentVerificationObservationDecision struct {
	TargetResourceID     string
	CommandPayloadDigest string
	Verification         CommandVerification
}

func AcceptAgentVerificationObservation(ctx context.Context, db TxBeginner, envelope session.Envelope, maxMessageBytes int, decision AgentVerificationObservationDecision) (receipt session.Receipt, returnedErr error) {
	if err := envelope.Validate(maxMessageBytes); err != nil {
		return session.Receipt{}, err
	}
	if decision.Verification.CommandID == "" || envelope.CorrelationKey != decision.Verification.CommandID || envelope.Sequence != uint64(decision.Verification.AttemptIndex) {
		return session.Receipt{}, ErrVerificationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		existing, found, err := readAgentReceiptTx(ctx, tx, envelope.HostIdentity, envelope.MessageID)
		if err != nil {
			return err
		}
		if found {
			if existing.ValidateFor(envelope) != nil {
				return ErrAgentMessageEvidenceConflict
			}
			receipt = existing
			return nil
		}
		var hostID, targetID, payloadDigest, state, sessionState, authorizationState string
		var attempt int
		var sessionGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT command.host_id,command.target_resource_id,command.payload_digest,current.command_state,current.current_attempt_index,
			       session.session_generation,session.state,session_auth.authorization_state
			FROM kim.execution_commands command JOIN kim.execution_commands_current current USING(command_id)
			JOIN kim.agent_transport_sessions_current session ON session.host_id=command.host_id
			JOIN kim.host_session_authorizations_current session_auth ON session_auth.host_id=command.host_id AND session_auth.session_generation=session.session_generation
			WHERE command.command_id=$1
		`, decision.Verification.CommandID).Scan(&hostID, &targetID, &payloadDigest, &state, &attempt, &sessionGeneration, &sessionState, &authorizationState); err != nil {
			return ErrVerificationConflict
		}
		if hostID != envelope.HostIdentity || sessionGeneration != int64(envelope.SessionGeneration) || sessionState != "CURRENT" || authorizationState != "AUTHORIZED" || state != "UNKNOWN" || attempt != decision.Verification.AttemptIndex || targetID != decision.TargetResourceID || payloadDigest != decision.CommandPayloadDigest {
			return ErrVerificationConflict
		}
		if err := RecordCommandVerification(ctx, nestedTxBeginner{tx: tx}, decision.Verification); err != nil {
			return err
		}
		receipt, err = acceptAgentMessageTx(ctx, tx, envelope)
		return err
	})
	return receipt, err
}

type CommandDispatchCandidate struct {
	CommandID               string
	HostID                  string
	HostAuthorityGeneration int64
	SessionGeneration       int64
	CommandType             string
	SchemaVersion           string
	TargetResourceID        string
	Payload                 json.RawMessage
	PayloadDigest           string
}

type CommandVerificationCandidate struct {
	CommandID         string
	HostID            string
	SessionGeneration int64
	AttemptIndex      int
	CommandType       string
	SchemaVersion     string
	TargetResourceID  string
	Payload           json.RawMessage
	PayloadDigest     string
}

func LoadCommandVerificationCandidate(ctx context.Context, db TxBeginner, commandID string) (CommandVerificationCandidate, error) {
	var candidate CommandVerificationCandidate
	var state string
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
		SELECT command.command_id,command.host_id,session.session_generation,current.current_attempt_index,
		       command.command_type,command.schema_version,command.target_resource_id,command.payload,command.payload_digest,current.command_state
		FROM kim.execution_commands command
		JOIN kim.execution_commands_current current USING(command_id)
		JOIN kim.agent_transport_sessions_current session ON session.host_id=command.host_id AND session.state='CURRENT'
		JOIN kim.host_session_authorizations_current session_auth ON session_auth.host_id=command.host_id AND session_auth.session_generation=session.session_generation AND session_auth.authorization_state='AUTHORIZED'
		WHERE command.command_id=$1
	`, commandID).Scan(&candidate.CommandID, &candidate.HostID, &candidate.SessionGeneration, &candidate.AttemptIndex, &candidate.CommandType, &candidate.SchemaVersion, &candidate.TargetResourceID, &candidate.Payload, &candidate.PayloadDigest, &state)
	})
	if err != nil {
		return CommandVerificationCandidate{}, fmt.Errorf("load Command verification candidate: %w", err)
	}
	if state != "UNKNOWN" || candidate.AttemptIndex < 1 {
		return CommandVerificationCandidate{}, ErrCommandNotDispatchable
	}
	return candidate, nil
}

func LoadCommandDispatchCandidate(ctx context.Context, db TxBeginner, commandID string) (CommandDispatchCandidate, error) {
	var candidate CommandDispatchCandidate
	var state string
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT command.command_id, command.host_id,
			       authority.authority_generation, authority.session_generation,
			       command.command_type, command.schema_version,
			       command.target_resource_id, command.payload, command.payload_digest,
			       current.command_state
			FROM kim.execution_commands command
			JOIN kim.execution_commands_current current USING(command_id)
			JOIN kim.host_operation_authorities_current authority ON authority.host_id=command.host_id
			WHERE command.command_id=$1
		`, commandID).Scan(&candidate.CommandID, &candidate.HostID,
			&candidate.HostAuthorityGeneration, &candidate.SessionGeneration,
			&candidate.CommandType, &candidate.SchemaVersion,
			&candidate.TargetResourceID, &candidate.Payload, &candidate.PayloadDigest, &state)
	})
	if err != nil {
		return CommandDispatchCandidate{}, ErrCommandNotDispatchable
	}
	if state != "PENDING" && state != "REDISPATCHABLE" {
		return CommandDispatchCandidate{}, ErrCommandNotDispatchable
	}
	return candidate, nil
}

func CreateExecutionCommand(ctx context.Context, db TxBeginner, request ExecutionCommandRequest) error {
	if request.JobID == "" || request.CommandID == "" || request.HostID == "" || request.ResourceType == "" || request.ResourceID == "" || request.DesiredRevision < 1 || request.CommandType == "" || request.SchemaVersion == "" || request.TargetResourceID == "" {
		return errors.New("complete typed Command identity is required")
	}
	payload, err := json.Marshal(request.Payload)
	if err != nil {
		return err
	}
	payloadDigest := digestBytes(payload)
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockHostAuthorityTx(ctx, tx, request.HostID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.execution_jobs (job_id, resource_type, resource_id, desired_revision, job_state)
			VALUES ($1,$2,$3,$4,'DISPATCHABLE') ON CONFLICT (job_id) DO NOTHING
		`, request.JobID, request.ResourceType, request.ResourceID, request.DesiredRevision); err != nil {
			return err
		}
		var resourceType, resourceID string
		var desiredRevision int64
		if err := tx.QueryRow(ctx, `
			SELECT resource_type, resource_id, desired_revision
			FROM kim.execution_jobs WHERE job_id=$1
		`, request.JobID).Scan(&resourceType, &resourceID, &desiredRevision); err != nil || resourceType != request.ResourceType || resourceID != request.ResourceID || desiredRevision != request.DesiredRevision {
			return ErrCommandEvidenceConflict
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.execution_commands (
				command_id, job_id, host_id, command_type, schema_version,
				target_resource_id, payload, payload_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (command_id) DO NOTHING
		`, request.CommandID, request.JobID, request.HostID, request.CommandType,
			request.SchemaVersion, request.TargetResourceID, payload, payloadDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var jobID, hostID, commandType, schemaVersion, targetID, acceptedDigest string
			if err := tx.QueryRow(ctx, `
				SELECT job_id, host_id, command_type, schema_version, target_resource_id, payload_digest
				FROM kim.execution_commands WHERE command_id=$1
			`, request.CommandID).Scan(&jobID, &hostID, &commandType, &schemaVersion, &targetID, &acceptedDigest); err != nil || jobID != request.JobID || hostID != request.HostID || commandType != request.CommandType || schemaVersion != request.SchemaVersion || targetID != request.TargetResourceID || acceptedDigest != payloadDigest {
				return ErrCommandEvidenceConflict
			}
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current (command_id, command_state) VALUES ($1,'PENDING')`, request.CommandID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2, updated_at=statement_timestamp() WHERE job_id=$1`, request.JobID, request.CommandID); err != nil {
			return err
		}
		return appendJobEventTx(ctx, tx, request.JobID, "COMMAND_CREATED", map[string]any{"command_id": request.CommandID, "payload_digest": payloadDigest})
	})
}

func AcquireCommandLease(ctx context.Context, db TxBeginner, request CommandLeaseRequest) (CommandLeaseGrant, error) {
	if request.CommandID == "" || request.HostAuthorityGeneration < 1 || request.Duration <= 0 || request.Duration > time.Hour {
		return CommandLeaseGrant{}, errors.New("complete bounded Command Lease request is required")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return CommandLeaseGrant{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenDigest := tokenSHA256(token)
	var grant CommandLeaseGrant
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var hostID, jobID string
		if err := tx.QueryRow(ctx, `
			SELECT command.host_id, command.job_id
			FROM kim.execution_commands command
			WHERE command.command_id=$1
		`, request.CommandID).Scan(&hostID, &jobID); err != nil {
			return ErrCommandNotDispatchable
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		var commandState string
		var attemptIndex int
		if err := tx.QueryRow(ctx, `
			SELECT command_state, current_attempt_index
			FROM kim.execution_commands_current
			WHERE command_id=$1 FOR UPDATE
		`, request.CommandID).Scan(&commandState, &attemptIndex); err != nil {
			return ErrCommandNotDispatchable
		}
		if commandState != "PENDING" && commandState != "REDISPATCHABLE" {
			if commandState == "LEASED" || commandState == "EXECUTING" {
				return ErrActiveCommandLease
			}
			return ErrCommandNotDispatchable
		}
		authority, err := readHostMutationAuthorityTx(ctx, tx, hostID, request.HostAuthorityGeneration)
		if err != nil {
			return err
		}
		var leaseGeneration int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(lease_generation),0)+1 FROM kim.command_lease_grants WHERE command_id=$1`, request.CommandID).Scan(&leaseGeneration); err != nil {
			return err
		}
		attemptIndex++
		if err := tx.QueryRow(ctx, `
			SELECT statement_timestamp(), statement_timestamp() + ($1 * interval '1 microsecond')
		`, request.Duration.Microseconds()).Scan(&grant.NotBefore, &grant.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.command_lease_grants (
				command_id, lease_generation, attempt_index, host_id,
				host_authority_generation, session_generation, token_digest,
				not_before, expires_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, request.CommandID, leaseGeneration, attemptIndex, hostID,
			authority.AuthorityGeneration, authority.SessionGeneration, tokenDigest,
			grant.NotBefore, grant.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.command_leases_current (
				command_id, lease_generation, attempt_index, host_id,
				host_authority_generation, session_generation, token_digest,
				lease_state, not_before, expires_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,'ACTIVE',$8,$9)
			ON CONFLICT (command_id) DO UPDATE SET
				lease_generation=EXCLUDED.lease_generation,
				attempt_index=EXCLUDED.attempt_index,
				host_id=EXCLUDED.host_id,
				host_authority_generation=EXCLUDED.host_authority_generation,
				session_generation=EXCLUDED.session_generation,
				token_digest=EXCLUDED.token_digest,
				lease_state='ACTIVE', not_before=EXCLUDED.not_before,
				expires_at=EXCLUDED.expires_at, updated_at=statement_timestamp()
		`, request.CommandID, leaseGeneration, attemptIndex, hostID,
			authority.AuthorityGeneration, authority.SessionGeneration, tokenDigest,
			grant.NotBefore, grant.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.command_attempts (
				command_id, attempt_index, lease_generation,
				host_authority_generation, session_generation
			) VALUES ($1,$2,$3,$4,$5)
		`, request.CommandID, attemptIndex, leaseGeneration, authority.AuthorityGeneration, authority.SessionGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state='LEASED', current_attempt_index=$2, updated_at=statement_timestamp() WHERE command_id=$1`, request.CommandID, attemptIndex); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET job_state='LEASED', updated_at=statement_timestamp() WHERE job_id=$1`, jobID); err != nil {
			return err
		}
		if err := appendLeaseEventTx(ctx, tx, request.CommandID, leaseGeneration, "GRANTED", map[string]any{"attempt_index": attemptIndex, "host_authority_generation": authority.AuthorityGeneration, "session_generation": authority.SessionGeneration, "expires_at": grant.ExpiresAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
		if err := appendAttemptEventTx(ctx, tx, request.CommandID, attemptIndex, "LEASED", "lease_granted", map[string]any{"lease_generation": leaseGeneration}); err != nil {
			return err
		}
		grant.CommandID, grant.HostID = request.CommandID, hostID
		grant.LeaseGeneration, grant.AttemptIndex = leaseGeneration, attemptIndex
		grant.HostAuthorityGeneration, grant.SessionGeneration = authority.AuthorityGeneration, authority.SessionGeneration
		grant.Token = token
		return nil
	})
	return grant, err
}

func MarkCommandAttemptJournaled(ctx context.Context, db TxBeginner, start CommandAttemptStart) error {
	if start.CommandID == "" || start.AttemptIndex < 1 || start.LeaseToken == "" || len(start.JournalEvidenceDigest) != 64 {
		return errors.New("complete Agent journal evidence is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		lease, err := validateActiveLeaseTx(ctx, tx, start.CommandID, start.AttemptIndex, start.LeaseToken)
		if err != nil {
			return err
		}
		if err := appendAttemptEventTx(ctx, tx, start.CommandID, start.AttemptIndex, "JOURNALED", "write_before_execute", map[string]any{"journal_evidence_digest": start.JournalEvidenceDigest}); err != nil {
			return err
		}
		if err := appendLeaseEventTx(ctx, tx, start.CommandID, lease.LeaseGeneration, "STARTED", map[string]any{"attempt_index": start.AttemptIndex}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state='EXECUTING', updated_at=statement_timestamp() WHERE command_id=$1`, start.CommandID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.execution_jobs job SET job_state='EXECUTING', updated_at=statement_timestamp() FROM kim.execution_commands command WHERE command.command_id=$1 AND job.job_id=command.job_id`, start.CommandID)
		return err
	})
}

func AcceptCommandResult(ctx context.Context, db TxBeginner, submission CommandResultSubmission) (CommandResultReceipt, error) {
	if submission.CommandID == "" || submission.AttemptIndex < 1 || submission.LeaseToken == "" || submission.ResultID == "" {
		return CommandResultReceipt{}, errors.New("complete Command Result identity is required")
	}
	if submission.Outcome != "SUCCEEDED" && submission.Outcome != "FAILED" && submission.Outcome != "UNKNOWN" {
		return CommandResultReceipt{}, errors.New("unsupported execution outcome")
	}
	payload, err := json.Marshal(submission.Payload)
	if err != nil {
		return CommandResultReceipt{}, err
	}
	resultDigest := digestBytes(payload)
	receiptID := digestBytes([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", submission.CommandID, submission.AttemptIndex, submission.ResultID, submission.Outcome, resultDigest)))
	receipt := CommandResultReceipt{ReceiptID: receiptID, CommandID: submission.CommandID, AttemptIndex: submission.AttemptIndex, ResultDigest: resultDigest, Outcome: submission.Outcome}
	var decisionErr error
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var acceptedID, acceptedDigest, acceptedOutcome, acceptedReceipt string
		err := tx.QueryRow(ctx, `SELECT result_id, result_digest, execution_outcome, receipt_id FROM kim.command_results WHERE command_id=$1 AND attempt_index=$2`, submission.CommandID, submission.AttemptIndex).Scan(&acceptedID, &acceptedDigest, &acceptedOutcome, &acceptedReceipt)
		if err == nil {
			if acceptedID != submission.ResultID || acceptedDigest != resultDigest || acceptedOutcome != submission.Outcome {
				return ErrCommandResultConflict
			}
			receipt.ReceiptID = acceptedReceipt
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		lease, err := validateActiveLeaseTx(ctx, tx, submission.CommandID, submission.AttemptIndex, submission.LeaseToken)
		if err != nil {
			if expireErr := expireElapsedLeaseTx(ctx, tx, submission.CommandID, submission.AttemptIndex); expireErr != nil {
				return expireErr
			}
			if appendErr := appendAttemptEventTx(ctx, tx, submission.CommandID, submission.AttemptIndex, "STALE_RESULT_REJECTED", "lease_or_authority_not_current", map[string]any{"result_id": submission.ResultID, "result_digest": resultDigest}); appendErr != nil {
				return appendErr
			}
			decisionErr = ErrStaleCommandResult
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.command_results (
				command_id, attempt_index, result_id, result_digest,
				execution_outcome, result_payload, receipt_id
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, submission.CommandID, submission.AttemptIndex, submission.ResultID, resultDigest, submission.Outcome, payload, receiptID); err != nil {
			return err
		}
		if err := appendAttemptEventTx(ctx, tx, submission.CommandID, submission.AttemptIndex, "RESULT_ACCEPTED", "result_durably_accepted", map[string]any{"result_id": submission.ResultID, "result_digest": resultDigest, "outcome": submission.Outcome}); err != nil {
			return err
		}
		if err := appendLeaseEventTx(ctx, tx, submission.CommandID, lease.LeaseGeneration, "CONSUMED", map[string]any{"result_id": submission.ResultID}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.command_leases_current SET lease_state='CONSUMED', updated_at=statement_timestamp() WHERE command_id=$1`, submission.CommandID); err != nil {
			return err
		}
		commandState, jobState := "VERIFYING", "VERIFYING"
		switch submission.Outcome {
		case "FAILED":
			commandState, jobState = "FAILED", "FAILED"
		case "UNKNOWN":
			commandState, jobState = "UNKNOWN", "ACTION_REQUIRED"
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state=$2, updated_at=statement_timestamp() WHERE command_id=$1`, submission.CommandID, commandState); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.execution_jobs job SET job_state=$2, updated_at=statement_timestamp() FROM kim.execution_commands command WHERE command.command_id=$1 AND job.job_id=command.job_id`, submission.CommandID, jobState)
		return err
	})
	if err != nil {
		return CommandResultReceipt{}, err
	}
	if decisionErr != nil {
		return CommandResultReceipt{}, decisionErr
	}
	return receipt, nil
}

func expireElapsedLeaseTx(ctx context.Context, tx pgx.Tx, commandID string, attemptIndex int) error {
	var leaseGeneration int64
	var state string
	err := tx.QueryRow(ctx, `
		SELECT lease_generation, lease_state
		FROM kim.command_leases_current
		WHERE command_id=$1 AND attempt_index=$2 AND expires_at <= statement_timestamp()
		FOR UPDATE
	`, commandID, attemptIndex).Scan(&leaseGeneration, &state)
	if errors.Is(err, pgx.ErrNoRows) || state != "ACTIVE" {
		return nil
	}
	if err != nil {
		return err
	}
	return markLeaseUnknownTx(ctx, tx, commandID, leaseGeneration, attemptIndex, "EXPIRED", "lease_expired")
}

func ExpireCommandLease(ctx context.Context, db TxBeginner, commandID string) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var hostID string
		if err := tx.QueryRow(ctx, `SELECT host_id FROM kim.command_leases_current WHERE command_id=$1`, commandID).Scan(&hostID); err != nil {
			return ErrStaleCommandLease
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		var leaseGeneration int64
		var attemptIndex int
		var state string
		if err := tx.QueryRow(ctx, `
			SELECT lease_generation, attempt_index, lease_state
			FROM kim.command_leases_current
			WHERE command_id=$1 AND expires_at <= statement_timestamp()
			FOR UPDATE
		`, commandID).Scan(&leaseGeneration, &attemptIndex, &state); err != nil {
			return ErrStaleCommandLease
		}
		if state != "ACTIVE" {
			return ErrStaleCommandLease
		}
		return markLeaseUnknownTx(ctx, tx, commandID, leaseGeneration, attemptIndex, "EXPIRED", "lease_expired")
	})
}

func RecordCommandVerification(ctx context.Context, db TxBeginner, verification CommandVerification) error {
	if verification.VerificationID == "" || verification.CommandID == "" || verification.AttemptIndex < 1 || verification.ObservationGeneration < 1 || len(verification.ObservationDigest) != 64 || len(verification.VerifierArtifactDigest) != 64 {
		return errors.New("complete verification evidence is required")
	}
	if verification.State != "MATCHED" && verification.State != "NOT_APPLIED" && verification.State != "CONFLICTING" && verification.State != "UNKNOWN" {
		return errors.New("unsupported verification state")
	}
	payload, err := json.Marshal(verification.Evidence)
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var commandState, jobID string
		var currentAttempt int
		if err := tx.QueryRow(ctx, `
			SELECT current.command_state, current.current_attempt_index, command.job_id
			FROM kim.execution_commands_current current
			JOIN kim.execution_commands command USING(command_id)
			WHERE current.command_id=$1 FOR UPDATE
		`, verification.CommandID).Scan(&commandState, &currentAttempt, &jobID); err != nil || currentAttempt != verification.AttemptIndex {
			return ErrVerificationConflict
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.command_verification_evidence (
				verification_id, command_id, attempt_index, observation_generation,
				observation_digest, verification_state, verifier_artifact_digest, evidence_payload
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (verification_id) DO NOTHING
		`, verification.VerificationID, verification.CommandID, verification.AttemptIndex,
			verification.ObservationGeneration, verification.ObservationDigest,
			verification.State, verification.VerifierArtifactDigest, payload)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM kim.command_verification_evidence
					WHERE verification_id=$1 AND command_id=$2 AND attempt_index=$3
					  AND observation_generation=$4 AND observation_digest=$5
					  AND verification_state=$6 AND verifier_artifact_digest=$7
					  AND evidence_payload=$8::jsonb
				)
			`, verification.VerificationID, verification.CommandID, verification.AttemptIndex,
				verification.ObservationGeneration, verification.ObservationDigest,
				verification.State, verification.VerifierArtifactDigest, payload).Scan(&identical); err != nil || !identical {
				return ErrVerificationConflict
			}
			return nil
		}
		commandNext, jobNext, eventType := "UNKNOWN", "ACTION_REQUIRED", "VERIFICATION_UNRESOLVED"
		switch verification.State {
		case "MATCHED":
			if commandState != "VERIFYING" && commandState != "UNKNOWN" {
				return ErrVerificationConflict
			}
			commandNext, jobNext, eventType = "SUCCEEDED", "SUCCEEDED", "VERIFICATION_MATCHED"
		case "NOT_APPLIED":
			if commandState != "UNKNOWN" {
				return ErrVerificationConflict
			}
			commandNext, jobNext, eventType = "REDISPATCHABLE", "DISPATCHABLE", "VERIFICATION_NOT_APPLIED"
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state=$2, updated_at=statement_timestamp() WHERE command_id=$1`, verification.CommandID, commandNext); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET job_state=$2, updated_at=statement_timestamp() WHERE job_id=$1`, jobID, jobNext); err != nil {
			return err
		}
		return appendJobEventTx(ctx, tx, jobID, eventType, map[string]any{"verification_id": verification.VerificationID, "attempt_index": verification.AttemptIndex, "observation_generation": verification.ObservationGeneration})
	})
}

type currentLease struct {
	LeaseGeneration         int64
	HostID                  string
	HostAuthorityGeneration int64
	SessionGeneration       int64
}

func validateActiveLeaseTx(ctx context.Context, tx pgx.Tx, commandID string, attemptIndex int, token string) (currentLease, error) {
	var lease currentLease
	var tokenDigest, state string
	if err := tx.QueryRow(ctx, `
		SELECT host_id FROM kim.command_leases_current
		WHERE command_id=$1 AND attempt_index=$2
	`, commandID, attemptIndex).Scan(&lease.HostID); err != nil {
		return currentLease{}, ErrStaleCommandLease
	}
	if err := lockHostAuthorityTx(ctx, tx, lease.HostID); err != nil {
		return currentLease{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT lease_generation, host_authority_generation,
		       session_generation, token_digest, lease_state
		FROM kim.command_leases_current
		WHERE command_id=$1 AND attempt_index=$2
		  AND not_before <= statement_timestamp() AND expires_at > statement_timestamp()
		FOR UPDATE
	`, commandID, attemptIndex).Scan(&lease.LeaseGeneration, &lease.HostAuthorityGeneration, &lease.SessionGeneration, &tokenDigest, &state); err != nil || state != "ACTIVE" || tokenDigest != tokenSHA256(token) {
		return currentLease{}, ErrStaleCommandLease
	}
	authority, err := readHostMutationAuthorityTx(ctx, tx, lease.HostID, lease.HostAuthorityGeneration)
	if err != nil || authority.SessionGeneration != lease.SessionGeneration {
		return currentLease{}, ErrStaleCommandLease
	}
	return lease, nil
}

func readHostMutationAuthorityTx(ctx context.Context, tx pgx.Tx, hostID string, authorityGeneration int64) (HostMutationAuthority, error) {
	var authority HostMutationAuthority
	var authorityState, sessionState, authorizationState, credentialState, enrollmentState, gateState string
	err := tx.QueryRow(ctx, `
		SELECT authority.host_id, authority.authority_generation, authority.session_generation,
		       authority.credential_binding_revision, authority.enrollment_decision_revision,
		       authority.capability_generation, authority.baseline_assignment_generation,
		       authority.preflight_generation, authority.compliance_generation,
		       authority.authority_state, session.state, session_auth.authorization_state,
		       credential.binding_state, enrollment.binding_state, gates.gate_state
		FROM kim.host_operation_authorities_current authority
		JOIN kim.host_identities host ON host.host_id=authority.host_id AND host.enrollment_state='APPROVED' AND host.host_authority_generation=authority.authority_generation
		JOIN kim.agent_transport_sessions_current session ON session.host_id=authority.host_id AND session.session_generation=authority.session_generation
		JOIN kim.host_session_authorizations_current session_auth ON session_auth.host_id=authority.host_id AND session_auth.session_generation=authority.session_generation AND session_auth.capability_generation=authority.capability_generation
		JOIN kim.agent_credential_bindings_current credential ON credential.host_id=authority.host_id AND credential.binding_revision=authority.credential_binding_revision
		JOIN kim.agent_credential_binding_evidence credential_evidence ON credential_evidence.host_id=credential.host_id AND credential_evidence.binding_revision=credential.binding_revision AND credential_evidence.binding_state='ACTIVE' AND statement_timestamp() >= credential_evidence.valid_not_before AND statement_timestamp() < credential_evidence.valid_not_after
		JOIN kim.host_enrollment_bindings_current enrollment ON enrollment.host_id=authority.host_id AND enrollment.decision_revision=authority.enrollment_decision_revision
		JOIN kim.host_readiness_gates_current gates ON gates.host_id=authority.host_id AND gates.capability_generation=authority.capability_generation AND gates.baseline_assignment_generation=authority.baseline_assignment_generation AND gates.preflight_generation=authority.preflight_generation AND gates.compliance_generation=authority.compliance_generation
		JOIN kim.host_capability_projections capability ON capability.host_id=authority.host_id AND capability.observation_generation=authority.capability_generation AND capability.projection_state='CURRENT'
		WHERE authority.host_id=$1 AND authority.authority_generation=$2
	`, hostID, authorityGeneration).Scan(
		&authority.HostID, &authority.AuthorityGeneration, &authority.SessionGeneration,
		&authority.CredentialBindingRevision, &authority.EnrollmentDecisionRevision,
		&authority.CapabilityGeneration, &authority.BaselineAssignmentGeneration,
		&authority.PreflightGeneration, &authority.ComplianceGeneration,
		&authorityState, &sessionState, &authorizationState, &credentialState, &enrollmentState, &gateState,
	)
	if err != nil || authorityState != "ARMED" || sessionState != "CURRENT" || authorizationState != "AUTHORIZED" || credentialState != "CURRENT" || enrollmentState != "ENROLLED" || gateState != "READY" {
		return HostMutationAuthority{}, ErrHostAuthorityNotArmed
	}
	return authority, nil
}

func fenceHostCommandLeasesTx(ctx context.Context, tx pgx.Tx, hostID, reason string) error {
	rows, err := tx.Query(ctx, `
		SELECT command_id, lease_generation, attempt_index
		FROM kim.command_leases_current
		WHERE host_id=$1 AND lease_state='ACTIVE'
		ORDER BY command_id FOR UPDATE
	`, hostID)
	if err != nil {
		return err
	}
	type leaseIdentity struct {
		commandID       string
		leaseGeneration int64
		attemptIndex    int
	}
	var leases []leaseIdentity
	for rows.Next() {
		var lease leaseIdentity
		if err := rows.Scan(&lease.commandID, &lease.leaseGeneration, &lease.attemptIndex); err != nil {
			rows.Close()
			return err
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, lease := range leases {
		if err := markLeaseUnknownTx(ctx, tx, lease.commandID, lease.leaseGeneration, lease.attemptIndex, "FENCED", reason); err != nil {
			return err
		}
	}
	return nil
}

func markLeaseUnknownTx(ctx context.Context, tx pgx.Tx, commandID string, leaseGeneration int64, attemptIndex int, leaseEvent, reason string) error {
	if _, err := tx.Exec(ctx, `UPDATE kim.command_leases_current SET lease_state=$2, updated_at=statement_timestamp() WHERE command_id=$1 AND lease_state='ACTIVE'`, commandID, leaseEvent); err != nil {
		return err
	}
	if err := appendLeaseEventTx(ctx, tx, commandID, leaseGeneration, leaseEvent, map[string]any{"reason": reason}); err != nil {
		return err
	}
	if err := appendAttemptEventTx(ctx, tx, commandID, attemptIndex, "UNKNOWN", reason, map[string]any{"lease_generation": leaseGeneration}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.execution_commands_current SET command_state='UNKNOWN', updated_at=statement_timestamp() WHERE command_id=$1`, commandID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE kim.execution_jobs job SET job_state='ACTION_REQUIRED', updated_at=statement_timestamp() FROM kim.execution_commands command WHERE command.command_id=$1 AND job.job_id=command.job_id`, commandID)
	return err
}

func appendLeaseEventTx(ctx context.Context, tx pgx.Tx, commandID string, leaseGeneration int64, eventType string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	digest := digestBytes(encoded)
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.command_lease_events (
			command_id, lease_generation, event_sequence, event_type,
			event_payload, event_payload_digest
		) SELECT $1,$2,COALESCE(max(event_sequence),0)+1,$3,$4,$5
		FROM kim.command_lease_events WHERE command_id=$1 AND lease_generation=$2
		ON CONFLICT (command_id, lease_generation, event_type) DO NOTHING
	`, commandID, leaseGeneration, eventType, encoded, digest)
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	var acceptedDigest string
	if err := tx.QueryRow(ctx, `SELECT event_payload_digest FROM kim.command_lease_events WHERE command_id=$1 AND lease_generation=$2 AND event_type=$3`, commandID, leaseGeneration, eventType).Scan(&acceptedDigest); err != nil || acceptedDigest != digest {
		return ErrCommandEvidenceConflict
	}
	return nil
}

func appendAttemptEventTx(ctx context.Context, tx pgx.Tx, commandID string, attemptIndex int, eventType, reason string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM kim.command_attempts WHERE command_id=$1 AND attempt_index=$2 FOR UPDATE`, commandID, attemptIndex); err != nil {
		return err
	}
	digest := digestBytes(encoded)
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.command_attempt_events (
			command_id, attempt_index, event_sequence, event_type,
			reason_code, event_payload, event_payload_digest
		) SELECT $1,$2,COALESCE(max(event_sequence),0)+1,$3,$4,$5,$6
		FROM kim.command_attempt_events WHERE command_id=$1 AND attempt_index=$2
		ON CONFLICT DO NOTHING
	`, commandID, attemptIndex, eventType, reason, encoded, digest)
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	if eventType == "STALE_RESULT_REJECTED" {
		return nil
	}
	var acceptedReason, acceptedDigest string
	if err := tx.QueryRow(ctx, `SELECT reason_code, event_payload_digest FROM kim.command_attempt_events WHERE command_id=$1 AND attempt_index=$2 AND event_type=$3`, commandID, attemptIndex, eventType).Scan(&acceptedReason, &acceptedDigest); err != nil || acceptedReason != reason || acceptedDigest != digest {
		return ErrCommandEvidenceConflict
	}
	return nil
}

func appendJobEventTx(ctx context.Context, tx pgx.Tx, jobID, eventType string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.execution_job_events (
			job_id, event_sequence, event_type, event_payload, event_payload_digest
		) SELECT $1,COALESCE(max(event_sequence),0)+1,$2,$3,$4
		FROM kim.execution_job_events WHERE job_id=$1
	`, jobID, eventType, encoded, digestBytes(encoded))
	return err
}

func tokenSHA256(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
