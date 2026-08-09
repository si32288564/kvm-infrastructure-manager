package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func prepareSessionIdentityFixture(t *testing.T, ctx context.Context, db TxBeginner, hostID string, revision int64, fingerprint string) {
	t.Helper()
	if revision == 1 {
		if err := RegisterDiscoveredHost(ctx, db, hostID); err != nil {
			t.Fatal(err)
		}
		if err := RecordEnrollmentDecision(ctx, db, EnrollmentDecision{
			DecisionID: hostID + "-enrollment-1", HostID: hostID, Revision: 1,
			PolicyID: "integration", PolicyGeneration: 1,
			HardwareEvidenceDigest: digestBytes([]byte(hostID + "-hardware")),
			State:                  "APPROVED", ActorID: "integration", ReasonCode: "fixture",
		}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if err := RecordAgentCredentialBinding(ctx, db, AgentCredentialBindingEvidence{
		HostID: hostID, Revision: revision, CertificateFingerprint: fingerprint,
		PublicKeyDigest: digestBytes([]byte(fmt.Sprintf("%s-key-%d", hostID, revision))),
		IssuerID:        "integration-ca", ProfileRevision: "host-agent/v1", TrustGeneration: 1,
		EnrollmentDecisionID: hostID + "-enrollment-1", EnrollmentRevision: 1,
		EvidenceDigest: digestBytes([]byte(fmt.Sprintf("%s-binding-%d", hostID, revision))),
		State:          "ACTIVE", ValidNotBefore: now.Add(-time.Hour), ValidNotAfter: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}
