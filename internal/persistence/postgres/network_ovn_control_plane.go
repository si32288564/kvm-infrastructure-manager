package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/network/ovnadapter"
)

type OVNControlPlaneObservation struct {
	LogicalFlowObservationID, ChassisEncapObservationID                             string
	IntentID, PortID, SBObservationID                                               string
	LogicalFlowObservationDigest, ChassisObservationDigest                          string
	ExpectedDatapathIdentityDigest, ObservedDatapathIdentityDigest                  string
	LogicalFlowSetDigest                                                            string
	ExpectedChassisIdentityDigest, ObservedChassisIdentityDigest                    string
	TunnelEndpointDigest, EncapOptionsDigest, EvaluatorArtifactDigest               string
	IntentGeneration, PortGeneration, BindingGeneration                             uint64
	SBObservationGeneration, LogicalFlowObservationGeneration                       uint64
	ChassisObservationGeneration                                                    uint64
	IngressFlowCount, EgressFlowCount                                               uint64
	LogicalDatapathPresent, ChassisRegistered, EncapPresent, TunnelEndpointObserved bool
	RequiredPortIdentityFlowsPresent                                                bool
	EncapType                                                                       string
}

func AcceptOVNControlPlaneObservation(ctx context.Context, db TxBeginner, observed OVNControlPlaneObservation) error {
	if observed.LogicalFlowObservationID == "" || observed.ChassisEncapObservationID == "" ||
		observed.IntentID == "" || observed.PortID == "" || observed.SBObservationID == "" ||
		observed.IntentGeneration == 0 || observed.PortGeneration == 0 || observed.BindingGeneration == 0 ||
		observed.SBObservationGeneration == 0 || observed.LogicalFlowObservationGeneration == 0 ||
		observed.ChassisObservationGeneration == 0 ||
		!allSHA256(observed.LogicalFlowObservationDigest, observed.ChassisObservationDigest,
			observed.ExpectedDatapathIdentityDigest, observed.ObservedDatapathIdentityDigest,
			observed.LogicalFlowSetDigest,
			observed.ExpectedChassisIdentityDigest, observed.ObservedChassisIdentityDigest,
			observed.TunnelEndpointDigest, observed.EncapOptionsDigest, observed.EvaluatorArtifactDigest) {
		return ErrPlacementConflict
	}
	encapType := strings.ToUpper(observed.EncapType)
	if encapType != "GENEVE" && encapType != "STT" && encapType != "VXLAN" && encapType != "UNKNOWN" {
		return ErrPlacementConflict
	}
	typed := ovnadapter.ControlPlaneObservation{
		LogicalDatapathPresent:           observed.LogicalDatapathPresent,
		ExpectedDatapathMatches:          observed.ExpectedDatapathIdentityDigest == observed.ObservedDatapathIdentityDigest,
		RequiredIngressFlowsPresent:      observed.IngressFlowCount > 0,
		RequiredEgressFlowsPresent:       observed.EgressFlowCount > 0,
		RequiredPortIdentityFlowsPresent: observed.RequiredPortIdentityFlowsPresent,
		ExpectedChassisMatches:           observed.ExpectedChassisIdentityDigest == observed.ObservedChassisIdentityDigest,
		ChassisRegistered:                observed.ChassisRegistered,
		EncapPresent:                     observed.EncapPresent,
		EncapTypeAllowed:                 encapType == "GENEVE",
		TunnelEndpointKnown:              observed.TunnelEndpointObserved,
	}
	flowState, chassisState := typed.LogicalFlowState(), typed.ChassisEncapState()
	status := "UNKNOWN"
	if flowState == "CONFLICTING" || chassisState == "CONFLICTING" {
		status = "CONFLICTING"
	} else if flowState == "MATCHED" && chassisState == "MATCHED" {
		status = "CONTROL_PLANE_CONVERGED"
	} else if flowState == "MATCHED" {
		status = "LOGICAL_FLOW_PROGRAMMED"
	} else if chassisState == "MATCHED" {
		status = "CHASSIS_ENCAP_READY"
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var current bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1
			FROM kim.network_ovn_state_current ovn
			JOIN kim.network_intent_revision_evidence intent
			 ON intent.intent_id=ovn.intent_id AND intent.intent_generation=ovn.intent_generation
			JOIN kim.network_ports_current port
			 ON port.port_id=ovn.port_id AND port.port_generation=ovn.port_generation
			JOIN kim.port_bindings_current binding
			 ON binding.port_id=port.port_id AND binding.binding_generation=ovn.binding_generation
			JOIN kim.ovn_sb_observation_evidence sb
			 ON sb.observation_id=ovn.sb_observation_id AND sb.intent_id=ovn.intent_id
			 AND sb.intent_generation=ovn.intent_generation AND sb.observation_generation=ovn.sb_observation_generation
			WHERE ovn.port_id=$1 AND ovn.port_generation=$2 AND ovn.binding_generation=$3
			 AND ovn.intent_id=$4 AND ovn.intent_generation=$5 AND ovn.sb_observation_id=$6
			 AND ovn.sb_observation_generation=$7 AND ovn.sb_state='MATCHED' AND ovn.layer_status='SB_REALIZED'
			 AND intent.aggregate_type='PORT' AND intent.aggregate_id=port.port_id
			 AND intent.port_generation=port.port_generation AND intent.binding_generation=binding.binding_generation
		)`, observed.PortID, observed.PortGeneration, observed.BindingGeneration, observed.IntentID,
			observed.IntentGeneration, observed.SBObservationID, observed.SBObservationGeneration).Scan(&current); err != nil || !current {
			return ErrPlacementConflict
		}
		flowCoverage := observed.IngressFlowCount > 0 && observed.EgressFlowCount > 0
		flowTag, err := tx.Exec(ctx, `INSERT INTO kim.ovn_logical_flow_observation_evidence(
			observation_id,intent_id,intent_generation,sb_observation_id,observation_generation,observation_digest,
			expected_datapath_identity_digest,observed_datapath_identity_digest,
			logical_flow_set_digest,ingress_flow_count,egress_flow_count,
			required_pipeline_coverage,required_port_identity_coverage,logical_flow_state,evaluator_artifact_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT(observation_id) DO NOTHING`, observed.LogicalFlowObservationID, observed.IntentID,
			observed.IntentGeneration, observed.SBObservationID, observed.LogicalFlowObservationGeneration,
			observed.LogicalFlowObservationDigest, observed.ExpectedDatapathIdentityDigest,
			observed.ObservedDatapathIdentityDigest, observed.LogicalFlowSetDigest,
			observed.IngressFlowCount, observed.EgressFlowCount, flowCoverage,
			observed.RequiredPortIdentityFlowsPresent, flowState, observed.EvaluatorArtifactDigest)
		if err != nil {
			return err
		}
		if flowTag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.ovn_logical_flow_observation_evidence
			 WHERE observation_id=$1 AND intent_id=$2 AND intent_generation=$3 AND sb_observation_id=$4
			 AND observation_generation=$5 AND observation_digest=$6 AND expected_datapath_identity_digest=$7
			 AND observed_datapath_identity_digest=$8 AND logical_flow_set_digest=$9
			 AND ingress_flow_count=$10 AND egress_flow_count=$11
			 AND required_pipeline_coverage=$12 AND required_port_identity_coverage=$13
			 AND logical_flow_state=$14 AND evaluator_artifact_digest=$15)`,
				observed.LogicalFlowObservationID, observed.IntentID, observed.IntentGeneration,
				observed.SBObservationID, observed.LogicalFlowObservationGeneration,
				observed.LogicalFlowObservationDigest, observed.ExpectedDatapathIdentityDigest,
				observed.ObservedDatapathIdentityDigest, observed.LogicalFlowSetDigest,
				observed.IngressFlowCount, observed.EgressFlowCount,
				flowCoverage, observed.RequiredPortIdentityFlowsPresent,
				flowState, observed.EvaluatorArtifactDigest).Scan(&identical); err != nil || !identical {
				return ErrPlacementConflict
			}
		}
		chassisTag, err := tx.Exec(ctx, `INSERT INTO kim.ovn_chassis_encap_observation_evidence(
			observation_id,intent_id,intent_generation,sb_observation_id,observation_generation,observation_digest,
			expected_chassis_identity_digest,observed_chassis_identity_digest,encap_type,tunnel_endpoint_digest,
			encap_options_digest,chassis_registered,encap_present,tunnel_endpoint_observed,chassis_encap_state,
			evaluator_artifact_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT(observation_id) DO NOTHING`, observed.ChassisEncapObservationID, observed.IntentID,
			observed.IntentGeneration, observed.SBObservationID, observed.ChassisObservationGeneration,
			observed.ChassisObservationDigest, observed.ExpectedChassisIdentityDigest,
			observed.ObservedChassisIdentityDigest, encapType, observed.TunnelEndpointDigest,
			observed.EncapOptionsDigest, observed.ChassisRegistered, observed.EncapPresent,
			observed.TunnelEndpointObserved, chassisState, observed.EvaluatorArtifactDigest)
		if err != nil {
			return err
		}
		if chassisTag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.ovn_chassis_encap_observation_evidence
			 WHERE observation_id=$1 AND intent_id=$2 AND intent_generation=$3 AND sb_observation_id=$4
			 AND observation_generation=$5 AND observation_digest=$6 AND expected_chassis_identity_digest=$7
			 AND observed_chassis_identity_digest=$8 AND encap_type=$9 AND tunnel_endpoint_digest=$10
			 AND encap_options_digest=$11 AND chassis_registered=$12 AND encap_present=$13
			 AND tunnel_endpoint_observed=$14 AND chassis_encap_state=$15 AND evaluator_artifact_digest=$16)`,
				observed.ChassisEncapObservationID, observed.IntentID, observed.IntentGeneration,
				observed.SBObservationID, observed.ChassisObservationGeneration,
				observed.ChassisObservationDigest, observed.ExpectedChassisIdentityDigest,
				observed.ObservedChassisIdentityDigest, encapType, observed.TunnelEndpointDigest,
				observed.EncapOptionsDigest, observed.ChassisRegistered, observed.EncapPresent,
				observed.TunnelEndpointObserved, chassisState, observed.EvaluatorArtifactDigest).Scan(&identical); err != nil || !identical {
				return ErrPlacementConflict
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO kim.network_ovn_control_plane_state_current(
			port_id,port_generation,binding_generation,intent_id,intent_generation,sb_observation_id,
			sb_observation_generation,logical_flow_observation_id,logical_flow_observation_generation,
			logical_flow_state,chassis_encap_observation_id,chassis_encap_observation_generation,
			chassis_encap_state,control_plane_status
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT(port_id) DO UPDATE SET
			port_generation=EXCLUDED.port_generation,binding_generation=EXCLUDED.binding_generation,
			intent_id=EXCLUDED.intent_id,intent_generation=EXCLUDED.intent_generation,
			sb_observation_id=EXCLUDED.sb_observation_id,sb_observation_generation=EXCLUDED.sb_observation_generation,
			logical_flow_observation_id=EXCLUDED.logical_flow_observation_id,
			logical_flow_observation_generation=EXCLUDED.logical_flow_observation_generation,
			logical_flow_state=EXCLUDED.logical_flow_state,
			chassis_encap_observation_id=EXCLUDED.chassis_encap_observation_id,
			chassis_encap_observation_generation=EXCLUDED.chassis_encap_observation_generation,
			chassis_encap_state=EXCLUDED.chassis_encap_state,
			control_plane_status=EXCLUDED.control_plane_status,updated_at=statement_timestamp()
		WHERE kim.network_ovn_control_plane_state_current.intent_generation<EXCLUDED.intent_generation
		 OR (kim.network_ovn_control_plane_state_current.intent_generation=EXCLUDED.intent_generation
		  AND kim.network_ovn_control_plane_state_current.logical_flow_observation_generation<EXCLUDED.logical_flow_observation_generation
		  AND kim.network_ovn_control_plane_state_current.chassis_encap_observation_generation<EXCLUDED.chassis_encap_observation_generation)`,
			observed.PortID, observed.PortGeneration, observed.BindingGeneration, observed.IntentID,
			observed.IntentGeneration, observed.SBObservationID, observed.SBObservationGeneration,
			observed.LogicalFlowObservationID, observed.LogicalFlowObservationGeneration, flowState,
			observed.ChassisEncapObservationID, observed.ChassisObservationGeneration, chassisState, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.network_ovn_control_plane_state_current
			 WHERE port_id=$1 AND port_generation=$2 AND binding_generation=$3 AND intent_id=$4
			 AND intent_generation=$5 AND sb_observation_id=$6 AND sb_observation_generation=$7
			 AND logical_flow_observation_id=$8 AND logical_flow_observation_generation=$9
			 AND logical_flow_state=$10 AND chassis_encap_observation_id=$11
			 AND chassis_encap_observation_generation=$12 AND chassis_encap_state=$13
			 AND control_plane_status=$14)`, observed.PortID, observed.PortGeneration,
				observed.BindingGeneration, observed.IntentID, observed.IntentGeneration,
				observed.SBObservationID, observed.SBObservationGeneration,
				observed.LogicalFlowObservationID, observed.LogicalFlowObservationGeneration, flowState,
				observed.ChassisEncapObservationID, observed.ChassisObservationGeneration, chassisState, status).Scan(&identical); err != nil || !identical {
				return ErrPlacementConflict
			}
		}
		return nil
	})
}

func allSHA256(values ...string) bool {
	for _, value := range values {
		if len(value) != 64 {
			return false
		}
		for _, char := range value {
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return false
			}
		}
	}
	return true
}
