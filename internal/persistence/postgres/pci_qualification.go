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
)

var (
	ErrPCIQualificationBlocked = errors.New("PCI qualification is not current")
	ErrPCIAllocationConflict   = errors.New("PCI device allocation conflict")
)

type PCIQualificationEvidence struct {
	QualificationID     string
	Revision            uint64
	HostID              string
	DeviceAddress       string
	ProfileRevision     string
	TestArtifactDigest  string
	EvaluatorDigest     string
	ObservedGeneration  uint64
	ObservationDigest   string
	BindingFingerprint  map[string]string
	ValidatedOperations []string
	EvidenceState       string
}

type PCIQualificationBindingRequest struct {
	HostID                    string
	DeviceAddress             string
	QualificationID           string
	Revision                  uint64
	CurrentGeneration         uint64
	CurrentObservationDigest  string
	CurrentBindingFingerprint map[string]string
}

type PCIVFClaimRequest struct {
	ClaimID, HostID, DeviceAddress, ProjectID, WorkloadID string
	PlacementAdmissionID                                  string
	PolicyID, QualificationID                             string
	PolicyGeneration, HostCapabilityGeneration            uint64
	QualificationRevision                                 uint64
	RequiredNUMANodeID                                    *int
	RequiredIOMMUGroup                                    string
}

type PCIAllocationDecision struct {
	State      string
	ReasonCode string
}

type QueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// EvaluatePCIAllocationState is a read-only projection. ClaimQualifiedVF
// repeats every authority-bearing check transactionally before mutation.
func EvaluatePCIAllocationState(ctx context.Context, db QueryRower, hostID, deviceAddress, policyID string) (PCIAllocationDecision, error) {
	var hostGeneration, deviceGeneration uint64
	var hostState, observationState, relationshipState string
	var bindingState, bindingProfile, policyState, policyProfile string
	var bindingGeneration uint64
	var assignmentQualified bool
	var activeClaims int
	var pfAddress string
	err := db.QueryRow(ctx, `
		SELECT c.observation_generation, p.observation_generation,
		       c.projection_state, p.observation_state, p.relationship_state,
		       COALESCE(b.binding_state,''), COALESCE(b.qualification_profile_revision,''),
		       COALESCE(b.observed_generation,0), COALESCE(a.policy_state,''),
		       COALESCE(a.qualification_profile_revision,''), COALESCE(p.pf_address,''),
		       COALESCE('VF_ASSIGN' = ANY(e.validated_operations),false),
		       (SELECT count(*) FROM kim.pci_vf_allocation_claims x
		        WHERE x.host_id=p.host_id AND x.device_address=p.device_address
		          AND x.claim_state IN ('ACTIVE','RELEASE_PENDING'))
		FROM kim.host_capability_projections c
		JOIN kim.host_pci_device_projections p ON p.host_id=c.host_id
		LEFT JOIN kim.pci_qualification_bindings_current b
		  ON b.host_id=p.host_id AND b.device_address=p.device_address
		LEFT JOIN kim.pci_allocation_policy_bindings a
		  ON a.host_id=p.host_id AND a.policy_id=$3
		LEFT JOIN kim.pci_qualification_evidence e
		  ON e.qualification_id=b.qualification_id AND e.qualification_revision=b.qualification_revision
		WHERE p.host_id=$1 AND p.device_address=$2
	`, hostID, deviceAddress, policyID).Scan(&hostGeneration, &deviceGeneration, &hostState, &observationState, &relationshipState, &bindingState, &bindingProfile, &bindingGeneration, &policyState, &policyProfile, &pfAddress, &assignmentQualified, &activeClaims)
	if errors.Is(err, pgx.ErrNoRows) {
		return PCIAllocationDecision{State: "UNKNOWN", ReasonCode: "device_observation_missing"}, nil
	}
	if err != nil {
		return PCIAllocationDecision{}, err
	}
	if activeClaims > 0 {
		return PCIAllocationDecision{State: "CLAIMED", ReasonCode: "active_claim_exists"}, nil
	}
	if hostGeneration != deviceGeneration || hostState != "CURRENT" || observationState != "AVAILABLE" || relationshipState != "AVAILABLE" || pfAddress == "" {
		return PCIAllocationDecision{State: "BLOCKED", ReasonCode: "current_observation_not_eligible"}, nil
	}
	if bindingState != "CURRENT" || bindingGeneration != hostGeneration || !assignmentQualified {
		return PCIAllocationDecision{State: "BLOCKED", ReasonCode: "qualification_not_current"}, nil
	}
	if policyState != "ALLOWED" || policyProfile != bindingProfile {
		return PCIAllocationDecision{State: "BLOCKED", ReasonCode: "allocation_policy_not_allowed"}, nil
	}
	return PCIAllocationDecision{State: "AVAILABLE", ReasonCode: "qualified_and_policy_allowed"}, nil
}

func RecordPCIQualificationEvidence(ctx context.Context, db TxBeginner, evidence PCIQualificationEvidence) error {
	bindingPayload, bindingDigest, operations, err := normalizeQualificationEvidence(evidence)
	if err != nil {
		return err
	}
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, evidence.HostID+"/"+evidence.DeviceAddress); err != nil {
			return err
		}
		if evidence.EvidenceState == "QUALIFIED" {
			var generation uint64
			var observationDigest, observationState, relationshipState, iommuGroup string
			var pfAddress string
			if err := tx.QueryRow(ctx, `
				SELECT c.observation_generation, p.observation_digest,
				       p.observation_state, p.relationship_state,
				       COALESCE(p.iommu_group,''), COALESCE(p.pf_address,'')
				FROM kim.host_capability_projections c
				JOIN kim.host_pci_device_projections p ON p.host_id=c.host_id
				WHERE p.host_id=$1 AND p.device_address=$2
				  AND p.observation_generation=c.observation_generation
				  AND c.projection_state='CURRENT'
			`, evidence.HostID, evidence.DeviceAddress).Scan(&generation, &observationDigest, &observationState, &relationshipState, &iommuGroup, &pfAddress); err != nil {
				return ErrPCIQualificationBlocked
			}
			if generation != evidence.ObservedGeneration || observationDigest != evidence.ObservationDigest || observationState != "AVAILABLE" || relationshipState != "AVAILABLE" || iommuGroup == "" || pfAddress == "" {
				return ErrPCIQualificationBlocked
			}
		}
		_, err := tx.Exec(ctx, `
		INSERT INTO kim.pci_qualification_evidence (
			qualification_id, qualification_revision, host_id, device_address,
			qualification_profile_revision, test_artifact_digest, evaluator_digest,
			observed_generation, observation_digest, binding_fingerprint,
			binding_digest, validated_operations, evidence_state
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		`, evidence.QualificationID, evidence.Revision, evidence.HostID, evidence.DeviceAddress,
			evidence.ProfileRevision, evidence.TestArtifactDigest, evidence.EvaluatorDigest,
			evidence.ObservedGeneration, evidence.ObservationDigest, bindingPayload,
			bindingDigest, operations, evidence.EvidenceState)
		return err
	})
	if err != nil {
		return fmt.Errorf("record PCI qualification evidence: %w", err)
	}
	return nil
}

func RefreshPCIQualificationBinding(ctx context.Context, db TxBeginner, request PCIQualificationBindingRequest) (string, error) {
	_, currentDigest, err := canonicalStringMap(request.CurrentBindingFingerprint)
	if err != nil {
		return "", err
	}
	state, reason := "CURRENT", "binding_matches_current_observation"
	err = pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, request.HostID+"/"+request.DeviceAddress); err != nil {
			return err
		}
		var evidenceState, profileRevision, expectedObservationDigest, expectedBindingDigest string
		var expectedGeneration uint64
		if err := tx.QueryRow(ctx, `
			SELECT evidence_state, qualification_profile_revision, observed_generation,
			       observation_digest, binding_digest
			FROM kim.pci_qualification_evidence
			WHERE qualification_id=$1 AND qualification_revision=$2
			  AND host_id=$3 AND device_address=$4
		`, request.QualificationID, request.Revision, request.HostID, request.DeviceAddress).Scan(&evidenceState, &profileRevision, &expectedGeneration, &expectedObservationDigest, &expectedBindingDigest); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				state, reason = "UNKNOWN", "qualification_evidence_missing"
				return nil
			}
			return err
		}
		var revocationRevision uint64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(max(qualification_revision),0)
			FROM kim.pci_qualification_evidence
			WHERE qualification_id=$1 AND evidence_state='REVOKED'
		`, request.QualificationID).Scan(&revocationRevision); err != nil {
			return err
		}
		var actualGeneration uint64
		var actualObservationDigest string
		var hostCapabilityGeneration uint64
		var hostProjectionState string
		if err := tx.QueryRow(ctx, `
			SELECT p.observation_generation, p.observation_digest,
			       c.observation_generation, c.projection_state
			FROM kim.host_pci_device_projections p
			JOIN kim.host_capability_projections c ON c.host_id=p.host_id
			WHERE p.host_id=$1 AND p.device_address=$2
		`, request.HostID, request.DeviceAddress).Scan(
			&actualGeneration, &actualObservationDigest,
			&hostCapabilityGeneration, &hostProjectionState,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				state, reason = "UNKNOWN", "current_device_observation_missing"
				return nil
			}
			return err
		}
		switch {
		case evidenceState == "REVOKED" || revocationRevision >= request.Revision:
			state, reason = "REVOKED", "qualification_evidence_revoked"
		case evidenceState != "QUALIFIED":
			state, reason = "UNKNOWN", "qualification_not_successful"
		case hostProjectionState != "CURRENT" || actualGeneration != hostCapabilityGeneration:
			state, reason = "STALE", "host_capability_generation_changed"
		case expectedGeneration != actualGeneration || request.CurrentGeneration != actualGeneration:
			state, reason = "STALE", "observation_generation_changed"
		case expectedObservationDigest != actualObservationDigest || request.CurrentObservationDigest != actualObservationDigest:
			state, reason = "STALE", "device_observation_changed"
		case expectedBindingDigest != currentDigest:
			state, reason = "STALE", "software_stack_binding_changed"
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO kim.pci_qualification_bindings_current (
				host_id, device_address, qualification_id, qualification_revision,
				qualification_profile_revision, observed_generation, observation_digest,
				current_binding_digest, binding_state, reason_code
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (host_id, device_address) DO UPDATE SET
				qualification_id=EXCLUDED.qualification_id,
				qualification_revision=EXCLUDED.qualification_revision,
				qualification_profile_revision=EXCLUDED.qualification_profile_revision,
				observed_generation=EXCLUDED.observed_generation,
				observation_digest=EXCLUDED.observation_digest,
				current_binding_digest=EXCLUDED.current_binding_digest,
				binding_state=EXCLUDED.binding_state,
				reason_code=EXCLUDED.reason_code,
				evaluated_at=statement_timestamp()
		`, request.HostID, request.DeviceAddress, request.QualificationID, request.Revision,
			profileRevision, actualGeneration, actualObservationDigest, currentDigest, state, reason)
		return err
	})
	return state, err
}

func ClaimQualifiedVF(ctx context.Context, db TxBeginner, request PCIVFClaimRequest) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return claimQualifiedVFTx(ctx, tx, request)
	})
}

func claimQualifiedVFTx(ctx context.Context, tx pgx.Tx, request PCIVFClaimRequest) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, request.HostID+"/"+request.DeviceAddress); err != nil {
		return err
	}
	var databaseMode string
	if err := tx.QueryRow(ctx, `SELECT mode FROM kim.database_authority WHERE singleton`).Scan(&databaseMode); err != nil || databaseMode != "ACTIVE" {
		return ErrPCIQualificationBlocked
	}
	var generation uint64
	var projectionState, observationState, relationshipState, iommuGroup string
	var numaNodeID int
	var pfAddress string
	var vfIndex int
	if err := tx.QueryRow(ctx, `
			SELECT c.observation_generation, c.projection_state,
			       p.observation_state, p.relationship_state, p.numa_node_id,
			       COALESCE(p.iommu_group,''), COALESCE(p.pf_address,''), COALESCE(p.vf_index,-1)
			FROM kim.host_capability_projections c
			JOIN kim.host_pci_device_projections p ON p.host_id=c.host_id
			WHERE c.host_id=$1 AND p.device_address=$2
		`, request.HostID, request.DeviceAddress).Scan(&generation, &projectionState, &observationState, &relationshipState, &numaNodeID, &iommuGroup, &pfAddress, &vfIndex); err != nil {
		return ErrPCIQualificationBlocked
	}
	if generation != request.HostCapabilityGeneration || projectionState != "CURRENT" || observationState != "AVAILABLE" || relationshipState != "AVAILABLE" || pfAddress == "" || vfIndex < 0 {
		return ErrPCIQualificationBlocked
	}
	var bindingState, bindingProfile string
	var bindingGeneration uint64
	if err := tx.QueryRow(ctx, `
			SELECT binding_state, observed_generation, qualification_profile_revision
			FROM kim.pci_qualification_bindings_current
			WHERE host_id=$1 AND device_address=$2
			  AND qualification_id=$3 AND qualification_revision=$4
		`, request.HostID, request.DeviceAddress, request.QualificationID, request.QualificationRevision).Scan(&bindingState, &bindingGeneration, &bindingProfile); err != nil || bindingState != "CURRENT" || bindingGeneration != generation {
		return ErrPCIQualificationBlocked
	}
	var policyState, policyProfile string
	var policyGeneration uint64
	if err := tx.QueryRow(ctx, `
			SELECT policy_state, policy_generation, qualification_profile_revision
			FROM kim.pci_allocation_policy_bindings
			WHERE host_id=$1 AND policy_id=$2
		`, request.HostID, request.PolicyID).Scan(&policyState, &policyGeneration, &policyProfile); err != nil || policyState != "ALLOWED" || policyGeneration != request.PolicyGeneration || policyProfile != bindingProfile {
		return ErrPCIQualificationBlocked
	}
	var assignmentQualified bool
	if err := tx.QueryRow(ctx, `
			SELECT 'VF_ASSIGN' = ANY(validated_operations)
			FROM kim.pci_qualification_evidence
			WHERE qualification_id=$1 AND qualification_revision=$2 AND evidence_state='QUALIFIED'
		`, request.QualificationID, request.QualificationRevision).Scan(&assignmentQualified); err != nil || !assignmentQualified {
		return ErrPCIQualificationBlocked
	}
	if request.RequiredNUMANodeID != nil && numaNodeID != *request.RequiredNUMANodeID {
		return ErrPCIQualificationBlocked
	}
	if request.RequiredIOMMUGroup != "" && iommuGroup != request.RequiredIOMMUGroup {
		return ErrPCIQualificationBlocked
	}
	var active int
	if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM kim.pci_vf_allocation_claims
			WHERE host_id=$1 AND device_address=$2 AND claim_state IN ('ACTIVE','RELEASE_PENDING')
		`, request.HostID, request.DeviceAddress).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return ErrPCIAllocationConflict
	}
	_, err := tx.Exec(ctx, `
			INSERT INTO kim.pci_vf_allocation_claims (
				claim_id, host_id, device_address, project_id, workload_id,
				policy_id, policy_generation, host_capability_generation,
				qualification_id, qualification_revision, placement_admission_id, claim_state,
				allocation_generation
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),'ACTIVE',1)
		`, request.ClaimID, request.HostID, request.DeviceAddress, request.ProjectID, request.WorkloadID,
		request.PolicyID, request.PolicyGeneration, request.HostCapabilityGeneration,
		request.QualificationID, request.QualificationRevision, request.PlacementAdmissionID)
	if err != nil {
		return fmt.Errorf("commit qualified VF claim: %w", err)
	}
	return nil
}

func normalizeQualificationEvidence(evidence PCIQualificationEvidence) ([]byte, string, []string, error) {
	if evidence.QualificationID == "" || evidence.Revision == 0 || evidence.HostID == "" || evidence.DeviceAddress == "" || evidence.ProfileRevision == "" || evidence.ObservedGeneration == 0 || !validDigestString(evidence.TestArtifactDigest) || !validDigestString(evidence.EvaluatorDigest) || !validDigestString(evidence.ObservationDigest) || (evidence.EvidenceState != "QUALIFIED" && evidence.EvidenceState != "REJECTED" && evidence.EvidenceState != "REVOKED") {
		return nil, "", nil, errors.New("complete PCI qualification evidence is required")
	}
	payload, digest, err := canonicalStringMap(evidence.BindingFingerprint)
	if err != nil {
		return nil, "", nil, err
	}
	operations := append(make([]string, 0, len(evidence.ValidatedOperations)), evidence.ValidatedOperations...)
	sort.Strings(operations)
	for index, operation := range operations {
		if operation == "" || (index > 0 && operations[index-1] == operation) {
			return nil, "", nil, errors.New("validated PCI operations must be non-empty and unique")
		}
	}
	if evidence.EvidenceState == "QUALIFIED" && len(operations) == 0 {
		return nil, "", nil, errors.New("qualified PCI evidence requires validated operations")
	}
	if evidence.EvidenceState == "QUALIFIED" {
		for _, required := range []string{"device", "driver", "firmware", "kernel", "iommu", "libvirt_qemu"} {
			if evidence.BindingFingerprint[required] == "" {
				return nil, "", nil, fmt.Errorf("qualified PCI evidence requires binding field %s", required)
			}
		}
	}
	return payload, digest, operations, nil
}

func canonicalStringMap(value map[string]string) ([]byte, string, error) {
	if len(value) == 0 {
		return nil, "", errors.New("PCI qualification binding fingerprint is required")
	}
	for key, item := range value {
		if key == "" || item == "" {
			return nil, "", errors.New("PCI qualification binding fingerprint fields must be non-empty")
		}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func validDigestString(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
