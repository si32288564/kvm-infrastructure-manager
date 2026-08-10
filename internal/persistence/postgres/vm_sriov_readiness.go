package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/sriovnetwork"
)

type SRIOVPortRealizationRequest struct{ VMID, PlanID, PortID, JobID, CommandID string }
type SRIOVPortRealizationDecision struct{ VMID, HostID, PortID, JobID, CommandID, PayloadDigest string }

func PrepareSRIOVPortRealization(ctx context.Context, db TxBeginner, r SRIOVPortRealizationRequest) (SRIOVPortRealizationDecision, error) {
	if !vmUUIDPattern.MatchString(r.VMID) || r.PlanID == "" || r.PortID == "" || r.JobID == "" || r.CommandID == "" {
		return SRIOVPortRealizationDecision{}, ErrVMMaterializationConflict
	}
	var decision SRIOVPortRealizationDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var hostID, networkID, segmentID, mac, device, claimID, observationDigest, qualificationID, policyID string
		var vmGeneration, portGeneration, networkGeneration, segmentGeneration, mappingGeneration, bindingGeneration, observationGeneration, qualificationRevision, policyGeneration int64
		err := tx.QueryRow(ctx, `SELECT vm.host_id,vm.vm_generation,port.network_id,port.port_generation,network.network_generation,binding.segment_claim_id,segment.segment_generation,mapping.mapping_generation,binding.binding_generation,mac.mac_address::text,binding.device_address,claim.claim_id,pci.observation_generation,pci.observation_digest,q.qualification_id,q.qualification_revision,policy.policy_id,policy.policy_generation FROM kim.virtual_machines_current vm JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.domain_state='DEFINED' AND ready.storage_state='BOUND' AND ready.image_state='REALIZED' JOIN kim.network_ports_current port ON port.placement_admission_id=vm.placement_admission_id AND port.port_id=$3 AND port.desired_state='RESERVED' JOIN kim.networks_current network ON network.network_id=port.network_id AND network.lifecycle_state='ACTIVE' JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.host_id=vm.host_id AND binding.binding_type='SRIOV_DIRECT' AND binding.binding_state='RESERVED' JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.claim_state='ACTIVE' JOIN kim.host_network_mappings_current mapping ON mapping.host_id=vm.host_id AND mapping.segment_claim_id=segment.segment_claim_id AND mapping.mapping_state='CURRENT' AND 'SRIOV_DIRECT'=ANY(mapping.supported_binding_types) JOIN kim.network_identity_claims mac ON mac.port_id=port.port_id AND mac.claim_type='MAC' AND mac.claim_state='RESERVED' JOIN kim.pci_vf_allocation_claims claim ON claim.placement_admission_id=vm.placement_admission_id AND claim.host_id=vm.host_id AND claim.device_address=binding.device_address AND claim.claim_state='ACTIVE' JOIN kim.host_pci_device_projections pci ON pci.host_id=claim.host_id AND pci.device_address=claim.device_address AND pci.observation_state='AVAILABLE' AND pci.relationship_state='AVAILABLE' JOIN kim.pci_qualification_bindings_current q ON q.host_id=pci.host_id AND q.device_address=pci.device_address AND q.binding_state='CURRENT' AND q.observed_generation=pci.observation_generation AND q.observation_digest=pci.observation_digest JOIN kim.pci_qualification_evidence qe ON qe.qualification_id=q.qualification_id AND qe.qualification_revision=q.qualification_revision AND qe.evidence_state='QUALIFIED' AND 'VF_ASSIGN'=ANY(qe.validated_operations) JOIN kim.pci_allocation_policy_bindings policy ON policy.host_id=claim.host_id AND policy.policy_id=claim.policy_id AND policy.policy_generation=claim.policy_generation AND policy.policy_state='ALLOWED' AND policy.qualification_profile_revision=q.qualification_profile_revision WHERE vm.vm_id=$1 AND vm.current_plan_id=$2 AND vm.lifecycle_state='DEFINED' FOR UPDATE OF vm,ready,port,binding,mapping,claim`, r.VMID, r.PlanID, r.PortID).Scan(&hostID, &vmGeneration, &networkID, &portGeneration, &networkGeneration, &segmentID, &segmentGeneration, &mappingGeneration, &bindingGeneration, &mac, &device, &claimID, &observationGeneration, &observationDigest, &qualificationID, &qualificationRevision, &policyID, &policyGeneration)
		if err != nil {
			return ErrVMMaterializationConflict
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"domain_uuid": r.VMID, "vm_generation": vmGeneration, "port_id": r.PortID, "port_generation": portGeneration, "network_id": networkID, "network_generation": networkGeneration, "segment_claim_id": segmentID, "segment_generation": segmentGeneration, "host_mapping_generation": mappingGeneration, "binding_generation": bindingGeneration, "mac_address": mac, "device_address": device, "vf_claim_id": claimID, "pci_observation_generation": observationGeneration, "pci_observation_digest": observationDigest, "qualification_id": qualificationID, "qualification_revision": qualificationRevision, "policy_id": policyID, "policy_generation": policyGeneration, "binding_type": "SRIOV_DIRECT", "desired_state": "REALIZED"})
		digest := digestBytes(payload)
		jobTag, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VM_NETWORK_PORT',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, r.JobID, r.PortID, bindingGeneration)
		if err != nil {
			return err
		}
		if jobTag.RowsAffected() == 0 {
			var resourceType, resourceID string
			var revision int64
			if err := tx.QueryRow(ctx, `SELECT resource_type,resource_id,desired_revision FROM kim.execution_jobs WHERE job_id=$1`, r.JobID).Scan(&resourceType, &resourceID, &revision); err != nil || resourceType != "VM_NETWORK_PORT" || resourceID != r.PortID || revision != bindingGeneration {
				return ErrVMMaterializationConflict
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(command_id) DO NOTHING`, r.CommandID, r.JobID, hostID, sriovnetwork.CommandType, sriovnetwork.SchemaVersion, "port:"+r.PortID, payload, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($1,'PENDING')`, r.CommandID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, r.JobID, r.CommandID); err != nil {
				return err
			}
			if err := appendJobEventTx(ctx, tx, r.JobID, "COMMAND_CREATED", map[string]any{"command_id": r.CommandID, "payload_digest": digest}); err != nil {
				return err
			}
		} else {
			var acceptedJob, acceptedHost, acceptedType, acceptedSchema, acceptedTarget, acceptedDigest string
			if err := tx.QueryRow(ctx, `SELECT job_id,host_id,command_type,schema_version,target_resource_id,payload_digest FROM kim.execution_commands WHERE command_id=$1`, r.CommandID).Scan(&acceptedJob, &acceptedHost, &acceptedType, &acceptedSchema, &acceptedTarget, &acceptedDigest); err != nil || acceptedJob != r.JobID || acceptedHost != hostID || acceptedType != sriovnetwork.CommandType || acceptedSchema != sriovnetwork.SchemaVersion || acceptedTarget != "port:"+r.PortID || acceptedDigest != digest {
				return ErrVMMaterializationConflict
			}
		}
		decision = SRIOVPortRealizationDecision{r.VMID, hostID, r.PortID, r.JobID, r.CommandID, digest}
		return nil
	})
	return decision, err
}

type SRIOVPortRealizationObservation struct {
	EvidenceID, VMID, PlanID, HostID, PortID, NetworkID, SegmentClaimID, DeviceAddress, VFClaimID, QualificationID, PolicyID, CommandID, VerificationID, ObservationDigest, VerifierDigest, PowerJobID, PowerCommandID string
	VMGeneration, PortGeneration, NetworkGeneration, SegmentGeneration, HostMappingGeneration, BindingGeneration, PCIObservationGeneration, QualificationRevision, PolicyGeneration, ObservationGeneration             uint64
	AttemptIndex                                                                                                                                                                                                       uint32
}

func AcceptSRIOVPortRealizationAndMaybeArmPower(ctx context.Context, db TxBeginner, v SRIOVPortRealizationObservation) error {
	if v.EvidenceID == "" || !vmUUIDPattern.MatchString(v.VMID) || v.DeviceAddress == "" || v.VFClaimID == "" || v.QualificationID == "" || v.PolicyID == "" || v.CommandID == "" || v.VerificationID == "" || len(v.ObservationDigest) != 64 || len(v.VerifierDigest) != 64 {
		return ErrVMMaterializationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := lockHostAuthorityTx(ctx, tx, v.HostID); err != nil {
			return err
		}
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, v.HostID); err != nil {
			return err
		}
		var ok bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.virtual_machines_current vm JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.domain_state='DEFINED' AND ready.storage_state='BOUND' AND ready.image_state='REALIZED' JOIN kim.network_ports_current port ON port.placement_admission_id=vm.placement_admission_id JOIN kim.networks_current network ON network.network_id=port.network_id AND network.lifecycle_state='ACTIVE' JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.host_id=vm.host_id AND binding.binding_type='SRIOV_DIRECT' AND binding.binding_state='RESERVED' JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.claim_state='ACTIVE' JOIN kim.host_network_mappings_current mapping ON mapping.host_id=vm.host_id AND mapping.segment_claim_id=segment.segment_claim_id AND mapping.mapping_state='CURRENT' AND 'SRIOV_DIRECT'=ANY(mapping.supported_binding_types) JOIN kim.network_identity_claims mac ON mac.port_id=port.port_id AND mac.claim_type='MAC' AND mac.claim_state='RESERVED' JOIN kim.pci_vf_allocation_claims claim ON claim.placement_admission_id=vm.placement_admission_id AND claim.host_id=vm.host_id AND claim.device_address=binding.device_address AND claim.claim_state='ACTIVE' JOIN kim.host_pci_device_projections pci ON pci.host_id=claim.host_id AND pci.device_address=claim.device_address AND pci.observation_generation=$8 AND pci.observation_state='AVAILABLE' AND pci.relationship_state='AVAILABLE' JOIN kim.pci_qualification_bindings_current q ON q.host_id=pci.host_id AND q.device_address=pci.device_address AND q.binding_state='CURRENT' AND q.qualification_id=$9 AND q.qualification_revision=$10 AND q.observed_generation=pci.observation_generation AND q.observation_digest=pci.observation_digest JOIN kim.pci_qualification_evidence qe ON qe.qualification_id=q.qualification_id AND qe.qualification_revision=q.qualification_revision AND qe.evidence_state='QUALIFIED' AND 'VF_ASSIGN'=ANY(qe.validated_operations) JOIN kim.pci_allocation_policy_bindings policy ON policy.host_id=claim.host_id AND policy.policy_id=$11 AND policy.policy_generation=$12 AND policy.policy_state='ALLOWED' AND policy.qualification_profile_revision=q.qualification_profile_revision JOIN kim.execution_commands command ON command.command_id=$13 AND command.command_type=$14 AND command.schema_version=$15 AND command.target_resource_id='port:'||port.port_id JOIN kim.command_verification_evidence verification ON verification.verification_id=$16 AND verification.command_id=command.command_id AND verification.attempt_index=$17 AND verification.observation_generation=$18 AND verification.observation_digest=$19 AND verification.verification_state='MATCHED' AND verification.verifier_artifact_digest=$20 WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND vm.current_plan_id=$3 AND vm.host_id=$4 AND port.port_id=$5 AND port.port_generation=$6 AND binding.binding_generation=$7 AND binding.device_address=$21 AND claim.claim_id=$22 AND network.network_id=$23 AND network.network_generation=$24 AND segment.segment_claim_id=$25 AND segment.segment_generation=$26 AND mapping.mapping_generation=$27 AND verification.evidence_payload->>'domain_uuid'=vm.vm_id::text AND (verification.evidence_payload->>'vm_generation')::bigint=vm.vm_generation AND verification.evidence_payload->>'port_id'=port.port_id AND verification.evidence_payload->>'network_id'=network.network_id AND verification.evidence_payload->>'segment_claim_id'=segment.segment_claim_id AND verification.evidence_payload->>'mac_address'=mac.mac_address::text AND verification.evidence_payload->>'device_address'=$21 AND verification.evidence_payload->>'vf_claim_id'=$22 AND verification.evidence_payload->>'qualification_id'=$9 AND verification.evidence_payload->>'policy_id'=$11 AND (verification.evidence_payload->>'domain_hostdev_identity_matches')::boolean)`, v.VMID, v.VMGeneration, v.PlanID, v.HostID, v.PortID, v.PortGeneration, v.BindingGeneration, v.PCIObservationGeneration, v.QualificationID, v.QualificationRevision, v.PolicyID, v.PolicyGeneration, v.CommandID, sriovnetwork.CommandType, sriovnetwork.SchemaVersion, v.VerificationID, v.AttemptIndex, v.ObservationGeneration, v.ObservationDigest, v.VerifierDigest, v.DeviceAddress, v.VFClaimID, v.NetworkID, v.NetworkGeneration, v.SegmentClaimID, v.SegmentGeneration, v.HostMappingGeneration).Scan(&ok)
		if err != nil || !ok {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_network_port_realization_evidence(evidence_id,vm_id,vm_generation,plan_id,host_id,port_id,port_generation,network_id,network_generation,segment_claim_id,segment_generation,host_mapping_generation,binding_generation,binding_type,device_address,vf_claim_id,pci_observation_generation,pci_observation_digest,qualification_id,qualification_revision,policy_id,policy_generation,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,preboot_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'SRIOV_DIRECT',$14,$15,$16,(SELECT observation_digest FROM kim.host_pci_device_projections WHERE host_id=$5 AND device_address=$14),$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,'REALIZED') ON CONFLICT(evidence_id) DO NOTHING`, v.EvidenceID, v.VMID, v.VMGeneration, v.PlanID, v.HostID, v.PortID, v.PortGeneration, v.NetworkID, v.NetworkGeneration, v.SegmentClaimID, v.SegmentGeneration, v.HostMappingGeneration, v.BindingGeneration, v.DeviceAddress, v.VFClaimID, v.PCIObservationGeneration, v.QualificationID, v.QualificationRevision, v.PolicyID, v.PolicyGeneration, v.CommandID, v.AttemptIndex, v.VerificationID, v.ObservationGeneration, v.ObservationDigest, v.VerifierDigest); err != nil {
			return err
		}
		var evidenceMatches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_network_port_realization_evidence WHERE evidence_id=$1 AND vm_id=$2 AND vm_generation=$3 AND port_id=$4 AND binding_type='SRIOV_DIRECT' AND device_address=$5 AND vf_claim_id=$6 AND qualification_id=$7 AND qualification_revision=$8 AND policy_id=$9 AND policy_generation=$10 AND command_id=$11 AND verification_id=$12 AND observation_generation=$13 AND observation_digest=$14)`, v.EvidenceID, v.VMID, v.VMGeneration, v.PortID, v.DeviceAddress, v.VFClaimID, v.QualificationID, v.QualificationRevision, v.PolicyID, v.PolicyGeneration, v.CommandID, v.VerificationID, v.ObservationGeneration, v.ObservationDigest).Scan(&evidenceMatches); err != nil || !evidenceMatches {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_network_port_realizations_current(vm_id,vm_generation,port_id,port_generation,binding_generation,observation_generation,evidence_id,preboot_state) VALUES($1,$2,$3,$4,$5,$6,$7,'REALIZED') ON CONFLICT(vm_id,port_id) DO UPDATE SET observation_generation=EXCLUDED.observation_generation,evidence_id=EXCLUDED.evidence_id,preboot_state='REALIZED',updated_at=statement_timestamp() WHERE kim.vm_network_port_realizations_current.observation_generation<EXCLUDED.observation_generation`, v.VMID, v.VMGeneration, v.PortID, v.PortGeneration, v.BindingGeneration, v.ObservationGeneration, v.EvidenceID); err != nil {
			return err
		}
		return maybeArmReadyVMPowerTx(ctx, tx, v.VMID, v.VMGeneration, v.HostID, v.ObservationGeneration, v.PowerJobID, v.PowerCommandID)
	})
}

func maybeArmReadyVMPowerTx(ctx context.Context, tx pgx.Tx, vmID string, vmGeneration uint64, hostID string, observationGeneration uint64, powerJobID, powerCommandID string) error {
	var required, realized int
	var evidenceSet string
	if err := tx.QueryRow(ctx, `SELECT count(*),count(evidence.evidence_id),coalesce(string_agg(current.evidence_id||':'||evidence.observation_digest,',' ORDER BY port.port_id) FILTER(WHERE evidence.evidence_id IS NOT NULL),'') FROM kim.network_ports_current port JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.binding_state='RESERVED' JOIN kim.networks_current network ON network.network_id=port.network_id AND network.lifecycle_state='ACTIVE' JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.claim_state='ACTIVE' JOIN kim.host_network_mappings_current mapping ON mapping.host_id=binding.host_id AND mapping.segment_claim_id=segment.segment_claim_id AND mapping.mapping_state='CURRENT' LEFT JOIN kim.vm_network_port_realizations_current current ON current.vm_id=$1 AND current.port_id=port.port_id AND current.vm_generation=$2 AND current.port_generation=port.port_generation AND current.binding_generation=binding.binding_generation AND current.preboot_state='REALIZED' LEFT JOIN kim.vm_network_port_realization_evidence evidence ON evidence.evidence_id=current.evidence_id AND evidence.network_generation=network.network_generation AND evidence.segment_generation=segment.segment_generation AND evidence.host_mapping_generation=mapping.mapping_generation WHERE port.placement_admission_id=(SELECT placement_admission_id FROM kim.virtual_machines_current WHERE vm_id=$1) AND port.desired_state='RESERVED'`, vmID, vmGeneration).Scan(&required, &realized, &evidenceSet); err != nil || required == 0 || realized != required {
		return nil
	}
	if powerJobID == "" || powerCommandID == "" {
		return ErrVMMaterializationConflict
	}
	setDigest := digestBytes([]byte(evidenceSet))
	payload := []byte(`{"desired_state":"RUNNING"}`)
	digest := digestBytes(payload)
	if _, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET network_state='REALIZED',network_observation_generation=$2,network_evidence_set_digest=$3,boot_readiness='READY',blocking_reasons='{}',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$4 AND domain_state='DEFINED' AND storage_state='BOUND' AND image_state='REALIZED'`, vmID, observationGeneration, setDigest, vmGeneration); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET desired_power_state='RUNNING',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$2`, vmID, vmGeneration); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VIRTUAL_MACHINE_POWER',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, powerJobID, vmID, vmGeneration); err != nil {
		return err
	}
	var jobResource, jobType string
	var jobRevision int64
	if err := tx.QueryRow(ctx, `SELECT resource_id,resource_type,desired_revision FROM kim.execution_jobs WHERE job_id=$1`, powerJobID).Scan(&jobResource, &jobType, &jobRevision); err != nil || jobResource != vmID || jobType != "VIRTUAL_MACHINE_POWER" || jobRevision != int64(vmGeneration) {
		return ErrVMMaterializationConflict
	}
	tag, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(command_id) DO NOTHING`, powerCommandID, powerJobID, hostID, libvirtdomain.CommandType, libvirtdomain.SchemaVersion, "vm:"+vmID, payload, digest)
	if err != nil {
		return err
	}
	var acceptedJob, acceptedHost, acceptedType, acceptedSchema, acceptedTarget, acceptedDigest string
	if err := tx.QueryRow(ctx, `SELECT job_id,host_id,command_type,schema_version,target_resource_id,payload_digest FROM kim.execution_commands WHERE command_id=$1`, powerCommandID).Scan(&acceptedJob, &acceptedHost, &acceptedType, &acceptedSchema, &acceptedTarget, &acceptedDigest); err != nil || acceptedJob != powerJobID || acceptedHost != hostID || acceptedType != libvirtdomain.CommandType || acceptedSchema != libvirtdomain.SchemaVersion || acceptedTarget != "vm:"+vmID || acceptedDigest != digest {
		return ErrVMMaterializationConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($1,'PENDING') ON CONFLICT(command_id) DO NOTHING`, powerCommandID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, powerJobID, powerCommandID); err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return appendJobEventTx(ctx, tx, powerJobID, "COMMAND_CREATED", map[string]any{"command_id": powerCommandID, "payload_digest": digest, "readiness_digest": setDigest})
	}
	return nil
}
