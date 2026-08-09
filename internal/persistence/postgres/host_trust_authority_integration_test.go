package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestHostTrustSessionAuthorizationAndExplicitArmingPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch, authority_generation, mode)
		VALUES ('host-trust-test', 1, 'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}
	hostID := fmt.Sprintf("host-trust-%d", time.Now().UnixNano())
	certificateFingerprint := digestBytes([]byte("agent-certificate"))
	if err := RegisterDiscoveredHost(ctx, pool, hostID); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 1, 1, certificateFingerprint)); !errors.Is(err, ErrCredentialBindingNotCurrent) {
		t.Fatalf("discovered Host session admission error = %v", err)
	}

	recordEnrollment(t, ctx, pool, hostID, 1, "APPROVED")
	recordCredential(t, ctx, pool, hostID, 1, 1, certificateFingerprint)
	firstGrant, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 1, 1, certificateFingerprint))
	if err != nil || firstGrant.SessionGeneration != 1 {
		t.Fatalf("first session grant = %#v, error = %v", firstGrant, err)
	}
	assertSessionAuthorization(t, ctx, pool, hostID, "PENDING_CAPABILITY", 0)
	assertAuthorityState(t, ctx, pool, hostID, "", 0)

	acceptTrustInventory(t, ctx, pool, hostID, 1, 1)
	assertSessionAuthorization(t, ctx, pool, hostID, "AUTHORIZED", 1)
	assertAuthorityState(t, ctx, pool, hostID, "", 0)
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{
		HostID: hostID, CapabilityGeneration: 1, BaselineAssignmentGeneration: 1,
		PreflightGeneration: 1, PreflightState: "PASSED",
		ComplianceGeneration: 1, ComplianceState: "COMPLIANT",
	}); err != nil {
		t.Fatal(err)
	}
	assertAuthorityState(t, ctx, pool, hostID, "", 0)
	firstAuthority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "initial_arm"})
	if err != nil || firstAuthority.AuthorityGeneration != 1 {
		t.Fatalf("first authority = %#v, error = %v", firstAuthority, err)
	}
	if _, err := AuthorizeHostMutation(ctx, pool, hostID, 1); err != nil {
		t.Fatal(err)
	}

	// Renewal creates a new Credential Binding revision but never rearms the
	// already-authorized Host mutation authority.
	recordCredential(t, ctx, pool, hostID, 2, 1, certificateFingerprint)
	assertSessionAuthorization(t, ctx, pool, hostID, "STALE", 1)
	assertAuthorityState(t, ctx, pool, hostID, "FENCED", 1)
	if _, err := AuthorizeHostMutation(ctx, pool, hostID, 1); !errors.Is(err, ErrHostAuthorityNotArmed) {
		t.Fatalf("renewal preserved mutation authority: %v", err)
	}
	if _, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 2, 1, certificateFingerprint)); !errors.Is(err, ErrCredentialBindingNotCurrent) {
		t.Fatalf("stale certificate binding admitted: %v", err)
	}
	secondGrant, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 2, 2, certificateFingerprint))
	if err != nil || secondGrant.SessionGeneration != 2 {
		t.Fatalf("renewed session grant = %#v, error = %v", secondGrant, err)
	}
	assertSessionAuthorization(t, ctx, pool, hostID, "AUTHORIZED", 1)
	assertAuthorityState(t, ctx, pool, hostID, "FENCED", 1)
	secondAuthority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "post_renewal_arm"})
	if err != nil || secondAuthority.AuthorityGeneration != 2 {
		t.Fatalf("second authority = %#v, error = %v", secondAuthority, err)
	}
	thirdGrant, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 3, 2, certificateFingerprint))
	if err != nil || thirdGrant.SessionGeneration != 3 {
		t.Fatalf("reconnected session grant = %#v, error = %v", thirdGrant, err)
	}
	assertSessionAuthorization(t, ctx, pool, hostID, "AUTHORIZED", 1)
	assertAuthorityState(t, ctx, pool, hostID, "FENCED", 2)
	thirdAuthority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "post_reconnect_arm"})
	if err != nil || thirdAuthority.AuthorityGeneration != 3 {
		t.Fatalf("third authority = %#v, error = %v", thirdAuthority, err)
	}

	// A fresh capability generation refreshes session authorization evidence,
	// but fences mutation authority until readiness and explicit arming repeat.
	acceptTrustInventory(t, ctx, pool, hostID, 2, 3)
	assertSessionAuthorization(t, ctx, pool, hostID, "AUTHORIZED", 2)
	assertAuthorityState(t, ctx, pool, hostID, "FENCED", 3)
	if _, err := AuthorizeHostMutation(ctx, pool, hostID, 3); !errors.Is(err, ErrHostAuthorityNotArmed) {
		t.Fatalf("capability change implicitly rearmed Host: %v", err)
	}
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{
		HostID: hostID, CapabilityGeneration: 2, BaselineAssignmentGeneration: 1,
		PreflightGeneration: 2, PreflightState: "PASSED",
		ComplianceGeneration: 2, ComplianceState: "COMPLIANT",
	}); err != nil {
		t.Fatal(err)
	}
	fourthAuthority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "capability_revalidated"})
	if err != nil || fourthAuthority.AuthorityGeneration != 4 {
		t.Fatalf("fourth authority = %#v, error = %v", fourthAuthority, err)
	}

	// Compliance drift is another independent fail-closed gate. Updating the
	// current summary can fence, but can never increment/rearm authority.
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{
		HostID: hostID, CapabilityGeneration: 2, BaselineAssignmentGeneration: 1,
		PreflightGeneration: 2, PreflightState: "PASSED",
		ComplianceGeneration: 3, ComplianceState: "NON_COMPLIANT",
	}); err != nil {
		t.Fatal(err)
	}
	assertAuthorityState(t, ctx, pool, hostID, "FENCED", 4)
	if _, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "must_fail"}); !errors.Is(err, ErrHostAuthorityNotReady) {
		t.Fatalf("non-compliant Host armed: %v", err)
	}
	if err := UpdateHostReadinessGate(ctx, pool, HostReadinessGate{
		HostID: hostID, CapabilityGeneration: 2, BaselineAssignmentGeneration: 1,
		PreflightGeneration: 2, PreflightState: "PASSED",
		ComplianceGeneration: 4, ComplianceState: "COMPLIANT",
	}); err != nil {
		t.Fatal(err)
	}
	fifthAuthority, err := ArmHostOperationAuthority(ctx, pool, HostAuthorityArmRequest{HostID: hostID, PolicyID: "manual", PolicyGeneration: 1, ActorID: "operator", ReasonCode: "compliance_revalidated"})
	if err != nil || fifthAuthority.AuthorityGeneration != 5 {
		t.Fatalf("fifth authority = %#v, error = %v", fifthAuthority, err)
	}

	// Enrollment quarantine fences both transport/session authorization and
	// mutation authority. Later approval and a new credential remain unarmed.
	recordEnrollment(t, ctx, pool, hostID, 2, "QUARANTINED")
	assertSessionAuthorization(t, ctx, pool, hostID, "FENCED", 2)
	assertAuthorityState(t, ctx, pool, hostID, "FENCED", 5)
	recordEnrollment(t, ctx, pool, hostID, 3, "APPROVED")
	recordCredential(t, ctx, pool, hostID, 3, 3, certificateFingerprint)
	fourthGrant, err := AdmitAgentSession(ctx, pool, trustSession(hostID, 4, 3, certificateFingerprint))
	if err != nil || fourthGrant.SessionGeneration != 4 {
		t.Fatalf("reenrolled session grant = %#v, error = %v", fourthGrant, err)
	}
	assertSessionAuthorization(t, ctx, pool, hostID, "AUTHORIZED", 2)
	assertAuthorityState(t, ctx, pool, hostID, "FENCED", 5)
}

func trustSession(hostID string, generation, credentialRevision int64, fingerprint string) AgentSessionAdmission {
	return AgentSessionAdmission{
		SessionAttemptID: fmt.Sprintf("%s-attempt-%d", hostID, generation), HostID: hostID,
		ConnectionInstanceID: fmt.Sprintf("connection-%d", generation), TransportProfile: "integration",
		ProtocolVersion: "v1", AgentArtifactDigest: digestBytes([]byte("agent")),
		CredentialBindingRevision: credentialRevision, PeerCertificateFingerprint: fingerprint,
		ExpectedSessionGeneration: generation,
	}
}

func recordEnrollment(t *testing.T, ctx context.Context, db TxBeginner, hostID string, revision int64, state string) {
	t.Helper()
	if err := RecordEnrollmentDecision(ctx, db, EnrollmentDecision{
		DecisionID: fmt.Sprintf("%s-enrollment-%d", hostID, revision), HostID: hostID, Revision: revision,
		PolicyID: "manual-enrollment", PolicyGeneration: 1,
		HardwareEvidenceDigest: digestBytes([]byte(fmt.Sprintf("hardware-%d", revision))),
		State:                  state, ActorID: "operator", ReasonCode: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
}

func recordCredential(t *testing.T, ctx context.Context, db TxBeginner, hostID string, revision, enrollmentRevision int64, fingerprint string) {
	t.Helper()
	now := time.Now().UTC()
	if err := RecordAgentCredentialBinding(ctx, db, AgentCredentialBindingEvidence{
		HostID: hostID, Revision: revision, CertificateFingerprint: fingerprint,
		PublicKeyDigest: digestBytes([]byte(fmt.Sprintf("key-%d", revision))), IssuerID: "fixture-ca",
		ProfileRevision: "host-agent/v1", TrustGeneration: 1,
		EnrollmentDecisionID: fmt.Sprintf("%s-enrollment-%d", hostID, enrollmentRevision),
		EnrollmentRevision:   enrollmentRevision, EvidenceDigest: digestBytes([]byte(fmt.Sprintf("credential-%d", revision))),
		State: "ACTIVE", ValidNotBefore: now.Add(-time.Hour), ValidNotAfter: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func acceptTrustInventory(t *testing.T, ctx context.Context, db TxBeginner, hostID string, generation, sessionGeneration uint64) {
	t.Helper()
	snapshot := inventoryFixture(t, hostID, generation, "COMPLETE", "kim.host.kvm.v1")
	envelope, err := agentinventory.NewEnvelope(snapshot, sessionGeneration, fmt.Sprintf("%s-inventory-%d", hostID, generation))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptHostInventory(ctx, db, envelope, 1<<20); err != nil {
		t.Fatal(err)
	}
}

func assertSessionAuthorization(t *testing.T, ctx context.Context, db TxBeginner, hostID, wantState string, wantCapability int64) {
	t.Helper()
	var state string
	var capability *int64
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT authorization_state, capability_generation FROM kim.host_session_authorizations_current WHERE host_id=$1`, hostID).Scan(&state, &capability)
	})
	if err != nil {
		t.Fatal(err)
	}
	if state != wantState || (capability == nil && wantCapability != 0) || (capability != nil && *capability != wantCapability) {
		t.Fatalf("session authorization state/capability = %s/%v, want %s/%d", state, capability, wantState, wantCapability)
	}
}

func assertAuthorityState(t *testing.T, ctx context.Context, db TxBeginner, hostID, wantState string, wantGeneration int64) {
	t.Helper()
	var state string
	var generation int64
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT authority_state, authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1`, hostID).Scan(&state, &generation)
	})
	if wantState == "" && errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if state != wantState || generation != wantGeneration {
		t.Fatalf("Host authority state/generation = %s/%d, want %s/%d", state, generation, wantState, wantGeneration)
	}
}
