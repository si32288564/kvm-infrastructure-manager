package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/placement"
)

const (
	PlannedSourceVolumeSafetyReadBackCommandType = "PLANNED_SOURCE_VOLUME_SAFETY_READ_BACK"
	PlannedSourceVolumeSafetyReadBackSchema      = "kim.command.planned-source-volume-safety-read-back/v1"
)

// AcceptPlannedSourceVolumeSafetyObservation records an observation-only
// read-back for an exact attachment in the current source incarnation. Unlike
// the ordinary attachment consumer this deliberately accepts an inactive
// Domain whose configured device is present while no QEMU holder is open.
func AcceptPlannedSourceVolumeSafetyObservation(ctx context.Context, db TxBeginner, observation LocalLVMAttachmentObservation) error {
	if observation.EvidenceID == "" || observation.AttachmentID == "" || observation.VolumeID == "" || observation.BindingID == "" || observation.HostID == "" || observation.DomainUUID == "" || (observation.TargetDevice != "vda" && observation.TargetDevice != "vdb") || observation.ObservedLVUUID == "" || observation.CommandID == "" || observation.VerificationID == "" || observation.AttachmentGeneration < 1 || observation.BindingGeneration < 1 || observation.ObservationGeneration < 1 || observation.AttemptIndex < 1 || len(observation.ObservationDigest) != 64 || len(observation.VerifierDigest) != 64 || observation.EvidenceState != "MATCHED" || !observation.DevicePresent || !observation.DeviceIdentityMatches || !observation.SourceIdentityMatches || observation.HolderOpen || observation.ReadOnly {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, observation.HostID); err != nil {
			return err
		}
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM kim.virtual_machines_current vm
			JOIN kim.volume_attachments_current attachment ON attachment.attachment_id=$1 AND attachment.volume_id=$2 AND attachment.attachment_generation=$3 AND attachment.workload_id=vm.workload_id AND attachment.placement_admission_id=vm.placement_admission_id AND attachment.desired_host_id=vm.host_id AND attachment.desired_state IN ('RESERVED','ATTACHED')
			JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=$4 AND binding.volume_id=attachment.volume_id AND binding.binding_generation=$5 AND binding.host_id=vm.host_id AND binding.binding_state='BOUND' AND binding.lv_uuid=$6
			JOIN kim.command_verification_evidence verification ON verification.verification_id=$7 AND verification.command_id=$8 AND verification.attempt_index=$9 AND verification.observation_generation=$10 AND verification.observation_digest=$11 AND verification.verifier_artifact_digest=$12 AND verification.verification_state='MATCHED'
			JOIN kim.execution_commands command ON command.command_id=verification.command_id AND command.host_id=vm.host_id AND command.command_type=$13 AND command.schema_version=$14 AND command.target_resource_id='attachment:' || attachment.attachment_id
			WHERE vm.vm_id=$15::uuid AND vm.host_id=$16
			AND verification.evidence_payload->>'attachment_id'=attachment.attachment_id
			AND verification.evidence_payload->>'volume_id'=attachment.volume_id
			AND verification.evidence_payload->>'binding_id'=binding.binding_id
			AND verification.evidence_payload->>'domain_uuid'=vm.vm_id::text
			AND verification.evidence_payload->>'target_device'=$17
			AND verification.evidence_payload->>'observed_lv_uuid'=binding.lv_uuid
			AND (verification.evidence_payload->>'device_present')::boolean
			AND (verification.evidence_payload->>'device_identity_matches')::boolean
			AND (verification.evidence_payload->>'source_identity_matches')::boolean
			AND NOT (verification.evidence_payload->>'holder_open')::boolean)`, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration, observation.BindingID, observation.BindingGeneration, observation.ObservedLVUUID, observation.VerificationID, observation.CommandID, observation.AttemptIndex, observation.ObservationGeneration, observation.ObservationDigest, observation.VerifierDigest, PlannedSourceVolumeSafetyReadBackCommandType, PlannedSourceVolumeSafetyReadBackSchema, observation.DomainUUID, observation.HostID, observation.TargetDevice).Scan(&identical); err != nil || !identical {
			return ErrHostEvacuationBlocked
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.volume_attachment_observation_evidence(evidence_id,attachment_id,volume_id,attachment_generation,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,desired_state,device_present,device_identity_matches,source_identity_matches,holder_open,read_only,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,evidence_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8::uuid,$9,$10,'ATTACHED',true,true,true,false,false,$11,$12,$13,$14,$15,$16,'MATCHED') ON CONFLICT(evidence_id) DO NOTHING`, observation.EvidenceID, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration, observation.BindingID, observation.BindingGeneration, observation.HostID, observation.DomainUUID, observation.TargetDevice, observation.ObservedLVUUID, observation.CommandID, observation.AttemptIndex, observation.VerificationID, observation.ObservationGeneration, observation.ObservationDigest, observation.VerifierDigest); err != nil {
			return err
		}
		var currentGeneration uint64
		err := tx.QueryRow(ctx, `SELECT observation_generation FROM kim.volume_attachment_observations_current WHERE attachment_id=$1 FOR UPDATE`, observation.AttachmentID).Scan(&currentGeneration)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && currentGeneration >= observation.ObservationGeneration {
			return ErrHostEvacuationStale
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.volume_attachment_observations_current(attachment_id,volume_id,attachment_generation,observation_generation,evidence_id,attachment_state,binding_id,binding_generation,host_id,domain_uuid,target_device,observed_lv_uuid,device_present,holder_open) VALUES($1,$2,$3,$4,$5,'ATTACHED',$6,$7,$8,$9::uuid,$10,$11,true,false) ON CONFLICT(attachment_id) DO UPDATE SET volume_id=EXCLUDED.volume_id,attachment_generation=EXCLUDED.attachment_generation,observation_generation=EXCLUDED.observation_generation,evidence_id=EXCLUDED.evidence_id,attachment_state='ATTACHED',binding_id=EXCLUDED.binding_id,binding_generation=EXCLUDED.binding_generation,host_id=EXCLUDED.host_id,domain_uuid=EXCLUDED.domain_uuid,target_device=EXCLUDED.target_device,observed_lv_uuid=EXCLUDED.observed_lv_uuid,device_present=true,holder_open=false,updated_at=statement_timestamp()`, observation.AttachmentID, observation.VolumeID, observation.AttachmentGeneration, observation.ObservationGeneration, observation.EvidenceID, observation.BindingID, observation.BindingGeneration, observation.HostID, observation.DomainUUID, observation.TargetDevice, observation.ObservedLVUUID)
		return err
	})
}

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

// EvaluateHostEvacuationSourceStorageSafetySet binds every storage requirement
// to an exact no-holder observation after planned SHUTOFF. The historical root
// safety evidence remains the parent authority; DATA safety is never inferred
// from the root member.
func EvaluateHostEvacuationSourceStorageSafetySet(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, setID, rootSafetyID string) error {
	if setID == "" || rootSafetyID == "" {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT c.child_generation,CASE WHEN COALESCE((requirement.value->>'Bootable')::boolean,false) THEN 0 ELSE 1 END,
			CASE WHEN COALESCE((requirement.value->>'Bootable')::boolean,false) THEN 'ROOT' ELSE 'DATA' END,
			v.volume_id,b.binding_id,b.binding_generation,a.attachment_id,a.attachment_generation,b.lv_uuid,
			o.evidence_id,o.observation_generation
			FROM kim.host_evacuation_workloads_current c
			JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.host_evacuation_source_storage_safety_evidence root ON root.safety_evidence_id=$2 AND root.child_operation_id=c.child_operation_id AND root.child_generation=c.child_generation
			CROSS JOIN LATERAL jsonb_array_elements(e.storage_requirements) WITH ORDINALITY requirement(value,ordinality)
			JOIN kim.volumes_current v ON v.volume_id=requirement.value->>'VolumeID' AND v.placement_admission_id=e.source_admission_id AND v.bootable=COALESCE((requirement.value->>'Bootable')::boolean,false)
			JOIN kim.volume_backend_bindings_current b ON b.volume_id=v.volume_id AND b.host_id=e.source_host_id AND b.binding_state='BOUND' AND b.lv_uuid IS NOT NULL
			JOIN kim.volume_attachments_current a ON a.volume_id=v.volume_id AND a.placement_admission_id=e.source_admission_id AND a.desired_host_id=e.source_host_id
			JOIN kim.volume_attachment_observations_current current ON current.attachment_id=a.attachment_id AND current.attachment_generation=a.attachment_generation AND current.binding_id=b.binding_id AND current.binding_generation=b.binding_generation AND current.host_id=e.source_host_id AND current.domain_uuid=e.vm_id AND current.device_present AND NOT current.holder_open
			JOIN kim.volume_attachment_observation_evidence o ON o.evidence_id=current.evidence_id AND o.observation_generation=current.observation_generation AND o.observed_lv_uuid=b.lv_uuid AND o.evidence_state='MATCHED' AND o.device_identity_matches AND o.source_identity_matches AND NOT o.holder_open
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED'
			ORDER BY v.bootable DESC,v.volume_id FOR UPDATE OF c,v,b,a,current`, claim.ChildOperationID, rootSafetyID)
		if err != nil {
			return err
		}
		type safetyVolume struct {
			childGeneration                                                uint64
			ordinal                                                        int
			role, volumeID, bindingID, attachmentID, lvUUID, observationID string
			bindingGeneration, attachmentGeneration, observationGeneration uint64
		}
		volumes := make([]safetyVolume, 0, 2)
		for rows.Next() {
			var volume safetyVolume
			if err := rows.Scan(&volume.childGeneration, &volume.ordinal, &volume.role, &volume.volumeID, &volume.bindingID, &volume.bindingGeneration, &volume.attachmentID, &volume.attachmentGeneration, &volume.lvUUID, &volume.observationID, &volume.observationGeneration); err != nil {
				return err
			}
			volumes = append(volumes, volume)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(volumes) < 1 || len(volumes) > 2 {
			return ErrHostEvacuationBlocked
		}
		members := make([]string, 0, len(volumes))
		for _, volume := range volumes {
			memberID := fmt.Sprintf("%s:%d", setID, volume.ordinal)
			memberDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%d/%s/%s/%s/%d/%s/%d/%s/%s/%d", claim.ChildOperationID, volume.ordinal, volume.role, volume.volumeID, volume.bindingID, volume.bindingGeneration, volume.attachmentID, volume.attachmentGeneration, volume.lvUUID, volume.observationID, volume.observationGeneration)))
			if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_source_storage_volume_safety_evidence(safety_member_evidence_id,child_operation_id,child_generation,root_storage_safety_evidence_id,volume_ordinal,device_role,volume_id,binding_id,binding_generation,attachment_id,attachment_generation,lv_uuid,observation_evidence_id,observation_generation,safety_state,safety_member_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'SAFE',$15)`, memberID, claim.ChildOperationID, volume.childGeneration, rootSafetyID, volume.ordinal, volume.role, volume.volumeID, volume.bindingID, volume.bindingGeneration, volume.attachmentID, volume.attachmentGeneration, volume.lvUUID, volume.observationID, volume.observationGeneration, memberDigest); err != nil {
				return err
			}
			members = append(members, fmt.Sprintf("%d:%s", volume.ordinal, memberDigest))
		}
		setDigest := digestReleaseBytes([]byte(strings.Join(members, ",")))
		_, err = tx.Exec(ctx, `INSERT INTO kim.host_evacuation_source_storage_safety_set_evidence(safety_set_evidence_id,child_operation_id,child_generation,root_storage_safety_evidence_id,volume_count,member_set_digest,safety_state) VALUES($1,$2,$3,$4,$5,$6,'SAFE')`, setID, claim.ChildOperationID, volumes[0].childGeneration, rootSafetyID, len(members), setDigest)
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
		var requiredStorage, safeStorage int
		if err := tx.QueryRow(ctx, `SELECT jsonb_array_length(e.storage_requirements),COALESCE((SELECT count(*) FROM kim.host_evacuation_source_storage_volume_safety_evidence member WHERE member.child_operation_id=e.child_operation_id AND member.child_generation=e.child_generation AND member.root_storage_safety_evidence_id=$2 AND member.safety_state='SAFE'),0) FROM kim.host_evacuation_workload_evidence e WHERE e.child_operation_id=$1`, claim.ChildOperationID, safetyID).Scan(&requiredStorage, &safeStorage); err != nil || (requiredStorage > 1 && safeStorage != requiredStorage) {
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
		var sourceHost string
		var sourceNetworkRaw []byte
		if err := tx.QueryRow(ctx, `SELECT a.project_id,a.workload_id,a.image_id,a.flavor_id,a.pool_id,e.source_host_id,e.network_requirements
			FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.placement_admission_decisions a ON a.admission_id=e.source_admission_id
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' AND $2<>e.source_host_id`, claim.ChildOperationID, destinationHostID).Scan(&out.ProjectID, &out.WorkloadID, &out.ImageID, &out.FlavorID, &out.PoolID, &sourceHost, &sourceNetworkRaw); err != nil {
			return ErrHostEvacuationBlocked
		}
		out.RequestID = requestID
		storageRows, err := tx.Query(ctx, `SELECT CASE WHEN v.bootable THEN 0 ELSE 1 END,v.storage_class_id,v.storage_class_revision,v.size_bytes,v.access_mode,v.bootable,ce.fencing_policy_revision,b.backend_id,b.vg_uuid,b.backend_generation,p.capacity_generation
			FROM kim.host_evacuation_workload_evidence e
			CROSS JOIN LATERAL jsonb_array_elements(e.storage_requirements) WITH ORDINALITY requirement(value,ordinality)
			JOIN kim.volumes_current v ON v.volume_id=requirement.value->>'VolumeID' AND v.placement_admission_id=e.source_admission_id AND v.bootable=COALESCE((requirement.value->>'Bootable')::boolean,false)
			JOIN kim.storage_class_revision_evidence ce ON ce.storage_class_id=v.storage_class_id AND ce.class_revision=v.storage_class_revision
			JOIN kim.storage_backends_current b ON b.host_id=$2 AND b.backend_type='LOCAL_LVM' AND b.lifecycle_state='ACTIVE' AND b.capability_state='CURRENT'
			JOIN kim.storage_capacity_projections_current p ON p.backend_id=b.backend_id AND p.projection_state='CURRENT'
			WHERE e.child_operation_id=$1 ORDER BY v.bootable DESC,v.volume_id`, claim.ChildOperationID, destinationHostID)
		if err != nil {
			return ErrHostEvacuationBlocked
		}
		defer storageRows.Close()
		for storageRows.Next() {
			var ordinal int
			var classID, accessMode, backendID, vgUUID string
			var classRevision, sizeBytes, fencingRevision, backendGeneration, capacityGeneration uint64
			var bootable bool
			if err := storageRows.Scan(&ordinal, &classID, &classRevision, &sizeBytes, &accessMode, &bootable, &fencingRevision, &backendID, &vgUUID, &backendGeneration, &capacityGeneration); err != nil {
				return err
			}
			role := "data"
			if ordinal == 0 {
				role = "root"
			}
			out.Storage = append(out.Storage, placement.StorageRequirement{VolumeID: fmt.Sprintf("%s:%s", requestID, role), AttachmentID: fmt.Sprintf("%s:%s-attachment", requestID, role), BackendID: backendID, BackendGeneration: backendGeneration, VGUUID: vgUUID, StorageClassID: classID, StorageClassRevision: classRevision, CapacityGeneration: capacityGeneration, AttachmentGeneration: 1, FencingPolicyRevision: fencingRevision, SizeBytes: sizeBytes, AccessMode: accessMode, Bootable: bootable})
		}
		if err := storageRows.Err(); err != nil || len(out.Storage) < 1 || len(out.Storage) > 2 {
			return ErrHostEvacuationBlocked
		}
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
		var sourceVolumeCount, destinationVolumeCount int
		if err := tx.QueryRow(ctx, `SELECT jsonb_array_length(e.storage_requirements),jsonb_array_length(d.storage_requirements) FROM kim.host_evacuation_workload_evidence e JOIN kim.placement_admission_decisions d ON d.admission_id=$2 WHERE e.child_operation_id=$1`, claim.ChildOperationID, destinationAdmissionID).Scan(&sourceVolumeCount, &destinationVolumeCount); err != nil || sourceVolumeCount < 1 || sourceVolumeCount > 2 || destinationVolumeCount != sourceVolumeCount {
			return ErrHostEvacuationBlocked
		}
		type relocationVolume struct {
			ordinal                                                   int
			role, safetyMember, sourceVolume, sourceBinding, sourceLV string
			destinationVolume, destinationBinding, destinationLV      string
			copyTerminal, copyTerminalDigest, memberDigest            string
			sourceBindingGeneration, destinationBindingGeneration     uint64
		}
		copyRows, err := tx.Query(ctx, `SELECT copy.volume_ordinal,copy.device_role,COALESCE(copy.source_volume_safety_evidence_id,''),copy.source_volume_id,copy.source_binding_id,copy.source_binding_generation,copy.source_lv_uuid,
			copy.destination_volume_id,copy.destination_binding_id,copy.destination_binding_generation,copy.destination_lv_uuid,terminal.terminal_evidence_id,terminal.terminal_digest
			FROM kim.local_lvm_relocation_copy_operation_evidence copy
			JOIN kim.local_lvm_relocation_copy_operations_current current ON current.copy_operation_id=copy.copy_operation_id AND current.copy_generation=copy.copy_generation AND current.operation_state='VERIFIED'
			JOIN kim.local_lvm_relocation_copy_terminal_evidence terminal ON terminal.terminal_evidence_id=current.terminal_evidence_id AND terminal.copy_operation_id=copy.copy_operation_id AND terminal.copy_generation=copy.copy_generation AND terminal.terminal_state='VERIFIED'
			JOIN kim.volume_backend_bindings_current binding ON binding.binding_id=copy.destination_binding_id AND binding.binding_generation=copy.destination_binding_generation AND binding.volume_id=copy.destination_volume_id AND binding.host_id=copy.destination_host_id AND binding.lv_uuid=copy.destination_lv_uuid AND binding.binding_state='BOUND'
			WHERE copy.child_operation_id=$1 AND copy.child_generation=$2 AND copy.source_storage_safety_evidence_id=$3 AND copy.destination_admission_id=$4 ORDER BY copy.volume_ordinal`, claim.ChildOperationID, childGeneration, safetyID, destinationAdmissionID)
		if err != nil {
			return err
		}
		defer copyRows.Close()
		volumes := make([]relocationVolume, 0, 2)
		for copyRows.Next() {
			var item relocationVolume
			if err := copyRows.Scan(&item.ordinal, &item.role, &item.safetyMember, &item.sourceVolume, &item.sourceBinding, &item.sourceBindingGeneration, &item.sourceLV, &item.destinationVolume, &item.destinationBinding, &item.destinationBindingGeneration, &item.destinationLV, &item.copyTerminal, &item.copyTerminalDigest); err != nil {
				return err
			}
			item.memberDigest = digestReleaseBytes([]byte(fmt.Sprintf("%d/%s/%s/%s/%d/%s/%s/%s/%d/%s/%s", item.ordinal, item.role, item.sourceVolume, item.sourceBinding, item.sourceBindingGeneration, item.sourceLV, item.destinationVolume, item.destinationBinding, item.destinationBindingGeneration, item.destinationLV, item.copyTerminalDigest)))
			volumes = append(volumes, item)
		}
		if err := copyRows.Err(); err != nil || len(volumes) != sourceVolumeCount || (sourceVolumeCount == 2 && (volumes[0].ordinal != 0 || volumes[1].ordinal != 1 || volumes[0].safetyMember == "" || volumes[1].safetyMember == "")) {
			return ErrHostEvacuationBlocked
		}
		copyTerminalID, copyTerminalDigest := volumes[0].copyTerminal, volumes[0].copyTerminalDigest
		memberParts := make([]string, len(volumes))
		for i := range volumes {
			memberParts[i] = fmt.Sprintf("%d:%s", volumes[i].ordinal, volumes[i].memberDigest)
		}
		storageSetDigest := digestReleaseBytes([]byte(strings.Join(memberParts, ",")))
		destinationMaterialization := sourceMaterialization + 1
		sourceRequirements := digestReleaseBytes([]byte(sourceStorageDigest + "/" + sourceNetworkDigest + "/" + sourcePCIDigest))
		destinationRequirements := digestReleaseBytes([]byte(destinationStorageDigest + "/" + destinationNetworkDigest + "/" + destinationPCIDigest))
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%s/%d/%s", claim.ChildOperationID, sourcePlan, destinationAdmissionID, safetyID, releaseID, destinationMaterialization, storageSetDigest)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_materialization_relocation_authority_evidence(relocation_authority_id,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,source_admission_id,source_plan_id,source_materialization_generation,source_quiescence_evidence_id,source_storage_safety_evidence_id,source_placement_release_evidence_id,destination_host_id,destination_admission_id,destination_materialization_generation,source_requirements_digest,destination_requirements_digest,authority_digest,local_lvm_copy_terminal_evidence_id,local_lvm_copy_terminal_digest,volume_count,storage_evidence_set_digest) VALUES($1,$2,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`, authorityID, claim.ChildOperationID, childGeneration, vmID, vmGeneration, sourceHost, sourceAdmission, sourcePlan, sourceMaterialization, quiescenceID, safetyID, releaseID, destinationHost, destinationAdmissionID, destinationMaterialization, sourceRequirements, destinationRequirements, digest, copyTerminalID, copyTerminalDigest, sourceVolumeCount, storageSetDigest); err != nil {
			return err
		}
		if sourceVolumeCount == 2 {
			for _, item := range volumes {
				if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_materialization_relocation_volume_evidence(relocation_authority_id,volume_ordinal,device_role,source_volume_safety_evidence_id,source_volume_id,source_binding_id,source_binding_generation,source_lv_uuid,destination_volume_id,destination_binding_id,destination_binding_generation,destination_lv_uuid,copy_terminal_evidence_id,copy_terminal_digest,member_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, authorityID, item.ordinal, item.role, item.safetyMember, item.sourceVolume, item.sourceBinding, item.sourceBindingGeneration, item.sourceLV, item.destinationVolume, item.destinationBinding, item.destinationBindingGeneration, item.destinationLV, item.copyTerminal, item.copyTerminalDigest, item.memberDigest); err != nil {
					return err
				}
			}
		}
		_ = workloadID
		return nil
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
