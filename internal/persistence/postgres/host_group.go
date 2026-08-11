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
	SelectorID, SelectorEvaluationID                          string
	SelectorGeneration, SelectorEvaluationGeneration          *uint64
	HierarchyGeneration                                       *uint64
	Members                                                   []HostGroupMembership
}

type HostGroupMembershipSet struct {
	PublishRequestID, HostGroupID, CanonicalMemberSetDigest string
	MembershipSetGeneration, BasedOnHostGroupGeneration     uint64
	CardinalityPolicyID, Cardinality                        string
	CardinalityPolicyGeneration                             uint64
	HierarchyID                                             string
	HierarchyGeneration                                     uint64
	SelectorID, SelectorEvaluationID                        string
	SelectorGeneration, SelectorEvaluationGeneration        uint64
	MemberCount                                             int
}

type HostGroupCardinalityPolicy struct {
	PolicyID, GroupType, Dimension, Level, ScopeType, ScopeID string
	Cardinality, State                                        string
	Generation                                                uint64
}

type HostGroupSnapshotRequest struct {
	SnapshotID, HostGroupID, Purpose string
}

type HostGroupSnapshot struct {
	SnapshotID, HostGroupID, Purpose, MembershipDigest, SnapshotDigest string
	HostGroupGeneration, MembershipSetGeneration                       uint64
	SelectorID, SelectorEvaluationID                                   string
	SelectorGeneration, SelectorEvaluationGeneration                   uint64
	CardinalityPolicyID, Cardinality                                   string
	CardinalityPolicyGeneration                                        uint64
	HierarchyID                                                        string
	HierarchyGeneration                                                uint64
	MemberCount                                                        int
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
		return upsertHostGroupAndPolicyTx(ctx, tx, revision)
	})
}

func UpsertHostGroupCardinalityPolicy(ctx context.Context, db TxBeginner, policy HostGroupCardinalityPolicy) error {
	if err := validateHostGroupCardinalityPolicy(policy); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupCardinalityScopeTx(ctx, tx, policy.GroupType, policy.Dimension, policy.Level); err != nil {
			return err
		}
		return upsertHostGroupCardinalityPolicyTx(ctx, tx, policy)
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
			SELECT snapshot_id,host_group_id,host_group_generation,membership_set_generation,purpose,member_count,membership_set_digest,
			       COALESCE(selector_id,''),COALESCE(selector_generation,0),COALESCE(selector_evaluation_id,''),
			       COALESCE(selector_evaluation_generation,0),COALESCE(cardinality_policy_id,''),
			       COALESCE(cardinality_policy_generation,0),COALESCE(cardinality,''),
			       COALESCE(hierarchy_id,''),COALESCE(hierarchy_generation,0),COALESCE(snapshot_digest,'')
			FROM kim.host_group_membership_snapshot_evidence WHERE snapshot_id=$1
		`, request.SnapshotID).Scan(&existing.SnapshotID, &existing.HostGroupID,
			&existing.HostGroupGeneration, &existing.MembershipSetGeneration, &existing.Purpose, &existing.MemberCount,
			&existing.MembershipDigest, &existing.SelectorID, &existing.SelectorGeneration,
			&existing.SelectorEvaluationID, &existing.SelectorEvaluationGeneration, &existing.CardinalityPolicyID,
			&existing.CardinalityPolicyGeneration, &existing.Cardinality, &existing.HierarchyID,
			&existing.HierarchyGeneration, &existing.SnapshotDigest)
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
			       set_current.validation_state,COALESCE(set_current.selector_id,''),
			       COALESCE(set_current.selector_generation,0),COALESCE(set_current.selector_evaluation_id,''),
			       COALESCE(set_current.selector_evaluation_generation,0),set_current.cardinality_policy_id,
			       set_current.cardinality_policy_generation,set_current.cardinality,
			       COALESCE(set_current.hierarchy_id,''),COALESCE(set_current.hierarchy_generation,0)
			FROM kim.host_groups_current group_current
			JOIN kim.host_group_membership_sets_current set_current USING (host_group_id)
			JOIN kim.host_group_cardinality_policies_current policy
			  ON policy.group_type=group_current.group_type AND policy.dimension=group_current.dimension
			 AND policy.level=group_current.level AND policy.scope_type='SYSTEM' AND policy.scope_id='system'
			LEFT JOIN kim.host_group_hierarchy_sets_current hierarchy_current
			  ON hierarchy_current.group_type=group_current.group_type
			 AND hierarchy_current.dimension=group_current.dimension
			 AND hierarchy_current.scope_type='SYSTEM' AND hierarchy_current.scope_id='system'
			LEFT JOIN kim.host_group_selectors_current selector_current
			  ON selector_current.host_group_id=group_current.host_group_id
			WHERE group_current.host_group_id=$1
			  AND set_current.based_on_host_group_generation=group_current.host_group_generation
			  AND policy.policy_state='ACTIVE'
			  AND (
			    (selector_current.selector_id IS NULL AND set_current.selector_id IS NULL)
			    OR (
			      selector_current.selector_generation=set_current.selector_generation
			      AND selector_current.host_group_id=group_current.host_group_id
			      AND selector_current.based_on_host_group_generation=group_current.host_group_generation
			      AND selector_current.lifecycle_state='ACTIVE'
			    )
			  )
			  AND ((set_current.cardinality_policy_id=policy.cardinality_policy_id
			        AND set_current.cardinality_policy_generation=policy.policy_generation
			        AND set_current.cardinality=policy.cardinality)
			       OR (set_current.cardinality_policy_id IS NULL
			           AND policy.policy_generation=1 AND policy.cardinality='MANY'))
			  AND (
			    (hierarchy_current.hierarchy_id IS NULL AND set_current.hierarchy_id IS NULL)
			    OR (
			      set_current.hierarchy_id=hierarchy_current.hierarchy_id
			      AND set_current.hierarchy_generation=hierarchy_current.hierarchy_generation
			      AND EXISTS (
			        SELECT 1 FROM kim.host_group_hierarchy_node_evidence node
			        WHERE node.hierarchy_id=hierarchy_current.hierarchy_id
			          AND node.hierarchy_generation=hierarchy_current.hierarchy_generation
			          AND node.host_group_id=group_current.host_group_id
			          AND node.host_group_generation=group_current.host_group_generation
			          AND node.level=group_current.level
			      )
			      AND NOT EXISTS (
			        SELECT 1
			        FROM kim.host_group_hierarchy_node_evidence graph_node
			        JOIN kim.host_groups_current graph_group ON graph_group.host_group_id=graph_node.host_group_id
			        WHERE graph_node.hierarchy_id=hierarchy_current.hierarchy_id
			          AND graph_node.hierarchy_generation=hierarchy_current.hierarchy_generation
			          AND (graph_node.host_group_generation<>graph_group.host_group_generation
			               OR graph_node.level<>graph_group.level OR graph_group.lifecycle_state<>'ACTIVE')
			      )
			    )
			  )
			FOR SHARE OF group_current,set_current,policy
		`, request.HostGroupID).Scan(&snapshot.HostGroupGeneration, &lifecycle,
			&snapshot.MembershipSetGeneration, &snapshot.MembershipDigest, &validationState,
			&snapshot.SelectorID, &snapshot.SelectorGeneration, &snapshot.SelectorEvaluationID,
			&snapshot.SelectorEvaluationGeneration, &snapshot.CardinalityPolicyID,
			&snapshot.CardinalityPolicyGeneration, &snapshot.Cardinality,
			&snapshot.HierarchyID, &snapshot.HierarchyGeneration); err != nil {
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
		var projectionMatches bool
		if err := tx.QueryRow(ctx, `
			SELECT NOT EXISTS (
			  (SELECT host_id,membership_generation,evidence_digest FROM kim.host_group_memberships_current
			   WHERE host_group_id=$1 AND membership_state='ACTIVE'
			   EXCEPT
			   SELECT host_id,membership_generation,membership_evidence_digest FROM kim.host_group_membership_set_member_evidence
			   WHERE host_group_id=$1 AND membership_set_generation=$2 AND membership_state='ACTIVE')
			  UNION ALL
			  (SELECT host_id,membership_generation,membership_evidence_digest FROM kim.host_group_membership_set_member_evidence
			   WHERE host_group_id=$1 AND membership_set_generation=$2 AND membership_state='ACTIVE'
			   EXCEPT
			   SELECT host_id,membership_generation,evidence_digest FROM kim.host_group_memberships_current
			   WHERE host_group_id=$1 AND membership_state='ACTIVE')
			)
		`, request.HostGroupID, snapshot.MembershipSetGeneration).Scan(&projectionMatches); err != nil || !projectionMatches {
			if err != nil {
				return err
			}
			return ErrHostGroupConflict
		}
		snapshot = HostGroupSnapshot{
			SnapshotID: request.SnapshotID, HostGroupID: request.HostGroupID,
			Purpose: request.Purpose, HostGroupGeneration: snapshot.HostGroupGeneration,
			MembershipSetGeneration: snapshot.MembershipSetGeneration,
			HierarchyID:             snapshot.HierarchyID, HierarchyGeneration: snapshot.HierarchyGeneration,
			SelectorID: snapshot.SelectorID, SelectorGeneration: snapshot.SelectorGeneration,
			SelectorEvaluationID:         snapshot.SelectorEvaluationID,
			SelectorEvaluationGeneration: snapshot.SelectorEvaluationGeneration,
			CardinalityPolicyID:          snapshot.CardinalityPolicyID,
			CardinalityPolicyGeneration:  snapshot.CardinalityPolicyGeneration, Cardinality: snapshot.Cardinality,
			MemberCount: len(members), MembershipDigest: snapshot.MembershipDigest,
		}
		snapshot.SnapshotDigest = hostGroupSnapshotDigest(snapshot)
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_membership_snapshot_evidence (
				snapshot_id,host_group_id,host_group_generation,membership_set_generation,
				purpose,member_count,membership_digest,membership_set_digest,
				hierarchy_id,hierarchy_generation,selector_id,selector_generation,
				selector_evaluation_id,selector_evaluation_generation,cardinality_policy_id,
				cardinality_policy_generation,cardinality,snapshot_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT (snapshot_id) DO NOTHING
		`, snapshot.SnapshotID, snapshot.HostGroupID, snapshot.HostGroupGeneration,
			snapshot.MembershipSetGeneration, snapshot.Purpose, snapshot.MemberCount, snapshot.MembershipDigest,
			nullableSnapshotHierarchyID(snapshot), nullableSnapshotHierarchyGeneration(snapshot),
			nullableSnapshotSelectorID(snapshot), nullableSnapshotGeneration(snapshot.SelectorGeneration),
			nullableSnapshotSelectorEvaluationID(snapshot), nullableSnapshotGeneration(snapshot.SelectorEvaluationGeneration),
			snapshot.CardinalityPolicyID, snapshot.CardinalityPolicyGeneration, snapshot.Cardinality, snapshot.SnapshotDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if err := tx.QueryRow(ctx, `
				SELECT snapshot_id,host_group_id,host_group_generation,membership_set_generation,purpose,member_count,membership_set_digest,
				       COALESCE(selector_id,''),COALESCE(selector_generation,0),COALESCE(selector_evaluation_id,''),
				       COALESCE(selector_evaluation_generation,0),COALESCE(cardinality_policy_id,''),
				       COALESCE(cardinality_policy_generation,0),COALESCE(cardinality,''),
				       COALESCE(hierarchy_id,''),COALESCE(hierarchy_generation,0),COALESCE(snapshot_digest,'')
				FROM kim.host_group_membership_snapshot_evidence WHERE snapshot_id=$1
			`, request.SnapshotID).Scan(&existing.SnapshotID, &existing.HostGroupID,
				&existing.HostGroupGeneration, &existing.MembershipSetGeneration, &existing.Purpose, &existing.MemberCount,
				&existing.MembershipDigest, &existing.SelectorID, &existing.SelectorGeneration,
				&existing.SelectorEvaluationID, &existing.SelectorEvaluationGeneration, &existing.CardinalityPolicyID,
				&existing.CardinalityPolicyGeneration, &existing.Cardinality, &existing.HierarchyID,
				&existing.HierarchyGeneration, &existing.SnapshotDigest); err != nil {
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

func nullableSnapshotHierarchyID(snapshot HostGroupSnapshot) any {
	if snapshot.HierarchyGeneration == 0 {
		return nil
	}
	return snapshot.HierarchyID
}

func nullableSnapshotHierarchyGeneration(snapshot HostGroupSnapshot) any {
	if snapshot.HierarchyGeneration == 0 {
		return nil
	}
	return snapshot.HierarchyGeneration
}

func nullableSnapshotSelectorID(snapshot HostGroupSnapshot) any {
	if snapshot.SelectorID == "" {
		return nil
	}
	return snapshot.SelectorID
}

func nullableSnapshotSelectorEvaluationID(snapshot HostGroupSnapshot) any {
	if snapshot.SelectorEvaluationID == "" {
		return nil
	}
	return snapshot.SelectorEvaluationID
}

func nullableSnapshotGeneration(generation uint64) any {
	if generation == 0 {
		return nil
	}
	return generation
}

func hostGroupSnapshotDigest(snapshot HostGroupSnapshot) string {
	return digestHostGroupFields(snapshot.SnapshotID, snapshot.HostGroupID,
		fmt.Sprint(snapshot.HostGroupGeneration), fmt.Sprint(snapshot.MembershipSetGeneration),
		snapshot.Purpose, fmt.Sprint(snapshot.MemberCount), snapshot.MembershipDigest,
		snapshot.SelectorID, fmt.Sprint(snapshot.SelectorGeneration), snapshot.SelectorEvaluationID,
		fmt.Sprint(snapshot.SelectorEvaluationGeneration), snapshot.CardinalityPolicyID,
		fmt.Sprint(snapshot.CardinalityPolicyGeneration), snapshot.Cardinality,
		snapshot.HierarchyID, fmt.Sprint(snapshot.HierarchyGeneration))
}

func upsertHostGroupAndPolicyTx(ctx context.Context, tx pgx.Tx, revision HostGroupRevision) error {
	if err := upsertHostGroupTx(ctx, tx, revision); err != nil {
		return err
	}
	return ensureDefaultHostGroupCardinalityPolicyTx(ctx, tx, revision.GroupType, revision.Dimension, revision.Level)
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
	var groupType, dimension, level string
	if err := tx.QueryRow(ctx, `
		SELECT group_current.host_group_generation,COALESCE(set_current.membership_set_generation,0),
		       group_current.group_type,group_current.dimension,group_current.level
		FROM kim.host_groups_current group_current
		LEFT JOIN kim.host_group_membership_sets_current set_current USING (host_group_id)
		WHERE group_current.host_group_id=$1 AND group_current.lifecycle_state='ACTIVE'
		FOR SHARE OF group_current
	`, membership.HostGroupID).Scan(&groupGeneration, &currentSetGeneration, &groupType, &dimension, &level); err != nil {
		return err
	}
	if err := lockHostGroupCardinalityScopeTx(ctx, tx, groupType, dimension, level); err != nil {
		return err
	}
	policy, err := loadCurrentHostGroupCardinalityPolicyTx(ctx, tx, groupType, dimension, level)
	if err != nil || policy.State != "ACTIVE" {
		return ErrHostGroupConflict
	}
	if err := lockHostGroupHierarchyScopeTx(ctx, tx, groupType, dimension); err != nil {
		return err
	}
	hierarchy, err := resolveCurrentHostGroupHierarchyTx(ctx, tx, groupType, dimension, membership.HostGroupID)
	if err != nil {
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
	request := HostGroupMembershipSetRequest{
		PublishRequestID: "membership/" + digestHostGroupFields(
			hostGroupMembershipDigest(membership), fmt.Sprint(groupGeneration),
			fmt.Sprint(currentSetGeneration), policy.PolicyID, fmt.Sprint(policy.Generation),
			hierarchy.HierarchyID, fmt.Sprint(hierarchy.Generation)),
		HostGroupID: membership.HostGroupID, SourceType: membership.SourceType,
		SourceRevision: membership.SourceRevision, BasedOnHostGroupGeneration: groupGeneration,
		ExpectedCurrentSetGeneration: currentSetGeneration, Members: members,
	}
	if hierarchy.Generation != 0 {
		request.HierarchyGeneration = &hierarchy.Generation
	}
	_, err = publishHostGroupMembershipSetTx(ctx, tx, request)
	return err
}

func publishHostGroupMembershipSetTx(ctx context.Context, tx pgx.Tx, request HostGroupMembershipSetRequest) (HostGroupMembershipSet, error) {
	if err := validateHostGroupMembershipSetRequest(request); err != nil {
		return HostGroupMembershipSet{}, err
	}
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
		       based_on_host_group_generation,canonical_member_set_digest,member_count,request_digest,
		       COALESCE(cardinality_policy_id,''),COALESCE(cardinality_policy_generation,0),COALESCE(cardinality,''),
		       COALESCE(hierarchy_id,''),COALESCE(hierarchy_generation,0),
		       COALESCE(selector_id,''),COALESCE(selector_generation,0),
		       COALESCE(selector_evaluation_id,''),COALESCE(selector_evaluation_generation,0)
		FROM kim.host_group_membership_set_evidence WHERE publish_request_id=$1
	`, request.PublishRequestID).Scan(&existing.PublishRequestID, &existing.HostGroupID,
		&existing.MembershipSetGeneration, &existing.BasedOnHostGroupGeneration,
		&existing.CanonicalMemberSetDigest, &existing.MemberCount, &existingRequestDigest,
		&existing.CardinalityPolicyID, &existing.CardinalityPolicyGeneration, &existing.Cardinality,
		&existing.HierarchyID, &existing.HierarchyGeneration, &existing.SelectorID,
		&existing.SelectorGeneration, &existing.SelectorEvaluationID,
		&existing.SelectorEvaluationGeneration)
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
	var lifecycle, groupType, dimension, level string
	if err := tx.QueryRow(ctx, `SELECT host_group_generation,lifecycle_state,group_type,dimension,level FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE`, request.HostGroupID).Scan(&currentGroupGeneration, &lifecycle, &groupType, &dimension, &level); err != nil {
		return HostGroupMembershipSet{}, err
	}
	if lifecycle != "ACTIVE" || currentGroupGeneration != request.BasedOnHostGroupGeneration {
		return HostGroupMembershipSet{}, ErrHostGroupConflict
	}
	if err := lockHostGroupCardinalityScopeTx(ctx, tx, groupType, dimension, level); err != nil {
		return HostGroupMembershipSet{}, err
	}
	policy, err := loadCurrentHostGroupCardinalityPolicyTx(ctx, tx, groupType, dimension, level)
	if err != nil || policy.State != "ACTIVE" {
		return HostGroupMembershipSet{}, ErrHostGroupConflict
	}
	if err := lockHostGroupHierarchyScopeTx(ctx, tx, groupType, dimension); err != nil {
		return HostGroupMembershipSet{}, err
	}
	hierarchy, err := resolveCurrentHostGroupHierarchyTx(ctx, tx, groupType, dimension, request.HostGroupID)
	if err != nil {
		return HostGroupMembershipSet{}, err
	}
	if hierarchy.Generation == 0 {
		if request.HierarchyGeneration != nil {
			return HostGroupMembershipSet{}, ErrHostGroupConflict
		}
	} else if request.HierarchyGeneration == nil || *request.HierarchyGeneration != hierarchy.Generation {
		return HostGroupMembershipSet{}, ErrHostGroupConflict
	}
	if err := validateSelectorMembershipSetRequestTx(ctx, tx, request, currentGroupGeneration, policy, hierarchy); err != nil {
		return HostGroupMembershipSet{}, err
	}
	var currentSetGeneration uint64
	requestedSetDigest := hostGroupMembershipSnapshotDigest(requestedDigests)
	err = tx.QueryRow(ctx, `
		SELECT evidence.publish_request_id,evidence.host_group_id,evidence.membership_set_generation,
		       evidence.based_on_host_group_generation,evidence.canonical_member_set_digest,evidence.member_count,
		       evidence.cardinality_policy_id,evidence.cardinality_policy_generation,evidence.cardinality,
		       COALESCE(evidence.hierarchy_id,''),COALESCE(evidence.hierarchy_generation,0),
		       COALESCE(evidence.selector_id,''),COALESCE(evidence.selector_generation,0),
		       COALESCE(evidence.selector_evaluation_id,''),COALESCE(evidence.selector_evaluation_generation,0)
		FROM kim.host_group_membership_sets_current current_set
		JOIN kim.host_group_membership_set_evidence evidence
		  ON evidence.host_group_id=current_set.host_group_id
		 AND evidence.membership_set_generation=current_set.membership_set_generation
		WHERE evidence.host_group_id=$1 AND evidence.based_on_host_group_generation=$2
		  AND evidence.source_type=$3 AND evidence.source_revision=$4
		  AND evidence.selector_id IS NOT DISTINCT FROM $5
		  AND evidence.selector_generation IS NOT DISTINCT FROM $6
		  AND evidence.selector_evaluation_id IS NOT DISTINCT FROM $7
		  AND evidence.selector_evaluation_generation IS NOT DISTINCT FROM $8
		  AND evidence.hierarchy_generation IS NOT DISTINCT FROM $9
		  AND evidence.canonical_member_set_digest=$10
		  AND evidence.cardinality_policy_id=$11 AND evidence.cardinality_policy_generation=$12
		  AND evidence.hierarchy_id IS NOT DISTINCT FROM $13
		  AND evidence.hierarchy_generation IS NOT DISTINCT FROM $14
	`, request.HostGroupID, request.BasedOnHostGroupGeneration, request.SourceType,
		request.SourceRevision, nullableSelectorID(request), request.SelectorGeneration,
		nullableSelectorEvaluationID(request), request.SelectorEvaluationGeneration,
		request.HierarchyGeneration, requestedSetDigest, policy.PolicyID, policy.Generation, nullableHierarchyID(hierarchy),
		nullableHierarchyGeneration(hierarchy)).Scan(&existing.PublishRequestID, &existing.HostGroupID,
		&existing.MembershipSetGeneration, &existing.BasedOnHostGroupGeneration,
		&existing.CanonicalMemberSetDigest, &existing.MemberCount, &existing.CardinalityPolicyID,
		&existing.CardinalityPolicyGeneration, &existing.Cardinality,
		&existing.HierarchyID, &existing.HierarchyGeneration, &existing.SelectorID,
		&existing.SelectorGeneration, &existing.SelectorEvaluationID,
		&existing.SelectorEvaluationGeneration)
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
	if err := validateHostGroupCardinalityTx(ctx, tx, request.HostGroupID, groupType, dimension, level, policy.Cardinality, members); err != nil {
		return HostGroupMembershipSet{}, err
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
		       evidence.based_on_host_group_generation,evidence.canonical_member_set_digest,evidence.member_count,
		       evidence.cardinality_policy_id,evidence.cardinality_policy_generation,evidence.cardinality,
		       COALESCE(evidence.hierarchy_id,''),COALESCE(evidence.hierarchy_generation,0),
		       COALESCE(evidence.selector_id,''),COALESCE(evidence.selector_generation,0),
		       COALESCE(evidence.selector_evaluation_id,''),COALESCE(evidence.selector_evaluation_generation,0)
		FROM kim.host_group_membership_sets_current current_set
		JOIN kim.host_group_membership_set_evidence evidence
		  ON evidence.host_group_id=current_set.host_group_id
		 AND evidence.membership_set_generation=current_set.membership_set_generation
		WHERE evidence.host_group_id=$1 AND evidence.based_on_host_group_generation=$2
		  AND evidence.source_type=$3 AND evidence.source_revision=$4
		  AND evidence.selector_id IS NOT DISTINCT FROM $5
		  AND evidence.selector_generation IS NOT DISTINCT FROM $6
		  AND evidence.selector_evaluation_id IS NOT DISTINCT FROM $7
		  AND evidence.selector_evaluation_generation IS NOT DISTINCT FROM $8
		  AND evidence.hierarchy_generation IS NOT DISTINCT FROM $9
		  AND evidence.canonical_member_set_digest=$10
		  AND evidence.cardinality_policy_id=$11 AND evidence.cardinality_policy_generation=$12
		  AND evidence.hierarchy_id IS NOT DISTINCT FROM $13
		  AND evidence.hierarchy_generation IS NOT DISTINCT FROM $14
	`, request.HostGroupID, request.BasedOnHostGroupGeneration, request.SourceType,
		request.SourceRevision, nullableSelectorID(request), request.SelectorGeneration,
		nullableSelectorEvaluationID(request), request.SelectorEvaluationGeneration,
		request.HierarchyGeneration, setDigest, policy.PolicyID, policy.Generation, nullableHierarchyID(hierarchy),
		nullableHierarchyGeneration(hierarchy)).Scan(&existing.PublishRequestID, &existing.HostGroupID,
		&existing.MembershipSetGeneration, &existing.BasedOnHostGroupGeneration,
		&existing.CanonicalMemberSetDigest, &existing.MemberCount, &existing.CardinalityPolicyID,
		&existing.CardinalityPolicyGeneration, &existing.Cardinality,
		&existing.HierarchyID, &existing.HierarchyGeneration, &existing.SelectorID,
		&existing.SelectorGeneration, &existing.SelectorEvaluationID,
		&existing.SelectorEvaluationGeneration)
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
			selector_id,selector_generation,selector_evaluation_id,
			selector_evaluation_generation,hierarchy_generation,
			canonical_member_set_digest,member_count,validation_state,
			cardinality_policy_id,cardinality_policy_generation,cardinality,hierarchy_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'ACCEPTED',$15,$16,$17,$18)
	`, request.HostGroupID, setGeneration, request.BasedOnHostGroupGeneration,
		request.PublishRequestID, requestDigest, request.SourceType, request.SourceRevision,
		nullableSelectorID(request), request.SelectorGeneration, nullableSelectorEvaluationID(request),
		request.SelectorEvaluationGeneration, request.HierarchyGeneration, setDigest, memberCount,
		policy.PolicyID, policy.Generation, policy.Cardinality, nullableHierarchyID(hierarchy))
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
			canonical_member_set_digest,member_count,validation_state,
			cardinality_policy_id,cardinality_policy_generation,cardinality,
			hierarchy_id,hierarchy_generation,selector_id,selector_generation,
			selector_evaluation_id,selector_evaluation_generation
		) VALUES ($1,$2,$3,$4,$5,'ACCEPTED',$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (host_group_id) DO UPDATE SET
			membership_set_generation=EXCLUDED.membership_set_generation,
			based_on_host_group_generation=EXCLUDED.based_on_host_group_generation,
			canonical_member_set_digest=EXCLUDED.canonical_member_set_digest,
			member_count=EXCLUDED.member_count,validation_state='ACCEPTED',
			cardinality_policy_id=EXCLUDED.cardinality_policy_id,
			cardinality_policy_generation=EXCLUDED.cardinality_policy_generation,
			cardinality=EXCLUDED.cardinality,hierarchy_id=EXCLUDED.hierarchy_id,
			hierarchy_generation=EXCLUDED.hierarchy_generation,
			selector_id=EXCLUDED.selector_id,selector_generation=EXCLUDED.selector_generation,
			selector_evaluation_id=EXCLUDED.selector_evaluation_id,
			selector_evaluation_generation=EXCLUDED.selector_evaluation_generation,
			updated_at=statement_timestamp()
	`, request.HostGroupID, setGeneration, request.BasedOnHostGroupGeneration, setDigest, memberCount,
		policy.PolicyID, policy.Generation, policy.Cardinality,
		nullableHierarchyID(hierarchy), nullableHierarchyGeneration(hierarchy),
		nullableSelectorID(request), request.SelectorGeneration,
		nullableSelectorEvaluationID(request), request.SelectorEvaluationGeneration)
	if err != nil {
		return HostGroupMembershipSet{}, err
	}
	return HostGroupMembershipSet{PublishRequestID: request.PublishRequestID,
		HostGroupID: request.HostGroupID, MembershipSetGeneration: setGeneration,
		BasedOnHostGroupGeneration: request.BasedOnHostGroupGeneration,
		CanonicalMemberSetDigest:   setDigest, CardinalityPolicyID: policy.PolicyID,
		CardinalityPolicyGeneration: policy.Generation, Cardinality: policy.Cardinality,
		HierarchyID: hierarchy.HierarchyID, HierarchyGeneration: hierarchy.Generation,
		SelectorID: request.SelectorID, SelectorEvaluationID: request.SelectorEvaluationID,
		SelectorGeneration:           nullableUint64Value(request.SelectorGeneration),
		SelectorEvaluationGeneration: nullableUint64Value(request.SelectorEvaluationGeneration),
		MemberCount:                  memberCount}, nil
}

func nullableSelectorID(request HostGroupMembershipSetRequest) any {
	if request.SelectorID == "" {
		return nil
	}
	return request.SelectorID
}

func nullableSelectorEvaluationID(request HostGroupMembershipSetRequest) any {
	if request.SelectorEvaluationID == "" {
		return nil
	}
	return request.SelectorEvaluationID
}

func nullableUint64Value(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func nullableHierarchyID(hierarchy currentHostGroupHierarchy) any {
	if hierarchy.Generation == 0 {
		return nil
	}
	return hierarchy.HierarchyID
}

func nullableHierarchyGeneration(hierarchy currentHostGroupHierarchy) any {
	if hierarchy.Generation == 0 {
		return nil
	}
	return hierarchy.Generation
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

func ensureDefaultHostGroupCardinalityPolicyTx(ctx context.Context, tx pgx.Tx, groupType, dimension, level string) error {
	if err := lockHostGroupCardinalityScopeTx(ctx, tx, groupType, dimension, level); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kim.host_group_cardinality_policies_current WHERE group_type=$1 AND dimension=$2 AND level=$3 AND scope_type='SYSTEM' AND scope_id='system')`, groupType, dimension, level).Scan(&exists); err != nil || exists {
		return err
	}
	policyID := "cardinality:" + digestHostGroupFields(groupType, dimension, level, "SYSTEM", "system")
	return upsertHostGroupCardinalityPolicyTx(ctx, tx, HostGroupCardinalityPolicy{
		PolicyID: policyID, Generation: 1, GroupType: groupType, Dimension: dimension, Level: level,
		ScopeType: "SYSTEM", ScopeID: "system", Cardinality: "MANY", State: "ACTIVE",
	})
}

func upsertHostGroupCardinalityPolicyTx(ctx context.Context, tx pgx.Tx, policy HostGroupCardinalityPolicy) error {
	digest := hostGroupCardinalityPolicyDigest(policy)
	var current HostGroupCardinalityPolicy
	var currentDigest string
	err := tx.QueryRow(ctx, `
		SELECT cardinality_policy_id,policy_generation,cardinality,policy_state,revision_digest
		FROM kim.host_group_cardinality_policies_current
		WHERE group_type=$1 AND dimension=$2 AND level=$3 AND scope_type=$4 AND scope_id=$5
	`, policy.GroupType, policy.Dimension, policy.Level, policy.ScopeType, policy.ScopeID).
		Scan(&current.PolicyID, &current.Generation, &current.Cardinality, &current.State, &currentDigest)
	if err == nil && current.PolicyID != policy.PolicyID {
		return ErrHostGroupConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if policy.State == "ACTIVE" && policy.Cardinality != "MANY" {
		if err := validateExistingHostGroupCardinalityTx(ctx, tx, policy); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO kim.host_group_cardinality_policy_evidence (
			cardinality_policy_id,policy_generation,group_type,dimension,level,
			scope_type,scope_id,cardinality,policy_state,revision_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING
	`, policy.PolicyID, policy.Generation, policy.GroupType, policy.Dimension, policy.Level,
		policy.ScopeType, policy.ScopeID, policy.Cardinality, policy.State, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var recorded string
		if err := tx.QueryRow(ctx, `SELECT revision_digest FROM kim.host_group_cardinality_policy_evidence WHERE cardinality_policy_id=$1 AND policy_generation=$2`, policy.PolicyID, policy.Generation).Scan(&recorded); err != nil || recorded != digest {
			return ErrHostGroupConflict
		}
	}
	tag, err = tx.Exec(ctx, `
		INSERT INTO kim.host_group_cardinality_policies_current (
			group_type,dimension,level,scope_type,scope_id,cardinality_policy_id,
			policy_generation,cardinality,policy_state,revision_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (group_type,dimension,level,scope_type,scope_id) DO UPDATE SET
			cardinality_policy_id=EXCLUDED.cardinality_policy_id,
			policy_generation=EXCLUDED.policy_generation,
			cardinality=EXCLUDED.cardinality,policy_state=EXCLUDED.policy_state,
			revision_digest=EXCLUDED.revision_digest,updated_at=statement_timestamp()
		WHERE kim.host_group_cardinality_policies_current.policy_generation < EXCLUDED.policy_generation
	`, policy.GroupType, policy.Dimension, policy.Level, policy.ScopeType, policy.ScopeID,
		policy.PolicyID, policy.Generation, policy.Cardinality, policy.State, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 || (current.Generation == policy.Generation && currentDigest == digest) {
		return nil
	}
	return ErrHostGroupConflict
}

func loadCurrentHostGroupCardinalityPolicyTx(ctx context.Context, tx pgx.Tx, groupType, dimension, level string) (HostGroupCardinalityPolicy, error) {
	policy := HostGroupCardinalityPolicy{GroupType: groupType, Dimension: dimension, Level: level}
	err := tx.QueryRow(ctx, `
		SELECT cardinality_policy_id,policy_generation,scope_type,scope_id,cardinality,policy_state
		FROM kim.host_group_cardinality_policies_current
		WHERE group_type=$1 AND dimension=$2 AND level=$3 AND scope_type='SYSTEM' AND scope_id='system'
		FOR SHARE
	`, groupType, dimension, level).Scan(&policy.PolicyID, &policy.Generation, &policy.ScopeType,
		&policy.ScopeID, &policy.Cardinality, &policy.State)
	return policy, err
}

func validateExistingHostGroupCardinalityTx(ctx context.Context, tx pgx.Tx, policy HostGroupCardinalityPolicy) error {
	var violation bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM kim.host_group_memberships_current member
			JOIN kim.host_groups_current group_current USING (host_group_id)
			WHERE group_current.group_type=$1 AND group_current.dimension=$2 AND group_current.level=$3
			  AND group_current.lifecycle_state='ACTIVE' AND member.membership_state='ACTIVE'
			GROUP BY member.host_id HAVING count(*) > 1
		)
	`, policy.GroupType, policy.Dimension, policy.Level).Scan(&violation)
	if err != nil {
		return err
	}
	if violation {
		return ErrHostGroupConflict
	}
	return nil
}

func validateHostGroupCardinalityTx(ctx context.Context, tx pgx.Tx, hostGroupID, groupType, dimension, level, cardinality string, proposed []HostGroupMembership) error {
	if cardinality == "MANY" {
		return nil
	}
	counts := make(map[string]int)
	rows, err := tx.Query(ctx, `
		SELECT member.host_id,count(*)
		FROM kim.host_group_memberships_current member
		JOIN kim.host_groups_current group_current USING (host_group_id)
		WHERE member.host_group_id<>$1 AND group_current.group_type=$2
		  AND group_current.dimension=$3 AND group_current.level=$4
		  AND group_current.lifecycle_state='ACTIVE' AND member.membership_state='ACTIVE'
		GROUP BY member.host_id
	`, hostGroupID, groupType, dimension, level)
	if err != nil {
		return err
	}
	for rows.Next() {
		var hostID string
		var count int
		if err := rows.Scan(&hostID, &count); err != nil {
			rows.Close()
			return err
		}
		counts[hostID] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, member := range proposed {
		if member.State == "ACTIVE" {
			counts[member.HostID]++
		} else if cardinality == "EXACTLY_ONE" {
			if _, assignedElsewhere := counts[member.HostID]; !assignedElsewhere {
				return ErrHostGroupConflict
			}
		}
	}
	for _, count := range counts {
		if count > 1 || (cardinality == "EXACTLY_ONE" && count != 1) {
			return ErrHostGroupConflict
		}
	}
	return nil
}

func lockHostGroupTx(ctx context.Context, tx pgx.Tx, hostGroupID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "host-group/"+hostGroupID)
	return err
}

func lockHostGroupCardinalityScopeTx(ctx context.Context, tx pgx.Tx, groupType, dimension, level string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"host-group-cardinality/SYSTEM/system/"+groupType+"/"+dimension+"/"+level)
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
		(request.SelectorGeneration != nil && *request.SelectorGeneration == 0) ||
		(request.SelectorEvaluationGeneration != nil && *request.SelectorEvaluationGeneration == 0) ||
		(request.HierarchyGeneration != nil && *request.HierarchyGeneration == 0) {
		return errors.New("complete HostGroup membership set request is required")
	}
	selectorComplete := request.SelectorID != "" && request.SelectorGeneration != nil &&
		request.SelectorEvaluationID != "" && request.SelectorEvaluationGeneration != nil
	selectorAbsent := request.SelectorID == "" && request.SelectorGeneration == nil &&
		request.SelectorEvaluationID == "" && request.SelectorEvaluationGeneration == nil
	if (request.SourceType == "SELECTOR" && !selectorComplete) ||
		(request.SourceType != "SELECTOR" && !selectorAbsent) {
		return errors.New("selector membership provenance must be complete and source-bound")
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

func validateHostGroupCardinalityPolicy(policy HostGroupCardinalityPolicy) error {
	if policy.PolicyID == "" || policy.Generation == 0 || !validHostGroupType(policy.GroupType) ||
		policy.Dimension == "" || policy.Level == "" || policy.ScopeType != "SYSTEM" || policy.ScopeID != "system" ||
		!validHostGroupCardinality(policy.Cardinality) || (policy.State != "ACTIVE" && policy.State != "RETIRED") {
		return errors.New("complete HostGroup cardinality policy is required")
	}
	return nil
}

func validHostGroupCardinality(value string) bool {
	return value == "EXACTLY_ONE" || value == "ZERO_OR_ONE" || value == "MANY"
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
	hierarchyGeneration := ""
	if request.HierarchyGeneration != nil {
		hierarchyGeneration = fmt.Sprint(*request.HierarchyGeneration)
	}
	// Keep the pre-042 digest byte-for-byte stable for non-selector publishers.
	if request.SourceType != "SELECTOR" {
		selectorEvaluationGeneration := ""
		if request.SelectorEvaluationGeneration != nil {
			selectorEvaluationGeneration = fmt.Sprint(*request.SelectorEvaluationGeneration)
		}
		return digestHostGroupFields(request.HostGroupID,
			fmt.Sprint(request.BasedOnHostGroupGeneration), request.SourceType,
			request.SourceRevision, selectorEvaluationGeneration, hierarchyGeneration, setDigest)
	}
	selectorGeneration, selectorEvaluationGeneration := "", ""
	if request.SelectorGeneration != nil {
		selectorGeneration = fmt.Sprint(*request.SelectorGeneration)
	}
	if request.SelectorEvaluationGeneration != nil {
		selectorEvaluationGeneration = fmt.Sprint(*request.SelectorEvaluationGeneration)
	}
	return digestHostGroupFields(request.HostGroupID,
		fmt.Sprint(request.BasedOnHostGroupGeneration), request.SourceType,
		request.SourceRevision, request.SelectorID, selectorGeneration,
		request.SelectorEvaluationID, selectorEvaluationGeneration,
		hierarchyGeneration, setDigest)
}

func hostGroupCardinalityPolicyDigest(policy HostGroupCardinalityPolicy) string {
	return digestHostGroupFields(policy.PolicyID, fmt.Sprint(policy.Generation), policy.GroupType,
		policy.Dimension, policy.Level, policy.ScopeType, policy.ScopeID, policy.Cardinality, policy.State)
}

func digestHostGroupFields(fields ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(digest[:])
}
