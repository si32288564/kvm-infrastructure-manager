package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	RequestID, ProjectID, WorkloadID, ImageID, FlavorID, PoolID string
	PCI                                                         []placement.PCIRequirement
}

type PlacementAdmission struct {
	AdmissionID, AllocationID, RequestID, RequestDigest string
	HostID, PoolID                                      string
	EvaluationDigest                                    string
}

func UpsertPlacementPool(ctx context.Context, db TxBeginner, pool PlacementPoolBinding) error {
	if pool.PoolID == "" || pool.PoolGeneration == 0 || pool.PolicyID == "" || pool.PolicyGeneration == 0 || (pool.LifecycleState != "ACTIVE" && pool.LifecycleState != "DRAINING" && pool.LifecycleState != "DISABLED") {
		return errors.New("complete Placement Pool binding is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "placement-pool/"+pool.PoolID); err != nil {
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
			return ErrPlacementConflict
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
		if err := lockHostAuthorityTx(ctx, tx, membership.HostID); err != nil {
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
			return ErrPlacementConflict
		}
		return nil
	})
}

// DryEvaluatePlacement uses a read-only repeatable-read transaction. It never
// writes a decision, reservation, Outbox intent, or backend side effect.
func DryEvaluatePlacement(ctx context.Context, db TxBeginner, request PlacementAdmissionRequest, hostID string) (placement.Evaluation, error) {
	if err := validatePlacementAdmissionRequest(request, hostID); err != nil {
		return placement.Evaluation{}, err
	}
	request.PCI = normalizePlacementPCIRequirements(request.PCI)
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
	if err := validatePlacementAdmissionRequest(request, dry.HostID); err != nil {
		return PlacementAdmission{}, err
	}
	request.PCI = normalizePlacementPCIRequirements(request.PCI)
	pciRequirementsPayload, pciRequirementsDigest, err := placementPCIRequirementsPayload(request.PCI)
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
		var existing PlacementAdmission
		var existingProjectID, existingWorkloadID, existingImageID, existingFlavorID, existingPCIDigest string
		err := tx.QueryRow(ctx, `
			SELECT decision.admission_id, claim.allocation_id, decision.request_id,
			       decision.request_digest, decision.host_id, decision.pool_id,
			       decision.evaluation_digest, decision.project_id, decision.workload_id,
			       decision.image_id, decision.flavor_id, decision.pci_requirements_digest
			FROM kim.placement_admission_decisions decision
			JOIN kim.compute_allocation_claims claim ON claim.admission_id=decision.admission_id
			WHERE decision.request_id=$1
		`, request.RequestID).Scan(&existing.AdmissionID, &existing.AllocationID, &existing.RequestID,
			&existing.RequestDigest, &existing.HostID, &existing.PoolID, &existing.EvaluationDigest,
			&existingProjectID, &existingWorkloadID, &existingImageID, &existingFlavorID, &existingPCIDigest)
		if err == nil {
			if existing.RequestDigest != dry.RequestDigest || existingProjectID != request.ProjectID || existingWorkloadID != request.WorkloadID || existingImageID != request.ImageID || existingFlavorID != request.FlavorID || existing.PoolID != request.PoolID || existingPCIDigest != pciRequirementsDigest {
				return ErrPlacementConflict
			}
			admission = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		for _, lockKey := range []string{"image/" + request.ImageID, "flavor/" + request.FlavorID, "placement-pool/" + request.PoolID} {
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
		current, err := evaluatePlacementTx(ctx, tx, request, dry.HostID)
		if err != nil {
			return err
		}
		if !current.Eligible {
			return ErrPlacementIneligible
		}
		if current.RequestDigest != dry.RequestDigest || current.EvaluationDigest != dry.EvaluationDigest {
			return ErrPlacementStale
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
				pool_policy_id, pool_policy_generation, membership_generation,
				image_id, image_revision, flavor_id,
				flavor_revision, flavor_shape_digest, capability_generation,
				baseline_assignment_generation, preflight_generation,
				compliance_generation, pci_requirements, pci_requirements_digest,
				decision_state, explanation
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,'ACCEPTED',$24)
		`, admission.AdmissionID, request.RequestID, current.RequestDigest, current.EvaluationDigest,
			request.ProjectID, request.WorkloadID, current.HostID, current.PoolID,
			current.PoolGeneration, current.PoolPolicyID, current.PoolPolicyGeneration,
			current.MembershipGeneration, current.ImageID, current.ImageRevision,
			current.FlavorID, current.FlavorRevision, current.FlavorShapeDigest,
			current.CapabilityGeneration, current.BaselineAssignmentGeneration,
			current.PreflightGeneration, current.ComplianceGeneration,
			pciRequirementsPayload, pciRequirementsDigest, explanation)
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
	}
	var authority placement.AuthoritySnapshot
	var snapshotPayload []byte
	err = row.QueryRow(ctx, `
		SELECT database.mode, capability.host_id, membership.pool_id,
		       pool.pool_generation, pool.policy_id, pool.policy_generation,
		       membership.membership_generation,
		       pool.lifecycle_state, membership.membership_state,
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
		JOIN kim.host_placement_pool_memberships_current membership ON membership.host_id=capability.host_id
		JOIN kim.placement_pools_current pool ON pool.pool_id=membership.pool_id
		LEFT JOIN LATERAL (
			SELECT sum(vcpus) vcpus, sum(memory_mib) memory_mib
			FROM kim.compute_allocation_claims
			WHERE host_id=capability.host_id
			  AND claim_state IN ('RESERVED','ALLOCATED','RELEASE_PENDING')
		) claims ON true
		WHERE database.singleton AND capability.host_id=$1
	`, hostID).Scan(&authority.DatabaseMode, &authority.HostID, &authority.PoolID,
		&authority.PoolGeneration, &authority.PoolPolicyID, &authority.PoolPolicyGeneration,
		&authority.MembershipGeneration, &authority.PoolState,
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
	authority.Inventory, err = agentinventory.DecodeSnapshot(snapshotPayload)
	if err != nil {
		return placement.Evaluation{}, err
	}
	return placement.Evaluate(evaluationRequest, authority)
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
	return nil
}

func normalizePlacementPCIRequirements(requirements []placement.PCIRequirement) []placement.PCIRequirement {
	normalized := append([]placement.PCIRequirement(nil), requirements...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].DeviceAddress < normalized[j].DeviceAddress })
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
