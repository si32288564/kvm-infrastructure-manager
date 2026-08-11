package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	ExternalHostGroupAssertionSchema   = "kim.host-group.external-assertion/v1"
	ExternalHostGroupAssertionAudience = "kim-control-plane"
	ExternalHostGroupAssertionSubject  = "HOST_GROUP_COMPLETE_MEMBERSHIP"
	externalAssertionVerifierVersion   = "kim.external-assertion.ed25519-verifier/v1"
)

var ErrExternalAssertionConflict = errors.New("HostGroup external assertion authority conflict")

type ExternalAssertionIssuerScope struct {
	HostGroupID         string
	HostGroupGeneration uint64
}

type ExternalAssertionIssuerRevision struct {
	PublishRequestID, IssuerID, LifecycleState string
	IssuerGeneration                           uint64
	PublicKey                                  []byte
	ExpectedCurrentGeneration                  uint64
	Scopes                                     []ExternalAssertionIssuerScope
}

type ExternalHostGroupAssertion struct {
	AssertionID, IssuerID, SchemaVersion, SubjectType string
	HostGroupID, Audience, Nonce, PayloadDigest       string
	HostGroupGeneration                               uint64
	IssuedAt, ExpiresAt                               time.Time
	Members                                           []string
	Signature                                         []byte
}

type ExternalAssertionVerification struct {
	AssertionID, IssuerID, SchemaVersion, HostGroupID string
	Audience, Nonce, PayloadDigest, SignatureDigest   string
	CanonicalPayloadDigest, CanonicalMemberSetDigest  string
	VerificationResult, VerificationDigest            string
	VerifierVersion, VerifierDigest                   string
	IssuerGeneration, HostGroupGeneration             uint64
	HierarchyID                                       string
	HierarchyGeneration                               uint64
	IssuedAt, ExpiresAt                               time.Time
	MemberCount                                       int
}

type ExternalAssertionMaterializationRequest struct {
	PublishRequestID, AssertionID string
	ExpectedCurrentSetGeneration  uint64
}

type externalAssertionCanonicalPayload struct {
	SchemaVersion       string   `json:"schema_version"`
	AssertionID         string   `json:"assertion_id"`
	IssuerID            string   `json:"issuer_id"`
	SubjectType         string   `json:"subject_type"`
	HostGroupID         string   `json:"host_group_id"`
	HostGroupGeneration uint64   `json:"host_group_generation"`
	Members             []string `json:"members"`
	IssuedAt            string   `json:"issued_at"`
	ExpiresAt           string   `json:"expires_at"`
	Audience            string   `json:"audience"`
	Nonce               string   `json:"nonce"`
}

// CanonicalExternalHostGroupAssertionPayload returns the only byte encoding
// accepted by the Phase 1 ED25519 verifier. Caller JSON serialization is never
// signature authority.
func CanonicalExternalHostGroupAssertionPayload(assertion ExternalHostGroupAssertion) ([]byte, string, error) {
	members := append([]string(nil), assertion.Members...)
	sort.Strings(members)
	for index, hostID := range members {
		if hostID == "" || (index > 0 && members[index-1] == hostID) {
			return nil, "", ErrExternalAssertionConflict
		}
	}
	if assertion.AssertionID == "" || assertion.IssuerID == "" || assertion.SubjectType == "" ||
		assertion.HostGroupID == "" || assertion.HostGroupGeneration == 0 || assertion.Audience == "" ||
		assertion.Nonce == "" || assertion.IssuedAt.IsZero() || assertion.ExpiresAt.IsZero() {
		return nil, "", ErrExternalAssertionConflict
	}
	payload, err := json.Marshal(externalAssertionCanonicalPayload{
		SchemaVersion: assertion.SchemaVersion, AssertionID: assertion.AssertionID,
		IssuerID: assertion.IssuerID, SubjectType: assertion.SubjectType,
		HostGroupID: assertion.HostGroupID, HostGroupGeneration: assertion.HostGroupGeneration,
		Members: members, IssuedAt: assertion.IssuedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt: assertion.ExpiresAt.UTC().Format(time.RFC3339Nano), Audience: assertion.Audience,
		Nonce: assertion.Nonce,
	})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func PublishExternalAssertionIssuer(ctx context.Context, db TxBeginner, revision ExternalAssertionIssuerRevision) error {
	if revision.PublishRequestID == "" || revision.IssuerID == "" || revision.IssuerGeneration == 0 ||
		len(revision.PublicKey) != ed25519.PublicKeySize ||
		(revision.LifecycleState != "TRUSTED" && revision.LifecycleState != "RETIRED" && revision.LifecycleState != "REVOKED") {
		return ErrExternalAssertionConflict
	}
	scopes := append([]ExternalAssertionIssuerScope(nil), revision.Scopes...)
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].HostGroupID < scopes[j].HostGroupID })
	for index, scope := range scopes {
		if scope.HostGroupID == "" || scope.HostGroupGeneration == 0 || (index > 0 && scopes[index-1].HostGroupID == scope.HostGroupID) {
			return ErrExternalAssertionConflict
		}
	}
	keyDigest := digestHostGroupFields(string(revision.PublicKey))
	scopeFields := make([]string, 0, len(scopes)*2)
	for _, scope := range scopes {
		scopeFields = append(scopeFields, scope.HostGroupID, fmt.Sprint(scope.HostGroupGeneration))
	}
	scopeDigest := digestHostGroupFields(scopeFields...)
	requestDigest := digestHostGroupFields(revision.IssuerID, fmt.Sprint(revision.IssuerGeneration),
		revision.LifecycleState, keyDigest, scopeDigest, ExternalHostGroupAssertionSchema,
		ExternalHostGroupAssertionAudience, "ED25519", "SYSTEM", "system")
	trustDigest := digestHostGroupFields(requestDigest, revision.PublishRequestID)
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockExternalAssertionIssuerTx(ctx, tx, revision.IssuerID); err != nil {
			return err
		}
		var replayDigest string
		err := tx.QueryRow(ctx, `SELECT request_digest FROM kim.host_group_external_assertion_issuer_revision_evidence WHERE publish_request_id=$1`, revision.PublishRequestID).Scan(&replayDigest)
		if err == nil {
			if replayDigest != requestDigest {
				return ErrExternalAssertionConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var currentGeneration uint64
		var currentLifecycle string
		err = tx.QueryRow(ctx, `SELECT issuer_generation,lifecycle_state FROM kim.host_group_external_assertion_issuers_current WHERE issuer_id=$1 FOR UPDATE`, revision.IssuerID).Scan(&currentGeneration, &currentLifecycle)
		if errors.Is(err, pgx.ErrNoRows) {
			currentGeneration = 0
		} else if err != nil {
			return err
		}
		if currentGeneration != revision.ExpectedCurrentGeneration || revision.IssuerGeneration != currentGeneration+1 ||
			(currentLifecycle == "REVOKED" || currentLifecycle == "RETIRED") {
			return ErrExternalAssertionConflict
		}
		if revision.LifecycleState == "TRUSTED" && len(scopes) == 0 {
			return ErrExternalAssertionConflict
		}
		if revision.LifecycleState == "TRUSTED" {
			for _, scope := range scopes {
				var generation uint64
				var lifecycle string
				if err := tx.QueryRow(ctx, `SELECT host_group_generation,lifecycle_state FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE`, scope.HostGroupID).Scan(&generation, &lifecycle); err != nil || generation != scope.HostGroupGeneration || lifecycle != "ACTIVE" {
					return ErrExternalAssertionConflict
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_external_assertion_issuer_revision_evidence(
			issuer_id,issuer_generation,publish_request_id,request_digest,issuer_type,schema_version,audience,
			verification_algorithm,public_key,public_key_digest,scope_type,scope_id,lifecycle_state,trust_digest)
			VALUES($1,$2,$3,$4,'HOST_GROUP_MEMBERSHIP',$5,$6,'ED25519',$7,$8,'SYSTEM','system',$9,$10)`,
			revision.IssuerID, revision.IssuerGeneration, revision.PublishRequestID, requestDigest,
			ExternalHostGroupAssertionSchema, ExternalHostGroupAssertionAudience, revision.PublicKey, keyDigest,
			revision.LifecycleState, trustDigest); err != nil {
			return err
		}
		for _, scope := range scopes {
			digest := digestHostGroupFields(revision.IssuerID, fmt.Sprint(revision.IssuerGeneration), scope.HostGroupID, fmt.Sprint(scope.HostGroupGeneration))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_external_assertion_issuer_scope_evidence(issuer_id,issuer_generation,host_group_id,host_group_generation,scope_digest) VALUES($1,$2,$3,$4,$5)`, revision.IssuerID, revision.IssuerGeneration, scope.HostGroupID, scope.HostGroupGeneration, digest); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.host_group_external_assertion_issuers_current(
			issuer_id,issuer_generation,lifecycle_state,schema_version,audience,verification_algorithm,public_key_digest,trust_digest)
			VALUES($1,$2,$3,$4,$5,'ED25519',$6,$7)
			ON CONFLICT(issuer_id) DO UPDATE SET issuer_generation=EXCLUDED.issuer_generation,
			lifecycle_state=EXCLUDED.lifecycle_state,schema_version=EXCLUDED.schema_version,audience=EXCLUDED.audience,
			verification_algorithm=EXCLUDED.verification_algorithm,public_key_digest=EXCLUDED.public_key_digest,
			trust_digest=EXCLUDED.trust_digest,updated_at=statement_timestamp()`, revision.IssuerID,
			revision.IssuerGeneration, revision.LifecycleState, ExternalHostGroupAssertionSchema,
			ExternalHostGroupAssertionAudience, keyDigest, trustDigest)
		return err
	})
}

func VerifyExternalHostGroupAssertion(ctx context.Context, db TxBeginner, assertion ExternalHostGroupAssertion) (ExternalAssertionVerification, error) {
	payload, canonicalPayloadDigest, err := CanonicalExternalHostGroupAssertionPayload(assertion)
	if err != nil {
		return ExternalAssertionVerification{}, err
	}
	members := append([]string(nil), assertion.Members...)
	sort.Strings(members)
	memberDigest := digestHostGroupFields(members...)
	signatureSum := sha256.Sum256(assertion.Signature)
	signatureDigest := hex.EncodeToString(signatureSum[:])
	verifierDigest := digestHostGroupFields(externalAssertionVerifierVersion, "ED25519", ExternalHostGroupAssertionSchema)
	var result ExternalAssertionVerification
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockExternalAssertionIssuerTx(ctx, tx, assertion.IssuerID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "external-assertion/"+assertion.AssertionID); err != nil {
			return err
		}
		var existingCanonical, existingSignature string
		existing, found, err := loadExternalAssertionVerificationTx(ctx, tx, assertion.AssertionID)
		if err != nil {
			return err
		}
		if found {
			if err := tx.QueryRow(ctx, `SELECT canonical_payload_digest,signature_digest FROM kim.host_group_external_assertion_evidence WHERE assertion_id=$1`, assertion.AssertionID).Scan(&existingCanonical, &existingSignature); err != nil {
				return err
			}
			if existingCanonical == canonicalPayloadDigest && existingSignature == signatureDigest && existing.PayloadDigest == assertion.PayloadDigest {
				result = existing
				return nil
			}
			if err := recordExternalAssertionConflictTx(ctx, tx, assertion.AssertionID, assertion.IssuerID, assertion.Nonce, existingCanonical, canonicalPayloadDigest, "ASSERTION_ID"); err != nil {
				return err
			}
			result = externalAssertionConflictResult(assertion, canonicalPayloadDigest, signatureDigest, memberDigest, verifierDigest)
			return nil
		}

		var issuerGeneration uint64
		var lifecycle, schemaVersion, audience, algorithm string
		var publicKey []byte
		err = tx.QueryRow(ctx, `SELECT current.issuer_generation,current.lifecycle_state,evidence.schema_version,evidence.audience,
			evidence.verification_algorithm,evidence.public_key
			FROM kim.host_group_external_assertion_issuers_current current
			JOIN kim.host_group_external_assertion_issuer_revision_evidence evidence
			  ON evidence.issuer_id=current.issuer_id AND evidence.issuer_generation=current.issuer_generation
			WHERE current.issuer_id=$1 FOR SHARE`, assertion.IssuerID).Scan(&issuerGeneration, &lifecycle, &schemaVersion, &audience, &algorithm, &publicKey)
		issuerKnown := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		verificationResult := "VERIFIED"
		var hierarchyID string
		var hierarchyGeneration uint64
		nonceAlreadyRegistered := false
		if !issuerKnown || lifecycle != "TRUSTED" {
			verificationResult = "UNTRUSTED_ISSUER"
		} else {
			var nonceAssertionID, noncePayloadDigest string
			err := tx.QueryRow(ctx, `SELECT assertion_id,payload_digest FROM kim.host_group_external_assertion_nonce_evidence WHERE issuer_id=$1 AND nonce=$2`, assertion.IssuerID, assertion.Nonce).Scan(&nonceAssertionID, &noncePayloadDigest)
			nonceAlreadyRegistered = err == nil
			if err == nil && (nonceAssertionID != assertion.AssertionID || noncePayloadDigest != canonicalPayloadDigest) {
				if err := recordExternalAssertionConflictTx(ctx, tx, assertion.AssertionID, assertion.IssuerID, assertion.Nonce, noncePayloadDigest, canonicalPayloadDigest, "NONCE"); err != nil {
					return err
				}
				verificationResult = "REPLAY_CONFLICT"
			} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if verificationResult == "VERIFIED" && (assertion.SchemaVersion != ExternalHostGroupAssertionSchema ||
				assertion.SubjectType != ExternalHostGroupAssertionSubject) {
				verificationResult = "UNSUPPORTED_SCHEMA"
			} else if verificationResult == "VERIFIED" && (schemaVersion != ExternalHostGroupAssertionSchema || algorithm != "ED25519") {
				verificationResult = "UNKNOWN"
			} else if verificationResult == "VERIFIED" && (assertion.Audience != audience || assertion.Audience != ExternalHostGroupAssertionAudience) {
				verificationResult = "AUDIENCE_MISMATCH"
			} else if verificationResult == "VERIFIED" && assertion.PayloadDigest != canonicalPayloadDigest {
				verificationResult = "PAYLOAD_DIGEST_MISMATCH"
			} else if verificationResult == "VERIFIED" && !ed25519.Verify(ed25519.PublicKey(publicKey), payload, assertion.Signature) {
				verificationResult = "INVALID_SIGNATURE"
			}
			var now time.Time
			if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
				return err
			}
			if verificationResult == "VERIFIED" && (!assertion.ExpiresAt.After(assertion.IssuedAt) || !assertion.ExpiresAt.After(now) || assertion.IssuedAt.After(now.Add(5*time.Minute))) {
				verificationResult = "EXPIRED"
			}
			if verificationResult == "VERIFIED" {
				var groupGeneration uint64
				var groupType, dimension, groupLifecycle string
				err := tx.QueryRow(ctx, `SELECT group_current.host_group_generation,group_current.group_type,group_current.dimension,group_current.lifecycle_state
					FROM kim.host_groups_current group_current
					JOIN kim.host_group_external_assertion_issuer_scope_evidence scope
					  ON scope.host_group_id=group_current.host_group_id AND scope.host_group_generation=group_current.host_group_generation
					WHERE scope.issuer_id=$1 AND scope.issuer_generation=$2 AND scope.host_group_id=$3 FOR SHARE`,
					assertion.IssuerID, issuerGeneration, assertion.HostGroupID).Scan(&groupGeneration, &groupType, &dimension, &groupLifecycle)
				if err != nil || groupGeneration != assertion.HostGroupGeneration || groupLifecycle != "ACTIVE" {
					verificationResult = "STALE_HOST_GROUP"
				} else {
					if err := lockHostGroupHierarchyScopeTx(ctx, tx, groupType, dimension); err != nil {
						return err
					}
					hierarchy, err := resolveCurrentHostGroupHierarchyTx(ctx, tx, groupType, dimension, assertion.HostGroupID)
					if err != nil {
						return err
					}
					hierarchyID, hierarchyGeneration = hierarchy.HierarchyID, hierarchy.Generation
				}
			}
			if verificationResult == "VERIFIED" {
				var known int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.host_identities WHERE host_id=ANY($1::text[])`, members).Scan(&known); err != nil {
					return err
				}
				if known != len(members) {
					verificationResult = "UNKNOWN_HOST"
				}
			}
		}
		verificationDigest := digestHostGroupFields(assertion.AssertionID, assertion.IssuerID, fmt.Sprint(issuerGeneration),
			canonicalPayloadDigest, signatureDigest, verificationResult, verifierDigest, hierarchyID, fmt.Sprint(hierarchyGeneration))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_external_assertion_evidence(
			assertion_id,issuer_id,issuer_generation,schema_version,subject_type,host_group_id,host_group_generation,
			audience,nonce,issued_at,expires_at,payload_digest,canonical_payload_digest,signature_digest,
			canonical_member_set_digest,member_count,verified_hierarchy_id,verified_hierarchy_generation,
			verification_result,verifier_version,verifier_digest,verification_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NULLIF($17,''),NULLIF($18,0),$19,$20,$21,$22)`,
			assertion.AssertionID, assertion.IssuerID, nullableIssuerGeneration(issuerKnown, issuerGeneration), assertion.SchemaVersion,
			assertion.SubjectType, assertion.HostGroupID, assertion.HostGroupGeneration, assertion.Audience, assertion.Nonce,
			assertion.IssuedAt, assertion.ExpiresAt, assertion.PayloadDigest, canonicalPayloadDigest, signatureDigest,
			memberDigest, len(members), hierarchyID, hierarchyGeneration, verificationResult,
			externalAssertionVerifierVersion, verifierDigest, verificationDigest); err != nil {
			return err
		}
		for _, hostID := range members {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_external_assertion_member_evidence(assertion_id,host_id,member_digest) VALUES($1,$2,$3)`, assertion.AssertionID, hostID, digestHostGroupFields(assertion.AssertionID, hostID)); err != nil {
				return err
			}
		}
		if issuerKnown && lifecycle == "TRUSTED" && !nonceAlreadyRegistered {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_external_assertion_nonce_evidence(issuer_id,issuer_generation,nonce,assertion_id,payload_digest) VALUES($1,$2,$3,$4,$5)`, assertion.IssuerID, issuerGeneration, assertion.Nonce, assertion.AssertionID, canonicalPayloadDigest); err != nil {
				return err
			}
		}
		result = ExternalAssertionVerification{AssertionID: assertion.AssertionID, IssuerID: assertion.IssuerID,
			IssuerGeneration: issuerGeneration, SchemaVersion: assertion.SchemaVersion, HostGroupID: assertion.HostGroupID,
			HostGroupGeneration: assertion.HostGroupGeneration, Audience: assertion.Audience, Nonce: assertion.Nonce,
			PayloadDigest: assertion.PayloadDigest, CanonicalPayloadDigest: canonicalPayloadDigest,
			SignatureDigest: signatureDigest, CanonicalMemberSetDigest: memberDigest, VerificationResult: verificationResult,
			VerificationDigest: verificationDigest, VerifierVersion: externalAssertionVerifierVersion,
			VerifierDigest: verifierDigest, HierarchyID: hierarchyID, HierarchyGeneration: hierarchyGeneration,
			IssuedAt: assertion.IssuedAt, ExpiresAt: assertion.ExpiresAt, MemberCount: len(members)}
		return nil
	})
	return result, err
}

func MaterializeExternalAssertionMembershipSet(ctx context.Context, db TxBeginner, request ExternalAssertionMaterializationRequest) (HostGroupMembershipSet, error) {
	if request.PublishRequestID == "" || request.AssertionID == "" {
		return HostGroupMembershipSet{}, ErrExternalAssertionConflict
	}
	var result HostGroupMembershipSet
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if existing, found, err := loadMembershipSetByPublishRequestTx(ctx, tx, request.PublishRequestID); err != nil {
			return err
		} else if found {
			var assertionID string
			if err := tx.QueryRow(ctx, `SELECT COALESCE(external_assertion_id,'') FROM kim.host_group_membership_set_evidence WHERE publish_request_id=$1`, request.PublishRequestID).Scan(&assertionID); err != nil {
				return err
			}
			if assertionID != request.AssertionID ||
				existing.MembershipSetGeneration != request.ExpectedCurrentSetGeneration+1 {
				return ErrExternalAssertionConflict
			}
			result = existing
			return nil
		}
		verification, found, err := loadExternalAssertionVerificationTx(ctx, tx, request.AssertionID)
		if err != nil {
			return err
		}
		if !found || verification.VerificationResult != "VERIFIED" {
			return ErrExternalAssertionConflict
		}
		if err := lockExternalAssertionIssuerTx(ctx, tx, verification.IssuerID); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, verification.HostGroupID); err != nil {
			return err
		}
		var now time.Time
		var currentIssuerGeneration, currentGroupGeneration uint64
		var issuerLifecycle, groupLifecycle string
		if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT issuer_generation,lifecycle_state FROM kim.host_group_external_assertion_issuers_current WHERE issuer_id=$1 FOR SHARE`, verification.IssuerID).Scan(&currentIssuerGeneration, &issuerLifecycle); err != nil || currentIssuerGeneration != verification.IssuerGeneration || issuerLifecycle != "TRUSTED" || !verification.ExpiresAt.After(now) {
			return ErrExternalAssertionConflict
		}
		if err := tx.QueryRow(ctx, `SELECT host_group_generation,lifecycle_state FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE`, verification.HostGroupID).Scan(&currentGroupGeneration, &groupLifecycle); err != nil || currentGroupGeneration != verification.HostGroupGeneration || groupLifecycle != "ACTIVE" {
			return ErrExternalAssertionConflict
		}
		var scoped bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_group_external_assertion_issuer_scope_evidence WHERE issuer_id=$1 AND issuer_generation=$2 AND host_group_id=$3 AND host_group_generation=$4)`, verification.IssuerID, verification.IssuerGeneration, verification.HostGroupID, verification.HostGroupGeneration).Scan(&scoped); err != nil || !scoped {
			return ErrExternalAssertionConflict
		}
		var groupType, dimension string
		if err := tx.QueryRow(ctx, `SELECT group_type,dimension FROM kim.host_groups_current WHERE host_group_id=$1`, verification.HostGroupID).Scan(&groupType, &dimension); err != nil {
			return err
		}
		if err := lockHostGroupHierarchyScopeTx(ctx, tx, groupType, dimension); err != nil {
			return err
		}
		hierarchy, err := resolveCurrentHostGroupHierarchyTx(ctx, tx, groupType, dimension, verification.HostGroupID)
		if err != nil || hierarchy.HierarchyID != verification.HierarchyID || hierarchy.Generation != verification.HierarchyGeneration {
			return ErrExternalAssertionConflict
		}
		rows, err := tx.Query(ctx, `SELECT host_id FROM kim.host_group_external_assertion_member_evidence WHERE assertion_id=$1 ORDER BY host_id`, request.AssertionID)
		if err != nil {
			return err
		}
		var hostIDs []string
		for rows.Next() {
			var hostID string
			if err := rows.Scan(&hostID); err != nil {
				rows.Close()
				return err
			}
			hostIDs = append(hostIDs, hostID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		currentMembers, err := loadCurrentHostGroupMemberships(ctx, tx, verification.HostGroupID)
		if err != nil {
			return err
		}
		currentByHost := make(map[string]HostGroupMembership, len(currentMembers))
		for _, member := range currentMembers {
			currentByHost[member.HostID] = member
		}
		members := make([]HostGroupMembership, 0, len(hostIDs))
		for _, hostID := range hostIDs {
			generation := uint64(1)
			if current, exists := currentByHost[hostID]; exists {
				generation = current.Generation + 1
			}
			members = append(members, HostGroupMembership{HostGroupID: verification.HostGroupID, HostID: hostID,
				Generation: generation, State: "ACTIVE", SourceType: "EXTERNAL_ASSERTION", SourceRevision: request.AssertionID})
		}
		externalIssuerGeneration := verification.IssuerGeneration
		setRequest := HostGroupMembershipSetRequest{PublishRequestID: request.PublishRequestID,
			HostGroupID: verification.HostGroupID, SourceType: "EXTERNAL_ASSERTION", SourceRevision: request.AssertionID,
			BasedOnHostGroupGeneration:   verification.HostGroupGeneration,
			ExpectedCurrentSetGeneration: request.ExpectedCurrentSetGeneration, Members: members,
			ExternalAssertionID: request.AssertionID, ExternalIssuerID: verification.IssuerID,
			ExternalIssuerGeneration: &externalIssuerGeneration, ExternalPayloadDigest: verification.PayloadDigest,
			ExternalVerificationDigest: verification.VerificationDigest}
		if hierarchy.Generation != 0 {
			setRequest.HierarchyGeneration = &hierarchy.Generation
		}
		result, err = publishHostGroupMembershipSetTx(ctx, tx, setRequest)
		return err
	})
	if err != nil {
		return HostGroupMembershipSet{}, fmt.Errorf("materialize external HostGroup assertion: %w", err)
	}
	return result, nil
}

func loadExternalAssertionVerificationTx(ctx context.Context, tx pgx.Tx, assertionID string) (ExternalAssertionVerification, bool, error) {
	var result ExternalAssertionVerification
	err := tx.QueryRow(ctx, `SELECT assertion_id,issuer_id,COALESCE(issuer_generation,0),schema_version,host_group_id,
		host_group_generation,audience,nonce,issued_at,expires_at,payload_digest,canonical_payload_digest,
		signature_digest,canonical_member_set_digest,member_count,verification_result,verifier_version,
		verifier_digest,verification_digest,COALESCE(verified_hierarchy_id,''),COALESCE(verified_hierarchy_generation,0)
		FROM kim.host_group_external_assertion_evidence WHERE assertion_id=$1`, assertionID).Scan(
		&result.AssertionID, &result.IssuerID, &result.IssuerGeneration, &result.SchemaVersion,
		&result.HostGroupID, &result.HostGroupGeneration, &result.Audience, &result.Nonce,
		&result.IssuedAt, &result.ExpiresAt, &result.PayloadDigest, &result.CanonicalPayloadDigest,
		&result.SignatureDigest, &result.CanonicalMemberSetDigest, &result.MemberCount,
		&result.VerificationResult, &result.VerifierVersion, &result.VerifierDigest,
		&result.VerificationDigest, &result.HierarchyID, &result.HierarchyGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExternalAssertionVerification{}, false, nil
	}
	return result, err == nil, err
}

func recordExternalAssertionConflictTx(ctx context.Context, tx pgx.Tx, assertionID, issuerID, nonce, originalDigest, conflictingDigest, conflictType string) error {
	conflictID := "external-assertion-conflict/" + digestHostGroupFields(assertionID, issuerID, nonce, originalDigest, conflictingDigest, conflictType)
	_, err := tx.Exec(ctx, `INSERT INTO kim.host_group_external_assertion_conflict_evidence(
		conflict_id,assertion_id,issuer_id,nonce,original_payload_digest,conflicting_payload_digest,conflict_type)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(conflict_id) DO NOTHING`, conflictID, assertionID,
		issuerID, nonce, originalDigest, conflictingDigest, conflictType)
	return err
}

func externalAssertionConflictResult(assertion ExternalHostGroupAssertion, canonicalPayloadDigest, signatureDigest, memberDigest, verifierDigest string) ExternalAssertionVerification {
	return ExternalAssertionVerification{AssertionID: assertion.AssertionID, IssuerID: assertion.IssuerID,
		SchemaVersion: assertion.SchemaVersion, HostGroupID: assertion.HostGroupID,
		HostGroupGeneration: assertion.HostGroupGeneration, Audience: assertion.Audience, Nonce: assertion.Nonce,
		PayloadDigest: assertion.PayloadDigest, CanonicalPayloadDigest: canonicalPayloadDigest,
		SignatureDigest: signatureDigest, CanonicalMemberSetDigest: memberDigest,
		VerificationResult: "REPLAY_CONFLICT", VerifierVersion: externalAssertionVerifierVersion,
		VerifierDigest: verifierDigest, VerificationDigest: digestHostGroupFields(assertion.AssertionID, canonicalPayloadDigest, "REPLAY_CONFLICT"),
		IssuedAt: assertion.IssuedAt, ExpiresAt: assertion.ExpiresAt, MemberCount: len(assertion.Members)}
}

func lockExternalAssertionIssuerTx(ctx context.Context, tx pgx.Tx, issuerID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "external-assertion-issuer/"+issuerID)
	return err
}

func nullableIssuerGeneration(known bool, generation uint64) any {
	if !known {
		return nil
	}
	return generation
}
