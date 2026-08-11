package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

func TestHostGroupExternalAssertionAuthorityPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch,authority_generation,mode)
		VALUES ('host-group-external-assertion-test',1,'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	groupID, issuerID := "external-group-"+suffix, "external-issuer-"+suffix
	hostA, hostB, hostC := "external-a-"+suffix, "external-b-"+suffix, "external-c-"+suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state)
		VALUES($1,'APPROVED'),($2,'APPROVED'),($3,'APPROVED')`, hostA, hostB, hostC); err != nil {
		t.Fatal(err)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 1,
		GroupType: "OPERATIONAL_COHORT", Dimension: "external-assertion-" + suffix,
		Level: "cohort", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	publicKey1, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey2, privateKey2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publishIssuer := func(generation, expected uint64, lifecycle string, key ed25519.PublicKey) {
		t.Helper()
		if err := PublishExternalAssertionIssuer(ctx, pool, ExternalAssertionIssuerRevision{
			PublishRequestID: fmt.Sprintf("issuer-%d-%s", generation, suffix), IssuerID: issuerID,
			IssuerGeneration: generation, ExpectedCurrentGeneration: expected,
			LifecycleState: lifecycle, PublicKey: key,
			Scopes: []ExternalAssertionIssuerScope{{HostGroupID: groupID, HostGroupGeneration: 1}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	publishIssuer(1, 0, "TRUSTED", publicKey1)
	if err := AssignHostGroupMembership(ctx, pool, HostGroupMembership{
		HostGroupID: groupID, HostID: hostA, Generation: 1, State: "ACTIVE",
		SourceType: "EXTERNAL_ASSERTION", SourceRevision: "unverified-direct-write",
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("direct external membership write = %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	assertion := func(id, nonce string, members []string, key ed25519.PrivateKey) ExternalHostGroupAssertion {
		t.Helper()
		value := ExternalHostGroupAssertion{
			AssertionID: id, IssuerID: issuerID, SchemaVersion: ExternalHostGroupAssertionSchema,
			SubjectType: ExternalHostGroupAssertionSubject, HostGroupID: groupID, HostGroupGeneration: 1,
			Audience: ExternalHostGroupAssertionAudience, Nonce: nonce,
			IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Members: members,
		}
		payload, digest, err := CanonicalExternalHostGroupAssertionPayload(value)
		if err != nil {
			t.Fatal(err)
		}
		value.PayloadDigest = digest
		value.Signature = ed25519.Sign(key, payload)
		return value
	}
	verifyResult := func(value ExternalHostGroupAssertion, want string) ExternalAssertionVerification {
		t.Helper()
		got, err := VerifyExternalHostGroupAssertion(ctx, pool, value)
		if err != nil {
			t.Fatal(err)
		}
		if got.VerificationResult != want {
			t.Fatalf("assertion %s result = %s, want %s", value.AssertionID, got.VerificationResult, want)
		}
		return got
	}

	valid1 := assertion("assertion-1-"+suffix, "nonce-1-"+suffix, []string{hostA, hostB}, privateKey1)
	verified1 := verifyResult(valid1, "VERIFIED")
	var currentSets int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_group_membership_sets_current WHERE host_group_id=$1`, groupID).Scan(&currentSets); err != nil || currentSets != 0 {
		t.Fatalf("verification became membership authority: count=%d err=%v", currentSets, err)
	}
	set1, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "external-set-1-" + suffix, AssertionID: valid1.AssertionID,
		ExpectedCurrentSetGeneration: 0,
	})
	if err != nil || set1.MembershipSetGeneration != 1 || set1.MemberCount != 2 {
		t.Fatalf("materialize set 1 = %#v/%v", set1, err)
	}
	var assertionID, payloadDigest, verificationDigest string
	if err := pool.QueryRow(ctx, `SELECT external_assertion_id,external_assertion_payload_digest,
		external_assertion_verification_digest FROM kim.host_group_membership_set_evidence
		WHERE host_group_id=$1 AND membership_set_generation=1`, groupID).Scan(
		&assertionID, &payloadDigest, &verificationDigest); err != nil {
		t.Fatal(err)
	}
	if assertionID != valid1.AssertionID || payloadDigest != valid1.PayloadDigest ||
		verificationDigest != verified1.VerificationDigest {
		t.Fatal("membership Set did not preserve exact assertion provenance")
	}

	// Stable assertion replay returns the original immutable decision.
	replayedVerification := verifyResult(valid1, "VERIFIED")
	if replayedVerification.VerificationDigest != verified1.VerificationDigest {
		t.Fatal("exact verification replay created a new decision")
	}

	invalidSignature := assertion("invalid-signature-"+suffix, "nonce-invalid-signature-"+suffix, []string{hostA}, privateKey1)
	invalidSignature.Signature[0] ^= 0xff
	verifyResult(invalidSignature, "INVALID_SIGNATURE")
	if _, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "invalid-materialize-" + suffix, AssertionID: invalidSignature.AssertionID,
		ExpectedCurrentSetGeneration: 1,
	}); !errors.Is(err, ErrExternalAssertionConflict) {
		t.Fatalf("invalid signature materialization = %v", err)
	}
	expired := assertion("expired-"+suffix, "nonce-expired-"+suffix, []string{hostA}, privateKey1)
	expired.IssuedAt, expired.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
	payload, digest, err := CanonicalExternalHostGroupAssertionPayload(expired)
	if err != nil {
		t.Fatal(err)
	}
	expired.PayloadDigest, expired.Signature = digest, ed25519.Sign(privateKey1, payload)
	verifyResult(expired, "EXPIRED")
	wrongAudience := assertion("audience-"+suffix, "nonce-audience-"+suffix, []string{hostA}, privateKey1)
	wrongAudience.Audience = "another-control-plane"
	payload, digest, _ = CanonicalExternalHostGroupAssertionPayload(wrongAudience)
	wrongAudience.PayloadDigest, wrongAudience.Signature = digest, ed25519.Sign(privateKey1, payload)
	verifyResult(wrongAudience, "AUDIENCE_MISMATCH")
	unsupported := assertion("unsupported-"+suffix, "nonce-unsupported-"+suffix, []string{hostA}, privateKey1)
	unsupported.SchemaVersion = "kim.host-group.external-assertion/v2"
	payload, digest, _ = CanonicalExternalHostGroupAssertionPayload(unsupported)
	unsupported.PayloadDigest, unsupported.Signature = digest, ed25519.Sign(privateKey1, payload)
	verifyResult(unsupported, "UNSUPPORTED_SCHEMA")
	unknownHost := assertion("unknown-host-"+suffix, "nonce-unknown-host-"+suffix, []string{"missing-" + suffix}, privateKey1)
	verifyResult(unknownHost, "UNKNOWN_HOST")

	unknownIssuer := assertion("unknown-issuer-"+suffix, "nonce-unknown-issuer-"+suffix, []string{hostA}, privateKey1)
	unknownIssuer.IssuerID = "not-trusted-" + suffix
	payload, digest, _ = CanonicalExternalHostGroupAssertionPayload(unknownIssuer)
	unknownIssuer.PayloadDigest, unknownIssuer.Signature = digest, ed25519.Sign(privateKey1, payload)
	verifyResult(unknownIssuer, "UNTRUSTED_ISSUER")

	// Same assertion identity with changed semantics is conflict evidence; the original remains immutable.
	conflictingIdentity := valid1
	conflictingIdentity.Members = []string{hostC}
	conflictingIdentity.Nonce = "changed-nonce-" + suffix
	payload, digest, _ = CanonicalExternalHostGroupAssertionPayload(conflictingIdentity)
	conflictingIdentity.PayloadDigest, conflictingIdentity.Signature = digest, ed25519.Sign(privateKey1, payload)
	verifyResult(conflictingIdentity, "REPLAY_CONFLICT")
	verifyResult(valid1, "VERIFIED")
	sameNonce := assertion("same-nonce-"+suffix, valid1.Nonce, []string{hostC}, privateKey1)
	verifyResult(sameNonce, "REPLAY_CONFLICT")

	// Parallel identical verification converges to one evidence row.
	parallel := assertion("parallel-"+suffix, "nonce-parallel-"+suffix, []string{hostA, hostC}, privateKey1)
	var wait sync.WaitGroup
	results := make(chan ExternalAssertionVerification, 2)
	errorsOut := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := VerifyExternalHostGroupAssertion(ctx, pool, parallel)
			if err != nil {
				errorsOut <- err
				return
			}
			results <- value
		}()
	}
	wait.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Fatal(err)
	}
	first, second := <-results, <-results
	if first.VerificationResult != "VERIFIED" || first.VerificationDigest != second.VerificationDigest {
		t.Fatalf("parallel verification did not converge: %#v / %#v", first, second)
	}
	var parallelEvidence int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kim.host_group_external_assertion_evidence WHERE assertion_id=$1`, parallel.AssertionID).Scan(&parallelEvidence); err != nil || parallelEvidence != 1 {
		t.Fatalf("parallel evidence count=%d err=%v", parallelEvidence, err)
	}

	// Source authority can switch only through complete Membership Set publication.
	explicitSet, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "explicit-set-" + suffix, HostGroupID: groupID,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 1,
		SourceType: "EXPLICIT", SourceRevision: "operator-" + suffix,
		Members: []HostGroupMembership{{HostGroupID: groupID, HostID: hostA, Generation: 2,
			State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: "operator-" + suffix}},
	})
	if err != nil || explicitSet.MembershipSetGeneration != 2 {
		t.Fatalf("external to explicit source switch = %#v/%v", explicitSet, err)
	}

	publishIssuer(2, 1, "TRUSTED", publicKey2)
	oldKey := assertion("old-key-"+suffix, "nonce-old-key-"+suffix, []string{hostB}, privateKey1)
	verifyResult(oldKey, "INVALID_SIGNATURE")
	valid2 := assertion("assertion-2-"+suffix, "nonce-2-"+suffix, []string{hostB, hostC}, privateKey2)
	verifyResult(valid2, "VERIFIED")
	set3, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "external-set-3-" + suffix, AssertionID: valid2.AssertionID,
		ExpectedCurrentSetGeneration: 2,
	})
	if err != nil || set3.MembershipSetGeneration != 3 {
		t.Fatalf("explicit to external source switch = %#v/%v", set3, err)
	}
	pending := assertion("pending-before-revoke-"+suffix, "nonce-pending-"+suffix, []string{hostA}, privateKey2)
	verifyResult(pending, "VERIFIED")
	publishIssuer(3, 2, "REVOKED", publicKey2)
	if _, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "pending-after-revoke-" + suffix, AssertionID: pending.AssertionID,
		ExpectedCurrentSetGeneration: 3,
	}); !errors.Is(err, ErrExternalAssertionConflict) {
		t.Fatalf("revoked issuer materialization = %v", err)
	}
	// A lost success response may be replayed after distrust without re-authorizing anything.
	replayedSet, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "external-set-3-" + suffix, AssertionID: valid2.AssertionID,
		ExpectedCurrentSetGeneration: 2,
	})
	if err != nil || replayedSet.MembershipSetGeneration != 3 {
		t.Fatalf("materialization response-loss replay = %#v/%v", replayedSet, err)
	}
	var currentGeneration uint64
	if err := pool.QueryRow(ctx, `SELECT membership_set_generation FROM kim.host_group_membership_sets_current WHERE host_group_id=$1`, groupID).Scan(&currentGeneration); err != nil || currentGeneration != 3 {
		t.Fatalf("issuer distrust rewrote historical/current Set: generation=%d err=%v", currentGeneration, err)
	}
}

func TestHostGroupExternalAssertionGenerationAndPublisherFencingPostgreSQLIntegration(t *testing.T) {
	databaseURL := os.Getenv("KIM_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("KIM_POSTGRES_TEST_URL is not set")
	}
	ctx := context.Background()
	pool, err := OpenWithMaxConnections(ctx, databaseURL, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kim.database_authority (restore_epoch,authority_generation,mode)
		VALUES ('host-group-external-assertion-fencing-test',1,'ACTIVE')
		ON CONFLICT (singleton) DO UPDATE SET mode='ACTIVE'
	`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	dimension := "external-hierarchy-" + suffix
	hostID := "external-fenced-host-" + suffix
	parentA, parentB := "external-parent-a-"+suffix, "external-parent-b-"+suffix
	rackA, rackB := "external-rack-a-"+suffix, "external-rack-b-"+suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, hostID); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []HostGroupRevision{
		{HostGroupID: parentA, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "site", LifecycleState: "ACTIVE"},
		{HostGroupID: parentB, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "site", LifecycleState: "ACTIVE"},
		{HostGroupID: rackA, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE"},
		{HostGroupID: rackB, Generation: 1, GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE"},
	} {
		if err := UpsertHostGroup(ctx, pool, revision); err != nil {
			t.Fatal(err)
		}
	}
	var policyID string
	if err := pool.QueryRow(ctx, `SELECT cardinality_policy_id FROM kim.host_group_cardinality_policies_current
		WHERE group_type='FAILURE_DOMAIN' AND dimension=$1 AND level='rack'
		  AND scope_type='SYSTEM' AND scope_id='system'`, dimension).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := UpsertHostGroupCardinalityPolicy(ctx, pool, HostGroupCardinalityPolicy{
		PolicyID: policyID, Generation: 2, GroupType: "FAILURE_DOMAIN", Dimension: dimension,
		Level: "rack", ScopeType: "SYSTEM", ScopeID: "system", Cardinality: "ZERO_OR_ONE", State: "ACTIVE",
	}); err != nil {
		t.Fatal(err)
	}
	hierarchyRequest := HostGroupHierarchyRequest{
		PublishRequestID: "external-hierarchy-1-" + suffix, HierarchyID: "external-hierarchy-" + suffix,
		GroupType: "FAILURE_DOMAIN", Dimension: dimension, ScopeType: "SYSTEM", ScopeID: "system",
		GraphMode: "TREE", ExpectedCurrentGeneration: 0, Levels: []string{"site", "rack"},
		NodeGroupIDs: []string{parentA, parentB, rackA, rackB},
		Relations:    []HostGroupHierarchyRelation{{ParentGroupID: parentA, ChildGroupID: rackA}, {ParentGroupID: parentA, ChildGroupID: rackB}},
	}
	hierarchy1, err := PublishHostGroupHierarchy(ctx, pool, hierarchyRequest)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuerID := "external-fencing-issuer-" + suffix
	publishIssuer := func(generation, expected uint64) {
		t.Helper()
		if err := PublishExternalAssertionIssuer(ctx, pool, ExternalAssertionIssuerRevision{
			PublishRequestID: fmt.Sprintf("external-fencing-issuer-%d-%s", generation, suffix),
			IssuerID:         issuerID, IssuerGeneration: generation, ExpectedCurrentGeneration: expected,
			LifecycleState: "TRUSTED", PublicKey: publicKey,
			Scopes: []ExternalAssertionIssuerScope{{HostGroupID: rackA, HostGroupGeneration: 1}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	publishIssuer(1, 0)
	makeAssertion := func(id, nonce string) ExternalHostGroupAssertion {
		t.Helper()
		now := time.Now().UTC().Truncate(time.Microsecond)
		value := ExternalHostGroupAssertion{
			AssertionID: id, IssuerID: issuerID, SchemaVersion: ExternalHostGroupAssertionSchema,
			SubjectType: ExternalHostGroupAssertionSubject, HostGroupID: rackA, HostGroupGeneration: 1,
			Audience: ExternalHostGroupAssertionAudience, Nonce: nonce, Members: []string{hostID},
			IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
		}
		payload, digest, err := CanonicalExternalHostGroupAssertionPayload(value)
		if err != nil {
			t.Fatal(err)
		}
		value.PayloadDigest, value.Signature = digest, ed25519.Sign(privateKey, payload)
		return value
	}
	staleHierarchy := makeAssertion("external-stale-hierarchy-"+suffix, "external-stale-hierarchy-nonce-"+suffix)
	if result, err := VerifyExternalHostGroupAssertion(ctx, pool, staleHierarchy); err != nil || result.VerificationResult != "VERIFIED" || result.HierarchyGeneration != hierarchy1.HierarchyGeneration {
		t.Fatalf("hierarchy-bound verification = %#v/%v", result, err)
	}
	hierarchyRequest.PublishRequestID = "external-hierarchy-2-" + suffix
	hierarchyRequest.ExpectedCurrentGeneration = hierarchy1.HierarchyGeneration
	hierarchyRequest.Relations = []HostGroupHierarchyRelation{{ParentGroupID: parentB, ChildGroupID: rackA}, {ParentGroupID: parentA, ChildGroupID: rackB}}
	hierarchy2, err := PublishHostGroupHierarchy(ctx, pool, hierarchyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "external-stale-hierarchy-set-" + suffix, AssertionID: staleHierarchy.AssertionID,
		ExpectedCurrentSetGeneration: 0,
	}); !errors.Is(err, ErrExternalAssertionConflict) {
		t.Fatalf("hierarchy drift materialization = %v", err)
	}
	publishIssuer(2, 1)
	currentAssertion := makeAssertion("external-current-hierarchy-"+suffix, "external-current-hierarchy-nonce-"+suffix)
	if result, err := VerifyExternalHostGroupAssertion(ctx, pool, currentAssertion); err != nil || result.VerificationResult != "VERIFIED" || result.HierarchyGeneration != hierarchy2.HierarchyGeneration {
		t.Fatalf("current hierarchy verification = %#v/%v", result, err)
	}
	hierarchyGeneration := hierarchy2.HierarchyGeneration
	if _, err := PublishHostGroupMembershipSet(ctx, pool, HostGroupMembershipSetRequest{
		PublishRequestID: "external-sibling-set-" + suffix, HostGroupID: rackB,
		BasedOnHostGroupGeneration: 1, ExpectedCurrentSetGeneration: 0,
		HierarchyGeneration: &hierarchyGeneration, SourceType: "EXPLICIT", SourceRevision: "operator-" + suffix,
		Members: []HostGroupMembership{{HostGroupID: rackB, HostID: hostID, Generation: 1,
			State: "ACTIVE", SourceType: "EXPLICIT", SourceRevision: "operator-" + suffix}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "external-cardinality-conflict-" + suffix, AssertionID: currentAssertion.AssertionID,
		ExpectedCurrentSetGeneration: 0,
	}); !errors.Is(err, ErrHostGroupConflict) {
		t.Fatalf("external assertion bypassed cardinality: %v", err)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: rackA, Generation: 2,
		GroupType: "FAILURE_DOMAIN", Dimension: dimension, Level: "rack", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	groupDrift := makeAssertion("external-group-drift-"+suffix, "external-group-drift-nonce-"+suffix)
	if result, err := VerifyExternalHostGroupAssertion(ctx, pool, groupDrift); err != nil || result.VerificationResult != "STALE_HOST_GROUP" {
		t.Fatalf("HostGroup generation drift = %#v/%v", result, err)
	}
}

func TestHostGroupExternalAssertionAndSelectorPublisherRacePostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES('external-publisher-race',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	groupID, hostA, hostB := "external-race-group-"+suffix, "external-race-a-"+suffix, "external-race-b-"+suffix
	for _, hostID := range []string{hostA, hostB} {
		fingerprint := digestHostGroupFields(hostID, "external-race-certificate")
		prepareSessionIdentityFixture(t, ctx, pool, hostID, 1, fingerprint)
		if _, err := AdmitAgentSession(ctx, pool, AgentSessionAdmission{
			SessionAttemptID: hostID + "-external-race-attempt", HostID: hostID,
			ConnectionInstanceID: "external-race-connection", TransportProfile: "integration",
			ProtocolVersion: "v1", AgentArtifactDigest: digestHostGroupFields("external-race-agent"),
			CredentialBindingRevision: 1, PeerCertificateFingerprint: fingerprint,
			ExpectedSessionGeneration: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	acceptSelectorInventory(t, ctx, pool, hostA, 1, "x86_64", agentinventory.AvailabilityAvailable)
	acceptSelectorInventory(t, ctx, pool, hostB, 1, "aarch64", agentinventory.AvailabilityAvailable)
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 1,
		GroupType: "OPERATIONAL_COHORT", Dimension: "external-race-" + suffix, Level: "cohort", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	issuerID := "external-race-issuer-" + suffix
	if err := PublishExternalAssertionIssuer(ctx, pool, ExternalAssertionIssuerRevision{
		PublishRequestID: "external-race-issuer-publish-" + suffix, IssuerID: issuerID,
		IssuerGeneration: 1, LifecycleState: "TRUSTED", PublicKey: publicKey,
		Scopes: []ExternalAssertionIssuerScope{{HostGroupID: groupID, HostGroupGeneration: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	assertion := ExternalHostGroupAssertion{AssertionID: "external-race-assertion-" + suffix, IssuerID: issuerID,
		SchemaVersion: ExternalHostGroupAssertionSchema, SubjectType: ExternalHostGroupAssertionSubject,
		HostGroupID: groupID, HostGroupGeneration: 1, Audience: ExternalHostGroupAssertionAudience,
		Nonce: "external-race-nonce-" + suffix, Members: []string{hostA},
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	payload, digest, _ := CanonicalExternalHostGroupAssertionPayload(assertion)
	assertion.PayloadDigest, assertion.Signature = digest, ed25519.Sign(privateKey, payload)
	if result, err := VerifyExternalHostGroupAssertion(ctx, pool, assertion); err != nil || result.VerificationResult != "VERIFIED" {
		t.Fatalf("race assertion verification = %#v/%v", result, err)
	}
	selectorID := "external-race-selector-" + suffix
	if err := UpsertHostGroupSelector(ctx, pool, HostGroupSelectorRevision{
		SelectorID: selectorID, HostGroupID: groupID, Generation: 1,
		BasedOnHostGroupGeneration: 1, SchemaVersion: HostGroupSelectorSchemaV1,
		EvaluatorArtifactDigest: digestHostGroupFields("external-race-selector-evaluator"),
		LifecycleState:          "ACTIVE",
		Expression:              selectorExpression(HostGroupSelectorPredicate{Field: "COMPUTE_ARCHITECTURE", Operator: "EQUALS", Value: "aarch64"}),
	}); err != nil {
		t.Fatal(err)
	}
	evaluation, err := EvaluateHostGroupSelector(ctx, pool, HostGroupSelectorEvaluationRequest{
		EvaluationID: "external-race-evaluation-" + suffix, SelectorID: selectorID,
		SelectorGeneration: 1, ExpectedCurrentGeneration: 0, HostIDs: []string{hostA, hostB},
	})
	if err != nil || evaluation.ResultState != "MATCHED" {
		t.Fatalf("race selector evaluation = %#v/%v", evaluation, err)
	}
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	go func() {
		<-start
		_, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
			PublishRequestID: "external-race-set-" + suffix, AssertionID: assertion.AssertionID,
			ExpectedCurrentSetGeneration: 0,
		})
		errorsOut <- err
	}()
	go func() {
		<-start
		_, err := MaterializeHostGroupSelectorEvaluation(ctx, pool, HostGroupSelectorMaterializationRequest{
			PublishRequestID: "selector-race-set-" + suffix, EvaluationID: evaluation.EvaluationID,
			ExpectedCurrentSetGeneration: 0,
		})
		errorsOut <- err
	}()
	close(start)
	firstErr, secondErr := <-errorsOut, <-errorsOut
	successes := 0
	for _, err := range []error{firstErr, secondErr} {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrHostGroupConflict) {
			t.Fatalf("unexpected source race error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("source race successes=%d errors=%v/%v", successes, firstErr, secondErr)
	}
	var generation uint64
	if err := pool.QueryRow(ctx, `SELECT membership_set_generation FROM kim.host_group_membership_sets_current WHERE host_group_id=$1`, groupID).Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("source race current generation=%d err=%v", generation, err)
	}
}

func TestHostGroupExternalAssertionIssuerRotationRacePostgreSQLIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO kim.database_authority(restore_epoch,authority_generation,mode)
		VALUES('external-issuer-race',1,'ACTIVE') ON CONFLICT(singleton) DO UPDATE SET mode='ACTIVE'`); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	groupID, hostID, issuerID := "external-issuer-race-group-"+suffix,
		"external-issuer-race-host-"+suffix, "external-issuer-race-"+suffix
	if _, err := pool.Exec(ctx, `INSERT INTO kim.host_identities(host_id,enrollment_state) VALUES($1,'APPROVED')`, hostID); err != nil {
		t.Fatal(err)
	}
	if err := UpsertHostGroup(ctx, pool, HostGroupRevision{HostGroupID: groupID, Generation: 1,
		GroupType: "OPERATIONAL_COHORT", Dimension: "external-issuer-race-" + suffix,
		Level: "cohort", LifecycleState: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	publicKey1, privateKey1, _ := ed25519.GenerateKey(rand.Reader)
	publicKey2, _, _ := ed25519.GenerateKey(rand.Reader)
	scope := []ExternalAssertionIssuerScope{{HostGroupID: groupID, HostGroupGeneration: 1}}
	if err := PublishExternalAssertionIssuer(ctx, pool, ExternalAssertionIssuerRevision{
		PublishRequestID: "external-issuer-race-gen1-" + suffix, IssuerID: issuerID,
		IssuerGeneration: 1, LifecycleState: "TRUSTED", PublicKey: publicKey1, Scopes: scope,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	assertion := ExternalHostGroupAssertion{AssertionID: "external-issuer-race-assertion-" + suffix,
		IssuerID: issuerID, SchemaVersion: ExternalHostGroupAssertionSchema,
		SubjectType: ExternalHostGroupAssertionSubject, HostGroupID: groupID, HostGroupGeneration: 1,
		Audience: ExternalHostGroupAssertionAudience, Nonce: "external-issuer-race-nonce-" + suffix,
		Members: []string{hostID}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	payload, digest, _ := CanonicalExternalHostGroupAssertionPayload(assertion)
	assertion.PayloadDigest, assertion.Signature = digest, ed25519.Sign(privateKey1, payload)

	start := make(chan struct{})
	verificationOut := make(chan ExternalAssertionVerification, 1)
	errorsOut := make(chan error, 2)
	go func() {
		<-start
		value, err := VerifyExternalHostGroupAssertion(ctx, pool, assertion)
		verificationOut <- value
		errorsOut <- err
	}()
	go func() {
		<-start
		errorsOut <- PublishExternalAssertionIssuer(ctx, pool, ExternalAssertionIssuerRevision{
			PublishRequestID: "external-issuer-race-gen2-" + suffix, IssuerID: issuerID,
			IssuerGeneration: 2, ExpectedCurrentGeneration: 1, LifecycleState: "TRUSTED",
			PublicKey: publicKey2, Scopes: scope,
		})
	}()
	close(start)
	for range 2 {
		if err := <-errorsOut; err != nil {
			t.Fatal(err)
		}
	}
	verification := <-verificationOut
	switch verification.VerificationResult {
	case "VERIFIED":
		if verification.IssuerGeneration != 1 {
			t.Fatalf("VERIFIED mixed issuer generation: %#v", verification)
		}
	case "INVALID_SIGNATURE":
		if verification.IssuerGeneration != 2 {
			t.Fatalf("INVALID_SIGNATURE mixed issuer generation: %#v", verification)
		}
	default:
		t.Fatalf("issuer race result = %#v", verification)
	}
	replay, err := VerifyExternalHostGroupAssertion(ctx, pool, assertion)
	if err != nil || replay.VerificationDigest != verification.VerificationDigest ||
		replay.IssuerGeneration != verification.IssuerGeneration {
		t.Fatalf("issuer race replay = %#v/%v", replay, err)
	}
	if _, err := MaterializeExternalAssertionMembershipSet(ctx, pool, ExternalAssertionMaterializationRequest{
		PublishRequestID: "external-issuer-race-set-" + suffix, AssertionID: assertion.AssertionID,
		ExpectedCurrentSetGeneration: 0,
	}); !errors.Is(err, ErrExternalAssertionConflict) {
		t.Fatalf("stale/invalid issuer race materialization = %v", err)
	}
}
