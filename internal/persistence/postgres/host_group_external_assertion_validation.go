package postgres

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

func validateExternalAssertionMembershipSetRequestTx(ctx context.Context, tx pgx.Tx, request HostGroupMembershipSetRequest, hierarchy currentHostGroupHierarchy) error {
	if request.SourceType != "EXTERNAL_ASSERTION" {
		return nil
	}
	if request.ExternalIssuerGeneration == nil {
		return ErrHostGroupConflict
	}
	if request.SourceRevision != request.ExternalAssertionID {
		return ErrHostGroupConflict
	}
	var verificationResult, issuerID, hostGroupID, payloadDigest, verificationDigest string
	var issuerGeneration, hostGroupGeneration uint64
	var expiresAt time.Time
	var verifiedHierarchyID string
	var verifiedHierarchyGeneration uint64
	err := tx.QueryRow(ctx, `SELECT verification_result,issuer_id,issuer_generation,host_group_id,host_group_generation,
		expires_at,payload_digest,verification_digest,COALESCE(verified_hierarchy_id,''),COALESCE(verified_hierarchy_generation,0)
		FROM kim.host_group_external_assertion_evidence WHERE assertion_id=$1`, request.ExternalAssertionID).Scan(
		&verificationResult, &issuerID, &issuerGeneration, &hostGroupID, &hostGroupGeneration,
		&expiresAt, &payloadDigest, &verificationDigest, &verifiedHierarchyID, &verifiedHierarchyGeneration)
	if err != nil || verificationResult != "VERIFIED" || issuerID != request.ExternalIssuerID ||
		issuerGeneration != *request.ExternalIssuerGeneration || hostGroupID != request.HostGroupID ||
		hostGroupGeneration != request.BasedOnHostGroupGeneration || payloadDigest != request.ExternalPayloadDigest ||
		verificationDigest != request.ExternalVerificationDigest || hierarchy.HierarchyID != verifiedHierarchyID ||
		hierarchy.Generation != verifiedHierarchyGeneration {
		return ErrHostGroupConflict
	}
	var now time.Time
	var currentIssuerGeneration uint64
	var lifecycle string
	if err := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if !expiresAt.After(now) {
		return ErrHostGroupConflict
	}
	if err := tx.QueryRow(ctx, `SELECT issuer_generation,lifecycle_state FROM kim.host_group_external_assertion_issuers_current WHERE issuer_id=$1 FOR SHARE`, issuerID).Scan(&currentIssuerGeneration, &lifecycle); err != nil || currentIssuerGeneration != issuerGeneration || lifecycle != "TRUSTED" {
		return ErrHostGroupConflict
	}
	var scoped bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_group_external_assertion_issuer_scope_evidence WHERE issuer_id=$1 AND issuer_generation=$2 AND host_group_id=$3 AND host_group_generation=$4)`, issuerID, issuerGeneration, hostGroupID, hostGroupGeneration).Scan(&scoped); err != nil || !scoped {
		return ErrHostGroupConflict
	}
	rows, err := tx.Query(ctx, `SELECT host_id FROM kim.host_group_external_assertion_member_evidence WHERE assertion_id=$1 ORDER BY host_id`, request.ExternalAssertionID)
	if err != nil {
		return err
	}
	var asserted []string
	for rows.Next() {
		var hostID string
		if err := rows.Scan(&hostID); err != nil {
			rows.Close()
			return err
		}
		asserted = append(asserted, hostID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	requested := make([]string, 0, len(request.Members))
	for _, member := range request.Members {
		if member.State != "ACTIVE" || member.SourceType != "EXTERNAL_ASSERTION" || member.SourceRevision != request.ExternalAssertionID {
			return ErrHostGroupConflict
		}
		requested = append(requested, member.HostID)
	}
	sort.Strings(requested)
	if len(asserted) != len(requested) {
		return ErrHostGroupConflict
	}
	for index := range asserted {
		if asserted[index] != requested[index] {
			return ErrHostGroupConflict
		}
	}
	return nil
}

func nullableExternalAssertionID(request HostGroupMembershipSetRequest) any {
	if request.SourceType != "EXTERNAL_ASSERTION" {
		return nil
	}
	return request.ExternalAssertionID
}

func nullableExternalIssuerID(request HostGroupMembershipSetRequest) any {
	if request.SourceType != "EXTERNAL_ASSERTION" {
		return nil
	}
	return request.ExternalIssuerID
}

func nullableExternalPayloadDigest(request HostGroupMembershipSetRequest) any {
	if request.SourceType != "EXTERNAL_ASSERTION" {
		return nil
	}
	return request.ExternalPayloadDigest
}

func nullableExternalVerificationDigest(request HostGroupMembershipSetRequest) any {
	if request.SourceType != "EXTERNAL_ASSERTION" {
		return nil
	}
	return request.ExternalVerificationDigest
}
