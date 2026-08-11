package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
)

const HostGroupSelectorSchemaV1 = "kim.host-group.selector/v1"

type HostGroupSelectorPredicate struct {
	Field    string `json:"field"`
	Key      string `json:"key,omitempty"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type HostGroupSelectorExpression struct {
	MatchAll      []HostGroupSelectorPredicate `json:"match_all"`
	UnknownPolicy string                       `json:"unknown_policy"`
}

type HostGroupSelectorRevision struct {
	SelectorID, HostGroupID, SchemaVersion, LifecycleState string
	EvaluatorArtifactDigest                                string
	Generation, BasedOnHostGroupGeneration                 uint64
	Expression                                             HostGroupSelectorExpression
}

type HostGroupSelectorEvaluationRequest struct {
	EvaluationID, SelectorID  string
	SelectorGeneration        uint64
	ExpectedCurrentGeneration uint64
	HostIDs                   []string
}

type HostGroupSelectorHostResult struct {
	HostID, State, ReasonCode, SnapshotDigest string
	ObservationGeneration                     uint64
}

type HostGroupSelectorEvaluation struct {
	EvaluationID, SelectorID, HostGroupID, ResultState     string
	EvaluatedPopulationDigest, CanonicalCandidateSetDigest string
	SelectorGeneration, EvaluationGeneration               uint64
	HostGroupGeneration, CardinalityPolicyGeneration       uint64
	CardinalityPolicyID, HierarchyID                       string
	HierarchyGeneration                                    uint64
	EvaluatedHostCount, CandidateHostCount                 int
	Hosts                                                  []HostGroupSelectorHostResult
}

type HostGroupSelectorMaterializationRequest struct {
	PublishRequestID, EvaluationID string
	ExpectedCurrentSetGeneration   uint64
}

var selectorCapabilityAllowList = map[string]struct{}{
	"kim.host.cpu-isolation.v1":     {},
	"kim.host.cpu-model.v1":         {},
	"kim.host.cpu-topology.v1":      {},
	"kim.host.hugepages.v1":         {},
	"kim.host.iommu-observation.v1": {},
	"kim.host.memory.v1":            {},
	"kim.host.numa.v1":              {},
	"kim.host.pci-numa-locality.v1": {},
	"kim.host.pci-observation.v1":   {},
	"kim.host.sriov-observation.v1": {},
}

func UpsertHostGroupSelector(ctx context.Context, db TxBeginner, revision HostGroupSelectorRevision) error {
	normalized, payload, selectorDigest, revisionDigest, err := normalizeHostGroupSelectorRevision(revision)
	if err != nil {
		return err
	}
	revision.Expression = normalized
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, revision.HostGroupID); err != nil {
			return err
		}
		if err := lockHostGroupSelectorTx(ctx, tx, revision.SelectorID); err != nil {
			return err
		}
		var groupGeneration uint64
		var lifecycle string
		if err := tx.QueryRow(ctx, `SELECT host_group_generation,lifecycle_state FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE`,
			revision.HostGroupID).Scan(&groupGeneration, &lifecycle); err != nil {
			return err
		}
		if groupGeneration != revision.BasedOnHostGroupGeneration || lifecycle != "ACTIVE" {
			return ErrHostGroupConflict
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_group_selector_revision_evidence (
				selector_id,selector_generation,host_group_id,based_on_host_group_generation,
				selector_schema_version,normalized_expression,selector_digest,
				evaluator_artifact_digest,lifecycle_state,revision_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING
		`, revision.SelectorID, revision.Generation, revision.HostGroupID,
			revision.BasedOnHostGroupGeneration, revision.SchemaVersion, payload, selectorDigest,
			revision.EvaluatorArtifactDigest, revision.LifecycleState, revisionDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var recorded string
			if err := tx.QueryRow(ctx, `SELECT revision_digest FROM kim.host_group_selector_revision_evidence WHERE selector_id=$1 AND selector_generation=$2`,
				revision.SelectorID, revision.Generation).Scan(&recorded); err != nil || recorded != revisionDigest {
				return ErrHostGroupConflict
			}
		}
		tag, err = tx.Exec(ctx, `
			INSERT INTO kim.host_group_selectors_current (
				selector_id,selector_generation,host_group_id,based_on_host_group_generation,
				selector_schema_version,normalized_expression,selector_digest,
				evaluator_artifact_digest,lifecycle_state,revision_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (selector_id) DO UPDATE SET
				selector_generation=EXCLUDED.selector_generation,host_group_id=EXCLUDED.host_group_id,
				based_on_host_group_generation=EXCLUDED.based_on_host_group_generation,
				selector_schema_version=EXCLUDED.selector_schema_version,
				normalized_expression=EXCLUDED.normalized_expression,selector_digest=EXCLUDED.selector_digest,
				evaluator_artifact_digest=EXCLUDED.evaluator_artifact_digest,
				lifecycle_state=EXCLUDED.lifecycle_state,revision_digest=EXCLUDED.revision_digest,
				updated_at=statement_timestamp()
			WHERE kim.host_group_selectors_current.selector_generation < EXCLUDED.selector_generation
		`, revision.SelectorID, revision.Generation, revision.HostGroupID,
			revision.BasedOnHostGroupGeneration, revision.SchemaVersion, payload, selectorDigest,
			revision.EvaluatorArtifactDigest, revision.LifecycleState, revisionDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		var currentGeneration uint64
		var currentDigest string
		if err := tx.QueryRow(ctx, `SELECT selector_generation,revision_digest FROM kim.host_group_selectors_current WHERE selector_id=$1`,
			revision.SelectorID).Scan(&currentGeneration, &currentDigest); err != nil {
			return err
		}
		if currentGeneration == revision.Generation && currentDigest == revisionDigest {
			return nil
		}
		return ErrHostGroupConflict
	})
}

func EvaluateHostGroupSelector(ctx context.Context, db TxBeginner, request HostGroupSelectorEvaluationRequest) (HostGroupSelectorEvaluation, error) {
	if request.EvaluationID == "" || request.SelectorID == "" || request.SelectorGeneration == 0 || len(request.HostIDs) == 0 {
		return HostGroupSelectorEvaluation{}, errors.New("complete HostGroup selector evaluation request is required")
	}
	hostIDs := append([]string(nil), request.HostIDs...)
	sort.Strings(hostIDs)
	for index, hostID := range hostIDs {
		if hostID == "" || (index > 0 && hostID == hostIDs[index-1]) {
			return HostGroupSelectorEvaluation{}, errors.New("unique selector evaluation Host IDs are required")
		}
	}
	requestDigest := digestHostGroupFields(request.SelectorID, fmt.Sprint(request.SelectorGeneration),
		fmt.Sprint(request.ExpectedCurrentGeneration), strings.Join(hostIDs, "\n"))
	var result HostGroupSelectorEvaluation
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if existing, found, err := loadSelectorEvaluationByIDTx(ctx, tx, request.EvaluationID, requestDigest); err != nil {
			return err
		} else if found {
			result = existing
			return nil
		}
		var hostGroupID string
		if err := tx.QueryRow(ctx, `SELECT host_group_id FROM kim.host_group_selectors_current WHERE selector_id=$1`,
			request.SelectorID).Scan(&hostGroupID); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, hostGroupID); err != nil {
			return err
		}
		var groupGeneration uint64
		var groupType, dimension, level, groupLifecycle string
		if err := tx.QueryRow(ctx, `
			SELECT host_group_generation,group_type,dimension,level,lifecycle_state
			FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE
		`, hostGroupID).Scan(&groupGeneration, &groupType, &dimension, &level, &groupLifecycle); err != nil {
			return err
		}
		if groupLifecycle != "ACTIVE" {
			return ErrHostGroupConflict
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
		hierarchy, err := resolveCurrentHostGroupHierarchyTx(ctx, tx, groupType, dimension, hostGroupID)
		if err != nil {
			return err
		}
		if err := lockHostGroupSelectorTx(ctx, tx, request.SelectorID); err != nil {
			return err
		}
		selector, expression, err := loadCurrentHostGroupSelectorTx(ctx, tx, request.SelectorID)
		if err != nil {
			return err
		}
		if selector.Generation != request.SelectorGeneration || selector.HostGroupID != hostGroupID ||
			selector.BasedOnHostGroupGeneration != groupGeneration || selector.LifecycleState != "ACTIVE" {
			return ErrHostGroupConflict
		}

		hostResults := make([]HostGroupSelectorHostResult, 0, len(hostIDs))
		populationFields := make([]string, 0, len(hostIDs)*5)
		candidateFields := make([]string, 0, len(hostIDs))
		overall := "NOT_MATCHED"
		for _, hostID := range hostIDs {
			hostResult, err := evaluateSelectorHostTx(ctx, tx, expression, hostID)
			if err != nil {
				return err
			}
			hostResults = append(hostResults, hostResult)
			populationFields = append(populationFields, hostResult.HostID,
				fmt.Sprint(hostResult.ObservationGeneration), hostResult.SnapshotDigest,
				hostResult.State, hostResult.ReasonCode)
			if hostResult.State == "MATCHED" {
				candidateFields = append(candidateFields, hostResult.HostID)
				overall = "MATCHED"
			}
		}
		for _, hostResult := range hostResults {
			if hostResult.State == "UNKNOWN" {
				overall = "UNKNOWN"
				break
			}
			if hostResult.State == "UNSUPPORTED" {
				overall = "UNSUPPORTED"
			}
		}
		populationDigest := digestHostGroupFields(populationFields...)
		candidateDigest := digestHostGroupFields(candidateFields...)
		var currentEvaluationGeneration, currentSelectorGeneration uint64
		var currentEvaluationID, currentPopulationDigest, currentCandidateDigest, currentState string
		err = tx.QueryRow(ctx, `
			SELECT selector_generation,evaluation_generation,evaluation_id,evaluated_population_digest,
			       canonical_candidate_set_digest,result_state
			FROM kim.host_group_selector_evaluations_current WHERE selector_id=$1 FOR UPDATE
		`, request.SelectorID).Scan(&currentSelectorGeneration, &currentEvaluationGeneration, &currentEvaluationID,
			&currentPopulationDigest, &currentCandidateDigest, &currentState)
		if errors.Is(err, pgx.ErrNoRows) {
			currentEvaluationGeneration = 0
		} else if err != nil {
			return err
		}
		if currentEvaluationGeneration != request.ExpectedCurrentGeneration {
			if currentEvaluationGeneration > 0 && currentSelectorGeneration == request.SelectorGeneration &&
				currentPopulationDigest == populationDigest &&
				currentCandidateDigest == candidateDigest && currentState == overall {
				existing, found, err := loadSelectorEvaluationByIDTx(ctx, tx, currentEvaluationID, "")
				if err != nil || !found {
					return err
				}
				result = existing
				return nil
			}
			return ErrHostGroupConflict
		}
		evaluationGeneration := currentEvaluationGeneration + 1
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.host_group_selector_evaluation_evidence (
				evaluation_id,selector_id,selector_generation,evaluation_generation,
				host_group_id,host_group_generation,cardinality_policy_id,cardinality_policy_generation,
				hierarchy_id,hierarchy_generation,evaluator_artifact_digest,selector_schema_version,
				evaluated_population_digest,canonical_candidate_set_digest,evaluated_host_count,
				candidate_host_count,result_state,request_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		`, request.EvaluationID, request.SelectorID, selector.Generation, evaluationGeneration,
			hostGroupID, groupGeneration, policy.PolicyID, policy.Generation,
			nullableHierarchyID(hierarchy), nullableHierarchyGeneration(hierarchy),
			selector.EvaluatorArtifactDigest, selector.SchemaVersion, populationDigest, candidateDigest,
			len(hostResults), len(candidateFields), overall, requestDigest)
		if err != nil {
			return err
		}
		for _, hostResult := range hostResults {
			var observationGeneration, snapshotDigest any
			if hostResult.ObservationGeneration > 0 {
				observationGeneration, snapshotDigest = hostResult.ObservationGeneration, hostResult.SnapshotDigest
			}
			resultDigest := digestHostGroupFields(hostResult.HostID, fmt.Sprint(hostResult.ObservationGeneration),
				hostResult.SnapshotDigest, hostResult.State, hostResult.ReasonCode)
			if _, err := tx.Exec(ctx, `
				INSERT INTO kim.host_group_selector_evaluation_host_evidence (
					evaluation_id,host_id,observation_generation,snapshot_digest,
					result_state,reason_code,result_digest
				) VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, request.EvaluationID, hostResult.HostID, observationGeneration, snapshotDigest,
				hostResult.State, hostResult.ReasonCode, resultDigest); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.host_group_selector_evaluations_current (
				selector_id,selector_generation,evaluation_generation,evaluation_id,
				evaluated_population_digest,canonical_candidate_set_digest,result_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (selector_id) DO UPDATE SET
				selector_generation=EXCLUDED.selector_generation,
				evaluation_generation=EXCLUDED.evaluation_generation,evaluation_id=EXCLUDED.evaluation_id,
				evaluated_population_digest=EXCLUDED.evaluated_population_digest,
				canonical_candidate_set_digest=EXCLUDED.canonical_candidate_set_digest,
				result_state=EXCLUDED.result_state,updated_at=statement_timestamp()
		`, request.SelectorID, selector.Generation, evaluationGeneration, request.EvaluationID,
			populationDigest, candidateDigest, overall)
		if err != nil {
			return err
		}
		result = HostGroupSelectorEvaluation{
			EvaluationID: request.EvaluationID, SelectorID: request.SelectorID,
			SelectorGeneration: selector.Generation, EvaluationGeneration: evaluationGeneration,
			HostGroupID: hostGroupID, HostGroupGeneration: groupGeneration,
			CardinalityPolicyID: policy.PolicyID, CardinalityPolicyGeneration: policy.Generation,
			HierarchyID: hierarchy.HierarchyID, HierarchyGeneration: hierarchy.Generation,
			EvaluatedPopulationDigest: populationDigest, CanonicalCandidateSetDigest: candidateDigest,
			EvaluatedHostCount: len(hostResults), CandidateHostCount: len(candidateFields),
			ResultState: overall, Hosts: hostResults,
		}
		return nil
	})
	if err != nil {
		return HostGroupSelectorEvaluation{}, fmt.Errorf("evaluate HostGroup selector: %w", err)
	}
	return result, nil
}

func MaterializeHostGroupSelectorEvaluation(ctx context.Context, db TxBeginner, request HostGroupSelectorMaterializationRequest) (HostGroupMembershipSet, error) {
	if request.PublishRequestID == "" || request.EvaluationID == "" {
		return HostGroupMembershipSet{}, errors.New("complete selector materialization request is required")
	}
	var published HostGroupMembershipSet
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		evaluation, found, err := loadSelectorEvaluationByIDTx(ctx, tx, request.EvaluationID, "")
		if err != nil || !found {
			return err
		}
		if evaluation.ResultState == "UNKNOWN" || evaluation.ResultState == "UNSUPPORTED" {
			return ErrHostGroupConflict
		}
		if err := lockHostGroupTx(ctx, tx, evaluation.HostGroupID); err != nil {
			return err
		}
		if existing, found, err := loadMembershipSetByPublishRequestTx(ctx, tx, request.PublishRequestID); err != nil {
			return err
		} else if found {
			if existing.SelectorEvaluationID != request.EvaluationID {
				return ErrHostGroupConflict
			}
			published = existing
			return nil
		}
		selector, _, err := loadCurrentHostGroupSelectorTx(ctx, tx, evaluation.SelectorID)
		if err != nil || selector.Generation != evaluation.SelectorGeneration ||
			selector.HostGroupID != evaluation.HostGroupID || selector.LifecycleState != "ACTIVE" {
			return ErrHostGroupConflict
		}
		hostRows, err := loadAndRevalidateSelectorEvaluationHostsTx(ctx, tx, evaluation)
		if err != nil {
			return err
		}
		if existing, found, err := loadCurrentMembershipSetForSelectorEvaluationTx(ctx, tx, evaluation); err != nil {
			return err
		} else if found {
			published = existing
			return nil
		}
		currentMembers, err := loadCurrentHostGroupMemberships(ctx, tx, evaluation.HostGroupID)
		if err != nil {
			return err
		}
		currentByHost := make(map[string]HostGroupMembership, len(currentMembers))
		for _, member := range currentMembers {
			currentByHost[member.HostID] = member
		}
		sourceRevision := fmt.Sprintf("selector:%s/%d/evaluation/%d", evaluation.SelectorID,
			evaluation.SelectorGeneration, evaluation.EvaluationGeneration)
		members := make([]HostGroupMembership, 0, evaluation.CandidateHostCount)
		for _, hostResult := range hostRows {
			if hostResult.State != "MATCHED" {
				continue
			}
			generation := uint64(1)
			if current, exists := currentByHost[hostResult.HostID]; exists {
				generation = current.Generation
				if current.State != "ACTIVE" || current.SourceType != "SELECTOR" || current.SourceRevision != sourceRevision {
					generation++
				}
			}
			members = append(members, HostGroupMembership{
				HostGroupID: evaluation.HostGroupID, HostID: hostResult.HostID,
				Generation: generation, State: "ACTIVE", SourceType: "SELECTOR",
				SourceRevision: sourceRevision,
			})
		}
		selectorGeneration := evaluation.SelectorGeneration
		evaluationGeneration := evaluation.EvaluationGeneration
		setRequest := HostGroupMembershipSetRequest{
			PublishRequestID: request.PublishRequestID, HostGroupID: evaluation.HostGroupID,
			BasedOnHostGroupGeneration:   evaluation.HostGroupGeneration,
			ExpectedCurrentSetGeneration: request.ExpectedCurrentSetGeneration,
			SourceType:                   "SELECTOR", SourceRevision: sourceRevision,
			SelectorID: evaluation.SelectorID, SelectorGeneration: &selectorGeneration,
			SelectorEvaluationID:         evaluation.EvaluationID,
			SelectorEvaluationGeneration: &evaluationGeneration, Members: members,
		}
		if evaluation.HierarchyGeneration > 0 {
			hierarchyGeneration := evaluation.HierarchyGeneration
			setRequest.HierarchyGeneration = &hierarchyGeneration
		}
		published, err = publishHostGroupMembershipSetTx(ctx, tx, setRequest)
		return err
	})
	if err != nil {
		return HostGroupMembershipSet{}, fmt.Errorf("materialize HostGroup selector evaluation: %w", err)
	}
	return published, nil
}

func evaluateSelectorHostTx(ctx context.Context, tx pgx.Tx, expression HostGroupSelectorExpression, hostID string) (HostGroupSelectorHostResult, error) {
	result := HostGroupSelectorHostResult{HostID: hostID, State: "UNKNOWN", ReasonCode: "inventory_missing"}
	var projectionState string
	var payload []byte
	err := tx.QueryRow(ctx, `
		SELECT projection.observation_generation,projection.snapshot_digest,
		       projection.projection_state,snapshot.snapshot_payload
		FROM kim.host_capability_projections projection
		JOIN kim.host_inventory_snapshots snapshot
		  ON snapshot.host_id=projection.host_id
		 AND snapshot.observation_generation=projection.observation_generation
		WHERE projection.host_id=$1 FOR SHARE OF projection,snapshot
	`, hostID).Scan(&result.ObservationGeneration, &result.SnapshotDigest, &projectionState, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if scanErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.host_identities WHERE host_id=$1)`, hostID).Scan(&exists); scanErr != nil {
			return result, scanErr
		}
		if !exists {
			return result, ErrHostGroupConflict
		}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if projectionState != "CURRENT" {
		result.ReasonCode = "projection_not_current"
		return result, nil
	}
	snapshot, err := agentinventory.DecodeSnapshot(payload)
	if err != nil {
		return result, err
	}
	state, reason := evaluateHostGroupSelectorExpression(expression, snapshot)
	result.State, result.ReasonCode = state, reason
	return result, nil
}

func evaluateHostGroupSelectorExpression(expression HostGroupSelectorExpression, snapshot agentinventory.Snapshot) (string, string) {
	capabilities := make(map[string]agentinventory.Availability, len(snapshot.Capabilities))
	for _, capability := range snapshot.Capabilities {
		capabilities[capability.Name] = capability.State
	}
	architecture := ""
	for _, fragment := range snapshot.Fragments {
		if fragment.Compute != nil {
			architecture = fragment.Compute.Architecture
			break
		}
	}
	deferredState, deferredReason := "", ""
	for _, predicate := range expression.MatchAll {
		switch predicate.Field {
		case "HOST_ID":
			if snapshot.HostIdentity != predicate.Value {
				return "NOT_MATCHED", "host_identity_mismatch"
			}
		case "COMPUTE_ARCHITECTURE":
			state, exists := capabilities["kim.host.cpu-topology.v1"]
			if !exists || state == agentinventory.AvailabilityUnknown {
				deferredState, deferredReason = "UNKNOWN", "architecture_unknown"
				continue
			}
			if state == agentinventory.AvailabilityUnavailable {
				deferredState, deferredReason = "UNKNOWN", "architecture_unavailable"
				continue
			}
			if state == agentinventory.AvailabilityUnsupported {
				deferredState, deferredReason = "UNSUPPORTED", "architecture_unsupported"
				continue
			}
			if state != agentinventory.AvailabilityAvailable || architecture != predicate.Value {
				return "NOT_MATCHED", "architecture_mismatch"
			}
		case "CAPABILITY_STATE":
			state, exists := capabilities[predicate.Key]
			if !exists || state == agentinventory.AvailabilityUnknown {
				deferredState, deferredReason = "UNKNOWN", "capability_unknown"
				continue
			}
			if state == agentinventory.AvailabilityUnsupported && predicate.Value != string(agentinventory.AvailabilityUnsupported) {
				deferredState, deferredReason = "UNSUPPORTED", "capability_unsupported"
				continue
			}
			if string(state) != predicate.Value {
				return "NOT_MATCHED", "capability_state_mismatch"
			}
		}
	}
	if deferredState != "" {
		return deferredState, deferredReason
	}
	return "MATCHED", "all_predicates_matched"
}

func normalizeHostGroupSelectorRevision(revision HostGroupSelectorRevision) (HostGroupSelectorExpression, []byte, string, string, error) {
	if revision.SelectorID == "" || revision.HostGroupID == "" || revision.Generation == 0 ||
		revision.BasedOnHostGroupGeneration == 0 || revision.SchemaVersion != HostGroupSelectorSchemaV1 ||
		!isSHA256(revision.EvaluatorArtifactDigest) ||
		(revision.LifecycleState != "ACTIVE" && revision.LifecycleState != "DRAINING" && revision.LifecycleState != "RETIRED") {
		return HostGroupSelectorExpression{}, nil, "", "", errors.New("complete HostGroup selector revision is required")
	}
	expression := revision.Expression
	if expression.UnknownPolicy == "" {
		expression.UnknownPolicy = "FAIL_CLOSED"
	}
	if expression.UnknownPolicy != "FAIL_CLOSED" || len(expression.MatchAll) == 0 {
		return HostGroupSelectorExpression{}, nil, "", "", errors.New("closed fail-closed selector expression is required")
	}
	for index := range expression.MatchAll {
		predicate := &expression.MatchAll[index]
		if predicate.Operator != "EQUALS" {
			return HostGroupSelectorExpression{}, nil, "", "", errors.New("unsupported selector operator")
		}
		switch predicate.Field {
		case "HOST_ID":
			if predicate.Key != "" || predicate.Value == "" {
				return HostGroupSelectorExpression{}, nil, "", "", errors.New("invalid Host identity selector")
			}
		case "COMPUTE_ARCHITECTURE":
			if predicate.Key != "" || (predicate.Value != "x86_64" && predicate.Value != "aarch64") {
				return HostGroupSelectorExpression{}, nil, "", "", errors.New("invalid architecture selector")
			}
		case "CAPABILITY_STATE":
			if _, allowed := selectorCapabilityAllowList[predicate.Key]; !allowed ||
				(predicate.Value != "AVAILABLE" && predicate.Value != "UNAVAILABLE" && predicate.Value != "UNSUPPORTED") {
				return HostGroupSelectorExpression{}, nil, "", "", errors.New("invalid capability selector")
			}
		default:
			return HostGroupSelectorExpression{}, nil, "", "", errors.New("unsupported selector field")
		}
	}
	sort.Slice(expression.MatchAll, func(i, j int) bool {
		left, right := expression.MatchAll[i], expression.MatchAll[j]
		return left.Field+"\n"+left.Key+"\n"+left.Operator+"\n"+left.Value <
			right.Field+"\n"+right.Key+"\n"+right.Operator+"\n"+right.Value
	})
	for index := 1; index < len(expression.MatchAll); index++ {
		if expression.MatchAll[index] == expression.MatchAll[index-1] {
			return HostGroupSelectorExpression{}, nil, "", "", errors.New("duplicate selector predicate")
		}
	}
	payload, err := json.Marshal(expression)
	if err != nil {
		return HostGroupSelectorExpression{}, nil, "", "", err
	}
	selectorDigest := digestHostGroupFields(string(payload))
	revisionDigest := digestHostGroupFields(revision.SelectorID, fmt.Sprint(revision.Generation),
		revision.HostGroupID, fmt.Sprint(revision.BasedOnHostGroupGeneration), revision.SchemaVersion,
		selectorDigest, revision.EvaluatorArtifactDigest, revision.LifecycleState)
	return expression, payload, selectorDigest, revisionDigest, nil
}

func loadCurrentHostGroupSelectorTx(ctx context.Context, tx pgx.Tx, selectorID string) (HostGroupSelectorRevision, HostGroupSelectorExpression, error) {
	var selector HostGroupSelectorRevision
	var payload []byte
	err := tx.QueryRow(ctx, `
		SELECT selector_id,selector_generation,host_group_id,based_on_host_group_generation,
		       selector_schema_version,normalized_expression,evaluator_artifact_digest,lifecycle_state
		FROM kim.host_group_selectors_current WHERE selector_id=$1 FOR SHARE
	`, selectorID).Scan(&selector.SelectorID, &selector.Generation, &selector.HostGroupID,
		&selector.BasedOnHostGroupGeneration, &selector.SchemaVersion, &payload,
		&selector.EvaluatorArtifactDigest, &selector.LifecycleState)
	if err != nil {
		return HostGroupSelectorRevision{}, HostGroupSelectorExpression{}, err
	}
	if err := json.Unmarshal(payload, &selector.Expression); err != nil {
		return HostGroupSelectorRevision{}, HostGroupSelectorExpression{}, err
	}
	return selector, selector.Expression, nil
}

func loadSelectorEvaluationByIDTx(ctx context.Context, tx pgx.Tx, evaluationID, expectedRequestDigest string) (HostGroupSelectorEvaluation, bool, error) {
	var result HostGroupSelectorEvaluation
	var requestDigest string
	err := tx.QueryRow(ctx, `
		SELECT evaluation_id,selector_id,selector_generation,evaluation_generation,
		       host_group_id,host_group_generation,cardinality_policy_id,cardinality_policy_generation,
		       COALESCE(hierarchy_id,''),COALESCE(hierarchy_generation,0),
		       evaluated_population_digest,canonical_candidate_set_digest,
		       evaluated_host_count,candidate_host_count,result_state,request_digest
		FROM kim.host_group_selector_evaluation_evidence WHERE evaluation_id=$1
	`, evaluationID).Scan(&result.EvaluationID, &result.SelectorID, &result.SelectorGeneration,
		&result.EvaluationGeneration, &result.HostGroupID, &result.HostGroupGeneration,
		&result.CardinalityPolicyID, &result.CardinalityPolicyGeneration,
		&result.HierarchyID, &result.HierarchyGeneration, &result.EvaluatedPopulationDigest,
		&result.CanonicalCandidateSetDigest, &result.EvaluatedHostCount,
		&result.CandidateHostCount, &result.ResultState, &requestDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return HostGroupSelectorEvaluation{}, false, nil
	}
	if err != nil {
		return HostGroupSelectorEvaluation{}, false, err
	}
	if expectedRequestDigest != "" && requestDigest != expectedRequestDigest {
		return HostGroupSelectorEvaluation{}, false, ErrHostGroupConflict
	}
	rows, err := tx.Query(ctx, `
		SELECT host_id,COALESCE(observation_generation,0),COALESCE(snapshot_digest,''),
		       result_state,reason_code
		FROM kim.host_group_selector_evaluation_host_evidence
		WHERE evaluation_id=$1 ORDER BY host_id
	`, evaluationID)
	if err != nil {
		return HostGroupSelectorEvaluation{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var host HostGroupSelectorHostResult
		if err := rows.Scan(&host.HostID, &host.ObservationGeneration, &host.SnapshotDigest,
			&host.State, &host.ReasonCode); err != nil {
			return HostGroupSelectorEvaluation{}, false, err
		}
		result.Hosts = append(result.Hosts, host)
	}
	return result, true, rows.Err()
}

func loadAndRevalidateSelectorEvaluationHostsTx(ctx context.Context, tx pgx.Tx, evaluation HostGroupSelectorEvaluation) ([]HostGroupSelectorHostResult, error) {
	for _, host := range evaluation.Hosts {
		if host.ObservationGeneration == 0 {
			return nil, ErrHostGroupConflict
		}
		var generation uint64
		var digest, state string
		if err := tx.QueryRow(ctx, `
			SELECT observation_generation,snapshot_digest,projection_state
			FROM kim.host_capability_projections WHERE host_id=$1 FOR SHARE
		`, host.HostID).Scan(&generation, &digest, &state); err != nil ||
			generation != host.ObservationGeneration || digest != host.SnapshotDigest || state != "CURRENT" {
			return nil, ErrHostGroupConflict
		}
	}
	return evaluation.Hosts, nil
}

func validateSelectorMembershipSetRequestTx(
	ctx context.Context,
	tx pgx.Tx,
	request HostGroupMembershipSetRequest,
	hostGroupGeneration uint64,
	policy HostGroupCardinalityPolicy,
	hierarchy currentHostGroupHierarchy,
) error {
	if request.SourceType != "SELECTOR" {
		if request.SelectorID != "" || request.SelectorGeneration != nil ||
			request.SelectorEvaluationID != "" || request.SelectorEvaluationGeneration != nil {
			return ErrHostGroupConflict
		}
		return nil
	}
	if request.SelectorID == "" || request.SelectorGeneration == nil ||
		request.SelectorEvaluationID == "" || request.SelectorEvaluationGeneration == nil {
		return ErrHostGroupConflict
	}
	evaluation, found, err := loadSelectorEvaluationByIDTx(ctx, tx, request.SelectorEvaluationID, "")
	if err != nil || !found {
		return ErrHostGroupConflict
	}
	if evaluation.SelectorID != request.SelectorID ||
		evaluation.SelectorGeneration != *request.SelectorGeneration ||
		evaluation.EvaluationGeneration != *request.SelectorEvaluationGeneration ||
		evaluation.HostGroupID != request.HostGroupID ||
		evaluation.HostGroupGeneration != hostGroupGeneration ||
		evaluation.CardinalityPolicyID != policy.PolicyID ||
		evaluation.CardinalityPolicyGeneration != policy.Generation ||
		evaluation.HierarchyID != hierarchy.HierarchyID ||
		evaluation.HierarchyGeneration != hierarchy.Generation ||
		evaluation.ResultState == "UNKNOWN" || evaluation.ResultState == "UNSUPPORTED" {
		return ErrHostGroupConflict
	}
	selector, _, err := loadCurrentHostGroupSelectorTx(ctx, tx, request.SelectorID)
	if err != nil || selector.Generation != evaluation.SelectorGeneration ||
		selector.HostGroupID != request.HostGroupID ||
		selector.BasedOnHostGroupGeneration != hostGroupGeneration || selector.LifecycleState != "ACTIVE" {
		return ErrHostGroupConflict
	}
	hosts, err := loadAndRevalidateSelectorEvaluationHostsTx(ctx, tx, evaluation)
	if err != nil {
		return err
	}
	expected := make(map[string]struct{}, evaluation.CandidateHostCount)
	for _, host := range hosts {
		if host.State == "MATCHED" {
			expected[host.HostID] = struct{}{}
		}
	}
	if len(expected) != len(request.Members) {
		return ErrHostGroupConflict
	}
	expectedSourceRevision := fmt.Sprintf("selector:%s/%d/evaluation/%d", evaluation.SelectorID,
		evaluation.SelectorGeneration, evaluation.EvaluationGeneration)
	if request.SourceRevision != expectedSourceRevision {
		return ErrHostGroupConflict
	}
	for _, member := range request.Members {
		if _, ok := expected[member.HostID]; !ok || member.State != "ACTIVE" ||
			member.SourceType != "SELECTOR" || member.SourceRevision != expectedSourceRevision {
			return ErrHostGroupConflict
		}
	}
	return nil
}

func loadMembershipSetByPublishRequestTx(ctx context.Context, tx pgx.Tx, publishRequestID string) (HostGroupMembershipSet, bool, error) {
	var set HostGroupMembershipSet
	err := tx.QueryRow(ctx, `
		SELECT publish_request_id,host_group_id,membership_set_generation,
		       based_on_host_group_generation,canonical_member_set_digest,member_count,
		       COALESCE(cardinality_policy_id,''),COALESCE(cardinality_policy_generation,0),COALESCE(cardinality,''),
		       COALESCE(hierarchy_id,''),COALESCE(hierarchy_generation,0),
		       COALESCE(selector_id,''),COALESCE(selector_generation,0),
		       COALESCE(selector_evaluation_id,''),COALESCE(selector_evaluation_generation,0)
		FROM kim.host_group_membership_set_evidence WHERE publish_request_id=$1
	`, publishRequestID).Scan(&set.PublishRequestID, &set.HostGroupID, &set.MembershipSetGeneration,
		&set.BasedOnHostGroupGeneration, &set.CanonicalMemberSetDigest, &set.MemberCount,
		&set.CardinalityPolicyID, &set.CardinalityPolicyGeneration, &set.Cardinality,
		&set.HierarchyID, &set.HierarchyGeneration, &set.SelectorID, &set.SelectorGeneration,
		&set.SelectorEvaluationID, &set.SelectorEvaluationGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return HostGroupMembershipSet{}, false, nil
	}
	return set, err == nil, err
}

func loadCurrentMembershipSetForSelectorEvaluationTx(ctx context.Context, tx pgx.Tx, evaluation HostGroupSelectorEvaluation) (HostGroupMembershipSet, bool, error) {
	var set HostGroupMembershipSet
	err := tx.QueryRow(ctx, `
		SELECT evidence.publish_request_id,evidence.host_group_id,evidence.membership_set_generation,
		       evidence.based_on_host_group_generation,evidence.canonical_member_set_digest,evidence.member_count,
		       COALESCE(evidence.cardinality_policy_id,''),COALESCE(evidence.cardinality_policy_generation,0),COALESCE(evidence.cardinality,''),
		       COALESCE(evidence.hierarchy_id,''),COALESCE(evidence.hierarchy_generation,0),
		       COALESCE(evidence.selector_id,''),COALESCE(evidence.selector_generation,0),
		       COALESCE(evidence.selector_evaluation_id,''),COALESCE(evidence.selector_evaluation_generation,0)
		FROM kim.host_group_membership_sets_current current_set
		JOIN kim.host_group_membership_set_evidence evidence
		  ON evidence.host_group_id=current_set.host_group_id
		 AND evidence.membership_set_generation=current_set.membership_set_generation
		WHERE evidence.host_group_id=$1 AND evidence.selector_id=$2
		  AND evidence.selector_generation=$3 AND evidence.selector_evaluation_id=$4
		  AND evidence.selector_evaluation_generation=$5
	`, evaluation.HostGroupID, evaluation.SelectorID, evaluation.SelectorGeneration,
		evaluation.EvaluationID, evaluation.EvaluationGeneration).Scan(&set.PublishRequestID,
		&set.HostGroupID, &set.MembershipSetGeneration, &set.BasedOnHostGroupGeneration,
		&set.CanonicalMemberSetDigest, &set.MemberCount, &set.CardinalityPolicyID,
		&set.CardinalityPolicyGeneration, &set.Cardinality, &set.HierarchyID,
		&set.HierarchyGeneration, &set.SelectorID, &set.SelectorGeneration,
		&set.SelectorEvaluationID, &set.SelectorEvaluationGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return HostGroupMembershipSet{}, false, nil
	}
	return set, err == nil, err
}

func lockHostGroupSelectorTx(ctx context.Context, tx pgx.Tx, selectorID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "host-group-selector/"+selectorID)
	return err
}
