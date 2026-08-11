package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

var ErrPlacementScopeConflict = errors.New("Placement Scope authority conflict")

type PlacementScopeExposure struct {
	HostGroupID         string
	HostGroupGeneration uint64
}

type PlacementScopePublishRequest struct {
	PublishRequestID, PlacementScopeID, ConsumerType, ProjectID, LifecycleState string
	ExpectedCurrentGeneration                                                   uint64
	Exposures                                                                   []PlacementScopeExposure
}

type PlacementScope struct {
	PlacementScopeID, ConsumerType, ProjectID, LifecycleState string
	ExposureSetDigest, ScopeDigest                            string
	ScopeGeneration                                           uint64
	ExposureCount                                             int
}

type PlacementVisibilityProvenance struct {
	HostGroupID, MembershipEvidenceDigest, ProvenanceDigest string
	HostGroupGeneration, MembershipSetGeneration            uint64
	MembershipGeneration                                    uint64
}

type PlacementScopeCandidate struct {
	HostID      string
	Visible     bool
	Eligible    bool
	Evaluation  placement.Evaluation
	Provenance  []PlacementVisibilityProvenance
	Evaluations []placement.Evaluation
}

type PlacementScopeDryResult struct {
	PlacementScopeID, ScopeDigest, ProjectID, Status string
	ScopeGeneration                                  uint64
	Candidates                                       []PlacementScopeCandidate
}

type scopeTxBeginner struct{ pgx.Tx }

func (b scopeTxBeginner) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return b.Tx.Begin(ctx)
}

func PublishPlacementScope(ctx context.Context, db TxBeginner, request PlacementScopePublishRequest) (PlacementScope, error) {
	if request.PublishRequestID == "" || request.PlacementScopeID == "" || request.ConsumerType != "VM_PLACEMENT" ||
		request.ProjectID == "" || (request.LifecycleState != "ACTIVE" && request.LifecycleState != "DRAINING" && request.LifecycleState != "RETIRED") {
		return PlacementScope{}, ErrPlacementScopeConflict
	}
	exposures := append([]PlacementScopeExposure(nil), request.Exposures...)
	sort.Slice(exposures, func(i, j int) bool { return exposures[i].HostGroupID < exposures[j].HostGroupID })
	for i, exposure := range exposures {
		if exposure.HostGroupID == "" || exposure.HostGroupGeneration == 0 || (i > 0 && exposures[i-1].HostGroupID == exposure.HostGroupID) {
			return PlacementScope{}, ErrPlacementScopeConflict
		}
	}
	exposureRaw, _ := json.Marshal(exposures)
	exposureDigest := digestReleaseBytes(exposureRaw)
	requestRaw, _ := json.Marshal(map[string]any{"scope_id": request.PlacementScopeID, "consumer_type": request.ConsumerType,
		"project_id": request.ProjectID, "lifecycle": request.LifecycleState, "exposures": exposures})
	requestDigest := digestReleaseBytes(requestRaw)
	var result PlacementScope
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "placement-scope/"+request.PlacementScopeID); err != nil {
			return err
		}
		var replayDigest, replayScopeDigest string
		var replayGeneration uint64
		err := tx.QueryRow(ctx, `SELECT request_digest,scope_digest,scope_generation FROM kim.placement_scope_revision_evidence WHERE publish_request_id=$1`, request.PublishRequestID).Scan(&replayDigest, &replayScopeDigest, &replayGeneration)
		if err == nil {
			if replayDigest != requestDigest {
				return ErrPlacementScopeConflict
			}
			result = PlacementScope{PlacementScopeID: request.PlacementScopeID, ScopeGeneration: replayGeneration,
				ConsumerType: request.ConsumerType, ProjectID: request.ProjectID, LifecycleState: request.LifecycleState,
				ExposureSetDigest: exposureDigest, ScopeDigest: replayScopeDigest, ExposureCount: len(exposures)}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		for _, exposure := range exposures {
			var generation uint64
			var groupType, lifecycle string
			if err := tx.QueryRow(ctx, `SELECT host_group_generation,group_type,lifecycle_state FROM kim.host_groups_current WHERE host_group_id=$1 FOR SHARE`, exposure.HostGroupID).Scan(&generation, &groupType, &lifecycle); err != nil || generation != exposure.HostGroupGeneration || groupType != "PLACEMENT_POOL" || lifecycle != "ACTIVE" {
				return ErrPlacementScopeConflict
			}
		}
		var current uint64
		err = tx.QueryRow(ctx, `SELECT scope_generation FROM kim.placement_scopes_current WHERE placement_scope_id=$1 FOR UPDATE`, request.PlacementScopeID).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			current = 0
		} else if err != nil {
			return err
		}
		if current != request.ExpectedCurrentGeneration {
			return ErrPlacementScopeConflict
		}
		generation := current + 1
		scopeRaw, _ := json.Marshal(map[string]any{"scope_id": request.PlacementScopeID, "generation": generation,
			"consumer_type": request.ConsumerType, "project_id": request.ProjectID, "lifecycle": request.LifecycleState,
			"exposure_set_digest": exposureDigest})
		scopeDigest := digestReleaseBytes(scopeRaw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.placement_scope_revision_evidence(
			placement_scope_id,scope_generation,publish_request_id,request_digest,consumer_type,project_id,
			lifecycle_state,exposure_set_digest,exposure_count,scope_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			request.PlacementScopeID, generation, request.PublishRequestID, requestDigest, request.ConsumerType,
			request.ProjectID, request.LifecycleState, exposureDigest, len(exposures), scopeDigest); err != nil {
			return err
		}
		for _, exposure := range exposures {
			digest := digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%s\n%d\nCANDIDATE", request.PlacementScopeID, generation, exposure.HostGroupID, exposure.HostGroupGeneration)))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.placement_scope_host_group_evidence(
				placement_scope_id,scope_generation,host_group_id,host_group_generation,exposure_mode,exposure_digest)
				VALUES($1,$2,$3,$4,'CANDIDATE',$5)`, request.PlacementScopeID, generation,
				exposure.HostGroupID, exposure.HostGroupGeneration, digest); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.placement_scopes_current(
			placement_scope_id,scope_generation,consumer_type,project_id,lifecycle_state,exposure_set_digest,
			exposure_count,scope_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT(placement_scope_id) DO UPDATE SET scope_generation=EXCLUDED.scope_generation,
			consumer_type=EXCLUDED.consumer_type,project_id=EXCLUDED.project_id,lifecycle_state=EXCLUDED.lifecycle_state,
			exposure_set_digest=EXCLUDED.exposure_set_digest,exposure_count=EXCLUDED.exposure_count,
			scope_digest=EXCLUDED.scope_digest,updated_at=statement_timestamp()`, request.PlacementScopeID, generation,
			request.ConsumerType, request.ProjectID, request.LifecycleState, exposureDigest, len(exposures), scopeDigest); err != nil {
			return err
		}
		result = PlacementScope{PlacementScopeID: request.PlacementScopeID, ScopeGeneration: generation,
			ConsumerType: request.ConsumerType, ProjectID: request.ProjectID, LifecycleState: request.LifecycleState,
			ExposureSetDigest: exposureDigest, ScopeDigest: scopeDigest, ExposureCount: len(exposures)}
		return nil
	})
	return result, err
}

func DryEvaluatePlacementScope(ctx context.Context, db TxBeginner, request PlacementAdmissionRequest) (PlacementScopeDryResult, error) {
	scopeID := request.PlacementScopeID
	if scopeID == "" {
		return PlacementScopeDryResult{Status: "NO_SCOPE"}, nil
	}
	if request.RequestID == "" || request.ProjectID == "" || request.WorkloadID == "" || request.ImageID == "" || request.FlavorID == "" || request.PoolID != "" || request.PlacementScopeGeneration != 0 || request.PlacementScopeDigest != "" || request.VisibilityProvenanceDigest != "" {
		return PlacementScopeDryResult{}, ErrPlacementScopeConflict
	}
	request.PCI = normalizePlacementPCIRequirements(request.PCI)
	request.Network = normalizePlacementNetworkRequirements(request.Network)
	request.Storage = normalizePlacementStorageRequirements(request.Storage)
	var result PlacementScopeDryResult
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var lifecycle, consumer, projectID string
		var exposureCount int
		if err := tx.QueryRow(ctx, `SELECT scope_generation,consumer_type,project_id,lifecycle_state,exposure_count,scope_digest
			FROM kim.placement_scopes_current WHERE placement_scope_id=$1`, scopeID).Scan(&result.ScopeGeneration,
			&consumer, &projectID, &lifecycle, &exposureCount, &result.ScopeDigest); errors.Is(err, pgx.ErrNoRows) {
			result = PlacementScopeDryResult{PlacementScopeID: scopeID, Status: "NO_SCOPE"}
			return nil
		} else if err != nil {
			return err
		}
		result.PlacementScopeID, result.ProjectID = scopeID, projectID
		if consumer != "VM_PLACEMENT" || projectID != request.ProjectID || lifecycle != "ACTIVE" {
			result.Status = "SCOPE_BLOCKED"
			return nil
		}
		var validExposureCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.placement_scope_host_group_evidence exposure
			JOIN kim.host_groups_current group_current ON group_current.host_group_id=exposure.host_group_id
			WHERE exposure.placement_scope_id=$1 AND exposure.scope_generation=$2
			AND exposure.host_group_generation=group_current.host_group_generation
			AND group_current.group_type='PLACEMENT_POOL' AND group_current.lifecycle_state='ACTIVE'`,
			scopeID, result.ScopeGeneration).Scan(&validExposureCount); err != nil {
			return err
		}
		if validExposureCount != exposureCount {
			result.Status = "SCOPE_BLOCKED"
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT member.host_id,exposure.host_group_id,exposure.host_group_generation,
			set_current.membership_set_generation,member.membership_generation,member.evidence_digest
			FROM kim.placement_scope_host_group_evidence exposure
			JOIN kim.host_group_membership_sets_current set_current ON set_current.host_group_id=exposure.host_group_id
			JOIN kim.host_group_memberships_current member ON member.host_group_id=exposure.host_group_id
			WHERE exposure.placement_scope_id=$1 AND exposure.scope_generation=$2
			AND set_current.based_on_host_group_generation=exposure.host_group_generation
			AND set_current.validation_state='ACCEPTED' AND member.membership_set_generation=set_current.membership_set_generation
			AND member.membership_state='ACTIVE' ORDER BY member.host_id,exposure.host_group_id`, scopeID, result.ScopeGeneration)
		if err != nil {
			return err
		}
		byHost := map[string][]PlacementVisibilityProvenance{}
		for rows.Next() {
			var p PlacementVisibilityProvenance
			var hostID string
			if err := rows.Scan(&hostID, &p.HostGroupID, &p.HostGroupGeneration, &p.MembershipSetGeneration,
				&p.MembershipGeneration, &p.MembershipEvidenceDigest); err != nil {
				rows.Close()
				return err
			}
			p.ProvenanceDigest = placementVisibilityDigest(scopeID, result.ScopeGeneration, hostID, p)
			byHost[hostID] = append(byHost[hostID], p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		hosts := make([]string, 0, len(byHost))
		for hostID := range byHost {
			hosts = append(hosts, hostID)
		}
		sort.Strings(hosts)
		for _, hostID := range hosts {
			candidate := PlacementScopeCandidate{HostID: hostID, Visible: true, Provenance: byHost[hostID]}
			for _, provenance := range candidate.Provenance {
				perGroup := request
				perGroup.PoolID = provenance.HostGroupID
				evaluation, err := evaluatePlacementTx(ctx, tx, perGroup, hostID)
				if err != nil {
					return err
				}
				candidate.Evaluations = append(candidate.Evaluations, evaluation)
			}
			eligible := placement.Select(candidate.Evaluations)
			if len(eligible) > 0 {
				candidate.Eligible = true
				candidate.Evaluation = eligible[0]
			} else if len(candidate.Evaluations) > 0 {
				candidate.Evaluation = candidate.Evaluations[0]
			}
			result.Candidates = append(result.Candidates, candidate)
		}
		if len(result.Candidates) == 0 {
			result.Status = "NO_VISIBLE_HOST"
			return nil
		}
		result.Status = "VISIBLE_BUT_NO_ELIGIBLE_HOST"
		for _, candidate := range result.Candidates {
			if candidate.Eligible {
				result.Status = "READY"
				break
			}
		}
		return nil
	})
	return result, err
}

func FinalAdmitPlacementScope(ctx context.Context, db TxBeginner, scopeDry PlacementScopeDryResult, request PlacementAdmissionRequest, candidate PlacementScopeCandidate) (PlacementAdmission, error) {
	if scopeDry.Status != "READY" || scopeDry.PlacementScopeID == "" || candidate.HostID == "" || !candidate.Eligible || candidate.Evaluation.HostID != candidate.HostID || request.PoolID != "" || request.PlacementScopeID != scopeDry.PlacementScopeID || request.PlacementScopeGeneration != 0 || request.PlacementScopeDigest != "" || request.VisibilityProvenanceDigest != "" || request.ProjectID != scopeDry.ProjectID {
		return PlacementAdmission{}, ErrPlacementScopeConflict
	}
	var admission PlacementAdmission
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "placement-scope/"+scopeDry.PlacementScopeID); err != nil {
			return err
		}
		groupIDs := make([]string, 0, len(candidate.Provenance))
		seenGroup := make(map[string]struct{}, len(candidate.Provenance))
		for _, provenance := range candidate.Provenance {
			if _, seen := seenGroup[provenance.HostGroupID]; seen {
				continue
			}
			seenGroup[provenance.HostGroupID] = struct{}{}
			groupIDs = append(groupIDs, provenance.HostGroupID)
		}
		sort.Strings(groupIDs)
		for _, hostGroupID := range groupIDs {
			if err := lockHostGroupTx(ctx, tx, hostGroupID); err != nil {
				return err
			}
		}
		var generation uint64
		var lifecycle, consumer, projectID, scopeDigest string
		if err := tx.QueryRow(ctx, `SELECT scope_generation,lifecycle_state,consumer_type,project_id,scope_digest
			FROM kim.placement_scopes_current WHERE placement_scope_id=$1 FOR SHARE`, scopeDry.PlacementScopeID).Scan(&generation, &lifecycle, &consumer, &projectID, &scopeDigest); err != nil || generation != scopeDry.ScopeGeneration || lifecycle != "ACTIVE" || consumer != "VM_PLACEMENT" || projectID != request.ProjectID || scopeDigest != scopeDry.ScopeDigest {
			return ErrPlacementStale
		}
		for _, p := range candidate.Provenance {
			var valid bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.placement_scope_host_group_evidence exposure
				JOIN kim.host_groups_current group_current ON group_current.host_group_id=exposure.host_group_id
				JOIN kim.host_group_membership_sets_current set_current ON set_current.host_group_id=exposure.host_group_id
				JOIN kim.host_group_memberships_current member ON member.host_group_id=exposure.host_group_id AND member.host_id=$3
				WHERE exposure.placement_scope_id=$1 AND exposure.scope_generation=$2 AND exposure.host_group_id=$4
				AND exposure.host_group_generation=$5 AND group_current.host_group_generation=$5 AND group_current.lifecycle_state='ACTIVE'
				AND set_current.membership_set_generation=$6 AND member.membership_set_generation=$6
				AND member.membership_generation=$7 AND member.evidence_digest=$8 AND member.membership_state='ACTIVE')`,
				scopeDry.PlacementScopeID, scopeDry.ScopeGeneration, candidate.HostID, p.HostGroupID, p.HostGroupGeneration,
				p.MembershipSetGeneration, p.MembershipGeneration, p.MembershipEvidenceDigest).Scan(&valid); err != nil || !valid {
				return ErrPlacementStale
			}
		}
		provenanceRaw, _ := json.Marshal(candidate.Provenance)
		provenanceDigest := digestReleaseBytes(provenanceRaw)
		scopedRequest := request
		scopedRequest.PoolID = candidate.Evaluation.PoolID
		scopedRequest.PlacementScopeID = scopeDry.PlacementScopeID
		scopedRequest.PlacementScopeGeneration = scopeDry.ScopeGeneration
		scopedRequest.PlacementScopeDigest = scopeDry.ScopeDigest
		scopedRequest.VisibilityProvenanceDigest = provenanceDigest
		var err error
		admission, err = finalAdmitPlacement(ctx, scopeTxBeginner{tx}, scopedRequest, candidate.Evaluation)
		if err != nil {
			return err
		}
		for _, p := range candidate.Provenance {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.placement_admission_scope_visibility_evidence(
				admission_id,placement_scope_id,scope_generation,host_id,host_group_id,host_group_generation,
				membership_set_generation,membership_generation,membership_evidence_digest,provenance_digest)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(admission_id,host_group_id) DO NOTHING`,
				admission.AdmissionID, scopeDry.PlacementScopeID, scopeDry.ScopeGeneration, candidate.HostID, p.HostGroupID,
				p.HostGroupGeneration, p.MembershipSetGeneration, p.MembershipGeneration, p.MembershipEvidenceDigest, p.ProvenanceDigest); err != nil {
				return err
			}
		}
		return nil
	})
	return admission, err
}

func placementVisibilityDigest(scopeID string, generation uint64, hostID string, p PlacementVisibilityProvenance) string {
	return digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%s\n%s\n%d\n%d\n%d\n%s", scopeID, generation, hostID, p.HostGroupID, p.HostGroupGeneration, p.MembershipSetGeneration, p.MembershipGeneration, p.MembershipEvidenceDigest)))
}
