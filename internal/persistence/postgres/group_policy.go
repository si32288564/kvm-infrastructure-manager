package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

var ErrGroupPolicyConflict = errors.New("Group Policy authority conflict")

type MaintenancePolicyRevision struct {
	PolicyID, OperationType, OperationSchemaVersion    string
	ProfileID, ProfileDigest, LifecycleState           string
	PolicyRevision, ProfileRevision                    uint64
	MaximumConcurrent, FailureDomainMaximumUnavailable int
}

type GroupPolicyBindingRequest struct {
	PublishRequestID, BindingID, HostGroupID         string
	PolicyType, ConsumerType, PolicyID, PolicyDigest string
	LifecycleState                                   string
	ExpectedCurrentGeneration, HostGroupGeneration   uint64
	PolicyRevision                                   uint64
	Priority                                         int
}

type GroupPolicyBinding struct {
	BindingID, BindingDigest, HostGroupID, PolicyID, PolicyDigest string
	BindingGeneration, HostGroupGeneration, PolicyRevision        uint64
	Priority                                                      int
}

type GroupPolicyResolutionRequest struct {
	ResolutionID, HostID, PolicyType, ConsumerType string
	PinnedHostGroupID                              string
	PinnedMembershipSetGeneration                  uint64
	ReadOnly                                       bool
}

type GroupPolicyResolution struct {
	ResolutionID, HostID, Result             string
	EffectivePolicyID, EffectivePolicyDigest string
	EffectivePolicyRevision                  uint64
	WinningPriority                          int
	InputDigest, ResolutionDigest            string
}

type groupPolicyCandidate struct {
	BindingID, HostGroupID, PolicyID, PolicyDigest, BindingDigest   string
	BindingGeneration, HostGroupGeneration, MembershipSetGeneration uint64
	PolicyRevision                                                  uint64
	Priority                                                        int
	State                                                           string
}

func PublishMaintenancePolicy(ctx context.Context, db TxBeginner, policy MaintenancePolicyRevision) (string, error) {
	if policy.PolicyID == "" || policy.PolicyRevision == 0 || policy.OperationType != "HOST_DRAIN" ||
		policy.OperationSchemaVersion != "v1" || policy.ProfileID == "" || policy.ProfileRevision == 0 ||
		!validDigest(policy.ProfileDigest) || policy.MaximumConcurrent <= 0 ||
		policy.FailureDomainMaximumUnavailable <= 0 || (policy.LifecycleState != "ACTIVE" && policy.LifecycleState != "RETIRED") {
		return "", ErrGroupPolicyConflict
	}
	raw, _ := json.Marshal(policy)
	digest := digestReleaseBytes(raw)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "maintenance-policy/"+policy.PolicyID); err != nil {
			return err
		}
		var recorded string
		err := tx.QueryRow(ctx, `SELECT policy_digest FROM kim.maintenance_policy_revision_evidence WHERE policy_id=$1 AND policy_revision=$2`, policy.PolicyID, policy.PolicyRevision).Scan(&recorded)
		if err == nil {
			if recorded != digest {
				return ErrGroupPolicyConflict
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_policy_revision_evidence(
				policy_id,policy_revision,operation_type,operation_schema_version,profile_id,profile_revision,
				profile_digest,maximum_concurrent,failure_domain_maximum_unavailable,lifecycle_state,policy_digest)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, policy.PolicyID, policy.PolicyRevision,
				policy.OperationType, policy.OperationSchemaVersion, policy.ProfileID, policy.ProfileRevision,
				policy.ProfileDigest, policy.MaximumConcurrent, policy.FailureDomainMaximumUnavailable,
				policy.LifecycleState, digest); err != nil {
				return err
			}
		} else {
			return err
		}
		command, err := tx.Exec(ctx, `INSERT INTO kim.maintenance_policies_current(policy_id,policy_revision,lifecycle_state,policy_digest)
			VALUES($1,$2,$3,$4) ON CONFLICT(policy_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,
			lifecycle_state=EXCLUDED.lifecycle_state,policy_digest=EXCLUDED.policy_digest,updated_at=statement_timestamp()
			WHERE kim.maintenance_policies_current.policy_revision<EXCLUDED.policy_revision`, policy.PolicyID,
			policy.PolicyRevision, policy.LifecycleState, digest)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			var revision uint64
			if err := tx.QueryRow(ctx, `SELECT policy_revision FROM kim.maintenance_policies_current WHERE policy_id=$1`, policy.PolicyID).Scan(&revision); err != nil {
				return err
			}
			if revision != policy.PolicyRevision {
				return ErrGroupPolicyConflict
			}
		}
		if err := publishGroupPolicyCatalogTx(ctx, tx, "MAINTENANCE", policy.PolicyID, policy.PolicyRevision, digest, policy.LifecycleState); err != nil {
			return err
		}
		return nil
	})
	return digest, err
}

func PublishGroupPolicyBinding(ctx context.Context, db TxBeginner, request GroupPolicyBindingRequest) (GroupPolicyBinding, error) {
	if request.PublishRequestID == "" || request.BindingID == "" || request.HostGroupID == "" || request.PolicyID == "" ||
		!((request.PolicyType == "MAINTENANCE" && request.ConsumerType == "MAINTENANCE_PLAN") || (request.PolicyType == "AVAILABILITY_POLICY" && request.ConsumerType == "VM_PLACEMENT")) ||
		request.PolicyRevision == 0 || !validDigest(request.PolicyDigest) || request.HostGroupGeneration == 0 ||
		(request.LifecycleState != "ACTIVE" && request.LifecycleState != "DRAINING" && request.LifecycleState != "RETIRED") {
		return GroupPolicyBinding{}, ErrGroupPolicyConflict
	}
	raw, _ := json.Marshal(request)
	requestDigest := digestReleaseBytes(raw)
	var result GroupPolicyBinding
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "group-policy-binding/"+request.BindingID); err != nil {
			return err
		}
		var priorRequestDigest, priorBindingDigest string
		var priorGeneration uint64
		err := tx.QueryRow(ctx, `SELECT request_digest,binding_digest,binding_generation FROM kim.host_group_policy_binding_revision_evidence WHERE publish_request_id=$1`, request.PublishRequestID).Scan(&priorRequestDigest, &priorBindingDigest, &priorGeneration)
		if err == nil {
			if priorRequestDigest != requestDigest {
				return ErrGroupPolicyConflict
			}
			result = GroupPolicyBinding{BindingID: request.BindingID, BindingGeneration: priorGeneration, BindingDigest: priorBindingDigest, HostGroupID: request.HostGroupID, HostGroupGeneration: request.HostGroupGeneration, PolicyID: request.PolicyID, PolicyRevision: request.PolicyRevision, PolicyDigest: request.PolicyDigest, Priority: request.Priority}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var groupGeneration uint64
		var groupLifecycle, groupType string
		if err := tx.QueryRow(ctx, `SELECT host_group_generation,lifecycle_state,group_type FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE`, request.HostGroupID).Scan(&groupGeneration, &groupLifecycle, &groupType); err != nil || groupGeneration != request.HostGroupGeneration || groupLifecycle != "ACTIVE" || (request.PolicyType == "AVAILABILITY_POLICY" && groupType != "PLACEMENT_POOL") {
			return ErrGroupPolicyConflict
		}
		var policyRevision uint64
		var policyDigest, policyLifecycle string
		if err := tx.QueryRow(ctx, `SELECT policy_revision,policy_digest,lifecycle_state FROM kim.group_policies_current WHERE policy_type=$1 AND policy_id=$2 FOR SHARE`, request.PolicyType, request.PolicyID).Scan(&policyRevision, &policyDigest, &policyLifecycle); err != nil || policyRevision != request.PolicyRevision || policyDigest != request.PolicyDigest || policyLifecycle != "ACTIVE" {
			return ErrGroupPolicyConflict
		}
		var current uint64
		err = tx.QueryRow(ctx, `SELECT binding_generation FROM kim.host_group_policy_bindings_current WHERE binding_id=$1 FOR UPDATE`, request.BindingID).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			current = 0
		} else if err != nil {
			return err
		}
		if current != request.ExpectedCurrentGeneration {
			return ErrGroupPolicyConflict
		}
		generation := current + 1
		bindingRaw, _ := json.Marshal(map[string]any{"binding_id": request.BindingID, "generation": generation, "host_group_id": request.HostGroupID, "host_group_generation": request.HostGroupGeneration, "policy_type": request.PolicyType, "consumer_type": request.ConsumerType, "policy_id": request.PolicyID, "policy_revision": request.PolicyRevision, "policy_digest": request.PolicyDigest, "priority": request.Priority, "lifecycle": request.LifecycleState})
		bindingDigest := digestReleaseBytes(bindingRaw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_policy_binding_revision_evidence(
			binding_id,binding_generation,publish_request_id,request_digest,host_group_id,host_group_generation,
			policy_type,consumer_type,policy_id,policy_revision,policy_digest,priority,lifecycle_state,binding_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, request.BindingID, generation,
			request.PublishRequestID, requestDigest, request.HostGroupID, request.HostGroupGeneration, request.PolicyType,
			request.ConsumerType, request.PolicyID, request.PolicyRevision, request.PolicyDigest, request.Priority,
			request.LifecycleState, bindingDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_policy_bindings_current(
			binding_id,binding_generation,host_group_id,host_group_generation,policy_type,consumer_type,
			policy_id,policy_revision,policy_digest,priority,lifecycle_state,binding_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(binding_id) DO UPDATE SET
			binding_generation=EXCLUDED.binding_generation,host_group_id=EXCLUDED.host_group_id,
			host_group_generation=EXCLUDED.host_group_generation,policy_id=EXCLUDED.policy_id,
			policy_revision=EXCLUDED.policy_revision,policy_digest=EXCLUDED.policy_digest,priority=EXCLUDED.priority,
			lifecycle_state=EXCLUDED.lifecycle_state,binding_digest=EXCLUDED.binding_digest,updated_at=statement_timestamp()`,
			request.BindingID, generation, request.HostGroupID, request.HostGroupGeneration, request.PolicyType,
			request.ConsumerType, request.PolicyID, request.PolicyRevision, request.PolicyDigest, request.Priority,
			request.LifecycleState, bindingDigest); err != nil {
			return err
		}
		result = GroupPolicyBinding{BindingID: request.BindingID, BindingGeneration: generation, BindingDigest: bindingDigest, HostGroupID: request.HostGroupID, HostGroupGeneration: request.HostGroupGeneration, PolicyID: request.PolicyID, PolicyRevision: request.PolicyRevision, PolicyDigest: request.PolicyDigest, Priority: request.Priority}
		return nil
	})
	return result, err
}

func ResolveGroupPolicy(ctx context.Context, db TxBeginner, request GroupPolicyResolutionRequest) (GroupPolicyResolution, error) {
	var resolution GroupPolicyResolution
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var err error
		resolution, err = resolveGroupPolicyTx(ctx, tx, request)
		return err
	})
	return resolution, err
}

func resolveGroupPolicyTx(ctx context.Context, tx pgx.Tx, request GroupPolicyResolutionRequest) (GroupPolicyResolution, error) {
	if request.ResolutionID == "" || request.HostID == "" {
		return GroupPolicyResolution{}, ErrGroupPolicyConflict
	}
	if !((request.PolicyType == "MAINTENANCE" && request.ConsumerType == "MAINTENANCE_PLAN") || (request.PolicyType == "AVAILABILITY_POLICY" && request.ConsumerType == "VM_PLACEMENT")) {
		return GroupPolicyResolution{ResolutionID: request.ResolutionID, HostID: request.HostID, Result: "UNSUPPORTED"}, nil
	}
	lockClause := " FOR SHARE OF set_evidence,group_current,binding"
	if request.ReadOnly {
		lockClause = ""
	}
	rows, err := tx.Query(ctx, `WITH applicable_memberships AS (
		 SELECT member.host_group_id,member.membership_set_generation
		 FROM kim.host_group_memberships_current member
		 JOIN kim.host_group_membership_sets_current set_current USING(host_group_id)
		 WHERE member.host_id=$1 AND member.membership_state='ACTIVE'
		   AND member.membership_set_generation=set_current.membership_set_generation
		   AND ($4='' OR member.host_group_id<>$4)
		 UNION ALL
		 SELECT $4::text,$5::bigint WHERE $4<>'' AND $5::bigint>0
		)
		SELECT binding.binding_id,binding.binding_generation,binding.host_group_id,
		binding.host_group_generation,member.membership_set_generation,binding.policy_id,binding.policy_revision,
		binding.policy_digest,binding.priority,binding.binding_digest,
		CASE WHEN binding.lifecycle_state='ACTIVE' AND group_current.lifecycle_state='ACTIVE'
		 AND binding.host_group_generation=group_current.host_group_generation
		 AND set_evidence.based_on_host_group_generation=group_current.host_group_generation
		 AND policy_current.policy_revision=binding.policy_revision AND policy_current.policy_digest=binding.policy_digest
		 AND policy_current.lifecycle_state='ACTIVE' THEN 'CURRENT' ELSE 'STALE' END
		FROM applicable_memberships member
		JOIN kim.host_group_membership_set_evidence set_evidence
		 ON set_evidence.host_group_id=member.host_group_id
		AND set_evidence.membership_set_generation=member.membership_set_generation
		JOIN kim.host_groups_current group_current ON group_current.host_group_id=member.host_group_id
        JOIN kim.host_group_policy_bindings_current binding ON binding.host_group_id=member.host_group_id
        LEFT JOIN kim.group_policies_current policy_current ON policy_current.policy_type=binding.policy_type AND policy_current.policy_id=binding.policy_id
		WHERE binding.consumer_type=$2 AND binding.policy_type=$3
        ORDER BY binding.priority DESC,binding.binding_id`+lockClause, request.HostID, request.ConsumerType, request.PolicyType,
		request.PinnedHostGroupID, request.PinnedMembershipSetGeneration)
	if err != nil {
		return GroupPolicyResolution{}, err
	}
	defer rows.Close()
	var candidates []groupPolicyCandidate
	for rows.Next() {
		var c groupPolicyCandidate
		if err := rows.Scan(&c.BindingID, &c.BindingGeneration, &c.HostGroupID, &c.HostGroupGeneration, &c.MembershipSetGeneration, &c.PolicyID, &c.PolicyRevision, &c.PolicyDigest, &c.Priority, &c.BindingDigest, &c.State); err != nil {
			return GroupPolicyResolution{}, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return GroupPolicyResolution{}, err
	}
	result := "NO_ASSIGNMENT"
	if len(candidates) > 0 {
		top := candidates[0].Priority
		var identities = map[string]bool{}
		stale := false
		for _, c := range candidates {
			if c.Priority != top {
				break
			}
			if c.State != "CURRENT" {
				stale = true
			}
			identities[fmt.Sprintf("%s/%d/%s", c.PolicyID, c.PolicyRevision, c.PolicyDigest)] = true
		}
		if stale {
			result = "STALE_ASSIGNMENT"
		} else if len(identities) > 1 {
			result = "ASSIGNMENT_CONFLICT"
		} else {
			result = "RESOLVED"
		}
	}
	return persistGroupPolicyResolutionTx(ctx, tx, request, result, candidates, !request.ReadOnly)
}

func persistGroupPolicyResolutionTx(ctx context.Context, tx pgx.Tx, request GroupPolicyResolutionRequest, result string, candidates []groupPolicyCandidate, persist bool) (GroupPolicyResolution, error) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority == candidates[j].Priority {
			return candidates[i].BindingID < candidates[j].BindingID
		}
		return candidates[i].Priority > candidates[j].Priority
	})
	inputRaw, _ := json.Marshal(candidates)
	inputDigest := digestReleaseBytes(inputRaw)
	r := GroupPolicyResolution{ResolutionID: request.ResolutionID, HostID: request.HostID, Result: result, InputDigest: inputDigest}
	if len(candidates) > 0 {
		r.WinningPriority = candidates[0].Priority
	}
	if result == "RESOLVED" {
		r.EffectivePolicyID = candidates[0].PolicyID
		r.EffectivePolicyRevision = candidates[0].PolicyRevision
		r.EffectivePolicyDigest = candidates[0].PolicyDigest
	}
	resolutionRaw, _ := json.Marshal(r)
	r.ResolutionDigest = digestReleaseBytes(resolutionRaw)
	if !persist {
		return r, nil
	}
	var priorDigest string
	err := tx.QueryRow(ctx, `SELECT resolution_digest FROM kim.host_group_policy_resolution_evidence WHERE resolution_id=$1`, request.ResolutionID).Scan(&priorDigest)
	if err == nil {
		if priorDigest != r.ResolutionDigest {
			return GroupPolicyResolution{}, ErrGroupPolicyConflict
		}
		return r, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return GroupPolicyResolution{}, err
	}
	var priority any
	if len(candidates) > 0 {
		priority = r.WinningPriority
	}
	var policyID, policyDigest any
	var policyRevision any
	if result == "RESOLVED" {
		policyID = r.EffectivePolicyID
		policyRevision = r.EffectivePolicyRevision
		policyDigest = r.EffectivePolicyDigest
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_policy_resolution_evidence(
		resolution_id,subject_type,subject_id,consumer_type,policy_type,resolution_result,winning_priority,
		effective_policy_id,effective_policy_revision,effective_policy_digest,input_digest,resolution_digest)
		VALUES($1,'HOST',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, request.ResolutionID, request.HostID, request.ConsumerType, request.PolicyType, result, priority, policyID, policyRevision, policyDigest, inputDigest, r.ResolutionDigest); err != nil {
		return GroupPolicyResolution{}, err
	}
	for _, c := range candidates {
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_group_policy_resolution_input_evidence(
		resolution_id,binding_id,binding_generation,host_group_id,host_group_generation,membership_set_generation,
		policy_id,policy_revision,policy_digest,priority,input_state,binding_digest)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, request.ResolutionID, c.BindingID, c.BindingGeneration, c.HostGroupID, c.HostGroupGeneration, c.MembershipSetGeneration, c.PolicyID, c.PolicyRevision, c.PolicyDigest, c.Priority, c.State, c.BindingDigest); err != nil {
			return GroupPolicyResolution{}, err
		}
	}
	return r, nil
}
