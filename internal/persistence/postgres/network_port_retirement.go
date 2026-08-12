package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

type OVNPortBindingRetirementRequest struct {
	OperationID         string
	OperationGeneration uint64
	IntentID            string
	IntentGeneration    uint64
	PortID              string
	PortGeneration      uint64
	BindingGeneration   uint64
	SourceHostID        string
}

type OVNPortBindingRetirementDecision struct {
	OperationID, IntentID, WorkID, PortID, SourceHostID, ObjectSetDigest string
	OperationGeneration, IntentGeneration, PortGeneration                uint64
	BindingGeneration                                                    uint64
}

func CommitOVNPortBindingRetirement(ctx context.Context, db TxBeginner, r OVNPortBindingRetirementRequest) (OVNPortBindingRetirementDecision, error) {
	var out OVNPortBindingRetirementDecision
	if r.OperationID == "" || r.OperationGeneration == 0 || r.IntentID == "" || r.IntentGeneration == 0 || r.PortID == "" || r.PortGeneration == 0 || r.BindingGeneration == 0 || r.SourceHostID == "" {
		return out, ErrPlacementConflict
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		// A committed operation remains recoverable after the logical Port has
		// advanced to a later Host-binding incarnation. This is response-loss
		// recovery only: the exact immutable identity must match and no new work
		// or authority is created.
		var existing OVNPortBindingRetirementDecision
		err := tx.QueryRow(ctx, `SELECT r.operation_id,r.operation_generation,r.intent_id,r.intent_generation,
			r.port_id,r.port_generation,r.binding_generation,r.source_host_id,intent.object_set_digest
			FROM kim.network_port_binding_retirements_current r
			JOIN kim.network_intent_revision_evidence intent
			  ON intent.intent_id=r.intent_id AND intent.intent_generation=r.intent_generation
			WHERE r.operation_id=$1`, r.OperationID).Scan(
			&existing.OperationID, &existing.OperationGeneration, &existing.IntentID, &existing.IntentGeneration,
			&existing.PortID, &existing.PortGeneration, &existing.BindingGeneration, &existing.SourceHostID,
			&existing.ObjectSetDigest)
		if err == nil {
			if existing.OperationGeneration != r.OperationGeneration || existing.IntentID != r.IntentID ||
				existing.IntentGeneration != r.IntentGeneration || existing.PortID != r.PortID ||
				existing.PortGeneration != r.PortGeneration || existing.BindingGeneration != r.BindingGeneration ||
				existing.SourceHostID != r.SourceHostID {
				return ErrPlacementConflict
			}
			existing.WorkID = fmt.Sprintf("ovn-runtime:%s:%d", existing.IntentID, existing.IntentGeneration)
			out = existing
			return nil
		}
		if err != pgx.ErrNoRows {
			return err
		}
		var incarnationExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_port_binding_retirements_current
			WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3)`,
			r.PortID, r.PortGeneration, r.BindingGeneration).Scan(&incarnationExists); err != nil {
			return err
		}
		if incarnationExists {
			return ErrPlacementConflict
		}
		var projectID, networkID, segmentID, sourceIntentID, sourceDigest string
		var networkGeneration, segmentGeneration, mappingGeneration, sourceIntentGeneration int64
		var sourcePlanRaw []byte
		if err := tx.QueryRow(ctx, `SELECT p.project_id,p.network_id,n.network_generation,b.segment_claim_id,s.segment_generation,m.mapping_generation,
			current.intent_id,current.intent_generation,intent.canonical_object_set,intent.object_set_digest
			FROM kim.network_ports_current p
			JOIN kim.networks_current n ON n.network_id=p.network_id AND n.lifecycle_state='ACTIVE'
			JOIN kim.port_bindings_current b ON b.port_id=p.port_id AND b.host_id=$4 AND b.binding_type='OVS' AND b.binding_state IN ('RESERVED','BINDING','VERIFYING','ACTIVE','UNKNOWN')
			JOIN kim.network_segment_claims_current s ON s.segment_claim_id=b.segment_claim_id AND s.claim_state='ACTIVE'
			JOIN kim.host_network_mappings_current m ON m.host_id=b.host_id AND m.segment_claim_id=b.segment_claim_id AND m.mapping_state='CURRENT'
			JOIN kim.network_ovn_state_current current ON current.port_id=p.port_id AND current.port_generation=p.port_generation AND current.binding_generation=b.binding_generation AND current.nb_state='MATCHED'
			JOIN kim.network_intent_revision_evidence intent ON intent.intent_id=current.intent_id AND intent.intent_generation=current.intent_generation AND intent.schema_version=$5
			WHERE p.port_id=$1 AND p.port_generation=$2 AND b.binding_generation=$3 FOR UPDATE OF p,b`,
			r.PortID, r.PortGeneration, r.BindingGeneration, r.SourceHostID, ovnadapter.PortIntentSchema).Scan(
			&projectID, &networkID, &networkGeneration, &segmentID, &segmentGeneration, &mappingGeneration,
			&sourceIntentID, &sourceIntentGeneration, &sourcePlanRaw, &sourceDigest); err != nil {
			return ErrPlacementStale
		}
		sourceCanonical, sourcePlan, err := ovnadapter.RestoreStoredPortPlan(sourcePlanRaw, sourceDigest)
		if err != nil || len(sourceCanonical) == 0 || sourcePlan.LogicalPort.HostID != r.SourceHostID {
			return ErrPlacementConflict
		}
		plan, digest, err := ovnadapter.PlanPortBindingRetirement(r.OperationID, r.OperationGeneration, sourcePlan, sourceDigest)
		if err != nil {
			return ErrPlacementConflict
		}
		tag, err := tx.Exec(ctx, `INSERT INTO kim.network_intent_revision_evidence(intent_id,intent_generation,aggregate_type,aggregate_id,project_id,network_id,network_generation,port_generation,segment_claim_id,segment_generation,host_mapping_generation,binding_generation,schema_version,canonical_object_set,object_set_digest,intent_state)
			VALUES($1,$2,'PORT',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'COMMITTED') ON CONFLICT(intent_id,intent_generation) DO NOTHING`,
			r.IntentID, r.IntentGeneration, r.PortID, projectID, networkID, networkGeneration, r.PortGeneration, segmentID, segmentGeneration, mappingGeneration, r.BindingGeneration, ovnadapter.PortBindingRetirementSchema, plan, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_intent_revision_evidence WHERE intent_id=$1 AND intent_generation=$2 AND aggregate_id=$3 AND schema_version=$4 AND object_set_digest=$5)`, r.IntentID, r.IntentGeneration, r.PortID, ovnadapter.PortBindingRetirementSchema, digest).Scan(&identical); err != nil || !identical {
				return ErrPlacementConflict
			}
		}
		workID := fmt.Sprintf("ovn-runtime:%s:%d", r.IntentID, r.IntentGeneration)
		workSchema := OVNRuntimeWorkSchemaV1
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT write_work_schema_version FROM kim.release_authority_current WHERE singleton=true),$1)`, OVNRuntimeWorkSchemaV1).Scan(&workSchema); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.ovn_runtime_work_current(work_id,intent_id,intent_generation,port_id,port_generation,binding_generation,object_set_digest,work_state,required_work_schema_version,operation_kind) VALUES($1,$2,$3,$4,$5,$6,$7,'PENDING',$8,'UNBIND') ON CONFLICT(intent_id,intent_generation) DO NOTHING`, workID, r.IntentID, r.IntentGeneration, r.PortID, r.PortGeneration, r.BindingGeneration, digest, workSchema); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.network_port_binding_retirements_current(port_id,operation_id,operation_generation,intent_id,intent_generation,port_generation,binding_generation,source_host_id,retirement_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'PENDING') ON CONFLICT(port_id,port_generation,binding_generation) DO NOTHING`, r.PortID, r.OperationID, r.OperationGeneration, r.IntentID, r.IntentGeneration, r.PortGeneration, r.BindingGeneration, r.SourceHostID); err != nil {
			return err
		}
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_port_binding_retirements_current WHERE port_id=$1 AND operation_id=$2 AND operation_generation=$3 AND intent_id=$4 AND intent_generation=$5 AND port_generation=$6 AND binding_generation=$7 AND source_host_id=$8)`, r.PortID, r.OperationID, r.OperationGeneration, r.IntentID, r.IntentGeneration, r.PortGeneration, r.BindingGeneration, r.SourceHostID).Scan(&identical); err != nil || !identical {
			return ErrPlacementConflict
		}
		tag, err = tx.Exec(ctx, `INSERT INTO kim.network_port_binding_retirement_latest_current(port_id,port_generation,binding_generation,operation_id,operation_generation,retirement_state) VALUES($1,$2,$3,$4,$5,'PENDING') ON CONFLICT(port_id) DO UPDATE SET port_generation=EXCLUDED.port_generation,binding_generation=EXCLUDED.binding_generation,operation_id=EXCLUDED.operation_id,operation_generation=EXCLUDED.operation_generation,retirement_state='PENDING',terminal_evidence_id=NULL,updated_at=statement_timestamp() WHERE (kim.network_port_binding_retirement_latest_current.port_generation,kim.network_port_binding_retirement_latest_current.binding_generation)<(EXCLUDED.port_generation,EXCLUDED.binding_generation)`, r.PortID, r.PortGeneration, r.BindingGeneration, r.OperationID, r.OperationGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrPlacementStale
		}
		out = OVNPortBindingRetirementDecision{OperationID: r.OperationID, OperationGeneration: r.OperationGeneration, IntentID: r.IntentID, IntentGeneration: r.IntentGeneration, WorkID: workID, PortID: r.PortID, PortGeneration: r.PortGeneration, BindingGeneration: r.BindingGeneration, SourceHostID: r.SourceHostID, ObjectSetDigest: digest}
		return nil
	})
	return out, err
}

type OVNPortBindingRetirementObservation struct {
	EvidenceID, IntentID, PortID, SourceHostID                            string
	NBObservationDigest, SBObservationDigest, OVSObservationDigest        string
	AdapterArtifactDigest                                                 string
	IntentGeneration, PortGeneration, BindingGeneration                   uint64
	OperationGeneration, NBObservationGeneration, SBObservationGeneration uint64
	OVSObservationGeneration                                              uint64
	ApplyResponseState                                                    string
	Observation                                                           ovnadapter.PortBindingRetirementObservation
}

func completeOVNPortBindingRetirementTx(ctx context.Context, tx pgx.Tx, claim OVNRuntimeClaim, observed OVNPortBindingRetirementObservation) error {
	if observed.EvidenceID == "" || observed.IntentID == "" || observed.PortID == "" || observed.SourceHostID == "" || observed.IntentGeneration == 0 || observed.PortGeneration == 0 || observed.BindingGeneration == 0 || observed.OperationGeneration == 0 || observed.NBObservationGeneration == 0 || observed.SBObservationGeneration == 0 || observed.OVSObservationGeneration == 0 || len(observed.NBObservationDigest) != 64 || len(observed.SBObservationDigest) != 64 || len(observed.OVSObservationDigest) != 64 || len(observed.AdapterArtifactDigest) != 64 {
		return ErrPlacementConflict
	}
	var operationID, intentID, portID, sourceHost string
	var operationGeneration, intentGeneration, portGeneration, bindingGeneration int64
	if err := tx.QueryRow(ctx, `SELECT r.operation_id,r.operation_generation,r.intent_id,r.intent_generation,r.port_id,r.port_generation,r.binding_generation,r.source_host_id FROM kim.network_port_binding_retirements_current r JOIN kim.ovn_runtime_work_current w ON w.intent_id=r.intent_id AND w.intent_generation=r.intent_generation AND w.work_id=$1 AND w.operation_kind='UNBIND' WHERE r.port_id=w.port_id AND r.port_generation=w.port_generation AND r.binding_generation=w.binding_generation FOR UPDATE OF r`, claim.WorkID).Scan(&operationID, &operationGeneration, &intentID, &intentGeneration, &portID, &portGeneration, &bindingGeneration, &sourceHost); err != nil {
		return ErrStaleOVNRuntimeClaim
	}
	if observed.IntentID != intentID || observed.IntentGeneration != uint64(intentGeneration) || observed.PortID != portID || observed.PortGeneration != uint64(portGeneration) || observed.BindingGeneration != uint64(bindingGeneration) || observed.SourceHostID != sourceHost || observed.OperationGeneration != uint64(operationGeneration) {
		return ErrStaleOVNRuntimeClaim
	}
	state := observed.Observation.State()
	// A lost mutation response remains ambiguous even when the same transport
	// turn managed to collect a matching read-back.  Persist UNKNOWN first so
	// only a successor claim in READ_BACK_FIRST mode may establish terminal
	// UNBOUND authority.
	if observed.ApplyResponseState == "LOST" {
		state = "UNKNOWN"
	}
	payload, _ := json.Marshal(observed)
	evidenceDigest := digestReleaseBytes(payload)
	if _, err := tx.Exec(ctx, `INSERT INTO kim.network_port_binding_retirement_evidence(evidence_id,operation_id,operation_generation,work_id,claim_generation,intent_id,intent_generation,port_id,port_generation,binding_generation,source_host_id,ownership_marker_matches,logical_port_preserved,requested_chassis_absent,source_chassis_inactive,source_ovs_interface_absent,apply_response_state,nb_observation_generation,nb_observation_digest,sb_observation_generation,sb_observation_digest,ovs_observation_generation,ovs_observation_digest,adapter_artifact_digest,retirement_state,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`, observed.EvidenceID, operationID, operationGeneration, claim.WorkID, claim.ClaimGeneration, intentID, intentGeneration, portID, portGeneration, bindingGeneration, sourceHost, observed.Observation.OwnershipMarkerMatches, observed.Observation.LogicalSwitchPortPresent, observed.Observation.RequestedChassisAbsent, observed.Observation.SourceChassisInactive, observed.Observation.SourceOVSInterfaceAbsent, observed.ApplyResponseState, observed.NBObservationGeneration, observed.NBObservationDigest, observed.SBObservationGeneration, observed.SBObservationDigest, observed.OVSObservationGeneration, observed.OVSObservationDigest, observed.AdapterArtifactDigest, state, evidenceDigest); err != nil {
		return err
	}
	workState, retirementState, event := "DISPATCH_UNKNOWN", "DISPATCH_UNKNOWN", "OBSERVATION_ACCEPTED"
	terminal := any(nil)
	if state == "VERIFIED" {
		workState, retirementState, terminal = "OBSERVED", "VERIFIED", observed.EvidenceID
	} else if state == "CONFLICTING" {
		workState, retirementState, event, terminal = "CONFLICTING", "CONFLICTING", "CONFLICT_QUARANTINED", observed.EvidenceID
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.ovn_runtime_work_current SET work_state=$4,claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,claim_maximum_expires_at=NULL,terminal_observation_id=$5,updated_at=statement_timestamp() WHERE work_id=$1 AND claim_owner=$2 AND claim_generation=$3`, claim.WorkID, claim.Owner, claim.ClaimGeneration, workState, terminal); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.network_port_binding_retirements_current SET retirement_state=$4,terminal_evidence_id=$5,updated_at=statement_timestamp() WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3`, portID, portGeneration, bindingGeneration, retirementState, terminal); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kim.network_port_binding_retirement_latest_current SET retirement_state=$4,terminal_evidence_id=$5,updated_at=statement_timestamp() WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3`, portID, portGeneration, bindingGeneration, retirementState, terminal); err != nil {
		return err
	}
	return appendOVNRuntimeEventTx(ctx, tx, claim, event, map[string]any{"operation_kind": "UNBIND", "retirement_state": state, "work_state": workState})
}
