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

type HostGroupMembershipSetRequest struct {
	PublishRequestID, HostGroupID, SourceType, SourceRevision string
	BasedOnHostGroupGeneration, ExpectedCurrentSetGeneration  uint64
	SelectorEvaluationGeneration, HierarchyGeneration         *uint64
	Members                                                   []HostGroupMembership
}

type HostGroupMembershipSet struct {
	PublishRequestID, HostGroupID, CanonicalMemberSetDigest string
	MembershipSetGeneration, BasedOnHostGroupGeneration     uint64
	MemberCount                                             int
}

type HostGroupSnapshotRequest struct {
	SnapshotID, HostGroupID, Purpose string
}

type HostGroupSnapshot struct {
	SnapshotID, HostGroupID, Purpose, MembershipDigest string
	HostGroupGeneration, MembershipSetGeneration       uint64
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

// PublishHostGroupMembershipSet validates and atomically publishes one complete
// HostGroup membership projection. Individual membership evidence written by a
// failed transaction never becomes current set authority.
func PublishHostGroupMembershipSet(ctx context.Context, db TxBeginner, request HostGroupMembershipSetRequest) (HostGroupMembershipSet, error) {
	if err := validateHostGroupMembershipSetRequest(request); err != nil {
		return HostGroupMembershipSet{}, err
	}
	var published HostGroupMembershipSet
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, request.HostGroupID); err != nil {
			return err
		}
		var err error
		published, err = publishHostGroupMembershipSetTx(ctx, tx, request)
		return err
	})
	if err != nil {
		return HostGroupMembershipSet{}, fmt.Errorf("publish HostGroup membership set: %w", err)
	}
	return published, nil
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
			SELECT snapshot_id,host_group_id,host_group_generation,membership_set_generation,purpose,member_count,membership_set_digest
			FROM kim.host_group_membership_snapshot_evidence WHERE snapshot_id=$1
		`, request.SnapshotID).Scan(&existing.SnapshotID, &existing.HostGroupID,
			&existing.HostGroupGeneration, &existing.MembershipSetGeneration, &existing.Purpose, &existing.MemberCount,
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
		var lifecycle, validationState string
		if err := tx.QueryRow(ctx, `
			SELECT group_current.host_group_generation,group_current.lifecycle_state,
			       set_current.membership_set_generation,set_current.canonical_member_set_digest,
			       set_current.validation_state
			FROM kim.host_groups_current group_current
			JOIN kim.host_group_membership_sets_current set_current USING (host_group_id)
			WHERE group_current.host_group_id=$1
			  AND set_current.based_on_host_group_generation=group_current.host_group_generation
			FOR SHARE
		`, request.HostGroupID).Scan(&snapshot.HostGroupGeneration, &lifecycle,
			&snapshot.MembershipSetGeneration, &snapshot.MembershipDigest, &validationState); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrHostGroupConflict
			}
			return err
		}
		if lifecycle != "ACTIVE" || validationState != "ACCEPTED" {
			return ErrHostGroupConflict
		}
		rows, err := tx.Query(ctx, `
			SELECT host_id,membership_generation,membership_evidence_digest
			FROM kim.host_group_membership_set_member_evidence
			WHERE host_group_id=$1 AND membership_set_generation=$2 AND membership_state='ACTIVE'
			ORDER BY host_id
			FOR SHARE
		`, request.HostGroupID, snapshot.MembershipSetGeneration)
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
			MembershipSetGeneration: snapshot.MembershipSetGeneration,
			MemberCount:             len(members), MembershipDigest: snapshot.MembershipDigest,
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_membership_snapshot_evidence (
				snapshot_id,host_group_id,host_group_generation,membership_set_generation,
				purpose,member_count,membership_digest,membership_set_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
			ON CONFLICT (snapshot_id) DO NOTHING
		`, snapshot.SnapshotID, snapshot.HostGroupID, snapshot.HostGroupGeneration,
			snapshot.MembershipSetGeneration, snapshot.Purpose, snapshot.MemberCount, snapshot.MembershipDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if err := tx.QueryRow(ctx, `
				SELECT snapshot_id,host_group_id,host_group_generation,membership_set_generation,purpose,member_count,membership_set_digest
				FROM kim.host_group_membership_snapshot_evidence WHERE snapshot_id=$1
			`, request.SnapshotID).Scan(&existing.SnapshotID, &existing.HostGroupID,
				&existing.HostGroupGeneration, &existing.MembershipSetGeneration, &existing.Purpose, &existing.MemberCount,
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
					snapshot_id,host_group_id,host_id,membership_generation,membership_evidence_digest,membership_set_generation
				) VALUES ($1,$2,$3,$4,$5,$6)
			`, snapshot.SnapshotID, snapshot.HostGroupID, member.HostID, member.Generation, member.EvidenceDigest, snapshot.MembershipSetGeneration); err != nil {
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
	var groupGeneration, currentSetGeneration uint64
	if err := tx.QueryRow(ctx, `
		SELECT group_current.host_group_generation,COALESCE(set_current.membership_set_generation,0)
		FROM kim.host_groups_current group_current
		LEFT JOIN kim.host_group_membership_sets_current set_current USING (host_group_id)
		WHERE group_current.host_group_id=$1 AND group_current.lifecycle_state='ACTIVE'
		FOR SHARE OF group_current
	`, membership.HostGroupID).Scan(&groupGeneration, &currentSetGeneration); err != nil {
		return err
	}
	members, err := loadCurrentHostGroupMemberships(ctx, tx, membership.HostGroupID)
	if err != nil {
		return err
	}
	replaced := false
	for index := range members {
		if members[index].HostID == membership.HostID {
			members[index] = membership
			replaced = true
			break
		}
	}
	if !replaced {
		members = append(members, membership)
	}
	_, err = publishHostGroupMembershipSetTx(ctx, tx, HostGroupMembershipSetRequest{
		PublishRequestID: "membership/" + hostGroupMembershipDigest(membership),
		HostGroupID:      membership.HostGroupID, SourceType: membership.SourceType,
		SourceRevision: membership.SourceRevision, BasedOnHostGroupGeneration: groupGeneration,
		ExpectedCurrentSetGeneration: currentSetGeneration, Members: members,
	})
	return err
}

func publishHostGroupMembershipSetTx(ctx context.Context, tx pgx.Tx, request HostGroupMembershipSetRequest) (HostGroupMembershipSet, error) {
	requestedMembers := append([]HostGroupMembership(nil), request.Members...)
	sort.Slice(requestedMembers, func(i, j int) bool { return requestedMembers[i].HostID < requestedMembers[j].HostID })
	requestedDigests := make([]hostGroupSnapshotMember, 0, len(requestedMembers))
	for _, member := range requestedMembers {
		requestedDigests = append(requestedDigests, hostGroupSnapshotMember{
			HostID: member.HostID, Generation: member.Generation,
			EvidenceDigest: hostGroupMembershipDigest(member),
		})
	}
	requestDigest := hostGroupMembershipSetRequestDigest(request, hostGroupMembershipSnapshotDigest(requestedDigests))
	var existing HostGroupMembershipSet
	var existingRequestDigest string
	err := tx.QueryRow(ctx, `
		SELECT publish_request_id,host_group_id,membership_set_generation,
		       based_on_host_group_generation,canonical_member_set_digest,member_count,request_digest
		FROM kim.host_group_membership_set_evidence WHERE publish_request_id=$1
	`, request.PublishRequestID).Scan(&existing.PublishRequestID, &existing.HostGroupID,
		&existing.MembershipSetGeneration, &existing.BasedOnHostGroupGeneration,
		&existing.CanonicalMemberSetDigest, &existing.MemberCount, &existingRequestDigest)
	if err == nil {
		if existingRequestDigest != requestDigest {
			return HostGroupMembershipSet{}, ErrHostGroupConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return HostGroupMembershipSet{}, err
	}
	var currentGroupGeneration uint64
	var lifecycle string
	if err := tx.QueryRow(ctx, `SELECT host_group_generation,lifecycle_state FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE`, request.HostGroupID).Scan(&currentGroupGeneration, &lifecycle); err != nil {
		return HostGroupMembershipSet{}, err
	}
	if lifecycle != "ACTIVE" || currentGroupGeneration != request.BasedOnHostGroupGeneration {
		return HostGroupMembershipSet{}, ErrHostGroupConflict
	}
	var currentSetGeneration uint64
	requestedSetDigest := hostGroupMembershipSnapshotDigest(requestedDigests)
	err = tx.QueryRow(ctx, `
		SELECT evidence.publish_request_id,evidence.host_group_id,evidence.membership_set_generation,
		       evidence.based_on_host_group_generation,evidence.canonical_member_set_digest,evidence.member_count
		FROM kim.host_group_membership_sets_current current_set
		JOIN kim.host_group_membership_set_evidence evidence
		  ON evidence.host_group_id=current_set.host_group_id
		 AND evidence.membership_set_generation=current_set.membership_set_generation
		WHERE evidence.host_group_id=$1 AND evidence.based_on_host_group_generation=$2
		  AND evidence.source_type=$3 AND evidence.source_revision=$4
		  AND evidence.selector_evaluation_generation IS NOT DISTINCT FROM $5
		  AND evidence.hierarchy_generation IS NOT DISTINCT FROM $6
		  AND evidence.canonical_member_set_digest=$7
	`, request.HostGroupID, request.BasedOnHostGroupGeneration, request.SourceType,
		request.SourceRevision, request.SelectorEvaluationGeneration, request.HierarchyGeneration,
		requestedSetDigest).Scan(&existing.PublishRequestID, &existing.HostGroupID,
		&existing.MembershipSetGeneration, &existing.BasedOnHostGroupGeneration,
		&existing.CanonicalMemberSetDigest, &existing.MemberCount)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return HostGroupMembershipSet{}, err
	}
	err = tx.QueryRow(ctx, `SELECT membership_set_generation FROM kim.host_group_membership_sets_current WHERE host_group_id=$1 FOR UPDATE`, request.HostGroupID).Scan(&currentSetGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		currentSetGeneration = 0
	} else if err != nil {
		return HostGroupMembershipSet{}, err
	}
	if request.ExpectedCurrentSetGeneration != currentSetGeneration {
		return HostGroupMembershipSet{}, ErrHostGroupConflict
	}

	members := append([]HostGroupMembership(nil), request.Members...)
	currentMembers, err := loadCurrentHostGroupMemberships(ctx, tx, request.HostGroupID)
	if err != nil {
		return HostGroupMembershipSet{}, err
	}
	provided := make(map[string]struct{}, len(members))
	for _, member := range members {
		provided[member.HostID] = struct{}{}
	}
	currentByHost := make(map[string]HostGroupMembership, len(currentMembers))
	for _, current := range currentMembers {
		currentByHost[current.HostID] = current
	}
	for _, member := range members {
		if current, exists := currentByHost[member.HostID]; exists && member.Generation < current.Generation {
			return HostGroupMembershipSet{}, ErrHostGroupConflict
		}
	}
	for _, old := range currentMembers {
		if _, exists := provided[old.HostID]; exists {
			continue
		}
		if old.State != "REMOVED" {
			old.Generation++
			old.State = "REMOVED"
			old.SourceType = request.SourceType
			old.SourceRevision = request.SourceRevision
		}
		members = append(members, old)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].HostID < members[j].HostID })
	for _, member := range members {
		if err := lockHostAuthorityTx(ctx, tx, member.HostID); err != nil {
			return HostGroupMembershipSet{}, err
		}
	}

	memberDigests := make([]hostGroupSnapshotMember, 0, len(members))
	memberCount := 0
	for _, member := range members {
		digest := hostGroupMembershipDigest(member)
		memberDigests = append(memberDigests, hostGroupSnapshotMember{HostID: member.HostID, Generation: member.Generation, EvidenceDigest: digest})
		if member.State != "REMOVED" {
			memberCount++
		}
	}
	setDigest := hostGroupMembershipSnapshotDigest(memberDigests)
	err = tx.QueryRow(ctx, `
		SELECT evidence.publish_request_id,evidence.host_group_id,evidence.membership_set_generation,
		       evidence.based_on_host_group_generation,evidence.canonical_member_set_digest,evidence.member_count
		FROM kim.host_group_membership_sets_current current_set
		JOIN kim.host_group_membership_set_evidence evidence
		  ON evidence.host_group_id=current_set.host_group_id
		 AND evidence.membership_set_generation=current_set.membership_set_generation
		WHERE evidence.host_group_id=$1 AND evidence.based_on_host_group_generation=$2
		  AND evidence.source_type=$3 AND evidence.source_revision=$4
		  AND evidence.selector_evaluation_generation IS NOT DISTINCT FROM $5
		  AND evidence.hierarchy_generation IS NOT DISTINCT FROM $6
		  AND evidence.canonical_member_set_digest=$7
	`, request.HostGroupID, request.BasedOnHostGroupGeneration, request.SourceType,
		request.SourceRevision, request.SelectorEvaluationGeneration, request.HierarchyGeneration,
		setDigest).Scan(&existing.PublishRequestID, &existing.HostGroupID,
		&existing.MembershipSetGeneration, &existing.BasedOnHostGroupGeneration,
		&existing.CanonicalMemberSetDigest, &existing.MemberCount)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return HostGroupMembershipSet{}, err
	}

	setGeneration := currentSetGeneration + 1
	for index, member := range members {
		digest := memberDigests[index].EvidenceDigest
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_membership_evidence (
				host_group_id,host_id,membership_generation,membership_state,source_type,source_revision,evidence_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING
		`, member.HostGroupID, member.HostID, member.Generation, member.State, member.SourceType, member.SourceRevision, digest)
		if err != nil {
			return HostGroupMembershipSet{}, err
		}
		if tag.RowsAffected() == 0 {
			var recorded string
			if err := tx.QueryRow(ctx, `SELECT evidence_digest FROM kim.host_group_membership_evidence WHERE host_group_id=$1 AND host_id=$2 AND membership_generation=$3`, member.HostGroupID, member.HostID, member.Generation).Scan(&recorded); err != nil || recorded != digest {
				return HostGroupMembershipSet{}, ErrHostGroupConflict
			}
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.host_group_membership_set_evidence (
			host_group_id,membership_set_generation,based_on_host_group_generation,
			publish_request_id,request_digest,source_type,source_revision,
			selector_evaluation_generation,hierarchy_generation,
			canonical_member_set_digest,member_count,validation_state
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'ACCEPTED')
	`, request.HostGroupID, setGeneration, request.BasedOnHostGroupGeneration,
		request.PublishRequestID, requestDigest, request.SourceType, request.SourceRevision,
		request.SelectorEvaluationGeneration, request.HierarchyGeneration, setDigest, memberCount)
	if err != nil {
		return HostGroupMembershipSet{}, err
	}
	for index, member := range members {
		digest := memberDigests[index].EvidenceDigest
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_membership_set_member_evidence (
				host_group_id,membership_set_generation,host_id,membership_generation,
				membership_state,source_type,source_revision,membership_evidence_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, request.HostGroupID, setGeneration, member.HostID, member.Generation,
			member.State, member.SourceType, member.SourceRevision, digest); err != nil {
			return HostGroupMembershipSet{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_memberships_current (
				host_group_id,host_id,membership_generation,membership_state,
				source_type,source_revision,evidence_digest,membership_set_generation
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (host_group_id,host_id) DO UPDATE SET
				membership_generation=EXCLUDED.membership_generation,
				membership_state=EXCLUDED.membership_state,
				source_type=EXCLUDED.source_type,source_revision=EXCLUDED.source_revision,
				evidence_digest=EXCLUDED.evidence_digest,
				membership_set_generation=EXCLUDED.membership_set_generation,
				updated_at=statement_timestamp()
		`, request.HostGroupID, member.HostID, member.Generation, member.State,
			member.SourceType, member.SourceRevision, digest, setGeneration); err != nil {
			return HostGroupMembershipSet{}, err
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.host_group_membership_sets_current (
			host_group_id,membership_set_generation,based_on_host_group_generation,
			canonical_member_set_digest,member_count,validation_state
		) VALUES ($1,$2,$3,$4,$5,'ACCEPTED')
		ON CONFLICT (host_group_id) DO UPDATE SET
			membership_set_generation=EXCLUDED.membership_set_generation,
			based_on_host_group_generation=EXCLUDED.based_on_host_group_generation,
			canonical_member_set_digest=EXCLUDED.canonical_member_set_digest,
			member_count=EXCLUDED.member_count,validation_state='ACCEPTED',updated_at=statement_timestamp()
	`, request.HostGroupID, setGeneration, request.BasedOnHostGroupGeneration, setDigest, memberCount)
	if err != nil {
		return HostGroupMembershipSet{}, err
	}
	return HostGroupMembershipSet{PublishRequestID: request.PublishRequestID,
		HostGroupID: request.HostGroupID, MembershipSetGeneration: setGeneration,
		BasedOnHostGroupGeneration: request.BasedOnHostGroupGeneration,
		CanonicalMemberSetDigest:   setDigest, MemberCount: memberCount}, nil
}

func loadCurrentHostGroupMemberships(ctx context.Context, row pgx.Tx, hostGroupID string) ([]HostGroupMembership, error) {
	rows, err := row.Query(ctx, `SELECT host_id,membership_generation,membership_state,source_type,source_revision FROM kim.host_group_memberships_current WHERE host_group_id=$1 ORDER BY host_id`, hostGroupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]HostGroupMembership, 0)
	for rows.Next() {
		member := HostGroupMembership{HostGroupID: hostGroupID}
		if err := rows.Scan(&member.HostID, &member.Generation, &member.State, &member.SourceType, &member.SourceRevision); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
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

func validateHostGroupMembershipSetRequest(request HostGroupMembershipSetRequest) error {
	if request.PublishRequestID == "" || request.HostGroupID == "" || request.BasedOnHostGroupGeneration == 0 ||
		request.SourceRevision == "" || !validHostGroupMembershipSource(request.SourceType) ||
		(request.SelectorEvaluationGeneration != nil && *request.SelectorEvaluationGeneration == 0) ||
		(request.HierarchyGeneration != nil && *request.HierarchyGeneration == 0) {
		return errors.New("complete HostGroup membership set request is required")
	}
	seen := make(map[string]struct{}, len(request.Members))
	for _, member := range request.Members {
		if err := validateHostGroupMembership(member); err != nil || member.HostGroupID != request.HostGroupID {
			return errors.New("invalid HostGroup membership set member")
		}
		if _, duplicate := seen[member.HostID]; duplicate {
			return errors.New("duplicate HostGroup membership set member")
		}
		seen[member.HostID] = struct{}{}
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

func hostGroupMembershipSetRequestDigest(request HostGroupMembershipSetRequest, setDigest string) string {
	selectorGeneration, hierarchyGeneration := "", ""
	if request.SelectorEvaluationGeneration != nil {
		selectorGeneration = fmt.Sprint(*request.SelectorEvaluationGeneration)
	}
	if request.HierarchyGeneration != nil {
		hierarchyGeneration = fmt.Sprint(*request.HierarchyGeneration)
	}
	return digestHostGroupFields(request.HostGroupID,
		fmt.Sprint(request.BasedOnHostGroupGeneration), request.SourceType,
		request.SourceRevision, selectorGeneration, hierarchyGeneration, setDigest)
}

func digestHostGroupFields(fields ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(digest[:])
}
