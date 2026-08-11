package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrHostGroupConflict = errors.New("HostGroup authority conflict")

type HostGroupRevision struct {
	HostGroupID, GroupType, Dimension, Level, LifecycleState string
	Generation                                               uint64
}

type HostGroupMembership struct {
	HostGroupID, HostID, State, SourceType, SourceRevision string
	Generation                                             uint64
}

type HostGroupSnapshotRequest struct {
	SnapshotID, HostGroupID, Purpose string
}

type HostGroupSnapshot struct {
	SnapshotID, HostGroupID, Purpose, MembershipDigest string
	HostGroupGeneration                                uint64
	MemberCount                                        int
}

type hostGroupSnapshotMember struct {
	HostID, EvidenceDigest string
	Generation             uint64
}

func UpsertHostGroup(ctx context.Context, db TxBeginner, revision HostGroupRevision) error {
	if err := validateHostGroupRevision(revision); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, revision.HostGroupID); err != nil {
			return err
		}
		return upsertHostGroupTx(ctx, tx, revision)
	})
}

func AssignHostGroupMembership(ctx context.Context, db TxBeginner, membership HostGroupMembership) error {
	if err := validateHostGroupMembership(membership); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, membership.HostGroupID); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, membership.HostID); err != nil {
			return err
		}
		return assignHostGroupMembershipTx(ctx, tx, membership)
	})
}

func CreateHostGroupMembershipSnapshot(ctx context.Context, db TxBeginner, request HostGroupSnapshotRequest) (HostGroupSnapshot, error) {
	if request.SnapshotID == "" || request.HostGroupID == "" || !validHostGroupSnapshotPurpose(request.Purpose) {
		return HostGroupSnapshot{}, errors.New("complete HostGroup membership snapshot request is required")
	}
	var snapshot HostGroupSnapshot
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, request.HostGroupID); err != nil {
			return err
		}
		var existing HostGroupSnapshot
		err := tx.QueryRow(ctx, `
			SELECT snapshot_id,host_group_id,host_group_generation,purpose,member_count,membership_digest
			FROM kim.host_group_membership_snapshot_evidence WHERE snapshot_id=$1
		`, request.SnapshotID).Scan(&existing.SnapshotID, &existing.HostGroupID,
			&existing.HostGroupGeneration, &existing.Purpose, &existing.MemberCount,
			&existing.MembershipDigest)
		if err == nil {
			if existing.HostGroupID != request.HostGroupID || existing.Purpose != request.Purpose {
				return ErrHostGroupConflict
			}
			snapshot = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var lifecycle string
		if err := tx.QueryRow(ctx, `
			SELECT host_group_generation,lifecycle_state
			FROM kim.host_groups_current
			WHERE host_group_id=$1
			FOR SHARE
		`, request.HostGroupID).Scan(&snapshot.HostGroupGeneration, &lifecycle); err != nil {
			return err
		}
		if lifecycle != "ACTIVE" {
			return ErrHostGroupConflict
		}
		rows, err := tx.Query(ctx, `
			SELECT host_id,membership_generation,evidence_digest
			FROM kim.host_group_memberships_current
			WHERE host_group_id=$1 AND membership_state='ACTIVE'
			ORDER BY host_id
			FOR SHARE
		`, request.HostGroupID)
		if err != nil {
			return err
		}
		members := make([]hostGroupSnapshotMember, 0)
		for rows.Next() {
			var member hostGroupSnapshotMember
			if err := rows.Scan(&member.HostID, &member.Generation, &member.EvidenceDigest); err != nil {
				rows.Close()
				return err
			}
			members = append(members, member)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		snapshot = HostGroupSnapshot{
			SnapshotID: request.SnapshotID, HostGroupID: request.HostGroupID,
			Purpose: request.Purpose, HostGroupGeneration: snapshot.HostGroupGeneration,
			MemberCount: len(members), MembershipDigest: hostGroupMembershipSnapshotDigest(members),
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_membership_snapshot_evidence (
				snapshot_id,host_group_id,host_group_generation,purpose,member_count,membership_digest
			) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (snapshot_id) DO NOTHING
		`, snapshot.SnapshotID, snapshot.HostGroupID, snapshot.HostGroupGeneration,
			snapshot.Purpose, snapshot.MemberCount, snapshot.MembershipDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if err := tx.QueryRow(ctx, `
				SELECT snapshot_id,host_group_id,host_group_generation,purpose,member_count,membership_digest
				FROM kim.host_group_membership_snapshot_evidence WHERE snapshot_id=$1
			`, request.SnapshotID).Scan(&existing.SnapshotID, &existing.HostGroupID,
				&existing.HostGroupGeneration, &existing.Purpose, &existing.MemberCount,
				&existing.MembershipDigest); err != nil {
				return err
			}
			if existing != snapshot {
				return ErrHostGroupConflict
			}
			return nil
		}
		for _, member := range members {
			if _, err := tx.Exec(ctx, `
				INSERT INTO kim.host_group_membership_snapshot_members (
					snapshot_id,host_group_id,host_id,membership_generation,membership_evidence_digest
				) VALUES ($1,$2,$3,$4,$5)
			`, snapshot.SnapshotID, snapshot.HostGroupID, member.HostID, member.Generation, member.EvidenceDigest); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return HostGroupSnapshot{}, fmt.Errorf("create HostGroup membership snapshot: %w", err)
	}
	return snapshot, nil
}

func upsertHostGroupTx(ctx context.Context, tx pgx.Tx, revision HostGroupRevision) error {
	digest := hostGroupRevisionDigest(revision)
	var currentType, currentDimension, currentLevel, currentLifecycle string
	err := tx.QueryRow(ctx, `SELECT group_type,dimension,level,lifecycle_state FROM kim.host_groups_current WHERE host_group_id=$1`, revision.HostGroupID).Scan(&currentType, &currentDimension, &currentLevel, &currentLifecycle)
	if err == nil && (currentType != revision.GroupType || currentDimension != revision.Dimension || currentLevel != revision.Level) {
		return ErrHostGroupConflict
	}
	if err == nil && !validHostGroupLifecycleTransition(currentLifecycle, revision.LifecycleState) {
		return ErrHostGroupConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.host_group_revision_evidence (
			host_group_id,host_group_generation,group_type,dimension,level,lifecycle_state,revision_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (host_group_id,host_group_generation) DO NOTHING
	`, revision.HostGroupID, revision.Generation, revision.GroupType, revision.Dimension,
		revision.Level, revision.LifecycleState, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var existingDigest string
		if err := tx.QueryRow(ctx, `SELECT revision_digest FROM kim.host_group_revision_evidence WHERE host_group_id=$1 AND host_group_generation=$2`, revision.HostGroupID, revision.Generation).Scan(&existingDigest); err != nil {
			return err
		}
		if existingDigest != digest {
			return ErrHostGroupConflict
		}
	}
	tag, err = tx.Exec(ctx, `
		INSERT INTO kim.host_groups_current (
			host_group_id,host_group_generation,group_type,dimension,level,lifecycle_state,revision_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (host_group_id) DO UPDATE SET
			host_group_generation=EXCLUDED.host_group_generation,
			group_type=EXCLUDED.group_type,
			dimension=EXCLUDED.dimension,
			level=EXCLUDED.level,
			lifecycle_state=EXCLUDED.lifecycle_state,
			revision_digest=EXCLUDED.revision_digest,
			updated_at=statement_timestamp()
		WHERE kim.host_groups_current.host_group_generation < EXCLUDED.host_group_generation
	`, revision.HostGroupID, revision.Generation, revision.GroupType, revision.Dimension,
		revision.Level, revision.LifecycleState, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var generation uint64
	var currentDigest string
	if err := tx.QueryRow(ctx, `SELECT host_group_generation,revision_digest FROM kim.host_groups_current WHERE host_group_id=$1`, revision.HostGroupID).Scan(&generation, &currentDigest); err != nil {
		return err
	}
	if generation == revision.Generation && currentDigest == digest {
		return nil
	}
	return ErrHostGroupConflict
}

func assignHostGroupMembershipTx(ctx context.Context, tx pgx.Tx, membership HostGroupMembership) error {
	var groupType string
	if err := tx.QueryRow(ctx, `SELECT group_type FROM kim.host_groups_current WHERE host_group_id=$1 AND lifecycle_state IN ('ACTIVE','DRAINING') FOR SHARE`, membership.HostGroupID).Scan(&groupType); err != nil {
		return err
	}
	digest := hostGroupMembershipDigest(membership)
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.host_group_membership_evidence (
			host_group_id,host_id,membership_generation,membership_state,source_type,source_revision,evidence_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (host_group_id,host_id,membership_generation) DO NOTHING
	`, membership.HostGroupID, membership.HostID, membership.Generation, membership.State,
		membership.SourceType, membership.SourceRevision, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var existingDigest string
		if err := tx.QueryRow(ctx, `SELECT evidence_digest FROM kim.host_group_membership_evidence WHERE host_group_id=$1 AND host_id=$2 AND membership_generation=$3`, membership.HostGroupID, membership.HostID, membership.Generation).Scan(&existingDigest); err != nil {
			return err
		}
		if existingDigest != digest {
			return ErrHostGroupConflict
		}
	}
	tag, err = tx.Exec(ctx, `
		INSERT INTO kim.host_group_memberships_current (
			host_group_id,host_id,membership_generation,membership_state,source_type,source_revision,evidence_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (host_group_id,host_id) DO UPDATE SET
			membership_generation=EXCLUDED.membership_generation,
			membership_state=EXCLUDED.membership_state,
			source_type=EXCLUDED.source_type,
			source_revision=EXCLUDED.source_revision,
			evidence_digest=EXCLUDED.evidence_digest,
			updated_at=statement_timestamp()
		WHERE kim.host_group_memberships_current.membership_generation < EXCLUDED.membership_generation
	`, membership.HostGroupID, membership.HostID, membership.Generation, membership.State,
		membership.SourceType, membership.SourceRevision, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var generation uint64
	var currentDigest string
	if err := tx.QueryRow(ctx, `SELECT membership_generation,evidence_digest FROM kim.host_group_memberships_current WHERE host_group_id=$1 AND host_id=$2`, membership.HostGroupID, membership.HostID).Scan(&generation, &currentDigest); err != nil {
		return err
	}
	if generation == membership.Generation && currentDigest == digest {
		return nil
	}
	return ErrHostGroupConflict
}

func lockHostGroupTx(ctx context.Context, tx pgx.Tx, hostGroupID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "host-group/"+hostGroupID)
	return err
}

func validateHostGroupRevision(revision HostGroupRevision) error {
	if revision.HostGroupID == "" || revision.Generation == 0 || revision.Dimension == "" || revision.Level == "" ||
		!validHostGroupType(revision.GroupType) || !validHostGroupLifecycle(revision.LifecycleState) {
		return errors.New("complete HostGroup revision is required")
	}
	return nil
}

func validateHostGroupMembership(membership HostGroupMembership) error {
	if membership.HostGroupID == "" || membership.HostID == "" || membership.Generation == 0 ||
		membership.SourceRevision == "" || !validHostGroupMembershipState(membership.State) ||
		!validHostGroupMembershipSource(membership.SourceType) {
		return errors.New("complete HostGroup membership is required")
	}
	return nil
}

func validHostGroupType(value string) bool {
	return value == "PLACEMENT_POOL" || value == "FAILURE_DOMAIN" || value == "OPERATIONAL_COHORT"
}

func validHostGroupLifecycle(value string) bool {
	return value == "DRAFT" || value == "ACTIVE" || value == "DRAINING" || value == "RETIRED"
}

func validHostGroupLifecycleTransition(current, next string) bool {
	if current == next {
		return true
	}
	switch current {
	case "DRAFT":
		return next == "ACTIVE"
	case "ACTIVE":
		return next == "DRAINING"
	case "DRAINING":
		return next == "ACTIVE" || next == "RETIRED"
	default:
		return false
	}
}

func validHostGroupMembershipState(value string) bool {
	return value == "ACTIVE" || value == "STALE" || value == "BLOCKED" || value == "REMOVED"
}

func validHostGroupMembershipSource(value string) bool {
	return value == "EXPLICIT" || value == "SELECTOR" || value == "EXTERNAL_ASSERTION" || value == "PLACEMENT_POOL_COMPAT"
}

func validHostGroupSnapshotPurpose(value string) bool {
	return value == "UPGRADE" || value == "MAINTENANCE" || value == "BASELINE_ROLLOUT" || value == "PLACEMENT_AUDIT"
}

func hostGroupRevisionDigest(revision HostGroupRevision) string {
	return digestHostGroupFields(revision.HostGroupID, fmt.Sprint(revision.Generation), revision.GroupType, revision.Dimension, revision.Level, revision.LifecycleState)
}

func hostGroupMembershipDigest(membership HostGroupMembership) string {
	return digestHostGroupFields(membership.HostGroupID, membership.HostID, fmt.Sprint(membership.Generation), membership.State, membership.SourceType, membership.SourceRevision)
}

func hostGroupMembershipSnapshotDigest(members []hostGroupSnapshotMember) string {
	sorted := append([]hostGroupSnapshotMember(nil), members...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].HostID < sorted[j].HostID })
	fields := make([]string, 0, len(sorted)*3)
	for _, member := range sorted {
		fields = append(fields, member.HostID, fmt.Sprint(member.Generation), member.EvidenceDigest)
	}
	return digestHostGroupFields(fields...)
}

func digestHostGroupFields(fields ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(digest[:])
}
