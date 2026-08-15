package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"

	"github.com/jackc/pgx/v5"
	agentinventory "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/inventory"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

var (
	ErrPlacementIneligible = errors.New("placement candidate is ineligible")
	ErrPlacementStale      = errors.New("placement evaluation is stale")
	ErrPlacementConflict   = errors.New("placement admission conflict")
)

type PlacementPoolBinding struct {
	PoolID, LifecycleState, PolicyID string
	PoolGeneration, PolicyGeneration uint64
}

type HostPlacementMembership struct {
	HostID, PoolID, State string
	Generation            uint64
}

type PlacementAdmissionRequest struct {
	RequestID, ProjectID, WorkloadID, ImageID, FlavorID, PoolID        string
	PlacementScopeID, PlacementScopeDigest, VisibilityProvenanceDigest string
	PlacementScopeGeneration                                           uint64
	PCI                                                                []placement.PCIRequirement
	Network                                                            []placement.NetworkRequirement
	Storage                                                            []placement.StorageRequirement
}

type PlacementAdmission struct {
	AdmissionID, AllocationID, RequestID, RequestDigest string
	HostID, PoolID                                      string
	EvaluationDigest                                    string
	AvailabilityBinding                                 *VMAvailabilityBinding
}

func UpsertPlacementPool(ctx context.Context, db TxBeginner, pool PlacementPoolBinding) error {
	if pool.PoolID == "" || pool.PoolGeneration == 0 || pool.PolicyID == "" || pool.PolicyGeneration == 0 || (pool.LifecycleState != "ACTIVE" && pool.LifecycleState != "DRAINING" && pool.LifecycleState != "DISABLED") {
		return errors.New("complete Placement Pool binding is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, pool.PoolID); err != nil {
			return err
		}
		groupLifecycle := pool.LifecycleState
		if groupLifecycle == "DISABLED" {
			groupLifecycle = "DRAINING"
		}
		if err := upsertHostGroupAndPolicyTx(ctx, tx, HostGroupRevision{
			HostGroupID: pool.PoolID, Generation: pool.PoolGeneration,
			GroupType: "PLACEMENT_POOL", Dimension: "service-class", Level: "pool",
			LifecycleState: groupLifecycle,
		}); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.placement_pools_current (
				pool_id, pool_generation, lifecycle_state, policy_id, policy_generation
			) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (pool_id) DO UPDATE SET
				pool_generation=EXCLUDED.pool_generation,
				lifecycle_state=EXCLUDED.lifecycle_state,
				policy_id=EXCLUDED.policy_id,
				policy_generation=EXCLUDED.policy_generation,
				updated_at=statement_timestamp()
			WHERE kim.placement_pools_current.pool_generation < EXCLUDED.pool_generation
		`, pool.PoolID, pool.PoolGeneration, pool.LifecycleState, pool.PolicyID, pool.PolicyGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			var generation, policyGeneration uint64
			var lifecycle, policyID string
			if err := tx.QueryRow(ctx, `SELECT pool_generation,lifecycle_state,policy_id,policy_generation FROM kim.placement_pools_current WHERE pool_id=$1`, pool.PoolID).Scan(&generation, &lifecycle, &policyID, &policyGeneration); err != nil {
				return err
			}
			if generation != pool.PoolGeneration || lifecycle != pool.LifecycleState || policyID != pool.PolicyID || policyGeneration != pool.PolicyGeneration {
				return ErrPlacementConflict
			}
		}
		return nil
	})
}

func AssignHostPlacementPool(ctx context.Context, db TxBeginner, membership HostPlacementMembership) error {
	if membership.HostID == "" || membership.PoolID == "" || membership.Generation == 0 || (membership.State != "ACTIVE" && membership.State != "STALE" && membership.State != "BLOCKED") {
		return errors.New("complete Host Placement Pool membership is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostGroupTx(ctx, tx, membership.PoolID); err != nil {
			return err
		}
		if err := assignHostGroupMembershipTx(ctx, tx, HostGroupMembership{
			HostGroupID: membership.PoolID, HostID: membership.HostID,
			Generation: membership.Generation, State: membership.State,
			SourceType: "PLACEMENT_POOL_COMPAT", SourceRevision: fmt.Sprint(membership.Generation),
		}); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.host_placement_pool_memberships_current (
				host_id, pool_id, membership_generation, membership_state
			) VALUES ($1,$2,$3,$4)
			ON CONFLICT (host_id) DO UPDATE SET
				pool_id=EXCLUDED.pool_id,
				membership_generation=EXCLUDED.membership_generation,
				membership_state=EXCLUDED.membership_state,
				updated_at=statement_timestamp()
			WHERE kim.host_placement_pool_memberships_current.membership_generation < EXCLUDED.membership_generation
		`, membership.HostID, membership.PoolID, membership.Generation, membership.State)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			var poolID, state string
			var generation uint64
			if err := tx.QueryRow(ctx, `SELECT pool_id,membership_generation,membership_state FROM kim.host_placement_pool_memberships_current WHERE host_id=$1`, membership.HostID).Scan(&poolID, &generation, &state); err != nil {
				return err
			}
			if poolID != membership.PoolID || generation != membership.Generation || state != membership.State {
				return ErrPlacementConflict
			}
		}
		return nil
	})
}

// DryEvaluatePlacement uses a read-only repeatable-read transaction. It never
// writes a decision, reservation, Outbox intent, or backend side effect.
func DryEvaluatePlacement(ctx context.Context, db TxBeginner, request PlacementAdmissionRequest, hostID string) (placement.Evaluation, error) {
	if request.PlacementScopeID != "" || request.PlacementScopeGeneration != 0 || request.PlacementScopeDigest != "" || request.VisibilityProvenanceDigest != "" {
		return placement.Evaluation{}, ErrPlacementConflict
	}
	if err := validatePlacementAdmissionRequest(request, hostID); err != nil {
		return placement.Evaluation{}, err
	}
	request.PCI = normalizePlacementPCIRequirements(request.PCI)
	request.Network = normalizePlacementNetworkRequirements(request.Network)
	request.Storage = normalizePlacementStorageRequirements(request.Storage)
	var evaluation placement.Evaluation
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		evaluation, err = evaluatePlacementTx(ctx, tx, request, hostID)
		return err
	})
	return evaluation, err
}

// FinalAdmitPlacement repeats the dry rules over current authority and commits
// immutable decision plus compute reservation in one PostgreSQL transaction.
func FinalAdmitPlacement(ctx context.Context, db TxBeginner, request PlacementAdmissionRequest, dry placement.Evaluation) (PlacementAdmission, error) {
	if request.PlacementScopeID != "" || request.PlacementScopeGeneration != 0 || request.PlacementScopeDigest != "" || request.VisibilityProvenanceDigest != "" {
		return PlacementAdmission{}, ErrPlacementConflict
	}
	return finalAdmitPlacement(ctx, db, request, dry)
}

func finalAdmitPlacement(ctx context.Context, db TxBeginner, request PlacementAdmissionRequest, dry placement.Evaluation) (PlacementAdmission, error) {
	if err := validatePlacementAdmissionRequest(request, dry.HostID); err != nil {
		return PlacementAdmission{}, err
	}
	request.PCI = normalizePlacementPCIRequirements(request.PCI)
	request.Network = normalizePlacementNetworkRequirements(request.Network)
	request.Storage = normalizePlacementStorageRequirements(request.Storage)
	pciRequirementsPayload, pciRequirementsDigest, err := placementPCIRequirementsPayload(request.PCI)
	if err != nil {
		return PlacementAdmission{}, err
	}
	networkRequirementsPayload, networkRequirementsDigest, err := placementNetworkRequirementsPayload(request.Network)
	if err != nil {
		return PlacementAdmission{}, err
	}
	storageRequirementsPayload, storageRequirementsDigest, err := placementStorageRequirementsPayload(request.Storage)
	if err != nil {
		return PlacementAdmission{}, err
	}
	var admission PlacementAdmission
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "placement-request/"+request.RequestID); err != nil {
			return err
		}
		// Serialize Final Admission with a Host evacuation drain. This closes
		// the race where a dry result was produced before the drain snapshot but
		// would otherwise commit after the immutable workload set was recorded.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "host-placement/"+dry.HostID); err != nil {
			return err
		}
		var existing PlacementAdmission
		var existingProjectID, existingWorkloadID, existingImageID, existingFlavorID, existingPCIDigest, existingNetworkDigest, existingStorageDigest string
		var existingScopeID, existingScopeDigest, existingVisibilityDigest string
		var existingScopeGeneration uint64
		err := tx.QueryRow(ctx, `
			SELECT decision.admission_id, claim.allocation_id, decision.request_id,
			       decision.request_digest, decision.host_id, decision.pool_id,
			       decision.evaluation_digest, decision.project_id, decision.workload_id,
			       decision.image_id, decision.flavor_id, decision.pci_requirements_digest,
			       decision.network_requirements_digest, decision.storage_requirements_digest,
			       COALESCE(decision.placement_scope_id,''),COALESCE(decision.placement_scope_generation,0),
			       COALESCE(decision.placement_scope_digest,''),COALESCE(decision.visibility_provenance_digest,'')
			FROM kim.placement_admission_decisions decision
			JOIN kim.compute_allocation_claims claim ON claim.admission_id=decision.admission_id
			WHERE decision.request_id=$1
		`, request.RequestID).Scan(&existing.AdmissionID, &existing.AllocationID, &existing.RequestID,
			&existing.RequestDigest, &existing.HostID, &existing.PoolID, &existing.EvaluationDigest,
			&existingProjectID, &existingWorkloadID, &existingImageID, &existingFlavorID,
			&existingPCIDigest, &existingNetworkDigest, &existingStorageDigest, &existingScopeID,
			&existingScopeGeneration, &existingScopeDigest, &existingVisibilityDigest)
		if err == nil {
			if existing.RequestDigest != dry.RequestDigest || existingProjectID != request.ProjectID || existingWorkloadID != request.WorkloadID || existingImageID != request.ImageID || existingFlavorID != request.FlavorID || existing.PoolID != request.PoolID || existingPCIDigest != pciRequirementsDigest || existingNetworkDigest != networkRequirementsDigest || existingStorageDigest != storageRequirementsDigest ||
				existingScopeID != request.PlacementScopeID || existingScopeGeneration != request.PlacementScopeGeneration ||
				existingScopeDigest != request.PlacementScopeDigest || existingVisibilityDigest != request.VisibilityProvenanceDigest {
				return ErrPlacementConflict
			}
			admission = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		for _, lockKey := range []string{"image/" + request.ImageID, "flavor/" + request.FlavorID, "host-group/" + request.PoolID} {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
				return err
			}
		}
		if err := lockPlacementCatalogRows(ctx, tx, request); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, dry.HostID); err != nil {
			return err
		}
		if err := lockNetworkRequirementKeys(ctx, tx, request.Network); err != nil {
			return err
		}
		if err := lockNetworkAuthorityRows(ctx, tx, dry.HostID, request.Network); err != nil {
			return err
		}
		if err := lockStorageRequirementKeys(ctx, tx, request.Storage); err != nil {
			return err
		}
		if err := lockStorageAuthorityRows(ctx, tx, dry.HostID, request.Storage); err != nil {
			return err
		}
		var membershipSetCurrent bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM kim.host_groups_current group_current
				JOIN kim.host_group_membership_sets_current set_current USING (host_group_id)
				JOIN kim.host_group_cardinality_policies_current cardinality_policy
				  ON cardinality_policy.group_type=group_current.group_type
				 AND cardinality_policy.dimension=group_current.dimension
				 AND cardinality_policy.level=group_current.level
				 AND cardinality_policy.scope_type='SYSTEM' AND cardinality_policy.scope_id='system'
				LEFT JOIN kim.host_group_hierarchy_sets_current hierarchy_current
				  ON hierarchy_current.group_type=group_current.group_type
				 AND hierarchy_current.dimension=group_current.dimension
				 AND hierarchy_current.scope_type='SYSTEM' AND hierarchy_current.scope_id='system'
				LEFT JOIN kim.host_group_selectors_current selector_current
				  ON selector_current.host_group_id=group_current.host_group_id
				JOIN kim.host_group_memberships_current member
				  ON member.host_group_id=group_current.host_group_id
				WHERE group_current.host_group_id=$1 AND member.host_id=$2
				  AND group_current.host_group_generation=$3
				  AND set_current.membership_set_generation=$4
				  AND member.membership_set_generation=$4 AND member.membership_generation=$5
				  AND COALESCE(set_current.hierarchy_id,'')=$6
				  AND COALESCE(set_current.hierarchy_generation,0)=$7
				  AND group_current.lifecycle_state='ACTIVE' AND set_current.validation_state='ACCEPTED'
				  AND (
				    (selector_current.selector_id IS NULL AND set_current.selector_id IS NULL)
				    OR (
				      selector_current.selector_generation=set_current.selector_generation
				      AND selector_current.host_group_id=group_current.host_group_id
				      AND selector_current.based_on_host_group_generation=group_current.host_group_generation
				      AND selector_current.lifecycle_state='ACTIVE'
				    )
				  )
				  AND cardinality_policy.policy_state='ACTIVE'
				  AND ((set_current.cardinality_policy_id=cardinality_policy.cardinality_policy_id
				        AND set_current.cardinality_policy_generation=cardinality_policy.policy_generation
				        AND set_current.cardinality=cardinality_policy.cardinality)
				       OR (set_current.cardinality_policy_id IS NULL
				           AND cardinality_policy.policy_generation=1 AND cardinality_policy.cardinality='MANY'))
				  AND (
				    (hierarchy_current.hierarchy_id IS NULL AND set_current.hierarchy_id IS NULL)
				    OR (set_current.hierarchy_id=hierarchy_current.hierarchy_id
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
				          SELECT 1 FROM kim.host_group_hierarchy_node_evidence graph_node
				          JOIN kim.host_groups_current graph_group ON graph_group.host_group_id=graph_node.host_group_id
				          WHERE graph_node.hierarchy_id=hierarchy_current.hierarchy_id
				            AND graph_node.hierarchy_generation=hierarchy_current.hierarchy_generation
				            AND (graph_node.host_group_generation<>graph_group.host_group_generation
				                 OR graph_node.level<>graph_group.level OR graph_group.lifecycle_state<>'ACTIVE')
				        ))
				  )
				  AND member.membership_state='ACTIVE'
			)
		`, request.PoolID, dry.HostID, dry.PoolGeneration, dry.MembershipSetGeneration,
			dry.MembershipGeneration, dry.HierarchyID, dry.HierarchyGeneration).Scan(&membershipSetCurrent); err != nil {
			return fmt.Errorf("revalidate HostGroup membership Set authority: %w", err)
		}
		if !membershipSetCurrent {
			return fmt.Errorf("HostGroup membership Set authority changed: %w", ErrPlacementStale)
		}
		current, err := evaluatePlacementTx(ctx, tx, request, dry.HostID)
		if err != nil {
			return err
		}
		if !current.Eligible {
			return ErrPlacementIneligible
		}
		if current.RequestDigest != dry.RequestDigest || current.EvaluationDigest != dry.EvaluationDigest {
			return fmt.Errorf("placement evaluation authority changed (dry request=%s evaluation=%s, current request=%s evaluation=%s): %w", dry.RequestDigest, dry.EvaluationDigest, current.RequestDigest, current.EvaluationDigest, ErrPlacementStale)
		}
		explanation, err := json.Marshal(map[string]any{"eligible": true, "score": current.Score, "reason_codes": current.ReasonCodes})
		if err != nil {
			return err
		}
		admission = PlacementAdmission{
			AdmissionID: "admission:" + request.RequestID, AllocationID: "allocation:" + request.RequestID,
			RequestID: request.RequestID, RequestDigest: current.RequestDigest,
			HostID: current.HostID, PoolID: current.PoolID, EvaluationDigest: current.EvaluationDigest,
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.placement_admission_decisions (
				admission_id, request_id, request_digest, evaluation_digest,
				project_id, workload_id, host_id, pool_id, pool_generation,
				pool_policy_id, pool_policy_generation, membership_set_generation, membership_generation,
				hierarchy_id,hierarchy_generation,
				image_id, image_revision, flavor_id,
				flavor_revision, flavor_shape_digest, capability_generation,
				baseline_assignment_generation, preflight_generation,
				compliance_generation, pci_requirements, pci_requirements_digest,
				network_requirements, network_requirements_digest,
				storage_requirements, storage_requirements_digest,
				placement_scope_id,placement_scope_generation,placement_scope_digest,visibility_provenance_digest,
				decision_state, explanation
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,'ACCEPTED',$35)
		`, admission.AdmissionID, request.RequestID, current.RequestDigest, current.EvaluationDigest,
			request.ProjectID, request.WorkloadID, current.HostID, current.PoolID,
			current.PoolGeneration, current.PoolPolicyID, current.PoolPolicyGeneration, current.MembershipSetGeneration,
			current.MembershipGeneration, nullablePlacementHierarchyID(current), nullablePlacementHierarchyGeneration(current),
			current.ImageID, current.ImageRevision,
			current.FlavorID, current.FlavorRevision, current.FlavorShapeDigest,
			current.CapabilityGeneration, current.BaselineAssignmentGeneration,
			current.PreflightGeneration, current.ComplianceGeneration,
			pciRequirementsPayload, pciRequirementsDigest,
			networkRequirementsPayload, networkRequirementsDigest,
			storageRequirementsPayload, storageRequirementsDigest,
			nullablePlacementScopeID(request), nullablePlacementScopeGeneration(request),
			nullablePlacementScopeDigest(request), nullablePlacementVisibilityDigest(request), explanation)
		if err != nil {
			return fmt.Errorf("record Placement admission decision: %w", err)
		}
		for _, required := range request.PCI {
			claimRequest := PCIVFClaimRequest{
				ClaimID:              "pci:" + request.RequestID + ":" + required.DeviceAddress,
				PlacementAdmissionID: admission.AdmissionID,
				HostID:               current.HostID, DeviceAddress: required.DeviceAddress,
				ProjectID: request.ProjectID, WorkloadID: request.WorkloadID,
				PolicyID: required.PolicyID, PolicyGeneration: required.PolicyGeneration,
				HostCapabilityGeneration: current.CapabilityGeneration,
				QualificationID:          required.QualificationID, QualificationRevision: required.QualificationRevision,
				RequiredNUMANodeID: required.RequiredNUMANodeID, RequiredIOMMUGroup: required.RequiredIOMMUGroup,
			}
			if err := claimQualifiedVFTx(ctx, tx, claimRequest); err != nil {
				return err
			}
		}
		for _, required := range request.Network {
			if err := claimNetworkPortTx(ctx, tx, admission.AdmissionID, request, current, required); err != nil {
				return err
			}
		}
		// Final Admission binds an SR-IOV allocation claim to the exact Port
		// incarnation created in this transaction. Non-Network PCI claims remain
		// unbound and cannot be consumed by VF retirement/handoff authority.
		for _, required := range request.PCI {
			if _, err := tx.Exec(ctx, `UPDATE kim.pci_vf_allocation_claims claim SET
				port_id=port.port_id,port_generation=port.port_generation,binding_generation=binding.binding_generation
				FROM kim.network_ports_current port JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id
				WHERE claim.claim_id=$1 AND claim.placement_admission_id=$2
				 AND port.placement_admission_id=$2 AND binding.placement_admission_id=$2
				 AND binding.binding_type='SRIOV_DIRECT' AND binding.device_address=claim.device_address`,
				"pci:"+request.RequestID+":"+required.DeviceAddress, admission.AdmissionID); err != nil {
				return err
			}
			if required.HandoffID != "" {
				claimID := "pci:" + request.RequestID + ":" + required.DeviceAddress
				var portID, device string
				var portGeneration, allocationGeneration uint64
				if err := tx.QueryRow(ctx, `SELECT coalesce(port_id,''),coalesce(port_generation,0),device_address,allocation_generation FROM kim.pci_vf_allocation_claims WHERE claim_id=$1`, claimID).Scan(&portID, &portGeneration, &device, &allocationGeneration); err != nil || portID == "" {
					return ErrPlacementStale
				}
				if err := commitPCIVFHandoffTx(ctx, tx, PCIVFHandoffRequest{HandoffID: required.HandoffID, WorkloadID: request.WorkloadID, PortID: portID, PortGeneration: portGeneration, SourceClaimID: required.SourceClaimID, SourceAllocationGeneration: required.SourceAllocationGeneration, SourceHostID: required.SourceHostID, SourceDeviceAddress: required.SourceDeviceAddress, SourceRetirementEvidenceID: required.SourceRetirementEvidenceID, DestinationClaimID: claimID, DestinationAllocationGeneration: allocationGeneration, DestinationHostID: current.HostID, DestinationDeviceAddress: device, DestinationAdmissionID: admission.AdmissionID}); err != nil {
					return err
				}
			}
		}
		for _, required := range request.Storage {
			if err := claimLocalLVMStorageTx(ctx, tx, admission.AdmissionID, request, current, required); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.compute_allocation_claims (
				allocation_id, admission_id, request_id, request_digest, project_id,
				workload_id, host_id, pool_id, vcpus, memory_mib,
				hugepage_size_kib, hugepage_pages, cpu_allocation, claim_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'RESERVED')
		`, admission.AllocationID, admission.AdmissionID, request.RequestID, current.RequestDigest,
			request.ProjectID, request.WorkloadID, current.HostID, current.PoolID,
			current.RequiredClaim.VCPUs, current.RequiredClaim.MemoryMiB,
			current.RequiredClaim.HugePageSizeKiB, current.RequiredClaim.HugePagePages,
			current.RequiredClaim.CPUAllocation)
		return err
	})
	if err != nil {
		return PlacementAdmission{}, fmt.Errorf("final Placement admission: %w", err)
	}
	return admission, nil
}

func evaluatePlacementTx(ctx context.Context, row QueryRower, request PlacementAdmissionRequest, hostID string) (placement.Evaluation, error) {
	shape, err := LoadCurrentPlacementShape(ctx, row, request.FlavorID)
	if err != nil {
		return placement.Evaluation{}, err
	}
	var imageRevision uint64
	var imageOwner, imageVisibility, flavorOwner string
	if err := row.QueryRow(ctx, `
		SELECT current.image_revision, current.owner_project_id, evidence.visibility
		FROM kim.images_current current
		JOIN kim.image_revision_evidence evidence
		  ON evidence.image_id=current.image_id AND evidence.image_revision=current.image_revision
		WHERE current.image_id=$1 AND current.lifecycle_state='ACTIVE'
		  AND evidence.validation_state='VERIFIED'
	`, request.ImageID).Scan(&imageRevision, &imageOwner, &imageVisibility); err != nil {
		return placement.Evaluation{}, err
	}
	if err := row.QueryRow(ctx, `SELECT owner_project_id FROM kim.flavors_current WHERE flavor_id=$1 AND lifecycle_state='ACTIVE'`, request.FlavorID).Scan(&flavorOwner); err != nil {
		return placement.Evaluation{}, err
	}
	catalogAccessAllowed := flavorOwner == request.ProjectID && (imageOwner == request.ProjectID || imageVisibility == "PUBLIC")
	evaluationRequest := placement.Request{
		RequestID: request.RequestID, ProjectID: request.ProjectID, WorkloadID: request.WorkloadID,
		ImageID: request.ImageID, ImageRevision: imageRevision, FlavorID: request.FlavorID,
		FlavorRevision: shape.FlavorRevision, FlavorShapeDigest: shape.ShapeDigest, PoolID: request.PoolID,
		VCPUs: shape.VCPUs, MemoryMiB: shape.MemoryMiB, RootDiskGiB: shape.RootDiskGiB,
		NUMAPolicy: shape.NUMAPolicy, NUMANodes: shape.NUMANodes, HugePageSizeKiB: shape.HugePageSizeKiB,
		CPUAllocation: shape.CPUAllocation, CPUPinning: shape.CPUPinning, ExtraSpecs: shape.ExtraSpecs,
		CatalogAccessAllowed: catalogAccessAllowed,
		PCI:                  request.PCI,
		Network:              request.Network,
		Storage:              request.Storage,
	}
	var authority placement.AuthoritySnapshot
	var snapshotPayload []byte
	err = row.QueryRow(ctx, `
		SELECT database.mode, capability.host_id, membership.host_group_id,
		       host_group.host_group_generation, pool.policy_id, pool.policy_generation,
		       membership_set.membership_set_generation,
		       membership.membership_generation,
		       COALESCE(membership_set.hierarchy_id,''),
		       COALESCE(membership_set.hierarchy_generation,0),
		       host_group.lifecycle_state, membership.membership_state,
		       capability.observation_generation, capability.projection_state,
		       gates.gate_state, gates.preflight_state, gates.compliance_state,
		       gates.capability_generation, gates.baseline_assignment_generation, gates.preflight_generation,
		       gates.compliance_generation, snapshot.snapshot_payload,
		       COALESCE(claims.vcpus,0), COALESCE(claims.memory_mib,0)
		FROM kim.database_authority database
		JOIN kim.host_capability_projections capability ON true
		JOIN kim.host_inventory_snapshots snapshot
		  ON snapshot.host_id=capability.host_id
		 AND snapshot.observation_generation=capability.observation_generation
		JOIN kim.host_readiness_gates_current gates ON gates.host_id=capability.host_id
		JOIN kim.host_group_memberships_current membership
		  ON membership.host_id=capability.host_id AND membership.host_group_id=$2
		JOIN kim.host_groups_current host_group
		  ON host_group.host_group_id=membership.host_group_id
		 AND host_group.group_type='PLACEMENT_POOL'
		JOIN kim.host_group_membership_sets_current membership_set
		  ON membership_set.host_group_id=membership.host_group_id
		 AND membership_set.membership_set_generation=membership.membership_set_generation
		 AND membership_set.based_on_host_group_generation=host_group.host_group_generation
		 AND membership_set.validation_state='ACCEPTED'
		JOIN kim.host_group_cardinality_policies_current cardinality_policy
		  ON cardinality_policy.group_type=host_group.group_type
		 AND cardinality_policy.dimension=host_group.dimension
		 AND cardinality_policy.level=host_group.level
		 AND cardinality_policy.scope_type='SYSTEM' AND cardinality_policy.scope_id='system'
		 AND cardinality_policy.policy_state='ACTIVE'
		 AND ((membership_set.cardinality_policy_id=cardinality_policy.cardinality_policy_id
		       AND membership_set.cardinality_policy_generation=cardinality_policy.policy_generation
		       AND membership_set.cardinality=cardinality_policy.cardinality)
		      OR (membership_set.cardinality_policy_id IS NULL
		          AND cardinality_policy.policy_generation=1 AND cardinality_policy.cardinality='MANY'))
		LEFT JOIN kim.host_group_hierarchy_sets_current hierarchy_current
		  ON hierarchy_current.group_type=host_group.group_type
		 AND hierarchy_current.dimension=host_group.dimension
		 AND hierarchy_current.scope_type='SYSTEM' AND hierarchy_current.scope_id='system'
		LEFT JOIN kim.host_group_selectors_current selector_current
		  ON selector_current.host_group_id=host_group.host_group_id
		JOIN kim.placement_pools_current pool ON pool.pool_id=host_group.host_group_id
		LEFT JOIN LATERAL (
			SELECT sum(vcpus) vcpus, sum(memory_mib) memory_mib
			FROM kim.compute_allocation_claims
			WHERE host_id=capability.host_id
			  AND claim_state IN ('RESERVED','ALLOCATED','RELEASE_PENDING')
		) claims ON true
		WHERE database.singleton AND capability.host_id=$1
		  AND NOT EXISTS (
		    SELECT 1 FROM kim.host_placement_drains_current drain
		    WHERE drain.source_host_id=capability.host_id
		      AND drain.drain_state IN ('DRAINING','DRAINED')
		  )
		  AND (
		    (selector_current.selector_id IS NULL AND membership_set.selector_id IS NULL)
		    OR (
		      selector_current.selector_generation=membership_set.selector_generation
		      AND selector_current.host_group_id=host_group.host_group_id
		      AND selector_current.based_on_host_group_generation=host_group.host_group_generation
		      AND selector_current.lifecycle_state='ACTIVE'
		    )
		  )
		  AND (
		    (hierarchy_current.hierarchy_id IS NULL AND membership_set.hierarchy_id IS NULL)
		    OR (membership_set.hierarchy_id=hierarchy_current.hierarchy_id
		        AND membership_set.hierarchy_generation=hierarchy_current.hierarchy_generation
		        AND EXISTS (
		          SELECT 1 FROM kim.host_group_hierarchy_node_evidence node
		          WHERE node.hierarchy_id=hierarchy_current.hierarchy_id
		            AND node.hierarchy_generation=hierarchy_current.hierarchy_generation
		            AND node.host_group_id=host_group.host_group_id
		            AND node.host_group_generation=host_group.host_group_generation
		            AND node.level=host_group.level
		        )
		        AND NOT EXISTS (
		          SELECT 1 FROM kim.host_group_hierarchy_node_evidence graph_node
		          JOIN kim.host_groups_current graph_group ON graph_group.host_group_id=graph_node.host_group_id
		          WHERE graph_node.hierarchy_id=hierarchy_current.hierarchy_id
		            AND graph_node.hierarchy_generation=hierarchy_current.hierarchy_generation
		            AND (graph_node.host_group_generation<>graph_group.host_group_generation
		                 OR graph_node.level<>graph_group.level OR graph_group.lifecycle_state<>'ACTIVE')
		        ))
		  )
	`, hostID, request.PoolID).Scan(&authority.DatabaseMode, &authority.HostID, &authority.PoolID,
		&authority.PoolGeneration, &authority.PoolPolicyID, &authority.PoolPolicyGeneration,
		&authority.MembershipSetGeneration, &authority.MembershipGeneration,
		&authority.HierarchyID, &authority.HierarchyGeneration, &authority.PoolState,
		&authority.MembershipState, &authority.CapabilityGeneration, &authority.CapabilityState,
		&authority.ReadinessState, &authority.PreflightState, &authority.ComplianceState,
		&authority.ReadinessCapabilityGeneration, &authority.BaselineAssignmentGeneration, &authority.PreflightGeneration,
		&authority.ComplianceGeneration, &snapshotPayload, &authority.ClaimedVCPUs,
		&authority.ClaimedMemoryMiB)
	if errors.Is(err, pgx.ErrNoRows) {
		return placement.Evaluate(evaluationRequest, placement.AuthoritySnapshot{HostID: hostID, ClaimedHugePages: map[uint64]uint64{}})
	}
	if err != nil {
		return placement.Evaluation{}, err
	}
	authority.ClaimedHugePages = map[uint64]uint64{}
	authority.PCIDevices = map[string]placement.PCIDeviceAuthority{}
	authority.Networks = map[string]placement.NetworkAuthority{}
	authority.Storage = map[string]placement.StorageAuthority{}
	rows, err := queryHugePageClaims(ctx, row, hostID)
	if err != nil {
		return placement.Evaluation{}, err
	}
	for size, pages := range rows {
		authority.ClaimedHugePages[size] = pages
	}
	for _, required := range request.PCI {
		device, found, err := loadPCIDeviceAuthority(ctx, row, hostID, required)
		if err != nil {
			return placement.Evaluation{}, err
		}
		if found {
			authority.PCIDevices[required.DeviceAddress] = device
		}
	}
	for _, required := range request.Network {
		network, found, err := loadNetworkAuthority(ctx, row, request.ProjectID, request.WorkloadID, hostID, required)
		if err != nil {
			return placement.Evaluation{}, err
		}
		if found {
			authority.Networks[required.PortID] = network
		}
	}
	for _, required := range request.Storage {
		storage, found, err := loadStorageAuthority(ctx, row, request.ProjectID, request.WorkloadID, hostID, required)
		if err != nil {
			return placement.Evaluation{}, err
		}
		if found {
			authority.Storage[required.VolumeID] = storage
		}
	}
	authority.Inventory, err = agentinventory.DecodeSnapshot(snapshotPayload)
	if err != nil {
		return placement.Evaluation{}, err
	}
	return placement.Evaluate(evaluationRequest, authority)
}

func nullablePlacementHierarchyID(evaluation placement.Evaluation) any {
	if evaluation.HierarchyGeneration == 0 {
		return nil
	}
	return evaluation.HierarchyID
}

func nullablePlacementHierarchyGeneration(evaluation placement.Evaluation) any {
	if evaluation.HierarchyGeneration == 0 {
		return nil
	}
	return evaluation.HierarchyGeneration
}

func loadPCIDeviceAuthority(ctx context.Context, row QueryRower, hostID string, required placement.PCIRequirement) (placement.PCIDeviceAuthority, bool, error) {
	var device placement.PCIDeviceAuthority
	err := row.QueryRow(ctx, `
		SELECT p.device_address, p.observation_generation, p.observation_state,
		       p.relationship_state, COALESCE(p.pf_address,''), COALESCE(p.vf_index,-1),
		       p.numa_node_id, COALESCE(p.iommu_group,''),
		       COALESCE(b.qualification_id,''), COALESCE(b.qualification_revision,0),
		       COALESCE(b.binding_state,''), COALESCE(b.observed_generation,0),
		       COALESCE(b.qualification_profile_revision,''),
		       COALESCE(a.policy_id,''), COALESCE(a.policy_generation,0),
		       COALESCE(a.policy_state,''), COALESCE(a.qualification_profile_revision,''),
		       COALESCE('VF_ASSIGN' = ANY(e.validated_operations),false),
		       EXISTS (
		           SELECT 1 FROM kim.pci_vf_allocation_claims claim
		           WHERE claim.host_id=p.host_id AND claim.device_address=p.device_address
		             AND claim.claim_state IN ('ACTIVE','RELEASE_PENDING')
		       )
		FROM kim.host_pci_device_projections p
		LEFT JOIN kim.pci_qualification_bindings_current b
		  ON b.host_id=p.host_id AND b.device_address=p.device_address
		LEFT JOIN kim.pci_qualification_evidence e
		  ON e.qualification_id=b.qualification_id AND e.qualification_revision=b.qualification_revision
		LEFT JOIN kim.pci_allocation_policy_bindings a
		  ON a.host_id=p.host_id AND a.policy_id=$3
		WHERE p.host_id=$1 AND p.device_address=$2
	`, hostID, required.DeviceAddress, required.PolicyID).Scan(
		&device.DeviceAddress, &device.ObservationGeneration, &device.ObservationState,
		&device.RelationshipState, &device.PFAddress, &device.VFIndex,
		&device.NUMANodeID, &device.IOMMUGroup,
		&device.QualificationID, &device.QualificationRevision,
		&device.BindingState, &device.BindingGeneration, &device.BindingProfile,
		&device.PolicyID, &device.PolicyGeneration, &device.PolicyState, &device.PolicyProfile,
		&device.AssignmentQualified, &device.ActiveClaim,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return placement.PCIDeviceAuthority{}, false, nil
	}
	if err != nil {
		return placement.PCIDeviceAuthority{}, false, err
	}
	return device, true, nil
}

func loadNetworkAuthority(ctx context.Context, row QueryRower, projectID, workloadID, hostID string, required placement.NetworkRequirement) (placement.NetworkAuthority, bool, error) {
	var authority placement.NetworkAuthority
	allocationSource := required.AllocationSource
	if allocationSource == "" {
		allocationSource = "EXPLICIT"
	}
	err := row.QueryRow(ctx, `
		SELECT $3, network.network_id, network.project_id, network.network_generation,
		       network.lifecycle_state, network.mtu,
		       subnet.subnet_id, subnet.subnet_generation, subnet.lifecycle_state,
		       segment.segment_claim_id, segment.segment_generation, segment.claim_state,
		       mapping.mapping_generation, mapping.mapping_state, mapping.maximum_mtu,
		       CASE WHEN EXISTS(SELECT 1 FROM kim.network_ports_current rp WHERE rp.port_id=$3 AND rp.authority_source='PORT_RESOURCE') THEN
		           EXISTS(SELECT 1 FROM kim.subnet_ip_allocations_current a WHERE a.owner_resource_type='PORT' AND a.owner_resource_id=$3 AND a.subnet_id=$6 AND a.assigned_address=NULLIF($8,'')::inet AND a.allocation_state='ALLOCATED')
		       WHEN $11='AUTOMATIC' THEN
		           family(subnet.cidr)=4
		           AND (subnet.allocation_end - subnet.allocation_start) < $12
		           AND EXISTS (
		               SELECT 1
		               FROM generate_series(0::bigint, subnet.allocation_end - subnet.allocation_start) candidate_offset
		               WHERE NOT ((subnet.allocation_start + candidate_offset) = ANY(subnet.excluded_addresses))
			         AND NOT EXISTS (
			             SELECT 1 FROM kim.network_identity_claims automatic_claim
		                     WHERE automatic_claim.claim_type='IP'
		                       AND automatic_claim.subnet_id=subnet.subnet_id
		                       AND automatic_claim.ip_address=(subnet.allocation_start + candidate_offset)
			               AND automatic_claim.claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','QUARANTINED')
			         )
			         AND NOT EXISTS (
			             SELECT 1 FROM kim.subnet_ip_allocations_current allocation
			             WHERE allocation.subnet_id=subnet.subnet_id
			               AND allocation.assigned_address=(subnet.allocation_start + candidate_offset)
			               AND allocation.allocation_state IN ('ALLOCATED','RELEASE_PENDING')
			         )
		           )
		       ELSE
		           NULLIF($8,'')::inet <<= subnet.cidr
		           AND NULLIF($8,'')::inet >= subnet.allocation_start
		           AND NULLIF($8,'')::inet <= subnet.allocation_end
		           AND NOT (NULLIF($8,'')::inet = ANY(subnet.excluded_addresses))
		       END,
		       CASE WHEN EXISTS(SELECT 1 FROM kim.network_ports_current rp WHERE rp.port_id=$3 AND rp.authority_source='PORT_RESOURCE') THEN EXISTS (
		           SELECT 1 FROM kim.network_identity_claims identity WHERE identity.port_id<>$3 AND identity.claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','QUARANTINED') AND ((identity.claim_type='IP' AND identity.subnet_id=$6 AND identity.ip_address=NULLIF($8,'')::inet) OR(identity.claim_type='MAC' AND identity.network_id=$5 AND identity.mac_address=NULLIF($9,'')::macaddr))
		           UNION ALL SELECT 1 FROM kim.subnet_ip_allocations_current a WHERE a.owner_resource_id<>$3 AND a.subnet_id=$6 AND a.assigned_address=NULLIF($8,'')::inet AND a.allocation_state IN ('ALLOCATED','RELEASE_PENDING')
		           UNION ALL SELECT 1 FROM kim.port_mac_allocations_current m WHERE m.port_id<>$3 AND m.network_id=$5 AND m.assigned_mac=NULLIF($9,'')::macaddr AND m.allocation_state IN ('ALLOCATED','RELEASE_PENDING')
		       ) WHEN $11='AUTOMATIC' THEN false ELSE EXISTS (
		           SELECT 1 FROM kim.network_identity_claims identity
		           WHERE identity.claim_state IN ('RESERVED','ACTIVE','RELEASE_PENDING','QUARANTINED')
		             AND ((identity.claim_type='IP' AND identity.subnet_id=subnet.subnet_id AND identity.ip_address=NULLIF($8,'')::inet)
		               OR (identity.claim_type='MAC' AND identity.network_id=network.network_id AND identity.mac_address=NULLIF($9,'')::macaddr))
		             AND ($13='' OR identity.port_id<>$3)
		           UNION ALL
		           SELECT 1 FROM kim.subnet_ip_allocations_current allocation
		           WHERE allocation.subnet_id=subnet.subnet_id AND allocation.assigned_address=NULLIF($8,'')::inet
		             AND allocation.allocation_state IN ('ALLOCATED','RELEASE_PENDING')
		       ) END,
		       EXISTS (SELECT 1 FROM kim.network_ports_current port WHERE port.port_id=$3 AND $13='' AND NOT(
		           port.authority_source='PORT_RESOURCE' AND port.port_revision=$21 AND port.project_id=$1 AND port.network_id=$5 AND port.subnet_id=$6 AND port.workload_id=$4 AND port.attachment_state='ATTACHMENT_REQUESTED'
		           AND EXISTS(SELECT 1 FROM kim.port_attachment_intents_current ai WHERE ai.port_id=port.port_id AND ai.port_revision=port.port_revision AND ai.attachment_intent_id=$22 AND ai.workload_id=$4 AND ai.intent_state='REQUESTED')
		           AND EXISTS(SELECT 1 FROM kim.port_mac_allocations_current m WHERE m.port_id=port.port_id AND m.assigned_mac=NULLIF($9,'')::macaddr AND m.allocation_state='ALLOCATED')
		           AND EXISTS(SELECT 1 FROM kim.subnet_ip_allocations_current i WHERE i.owner_resource_id=port.port_id AND i.assigned_address=NULLIF($8,'')::inet AND i.allocation_state='ALLOCATED')
		       )),
		       ($10 = ANY(mapping.supported_binding_types)),
		       CASE WHEN $13='' THEN true ELSE EXISTS (
		           SELECT 1 FROM kim.network_ports_current port
		           JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id
		           JOIN kim.network_port_source_quiescence_evidence quiescence
		             ON quiescence.evidence_id=$14 AND quiescence.evidence_digest=$15
		            AND quiescence.port_id=port.port_id AND quiescence.port_generation=$16
		            AND quiescence.source_host_id=$17 AND quiescence.source_binding_generation=$18
		            AND quiescence.quiescence_state='QUIESCED'
		           JOIN kim.network_port_binding_retirement_evidence retirement ON retirement.evidence_id=quiescence.retirement_evidence_id
		            AND retirement.port_id=port.port_id AND retirement.port_generation=port.port_generation
		            AND retirement.binding_generation=$18 AND retirement.source_host_id=$17 AND retirement.retirement_state='VERIFIED'
		           WHERE port.port_id=$3 AND port.project_id=$1 AND port.workload_id=$4
		             AND port.port_generation=$16 AND binding.host_id=$17 AND binding.binding_generation=$18
		             AND binding.binding_type=$10 AND binding.binding_state IN ('RESERVED','BINDING','VERIFYING','ACTIVE','UNKNOWN')
		             AND $19=$16+1 AND $20=$18+1 AND $17<>$2
		             AND NOT EXISTS (SELECT 1 FROM kim.port_binding_handoff_evidence h WHERE h.handoff_id=$13)
		             AND EXISTS (SELECT 1 FROM kim.network_identity_claims ip WHERE ip.port_id=port.port_id AND ip.claim_type='IP' AND ip.claim_state IN ('RESERVED','ACTIVE') AND ip.ip_address=NULLIF($8,'')::inet)
		             AND EXISTS (SELECT 1 FROM kim.network_identity_claims mac WHERE mac.port_id=port.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE') AND mac.mac_address=NULLIF($9,'')::macaddr)
		       ) END
		FROM kim.networks_current network
		JOIN kim.network_subnets_current subnet
		  ON subnet.network_id=network.network_id AND subnet.subnet_id=$6
		JOIN kim.network_segment_claims_current segment
		  ON segment.network_id=network.network_id AND segment.segment_claim_id=$7
		JOIN kim.host_network_mappings_current mapping
		  ON mapping.host_id=$2 AND mapping.segment_claim_id=segment.segment_claim_id
		WHERE network.network_id=$5 AND network.project_id=$1
		  AND (network.authority_source='LEGACY_FOUNDATION' OR EXISTS(
		    SELECT 1 FROM kim.network_realizations_current realization
		    WHERE realization.network_id=network.network_id AND realization.network_revision=network.network_revision
		      AND realization.realization_state='VERIFIED' AND realization.terminal_evidence_id IS NOT NULL))
		  AND (subnet.authority_source='LEGACY_FOUNDATION' OR EXISTS(
		    SELECT 1 FROM kim.subnet_realizations_current realization
		    WHERE realization.subnet_id=subnet.subnet_id AND realization.subnet_revision=subnet.subnet_revision
		      AND realization.network_revision=network.network_revision
		      AND realization.realization_state='VERIFIED' AND realization.terminal_evidence_id IS NOT NULL))
	`, projectID, hostID, required.PortID, workloadID, required.NetworkID, required.SubnetID,
		required.SegmentClaimID, required.IPAddress, required.MACAddress,
		required.BindingType, allocationSource, maximumAutomaticIPv4PoolSize, required.HandoffID,
		required.SourceQuiescenceEvidenceID, required.SourceQuiescenceEvidenceDigest,
		required.SourcePortGeneration, required.SourceHostID, required.SourceBindingGeneration,
		required.DestinationPortGeneration, required.DestinationBindingGeneration,
		required.PortRevision, required.AttachmentIntentID).Scan(
		&authority.PortID, &authority.NetworkID, &authority.NetworkProjectID,
		&authority.NetworkGeneration, &authority.NetworkState, &authority.NetworkMTU,
		&authority.SubnetID, &authority.SubnetGeneration, &authority.SubnetState,
		&authority.SegmentClaimID, &authority.SegmentGeneration, &authority.SegmentState,
		&authority.HostMappingGeneration, &authority.MappingState, &authority.MappingMaximumMTU,
		&authority.IPAddressAllowed, &authority.IdentityConflict, &authority.PortConflict,
		&authority.BindingSupported, &authority.HandoffReady,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return placement.NetworkAuthority{}, false, nil
	}
	if err != nil {
		return placement.NetworkAuthority{}, false, err
	}
	return authority, true, nil
}

func lockNetworkRequirementKeys(ctx context.Context, tx pgx.Tx, requirements []placement.NetworkRequirement) error {
	keys := make([]string, 0, len(requirements)*3)
	for _, required := range requirements {
		keys = append(keys, "network-port/"+required.PortID)
		if required.AllocationSource == "AUTOMATIC" {
			keys = append(keys,
				"network-ipam/"+required.SubnetID,
				"network-mac-auto/"+required.NetworkID,
			)
		} else {
			keys = append(keys,
				"network-ip/"+required.SubnetID+"/"+required.IPAddress,
				"network-mac/"+required.NetworkID+"/"+required.MACAddress,
			)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return err
		}
	}
	return nil
}

func lockNetworkAuthorityRows(ctx context.Context, tx pgx.Tx, hostID string, requirements []placement.NetworkRequirement) error {
	for _, required := range requirements {
		var networkID string
		if err := tx.QueryRow(ctx, `
			SELECT network.network_id
			FROM kim.networks_current network
			JOIN kim.network_subnets_current subnet
			  ON subnet.network_id=network.network_id AND subnet.subnet_id=$2
			JOIN kim.network_segment_claims_current segment
			  ON segment.network_id=network.network_id AND segment.segment_claim_id=$3
			JOIN kim.host_network_mappings_current mapping
			  ON mapping.host_id=$4 AND mapping.segment_claim_id=segment.segment_claim_id
			WHERE network.network_id=$1
			  AND (network.authority_source='LEGACY_FOUNDATION' OR EXISTS(
			    SELECT 1 FROM kim.network_realizations_current realization
			    WHERE realization.network_id=network.network_id AND realization.network_revision=network.network_revision
			      AND realization.realization_state='VERIFIED' AND realization.terminal_evidence_id IS NOT NULL))
			  AND (subnet.authority_source='LEGACY_FOUNDATION' OR EXISTS(
			    SELECT 1 FROM kim.subnet_realizations_current realization
			    WHERE realization.subnet_id=subnet.subnet_id AND realization.subnet_revision=subnet.subnet_revision
			      AND realization.network_revision=network.network_revision
			      AND realization.realization_state='VERIFIED' AND realization.terminal_evidence_id IS NOT NULL))
			FOR SHARE OF network, subnet, segment, mapping
		`, required.NetworkID, required.SubnetID, required.SegmentClaimID, hostID).Scan(&networkID); err != nil {
			return err
		}
	}
	return nil
}

func claimNetworkPortTx(ctx context.Context, tx pgx.Tx, admissionID string, request PlacementAdmissionRequest, current placement.Evaluation, required placement.NetworkRequirement) error {
	if required.HandoffID != "" {
		return claimNetworkPortHandoffTx(ctx, tx, admissionID, request, current, required)
	}
	var authoritySource string
	if err := tx.QueryRow(ctx, `SELECT authority_source FROM kim.network_ports_current WHERE port_id=$1`, required.PortID).Scan(&authoritySource); err == nil {
		if authoritySource != "PORT_RESOURCE" {
			return ErrPlacementConflict
		}
		return bindPortResourceTx(ctx, tx, admissionID, request, current, required)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.network_ports_current (
			port_id, placement_admission_id, project_id, workload_id,
			network_id, subnet_id, port_generation, desired_state,
			port_revision,port_name,mac_policy,ip_allocation_mode,attachment_policy,
			attachment_state,datapath_profile,delete_protection,desired_digest,authority_source,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,1,'RESERVED',1,$1,'LEGACY_CLAIM','LEGACY_CLAIM','WORKLOAD',
			'BOUND','STANDARD',false,encode(sha256(convert_to($1||':legacy:1','UTF8')),'hex'),'LEGACY_ADMISSION',statement_timestamp())
	`, required.PortID, admissionID, request.ProjectID, request.WorkloadID,
		required.NetworkID, required.SubnetID); err != nil {
		return fmt.Errorf("reserve Network Port %s: %w", required.PortID, err)
	}
	allocationSource := required.AllocationSource
	if allocationSource == "" {
		allocationSource = "EXPLICIT"
	}
	ipAddress, macAddress := required.IPAddress, required.MACAddress
	if allocationSource == "AUTOMATIC" {
		var err error
		ipAddress, macAddress, err = allocateAutomaticNetworkIdentitiesTx(ctx, tx, request.RequestID, required.PortID, required.NetworkID, required.SubnetID)
		if err != nil {
			return fmt.Errorf("allocate automatic Network identities for Port %s: %w", required.PortID, err)
		}
	}
	allocationMode := "EXPLICIT"
	if allocationSource == "AUTOMATIC" {
		allocationMode = "AUTO"
	}
	subnetAllocation, err := recordSubnetPortAllocationTx(ctx, tx, request.RequestID, required.PortID, required.SubnetID, allocationMode, ipAddress)
	if err != nil {
		return fmt.Errorf("authorize Subnet IP allocation for Port %s: %w", required.PortID, err)
	}
	var subnetRevision, ipAllocationGeneration any
	var ipAllocationID any
	if subnetAllocation != nil {
		subnetRevision = subnetAllocation.SubnetRevision
		ipAllocationID = subnetAllocation.AllocationID
		ipAllocationGeneration = subnetAllocation.AllocationGeneration
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.network_identity_claims (
			identity_claim_id, placement_admission_id, port_id, project_id,
			network_id, subnet_id, claim_type, ip_address,
			allocation_source, claim_generation, claim_state,
			subnet_revision,ip_allocation_id,ip_allocation_generation
		) VALUES ($1,$2,$3,$4,$5,$6,'IP',$7::inet,$8,1,'RESERVED',$9,$10,$11)
	`, "ip:"+request.RequestID+":"+required.PortID, admissionID, required.PortID,
		request.ProjectID, required.NetworkID, required.SubnetID, ipAddress, allocationSource, subnetRevision, ipAllocationID, ipAllocationGeneration); err != nil {
		return fmt.Errorf("reserve IP identity for Port %s: %w", required.PortID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.network_identity_claims (
			identity_claim_id, placement_admission_id, port_id, project_id,
			network_id, subnet_id, claim_type, mac_address,
			allocation_source, claim_generation, claim_state
		) VALUES ($1,$2,$3,$4,$5,$6,'MAC',$7::macaddr,$8,1,'RESERVED')
	`, "mac:"+request.RequestID+":"+required.PortID, admissionID, required.PortID,
		request.ProjectID, required.NetworkID, required.SubnetID, macAddress, allocationSource); err != nil {
		return fmt.Errorf("reserve MAC identity for Port %s: %w", required.PortID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.port_bindings_current (
			port_id, placement_admission_id, host_id, segment_claim_id,
			binding_generation, binding_type, device_address, binding_state
		) VALUES ($1,$2,$3,$4,1,$5,NULLIF($6,''),'RESERVED')
	`, required.PortID, admissionID, current.HostID, required.SegmentClaimID,
		required.BindingType, required.DeviceAddress); err != nil {
		return fmt.Errorf("reserve Port Binding %s: %w", required.PortID, err)
	}
	return nil
}

// bindPortResourceTx makes Final Admission a consumer of an independently
// allocated logical Port. The Port revision and MAC/IP allocation generations
// remain unchanged; only the physical binding incarnation is created.
func bindPortResourceTx(ctx context.Context, tx pgx.Tx, admissionID string, request PlacementAdmissionRequest, current placement.Evaluation, required placement.NetworkRequirement) error {
	var revision, networkRevision, subnetRevision, macGeneration, ipGeneration, realizationGeneration int64
	var projectID, networkID, subnetID, workloadID, attachmentID, macID, macAddress, ipID, ipAddress, chassis string
	var desired PortResourceRequest
	err := tx.QueryRow(ctx, `SELECT p.port_revision,p.project_id,p.network_id,p.subnet_id,p.workload_id,a.attachment_intent_id,m.allocation_id,m.allocation_generation,m.assigned_mac::text,i.allocation_id,i.allocation_generation,host(i.assigned_address),n.network_revision,s.subnet_revision,r.realization_generation,map.ovn_chassis_name,p.port_name,p.mac_policy,COALESCE(p.requested_mac::text,''),p.ip_allocation_mode,COALESCE(host(p.requested_ip),''),p.attachment_policy,p.datapath_profile,p.delete_protection
		FROM kim.network_ports_current p JOIN kim.port_attachment_intents_current a ON a.port_id=p.port_id JOIN kim.port_mac_allocations_current m ON m.port_id=p.port_id JOIN kim.subnet_ip_allocations_current i ON i.owner_resource_type='PORT' AND i.owner_resource_id=p.port_id JOIN kim.networks_current n ON n.network_id=p.network_id JOIN kim.network_subnets_current s ON s.subnet_id=p.subnet_id JOIN kim.port_realizations_current r ON r.port_id=p.port_id JOIN kim.host_network_mappings_current map ON map.host_id=$3 AND map.segment_claim_id=$4
		WHERE p.port_id=$1 AND p.authority_source='PORT_RESOURCE' AND p.attachment_state='ATTACHMENT_REQUESTED' AND p.desired_state='ACTIVE' AND p.port_revision=$2 AND a.intent_state='REQUESTED' AND a.attachment_intent_id=$5 AND m.allocation_state='ALLOCATED' AND i.allocation_state='ALLOCATED' FOR UPDATE OF p,a,m,i,r`, required.PortID, required.PortRevision, current.HostID, required.SegmentClaimID, required.AttachmentIntentID).Scan(&revision, &projectID, &networkID, &subnetID, &workloadID, &attachmentID, &macID, &macGeneration, &macAddress, &ipID, &ipGeneration, &ipAddress, &networkRevision, &subnetRevision, &realizationGeneration, &chassis, &desired.Name, &desired.MACPolicy, &desired.RequestedMAC, &desired.IPAllocationMode, &desired.RequestedIP, &desired.AttachmentPolicy, &desired.DatapathProfile, &desired.DeleteProtection)
	if err != nil {
		return fmt.Errorf("load independent Port binding authority: %w", err)
	}
	if projectID != request.ProjectID || workloadID != request.WorkloadID || networkID != required.NetworkID || subnetID != required.SubnetID || attachmentID != required.AttachmentIntentID || macAddress != required.MACAddress || ipAddress != required.IPAddress || chassis == "" {
		return fmt.Errorf("independent Port binding authority mismatch project=%t workload=%t network=%t subnet=%t attachment=%t mac=%t ip=%t chassis=%t: %w", projectID == request.ProjectID, workloadID == request.WorkloadID, networkID == required.NetworkID, subnetID == required.SubnetID, attachmentID == required.AttachmentIntentID, macAddress == required.MACAddress, ipAddress == required.IPAddress, chassis != "", ErrPlacementStale)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kim.network_identity_claims(identity_claim_id,placement_admission_id,port_id,project_id,network_id,subnet_id,claim_type,ip_address,allocation_source,claim_generation,claim_state,subnet_revision,ip_allocation_id,ip_allocation_generation) VALUES($1,$2,$3,$4,$5,$6,'IP',$7,'EXTERNAL',1,'RESERVED',$8,$9,$10)`, "ip:"+request.RequestID+":"+required.PortID, admissionID, required.PortID, projectID, networkID, subnetID, ipAddress, subnetRevision, ipID, ipGeneration); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kim.network_identity_claims(identity_claim_id,placement_admission_id,port_id,project_id,network_id,subnet_id,claim_type,mac_address,allocation_source,claim_generation,claim_state) VALUES($1,$2,$3,$4,$5,$6,'MAC',$7,'EXTERNAL',1,'RESERVED')`, "mac:"+request.RequestID+":"+required.PortID, admissionID, required.PortID, projectID, networkID, subnetID, macAddress); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kim.port_bindings_current(port_id,placement_admission_id,host_id,segment_claim_id,binding_generation,binding_type,device_address,binding_state) VALUES($1,$2,$3,$4,1,$5,NULLIF($6,''),'RESERVED')`, required.PortID, admissionID, current.HostID, required.SegmentClaimID, required.BindingType, required.DeviceAddress); err != nil {
		return err
	}
	boundID := attachmentID + ":bound:" + admissionID
	digest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s/1", boundID, required.PortID, revision, admissionID))
	if _, err = tx.Exec(ctx, `INSERT INTO kim.port_attachment_intent_evidence(attachment_intent_id,port_id,port_revision,attachment_generation,workload_id,intent_state,placement_admission_id,binding_generation,intent_digest) VALUES($1,$2,$3,2,$4,'BOUND',$5,1,$6)`, boundID, required.PortID, revision, workloadID, admissionID, digest); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE kim.port_attachment_intents_current SET attachment_intent_id=$2,attachment_generation=2,intent_state='BOUND',updated_at=statement_timestamp() WHERE port_id=$1 AND attachment_intent_id=$3`, required.PortID, boundID, attachmentID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE kim.network_ports_current SET placement_admission_id=$2,attachment_state='BOUND',desired_state='RESERVED',updated_at=statement_timestamp() WHERE port_id=$1 AND port_revision=$3`, required.PortID, admissionID, revision); err != nil {
		return err
	}
	desired.PortID, desired.ProjectID, desired.NetworkID, desired.SubnetID = required.PortID, projectID, networkID, subnetID
	mac := PortResource{MACAllocationID: macID, MACAllocationGeneration: uint64(macGeneration), MACAddress: macAddress}
	ip := &SubnetIPAllocation{AllocationID: ipID, AllocationGeneration: uint64(ipGeneration), AssignedAddress: ipAddress}
	return insertPortRealizationOperationTx(ctx, tx, desired, uint64(revision), uint64(networkRevision), uint64(subnetRevision), mac, ip, uint64(realizationGeneration+1), "REALIZE", "PRESENT", 1, chassis)
}

// claimNetworkPortHandoffTx preserves logical Port and IP/MAC claim identity
// while atomically advancing only the Host binding incarnation. Eligibility is
// still read-only; this Final Admission transaction is the mutation authority.
func claimNetworkPortHandoffTx(ctx context.Context, tx pgx.Tx, admissionID string, request PlacementAdmissionRequest, current placement.Evaluation, required placement.NetworkRequirement) error {
	var sourceAdmission, sourceHost, workloadID, quiescenceDigest string
	var sourcePortGeneration, sourceBindingGeneration uint64
	var authoritySource string
	var logicalPortRevision uint64
	var quiesced bool
	err := tx.QueryRow(ctx, `SELECT p.placement_admission_id,b.host_id,p.workload_id,p.port_generation,b.binding_generation,q.evidence_digest,q.quiescence_state='QUIESCED',p.authority_source,p.port_revision
		FROM kim.network_ports_current p JOIN kim.port_bindings_current b ON b.port_id=p.port_id
		JOIN kim.network_port_source_quiescence_evidence q ON q.evidence_id=$2 AND q.port_id=p.port_id AND q.port_generation=p.port_generation AND q.source_host_id=b.host_id AND q.source_binding_generation=b.binding_generation
		JOIN kim.network_port_binding_retirement_evidence re ON re.evidence_id=q.retirement_evidence_id AND re.port_id=p.port_id AND re.port_generation=p.port_generation AND re.binding_generation=b.binding_generation AND re.source_host_id=b.host_id AND re.retirement_state='VERIFIED'
		JOIN kim.network_port_binding_retirements_current rc ON rc.port_id=p.port_id AND rc.port_generation=p.port_generation AND rc.binding_generation=b.binding_generation AND rc.terminal_evidence_id=re.evidence_id AND rc.retirement_state='VERIFIED'
		WHERE p.port_id=$1 FOR UPDATE OF p,b`, required.PortID, required.SourceQuiescenceEvidenceID).Scan(&sourceAdmission, &sourceHost, &workloadID, &sourcePortGeneration, &sourceBindingGeneration, &quiescenceDigest, &quiesced, &authoritySource, &logicalPortRevision)
	if err != nil || !quiesced || workloadID != request.WorkloadID || sourceHost != required.SourceHostID || sourceHost == current.HostID || sourcePortGeneration != required.SourcePortGeneration || sourceBindingGeneration != required.SourceBindingGeneration || quiescenceDigest != required.SourceQuiescenceEvidenceDigest || required.DestinationPortGeneration != sourcePortGeneration+1 || required.DestinationBindingGeneration != sourceBindingGeneration+1 {
		return ErrPlacementStale
	}
	payload, _ := json.Marshal(map[string]any{"handoff_id": required.HandoffID, "port_id": required.PortID, "workload_id": workloadID, "source_admission_id": sourceAdmission, "destination_admission_id": admissionID, "source_host_id": sourceHost, "destination_host_id": current.HostID, "source_port_generation": sourcePortGeneration, "destination_port_generation": required.DestinationPortGeneration, "source_binding_generation": sourceBindingGeneration, "destination_binding_generation": required.DestinationBindingGeneration, "source_quiescence_evidence_id": required.SourceQuiescenceEvidenceID, "source_quiescence_evidence_digest": quiescenceDigest})
	handoffDigest := digestReleaseBytes(payload)
	if _, err := tx.Exec(ctx, `INSERT INTO kim.port_binding_handoff_evidence(handoff_id,port_id,workload_id,source_admission_id,destination_admission_id,source_host_id,destination_host_id,source_port_generation,destination_port_generation,source_binding_generation,destination_binding_generation,source_quiescence_evidence_id,source_quiescence_evidence_digest,handoff_state,handoff_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'DESTINATION_RESERVED',$14)`, required.HandoffID, required.PortID, workloadID, sourceAdmission, admissionID, sourceHost, current.HostID, sourcePortGeneration, required.DestinationPortGeneration, sourceBindingGeneration, required.DestinationBindingGeneration, required.SourceQuiescenceEvidenceID, quiescenceDigest, handoffDigest); err != nil {
		return fmt.Errorf("record PortBindingHandoff %s: %w", required.PortID, err)
	}
	if tag, err := tx.Exec(ctx, `UPDATE kim.network_ports_current SET placement_admission_id=$2,port_generation=$3,desired_state='RESERVED' WHERE port_id=$1 AND placement_admission_id=$4 AND port_generation=$5`, required.PortID, admissionID, required.DestinationPortGeneration, sourceAdmission, sourcePortGeneration); err != nil || tag.RowsAffected() != 1 {
		return ErrPlacementStale
	}
	if tag, err := tx.Exec(ctx, `UPDATE kim.port_bindings_current SET placement_admission_id=$2,host_id=$3,binding_generation=$4,binding_state='RESERVED',created_at=statement_timestamp() WHERE port_id=$1 AND placement_admission_id=$5 AND host_id=$6 AND binding_generation=$7`, required.PortID, admissionID, current.HostID, required.DestinationBindingGeneration, sourceAdmission, sourceHost, sourceBindingGeneration); err != nil || tag.RowsAffected() != 1 {
		return ErrPlacementStale
	}
	tag, err := tx.Exec(ctx, `INSERT INTO kim.port_binding_handoffs_current(port_id,handoff_id,destination_binding_generation,handoff_state)
		VALUES($1,$2,$3,'DESTINATION_RESERVED')
		ON CONFLICT(port_id) DO UPDATE SET handoff_id=EXCLUDED.handoff_id,
			destination_binding_generation=EXCLUDED.destination_binding_generation,
			handoff_state='DESTINATION_RESERVED',updated_at=statement_timestamp()
		WHERE kim.port_binding_handoffs_current.destination_binding_generation<EXCLUDED.destination_binding_generation`, required.PortID, required.HandoffID, required.DestinationBindingGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrPlacementStale
	}
	if authoritySource == "PORT_RESOURCE" {
		if err := advancePortResourceHandoffTx(ctx, tx, admissionID, current.HostID, required, workloadID, logicalPortRevision); err != nil {
			return err
		}
	}
	return nil
}

func advancePortResourceHandoffTx(ctx context.Context, tx pgx.Tx, admissionID, destinationHost string, required placement.NetworkRequirement, workloadID string, portRevision uint64) error {
	var attachmentGeneration, networkRevision, subnetRevision, realizationGeneration int64
	var oldAttachment, projectID, name, macPolicy, requestedMAC, ipMode, requestedIP, attachmentPolicy, profile, macID, macAddress, ipID, ipAddress, chassis string
	var macGeneration, ipGeneration uint64
	var protect bool
	err := tx.QueryRow(ctx, `SELECT a.attachment_intent_id,a.attachment_generation,p.project_id,p.port_name,p.mac_policy,COALESCE(p.requested_mac::text,''),p.ip_allocation_mode,COALESCE(host(p.requested_ip),''),p.attachment_policy,p.datapath_profile,p.delete_protection,n.network_revision,s.subnet_revision,r.realization_generation,m.allocation_id,m.allocation_generation,m.assigned_mac::text,i.allocation_id,i.allocation_generation,host(i.assigned_address),map.ovn_chassis_name FROM kim.network_ports_current p JOIN kim.port_attachment_intents_current a ON a.port_id=p.port_id JOIN kim.networks_current n ON n.network_id=p.network_id JOIN kim.network_subnets_current s ON s.subnet_id=p.subnet_id JOIN kim.port_realizations_current r ON r.port_id=p.port_id JOIN kim.port_mac_allocations_current m ON m.port_id=p.port_id JOIN kim.subnet_ip_allocations_current i ON i.owner_resource_id=p.port_id AND i.allocation_state='ALLOCATED' JOIN kim.host_network_mappings_current map ON map.host_id=$2 AND map.segment_claim_id=$3 WHERE p.port_id=$1 AND p.port_revision=$4 AND p.authority_source='PORT_RESOURCE' FOR UPDATE OF a,r`, required.PortID, destinationHost, required.SegmentClaimID, portRevision).Scan(&oldAttachment, &attachmentGeneration, &projectID, &name, &macPolicy, &requestedMAC, &ipMode, &requestedIP, &attachmentPolicy, &profile, &protect, &networkRevision, &subnetRevision, &realizationGeneration, &macID, &macGeneration, &macAddress, &ipID, &ipGeneration, &ipAddress, &chassis)
	if err != nil {
		return ErrPlacementStale
	}
	newAttachment := required.HandoffID + ":attachment"
	nextAttachment := uint64(attachmentGeneration + 1)
	digest := digestNetworkResource(fmt.Sprintf("%s/%s/%d/%s/%d", newAttachment, required.PortID, portRevision, admissionID, required.DestinationBindingGeneration))
	if _, err = tx.Exec(ctx, `INSERT INTO kim.port_attachment_intent_evidence(attachment_intent_id,port_id,port_revision,attachment_generation,workload_id,intent_state,placement_admission_id,binding_generation,intent_digest) VALUES($1,$2,$3,$4,$5,'BOUND',$6,$7,$8)`, newAttachment, required.PortID, portRevision, nextAttachment, workloadID, admissionID, required.DestinationBindingGeneration, digest); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE kim.port_attachment_intents_current SET attachment_intent_id=$2,attachment_generation=$3,intent_state='BOUND',updated_at=statement_timestamp() WHERE port_id=$1 AND attachment_intent_id=$4`, required.PortID, newAttachment, nextAttachment, oldAttachment); err != nil {
		return err
	}
	desired := PortResourceRequest{PortID: required.PortID, ProjectID: projectID, NetworkID: required.NetworkID, Name: name, MACPolicy: macPolicy, RequestedMAC: requestedMAC, SubnetID: required.SubnetID, IPAllocationMode: ipMode, RequestedIP: requestedIP, AttachmentPolicy: attachmentPolicy, DatapathProfile: profile, DeleteProtection: protect}
	mac := PortResource{MACAllocationID: macID, MACAllocationGeneration: macGeneration, MACAddress: macAddress}
	ip := &SubnetIPAllocation{AllocationID: ipID, AllocationGeneration: ipGeneration, AssignedAddress: ipAddress}
	return insertPortRealizationOperationTx(ctx, tx, desired, portRevision, uint64(networkRevision), uint64(subnetRevision), mac, ip, uint64(realizationGeneration+1), "REALIZE", "PRESENT", required.DestinationBindingGeneration, chassis)
}

func loadStorageAuthority(ctx context.Context, row QueryRower, projectID, workloadID, hostID string, required placement.StorageRequirement) (placement.StorageAuthority, bool, error) {
	var authority placement.StorageAuthority
	err := row.QueryRow(ctx, `
		SELECT $3, $4, backend.backend_id, backend.backend_type,
		       backend.backend_generation, backend.lifecycle_state, backend.host_id,
		       backend.vg_uuid, backend.capability_generation, backend.capability_state,
		       backend.support_tier,
		       class_current.storage_class_id, class_current.class_revision,
		       class_current.lifecycle_state, class_evidence.allowed_backend_type,
		       class_evidence.locality, class_evidence.fencing_policy_revision,
		       class_evidence.thin_provisioning, class_evidence.encryption_required,
		       ('SINGLE_WRITER' = ANY(class_evidence.access_modes)),
		       capacity_current.capacity_generation, capacity_current.projection_state,
		       capacity.health_state, capacity.total_bytes, capacity.observed_free_bytes,
		       capacity.external_or_unknown_bytes, capacity.hard_reserve_bytes,
		       COALESCE(claims.reserved_bytes,0),
		       EXISTS (SELECT 1 FROM kim.volumes_current volume WHERE volume.volume_id=$3),
		       EXISTS (SELECT 1 FROM kim.volume_attachments_current attachment WHERE attachment.attachment_id=$4)
		FROM kim.storage_backends_current backend
		JOIN kim.storage_classes_current class_current
		  ON class_current.storage_class_id=$5
		JOIN kim.storage_class_revision_evidence class_evidence
		  ON class_evidence.storage_class_id=class_current.storage_class_id
		 AND class_evidence.class_revision=class_current.class_revision
		JOIN kim.storage_capacity_projections_current capacity_current
		  ON capacity_current.backend_id=backend.backend_id
		JOIN kim.storage_capacity_observation_evidence capacity
		  ON capacity.observation_id=capacity_current.observation_id
		LEFT JOIN LATERAL (
		    SELECT sum(reserved_bytes) reserved_bytes
		    FROM kim.storage_capacity_claims
		    WHERE backend_id=backend.backend_id
		      AND claim_state IN ('RESERVED','ALLOCATED','RELEASE_PENDING','QUARANTINED')
		) claims ON true
		WHERE backend.backend_id=$1 AND backend.host_id=$2
	`, required.BackendID, hostID, required.VolumeID, required.AttachmentID,
		required.StorageClassID).Scan(
		&authority.VolumeID, &authority.AttachmentID, &authority.BackendID,
		&authority.BackendType, &authority.BackendGeneration, &authority.BackendState,
		&authority.BackendHostID, &authority.VGUUID,
		&authority.BackendCapabilityGeneration, &authority.CapabilityState,
		&authority.SupportTier,
		&authority.StorageClassID, &authority.StorageClassRevision,
		&authority.StorageClassState, &authority.AllowedBackendType,
		&authority.Locality, &authority.FencingPolicyRevision,
		&authority.ThinProvisioning, &authority.EncryptionRequired,
		&authority.SingleWriterAllowed, &authority.CapacityGeneration,
		&authority.CapacityState, &authority.HealthState, &authority.TotalBytes,
		&authority.ObservedFreeBytes, &authority.ExternalOrUnknownBytes,
		&authority.HardReserveBytes, &authority.ClaimedBytes,
		&authority.VolumeConflict, &authority.AttachmentConflict,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return placement.StorageAuthority{}, false, nil
	}
	if err != nil {
		return placement.StorageAuthority{}, false, err
	}
	if required.VolumeRevision > 0 {
		var reservedBytes uint64
		err = row.QueryRow(ctx, `
			WITH exact_authority AS (
				SELECT claim.reserved_bytes
				FROM kim.volumes_current volume
				JOIN kim.volume_resource_revision_evidence revision
				  ON revision.volume_id=volume.volume_id
				 AND revision.volume_revision=volume.volume_revision
				JOIN kim.volume_capacity_allocation_decision_evidence allocation
				  ON allocation.allocation_id=$11
				 AND allocation.allocation_generation=$12
				 AND allocation.volume_id=volume.volume_id
				 AND allocation.volume_revision=volume.volume_revision
				JOIN kim.storage_capacity_claims claim
				  ON claim.allocation_decision_id=allocation.allocation_id
				 AND claim.allocation_generation=allocation.allocation_generation
				 AND claim.volume_id=volume.volume_id
				 AND claim.volume_revision=volume.volume_revision
				JOIN kim.volume_backend_binding_intents binding_intent
				  ON binding_intent.volume_id=volume.volume_id
				 AND binding_intent.volume_revision=volume.volume_revision
				 AND binding_intent.capacity_allocation_id=allocation.allocation_id
				 AND binding_intent.capacity_allocation_generation=allocation.allocation_generation
				JOIN kim.volume_backend_bindings_current binding
				  ON binding.binding_id=binding_intent.binding_id
				 AND binding.binding_generation=binding_intent.binding_generation
				JOIN kim.volume_materializations_current materialization
				  ON materialization.volume_id=volume.volume_id
				 AND materialization.volume_revision=volume.volume_revision
				 AND materialization.materialization_generation=binding_intent.materialization_generation
				 AND materialization.binding_id=binding_intent.binding_id
				 AND materialization.binding_generation=binding_intent.binding_generation
				JOIN kim.volume_materialization_operation_evidence operation
				  ON operation.operation_id=materialization.operation_id
				 AND operation.operation_generation=materialization.operation_generation
				JOIN kim.volume_materialization_terminal_evidence terminal
				  ON terminal.terminal_evidence_id=materialization.terminal_evidence_id
				 AND terminal.operation_id=materialization.operation_id
				 AND terminal.operation_generation=materialization.operation_generation
				 AND terminal.volume_id=volume.volume_id
				 AND terminal.volume_revision=volume.volume_revision
				 AND terminal.materialization_generation=materialization.materialization_generation
				 AND terminal.binding_id=binding_intent.binding_id
				 AND terminal.binding_generation=binding_intent.binding_generation
				JOIN kim.volume_attachment_intents_current attachment
				  ON attachment.volume_id=volume.volume_id
				 AND attachment.volume_revision=volume.volume_revision
				WHERE volume.volume_id=$1
				  AND volume.volume_revision=$2
				  AND volume.project_id=$3
				  AND volume.storage_class_id=$4
				  AND volume.storage_class_revision=$5
				  AND volume.size_bytes=$6
				  AND volume.access_mode='SINGLE_WRITER'
				  AND volume.lifecycle_state='AVAILABLE'
				  AND volume.authority_source='VOLUME_RESOURCE'
				  AND allocation.storage_class_id=$4
				  AND allocation.storage_class_revision=$5
				  AND allocation.requested_bytes=$6
				  AND allocation.backend_id=$7
				  AND allocation.backend_generation=$8
				  AND allocation.host_id=$9
				  AND allocation.vg_uuid=$10
				  AND allocation.capacity_generation=$13
				  AND allocation.decision_state='ALLOCATED'
				  AND claim.backend_id=$7
				  AND claim.capacity_generation=$13
				  AND claim.reserved_bytes=$6
				  AND claim.claim_state IN('RESERVED','ALLOCATED')
				  AND claim.authority_source='VOLUME_RESOURCE'
				  AND binding_intent.backend_id=$7
				  AND binding_intent.host_id=$9
				  AND binding_intent.vg_uuid=$10
				  AND binding_intent.binding_state='BOUND'
				  AND binding_intent.authority_source='VOLUME_RESOURCE'
				  AND binding.binding_state='BOUND'
				  AND binding.host_id=$9
				  AND binding.vg_uuid=$10
				  AND materialization.materialization_state='VERIFIED'
				  AND materialization.terminal_evidence_id IS NOT NULL
				  AND operation.volume_revision=volume.volume_revision
				  AND operation.allocation_id=allocation.allocation_id
				  AND operation.allocation_generation=allocation.allocation_generation
				  AND operation.binding_id=binding_intent.binding_id
				  AND operation.binding_generation=binding_intent.binding_generation
				  AND operation.materialization_generation=materialization.materialization_generation
				  AND operation.backend_id=$7
				  AND operation.backend_generation=$8
				  AND operation.host_id=$9
				  AND operation.vg_uuid=$10
				  AND terminal.terminal_state='VERIFIED'
				  AND attachment.attachment_intent_id=$14
				  AND attachment.attachment_generation=$15
				  AND attachment.requested_attachment_id=$16
				  AND attachment.requested_physical_attachment_generation=$17
				  AND attachment.workload_id=$18
				  AND attachment.intent_state='REQUESTED'
				  AND NOT EXISTS (
					SELECT 1 FROM kim.volume_capacity_allocation_decision_evidence newer
					WHERE newer.volume_id=volume.volume_id
					  AND newer.allocation_generation>allocation.allocation_generation
				  )
				  AND NOT EXISTS (
					SELECT 1 FROM kim.volume_materializations_current newer
					WHERE newer.volume_id=volume.volume_id
					  AND newer.materialization_generation>materialization.materialization_generation
				  )
			)
			SELECT EXISTS(SELECT 1 FROM exact_authority),
			       COALESCE((SELECT reserved_bytes FROM exact_authority),0)
		`, required.VolumeID, required.VolumeRevision, projectID,
			required.StorageClassID, required.StorageClassRevision, required.SizeBytes,
			required.BackendID, required.BackendGeneration, hostID, required.VGUUID,
			required.CapacityAllocationID, required.CapacityAllocationGeneration,
			required.CapacityGeneration, required.AttachmentIntentID,
			required.AttachmentIntentGeneration, required.AttachmentID,
			required.AttachmentGeneration, workloadID).Scan(&authority.ResourceConsumerReady, &reservedBytes)
		if err != nil {
			return placement.StorageAuthority{}, false, err
		}
		if authority.ResourceConsumerReady {
			if reservedBytes > authority.ClaimedBytes {
				return placement.StorageAuthority{}, false, ErrPlacementConflict
			}
			authority.ClaimedBytes -= reservedBytes
			authority.VolumeConflict = false
			authority.AttachmentConflict = false
		}
	}
	return authority, true, nil
}

func lockStorageRequirementKeys(ctx context.Context, tx pgx.Tx, requirements []placement.StorageRequirement) error {
	keys := make([]string, 0, len(requirements)*3)
	for _, required := range requirements {
		keys = append(keys,
			"storage-backend/"+required.BackendID,
			"storage-volume/"+required.VolumeID,
			"storage-attachment/"+required.AttachmentID,
		)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return err
		}
	}
	return nil
}

func lockStorageAuthorityRows(ctx context.Context, tx pgx.Tx, hostID string, requirements []placement.StorageRequirement) error {
	for _, required := range requirements {
		var backendID string
		if err := tx.QueryRow(ctx, `
			SELECT backend.backend_id
			FROM kim.storage_backends_current backend
			JOIN kim.storage_classes_current class_current
			  ON class_current.storage_class_id=$2
			JOIN kim.storage_class_revision_evidence class_evidence
			  ON class_evidence.storage_class_id=class_current.storage_class_id
			 AND class_evidence.class_revision=class_current.class_revision
			JOIN kim.storage_capacity_projections_current capacity_current
			  ON capacity_current.backend_id=backend.backend_id
			JOIN kim.storage_capacity_observation_evidence capacity
			  ON capacity.observation_id=capacity_current.observation_id
			WHERE backend.backend_id=$1 AND backend.host_id=$3
			FOR SHARE OF backend, class_current, class_evidence, capacity_current, capacity
		`, required.BackendID, required.StorageClassID, hostID).Scan(&backendID); err != nil {
			return err
		}
	}
	return nil
}

func claimLocalLVMStorageTx(ctx context.Context, tx pgx.Tx, admissionID string, request PlacementAdmissionRequest, current placement.Evaluation, required placement.StorageRequirement) error {
	if required.VolumeRevision > 0 {
		return claimVolumeResourceStorageTx(ctx, tx, admissionID, request, current, required)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.volumes_current (
			volume_id, placement_admission_id, project_id, storage_class_id,
			storage_class_revision, desired_generation, size_bytes, access_mode,
			bootable, lifecycle_state
		) VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8,'RESERVED')
	`, required.VolumeID, admissionID, request.ProjectID, required.StorageClassID,
		required.StorageClassRevision, required.SizeBytes, required.AccessMode,
		required.Bootable); err != nil {
		return fmt.Errorf("reserve Volume %s: %w", required.VolumeID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.storage_capacity_claims (
			capacity_claim_id, placement_admission_id, backend_id, volume_id,
			capacity_generation, reserved_bytes, claim_state
		) VALUES ($1,$2,$3,$4,$5,$6,'RESERVED')
	`, "storage-capacity:"+request.RequestID+":"+required.VolumeID, admissionID,
		required.BackendID, required.VolumeID, required.CapacityGeneration,
		required.SizeBytes); err != nil {
		return fmt.Errorf("reserve Storage capacity for Volume %s: %w", required.VolumeID, err)
	}
	resourceDigest := sha256.Sum256([]byte(required.VolumeID))
	backendResourceKey := "kim-" + hex.EncodeToString(resourceDigest[:16])
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.volume_backend_binding_intents (
			binding_id, placement_admission_id, volume_id, binding_generation,
			backend_id, host_id, vg_uuid, backend_resource_key, binding_state
		) VALUES ($1,$2,$3,1,$4,$5,$6,$7,'RESERVED')
	`, "storage-binding:"+request.RequestID+":"+required.VolumeID, admissionID,
		required.VolumeID, required.BackendID, current.HostID, required.VGUUID,
		backendResourceKey); err != nil {
		return fmt.Errorf("reserve Local LVM binding for Volume %s: %w", required.VolumeID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.volume_attachments_current (
			attachment_id, placement_admission_id, volume_id, workload_id,
			desired_host_id, attachment_generation, access_mode, desired_state
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'RESERVED')
	`, required.AttachmentID, admissionID, required.VolumeID, request.WorkloadID,
		current.HostID, required.AttachmentGeneration, required.AccessMode); err != nil {
		return fmt.Errorf("reserve Volume Attachment %s: %w", required.AttachmentID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kim.volume_attachment_claims (
			attachment_claim_id, placement_admission_id, attachment_id, volume_id,
			workload_id, host_id, attachment_generation, access_mode,
			fencing_policy_revision, claim_state
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'RESERVED')
	`, "storage-attachment-claim:"+request.RequestID+":"+required.AttachmentID,
		admissionID, required.AttachmentID, required.VolumeID, request.WorkloadID,
		current.HostID, required.AttachmentGeneration, required.AccessMode,
		required.FencingPolicyRevision); err != nil {
		return fmt.Errorf("reserve single-writer Attachment Claim %s: %w", required.AttachmentID, err)
	}
	return nil
}

// claimVolumeResourceStorageTx consumes an already-reserved standalone Volume.
// It binds, but never releases or reduces, the exact capacity allocation.
func claimVolumeResourceStorageTx(ctx context.Context, tx pgx.Tx, admissionID string, request PlacementAdmissionRequest, current placement.Evaluation, required placement.StorageRequirement) error {
	var volumeID, capacityClaimID, bindingID, intentID string
	if err := tx.QueryRow(ctx, `SELECT volume_id FROM kim.volumes_current
		WHERE volume_id=$1 AND volume_revision=$2 AND project_id=$3
		  AND lifecycle_state='AVAILABLE' AND authority_source='VOLUME_RESOURCE'
		  AND placement_admission_id IS NULL FOR UPDATE`, required.VolumeID,
		required.VolumeRevision, request.ProjectID).Scan(&volumeID); err != nil {
		return ErrPlacementConflict
	}
	if err := tx.QueryRow(ctx, `SELECT capacity_claim_id FROM kim.storage_capacity_claims
		WHERE volume_id=$1 AND volume_revision=$2 AND allocation_decision_id=$3
		  AND allocation_generation=$4 AND backend_id=$5 AND capacity_generation=$6
		  AND reserved_bytes=$7 AND claim_state IN('RESERVED','ALLOCATED')
		  AND authority_source='VOLUME_RESOURCE' AND placement_admission_id IS NULL
		FOR UPDATE`, required.VolumeID, required.VolumeRevision,
		required.CapacityAllocationID, required.CapacityAllocationGeneration,
		required.BackendID, required.CapacityGeneration, required.SizeBytes).Scan(&capacityClaimID); err != nil {
		return ErrPlacementConflict
	}
	if err := tx.QueryRow(ctx, `SELECT binding_id FROM kim.volume_backend_binding_intents
		WHERE volume_id=$1 AND volume_revision=$2 AND capacity_allocation_id=$3
		  AND capacity_allocation_generation=$4 AND backend_id=$5 AND host_id=$6
		  AND vg_uuid=$7 AND binding_state='BOUND' AND authority_source='VOLUME_RESOURCE'
		  AND placement_admission_id IS NULL FOR UPDATE`, required.VolumeID,
		required.VolumeRevision, required.CapacityAllocationID,
		required.CapacityAllocationGeneration, required.BackendID, current.HostID,
		required.VGUUID).Scan(&bindingID); err != nil {
		return ErrPlacementConflict
	}
	var bindingGeneration uint64
	if err := tx.QueryRow(ctx, `SELECT binding_generation FROM kim.volume_backend_bindings_current
		WHERE binding_id=$1 AND volume_id=$2 AND binding_state='BOUND'
		  AND host_id=$3 AND vg_uuid=$4 FOR UPDATE`, bindingID, required.VolumeID,
		current.HostID, required.VGUUID).Scan(&bindingGeneration); err != nil {
		return ErrPlacementConflict
	}
	if err := tx.QueryRow(ctx, `SELECT attachment_intent_id FROM kim.volume_attachment_intents_current
		WHERE volume_id=$1 AND volume_revision=$2 AND attachment_intent_id=$3
		  AND attachment_generation=$4 AND requested_attachment_id=$5
		  AND requested_physical_attachment_generation=$6
		  AND workload_id=$7 AND intent_state='REQUESTED' FOR UPDATE`, required.VolumeID,
		required.VolumeRevision, required.AttachmentIntentID,
		required.AttachmentIntentGeneration, required.AttachmentID,
		required.AttachmentGeneration, request.WorkloadID).Scan(&intentID); err != nil {
		return ErrPlacementConflict
	}
	// Re-evaluate after all mutable current projections are locked. This exact
	// check includes immutable allocation, materialization, terminal, binding,
	// and attachment provenance plus supersession fencing.
	authority, found, err := loadStorageAuthority(ctx, tx, request.ProjectID,
		request.WorkloadID, current.HostID, required)
	if err != nil || !found || !authority.ResourceConsumerReady {
		return ErrPlacementConflict
	}

	if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachments_current(
		attachment_id,placement_admission_id,volume_id,workload_id,desired_host_id,
		attachment_generation,access_mode,desired_state
	) VALUES($1,$2,$3,$4,$5,$6,'SINGLE_WRITER','RESERVED')`, required.AttachmentID,
		admissionID, required.VolumeID, request.WorkloadID, current.HostID,
		required.AttachmentGeneration); err != nil {
		return ErrPlacementConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_claims(
		attachment_claim_id,placement_admission_id,attachment_id,volume_id,workload_id,
		host_id,attachment_generation,access_mode,fencing_policy_revision,claim_state
	) VALUES($1,$2,$3,$4,$5,$6,$7,'SINGLE_WRITER',$8,'RESERVED')`,
		"storage-attachment-claim:"+request.RequestID+":"+required.AttachmentID,
		admissionID, required.AttachmentID, required.VolumeID, request.WorkloadID,
		current.HostID, required.AttachmentGeneration, required.FencingPolicyRevision); err != nil {
		return ErrPlacementConflict
	}
	attachedIntentID := intentID + ":attached:" + admissionID
	attachedGeneration := required.AttachmentIntentGeneration + 1
	intentDigest := digestVolumeAuthority(fmt.Sprintf("%s/%s/%d/%d/%s/%s/%s/%d",
		attachedIntentID, required.VolumeID, required.VolumeRevision, attachedGeneration,
		request.WorkloadID, required.AttachmentID, bindingID, bindingGeneration))
	if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_intent_evidence(
		attachment_intent_id,volume_id,volume_revision,attachment_generation,workload_id,
		requested_attachment_id,requested_physical_attachment_generation,intent_state,
		placement_admission_id,physical_attachment_id,binding_id,binding_generation,intent_digest
	) VALUES($1,$2,$3,$4,$5,$6,$7,'ATTACHED',$8,$6,$9,$10,$11)`, attachedIntentID,
		required.VolumeID, required.VolumeRevision, attachedGeneration, request.WorkloadID,
		required.AttachmentID, required.AttachmentGeneration, admissionID, bindingID,
		bindingGeneration, intentDigest); err != nil {
		return err
	}
	updated, err := tx.Exec(ctx, `UPDATE kim.volume_attachment_intents_current SET
		attachment_intent_id=$2,attachment_generation=$3,intent_state='ATTACHED',updated_at=statement_timestamp()
		WHERE volume_id=$1 AND attachment_intent_id=$4 AND attachment_generation=$5
		  AND intent_state='REQUESTED'`, required.VolumeID, attachedIntentID,
		attachedGeneration, intentID, required.AttachmentIntentGeneration)
	if err != nil || updated.RowsAffected() != 1 {
		return ErrPlacementConflict
	}
	updates := []struct{ query, id string }{
		{`UPDATE kim.volumes_current SET placement_admission_id=$2,updated_at=statement_timestamp() WHERE volume_id=$1 AND placement_admission_id IS NULL`, required.VolumeID},
		{`UPDATE kim.storage_capacity_claims SET placement_admission_id=$2,updated_at=statement_timestamp() WHERE capacity_claim_id=$1 AND placement_admission_id IS NULL`, capacityClaimID},
		{`UPDATE kim.volume_backend_binding_intents SET placement_admission_id=$2,updated_at=statement_timestamp() WHERE binding_id=$1 AND placement_admission_id IS NULL`, bindingID},
	}
	for _, update := range updates {
		tag, err := tx.Exec(ctx, update.query, update.id, admissionID)
		if err != nil || tag.RowsAffected() != 1 {
			return ErrPlacementConflict
		}
	}
	return nil
}

func queryHugePageClaims(ctx context.Context, row QueryRower, hostID string) (map[uint64]uint64, error) {
	// QueryRower intentionally exposes only QueryRow. Aggregate the supported
	// sizes used by the current Flavor at call sites through one JSON object.
	var payload []byte
	err := row.QueryRow(ctx, `
		SELECT COALESCE(jsonb_object_agg(hugepage_size_kib, pages), '{}'::jsonb)
		FROM (
			SELECT hugepage_size_kib, sum(hugepage_pages) pages
			FROM kim.compute_allocation_claims
			WHERE host_id=$1 AND hugepage_size_kib IS NOT NULL
			  AND claim_state IN ('RESERVED','ALLOCATED','RELEASE_PENDING')
			GROUP BY hugepage_size_kib
		) claims
	`, hostID).Scan(&payload)
	if err != nil {
		return nil, err
	}
	encoded := map[string]uint64{}
	if err := json.Unmarshal(payload, &encoded); err != nil {
		return nil, err
	}
	result := make(map[uint64]uint64, len(encoded))
	for key, value := range encoded {
		var size uint64
		if _, err := fmt.Sscan(key, &size); err != nil {
			return nil, err
		}
		result[size] = value
	}
	return result, nil
}

func validatePlacementAdmissionRequest(request PlacementAdmissionRequest, hostID string) error {
	if request.RequestID == "" || request.ProjectID == "" || request.WorkloadID == "" || request.ImageID == "" || request.FlavorID == "" || request.PoolID == "" || hostID == "" {
		return errors.New("complete Placement admission request and candidate Host are required")
	}
	for _, required := range request.Network {
		allocationSource := required.AllocationSource
		if allocationSource == "" {
			allocationSource = "EXPLICIT"
		}
		if allocationSource == "AUTOMATIC" {
			if required.IPAddress != "" || required.MACAddress != "" {
				return errors.New("automatic Network identity must not include caller-selected IP or MAC")
			}
			continue
		}
		if allocationSource != "EXPLICIT" {
			return errors.New("unsupported Network identity allocation source")
		}
		address, err := netip.ParseAddr(required.IPAddress)
		if err != nil || address.IsUnspecified() || address.IsMulticast() {
			return errors.New("valid explicit Network IP identity is required")
		}
		mac, err := net.ParseMAC(required.MACAddress)
		if err != nil || len(mac) != 6 || mac[0]&1 != 0 || isZeroMAC(mac) {
			return errors.New("valid explicit Network MAC identity is required")
		}
	}
	return nil
}

func isZeroMAC(address net.HardwareAddr) bool {
	for _, octet := range address {
		if octet != 0 {
			return false
		}
	}
	return true
}

func normalizePlacementPCIRequirements(requirements []placement.PCIRequirement) []placement.PCIRequirement {
	normalized := make([]placement.PCIRequirement, len(requirements))
	copy(normalized, requirements)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].DeviceAddress < normalized[j].DeviceAddress })
	return normalized
}

func normalizePlacementNetworkRequirements(requirements []placement.NetworkRequirement) []placement.NetworkRequirement {
	normalized := make([]placement.NetworkRequirement, len(requirements))
	copy(normalized, requirements)
	for index := range normalized {
		if normalized[index].AllocationSource == "" {
			normalized[index].AllocationSource = "EXPLICIT"
		}
		if normalized[index].AllocationSource == "EXPLICIT" {
			if address, err := netip.ParseAddr(normalized[index].IPAddress); err == nil {
				normalized[index].IPAddress = address.String()
			}
			if address, err := net.ParseMAC(normalized[index].MACAddress); err == nil {
				normalized[index].MACAddress = address.String()
			}
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].PortID < normalized[j].PortID })
	return normalized
}

func normalizePlacementStorageRequirements(requirements []placement.StorageRequirement) []placement.StorageRequirement {
	normalized := make([]placement.StorageRequirement, len(requirements))
	copy(normalized, requirements)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].VolumeID < normalized[j].VolumeID })
	return normalized
}

func placementPCIRequirementsPayload(requirements []placement.PCIRequirement) ([]byte, string, error) {
	payload, err := json.Marshal(requirements)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func placementNetworkRequirementsPayload(requirements []placement.NetworkRequirement) ([]byte, string, error) {
	payload, err := json.Marshal(requirements)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func placementStorageRequirementsPayload(requirements []placement.StorageRequirement) ([]byte, string, error) {
	payload, err := json.Marshal(requirements)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func lockPlacementCatalogRows(ctx context.Context, tx pgx.Tx, request PlacementAdmissionRequest) error {
	var imageID, flavorID string
	if err := tx.QueryRow(ctx, `SELECT image_id FROM kim.images_current WHERE image_id=$1 AND lifecycle_state='ACTIVE' FOR SHARE`, request.ImageID).Scan(&imageID); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT flavor_id FROM kim.flavors_current WHERE flavor_id=$1 AND lifecycle_state='ACTIVE' FOR SHARE`, request.FlavorID).Scan(&flavorID); err != nil {
		return err
	}
	return nil
}

func nullablePlacementScopeID(request PlacementAdmissionRequest) any {
	if request.PlacementScopeID == "" {
		return nil
	}
	return request.PlacementScopeID
}
func nullablePlacementScopeGeneration(request PlacementAdmissionRequest) any {
	if request.PlacementScopeID == "" {
		return nil
	}
	return request.PlacementScopeGeneration
}
func nullablePlacementScopeDigest(request PlacementAdmissionRequest) any {
	if request.PlacementScopeID == "" {
		return nil
	}
	return request.PlacementScopeDigest
}
func nullablePlacementVisibilityDigest(request PlacementAdmissionRequest) any {
	if request.PlacementScopeID == "" {
		return nil
	}
	return request.VisibilityProvenanceDigest
}
