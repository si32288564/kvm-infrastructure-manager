package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

func EvaluateHostEvacuationSourceStorageSafety(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, evidenceID string) error {
	if evidenceID == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var childGeneration, vmGeneration, materializationGeneration, bindingGeneration, attachmentGeneration, rootObservationGeneration, powerGeneration uint64
		var vmID, sourceHost, sourcePlan, quiescenceID, volumeID, bindingID, attachmentID, rootEvidenceID, powerEvidenceID, rootLV, observedLV string
		err := tx.QueryRow(ctx, `SELECT c.child_generation,e.vm_id::text,e.vm_generation,e.source_host_id,e.source_plan_id,e.source_materialization_generation,
			q.quiescence_evidence_id,plan.root_volume_id,plan.root_binding_id,plan.root_binding_generation,plan.root_attachment_id,plan.root_attachment_generation,
			o.evidence_id,o.observation_generation,p.evidence_id,p.observation_generation,b.lv_uuid,o.observed_lv_uuid
			FROM kim.host_evacuation_workloads_current c
			JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.planned_source_quiescence_evidence q ON q.child_operation_id=c.child_operation_id AND q.child_generation=c.child_generation
			JOIN kim.planned_source_quiescence_execution_evidence qe ON qe.quiescence_evidence_id=q.quiescence_evidence_id
			JOIN kim.vm_materialization_plan_evidence plan ON plan.plan_id=e.source_plan_id AND plan.vm_id=e.vm_id AND plan.vm_generation=e.vm_generation AND plan.host_id=e.source_host_id
			JOIN kim.volume_backend_bindings_current b ON b.binding_id=plan.root_binding_id AND b.binding_generation=plan.root_binding_generation AND b.volume_id=plan.root_volume_id AND b.host_id=e.source_host_id AND b.binding_state='BOUND'
			JOIN kim.volume_attachment_observations_current oc ON oc.attachment_id=plan.root_attachment_id AND oc.attachment_generation=plan.root_attachment_generation AND oc.binding_id=plan.root_binding_id AND oc.binding_generation=plan.root_binding_generation AND oc.host_id=e.source_host_id AND oc.domain_uuid=e.vm_id AND oc.target_device='vda' AND oc.device_present AND NOT oc.holder_open
			JOIN kim.volume_attachment_observation_evidence o ON o.evidence_id=oc.evidence_id AND o.observation_generation=oc.observation_generation AND o.evidence_state='MATCHED' AND o.device_identity_matches AND o.source_identity_matches AND o.observed_lv_uuid=b.lv_uuid AND NOT o.holder_open
			JOIN kim.vm_power_state_current pc ON pc.vm_id=e.vm_id AND pc.vm_generation=e.vm_generation AND pc.observed_power_state='SHUTOFF' AND pc.convergence_state='MATCHED'
			JOIN kim.vm_power_observation_evidence p ON p.evidence_id=pc.evidence_id AND p.observation_generation=pc.observation_generation AND p.host_id=e.source_host_id AND p.observed_power_state='SHUTOFF'
			JOIN kim.host_evacuation_operation_evidence operation ON operation.evacuation_operation_id=c.evacuation_operation_id
			JOIN kim.host_operation_authorities_current hoa ON hoa.host_id=e.source_host_id AND hoa.authority_state='ARMED' AND hoa.authority_generation=operation.source_host_authority_generation
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' FOR UPDATE OF c,b,oc,pc`, claim.ChildOperationID).Scan(&childGeneration, &vmID, &vmGeneration, &sourceHost, &sourcePlan, &materializationGeneration, &quiescenceID, &volumeID, &bindingID, &bindingGeneration, &attachmentID, &attachmentGeneration, &rootEvidenceID, &rootObservationGeneration, &powerEvidenceID, &powerGeneration, &rootLV, &observedLV)
		if err != nil || rootLV != observedLV {
			return ErrHostEvacuationBlocked
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%d/%s/%d", claim.ChildOperationID, quiescenceID, sourcePlan, rootEvidenceID, rootObservationGeneration, powerEvidenceID, powerGeneration)))
		_, err = tx.Exec(ctx, `INSERT INTO kim.host_evacuation_source_storage_safety_evidence(safety_evidence_id,child_operation_id,child_generation,quiescence_evidence_id,vm_id,vm_generation,source_host_id,source_plan_id,source_materialization_generation,root_volume_id,root_binding_id,root_binding_generation,root_attachment_id,root_attachment_generation,root_observation_evidence_id,root_observation_generation,power_observation_evidence_id,power_observation_generation,safety_state,safety_digest) VALUES($1,$2,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'SAFE',$19)`, evidenceID, claim.ChildOperationID, childGeneration, quiescenceID, vmID, vmGeneration, sourceHost, sourcePlan, materializationGeneration, volumeID, bindingID, bindingGeneration, attachmentID, attachmentGeneration, rootEvidenceID, rootObservationGeneration, powerEvidenceID, powerGeneration, digest)
		return err
	})
}

func ReleaseHostEvacuationSourcePlacement(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, releaseID, safetyID string) error {
	if releaseID == "" || safetyID == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var admissionID, allocationID string
		var childGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT c.child_generation,e.source_admission_id,a.allocation_id FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id) JOIN kim.host_evacuation_source_storage_safety_evidence s ON s.safety_evidence_id=$2 AND s.child_operation_id=c.child_operation_id JOIN kim.compute_allocation_claims a ON a.admission_id=e.source_admission_id AND a.claim_state IN ('RESERVED','ALLOCATED') WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' FOR UPDATE OF c,a`, claim.ChildOperationID, safetyID).Scan(&childGeneration, &admissionID, &allocationID); err != nil {
			return ErrHostEvacuationBlocked
		}
		var requiredNetwork, quiescedNetwork int
		if err := tx.QueryRow(ctx, `SELECT jsonb_array_length(e.network_requirements),count(q.evidence_id)
			FROM kim.host_evacuation_workload_evidence e
			LEFT JOIN kim.host_evacuation_source_network_retirement_authority_evidence a ON a.child_operation_id=e.child_operation_id AND a.child_generation=e.child_generation
			LEFT JOIN kim.network_port_source_quiescence_evidence q ON q.port_id=a.port_id AND q.port_generation=a.source_port_generation AND q.source_binding_generation=a.source_binding_generation AND q.source_host_id=e.source_host_id AND q.quiescence_state='QUIESCED'
			WHERE e.child_operation_id=$1 GROUP BY e.network_requirements`, claim.ChildOperationID).Scan(&requiredNetwork, &quiescedNetwork); err != nil || requiredNetwork != quiescedNetwork {
			return ErrHostEvacuationBlocked
		}
		digest := digestReleaseBytes([]byte(claim.ChildOperationID + "/" + admissionID + "/" + allocationID + "/" + safetyID))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_source_placement_release_evidence(release_evidence_id,child_operation_id,child_generation,source_admission_id,source_compute_allocation_id,source_storage_safety_evidence_id,release_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, releaseID, claim.ChildOperationID, childGeneration, admissionID, allocationID, safetyID, digest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.compute_allocation_claims SET claim_state='RELEASED',released_at=statement_timestamp() WHERE allocation_id=$1`, allocationID)
		return err
	})
}

func BuildHostEvacuationDestinationPlacementRequest(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, requestID, destinationHostID string) (PlacementAdmissionRequest, error) {
	var out PlacementAdmissionRequest
	if requestID == "" || destinationHostID == "" {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var sourceHost, classID, accessMode, backendID, vgUUID string
		var sourceNetworkRaw []byte
		var classRevision, sizeBytes, fencingRevision, backendGeneration, capacityGeneration, attachmentGeneration uint64
		var bootable bool
		if err := tx.QueryRow(ctx, `SELECT a.project_id,a.workload_id,a.image_id,a.flavor_id,a.pool_id,e.source_host_id,e.network_requirements,v.storage_class_id,v.storage_class_revision,v.size_bytes,v.access_mode,v.bootable,ce.fencing_policy_revision,b.backend_id,b.vg_uuid,b.backend_generation,p.capacity_generation
			FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.placement_admission_decisions a ON a.admission_id=e.source_admission_id
			JOIN kim.volumes_current v ON v.placement_admission_id=a.admission_id AND v.bootable
			JOIN kim.storage_class_revision_evidence ce ON ce.storage_class_id=v.storage_class_id AND ce.class_revision=v.storage_class_revision
			JOIN kim.storage_backends_current b ON b.host_id=$2 AND b.backend_type='LOCAL_LVM' AND b.lifecycle_state='ACTIVE' AND b.capability_state='CURRENT'
			JOIN kim.storage_capacity_projections_current p ON p.backend_id=b.backend_id AND p.projection_state='CURRENT'
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' AND b.host_id<>e.source_host_id`, claim.ChildOperationID, destinationHostID).Scan(&out.ProjectID, &out.WorkloadID, &out.ImageID, &out.FlavorID, &out.PoolID, &sourceHost, &sourceNetworkRaw, &classID, &classRevision, &sizeBytes, &accessMode, &bootable, &fencingRevision, &backendID, &vgUUID, &backendGeneration, &capacityGeneration); err != nil {
			return ErrHostEvacuationBlocked
		}
		attachmentGeneration = 1
		out.RequestID = requestID
		out.Storage = []placement.StorageRequirement{{VolumeID: requestID + ":root", AttachmentID: requestID + ":root-attachment", BackendID: backendID, BackendGeneration: backendGeneration, VGUUID: vgUUID, StorageClassID: classID, StorageClassRevision: classRevision, CapacityGeneration: capacityGeneration, AttachmentGeneration: attachmentGeneration, FencingPolicyRevision: fencingRevision, SizeBytes: sizeBytes, AccessMode: accessMode, Bootable: bootable}}
		if err := json.Unmarshal(sourceNetworkRaw, &out.Network); err != nil {
			return ErrHostEvacuationBlocked
		}
		for index := range out.Network {
			required := &out.Network[index]
			var evidenceID, evidenceDigest, ip, mac string
			var sourcePortGeneration, sourceBindingGeneration uint64
			if err := tx.QueryRow(ctx, `SELECT q.evidence_id,q.evidence_digest,host(ip.ip_address),mac.mac_address::text,a.source_port_generation,a.source_binding_generation
				FROM kim.host_evacuation_source_network_retirement_authority_evidence a
				JOIN kim.network_port_source_quiescence_evidence q ON q.port_id=a.port_id AND q.port_generation=a.source_port_generation AND q.source_binding_generation=a.source_binding_generation AND q.source_host_id=$3 AND q.quiescence_state='QUIESCED'
				JOIN kim.network_identity_claims ip ON ip.port_id=a.port_id AND ip.claim_type='IP' AND ip.claim_state IN ('RESERVED','ACTIVE')
				JOIN kim.network_identity_claims mac ON mac.port_id=a.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE')
				WHERE a.child_operation_id=$1 AND a.port_id=$2`, claim.ChildOperationID, required.PortID, sourceHost).Scan(&evidenceID, &evidenceDigest, &ip, &mac, &sourcePortGeneration, &sourceBindingGeneration); err != nil {
				return ErrHostEvacuationBlocked
			}
			required.AllocationSource, required.IPAddress, required.MACAddress = "EXPLICIT", ip, mac
			required.HandoffID = fmt.Sprintf("evacuation-handoff:%s:%d", requestID, index+1)
			required.SourceHostID, required.SourceQuiescenceEvidenceID, required.SourceQuiescenceEvidenceDigest = sourceHost, evidenceID, evidenceDigest
			required.SourcePortGeneration, required.SourceBindingGeneration = sourcePortGeneration, sourceBindingGeneration
			required.DestinationPortGeneration, required.DestinationBindingGeneration = sourcePortGeneration+1, sourceBindingGeneration+1
		}
		return nil
	})
	return out, err
}

func AuthorizeHostEvacuationRelocation(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, authorityID, destinationAdmissionID, safetyID, releaseID string) error {
	if authorityID == "" || destinationAdmissionID == "" || safetyID == "" || releaseID == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var childGeneration, vmGeneration, sourceMaterialization uint64
		var vmID, workloadID, sourceHost, sourceAdmission, sourcePlan, quiescenceID, destinationHost string
		var sourceStorageDigest, sourceNetworkDigest, sourcePCIDigest, destinationStorageDigest, destinationNetworkDigest, destinationPCIDigest string
		var sourceNetworkRaw, destinationNetworkRaw []byte
		if err := tx.QueryRow(ctx, `SELECT c.child_generation,e.vm_id::text,e.vm_generation,e.workload_id,e.source_host_id,e.source_admission_id,e.source_plan_id,e.source_materialization_generation,s.quiescence_evidence_id,d.host_id,
			sa.storage_requirements_digest,sa.network_requirements_digest,sa.pci_requirements_digest,d.storage_requirements_digest,d.network_requirements_digest,d.pci_requirements_digest
			FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.placement_admission_decisions sa ON sa.admission_id=e.source_admission_id
			JOIN kim.host_evacuation_source_storage_safety_evidence s ON s.safety_evidence_id=$3 AND s.child_operation_id=c.child_operation_id
			JOIN kim.host_evacuation_source_placement_release_evidence r ON r.release_evidence_id=$4 AND r.child_operation_id=c.child_operation_id AND r.source_storage_safety_evidence_id=s.safety_evidence_id
			JOIN kim.placement_admission_decisions d ON d.admission_id=$2 AND d.workload_id=e.workload_id AND d.host_id<>e.source_host_id AND d.decision_state='ACCEPTED'
			JOIN kim.host_evacuation_operation_evidence operation ON operation.evacuation_operation_id=c.evacuation_operation_id
			JOIN kim.host_operation_authorities_current hoa ON hoa.host_id=e.source_host_id AND hoa.authority_state='ARMED' AND hoa.authority_generation=operation.source_host_authority_generation
			JOIN kim.host_placement_drains_current drain ON drain.source_host_id=e.source_host_id AND drain.drain_state='DRAINING'
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' AND jsonb_array_length(e.pci_requirements)=0 AND jsonb_array_length(d.pci_requirements)=0`, claim.ChildOperationID, destinationAdmissionID, safetyID, releaseID).Scan(&childGeneration, &vmID, &vmGeneration, &workloadID, &sourceHost, &sourceAdmission, &sourcePlan, &sourceMaterialization, &quiescenceID, &destinationHost, &sourceStorageDigest, &sourceNetworkDigest, &sourcePCIDigest, &destinationStorageDigest, &destinationNetworkDigest, &destinationPCIDigest); err != nil {
			return ErrHostEvacuationBlocked
		}
		if err := tx.QueryRow(ctx, `SELECT network_requirements FROM kim.host_evacuation_workload_evidence WHERE child_operation_id=$1`, claim.ChildOperationID).Scan(&sourceNetworkRaw); err != nil {
			return ErrHostEvacuationBlocked
		}
		if err := tx.QueryRow(ctx, `SELECT network_requirements FROM kim.placement_admission_decisions WHERE admission_id=$1`, destinationAdmissionID).Scan(&destinationNetworkRaw); err != nil {
			return ErrHostEvacuationBlocked
		}
		var sourceNetwork, destinationNetwork []placement.NetworkRequirement
		if json.Unmarshal(sourceNetworkRaw, &sourceNetwork) != nil || json.Unmarshal(destinationNetworkRaw, &destinationNetwork) != nil || !sameEvacuationNetworkIdentity(sourceNetwork, destinationNetwork) {
			return ErrHostEvacuationBlocked
		}
		if len(sourceNetwork) > 0 {
			var closed int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM kim.port_binding_handoff_evidence h
				JOIN kim.network_port_source_quiescence_evidence q ON q.evidence_id=h.source_quiescence_evidence_id AND q.evidence_digest=h.source_quiescence_evidence_digest AND q.quiescence_state='QUIESCED'
				JOIN kim.network_port_binding_retirement_evidence re ON re.evidence_id=q.retirement_evidence_id AND re.retirement_state='VERIFIED'
				JOIN kim.network_ports_current p ON p.port_id=h.port_id AND p.placement_admission_id=h.destination_admission_id AND p.port_generation=h.destination_port_generation
				JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=h.destination_admission_id AND b.host_id=h.destination_host_id AND b.binding_generation=h.destination_binding_generation
				WHERE h.source_admission_id=$1 AND h.destination_admission_id=$2 AND h.source_host_id=$3 AND h.destination_host_id=$4`, sourceAdmission, destinationAdmissionID, sourceHost, destinationHost).Scan(&closed); err != nil || closed != len(sourceNetwork) {
				return ErrHostEvacuationBlocked
			}
		}
		var shapeMatches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.volumes_current s JOIN kim.volumes_current d ON d.placement_admission_id=$2 AND d.bootable WHERE s.placement_admission_id=$1 AND s.bootable AND s.storage_class_id=d.storage_class_id AND s.storage_class_revision=d.storage_class_revision AND s.size_bytes=d.size_bytes AND s.access_mode=d.access_mode)`, sourceAdmission, destinationAdmissionID).Scan(&shapeMatches); err != nil || !shapeMatches {
			return ErrHostEvacuationBlocked
		}
		destinationMaterialization := sourceMaterialization + 1
		sourceRequirements := digestReleaseBytes([]byte(sourceStorageDigest + "/" + sourceNetworkDigest + "/" + sourcePCIDigest))
		destinationRequirements := digestReleaseBytes([]byte(destinationStorageDigest + "/" + destinationNetworkDigest + "/" + destinationPCIDigest))
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%s/%d", claim.ChildOperationID, sourcePlan, destinationAdmissionID, safetyID, releaseID, destinationMaterialization)))
		_, err := tx.Exec(ctx, `INSERT INTO kim.vm_materialization_relocation_authority_evidence(relocation_authority_id,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,source_admission_id,source_plan_id,source_materialization_generation,source_quiescence_evidence_id,source_storage_safety_evidence_id,source_placement_release_evidence_id,destination_host_id,destination_admission_id,destination_materialization_generation,source_requirements_digest,destination_requirements_digest,authority_digest) VALUES($1,$2,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, authorityID, claim.ChildOperationID, childGeneration, vmID, vmGeneration, sourceHost, sourceAdmission, sourcePlan, sourceMaterialization, quiescenceID, safetyID, releaseID, destinationHost, destinationAdmissionID, destinationMaterialization, sourceRequirements, destinationRequirements, digest)
		_ = workloadID
		return err
	})
}

func sameEvacuationNetworkIdentity(source, destination []placement.NetworkRequirement) bool {
	if len(source) != len(destination) {
		return false
	}
	byPort := make(map[string]placement.NetworkRequirement, len(source))
	for _, required := range source {
		byPort[required.PortID] = required
	}
	for _, required := range destination {
		original, ok := byPort[required.PortID]
		if !ok || original.NetworkID != required.NetworkID || original.SubnetID != required.SubnetID || original.SegmentClaimID != required.SegmentClaimID || original.IPAddress != required.IPAddress || original.MACAddress != required.MACAddress || original.BindingType != required.BindingType || original.NetworkGeneration != required.NetworkGeneration || original.SubnetGeneration != required.SubnetGeneration || original.SegmentGeneration != required.SegmentGeneration || original.RequiredMTU != required.RequiredMTU {
			return false
		}
	}
	return true
}
