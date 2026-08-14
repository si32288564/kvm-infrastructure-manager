package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

type OVNPortIntentRequest struct {
	IntentID, PortID string
	IntentGeneration uint64
}

type OVNPortIntentDecision struct {
	IntentID, PortID, ObjectSetDigest string
	IntentGeneration, PortGeneration  uint64
	BindingGeneration                 uint64
	CanonicalObjectSet                []byte
}

func CommitOVNPortIntent(ctx context.Context, db TxBeginner, request OVNPortIntentRequest) (OVNPortIntentDecision, error) {
	if request.IntentID == "" || request.PortID == "" || request.IntentGeneration == 0 {
		return OVNPortIntentDecision{}, ErrPlacementConflict
	}
	var decision OVNPortIntentDecision
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var projectID, networkID, segmentID, hostID, ovnChassisName, mac, ip string
		var networkGeneration, portGeneration, segmentGeneration, mappingGeneration, bindingGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT port.project_id,port.network_id,network.network_generation,port.port_generation,
			 binding.segment_claim_id,segment.segment_generation,mapping.mapping_generation,binding.binding_generation,binding.host_id,mapping.ovn_chassis_name,
			 mac.mac_address::text,host(ip.ip_address)::text
			FROM kim.network_ports_current port
			JOIN kim.networks_current network ON network.network_id=port.network_id AND network.lifecycle_state='ACTIVE'
			 AND (network.authority_source='LEGACY_FOUNDATION' OR EXISTS(
			   SELECT 1 FROM kim.network_realizations_current realization
			   WHERE realization.network_id=network.network_id AND realization.network_revision=network.network_revision
			     AND realization.realization_state='VERIFIED' AND realization.terminal_evidence_id IS NOT NULL))
			JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id
			 AND binding.binding_type='OVS' AND binding.binding_state IN ('RESERVED','BINDING','VERIFYING','ACTIVE')
			JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.claim_state='ACTIVE'
			JOIN kim.host_network_mappings_current mapping ON mapping.host_id=binding.host_id
			 AND mapping.segment_claim_id=segment.segment_claim_id AND mapping.mapping_state='CURRENT' AND 'OVS'=ANY(mapping.supported_binding_types)
			JOIN kim.network_identity_claims mac ON mac.port_id=port.port_id AND mac.claim_type='MAC'
			 AND mac.claim_state IN ('RESERVED','ACTIVE')
			JOIN kim.network_identity_claims ip ON ip.port_id=port.port_id AND ip.claim_type='IP'
			 AND ip.claim_state IN ('RESERVED','ACTIVE')
			WHERE port.port_id=$1 AND port.desired_state IN ('RESERVED','BINDING','ACTIVE')
			FOR UPDATE OF port,binding
		`, request.PortID).Scan(&projectID, &networkID, &networkGeneration, &portGeneration, &segmentID, &segmentGeneration, &mappingGeneration, &bindingGeneration, &hostID, &ovnChassisName, &mac, &ip); err != nil {
			return ErrPlacementConflict
		}
		plan, digest, err := ovnadapter.PlanPort(ovnadapter.PortIntentInput{
			IntentID: request.IntentID, IntentGeneration: request.IntentGeneration,
			ProjectID: projectID, NetworkID: networkID, NetworkGeneration: uint64(networkGeneration),
			PortID: request.PortID, PortGeneration: uint64(portGeneration),
			SegmentClaimID: segmentID, SegmentGeneration: uint64(segmentGeneration),
			HostMappingGeneration: uint64(mappingGeneration), BindingGeneration: uint64(bindingGeneration),
			HostID: hostID, OVNChassisName: ovnChassisName, MACAddress: mac, IPAddress: ip,
		})
		if err != nil {
			return ErrPlacementConflict
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO kim.network_intent_revision_evidence(
			 intent_id,intent_generation,aggregate_type,aggregate_id,project_id,network_id,network_generation,
			 port_generation,segment_claim_id,segment_generation,host_mapping_generation,binding_generation,schema_version,
			 canonical_object_set,object_set_digest,intent_state
			) VALUES($1,$2,'PORT',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'COMMITTED')
			ON CONFLICT(intent_id,intent_generation) DO NOTHING
		`, request.IntentID, request.IntentGeneration, request.PortID, projectID, networkID, networkGeneration,
			portGeneration, segmentID, segmentGeneration, mappingGeneration, bindingGeneration, ovnadapter.PortIntentSchema, plan, digest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_intent_revision_evidence
			 WHERE intent_id=$1 AND intent_generation=$2 AND aggregate_type='PORT' AND aggregate_id=$3
			  AND project_id=$4 AND network_id=$5 AND network_generation=$6 AND port_generation=$7
			  AND segment_claim_id=$8 AND segment_generation=$9 AND host_mapping_generation=$10 AND binding_generation=$11
			  AND schema_version=$12 AND canonical_object_set=$13 AND object_set_digest=$14 AND intent_state='COMMITTED')`,
				request.IntentID, request.IntentGeneration, request.PortID, projectID, networkID, networkGeneration,
				portGeneration, segmentID, segmentGeneration, mappingGeneration, bindingGeneration, ovnadapter.PortIntentSchema, plan, digest).Scan(&identical); err != nil || !identical {
				return ErrPlacementConflict
			}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO kim.network_ovn_state_current(port_id,port_generation,binding_generation,intent_id,intent_generation,nb_state,sb_state,layer_status)
			VALUES($1,$2,$3,$4,$5,'PENDING','PENDING','INTENT_COMMITTED')
			ON CONFLICT(port_id) DO UPDATE SET port_generation=EXCLUDED.port_generation,
			 binding_generation=EXCLUDED.binding_generation,intent_id=EXCLUDED.intent_id,
			 intent_generation=EXCLUDED.intent_generation,nb_observation_id=NULL,nb_observation_generation=NULL,
			 nb_state='PENDING',sb_observation_id=NULL,sb_observation_generation=NULL,sb_state='PENDING',
			 layer_status='INTENT_COMMITTED',updated_at=statement_timestamp()
			WHERE kim.network_ovn_state_current.intent_generation<EXCLUDED.intent_generation
		`, request.PortID, portGeneration, bindingGeneration, request.IntentID, request.IntentGeneration)
		if err != nil {
			return err
		}
		// A new ordinary binding intent for the exact retired incarnation is an
		// authority-level ABA revival.  Fence the old positive UNBOUND proof
		// before the new backend mutation can be claimed.  Destination handoff
		// generations do not match and therefore do not invalidate history.
		if _, err := tx.Exec(ctx, `UPDATE kim.network_port_binding_retirements_current
			SET retirement_state='STALE',updated_at=statement_timestamp()
			WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3
			  AND retirement_state='VERIFIED'`, request.PortID, portGeneration, bindingGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.network_port_binding_retirement_latest_current
			SET retirement_state='STALE',updated_at=statement_timestamp()
			WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3
			  AND retirement_state='VERIFIED'`, request.PortID, portGeneration, bindingGeneration); err != nil {
			return err
		}
		workID := fmt.Sprintf("ovn-runtime:%s:%d", request.IntentID, request.IntentGeneration)
		workSchema := OVNRuntimeWorkSchemaV1
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT write_work_schema_version FROM kim.release_authority_current WHERE singleton=true),$1)`, OVNRuntimeWorkSchemaV1).Scan(&workSchema); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE kim.ovn_runtime_work_current
			SET work_state='SUPERSEDED',claim_owner=NULL,claim_generation=NULL,claim_expires_at=NULL,
			 claim_maximum_expires_at=NULL,updated_at=statement_timestamp()
			WHERE port_id=$1 AND intent_generation<$2
			  AND work_state IN ('PENDING','CLAIMED','DISPATCH_UNKNOWN')
		`, request.PortID, request.IntentGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kim.ovn_runtime_work_current(
			 work_id,intent_id,intent_generation,port_id,port_generation,binding_generation,object_set_digest,work_state,required_work_schema_version
			) VALUES($1,$2,$3,$4,$5,$6,$7,'PENDING',$8)
			ON CONFLICT(intent_id,intent_generation) DO NOTHING
		`, workID, request.IntentID, request.IntentGeneration, request.PortID, portGeneration, bindingGeneration, digest, workSchema); err != nil {
			return err
		}
		var identicalWork bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.ovn_runtime_work_current
			WHERE work_id=$1 AND intent_id=$2 AND intent_generation=$3 AND port_id=$4
			 AND port_generation=$5 AND binding_generation=$6 AND object_set_digest=$7 AND required_work_schema_version=$8
		)`, workID, request.IntentID, request.IntentGeneration, request.PortID, portGeneration, bindingGeneration, digest, workSchema).Scan(&identicalWork); err != nil || !identicalWork {
			return ErrPlacementConflict
		}
		decision = OVNPortIntentDecision{IntentID: request.IntentID, PortID: request.PortID, ObjectSetDigest: digest,
			IntentGeneration: request.IntentGeneration, PortGeneration: uint64(portGeneration), BindingGeneration: uint64(bindingGeneration), CanonicalObjectSet: plan}
		return nil
	})
	return decision, err
}

type OVNPortObservation struct {
	NBObservationID, SBObservationID, IntentID, PortID  string
	NBObservationDigest, SBObservationDigest            string
	AdapterArtifactDigest, ChassisIdentityDigest        string
	IntentGeneration, PortGeneration, BindingGeneration uint64
	NBObservationGeneration, SBObservationGeneration    uint64
	ApplyResponseState                                  string
	Observation                                         ovnadapter.Observation
}

func AcceptOVNPortObservation(ctx context.Context, db TxBeginner, observed OVNPortObservation) error {
	if err := validateOVNPortObservation(observed); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var state string
		var terminalObservationID *string
		if err := tx.QueryRow(ctx, `SELECT work_state,terminal_observation_id
			FROM kim.ovn_runtime_work_current
			WHERE intent_id=$1 AND intent_generation=$2 FOR UPDATE`, observed.IntentID, observed.IntentGeneration).Scan(&state, &terminalObservationID); err != nil || state != "OBSERVED" || terminalObservationID == nil || *terminalObservationID != observed.SBObservationID {
			return ErrPlacementConflict
		}
		return acceptOVNPortObservationTx(ctx, tx, observed)
	})
}

func validateOVNPortObservation(observed OVNPortObservation) error {
	if observed.NBObservationID == "" || observed.SBObservationID == "" || observed.IntentID == "" || observed.PortID == "" || observed.IntentGeneration == 0 || observed.PortGeneration == 0 || observed.BindingGeneration == 0 || observed.NBObservationGeneration == 0 || observed.SBObservationGeneration == 0 || len(observed.NBObservationDigest) != 64 || len(observed.SBObservationDigest) != 64 || len(observed.AdapterArtifactDigest) != 64 || len(observed.ChassisIdentityDigest) != 64 || (observed.ApplyResponseState != "RECEIVED" && observed.ApplyResponseState != "LOST" && observed.ApplyResponseState != "UNKNOWN") {
		return ErrPlacementConflict
	}
	return nil
}

func acceptOVNPortObservationTx(ctx context.Context, tx pgx.Tx, observed OVNPortObservation) error {
	nbState, sbState := observed.Observation.NBState(), observed.Observation.SBState()
	layerStatus := "UNKNOWN"
	if nbState == "CONFLICTING" || sbState == "CONFLICTING" {
		layerStatus = "CONFLICTING"
	} else if nbState == "MATCHED" && sbState == "MATCHED" {
		layerStatus = "SB_REALIZED"
	} else if nbState == "MATCHED" {
		layerStatus = "NB_APPLIED"
	}
	if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
		return err
	}
	var current bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1
			FROM kim.network_ovn_state_current current
			JOIN kim.network_intent_revision_evidence intent ON intent.intent_id=current.intent_id AND intent.intent_generation=current.intent_generation
			JOIN kim.network_ports_current port ON port.port_id=current.port_id AND port.port_generation=current.port_generation
			JOIN kim.networks_current network ON network.network_id=port.network_id AND network.network_generation=intent.network_generation AND network.lifecycle_state='ACTIVE'
			JOIN kim.port_bindings_current binding ON binding.port_id=port.port_id AND binding.binding_generation=current.binding_generation AND binding.binding_type='OVS'
			JOIN kim.network_segment_claims_current segment ON segment.segment_claim_id=binding.segment_claim_id AND segment.segment_generation=intent.segment_generation AND segment.claim_state='ACTIVE'
			JOIN kim.host_network_mappings_current mapping ON mapping.host_id=binding.host_id AND mapping.segment_claim_id=segment.segment_claim_id
			 AND mapping.mapping_generation=intent.host_mapping_generation AND mapping.mapping_state='CURRENT' AND 'OVS'=ANY(mapping.supported_binding_types)
			WHERE current.port_id=$1 AND current.port_generation=$2 AND current.binding_generation=$3
			 AND current.intent_id=$4 AND current.intent_generation=$5
			 AND intent.aggregate_type='PORT' AND intent.aggregate_id=port.port_id
			 AND intent.port_generation=port.port_generation AND intent.binding_generation=binding.binding_generation
			 AND intent.segment_claim_id=segment.segment_claim_id AND intent.intent_state='COMMITTED'
		)`, observed.PortID, observed.PortGeneration, observed.BindingGeneration, observed.IntentID, observed.IntentGeneration).Scan(&current); err != nil || !current {
		return ErrPlacementConflict
	}
	// The adapter supplies typed booleans only; raw OVN rows/columns are not accepted here.
	nbTag, err := tx.Exec(ctx, `INSERT INTO kim.ovn_nb_observation_evidence(
			observation_id,intent_id,intent_generation,observation_generation,observation_digest,apply_response_state,
			ownership_marker_matches,object_set_digest_matches,logical_switch_present,logical_switch_port_present,nb_state,adapter_artifact_digest
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(observation_id) DO NOTHING`,
		observed.NBObservationID, observed.IntentID, observed.IntentGeneration, observed.NBObservationGeneration,
		observed.NBObservationDigest, observed.ApplyResponseState, observed.Observation.OwnershipMarkerMatches,
		observed.Observation.ObjectSetDigestMatches, observed.Observation.LogicalSwitchPresent,
		observed.Observation.LogicalSwitchPortPresent, nbState, observed.AdapterArtifactDigest)
	if err != nil {
		return err
	}
	if nbTag.RowsAffected() == 0 {
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.ovn_nb_observation_evidence
			 WHERE observation_id=$1 AND intent_id=$2 AND intent_generation=$3 AND observation_generation=$4
			  AND observation_digest=$5 AND apply_response_state=$6 AND ownership_marker_matches=$7
			  AND object_set_digest_matches=$8 AND logical_switch_present=$9 AND logical_switch_port_present=$10
			  AND nb_state=$11 AND adapter_artifact_digest=$12)`, observed.NBObservationID, observed.IntentID,
			observed.IntentGeneration, observed.NBObservationGeneration, observed.NBObservationDigest,
			observed.ApplyResponseState, observed.Observation.OwnershipMarkerMatches,
			observed.Observation.ObjectSetDigestMatches, observed.Observation.LogicalSwitchPresent,
			observed.Observation.LogicalSwitchPortPresent, nbState, observed.AdapterArtifactDigest).Scan(&identical); err != nil || !identical {
			return ErrPlacementConflict
		}
	}
	sbTag, err := tx.Exec(ctx, `INSERT INTO kim.ovn_sb_observation_evidence(
			observation_id,intent_id,intent_generation,nb_observation_id,observation_generation,observation_digest,
			port_binding_present,datapath_present,expected_chassis_matches,chassis_identity_digest,sb_state,adapter_artifact_digest
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(observation_id) DO NOTHING`,
		observed.SBObservationID, observed.IntentID, observed.IntentGeneration, observed.NBObservationID,
		observed.SBObservationGeneration, observed.SBObservationDigest, observed.Observation.PortBindingPresent,
		observed.Observation.DatapathPresent, observed.Observation.ExpectedChassisMatches,
		observed.ChassisIdentityDigest, sbState, observed.AdapterArtifactDigest)
	if err != nil {
		return err
	}
	if sbTag.RowsAffected() == 0 {
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.ovn_sb_observation_evidence
			 WHERE observation_id=$1 AND intent_id=$2 AND intent_generation=$3 AND nb_observation_id=$4
			  AND observation_generation=$5 AND observation_digest=$6 AND port_binding_present=$7
			  AND datapath_present=$8 AND expected_chassis_matches=$9 AND chassis_identity_digest=$10
			  AND sb_state=$11 AND adapter_artifact_digest=$12)`, observed.SBObservationID, observed.IntentID,
			observed.IntentGeneration, observed.NBObservationID, observed.SBObservationGeneration,
			observed.SBObservationDigest, observed.Observation.PortBindingPresent,
			observed.Observation.DatapathPresent, observed.Observation.ExpectedChassisMatches,
			observed.ChassisIdentityDigest, sbState, observed.AdapterArtifactDigest).Scan(&identical); err != nil || !identical {
			return ErrPlacementConflict
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE kim.network_ovn_state_current SET
			nb_observation_id=$6,nb_observation_generation=$7,nb_state=$8,
			sb_observation_id=$9,sb_observation_generation=$10,sb_state=$11,layer_status=$12,updated_at=statement_timestamp()
			WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3 AND intent_id=$4 AND intent_generation=$5
			 AND (nb_observation_generation IS NULL OR nb_observation_generation<$7)
			 AND (sb_observation_generation IS NULL OR sb_observation_generation<$10)`, observed.PortID, observed.PortGeneration,
		observed.BindingGeneration, observed.IntentID, observed.IntentGeneration, observed.NBObservationID,
		observed.NBObservationGeneration, nbState, observed.SBObservationID, observed.SBObservationGeneration,
		sbState, layerStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_ovn_state_current
			 WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3 AND intent_id=$4
			  AND intent_generation=$5 AND nb_observation_id=$6 AND nb_observation_generation=$7
			  AND nb_state=$8 AND sb_observation_id=$9 AND sb_observation_generation=$10
			  AND sb_state=$11 AND layer_status=$12)`, observed.PortID, observed.PortGeneration,
			observed.BindingGeneration, observed.IntentID, observed.IntentGeneration, observed.NBObservationID,
			observed.NBObservationGeneration, nbState, observed.SBObservationID,
			observed.SBObservationGeneration, sbState, layerStatus).Scan(&identical); err != nil || !identical {
			return ErrPlacementConflict
		}
	}
	return nil
}
