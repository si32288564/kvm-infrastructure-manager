package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrCredentialBindingNotCurrent = errors.New("Agent credential binding is not current")
	ErrSessionNotAuthorized        = errors.New("Host Agent session is not authorized")
	ErrHostAuthorityNotReady       = errors.New("Host authority gates are not ready")
	ErrHostAuthorityNotArmed       = errors.New("Host operation authority is not armed")
)

type EnrollmentDecision struct {
	DecisionID             string
	HostID                 string
	Revision               int64
	PolicyID               string
	PolicyGeneration       int64
	HardwareEvidenceDigest string
	State                  string
	ActorID                string
	ReasonCode             string
}

type AgentCredentialBindingEvidence struct {
	HostID                 string
	Revision               int64
	CertificateFingerprint string
	PublicKeyDigest        string
	IssuerID               string
	ProfileRevision        string
	TrustGeneration        int64
	EnrollmentDecisionID   string
	EnrollmentRevision     int64
	EvidenceDigest         string
	State                  string
	ValidNotBefore         time.Time
	ValidNotAfter          time.Time
}

type HostReadinessGate struct {
	HostID                       string
	CapabilityGeneration         int64
	BaselineAssignmentGeneration int64
	PreflightGeneration          int64
	PreflightState               string
	ComplianceGeneration         int64
	ComplianceState              string
}

type HostAuthorityArmRequest struct {
	HostID           string
	PolicyID         string
	PolicyGeneration int64
	ActorID          string
	ReasonCode       string
}

type HostMutationAuthority struct {
	HostID                       string
	AuthorityGeneration          int64
	SessionGeneration            int64
	CredentialBindingRevision    int64
	EnrollmentDecisionRevision   int64
	CapabilityGeneration         int64
	BaselineAssignmentGeneration int64
	PreflightGeneration          int64
	ComplianceGeneration         int64
}

func RegisterDiscoveredHost(ctx context.Context, db TxBeginner, hostID string) error {
	if hostID == "" {
		return errors.New("Host identity is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.host_identities (host_id, enrollment_state)
			VALUES ($1, 'DISCOVERED') ON CONFLICT (host_id) DO NOTHING
		`, hostID)
		return err
	})
}

func RecordEnrollmentDecision(ctx context.Context, db TxBeginner, decision EnrollmentDecision) error {
	if decision.DecisionID == "" || decision.HostID == "" || decision.Revision < 1 || decision.PolicyID == "" || decision.PolicyGeneration < 1 || len(decision.HardwareEvidenceDigest) != 64 || decision.ActorID == "" || decision.ReasonCode == "" {
		return errors.New("complete Enrollment decision is required")
	}
	bindingState, identityState := "ENROLLED", "APPROVED"
	switch decision.State {
	case "APPROVED":
	case "REJECTED":
		bindingState, identityState = "REJECTED", "QUARANTINED"
	case "QUARANTINED":
		bindingState, identityState = "QUARANTINED", "QUARANTINED"
	case "DECOMMISSIONED":
		bindingState, identityState = "DECOMMISSIONED", "DECOMMISSIONED"
	default:
		return errors.New("unsupported Enrollment decision state")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockHostAuthorityTx(ctx, tx, decision.HostID); err != nil {
			return err
		}
		var currentIdentityState string
		if err := tx.QueryRow(ctx, `SELECT enrollment_state FROM kim.host_identities WHERE host_id=$1`, decision.HostID).Scan(&currentIdentityState); err != nil {
			return err
		}
		if currentIdentityState == "DECOMMISSIONED" && decision.State != "DECOMMISSIONED" {
			return errors.New("decommissioned Host identity cannot be reenrolled")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.host_enrollment_decisions (
				decision_id, host_id, decision_revision, policy_id, policy_generation,
				hardware_evidence_digest, decision_state, actor_id, reason_code
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, decision.DecisionID, decision.HostID, decision.Revision, decision.PolicyID,
			decision.PolicyGeneration, decision.HardwareEvidenceDigest, decision.State,
			decision.ActorID, decision.ReasonCode); err != nil {
			return fmt.Errorf("record Enrollment decision: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_enrollment_bindings_current (
				host_id, decision_id, decision_revision, binding_state
			) VALUES ($1,$2,$3,$4)
			ON CONFLICT (host_id) DO UPDATE SET
				decision_id=EXCLUDED.decision_id,
				decision_revision=EXCLUDED.decision_revision,
				binding_state=EXCLUDED.binding_state,
				updated_at=statement_timestamp()
			WHERE host_enrollment_bindings_current.decision_revision < EXCLUDED.decision_revision
		`, decision.HostID, decision.DecisionID, decision.Revision, bindingState)
		if err != nil {
			return fmt.Errorf("update current Enrollment binding: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return errors.New("Enrollment decision revision is not newer than current")
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_identities SET enrollment_state=$2, updated_at=statement_timestamp() WHERE host_id=$1`, decision.HostID, identityState); err != nil {
			return err
		}
		authorizationState := "STALE"
		if decision.State != "APPROVED" {
			authorizationState = "FENCED"
			if _, err := tx.Exec(ctx, `UPDATE kim.agent_transport_sessions_current SET state='FENCED', updated_at=statement_timestamp() WHERE host_id=$1`, decision.HostID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_session_authorizations_current SET authorization_state=$2, reason_code='enrollment_decision_changed', evaluated_at=statement_timestamp() WHERE host_id=$1`, decision.HostID, authorizationState); err != nil {
			return err
		}
		return fenceHostOperationAuthorityTx(ctx, tx, decision.HostID, "enrollment_decision_changed")
	})
}

func RecordAgentCredentialBinding(ctx context.Context, db TxBeginner, binding AgentCredentialBindingEvidence) error {
	if binding.HostID == "" || binding.Revision < 1 || len(binding.CertificateFingerprint) != 64 || len(binding.PublicKeyDigest) != 64 || binding.IssuerID == "" || binding.ProfileRevision == "" || binding.TrustGeneration < 1 || binding.EnrollmentDecisionID == "" || binding.EnrollmentRevision < 1 || len(binding.EvidenceDigest) != 64 || !binding.ValidNotAfter.After(binding.ValidNotBefore) {
		return errors.New("complete Agent Credential Binding evidence is required")
	}
	currentState := "CURRENT"
	switch binding.State {
	case "ACTIVE":
	case "REVOKED":
		currentState = "REVOKED"
	case "EXPIRED":
		currentState = "EXPIRED"
	case "QUARANTINED":
		currentState = "QUARANTINED"
	default:
		return errors.New("unsupported Credential Binding state")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockHostAuthorityTx(ctx, tx, binding.HostID); err != nil {
			return err
		}
		var currentDecisionID, enrollmentState string
		var currentEnrollmentRevision int64
		if err := tx.QueryRow(ctx, `
			SELECT decision_id, decision_revision, binding_state
			FROM kim.host_enrollment_bindings_current WHERE host_id=$1
		`, binding.HostID).Scan(&currentDecisionID, &currentEnrollmentRevision, &enrollmentState); err != nil || currentDecisionID != binding.EnrollmentDecisionID || currentEnrollmentRevision != binding.EnrollmentRevision || enrollmentState != "ENROLLED" {
			return ErrCredentialBindingNotCurrent
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.agent_credential_binding_evidence (
				host_id, binding_revision, certificate_fingerprint_sha256, public_key_digest,
				issuer_id, certificate_profile_revision, trust_generation,
				enrollment_decision_id, enrollment_decision_revision, evidence_digest,
				binding_state, valid_not_before, valid_not_after
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		`, binding.HostID, binding.Revision, binding.CertificateFingerprint, binding.PublicKeyDigest,
			binding.IssuerID, binding.ProfileRevision, binding.TrustGeneration,
			binding.EnrollmentDecisionID, binding.EnrollmentRevision, binding.EvidenceDigest,
			binding.State, binding.ValidNotBefore, binding.ValidNotAfter); err != nil {
			return fmt.Errorf("record Agent Credential Binding evidence: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.agent_credential_bindings_current (host_id, binding_revision, binding_state, trust_generation)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (host_id) DO UPDATE SET
				binding_revision=EXCLUDED.binding_revision,
				binding_state=EXCLUDED.binding_state,
				trust_generation=EXCLUDED.trust_generation,
				updated_at=statement_timestamp()
			WHERE agent_credential_bindings_current.binding_revision < EXCLUDED.binding_revision
		`, binding.HostID, binding.Revision, currentState, binding.TrustGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("Credential Binding revision is not newer than current")
		}
		authorizationState := "STALE"
		if binding.State != "ACTIVE" {
			authorizationState = "FENCED"
			if _, err := tx.Exec(ctx, `UPDATE kim.agent_transport_sessions_current SET state='FENCED', updated_at=statement_timestamp() WHERE host_id=$1`, binding.HostID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.host_session_authorizations_current SET authorization_state=$2, reason_code='credential_binding_changed', evaluated_at=statement_timestamp() WHERE host_id=$1`, binding.HostID, authorizationState); err != nil {
			return err
		}
		return fenceHostOperationAuthorityTx(ctx, tx, binding.HostID, "credential_binding_changed")
	})
}

func validateCurrentCredentialBindingTx(ctx context.Context, tx pgx.Tx, hostID string, revision int64, certificateFingerprint string) (int64, error) {
	var enrollmentRevision, credentialEnrollmentRevision int64
	var enrollmentState, identityState, currentState, evidenceState, fingerprint string
	if err := tx.QueryRow(ctx, `
		SELECT enrollment.decision_revision, evidence.enrollment_decision_revision,
		       enrollment.binding_state, host.enrollment_state,
		       current.binding_state, evidence.binding_state, evidence.certificate_fingerprint_sha256
		FROM kim.host_identities host
		JOIN kim.host_enrollment_bindings_current enrollment ON enrollment.host_id=host.host_id
		JOIN kim.agent_credential_bindings_current current ON current.host_id=host.host_id
		JOIN kim.agent_credential_binding_evidence evidence
		  ON evidence.host_id=current.host_id AND evidence.binding_revision=current.binding_revision
		WHERE host.host_id=$1 AND current.binding_revision=$2
		  AND statement_timestamp() >= evidence.valid_not_before
		  AND statement_timestamp() < evidence.valid_not_after
	`, hostID, revision).Scan(&enrollmentRevision, &credentialEnrollmentRevision, &enrollmentState, &identityState, &currentState, &evidenceState, &fingerprint); err != nil {
		return 0, ErrCredentialBindingNotCurrent
	}
	if enrollmentRevision != credentialEnrollmentRevision || enrollmentState != "ENROLLED" || identityState != "APPROVED" || currentState != "CURRENT" || evidenceState != "ACTIVE" || fingerprint != certificateFingerprint {
		return 0, ErrCredentialBindingNotCurrent
	}
	return enrollmentRevision, nil
}

func refreshHostSessionAuthorizationTx(ctx context.Context, tx pgx.Tx, hostID string) error {
	var sessionGeneration, credentialRevision, enrollmentRevision int64
	var attemptID, sessionState string
	if err := tx.QueryRow(ctx, `
		SELECT session_generation, credential_binding_revision, current_session_attempt_id, state
		FROM kim.agent_transport_sessions_current WHERE host_id=$1
	`, hostID).Scan(&sessionGeneration, &credentialRevision, &attemptID, &sessionState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	var bindingState, evidenceState, enrollmentState, identityState string
	var credentialEnrollmentRevision int64
	var credentialTimeValid bool
	if err := tx.QueryRow(ctx, `
		SELECT current.binding_state, evidence.binding_state,
		       enrollment.binding_state, host.enrollment_state, enrollment.decision_revision,
		       evidence.enrollment_decision_revision,
		       statement_timestamp() >= evidence.valid_not_before AND statement_timestamp() < evidence.valid_not_after
		FROM kim.agent_credential_bindings_current current
		JOIN kim.agent_credential_binding_evidence evidence
		  ON evidence.host_id=current.host_id AND evidence.binding_revision=current.binding_revision
		JOIN kim.host_enrollment_bindings_current enrollment ON enrollment.host_id=current.host_id
		JOIN kim.host_identities host ON host.host_id=current.host_id
		WHERE current.host_id=$1 AND current.binding_revision=$2
	`, hostID, credentialRevision).Scan(&bindingState, &evidenceState, &enrollmentState, &identityState, &enrollmentRevision, &credentialEnrollmentRevision, &credentialTimeValid); err != nil {
		return ErrSessionNotAuthorized
	}
	state, reason := "PENDING_CAPABILITY", "current_capability_required"
	var capabilityGeneration *int64
	if sessionState != "CURRENT" || bindingState != "CURRENT" || evidenceState != "ACTIVE" || !credentialTimeValid || enrollmentState != "ENROLLED" || identityState != "APPROVED" || enrollmentRevision != credentialEnrollmentRevision {
		state, reason = "FENCED", "identity_or_session_not_current"
	} else {
		var generation int64
		var projectionState string
		err := tx.QueryRow(ctx, `SELECT observation_generation, projection_state FROM kim.host_capability_projections WHERE host_id=$1`, hostID).Scan(&generation, &projectionState)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case projectionState == "CURRENT":
			capabilityGeneration = &generation
			state, reason = "AUTHORIZED", "identity_session_and_capability_current"
		default:
			capabilityGeneration = &generation
			state, reason = "PENDING_CAPABILITY", "capability_projection_not_current"
		}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO kim.host_session_authorizations_current (
			host_id, session_generation, session_attempt_id, credential_binding_revision,
			enrollment_decision_revision, capability_generation, authorization_state, reason_code
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (host_id) DO UPDATE SET
			session_generation=EXCLUDED.session_generation,
			session_attempt_id=EXCLUDED.session_attempt_id,
			credential_binding_revision=EXCLUDED.credential_binding_revision,
			enrollment_decision_revision=EXCLUDED.enrollment_decision_revision,
			capability_generation=EXCLUDED.capability_generation,
			authorization_state=EXCLUDED.authorization_state,
			reason_code=EXCLUDED.reason_code,
			evaluated_at=statement_timestamp()
	`, hostID, sessionGeneration, attemptID, credentialRevision, enrollmentRevision, capabilityGeneration, state, reason)
	return err
}

func UpdateHostReadinessGate(ctx context.Context, db TxBeginner, gate HostReadinessGate) error {
	if gate.HostID == "" || gate.CapabilityGeneration < 1 || gate.BaselineAssignmentGeneration < 1 || gate.PreflightGeneration < 1 || gate.ComplianceGeneration < 1 {
		return errors.New("complete Host readiness gate is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockHostAuthorityTx(ctx, tx, gate.HostID); err != nil {
			return err
		}
		var currentGeneration int64
		var projectionState string
		if err := tx.QueryRow(ctx, `SELECT observation_generation, projection_state FROM kim.host_capability_projections WHERE host_id=$1`, gate.HostID).Scan(&currentGeneration, &projectionState); err != nil {
			return ErrHostAuthorityNotReady
		}
		gateState := "BLOCKED"
		if gate.PreflightState == "UNKNOWN" || gate.ComplianceState == "UNKNOWN" || projectionState != "CURRENT" || currentGeneration != gate.CapabilityGeneration {
			gateState = "UNKNOWN"
		} else if gate.PreflightState == "PASSED" && gate.ComplianceState == "COMPLIANT" {
			gateState = "READY"
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_readiness_gates_current (
				host_id, capability_generation, baseline_assignment_generation,
				preflight_generation, preflight_state, compliance_generation,
				compliance_state, gate_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (host_id) DO UPDATE SET
				capability_generation=EXCLUDED.capability_generation,
				baseline_assignment_generation=EXCLUDED.baseline_assignment_generation,
				preflight_generation=EXCLUDED.preflight_generation,
				preflight_state=EXCLUDED.preflight_state,
				compliance_generation=EXCLUDED.compliance_generation,
				compliance_state=EXCLUDED.compliance_state,
				gate_state=EXCLUDED.gate_state,
				updated_at=statement_timestamp()
			WHERE ROW(
				host_readiness_gates_current.capability_generation,
				host_readiness_gates_current.baseline_assignment_generation,
				host_readiness_gates_current.preflight_generation,
				host_readiness_gates_current.preflight_state,
				host_readiness_gates_current.compliance_generation,
				host_readiness_gates_current.compliance_state,
				host_readiness_gates_current.gate_state
			) IS DISTINCT FROM ROW(
				EXCLUDED.capability_generation, EXCLUDED.baseline_assignment_generation,
				EXCLUDED.preflight_generation, EXCLUDED.preflight_state,
				EXCLUDED.compliance_generation, EXCLUDED.compliance_state,
				EXCLUDED.gate_state
			)
		`, gate.HostID, gate.CapabilityGeneration, gate.BaselineAssignmentGeneration,
			gate.PreflightGeneration, gate.PreflightState, gate.ComplianceGeneration,
			gate.ComplianceState, gateState)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return fenceHostOperationAuthorityTx(ctx, tx, gate.HostID, "readiness_gate_changed")
	})
}

func ArmHostOperationAuthority(ctx context.Context, db TxBeginner, request HostAuthorityArmRequest) (HostMutationAuthority, error) {
	if request.HostID == "" || request.PolicyID == "" || request.PolicyGeneration < 1 || request.ActorID == "" || request.ReasonCode == "" {
		return HostMutationAuthority{}, errors.New("complete Host authority arming request is required")
	}
	var authority HostMutationAuthority
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockHostAuthorityTx(ctx, tx, request.HostID); err != nil {
			return err
		}
		var databaseMode, enrollmentState, enrollmentBindingState, credentialState, credentialEvidenceState, sessionState, authorizationState, gateState, preflightState, complianceState string
		var credentialTimeValid bool
		if err := tx.QueryRow(ctx, `
			SELECT database.mode, host.enrollment_state, enrollment.binding_state, enrollment.decision_revision,
			       credential.binding_revision, credential.binding_state,
			       credential_evidence.binding_state,
			       statement_timestamp() >= credential_evidence.valid_not_before AND statement_timestamp() < credential_evidence.valid_not_after,
			       session.session_generation, session.state,
			       session_auth.authorization_state, session_auth.capability_generation,
			       gates.baseline_assignment_generation, gates.preflight_generation,
			       gates.compliance_generation, gates.gate_state,
			       gates.preflight_state, gates.compliance_state
			FROM kim.database_authority database
			JOIN kim.host_identities host ON true
			JOIN kim.host_enrollment_bindings_current enrollment ON enrollment.host_id=host.host_id
			JOIN kim.agent_credential_bindings_current credential ON credential.host_id=host.host_id
			JOIN kim.agent_credential_binding_evidence credential_evidence ON credential_evidence.host_id=credential.host_id AND credential_evidence.binding_revision=credential.binding_revision
			JOIN kim.agent_transport_sessions_current session ON session.host_id=host.host_id
			JOIN kim.host_session_authorizations_current session_auth ON session_auth.host_id=host.host_id
			JOIN kim.host_readiness_gates_current gates ON gates.host_id=host.host_id
			JOIN kim.host_capability_projections capability ON capability.host_id=host.host_id
			WHERE database.singleton AND host.host_id=$1
		`, request.HostID).Scan(
			&databaseMode, &enrollmentState, &enrollmentBindingState, &authority.EnrollmentDecisionRevision,
			&authority.CredentialBindingRevision, &credentialState, &credentialEvidenceState, &credentialTimeValid,
			&authority.SessionGeneration, &sessionState,
			&authorizationState, &authority.CapabilityGeneration,
			&authority.BaselineAssignmentGeneration, &authority.PreflightGeneration,
			&authority.ComplianceGeneration, &gateState, &preflightState, &complianceState,
		); err != nil {
			return ErrHostAuthorityNotReady
		}
		var authorizedSessionGeneration, authorizedCredentialRevision, authorizedEnrollmentRevision int64
		if err := tx.QueryRow(ctx, `
			SELECT session_generation, credential_binding_revision, enrollment_decision_revision
			FROM kim.host_session_authorizations_current WHERE host_id=$1
		`, request.HostID).Scan(&authorizedSessionGeneration, &authorizedCredentialRevision, &authorizedEnrollmentRevision); err != nil {
			return ErrHostAuthorityNotReady
		}
		var gateCapabilityGeneration int64
		if err := tx.QueryRow(ctx, `SELECT capability_generation FROM kim.host_readiness_gates_current WHERE host_id=$1`, request.HostID).Scan(&gateCapabilityGeneration); err != nil {
			return ErrHostAuthorityNotReady
		}
		var currentCapabilityGeneration int64
		var currentCapabilityState string
		if err := tx.QueryRow(ctx, `SELECT observation_generation, projection_state FROM kim.host_capability_projections WHERE host_id=$1`, request.HostID).Scan(&currentCapabilityGeneration, &currentCapabilityState); err != nil {
			return ErrHostAuthorityNotReady
		}
		if databaseMode != "ACTIVE" || enrollmentState != "APPROVED" || enrollmentBindingState != "ENROLLED" || credentialState != "CURRENT" || credentialEvidenceState != "ACTIVE" || !credentialTimeValid || sessionState != "CURRENT" || authorizationState != "AUTHORIZED" || gateState != "READY" || preflightState != "PASSED" || complianceState != "COMPLIANT" || authorizedSessionGeneration != authority.SessionGeneration || authorizedCredentialRevision != authority.CredentialBindingRevision || authorizedEnrollmentRevision != authority.EnrollmentDecisionRevision || gateCapabilityGeneration != authority.CapabilityGeneration || currentCapabilityGeneration != authority.CapabilityGeneration || currentCapabilityState != "CURRENT" {
			return ErrHostAuthorityNotReady
		}
		if err := tx.QueryRow(ctx, `UPDATE kim.host_identities SET host_authority_generation=host_authority_generation+1, updated_at=statement_timestamp() WHERE host_id=$1 RETURNING host_authority_generation`, request.HostID).Scan(&authority.AuthorityGeneration); err != nil {
			return err
		}
		authority.HostID = request.HostID
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.host_operation_authorities_current (
				host_id, authority_generation, authority_state, session_generation,
				credential_binding_revision, enrollment_decision_revision, capability_generation,
				baseline_assignment_generation, preflight_generation, compliance_generation,
				policy_id, policy_generation, armed_by, reason_code
			) VALUES ($1,$2,'ARMED',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (host_id) DO UPDATE SET
				authority_generation=EXCLUDED.authority_generation,
				authority_state='ARMED', session_generation=EXCLUDED.session_generation,
				credential_binding_revision=EXCLUDED.credential_binding_revision,
				enrollment_decision_revision=EXCLUDED.enrollment_decision_revision,
				capability_generation=EXCLUDED.capability_generation,
				baseline_assignment_generation=EXCLUDED.baseline_assignment_generation,
				preflight_generation=EXCLUDED.preflight_generation,
				compliance_generation=EXCLUDED.compliance_generation,
				policy_id=EXCLUDED.policy_id, policy_generation=EXCLUDED.policy_generation,
				armed_by=EXCLUDED.armed_by, reason_code=EXCLUDED.reason_code,
				updated_at=statement_timestamp()
		`, request.HostID, authority.AuthorityGeneration, authority.SessionGeneration,
			authority.CredentialBindingRevision, authority.EnrollmentDecisionRevision,
			authority.CapabilityGeneration, authority.BaselineAssignmentGeneration,
			authority.PreflightGeneration, authority.ComplianceGeneration,
			request.PolicyID, request.PolicyGeneration, request.ActorID, request.ReasonCode); err != nil {
			return err
		}
		return appendHostAuthorityEventTx(ctx, tx, request.HostID, authority.AuthorityGeneration, "ARMED", request.ReasonCode, map[string]any{"actor_id": request.ActorID, "policy_id": request.PolicyID, "policy_generation": request.PolicyGeneration})
	})
	return authority, err
}

func AuthorizeHostMutation(ctx context.Context, db TxBeginner, hostID string, authorityGeneration int64) (HostMutationAuthority, error) {
	var authority HostMutationAuthority
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
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
			return ErrHostAuthorityNotArmed
		}
		return nil
	})
	if err != nil {
		return HostMutationAuthority{}, ErrHostAuthorityNotArmed
	}
	return authority, nil
}

func lockHostAuthorityTx(ctx context.Context, tx pgx.Tx, hostID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, hostID)
	return err
}

func fenceHostOperationAuthorityTx(ctx context.Context, tx pgx.Tx, hostID, reason string) error {
	var generation int64
	err := tx.QueryRow(ctx, `
		UPDATE kim.host_operation_authorities_current
		SET authority_state='FENCED', reason_code=$2, updated_at=statement_timestamp()
		WHERE host_id=$1 AND authority_state='ARMED'
		RETURNING authority_generation
	`, hostID, reason).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return fenceHostCommandLeasesTx(ctx, tx, hostID, reason)
	}
	if err != nil {
		return err
	}
	if err := appendHostAuthorityEventTx(ctx, tx, hostID, generation, "FENCED", reason, map[string]any{"reason": reason}); err != nil {
		return err
	}
	return fenceHostCommandLeasesTx(ctx, tx, hostID, reason)
}

func appendHostAuthorityEventTx(ctx context.Context, tx pgx.Tx, hostID string, generation int64, eventType, reason string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.host_operation_authority_events (
			host_id, authority_generation, event_sequence, event_type,
			reason_code, event_payload, event_payload_digest
		) SELECT $1,$2,COALESCE(max(event_sequence),0)+1,$3,$4,$5,$6
		FROM kim.host_operation_authority_events
		WHERE host_id=$1 AND authority_generation=$2
	`, hostID, generation, eventType, reason, encoded, digestBytes(encoded))
	return err
}
