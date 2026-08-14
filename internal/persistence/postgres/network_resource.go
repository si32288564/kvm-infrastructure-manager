package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

var networkResourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,254}$`)

var ErrNoNetworkRealizationWork = errors.New("no Network realization work available")

type NetworkResourceRequest struct {
	NetworkID, ProjectID, Name, Profile, SegmentPolicy string
	MTU                                                uint32
	RequestedSegmentID                                 uint32
	DeleteProtection                                   bool
}

type NetworkResourcePatch struct {
	ExpectedRevision uint64
	Name             string
	MTU              uint32
	DeleteProtection bool
}

type NetworkResource struct {
	NetworkID, ProjectID, Name, Profile, SegmentPolicy, Lifecycle string
	SegmentType, AllocationID, RealizationState, OperationID      string
	Revision, AllocationGeneration, RealizationGeneration         uint64
	SegmentID, MTU                                                uint32
	DeleteProtection                                              bool
	CreatedAt, UpdatedAt                                          time.Time
}

type NetworkRealizationClaim struct {
	OperationID, Owner, ClaimMode, OperationKind, PlanDigest string
	OperationGeneration, ClaimGeneration                     uint64
	CanonicalPlan                                            []byte
	LeaseExpiresAt                                           time.Time
}

type NetworkRealizationObservation struct {
	ObservationID, OperationID, LogicalSwitchName, BackendUUID string
	ObservationDigest, AdapterArtifactDigest                   string
	OperationGeneration, ObservationGeneration                 uint64
	ApplyResponseState                                         string
	Observation                                                ovnadapter.NetworkObservation
}

func networkDesiredDigest(request NetworkResourceRequest, revision uint64, lifecycle string) (string, error) {
	raw, err := json.Marshal(struct {
		NetworkID, ProjectID, Name, Profile, SegmentPolicy, Lifecycle string
		MTU, RequestedSegmentID                                       uint32
		DeleteProtection                                              bool
		Revision                                                      uint64
	}{request.NetworkID, request.ProjectID, request.Name, request.Profile, request.SegmentPolicy, lifecycle,
		request.MTU, request.RequestedSegmentID, request.DeleteProtection, revision})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func CreateNetworkResource(ctx context.Context, db TxBeginner, request NetworkResourceRequest) (NetworkResource, error) {
	if !networkResourceIDPattern.MatchString(request.NetworkID) || request.ProjectID == "" || request.Name == "" || len(request.Name) > 255 || request.MTU < 576 || request.MTU > 9216 ||
		(request.Profile != "STANDARD_OVERLAY" && request.Profile != "PROVIDER_VLAN") || (request.SegmentPolicy != "AUTO" && request.SegmentPolicy != "EXPLICIT") ||
		(request.SegmentPolicy == "AUTO" && request.RequestedSegmentID != 0) || (request.SegmentPolicy == "EXPLICIT" && request.RequestedSegmentID == 0) ||
		(request.Profile == "STANDARD_OVERLAY" && request.RequestedSegmentID > 16777215) || (request.Profile == "PROVIDER_VLAN" && request.RequestedSegmentID > 4094) {
		return NetworkResource{}, errors.New("complete logical Network desired authority is required")
	}
	digest, err := networkDesiredDigest(request, 1, "ACTIVE")
	if err != nil {
		return NetworkResource{}, err
	}
	err = RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 5, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "network-resource/"+request.NetworkID); err != nil {
			return err
		}
		var existingDigest, source string
		if err := tx.QueryRow(ctx, `SELECT desired_digest,authority_source FROM kim.networks_current WHERE network_id=$1`, request.NetworkID).Scan(&existingDigest, &source); err == nil {
			if source == "NETWORK_RESOURCE" && existingDigest == digest {
				return nil
			}
			return ErrPlacementConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var projectActive bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.projects_current WHERE project_id=$1 AND lifecycle_state='ACTIVE')`, request.ProjectID).Scan(&projectActive); err != nil || !projectActive {
			return ErrPlacementConflict
		}
		poolID, segmentType := "standard-overlay", "VNI"
		if request.Profile == "PROVIDER_VLAN" {
			poolID, segmentType = "provider-vlan", "VLAN"
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "network-segment-pool/"+poolID); err != nil {
			return err
		}
		var minimum, maximum int64
		var poolGeneration int64
		if err := tx.QueryRow(ctx, `SELECT minimum_segment_id,maximum_segment_id,pool_generation FROM kim.network_segment_pools_current WHERE segment_pool_id=$1 AND lifecycle_state='ACTIVE' FOR UPDATE`, poolID).Scan(&minimum, &maximum, &poolGeneration); err != nil {
			return ErrPlacementConflict
		}
		segmentID := int64(request.RequestedSegmentID)
		if request.SegmentPolicy == "AUTO" {
			if err := tx.QueryRow(ctx, `SELECT candidate FROM generate_series($2::integer,$3::integer) candidate WHERE NOT EXISTS(SELECT 1 FROM kim.network_segment_allocations_current a WHERE a.segment_pool_id=$1 AND a.segment_id=candidate AND a.allocation_state IN ('ALLOCATED','RELEASE_PENDING')) ORDER BY candidate LIMIT 1`, poolID, minimum, maximum).Scan(&segmentID); err != nil {
				return ErrPlacementConflict
			}
		} else if segmentID < minimum || segmentID > maximum {
			return ErrPlacementConflict
		}
		var collision bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_segment_allocations_current WHERE segment_pool_id=$1 AND segment_id=$2 AND allocation_state IN ('ALLOCATED','RELEASE_PENDING'))`, poolID, segmentID).Scan(&collision); err != nil || collision {
			return ErrPlacementConflict
		}
		allocationID := fmt.Sprintf("network-allocation:%s:1", request.NetworkID)
		allocationDigest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s/%d/1", request.NetworkID, poolID, poolGeneration, segmentType, segmentID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_resource_revision_evidence(network_id,network_revision,project_id,network_name,network_profile,mtu,segment_policy,requested_segment_id,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,1,$2,$3,$4,$5,$6,NULLIF($7,0),$8,'ACTIVE',NULL,$9)`, request.NetworkID, request.ProjectID, request.Name, request.Profile, request.MTU, request.SegmentPolicy, request.RequestedSegmentID, request.DeleteProtection, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.networks_current(network_id,project_id,network_generation,lifecycle_state,mtu,network_revision,network_name,network_profile,segment_policy,delete_protection,desired_digest,authority_source,created_at) VALUES($1,$2,1,'ACTIVE',$3,1,$4,$5,$6,$7,$8,'NETWORK_RESOURCE',statement_timestamp())`, request.NetworkID, request.ProjectID, request.MTU, request.Name, request.Profile, request.SegmentPolicy, request.DeleteProtection, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_segment_allocation_decision_evidence(allocation_id,network_id,network_revision,segment_pool_id,pool_generation,segment_type,segment_id,allocation_generation,decision_state,decision_digest) VALUES($1,$2,1,$3,$4,$5,$6,1,'ALLOCATED',$7)`, allocationID, request.NetworkID, poolID, poolGeneration, segmentType, segmentID, allocationDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_segment_allocations_current(network_id,allocation_id,network_revision,segment_pool_id,segment_type,segment_id,allocation_generation,allocation_state) VALUES($1,$2,1,$3,$4,$5,1,'ALLOCATED')`, request.NetworkID, allocationID, poolID, segmentType, segmentID); err != nil {
			return err
		}
		segmentClaimID := "network-segment:" + request.NetworkID
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_segment_claims_current(segment_claim_id,network_id,segment_generation,segment_type,scope_id,segment_id,provider_mapping_revision,claim_state,allocation_id,allocation_generation,network_revision) VALUES($1,$2,1,$3,$4,$5,1,'ACTIVE',$6,1,1)`, segmentClaimID, request.NetworkID, segmentType, poolID, segmentID, allocationID); err != nil {
			return err
		}
		return insertNetworkRealizationOperationTx(ctx, tx, request, 1, 1, 1, 1, allocationID, segmentType, uint32(segmentID), "REALIZE", "PRESENT", 1)
	})
	if err != nil {
		return NetworkResource{}, err
	}
	return GetNetworkResource(ctx, db, request.NetworkID)
}

func insertNetworkRealizationOperationTx(ctx context.Context, tx pgx.Tx, desired NetworkResourceRequest, authorityRevision, backendRevision, allocationGeneration, realizationGeneration uint64, allocationID, segmentType string, segmentID uint32, kind, state string, operationGeneration uint64) error {
	operationID := fmt.Sprintf("network-realization:%s:%d", desired.NetworkID, realizationGeneration)
	plan, planDigest, err := ovnadapter.PlanNetwork(ovnadapter.NetworkIntentInput{OperationID: operationID, OperationGeneration: operationGeneration,
		ProjectID: desired.ProjectID, NetworkID: desired.NetworkID, AuthorityRevision: authorityRevision, BackendRevision: backendRevision,
		AllocationID: allocationID, AllocationGeneration: allocationGeneration, RealizationGeneration: realizationGeneration,
		SegmentType: segmentType, SegmentID: segmentID, DesiredState: state})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.network_realization_operation_evidence(operation_id,operation_generation,operation_kind,network_id,network_revision,allocation_id,allocation_generation,realization_generation,schema_version,canonical_plan,plan_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, operationID, operationGeneration, kind, desired.NetworkID, authorityRevision, allocationID, allocationGeneration, realizationGeneration, ovnadapter.NetworkIntentSchema, plan, planDigest); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.network_realization_operations_current(operation_id,operation_generation,network_id,network_revision,allocation_id,allocation_generation,realization_generation,operation_kind,phase) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'PENDING') ON CONFLICT(network_id) DO UPDATE SET operation_id=EXCLUDED.operation_id,operation_generation=EXCLUDED.operation_generation,network_revision=EXCLUDED.network_revision,allocation_id=EXCLUDED.allocation_id,allocation_generation=EXCLUDED.allocation_generation,realization_generation=EXCLUDED.realization_generation,operation_kind=EXCLUDED.operation_kind,phase='PENDING',last_claim_generation=0,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,response_state=NULL,terminal_evidence_id=NULL,updated_at=statement_timestamp()`, operationID, operationGeneration, desired.NetworkID, authorityRevision, allocationID, allocationGeneration, realizationGeneration, kind); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.network_realizations_current(network_id,network_revision,allocation_generation,realization_generation,operation_id,operation_generation,realization_state) VALUES($1,$2,$3,$4,$5,$6,'PENDING') ON CONFLICT(network_id) DO UPDATE SET network_revision=EXCLUDED.network_revision,allocation_generation=EXCLUDED.allocation_generation,realization_generation=EXCLUDED.realization_generation,operation_id=EXCLUDED.operation_id,operation_generation=EXCLUDED.operation_generation,realization_state='PENDING',terminal_evidence_id=NULL,updated_at=statement_timestamp()`, desired.NetworkID, authorityRevision, allocationGeneration, realizationGeneration, operationID, operationGeneration)
	return err
}

func UpdateNetworkResource(ctx context.Context, db TxBeginner, networkID string, patch NetworkResourcePatch) (NetworkResource, error) {
	if !networkResourceIDPattern.MatchString(networkID) || patch.ExpectedRevision == 0 || patch.Name == "" || len(patch.Name) > 255 || patch.MTU < 576 || patch.MTU > 9216 {
		return NetworkResource{}, errors.New("complete Network revision patch is required")
	}
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 5, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var desired NetworkResourceRequest
		var revision, allocationGeneration, realizationGeneration, segmentID int64
		var allocationID, segmentType, lifecycle string
		if err := tx.QueryRow(ctx, `SELECT n.project_id,n.network_name,n.network_profile,n.mtu,n.segment_policy,n.delete_protection,n.lifecycle_state,n.network_revision,a.allocation_id,a.allocation_generation,a.segment_type,a.segment_id,r.realization_generation FROM kim.networks_current n JOIN kim.network_segment_allocations_current a USING(network_id) JOIN kim.network_realizations_current r USING(network_id) WHERE n.network_id=$1 AND n.authority_source='NETWORK_RESOURCE' FOR UPDATE OF n,a,r`, networkID).Scan(&desired.ProjectID, &desired.Name, &desired.Profile, &desired.MTU, &desired.SegmentPolicy, &desired.DeleteProtection, &lifecycle, &revision, &allocationID, &allocationGeneration, &segmentType, &segmentID, &realizationGeneration); err != nil || lifecycle != "ACTIVE" || uint64(revision) != patch.ExpectedRevision {
			return ErrPlacementConflict
		}
		desired.NetworkID, desired.Name, desired.MTU, desired.DeleteProtection = networkID, patch.Name, patch.MTU, patch.DeleteProtection
		if desired.SegmentPolicy == "EXPLICIT" {
			desired.RequestedSegmentID = uint32(segmentID)
		}
		next := uint64(revision + 1)
		digest, err := networkDesiredDigest(desired, next, "ACTIVE")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_resource_revision_evidence(network_id,network_revision,project_id,network_name,network_profile,mtu,segment_policy,requested_segment_id,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,'ACTIVE',$10,$11)`, networkID, next, desired.ProjectID, desired.Name, desired.Profile, desired.MTU, desired.SegmentPolicy, desired.RequestedSegmentID, desired.DeleteProtection, revision, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.networks_current SET network_revision=$2,network_generation=$2,network_name=$3,mtu=$4,delete_protection=$5,desired_digest=$6,updated_at=statement_timestamp() WHERE network_id=$1 AND network_revision=$7`, networkID, next, desired.Name, desired.MTU, desired.DeleteProtection, digest, revision); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_segment_allocations_current SET network_revision=$2,updated_at=statement_timestamp() WHERE network_id=$1`, networkID, next); err != nil {
			return err
		}
		return insertNetworkRealizationOperationTx(ctx, tx, desired, next, next, uint64(allocationGeneration), uint64(realizationGeneration+1), allocationID, segmentType, uint32(segmentID), "REALIZE", "PRESENT", 1)
	})
	if err != nil {
		return NetworkResource{}, err
	}
	return GetNetworkResource(ctx, db, networkID)
}

func ClaimNetworkRealization(ctx context.Context, db TxBeginner, operationID, owner string, lease time.Duration) (NetworkRealizationClaim, error) {
	if owner == "" || lease <= 0 || lease > MaxOVNRuntimeClaimLifetime {
		return NetworkRealizationClaim{}, errors.New("bounded Network realization claim is required")
	}
	var claim NetworkRealizationClaim
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var phase string
		var priorOwner *string
		var priorGeneration *int64
		var generation int64
		if err := tx.QueryRow(ctx, `SELECT c.operation_id,c.operation_generation,c.operation_kind,c.phase,c.claim_owner,c.claim_generation,e.canonical_plan,e.plan_digest FROM kim.network_realization_operations_current c JOIN kim.network_realization_operation_evidence e USING(operation_id,operation_generation) WHERE ($1='' OR c.operation_id=$1) AND (c.phase IN ('PENDING','DISPATCH_UNKNOWN') OR (c.phase='CLAIMED' AND c.claim_expires_at<=statement_timestamp())) ORDER BY c.updated_at,c.operation_id FOR UPDATE OF c SKIP LOCKED LIMIT 1`, operationID).Scan(&claim.OperationID, &claim.OperationGeneration, &claim.OperationKind, &phase, &priorOwner, &priorGeneration, &claim.CanonicalPlan, &claim.PlanDigest); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoNetworkRealizationWork
		} else if err != nil {
			return err
		}
		claim.ClaimMode = "APPLY_ALLOWED"
		if phase != "PENDING" {
			claim.ClaimMode = "READ_BACK_FIRST"
		}
		if err := tx.QueryRow(ctx, `UPDATE kim.network_realization_operations_current SET phase='CLAIMED',claim_owner=$2,claim_generation=last_claim_generation+1,last_claim_generation=last_claim_generation+1,claim_expires_at=statement_timestamp()+($3*interval '1 microsecond'),updated_at=statement_timestamp() WHERE operation_id=$1 RETURNING claim_generation,claim_expires_at`, claim.OperationID, owner, lease.Microseconds()).Scan(&generation, &claim.LeaseExpiresAt); err != nil {
			return err
		}
		claim.Owner, claim.ClaimGeneration = owner, uint64(generation)
		_, err := tx.Exec(ctx, `INSERT INTO kim.network_realization_attempt_evidence(operation_id,operation_generation,claim_generation,claim_owner,claim_mode,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6)`, claim.OperationID, claim.OperationGeneration, generation, owner, claim.ClaimMode, claim.LeaseExpiresAt)
		return err
	})
	return claim, err
}

func MarkNetworkRealizationDispatchUnknown(ctx context.Context, db TxBeginner, claim NetworkRealizationClaim) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockNetworkRealizationClaim(ctx, tx, claim); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.network_realization_operations_current SET phase='DISPATCH_UNKNOWN',response_state='UNKNOWN',claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, claim.OperationID)
		return err
	})
}

func RecordNetworkRealizationReadBackStarted(ctx context.Context, db TxBeginner, claim NetworkRealizationClaim) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockNetworkRealizationClaim(ctx, tx, claim); err != nil {
			return err
		}
		var mode string
		if err := tx.QueryRow(ctx, `SELECT claim_mode FROM kim.network_realization_attempt_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration).Scan(&mode); err != nil || mode != "READ_BACK_FIRST" {
			return ErrPlacementConflict
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.network_realization_attempt_event_evidence(operation_id,operation_generation,claim_generation,event_type) VALUES($1,$2,$3,'READ_BACK_STARTED') ON CONFLICT DO NOTHING`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration)
		return err
	})
}

func AuthorizeNetworkRealizationApply(ctx context.Context, db TxBeginner, claim NetworkRealizationClaim) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockNetworkRealizationClaim(ctx, tx, claim); err != nil {
			return err
		}
		var mode string
		if err := tx.QueryRow(ctx, `SELECT claim_mode FROM kim.network_realization_attempt_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration).Scan(&mode); err != nil {
			return err
		}
		if mode == "READ_BACK_FIRST" {
			var readBackRecorded bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_realization_attempt_event_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3 AND event_type='READ_BACK_STARTED')`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration).Scan(&readBackRecorded); err != nil || !readBackRecorded {
				return ErrPlacementConflict
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.network_realization_attempt_event_evidence(operation_id,operation_generation,claim_generation,event_type) VALUES($1,$2,$3,'APPLY_AUTHORIZED') ON CONFLICT DO NOTHING`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration)
		return err
	})
}

func AcceptNetworkRealizationObservation(ctx context.Context, db TxBeginner, claim NetworkRealizationClaim, observed NetworkRealizationObservation) (string, error) {
	if observed.ObservationID == "" || observed.OperationID != claim.OperationID || observed.OperationGeneration != claim.OperationGeneration || observed.ObservationGeneration == 0 || len(observed.ObservationDigest) != 64 || len(observed.AdapterArtifactDigest) != 64 || (observed.ApplyResponseState != "RECEIVED" && observed.ApplyResponseState != "LOST" && observed.ApplyResponseState != "UNKNOWN") {
		return "", ErrPlacementConflict
	}
	terminalState := ""
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockNetworkRealizationClaim(ctx, tx, claim); err != nil {
			return err
		}
		var networkID, allocationID, kind, planDigest string
		var networkRevision, allocationGeneration, realizationGeneration int64
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT c.network_id,c.network_revision,c.allocation_id,c.allocation_generation,c.realization_generation,c.operation_kind,e.canonical_plan,e.plan_digest FROM kim.network_realization_operations_current c JOIN kim.network_realization_operation_evidence e USING(operation_id,operation_generation) WHERE c.operation_id=$1`, claim.OperationID).Scan(&networkID, &networkRevision, &allocationID, &allocationGeneration, &realizationGeneration, &kind, &raw, &planDigest); err != nil {
			return err
		}
		_, plan, err := ovnadapter.RestoreStoredNetworkPlan(raw, planDigest)
		if err != nil {
			return err
		}
		if observed.LogicalSwitchName != plan.LogicalSwitchName {
			return ErrPlacementConflict
		}
		var claimMode string
		if err := tx.QueryRow(ctx, `SELECT claim_mode FROM kim.network_realization_attempt_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration).Scan(&claimMode); err != nil {
			return err
		}
		var readBackRecorded, applyAuthorized bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_realization_attempt_event_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3 AND event_type='READ_BACK_STARTED'), EXISTS(SELECT 1 FROM kim.network_realization_attempt_event_evidence WHERE operation_id=$1 AND operation_generation=$2 AND claim_generation=$3 AND event_type='APPLY_AUTHORIZED')`, claim.OperationID, claim.OperationGeneration, claim.ClaimGeneration).Scan(&readBackRecorded, &applyAuthorized); err != nil {
			return err
		}
		if (claimMode == "READ_BACK_FIRST" && !readBackRecorded) || (observed.ApplyResponseState != "UNKNOWN" && !applyAuthorized) {
			return ErrPlacementConflict
		}
		state := observed.Observation.State(plan.DesiredState)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_realization_observation_evidence(observation_id,operation_id,operation_generation,network_id,network_revision,allocation_generation,realization_generation,observation_generation,apply_response_state,logical_switch_name,backend_uuid,object_present,ownership_marker_matches,plan_digest_matches,observation_digest,adapter_artifact_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,$16)`, observed.ObservationID, claim.OperationID, claim.OperationGeneration, networkID, networkRevision, allocationGeneration, realizationGeneration, observed.ObservationGeneration, observed.ApplyResponseState, observed.LogicalSwitchName, observed.BackendUUID, observed.Observation.LogicalSwitchPresent, observed.Observation.OwnershipMarkerMatches, observed.Observation.PlanDigestMatches, observed.ObservationDigest, observed.AdapterArtifactDigest); err != nil {
			return err
		}
		if (kind == "REALIZE" && state == "VERIFIED") || (kind == "RETIRE" && state == "ABSENT") {
			terminalState = state
			terminalID := fmt.Sprintf("network-terminal:%s:%d", claim.OperationID, claim.OperationGeneration)
			terminalDigest := digestNetworkResource(fmt.Sprintf("%s/%d/%s/%d/%d/%s", networkID, networkRevision, allocationID, allocationGeneration, realizationGeneration, state))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.network_realization_terminal_evidence(terminal_evidence_id,operation_id,operation_generation,observation_id,network_id,network_revision,allocation_generation,realization_generation,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, terminalID, claim.OperationID, claim.OperationGeneration, observed.ObservationID, networkID, networkRevision, allocationGeneration, realizationGeneration, state, terminalDigest); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.network_realization_operations_current SET phase='SUCCEEDED',response_state=$2,terminal_evidence_id=$3,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, claim.OperationID, observed.ApplyResponseState, terminalID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.network_realizations_current SET realization_state=$2,terminal_evidence_id=$3,updated_at=statement_timestamp() WHERE network_id=$1 AND network_revision=$4 AND realization_generation=$5`, networkID, state, terminalID, networkRevision, realizationGeneration); err != nil {
				return err
			}
			if kind == "RETIRE" {
				var poolID, segmentType string
				var segmentID int64
				if err := tx.QueryRow(ctx, `SELECT segment_pool_id,segment_type,segment_id FROM kim.network_segment_allocations_current WHERE network_id=$1 AND allocation_id=$2 AND allocation_generation=$3 AND allocation_state='RELEASE_PENDING' FOR UPDATE`, networkID, allocationID, allocationGeneration).Scan(&poolID, &segmentType, &segmentID); err != nil {
					return ErrPlacementConflict
				}
				releaseDigest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s", allocationID, networkID, allocationGeneration, terminalID))
				if _, err := tx.Exec(ctx, `INSERT INTO kim.network_segment_release_evidence(release_evidence_id,allocation_id,network_id,allocation_generation,retirement_terminal_evidence_id,segment_pool_id,segment_type,segment_id,release_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, `network-release:`+allocationID, allocationID, networkID, allocationGeneration, terminalID, poolID, segmentType, segmentID, releaseDigest); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM kim.network_segment_allocations_current WHERE network_id=$1 AND allocation_id=$2 AND allocation_generation=$3`, networkID, allocationID, allocationGeneration); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE kim.network_segment_claims_current SET claim_state='RELEASED',updated_at=statement_timestamp() WHERE network_id=$1 AND allocation_id=$2 AND allocation_generation=$3`, networkID, allocationID, allocationGeneration); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE kim.networks_current SET lifecycle_state='DELETED',updated_at=statement_timestamp() WHERE network_id=$1 AND network_revision=$2 AND lifecycle_state='RETIRE_PENDING'`, networkID, networkRevision); err != nil {
					return err
				}
			}
			return nil
		}
		phase := "DISPATCH_UNKNOWN"
		if state == "CONFLICTING" {
			phase = "FAILED"
		}
		_, err = tx.Exec(ctx, `UPDATE kim.network_realization_operations_current SET phase=$2,response_state=$3,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,updated_at=statement_timestamp() WHERE operation_id=$1`, claim.OperationID, phase, observed.ApplyResponseState)
		return err
	})
	return terminalState, err
}

func RequestNetworkRetirement(ctx context.Context, db TxBeginner, networkID string, expectedRevision uint64) (NetworkResource, error) {
	err := RunSerializable(ctx, db, SerializableOptions{MaxAttempts: 5, BaseDelay: time.Millisecond}, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var desired NetworkResourceRequest
		var revision, allocationGeneration, realizationGeneration, segmentID int64
		var allocationID, segmentType, lifecycle string
		if err := tx.QueryRow(ctx, `SELECT n.project_id,n.network_name,n.network_profile,n.mtu,n.segment_policy,n.delete_protection,n.lifecycle_state,n.network_revision,a.allocation_id,a.allocation_generation,a.segment_type,a.segment_id,r.realization_generation FROM kim.networks_current n JOIN kim.network_segment_allocations_current a USING(network_id) JOIN kim.network_realizations_current r USING(network_id) WHERE n.network_id=$1 AND n.authority_source='NETWORK_RESOURCE' FOR UPDATE OF n,a,r`, networkID).Scan(&desired.ProjectID, &desired.Name, &desired.Profile, &desired.MTU, &desired.SegmentPolicy, &desired.DeleteProtection, &lifecycle, &revision, &allocationID, &allocationGeneration, &segmentType, &segmentID, &realizationGeneration); err != nil || lifecycle != "ACTIVE" || uint64(revision) != expectedRevision {
			return ErrPlacementConflict
		}
		if desired.DeleteProtection {
			return errors.New("Network delete protection is enabled")
		}
		var dependent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_subnets_current WHERE network_id=$1 AND lifecycle_state<>'DISABLED') OR EXISTS(SELECT 1 FROM kim.network_ports_current WHERE network_id=$1 AND desired_state<>'RELEASED')`, networkID).Scan(&dependent); err != nil || dependent {
			return ErrPlacementConflict
		}
		desired.NetworkID = networkID
		if desired.SegmentPolicy == "EXPLICIT" {
			desired.RequestedSegmentID = uint32(segmentID)
		}
		next := uint64(revision + 1)
		digest, err := networkDesiredDigest(desired, next, "RETIRE_PENDING")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_resource_revision_evidence(network_id,network_revision,project_id,network_name,network_profile,mtu,segment_policy,requested_segment_id,delete_protection,lifecycle_state,previous_revision,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),false,'RETIRE_PENDING',$9,$10)`, networkID, next, desired.ProjectID, desired.Name, desired.Profile, desired.MTU, desired.SegmentPolicy, desired.RequestedSegmentID, revision, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.networks_current SET network_revision=$2,network_generation=$2,lifecycle_state='RETIRE_PENDING',desired_digest=$3,updated_at=statement_timestamp() WHERE network_id=$1`, networkID, next, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_segment_allocations_current SET network_revision=$2,allocation_state='RELEASE_PENDING',updated_at=statement_timestamp() WHERE network_id=$1`, networkID, next); err != nil {
			return err
		}
		return insertNetworkRealizationOperationTx(ctx, tx, desired, next, uint64(revision), uint64(allocationGeneration), uint64(realizationGeneration+1), allocationID, segmentType, uint32(segmentID), "RETIRE", "ABSENT", 1)
	})
	if err != nil {
		return NetworkResource{}, err
	}
	return GetNetworkResource(ctx, db, networkID)
}

func lockNetworkRealizationClaim(ctx context.Context, tx pgx.Tx, claim NetworkRealizationClaim) error {
	if claim.OperationID == "" || claim.Owner == "" || claim.ClaimGeneration == 0 {
		return ErrPlacementConflict
	}
	if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
		return err
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_realization_operations_current WHERE operation_id=$1 AND operation_generation=$2 AND phase='CLAIMED' AND claim_owner=$3 AND claim_generation=$4 AND claim_expires_at>statement_timestamp())`, claim.OperationID, claim.OperationGeneration, claim.Owner, claim.ClaimGeneration).Scan(&ok); err != nil || !ok {
		return ErrPlacementConflict
	}
	return nil
}

func GetNetworkResource(ctx context.Context, db TxBeginner, networkID string) (NetworkResource, error) {
	var out NetworkResource
	var revision, allocationGeneration, realizationGeneration, segmentID, mtu int64
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT n.network_id,n.project_id,n.network_name,n.network_profile,n.segment_policy,n.lifecycle_state,n.network_revision,n.mtu,n.delete_protection,n.created_at,n.updated_at,COALESCE(a.allocation_id,''),COALESCE(a.allocation_generation,0),COALESCE(a.segment_type,''),COALESCE(a.segment_id,0),COALESCE(r.realization_generation,0),COALESCE(r.realization_state,''),COALESCE(r.operation_id,'') FROM kim.networks_current n LEFT JOIN kim.network_segment_allocations_current a USING(network_id) LEFT JOIN kim.network_realizations_current r USING(network_id) WHERE n.network_id=$1 AND n.authority_source='NETWORK_RESOURCE'`, networkID).Scan(&out.NetworkID, &out.ProjectID, &out.Name, &out.Profile, &out.SegmentPolicy, &out.Lifecycle, &revision, &mtu, &out.DeleteProtection, &out.CreatedAt, &out.UpdatedAt, &out.AllocationID, &allocationGeneration, &out.SegmentType, &segmentID, &realizationGeneration, &out.RealizationState, &out.OperationID)
	})
	out.Revision, out.MTU, out.AllocationGeneration, out.SegmentID, out.RealizationGeneration = uint64(revision), uint32(mtu), uint64(allocationGeneration), uint32(segmentID), uint64(realizationGeneration)
	return out, err
}

func digestNetworkResource(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
