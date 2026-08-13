package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/ovsnetwork"
)

type HostEvacuationNetworkRetirementRequest struct {
	AuthorityID, PortID, OperationID, IntentID string
	OperationGeneration, IntentGeneration      uint64
}

// AuthorizeHostEvacuationNetworkPortRetirement is a planned-EVACUATE
// consumer of the generic OVN retirement primitive. It proves the VM has
// reached planned SHUTOFF before committing the ordinary UNBIND work.
func AuthorizeHostEvacuationNetworkPortRetirement(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, r HostEvacuationNetworkRetirementRequest) (OVNPortBindingRetirementDecision, error) {
	var out OVNPortBindingRetirementDecision
	if r.AuthorityID == "" || r.PortID == "" || r.OperationID == "" || r.IntentID == "" || r.OperationGeneration == 0 || r.IntentGeneration == 0 {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var existingChild, existingPort, existingOperation, existingIntent, existingSourceHost string
		var existingOperationGeneration, existingIntentGeneration, existingPortGeneration, existingBindingGeneration uint64
		if err := tx.QueryRow(ctx, `SELECT child_operation_id,port_id,retirement_operation_id,retirement_operation_generation,retirement_intent_id,retirement_intent_generation,source_host_id,source_port_generation,source_binding_generation FROM kim.host_evacuation_source_network_retirement_authority_evidence WHERE retirement_authority_id=$1`, r.AuthorityID).Scan(&existingChild, &existingPort, &existingOperation, &existingOperationGeneration, &existingIntent, &existingIntentGeneration, &existingSourceHost, &existingPortGeneration, &existingBindingGeneration); err == nil {
			if existingChild != claim.ChildOperationID || existingPort != r.PortID || existingOperation != r.OperationID || existingOperationGeneration != r.OperationGeneration || existingIntent != r.IntentID || existingIntentGeneration != r.IntentGeneration {
				return ErrHostEvacuationConflict
			}
			var err error
			out, err = CommitOVNPortBindingRetirement(ctx, scopeTxBeginner{tx}, OVNPortBindingRetirementRequest{OperationID: existingOperation, OperationGeneration: existingOperationGeneration, IntentID: existingIntent, IntentGeneration: existingIntentGeneration, PortID: existingPort, PortGeneration: existingPortGeneration, BindingGeneration: existingBindingGeneration, SourceHostID: existingSourceHost})
			return err
		} else if err != pgx.ErrNoRows {
			return err
		}
		var childGeneration, vmGeneration, materializationGeneration, portGeneration, bindingGeneration uint64
		var vmID, sourceHost, sourceAdmission, sourcePlan, plannedQuiescenceID string
		if err := tx.QueryRow(ctx, `SELECT c.child_generation,e.vm_id::text,e.vm_generation,e.source_host_id,e.source_admission_id,e.source_plan_id,e.source_materialization_generation,
			q.quiescence_evidence_id,p.port_generation,b.binding_generation
			FROM kim.host_evacuation_workloads_current c
			JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.planned_source_quiescence_evidence q ON q.child_operation_id=c.child_operation_id AND q.child_generation=c.child_generation
			JOIN kim.planned_source_quiescence_execution_evidence qe ON qe.quiescence_evidence_id=q.quiescence_evidence_id
			JOIN kim.vm_power_state_current power ON power.vm_id=e.vm_id AND power.vm_generation=e.vm_generation AND power.observed_power_state='SHUTOFF' AND power.convergence_state='MATCHED'
			JOIN kim.vm_power_observation_evidence power_evidence ON power_evidence.evidence_id=power.evidence_id AND power_evidence.host_id=e.source_host_id AND power_evidence.observed_power_state='SHUTOFF'
			JOIN kim.network_ports_current p ON p.placement_admission_id=e.source_admission_id AND p.port_id=$2
			JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.placement_admission_id=e.source_admission_id AND b.host_id=e.source_host_id AND b.binding_type='OVS'
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' FOR UPDATE OF c,p,b,power`, claim.ChildOperationID, r.PortID).Scan(
			&childGeneration, &vmID, &vmGeneration, &sourceHost, &sourceAdmission, &sourcePlan, &materializationGeneration,
			&plannedQuiescenceID, &portGeneration, &bindingGeneration); err != nil {
			return ErrHostEvacuationBlocked
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s/%s/%s/%s/%d/%d/%s", claim.ChildOperationID, plannedQuiescenceID, r.PortID, sourceHost, portGeneration, bindingGeneration, r.OperationID)))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.host_evacuation_source_network_retirement_authority_evidence(retirement_authority_id,child_operation_id,child_generation,vm_id,vm_generation,source_host_id,source_admission_id,source_plan_id,source_materialization_generation,planned_quiescence_evidence_id,port_id,source_port_generation,source_binding_generation,retirement_operation_id,retirement_operation_generation,retirement_intent_id,retirement_intent_generation,authority_digest) VALUES($1,$2,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, r.AuthorityID, claim.ChildOperationID, childGeneration, vmID, vmGeneration, sourceHost, sourceAdmission, sourcePlan, materializationGeneration, plannedQuiescenceID, r.PortID, portGeneration, bindingGeneration, r.OperationID, r.OperationGeneration, r.IntentID, r.IntentGeneration, digest); err != nil {
			return err
		}
		var err error
		out, err = CommitOVNPortBindingRetirement(ctx, scopeTxBeginner{tx}, OVNPortBindingRetirementRequest{OperationID: r.OperationID, OperationGeneration: r.OperationGeneration, IntentID: r.IntentID, IntentGeneration: r.IntentGeneration, PortID: r.PortID, PortGeneration: portGeneration, BindingGeneration: bindingGeneration, SourceHostID: sourceHost})
		return err
	})
	return out, err
}

type HostEvacuationNetworkQuiescenceRequest struct {
	PortID, JobID, CommandID string
}

type HostEvacuationNetworkQuiescenceDecision struct {
	PortID, SourceHostID, VMID, CommandID, PayloadDigest string
	PortGeneration, BindingGeneration, VMGeneration      uint64
}

// PrepareHostEvacuationNetworkPortSourceQuiescence emits the ordinary typed
// OVS dataplane read-back only after generic retirement is VERIFIED.
func PrepareHostEvacuationNetworkPortSourceQuiescence(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, r HostEvacuationNetworkQuiescenceRequest) (HostEvacuationNetworkQuiescenceDecision, error) {
	var out HostEvacuationNetworkQuiescenceDecision
	if r.PortID == "" || r.JobID == "" || r.CommandID == "" {
		return out, ErrHostEvacuationConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var networkID, segmentID, mac string
		var networkGeneration, segmentGeneration, mappingGeneration, mtu uint64
		if err := tx.QueryRow(ctx, `SELECT e.vm_id::text,e.vm_generation,e.source_host_id,p.port_generation,n.network_id,n.network_generation,b.segment_claim_id,s.segment_generation,m.mapping_generation,b.binding_generation,n.mtu,mac.mac_address::text
			FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.host_evacuation_source_network_retirement_authority_evidence a ON a.child_operation_id=c.child_operation_id AND a.child_generation=c.child_generation AND a.port_id=$2
			JOIN kim.network_ports_current p ON p.port_id=a.port_id AND p.port_generation=a.source_port_generation
			JOIN kim.networks_current n ON n.network_id=p.network_id
			JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.host_id=e.source_host_id AND b.binding_generation=a.source_binding_generation
			JOIN kim.network_segment_claims_current s ON s.segment_claim_id=b.segment_claim_id
			JOIN kim.host_network_mappings_current m ON m.host_id=e.source_host_id AND m.segment_claim_id=b.segment_claim_id
			JOIN kim.network_identity_claims mac ON mac.port_id=p.port_id AND mac.claim_type='MAC' AND mac.claim_state IN ('RESERVED','ACTIVE')
			JOIN kim.network_port_binding_retirements_current rc ON rc.operation_id=a.retirement_operation_id AND rc.port_id=p.port_id AND rc.port_generation=p.port_generation AND rc.binding_generation=b.binding_generation AND rc.retirement_state='VERIFIED'
			JOIN kim.network_port_binding_retirement_evidence re ON re.evidence_id=rc.terminal_evidence_id AND re.retirement_state='VERIFIED'
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED'`, claim.ChildOperationID, r.PortID).Scan(&out.VMID, &out.VMGeneration, &out.SourceHostID, &out.PortGeneration, &networkID, &networkGeneration, &segmentID, &segmentGeneration, &mappingGeneration, &out.BindingGeneration, &mtu, &mac); err != nil {
			return ErrHostEvacuationBlocked
		}
		payload := map[string]any{"domain_uuid": out.VMID, "vm_generation": out.VMGeneration, "port_id": r.PortID, "port_generation": out.PortGeneration, "network_id": networkID, "network_generation": networkGeneration, "segment_claim_id": segmentID, "segment_generation": segmentGeneration, "host_mapping_generation": mappingGeneration, "binding_generation": out.BindingGeneration, "mac_address": mac, "mtu": mtu, "binding_type": "OVS", "desired_state": "QUIESCED"}
		encoded, _ := json.Marshal(payload)
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: r.JobID, CommandID: r.CommandID, HostID: out.SourceHostID, ResourceType: "NETWORK_PORT_SOURCE_QUIESCENCE", ResourceID: r.PortID, DesiredRevision: int64(out.BindingGeneration), CommandType: ovsnetwork.DataplaneCommandType, SchemaVersion: ovsnetwork.DataplaneSchemaVersion, TargetResourceID: "port:" + r.PortID, Payload: payload}); err != nil {
			return err
		}
		out.PortID, out.CommandID, out.PayloadDigest = r.PortID, r.CommandID, digestBytes(encoded)
		return nil
	})
	return out, err
}

type HostEvacuationNetworkQuiescenceObservation struct {
	EvidenceID, PortID, SourceHostID, VMID, CommandID string
	VerificationID, ObservationDigest, VerifierDigest string
	PortGeneration, BindingGeneration, VMGeneration   uint64
	ObservationGeneration                             uint64
	AttemptIndex                                      uint32
}

func AcceptHostEvacuationNetworkPortSourceQuiescence(ctx context.Context, db TxBeginner, claim HostEvacuationClaim, o HostEvacuationNetworkQuiescenceObservation) error {
	if o.EvidenceID == "" || o.PortID == "" || o.SourceHostID == "" || o.VMID == "" || o.CommandID == "" || o.VerificationID == "" || o.PortGeneration == 0 || o.BindingGeneration == 0 || o.VMGeneration == 0 || o.ObservationGeneration == 0 || o.AttemptIndex == 0 || len(o.ObservationDigest) != 64 || len(o.VerifierDigest) != 64 {
		return ErrHostEvacuationConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := validateEvacuationClaimTx(ctx, tx, claim); err != nil {
			return err
		}
		var retirementEvidenceID string
		var accepted bool
		if err := tx.QueryRow(ctx, `SELECT rc.terminal_evidence_id,EXISTS(SELECT 1
			FROM kim.host_evacuation_workloads_current c JOIN kim.host_evacuation_workload_evidence e USING(child_operation_id)
			JOIN kim.host_evacuation_source_network_retirement_authority_evidence a ON a.child_operation_id=c.child_operation_id AND a.child_generation=c.child_generation AND a.port_id=$2
			JOIN kim.network_port_binding_retirements_current rc2 ON rc2.operation_id=a.retirement_operation_id AND rc2.retirement_state='VERIFIED'
			JOIN kim.execution_commands cmd ON cmd.command_id=$6 AND cmd.host_id=e.source_host_id AND cmd.command_type=$12 AND cmd.schema_version=$13
			JOIN kim.command_verification_evidence v ON v.verification_id=$7 AND v.command_id=cmd.command_id AND v.attempt_index=$8 AND v.observation_generation=$9 AND v.observation_digest=$10 AND v.verifier_artifact_digest=$11 AND v.verification_state='MATCHED'
			WHERE c.child_operation_id=$1 AND c.phase='SOURCE_QUIESCED' AND e.vm_id=$5::uuid AND e.vm_generation=$4 AND e.source_host_id=$3
			AND a.source_port_generation=$14 AND a.source_binding_generation=$15
			AND cmd.payload->>'desired_state'='QUIESCED' AND v.evidence_payload->>'port_id'=$2
			AND (v.evidence_payload->>'port_generation')::bigint=$14 AND (v.evidence_payload->>'binding_generation')::bigint=$15
			AND (v.evidence_payload->>'domain_running')::boolean=false AND (v.evidence_payload->>'interface_present')::boolean=false)
			FROM kim.host_evacuation_source_network_retirement_authority_evidence a0
			JOIN kim.network_port_binding_retirements_current rc ON rc.operation_id=a0.retirement_operation_id AND rc.retirement_state='VERIFIED'
			WHERE a0.child_operation_id=$1 AND a0.port_id=$2`, claim.ChildOperationID, o.PortID, o.SourceHostID, o.VMGeneration, o.VMID, o.CommandID, o.VerificationID, o.AttemptIndex, o.ObservationGeneration, o.ObservationDigest, o.VerifierDigest, ovsnetwork.DataplaneCommandType, ovsnetwork.DataplaneSchemaVersion, o.PortGeneration, o.BindingGeneration).Scan(&retirementEvidenceID, &accepted); err != nil || !accepted {
			return ErrHostEvacuationBlocked
		}
		payload, _ := json.Marshal(o)
		evidenceDigest := digestReleaseBytes(payload)
		tag, err := tx.Exec(ctx, `INSERT INTO kim.network_port_source_quiescence_evidence(evidence_id,port_id,port_generation,source_host_id,source_binding_generation,vm_id,vm_generation,command_id,verification_id,observation_generation,observation_digest,source_vm_not_running,source_interface_absent,quiescence_state,evidence_digest,retirement_evidence_id) VALUES($1,$2,$3,$4,$5,$6::uuid,$7,$8,$9,$10,$11,true,true,'QUIESCED',$12,$13) ON CONFLICT(evidence_id) DO NOTHING`, o.EvidenceID, o.PortID, o.PortGeneration, o.SourceHostID, o.BindingGeneration, o.VMID, o.VMGeneration, o.CommandID, o.VerificationID, o.ObservationGeneration, o.ObservationDigest, evidenceDigest, retirementEvidenceID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var existingDigest string
			if err := tx.QueryRow(ctx, `SELECT evidence_digest FROM kim.network_port_source_quiescence_evidence WHERE evidence_id=$1`, o.EvidenceID).Scan(&existingDigest); err != nil || existingDigest != evidenceDigest {
				return ErrHostEvacuationConflict
			}
		}
		return nil
	})
}
