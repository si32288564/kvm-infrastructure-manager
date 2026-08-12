package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/libvirtdomain"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/ovsnetwork"
)

type OVSPortRealizationRequest struct{ VMID, PlanID, PortID, JobID, CommandID string }
type OVSPortRealizationDecision struct{ VMID, HostID, PortID, JobID, CommandID, PayloadDigest string }

func PrepareOVSPortRealization(ctx context.Context, db TxBeginner, request OVSPortRealizationRequest) (OVSPortRealizationDecision, error) {
	if !vmUUIDPattern.MatchString(request.VMID) || request.PlanID == "" || request.PortID == "" || request.JobID == "" || request.CommandID == "" {
		return OVSPortRealizationDecision{}, ErrVMMaterializationConflict
	}
	var decision OVSPortRealizationDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var hostID, networkID, segmentID, mac string
		var vmGeneration, portGeneration, networkGeneration, segmentGeneration, mappingGeneration, bindingGeneration, mtu int64
		if err := tx.QueryRow(ctx, `
			SELECT vm.host_id,vm.vm_generation,port.network_id,port.port_generation,
			 network.network_generation,binding.segment_claim_id,segment.segment_generation,
			 mapping.mapping_generation,binding.binding_generation,network.mtu,mac.mac_address::text
			FROM kim.virtual_machines_current vm
			JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.domain_state='DEFINED' AND ready.storage_state='BOUND' AND ready.image_state='REALIZED'
			JOIN kim.network_ports_current port ON port.placement_admission_id=vm.placement_admission_id AND port.port_id=$3 AND port.desired_state='RESERVED'
			JOIN kim.networks_current network ON network.network_id=port.network_id AND network.lifecycle_state='ACTIVE'
			JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.host_id=vm.host_id AND binding.binding_type='OVS' AND binding.binding_state='RESERVED'
			JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.claim_state='ACTIVE'
			JOIN kim.host_network_mappings_current mapping ON mapping.host_id=vm.host_id AND mapping.segment_claim_id=segment.segment_claim_id AND mapping.mapping_state='CURRENT' AND 'OVS'=ANY(mapping.supported_binding_types)
			JOIN kim.network_identity_claims mac ON mac.port_id=port.port_id AND mac.claim_type='MAC' AND mac.claim_state='RESERVED'
			WHERE vm.vm_id=$1 AND vm.current_plan_id=$2 AND vm.lifecycle_state='DEFINED'
			FOR UPDATE OF vm,ready,port,binding,mapping
		`, request.VMID, request.PlanID, request.PortID).Scan(&hostID, &vmGeneration, &networkID, &portGeneration, &networkGeneration, &segmentID, &segmentGeneration, &mappingGeneration, &bindingGeneration, &mtu, &mac); err != nil {
			return ErrVMMaterializationConflict
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{"domain_uuid": request.VMID, "vm_generation": vmGeneration, "port_id": request.PortID, "port_generation": portGeneration, "network_id": networkID, "network_generation": networkGeneration, "segment_claim_id": segmentID, "segment_generation": segmentGeneration, "host_mapping_generation": mappingGeneration, "binding_generation": bindingGeneration, "mac_address": mac, "mtu": mtu, "binding_type": "OVS", "desired_state": "REALIZED"})
		if err != nil {
			return err
		}
		digest := digestBytes(payload)
		tag, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VM_NETWORK_PORT',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, request.JobID, request.PortID, bindingGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var rt, rid string
			var rev int64
			if err := tx.QueryRow(ctx, `SELECT resource_type,resource_id,desired_revision FROM kim.execution_jobs WHERE job_id=$1`, request.JobID).Scan(&rt, &rid, &rev); err != nil || rt != "VM_NETWORK_PORT" || rid != request.PortID || rev != bindingGeneration {
				return ErrVMMaterializationConflict
			}
		}
		tag, err = tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(command_id) DO NOTHING`, request.CommandID, request.JobID, hostID, ovsnetwork.CommandType, ovsnetwork.SchemaVersion, "port:"+request.PortID, payload, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($1,'PENDING')`, request.CommandID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, request.JobID, request.CommandID); err != nil {
				return err
			}
			if err := appendJobEventTx(ctx, tx, request.JobID, "COMMAND_CREATED", map[string]any{"command_id": request.CommandID, "payload_digest": digest}); err != nil {
				return err
			}
		} else {
			var accepted string
			if err := tx.QueryRow(ctx, `SELECT payload_digest FROM kim.execution_commands WHERE command_id=$1 AND job_id=$2 AND host_id=$3 AND command_type=$4 AND schema_version=$5 AND target_resource_id=$6`, request.CommandID, request.JobID, hostID, ovsnetwork.CommandType, ovsnetwork.SchemaVersion, "port:"+request.PortID).Scan(&accepted); err != nil || accepted != digest {
				return ErrVMMaterializationConflict
			}
		}
		decision = OVSPortRealizationDecision{request.VMID, hostID, request.PortID, request.JobID, request.CommandID, digest}
		return nil
	})
	return decision, err
}

type OVSPortRealizationObservation struct {
	EvidenceID, VMID, PlanID, HostID, PortID, NetworkID, SegmentClaimID string
	CommandID, VerificationID, ObservationDigest, VerifierDigest        string
	PowerJobID, PowerCommandID                                          string
	DeferPowerAuthorization                                             bool
	VMGeneration, PortGeneration, NetworkGeneration, SegmentGeneration  uint64
	HostMappingGeneration, BindingGeneration, ObservationGeneration     uint64
	AttemptIndex                                                        uint32
}

func AcceptOVSPortRealizationAndMaybeArmPower(ctx context.Context, db TxBeginner, v OVSPortRealizationObservation) error {
	if v.EvidenceID == "" || !vmUUIDPattern.MatchString(v.VMID) || v.PlanID == "" || v.HostID == "" || v.PortID == "" || v.NetworkID == "" || v.SegmentClaimID == "" || v.CommandID == "" || v.VerificationID == "" || len(v.ObservationDigest) != 64 || len(v.VerifierDigest) != 64 || v.VMGeneration == 0 || v.PortGeneration == 0 || v.NetworkGeneration == 0 || v.SegmentGeneration == 0 || v.HostMappingGeneration == 0 || v.BindingGeneration == 0 || v.ObservationGeneration == 0 || v.AttemptIndex == 0 {
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
		var accepted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.virtual_machines_current vm JOIN kim.vm_materialization_readiness_current ready ON ready.vm_id=vm.vm_id AND ready.vm_generation=vm.vm_generation AND ready.domain_state='DEFINED' AND ready.storage_state='BOUND' AND ready.image_state='REALIZED' JOIN kim.network_ports_current port ON port.placement_admission_id=vm.placement_admission_id JOIN kim.networks_current network ON network.network_id=port.network_id JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.host_id=vm.host_id JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id JOIN kim.host_network_mappings_current mapping ON mapping.host_id=vm.host_id AND mapping.segment_claim_id=segment.segment_claim_id JOIN kim.network_identity_claims mac ON mac.port_id=port.port_id AND mac.claim_type='MAC' AND mac.claim_state='RESERVED' JOIN kim.execution_commands command ON command.command_id=$12 AND command.command_type=$13 AND command.schema_version=$14 AND command.target_resource_id='port:'||port.port_id JOIN kim.command_verification_evidence verification ON verification.verification_id=$15 AND verification.command_id=command.command_id AND verification.attempt_index=$16 AND verification.observation_generation=$17 AND verification.observation_digest=$18 AND verification.verification_state='MATCHED' AND verification.verifier_artifact_digest=$19 WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND vm.current_plan_id=$3 AND vm.host_id=$4 AND port.port_id=$5 AND port.port_generation=$6 AND port.desired_state='RESERVED' AND network.network_id=$7 AND network.network_generation=$8 AND network.lifecycle_state='ACTIVE' AND segment.segment_claim_id=$9 AND segment.segment_generation=$10 AND segment.claim_state='ACTIVE' AND mapping.mapping_generation=$11 AND mapping.mapping_state='CURRENT' AND binding.binding_generation=$20 AND binding.binding_type='OVS' AND binding.binding_state='RESERVED' AND verification.evidence_payload->>'domain_uuid'=vm.vm_id::text AND (verification.evidence_payload->>'vm_generation')::bigint=vm.vm_generation AND verification.evidence_payload->>'port_id'=port.port_id AND (verification.evidence_payload->>'port_generation')::bigint=port.port_generation AND verification.evidence_payload->>'network_id'=network.network_id AND (verification.evidence_payload->>'network_generation')::bigint=network.network_generation AND verification.evidence_payload->>'segment_claim_id'=segment.segment_claim_id AND (verification.evidence_payload->>'segment_generation')::bigint=segment.segment_generation AND (verification.evidence_payload->>'host_mapping_generation')::bigint=mapping.mapping_generation AND (verification.evidence_payload->>'binding_generation')::bigint=binding.binding_generation AND verification.evidence_payload->>'binding_type'='OVS' AND verification.evidence_payload->>'mac_address'=mac.mac_address::text AND verification.evidence_payload->>'interface_id'=port.port_id AND (verification.evidence_payload->>'domain_nic_identity_matches')::boolean AND (verification.evidence_payload->>'bridge_observed')::boolean)`, v.VMID, v.VMGeneration, v.PlanID, v.HostID, v.PortID, v.PortGeneration, v.NetworkID, v.NetworkGeneration, v.SegmentClaimID, v.SegmentGeneration, v.HostMappingGeneration, v.CommandID, ovsnetwork.CommandType, ovsnetwork.SchemaVersion, v.VerificationID, v.AttemptIndex, v.ObservationGeneration, v.ObservationDigest, v.VerifierDigest, v.BindingGeneration).Scan(&accepted); err != nil || !accepted {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_network_port_realization_evidence(evidence_id,vm_id,vm_generation,plan_id,host_id,port_id,port_generation,network_id,network_generation,segment_claim_id,segment_generation,host_mapping_generation,binding_generation,binding_type,command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,preboot_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'OVS',$14,$15,$16,$17,$18,$19,'REALIZED') ON CONFLICT(evidence_id) DO NOTHING`, v.EvidenceID, v.VMID, v.VMGeneration, v.PlanID, v.HostID, v.PortID, v.PortGeneration, v.NetworkID, v.NetworkGeneration, v.SegmentClaimID, v.SegmentGeneration, v.HostMappingGeneration, v.BindingGeneration, v.CommandID, v.AttemptIndex, v.VerificationID, v.ObservationGeneration, v.ObservationDigest, v.VerifierDigest); err != nil {
			return err
		}
		var evidenceMatches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_network_port_realization_evidence WHERE evidence_id=$1 AND vm_id=$2 AND vm_generation=$3 AND port_id=$4 AND observation_generation=$5 AND observation_digest=$6 AND command_id=$7 AND verification_id=$8 AND preboot_state='REALIZED')`, v.EvidenceID, v.VMID, v.VMGeneration, v.PortID, v.ObservationGeneration, v.ObservationDigest, v.CommandID, v.VerificationID).Scan(&evidenceMatches); err != nil || !evidenceMatches {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.vm_network_port_realizations_current(vm_id,vm_generation,port_id,port_generation,binding_generation,observation_generation,evidence_id,preboot_state) VALUES($1,$2,$3,$4,$5,$6,$7,'REALIZED') ON CONFLICT(vm_id,port_id) DO UPDATE SET port_generation=EXCLUDED.port_generation,binding_generation=EXCLUDED.binding_generation,observation_generation=EXCLUDED.observation_generation,evidence_id=EXCLUDED.evidence_id,preboot_state='REALIZED',updated_at=statement_timestamp() WHERE kim.vm_network_port_realizations_current.observation_generation<EXCLUDED.observation_generation`, v.VMID, v.VMGeneration, v.PortID, v.PortGeneration, v.BindingGeneration, v.ObservationGeneration, v.EvidenceID); err != nil {
			return err
		}
		_, _ = tx.Exec(ctx, `UPDATE kim.port_binding_handoffs_current SET handoff_state='DESTINATION_REALIZED',updated_at=statement_timestamp() WHERE port_id=$1 AND destination_binding_generation=$2 AND handoff_state='DESTINATION_RESERVED'`, v.PortID, v.BindingGeneration)
		var required, realized int
		var evidenceSet string
		if err := tx.QueryRow(ctx, `SELECT count(*),count(evidence.evidence_id),coalesce(string_agg(current.evidence_id||':'||evidence.observation_digest,',' ORDER BY port.port_id) FILTER(WHERE evidence.evidence_id IS NOT NULL),'') FROM kim.network_ports_current port JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.binding_state='RESERVED' JOIN kim.networks_current network ON network.network_id=port.network_id AND network.lifecycle_state='ACTIVE' JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.claim_state='ACTIVE' JOIN kim.host_network_mappings_current mapping ON mapping.host_id=binding.host_id AND mapping.segment_claim_id=segment.segment_claim_id AND mapping.mapping_state='CURRENT' LEFT JOIN kim.vm_network_port_realizations_current current ON current.vm_id=$1 AND current.port_id=port.port_id AND current.vm_generation=$2 AND current.port_generation=port.port_generation AND current.binding_generation=binding.binding_generation AND current.preboot_state='REALIZED' LEFT JOIN kim.vm_network_port_realization_evidence evidence ON evidence.evidence_id=current.evidence_id AND evidence.network_generation=network.network_generation AND evidence.segment_generation=segment.segment_generation AND evidence.host_mapping_generation=mapping.mapping_generation WHERE port.placement_admission_id=(SELECT placement_admission_id FROM kim.virtual_machines_current WHERE vm_id=$1) AND port.desired_state='RESERVED'`, v.VMID, v.VMGeneration).Scan(&required, &realized, &evidenceSet); err != nil || required == 0 || realized != required {
			return nil
		}
		setDigest := digestBytes([]byte(evidenceSet))
		powerPayload := []byte(`{"desired_state":"RUNNING"}`)
		powerDigest := digestBytes(powerPayload)
		tag, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current SET network_state='REALIZED',network_observation_generation=$2,network_evidence_set_digest=$3,boot_readiness='READY',blocking_reasons='{}',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$4 AND domain_state='DEFINED' AND storage_state='BOUND' AND image_state='REALIZED'`, v.VMID, v.ObservationGeneration, setDigest, v.VMGeneration)
		if err != nil || tag.RowsAffected() != 1 {
			return ErrVMMaterializationConflict
		}
		if v.DeferPowerAuthorization {
			var recoveryBound bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.recovery_materialization_evidence m JOIN kim.port_binding_handoffs_current h ON h.port_id=$2 AND h.destination_binding_generation=$3 AND h.handoff_state='DESTINATION_REALIZED' WHERE m.vm_id=$1 AND m.vm_generation=$4 AND m.destination_admission_id=(SELECT placement_admission_id FROM kim.network_ports_current WHERE port_id=$2))`, v.VMID, v.PortID, v.BindingGeneration, v.VMGeneration).Scan(&recoveryBound); err != nil || !recoveryBound {
				return ErrVMMaterializationConflict
			}
			return nil
		}
		if v.PowerJobID == "" || v.PowerCommandID == "" {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.virtual_machines_current SET desired_power_state='RUNNING',updated_at=statement_timestamp() WHERE vm_id=$1 AND vm_generation=$2`, v.VMID, v.VMGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VIRTUAL_MACHINE_POWER',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, v.PowerJobID, v.VMID, v.VMGeneration); err != nil {
			return err
		}
		var acceptedResource, acceptedType string
		var acceptedRevision int64
		if err := tx.QueryRow(ctx, `SELECT resource_id,resource_type,desired_revision FROM kim.execution_jobs WHERE job_id=$1`, v.PowerJobID).Scan(&acceptedResource, &acceptedType, &acceptedRevision); err != nil || acceptedResource != v.VMID || acceptedType != "VIRTUAL_MACHINE_POWER" || acceptedRevision != int64(v.VMGeneration) {
			return ErrVMMaterializationConflict
		}
		powerCommandTag, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(command_id) DO NOTHING`, v.PowerCommandID, v.PowerJobID, v.HostID, libvirtdomain.CommandType, libvirtdomain.SchemaVersion, "vm:"+v.VMID, powerPayload, powerDigest)
		if err != nil {
			return err
		}
		var acceptedJob, acceptedHost, acceptedCommandType, acceptedSchema, acceptedTarget, acceptedDigest string
		if err := tx.QueryRow(ctx, `SELECT job_id,host_id,command_type,schema_version,target_resource_id,payload_digest FROM kim.execution_commands WHERE command_id=$1`, v.PowerCommandID).Scan(&acceptedJob, &acceptedHost, &acceptedCommandType, &acceptedSchema, &acceptedTarget, &acceptedDigest); err != nil || acceptedJob != v.PowerJobID || acceptedHost != v.HostID || acceptedCommandType != libvirtdomain.CommandType || acceptedSchema != libvirtdomain.SchemaVersion || acceptedTarget != "vm:"+v.VMID || acceptedDigest != powerDigest {
			return ErrVMMaterializationConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.execution_commands_current(command_id,command_state) VALUES($1,'PENDING') ON CONFLICT(command_id) DO NOTHING`, v.PowerCommandID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.execution_jobs SET current_command_id=$2 WHERE job_id=$1`, v.PowerJobID, v.PowerCommandID); err != nil {
			return err
		}
		if powerCommandTag.RowsAffected() == 0 {
			return nil
		}
		return appendJobEventTx(ctx, tx, v.PowerJobID, "COMMAND_CREATED", map[string]any{"command_id": v.PowerCommandID, "payload_digest": powerDigest, "readiness_digest": setDigest})
	})
}

func requireCurrentHostPowerAuthorityTx(ctx context.Context, tx pgx.Tx, hostID string) error {
	var current bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM kim.host_operation_authorities_current authority
			JOIN kim.agent_transport_sessions_current session
			  ON session.host_id=authority.host_id
			 AND session.state='CURRENT'
			 AND session.session_generation=authority.session_generation
			JOIN kim.host_capability_projections capability
			  ON capability.host_id=authority.host_id
			 AND capability.projection_state='CURRENT'
			 AND capability.observation_generation=authority.capability_generation
			JOIN kim.host_readiness_gates_current gates
			  ON gates.host_id=authority.host_id
			 AND gates.gate_state='READY'
			 AND gates.capability_generation=authority.capability_generation
			 AND gates.baseline_assignment_generation=authority.baseline_assignment_generation
			 AND gates.preflight_generation=authority.preflight_generation
			 AND gates.compliance_generation=authority.compliance_generation
			WHERE authority.host_id=$1
			  AND authority.authority_state='ARMED'
		)
	`, hostID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrVMMaterializationConflict
	}
	return nil
}
