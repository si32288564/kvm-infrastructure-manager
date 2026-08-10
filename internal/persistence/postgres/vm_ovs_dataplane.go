package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/ovsnetwork"
)

type OVSDataplaneRequest struct{ VMID, PlanID, PortID, JobID, CommandID string }
type OVSDataplaneDecision struct{ HostID, CommandID, PayloadDigest string }

func PrepareOVSDataplaneObservation(ctx context.Context, db TxBeginner, request OVSDataplaneRequest) (OVSDataplaneDecision, error) {
	if !vmUUIDPattern.MatchString(request.VMID) || request.PlanID == "" || request.PortID == "" || request.JobID == "" || request.CommandID == "" {
		return OVSDataplaneDecision{}, ErrVMMaterializationConflict
	}
	var decision OVSDataplaneDecision
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
			JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id AND power.vm_generation=vm.vm_generation
			 AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED'
			JOIN kim.network_ports_current port ON port.placement_admission_id=vm.placement_admission_id
			 AND port.port_id=$3 AND port.desired_state='RESERVED'
			JOIN kim.networks_current network ON network.network_id=port.network_id AND network.lifecycle_state='ACTIVE'
			JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.host_id=vm.host_id
			 AND binding.binding_type='OVS' AND binding.binding_state='RESERVED'
			JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.claim_state='ACTIVE'
			JOIN kim.host_network_mappings_current mapping ON mapping.host_id=vm.host_id
			 AND mapping.segment_claim_id=segment.segment_claim_id AND mapping.mapping_state='CURRENT' AND 'OVS'=ANY(mapping.supported_binding_types)
			JOIN kim.network_identity_claims mac ON mac.port_id=port.port_id AND mac.claim_type='MAC' AND mac.claim_state='RESERVED'
			JOIN kim.vm_network_port_realizations_current preboot ON preboot.vm_id=vm.vm_id AND preboot.port_id=port.port_id
			 AND preboot.vm_generation=vm.vm_generation AND preboot.port_generation=port.port_generation
			 AND preboot.binding_generation=binding.binding_generation AND preboot.preboot_state='REALIZED'
			WHERE vm.vm_id=$1 AND vm.current_plan_id=$2 AND vm.lifecycle_state='DEFINED'
			FOR UPDATE OF vm,port,binding,mapping
		`, request.VMID, request.PlanID, request.PortID).Scan(&hostID, &vmGeneration, &networkID, &portGeneration, &networkGeneration, &segmentID, &segmentGeneration, &mappingGeneration, &bindingGeneration, &mtu, &mac); err != nil {
			return ErrVMMaterializationConflict
		}
		if err := lockHostAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		if err := requireCurrentHostPowerAuthorityTx(ctx, tx, hostID); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"domain_uuid": request.VMID, "vm_generation": vmGeneration,
			"port_id": request.PortID, "port_generation": portGeneration,
			"network_id": networkID, "network_generation": networkGeneration,
			"segment_claim_id": segmentID, "segment_generation": segmentGeneration,
			"host_mapping_generation": mappingGeneration, "binding_generation": bindingGeneration,
			"mac_address": mac, "mtu": mtu, "binding_type": "OVS", "desired_state": "CONVERGED",
		})
		if err != nil {
			return err
		}
		digest := digestBytes(payload)
		tag, err := tx.Exec(ctx, `INSERT INTO kim.execution_jobs(job_id,resource_type,resource_id,desired_revision,job_state) VALUES($1,'VM_PORT_DATAPLANE',$2,$3,'DISPATCHABLE') ON CONFLICT(job_id) DO NOTHING`, request.JobID, request.PortID, bindingGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var resourceType, resourceID string
			var revision int64
			if err := tx.QueryRow(ctx, `SELECT resource_type,resource_id,desired_revision FROM kim.execution_jobs WHERE job_id=$1`, request.JobID).Scan(&resourceType, &resourceID, &revision); err != nil || resourceType != "VM_PORT_DATAPLANE" || resourceID != request.PortID || revision != bindingGeneration {
				return ErrVMMaterializationConflict
			}
		}
		tag, err = tx.Exec(ctx, `INSERT INTO kim.execution_commands(command_id,job_id,host_id,command_type,schema_version,target_resource_id,payload,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(command_id) DO NOTHING`, request.CommandID, request.JobID, hostID, ovsnetwork.DataplaneCommandType, ovsnetwork.DataplaneSchemaVersion, "port:"+request.PortID, payload, digest)
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
			var acceptedJob, acceptedHost, acceptedType, acceptedSchema, acceptedTarget, acceptedDigest string
			if err := tx.QueryRow(ctx, `SELECT job_id,host_id,command_type,schema_version,target_resource_id,payload_digest FROM kim.execution_commands WHERE command_id=$1`, request.CommandID).Scan(&acceptedJob, &acceptedHost, &acceptedType, &acceptedSchema, &acceptedTarget, &acceptedDigest); err != nil || acceptedJob != request.JobID || acceptedHost != hostID || acceptedType != ovsnetwork.DataplaneCommandType || acceptedSchema != ovsnetwork.DataplaneSchemaVersion || acceptedTarget != "port:"+request.PortID || acceptedDigest != digest {
				return ErrVMMaterializationConflict
			}
		}
		decision = OVSDataplaneDecision{HostID: hostID, CommandID: request.CommandID, PayloadDigest: digest}
		return nil
	})
	return decision, err
}

type OVSDataplaneObservation struct {
	EvidenceID, VMID, PlanID, HostID, PortID, NetworkID, SegmentClaimID string
	CommandID, VerificationID, ObservationDigest, VerifierDigest        string
	VMGeneration, PortGeneration, NetworkGeneration, SegmentGeneration  uint64
	HostMappingGeneration, BindingGeneration, ObservationGeneration     uint64
	AttemptIndex                                                        uint32
}

func AcceptOVSDataplaneObservation(ctx context.Context, db TxBeginner, v OVSDataplaneObservation) error {
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
		var prebootEvidenceID, targetDevice, bridgeObserved, linkState string
		var accepted bool
		if err := tx.QueryRow(ctx, `
			SELECT preboot.evidence_id,
			 verification.evidence_payload->>'target_device',verification.evidence_payload->>'bridge_observed',verification.evidence_payload->>'link_state',
			 EXISTS(SELECT 1
			  FROM kim.virtual_machines_current vm
			  JOIN kim.vm_power_state_current power ON power.vm_id=vm.vm_id AND power.vm_generation=vm.vm_generation
			   AND power.observed_power_state='RUNNING' AND power.convergence_state='MATCHED'
			  JOIN kim.network_ports_current port ON port.placement_admission_id=vm.placement_admission_id AND port.port_id=$5
			  JOIN kim.networks_current network ON network.network_id=port.network_id
			  JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.host_id=vm.host_id
			  JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id
			  JOIN kim.host_network_mappings_current mapping ON mapping.host_id=vm.host_id AND mapping.segment_claim_id=segment.segment_claim_id
			  JOIN kim.network_identity_claims mac ON mac.port_id=port.port_id AND mac.claim_type='MAC' AND mac.claim_state='RESERVED'
			  JOIN kim.execution_commands command ON command.command_id=$12 AND command.command_type=$13 AND command.schema_version=$14 AND command.target_resource_id='port:'||port.port_id
			  WHERE vm.vm_id=$1 AND vm.vm_generation=$2 AND vm.current_plan_id=$3 AND vm.host_id=$4
			   AND port.port_generation=$6 AND port.desired_state='RESERVED'
			   AND network.network_id=$7 AND network.network_generation=$8 AND network.lifecycle_state='ACTIVE'
			   AND segment.segment_claim_id=$9 AND segment.segment_generation=$10 AND segment.claim_state='ACTIVE'
			   AND mapping.mapping_generation=$11 AND mapping.mapping_state='CURRENT' AND 'OVS'=ANY(mapping.supported_binding_types)
			   AND binding.binding_generation=$15 AND binding.binding_type='OVS' AND binding.binding_state='RESERVED'
			   AND command.payload->>'domain_uuid'=vm.vm_id::text
			   AND (command.payload->>'vm_generation')::bigint=vm.vm_generation
			   AND command.payload->>'port_id'=port.port_id
			   AND (command.payload->>'port_generation')::bigint=port.port_generation
			   AND command.payload->>'network_id'=network.network_id
			   AND (command.payload->>'network_generation')::bigint=network.network_generation
			   AND command.payload->>'segment_claim_id'=segment.segment_claim_id
			   AND (command.payload->>'segment_generation')::bigint=segment.segment_generation
			   AND (command.payload->>'host_mapping_generation')::bigint=mapping.mapping_generation
			   AND (command.payload->>'binding_generation')::bigint=binding.binding_generation
			   AND command.payload->>'binding_type'='OVS'
			   AND command.payload->>'mac_address'=mac.mac_address::text
			   AND command.payload->>'desired_state'='CONVERGED'
			   AND verification.evidence_payload->>'domain_uuid'=vm.vm_id::text
			   AND (verification.evidence_payload->>'vm_generation')::bigint=vm.vm_generation
			   AND verification.evidence_payload->>'port_id'=port.port_id
			   AND (verification.evidence_payload->>'port_generation')::bigint=port.port_generation
			   AND verification.evidence_payload->>'network_id'=network.network_id
			   AND (verification.evidence_payload->>'network_generation')::bigint=network.network_generation
			   AND verification.evidence_payload->>'segment_claim_id'=segment.segment_claim_id
			   AND (verification.evidence_payload->>'segment_generation')::bigint=segment.segment_generation
			   AND (verification.evidence_payload->>'host_mapping_generation')::bigint=mapping.mapping_generation
			   AND (verification.evidence_payload->>'binding_generation')::bigint=binding.binding_generation
			   AND verification.evidence_payload->>'binding_type'='OVS'
			   AND verification.evidence_payload->>'mac_address'=mac.mac_address::text
			   AND (verification.evidence_payload->>'domain_running')::boolean
			   AND (verification.evidence_payload->>'interface_present')::boolean
			   AND (verification.evidence_payload->>'bridge_matches')::boolean
			   AND coalesce(verification.evidence_payload->>'target_device','')<>''
			   AND verification.evidence_payload->>'link_state'='up')
			FROM kim.vm_network_port_realizations_current preboot
			JOIN kim.command_verification_evidence verification ON verification.verification_id=$16
			 AND verification.command_id=$12 AND verification.attempt_index=$17
			 AND verification.observation_generation=$18 AND verification.observation_digest=$19
			 AND verification.verification_state='MATCHED' AND verification.verifier_artifact_digest=$20
			WHERE preboot.vm_id=$1 AND preboot.port_id=$5 AND preboot.vm_generation=$2
			 AND preboot.port_generation=$6 AND preboot.binding_generation=$15 AND preboot.preboot_state='REALIZED'
		`, v.VMID, v.VMGeneration, v.PlanID, v.HostID, v.PortID, v.PortGeneration, v.NetworkID, v.NetworkGeneration, v.SegmentClaimID, v.SegmentGeneration, v.HostMappingGeneration, v.CommandID, ovsnetwork.DataplaneCommandType, ovsnetwork.DataplaneSchemaVersion, v.BindingGeneration, v.VerificationID, v.AttemptIndex, v.ObservationGeneration, v.ObservationDigest, v.VerifierDigest).Scan(&prebootEvidenceID, &targetDevice, &bridgeObserved, &linkState, &accepted); err != nil || !accepted {
			return ErrVMMaterializationConflict
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.vm_port_dataplane_observation_evidence(
			 evidence_id,vm_id,vm_generation,plan_id,host_id,port_id,port_generation,network_id,network_generation,
			 segment_claim_id,segment_generation,host_mapping_generation,binding_generation,preboot_evidence_id,
			 command_id,attempt_index,verification_id,observation_generation,observation_digest,verifier_digest,
			 target_device,bridge_observed,link_state,convergence_state
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,'CONVERGED')
			ON CONFLICT(evidence_id) DO NOTHING
		`, v.EvidenceID, v.VMID, v.VMGeneration, v.PlanID, v.HostID, v.PortID, v.PortGeneration, v.NetworkID, v.NetworkGeneration, v.SegmentClaimID, v.SegmentGeneration, v.HostMappingGeneration, v.BindingGeneration, prebootEvidenceID, v.CommandID, v.AttemptIndex, v.VerificationID, v.ObservationGeneration, v.ObservationDigest, v.VerifierDigest, targetDevice, bridgeObserved, linkState)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.vm_port_dataplane_observation_evidence
			 WHERE evidence_id=$1 AND vm_id=$2 AND vm_generation=$3 AND plan_id=$4 AND host_id=$5
			  AND port_id=$6 AND port_generation=$7 AND network_id=$8 AND network_generation=$9
			  AND segment_claim_id=$10 AND segment_generation=$11 AND host_mapping_generation=$12
			  AND binding_generation=$13 AND preboot_evidence_id=$14 AND command_id=$15
			  AND attempt_index=$16 AND verification_id=$17 AND observation_generation=$18
			  AND observation_digest=$19 AND verifier_digest=$20 AND target_device=$21
			  AND bridge_observed=$22 AND link_state=$23 AND convergence_state='CONVERGED')`,
				v.EvidenceID, v.VMID, v.VMGeneration, v.PlanID, v.HostID, v.PortID, v.PortGeneration,
				v.NetworkID, v.NetworkGeneration, v.SegmentClaimID, v.SegmentGeneration,
				v.HostMappingGeneration, v.BindingGeneration, prebootEvidenceID, v.CommandID,
				v.AttemptIndex, v.VerificationID, v.ObservationGeneration, v.ObservationDigest,
				v.VerifierDigest, targetDevice, bridgeObserved, linkState).Scan(&identical); err != nil || !identical {
				return ErrVMMaterializationConflict
			}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.vm_port_dataplane_state_current(vm_id,vm_generation,port_id,port_generation,binding_generation,observation_generation,evidence_id,convergence_state)
			VALUES($1,$2,$3,$4,$5,$6,$7,'CONVERGED')
			ON CONFLICT(vm_id,port_id) DO UPDATE SET
			 vm_generation=EXCLUDED.vm_generation,port_generation=EXCLUDED.port_generation,
			 binding_generation=EXCLUDED.binding_generation,observation_generation=EXCLUDED.observation_generation,
			 evidence_id=EXCLUDED.evidence_id,convergence_state='CONVERGED',updated_at=statement_timestamp()
			WHERE kim.vm_port_dataplane_state_current.vm_generation<EXCLUDED.vm_generation
			 OR (kim.vm_port_dataplane_state_current.vm_generation=EXCLUDED.vm_generation
			     AND kim.vm_port_dataplane_state_current.observation_generation<EXCLUDED.observation_generation)
		`, v.VMID, v.VMGeneration, v.PortID, v.PortGeneration, v.BindingGeneration, v.ObservationGeneration, v.EvidenceID)
		return err
	})
}
