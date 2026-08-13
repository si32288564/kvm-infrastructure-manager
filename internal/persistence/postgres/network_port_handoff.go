package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/locallvm"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/ovsnetwork"
)

type NetworkPortSourceQuiescenceRequest struct {
	FailureEpochID, PortID, JobID, CommandID string
}

type NetworkPortSourceQuiescenceDecision struct {
	FailureEpochID, PortID, SourceHostID, VMID, CommandID, PayloadDigest string
	PortGeneration, BindingGeneration, VMGeneration                      uint64
}

type NetworkPortSourceQuiescenceObservation struct {
	EvidenceID, FailureEpochID, PortID, SourceHostID, VMID                 string
	CommandID, VerificationID, ObservationDigest, VerifierDigest           string
	PortGeneration, BindingGeneration, VMGeneration, ObservationGeneration uint64
	AttemptIndex                                                           uint32
}

func PrepareNetworkPortSourceQuiescence(ctx context.Context, db TxBeginner, r NetworkPortSourceQuiescenceRequest) (NetworkPortSourceQuiescenceDecision, error) {
	var out NetworkPortSourceQuiescenceDecision
	if r.FailureEpochID == "" || r.PortID == "" || r.JobID == "" || r.CommandID == "" {
		return out, ErrPlacementConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var admissionID, workloadID, networkID, segmentID, mac, epochState string
		var networkGeneration, segmentGeneration, mappingGeneration, mtu uint64
		if err := tx.QueryRow(ctx, `SELECT e.admission_id,e.workload_id,e.source_host_id,plan.vm_id::text,plan.vm_generation,c.epoch_state,p.port_generation,p.network_id,n.network_generation,b.segment_claim_id,s.segment_generation,m.mapping_generation,b.binding_generation,n.mtu,mac.mac_address::text
			FROM kim.failure_epochs_current c JOIN kim.failure_epoch_evidence e ON e.failure_epoch_id=c.failure_epoch_id
			JOIN kim.vm_materialization_plan_evidence plan ON plan.placement_admission_id=e.admission_id AND plan.host_id=e.source_host_id
			JOIN kim.network_ports_current p ON p.placement_admission_id=e.admission_id AND p.port_id=$2
			JOIN kim.networks_current n ON n.network_id=p.network_id JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.host_id=e.source_host_id
			JOIN kim.network_segment_claims_current s ON s.segment_claim_id=b.segment_claim_id JOIN kim.host_network_mappings_current m ON m.host_id=e.source_host_id AND m.segment_claim_id=b.segment_claim_id
			JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE')
			WHERE c.failure_epoch_id=$1 AND c.epoch_state='FENCED' AND b.binding_type='OVS' FOR UPDATE OF p,b`, r.FailureEpochID, r.PortID).Scan(&admissionID, &workloadID, &out.SourceHostID, &out.VMID, &out.VMGeneration, &epochState, &out.PortGeneration, &networkID, &networkGeneration, &segmentID, &segmentGeneration, &mappingGeneration, &out.BindingGeneration, &mtu, &mac); err != nil {
			return ErrPlacementStale
		}
		payloadMap := map[string]any{"domain_uuid": out.VMID, "vm_generation": out.VMGeneration, "port_id": r.PortID, "port_generation": out.PortGeneration, "network_id": networkID, "network_generation": networkGeneration, "segment_claim_id": segmentID, "segment_generation": segmentGeneration, "host_mapping_generation": mappingGeneration, "binding_generation": out.BindingGeneration, "mac_address": mac, "mtu": mtu, "binding_type": "OVS", "desired_state": "QUIESCED"}
		payload, _ := json.Marshal(payloadMap)
		digest := digestBytes(payload)
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: r.JobID, CommandID: r.CommandID, HostID: out.SourceHostID, ResourceType: "NETWORK_PORT_SOURCE_QUIESCENCE", ResourceID: r.PortID, DesiredRevision: int64(out.BindingGeneration), CommandType: ovsnetwork.DataplaneCommandType, SchemaVersion: ovsnetwork.DataplaneSchemaVersion, TargetResourceID: "port:" + r.PortID, Payload: payloadMap}); err != nil {
			return err
		}
		out.FailureEpochID, out.PortID, out.CommandID, out.PayloadDigest = r.FailureEpochID, r.PortID, r.CommandID, digest
		return nil
	})
	return out, err
}

func AcceptNetworkPortSourceQuiescence(ctx context.Context, db TxBeginner, o NetworkPortSourceQuiescenceObservation) error {
	if o.EvidenceID == "" || o.FailureEpochID == "" || o.PortID == "" || o.SourceHostID == "" || o.VMID == "" || o.CommandID == "" || o.VerificationID == "" || o.PortGeneration == 0 || o.BindingGeneration == 0 || o.VMGeneration == 0 || o.ObservationGeneration == 0 || o.AttemptIndex == 0 || len(o.ObservationDigest) != 64 || len(o.VerifierDigest) != 64 {
		return ErrPlacementConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var accepted bool
		var retirementEvidenceID string
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.failure_epochs_current c JOIN kim.failure_epoch_evidence e ON e.failure_epoch_id=c.failure_epoch_id
			JOIN kim.vm_materialization_plan_evidence plan ON plan.placement_admission_id=e.admission_id AND plan.host_id=e.source_host_id
			JOIN kim.network_ports_current p ON p.placement_admission_id=e.admission_id AND p.port_id=$2 AND p.port_generation=$3
			JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.host_id=e.source_host_id AND b.binding_generation=$4
			JOIN kim.execution_commands cmd ON cmd.command_id=$5 AND cmd.host_id=e.source_host_id AND cmd.command_type=$6 AND cmd.schema_version=$7
			JOIN kim.command_verification_evidence v ON v.verification_id=$8 AND v.command_id=cmd.command_id AND v.attempt_index=$9 AND v.observation_generation=$10 AND v.observation_digest=$11 AND v.verifier_artifact_digest=$12 AND v.verification_state='MATCHED'
			WHERE c.failure_epoch_id=$1 AND c.epoch_state='FENCED' AND e.source_host_id=$13 AND plan.vm_id=$14::uuid AND plan.vm_generation=$15
			AND cmd.payload->>'desired_state'='QUIESCED' AND (v.evidence_payload->>'domain_running')::boolean=false AND (v.evidence_payload->>'interface_present')::boolean=false
			AND v.evidence_payload->>'port_id'=p.port_id AND (v.evidence_payload->>'port_generation')::bigint=p.port_generation AND (v.evidence_payload->>'binding_generation')::bigint=b.binding_generation
			AND EXISTS(SELECT 1 FROM kim.network_port_binding_retirements_current r JOIN kim.network_port_binding_retirement_evidence re ON re.evidence_id=r.terminal_evidence_id WHERE r.port_id=p.port_id AND r.port_generation=p.port_generation AND r.binding_generation=b.binding_generation AND r.source_host_id=e.source_host_id AND r.retirement_state='VERIFIED' AND re.retirement_state='VERIFIED'))`, o.FailureEpochID, o.PortID, o.PortGeneration, o.BindingGeneration, o.CommandID, ovsnetwork.DataplaneCommandType, ovsnetwork.DataplaneSchemaVersion, o.VerificationID, o.AttemptIndex, o.ObservationGeneration, o.ObservationDigest, o.VerifierDigest, o.SourceHostID, o.VMID, o.VMGeneration).Scan(&accepted); err != nil || !accepted {
			return ErrPlacementStale
		}
		if err := tx.QueryRow(ctx, `SELECT terminal_evidence_id FROM kim.network_port_binding_retirements_current WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3 AND source_host_id=$4 AND retirement_state='VERIFIED'`, o.PortID, o.PortGeneration, o.BindingGeneration, o.SourceHostID).Scan(&retirementEvidenceID); err != nil {
			return ErrPlacementStale
		}
		payload, _ := json.Marshal(o)
		evidenceDigest := digestReleaseBytes(payload)
		tag, err := tx.Exec(ctx, `INSERT INTO kim.network_port_source_quiescence_evidence(evidence_id,port_id,port_generation,source_host_id,source_binding_generation,vm_id,vm_generation,command_id,verification_id,observation_generation,observation_digest,source_vm_not_running,source_interface_absent,quiescence_state,evidence_digest,retirement_evidence_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,true,'QUIESCED',$12,$13) ON CONFLICT(evidence_id) DO NOTHING`, o.EvidenceID, o.PortID, o.PortGeneration, o.SourceHostID, o.BindingGeneration, o.VMID, o.VMGeneration, o.CommandID, o.VerificationID, o.ObservationGeneration, o.ObservationDigest, evidenceDigest, retirementEvidenceID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var existing string
			if err := tx.QueryRow(ctx, `SELECT evidence_digest FROM kim.network_port_source_quiescence_evidence WHERE evidence_id=$1`, o.EvidenceID).Scan(&existing); err != nil || existing != evidenceDigest {
				return ErrPlacementConflict
			}
		}
		return nil
	})
}

func isReadOnlyVerificationCommand(commandType, schemaVersion string) bool {
	return (commandType == SourceRootSafetyReadBackCommandType && schemaVersion == SourceRootSafetyReadBackSchema) ||
		(commandType == ovsnetwork.DataplaneCommandType && schemaVersion == ovsnetwork.DataplaneSchemaVersion) ||
		(commandType == locallvm.DeleteReadBackType && schemaVersion == locallvm.DeleteReadBackSchema)
}

func currentRecoveryNetworkEvidenceSetTx(ctx context.Context, tx pgx.Tx, operationID string) (uint64, string, bool, error) {
	var required, matched int
	var generation uint64
	var evidenceSet string
	if err := tx.QueryRow(ctx, `SELECT jsonb_array_length(a.network_requirements)
		FROM kim.recovery_materialization_evidence m
		JOIN kim.placement_admission_decisions a ON a.admission_id=m.destination_admission_id
		WHERE m.recovery_operation_id=$1`, operationID).Scan(&required); err != nil {
		return 0, "", false, err
	}
	if required == 0 {
		return 0, digestBytes([]byte("[]")), true, nil
	}
	err := tx.QueryRow(ctx, `SELECT count(*),coalesce(max(greatest(dp.observation_generation,ovn.nb_observation_generation,ovn.sb_observation_generation)),0),
		coalesce(string_agg(p.port_id||':'||p.port_generation||':'||b.binding_generation||':'||ip.ip_address::text||':'||mac.mac_address::text||':'||h.handoff_digest||':'||q.evidence_digest||':'||pre.evidence_id||':'||preobs.observation_digest||':'||ovn.intent_id||':'||ovn.intent_generation||':'||nb.observation_digest||':'||sb.observation_digest||':'||dp.evidence_id||':'||dpobs.observation_digest,',' ORDER BY p.port_id) FILTER (WHERE dp.evidence_id IS NOT NULL),'')
		FROM kim.recovery_materialization_evidence m
		JOIN kim.network_ports_current p ON p.placement_admission_id=m.destination_admission_id
		JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=m.destination_admission_id AND b.host_id=m.destination_host_id
		JOIN kim.port_binding_handoffs_current hc ON hc.port_id=p.port_id AND hc.destination_binding_generation=b.binding_generation AND hc.handoff_state IN ('DESTINATION_REALIZED','VERIFIED')
		JOIN kim.port_binding_handoff_evidence h ON h.handoff_id=hc.handoff_id AND h.destination_admission_id=m.destination_admission_id AND h.destination_host_id=m.destination_host_id
		JOIN kim.network_port_source_quiescence_evidence q ON q.evidence_id=h.source_quiescence_evidence_id AND q.evidence_digest=h.source_quiescence_evidence_digest AND q.quiescence_state='QUIESCED'
		JOIN kim.network_identity_claims ip ON ip.port_id=p.port_id AND ip.claim_type='IP' AND ip.claim_state IN ('RESERVED','ACTIVE')
		JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE')
		JOIN kim.vm_network_port_realizations_current pre ON pre.vm_id=m.vm_id AND pre.vm_generation=m.vm_generation AND pre.port_id=p.port_id AND pre.port_generation=p.port_generation AND pre.binding_generation=b.binding_generation AND pre.preboot_state='REALIZED'
		JOIN kim.vm_network_port_realization_evidence preobs ON preobs.evidence_id=pre.evidence_id
		JOIN kim.network_ovn_state_current ovn ON ovn.port_id=p.port_id AND ovn.port_generation=p.port_generation AND ovn.binding_generation=b.binding_generation AND ovn.nb_state='MATCHED' AND ovn.sb_state='MATCHED' AND ovn.layer_status='SB_REALIZED'
		JOIN kim.ovn_nb_observation_evidence nb ON nb.observation_id=ovn.nb_observation_id AND nb.nb_state='MATCHED'
		JOIN kim.ovn_sb_observation_evidence sb ON sb.observation_id=ovn.sb_observation_id AND sb.sb_state='MATCHED' AND sb.expected_chassis_matches
		LEFT JOIN kim.vm_port_dataplane_state_current dpc ON dpc.vm_id=m.vm_id AND dpc.vm_generation=m.vm_generation AND dpc.port_id=p.port_id AND dpc.port_generation=p.port_generation AND dpc.binding_generation=b.binding_generation AND dpc.convergence_state='CONVERGED'
		LEFT JOIN kim.vm_port_dataplane_observation_evidence dp ON dp.evidence_id=dpc.evidence_id AND dp.host_id=m.destination_host_id AND dp.convergence_state='CONVERGED'
		LEFT JOIN kim.vm_port_dataplane_observation_evidence dpobs ON dpobs.evidence_id=dp.evidence_id
		WHERE m.recovery_operation_id=$1`, operationID).Scan(&matched, &generation, &evidenceSet)
	if err != nil {
		return 0, "", false, err
	}
	return generation, digestBytes([]byte(evidenceSet)), matched == required && evidenceSet != "", nil
}

// RefreshRecoveryNetworkVerificationReadiness promotes only an exact set of
// current NB, SB, OVS, Port identity, source-quiescence and Handoff evidence.
// It does not complete Recovery; terminal authority remains separate.
func RefreshRecoveryNetworkVerificationReadiness(ctx context.Context, db TxBeginner, operationID string) (uint64, string, error) {
	var generation uint64
	var digest string
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var matched bool
		var err error
		generation, digest, matched, err = currentRecoveryNetworkEvidenceSetTx(ctx, tx, operationID)
		if err != nil || !matched || generation == 0 {
			return ErrRecoveryOperationBlocked
		}
		if tag, err := tx.Exec(ctx, `UPDATE kim.vm_materialization_readiness_current r SET network_state='REALIZED',network_observation_generation=$2,network_evidence_set_digest=$3,updated_at=statement_timestamp() FROM kim.recovery_materialization_evidence m WHERE m.recovery_operation_id=$1 AND r.vm_id=m.vm_id AND r.vm_generation=m.vm_generation AND r.plan_id=m.vm_plan_id AND r.boot_readiness='READY'`, operationID, generation, digest); err != nil || tag.RowsAffected() != 1 {
			return ErrRecoveryOperationStale
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.port_binding_handoffs_current hc SET handoff_state='VERIFIED',updated_at=statement_timestamp() FROM kim.network_ports_current p,kim.recovery_materialization_evidence m WHERE m.recovery_operation_id=$1 AND p.placement_admission_id=m.destination_admission_id AND hc.port_id=p.port_id AND hc.destination_binding_generation=(SELECT binding_generation FROM kim.port_bindings_current WHERE port_id=p.port_id) AND hc.handoff_state='DESTINATION_REALIZED'`, operationID); err != nil {
			return err
		}
		return nil
	})
	return generation, digest, err
}
