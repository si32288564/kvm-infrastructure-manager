package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

type HostGroupHierarchyRelation struct {
	ParentGroupID string
	ChildGroupID  string
}

type HostGroupHierarchyRequest struct {
	PublishRequestID, HierarchyID, GroupType, Dimension string
	ScopeType, ScopeID, GraphMode                       string
	ExpectedCurrentGeneration                           uint64
	Levels                                              []string
	NodeGroupIDs                                        []string
	Relations                                           []HostGroupHierarchyRelation
}

type HostGroupHierarchy struct {
	PublishRequestID, HierarchyID, GroupType, Dimension string
	ScopeType, ScopeID, GraphMode                       string
	CanonicalLevelOrderDigest                           string
	CanonicalNodeSetDigest                              string
	CanonicalRelationSetDigest                          string
	HierarchyGeneration                                 uint64
	LevelCount, NodeCount, RelationCount                int
}

type hostGroupHierarchyNode struct {
	HostGroupID, Level, Digest string
	Generation                 uint64
}

type hostGroupHierarchyRelationEvidence struct {
	ParentGroupID, ChildGroupID, ParentLevel, ChildLevel, Digest string
}

func PublishHostGroupHierarchy(ctx context.Context, db TxBeginner, request HostGroupHierarchyRequest) (HostGroupHierarchy, error) {
	if err := validateHostGroupHierarchyRequest(request); err != nil {
		return HostGroupHierarchy{}, err
	}
	var published HostGroupHierarchy
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupHierarchyScopeTx(ctx, tx, request.GroupType, request.Dimension); err != nil {
			return err
		}
		var err error
		published, err = publishHostGroupHierarchyTx(ctx, tx, request)
		return err
	})
	if err != nil {
		return HostGroupHierarchy{}, fmt.Errorf("publish HostGroup hierarchy: %w", err)
	}
	return published, nil
}

func publishHostGroupHierarchyTx(ctx context.Context, tx pgx.Tx, request HostGroupHierarchyRequest) (HostGroupHierarchy, error) {
	levelDigest := digestHostGroupFields(request.Levels...)
	requestDigest := hostGroupHierarchyRequestDigest(request, levelDigest)
	var existing HostGroupHierarchy
	var recordedRequestDigest string
	err := tx.QueryRow(ctx, `
		SELECT publish_request_id,hierarchy_id,hierarchy_generation,group_type,dimension,
		       scope_type,scope_id,graph_mode,canonical_level_order_digest,
		       canonical_node_set_digest,canonical_relation_set_digest,
		       level_count,node_count,relation_count,request_digest
		FROM kim.host_group_hierarchy_set_evidence WHERE publish_request_id=$1
	`, request.PublishRequestID).Scan(&existing.PublishRequestID, &existing.HierarchyID,
		&existing.HierarchyGeneration, &existing.GroupType, &existing.Dimension,
		&existing.ScopeType, &existing.ScopeID, &existing.GraphMode,
		&existing.CanonicalLevelOrderDigest, &existing.CanonicalNodeSetDigest,
		&existing.CanonicalRelationSetDigest, &existing.LevelCount, &existing.NodeCount,
		&existing.RelationCount, &recordedRequestDigest)
	if err == nil {
		if recordedRequestDigest != requestDigest {
			return HostGroupHierarchy{}, ErrHostGroupConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return HostGroupHierarchy{}, err
	}

	nodes, err := loadAndValidateHostGroupHierarchyNodes(ctx, tx, request)
	if err != nil {
		return HostGroupHierarchy{}, err
	}
	nodeFields := make([]string, 0, len(nodes)*4)
	nodeByID := make(map[string]hostGroupHierarchyNode, len(nodes))
	for _, node := range nodes {
		nodeFields = append(nodeFields, node.HostGroupID, fmt.Sprint(node.Generation), node.Level, node.Digest)
		nodeByID[node.HostGroupID] = node
	}
	nodeDigest := digestHostGroupFields(nodeFields...)
	relations, err := validateAndNormalizeHostGroupHierarchyRelations(request, nodeByID)
	if err != nil {
		return HostGroupHierarchy{}, err
	}
	relationFields := make([]string, 0, len(relations)*5)
	for _, relation := range relations {
		relationFields = append(relationFields, relation.ParentGroupID, relation.ChildGroupID,
			relation.ParentLevel, relation.ChildLevel, relation.Digest)
	}
	relationDigest := digestHostGroupFields(relationFields...)
	var currentGeneration uint64
	err = tx.QueryRow(ctx, `
		SELECT hierarchy_generation
		FROM kim.host_group_hierarchy_sets_current
		WHERE group_type=$1 AND dimension=$2 AND scope_type=$3 AND scope_id=$4
		FOR UPDATE
	`, request.GroupType, request.Dimension, request.ScopeType, request.ScopeID).Scan(&currentGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		currentGeneration = 0
	} else if err != nil {
		return HostGroupHierarchy{}, err
	}
	if currentGeneration != request.ExpectedCurrentGeneration {
		return HostGroupHierarchy{}, ErrHostGroupConflict
	}
	err = tx.QueryRow(ctx, `
		SELECT publish_request_id,hierarchy_id,hierarchy_generation,group_type,dimension,
		       scope_type,scope_id,graph_mode,canonical_level_order_digest,
		       canonical_node_set_digest,canonical_relation_set_digest,
		       level_count,node_count,relation_count
		FROM kim.host_group_hierarchy_set_evidence
		WHERE hierarchy_id=$1 AND group_type=$2 AND dimension=$3 AND scope_type=$4 AND scope_id=$5
		  AND graph_mode=$6 AND canonical_level_order_digest=$7
		  AND canonical_node_set_digest=$8 AND canonical_relation_set_digest=$9
		ORDER BY hierarchy_generation DESC LIMIT 1
	`, request.HierarchyID, request.GroupType, request.Dimension, request.ScopeType, request.ScopeID,
		request.GraphMode, levelDigest, nodeDigest, relationDigest).Scan(&existing.PublishRequestID,
		&existing.HierarchyID, &existing.HierarchyGeneration, &existing.GroupType, &existing.Dimension,
		&existing.ScopeType, &existing.ScopeID, &existing.GraphMode, &existing.CanonicalLevelOrderDigest,
		&existing.CanonicalNodeSetDigest, &existing.CanonicalRelationSetDigest, &existing.LevelCount,
		&existing.NodeCount, &existing.RelationCount)
	if err == nil && existing.HierarchyGeneration == currentGeneration {
		return existing, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return HostGroupHierarchy{}, err
	}

	generation := currentGeneration + 1
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.host_group_hierarchy_set_evidence (
			hierarchy_id,hierarchy_generation,publish_request_id,request_digest,
			group_type,dimension,scope_type,scope_id,graph_mode,
			canonical_level_order_digest,canonical_node_set_digest,canonical_relation_set_digest,
			level_count,node_count,relation_count,validation_state
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'ACCEPTED')
	`, request.HierarchyID, generation, request.PublishRequestID, requestDigest,
		request.GroupType, request.Dimension, request.ScopeType, request.ScopeID, request.GraphMode,
		levelDigest, nodeDigest, relationDigest, len(request.Levels), len(nodes), len(relations))
	if err != nil {
		return HostGroupHierarchy{}, err
	}
	for rank, level := range request.Levels {
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_hierarchy_level_evidence (
				hierarchy_id,hierarchy_generation,level,level_rank
			) VALUES ($1,$2,$3,$4)
		`, request.HierarchyID, generation, level, rank); err != nil {
			return HostGroupHierarchy{}, err
		}
	}
	for _, node := range nodes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_hierarchy_node_evidence (
				hierarchy_id,hierarchy_generation,host_group_id,host_group_generation,level,node_digest
			) VALUES ($1,$2,$3,$4,$5,$6)
		`, request.HierarchyID, generation, node.HostGroupID, node.Generation, node.Level, node.Digest); err != nil {
			return HostGroupHierarchy{}, err
		}
	}
	for _, relation := range relations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_hierarchy_relation_evidence (
				hierarchy_id,hierarchy_generation,parent_group_id,child_group_id,
				parent_level,child_level,relation_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, request.HierarchyID, generation, relation.ParentGroupID, relation.ChildGroupID,
			relation.ParentLevel, relation.ChildLevel, relation.Digest); err != nil {
			return HostGroupHierarchy{}, err
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kim.host_group_hierarchy_sets_current (
			group_type,dimension,scope_type,scope_id,hierarchy_id,hierarchy_generation,
			graph_mode,canonical_relation_set_digest,validation_state
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'ACCEPTED')
		ON CONFLICT (group_type,dimension,scope_type,scope_id) DO UPDATE SET
			hierarchy_id=EXCLUDED.hierarchy_id,hierarchy_generation=EXCLUDED.hierarchy_generation,
			graph_mode=EXCLUDED.graph_mode,
			canonical_relation_set_digest=EXCLUDED.canonical_relation_set_digest,
			validation_state='ACCEPTED',updated_at=statement_timestamp()
	`, request.GroupType, request.Dimension, request.ScopeType, request.ScopeID,
		request.HierarchyID, generation, request.GraphMode, relationDigest)
	if err != nil {
		return HostGroupHierarchy{}, err
	}
	return HostGroupHierarchy{
		PublishRequestID: request.PublishRequestID, HierarchyID: request.HierarchyID,
		HierarchyGeneration: generation, GroupType: request.GroupType, Dimension: request.Dimension,
		ScopeType: request.ScopeType, ScopeID: request.ScopeID, GraphMode: request.GraphMode,
		CanonicalLevelOrderDigest: levelDigest, CanonicalNodeSetDigest: nodeDigest,
		CanonicalRelationSetDigest: relationDigest, LevelCount: len(request.Levels),
		NodeCount: len(nodes), RelationCount: len(relations),
	}, nil
}

func hostGroupHierarchyRequestDigest(request HostGroupHierarchyRequest, levelDigest string) string {
	nodes := append([]string(nil), request.NodeGroupIDs...)
	sort.Strings(nodes)
	relations := append([]HostGroupHierarchyRelation(nil), request.Relations...)
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].ParentGroupID == relations[j].ParentGroupID {
			return relations[i].ChildGroupID < relations[j].ChildGroupID
		}
		return relations[i].ParentGroupID < relations[j].ParentGroupID
	})
	fields := []string{request.HierarchyID, request.GroupType, request.Dimension,
		request.ScopeType, request.ScopeID, request.GraphMode, levelDigest}
	fields = append(fields, nodes...)
	for _, relation := range relations {
		fields = append(fields, relation.ParentGroupID, relation.ChildGroupID)
	}
	return digestHostGroupFields(fields...)
}

func loadAndValidateHostGroupHierarchyNodes(ctx context.Context, tx pgx.Tx, request HostGroupHierarchyRequest) ([]hostGroupHierarchyNode, error) {
	levelRanks := make(map[string]int, len(request.Levels))
	for rank, level := range request.Levels {
		levelRanks[level] = rank
	}
	groupIDs := append([]string(nil), request.NodeGroupIDs...)
	sort.Strings(groupIDs)
	nodes := make([]hostGroupHierarchyNode, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		var node hostGroupHierarchyNode
		var groupType, dimension, lifecycle string
		node.HostGroupID = groupID
		if err := tx.QueryRow(ctx, `
			SELECT host_group_generation,group_type,dimension,level,lifecycle_state
			FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE
		`, groupID).Scan(&node.Generation, &groupType, &dimension, &node.Level, &lifecycle); err != nil {
			return nil, err
		}
		if groupType != request.GroupType || dimension != request.Dimension || lifecycle != "ACTIVE" {
			return nil, ErrHostGroupConflict
		}
		if _, exists := levelRanks[node.Level]; !exists {
			return nil, ErrHostGroupConflict
		}
		node.Digest = digestHostGroupFields(node.HostGroupID, fmt.Sprint(node.Generation), node.Level)
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func validateAndNormalizeHostGroupHierarchyRelations(request HostGroupHierarchyRequest, nodes map[string]hostGroupHierarchyNode) ([]hostGroupHierarchyRelationEvidence, error) {
	levelRanks := make(map[string]int, len(request.Levels))
	for rank, level := range request.Levels {
		levelRanks[level] = rank
	}
	parentByChild := make(map[string]string)
	relations := make([]hostGroupHierarchyRelationEvidence, 0, len(request.Relations))
	for _, requested := range request.Relations {
		parent, parentExists := nodes[requested.ParentGroupID]
		child, childExists := nodes[requested.ChildGroupID]
		if !parentExists || !childExists || parent.HostGroupID == child.HostGroupID ||
			levelRanks[parent.Level] >= levelRanks[child.Level] {
			return nil, ErrHostGroupConflict
		}
		if previous, exists := parentByChild[child.HostGroupID]; exists && previous != parent.HostGroupID {
			return nil, ErrHostGroupConflict
		}
		parentByChild[child.HostGroupID] = parent.HostGroupID
		relation := hostGroupHierarchyRelationEvidence{
			ParentGroupID: parent.HostGroupID, ChildGroupID: child.HostGroupID,
			ParentLevel: parent.Level, ChildLevel: child.Level,
		}
		relation.Digest = digestHostGroupFields(relation.ParentGroupID, relation.ChildGroupID,
			relation.ParentLevel, relation.ChildLevel)
		relations = append(relations, relation)
	}
	for _, node := range nodes {
		if levelRanks[node.Level] > 0 {
			if _, exists := parentByChild[node.HostGroupID]; !exists {
				return nil, ErrHostGroupConflict
			}
		}
	}
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].ParentGroupID == relations[j].ParentGroupID {
			return relations[i].ChildGroupID < relations[j].ChildGroupID
		}
		return relations[i].ParentGroupID < relations[j].ParentGroupID
	})
	return relations, nil
}

func validateHostGroupHierarchyRequest(request HostGroupHierarchyRequest) error {
	if request.PublishRequestID == "" || request.HierarchyID == "" ||
		!validHostGroupType(request.GroupType) || request.Dimension == "" ||
		request.ScopeType != "SYSTEM" || request.ScopeID != "system" ||
		request.GraphMode != "TREE" || len(request.Levels) == 0 || len(request.NodeGroupIDs) == 0 {
		return errors.New("complete HostGroup hierarchy request is required")
	}
	levels := make(map[string]struct{}, len(request.Levels))
	for _, level := range request.Levels {
		if level == "" {
			return errors.New("empty HostGroup hierarchy level")
		}
		if _, duplicate := levels[level]; duplicate {
			return errors.New("duplicate HostGroup hierarchy level")
		}
		levels[level] = struct{}{}
	}
	nodes := make(map[string]struct{}, len(request.NodeGroupIDs))
	for _, groupID := range request.NodeGroupIDs {
		if groupID == "" {
			return errors.New("empty HostGroup hierarchy node")
		}
		if _, duplicate := nodes[groupID]; duplicate {
			return errors.New("duplicate HostGroup hierarchy node")
		}
		nodes[groupID] = struct{}{}
	}
	relations := make(map[string]struct{}, len(request.Relations))
	for _, relation := range request.Relations {
		key := relation.ParentGroupID + "\n" + relation.ChildGroupID
		if relation.ParentGroupID == "" || relation.ChildGroupID == "" {
			return errors.New("incomplete HostGroup hierarchy relation")
		}
		if _, duplicate := relations[key]; duplicate {
			return errors.New("duplicate HostGroup hierarchy relation")
		}
		relations[key] = struct{}{}
	}
	return nil
}

func lockHostGroupHierarchyScopeTx(ctx context.Context, tx pgx.Tx, groupType, dimension string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"host-group-hierarchy/SYSTEM/system/"+groupType+"/"+dimension)
	return err
}

type currentHostGroupHierarchy struct {
	HierarchyID string
	Generation  uint64
}

func resolveCurrentHostGroupHierarchyTx(ctx context.Context, tx pgx.Tx, groupType, dimension, hostGroupID string) (currentHostGroupHierarchy, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM kim.host_group_hierarchy_sets_current
			WHERE group_type=$1 AND dimension=$2 AND scope_type='SYSTEM' AND scope_id='system'
		)
	`, groupType, dimension).Scan(&exists); err != nil {
		return currentHostGroupHierarchy{}, err
	}
	if !exists {
		return currentHostGroupHierarchy{}, nil
	}
	hierarchy, err := loadCurrentHostGroupHierarchyTx(ctx, tx, groupType, dimension, hostGroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return currentHostGroupHierarchy{}, ErrHostGroupConflict
	}
	return hierarchy, err
}

func loadCurrentHostGroupHierarchyTx(ctx context.Context, tx pgx.Tx, groupType, dimension, hostGroupID string) (currentHostGroupHierarchy, error) {
	var hierarchy currentHostGroupHierarchy
	err := tx.QueryRow(ctx, `
		SELECT current_set.hierarchy_id,current_set.hierarchy_generation
		FROM kim.host_group_hierarchy_sets_current current_set
		JOIN kim.host_group_hierarchy_node_evidence node
		  ON node.hierarchy_id=current_set.hierarchy_id
		 AND node.hierarchy_generation=current_set.hierarchy_generation
		JOIN kim.host_groups_current group_current ON group_current.host_group_id=node.host_group_id
		WHERE current_set.group_type=$1 AND current_set.dimension=$2
		  AND current_set.scope_type='SYSTEM' AND current_set.scope_id='system'
		  AND node.host_group_id=$3
		  AND node.host_group_generation=group_current.host_group_generation
		  AND node.level=group_current.level AND group_current.lifecycle_state='ACTIVE'
		  AND NOT EXISTS (
			SELECT 1
			FROM kim.host_group_hierarchy_node_evidence graph_node
			JOIN kim.host_groups_current graph_group ON graph_group.host_group_id=graph_node.host_group_id
			WHERE graph_node.hierarchy_id=current_set.hierarchy_id
			  AND graph_node.hierarchy_generation=current_set.hierarchy_generation
			  AND (graph_node.host_group_generation<>graph_group.host_group_generation
			       OR graph_node.level<>graph_group.level OR graph_group.lifecycle_state<>'ACTIVE')
		  )
		FOR SHARE OF current_set
	`, groupType, dimension, hostGroupID).Scan(&hierarchy.HierarchyID, &hierarchy.Generation)
	return hierarchy, err
}
